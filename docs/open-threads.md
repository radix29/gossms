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

## Release workflow: what is still open

**Tag v0.0.13, then re-activate the `replace`.** gosmo v0.0.15 is tagged and
pushed, `go.mod` requires it and the `replace` is commented out, so a clean
build resolves. Delete this paragraph once the tag is pushed.

**The homebrew job's Verify step hasn't run in CI.** The formula lost its
`version` line on 2026-09-24 and Verify now checks the archive URLs' tag.
Dry-run locally against a fake remote: passes a correct formula, fails
missing/stale/half-old ones; `brew audit --strict --online` clean on staged
v0.0.12, `brew info` scans `0.0.12`. Check that job's log on the next tag, then
delete this entry. The settled shape: `docs/decisions.md` § Release workflow.

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

- **B8 — Message dialogs fold line breaks.** `ModalDialog.fitMessage`
  (`internal/tuikit/dialogs/common.go`) wraps through `core.WrapText`, which
  splits on `strings.Fields`, so every `\n` in an Alert, Confirm,
  TypedConfirm or Prompt message becomes a space — `cycleLogMessage`'s
  paragraph break has never been drawn. Found in the Extended Events live run
  (2026-09-29), where the filter prompt's syntax lines ran together; that help
  text was rewritten as prose instead. Fix: wrap per paragraph (as
  `splitLogLines` + `WrapText` do in the Log Viewer's details pane) and keep
  `WrapTextLimit`'s cap across the joined result.

- **B9 — Property-sheet page titles clip silently past 22 columns.** The
  page list is `pageListWidth` (24, `internal/tuikit/propsheet/sheet.go`)
  less the `▸ ` marker, with no ellipsis, so "External Resource Pools"
  (Resource Governor Properties) renders as "External Resource Pool" and
  "Database Scoped Configurations" (Database Properties) loses its tail. Found
  in the Resource Governor live run (2026-10-01). Fix: a guard like
  `TestNoPropertySheetLabelIsTruncated` over `propPage.title` literals, then
  shorten the offenders — or widen the list, which costs every dialog form
  width.

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
  `W6-inSecure123!`), session `gossms_test_xe_w6`.

- **V4 — Resource Governor and Database Mail unverified on Managed
  Instance** (Phase 5 item 24; t-qmi-01 unavailable since 2026-09-30). Both
  shipped shown and ungated on EngineEdition 8, pinned by fake-driver tests
  only (`resourceGovernorHidden`, `databaseMailHidden` in `edition_gate.go`).
  To run when MI is back: RG catalog/DMV reads and `CREATE RESOURCE POOL` /
  `ALTER RESOURCE GOVERNOR` rights; Database Mail `sysmail_*` reads, writes and
  a test send; the gosmo version sweep of every new read; the tmux live pass
  for both. If MI refuses RG DDL, add an `edition_gate.go` entry.
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
  table variables (`sqlparse.ScanBindings`, batch-scoped). Left out, answering
  *nothing* rather than a wrong list: table-valued function result shapes
  (need a grammar and catalog shapes it lacks), `OPENJSON`/`OPENROWSET` `WITH`
  lists (own grammar), cross-database three-part chains (need an inventory per
  database). Deliberate limits of what shipped: a temp-table binding doesn't
  survive `GO` (the table does), and `PIVOT` columns are untyped (aggregates
  aren't modelled). Each is its own pass if asked.
- **N6 — Resource pool affinity is read-only.** `AFFINITY SCHEDULER` /
  `NUMANODE` on resource and external pools is read and scripted but not
  edited (Phase 5 item 24; `docs/decisions.md` § Resource Governor and
  Database Mail): editing needs a scheduler/NUMA picker
  nothing else in gossms has.
- **N7 — No SQL Server Agent Properties ▸ Alert System.** Agent's mail
  profile (`sp_set_sqlagent_properties @databasemail_profile`) can't be set
  from gossms — there is no Agent Properties dialog at all. Out of scope of
  Phase 5 item 24 (Database Mail); until then operator/job notification mail
  needs it set by T-SQL.
- **N8 — Database Mail items and log are own-only below sysadmin.**
  `sysmail_allitems` and `sysmail_event_log` filter on
  `IS_SRVROLEMEMBER('sysadmin')`, so CONTROL SERVER and msdb db_owner — who
  configure everything — see only their own items and those items' events,
  as SSMS's viewer does. Details says "Your failed items" and the log's
  Delete... warns that it purges unseen rows; the Log Viewer itself says
  nothing. Reading the base tables (`sysmail_mailitems`, `sysmail_log`),
  which both may SELECT, would show everything — a gosmo change, relying on
  undocumented tables. Found in the Phase 5 W14 live run (2026-10-01).
- **N9 — Resource Governor Properties offers a new pool to Workload Groups
  only after Apply.** RG's pages load independently with no page-shown hook,
  so a pool added on Resource Pools is missing from the Workload Groups pool
  dropdown until Apply. Database Mail solved the same problem with a model
  shared across pages (`mailModel`, `database_mail_props.go`); RG would need
  the same. Found in Phase 5 W5 (2026-10-01).
- **N10 — Phase 5 item 24 rough edges, left as is (2026-10-01).** Send Test
  E-Mail's status row shows the raw gosmo/mssql error ("gosmo: send test
  mail: mssql: profile name is not valid (14607)"); View Database Mail Log is
  offered to a login with no msdb access and opens on "Access denied"; a hint
  row below the fold (Accounts' "is deleted on Apply") is not scrolled into
  view; Script Resource Governor as is offered to a VIEW SERVER STATE-only
  login and fails "not visible" in the status line; an unreadable RG
  configuration leaves the node label bare, as if enabled
  (`resourceGovernorState`, deliberate). Each is small; none misleads into a
  wrong write.
- **N5 — Other Properties dialogs leave the tree stale after Apply.**
  `PropDialog.onSaved` (`internal/tui/prop_dialog.go`) runs after a write
  lands; only Session Properties sets it (`refreshXESession` reloads the
  session's target leaves). A dialog whose Apply changes a node's tree
  children — Table Properties' columns, a database's files — still needs a
  Refresh of the node. Each is its own call: wire `onSaved` to reload the
  object's node where the tree shows it.
- **N3 — `BatchEndOffset`'s forward scan is O(script).** `sqlparse.PrefixCache`
  made the prefix scan incremental, but `sqlparse.BatchEndOffset`
  (`internal/tui/sqlparse/token.go`) still lexes from the cursor to the next
  bare `GO` (or buffer end), once per keystroke while the popup is open, on the
  UI goroutine. Not in `PrefixCache` because the boundaries are *ahead* of the
  cursor and would be invalidated by edits below it. Unmeasured; trigger is a
  benchmark with the cursor well above the end showing it matters.
- **N4 — Wrap mode re-segments the whole document per keystroke.**
  `Editor.buildVisualLines` (`internal/tuikit/controls/editor_wrap.go`)
  memoises on the document version, which every edit bumps, and
  `visualIndexForCursor` scans every visual row. 2026-09-24, 20 k-line script:
  6.5 ms per keystroke wrapped vs 0.45 ms unwrapped — inside a frame, not acted
  on. Fix when a measurement passes ~16 ms: re-wrap only edited lines, keep a
  per-line visual-row prefix sum.
