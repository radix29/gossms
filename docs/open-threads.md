# Open threads

Work that was found, decided, or deferred but not finished. Aggregated
2026-07-30 from notes scattered across 22 session records; re-verified against
the code and pruned 2026-08-06.

Keep this file current: close an item by deleting it, and add one whenever
something is knowingly left undone. An open item recorded only in a session
note is invisible by the next session.

**This file holds only open work.** Fixed items do not accumulate here —
delete an item once it is done. The "do not re-raise" sections
below are the deliberate exception: they are not history, they are what stops
a settled question being reopened.

## By design — not issues, do not re-raise

- **Three items came off README's Known Issues 2026-08-18, as scope notes
  rather than defects**, and are not to be re-listed there: SQL Agent's
  ungated Start/Stop Job and missing step reordering, Always On's
  no-rollback of a partly created group, and Always On's one-login-for-every-
  replica limit of `db.ServerConn.Peer`. Each is described in full below — see
  § Reworks named in README's Known Issues and § Unbuilt features README
  already promises. README's Known Issues now names only Reports plus the
  environment and distribution caveats. Three of the four were closed in
  `v0.0.8` — Start/Stop Job read the job's state, Move Up / Move Down are on
  Job Properties > Steps, and a peer is reached with its own saved
  credentials; the no-rollback of a partly created group stands, and is now a
  decision taken deliberately rather than a gap — see the entry below.

- **New Availability Group does not roll back a group whose CREATE succeeded,
  and that is the decision.** Settled 2026-08-27, with the create-time
  preflight built the same day. Every ordinary reason a
  secondary's JOIN fails is now checked *before* the CREATE — peer reachable,
  Always On enabled there, endpoint present, STARTED and still at the address
  the dialog recorded, rights to join — and any failure refuses with nothing
  created. What survives is the peer that dies between the check and the JOIN,
  and there the group is left alone: a rollback would destroy what the user
  asked for on the strength of one unreachable instance, and it cannot even be
  complete. Verified live on 2026-08-27 — a group dropped from the primary
  stayed in the *secondary's* `sys.availability_groups` and needed a local
  DROP there, so a rollback that cannot reach a joined secondary leaves exactly
  the residue it exists to prevent.

- **No gosmo `Drop*` write method carries `IF EXISTS`, and that is the
  decision, not an omission.** Settled 2026-08-13 after a review found the
  family split down the middle: dropping a view that was already gone reported
  "View deleted" and dropping a sequence reported the server's refusal, from
  the same Object Explorer gesture. A bare DROP everywhere was chosen over
  `IF EXISTS` everywhere so that "deleted" means deleted; a caller that wants
  idempotence ignores the error, which is a choice the library cannot make for
  it. `TestDropStatementsAreNotIdempotent` pins it. The *scripts* Scripter
  generates keep `IF EXISTS` — DROP-and-CREATE output exists to be re-run.

- **A system Agent job still offers `Delete Job...`.** Rename came off every
  system object 2026-08-13 (`nodeData.IsSystem`), but the Agent family's own
  Delete in `agent_menu.go` was never part of the `objectOps` table and is left
  alone: SSMS permits deleting a system job, and msdb raises no objection.

- **Which databases a dropdown offers is settled, and lives in
  `internal/tui/database_list.go`.** The rule turns on when the name is
  resolved: a name stored now and used later (job step, alert, login default
  database, restore history) lists every database including system and
  non-ONLINE ones, because it is opened when the job runs, not now; a name
  acted on immediately lists only what the action will accept. Backup is the
  only dialog in the second class, and both its exclusions are hard server
  restrictions verified on win10cli 2026-08-09 — `BACKUP DATABASE tempdb` and
  a backup of an OFFLINE database each fail with "BACKUP DATABASE is
  terminating abnormally". Do not "unify" the two lists; they are different on
  purpose.
  The trap that comes with the filter, since it will be rediscovered: the
  Backup dialog is opened *on* a database from the Object Explorer, and its
  dropdown is swapped asynchronously afterwards. `setDatabaseItems` keeps a
  selection the incoming list doesn't contain, at the front. Without that,
  right-clicking an OFFLINE database and choosing Back Up silently retargets
  the dialog at whichever database sorts first and backs *that* one up —
  reproduced live, `gossms_p6_offline` became `HealthClinic`. Any future
  narrowing of a dropdown that a dialog can be opened on needs the same
  treatment.

- **The editor's redo stack is deliberately uncapped in bytes, and
  `applyStep`'s slice is deliberately unguarded.** Both were raised 2026-08-09
  and settled the same day; `maxUndoSteps` and `applyStep` in
  `internal/tuikit/controls/editor_undo.go` now carry the reasoning, and
  `TestEditorRedoStackBound` pins it.
  - Redo is bounded in *count* (one entry per undo, cleared on any new edit)
    and not in bytes. `maxUndoBytes` genuinely does not reach it — the inverse
    carries the lines being *replaced*, so on a growing document redo ends up
    above undo: 48.4 MB against 46.5 MB, measured. The undo stack's own byte
    cap is what bounds it, to within one document. A `redoBytes` cap would buy
    that one document back in exchange for silently dropping the deepest redo.
    Not worth it.
  - `applyStep` slicing `[st.row : st.row+st.newLen]` without a bounds check is
    the intended failure mode. The invariant is `pushUndoSpan`'s caller
    promise, and a violated promise means the document is about to be
    corrupted; clamping would turn that into an undo that quietly restores the
    wrong text. The panic is the more useful failure. Do not add a clamp.

- **gosmo untagged past its current tag with `go.mod`'s `replace` active is
  the intended development state**, not a release blocker. Tagging gosmo,
  bumping `require`, and commenting out the `replace`/`ignore` pair are steps
  of the release process itself (ARCHITECTURE.md § Developing against a local
  gosmo checkout). A CI release build not resolving gosmo mid-development is
  the expected consequence. As of `v0.0.8` the pair is commented out and
  `require` names gosmo `v0.0.10`; re-activate both when work resumes.

- **A Grid/Text query result can exhaust memory.** The Max Result Rows option
  and every `maxRows` parameter behind it were removed 2026-08-01: a result
  set is retained in full, so `SELECT * FROM` a billion-row table will OOM the
  process. SSMS parity of "you get what you asked for" was preferred to a
  silent cap. The retained form is already as small as it reasonably goes
  (`internal/query/arena.go`); the floor is the 16-byte string header per cell
  that `ResultSet.Rows [][]string` implies. Results To File never retained
  rows and is unaffected. Do not add a cap back.

- **The `Meta` (Output Column Metadata) block is a grid-only display aid.** It
  reads `Result.Sets`, which `ExecuteToSink` leaves empty by design — an export
  retains no rows, and the column *types* are not known to `RowSink.BeginSet`
  anyway. It also only takes effect on the *next* execution. Both intended; do
  not carry `ColumnTypes` through the sink interface to "fix" the first.
  Decided 2026-08-04.

