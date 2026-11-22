# Review plan, 2026-11-22 (third pass)

A review of gossms, `~/go/gosmo` and `~/go/tuikit` for bugs, inconsistencies,
optimizations, simplifications and architecture, after the second 2026-11-22
plan shipped. Nothing is fixed yet. Close an item by deleting it; delete this
file with the last one. Back-compat in gosmo and tuikit may break where an
item says so (CHANGELOG entry required in the library).

**Method.** `go vet`, `staticcheck` (default and `-checks all` minus
ST1000/3/20-22), `deadcode -test`, `dupl -t 70/120`, a go/ast pass ranking
every function by length and branch count, `go test ./...` in all three,
`go test -race` (tuikit, gosmo, gossms `internal/...`), 45 s runs of nine
fuzz targets (see § Fuzz results), and throwaway probes of the new
`--max-rows-affected` write classifier (17 adversarial scripts: `SET NOCOUNT
ON` without `;` inside `BEGIN`, `WAITFOR` prefixes, aliases in brackets and
with hints, CTE prefixes, `OUTPUT … INTO`, cursor `FOR UPDATE`; all
classified correctly, probe deleted). The commits since the last plan were
read in full: gossms `abe5a4c` (picture tone), `f6fc753`/`f861e5b` (CLI
write safety), gosmo `023b474` (index_stats split). The binary was not
driven: every item's fix still needs the `docs/testing.md` drive.

**What came out clean.** All tests and race runs pass. vet, staticcheck
(both gates) are clean in all three. `deadcode` lists only exported tuikit
and gosmo library surface (kept deliberately, CLAUDE.md) — nothing in
gossms. `dupl` finds only structural look-alikes (dashboard panels, DMV
scanners, palette tables), none worth a helper (the `dupl -t 90` verdict in
`docs/decisions.md` § Code and dependency hygiene still holds). No file is
GOOS-suffixed outside tuikit's `clipboard/native_windows.go` and
`runtime.GOOS` branches only where CLAUDE.md says. Every direct dependency
is at its latest version. No SQL is built by unquoted interpolation in the
code read. The settled items in `docs/decisions.md` (no `internal/tui`
split, gosmo one package, hand-rolled argument parser, …) are not re-raised.

Severity is ordered by what a user can lose, not by effort. This pass found
no data-loss or wrong-result bug; the items are small correctness gaps,
maintainability, and test coverage.

## Recommended implementation order

- **W4 — S1**: one dotted-name reader in `internal/cli/safety`, then split
  `classify.go` (968 lines).
- **W5 — O1**: completion's per-keystroke `strings.ToUpper` over the whole
  statement.
- **W6 — A2**: tuikit `PanelManager.HandleMouse` split by zone (pure
  refactor).
- **W7 — N1**: nits.
- **W8**: delete this file.

Each step: build in the library first when one is involved (`go test ./...`
there), then `gofmt -w . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...`, then drive the built binary (`docs/testing.md`). Nobody
commits but the user.

---

## Architecture, simplification, refactoring

### S1 — three dotted-name readers in `internal/cli/safety`; `classify.go` over the split prompt

**Where:** `internal/cli/safety/classify.go` (`objectName`, `nameParts`) and
`writes.go` (`nameAt`); `classify.go` is 968 non-test lines.

**What:** three walkers over the same `word (. word)*` shape: `objectName`
returns the raw text, `nameParts` lower-cases and refuses `@vars`, `nameAt`
keeps case and reports `ok`. They differ in edge rules (`nameParts` stops at
`@x`, `nameAt` accepts it; only `nameAt` reports a trailing dot) — the kind
of divergence the safety classifier should not carry, since the write check
and the EXEC check can disagree about where one name ends.

**Plan:** one `readName(toks, i) (n dottedName, next int, ok bool)` with
`parts` (unquoted, case kept) and `raw`; `nameParts` becomes a lower-casing
wrapper that also refuses a leading `@`, `objectName` returns `n.raw`. Then
split `classify.go` along its sections by exact line range (tokens and name
reading; procedure/EXEC rules `execProc`…`literalText`), diff byte-for-byte,
delete the source ranges (CLAUDE.md § Coding conventions). `FuzzClassify`,
`classify_test.go` and `writes_test.go` are the oracle.

### A2 — tuikit `PanelManager.HandleMouse` is one 153-line router

**Where:** `~/go/tuikit/layout/panel_manager.go`, `HandleMouse` (the most
branched function in tuikit, ~50 branches).

**What:** release handling, the foreign-drag pass-through, the drop-down
scrollbar, wheel, toggle arrow, tab row and list hit-tests are one sequence
whose early returns encode priority. Correct today and well commented, but
each new zone has to be threaded into the middle.

**Plan:** pure refactor into `mouseRelease`, `mouseForeign`, `mouseCombo`
(scrollbar + wheel + list), `mouseTabRow` and the final panel delegation,
called in the same order; keep the comments with the branches they explain.
tuikit's `layout` tests and gossms's tab tests are the oracle; drive tab
drag-select across the tab row afterwards (the `stranded` path).
`TreeView.HandleMouse` (147 lines) is the next candidate by the same
measure; leave it unless it is being changed anyway.

---

## Optimizations

### O1 — completion upper-cases every statement token on every keystroke

**Where:** `internal/tui/completion_provider.go`, the
`slices.ContainsFunc(tokens, … sqltext.EndsDML(strings.ToUpper(tok.Text)) …)`
check near the top of `sqlCompletionCandidates`.

**What:** `strings.ToUpper` allocates for any token with a lower-case letter,
so lower-case SQL pays one allocation per token of the statement before the
cursor on every keystroke, before anything is known to need it.

**Plan:** give `sqltext` (or `sqlparse.Token`) a case-insensitive
`EndsDMLFold` that compares with `strings.EqualFold` against its fixed word
list, or reuse the lexer's upper-cased word where the token carries one.
Benchmark with the existing completion benchmarks (if none covers a long
statement, add one typing at the end of a 2000-token SELECT).

---

## Nits

### N1 — comment drift

- gosmo error strings: a handful of internal errors are not `gosmo:`-prefixed
  at creation (`script.go`'s `script: …`, `catalog.go`/`scripter_table_prefetch.go`
  "result set %d of %d is missing"). Each is wrapped by a prefixed caller
  today; check that every exported path still wraps them when touching
  those files, rather than editing them now.

---

## Fuzz results

FILLED-IN-BELOW
