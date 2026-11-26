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

- **W7 — A3** (tuikit): split `controls/treeview.go` (902 lines) at its mouse
  section.
- **W8 — N1**: nits (gosmo `scanCapabilityRows` nested-map helper,
  `query.Execute`'s doc comment).
- **W9**: move what stays undone to `docs/open-threads.md`, then delete
  this file.

Each step: build in the library first when one is involved (`go test ./...`
there), then `gofmt -w . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...`, then drive the built binary (`docs/testing.md`). Nobody
commits but the user.

---

## Simplifications and refactoring

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
