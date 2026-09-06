# Next five features — implementation plan

Five items lifted out of `docs/open-threads.md`, in the order they are to be
built. Each is a *deferred capability*, not a bug: nothing here is broken today.

Every API shape named below was read out of the real source on 2026-09-06, not
recalled — `~/go/gosmo/*.go` and `internal/tui/*.go` at the line references
given. Re-check anything that has moved before writing against it.

Two rules that apply to all five, from `CLAUDE.md`:

- gosmo work goes **in gosmo**, built and tested there first. Never narrow an
  existing gosmo capability because gossms doesn't call it.
- Green `go test ./...` is not verification. Each item's "Verify live" step is
  the one that counts — `docs/testing.md`.

Close an item by deleting its bullet from `docs/open-threads.md` (the line
reference is given per item) and adding the feature to `CHANGELOG.md` at
release time, not before.

---

## 1. Query Store: per-query time series (Tracked Queries plot)

Closes `docs/open-threads.md:417`. **No new SQL** — the read already exists in
gosmo and nothing calls it.

**What exists.** `gosmo.Database.QueryStoreTrackedQueryContext(ctx, queryID,
opts)` (`query_store_reports.go:793`) returns `[]*QSPlanIntervalStat`
(`:464`: `PlanID`, `StartTime`, `EndTime`, `ExecCount`, `Value`), ordered plan
by plan, oldest interval first — exactly the order a per-plan series is plotted
in. It is already covered by `TestLiveVersionSweep` and
`live_query_store_reports_test.go:215`.

**Shape.** A per-query time-series *mode* of `QueryStorePanel`, not an eighth
report. The seven reports rank queries; this one plots one query the user has
selected, so it belongs to the row, like the plan pane and the two plan
actions — all of which already key off `qsResultRow.queryID`
(`query_store_reports.go:56`) and disable themselves on a zero.

**gossms work** (no gosmo change):

1. `query_store_panel.go` — one field for the mode and one for the series
   payload (`[]*gosmo.QSPlanIntervalStat` plus the query id it was read for),
   beside `res`/`plans`. Reuse the existing `planSeq`/`planCancel` pair's
   *pattern* but add a third seq/cancel: a series read must not kill the plan
   read beside it, which is the reason there are already two.
2. Read on the same trigger as the plan pane (selection change), or lazily on
   entering the mode — pick lazily; the series is a second round trip per row
   and the panel already reads plans on every cursor move.
3. Draw with `charts.HistoryChart` (`internal/tuikit/charts/history.go:23`):
   one `charts.Series` per `PlanID`, `Values` in interval order, `Interval` set
   from the Query Store interval width (derive it from
   `EndTime.Sub(StartTime)` of any bucket), `TimeLabel` from the newest bucket.
   Series colours from the same source the dashboard uses. This also gives
   `HistoryChart.Draw` its first caller — see `open-threads.md` § By design,
   which records that `Draw` was kept deliberately while unused; do not remove
   that note, it still covers `StackedHistoryChart`.
4. **Buckets are not dense.** `QSPlanIntervalStat` rows exist only for
   intervals in which that plan ran. `HistoryChart` indexes series positionally
   and `Series.At` returns 0 outside the slice (`charts/common.go:26`), so two
   plans with different gaps would be drawn against different time axes.
   Build one ordered interval axis from the union of `StartTime` across all
   plans and fill each series against it — a missing interval is a genuine
   zero. This is the one place this feature can silently lie; pin it with a
   test.
5. Toolbar: a mode toggle in `acts` (the plan-action row), disabled with a
   reason when the selected row's `queryID` is 0 — the row is an interval or a
   wait category, not a query. Honour the existing `busy` latch and the
   `More ▾` overflow (`query_store_panel.go:146`), or the button is unreachable
   below ~170 columns.
6. The metric/statistic/window selectors apply unchanged: `opts` is the same
   `QueryStoreReportOptions` the report was read with. `Top` and the execution
   floor do **not** apply — one query, every interval. Dim them with a reason,
   the way `qsFilters` already does per report
   (`query_store_reports.go:190`).

