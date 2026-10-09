# Review plan — 2026-10-09 (K series)

A second bug review of both repositories on 2026-10-09, at gossms `5ee1830` /
gosmo `3ea28a5`, after the L-series plan landed. It skipped what that pass read
and found sound (retry/pool core, quoting, the lexer and batch splitter, the
executor, config and `fileutil`, the editor's search and undo, the CSV sink,
Activity Monitor's maths) and read the rest by hand: catalog reads that scan a
nullable column, values handed back by the driver as `any`, msdb procedure
semantics, showplan aggregation, and the dialogs built on them. gosmo's
backward compatibility is waived for this plan; K2 uses that.

**Item IDs are `K1…K10`**, local to this plan. `K` appears in no commit
message or document of either repository.

**Evidence markers.** *Confirmed live* was observed on win10cli (17.0) with
read-only queries. *Confirmed by repro* was reproduced with a standalone Go
program against go-mssqldb. *Confirmed in code* was traced in source (and,
where an msdb procedure decides it, in that procedure's text read from the
server) with the failing input named. *Plausible* needs the probe the item
names before any code changes.

Like every earlier review plan this file is scratch: delete it once the last
step lands; anything left undone moves to `docs/open-threads.md`.

---

## What was checked and is clean

- `go vet ./...` and `go test ./...` green on both trees at the commits above.
- Mechanical sweeps: every `rows.Next()` loop has its `rows.Err()`; no
  `len()` used for screen-column maths in `internal/tui`/`internal/tuikit`;
  page-count arithmetic in gosmo is `bigint` throughout.
- Read and found sound: restore relocation planning (`restore_plan.go`),
  `ParseServerAddress`, collation folding, the table/FK/column scripter,
  module scripting and `alterModuleDefinition`, DML templates, login
  create/alter statements, permission statement rendering and
  `permTransition`, Query Store runtime aggregates (execution-weighted
  averages, pooled stdev), partition boundary literals, index creation,
  fragmentation, error-log reads and filters, XEvent session DDL, full-text
  index creation, Database Mail account updates (the `@username` trap is
  handled), schedule frequency forms, backup dialog device rules.

---

## Implementation order

Each step is one unit the user commits, gosmo first. At the end of every step
both repositories pass `gofmt -l`, `go vet ./...` (and `-tags livedb`),
`go test -race ./...`; gossms builds against the gosmo working tree; and the
step's live or tmux check has run (`docs/testing.md`). Live checks create
their own throwaway database/login and drop it; nothing pre-existing on the
test servers is touched. Mark an item **Done** under its heading when its
step lands.

- **S1 — gosmo reads. Done 2026-10-09** (uncommitted, gosmo). K1, K5, K10,
  K9 (probed; confirmed for unqualified `EXEC`). New tests
  `extended_properties_read_test.go` (appended), `statistics_histogram_test.go`,
  `login_details_test.go`, `live_k_reads_test.go`. Full `livedb`: green on
  17; on 14 and 13 a varying set of scripter/Query Store tests failed on
  win10cli memory pressure (Msg 701, 8645, XTP checkpoint error) — none calls
  S1 code. One failure repeated on 13 and is **not investigated**:
  `TestLiveScriptDatabaseOptionsRoundTrip`, the replay loses change-tracking
  retention (`5HOURS0` → empty); gosmo's open-threads list says it was green on 13.
  Version sweep on 13: 396 calls, 0 failed. gossms `go test -race` failed
  once in 12 runs in `internal/tui` (output lost; not reproduced).
- **S2 — gosmo backup model. Done 2026-10-09** (uncommitted, gosmo + a
  forced gossms compile fix). K2 gosmo half and gossms labels, K6 gosmo half.
  Removing `BackupType` broke gossms's four call sites, so the K2 labels
  moved here from S5. Live: `TestLiveBackupSetTypes` (new),
  `TestLiveBackupReads`, `TestLiveRestorePlan*` green on 17, 14, 13. tmux on
  17: Media Contents and the Restore Backup Set list name all seven set
  types; Script on a log set writes `RESTORE LOG … WITH FILE = 2`.
- **S3 — gosmo Agent.** K7 (step reorder self-reference).
- **S4 — gosmo Query Store.** K4 (all-category wait fan-out).
- **S5 — gossms.** K6 dialog half, K2's `backupHistoryQuery` CASE arms, K3
  (plan CPU), K8 (CONNECT SQL CASCADE), K10 display. tmux checks per item.

S5 depends on S1 and S2; the rest are independent.

---

## Bugs

### K1 — An extended property with a NULL value fails the whole Extended Properties page — gosmo — *confirmed in code*

`Database.ExtendedProperties` and `DatabaseExtendedProperties`
(`extended_properties.go:31,59`) select `CAST(value AS NVARCHAR(4000))` and
`scanExtProps` (`:150`) scans it into `ExtendedProperty.Value string`.
`sys.extended_properties.value` and `fn_listextendedproperty`'s `value` are
nullable (`is_nullable = 1`, checked live), and `sp_addextendedproperty`'s
`@value` defaults to NULL. One property added without a value — by any tool —
makes the scan fail with "converting NULL to string is unsupported", and every
Properties dialog that carries the page (database, schema, table, index, key,
statistic, user, role) shows the error instead of the grid.

**Fix.** Scan into `sql.NullString`; add `ExtendedProperty.IsNull bool` (the
field is new, so callers keep compiling). gossms shows a NULL value as empty
and, when the user leaves it untouched, sends nothing (it is not "changed").
**Test.** Fake-driver test returning a NULL value row for both reads. **Live.**
Throwaway database, `sp_addextendedproperty @name = N'x'` (no value) on a
table; the table's Extended Properties page lists `x` with an empty value.

**Done (S1, gosmo).** `scanExtProps` scans into `sql.NullString`;
`ExtendedProperty.IsNull` is new. gossms needed no change: a NULL value loads
as `""`, and an untouched row is not "changed", so Apply sends nothing.
Tests: `TestExtendedPropertiesReadANullValue` (fake driver, both reads);
`TestLiveKReads/NULL_extended_property` on 17, 14, 13.

### K2 — Backup set types gosmo cannot represent are shown, and will be restored, as something else — gosmo + gossms — *confirmed in code*

`BackupAction` has four values, but a backup set has seven types.
`backupTypeFromHeader` (`backup_headers.go:240`) maps `RESTORE HEADERONLY`'s
8 (differential partial) to `BackupActionDatabase` — a *Full* — and 6
(differential file) to `BackupActionFiles`. `BackupHistory`'s switch
(`restore.go:330`) knows `D/I/L/F` only, so `G` (differential file), `P`
(partial) and `Q` (differential partial) get the zero value `""`, which every
gossms label (`backupTypeLabel`, `backupSetTypeName`) renders as "Full"/
"Database". The Restore dialog's Backup Set list therefore offers a
differential as a full backup; restoring it onto a database that is not
already RESTORING is refused by the server, with nothing on screen to
explain why. gossms's own "View Backup History" query
(`backup_common.go`, `backupHistoryQuery`) likewise prints the raw letter for
`G/P/Q`.

**Fix (breaking).** Add `BackupSetType` (Database, Differential, Log, File,
DifferentialFile, Partial, DifferentialPartial) as `BackupHeader.SetType` and
`BackupInfo.SetType`, decoded from both sources; keep `BackupType` only if a
caller still needs the BACKUP verb, otherwise remove it. Methods:
`IsDifferential()`, `RestoreVerb()` (`LOG` for Log, `DATABASE` otherwise).
Read `bs.is_copy_only` into `BackupInfo.IsCopyOnly` while there. gossms labels
from `SetType`; `backupHistoryQuery` gains the three CASE arms.
**Test.** Table tests over header types 1–8 and history letters `D I L F G P
Q`. **Live.** Partial backup (`READ_WRITE_FILEGROUPS`) and a differential
partial of a throwaway database; Media Contents and the Restore list name
both correctly.

**Done (S2) except `backupHistoryQuery`.** gosmo: `BackupSetType` (seven
values; zero = unrecognised or not recorded) replaces `BackupType` on
`BackupHeader` and `BackupInfo` — removed, no caller needed the verb.
Methods `IsDifferential()`, `RestoreVerb()`, and `PlacesFiles()` (full,
partial or zero — what K6 needs for MOVE). `BackupInfo.IsCopyOnly` is new,
read from `bs.is_copy_only`. Example, diagram 14 and the ARCHITECTURE
history snippet updated. gossms: `backupSetTypeName` (Media Contents: SSMS's
names, "Unknown" for zero) and a new `backupSetLabel` (Restore list and
Inspect view: "Full", "Diff. Files", "Diff. Partial", …, fits the 15-column
field) take `BackupSetType`; `backupTypeLabel` stays for the Backup dialog's
action. Database Properties' last-backup dates switch on `SetType`, still
D/I/L only. Tests: `TestBackupSetTypeDecoding` (both decoders, all three
methods), `TestBackupHistoryScansNullColumns` (copy-only, NULL type),
`TestLiveBackupSetTypes` (all seven types plus a copy-only full, header and
history, on 17, 14, 13). **Left for S5:** `backupHistoryQuery`'s CASE arms
for `G/P/Q`.

### K3 — Operator CPU in actual and live plans is the busiest thread's, not the operator's — gossms — *confirmed live*

`decodeRuntime` (`internal/showplan/parse.go:406`) and `MergeProfiles`
(`internal/showplan/live.go:136`) take `max` over threads for CPU as well as
for elapsed time. Elapsed is wall-clock and max is right; CPU is consumed per
thread and adds up. Measured on win10cli, a MAXDOP 4 cross join in
HealthClinic: statement `QueryTimeStats CpuTime="117551"`, the Stream
Aggregate's four worker threads 32250 + 27646 + 28583 + 29067 = 117546 ms —
the plan view's Properties show "Actual CPU 32250 ms" for an operator that
burned 117 s, a quarter of the truth at DOP 4, and the figure disagrees with
the statement's own.

**Fix.** Sum `ActualCPUms`/`cpu_time_ms` across threads; keep elapsed as max.
Update the two doc comments ("times from the slowest thread"). **Test.**
Parse fixture with a multi-thread RelOp: CPU is the sum, elapsed the max;
`MergeProfiles` likewise. **tmux.** Run the probe query with Include Actual
Plan: the top Stream Aggregate's CPU ≈ the statement's CPU time.

### K4 — `QueryStoreWaitingQueries` with no category multiplies executions by the number of wait categories — gosmo — *confirmed in code*

`qsWaitFrom` (`query_store_reports.go:510`) joins each
`sys.query_store_wait_stats` row to its runtime-stats row on plan, interval
and execution type. That is one-to-one *per wait category*, which is all
`QueryStoreWaitCategories` needs. `QueryStoreWaitingQueries` (`:562`) with
`category == ""` — documented as "covers every one of them" — groups by query
instead, so each runtime-stats row is joined once per category the plan
waited on: `exec_count` is inflated by that factor and the Avg wait (total
wait ÷ summed executions) deflated by it, which also reorders the ranking.
Min/Max/Std dev are per-category values and mean nothing summed across
categories. gossms does not call it today; the library contract is wrong.

**Fix.** For `""`, pre-aggregate `ws` per (plan, interval, execution type)
in a derived table before the join; refuse Min/Max/Std dev there with
`ErrInvalidRequest` (no per-execution total exists to take them over).
**Test.** SQL-shape test that the empty-category form has no fan-out join.
**Live.** Throwaway database with Query Store and a query that waits in two
categories; its exec count matches `sys.query_store_runtime_stats`.

### K5 — Histogram RANGE_HI_KEY renders decimal, money, GUID and date keys wrongly — gosmo — *confirmed by repro*

`Statistic.Histogram` scans RANGE_HI_KEY into `any` and `formatHistogramKey`
(`statistics.go:450`) renders `[]byte` as hex and everything else with `%v`.
go-mssqldb hands back (repro against win10cli): `decimal(9,2) 123.45` as
`[]byte("123.45")` → shown `0x3132332E3435`; `money` likewise; a
`uniqueidentifier` as its 16 wire-order bytes → hex in the wrong byte order;
`datetime2(0)` as `time.Time` → `2026-01-02 03:04:05 +0000 UTC`; `real` as
widened float64 noise. Statistics Properties ▸ Histogram shows these.

**Fix.** Read the column's `DatabaseTypeName`/scale from `rows.ColumnTypes()`
once and format by type: decimal/numeric/money as text, GUID via
`mssql.UniqueIdentifier` (scan destination chosen by type), date/time types
in the layout their type and scale imply, `real` at 32 bits, binary as 0x.
**Test.** Formatter table test per type. **Live.** Throwaway table with
decimal, money, uniqueidentifier, datetime2(3), date, real columns and a
statistic on each.

**Done (S1, gosmo).** `Histogram` reads RANGE_HI_KEY's
`DatabaseTypeName`/scale once (`histogramKeyType`) and formats by it:
decimal/numeric/money as their digits, GUID via `mssql.UniqueIdentifier.Scan`
(byte order fixed), each date/time type in its own layout and scale,
`real` at 32 bits, `bit` as 1/0, binary as 0x. The driver values per type were
probed live first (17). Tests: `TestHistogramKeyFormatsByType`;
`TestLiveKReads/histogram_keys` on 17, 14, 13.

### K6 — The Restore dialog restores a transaction-log backup with RESTORE DATABASE — gossms + gosmo — *confirmed in code*

`buildRestoreOptions` (`restore_dialog_ops.go:550`) never sets
`RestoreOptions.Action`, and `RestoreOptions.FromHeader`
(`restore_plan.go:203`) sets only the file number and MOVE clauses, so every
restore defaults to `RESTORE DATABASE`. The Backup Set list offers log
backups (labelled "Transaction Log"), and the header the dialog reads says the
set is a log — but the statement sent is `RESTORE DATABASE … FROM <log
backup>`, which the server refuses ("…was created by BACKUP LOG and cannot be
used for this restore operation" — wording to confirm in the live check).
The dialog also plans MOVE clauses for a log or differential set exactly as
for a full one.

**Fix.** `FromHeader` sets `o.Action` from K2's `RestoreVerb()`, and builds
MOVE clauses only for a full or partial set (a differential or log restore
lands on files the earlier restore already placed). The dialog shows the
set's type and, when Recovery is WITH RECOVERY on a non-full set, says the
database must already be RESTORING. **Test.** gosmo: `FromHeader` with a
log header gives `RESTORE LOG` and no MOVE; gossms: options built from a
log-set selection. **Live.** Throwaway database: full + log backups; restore
the full WITH NORECOVERY, then the log, both from the dialog.

**gosmo half done (S2).** `FromHeader` sets `o.Action` from
`h.SetType.RestoreVerb()` (overriding any caller value — documented) and
builds MOVE clauses only when `h.SetType.PlacesFiles()`; otherwise
`RelocateFiles` is nil and `files` may be nil. Tests:
`TestRestoreOptionsFromHeaderFollowsTheSetType` (log, differential,
differential partial, partial); `TestLiveBackupSetTypes` restores a full
WITH NORECOVERY then the log through `FromHeader` (17, 14, 13). The dialog
already scripts `RESTORE LOG` for a log set with no gossms change (seen in
tmux). **Left for S5:** the dialog's RESTORING warning on a non-full set
with WITH RECOVERY; `buildRestoreOptions` still reads the file list for a
log/differential set when `NeedsFileList` says so (wasted read, harmless —
skip it when `!h.SetType.PlacesFiles()`); the gossms test and the
from-the-dialog live restore.

### K7 — Moving a job step fails when its old "go to step N" equals its new position — gosmo — *confirmed in code*

`Job.ReorderSteps` deletes a moved step and re-adds it at its target position
with its *original* goto references (`agent_job_step.go:450`), repairing all
references afterwards. `sp_add_jobstep` runs `sp_verify_jobstep`, which
raises Msg 14235 (severity 16) when `@on_success_step_id = @step_id` or
`@on_fail_step_id = @step_id` (read from msdb's text on 17). So in a job
whose step 3 says "on success go to step 1", moving step 3 to the top
re-adds it as step 1 with `@on_success_step_id = 1` and the whole atomic
batch is rolled back: Move Up, and Job Properties ▸ Steps' Apply with that
order, are refused for a legitimate reorder.

Separately, `MoveStep`'s doc comment says `sp_delete_jobstep` resets "a
reference to a step at or after the deleted one"; the procedure decrements
references after it and resets only references to it.

**Fix.** Re-add a moved step with each goto action replaced by "go to the
next step" (step id 0); the repair pass, which already covers every step
whose original action is goto, writes the real targets. Correct the comment.
**Test.** No `addStepStmt` in a reorder carries `@on_*_action = 4`.
**Live.** Throwaway job, three steps, step 3 → "on success go to step 1";
move it to the top; flow reads step 1 → "go to step 2".

### K8 — Login Status: changing CONNECT SQL away from a grant WITH GRANT OPTION is refused — gossms — *confirmed in code*

`pageLoginStatus` (`login_props.go:680`) sends DENY/REVOKE CONNECT SQL with
empty options. When the stored state is `GRANT_WITH_GRANT_OPTION` (shown as
"Grant"), SQL Server refuses both without CASCADE (Msg 4611) — the rule
`permTransition` exists for, which this page bypasses.

**Fix.** Keep the loaded `ConnectSQLState` and apply through
`permTransition(orig, new)`; "Grant" over a with-grant-option original is no
change. **Test.** Unit test of the page's transition from
`GRANT_WITH_GRANT_OPTION` to Deny and Default. **tmux.** Throwaway login
granted CONNECT SQL WITH GRANT OPTION; set Deny; Apply succeeds.

### K9 — View Dependencies misses procedures that name the object without a schema — gosmo — *plausible*

`Database.Dependents` (`dependency.go:39`) joins
`sys.sql_expression_dependencies` on `referenced_id`. For a non-schema-bound
reference whose schema depends on the caller — `SELECT … FROM Orders` inside
a procedure — the documented behaviour is `is_caller_dependent = 1` and
`referenced_id` NULL, so the procedure is not listed as depending on
`dbo.Orders`. (HealthClinic's modules all schema-qualify, so it could not be
observed there.) Both queries also join `sys.objects` without
`referenced_class = 1`, so a type or XML schema collection id can be read as
an object id.

**Probe first.** Throwaway database: table `dbo.t`, procedure with
unqualified `SELECT * FROM t`; check its row's `referenced_id` and
`is_caller_dependent`, and what `sys.dm_sql_referencing_entities(N'dbo.t',
N'OBJECT')` returns. **Fix if confirmed.** Dependents from
`sys.dm_sql_referencing_entities` (2008+, so within the 2016 floor), joined to
`sys.objects`, schema-binding from a left join to the expression-dependency
row; add `referenced_class = 1` to Dependencies. **Test.** Version sweep on
13; live as in the probe.

**Done (S1, gosmo) — confirmed, narrower than described.** The probe (17
and 13) showed an unqualified *table* reference (`SELECT … FROM t`) is not
caller-dependent: it binds with `referenced_id` set, even from another schema
and even when the table is created after the procedure. The miss is an
unqualified **`EXEC target`**: `is_caller_dependent = 1`, `referenced_id`
NULL, so `Dependents(dbo.target)` listed none of its callers. Fixed as
planned: `Dependents` reads `sys.dm_sql_referencing_entities` (bracketed name
accepted; a missing object returns no rows, no error), joined to `sys.objects`
with `referencing_class = 1`, schema binding from an `EXISTS` over
`sql_expression_dependencies` with `referenced_class = 1`; `Dependencies`
gained `referenced_class = 1`. Permissions: a user with only VIEW DEFINITION
is refused `sys.sql_expression_dependencies` (Msg 229) but may read the DMV —
the `EXISTS` keeps today's requirement, no new one. Test:
`TestLiveKReads/caller-dependent_dependents` on 17, 14, 13; version sweep on
13.

### K10 — "Bad password time" shows 1900-01-01 for a login that never had one — gosmo + gossms — *confirmed live*

`Login.Details` (`login.go:184`) returns `LOGINPROPERTY(…,
'BadPasswordTime')` as is. For a login with no failed attempt the server
answers its 1900-01-01 sentinel — shifted by the server's UTC offset,
`1900-01-01 02:00:00` on win10cli for six of its seven SQL logins — which is
not NULL, so gossms's "-" fallback (`login_props.go:629`) never applies and
the Status page shows the 1900 date.

**Fix.** gosmo maps any value before 1900-01-02 to the zero Time (documented
on the field), for `PasswordLastSetTime` too. **Test.** Decode test.
**tmux.** Login Properties ▸ Status on such a login shows "-".

**gosmo half done (S1).** `loginPropertyTime` maps NULL and anything before
1900-01-02 to the zero Time, for `BadPasswordTime` and `PasswordLastSet`
(server confirmed live: base type `datetime`, `1900-01-01 02:00` on all
three instances). Tests: `TestLoginPropertyTimeDropsTheSentinel`;
`TestLiveKReads/bad_password_sentinel` on 17, 14, 13. The gossms display
check stays in S5 (the existing "-" fallback should now apply unchanged).

---

## Not raised (checked, minor or by design)

- `Database.Permissions` (`security.go:71`) filters `major_id` without
  `class_desc = 'OBJECT_OR_COLUMN'`, unlike its siblings; a collision needs
  an object id equal to a schema, principal or type id — add the filter if
  the file is touched.
- `sp_enumerrorlogs` dates parse US-first (`01/02/2006`), so a dd/mm server
  locale misreads days ≤ 12; the raw string is kept beside it.
- `formatBytes` can print "1024.0 KB" just below 1 MB.
- `InstanceKey` keeps `localhost`, `.` and `(local)` apart.
- `Login.Details` finds a credential only for SQL logins (`sys.sql_logins`);
  `sys.server_principals.credential_id` covers every login.
- Backup's hard-coded `WITH INIT` — settled in `docs/decisions.md`.
