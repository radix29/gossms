# Review plan, 2026-11-26

A review of gossms, `~/go/gosmo` and `~/go/tuikit` after the 2026-11-25
bug-fix pass closed, covering what landed since the 2026-11-23 review: Use
Database and the active-database marker in Object Explorer (`2874a6f`,
`51cd9db`), the Object Explorer platform-feature table (`cc66edf`),
schema-qualified completion (`fa9d965`), the CLI parameter and safety fixes,
gosmo's decimal/money/datetimeoffset conversions, and tuikit's tree emphasis,
splitter collapse, viewport inside-bars and block-caret fix. Nothing is fixed
yet. Close an item by deleting it; delete this file with the last one. gosmo
and tuikit may break back-compat where an item says so (none needs to).

**Method.** `go build`, `go vet`, `staticcheck` (default gate and
`-checks all` without ST1000/3/20-22), `deadcode -test`, `govulncheck`,
`go list -m -u`, the `nilness` and `unusedwrite` analyzers and
`go test -race -count=1 ./...` in all three repos; tuikit's three monkey
tests at 40x their seed counts (editor 12 000, data grid 8 000, input field
30 000); every Go fuzz target in the three repos for 90 s each (in a copy, so
no corpus lands in the trees); a ranking of every non-test function and file
by length; a read of every commit since 2026-11-23 in each repo; greps for the
rules `CLAUDE.md`, `docs/ui-rules.md` and `docs/db-rules.md` state (mutation
in `Draw`, case-blind name comparisons, `context.Background()`, unquoted SQL
text, row loops without `rows.Err()`); and a probe of `safety.Classify` with
~170 hand-picked statements (DBCC forms, implicit EXEC, CTE-led DML,
WAITFOR RECEIVE, Service Broker verbs, cursors). No server was contacted and
the binary was not driven: every fix still needs the `docs/testing.md`
drive.

**What came out clean.** Build, vet, both staticcheck gates, govulncheck,
nilness, unusedwrite, every test with `-race` in all three repos, all
monkey seeds, and the fuzz targets run so far (see the fuzz note at the end). No direct
dependency has a newer version. The classifier probe found nothing to fix
(DBCC SQLPERF … CLEAR is already admin, LINENO floors to an exec — fail-safe).
`deadcode` lists gosmo's `QSStatistics` and 28 tuikit exports with no in-repo
caller: library surface, kept deliberately (`CLAUDE.md`). No gossms or gosmo
file is over 900 non-test lines; tuikit's `controls/treeview.go` is (A3). The
five property-page builders over 200 lines are already an open thread. The
gosmo conversions (`fitFraction`, `numberText`'s tie rounding, the money range
in uint64, the datetimeoffset UTC and offset checks) read correct at every
edge tried, as does tuikit's `Viewport.insideBars` fixed point.

Severity is ordered by what a user can lose, not by effort.

## Recommended implementation order

- **W4 — I1**: move the Object Explorer active-database check out of `Draw`
  into `handleEvent`'s post-step, and give the "is this the active database"
  test one home; document `query_panel_database.go` in the same change.
- **W5 — I2** (tuikit): `Button`, `CheckBox` and `RadioBox` claim the press
  latch on every `Button1`, as `Toolbar` and `Splitter` do (an open thread:
  one line each). Do **I3** (the Windows-only tuikit test) in the same tuikit
  pass.
- **W6 — A1 + A2**: `safety.NameParts` gives way to `gosmo.SplitName`;
  `tableCandidates`' four loops fold into two helpers.
- **W7 — A3** (tuikit): split `controls/treeview.go` (902 lines) at its mouse
  section.
- **W8 — N1**: nits (gosmo `scanCapabilityRows` nested-map helper,
  `query.Execute`'s doc comment).
- **W9**: move what stays undone to `docs/open-threads.md` (and close the
  two open threads W5 resolves), then delete this file.

Each step: build in the library first when one is involved (`go test ./...`
there), then `gofmt -w . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...`, then drive the built binary (`docs/testing.md`). Nobody
commits but the user.

---

## Inconsistencies

### I1 — Object Explorer rebuilds its tree inside `Draw`

**Where:** `internal/tui/object_explorer.go`, `ObjectExplorer.Draw` (the
`contextConn`/`contextDB` comparison and `oe.rebuild()`), and `row`;
`internal/tui/query_panel_database.go`, `useDatabaseMenuItem`'s `Checked`.

**What:** `ObjectExplorer.Draw` is the only `Draw` in `internal/tui` that
changes state: it compares the active query panel's connection and database
with the last frame's and rebuilds the tree on a difference.
`docs/ui-rules.md` § Mouse says state is set in the handler, never in
`Draw` — `App.Run` skips the frame after coalesced motion, so whatever
`Draw` computes lags what the next event is hit-tested against. Here the
rebuild changes only labels' styles, so nothing visible breaks today, but the
pattern is the one the rule exists to stop. The predicate "this node is the
active panel's database" is also written twice (`row`'s `current`, and
`Checked`).