**Tests.** A unit test over the axis-union fill (two plans, disjoint interval
sets, assert both series land on the same buckets and that a gap is a zero, not
a shift). A panel test that the mode is refused on a `queryID == 0` row.

**Verify live.** Against a database with Query Store on and a query with two
plans (force one, then unforce, to get a second): confirm the two series move
independently and that the newest bucket is rightmost.

---

## 2. Log File Viewer: merged multi-log view

Closes `docs/open-threads.md:381`. The Windows event log (`:385`) stays out —
it needs WMI, which a no-CGO portable build cannot have. That exclusion is not
reopened by this item.

**What exists.** `LogViewer` (`internal/tui/log_viewer.go:45`) holds one
`logType`/`logNum` pair, reads with
`gosmo.Server.ReadLogFilteredContext(ctx, logType, logNumber, search)`
(`~/go/gosmo/error_log.go:194`), caches each family's enumeration in
`files map[gosmo.ErrorLogType][]*gosmo.ErrorLogFile`, and sorts descending
in `sortLogEntriesDesc` (`:621`). `gosmo.ErrorLogEntry` already carries a
`Source()` (`error_log.go:73`) and the grid already has a Source column
(`logGridColumns`, `:510`).

**Shape.** A *selection set* of `(logType, logNum)` pairs replacing the single
pair, with the single pair as the degenerate case. SSMS's left-pane checkbox
tree is not being rebuilt: the two existing selectors stay and gain a "select
files…" checklist behind the file selector.

**gossms work** (no gosmo change — N reads of an existing call):

1. Replace `logType`/`logNum` with an ordered `[]logFileRef{Type, Num}`, and
   keep `ShowLog` (`:364`) as the one-element setter so every existing caller
   — including `App.refreshOpenLogViewer` (`:782`) and the tree's Recycle path
   (`:754`) — is unchanged.
2. `Load` (`:381`) fans out: one `ReadLogFilteredContext` per selected file,
   each with its own `logReadTimeout` deadline, all under the one `ctx` the
   panel's `cancel` pulls. Bound the concurrency (the Databases folder's 8-wide
   fan-out is the precedent) and keep the existing `seq` guard: **one** posted
   callback applying the merged result, never one per file.
3. **A failed file is a row, not a failed read.** Merging N reads means one
   unreadable archive must not empty the grid — collect per-file errors and
   report them in the status line ("3 of 4 files read"), showing what came
   back. This mirrors `applyJobStates`' rule in gosmo and the Databases
   folder's per-row `N/A` degradation.
4. Merge and sort with the existing `sortLogEntriesDesc`; the sort key stays
   the entry date, and **ties must break deterministically** (by file, then by
   position within the file) or a refresh reorders same-second rows under the
   cursor.
5. The Source column is not enough to say which *file* a row came from — a
   merged view needs a File column. Add it to `logGridColumns` and
   `logExportColumns` (`:510`, `:514`) only when more than one file is
   selected, so the single-file view is untouched.
6. `currentFileLabel` (`:349`), the status line (`summary`, `:549`), the
   export header (`exportText`, `:737`) and the window title all name one file
   today. Each needs the multi-file form ("SQL Server: 4 files").
7. Recycle stays single-file — it acts on a family, and `recycleDenied`
   (`:285`) is unchanged. After a cycle the enumeration is dropped
   (`Refresh`, `:373`), which already renumbers; with a *set* selected, a cycle
   invalidates the selection's numbering too. Re-anchor to the newest file
   rather than keeping stale numbers.
8. The toolbar busy latch is taken before the confirmation, as
   `open-threads.md` § Log File Viewer records. Do not move it.

**Tests.** Merge/sort unit test with entries from two files at the same
timestamp (assert stable, file-qualified order). A test that one file's error
leaves the other file's rows on screen.

**Verify live.** Two archives plus the current file on win10cli, then a
Search (server-side, `xp_readerrorlog` args 3-6) applied across the set —
the date bounds must still go as text, and the two search strings are still
AND-ed.

---

## 3. Database Audit Specifications

