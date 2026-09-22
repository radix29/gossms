# Review plan — 2026-09-22

A review of both repositories (gossms ~101 kLOC non-test, gosmo ~43 kLOC
non-test) for bugs, inconsistencies, optimisations, simplifications and
architecture. This is the plan, in priority order within each section. An
item that has since been implemented says so under its heading, marked
**Done**; everything else is still open. gosmo's backward compatibility is explicitly
waived for this review, so gosmo API breaks are on the table in § gosmo API.

**Item IDs in this document are `Q1…Q20`.** They are local to this plan and
chosen so they do not collide with the `P*` IDs the 2026-09-18 plan left in
commit messages, the `R*` IDs `docs/decisions.md` cites, or the permanent
`B*`/`V*`/`N*` series `docs/open-threads.md` owns.

Items marked **probed** were reproduced during the review with a throwaway
test, which was removed afterwards. Items marked **live** still need a real
server to confirm, per `docs/testing.md`.

## What was already clean

Re-measured so the next review does not have to:

- `gofmt -l`, `go vet ./...`, `go vet -tags livedb ./...`, `staticcheck ./...`
  are silent on **both** repositories, and `go test -race ./...` passes on both.
- `internal/tuikit`'s non-repo imports are still exactly tcell, tcell/color
  and displaywidth.
- No bare `go` statement in `internal/tui` outside `safego.go`; no
  `context.Background()` in a connection-scoped load (the eleven hits are the
  documented exceptions: Connect dialog's first dial, clipboard, update
  check, goroutine labels).
- An AST scan of every `fmt.Sprintf` in both repos for a `'%s'`/`N'%s'`/`[%s]`
  slot fed by an unescaped argument found none that is not an allowlisted
  enum or a validated keyword.
- No `time.Local` in gosmo; no `defer` inside a loop body; no dropped error
  that is not a documented best-effort repair.
- `dupl -t 120` over gossms finds only 12 small clone groups; the codebase is
  not duplication-heavy. gosmo's clones are almost all one shape — see Q15.
- `sp_rename`, `KILL`, `MUST_CHANGE`/`CHECK_EXPIRATION`, `bindScriptArgs`'
  literal/comment skipping and `AcquireConn`'s retry were read and are right.

---

## Bugs

### Q1 — `GO -- step 2` runs the batch twice; `GO 5` at end of script runs it once — **probed** — **Done**

**Done (2026-09-22), option 1.** `internal/tuikit/sqltext` holds
the one separator rule (`GoSeparatorAt`, now also returning the count) and
`SplitBatches`; `controls`, `sqlparse` and `query` all call it, and the
`go-mssqldb/batch` import is gone. Decisions taken: no repetition cap (SSMS
has none; cancellation is checked before every repetition); a batch run more
than once adds SSMS's `Batch execution completed N times.`; `GO 0` runs the
batch no times; `GO 1 2` and a non-ASCII digit count are no longer
separators, since neither is one count. Block comments now nest in the
splitter *and* in the Ctrl+Enter and IntelliSense lexers, so all three agree
on `/* /* */ GO */`; so does the SQL syntax highlighter, whose per-line cache
now carries a comment depth rather than a bool. Pinned by `sqltext`'s tests,
`TestExecuteRunsGoBatchesByTheEditorsRule` (reverting to `batch.Split` fails
all five rows) and the nested-comment tests in `controls` (statement select
and `TestSQLHighlighterNestedBlockComment`) and `sqlparse`; live by
`TestLiveGoBatchCounts` (`livedb`) and by the built binary. Nothing open.

`internal/query/executor.go:285` splits scripts with go-mssqldb's
`batch.Split`, whose separator rule differs from the one the editor uses
(`controls.isGoSeparatorLine`, `sqlparse.goSeparatorLineAt`). Measured
against go-mssqldb v1.11.0:

