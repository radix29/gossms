# Review plan, 2026-11-22

A review of gossms, `~/go/gosmo` and `~/go/tuikit` for bugs, inconsistencies,
optimizations, simplifications and architecture. Nothing is fixed yet. Close
an item by deleting it; delete this file with the last one. Back-compat in
gosmo and tuikit may break where an item says so (CHANGELOG entry required).

**Method.** `go vet`, `staticcheck -checks all` (style checks ST1000/3/20-22
off), `deadcode -test`, the `nilness`/`unusedwrite`/`shadow` analyzers,
`go test ./...` in all three and `go test -race` (tuikit, gosmo, gossms
`internal/...`), plus unit probes of the CLI parser and the safety classifier
(each repro below was run as a throwaway test, then deleted). The binary was
not driven: every item's fix still needs the `docs/testing.md` drive.

**What came out clean.** All tests and race runs pass; vet, nilness and
unusedwrite report nothing; staticcheck's default gate is clean in all three
(two ST1016 nits under `-checks all`, D2); the shadow hits are deliberate.
`deadcode` in tuikit and gosmo lists only exported library surface (kept
deliberately, CLAUDE.md). No SQL is built by unquoted interpolation: every
`Sprintf` into T-SQL takes an allowlisted word or goes through
`QuoteName`/`escapeSingle`. All five items of the 2026-11-21 review shipped
(D1).

Severity is ordered by what a user can lose, not by effort.

## Recommended implementation order

- **W6 — A1**: split the two over-long completion files and break up
  `keywordCandidates` (pure refactor).
- **W7 — T1**: offline coverage for the gosmo scripters only live tests reach.
- **W8 — T2**: chase the flaky `TestSessionPropertiesApplyReloadsTheSessionsTargets`.
- **W9 — O1 + O2**: tuikit DataGrid re-sort and TreeModel re-delivery costs.
- **W10 — D1 + D2**: delete the finished 2026-11-21 plan; receiver-name nits.

The rest cost no correctness.

Each step: build in the library first when one is involved (`go test ./...`
there), then `gofmt -w . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...`, then drive the built binary (`docs/testing.md`). Nobody
commits but the user.

---

## Architecture, simplification, refactoring

### A1 — split the PR 50 completion files and their long functions

**Where:** `internal/tui/completion_keywords.go` (1097 non-test lines) and
`completion_expressions.go` (1043), both over the ~900-line prompt in
CLAUDE.md § Coding conventions; inside them `keywordCandidates` (~400
lines), `analyzeCompletionContext` (~230) and `ddlRoutineChoices` (~200).

**What:** the code works and is well tested (≈2000 test lines), but each
function is one long `if/switch` ladder keyed on the clause and the last
tokens, with version flags threaded through as booleans, and a local
variable named `context` that shadows the package. It is the part of the
tree hardest to change safely.

**Plan:** extract by exact line range along the existing
sections (DDL choices, FOR/JSON/XML clause, predicate scanning, builtin
table), diff byte-for-byte, then refactor `keywordCandidates` into a
`[]clauseRule{match func(ctx) bool; choices func(ctx) []string; complete
bool}` table evaluated in order, so each rule is a few lines and testable
alone. Rename `context` → `cc`. New files need `ARCHITECTURE.md` § Package
map rows (`TestPackageMapListsEveryTUIFile`). No behaviour change: the
existing `completion_*_test.go` suites are the oracle; run them before and
after each extraction.

---

## Optimizations

### O1 — DataGrid re-sort builds a row-count-sized map while rows are marked

**Where:** `~/go/tuikit/controls/datagrid_sort.go`, `SetSourceReordered`
(tuikit `0ce6f8c`).

**What:** with any Ctrl+click marks, each sort materialises
`SelectedRows()` (a Ctrl+A base run of 1M rows becomes a 1M-int slice and
then a 1M-entry `markFlips` map) and builds `at := map[int]int` with one
entry per row. A sort of a 1M-row grid then allocates two large maps on the
UI goroutine.

**Fix:** invert `newPerm` into an `[]int` (dense indexes, no hashing);
carry a base run that covers every row across the sort as a run, not as
flips. Benchmark (`BenchmarkSetSourceReorderedMarked`, 1M rows, 10 marks
and select-all) before and after.