Closes `docs/open-threads.md:504`. The largest of the five, and the one with
real gosmo work.

**What exists.** The server half only:
`~/go/gosmo/audit_specification.go` — `ServerAuditSpecification` (`:26`),
`ServerAuditSpecifications{,ByName,}Context`, the `AuditActionGroups` pick list
(`:150`, `class_desc = 'SERVER' AND configuration_level = 'Group'`),
`ServerAuditSpecificationSpec` + `createStatement` (`:226`), `SetState`,
`WithDisabled`/`withSpecificationDisabled`, `AddActionGroups`,
`DropActionGroups`, `SetAudit`, `Drop`; the script in `scripter_server.go:548`.

**The database half is not a copy.** `sys.database_audit_specification_details`
records **actions on securables**, not only groups: a detail row carries
`class_desc`, `major_id`, `minor_id` and `audited_principal_id`, so the clause
is `ADD (SELECT ON OBJECT::dbo.T BY public)` as well as `ADD (SCHEMA_OBJECT_ACCESS_GROUP)`.
That is the whole of the extra work, and it is what makes this item bigger than
it looks.

**gosmo work** — new file `database_audit_specification.go`, modelled on the
server one:

1. `DatabaseAuditSpecification` on `*Database`: `SpecificationID`, `Name`,
   `AuditGUID`, `AuditName` (LEFT JOIN `sys.server_audits` — orphaning is the
   same hazard, `audit_specification.go:33` says why), `IsEnabled`,
   `CreateDate`, `ModifyDate`, and details.
2. A `DatabaseAuditAction` struct for a detail row: `ActionName`, `ClassDesc`,
   plus the resolved securable name and principal name. Resolve `major_id`/
   `minor_id` in SQL (`OBJECT_SCHEMA_NAME`/`OBJECT_NAME`, `SCHEMA_NAME`,
   `DB_NAME`) and `audited_principal_id` through `DATABASE_PRINCIPAL_ID`'s
   inverse — a raw id in the UI is unreadable and a second round trip per row
   is not acceptable. Use `commaList` (`sql_agg.go`) for any aggregation, never
   `STRING_AGG`: the floor is SQL Server 2016 SP1.
3. Reads: `DatabaseAuditSpecificationsContext`,
   `...ByNameContext` (`ErrNotFound` on absence, matching the server half),
   and a bare handle `DatabaseAuditSpecification(name)` — the `Server.Database`
   pattern, and the only form usable under a `WithScript` context.
4. The pick list: `DatabaseAuditActionGroupsContext` reading
   `sys.dm_audit_actions` with `class_desc = 'DATABASE'` for groups, and a
   second call for `configuration_level = 'Action'` (the per-securable actions
   — SELECT, INSERT, EXECUTE …). Read from the DMV for the reason the server
   one is (`audit_specification.go:146`): every release adds to it.
5. Writes: `CreateDatabaseAuditSpecificationContext(spec)`, `SetState`,
   `AddActions`/`DropActions`, `SetAudit`, `Drop` — all inside the same
   disabled window the server half uses. **Reuse
   `withSpecificationDisabled`'s shape and the `specificationDisabledKey`
   context marker** (`:310`) rather than writing a second one; factor if the
   types make that awkward, but the off/apply/on-with-restore-on-failure
   behaviour must be identical.
6. `validateAuditActionGroup` (`:201`) applies unchanged to group names — a
   bare keyword, no brackets. The **securable** in an action clause is the
   opposite: an identifier, so it goes through `quoteIdent`/`qualifiedName`.
   Getting these two the wrong way round is the injection hole here.
7. Script: `ScriptDatabaseAuditSpecification` beside the server one, emitting
   `CREATE DATABASE AUDIT SPECIFICATION … FOR SERVER AUDIT … ADD (…) WITH (STATE = …)`,
   `IF EXISTS`-wrapped like the rest of the scripter's output.
8. Version floor: run `TestLiveVersionSweep` on major 13 after adding the
   reads. `sys.database_audit_specifications` is 2008+, but confirm each
   column named.

**gossms work** — a new node family. The full touch-point list, from what
`NodeServerAuditSpecification` already occupies:

