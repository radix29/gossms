# Review plan, 2026-11-22 (fourth pass)

A fourth review of gossms, `~/go/gosmo` and `~/go/tuikit`, after the
user-defined-type completion, `WithLock` fail-fast and RECEIVE/WRITETEXT
commits. Nothing is fixed yet. Close an item by deleting it; delete this file
with the last one. gosmo and tuikit may break back-compat where an item says
so (CHANGELOG entry required, when the user asks for one).

**Method.** `go vet`, `staticcheck` (default gate and `-checks all` without
ST1000/3/20-22), `deadcode -test`, `govulncheck` and `go test ./...` in all
three repos; `go test -race -count=1` over gosmo, tuikit's `loop`,
`clipboard`, `controls`, `dialogs`, and gossms's `fileutil`, `query`, `db`,
`config`, `cli`, `activity`, `xevent` and `tui/...`; `dupl -t 120` over all
three; `go list -m -u` for direct dependencies; a read of the last commits in
each repo (`completion_ddl.go`, `fileutil/lock.go`, `safety/classify.go`,
gosmo `catalog.go`, the picture panel's tone controls), `query/executor.go`,
tuikit `widgets/input_field.go`, `clipboard/dispatcher.go` and
`loop/runner.go`; and throwaway probes, run as tests and then deleted: about 90
safety-classifier statements and about 30 completion positions. No server was
contacted and the binary was not driven: every fix still needs the
`docs/testing.md` drive.

**What came out clean.** Build, vet, both staticcheck gates, govulncheck and
every test in all three repos; every race run listed above. `deadcode` lists
only gosmo's exported `QSStatistics`, unused and kept deliberately (CLAUDE.md).
No direct dependency has a newer version. `dupl -t 120` re-finds only pairs
`docs/decisions.md` § Code and dependency hygiene already settles, plus the
one in A2. Every `'%s'` spliced into T-SQL in gosmo goes through
`escapeSingle`/`QuoteLiteral` or an allow-listed constant. The classifier
probes all came out fail-closed: CTE-fronted DML, dynamic SQL, `OPENQUERY`/
`OPENROWSET`, `NEXT VALUE FOR`, statement labels, comments and literals
hiding keywords, `dbo.`/`master..`/bracketed system procedures and Service
Broker verbs. `SELECT * FROM t AS updatetext` floors to write, like the bare
`receive` column docs/open-threads.md already lists. The items closed by the
three earlier 2026-11-22 plans are in place: gosmo `index_management.go` is
split, `safety` reads dotted names once (`readName`), and tuikit's
`PanelManager.HandleMouse` is 47 lines.

Severity is ordered by what a user can lose, not by effort.

## Recommended implementation order

- **W2 — B2 + B3**: done (default schema then dbo; `nameMatch` and open-`[`
  positions; `TestSQLCompletionUserTypeResolution`; driven on win10cli).
- **W3 — A1**: done (`completionPosition` built by `completionPositionAt` and
  `scopeCompletionPosition`; phases `routineSignatureCandidates`,
  `positionalCandidates`, `catalogCandidates`, `relationCandidates`; one
  `ddlCompletionTarget` call; driven on win10cli against the pre-split binary,
  identical popups).
- **W4 — A3 + N1**: done (gosmo `CatalogAliasType = iota + 1`, zero is no
  kind; `catalog(ctx, catalogQuery{...})`; an empty `types` skips the
  sys.types read, so `SystemCatalog` sends five result sets;
  `ddlTypes.match` returns `*gosmo.CatalogType`, nil for a system type, and
  the dead `Name` test is gone; `TestLiveVersionSweep` on 13: 442 calls, 0
  failed; driven on win10cli: READONLY, NULL/NOT NULL and `=` after alias,
  table and system types).
- **W5 — A2**: `buildSecurablesMatrix`'s 12 positional parameters.
- **W6 — B4** (gosmo): `BackupRefusal` compares a `Ref`'s name case-blind.
- **W7 — N2**: nits.
- **W8 — R1**: release notes for gossms, gosmo and tuikit (only if the user
  asks: CLAUDE.md keeps CHANGELOG/RELEASE edits out of fixes).
- **W9**: delete this file.

Each step: build in the library first when one is involved (`go test ./...`
there), then `gofmt -w . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...`, then drive the built binary (`docs/testing.md`). Nobody
commits but the user.

---

## Bugs

### B4 — gosmo `BackupRefusal` takes a user database named `TempDB` for tempdb

**Where:** `~/go/gosmo/backup.go`, `BackupRefusal`'s `isSystemDB`.

**What:** a `DatabaseRef` carries no ID, so the system databases are
recognised by `strings.EqualFold(d.Name, name)`. Under a case-sensitive
*server* collation a user database `TempDB` or `Master` can exist beside
the system one, and its differential/log backups are refused as tempdb's or
master's. Fails closed (refuses a valid backup), and no CS server exists here
(docs/open-threads.md § Environment), so it is low.

**Fix:** compare with `SameName(d.Server().Info().Collation, d.Name, name)`
(the collation `connection_retarget.go` uses for the same reason; guard a
nil `Info()` on a test fake); the
documented "by name" fallback stays. Offline test with a fake CS server
info. Add the case to the open-threads CS-server list for a live check.

---

## Architecture, simplification, refactoring

### A2 — `buildSecurablesMatrix` takes 12 positional parameters, two always the same

**Where:** `internal/tui/securables_matrix.go` `buildSecurablesMatrix`;
callers `securables_matrix.go` (Properties), `new_role_dialog.go`,
`new_application_role_dialog.go` (the clone `dupl` reports).

**What:** every caller passes `8, 12` for the heights and the same pair of
closures, `applySecurable`/`applyColumnSecurable` over a database and a
principal name; only how the principal and database are obtained differs
(`*principal` vs `d.objectName()`, the prefetched database vs
`DatabaseRef(dbName)`).

**Plan:** `buildSecurablesMatrix(d, database, seed securablesSeed, candidates,
find, target func() (*gosmo.Database, string))` — `securablesSeed` holds
`initial`/`entries`/`colEntries` (zero for a create dialog), the heights
become constants, and the two apply closures are built inside from
`target`. The create dialogs' 10-line clones go. Tests: the Securables page
tests for Database Role, Application Role and their New dialogs.

---

## Nits

### N2 — small ones

- `completion_expressions.go` `keywordDistinctFrom` is a one-line alias of
  `sqlparse.IsDistinctFrom` with seven callers; call the sqlparse function.
- `completion_ddl.go` `QueryPanel.ddlTypes` and `completion_expressions.go`
  (the TVF lookup) both spell `p.app.completion.inventories[completionInventoryKey(p.conn.Opts, p.database)]`;
  a `p.currentInventory()` returning the ready inventory or nil (loading, err,
  nil catalog) folds the four-part readiness test the DDL one repeats.
- tuikit `controls/treeview.go` `TreeView.HandleMouse` is 147 lines, the
  longest in tuikit; split by zone (scrollbar, expander, row) as
  `PanelManager.HandleMouse` was, when a change lands there anyway.

---

## Release

### R1 — three repos carry unreleased work with no release notes

gossms is 97 commits past `v0.2.1` and `CHANGELOG.md` has no unreleased
section (the command line, completion and safety work among them); tuikit is
64 past `v0.0.3`; gosmo's `v0.4.0 (unreleased)` entry is 21 commits behind
and omits `Catalog.Types`/`CatalogType`/`CatalogTypeKind`. CLAUDE.md (here
and gosmo's) keeps CHANGELOG/RELEASE edits out of fixes, so this is a
separate step, done only if the user asks, before the next tag.