### O2 — TreeModel.dropExcept is quadratic in re-delivered children

**Where:** `~/go/tuikit/controls/treemodel.go`, `dropExcept`
(`slices.Contains(keep, c)` per old child).

**What:** O(old × new). Not on gossms's refresh path today (`Reload` drops
the children before the load, so `SetChildren` finds none), but a host that
re-delivers a folder of 20k nodes in place pays 400M comparisons on the UI
goroutine. Fix: a `map[N]struct{}` when `len(keep) > 32`. Library-only
change; a benchmark pins it.

---

## Tests

### T1 — ~40 gosmo scripters run only under `-tags livedb`

**Where:** gosmo; `deadcode -test ./...` (which does not see the `livedb`
tag) lists `ScriptLogin`, `ScriptUser`, `ScriptCertificate`,
`ScriptAsymmetricKey`, `ScriptSymmetricKey`, `ScriptIndex`,
`ScriptForeignKey`, `ScriptCheckConstraint`, `ScriptStatistic`, the
full-text, external, Resource Governor, Database Mail and DML scripters,
and the unexported `Login.serverRoles` they call.

**What:** `go test ./...` never executes them, so a regression in any shows
only on a live run (~16 min per instance). gosmo's rule covers writes ("every
write has an offline test of its exact statement"); scripters are text
producers with the same exposure.

**Fix:** offline golden tests through the `gosmotest` row fakes, one per
scripter, starting with what gossms's Script As reaches (login, user,
keys, certificate, index, foreign key). Mutation-check each as gosmo's
CLAUDE.md asks. No production change.

### T2 — the flaky Session Properties test

`docs/open-threads.md` § Tests: `TestSessionPropertiesApplyReloadsTheSessionsTargets/Apply`
failed once with "gosmotest: no rule answers the query" for
`sys.dm_os_volume_stats`. Likely a background load started by an earlier
test landing on this test's fake. Run `go test -count=50 -run
'TestSessionProperties|TestXEvent' ./internal/tui/` to reproduce; fix by
having the test harness wait for (or cancel) the app's in-flight
`safego` work in `t.Cleanup`, not by adding a rule. Close the open thread.

---

## Docs and hygiene

### D1 — delete the finished 2026-11-21 review plan

Its five items shipped: B1 (gosmo `5b0eda9`), B2/B3 (`1ca0b10`,
`f088652`), B4 (`query.WithEachBatchOnce`, used by `query parse` in
`internal/cli/cmd/query.go`), B5 (`data_profile.go`'s `lob` rule). Its own
header says to delete it with the last item. Its last "left alone" bullet
asks whether `RemoveStaleTemps` deleting a user's own `x.sql.tmp123`
belongs in `docs/open-threads.md` — answer that before deleting (the
execute-bit half is settled: `docs/decisions.md` § File modes).

### D2 — receiver-name nits (`staticcheck -checks all`, ST1016)

gosmo `scripter_database_objects_order.go:162` (`a` vs `n`), gossms
`internal/cli/cmd/monitor_server_test.go:105` (`m` vs `s`). Not in the
default gate; fix when touching the files.

---

## Looked at and left alone

- **The safety classifier otherwise held**: DBCC, CHECKPOINT, transaction
  control and `SET IMPLICIT_TRANSACTIONS` behind `IF`/`WHILE`/`TRY` are cut
  and classed correctly; `ALTER AUTHORIZATION`, role membership procedures,
  event sessions, Resource Governor and `DATABASE SCOPED CONFIGURATION` are
  admin; `--where`/`--order-by` splicing is held to one read SELECT.
- **gosmo retry, AcquireConn, the Browser reply cache, the retargeting
  connector and the Poster wake/close protocol** read correct; their long
  comments match the code.
- **tuikit `Poster.Drain` loses the rest of a batch when a callback
  panics** — documented, and gossms ends the process on a UI-goroutine
  panic (EmergencySave), so nothing is lost there.
- **`query.Execute` spends a `SELECT DB_NAME()` round trip before the
  script**: one caller (Activity Monitor's Block/Sessions tab running its
  helper procedure); not worth an API change.
- **Splitting `internal/tui` into packages, gosmo into sub-packages, and the
  listed `dupl` near-twins**: settled in `docs/decisions.md`.