**Fix:** a `func (oe *ObjectExplorer) syncActiveDatabase()` holding today's
comparison, called from `App.handleEvent`'s post-step beside
`a.abandonStaleFormat()` (it runs after every event and every drained post,
which is every way the active panel or its database changes: tab switch,
connect, close, `setResult`, `changeDatabase`). `Draw` goes back to one line.
One helper, `activeDatabase(p *QueryPanel, sc *db.ServerConn, name string)
bool`, serves `row` and `Checked`. Give `query_panel_database.go`'s four
functions and the two new `ObjectExplorer` fields doc comments to match the
surrounding density (`CLAUDE.md` § Coding conventions) — `contextConn` in
particular, whose pointer comparison against `row`'s `IdentityKey`
comparison needs its one line of why.

**Tests:** the existing `object_explorer_test.go` marker tests keep passing
once they call `handleEvent` (or `syncActiveDatabase`) instead of relying on
a draw; add one asserting `Draw` alone no longer rebuilds.

### I2 — tuikit: three widgets claim the press latch only on a hit

Already in `docs/open-threads.md` § tuikit press latches, with no repro. Do it
now because it is three one-line moves and it makes the gesture rule
uniform: `Toolbar` and `layout.Splitter` already claim on every `Button1`.

**Where:** `~/go/tuikit/widgets/button.go`, `checkbox.go`, `radiobox.go`,
each `HandleMouse`.

**Fix:** call `press.Fresh(ev)` once, right after the `Buttons() != Button1`
check and before the bounds test, keeping its result; act on it inside the
hit. Mind the `disabled` early return: put `Fresh` before it too, so a press
on a disabled widget still claims the gesture (a widget enabled mid-drag must
not fire on the release's motion). Tests: press outside, drag onto the
widget, assert no `OnClick`/toggle/pick — one per widget, beside the existing
press tests. Delete the open-thread entry.

### I3 — tuikit: `TestLocalFileSystemExistsReportsPermissionError` on Windows

Already in `docs/open-threads.md` § tuikit tests on Windows. `os.Chmod(dir,
0)` does not deny traversal on Windows. Skip that fixture when
`runtime.GOOS == "windows"` (a test file, so the no-GOOS-branch rule for
production code is not touched), with the reason in the skip message. Run
`scripts/wintest.sh` against the tuikit tree to confirm. Delete the
open-thread entry.

---

## Simplifications and refactoring

### A1 — `safety.NameParts` duplicates `gosmo.SplitName`

**Where:** `internal/cli/safety/writes.go`, `NameParts`; its one caller,
`internal/cli/cmd/query_writes.go` (`targetCheck.object`, the synonym case).

**What:** `NameParts` lexes a synonym's `base_object_name` to split it into
parts. gosmo exports exactly that (`SplitName`: brackets, double quotes,
omitted middle parts as "", at most four parts), and the two agree on every
form the server writes there (it always brackets each part). `NameParts` is
the only exported function in `safety` with no caller inside the package.

**Fix:** call `gosmo.SplitName` (an error becomes today's "cannot be read"
reason) and delete `NameParts`. `parseObjectName` in `cli/cmd` stays: its
rules differ on purpose (no omitted part, at most three, its own usage
wording).

### A2 — completion: four copies of two loops in `tableCandidates`

**Where:** `internal/tui/completion_candidates.go`, `tableCandidates`.

**What:** the schema loop is written twice (user and system inventory), and
since `fa9d965` the qualify-and-append loop twice (objects, functions).
**Fix:** `schemaItems(schemas []string, pl string)` and
`qualifiedObjectItems(objs, pl)`; behaviour unchanged, pinned by the existing
completion tests (`softenExactMatches`/`qualifiesTyped` included).

### A3 — tuikit: `controls/treeview.go` is 902 lines

Over the ~900-line prompt (`~/go/tuikit/CLAUDE.md`: one file per type or
tight group). The mouse half — `HandleMouse` through `scrollRight` — is one
contiguous block since `5ff6596` split it by zone. **Fix:** extract that line
range into `controls/treeview_mouse.go` by exact range, diff byte-for-byte
against the original, then delete it from `treeview.go`; add the file to
tuikit's README/ARCHITECTURE file lists if they enumerate `controls/`.

---

## Nits

### N1

- **gosmo `capabilities_scan.go`, `scanCapabilityRows`:** the "S:", "G:"
  and "K:" cases each repeat nil-map check, inner-map make, assign. A
  `setState(m map[string]map[string]CapabilityState, key, perm string, st
  CapabilityState)` beside `recordSecurableState` (which is the
  denial-wins variant) removes nine lines and keeps the two policies named.
- **gossms `internal/query/executor.go`, `Execute`'s doc:** "The connection
  is discarded rather than pooled when the script leaves its database or the
  run fails or is cancelled" — a run whose batches fail but end in the
  starting database is pooled (the pool's reset clears its SET options and
  transaction). Say "when the script leaves its database, its database
  cannot be read, or the run is cancelled".

---

## Fuzz note

Every fuzz target runs 90 s in a scratch copy. Clean so far:
`FuzzSQLExpressionCompletion`, `FuzzRecordReader`, `FuzzParseFilter`,
`FuzzScope`, `FuzzClassify`. The remaining 13 were still running when this
plan was written; a failure among them is added here as a B item.
