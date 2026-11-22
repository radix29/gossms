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

- **W2 — B2 + B3**: an unqualified user-defined type resolves through the
  default schema, then dbo; type names match the way catalog names do
  (contains, bracketed prefix).
- **W3 — A1**: split `baseCompletionCandidates` (339 lines) into phases, which
  B1 (done, W1) and B2 both touch. Do it right after B2 so the split moves
  tested code.
- **W4 — A3 + N1** (gosmo): `CatalogTypeKind` gets a zero "unknown" value;
  `catalog()` takes a struct; `SystemCatalog` stops sending a query that
  can't return rows. Rerun `TestLiveVersionSweep` on 13.
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

### B2 — an unqualified user-defined type resolves to the first schema alphabetically

**Where:** `completion_ddl.go` `ddlTypes.match` (unqualified branch).

**What:** with `a.Dup` (table type) and `dbo.Dup` (alias type), `CREATE TABLE
dbo.T (Home Dup ‸` offers nothing (it matched `a.Dup`, a table type) and
`CREATE PROCEDURE dbo.p @rows Dup ‸` offers `READONLY`. SQL Server resolves
an unqualified type name as system type, then the caller's default schema,
then dbo — never another schema; so the right answers are `NULL`/`NOT NULL`
and `AS`/`OUTPUT`. `inv.defaultSchema` is already loaded
(`completion_inventory.go`).

`ddlTypes.match` also serves `completion_datatypes.go` since W1 (DECLARE,
`DECLARE @t TABLE (`, ALTER TABLE columns), so the fix reaches those too.

**Fix:** `ddlTypes` carries `defaultSchema` (set in `QueryPanel.ddlTypes`);
the unqualified branch tries the default schema, then `dbo`, under
`SameName`, and matches nothing else. Tests: the two probes above, plus a
type only in a third schema, unqualified (no match: no follow-up offered).

### B3 — user-defined types match by prefix only, and never after a `[`

**Where:** `completion_keywords.go` (`keywordCandidates`, the
`userTypes.userType` branch) and `completion_ddl.go`
`qualifiedTypeCandidates`.

**What:** catalog names match by `nameMatch` (contains, ASCII fast path,
`Partial` for a mid-name hit). User-defined types use
`strings.HasPrefix(strings.ToUpper(…))` on the label or the bare name, so
`CREATE TABLE dbo.T (Home addr‸` does not find `dbo.PostalAddress`, and
`Home [ph‸` finds no `dbo.Phone`. The types are the only names in
completion that behave this way.

**Fix:** match `typ.Name` with `nameMatch(…, strings.ToLower(prefix))` and
set `Partial` as `tableCandidates` does; and let a type position reached in
bracket state (`LexBracket` in `baseCompletionCandidates`, which skips the
keyword path today) offer the types with the bracket already open. Tests:
`addr‸`, `[ph‸`, `dbo.addr‸`, `dbo.[ph‸`.

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

### A1 — `baseCompletionCandidates` is 339 lines

**Where:** `internal/tui/completion_provider.go`, `baseCompletionCandidates`
(the longest function in the three repos).

**What:** one function does lexing and the row/column conversion, statement
narrowing and scope, the inventory-free answers (spatial, qualified types,
keywords), the inventory answers (DDL targets, procedures) and the relation
answers. Observed costs:

- `ddlCompletionTarget(tokens, rowStart+replaceFrom)` is computed twice
  (once to gate `qualifiedTypeCandidates`, again after the inventory loads).
- Qualified user-defined types go through `qualifiedTypeCandidates` for
  CREATE TABLE only; the routine-signature case reaches the same answer by
  another path (the keyword list). B1 added a third: `qualifiedDataTypePosition`
  widens the `qualifiedTypeCandidates` gate in `baseCompletionCandidates`.
- `slices.Contains([]string{"TABLE", "VIEW", "PROCEDURE", "FUNCTION"}, …)`
  builds a slice per keystroke.
- `rowStart+replaceFrom` is recomputed in ~10 places.

**Plan:** a `completionPosition` struct built once (`buf`, `tokens`,
`stmtTokens`, `scope`, `state`, `chain`, `qualifier`, `prefix`, `at :=
rowStart+replaceFrom`, `ddl` action/type/tail), and three methods in order —
`positionalCandidates` (no inventory: spatial, data types incl. B1's
positions, keywords), `catalogCandidates` (DDL targets, procedures),
`relationCandidates` (the final switch). Each returns `(items, done bool)`.
Pure move, no behaviour change: `completion_*_test.go` is the oracle, run
before and after; `TestSQLCompletionDataTypePositions` (B1's probe table) included.

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

### A3 — gosmo `CatalogTypeKind`'s zero value is a real kind

**Where:** `~/go/gosmo/catalog.go` (`CatalogAliasType = iota`); consumer
`completion_ddl.go` `ddlParameterChoices` (`typ.Kind ==
gosmo.CatalogTableType && typ.Name != ""`).

**What:** a zero `CatalogType` reads as an alias type, so a caller holding
"no user type matched" must test `Name` to tell it apart. In gossms the
`Name` test on that line is dead (a table kind is never the zero value), and
`ddlTypes.match` returns a zero `CatalogType` for a system type, which reads as
"alias type".

**Fix (breaking):** start the kinds at 1 (`CatalogAliasType CatalogTypeKind =
iota + 1`), keeping the SQL CASE in `catalogTypesSelect` in step (it writes
the Go constants); have `ddlTypes.match` return `*gosmo.CatalogType` (nil for
a system type) and drop the `Name` test. CHANGELOG entry under v0.4.0 when
the user asks for one.

---

## Nits

### N1 — gosmo `catalog()` shape and a query that can't return rows

- `~/go/gosmo/catalog.go`: `catalog(ctx, v, where, fnWhere, aggWhere,
  typeWhere string)` — four positional where-strings, the next family makes
  five. A `catalogQuery{views, objects, functions, aggregates, types}` struct
  literal per caller reads better and can't transpose two arguments.
- `SystemCatalog` sends `t.is_user_defined = 1 AND t.schema_id = 4`, which its
  own doc comment says is always empty. Send `1 = 0` (keeps the six result
  sets in order) or skip the result set when `types` is empty; one less
  scan of `sys.types` per completion warm-up.
- After any change to the batch: `TestLiveVersionSweep` on
  `win10cli\SQL2016` (13), as docs/open-threads.md § Version support asks —
  the new `sys.types` result set has not been swept yet. Note that
  `catalog()` is one batch, so a refusal there fails every completion read.

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
