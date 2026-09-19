# N2 — incremental completion prefix scan

Plan of record for closing **N2** in `docs/open-threads.md`: `sqlparse.ScanPrefix`
lexes the whole prefix on every keystroke while the completion popup is open,
which costs 0.57 ms on a 100-statement script and 5.7 ms on 1000, on the UI
goroutine, and grows linearly from there.

Delete this file once the work lands — the convention — and leave the pointers
behind in `internal/tui/sqlparse/prefix_cache.go` and `ARCHITECTURE.md`.

## Status

| Step | State |
|---|---|
| 1 — benchmark baseline | Not started |
| 2 — plumb text identity through `CompletionProvider` | Not started |
| 3 — `lexSQL` reports every boundary | Not started |
| 4 — `sqlparse.PrefixCache`, unwired | Not started |
| 5 — differential test | Not started |
| 6 — wire into `QueryPanel` | Not started |
| 7 — benchmark, live-test, close out | Not started |

## Effort

**Roughly one focused day — 5 to 7 hours — and about 500 lines net.** The lexer
change is small. Steps 5 and 7 are the bulk of it, and they are the point: N2's
own argument for leaving this alone is that a stale cache is a *silently wrong*
completion rather than a slow one, so the differential test and the live pass
are what make the change safe to ship, not overhead on top of it.

## What actually changes

Pass 1 of `ScanPrefix` (`internal/tui/sqlparse/token.go`, `ScanPrefix`) lexes
`[0, upTo)` with tokens off, purely to locate two boundaries: the offset after
the last top-level `;`, and the line after the last bare `GO` above the cursor's
row. Pass 2 then tokenizes from the later of the two, and is already
O(statement).

The fix caches those boundaries and restarts pass 1 from the last one *before*
the edit, instead of from offset 0.

Three facts make that sound:

- **Every boundary is a `LexNormal` position by construction.** The lexer
  recognises a top-level `;` and a separator line only in normal state, and a
  separator line carries nothing that could still be open past it. This is
  already the stated precondition on `TokenizeRangeFrom`, which takes an
  explicit start state for exactly this reason.
- **A boundary before the edit keeps its offset.** Only text at or after the
  edit point moves, so boundaries strictly above it survive an edit that changes
  the line count. Boundaries at or after it are discarded.
- **Restarting mid-buffer cannot miss a `GO` line.** A separator must start a
  line; the line containing the restart offset began before it and was already
  scanned, and every line start after it is still walked.

`prefixStates` (`internal/tuikit/controls/common.go`) is the existing, tested
precedent for the invalidation shape — keyed on the `*Document` as well as its
version, resuming at `Document.dirtyFrom`, replaying from scratch on anything it
cannot justify. This follows it deliberately rather than inventing a second
scheme.

### The one blocker

The provider signature is `func(lines [][]rune, row, col int)`. It receives no
document identity and no dirty hint, so today it **cannot** know where the edit
was, or even whether the text changed at all since the previous call. That has
to be plumbed before any cache is possible; it is step 2, and it is a
behaviour-preserving change of its own.

## Decisions taken up front

| Decision | Choice | Why |
|---|---|---|
| Where the cache lives | `internal/tui/sqlparse/prefix_cache.go`, a new file | One file per type. `token.go` is 584 lines and this would push it toward the split threshold for no reason. |
| `ScanPrefix`'s fate | Stays, unchanged, as the pure reference implementation | It is the oracle step 5 tests the cache against. Removing it would leave the cache with nothing to be checked against. |
| Invalidation rule | `*Document` identity + `version+1` + `dirtyFrom`, else full rescan | `prefixStates`' rule, proven in the highlighters. Typing is a single `setLine`, so it hits; a keystroke that first deletes a selection bumps twice and costs one full scan. |
| `GO` lines in the cache | Recorded cursor-independently, filtered by `< rowStart` at read time | Makes the cache survive plain cursor movement, so scrolling and arrow keys cost nothing in pass 1 at all — a win N2 does not mention. |
| A kill switch | None | Nothing else in the editor has one, and the fallback path (full rescan whenever the cache cannot justify a resume) is the kill switch. |
| `BatchEndOffset` | Out of scope | Its forward scan is O(script) too, but it is gated behind a name already in play, and N2 does not cover it. |