| Script | `batch.Split` result | Expected |
|---|---|---|
| `select 1` / `GO -- step 2` / `select 3` | `select 1` **twice**, then `select 3` | once — the digit is in a comment (SSMS) |
| `select 1` / `GO 2` (no trailing newline) | `select 1` **once** | twice (SSMS) |
| `select 1` / `GO;` / `select 2` | splits; `; select 2` is the next batch | not a separator (the editor's rule) |
| `select 1` / `GO/*x*/` / `select 2` | splits | not a separator (the editor's rule) |

The first row is the dangerous one: a trailing comment that happens to end in
a number (`GO -- retry 3`, `GO -- v2`) silently re-runs the preceding batch —
an `INSERT` or `UPDATE` applied N times. The second is the ordinary load-test
idiom `GO 1000` typed at the end of a window.

The comment above `goSeparatorLineCases` (both
`internal/tuikit/controls/sql_statement_test.go:225` and
`internal/tui/sqlparse/prefix_scan_test.go:375`) records that `batch.Split`
refuses `GO -- 5 items`, which is true only because text follows the digit.
It does not record the case above, which is how this survived.

**Fix.** Stop using `batch.Split`. Write a splitter that applies the editor's
rule, with a small lexer so a `GO` line inside a `'string'`, `"quoted"` or
`[bracketed]` identifier, or a nested `/* block comment */` is not a
separator, returning `[]struct{ text string; count int }`. Then one of:

1. **Recommended:** move the separator rule into one zero-dependency leaf
   package (e.g. `internal/tuikit/sqltext`, no tcell import) used by
   `controls`, `sqlparse` and `query`, retiring the two copies and the
   "shared table keeps them in step" discipline. This keeps tuikit
   extractable — the new package is inside it and depends on nothing.
2. Minimal: implement it inside `internal/query` and add the shared
   `goSeparatorLineCases` table there as a third pinned copy.

Either way drop the `github.com/microsoft/go-mssqldb/batch` import.
Keep or drop the driver's silent cap of 1000 repetitions deliberately — SSMS
has none.

**Pin it:** a table test over the four rows above plus the existing
`goSeparatorLineCases`, asserting batch texts *and* counts. Mutation: revert
to `batch.Split` and the `GO -- step 2` row must fail. **Live:** run
`CREATE TABLE #t(i int)` / `INSERT #t VALUES(1)` / `GO -- step 2` /
`SELECT COUNT(*) FROM #t` and check for 1.

### Q2 — Script Table as CREATE silently changes the table — **probed** (type), read (rest) — **Done**

**Done (2026-09-22).** All six points plus the index gaps, in gosmo.
`sqlTypeString` always emits the scale of the three types (so does
`PartitionFunction`, which gained `MaxLength`/`Precision`/`Scale` — it had
rendered its input type bare). `Column` gained `TypeSchema`,
`IsUserDefinedType`, `IsPersisted`, `IdentityNotForReplication`, `IsSparse`,
`IsColumnSet`, `MaskingFunction`, `GeneratedAlwaysType`, `IsHidden`, and
`ColumnTypeString` qualifies a user-defined type (`[app].[Phone]`, never a
length) — which also fixes table-type scripts, and shows the qualified name
on gossms's Columns pages. `CheckConstraint` gained `IsNotTrusted` and
`IsNotForReplication`; an enabled-but-untrusted constraint is now added `WITH
NOCHECK` too. Temporal tables script their period, `SYSTEM_VERSIONING` and
history table, and their DROP switches versioning off first (DROP TABLE is
refused otherwise). Point 6, the author's call: `COLLATE` is emitted only
when the column differs from the database default. Indexes and key
constraints carry `PAD_INDEX`, `FILLFACTOR`, `IGNORE_DUP_KEY`, lock options
and compression (a heap's too); a disabled index is disabled at the end of
the script. One extra round trip per script (`Table.scriptOptionsContext`).
Pinned by `buildTableScript` unit tests per gap and by
`TestLiveScriptFidelity` (`livedb`), which replays into a second database and
compares the catalogs — green on 17 and on the 2016 floor; with the scale,
check-constraint or SET-option fix reverted it fails. Still missing (Always
Encrypted, FILESTREAM, ledger, memory-optimized, per-partition compression):
gosmo's open threads, § Scripter fidelity.

`~/go/gosmo/scripter.go` `buildTableScript` and `sqlTypeString`:

1. **`datetime2(0)`, `time(0)` and `datetimeoffset(0)` script as the bare
   type**, i.e. precision 7 (`sqlTypeString`, `scale > 0` at line 720). A
   zero scale is the common "no fractional seconds" choice. The same function
   feeds `Parameter.TypeString`, so procedure parameters are wrong too, and
   IntelliSense's column detail shows the wrong type. Fix: for these three
   types always emit `(scale)`; the catalog always reports one.
2. **CHECK constraints are not scripted at all.** `ScriptTableContext` reads
   columns, indexes, foreign keys and data space, never
   `Table.CheckConstraintsContext`. A single-constraint builder already exists
   (`Scripter.ScriptCheckConstraintContext`, behind gossms's `NodeCheck`
   Script-as) — reuse it, with the `WITH NOCHECK` / `NOCHECK CONSTRAINT` pair
   for a disabled or untrusted one if it does not already emit them.
3. **Computed columns lose `PERSISTED`** (`is_persisted` is not read), and a
   persisted computed column that is part of an index then fails to recreate.
4. **`ROWGUIDCOL` is read (`Column.IsRowGUID`) and never emitted.** Same for
   `IDENTITY … NOT FOR REPLICATION`, `SPARSE`, `MASKED WITH (FUNCTION = …)`,
   and `GENERATED ALWAYS AS ROW START/END` + `PERIOD FOR SYSTEM_TIME` +
   `SYSTEM_VERSIONING` (all 2016+, inside the version floor).
5. **An alias-type column is emitted unqualified and unbracketed** (`Phone`
   instead of `[dbo].[Phone]`), because `tp.name` is used bare; a type in any
   non-default schema fails to resolve. Select `SCHEMA_NAME(tp.schema_id)` and
   `tp.is_user_defined` and qualify it.
6. `Column.Collation` is read and unused. SSMS's default ("Script collation:
   False") also omits it, so this one is parity, not a bug — but a column whose
   collation differs from the database default is recreated with the default.
   Emitting `COLLATE` only when it differs is the better default; author's call.

Indexes (`scriptIndex`) have smaller gaps of the same kind: a disabled index
scripts as enabled, and `DATA_COMPRESSION`, `IGNORE_DUP_KEY`, `PAD_INDEX` are
lost.

`DROP And CREATE` of a table is already destructive, so the harm is in
**CREATE To** used to clone a schema elsewhere: the copy differs silently.
**Pin it** with `buildTableScript` unit tests (it is already split out for
that), one per gap; **live** round trip on a disposable table carrying each
feature: script, drop, run the script, compare `sys.columns` /
`sys.check_constraints` before and after.

### Q3 — A scripted `CREATE SCHEMA` or `CREATE PROCEDURE` cannot run (Msg 111) — **Done**

**Done (2026-09-22), with Q9.** `Database.exec` no longer prefixes a USE: it
records a `ScriptEntry{Server, Database, SQL}`, and the rendering (`String()`,
`Statements()`) puts a `GO` after every `USE` and every statement.
`scriptUsePrefix` is now `"USE [App'DB];\nGO\n"`. **Live** on win10cli: the
rendered `CreateSchemaContext` + `CreateStoredProcedureContext` script ran
through `sqlcmd` into a scratch database, and a second script ending in
`DropDatabaseContext(force)` ran too — it needs the `USE [master]` the
renderer inserts before a server-scoped statement that follows a
database-scoped one.

`~/go/gosmo/database.go:114` records a database-scoped write under
`WithScript` as one entry, `"USE [db];\n" + stmt`. For a statement that must
open its batch, that entry can never run: `Database.CreateSchemaContext` and
`CreateStoredProcedureContext` (`CREATE OR ALTER PROCEDURE`) both script to
`USE [db];` followed by the DDL in the same batch, which SQL Server refuses
with Msg 111 — the rule `buildSchemaScript`'s own comment works around.
`script_write_common_test.go:64` (`scriptUsePrefix`) pins the broken shape.

gossms does not call either method today, so no gossms dialog shows it; it is
a library bug any other caller hits. The gossms half is Q9: three of the five
call sites join captured entries with a blank line and no `GO`, so even a
correct entry shares a batch with the one before it (a second `DECLARE` of the
same variable, e.g. two forced drops on Managed Instance, fails the same way).

**Fix** in gosmo: capture as `"USE [db];\nGO\n" + stmt`, and give
`ScriptCollector` the rendering (Q9). Update `scriptUsePrefix` and every
`script_*_write_test.go` expectation. **Live:** run the rendered script for
`CreateSchemaContext` on a scratch database.

### Q4 — Restore's "Close existing connections" races, and fails outright on Managed Instance — **Done**

**Done (2026-09-22), as planned.** `RestoreOptions.CloseExistingConnections`
makes the RESTORE one batch: a guarded `SET SINGLE_USER WITH ROLLBACK
IMMEDIATE`, the RESTORE, and a guarded `SET MULTI_USER` (released only if the
batch set it). The guards — online and not in standby — are load-bearing:
dropped, the NORECOVERY and STANDBY cases fail live with Msg 5052.
`Server.BuildRestoreStatement` writes the `killDatabaseSessions` batch
instead on a Managed Instance; the package-level `BuildRestoreStatement`
keeps the SINGLE_USER form. `RestoreContext` repairs MULTI_USER off the
caller's cancellation when the batch is cut short. gossms dropped its own
SINGLE_USER step, its `DatabasesContext` existence check and its MULTI_USER
repair; the dialog's Script now includes the close as well, as SSMS's does.
Pinned by `backup_test.go`'s restore tests and gosmo's
`TestLiveRestoreCloseExistingConnections` (parked second session; recovery,
NORECOVERY, STANDBY, new database). Live in the dialog on win10cli with a
parked `sqlcmd` session: restored, session killed, database left MULTI_USER.
Left open: a STANDBY database's readers are not closed (the ALTER is
refused there), so a log restore over one with readers connected still fails
with 3101 — recorded in gosmo's open threads.

`internal/tui/restore_dialog_ops.go:417` issues `ALTER DATABASE … SET
SINGLE_USER WITH ROLLBACK IMMEDIATE` as its own round trip, and only then
runs `RESTORE`, as a separate statement on whichever pooled connection it
gets. Two failures:

- **The single-user slot is free for anyone between the two statements** —
  another application's reconnect, or gossms's own background reads (Object
  Explorer refresh, IntelliSense inventory, a capability probe) issuing
  `USE [target]`. The RESTORE then fails with 3101 "Exclusive access could not
  be obtained". SSMS puts both statements in one batch.
- **On Managed Instance** `SET SINGLE_USER` is refused with Msg 5008 (gosmo's
  `refusesSingleUser` exists for exactly this), so the dialog fails on the
  preparation step with a message about SINGLE_USER, not about the restore.

**Fix** in gosmo, as a library feature: `RestoreOptions.CloseExistingConnections`
makes `BuildRestoreStatement` prefix the RESTORE with the SINGLE_USER switch
in the **same batch** (or, where `refusesSingleUser`, with
`killDatabaseSessions`' batch), and `RestoreContext` calls `restoreMultiUser`
on failure. gossms then drops its own two steps and its hand-rolled
MULTI_USER repair. **Pin** with `BuildRestoreStatement` tests; **live** on a
disposable database with a second session parked in it.

### Q5 — Renaming an enabled server audit can leave auditing switched off — **Done**

**Done (2026-09-22), as planned.** Both re-enables go through
`restoreWindow`; the failure path joins the restore's error to the rename's.
Pinned by `TestRenamingAnEnabledAuditRestoresAfterACancel` (both paths) and
`TestRenameFailureKeepsTheRestoreError`; both fail with the old code.

`~/go/gosmo/audit.go:561` and `:571`. `ServerAudit.RenameContext` is the one
write that cannot use `withAuditDisabled`, and it re-enables the audit on the
caller's `ctx` directly — on both the failure path and the success path.
`restoreWindow` (same file, `:480`) exists precisely because re-enabling on a
cancelled or expired context "fails without reaching the server, so the
audit stayed switched off exactly when the window promised it would not".
A rename cancelled by the user, or one whose MODIFY NAME ran out the
deadline, leaves the audit OFF; the failure path also discards the
re-enable's error.

**Fix:** route both re-enables through `restoreWindow`, and on the failure path
join the restore error to the rename error (`errors.Join`) rather than
dropping it. **Pin** with a scripted-driver test whose MODIFY NAME returns
after cancelling `ctx`, asserting the `STATE = ON` still reached the driver.

### Q6 — Module scripts drop `SET ANSI_NULLS` / `SET QUOTED_IDENTIFIER` — **Done**

**Done (2026-09-22), as planned.** Each `moduleKind` query reads the two
flags; `moduleSetOptions` emits both, each in its own batch, before the
definition (CREATE and ALTER). DDL triggers (`buildDatabaseTriggerScript`,
server triggers) are not `moduleKind`s and still don't. Live:
`TestLiveScriptFidelityModuleSetOptions` creates a procedure under both OFF
that returns `"literal"` only because of them, replays the script on one
session into a second database and checks `sys.sql_modules` and the
procedure's result.

`~/go/gosmo/scripter.go` `scriptModule` emits the stored definition only. A
view, procedure, function or trigger records the two settings it was created
under (`sys.sql_modules.uses_ansi_nulls`, `uses_quoted_identifier`), and they
govern its behaviour: `= NULL` comparisons, and whether `"x"` is a string or
an identifier. gossms sessions run with both ON, so Script as ALTER / DROP
And CREATE of a legacy module created with either OFF silently changes its
semantics, or fails to compile where it used `"string"` literals. SSMS emits
both `SET` lines before every module. `buildTableScript` has the table
equivalent (`uses_ansi_nulls` is already read in `table.go:111`).

**Fix:** read the two flags in each `moduleKind` query and emit
`SET ANSI_NULLS ON|OFF` / `GO` / `SET QUOTED_IDENTIFIER ON|OFF` / `GO` ahead of
the definition. **Live:** create a procedure under `SET QUOTED_IDENTIFIER OFF`
that uses a `"literal"`, script it, run the script, compare `sys.sql_modules`.

### Q7 — Statistics Properties "Script as CREATE" loses the filter and options — **Done**

**Done (2026-09-22), as planned.** gosmo `Scripter.ScriptStatistic[Context]`
(CREATE / DROP / DROP And CREATE; the DROP always guarded, since `DROP
STATISTICS` has no `IF EXISTS`) builds the CREATE with the existing
`buildCreateStatisticStatement`. An index's own statistic is refused with an
error naming the index. gossms: `NodeStatistic` joined `scriptables`
("Script Statistics as ▸" CREATE/DROP/DROP And CREATE/UPDATE STATISTICS), and
the Properties button calls the same generator. Checked in the built binary
against a filtered `NORECOMPUTE` statistic, both routes, and the refusal on
`PK_T`.

`internal/tui/statistics_props.go:230` hand-builds `CREATE STATISTICS … ON …
(cols)` in gossms instead of going through gosmo. It drops `WHERE <filter>`
(`Statistic.FilterDef` is on the struct), `NORECOMPUTE` and `INCREMENTAL = ON`,
so a filtered statistic scripts as an unfiltered one. Object DDL is otherwise
gosmo's job (`internal/showplan`'s missing-index suggestion is the other
exception, and has no existing object to be faithful to), and Object
Explorer's statistic node offers only `statisticUpdateVerb` (UPDATE
STATISTICS To), no CREATE.

**Fix:** add `Scripter.ScriptStatisticContext` to gosmo (CREATE / DROP / DROP
And CREATE, with the filter and both options), use it from the Properties
button, and add CREATE/DROP verbs for the statistic to `scripting.go`'s verb
table.

### Q8 — `alterModuleDefinition` gives up on a nested leading comment — **Done**

**Done (2026-09-22), as planned.** `leadingTriviaEnd` walks the leading
trivia with `scriptCodeSpans`; the regex now only matches the keyword after
it. `TestAlterModuleDefinition` gained the nested, mixed, unterminated and
quoted-first cases; the live module test's procedure opens with a nested
comment and is altered on the server.

`~/go/gosmo/scripter.go:486`: the `createKeyword` regex matches block
comments non-greedily, but T-SQL block comments nest. A definition opening
with `/* a /* b */ c */ CREATE PROCEDURE` is not recognised, and Script as
ALTER returns the CREATE unchanged, which fails with "already an object
named". Low impact (the fallback is safe, by design). Fix by scanning the
leading trivia with the same nesting-aware walk `scriptCodeSpans` already
implements in `script.go`, instead of a regex.

---

## Inconsistencies

### Q9 — Five ways to render a captured script — **Done**

**Done (2026-09-22).** gosmo: `ScriptCollector.Entries []ScriptEntry`
replaces `Statements []string`; `String()` is the one renderer (a
`-- on <server>` line opens each instance's run only when the capture spans
more than one instance); `Statements()` returns each entry standalone.
Departure from the fix below: `s.info.Name` alone would have labelled every
AG secondary's JOIN with the *primary*, because Script Changes issues those
through the primary's handle without connecting to the peer — so gosmo also
gained `WithScriptServer(ctx, name)`, which the two AG dialogs wrap the
JOIN/GRANT context in. There were six plain-join sites, not three:
`prop_dialog.go`'s `runScript` was the sixth; all now call `String()`.
`annotateScript` is gone from both AG dialogs, replaced by
`multiInstanceScript` (warning line + `String()`); their tests now drive
`createGroup`/`addReplica` under `WithScript` instead of feeding a list.
The endpoint dialog keeps its per-peer groups (it annotates each with
certificate notes) and reads `Statements()`.

`WithScript` output reaches a query window through five call sites that do
not agree:

- `explorer_object_actions.go:163`, `scripting.go:382`,
  `new_object_dialog.go:438` — `strings.Join(Statements, "\n\n")`, no `GO`
  (the half of Q3 that is gossms's);
- `new_ag_dialog.go:408`, `ag_add_replica_dialog.go:351` — each entry followed
  by `GO` and labelled with the instance it must run on, the label chosen by
  **position** in `Statements`, which both functions' comments admit is
  brittle (a MANUAL-seeding replica "shifts every label after it").

**Fix (gosmo, breaking):** make the collector record where each statement
would have run — `Entries []ScriptEntry{Server, Database, SQL string}`, filled
in `Server.execContext` / `Database.exec` from `s.info.Name` and `d.Name` — and
give it one renderer, `(*ScriptCollector).String()`, that emits `USE`/`GO`
correctly and a `-- on <server>` header whenever the server changes. All five
gossms sites then call it, and the two positional `annotateScript` functions
disappear.

### Q10 — the Connect dialog hand-rolls `animateUntil`

`internal/tui/connect_dialog.go:694` spins its own ticker goroutine; its
`attempt` channel is already `animateUntil`'s `done`. `ARCHITECTURE.md`
§ Async result delivery records it as "the one site that still hand-rolls the
same ticker". Replace with
`d.app.animateUntil("animating the connect dialog spinner",
connectSpinner.Period, attempt)` and delete that sentence.

### Q11 — `ServerConn.closed` is a plain bool read across goroutines

`internal/db/connection.go:89`/`:280`/`:295`. `closed` is written on the UI
goroutine and `IsOpen` has 19 callers; `peer.go`, which runs on loader
goroutines, carries two comments explaining why it must *not* use `IsOpen`
("closePeers writes closed … outside peerMu") and checks `Context().Err()`
instead. Not a live race today — it is a trap for the next caller. Make it an
`atomic.Bool` (simpler than deriving it from `ctx`, since tests build a bare
`&db.ServerConn{}` that must read as open), then simplify `peerLive` and the
two caveat comments.

### Q12 — gosmo mixes typed enums with runtime-checked strings — **Done**

**Done (2026-09-22).** Named string types with exported constants:
`UserAccess`, `FragmentationMode`, `QueryStoreState` / `…CaptureMode` /
`…CleanupMode` / `…WaitStatsMode`, `ChangeTrackingUnit`, `AvailabilityMode`,
`FailoverMode`, `SeedingMode`, `AllowConnections` (primary and secondary share
it; each role's allowlist checks its subset), `BackupPreference`,
`ClusterType`. The read-side fields that feed the setters are typed too, so a
value read off an object still round-trips; `AvailabilityGroup.Join` takes a
`ClusterType`. The allowlist maps stay as the validity check, since a
conversion from an arbitrary string still compiles; the setters still
upper-case. gossms converts at the select-widget boundary. Endpoint roles,
encryption and algorithms (`endpoint.go`) are the same pattern and were not
in this item's list — left as strings.


`RecoveryModel`, `CompatibilityLevel`, `CategoryClass`, `BackupAction` are
typed; `Database.SetUserAccess(mode string)`, the five
`AvailabilityReplica.Set*Mode(mode string)` methods,
`Table.FragmentationStats(mode string)`, and the Query Store, change-tracking,
cluster-type and backup-preference options are strings checked against an
allowlist map at run time (`userAccessModes`, `queryStoreCaptureModes`, …).
**Fix (breaking):** a named string type with exported constants for each, so
a typo is a compile error; the allowlist maps become the constants' validity
check. gossms's call sites change mechanically.

---

## gosmo API: simplifications that need the compatibility waiver

These re-open conventions `~/go/gosmo/CLAUDE.md` states as rules. They are
listed because the waiver changes the trade-off those rules were written
under, not because the rules were wrong at the time. Each is the author's
call; recommendations are given.

### Q13 — Drop the non-context method of every pair (~706 methods) — **Done**

**Done (2026-09-22), both passes.** 707 delegates deleted (plus `Connect`,
which was not a pure delegate: it applied `ConnectTimeout` to the whole call;
the one form now leaves that to ctx, since an interactive Entra sign-in in the
dial may outlast it) and `method_pair_wiring_test.go` with them. Every
`FooContext` renamed to `Foo` — the exported ones and the 17 unexported
`…Context` helpers — across gosmo, its examples and tests, and gossms (one
gossms test called a delegate; `roleWriter`'s interface methods renamed with
the gosmo ones). The Foo doc comment was folded into the surviving method's.
Done with a throwaway go/types rewriter, so every renamed identifier resolved
to a gosmo object; `DialContext` (go-mssqldb's `Dialer` interface) is the one
`…Context` method kept. gosmo's Markdown call snippets gained their `ctx`;
diagrams leave it out, as `ARCHITECTURE.md` § Maintaining now says.


706 exported methods are one-line `Foo(...)` delegates to
`FooContext(context.Background(), ...)` — about 40% of gosmo's 1,795 exported
functions and methods, ~3,500 lines, 0.0% covered, held in place only by
`method_pair_wiring_test.go`, which exists because a delegate wired to the
wrong sibling compiles and passes everything else. **gossms calls none of
them.** Every non-trivial Go library written since `context` landed takes
`ctx` first and has one form.

**Recommended:** delete the delegates and `method_pair_wiring_test.go`.
Optionally, in a second mechanical pass, rename `FooContext` → `Foo` (gossms
has ~1,229 call sites; `gofmt -r` / `gopls rename` per method, or a
throwaway `go/ast` rewriter). Doing only the first pass leaves the
`…Context` names, which is ugly but costs gossms nothing. `CLAUDE.md`
§ Conventions' method-pair bullet is rewritten either way.

### Q14 — The 134 `*Seq` iterators add nothing the slice methods don't — **Done**

**Done (2026-09-22), deleted** — `iter.go`, its two test files and
`examples/iterators`; `ARCHITECTURE.md` § No iterators records why and what a
streaming one would have to warn about. Done before Q15 rather than after:
`TestEveryCollectionMethodHasASeq` failed vacuously once Q13 renamed the
methods it scans for, and Q15 needed a green suite to verify against.


`~/go/gosmo/iter.go`'s own package comment says so: each iterator runs the
slice method to completion and then yields from the slice; breaking early
saves no query and no memory. They exist "for the call-site ergonomics of
range-over-func" — `for x := range slices.Values(xs)` after one error check
gives the same. With `iter_wiring_test.go` they are ~2,100 lines. Either
**delete them (recommended)**, or make them earn their place by actually
streaming (yielding from `rows.Next()`, holding the pinned connection for the
loop — which needs a new "never query inside the loop body" warning, since
`Database.query` pins a connection). Not both shapes under one name.

### Q15 — One generic list reader and one by-name reader — **Done**

**Done (2026-09-22).** `scanRows` and `foundRow` in `helpers.go`. `scanRows`
takes the query's `(rows, err)` straight through, so the query error, every
Scan and `rows.Err` share one message by construction; an empty `what`
returns errors bare, for the shared readers whose callers wrap. 113 list loops
and 41 by-name tails converted by a throwaway AST rewriter that only touched
the exact canonical shape (and found no site whose three messages differed).
~50 loops remain hand-rolled — grouping into maps, single-row aggregates, the
scripter's string builders — and ~20 not-found blocks with post-processing
after the read. With Q13 and Q14: gosmo non-test 43,344 → 37,572 lines; still
over 900: `query_store_reports.go`, `table.go`, `backup.go`,
`connection.go`, `scripter.go`. `livedb` suite green on SQL 2016 after each.


gosmo has **153** hand-written `rows.Next()` loops and **68**
`errors.Is(err, sql.ErrNoRows) → notFoundf` blocks, nearly all the same
twenty lines (`dupl` finds them as clone groups in `credential.go`,
`backup_device.go`, `service_broker*.go`, `user_defined_type.go`, …). Two
generic helpers — `listRows[T](rows, scan func(func(...any) error) (T, error),
wrap func(error) error) ([]T, error)` and a `lookupRow` counterpart that maps
`sql.ErrNoRows` to `notFoundf` — would remove on the order of a thousand
lines (estimated, ~10 per site) and make
`CLAUDE.md`'s "`rows.Err()` and every `rows.Scan` wrap with the *same* message"
rule true by construction instead of by review. Behaviour-preserving; do it
file by file with the existing `live_*` sweep (`TestLiveVersionSweep`) as the
check. Several files over the 900-line prompt (`database.go`, `table.go`,
`index.go`, `backup.go`) drop under it as a side effect of Q13 + Q15.

### Q16 — `RestoreOptions` encodes one tri-state in three fields — **Done**

**Done (2026-09-22), with Q4.** `Recovery RestoreRecovery`
(`RestoreRecoveryDefault`, `RestoreWithRecovery`, `RestoreWithNoRecovery`,
`RestoreWithStandBy`) plus `StandByFile`, required by standby and refused
with anything else; an unknown value is refused. Breaking: `NoRecovery` and
`StandBy` are gone.

`Recovery bool`, `NoRecovery bool`, `StandBy string`: `NoRecovery` silently
wins when both booleans are set (`backup.go:410`). Replace with
`Recovery RestoreRecovery` (`RecoveryDefault`, `WithRecovery`,
`WithNoRecovery`, `WithStandBy` + the undo path). Fold into Q4's change,
which touches the same struct.

### Q17 — (re-opens a decision) `CertificateByName` / `AsymmetricKeyByName` answering `(nil, nil)` — **Done**

**Done (2026-09-22).** Both now return an error wrapping `ErrNotFound`; the
scripter's own nil check went with it. The compiler could not flag the
callers that branched on `nil`, so every call site in both repos was read:
gossms's endpoint pipeline goes through `findCertificateIfAny` (absence → nil,
which also covers a certificate whose CREATE was only collected under
`WithScript`), the two property-page finders reword `ErrNotFound`. Verified
live by `TestLiveEndpointExchangeNamesNamedInstances`. `docs/decisions.md`'s
entry is rewritten; `TestLiveCertificateNotFoundIsNilNil` became
`…IsErrNotFound`.


`docs/decisions.md` § gosmo: deliberate API decisions keeps these two as the
only `*ByName` readers that answer absence with `(nil, nil)`, and gives one
reason: "making them error is a breaking change to a published contract".
This review's waiver removes that reason. If Q13 lands (itself breaking),
aligning them with `ErrNotFound` in the same release costs one release note
and removes the third convention `ErrNotFound`'s doc has to explain.
**Author's call** — listed only because the stated reason no longer holds;
the decisions entry would be rewritten, not deleted.

---

## Small cleanups (gossms)

### Q18 — test-only helpers living in production files

`deadcode ./cmd/gossms` reports functions only tests reach:
`query.formatGUID`, `formatValue`, `formatFloat` (each labelled "for
tests"), `db.Connect`, `sqlparse.isGoSeparatorLine`, `sqlparse.ScanPrefix`,
`tui.newCompletionInventory`, `controls.menuCascade.levelRect`,
`controls.startsInBlockComment`, `controls.xmlOpenBlock`,
`theme.SetPalette`, `widgets.SpinnerByName`, `config.UseTrackedQueries`. Move
the pure test helpers into `_test.go` files; delete `db.Connect` (every real
caller passes a ctx and a role); leave `StackedHistoryChart.Draw`, which
`docs/decisions.md` keeps. `Execute`'s `capture.readsCurrentDatabase()` test
is on a constant `planCaptureNone` and can go with its branch.

### Q19 — two small drawing duplicates

`planview/graph.go:209` `drawDoubleBox` is `core.DrawBox` with other runes —
give `core` a `DrawBoxWith(s, r, style, BoxRunes)` and a `DoubleBox` set.
`dashboard/common.go:120` `drawPanelTitle` and
`detail_browser_charts.go:212` `drawDetailChartTitle` are identical apart
from a constant, and the second's comment says it copies the first only
because of the package boundary — move one into `tuikit/charts`.

### Q20 — (hardening) bind the Entra tenant and client to a sealed secret

`internal/config/secret.go` `connectionAAD` binds "the fields that decide
where the password is sent". For `AuthEntraServicePrincipal` and
`AuthEntraPassword` the secret is sent to Entra, and `TenantID` / `ClientID`
decide which tenant and application it is presented to — neither is bound.
Low risk (Entra does not expose request secrets to a tenant's admin), but it
is the rule the comment states. Adding them is an `aadPrefix` bump to `v4:`
with the same read-old/write-new migration v3 used.

---

## Watch list — not items

- Over the ~900-line prompt: `internal/tui/detail_browser.go` (954),
  `internal/tui/activity_monitor.go` (930),
  `internal/tuikit/propsheet/rows.go` (921); in gosmo `query_store_reports.go`
  (1139), `table.go` (1113), `backup.go` (1025), `database.go` (1007),
  `index.go` (968), `connection.go` (958), `iter.go` (910). Per `CLAUDE.md`,
  split only when a change lands in the file anyway; Q13–Q15 remove most of
  gosmo's list.
- `docs/decisions.md` is 1,538 lines. It is doing its job (several proposals
  this review considered were already answered there), but entries whose code
  has since moved or been deleted should be pruned when touched, or the "check
  before re-raising" step stops being cheap.
- `docs/open-threads.md`'s N1 and N3 are unchanged by anything here.

## Suggested order

1. **Q1** — silent double execution; small, self-contained, gossms only.
2. **Q3 + Q9** together (gosmo collector rendering, then the five gossms
   sites).
3. **Q5**, **Q4 + Q16** (gosmo writes, then the restore dialog).
4. **Q2**, **Q6**, **Q7**, **Q8** — the scripter fidelity pass, gosmo first,
   each with a live round trip.
5. **Q13 → Q15 → Q14 → Q12 → Q17** — the breaking API pass, as one gosmo
   release with the gossms call sites updated in the same sitting (the
   `replace` directive makes that one build).
6. **Q10, Q11, Q18, Q19, Q20** — opportunistic.

Every gosmo change: `go vet -tags livedb ./...` in gosmo after any rename
(`~/go/gosmo/CLAUDE.md` § Build & verify), and `TestLiveVersionSweep` on the
2016 instance after any query change. Every gossms change that touches the UI
or the database: the tmux / live-server checks in `docs/testing.md`.
