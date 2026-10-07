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

## Fix order

Outstanding work by priority within each subsection. Each item points to where
the reasoning lives. **IDs are permanent, never reused or renumbered** (commit
messages cite them); a new item takes the next unused number and is inserted by
priority. **Maintained on request only** — don't regenerate it in ordinary
work; close an item by deleting it when fixed.

### Bugs and suspected defects

- **B19 — The main toolbar overdraws the menu bar on a narrow terminal.**
  `controls.Toolbar.SetBounds` right-aligns its buttons with no left limit,
  so once they are wider than the row beside the menu labels they paint over
  them (Tools and Help vanish). Below ~100 columns before Phase 5 W6; the
  Include Live Query Statistics button (`Live[-OFF]`, 12 columns) moved that
  to ~112. Likely fix: the toolbar takes the menu labels' right edge as its
  left limit and drops buttons that don't fit, as panel toolbars do
  (`docs/ui-rules.md` § Panels, toolbars and grid hosts) — which end to drop
  is a call to make.
- **B20 — F1 help clips its long lines.** `HelpDialog` is a fixed 62×28
  modal (`NewHelpDialog`) with no horizontal scroll, so a line over 57
  characters loses its tail — 67 lines do as of Phase 5 W7 (Plan Compare's,
  the Shift+click mouse line, …). Wrap them to 57, or size the dialog to the
  screen as wider dialogs do.
- **B21 — Rows holding Arabic combining marks draw one column wide under
  tmux.** Seen in Phase 5 W15 on a stoplist made `FROM SYSTEM STOPLIST`:
  words with a shadda (`إنّ`) push the grid's — and the dialog's — right
  border one column right on that row only. `core.DisplayWidth` is
  grapheme-aware (`displaywidth`), so the suspect is how tcell emits the
  combining mark or how tmux places it, not the grid's arithmetic; not
  diagnosed, and not tried in another terminal.

### Verification gaps

- **V2 — Azure Extended Events not run end to end.** Azure SQL Database's database-scoped sessions
  (`xeScope`) are pinned by fake-driver tests only — no Azure SQL Database is
  available. On the Managed Instance a blob `event_file` was created and
  refused its START (fake SAS, 25602), and a blob URL pattern merged to zero
  events; writing, watching and reading a blob with a real storage account
  and SAS, and whether the wildcard pattern gosmo builds lists a container's
  rollover blobs, are unrun.
- **V3 — Managed Instance low-privilege Extended Events run unfinished**
  (t-qmi-01 has refused every login, `testgo` too, Msg 40532, since
  2026-09-30). Verified so far: as a `VIEW SERVER STATE` login, Sessions
  listed, New/Start/Stop/Delete greyed "needs ALTER ANY EVENT SESSION"; MI
  answers all nine granular event-session names through `HAS_PERMS_BY_NAME`
  (not NULL as on 13–16), so the `gate.EventSession*` `Alt`s decide there.
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
  Instance** (t-qmi-01 unavailable since 2026-09-30). Both
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
  connections): its wait now covers sessions killed for a DATABASE lock held
  from another context (unit-tested only) — hold one from a second
  session's `USE master` + a cross-database open transaction, force-drop a
  throwaway database, expect no Msg 3702. And gosmo's
  `Database.CatalogCollation` (what gossms compares names inside a database
  under, `databaseCollation`): live on 13/14/17 with a contained `_CS_`
  database, never on MI, nor on an Azure SQL Database created `WITH
  CATALOG_COLLATION` (none in the estate) — `TestLiveCatalogCollation` and
  `TestLiveGatedColumnsMatchTheCatalog` on MI.
- **V5 — Resource Governor and Database Mail edition gates never met a real
  refusal.** Every
  instance in the estate is Developer, so Resource Governor's rule
  (Enterprise/Developer on 13–16, plus Standard on 17 —
  `resourceGovernorSupported`) ships from documentation, pinned by fake-driver
  tests; Database Mail on Express (procs present, no `DatabaseMail.exe`, so a
  test mail should sit `unsent` until the dialog's 30 s wait gives up) is
  unprobed. Run both when a Standard or Express instance is available.
- **V6 — Live Query Statistics never run on Azure.** Phase 5 W3/W7 drove it
  on 13, 14 and 17 only: t-qmi-01 refused the login (40532) on both
  2026-10-06 attempts. On MI, run a long query with Live on (as `testgo`, and
  as a login without `VIEW SERVER STATE` for the note); Azure SQL Database's
  `VIEW DATABASE STATE` path is untested for want of one. W8's Activity
  Monitor **Show Live Execution Plan** is likewise unrun there: MI should
  behave as 2019+ (lightweight profiling on, so an unprofiled query shows).
- **V7 — Replication reads (gosmo `replication*.go`) met real rows on 17
  only.** The fixture is on win10cli (2025). win10cli\SQL2016 has
  **replication components not installed** (`sp_addpullsubscription` fails
  Msg 21028), so there only the subscriber-side transactional read ran, against
  tables made by `sp_MScreate_sub_tables` in a throwaway database; the
  publisher-side reads (`syspublications`, `sysarticles`, `syssubscriptions`)
  and every merge read are unrun below 17. Linux (ubusql1) and 2016 ran the
  not-configured path only; MI not at all. Every column read predates 2016,
  so no gate was added — run `TestLiveReplication*` wherever a publisher can
  be set up. W11's Object Explorer Replication folder is likewise shown on MI
  and undriven there. W13's Replication Monitor (gosmo
  `replication_monitor.go`, `sp_replmonitor*`/`sp_MSenum_*`) likewise ran
  against the 2025 fixture only; on 2016/2017 only the not-a-distributor path
  (`MonitorPublishers` empty, the panel's not-configured note). Its
  remote-distributor note is unit-tested only — no instance here uses another
  as its distributor.
- **V8 — Full-Text reads (gosmo `fulltext.go`, W14) met real rows on 17 and
  14 only.** win10cli\SQL2016 has the component not installed, so it ran the
  empty path; Azure SQL Database, MI and Linux (`mssql-server-fts`) not at
  all. The one gate, `sys.fulltext_indexes.index_version` at 2025, is checked
  absent on 13/14 and present on 17; no 2019/2022 instance exists to confirm
  the floor. `FULLTEXTSERVICEPROPERTY`/`sys.fulltext_document_types` on Azure
  SQL Database are assumed, not seen. W15's Object Explorer folders, Details
  and read-only Properties were driven on 17 (rows) and 13 (the not-installed
  row) only; a running population's row on the index General page is
  unit-tested, never seen live (the fixture's populations finish at once).
  W16's writes and scripts ran live on 17 and 14 only (skipped on 13, no
  component); PAUSE/RESUME POPULATION were sent only to an idle index, where
  the server's answer is not asserted — a paused population was never seen.

### Nice to have
