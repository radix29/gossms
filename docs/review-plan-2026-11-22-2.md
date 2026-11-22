# Review plan, 2026-11-22 (second pass)

A second review of gossms, `~/go/gosmo` and `~/go/tuikit` on the day the
first one shipped, looking for bugs, inconsistencies, optimizations,
simplifications and architecture. Nothing is fixed yet. Close an item by
deleting it; delete this file with the last one. gosmo and tuikit may break
back-compat where an item says so (CHANGELOG entry required).

**Method.** `go build`, `go vet`, `staticcheck` (default gate, and `-checks
all` without ST1000/3/20-22), `deadcode -test` and `go test ./...` in all
three repos; every fuzz target in the three repos run for 25 s each; throwaway
probes of the safety classifier (run as a test, then deleted); a read of the
code added in the last few commits (completion split, `--param`, DataGrid
re-sort, clipboard dispatcher, gosmo retry and pinned connections); a check
for newer dependencies (`go list -m -u`). Every claim marked *live* was run
with `sqlcmd` against win10cli (17), throwaway objects in `tempdb` or a
throwaway database, all dropped after. The binary was not driven: each fix
still needs the `docs/testing.md` drive.

**What came out clean.** Build, vet, staticcheck (both gates) and tests in
all three. `deadcode` lists only exported library surface in gosmo (1) and
tuikit (20), unused and kept deliberately (CLAUDE.md). No file in the three
repos is GOOS-suffixed or build-tagged; `runtime.GOOS` appears only in
`internal/version`. No column math uses `len` of a display string. Every file
write goes through `fileutil`'s atomic path or an `O_EXCL`/append open. gosmo's
row reads all go through `Server.query`/`Database.query`, which check
`rows.Err` centrally. The 2026-11-22 DataGrid re-sort (`setAllMarkedReordered`)
and TreeModel `dropExcept` changes, and the clipboard `Paste`/`Copy` ordering,
read correct. Classifier probes that came out right: `CREATE SCHEMA … CREATE
VIEW …; DROP DATABASE …` (classified as two DDL statements and the rest hidden,
but the server refuses the whole batch, Msg 156: *live*), `SELECT 1; CREATE
VIEW …; …` (same), `EXEC (…) AT`, `ALTER AUTHORIZATION`, `ALTER [SERVER] ROLE`,
`[sp_configure]` as an implicit EXEC, `master..xp_cmdshell`, `DBCC … CLEAR`.
`--param` names `@stmt`/`@params` do not collide with `sp_executesql`'s own
(*live*). Fuzz: see § Fuzz results.

Severity is ordered by what a user can lose, not by effort.

## Recommended implementation order

- **W4 — D1**: go-mssqldb v1.12.0 in gosmo then gossms; reserve the new
  `authentication` synonym; refresh the "v1.11.2 is the latest" claims.
- **W5 — A1 + C1**: completion's version and edition gates in one table;
  `CREATE TABLE`/parameter type lists (same files, one drive).
- **W6 — A2**: split gosmo `index_management.go` (968 lines).
- **W7 — N1**: comment drift in gosmo `Server.query`, undocumented
  `Database.query`; gotuipic's `go` directive.
- **W8**: delete this file.

Each step: build in the library first when one is involved (`go test ./...`
there), then `gofmt -w . && go vet ./... && staticcheck ./... && deadcode -test ./...`,
then `go test ./...`, then drive the built binary (`docs/testing.md`). Nobody
commits but the user.

---

## Dependencies

### D1 — go-mssqldb v1.12.0 (2026-11-05)

**What:** `go list -m -u` reports v1.12.0. Its changes: the ADO.NET
`Authentication` keyword (mapped to `fedauth`) and spaced fedauth value
names; gokrb5 swapped for the community-maintained fork. Three repo texts
say v1.11.2 is the latest or was checked: docs/open-threads.md (bulk load
"Blocked on go-mssqldb (v1.11.2 is the latest)") and docs/decisions.md
(`sql_variant` inner type; the retryable-error note).

**Checked against v1.12.0:** `bulkcopy.go`'s column declaration still writes
no `COLLATE`, so the bulk-load thread stays blocked.

**Fix, in order:**

1. gosmo: bump; add `"authentication"` to `reservedDSNKeys`
   (`connection_dsn.go`) beside the other synonyms of reserved keys — the URL
   form gosmo builds does not translate synonyms, so an Extra Properties
   `Authentication=…` would otherwise be silently ignored, which that
   comment already calls no better than an override.
   `TestExtraParamsADOSynonymsMatchTheDriver` should fail first and pass
   after; if it does not notice, extend it to diff the driver's whole map.
   CHANGELOG entry.
2. gosmo: Kerberos dial on Linux (`KerberosOptions`) live once, since the
   gokrb5 implementation changed — or record it in docs/open-threads.md if no
   KDC is reachable.
3. gossms: bump; rerun the live connect tests.
4. Re-check the two decisions.md notes against v1.12.0 and update the version
   they cite; update the open-threads bulk-load line to "v1.12.0 checked".

---