- **The DataGrid cell-viewer popup is deliberately unhighlighted.** A "Show
  Value" on a cell whose trimmed text is bracketed by `<>`, `{}`, or a
  JSON-shaped `[]` opens its own query panel with
  `XMLHighlighter`/`JSONHighlighter` instead (`internal/tui/cell_value.go`, via
  `DataGrid.OnShowValue`); everything else gets the plain 60-column popup,
  unhighlighted, on purpose. Highlighting inside the popup is the thing not to
  do: wrap mode resolves each drawn column through `styleAt`
  (`editor_draw.go`), a linear scan of the logical line's runs, chosen so a
  `varchar(max)` cell costs work proportional to the ~15 visible rows rather
  than to the value. That scan is fine against SQL's few coarse runs and not
  against a highlighter emitting one run per token over a whole XML document.
  Routing to a panel, which draws unwrapped, is what sidesteps it. Decided
  2026-08-04.

- **The Databases folder's one round trip per database is intended.** Author's
  call 2026-08-05 on a costed proposal. `FILEPROPERTY` reports on the *current*
  database only, so Data/Log/Unallocated/AvailLog cannot come from a
  server-wide view the way the Tables folder's aggregates do. Collapsing it
  into a single dynamic batch does work and is faster (11-12ms vs 20-21ms over
  four databases), but one unreachable database fails the whole batch and every
  row loses its sizes, where the fan-out degrades to `N/A` in that one row
  alone. The fan-out is also already concurrent 8-wide, so it costs
  `ceil(N/8) x RTT`. If a user ever reports the folder being slow, the batch
  goes in as a *fast path* with the fan-out as the fallback on any batch error
  — never as a replacement.

- **Restructuring `internal/tui` is closed. No file-split or package-split
  candidates are outstanding.** Raised 2026-07-30, costed and then re-measured
  2026-08-05 against a type-checked cross-file reference graph (549 real symbol
  edges) and rejected on the numbers. What
  shipped instead is `internal/tui/sqlparse`, the only part of the package with
  *zero* outbound references. The earlier "P5" file-split list finished
  2026-08-04 — every file *on that list* came out under 400 lines, split on
  the draw/input seam. It was never a standing rule for the package, and
  isn't one now: 31 non-test files exceed 400 lines, fifteen of them directly
  in `internal/tui` (re-counted 2026-08-09). That is not a reason to re-open
  the question this paragraph closes.
  The negative results, each a proposal a future review will otherwise
  re-invent:

  - The `agent_*`/`database_props_*`/`new_*` name families are not a seam —
    they cut straight through the `App` dependency.
  - Neither is "the lines that never mention `App`". `grep -w App` misses
    `p.app`/`d.app` field access; the real figure is 48% (58 files, 12,618
    lines), not 56%, and nine files are misclassified by it.
  - A `props` package is **not** a five-method interface. The 41 `propPage`
    files have 110 outbound references to 44 symbols; only ~6 are `App`
    services, the rest shared helpers embedded in 146-221-reference hubs that
    cannot move. It needs a fourth package, not one interface.
  - Its stated benefit — "props becomes testable without an `App`" — already
    holds: five `*_test.go` files under `props` have zero `App` references.
  - `planview`, the precedent, has no `Host` interface at all. Both existing
    sub-packages are leaves, and that is why both worked.

- **The Block tab listing the monitoring session itself is intended.**
  `sp_block`'s final `WHERE` drops only sessions that are idle *and* blocking
  nobody, and the session running the procedure is neither, so an unblocked
  server shows exactly one row: its own. Raised by the 2026-08-07 second
  review with `and spr.spid <> @@spid` as the fix (sp_WhoIsActive's
  `@show_own_spid = 0` is the precedent) and **rejected — author's call, on
  purpose**. Do not "fix" it.

- **`sp_block`'s `cross apply sys.dm_exec_sql_text` and its lack of an
  `ecid = 0` filter are intended.** The same review proposed `outer apply`
  plus `spid > 50 and ecid = 0`, on the grounds that a blocker with no cached
  text takes its whole blocked subtree out of the tree and that a parallel
  plan is several `sys.sysprocesses` rows. Neither was reproducible on
  ubudock — a sleeping blocker kept a resolvable handle, and `DBCC
  FREEPROCCACHE` does not evict a live transaction's text — and the
  `cross apply` is what keeps system sessions out today. **Author's call: as
  is.** The one part of that finding that was accepted, the unused
  `dm_exec_cursors` join, is already gone.

- **The Activity Monitor probes `VIEW SERVER STATE` once per collector, so
  twice per panel open.** Hoisting it to a single shared check was proposed
  and dropped: the Retry control added in the same pass starts a *new*
  collector after a transient failure, and a cached permission answer would
  make that retry fail without asking the server. One extra round trip on
  open is the cheaper mistake.

- **`counterQueryFor`'s `RTRIM(instance_name) IN ('', '_Total')` filter drops
  no counter the panels read.** Raised twice on the grounds that
  `RTRIM(NULL)` is `NULL` and `NULL IN (...)` is false, so a NULL
  `instance_name` would be silently dropped. **Verified live 2026-08-07 on
  both servers.** win10cli (Windows) has no NULL `instance_name` rows at all;
  ubudock (Linux) has exactly five, and all five are `SQLPAL:Host Memory` /
  `SQLPAL:Guest Memory` rows that are not in `counterNames` and have no
  gossms consumer. Every one of the 33 names in `counterNames` resolves
  through the filter on both builds, 37 rows each (the extra four are names
  published under both `''` and `'_Total'`). Do not add a
  `OR instance_name IS NULL` arm.

- **`formatValue`'s `case float32` is unreachable but kept.** go-mssqldb
  returns `float64` for both `REAL` and `FLOAT`. It is correct if the driver
  ever narrows, and `formatFloat` already takes the bit size. Noted so it isn't
  "discovered" as live code.

