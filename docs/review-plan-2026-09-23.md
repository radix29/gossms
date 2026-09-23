# Review plan — 2026-09-23

A review of both repositories (gossms ~194 kLOC incl. tests, gosmo ~77 kLOC incl.
tests) for bugs, inconsistencies, optimisations, simplifications and
architecture, done the day after the 2026-09-22 plan (deleted in `59d94ca` once
its items shipped). This is the plan, in priority order within each section.
An item that has since been implemented says so under its heading, marked
**Done**; everything else is still open. gosmo's backward compatibility is explicitly
waived for this review again, so the API items in § gosmo API are breaking by
design.

**Item IDs are `S1…S26`.** Local to this plan, chosen so they collide with none
of the `P*`, `Q*`, `R*` IDs earlier plans left in commit messages and none of
the permanent `B*`/`V*`/`N*` series `docs/open-threads.md` owns.

**Evidence markers.** *Probed* means reproduced during this review against a
real server — win10cli 17.0.1135.8 in throwaway databases
(`gossms_review_probe`, `gossms_review_probe2`, both dropped afterwards), or a
read-only / value-preserving statement on `win10cli\SQL2016`; nothing
pre-existing was changed. *Read* means found by reading the code, with the
failure scenario argued but not executed. Every *read* item names what a live
run must confirm before it is called fixed.

---

## What was checked and is clean

Re-measured so the next review does not have to:

- `gofmt -l`, `go vet ./...`, `go vet -tags livedb ./...` (gosmo) and
  `staticcheck ./...` are silent on both repositories; `go test -race
  -count=1 ./...` passes on both. `deadcode ./cmd/gossms` reports only the six
  entries `docs/decisions.md` already keeps on purpose.
- Every item the 2026-09-22 plan left open is done: Q10 (`animateUntil` in the
  Connect dialog), Q11 (`ServerConn.closed` is an `atomic.Bool`), Q18 (test-only
  helpers), Q19 (`core.BoxRunes`, `charts.DrawPanelTitle`), Q20 (`v4:` AAD).
- `internal/tuikit` still imports only tcell, tcell/color and displaywidth, and
  nothing under `internal/tui`.
- gosmo's quoting discipline holds: every `N'%s'` / `'%s'` slot in a statement
  template is fed through `escapeSingle`, `QuoteLiteral` or `nStringLiteral`
  (see S9 for the one class that is escaped but not *N*-prefixed).