- `tree_node.go` — `NodeDatabaseAuditSpecifications` / `...Specification`, plus
  the icon, label, properties and contextual entries at `:271`, `:366`, `:461`,
  `:502` and the folder list at `:193`.
- `explorer_databases.go:135` — the folder under a database's Security node,
  where SSMS puts it; a loader beside `loadServerAuditSpecificationsChildren`
  (`explorer_security.go:62`), registered in `explorer_loaders.go`.
- `explorer_filter.go:123` — Name and Creation Date, client-side, like the
  other five; no push-down.
- `detail_browser.go` dispatch + folder and object detail loaders beside
  `serverAuditSpecification{s,}Detail` (`detail_browser_security.go:166`),
  using `filterObjects` on the collection before rows are built.
- `explorer_object_ops.go` — Drop (`:293` is the model) and the required
  right, which is `ALTER ANY DATABASE AUDIT`, **not** `rightAlterAnyAudit`
  (`:627`). A new right name must be added to the gate's name list or
  `permission_gate_names_test.go` will pass while gating nothing.
- `scripting.go:125` and `:181` — the script verbs.
- A New Database Audit Specification dialog modelled on
  `new_audit_specification_dialog.go`, and a Properties page. Both pages that
  write declare their rights with `withRequires` — `docs/db-rules.md`
  § Permission gating; `prop_page_requires_test.go` fails on a page that
  declares nothing.
- Audit Properties' one-page rule (`open-threads.md:498` §) applies here too:
  `ALTER DATABASE AUDIT SPECIFICATION` replaces settings wholesale.

**Tests.** gosmo: statement tests for create/add/drop covering both a group
clause and a securable clause, plus quoting of a securable with a `]` in its
name. gossms: a page test through `fakedb_test.go` pinning the statement the
apply sends, driven with `editText`/`toggleByName` (never `SetValue`).

**Verify live.** A throwaway database on win10cli with a throwaway server
audit: create a specification with one group and one per-object action, toggle
state, alter it while enabled (must be refused → the disable window must
handle it), script it, drop it. Then re-run on major 13.

---

## 4. Database-scope DDL triggers

Closes `docs/open-threads.md:506`. Small, and the one item with a hard "don't"
attached.

**Do not widen `Database.triggersWhere`.** It is `sys.triggers` with
`parent_class = 1` (`~/go/gosmo/database.go:716`), and the existing per-database
Triggers folder lists exactly that. Database DDL triggers are `parent_class = 0`
— no parent table, no schema — so they are a **third family**, as
`server_trigger.go:17` already says in so many words. `gosmo.Trigger`
(`database.go:686`) carries `TableName` and `Schema`, which a DDL trigger has
nothing to put in.

**gosmo work** — new `database_trigger.go`, modelled on `server_trigger.go`:

1. `DatabaseTrigger`: `Name`, `IsEnabled` (inverse of `is_disabled`),
   `CreateDate`, `ModifyDate`, `Events` (from `sys.trigger_events`, via
   `commaList`), `Definition` from `sys.sql_modules`. **LEFT JOIN
   `sys.sql_modules`**, for the reason `serverTriggerSelect` does
   (`server_trigger.go:45`): a CLR trigger has no row there and an inner join
   drops it silently. `is_ms_shipped = 0`.
2. `DatabaseTriggersContext`, `DatabaseTriggerByNameContext` (`ErrNotFound`),
   and a bare `DatabaseTrigger(name)` handle carrying `Enable`, `Disable`,
   `Drop` — `DROP TRIGGER name ON DATABASE`, which is what makes this its own
   drop rather than `DropTrigger`'s (`database.go:748`, schema-qualified and
   wrong here).
3. Script: `Scripter.ScriptDatabaseTrigger` — `scriptModule` cannot be reused
   as-is, it takes a schema.

**gossms work** — a "Database Triggers" folder under the database, beside the
existing Triggers folder (`explorer_databases.go:107`). Same touch-point list
as item 3, minus the New dialog: this family is read/enable/disable/drop, like
Server Triggers, which is the node to copy (`tree_node.go:305`, `:400`,
`:465`; `explorer_object_ops.go:317`; `scripting.go:181`). The right is
`ALTER ANY DATABASE DDL TRIGGER`.