- **Server-scope GRANT/DENY/REVOKE's `USE master;` prefix does not strand the
  pooled connection in master.** The 2026-07-31 review read
  gosmo's `"USE master; " + stmt` (now `permission_options.go:348`, which
  `server_security.go`'s grant/deny/revoke methods route through) as pool
  contamination and
  proposed a pinned connection that reads `DB_NAME()`, switches, and switches
  back. **Live A/B on 2026-08-01 disproved it**: eight pooled connections all
  still reported the right database after a GRANT, with the *original* code.
  `database/sql` calls `driver.SessionResetter.ResetSession` before handing a
  pooled connection to its next user, and go-mssqldb implements it by flagging
  the next TDS batch as a connection reset (`Conn.ResetSession` ->
  `sendSqlBatch72`'s `resetSession`), restoring the session's database to the
  connection string's. The proposed fix was three extra round trips per grant
  to re-solve what the driver already handles, and was reverted.

## Unbuilt features README already promises

Each is a feature, not a defect.

- **Reports** — server-level and database-level (top tables, disk usage). No
  entry point exists yet.
- **Always On Availability Groups** — all seven phases are **done**: gosmo's
  `availability_group.go` read, write, operation and create layers plus
  `endpoint.go` and `certificate.go`, `db.ServerConn.Peer` for following the
  primary, the Object Explorer branch, `ag_props*.go`, the `AGDashboard` panel
  in both its per-group and all-groups forms, `alwayson_menu.go` with its
  dialogs, `new_ag_dialog.go`/`new_ag_pages.go`, `ag_add_replica_dialog.go`,
  `ag_listener_props.go` and `new_endpoint_dialog.go`.
  What is left is scoped out rather than unfinished:

  **The create dialog has no Read-Only Routing page**, unlike SSMS's. Routing
  is a per-replica setting that AG Properties already covers, and it is
  reachable two clicks after the group exists.

  **The endpoint dialog assumes a full mesh and one shared master key
  password.** Every instance gets every other instance's certificate, which is
  what an availability group needs and more than a plain mirroring pair does;
  and the one password field is used for whichever instances turn out to have
  no database master key yet, rather than one per instance.

  **Peer credentials are resolved from connections the user has already
  made.** `ServerConn.SetPeerCredentials` installs a resolver `peerOptions`
  consults before falling back to the parent's settings, so connecting to a
  replica once via File > Connect is how it is given a different login, port,
  auth method or TLS setting. A saved connection is taken whole rather than
  field by field, so it cannot silently stop carrying whatever
  `config.Connection` gains next. Two things about the lookup: it is keyed by
  `db.InstanceKey`, which normalizes case, port and named-instance spelling;
  and because `@@SERVERNAME` is the short machine name while what people type
  on a domain network is the FQDN, a saved connection is *also* registered
  under its short host as a strictly lower-priority tier — an exact host match
  always wins, so `sql.a.example` and `sql.b.example` never hand each other
  their logins. **The resolver may never make an instance less reachable than
  it was before one existed**: `Peer` retries once with `parentPeerOptions`
  when the resolver's answer will not connect, and `loadPeerCredentials` does
  not seed an entry whose password this session cannot decrypt. A saved
  *low-privilege* login still wins over the parent's on purpose — that is the
  feature, not a defect.

  Still open: the retry is pinned only through the option derivation, since
  `Peer` sits behind `Connect`. **Verifying it live** — Always On on
  ubusql1/ubusql2, then a deliberately broken saved replica password — is the
  run that would close that.

  **A partly created group is left as it is**, and so is a partly added
  replica. If CREATE succeeds and a secondary then fails to JOIN, the error
  names the instance and says the group exists, but nothing is rolled back —
  dropping a group the user asked for on the strength of one unreachable peer
  would be worse. Reproduced live: with the test cluster's endpoint certificate
  broken, the dialog created the group, reported `availability group "AAG2" was
  created, but ubusql2 could not join it`, and left both halves visible. Add
  Replica makes the same choice for the same reason, in the same wording.

  **An added replica's endpoint URL is editable**, so an instance whose short
  name the other replicas cannot resolve can be given its FQDN. Connect still
  fills it and is still required — it is what proves the endpoint exists and is
  STARTED. Covered live 2026-08-22 against AAG1 on ubusql1: Connect on
  a typed `win10cli` filled the row with `tcp://win10cli:5022` and reported
  `Connected to win10cli (tcp://win10cli:5022).`, and replacing that with
  `win10cli.fritz.box:5022` was refused with `endpoint URL
  "win10cli.fritz.box:5022" has to start with tcp:// — the form is
  tcp://host:port`.

  Two things about how it was run, both worth keeping. The refusal was reached
  through **Script Changes, not OK**: `newObjectDialog.runPipeline` calls
  `preflight()` before the first apply function on *both* paths, so Script
  Changes exercises `validateAddReplica` in full without a write path — the way
  to test a refusal on a dialog whose OK would mutate a production group. And
  the run deliberately stops there: win10cli cannot actually join, so ADD
  REPLICA/JOIN past the validation is not what this covers. AAG1 was verified
  unchanged afterwards — two replicas, HEALTHY, both databases SYNCHRONIZED.

  **Add Replica does not offer initial data synchronization**, unlike SSMS's
  wizard, which can take a full and log backup to a share and restore them on
  the new replica. AUTOMATIC seeding covers the case the wizard's share-based
  route exists to work around; MANUAL seeding means restoring each database
  there by hand and then using Join to Availability Group on the secondary's
  copy, which is now in the tree.

  **Failover cannot be done in T-SQL under `cluster_type = EXTERNAL`** —
  handled, not open: `agFailoverRefusal` explains it and names Pacemaker
  instead of sending the statement. Recorded here because the two error codes
  behind it are worth keeping written down. EXTERNAL rejects both
  `ALTER AVAILABILITY GROUP ... FAILOVER` and
  `... FORCE_FAILOVER_ALLOW_DATA_LOSS` with `Msg 47104`; `NONE` rejects only
  the lossless form, with `Msg 47122`, and allows the forced one. Both
  verified live.

  **`RemoveReplica` and `Drop` have no live coverage against a real group.**
  Both were verified by standing up a throwaway `CLUSTER_TYPE = NONE` group
  across ubusql1/ubusql2 and tearing it down, since running them on AAG1 would
  destroy the test cluster. `TestLiveAvailabilityGroupOperations` deliberately
  skips them; only add/remove database, suspend/resume, the listener round
  trip and the failover refusal run there.

  **A listener address cannot be removed and a listener cannot be renamed.**
  Not a gap in gossms: `ALTER AVAILABILITY GROUP ... MODIFY LISTENER` has no
  statement for either, so both mean REMOVE LISTENER and ADD LISTENER. Listener
  Properties says so rather than offering buttons that would only work on rows
  not yet written. Note also that under an EXTERNAL cluster type an added
  address is recorded OFFLINE, since the external cluster manager owns it —
  verified live 2026-08-11.

  **The backup history is the wrong signal in both directions**, established on
  the AG cluster by running the statement in each state (2026-08-23). A
  database whose `msdb` history was deleted still joins; one that was backed up
  and then round-tripped through SIMPLE does not, though its history still
  shows the full backup. Both `ALTER AVAILABILITY GROUP ... ADD DATABASE` and
  `CREATE AVAILABILITY GROUP ... FOR DATABASE` refuse with **Msg 1475** ("might
  contain bulk logged changes that have not been backed up"), which is why the
  New AG page applies the same rule. Driven end to end in the TUI against AAG1,
  both directions: an unbacked-up database appears under "Not offered", and
  after `BACKUP DATABASE` the reopened dialog offers it.

  What is deliberately *not* built is SSMS's other half — offering to take the
  backup from inside the dialog. gossms has a full Backup dialog two clicks
  away, and the exclusion line names it.

  **The unreachable-primary fallback is unit tested but never exercised
  live.** `resolveAGView` degrades to the partial local view and flags it, but
  no test has actually made a replica unreachable. Note that AG Properties
  takes the opposite line — `agOnPrimary` treats an unreachable primary as an
  error, because a page that loaded from a secondary would offer edits the
  server rejects — and that path *is* only unit tested too.

## win10cli as a third instance: what it can and cannot be

Settled 2026-08-22 by probing the instance rather than by reasoning about it,
and recorded so the question is not reopened. `win10cli` **can** be the third
instance the Always On coverage entries above were waiting for, and **can never
be a third availability replica**.

- **It cannot host a replica**, and no amount of configuration changes that.
  `SERVERPROPERTY('IsHadrEnabled')` is 0, `IsClustered` is 0 and
  `sys.dm_os_cluster_nodes` is empty; the host is **Windows 10 Pro**, which has
  no Failover Clustering feature at all, and SQL Server on Windows will not
  enable Always On for a machine that is not a WSFC node. Even with the feature
  on, a Windows replica in a Linux availability group is unsupported — a
  distributed AG is the only cross-platform shape. Do not try to add it to AAG1.
- **It can do everything an availability group is built *on top of*.** Database
  mirroring endpoints, certificates, the principals that own them and the
  CONNECT grants between them are plain T-SQL with no HADR precondition, and
  gossms checks HADR only on the instance the endpoint dialog is *opened from*.
  That is what closed the endpoint-dialog and thumbprint-mismatch entries, and
  it is what lets Add Replica's Connect read a real endpoint off a third
  instance.
- **It is now configured as ubusql1's mirroring peer, deliberately left that
  way.** win10cli holds a database master key, `win10cli_Cert`, ubusql1's
  imported `ubusql1_Cert`, `ubusql1_login`/`ubusql1_user`, and endpoint `AGEP`
  STARTED on 5022; ubusql1 holds the matching `win10cli_*` principals and
  certificate. Left in place because a third instance with a STARTED endpoint is
  exactly what Add Replica's Connect needs, and rebuilding it costs a live run.
  It is inert — it is not a replica of anything and nothing connects over it.
- **What it still cannot supply is a named instance.** `InstanceName` is null,
  and Linux SQL Server has no named instances at all, so
  `endpointPrincipalBase`'s `HOST\INSTANCE` → `HOST$INSTANCE` mapping remains
  unexercised live. Closing that needs a second, *named* SQL Server instance
  installed on the Windows host — see the entry above.

## Reworks named in README's Known Issues

The author's own assessment; no defect list behind it yet.

**Backup and Restore no longer need one** — closed 2026-08-12, and dropped
from README's Known Issues. Every part `todo/todo.txt` named is done: the
Browse buttons list the *server's* filesystem rather than the client's, the
Restore File Locations view (`restore_dialog_files.go`) covers move-files
handling, a failed restore's SQL Server message is wrapped rather than clipped
to one line, and paths in the file list are clipped from the left so the file
name survives. Per-file destinations (SSMS's editable "Restore As" column) were
deliberately not built; the folder-level choice covers what the dialog's width
allows. The Backup dialog's progress view wraps its failure message too, as of
2026-08-12 — it was the last rough edge left in the pair.

Two rules from that work outlive it. **The backup set number must not get
re-scattered**: the restore itself, the MOVE clauses and the Files Included
panel all take it from `backupSetNumber` in `restore_dialog_ops.go`, and two of
the three deriving it separately is exactly what produced the "Logical file 'x'
is not part of database 'y'" failure on every rename-restore from an appended
`.bak`. **The relocation preview and the MOVE clauses must keep sharing
`relocateFiles`**, or the paths the Files view lists stop describing what the
restore does.

- **SQL Agent.** Reviewed 2026-08-12, the first pass over the `agent_*` files.
  The area is in better shape than "needs a complete rework" implied — the
  Steps page's three-pass apply around `sp_delete_jobstep`'s renumbering, the
  read-only non-T-SQL guard, per-row dirty gating, rename-last ordering, and
  gosmo's `escapeSingle` coverage of every `sp_*` call are all sound. "Needs a
  complete rework" is retired as the thread; these are what the pass actually
  found. The two dropdown bullets that were here — Notifications assigning an
  operator nobody picked, and a dropped owner login shown as a real one — are
  **fixed** (2026-08-12); what is left is below.

  **The `Unknown` path in "Jobs Without Schedules" has no live coverage.** The
  report carries a Schedules column reading `None`/`Unknown`, and a cancelled
  context returns the cancellation rather than a page of `Unknown`; aiming a
  failure at one job's round trip while the others succeed has no easy
  handle.
  **Start/Stop Job now read the job's state first** and refuse the request the
  Agent would refuse, in the app's own words ("Job X is already running" / "is
  not running"), refreshing the node either way. The read is free — both
  actions already fetched the job, and `JobByNameContext` carries
  `CurrentState`. The menu items are deliberately *not* greyed out: a job
  node's cached state is as old as the last folder load, so gating the item
  would hide a legitimate Stop for a job that started running since. The check
  belongs where the data is one query old.

  **Step reordering is built** — Move Up / Move Down on Job Properties > Steps,
  as a fourth apply pass. msdb does have the mechanism: `sp_add_jobstep`
  accepts `@step_id`, which *inserts* at that position, renumbers the later
  steps and follows their "go to step N" references. What was actually missing
  was fidelity, which is why it needed gosmo work: a move is a delete plus an
  insert, so the step's whole definition has to survive the round trip, and
  `JobStep` modelled none of `proxy_id`, `additional_parameters`,
  `cmdexec_success_code`, `server`, `database_user_name` or `os_run_priority`.
  All six are read and written now, alongside `flags`, and
  `Job.MoveStep`/`ReorderSteps` do the move.

  Two things established live and worth keeping (2026-08-23, SQL Server 2025):
  `sp_delete_jobstep` is **not** symmetrical with the insert — it silently
  resets a reference to a step at or after the deleted one to "quit with
  success" rather than following it, which is what `ReorderSteps`' repair pass
  exists for. And that repair pass is invisible to a test that moves the *last*
  step, because then no reference points past the delete: the live test moves a
  middle step for exactly that reason (the mutant survived the first version).

## Dropdowns: what is settled, and the one class left

The ten-site misreporting family found 2026-08-12 is **fixed** — one helper,
`selectPreserving`/`preservingItems` in `prop_grid_helpers.go`, generalised out
of Always On's `agSetSelect`, with `changedTo` gating every write. Two
things survive it.

- **Do not re-unify the eight remaining `indexOf` sites.** They are a
  different, already-safe class: every one indexes a list that *begins with a
  sentinel* (`(None)`, `<All databases>`) — `login_props.go:119`,
  `agent_operator_props.go:66`, `user_props.go:154`, `agent_alert_props.go:78`,
  `:82`, `:210`, plus `new_database_pages.go:46` whose value is the dialog's
  own. A miss there falls back to the sentinel, not to a real value, which is
  the correct answer and is pinned by
  `TestIndexOfSentinelListFallsBackToSentinel`.
  Converting them to `selectPreserving` *would* be a further improvement — a
  category deleted out from under an alert would then display its real name
  instead of `(None)` — but it changes documented, tested behaviour for a
  strictly smaller error, so it is a deliberate non-goal rather than an
  oversight. Raise it as its own change if ever wanted.

- **"A job whose owner login was dropped" is not reachable that way, and the
  old note here said it was.** Verified live on win10cli 2026-08-12: SQL Server
  *refuses* to drop a login that owns a job — `This login is the owner of 1
  job(s). You must delete or reassign these jobs before the login can be
  dropped.` A **schedule** has no such protection: dropping its owner login
  succeeds and `SUSER_SNAME(owner_sid)` goes NULL immediately, which is how the
  fix was A/B'd. So an orphaned *job* owner needs a different route — an msdb
  restored from another instance, or a Windows principal removed from AD — and
  is rarer than the schedule case, not equally common. Worth knowing before
  anyone tries to reproduce it by dropping a login and concludes the code is
  fine.

## Always Encrypted: what the two create dialogs deliberately leave out

Both dialogs went in 2026-08-21, closing "neither key
can be created from the tree". Three limits are the design, not a gap:

- **Key material is pasted, never computed.** A column master key's enclave
  signature and a column encryption key's `ENCRYPTED_VALUE` are produced
  client-side from the master key's private key, which lives in a Windows
  certificate store, a CNG/CSP provider or a Key Vault — none reachable from a
  portable no-CGO build. Both dialogs take the `0x…` value SSMS or the
  `SqlColumnMasterKey`/`SqlColumnEncryptionKey` cmdlets print. A pasted
  signature with "Allow enclave computations" unticked is *refused* rather than
  dropped: the key would be created, and would not be the one being set up.
- **One encrypted value per column encryption key.** A second value is what a
  master-key rotation adds; gosmo's `CreateColumnEncryptionKey` takes as many
  as it is given, but the dialog offers one and rotation stays out of the UI.
- **`RSA_OAEP` is the only algorithm**, because it is the only one SQL Server
  accepts — a dropdown of one would say nothing, so it is a static row.

## gosmo items left from the 2026-08-12 review

- **`CertificateByName`'s `(nil, nil)` was deliberately not changed** when
  `ErrNotFound` went in. Making it error on absence is a breaking change to a
  published contract, and its callers branch on absence as the ordinary case.
  Recorded so the remaining split doesn't read as an oversight — the three
  surviving conventions are now documented on `ErrNotFound` itself, and the
  `(nil, nil)` answer is pinned live by `TestLiveCertificateNotFoundIsNilNil`
  in both directions, so "nil" cannot quietly start meaning "always nil".

- **A missing principal and an invisible one are the same thing to
  `ErrNotFound`, and that is SQL Server's doing — do not try to fix it in
  gosmo.** Metadata visibility hides a principal the caller lacks
  `VIEW ANY DEFINITION` on by returning **zero rows, not an error**, so an
  existing login reads as absent and no lookup can tell the difference.
  Verified live on win10cli 2026-08-12 and pinned by
  `TestLiveNotFoundCannotSeePastMetadataVisibility`: a throwaway login saw two
  principals (`sa` and itself), and after `DENY VIEW ANY DEFINITION` saw one,
  having lost sight of its own row. The answer is idempotence at the write,
  not a better sentinel — `importPeerCertificate` now runs `CreateLoginContext`
  through `isAlreadyExists`, matching the `CreateUserContext` call below it.
  Note `isAlreadyExists` matches this by its "already exists" substring; its
  `15023` arm is the *user* code, and logins raise 15025.

- **The named-instance principal names are handled but have never run live.**
  Fixed 2026-08-13: `endpointPrincipalBase` maps `@@SERVERNAME`'s backslash to
  `$`, so a named instance contributes `HOST$INSTANCE_login` rather than
  `[HOST\INST_login]`, which is the spelling of a Windows principal. It is
  deliberately not truncated to the host the way `endpointURL` does — two named
  instances on one machine would then share every principal name in the
  exchange. `TestEndpointPrincipalBaseSurvivesANamedInstance` is the only thing
  pinning it; the test cluster is all default instances, so no named instance
  has ever gone through the exchange.

## A job's state comes from Agent, not from msdb

Fixed 2026-09-02. The old note here said six of gossms's eight
`formatJobState` arms were unreachable, and it was worse than that: gosmo's
`JobState` constants were not sp_help_job's encoding at all, only a set of
names consistent with gosmo's own `CASE ... THEN 4 ELSE 1`. Read live from
`msdb.dbo.sp_get_composite_job_info`, the real encoding is
1 Executing, 2 WaitingForThread, 3 BetweenRetries, 4 Idle, 5 Suspended,
6 WaitingForStepToFinish, 7 PerformingCompletionActions, with 0 meaning a job
Agent does not run itself. `Server.jobStates` now reads it from
`master.dbo.xp_sqlagent_enum_jobs` — the only place it exists, since msdb has
no such column — in **one** call for the whole instance, and overlays it onto
`Jobs`/`JobByName`.

Three things about it are load-bearing:

- **The `INSERT ... EXECUTE` table shape must match the extended procedure's
  thirteen columns exactly**, which is why `jobStateColumns` is copied from
  `sp_get_composite_job_info` rather than trimmed to the two columns gosmo
  reads.
- **A failed state read is not a failed listing.** `applyJobStates` swallows
  the error and leaves the `sysjobactivity` derivation in place: the extended
  procedure is unreachable whenever Agent is stopped, which is no reason to
  stop listing jobs. The derivation now emits the *same* encoding
  (`THEN 1 ELSE 4`), so the two paths cannot disagree — verified live against a
  job in each state.
- **`jobIsRunning` answers "unknown" for `JobStateUnknown` and
  `JobStateSuspended`, and `jobStateRefusal` then refuses nothing.** Unknown
  means Agent had nothing to say; Suspended is genuinely ambiguous —
  sp_help_job's own "not idle or suspended" filter groups it with idle, while a
  suspended job still holds a session. Sending the request and letting the
  server answer is better than refusing an action that would have worked.

Still open: **the Agent-stopped fallback has not been run live.**
`xp_cmdshell 'net stop SQLSERVERAGENT'` on win10cli fails with "Access is
denied" — the SQL Server service account cannot control the Agent service — so
the fallback is reasoned from the encoding and pinned only by the corrected
`CASE`, which *was* verified live in both states. Two deprecated constants
also remain: `JobStateCancelling` and `JobStateRunning` name states Agent's
encoding does not have. They carry negative values so no switch over the real
encoding reaches them, and are kept only so existing callers compile
(§ Changing gosmo) — worth retiring in a release that can take the break.

## Log File Viewer: what is deliberately out of it

Built 2026-08-12. One thing SSMS's own viewer has is
still left out on purpose; the other two were closed 2026-08-21:

- **One log file at a time, no merged view.** SSMS's left pane checkboxes let
  several logs (and the Windows event log) be merged into one date-sorted grid.
  The two selectors were chosen instead; merging means a source column, a merge
  sort, and N reads per refresh.
- **The toolbar's Filter box and "Search..." are different features — do not
  merge them.** Filter narrows what was read, instantly and with no round trip,
  and reports "N of M match". Search edits `xp_readerrorlog`'s own arguments
  3-6 and changes what the server returns, which is what a log too large to
  read in one go needs; the status line names it, because "no entries" on a
  searched read otherwise reads as an empty log. The client-side pass runs over
  whatever came back, so the two compose.
  Two things about those arguments that only a live run says: the date bounds
  must be sent as **text** (`YYYY-MM-DD HH:MM:SS`) — a typed datetime parameter
  is rejected with "The format for the date filter is incorrect" — and the two
  search strings are **AND**-ed, not alternatives.
Also not built: the Windows event log (needs WMI, out of scope for a no-CGO
portable build).

Two rules came out of it. The toolbar's busy latch is taken **before** the
confirmation is shown, not in the answer: the confirm dialog takes input but
does not stop F5 reaching the panel, so a read begun while the question was up
would clear `busy` from under the cycle. And an open viewer is put through
`Refresh`, never `Load`, after a cycle started from the tree — `Load`
re-enumerates only the family on screen, so cycling the Agent log while the
viewer sits on the SQL Server family would leave the Agent's pre-cycle
numbering cached and hand it to the user the moment they flipped the selector.

## Query Store: what is deliberately out of it

Built 2026-08-26 in three stages. The seven SSMS views,
the panel, and Force/Unforce Plan are in. What is not, and why:

- **Plan comparison is two grids, not two plan graphs** — an operator tile is
  eighteen columns wide. Operators pair on physical operator plus object
  without the index, so a seek that changed index reads as one changed row; a
  seek that became a scan stays two one-sided rows. What is still out:
  comparing a plan against a saved `.sqlplan`, and comparing two plans of
  *different* queries — refused, since they have no operators in common to
  pair.
- **No "Configure" button on the panel.** Query Store's own settings are a
  Database Properties page, which is where SSMS puts them too; the folder's
  context menu opens it.
- **No per-query time series.** gosmo's `QueryStoreTrackedQueryContext` returns
  one query's per-plan values interval by interval — SSMS plots it under the
  Tracked Queries view. Ours shows the tracked queries and their plans; the
  series is read by nothing. Kept in gosmo deliberately (§ Changing gosmo).
- **The panel reads on demand only** — no auto-refresh timer. Every read is
  one-shot and bounded, so nothing queues behind the shared host connection.
  F5 and the toolbar's Refresh are the whole story.
- **One metric and one statistic across all seven views.** Each report carries
  its own default (Total for the two that rank by accumulated cost, Avg for the
  rest) and the panel opens on it, but switching views afterwards keeps what
  the user chose rather than resetting. SSMS keeps them per view.

One thing that is *not* deferred and should not be re-raised: **the baseline of
Regressed Queries stays inside the reported window.** gosmo's default — the
equally long window immediately before `From` — is right for a caller that
picked its own range, and wrong for both surfaces here, because it makes the
report need twice its window of history before it can show a row. Verified
empty on a database with two minutes of history.

## Object Explorer folder filter: what is deliberately out of it

Built 2026-08-13. Two gaps left, both deliberate; the
other two were fixed 2026-08-15.

- **The folder filter is pushed down for the seven families where folder size
  can matter** — Tables, Views, Stored Procedures, Functions and the three
  System * variants — through gosmo's `ObjectFilter` and the
  `…FilteredContext` listings. Three things about it are load-bearing:
  - **The client-side pass still runs and is still the authority.**
    `filterChildren`/`filterObjects` narrow whatever comes back, so a
    translation can only ever be an optimisation. The only way it could change
    a result is by narrowing *further* than the client would, which is why
    `nodeFilter.pushdown` trims the value the way `matchText` does and refuses
    outright (falling back to the whole folder) rather than approximating.
  - **The comparison is `LOWER(col) LIKE LOWER(@p)`.** A bare LIKE is at the
    mercy of the database collation, and on a case-sensitive one it drops rows
    the case-insensitive client keeps.
  - **The pattern is escaped and carries `ESCAPE`.** `%`, `_` and `[` are all
    legal in an identifier; unescaped, a search for `pct_1` also matches
    `pct1100`, and one containing `[` matches nothing at all.

  The other filterable folders (sequences, synonyms, triggers, databases,
  logins, users, roles, schemas, partition functions/schemes, the Always
  Encrypted keys, security policies) stay client-side: they are small, and the
  clause builder is family-agnostic if that ever stops being true.
- **Owner and Durability Type are not offered on Tables, deliberately.** SSMS
  offers both; each is one `TableDetail` query per table, so listing them means
  a folder-wide detail fetch before the pane can draw a single row. Confirmed
  as the intended trade 2026-08-23 — this is a design decision, not a gap. Do
  not re-raise.
- **Filters are per-session and stay that way.** `App.savedFilters`, keyed by
  `filterKey` rather than by node pointer, brings a folder's filter back on a
  reconnect within the session. Writing filters to `config.json` so they
  survive an exit stays out: SSMS keeps them for the session only, and
  restoring one at startup against a folder whose objects have since changed is
  not wanted.
## Delete/Rename: what is deliberately out of it

Built 2026-08-13; column Delete, the drop-with-cascade option and Move to
Schema went in 2026-08-21. What is left out is:

- **No partition or filegroup Delete from the tree.** Neither has a tree node:
  partition functions and schemes do and are deletable, and a filegroup is
  removed from Database Properties > Filegroups.
- **A trigger cannot be moved to another schema**, and must not be offered: it
  belongs to its table and moves with it, and `ALTER SCHEMA ... TRANSFER`
  refuses one. Indexes, statistics, keys and constraints are the same case.
- **Agent objects keep their own Delete** (`agent_menu.go`), whose per-type
  wording explains what blocks each one; only Rename comes from the shared
  table. Availability groups likewise.
- **Multi-select delete lives in the Object Explorer Details pane and nowhere
  else.** `controls.TreeView` has a single selection, so SSMS's "Delete Object"
  dialog listing several objects will never come from the tree; do not propose
  it there. `DataGrid.OnMenuItems` gives the host entries in the grid's own
  cell menu, and `detail_browser_ops.go` contributes Delete over the block
  selection, gated per object and running through the same
  `confirmDeleteObjects` the tree's Delete calls.
  Two limits are the design: **only schema-scoped objects are deleted as a
  set** — tables, views, procedures, functions, indexes and the rest — while a
  database, a login, a server role, a user, a database role or an Always
  Encrypted key is deleted on its own (`objectOp.solo`, implied by `typed`),
  because a typed confirmation asks for one object's name and a principal's
  drop reaches past the object (orphaned users, closed connections) further
  than one shared warning can describe; and **Rename and Move to Schema stay in
  the tree**, neither meaning anything applied to a set. The solo rule is the
  *selection's*: one login selected in the pane still deletes, and the tree's
  Delete is untouched.

  What the pane can delete is what its loaders identify: the folders that go
  through `fetchChildObjectsDetail` (users, roles, schemas, indexes,
  statistics, keys, columns, sequences, synonyms, triggers, functions,
  partition functions/schemes, the Always Encrypted keys, security policies),
  plus Tables, Views, Stored Procedures, Logins and Databases. A view whose
  rows are not objects — Property/Value, a Query Store report, System
  Databases, a log listing — offers nothing, which is the correct answer
  rather than a gap.
- **The server does not say what blocks a column drop.** Verified live
  2026-08-21: `ALTER TABLE DROP COLUMN note failed because one or more objects
  access this column.` — no constraint, index or statistic named, even when a
  single default constraint is the whole reason. The delete confirmation
  therefore names the *classes* of blocker itself; do not "simplify" it to
  promise the error will identify one.

## Clipboard in a dialog: what is deliberately out of it

The `core.ClipboardHost` fix of 2026-08-20 gave every
dialog with a text field a working Ctrl+C/X/V and stopped the rest from
reaching past themselves to the query editor. Two consequences are deliberate,
not regressions:

- **A dialog with no text entry now has an inert clipboard.** Help, About /
  Object Dependencies, Confirm, Query List and Background Tasks do not
  implement the interface, so Ctrl+C there does nothing. It previously copied
  whatever the panel behind the dialog had selected, which was never the thing
  the user was looking at.

- **Key Diagnostics' `syncIfDirty` must not rebuild the editor while it has a
  selection.** The log lives in a read-only `controls.Editor` and the dialog is
  a `core.ClipboardHost`. It records the very keys used to copy from it, so
  `Ctrl+A` is itself logged and the next frame's `SetText` — which resets
  cursor, scroll *and* selection — would drop the selection before the `Ctrl+C`
  arrived. Status History does not need this, and a "simplification" would undo
  it.
## Comment drift: the semantic half is unfinished

A mechanical comment-drift survey of all 605 `.go` files (2026-08-29) found and
fixed nine items — stale doc-comment names, comment references to identifiers
that no longer exist, doc-vs-signature mismatches, numeric claims vs nearby
code — plus a semantic read of two batches of files. What is left:

- **The per-file semantic read is only two batches deep.** Drift whose *prose*
  misdescribes the logic without naming anything stale surfaces to no
  mechanical detector; only reading each file against its code finds it. Batch
  1 (`internal/query/executor.go`, `internal/tui/app_events.go`,
  `permission_gate.go`, `prop_grid_helpers.go`,
  `internal/tuikit/propsheet/form.go`,
  `internal/tuikit/controls/datagrid_input.go`) found no drift; batch 2
  (`query_store_panel.go`, `activity_monitor.go`, `log_viewer.go`,
  `explorer_object_ops.go`, `app_panel_actions.go`, `detail_browser.go`) found
  three wrong counts, each a number whose subject is a table two files away.
  **Batches 3 and 4 have not been read** — `internal/tuikit/controls/datagrid.go`,
  `editor*.go`, `treeview.go`, `internal/tuikit/propsheet/rows.go`,
  `internal/tuikit/layout/panel_manager.go`, then everything else.

- **Explicitly out of scope, and not to be re-proposed**: the 39 long comment
  blocks `CLAUDE.md` § Coding conventions protects, and the failure-naming
  comments in `query_store_panel.go`, `prop_grid_helpers.go` and similar. Each
  stops a regression a plausible simplification would reintroduce; trimming
  them is the opposite of what that section asks.

## Deferred scope (repeatedly, deliberately)

- **Windows / Microsoft Entra (Azure AD) authentication**, in Login Properties,
  New Login, and the External Provider login type generally. gosmo-side work
  needed first. Re-deferred on every properties/dialog pass; this is the
  standing answer to "why isn't this in the UI?".

## Left open by the 2026-08-14 cross-repo review

The pass fixed the `RevertFn` grid redraws and gave `internal/fileutil` its
first tests. These are what it found and did not act
on. Nothing here is urgent; the first two are small and self-contained.

- **Merging the two user-mapping page builders is a deliberate non-goal.**
  Both pages were converted to `wireGridEditor`, which is where the duplication
  that actually caused a bug lived; merging the two page *builders* was costed
  and rejected (six injection points, two different row structs, two unrelated
  applies). Do not re-propose the merge without new evidence.
- **`Form.Revert()` is exposed, not retired.** `Ctrl+Z` on a `PropertySheet`
  calls `RevertPage`, which reaches `Form.Revert`, every row's `Revert` and all
  21 `RevertFn` closures. Two rules came with it, both easy to undo by
  accident: **`Ctrl+Z` is handled ahead of the zone switch, beside `F5`** — a
  sheet-level command, so it works from the page list and the button row, and
  the focused row never sees it. And **`Ctrl+Z` must stay free inside a form
  row**: `widgets.InputField` takes `Ctrl+A`/`Ctrl+U` and no propsheet row
  hosts a `controls.Editor`, which is the one widget with a `Ctrl+Z` of its
  own. A row that ever embeds a full editor takes this key back and needs a
  different one.
- **`ServerConn.Peer` blanks `Opts.Database`, deliberately.** A database named
  in the connection string fails the *connect*, at ping time, and everything
  `Peer` reaches is server-scoped. The other three `sc.Opts` clones — the query
  panel's and the Activity Monitor's two — target the same instance and keep
  the database on purpose; this rule is about the cross-instance clone only.
## Left open by the 2026-08-22 cross-repo review

Both items below were taken to a decision and implemented the same day. What
remains of each is recorded here.

- **The per-set append in `scanPlanXML` is not live-testable in either
  direction.** Every showplan result set holds exactly one row — probed against
  win10cli over seven batch shapes under both SET options, and pinned by
  `TestLivePlanEveryShowplanSetHoldsOneRow` — so an overwriting `scanPlanXML`
  passes every live test. `TestScanNextKeepsEveryShowplanRow` (a scripted
  driver) is the only thing that kills that mutant, and the cross-*set* append
  is the only half the live tests pin. Both verified by mutation.
- **Entra logins stay unverifiable here.** `CREATE LOGIN ... FROM EXTERNAL
  PROVIDER WITH OBJECT_ID` is emitted, and its grammar is confirmed on a real
  server: on win10cli (no Entra) it and the bare `FROM EXTERNAL PROVIDER` fail
  with the *same* Msg 37525, so the parser accepted both. Whether a login is
  actually created needs an Entra-joined instance — see § Deferred scope.

  Two things settled live and worth keeping. Neither `DEFAULT_DATABASE` nor
  `DEFAULT_LANGUAGE` can be set for a certificate- or asymmetric-key-mapped
  login, in CREATE *or* ALTER, so the page refuses either rather than creating
  the login and then failing. And `masterMappableNames` swallows its two reads'
  errors on purpose: `sys.certificates` and `sys.asymmetric_keys` need
  permission on master, and a login without it must still be able to create
  ordinary SQL and Windows logins — an empty picker becomes a refusal naming
  what is missing, not a broken dialog.
## Left open by the 2026-09-02 cross-repo review

The three defects (§1 audit rename, §2 ungated Delete/Rename on server-level
nodes, §3 the doubled width scan) and §A/§B/§C are implemented. What was
declined, and why:

- **`charts.HistoryChart.Draw` and `StackedHistoryChart.Draw` stay, though
  nothing in the binary reaches them.** The review flagged them as dead code
  with the check attached: whether `doc.go` advertises `Draw` as the entry
  point. It does — "every chart draws into a `tcell.Screen` through a
  `core.Rect`" — and all six chart types implement `Draw`. The dashboard uses
  `DrawFrame` only because it also wants the time row, which `DrawFrame`'s own
  comment says. Removing two of six would break the package's one uniform
  method for four lines.
- **The five `staticcheck` U1000 findings in `clipboard_host_test.go` and
  `dialog_gesture_test.go` are suppressed, not deleted.** The review said
  delete. They cannot be: the fields are read by the reflection walk each test
  exists to exercise, so `type page`/`field p`/`field row` *are* the fixture —
  deleting `p` leaves `lazy` with nothing behind the nil pointer and the test
  asserts nothing. Each carries a `//lint:ignore U1000` naming the reader.
- **`rightAlterAnyLinkedSrv` and `rightCreateTable` still read as unused**, as
  the review recorded. Deliberate; pinned by `permission_gate_names_test.go`.
- **`core.NewRect`, `core.JoinPath` and `activity.WaitCategory.String` are
  genuinely idle** and left alone — `tuikit` and `internal/activity` are
  gossms-internal, so removal would be allowed, but the review costed it as
  low value either way.
- **The `internal/tui` package split is not attempted.** § Architecture of the
  review has the cost; nothing here changes it.

## Permission gating: what P3 deliberately leaves out

Added 2026-08-25 with the write-path phase of the permission work. Each of
these is a known limit of the gate layer, not an oversight.

- **A schema *node* is deliberately excluded from the schema-scoped gate.**
  `objectOpRights` names `rightAlterOnSchema` beside the three database-wide
  rights, but ALTER on a schema does not permit dropping or renaming the schema
  itself.
- **A DENY at object scope under a wider grant is closed** (2026-09-01), and
  what is left of it is a limit on the *shape* of the securable. `objectDenial`
  (`permission_gate.go`) asks gosmo's `DeniedOnObject` before the rights the
  action lists, because SQL Server resolves an object-scope DENY over every one
  of them: a login with database-wide `ALTER` and `DENY ALTER` on one table read
  1 for every right the gate asked about, so Rename/Move/Delete were offered and
  then failed Msg 297 — reproduced live and A/B'd against a pre-fix binary. The
  withheld item says `ALTER denied on this object` rather than naming a right the
  login already holds, and the page banner says the same.
  Three facts it rests on, all verified live 2026-09-01 and each a wrong gate if
  assumed the other way: **db_owner is not exempt** (it reads
  `HAS_PERMS_BY_NAME` 0 on the denied table) but **sysadmin is**, and the probe's
  principal set includes `public`, so a DENY to public is recorded for a sysadmin
  whose write the server still allows — hence the explicit sysadmin bypass. And
  **ownership needs no exception**: SQL Server refuses a DENY aimed at the owner
  of a securable, and `ALTER AUTHORIZATION` *deletes* an existing DENY row as it
  transfers ownership, so an owner never carries one. An owner denied through
  `public` is genuinely refused by the server, which is what the gate then
  reports.
  **What remains open**: only object-class securables have a DENY row to read.
  `ProbedObjectPermissions` is `ALTER` alone and gosmo's object block reads
  `class = 1` with `minor_id = 0`, so a DENY on a *column* (class 1, minor_id > 0,
  deliberately excluded — it would read as a denial on the table) and a DENY at
  any other class the tree acts on is still invisible, and the wider grant still
  answers for it. Nothing gossms writes today is column-scoped.
- **The `xp_dirtree` silent-empty guard is unit-tested only.** Both test
  servers are 2017 or later and so never take that path;
  `legacyListingRefusal` is exercised across all five combinations by test but
  has never run against a real pre-2017 instance.

## Detach / Attach

- **`xp_cmdshell` is on on win10cli and can never be on on ubusql1.**
  Turned on there 2026-08-26 to move database files on the server host, and
  left on for the next such test. `sp_configure 'xp_cmdshell', 1` on the Linux
  instance fails Msg 15392 — "not supported by this edition" — because SQL
  Server on Linux has no xp_cmdshell at all; file moves on ubusql1/ubusql2 go
  through ssh instead. Anything in gossms that would depend on xp_cmdshell is
  therefore Windows-only by construction.

  **The one place that showed was Server Properties > Advanced, and it is
  fixed** 2026-08-27. Linux still *lists* `xp_cmdshell` in `sys.configurations`
  at 0 (confirmed on ubusql1), so the option was not caught by the
  missing-option path `newConfigBoolEditor` already had: the checkbox rendered,
  ticked, and failed on OK with the server's raw Msg 15392. `xpCmdshellRow`
  (`server_props_advanced.go`) reads `ServerInfo.Platform` — which gosmo parses
  from `@@VERSION`, so it costs no query — and hands back a disabled "Not
  available on Linux" row instead. `TestXpCmdshellIsNotEditableOnLinux` pins it
  and `TestXpCmdshellStaysEditableOnWindows` guards the other direction, over a
  new `newFakeConnOnLinux`.

## Server-level families (Phase 3 item 12): what is deliberately out of them

Added 2026-09-02 with Credentials, Backup Devices, Server DDL Triggers,
Endpoints, Audits and Server Audit Specifications. Each of these is a decision
taken while building those six, not work that was forgotten.

- **Viewing audit records is absent.** `sys.fn_get_audit_file` is a feature the
  size of the Log File Viewer — a reader, a grid, a filter, paging over files
  the audit rolled over. SSMS's "View Audit Logs" command is therefore not
  offered at all rather than offered and empty.
- **Database Audit Specifications** are `todo.txt` item 15, not part of item 12.
  gosmo covers the server-scope half only; `sys.database_audit_specifications`
  has no reads.
- **Database-scope DDL triggers** (`parent_class = 0`) are item 13.
  `Database.triggersWhere`'s `parent_class = 1` was deliberately left alone —
  widening it would change what the existing per-database Triggers folder
  lists.
- **Cryptographic Providers** get no folder, and **database-scoped credentials**
  (`sys.database_credentials`) get no reads. A credential's provider *binding*
  is shown and scripted; registering a provider is not offered.
- **Creating TSQL, Service Broker and SOAP endpoints** is out. Endpoints are a
  read/state/drop family; the existing New Database Mirroring Endpoint dialog
  stays the only creation path, and SSMS's multi-page New Endpoint dialog is
  not reproduced.
- **Tape and virtual backup devices** are listed, scripted and dropped but not
  creatable — the New Backup Device dialog offers disk only, as SSMS's does.
- **An audit's destination cannot be changed from Properties.** ALTER SERVER
  AUDIT does accept a new `TO` clause on a disabled audit, but switching a FILE
  audit to a Windows log discards the whole file block and starts a new audit
  file. SSMS greys it and so does goSSMS; gosmo's `AlterContext` still accepts
  any destination, because the library is not the UI.
- **Neither audit object offers Rename from the tree.** A server audit
  specification has no `MODIFY NAME` form at all — it is a parse error, not a
  permission failure. An audit has one, but only while disabled, so a tree
  rename would silently stop auditing for its duration; the rename lives on the
  Properties page, where the disable is visible in Script Changes.
- **Audit Properties is one page**, where SSMS has General and a separate
  Filter tab. `ALTER SERVER AUDIT` replaces every setting at once, so a second
  page with its own apply would either write a second ALTER reverting the
  first, or need the filter's value before the page holding it had been opened.
- **No Object Explorer filter is offered on any of the six folders.** Adding
  one later means `nodeFilter.pushdown` has to reproduce
  `filterChildren`/`filterObjects` exactly or refuse — see CLAUDE.md.