## Architecture, simplification, refactoring

### A1 — completion gates live in three places

**Where:** `internal/tui/completion_builtins.go`,
`completion_expressions.go` (`builtinCompletionSupported`,
`expressionCompletionItems`), `completion_provider.go` (~lines 216-228),
`completion_keywords.go` (`namedWindow`).

**What:** the builtin table says it is the one inventory ("Kind, call shape
and availability belong to the same entry"), but:

- Versions are raw `14`…`17`; everywhere else gossms writes
  `gosmo.SQLServer2017`…`SQLServer2025`.
- Fabric availability is split: `builtinWarehouseOnly` is a kind bit,
  `noWarehouse` a positional bool field, so every row carries a trailing
  `false`.
- `||` (2025) is gated through a kind-0 table row, but `IS [NOT] DISTINCT
  FROM` (2022) through a `strings.Contains(item.Label, "DISTINCT FROM")`
  filter in `completion_provider.go`, and the `WINDOW` clause through an
  inline `AtLeast(gosmo.SQLServer2022)`.
- `expressionCompletionItems` takes five positional bools (`scalar,
  condition, aggregate, analytic, fabric`).

**Plan:** named fields in the table (`{name: "TRIM", kind: builtinScalar,
since: gosmo.SQLServer2017}`) and the named constants; `noWarehouse` becomes
a `builtinNoWarehouse` bit; add `IS DISTINCT FROM`/`IS NOT DISTINCT FROM`/
`DISTINCT FROM`/`NOT DISTINCT FROM` and `WINDOW` as gated rows (kind 0, as
`||` is) and delete the provider's string filter and `namedWindow`, so
`builtinCompletionSupported` is the one gate; pass the analyzed
`completionContext` (or a small flags struct) to
`expressionCompletionItems`. No behaviour change: the `completion_*_test.go`
suites and `version_gate_test.go` are the oracle; run them before and after.

### C1 — type completion misses common and version-gated types

**Where:** `completion_ddl.go`, `ddlDataTypes` (used by `CREATE TABLE`
columns and routine parameters).

**What:** offered types omit `SYSNAME`, `GEOGRAPHY`, `GEOMETRY`,
`HIERARCHYID`, and SQL Server 2025's `JSON` and `VECTOR(` (Azure SQL too);
user-defined types are never offered. A column typed `sysname` or
`geography` then gets no `NULL`/`NOT NULL` follow-up, since that step only
recognizes listed types.

**Fix:** add the four system types (not under Fabric); add `JSON` and
`VECTOR(` as rows gated through A1's table (2025, not Fabric Warehouse);
leave user-defined types to a separate decision (needs the catalog inventory
in a path that is lexical today). Tests per type and per version.

### A2 — gosmo `index_management.go` is over the split prompt

**Where:** `~/go/gosmo/index_management.go`, 968 lines (gosmo CLAUDE.md:
over ~900 is a prompt to split).

**Plan:** along its existing seams — maintenance writes (`Reorganize`,
`Disable`, `Enable`, `Drop`, the `*AllIndexes*` family, `SetOptions`,
`Rename`, `Rebuild`, `SetIncludedColumns`, `UpdateStatistics`, the
compression helpers) stay; the reads (`StorageInfo`, `Fragmentation`,
`FragmentationStats`, `XMLIndexes`, `IndexUsage`, `fragmentationSelect`,
`scanFragmentation`, `FragmentationMode.normalize`) move to
`index_stats.go`. Extract by exact line range, diff byte-for-byte, then
delete the source lines. Pure move (gosmo's ARCHITECTURE.md names no
`index_*` file, so no doc follows).

---

## Nits

### N1 — comment drift and directive skew

- gosmo `server.go`, `Server.query`'s doc: "retrying on a transient
  connection failure (up to readRetryAttempts tries) (a dropped pooled
  connection, etc.)" — `withRetry` retries on `IsTransient`, which includes
  deadlock victims and Azure's transient errors; and the doubled
  parenthetical. `Database.query` (`database_conn.go`), which that comment
  calls its counterpart, has no doc comment at all.
- `~/go/gotuipic/go.mod` says `go 1.27.3`; gossms, gosmo and tuikit say
  `1.27.4`.

---

## Fuzz results

All 17 fuzz targets passed 25 s each with no new corpus entries: gossms
`FuzzScope`, `FuzzRecordReader`, `FuzzSQLExpressionCompletion`,
`FuzzParseFilter`, `FuzzClassify`, `FuzzParse`, `FuzzParseObjectName`,
`FuzzFormatJSON`/`XML`/`SQL`, `FuzzLexersAgree`, `FuzzLexer`; gosmo
`FuzzExternalModuleVerb`, `FuzzNames`, `FuzzSpans`; tuikit
`FuzzDrawTextLineMatchesTruncateLine`, `FuzzRunesWidthAgreesWithDisplayWidth`.
A short run says little about rare inputs. A longer run (`-fuzztime 10m`) of
`FuzzClassify` and `FuzzLexersAgree` still fits (W2 ran `FuzzClassify` 90 s, clean).