**Naming risk, decide before building:** two sibling folders called "Triggers"
and "Database Triggers" under one database read as a distinction without a
difference. SSMS files DDL triggers under Programmability > Database Triggers.
Either follow SSMS's placement or rename the DML folder — do not ship both
labels flat and unexplained.

**Verify live.** A `CREATE TRIGGER … ON DATABASE FOR CREATE_TABLE` on a
throwaway database: list, disable, enable, script, drop; confirm the existing
DML Triggers folder is byte-for-byte unchanged in what it lists.

---

## 5. Database-scoped credentials and a Cryptographic Providers folder

Closes `docs/open-threads.md:509`. Two halves that share a page; do the folder
first, it is nearly free.

**Half A — Cryptographic Providers folder (gossms only).** The read already
exists: `Server.CryptographicProvidersContext` returns `[]*CryptographicProvider`
(`ProviderID`, `Name`, `GUID`, `Version`, `DLLPath`, `IsEnabled` —
`~/go/gosmo/credential.go:251`) and is already called by
`new_credential_dialog.go:39`. It needs a folder under Security, a folder
detail grid and an object detail grid — no writes: registering a provider
stays out (it needs a DLL path on the server's filesystem, and SSMS's own
dialog is a file browser this build cannot have). A read-only folder declares
no rights and belongs in `pagesThatOnlyRead` terms — no `withRequires`, and no
Drop entry in `explorer_object_ops.go`.

**Half B — database-scoped credentials (gosmo + gossms).**
`sys.database_scoped_credentials` has no reads at all today.

1. gosmo: `DatabaseScopedCredential` on `*Database` in a new
   `database_credential.go`, mirroring `credential.go`'s `Credential` (`:31`)
   — `CredentialID`, `Name`, `Identity`, `CreateDate`, `ModifyDate`. **The
   secret is write-only here for the same reason** (`credential.go:15`): the
   catalog never exposes it, so `Alter` takes a secret it can set or clear and
   the script can only emit a placeholder. Say so in the file's doc comment,
   as `credential.go` does — that comment is the load-bearing kind.
2. Writes: `CreateDatabaseScopedCredential{,Context}`, `Alter`, `Drop` —
   `CREATE/ALTER/DROP DATABASE SCOPED CREDENTIAL`. No `IF EXISTS` on the drop:
   `TestDropStatementsAreNotIdempotent` pins that rule family-wide
   (`open-threads.md:173` §).
3. Script: `Scripter.ScriptDatabaseScopedCredential`, alongside
   `buildCredentialScript`'s treatment of the unreadable secret
   (`scripter_server.go:459`).
4. gossms: a Credentials folder under the database's Security node, plus a New
   dialog and Properties page copied from `new_credential_dialog.go` and
   `credential_props.go`. The right is `CONTROL` on the database /
   `ALTER ANY CREDENTIAL`'s database-scoped equivalent — probe it live rather
   than assuming, and register the name in the gate's list.
5. The secret field is a `propsheet` Password row; check every new label fits
   `propsheet.LabelWidth` (30 columns) or
   `TestNoPropertySheetLabelIsTruncated` fails — and an over-long label
   renders as a *different*, shorter label, silently.

**Verify live.** A throwaway database: create a scoped credential with an
identity and a secret, alter the secret, script it (confirm the script does not
claim to carry the secret), drop it. The providers folder is verified on
win10cli by being empty and saying so, not by erroring — a server with no EKM
provider is the ordinary case (`credential.go:266`).

---

## Order, and why

1 first because it is the only one with no new SQL and no new tree family — it
is a panel mode over a read gosmo already ships. 2 next: also gossms-only, and
it is the item a user notices. 3 is the big one and wants a clear run. 4 is
small but carries a naming decision that should not be rushed at the end of 3.
5 last, and its two halves are separable if the run gets short — the providers
folder can ship without the scoped-credential work.
