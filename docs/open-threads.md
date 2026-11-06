# Open threads

Open work only. Add an item when something is knowingly left undone; close it
by deleting it. Settled answers move to `docs/decisions.md`, not history here.

## Version support: the policy, and how it is held

Target: **SQL Server 2016 SP1 and later** (SP1 because `procedure.go`,
`scripter_module.go` and `internal/activity/block.go` emit `CREATE OR ALTER`).

Live on-premises instances: majors **13** (`win10cli\SQL2016` SP3), **14**
(`win10cli\SQL2017`), **17**. No 15/16 can run here (no Docker, 3 GB RAM), so
those are argued from catalog docs and pinned by tests. Azure SQL Managed
Instance is a fourth environment, not swept — `docs/decisions.md` § Azure SQL
Managed Instance.

**Standing check: `TestLiveVersionSweep`**
(`~/go/gosmo/live_versionsweep_test.go`) calls every gosmo read and reports
rejections. Run it on the *oldest* instance after any query change — a missing
column fails the whole read and `go test ./...` says nothing. Expected
refusals: every on-prem major refuses the seven Azure-only reads (six DMVs
plus database-scoped XEvent sessions); 13 also refuses six 2017+ reads with
`ErrUnsupportedVersion` (two Query Store wait reads, three external-library
reads, graph tables). Last run on 13: 2026-11-05, 439 calls, 0 failed.
Compare failures, not call totals (those vary with each instance's
logins/jobs). Zero failures proves nothing if a read was never reached — log
the `call` labels to confirm.

The per-column gate table is gosmo's (`~/go/gosmo/OPEN-THREADS.md` § Version
support); this section covers only the environment.

## Environment: what the instances can and cannot do

- **win10cli can never be an availability replica.** `IsHadrEnabled` and
  `IsClustered` are 0, `sys.dm_os_cluster_nodes` is empty, and Windows 10 Pro
  has no Failover Clustering; a Windows replica in a Linux AG is unsupported
  anyway (only a distributed AG crosses platforms). Don't add it to AAG1.
- **It can host everything an AG is built on** — endpoints, certificates, their
  principals and CONNECT grants need no HADR; gossms checks HADR only on the
  instance the endpoint dialog opens from. That lets Add Replica's Connect read
  a real endpoint off a third instance.
- **It is ubusql1's mirroring peer, deliberately.** win10cli: database master
  key, `win10cli_Cert`, imported `ubusql1_Cert`, `ubusql1_login`/`ubusql1_user`,
  endpoint `AGEP` STARTED on 5022; ubusql1 has the matching `win10cli_*`
  objects. Inert; rebuilding costs a live run. Leave it.
- **`xp_cmdshell` is on on win10cli and impossible on ubusql1** (Linux: Msg
  15392); file moves on ubusql1/2 go through ssh. Linux still lists it in
  `sys.configurations` at 0, so `xpCmdshellRow` tests `ServerInfo.Platform`.
- **A live AG test's teardown runs `DROP AVAILABILITY GROUP` on a real
  cluster** — `liveDropGroupEverywhere` runs before the create too, so
  `-liveag-create-name` drops groups. It fatally refuses any group whose cluster
  type isn't NONE.
- **`TestLiveAvailabilityGroupOperations` skips Drop and RemoveReplica** on
  AAG1; only add/remove database, suspend/resume, listener round trip and the
  failover refusal run there.

## Format Document: what is left

Settled design: `docs/decisions.md` § Format Document.

- **Keyword case on a case-sensitive *server* is untried.** Run live in a
  `Latin1_General_CS_AS` database only, where INT, VARCHAR, XML, SYSNAME and
  the built-ins resolve case-insensitively; `sysname` and the CLR types are
  left alone because a CS server collation was not available to check them.

## Command line: what is left

The phase plan and its as-built record are `todo/comm-params.md` § 15.

- **Windows never driven** (phases 0 and 1): cmd, PowerShell, Git Bash on
  win10cli — there is no interactive shell there from this machine. Only
  `GOOS=windows go vet`/`go build` have run.
- **`--application-intent readonly` routing unverified.** Through the AAG
  listener (`ubuaag`) both intents landed on the primary: `AAG1` has no
  read-only routing list, so the run proves the parameter is accepted, not
  that a read-only connection reaches a secondary.
