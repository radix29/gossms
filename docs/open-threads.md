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

### Verification gaps

- **V2 — Azure Extended Events not run end to end.** Azure SQL Database's database-scoped sessions
  (`xeScope`) are pinned by fake-driver tests only — no Azure SQL Database is
  available. On the Managed Instance a blob `event_file` was created and
  refused its START (fake SAS, 25602), and a blob URL pattern merged to zero
  events; writing, watching and reading a blob with a real storage account
  and SAS, and whether the wildcard pattern gosmo builds lists a container's
  rollover blobs, are unrun.
- **V3 — Managed Instance low-privilege run unfinished**
  (t-qmi-01 refused every login, `testgo` too, Msg 40532, from 2026-09-30
  ~21:00). Done: as a `VIEW SERVER STATE` login, Sessions listed,
  New/Start/Stop/Delete greyed "needs ALTER ANY EVENT SESSION"; MI answers all
  nine granular event-session names through `HAS_PERMS_BY_NAME` (not NULL as
  on 13–16), so the `gate.EventSession*` `Alt`s decide there. Found: Watch
  Live Data on `system_health` failed Msg 40538 (reading the current file by
  path) — fixed by `xeReadsOnlyByPattern`, tests only. **Still to run:** the
  fix live (Watch Live Data, View Target Data, Merge on `system_health`);
  Properties read-only and browsable; the Profiler refused; then
  `gossms_w6_alt` (Profiler creates and watches, Properties editable) and
  `gossms_w6_gran` (granular CREATE/ENABLE/DISABLE only: Profiler allowed,
  Delete and Properties' Events greyed). **Left on t-qmi-01 to drop:** logins
  `gossms_w6_vss`, `gossms_w6_alt`, `gossms_w6_gran` (password
  `W6-inSecure123!`), session `gossms_test_xe_w6`. Also: whether MI accepts
  `MAX_DURATION` on an event session — gossms's Advanced page hides it off-box
  (`major >= 17 && !azure`, `xevent_session_advanced.go`) while gosmo reads
  `max_duration` there; the answer becomes one gosmo capability gossms asks
  (review plan 2026-10-03 E9).

- **V4 — Resource Governor and Database Mail unverified on Managed
  Instance** (Phase 5 item 24; t-qmi-01 unavailable since 2026-09-30). Both
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
  from another context (review K3, unit-tested only) — hold one from a second
  session's `USE master` + a cross-database open transaction, force-drop a
  throwaway database, expect no Msg 3702.
- **V5 — Edition gates of Phase 5 item 24 never met a real refusal.** Every
  instance in the estate is Developer, so Resource Governor's rule
  (Enterprise/Developer on 13–16, plus Standard on 17 —
  `resourceGovernorSupported`) ships from documentation, pinned by fake-driver
  tests; Database Mail on Express (procs present, no `DatabaseMail.exe`, so a
  test mail should sit `unsent` until the dialog's 30 s wait gives up) is
  unprobed. Run both when a Standard or Express instance is available.

### Nice to have

- **N1 — IntelliSense query-tree shapes deliberately left out.**
  `sqlparse.ScopeAt`/`completion_relations.go` resolve CTEs, derived tables,
  sub-SELECTs, set-operator chains, `PIVOT`/`UNPIVOT` outputs, temp tables and
  table variables (`sqlparse.ScanBindings`, batch-scoped; temp tables carry
  across `GO` via `sqlparse.CarryTempBindings`), `OPENJSON`/`OPENROWSET`/
  `OPENXML` `WITH` lists (`sqlparse.Rowset`), and table-valued function
  result columns (`gosmo.Catalog.Functions`, resolved only for a called ref,
  `sqlparse.FromRef.Call`), and three-part names in another database
  (`completion_crossdb.go`: listed by the server's database directory, loaded
  only after `HAS_DBACCESS`; `db..t` tries the login's default schema there,
  then `dbo`), and a linked server's four-part names (`completion_linked.go`:
  SQL Server remotes only, read through `OPENQUERY`; see `docs/decisions.md`
  § IntelliSense: linked-server four-part names). Left out, answering
  *nothing* rather than a wrong list: `LS..t`/`LS.db..t` (the remote login's
  default database or schema), and a non-SQL Server linked server. `PIVOT`
  columns are typed for every built-in aggregate PIVOT accepts (`CHECKSUM_AGG`
  and `STRING_AGG` it refuses); a user-defined aggregate leaves them untyped.
- **N2 — IntelliSense keys a contained database by its data collation.**
  `completionInventory.collation` is `sys.databases.collation_name`, but a
  partially contained database (and an Azure SQL Database with
  `CATALOG_COLLATION`) compares *names* under its catalog collation,
  `Latin1_General_100_CI_AS_KS_WS_SC` — case-insensitive. On a contained `_CS_`
  database a wrong-case qualifier (`ORDERS.`) answers nothing although the
  server would resolve it. Needs the containment / catalog collation on
  `gosmo.Database` (not read by `DatabaseByName` today); `nameSet` users via
  `databaseCollation` share the gap.
- **N12 — Master-detail pages each reimplement their pending-edit list.**
  About ten pages (Resource Governor pools/external pools/groups, Database
  Mail profiles/accounts, database files/filegroups, New Database filegroups,
  extended properties, role members, job steps, key signatures, symmetric-key
  encryptions) define their own row struct with `name`/`isNew`/removing/
  orig/cur and write their own dirty, visible, revert, add-with-duplicate-check
  and remove-or-mark. The duplicate checks are shared already
  (`pendingNameIndex` on Add, `pendingNamesRefusal` before Apply — Database
  Mail and job steps so far — `pending_edits.go`, collation-aware); the rest is
  meant to become one `pendingEdits[V]` there. Settled 2026-10-03: a page
  migrates onto it when next touched, each keeping its tests and getting a
  tmux pass — no sweep.
