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
refusals: every on-prem major refuses the six Azure-only DMV reads; 13 also
refuses six 2017+ reads with `ErrUnsupportedVersion` (two Query Store wait
reads, three external-library reads, graph tables). Compare failures, not call
totals (those vary with each instance's logins/jobs). Zero failures proves
nothing if a read was never reached — log the `call` labels to confirm.

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

- **Additional SQL styles and a picker.** Only Standard is registered. The
  registry is in place (`format.Styles`/`StyleByID`, unknown or empty ID →
  first style), and the rule tables sit in `sql_rules.go` so a second style
  is a table plus switches, not a second engine. Lands with it, not before:
  Edit > Format Document becomes a cascade (`controls/menu_cascade.go`) with the
  default style marked, and `config.SQLFormatStyle` plus a Tools > Options
  dropdown.
- **Keyword case on a case-sensitive *server* is untried.** Run live in a
  `Latin1_General_CS_AS` database only, where INT, VARCHAR, XML, SYSNAME and
  the built-ins resolve case-insensitively; `sysname` and the CLR types are
  left alone because a CS server collation was not available to check them.

## Group by Schema: what is left

- **Only the five main families group** (deferred scope). Tables (the folder's own user tables), Views, Synonyms, Stored
  Procedures and Functions; the System/File/External/Graph Tables
  sub-folders, System Views/Procedures/Functions, Sequences, Types and every
  other schema-scoped family stay flat. Extending it is one entry in
  `groupedFolderTypes` (`tree_node_kinds.go`) plus a Details check
  (`detail_browser_group_schema.go`) per family.

## Security: what is left

- **A login with no right on an application role sees the folder empty**
  (catalog visibility).

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
- **No Generate Scripts wizard** (phase 6, D7): `script db` is the
  database's own CREATE, not its objects. Scripting every object in
  dependency order (`--objects`, `--include-permissions`) needs
  dependency-ordered scripting in gosmo first — SCHEMABINDING, cycles — a
  feature the size of a phase. Neither the TUI nor the command line has it.
- **A bulk load cannot carry a character the database's code page lacks to
  a varchar column whose code page has it** (found 2026-10-30 in phase 6.i,
  live on 17 and 13). INSERT BULK declares the columns without COLLATE, so
  the server reads char bytes in the *database's* default code page and
  converts them to the column's. In a database whose default collation is
  not UTF-8, `Ж` cannot reach a Cyrillic varchar column of a Latin1
  database; gosmo's `BulkInsert` (which since 2026-11-04 checks and encodes
  every value) refuses it by name rather than storing `?`.
- **Multi-volume grouping and pre-2016-SP2 VLFs are unit-tested only**
  (phase 4) — every instance here has one volume, and none is older than
  2016 SP3.
- **`maint index-rebuild --online` → exit 11 is unit-tested only** (phase
  8, 8.m): every instance here is Developer or Enterprise, so the refusal on
  an edition without online index operations (gosmo
  `SupportsOnlineIndexOperations`) was never driven. An Express or Standard
  instance would close it.
- **The audit log is never rotated** (phase 8, D10): `audit.jsonl` grows
  one line per write or refusal for ever. Deferred by decision; a size cap
  with one `.1` generation, taken under the same lock, is the likely form.
- **Agent alerts and operators still need sysadmin, or SQLAgentOperatorRole**
  (2026-11-05, left out of the review plan's F1, which moved the job, step,
  history and schedule reads onto msdb's procedures for the Agent roles).
  gosmo's `Alerts`, `EventAlerts`, `Operators` and the job/operator
  notification reads SELECT `sysalerts`, `sysoperators` and
  `sysnotifications`, which msdb grants SQLAgentOperatorRole only; a Reader
  or User member gets the Access denied placeholder on the Agent node's
  Alerts and Operators folders and on Job Properties' Alerts and
  Notifications pages (measured as Reader on 17). msdb grants
  SQLAgentUserRole `sp_help_operator` and `sp_get_job_alerts`, so the same
  `agentRead` route would cover operators and a job's alerts; the alert list
  itself has no procedure below Operator. SSMS hides those folders from
  such a login.

- **Only the `object` group is off the `Catalog` seam** (2026-11-04, the
  pilot of review Q1 option A). Past connecting and resolving the name,
  `object list/search/describe/definition/dependencies/indexes/stats` call
  gosmo directly, and their tests serve gosmo canned rows through gosmo's
  `gosmotest`; `Catalog` lost the 27 methods only they read, and
  `serverCatalog` about 150 lines of delegation. The cost, measured: the
  object fakes (about 500 lines) became a 624-line row server, since each
  read needs its full row (41 values for a column, 39 for an index); the
  trimmed `objectFake` (`catalog_fake_test.go`) stays for the data, script
  and import tests. Five mutations, three of them in gosmo's scanning, each
  failed an object test; under the fakes the gosmo ones passed every test.
  Still on the seam: everything else — the shared reads in `Catalog`
  (resolution, tables' and views' children for data, grants and maint
  index), and all of `Writer`. Whether to move the rest group by group is the
  author's call. `internal/tui/fakedb_test.go` is a second, older scripted
  driver of the same kind; it could move onto `gosmotest` too.

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