- **Entra methods through the command line not run live** — no Azure instance
  is available; resolution and refusals are unit-tested only.
- **A bulk load cannot carry a character the database's code page lacks to
  a varchar column whose code page has it** (found 2026-10-30 in phase 6.i,
  live on 17 and 13). INSERT BULK declares the columns without COLLATE, so
  the server reads char bytes in the *database's* default code page and
  converts them to the column's. In a database whose default collation is
  not UTF-8, `Ж` cannot reach a Cyrillic varchar column of a Latin1
  database; gosmo's `BulkInsert` (which since 2026-11-04 checks and encodes
  every value) refuses it by name rather than storing `?`.
  **Blocked on go-mssqldb** (checked 2026-11-05 against v1.11.2, the latest;
  review plan F17, left as is by the author's call). Live on 17, a driver
  patched to append `COLLATE Cyrillic_General_CI_AS` to the column's INSERT
  BULK declaration stored `0xC6` as `Ж` (stock: `?`), so a per-column COLLATE
  plus encoding each value in its column's code page would fix it. But the
  driver writes that column list itself (`makeDecl` in its `bulkcopy.go`,
  never a COLLATE) and `mssql.CopyIn` takes only names; nor can a column be
  sent as nvarchar instead, since each column's wire type comes from the
  table's own `SELECT *` metadata. Reopen when the driver lets a caller
  declare a column's collation (an upstream PR; a forked `replace` would not
  reach gosmo's dependents).
- **Multi-volume grouping and pre-2016-SP2 VLFs are unit-tested only**
  (phase 4) — every instance here has one volume, and none is older than
  2016 SP3.
- **`maint index-rebuild --online` → exit 11 is unit-tested only** (phase
  8, 8.m): every instance here is Developer or Enterprise, so the refusal on
  an edition without online index operations (gosmo
  `SupportsOnlineIndexOperations`) was never driven. An Express or Standard
  instance would close it.
- **Only the `object`, `script`, `data`, `security`, `maint` and `monitor` groups are off the `Catalog` seam** (2026-11-04, the
  pilot of review Q1 option A). Past connecting and resolving the name,
  `object list/search/describe/definition/dependencies/indexes/stats` call
  gosmo directly, and their tests serve gosmo canned rows through gosmo's
  `gosmotest`; `Catalog` lost the 27 methods only they read, and
  `serverCatalog` about 150 lines of delegation. The cost, measured: the
  object fakes (about 500 lines) became a 624-line row server, since each
  read needs its full row (41 values for a column, 39 for an index); the
  trimmed `objectFake` stayed for the data and import tests until `data`
  moved too. Five mutations, three of them in gosmo's scanning, each
  failed an object test; under the fakes the gosmo ones passed every test.
  `script` followed (2026-11-05, plan step 16): `script object/db` call
  `internal/scripting` on the resolved database's gosmo Server, and
  `Catalog.Script` is gone. `scriptServer` (`script_server_test.go`, 127
  lines) adds the Scripter's reads on top of `objectServer` — most rows it
  needed (columns, indexes, keys, parameters) were already there — so the
  tests now check the real script text. Five gosmo scanning mutations
  (trigger order, the DROP's schema-bound and foreign-key notes, the table
  options row, the module row) each failed a script test; the old `Script`
  fake never reached gosmo. `script db` is tested with `--as drop` only: a
  CREATE's file, option and settings reads are left to gosmo's tests.
  `data` followed (2026-11-05, plan step 17): `data top/sample/count/
  profile/export/import` read the columns, the row count, the alias types
  and a collation's code page from gosmo; `Catalog.RowCount` and
  `CollationCodePage` are gone, and so is `objectFake`. Its tests run on
  `objectServer` (a `tableColumns` knob, a code-page rule); the row
  encoders moved to `internal/cli/clitest` so package `cmd`'s tests can
  build rows too (`catalogServer`, used by the dry-run harness's import
  case). Two mutations — gosmo's column scan with max_length and precision
  swapped, and `data count --approximate` on a `TableRef` — each failed a
  data test. `data import`'s writes (`TruncateTable`, `BulkInsert`) are
  still `Writer`'s.
  `security` followed (2026-11-05, plan step 17, "grants"): the principal
  reads and the grant read-back are shared with `security permissions`
  and `role-members`, so the whole group moved — 23 `Catalog` methods
  gone. `secServer` (`cmd/security_server_test.go`, 344 lines) replaces
  `secCatalog` and `permCatalog`; `permWriter` stays a `Writer` fake but
  changes `secServer`'s state, which the rules read back. Roles' member
  lists and role-members now come from one source, and every list read
  comes back in its ORDER BY — three tests had asserted a fake's
  insertion order. Seven gosmo scanning mutations (login flags, the
  schema row's normalization, role members, a user's login, server and
  column permission/state, a type's schema/name) each failed a security
  test.
  `maint` followed (2026-11-05, plan step 17): backup, backup-history,
  restore, index-fragmentation, index-rebuild, update-stats and checkdb
  read the media (HEADERONLY, FILELISTONLY), msdb's history, the
  instance's files, the indexes, fragmentation and statistics from gosmo
  past resolution — 13 `Catalog` methods gone, the tables' and views'
  by-name reads with them. `maintServer` (`cmd/maint_server_test.go`, 313
  lines, over `catalogServer`) replaces `backupCatalog`, `restoreCatalog`
  and `indexCatalog` (about 390 lines fewer in the three test files); the
  three writers stay `Writer` fakes that change its state. Its HAVING and
  ORDER BY are the server's: index-fragmentation drops what is under
  `--min-percent`, and backup-history `--all` reads the databases in name
  order — two expectations had relied on the fakes'. gosmotest's `Info`
  gained `HADREnabled` for the availability-group refusal. Seven gosmo
  scanning mutations (a header's start, history LSNs, LastBackups' log,
  database fragmentation pages, statistics rows sampled, an instance
  file's name, a file list's logical name) and one in gossms (a `TableRef`
  for the rebuild's table) each failed a maint test.
  Those three groups' writes followed (2026-11-05, plan step 17, the
  `Writer` step's first part): `security` grant/deny/revoke and role
  members, `maint` backup, restore (and --verify-only), index-rebuild,
  update-stats and checkdb, and `data import`'s TRUNCATE and bulk load
  call gosmo directly; 15 `Writer` methods are gone, and with them
  `permWriter`, `backupWriter`, `restoreWriter`, `indexWriter`,
  `loadWriter` and `importWriter`. The test servers take the statements
  instead (`applyWrite`), so the tests now see gosmo's real statement
  text; the dry-run harness compares a direct writer's dry run with what
  gosmo's statement observer reports the real run sent. gosmotest gained
  what this needed: BACKUP/RESTORE's progress-message stream and the bulk
  load (`INSERTBULK` rows, the flush's count), and gosmo
  `ScriptCollector.Add`. Seven gosmo mutations (server grant without `USE
  master`, a role's DROP sent as ADD, WITH GRANT OPTION dropped, BACKUP's
  CHECKSUM dropped, RESTORE's MOVE dropped, CHECKDB's ObjectId scan, the
  bulk load's KEEPNULLS) and four in gossms each failed a test; under the
  fakes the gosmo ones passed every test but the dry-run ones.
  `monitor` followed whole (2026-11-05, plan step 17, the first group of
  the `Writer` step's second part), with `plan cached`, which shares its
  VIEW SERVER STATE check: sessions, blocking, waits, top-queries,
  querystore and deadlocks read gosmo directly, and kill sends its KILL
  through it — 13 `Catalog` methods (sessions, waits, the plan cache, the
  six Query Store reports and metrics, the event-file read) and
  `Writer.KillSession` gone. `monitorServer` (`cmd/monitor_server_test.go`,
  443 lines, over `catalogServer`) replaces `monitorCatalog`, `killCatalog`
  and `killWriter`; the xevent tests, which had shared `monitorCatalog`,
  got their own `xeCatalog`. The reads answer in their ORDER BY and apply
  their TOP, HAVING and filters (`--include-system` now shows a system
  session; `--plan-database` keeps a plan by its database id; a regression
  floor drops a query with no baseline — the old regressed fixture had a
  row the server would never return). The Query Store tests assert the
  report's query (window, TOP, value expression, HAVING) where they had
  asserted the options handed to a fake, and the 2016 refusals come from
  gosmo's own version gates (`Info.ProductVersion` 13). Nine gosmo
  scanning mutations (a session's counters and its user-process flag, wait
  time, a cached statement's totals and handles, a regressed row's
  baseline, a wait category's value, the event-file filter, an event
  session's running flag) and four in gossms each failed a test.
  `server` info, memory, config and traceflag followed (2026-11-06, plan
  step 17): they read and write (`sp_configure`, DBCC TRACEON/OFF) through
  gosmo directly; seven `Catalog` methods (processor, start time, memory
  stats, clerks, performance counters, configurations, trace flags) and
  `Writer.ApplyConfiguration`/`SetTraceFlag` gone. `instanceServer`
  (`cmd/server_state_test.go`, over `catalogServer`) replaces
  `serverInfoCatalog`, `configWriter`, `traceFlagCatalog` and
  `traceFlagWriter`; its writes change its options and flags from the
  T-SQL. Option defaults are now gosmo's own table's (the fake's capitalised
  `Backup checksum default` had one the real, lower-case name has; its
  reset case is `Backup-Compression`). `server log` and `server volumes`
  stay on `Catalog`. Eight gosmo scanning/statement mutations and five in
  gossms each failed a test (one boundary survivor got its test).
  `xevent` sessions and capture followed (2026-11-06, plan step 17): the
  session reads, status, ring_buffer, and the CREATE/START/STOP/DROP go
  through `sc.Server.XEvents()` (`Database.XEvents()` on Azure SQL
  Database); five `Catalog` methods (`HasViewServerState`,
  `EventSessions`, `EventSessionStatus`, `EventSessionByName`,
  `ReadRingBuffer`) and the four event-session `Writer` methods gone.
  `xeServer` (`cmd/xevent_server_test.go`, over `catalogServer`) replaces
  `xeCatalog`, `captureCatalog` and `captureWriter`; its writes create,
  start, stop and drop sessions as the server would, and its `steps` are
  what a capture's cleanup is judged by. `clitest.EventXML` now carries
  actions, a field's `<text>` and `event_sequence`. Eleven mutations (five
  gosmo, six gossms) each failed a test.
  `db` create/drop/set/scoped-config and `job` list/show/history/start/stop
  moved the same way (`dbServer`, `jobServer`); `Writer` is gone entirely.
  Still on the seam: the shared reads in `Catalog` (resolution, the
  databases, server info, availability groups, the log and volume reads),
  which the groups not yet moved still read through. Whether to move those
  is the author's call. `internal/tui/fakedb_test.go` is gone (fix plan step 18): the
  `fakeResponse` fixture DSL and capability builders live in
  `gosmotest_fixtures_test.go`, served by `gosmotest` through
  `gosmotest_bridge_test.go`; the form helpers in `form_helpers_test.go`.

## Fix order

Outstanding work by priority. Each item points to where
the reasoning lives. **IDs are permanent, never reused or renumbered** (commit
messages cite them); a new item takes the next unused number and is inserted by
priority. **Maintained on request only** — don't regenerate it in ordinary
work; close an item by deleting it when fixed.

### Verification gaps

- **V2 — Azure Extended Events not run end to end.** Azure SQL Database's database-scoped sessions
  (`xeScope`) are pinned by fake-driver tests only — no Azure SQL Database is
  available. On the Managed Instance a blob `event_file` was created and
  refused its START (fake SAS, 25602), and a blob URL pattern merged to zero
  events; writing, watching and reading a blob with a real storage account
  and SAS, and whether the wildcard pattern gosmo builds lists a container's
  rollover blobs, are unrun.
- **V3 — Managed Instance low-privilege Extended Events run unfinished**
  (t-qmi-01 refuses every login, `testgo` too, Msg 40532). MI answers all nine
  granular event-session names through `HAS_PERMS_BY_NAME` (not NULL as on
  13–16), so the `gate.EventSession*` `Alt`s decide there.
  **Still to run:** `xeReadsOnlyByPattern` live (Watch Live Data, View
  Target Data, Merge on `system_health` — tests only so far);
  Properties read-only and browsable; the Profiler refused; then
  `gossms_w6_alt` (Profiler creates and watches, Properties editable) and
  `gossms_w6_gran` (granular CREATE/ENABLE/DISABLE only: Profiler allowed,
  Delete and Properties' Events greyed). **Left on t-qmi-01 to drop:** logins
  `gossms_w6_vss`, `gossms_w6_alt`, `gossms_w6_gran` (password
  `W6-inSecure123!`), session `gossms_test_xe_w6`. Also: whether MI accepts
  `MAX_DURATION` on an event session — gossms's Advanced page hides it off-box
  (`major >= 17 && !azure`, `xevent_session_advanced.go`) while gosmo reads
  `max_duration` there; the answer becomes one gosmo capability gossms asks.

- **V4 — Resource Governor and Database Mail unverified on Managed
  Instance** (t-qmi-01 refuses logins). Both
  shipped shown and ungated on EngineEdition 8, pinned by fake-driver tests
  only (`resourceGovernorHidden`, `databaseMailHidden` in `edition_gate.go`).
  To run when MI is back: RG catalog/DMV reads and `CREATE RESOURCE POOL` /
  `ALTER RESOURCE GOVERNOR` rights; Database Mail `sysmail_*` reads, writes and
  a test send; the gosmo version sweep of every new read; the tmux live pass
  for both. If MI refuses RG DDL, add an `edition_gate.go` entry. Also SQL
  Server Agent Properties ▸ Alert System: whether MI's `xp_instance_regread`/
  `sp_set_sqlagent_properties` answer for Agent's mail profile at all (MI's
  Agent is documented to use the profile named
  `AzureManagedInstance_dbmail_profile`). And gosmo's
  `killDatabaseSessionsBatch` (forced drop/rename, restore closing
  connections): live on 17 for the DATABASE-lock wait
  (`TestLiveKillDatabaseSessionsLockWait`); the MI run itself is still open.
  And gosmo's
  `Database.CatalogCollation` (what gossms compares names inside a database
  under, `databaseCollation`): live on 13/14/17 with a contained `_CS_`
  database, never on MI, nor on an Azure SQL Database created `WITH
  CATALOG_COLLATION` (none in the estate) — `TestLiveCatalogCollation` and
  `TestLiveGatedColumnsMatchTheCatalog` on MI.
- **V5 — Resource Governor, Database Mail and backup compression edition
  gates never met a real refusal.** Every
  instance in the estate is Developer, so Resource Governor's rule
  (Enterprise/Developer on 13–16, plus Standard on 17 —
  `resourceGovernorSupported`) ships from documentation, pinned by fake-driver
  tests; Database Mail on Express (procs present, no `DatabaseMail.exe`, so a
  test mail should sit `unsent` until the dialog's 30 s wait gives up) is
  unprobed. Back Up's Compression dropdown is pinned to the server default
  and greyed on Express and Web (`ServerInfo.SupportsBackupCompression`, Msg
  1844 from the edition matrix, not seen live). Run all three when a Standard,
  Web or Express instance is available.
- **V6 — Live Query Statistics never run on Azure.** Driven on 13, 14 and 17
  only (t-qmi-01 refuses the login, Msg 40532). On MI, run a long query with
  Live on (as `testgo`, and as a login without `VIEW SERVER STATE` for the
  note); Azure SQL Database's `VIEW DATABASE STATE` path is untested for want
  of one. Activity Monitor's **Show Live Execution Plan** is likewise unrun
  there: MI should behave as 2019+ (lightweight profiling on, so an unprofiled
  query shows).
- **V8 — Full-Text unverified outside 17 and 14.** Reads, folders,
  properties, writes, New dialogs and the end-to-end drive (create → populate
  → `CONTAINS` → alter → drop) ran on 17 (rows) and 14; win10cli\SQL2016 has
  the component not installed (empty path and the not-installed row only,
  writes skipped); the cascade ran on 17 only. Never run: Azure SQL Database,
  MI (Msg 40532), Linux (`mssql-server-fts`). Unknown: the `index_version`
  gate's floor (2025; absent on 13/14, present on 17, no 2019/2022 instance);
  `FULLTEXTSERVICEPROPERTY`/`sys.fulltext_document_types` on Azure SQL
  Database (assumed); whether its `HAS_PERMS_BY_NAME` accepts the class words
  for 23/29/31 (a rejection would fail the whole database probe, not just
  these rows). `STATISTICAL_SEMANTICS` ran on 17 only.
