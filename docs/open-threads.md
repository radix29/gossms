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
- **No instance has a case-sensitive *server* collation**, so nothing that
  compares server-scoped names under it has run live. gosmo's login-database
  retarget (`connection_retarget.go`: `retarget`, and Connect's `master`
  check) compares under `ServerInfo.Collation` since review step B5
  (2026-11-12), pinned only offline by
  `TestWriteOnLoginDatabaseFollowsServerCollation` and
  `TestRefusedDialOnCaseSensitiveServerMovesMASTER`. To check: connect with
  `Database: "sales"` to a `Latin1_General_CS_AS` server holding both `Sales`
  and `sales`, drop `Sales`, and see `InitialDatabase` stay `sales`. Format
  Document's keyword case (below) waits on the same instance.

## Picture viewer: what is left

- **File > Open of an image file** does not open it in a picture panel; only a
  result cell does. `openQueryFile` would sniff with `sniffPicture`.

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
  a varchar column whose code page has it.** INSERT BULK declares the columns
  without COLLATE, so the server reads char bytes in the *database's* default
  code page; in a non-UTF-8 database `Ж` cannot reach a Cyrillic varchar column
  of a Latin1 database. gosmo's `BulkInsert` checks and encodes every value and
  refuses it by name rather than storing `?`. **Blocked on go-mssqldb** (v1.11.2
  is the latest): it writes the column list itself (`makeDecl` in
  `bulkcopy.go`, never a COLLATE) and sends each column's wire type from the
  table's own metadata. Reopen when the driver lets a caller declare a column's
  collation (an upstream PR; a forked `replace` would not reach gosmo's
  dependents).
- **Multi-volume grouping and pre-2016-SP2 VLFs are unit-tested only**
  (phase 4) — every instance here has one volume, and none is older than
  2016 SP3.
- **`maint index-rebuild --online` → exit 11 is unit-tested only** (phase
  8, 8.m): every instance here is Developer or Enterprise, so the refusal on
  an edition without online index operations (gosmo
  `SupportsOnlineIndexOperations`) was never driven. An Express or Standard
  instance would close it.

## Theme: what is left

- **Nine palette/style pairs read below the WCAG floor** — found when review
  step S5 gave them named helpers; they were drawn inline before, so nothing
  changed visibly. `knownLowContrast` in tuikit's `theme/contrast_test.go`
  lists them (DialogAccent and DialogError in Default, DialogAccent/DialogDim/
  MenuDim in Light, MenuDim in MC, MenuDim/DialogDim/DialogError in MCRetro).
  Recolouring is a visible change: check each palette in the binary.

## tuikit: what is left

- **No tag yet — a release blocker.** tuikit became its own module
  (`github.com/radix29/tuikit`, `~/go/tuikit`) on 2026-11-12. `go.mod`
  requires the `v0.0.0-…` placeholder and builds only through
  `replace … => ../tuikit`; before a gossms tag, commit, tag and push tuikit,
  then bump the `require` and drop the `replace` (as for gosmo).
- **The Details pane's own cell menu stays open when a click lands in Object
  Explorer.** `DataGrid` keeps its context menu inside the grid, and
  `App.handleMouse` routes by position, so a right-click on a tree node opens
  the tree's menu beside the grid's still-open "Show Value" box. Breaks
  `core.MouseHandler`'s "any press outside an open menu dismisses it" one
  level up: the app does not offer the press to an `OverlayActive()` pane
  first. Seen 2026-11-12 driving tuikit-review step 4; predates it.
- **`widgets.InputField.HandleKey` breaks the `HandleKey` rule** that
  tuikit-review step 8 (IN1) applied to the grid, editor, tree and list: it
  tests its own focus and claims every key it binds, Left at column 0 and
  Right at the end included. Following the rule would hand a property-sheet
  text field's Left-at-start to the sheet (page list) and an edge arrow in a
  dialog to the dialog's own keys — a UX decision, not taken. tuikit's
  `ARCHITECTURE.md` § Widgets and grids names the exception.

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