- `sqltext.SplitBatches` (Q1's fix) was re-read against strings, bracket
  identifiers, nested comments and `GO n`: correct.
- No `TODO`/`FIXME` in either tree outside the vendored `whoisactive.sql`.
- Per-package coverage (own tests only): gossms `internal/tui` 62.4%,
  `tuikit/controls` 85.6%, `query` 89.1%, `db` 84.7%, `tui/gate` **0.0%** (S24);
  gosmo 58.1% without the `livedb` tests.

---

## Bugs

### S1 — gosmo leaves idle pooled sessions *inside* every database it read, and they block exclusive-access statements — **probed** — **Done**

**Done (2026-09-23), with S11.** `Server.ReleaseIdleConnections(ctx)` is
exported and called first by `DetachDatabase`, `RenameDatabase`,
`DropDatabase` (not in the list below, same failure without force),
`SetReadOnly`, `SetFileGroupReadOnly` and `SetDatabaseOption` for
`READ_COMMITTED_SNAPSHOT`; a no-op under `WithScript`. **Not the planned
`SetMaxIdleConns(0)`-and-restore:** a pool handed to `NewServer` carries its
owner's idle limit, which `database/sql` cannot read back, so it would be
restored to the wrong value. Instead each idle connection is taken and closed
through `Conn.Raw` returning `driver.ErrBadConn` — pool config untouched, no
mutex. A single idle connection never showed the bug (the statement reuses it,
and the driver's session reset leaves the database first); it takes two
overlapping reads, which gossms always has. Pins: `release_idle_test.go`
(closes every idle connection without dialling, idle limit unchanged);
`TestLiveExclusiveAccessDetachAfterRead` and `…FileGroupReadOnlyAfterRead`
on 17 — both fail on a deadline with the release stubbed out. Live in gossms:
Filegroups page read-only toggle applied in 0.66 s with a gossms session
sleeping in the database; Detach with the box unticked succeeded (no idle
session happened to be present at that moment — the livedb pin is the
deterministic proof). The Detach dialog's note no longer blames goSSMS's own
pooled connections.

`Database.query` / `queryRow` / `withConn` pin a pooled connection, `USE` the
database, run, and hand the connection back. go-mssqldb resets the session only
on the connection's *next* use, so until then the idle session sits in that
database holding its shared database lock — for up to `ConnMaxIdleTime` (5
minutes). Probed: after `d.Files` + `d.Tables`, `sys.dm_exec_sessions` showed
one idle session of the reading pool with `database_id` of the probe database,
and `DetachDatabase` from a second pool then failed Msg 3703 "currently in use".

gossms reads a database through the same pool immediately before each of these
writes, so it trips over its own sessions:

| Operation (gossms entry point) | Needs | With gossms's own idle session |
|---|---|---|
| Detach without "Drop connections" (`detach_database_dialog.go`; its prefetch calls `dbase.Files`) | exclusive | fails Msg 3703 whenever the pool hands the detach a different connection |
| Database Properties > Options > Read committed snapshot (`database_props_options.go`) | exclusive | **waits** — probed: blocked ~9 s behind one idle session, until it left. In gossms the Apply has no deadline, so it hangs up to the 5-minute idle eviction or until Cancel |
| Database Properties > Filegroups > Read-only (`database_props_filegroups.go:175`) | exclusive | **waits** — probed: `MODIFY FILEGROUP … READ_ONLY` blocked until the idle session left |
| Rename Database | exclusive | already handled — `explorer_object_ops.go` always forces, and its comment names this exact cause ("the tree's own metadata connections deny") |

**Fix (gosmo, library feature).** Give `Server` an unexported
`releaseIdle()` — `SetMaxIdleConns(0)` then restore the configured value, under
a mutex — and call it at the top of every exclusive-access write when not
`Scripting(ctx)`: `DetachDatabase`, `RenameDatabase`, `SetReadOnly`,
`SetFileGroupReadOnly`, and `SetDatabaseOption` for `READ_COMMITTED_SNAPSHOT`.
Export it as `Server.ReleaseIdleConnections()` for callers issuing their own.
Rejected alternative: appending `USE master` to every read's batch — it would
also stop gossms sessions appearing "in" a database in Activity Monitor, but a
trailing statement after a module-creating batch is unsafe on the `exec` path,
and an early `rows.Close` skips it anyway. Revisit only if the Activity Monitor
attribution is reported.

**Pin:** a `livedb` test in gosmo — read a database, then detach it without
`DropConnections` on the same pool, expecting success; and the same shape for
`SetFileGroupReadOnly`. **Live:** gossms Detach dialog with the box unticked,
and the Filegroups page toggle, both on a throwaway database.

### S2 — Server Properties cannot set any advanced option on a default server — **probed** — **Done**

**Done (2026-09-23), as planned.** `Server.ApplyConfiguration(ctx,
[]ConfigChange, ConfigApplyOptions{Override})` sends one batch; which options
are advanced is asked of the server (`is_advanced`), so the batch is the same
scripted and executed. No TRY/CATCH: a failing `sp_configure` does not stop
the rest, and the restore still runs. `SetValue`'s doc names the 15123 trap.
gossms: `applyConfigRows`/`configLookup` became `configChanges`, and all six
`sp_configure` pages (Memory, Processors incl. affinity, Security,
Connections, Database Settings incl. FILESTREAM, Advanced) send one batch per
page. Pins: `server_config_test.go` (batch shape, override, a change to `show
advanced options` itself, escaping) and
`TestSpConfigurePageSendsEveryEditInOneBatch`.
`TestLiveApplyConfiguration` passes on 13 and 14 (`show advanced options` 0 —
it also asserts the bare `SetValue` fails 15123) and on 17 (1, left at 1).
Live in gossms on `SQL2016`: Processors > Cost threshold 5 → 6 applied, `show
advanced options` still 0 afterwards; reset to 5 by hand.

`ConfigurationOption.SetValue` issues a bare `EXEC sp_configure N'name', v`.
With `show advanced options` = 0 — the installation default — every advanced
option fails **Msg 15123** "does not exist, or it may be an advanced option"
(probed on `win10cli\SQL2016` with the option's *current* value, changing
nothing). The main test server has `show advanced options` = 1, which is why
this never surfaced; `SQL2016` and `SQL2017` both have 0. Affected gossms
pages: Advanced (max degree of parallelism, cost threshold, …), Memory (min/max
server memory), Processors (max worker threads, affinity), Database Settings
(fill factor) — every write on them fails on a stock server.

**Fix (gosmo, breaking is fine).** Add `Server.ApplyConfiguration(ctx,
[]ConfigChange, ConfigApplyOptions{Override bool})` that, in **one batch**,
records `show advanced options`, turns it on if any change is advanced and it
was off, applies every change, runs one `RECONFIGURE [WITH OVERRIDE]`, and puts
`show advanced options` back — which is exactly what SSMS scripts. A change to
`show advanced options` itself inside the set wins over the restore. Keep
`SetValue` as the raw single-statement primitive but document the 15123 trap on
it. gossms: `applyConfigRows`/`configApply` and the Database Settings apply
call `ApplyConfiguration` once per page.

**Pin:** a `WithScript` test asserting the batch shape (advanced on → set →
reconfigure → restore); **live** on `SQL2016` against a throwaway-safe option
(set MAXDOP to its current value and back), confirming `show advanced options`
ends at 0.

### S3 — Changing a database's containment is a syntax error — **probed** — **Done**

**Done (2026-09-23), as planned.** `SetDatabaseOption` emits `SET CONTAINMENT
= …` for that option alone; `TestWithScriptSetDatabaseOptionContainmentTakesEquals`
pins it next to two bare-keyword options, and the gossms page test expects the
`=`. `TestLiveSetDatabaseOptionContainment` passes on 17, 14 and 13 — all three
have `contained database authentication` = 0, so it asserts Msg 12824 (the
statement parsed, the server refused); its PARTIAL → NONE read-back path has
not run live.

`SetDatabaseOption(DBOptContainment, "PARTIAL")` emits `ALTER DATABASE [x] SET
CONTAINMENT PARTIAL`; the grammar is `CONTAINMENT = { NONE | PARTIAL }`.
Probed: "Incorrect syntax near 'PARTIAL'". So Database Properties > Options >
Containment type always fails, and
`internal/tui/database_props_options_page_test.go:61` pins the broken text.

**Fix (gosmo):** render `SET CONTAINMENT = %s` for that option (it is the only
entry in `databaseOptionNames` that takes `=`); fix the gossms page test's
expectation; add the gosmo `WithScript` case. **Live:** set PARTIAL on a
throwaway database (needs `contained database authentication` = 1 — expect
that error, not a syntax one, where it is off).

### S4 — Index Properties > Included Columns silently rebuilds the index with different settings — **probed** — **Done**

**Done (2026-09-23), with S14 and S20.** `SetIncludedColumns` now builds its
statement with `rowstoreIndexCreate`, the scripter's own rowstore builder
(Script as CREATE uses the same one), `indexWithClause(…, "DROP_EXISTING =
ON")`, and `explicitDataSpaceClause`, which always names the filegroup or
partition scheme. It refuses anything but a rowstore nonclustered index
backing no constraint and not on a memory-optimized table, before sending
anything; `Index.IncludedColumnsSupported()` asks the same question for a
UI. **Beyond the plan:** a disabled index is re-disabled in the same batch,
since the rebuild enables it. Pins: `index_included_columns_test.go` (every
option and the ON clause; refusals by type; script and rebuild agree up to
WITH); `TestLiveSetIncludedColumnsKeepsEveryOption` — the probe's index on
`FG2`, before/after identical on 17 and on `win10cli\SQL2016` (where the
sequential-key option is absent and read as off). The probe's filter was
dropped from the live index: SQL Server refuses `IGNORE_DUP_KEY` on a
filtered index (10618). Live in gossms: ticking a column on the page and
Apply left fill factor, pad, IGNORE_DUP_KEY, page locks, NORECOMPUTE,
sequential key, PAGE compression and `FG2` all as they were.

`Index.SetIncludedColumns` reissues `CREATE [UNIQUE] {CLUSTERED|NONCLUSTERED}
INDEX … INCLUDE (…) [WHERE …] WITH (DROP_EXISTING = ON)` with nothing else, and
`DROP_EXISTING` builds the index from what the statement says. Probed on an
index created with `FILLFACTOR = 70, PAD_INDEX = ON, ALLOW_PAGE_LOCKS = OFF,
STATISTICS_NORECOMPUTE = ON, DATA_COMPRESSION = PAGE` on filegroup `FG2`:
afterwards fill factor 0, pad off, page locks on, norecompute off, and the
index **moved to PRIMARY** (compression survived). `IGNORE_DUP_KEY` is lost the
same way, which turns silently-ignored duplicate inserts into errors for the
application. The user asked to add one column.

There is also no type guard beyond columnstore: a clustered index, an XML or
spatial index, a hash index, and an index backing a PRIMARY KEY/UNIQUE
constraint are all offered the page (`index_props.go:126`) and fail at the
server — refused, at least, rather than silent.

**Fix.** gosmo: build the statement from the scripter's own pieces —
`indexColumnList`, `indexWithClause` (plus the two options S14 adds), and an
**explicit** ON clause (never `dataSpaceClause`'s default-filegroup omission:
with `DROP_EXISTING` an omitted ON means the *table's* data space, not the
default filegroup) — so SetIncludedColumns and Script as CREATE cannot drift.
Refuse anything but a rowstore nonclustered, non-constraint index with an error
naming the type. gossms: add the page only for those indexes (SSMS greys it for
the rest). **Pin:** the probe's before/after table as a `livedb` test; a unit
test on the builder asserting every option and the ON clause appear.

### S5 — withdrawn

The editor's tab expansion (`Editor.expandTabs`) is deliberate — author's call,
2026-09-23. See § Considered and not raised.

### S6 — Index Properties > Storage reports the row count ×3 for tables with LOB columns — **probed** — **Done**

**Done (2026-09-23), as planned.** The row count is a correlated subquery over
`sys.partitions`; the page sums keep the allocation-unit join.
`TestLiveIndexStorageInfoCountsRowsOncePerPartition` (1,000 rows across
IN_ROW, LOB and ROW_OVERFLOW units) passes on 17, 14 and 13, and
`TestLiveVersionSweep` is clean on 17 and 13.

`Index.StorageInfo`'s header query sums `p.rows` over a join of
`sys.partitions` with `sys.allocation_units`, so each partition's rows are
counted once per allocation unit. Probed: 1,000-row table with `nvarchar(max)`
and `varchar(8000)` columns → **3,000**. **Fix:** rows from `sys.partitions`
alone (a subquery), pages from the allocation-unit join. **Pin:** `livedb` with
a LOB table; unit test would not catch it.

### S7 — Delete Table with "Also drop the foreign keys" can drop the keys and keep the table — **read** — **Done**

**Done (2026-09-23), as planned.** `dropTableCascadeBatch`: one
`atomicBatch` holding `EXEC(N'…')` of the FK drop — its `DECLARE @sql` scoped
to the inner batch, so two captured drops do not collide — and the `DROP
TABLE`. Pins: the `WithScript` binding case in `script_test.go`;
`TestLiveDropTableCascadeIsAtomic` on 17 and 13 — the schema-bound view's
Msg 3729 leaves the FK in place, and with the view gone the cascade drops
the key and the table and keeps the referencing table.


`Database.DropTable(cascade=true)` drops every incoming foreign key in one
`exec` and the table in a second. When the `DROP TABLE` then fails — a
schema-bound view references the table (Msg 3729), a permission missing on the
second statement, a cancel — the foreign keys are gone for good and nothing
says so. **Fix:** one `atomicBatch` holding both (the FK drop as its dynamic
SQL with the table name inlined as a literal, since `atomicBatch` takes no
parameters). **Pin:** `livedb` — table referenced by a schema-bound view and an
FK; cascade drop fails; the FK must still exist.

### S8 — Forced drop, rename and detach still race for the single-user slot — **read** — **Done**

**Done (2026-09-23), not quite in the restore's shape.** `exclusiveBatch`
(drop, detach) and `renameExclusiveBatch`: `SET SINGLE_USER`, then the
operation gated on `@@ERROR` and inside TRY, with the `MULTI_USER` repair in
the CATCH before `THROW`; the rename releases the new name after success,
the old one in its CATCH. **Probed during the fix:** the planned trailing
`IF @closed = 1 … SET MULTI_USER` never runs after a failed `DROP DATABASE`
(Msg 3709, snapshot) or `sp_detach_db` — both abort the batch despite
severity 16 — which `TestLiveSingleUserForcedDropThatFailsLeavesTheDatabaseUsable`
and `TestLiveDetachThatFailsAfterSingleUserPutsTheDatabaseBack` caught; a
refused `ALTER` (5011 + 5069) is statement-level, so the gate works and both
messages reach the caller. The Go-side `restoreMultiUser` now runs only when
the batch was cut short (`batchCutShort`: not an `mssql.Error`, or severity
20+), so a refused ALTER never undoes a deliberate RESTRICTED_USER; after a
cut-short rename it asks `DB_ID` which name exists. MI: the KILL batch and the
`DROP`/`MODIFY NAME` are one statement; `killDatabaseSessions` went.
Renaming onto a system database name fails the batch at compile time (Msg
5058) before anything runs — harmless, noted on `renameExclusiveBatch`.
Pins: `TestWithScriptForcedExclusiveWritesAreOneStatement` (on-prem and MI),
the batch-shape assertions in `drop_rename_test.go` and
`database_attach_test.go`, `TestLiveForcedRenameAndDropAreOneBatch` (refused
rename onto a RESTRICTED_USER database leaves both as they were; a parked
open transaction is rolled back) plus every existing `TestLiveSingleUser*`,
`TestLiveDetach*`, `TestLiveExclusiveAccess*` and
`TestLiveRestoreCloseExistingConnections`, on 17 and 13 (restore and
exclusive-access on 17 only). gossms: `TestDetachWithEveryOptionOn` expects
one batch.


Q4 fixed this for Restore by putting `SET SINGLE_USER … ROLLBACK IMMEDIATE`,
the operation and the `MULTI_USER` repair in one batch. `DropDatabase`,
`RenameDatabase` and `DetachDatabase` still issue the `ALTER` and the operation
as separate `exec`s — possibly on different pooled connections — so a
reconnecting application takes the freed slot in between; `DropDatabase`'s own
comment lists that failure. The Managed Instance path (`killDatabaseSessions`
then `DROP`/`MODIFY NAME`) has the same gap. **Fix:** the restore's
`@closed`-flag batch shape for all three, and kill+operation in one batch on MI
(no transaction — neither `DROP DATABASE` nor `SET SINGLE_USER` may run in
one). **Pin:** `WithScript` tests asserting one captured statement; the MI form
is already live-verified for restore.

### S9 — `QuoteLiteral` produces a varchar literal, so non-ASCII file paths and names are mangled — **read** — **Done**

**Done (2026-09-23), as planned.** `QuoteLiteral` returns `N'…'`; the hand
`N`s in `killDatabaseSessionsBatch`, the restore's online check and gossms's
`sqlStringLiteral` went. Premise probed: `SELECT 'gosmo_Данные_日本'` on 17
returns `gosmo_??????_??`. Pins: `TestQuoteLiteralIsUnicode` (FILENAME in
CREATE/ADD FILE, the snapshot restore name), the FILENAME expectations in
five test files, `TestLiveNonASCIIFilePath` — Cyrillic and CJK data and log
file names round-trip through `sys.master_files` on 17 and 13.


`QuoteLiteral` is go-mssqldb's `TSQLQuoter.Value`, which renders `'…'`, not
`N'…'`. It builds `FILENAME =` in `CREATE DATABASE` / `ADD FILE`
(`database_files.go:155,179`), the snapshot files (`database_snapshot.go:232`)
and `RESTORE … FROM DATABASE_SNAPSHOT` (`:261`). Outside the database's code
page a character becomes `?` — a Cyrillic or CJK data path creates a file at a
different path, or fails. Backup devices already use `N'…'`, and
`killDatabaseSessionsBatch` prefixes `N` by hand. **Fix (breaking):**
`QuoteLiteral` returns `N'…'`; drop the hand-added `N`. **Pin:** unit test on a
non-Latin path; **live:** New Database with a `C:\Данные\` path on win10cli.

### S10 — Server addresses in the Azure Portal's `tcp:` form fail with an unhelpful error — **probed** — **Done**

**Done (2026-09-23), as planned.** `ParseServerAddress` drops a leading
`tcp:` (any case) itself, so every caller — gossms's Connect dialog and
`ResolveServer` included — gets the host; `dsnHost` refuses `np:`, `lpc:`
and `admin:` by name. A prefix followed only by digits is not one
(`admin:1433` is a host and port). `connection timeout` rounds up. Pins:
`TestParseServerAddress` rows, `TestBuildDSNAcceptsTheTCPPrefix` and
`TestBuildDSNRefusesOtherProtocolsByName` through `msdsn.Parse` and a masked
`ConnectionString`, `TestConnectTimeoutRoundsUpToWholeSeconds`. Live: gosmo
`Connect` with `tcp:win10cli.fritz.box,1433`; gossms's Connect dialog with the
same address previewed and connected. **Found, not fixed:** the driver's
`connection timeout` does not bound the TCP dial (its own 15 s `dial
timeout` does) — probed, a 500 ms `ConnectTimeout` to an unroutable address
failed after 15 s. Recorded in `~/go/gosmo/OPEN-THREADS.md`.


`ParseServerAddress` knows no protocol prefix, so `tcp:x.database.windows.net,1433`
— what the portal's ADO.NET string shows, and SSMS accepts — reaches `dsnHost`
with a colon in the host, is bracketed as an IPv6 literal, and
`ConnectionString` fails "cannot mask an unparseable DSN" (the Connect dialog's
preview and the connect itself). Same for `np:`, `lpc:`, `admin:`.
**Fix:** strip a leading `tcp:`; refuse the others by name ("named pipes are
not supported"). Also in the same file: `ConnectTimeout` is written as
`int(Seconds())`, so a sub-second value (probed: 500 ms) becomes `connection
timeout=0`, which the driver reads as *no* timeout — round up. **Pin:** add
both rows to the existing DSN tests, through `msdsn.Parse` per
`docs/testing.md`.

### S11 — Options needing exclusive access have no way to say how to get it — **probed** — **Done**

**Done (2026-09-23), with S1.** `gosmo.Termination` (`TerminationNone`,
`TerminationRollbackImmediate`) is a required parameter of
`SetDatabaseOption`, `SetReadOnly` and `SetFileGroupReadOnly`; an unknown
value is refused. **Probed during the fix:** `MODIFY FILEGROUP … WITH ROLLBACK
IMMEDIATE` parses and is ignored (waited ~20 s behind an open transaction,
then Msg 5070), so the filegroup form puts `killDatabaseSessionsBatch` ahead
of the ALTER in one batch — needs ALTER ANY CONNECTION. gossms: a generic
`propsheet.Form.SetApplyConfirm` / `PropertySheet.ApplyConfirmations`, asked by
`PropDialog.runApply` (not by Script Changes); the Options page registers it
for Read committed snapshot and applies that row with RollbackImmediate. New
Database passes `TerminationNone` and does not ask. The Filegroups page stays
at `TerminationNone` — the plan's gossms half named only RCSI. Pins:
`TestWithScriptTerminationClause`,
`TestWithScriptFileGroupReadOnlyRollbackImmediateKillsFirst`,
`TestLiveExclusiveAccessRCSIRollsBackAParkedSession` and
`…FileGroupRollsBackAParkedSession` on 17,
`TestSheetApplyConfirmationsOnlyFromDirtyPages`,
`TestPropDialogAsksBeforeApplyingAWarnedEdit`,
`TestReadCommittedSnapshotAsksBeforeClosingConnections`,
`TestNewDatabaseReadCommittedSnapshotTerminatesNothing`. Live in gossms on 17:
RCSI ON/OFF with a second session parked in an open transaction — the prompt
appears, No leaves the server and the edit untouched, Yes applies and the
parked transaction is rolled back.

Independent of S1: `READ_COMMITTED_SNAPSHOT`, `READ_ONLY`/`READ_WRITE` and
filegroup `READ_ONLY` wait indefinitely behind *any* other session. Probed on
17.0: `WITH NO_WAIT` still waited (~9 s), so only `ROLLBACK IMMEDIATE` makes
the statement finish. `SetUserAccess` and `SetOffline` already carry it.
**Fix (gosmo):** a `Termination` option (`TerminationNone`,
`TerminationRollbackImmediate`) on `SetDatabaseOption`, `SetReadOnly` and
`SetFileGroupReadOnly`. **gossms:** the Read committed snapshot row applies with
`RollbackImmediate` after a confirmation naming the consequence, like Rename
Database's `renameWarning`. **Live:** RCSI toggle with a second session parked
in the database.

### S12 — A panic on the UI goroutine discards every unsaved query — **read** — **Done**

**Done (2026-09-23).** `App.EmergencySave` (`internal/tui/emergency_save.go`)
writes each dirty query panel to `config.RecoveredDir()`
(`<config dir>/recovered/<yyyymmdd-hhmmss>-<title>.sql`, 0600, O_EXCL with a
`-N` suffix so nothing is overwritten), each panel under its own recover;
`cmd/gossms/main.go`'s `run` calls it from its recover and prints and logs
every path and every failure. Nil-safe and safe before `buildUI`. Pins:
`TestUIPanicInAPostedCallbackRecoversUnsavedQueries`,
`TestEmergencySaveNeverOverwrites`, `TestEmergencySaveOnAnUnbuiltApp`. Live: a
probe binary built with `-overlay` (a panic on F12) with two dirty panels and
one clean — terminal restored, both dirty panels' text recovered, the clean
one skipped, paths on stderr and in the log.


`safego`/`safegoRepair`/`fanOut` recover background panics, but the event loop,
every key/mouse handler, every draw and every `postAndWake` callback run on the
UI goroutine, whose only recovery is `cmd/gossms/main.go`'s `run`: it restores
the terminal and exits. `ARCHITECTURE.md` § Threading model names the cost
("along with unsaved query text"); nothing mitigates it. **Fix:** from `run`'s
recover, call a nil-safe `App.EmergencySave()` that writes every dirty query
panel's text to `<config dir>/recovered/<time>-<title>.sql` (its own recover
around each panel), and print the paths with the panic line. Resuming the loop
after a panic is rejected: state left mid-mutation is worse than a clean exit
with the text saved. **Pin:** a test that panics inside a posted callback with
a dirty panel and asserts the file exists.

### S13 — Two gossms instances overwrite each other's saved connections — **read** — **Done**

**Done (2026-09-23).** `Config.Save` re-reads `config.json` and replays this
process's own `AddOrUpdate`/`RemoveConnection` calls (`Config.ops`) onto it;
a setting is written from memory only if it differs from `Config.base` (what
this process loaded or last saved), otherwise the file's value is kept. After
a save `Connections` is the merged list; settings in memory are not replaced,
since a setting is in force only once Options applies it. `Connections` must
now change only through those two methods — the Connect dialog test fixture
that assigned it directly was moved to `AddOrUpdate`.
`TrackedQueries.Toggle` records a resolved add/remove that `Save` replays onto
the re-read file the same way. `loadOrCreateKey` creates the key with
`fileutil.CreateAtomic` (temp file + hard link, O_EXCL fallback), and a loser
reads the winner's key. Not closed: two saves in the same instant can still
interleave their read and write — no lock file. Pins:
`TestSaveKeepsAConnectionAnotherInstanceSaved`,
`TestSaveDoesNotResurrectAConnectionAnotherInstanceRemoved`,
`TestSaveKeepsASettingAnotherInstanceChanged`,
`TestSaveFailureKeepsPendingChanges`, `TestConcurrentFirstRunsAgreeOnOneKey`,
`TestTrackedSaveKeepsAnotherInstancesPins`,
`TestCreateAtomicNeverReplacesAnExistingFile` — each of the first six fails
against the old code. Live on a fresh scratch profile: two instances started
before either saved, each connected to win10cli with a different database and
Remember Password — both entries on disk under one key, and a third instance
connected from History with the first one's saved password.


`Config.Save` writes the whole in-memory config, which the process loaded at
start: instance A saves a new connection, instance B's next save (any connect)
deletes it. `tracked_queries.json` has the same shape. `loadOrCreateKey` has a
first-run race too: two processes each generate a key and the later rename
wins, leaving the loser's first save undecryptable. **Fix:** Save re-reads the
file and applies only this process's own add/update/remove operations before
the atomic write (a small operation log on `Config`); create the key with
link-if-absent so a loser adopts the winner's key. Low priority — only
multi-window users hit it.

### S14 — Script as CREATE still drops three index properties — **read** — **Done**

**Done (2026-09-23), with S4.** `Index` gained `StatisticsNoRecompute` (a
`LEFT JOIN sys.stats` on `stats_id = index_id`) and
`OptimizeForSequentialKey` (`colSince(…, SQLServer2019, …)`, inventory entry
and `index_list` golden file added); `indexWithClause` emits both, so the
PK/UNIQUE constraint path gets them too; the NCCI arm emits its `WHERE`.
**Found on the way, not in the plan:** every *read* nonclustered columnstore
index scripted as `ON [t] ()` — `sys.index_columns` marks each NCCI column
`is_included_column = 1`, so `KeyColumns` was empty and the arm listed only
those. The arm now lists key and included columns. Pins:
`TestScriptIndexByType` (filtered NCCI, NCCI as read),
`TestBuildTableScriptIndexOptions`, `TestLiveScriptFilteredColumnstoreKeepsItsFilter`
(replayed on 17 and 13); `TestLiveVersionSweep` clean on `win10cli\SQL2016`.

Not in `~/go/gosmo/OPEN-THREADS.md`'s fidelity list: a **filtered nonclustered
columnstore** index scripts without its `WHERE` (the columnstore arm of
`scriptIndex` never emits `FilterDefinition`), and `STATISTICS_NORECOMPUTE` and
`OPTIMIZE_FOR_SEQUENTIAL_KEY` (2019+, version-gated column) are neither read
into `Index` nor scripted. Each recreates a different index. **Fix:** read
`sys.stats.no_recompute` and `sys.indexes.optimize_for_sequential_key` (gated
per the version-gate table), emit both in `indexWithClause`, emit the NCCI
filter. S4 needs the same two fields. **Pin:** `buildTableScript` unit tests;
`TestLiveVersionSweep` on SQL2016 after the query change.

---

## gosmo API (breaking — waiver applies)

### S15 — `Index` is the only child type without a back-pointer — **Done**

**Done (2026-09-23).** `Index` carries its table (`Index.Table()`); every
index method lost its `t` parameter; `Fragmentation` takes a
`FragmentationMode`, `Rebuild` a typed `DataCompression`; `SetOptions` takes
`IndexSetOptions` with nil-means-unsent fields and `SetLockOptions` is gone.
`StorageInfo` and `Fragmentation` both resolve the index by table and index
name, so they work from the new `Table.IndexRef` handle (the 26th `Ref`
family) on a `TableRef`. `Statistic.Table()` added; the parent-accessor
wiring test now also requires `Table()` of a type holding `table *Table`
(mutation-checked). gossms's `findIndex` returns the index alone. Live:
`live_api_pass_test.go` on 17.x and 2016; Index Properties Options/Storage/
Fragmentation driven in tmux, the scripted SET + REBUILD run live.

Every `Index` write and read takes the table as a parameter —
`idx.Rebuild(ctx, t, …)`, thirteen methods in `index.go` — while `Statistic`
carries its table and every other child type its parent, and exposes it.
Passing the wrong `*Table` compiles and targets `ALTER INDEX [ix] ON
[wrong].[table]`. Alongside it: `Fragmentation(ctx, t, mode string)` beside the
typed `FragmentationMode` that `Table.FragmentationStats` takes;
`RebuildWithOptions(…, dataCompression string)` validated at runtime; and
`SetOptions`/`SetLockOptions` as two methods because `IGNORE_DUP_KEY` must not be
sent on a constraint index. **Change:** `Index.table` + `Table()`
(`parent_accessor_wiring_test.go` then enforces it), drop the `t` parameters,
type both parameters, and one `SetOptions(ctx, IndexSetOptions{IgnoreDupKey,
AllowRowLocks, AllowPageLocks *bool})` where nil leaves an option alone. Update
gossms's `index_props.go`, `key_props.go`, `scripting.go` call sites in the same
sitting. `Index.StorageInfo` should stop using `t.ObjectID` (zero on a
`TableRef`) where `Fragmentation` uses the name — one form for both.

### S16 — Eighteen `Foo`/`FooWithOptions` pairs — **Done**

**Done (2026-09-23).** One method per verb under the plain name:
Grant/Deny/Revoke at all five scopes take `PermissionOptions` last;
`ChangePassword` takes `ChangePasswordOptions`; `CreateStatistic` takes the
`CreateStatisticRequest`; `Rebuild` takes `IndexRebuildOptions` (zero = plain
REBUILD). `DatabasePermission` and `ServerPermission` are typed, as are the
permission-entry fields that read them back. The zero-options equivalence
test, now a tautology, was deleted; the legacy statement pins stay.

The same shape Q13 removed for `Foo`/`FooContext`: `Grant/Deny/Revoke` ×
{object, schema, column, database, server}`Permission`, `ChangePassword`,
`Rebuild`, `CreateStatistic`, each with a `…WithOptions` twin whose zero options
equal the plain form. **Change:** keep one method per verb under the plain name
taking the options struct; delete the twins. Also type the database/server
permission name (`permission string` today, `ObjectPermission` at object/schema
scope) the way Q12 typed the other string enums.

### S17 — Three ways to name a backup location — **Done**

**Done (2026-09-23).** `BackupOptions.Devices`/`RestoreOptions.Devices` are
`[]BackupTarget`, so a backup to or restore from a logical device is
`TO [dev]`; a zero target is refused. `VerifyBackup`, `BackupHeaders`,
`BackupFileList(target, setNumber)` are the only readers; the package-level
`BuildRestoreStatement` is gone. `execWithProgress` drains the stream and
reports every error message (live: "Cannot open backup device" and
"terminating abnormally" both reach the caller). Found on the way and fixed:
`Backup`/`Restore` with `Progress` set ran for real under `WithScript` — they
now take `exec`'s path there (mutation-checked).

`BackupTarget` (`DiskTarget`/`URLTarget`/`DeviceTarget`) exists for the reads,
next to string variants of each (`VerifyBackup`/`…From`,
`BackupHeaders`/`…From`, `BackupFileList`/`…ForSet`/`…ForSetFrom`), while
`BackupOptions.Devices` and `RestoreOptions.Devices` are `[]string` with URL
sniffing — so a backup to, or restore from, a **logical backup device** cannot
be expressed: it becomes `DISK = N'devicename'`, the exact bug `BackupTarget`'s
doc comment describes. And the package-level `BuildRestoreStatement` silently
builds the SINGLE_USER form a Managed Instance refuses, next to the
`Server.BuildRestoreStatement` that doesn't. **Change:** `Devices
[]BackupTarget`; one reader per operation taking a `BackupTarget` (and a set
number where relevant); delete the package-level restore builder. Also route
`execWithProgress`'s error through `withAllMessages` — the progress path
reports only the first error message where the plain path reports all.

### S18 — `ColumnDefinition` cannot say `datetime2(0)` — **Done**

**Done (2026-09-23).** `Precision, Scale *int`; `checkColumnDefinition` refuses
a decimal scale without a precision and a precision/scale on a type that takes
none. Live: `datetime2(0)`, `time(0)`, `decimal(10,0)` read back scale 0.

`colTypeSQL` treats `Scale == 0` as "unspecified", so `datetime2(0)`, `time(0)`
and `datetimeoffset(0)` become the 7-digit defaults and `decimal(p,0)` with
`Precision == 0` becomes `decimal(18,0)` — the class Q2 fixed in the scripter.
Not reachable from gossms (no New Table dialog); a library bug.
**Change:** `Precision, Scale *int` (nil = type default) or a `ScaleSet bool`;
the doc comment on `MaxLength` also omits binary/varbinary, which it covers.

### S19 — Small gosmo cleanups — **Done**

**Done (2026-09-23).** All four. The roles are read in the same query as the
user, one row per role through LEFT JOINs and grouped in Go, rather than a
second query — no extra round trip per database. `Index.UpdateStatistics`
takes a `samplePct` like `Statistic.Update` (0 = FULLSCAN); gossms passes 0.

- `Login.UnmapFromDatabase` calls `UserMappings`, one query per database on the
  server, to find the one mapping it already knows the database of — call
  `userMappingsIn` for that database.
- `UserMappings` splits role names on `", "`; a role named with a comma splits.
  Read roles in a second ordered query and group in Go (the file's own
  convention).
- `Index.UpdateStatistics` uses the server's default sample, `Statistic.Update(0)`
  is FULLSCAN — "update statistics" means two things depending on the node.
  Pick one (FULLSCAN, matching SSMS's default for the dialog) or name the sample.
- `likeEscape` builds a `strings.Replacer` per call; make it a package var.

---

## gossms simplifications and optimisations

### S20 — Gate the Included Columns page (with S4) — **Done**

**Done (2026-09-23).** The page stays listed — the index type is unknown
until the load reads it — but for an index `IncludedColumnsSupported`
refuses it shows why and has no apply (`TestIncludedColumnsOffersNothingForAnIndexThatCannotHaveThem`,
mutation-checked). Live: PK_T and a NCCI show the reason; a plain
nonclustered index shows the grid.

Show it only for rowstore nonclustered, non-constraint indexes; the others now
fail at the server on Apply.

### S21 — Statistics Properties builds a second UPDATE STATISTICS script — **Done**

**Done (2026-09-23).** Script as UPDATE runs `statisticUpdateVerb`'s
generator, so it collects the statement Update Statistics executes. Live on
win10cli: the button opened `UPDATE STATISTICS [dbo].[t] [st_b] WITH
FULLSCAN`.

`statistics_props.go:239` formats its own `UPDATE STATISTICS … WITH FULLSCAN`,
the "second copy" `scripting.go`'s `indexMaintenance` comment warns will drift
from gosmo's statement. Use `statisticUpdateVerb`.

### S22 — `ServerConn.Peer` ignores its `ctx` and dials once per concurrent caller — **Done**

**Done (2026-09-23).** One dial per instance (`peerDials`, `dbProbe`'s
shape): later callers wait bounded by their own ctx; the dialling caller's ctx
(and sc's) bounds the connect; a cancelled dial is abandoned, not cached as the
instance's failure, and a waiter still wanting an answer dials again. The stale
comment went with the rewrite. `connectPeer` is the test seam; four tests,
each mutation-checked. **Not run live:** ubusql1/ubusql2 were unreachable, so
`TestLivePeer*` still needs a run on the AG pair.

`Peer(ctx, server)` never reads `ctx`: both connect attempts use
`sc.Context()`, so a superseded load waits out up to two 30 s connects. And a
first expansion that asks for the same primary from three folders dials it three
times, keeping one. `capabilities.go`'s `dbProbe` already solves exactly this —
one in-flight probe per key, waiters bounded by their own context, abandoned
probes retried. Reuse that shape for peers. While there: the comment "Checked
via Context, not the closed flag (written on the UI goroutine)" is stale since
Q11 made `closed` atomic.

### S23 — Two per-keystroke costs proportional to the whole document — measure first

- Wrap mode re-segments every line of the document on every edit
  (`buildVisualLines` memoises on the document version, which every keystroke
  bumps), and `visualIndexForCursor` scans all visual rows. Cache segments per
  line and binary-search by row.
- IntelliSense's `nameMatch` lower-cases every candidate name per keystroke;
  keep a lower-cased copy in the inventory.

Both are like `docs/open-threads.md` N3: act on a measurement (a 20k-line
script in wrap mode; a 50k-object catalog), not on the shape.

### S24 — `internal/tui/gate` has no tests of its own logic — **Done**

**Done (2026-09-23).** `gate/allows_test.go`: table tests of `RightsAllow`
and `ObjectDenial` over hand-built capability sets, plus `Allows`/`Missing`
fail-open and `DeniedText`. Package coverage 0% → 57%; eight mutations of
`allows.go` each fail it.

0.0% statement coverage from its own package: `names_test.go` checks names, and
`allows.go`'s 400 lines are exercised only through `internal/tui`'s
`permission_gate_test.go`. The package was split out as a leaf; give it table
tests of `Allows`/`Missing`/`ObjectDenial` over hand-built
`gosmo.Capabilities`, so a gate regression fails in the package that owns it.

---

## Maintenance

### S25 — go-mssqldb v1.11.2 is out — **Done**

**Done (2026-09-23).** Both repos on v1.11.2. The driver diff is
`msdsn/conn_str.go` only (yes/no booleans, strict encryption kept in URL DSNs;
v1.11.1's response-drain change was reverted in v1.11.2); the
`mssql.ServerError` entry in `docs/decisions.md` still holds and now cites
v1.11.2. Live on win10cli: gosmo's livedb suite, gossms's `activity`, `query`,
`tui` and `TestLiveConnect*` all pass.

Both repos pin v1.11.0. Bump both, re-run the live suites, and re-check the
`docs/decisions.md` entry on `mssql.ServerError` (it cites v1.11.0 by name).

### S26 — File splits that ride on S4/S15 — **Done** (the S15 part)

**Done (2026-09-23).** gosmo `table.go` § Indexes moved to `index.go`, and
`CreateIndexRequest` with its validators to `index_create.go` (extracted
verbatim, checked byte-for-byte): `table.go` 814, `index.go` 840,
`index_create.go` 432. The rest of the watch list is unchanged.

Per `CLAUDE.md`, split only when a change lands anyway. S15 lands in gosmo's
`table.go` (1,020 lines; its index reads, lines ~362–670, belong beside the
index type) and `index.go` (872, pushed over by the move): split
`CreateIndexRequest` and its validators into `index_create.go`. Watch list,
unchanged: gosmo `query_store_reports.go` (1,046), `backup.go` (1,010 — S17
lands here), `connection.go` (950 — S10 lands here), `scripter.go` (992 after
S14; its index builders, ~385–600, are the natural split); `table.go` is 1,045
and `index.go` 881 after S4/S14;
gossms `detail_browser.go` (954), `activity_monitor.go` (930),
`propsheet/rows.go` (921). `docs/decisions.md` is 1,552 lines; prune entries
whose code has moved when touched.

---

## Considered and not raised

Checked against `docs/decisions.md` so the next review does not reinvent them:
restructuring `internal/tui` (closed), CI, `DatabaseByName` → `DatabaseRef` in
loaders, the Databases folder fan-out, result-set memory caps, the two search
dialogs' duplication, the two DDL-trigger files, `appendValue`'s `float32` arm.
The editor expanding tabs to spaces on load, paste and save — intended (S5, withdrawn). Grid copy writes cells tab-separated without quoting — SSMS parity. Results To
File truncates the target before the query runs — SSMS parity.

---

## Suggested order

1. **S3** and **S6** — one-line fixes to probed bugs, gosmo only.
2. **S2** — probed, blocks a whole dialog on stock servers; gosmo batch API,
   then the four gossms pages.
3. **S1 + S11** together — the idle-session release and the termination
   option, then the RCSI row's confirmation in gossms.
4. **S4 + S14 + S20** — the index-option fields, the shared builder, the page
   gate.
5. **S7, S8, S9, S10** — gosmo write-path correctness, each small.
6. **S12**, then **S13**.
7. **S15 → S16 → S17 → S18 → S19** as one breaking gosmo pass, gossms call
   sites updated in the same sitting (the `replace` directive makes that one
   build), then **S26**'s splits.
8. **S21, S22, S24, S25**; **S23** only with a measurement.

Every gosmo change: `go vet -tags livedb ./...` after any rename, and
`TestLiveVersionSweep` on `win10cli\SQL2016` after any catalog-query change
(S14, S6). Every gossms change touching the UI or the database: the tmux and
live-server checks in `docs/testing.md`. Items marked *read* are confirmed live
before being called fixed; S2 must be re-verified on an instance with `show
advanced options` = 0, which the main test server is not.