## Steps

Each step leaves the tree building, green and shippable on its own.

### 1. Measure first (tests only)

Run `BenchmarkCompletionPrefixScan_*` in
`internal/tui/sqlparse/bench_test.go` and record the numbers on this machine —
N2's 0.57 ms / 5.7 ms are the i5-2500K figures and are the acceptance baseline.

Then add two benchmarks that model real typing rather than a cold scan, because
the existing pair measures the first keystroke, which is the one case the cache
cannot help:

- an edit on the cursor's own line near the end of a 1000-statement script —
  the case the cache must turn into O(statement);
- an edit on line 1 — the worst case, which must not come out *slower* than
  today.

### 2. Plumb text identity through `CompletionProvider`

In `internal/tuikit/controls/editor_completion.go`:

```go
type TextRevision struct { Doc any; Version uint64; DirtyFrom int }

type CompletionRequest struct {
    Lines    [][]rune
    Row, Col int
    Text     TextRevision
}

type CompletionProvider func(CompletionRequest) (items []CompletionItem, replaceFrom int)
```

Both call sites (`updateCompletion`, `triggerCompletionExplicit`) fill it from
`e.doc`. `Doc` is `any` on purpose: `*Document` is unexported surface, and the
provider only ever compares it for identity.

Churn is small — one real call site in `internal/tui/completion_provider.go`,
the `testCompletionProvider` helper, and two inline closures in
`editor_completion_test.go`. The contract comment saying "a provider needs no
state of its own between calls" stops being true and has to change with it.

*Cheaper alternative, if the contract should not move:* an `Editor.TextRevision()`
accessor that the `QueryPanel` closure calls, since it already holds `p.editor`.
No test churn at all, but it hides the dependency at the call site. Prefer the
struct.

### 3. Teach `lexSQL` to report every boundary it crosses

`lexResult` carries only the *last* `;` boundary plus `firstGo`/`lastGo`. The
cache needs all of them. Add an optional sink — a `func(off int)` called once
per boundary, which is once per statement and so costs nothing measurable.

Behaviour-preserving, and the existing `ScanPrefix`/`ScopeAt` tests cover this
pass heavily.

### 4. Add `sqlparse.PrefixCache`, not yet wired

`prefix_cache.go` holds the document identity, the version it was built at, and
a sorted slice of boundaries (offset plus whether it was a `GO`).
`ScanPrefixCached` resumes from the last boundary below the edit offset and
falls back to a full scan whenever it cannot justify a resume: a different
document, a version that is not exactly one ahead, or a line count it cannot
reconcile.

New code, called by nothing yet.

### 5. Differential test — the step that justifies the change

A randomized edit-sequence test. Build a script carrying block comments, string
literals, bracket identifiers, `GO` lines and `;`, then apply thousands of
random edits — insert, delete, line split and join, opening and closing a block
comment, paste, and the same-length replace every undo produces — asserting
after *every single one* that `ScanPrefixCached` equals `ScanPrefix` field for
field.

Then drive the existing `testdata/completion_prefix_scan.golden` corpus through
the cached path as well, so both share one set of expectations.

This is where the hours go.

### 6. Wire it in

One field on `QueryPanel` beside `completionBuf`, one changed line in
`sqlCompletionCandidates`.

### 7. Benchmark, live-test, close out

Re-run step 1's benchmarks and compare. Then drive the real binary under tmux
per `docs/testing.md` — green `go test` is not verification here, and never has
been on this project. The pass that matters: type into a large script with the
popup open, toggle a block comment *above* the cursor, paste a block, undo and
redo it, insert a `GO` above the cursor, and move the cursor between batches
without typing.

Close out by deleting N2 from `docs/open-threads.md`, updating `ScanPrefix`'s
"two passes" comment to point at the cache, and fixing the `sqlparse` row in
`ARCHITECTURE.md` § Package map, which calls the package "Pure functions over
runes; no App, no connection" — the first half stops being true.

## The fallback, if step 5 turns up something that will not pin down

Keep the previous flattened buffer and restart the scan from the first differing
rune. A rune compare is 20 to 50 times cheaper than the state machine, it needs
none of step 2's plumbing, and it is still O(script). Worth knowing about. Not
worth choosing first.
