# Open threads

Work that was found, decided, or deferred but not finished. Aggregated
2026-07-30 from notes scattered across 22 session records (now in
`docs/journal.md`); re-verified against the code and pruned 2026-08-06.

Keep this file current: close an item by deleting it, and add one whenever
something is knowingly left undone. An open item recorded only in a session
note is invisible by the next session.

**This file holds only open work.** Fixed items do not accumulate here —
record a fix in `docs/journal.md` instead. The "do not re-raise" sections
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
  environment and distribution caveats.

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
  the expected consequence. As of `v0.0.7` the pair is commented out and
  `require` names gosmo `v0.0.9`; re-activate both when work resumes.

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
  `ag_listener_props.go` and `new_endpoint_dialog.go`. See `docs/journal.md`.
  What is left is scoped out rather than unfinished:

  **New Database Mirroring Endpoint does not script.** The dialog runs its
  pipeline for real; there is no Script Changes equivalent of
  `NewAGDialog.annotateScript` for it. Under a `WithScript` context the
  certificate exchange cannot work at all — `Certificate.Encoded` has nothing
  to read back, since nothing was created — so the pipeline stops after the
  statements it can emit rather than producing a script that looks complete and
  is not. Making it scriptable properly means emitting the peer's half as
  `FROM BINARY` literals read live while the rest is captured, which is a
  different mode, not a flag.

  **The create dialog has no Read-Only Routing page**, unlike SSMS's. Routing
  is a per-replica setting that AG Properties already covers, and it is
  reachable two clicks after the group exists.

  **The endpoint dialog assumes a full mesh and one shared master key
  password.** Every instance gets every other instance's certificate, which is
  what an availability group needs and more than a plain mirroring pair does;
  and the one password field is used for whichever instances turn out to have
  no database master key yet, rather than one per instance.

  **A replica that needs different credentials cannot be added.**
  `db.ServerConn.Peer` reuses the tree connection's login for every instance,
  so a topology with per-instance credentials surfaces as a connect error when
  Add Replica runs. Same limitation the Object Explorer's follow-the-primary
  already has.

  **A partly created group is left as it is**, and so is a partly added
  replica. If CREATE succeeds and a secondary then fails to JOIN, the error
  names the instance and says the group exists, but nothing is rolled back —
  dropping a group the user asked for on the strength of one unreachable peer
  would be worse. Reproduced live: with the test cluster's endpoint certificate
  broken, the dialog created the group, reported `availability group "AAG2" was
  created, but ubusql2 could not join it`, and left both halves visible. Add
  Replica makes the same choice for the same reason, in the same wording.

  **An added replica's endpoint URL is editable as of 2026-08-13**, so an
  instance whose short name the other replicas cannot resolve can be given its
  FQDN. Connect still fills it and is still required — it is what proves the
  endpoint exists and is STARTED. What is left open is coverage: the only other
  instance is already a replica, so `connect` refuses it before reading an
  endpoint, and neither Connect filling the row nor `validateEndpointURL`'s
  refusal has been seen live. Both are unit tested; showing either needs a third
  instance.

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

  **Add Database does not check for a full backup.** Full recovery, online and
  not-already-in-a-group are checked; the backup-chain prerequisite is stated
  in the dialog and left to the server's own error, because checking it is a
  per-database query into `msdb`. SSMS's wizard both checks it and offers to
  take the backup.

  **The unreachable-primary fallback is unit tested but never exercised
  live.** `resolveAGView` degrades to the partial local view and flags it, but
  no test has actually made a replica unreachable. Note that AG Properties
  takes the opposite line — `agOnPrimary` treats an unreachable primary as an
  error, because a page that loaded from a secondary would offer edits the
  server rejects — and that path *is* only unit tested too.

  **`AVAILABILITY_MODE`/`FAILOVER_MODE` writes are unit tested, never run
  live.** Dropping the last synchronous replica of an EXTERNAL group leaves
  Pacemaker unable to promote anything, recoverable only by recreating the
  group, so `TestLiveAvailabilityGroupWrite` deliberately skips both.

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
  **fixed** (2026-08-12, see `docs/journal.md`); what is left is below.

  ~~**"Jobs Without Schedules" silently drops what it couldn't check.**~~
    **Fixed** 2026-08-13 — the report carries a Schedules column reading
    `None`/`Unknown`, and a cancelled context returns the cancellation rather
    than a page of `Unknown`. See `docs/journal.md`. The `Unknown` path has no
    live coverage: aiming a failure at one job's round trip while the others
    succeed has no easy handle.

  ~~**Duration is formatted two ways in one feature.**~~ **Fixed** — every
  duration in the app now goes through `formatHMS` (`backup_common.go`); the
  query panel's rounding near-duplicate and both `Duration.String()` sites in
  the Agent detail/reports pages are gone.

  The first two bullets are not SQL Agent bugs — they are the local face of a
  ten-site family, and fixing them here alone would leave the other seven. See
  § Dropdowns that misreport a value the list doesn't contain, below.

  Two known gaps stay as scope notes, not defects: Start/Stop Job aren't gated
  on job state (the permitted reactive-error fallback), and there's no step
  reordering (msdb has no documented procedure for it).

## Dropdowns: what is settled, and the one class left

The ten-site misreporting family found 2026-08-12 is **fixed** — one helper,
`selectPreserving`/`preservingItems` in `prop_grid_helpers.go`, generalised out
of Always On's `agSetSelect`, with `changedTo` gating every write. See
`docs/journal.md`. Two things survive it.

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

## Left open by the 2026-08-18 cross-repo review

The pass closed gosmo's write-statement coverage gap and the New-X grids'
missing `RevertFn`s (see `docs/journal.md`). These are what it found and did
not act on.

- **Four gosmo methods still have no test: `BackupHeaders`, `BackupHistory`,
  `BackupFileList`, `BackupFileListForSet`.** All four are reads —
  `RESTORE HEADERONLY`/`FILELISTONLY` and an msdb history query — so
  `WithScript` cannot capture them and only a live server exercises them. They
  *are* driven by the Restore dialog's Analyze Backup, which was verified live
  2026-07-19; what is missing is a test, not the verification.

- **Five functions in gossms are unreachable, including from tests**:
  `charts.HistoryChart.Plot`, `charts.StackedHistoryChart.Plot`,
  `charts.historySpec.plotRect`, `core.ClearRect`, `theme.SetPalette`. The last
  is advertised in `theme/doc.go` as the palette extension point, so it is a
  keep-and-document rather than a delete. gosmo's no-removal rule does not
  reach these — they are gossms's own.

- ~~**`internal/tuikit/layout` is the least-covered tuikit package (46.6%)**~~
  **No longer true** — re-measured 2026-08-20 at **73.7%**, second-highest in
  tuikit, after the drop-down scroll work and its tests. The three below it are
  now `core` (60.5%), `dialogs` (61.0%) and `propsheet` (62.5%); `theme` is 0%
  and stays there, being a palette table. The point behind the original entry
  still stands and is worth keeping: `layout` is where `Splitter` and
  `PanelManager` keep their drag latches, the class of bug that has shipped
  here before — most recently `comboSbDragging` surviving a close (fixed
  2026-08-20). **`internal/activity` (54.8%) is the real gap now**, with the
  collector's backoff/pause/stop paths uncovered. Unit-testable with no server
  and no terminal.

- **The Properties pages now have a seam, and fourteen pages use it.** The
  `propPage` load/apply closures were unreachable from a unit test for as long
  as `gosmo.Server` could only come from a real connection: both halves open
  with a by-name read, and `gosmo.WithScript` intercepts writes only, so the
  script-collector harness the New-X dialogs use fails on the first query.
  Closed 2026-08-20 by `gosmo.NewServer(ctx, *sql.DB)`, plus
  `internal/tui/fakedb_test.go` — a scripted `database/sql` driver, a
  `newFakeConn` that hands back a `*db.ServerConn`, `loadPage`, and `textRow`
  for addressing a form by label. `database_props_files_page_test.go` is the
  worked example: it drives the real load and apply closures and reads back the
  statements that reached the server. The harness grew three things on
  2026-08-21, each because a page could not be driven without it: a
  `fakeResponse` can be scoped to the database a `USE` has pinned the
  connection to (`db:`) or to a query parameter (`arg:`, without which a
  by-name read is served the list read's answer and every object resolves to
  whichever row sorts first); `StatementsIn(db)` says *where* a write landed,
  which is half the meaning of one; and `plainGrid`/`selectGridRow`/
  `activateGridCell` drive a `controls.DataGrid` page by the keys a user sends,
  since `SetSelectedRow` deliberately does not fire `OnSelectRow`.

  **Fourteen pages are done**, chosen for what their apply can destroy: Files,
  Options (the twenty-one label/DatabaseOption pairs, plus Restrict access
  staying off that path so it keeps WITH ROLLBACK IMMEDIATE), Login > Status
  (CONNECT SQL grant/deny/revoke and enable/disable), Login > Server Roles
  (which role a tick grants), Securables (the grant/deny/revoke matrix, its
  WITH GRANT OPTION and CASCADE transitions, and the filter's row mapping),
  Filegroups (two toggle columns, and Remove against a shifting visible list),
  and, 2026-08-21, Login > User Mapping (which database a user is created or
  dropped in, and the schema/role edits that follow the row they were typed
  on), Database Scoped Configurations (the eleven label/option pairs and the
  OFF/ON keyword), Server Properties Memory / Processors / Advanced (the
  twenty-six label/sp_configure pairs, the affinity bit under each named
  processor, and RECONFIGURE only when something changed), Database and Server
  Role Members (which of the two names in ALTER ROLE is which), User
  Membership, and Change Tracking.

  **What is still open is the other 35 write pages.** The harness costs one
  `fakeResponse` per query a page reads, matched by substring, and a page whose
  script is incomplete fails naming the query it missed. Nothing on the
  "destructive or silent" list is outstanding any more; what remains is the
  ordinary tail — Agent job steps and schedules, the New-X dialogs' pages, the
  AG pages, Index/Table/Statistics Properties. Not worth doing for a page that
  only reads.

  Two shapes recur and are what these tests are actually for. First, a page
  builds a grid from a slice and reads it back by index — always assert on the
  *name* in the row, never on the index, or the test agrees with a page that
  has them misaligned. Second, a page keeps a filtered or pending-removal
  subset alongside the full list; the index hazard only appears once the two
  diverge, so a single-item test passes on the broken version. See
  `TestRemovingTwoFilegroupsRemovesBothTheOnesSelected` for that one, and
  `TestDatabaseRoleMembersRemovesBothOfTwo` for the same thing on the shared
  membership form. A third, added 2026-08-21: the object a test acts on must
  not be the *first* one in the list, or a page that ignores the selection
  entirely passes. That was a real missed mutant on User Membership.

  Know what it does and does not prove. Queries are answered by substring
  match, so a test here shows the page asked for the right things and built the
  right request — never that the T-SQL is valid or that SQL Server would accept
  it. Statement text is gosmo's own tests; acceptance is a live run. An
  assertion here that reaches for server semantics is asserting the fake.

  Coverage context, still current: the encoder layer inside the closures is
  done — `agent_schedule_form.go`, `agent_job_props_steps.go`,
  `database_props_files.go`, `backup_dialog.go` and `restore_dialog.go` all
  have their form-to-request encoders at 100%, done over 2026-08-20 with three
  small extractions (`planJobStepWrites`, and `fileEdit.changed`/`.modify`/
  `.spec`). See `docs/journal.md` for each, including the one thing a
  round-trip test provably cannot catch. What remains uncovered in
  `internal/tui` is the load closures the harness above now reaches, and
  draw/layout code.

- **`fileutil.WriteAtomic` preserves mode but not owner.** The rename makes the
  replacement file owned by whoever is running, where a write-in-place would
  have kept the original's uid/gid. It only bites if gossms is run as root over
  a user-owned file, which is not a case it supports; recorded so the next
  reader of `modeFor` doesn't assume ownership is handled there too.

- **`activity_monitor_proctab.go` has the app's only status strings with a
  trailing period — three of them, not one.** Re-counted 2026-08-20: `"Not
  connected."` (`:134`), `"Stopped unexpectedly — see the log for details."`
  (`:159`) and `"No result returned."` (`:273`). Every other `setStatus` in the
  app ends without one. Cosmetic, and all three are in the same file, so it is
  one edit whenever it is worth making.

## Left open by Phase 1 item 6 (2026-08-19)

- **`ScriptTable` doesn't emit a partitioned table's `ON <scheme>(<column>)`
  clause**, so scripting a partitioned table and running the script elsewhere
  recreates it on the default filegroup — silently unpartitioned. Found while
  scripting the item-6 probe database, whose `dbo.Parted` came back on
  `[PRIMARY]`. The fix needs `Table` to carry its data space (`sys.indexes`'s
  `data_space_id` for the heap/clustered index, resolved through
  `sys.partition_schemes` and `sys.index_columns` for the partitioning
  column); the same clause belongs on `ScriptIndex`.

- **Neither Always Encrypted key can be created from the tree** — no "New
  Column Master Key..."/"New Column Encryption Key..." dialog. gosmo has
  `CreateColumnMasterKey`, but its `ENCLAVE_COMPUTATIONS = YES` spelling looks
  wrong (the real syntax is `ENCLAVE_COMPUTATIONS (SIGNATURE = 0x…)`) and was
  not exercised live, since the probe keys were created by hand. Verify before
  building anything on it.

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

- **The peer-certificate mismatch check is not exercised live.**
  `importPeerCertificate` compares `Certificate.Thumbprint` before skipping an
  import (2026-08-13), so a peer that was rebuilt under the same instance name
  is named rather than silently left with a stale certificate. Reproducing it
  needs two instances and one of them reinstalled; only the same-thumbprint
  path has ever run.

- **The named-instance principal names are handled but have never run live.**
  Fixed 2026-08-13: `endpointPrincipalBase` maps `@@SERVERNAME`'s backslash to
  `$`, so a named instance contributes `HOST$INSTANCE_login` rather than
  `[HOST\INST_login]`, which is the spelling of a Windows principal. It is
  deliberately not truncated to the host the way `endpointURL` does — two named
  instances on one machine would then share every principal name in the
  exchange. `TestEndpointPrincipalBaseSurvivesANamedInstance` is the only thing
  pinning it; the test cluster is all default instances, so no named instance
  has ever gone through the exchange.

- **The endpoint dialog's own branch is still only compiler-checked.**
  `importPeerCertificate` needs a live `*gosmo.Server` — a concrete struct with
  no interface to fake — so the three-way switch has no unit test. The gosmo
  behaviour underneath it is covered by `live_notfound_test.go` (five tests,
  `-tags livedb`). Driving the dialog end to end still needs two instances and
  has not been done.

## Log File Viewer: what is deliberately out of it

Built 2026-08-12 (see `docs/journal.md`). Three things SSMS's own viewer has
were left out on purpose, not forgotten:

- **One log file at a time, no merged view.** SSMS's left pane checkboxes let
  several logs (and the Windows event log) be merged into one date-sorted grid.
  The two selectors were chosen instead; merging means a source column, a merge
  sort, and N reads per refresh.
- **The filter is client-side.** `xp_readerrorlog` takes two search strings and
  a date range as arguments 3-6, which is what SSMS's "Filter…" uses. Filtering
  in the panel re-filters instantly with no round trip and is honest about how
  many of the entries read match; a server-side filter would be needed only for
  a log too large to hold, which is also when `logReadTimeout` starts to matter.
- **No Enter / double-click on a log-file leaf.** `controls.TreeView` has no
  activation callback at all (`Enter` is `toggleExpand`), so a log leaf opens
  from its context menu like every other leaf in the tree. Adding `OnActivate`
  to TreeView would be a tuikit change affecting every node type.

Also not built: the Windows event log (needs WMI, out of scope for a no-CGO
portable build) and `sp_cycle_errorlog` / `sp_cycle_agent_errorlog` as a
"Recycle" action — gosmo has `CycleErrorLog`, but nothing in the UI calls it.

## Object Explorer folder filter: what is deliberately out of it

Built 2026-08-13 (see `docs/journal.md`). Two gaps left, both deliberate; the
other two were fixed 2026-08-15.

- **The filter is client-side.** SSMS pushes it into the folder's own query;
  here the loader fetches the folder as usual and `fetchChildren` drops the
  rows the filter rejects. A folder large enough for that to matter is a folder
  whose unfiltered expand is already slow.
- **Owner and Durability Type are not offered on Tables**, though SSMS offers
  both — each is one `TableDetail` query per table. Adding them means a
  folder-wide detail fetch first.
- ~~**Object Explorer Details ignores the filter.**~~ **Fixed** 2026-08-15 —
  every detail loader now filters what it lists: `filterObjects` for the ones
  holding gosmo objects, `filterChildren` for `fetchChildObjectsDetail`, which
  holds `*explorerNode` already. See `docs/journal.md`.
- ~~**Filters are per-session, not persisted.**~~ **Fixed as far as SSMS goes**
  2026-08-15 — `App.savedFilters`, keyed by `filterKey` rather than by node
  pointer, brings a folder's filter back on a reconnect within the session.
  Writing filters to `config.json` so they survive an exit stays out: SSMS
  keeps them for the session only, and restoring one at startup against a
  folder whose objects have since changed is not wanted.

## Delete/Rename: what is deliberately out of it

Built 2026-08-13 (see `docs/journal.md`).

- **No column, no partition, no filegroup Delete.** SSMS deletes a column from
  the Columns folder; that is an `ALTER TABLE DROP COLUMN` with its own
  constraint/index preconditions, not a member of the one-statement family the
  table covers.
- **No cascade.** A table is dropped with `cascade=false`, so a referenced
  table is refused by the server until its foreign keys are dealt with — the
  same refusal SSMS gives. gosmo's `DropTableContext` can cascade; nothing in
  the UI asks for it yet.
- **Agent objects keep their own Delete** (`agent_menu.go`), whose per-type
  wording explains what blocks each one; only Rename comes from the shared
  table. Availability groups likewise.
- **No multi-select delete.** `controls.TreeView` has a single selection, so
  SSMS's "Delete Object" dialog listing several objects has no equivalent.
- **Rename does not move an object between schemas** — `sp_rename` takes a
  bare name, and moving is `ALTER SCHEMA ... TRANSFER`, which Object Explorer
  does not offer (Schema Properties' owner transfer is a different operation).

## Clipboard in a dialog: what is deliberately out of it

The `core.ClipboardHost` fix of 2026-08-20 (see `docs/journal.md`) gave every
dialog with a text field a working Ctrl+C/X/V and stopped the rest from
reaching past themselves to the query editor. Two consequences are deliberate,
not regressions:

- **A dialog with no text entry now has an inert clipboard.** Help, About /
  Object Dependencies, Confirm, Query List and Background Tasks do not
  implement the interface, so Ctrl+C there does nothing. It previously copied
  whatever the panel behind the dialog had selected, which was never the thing
  the user was looking at.

- **Key Diagnostics cannot copy its log**, and that is the one place the
  omission is worth revisiting. Its whole purpose is reporting what a terminal
  actually sent, which is exactly the text someone would want to paste into a
  bug report — but it draws its log directly rather than holding a
  `controls.Editor` the way Status History does, so there is no target to
  hand back. Giving it one is a feature, not part of the fix. Status History
  *can* copy, for that reason.

## Deferred scope (repeatedly, deliberately)

- **Windows / Microsoft Entra (Azure AD) authentication**, in Login Properties,
  New Login, and the External Provider login type generally. gosmo-side work
  needed first. Re-deferred on every properties/dialog pass; this is the
  standing answer to "why isn't this in the UI?".

## Left open by the 2026-08-14 cross-repo review

The pass fixed the `RevertFn` grid redraws and gave `internal/fileutil` its
first tests (see `docs/journal.md`). These are what it found and did not act
on. Nothing here is urgent; the first two are small and self-contained.

- ~~**`fileutil.WriteAtomic` overwrites an existing file's permission bits, and
  replaces a symlink instead of writing through it.**~~ **Both fixed** — the
  mode half 2026-08-14 (`modeFor` keeps the existing file's mode with `perm` as
  a ceiling), the symlink half 2026-08-15 (`resolveSymlink` runs before the temp
  file is placed, so both it and the rename land in the target's directory). See
  `docs/journal.md`.

- ~~**`allDialogs` completeness is unpinned.**~~ **Fixed** 2026-08-14 —
  `dialog_registration_test.go` reflects over `App`'s dialog-typed fields and
  fails naming any that `buildUI` forgot. See `docs/journal.md`.

- ~~**`App.loadChildren` and `DetailBrowser.fetch` use `safego` where the rule
  says `safegoRepair`.**~~ **Fixed** 2026-08-14, across all five detail loaders
  as well as the expand. See `docs/journal.md` — including what the Object
  Explorer half deliberately gives up (a panicked expand now needs Refresh to
  retry, where before it retried on a re-expand).

- ~~**The user-mapping page exists twice.**~~ **Addressed differently, and the
  page merge is now a deliberate non-goal.** Both pages were converted to
  `wireGridEditor` 2026-08-14, which is where the duplication that actually
  caused a bug lived; merging the two page *builders* was costed and rejected
  (six injection points, two different row structs, two unrelated applies).
  See `docs/journal.md`. Do not re-propose the merge without new evidence.

- ~~**`Form.Revert()` has no non-test caller, and fourteen pages implement
  `RevertFn` for it.**~~ **Decided and done** 2026-08-15: Revert is exposed,
  not retired. `Ctrl+Z` on a `PropertySheet` calls `RevertPage`, which reaches
  `Form.Revert`, every row's `Revert` and all 21 `RevertFn` closures. See
  `docs/journal.md`.
  The two rules that came with it, since both are easy to undo by accident:
  **`Ctrl+Z` is handled ahead of the zone switch, beside `F5`** — a sheet-level
  command, so it works from the page list and the button row, and the focused
  row never sees it. And **`Ctrl+Z` must stay free inside a form row**:
  `widgets.InputField` takes `Ctrl+A`/`Ctrl+U` and no propsheet row hosts a
  `controls.Editor`, which is the one widget with a `Ctrl+Z` of its own. A row
  that ever embeds a full editor takes this key back and needs a different one.

- ~~**Open question: `ServerConn.Peer` copies `Opts.Database` to the peer.**~~
  **Answered and fixed** 2026-08-14 — a probe against win10cli showed a
  database named in the connection string failing the *connect*, at ping time,
  so `peerOptions` blanks it; everything `Peer` reaches is server-scoped. See
  `docs/journal.md`. Note the other three `sc.Opts` clones — the query panel's
  and the Activity Monitor's two — target the same instance and keep the
  database on purpose; this rule is about the cross-instance clone only.

- ~~**Schema Properties' Object summary fetched five database-wide listings.**~~
  **Fixed** 2026-08-20 — the General page counted views, procedures, functions,
  synonyms and sequences by listing all of them and matching `Schema` in Go,
  pulling every view and procedure definition across the wire on the way.
  gosmo gained `Schema.ObjectCountsByType`/`Context`; the page makes one round
  trip for all six numbers. Six scalar subqueries rather than a `GROUP BY` over
  `sys.objects`, because the listings' predicates differ in more than the type
  code — see `docs/journal.md`. Covered by `live_schemacounts_test.go`
  (`-tags livedb`), which asserts the query equals the listings-and-scan on
  three schemas.

- ~~**`Scripter.ScriptDatabase` had no `Context` sibling.**~~ **Fixed**
  2026-08-20, and it turned out not to be a symmetry question: the method
  renders from the `Database`'s cached metadata, so a bare
  `Server.Database(name)` handle scripted as `SET RECOVERY ;` and
  `COMPATIBILITY_LEVEL = 0` — invalid T-SQL. `ScriptDatabaseContext` refills
  the handle from `sys.databases` first, and each line is still guarded on its
  own value for the OFFLINE case where the catalog itself returns NULL. gossms
  was never affected (its `ddl` helper always goes through
  `DatabaseByNameContext`) but is now uniform with the other twenty-six
  entries in `scripting.go`'s table. Covered by `scripter_database_test.go`
  and `live_scriptdatabase_test.go`, which replays the generated script against
  a dropped database. All 27 `Script*` methods now have both forms.

- ~~**A click never fires `OnSelectRow` on a cell-cursor grid.**~~ Found
  2026-08-20 while live-testing the redrawGrid column-width fix, held back
  from that change, and **fixed** the same day on its own.
  `datagrid_input.go`'s `Button1` handler has two exits: the
  cell-cursor one (~:255) sets `selRow`/`selCol`, calls `activateCell()` and
  returns `true`, and the plain-row one below it fires `OnSelectRow`. So on a
  grid with `SetCellCursor(true)`, clicking a row moves the highlight but never
  tells the page — a detail panel wired to `OnSelectRow` goes on describing
  whatever row the keyboard last left it on. Verified live on Job Properties >
  Schedules: click row 2, the highlight moves and "Selected schedule" still
  reads row 1; press Down and it catches up. Eleven pages wire `OnSelectRow`
  onto a cell-cursor grid and all of them were affected. The fix fires
  `OnSelectRow` before `activateCell` — the order the keyboard already uses —
  and only when `selRow` actually moved, so a page that redraws from inside
  `OnActivateCell` is not re-entered on every toggle of the row it is already
  on. Three tests in `datagrid_cellcursor_test.go`; see `docs/journal.md`.

- **`logStatus` writes the Status History ring from background goroutines.**
  Found 2026-08-19 in the same sweep that produced `explorerNode.snapshot`, and
  deliberately not fixed with it — the loader race and this one are separate,
  and widening the change would have put an untested dialog on the same diff.
  `fetchChildren` calls `a.logStatus` on the loader goroutine
  (`explorer_loaders.go`), and `logStatus` calls
  `statusHistoryDialog.Record(msg)`, which appends to a slice the UI goroutine
  also reads and draws. Same class as the loader race: it is UB, not a stale
  read, and no test drives both sides so `-race` is quiet. Whichever way it is
  fixed — a mutex on the ring, or routing the log through `postAndWake` —
  check the other background `logStatus` callers, not just this one.

- ~~**Five families are looked up by scanning the whole collection.**~~
  **Fixed** 2026-08-19. gosmo gained the five finders it was missing —
  `PartitionFunctionByNameContext`, `PartitionSchemeByNameContext`,
  `SecurityPolicyByNameContext`, `ColumnMasterKeyByNameContext`,
  `ColumnEncryptionKeyByNameContext` — additively, with every bulk listing
  kept, and the five gossms helpers are now wrappers that only save the
  `DatabaseByNameContext` step. The `==` comparison went with the scan:
  matching happens in SQL, under the server's collation. Covered by
  `live_bynamefinders_test.go` (`-tags livedb`) in gosmo, which asserts each
  finder agrees with its listing field by field. See `docs/journal.md`.
  One deliberate narrowing recorded there: `findSecurityPolicy` no longer
  treats an empty schema as "match any".

  **Second batch fixed** 2026-08-20 — the other five, which that pass left
  behind: `findSchema`, `findIndex`, `findStatistic`, `findForeignKey` and the
  Change Tracking page's table lookup. gosmo gained
  `Database.SchemaByNameContext`, `Table.IndexByNameContext`,
  `Table.StatisticByNameContext`, `Table.ForeignKeyByNameContext` and
  `Database.TableChangeTrackingForContext`, again additively. `IndexByName`
  keeps `IndexesContext`'s two-query shape rather than collapsing it, and
  `TableChangeTrackingForContext` keeps "tracking off" as a value rather than
  an absence. Covered live on both sides —
  `TestLiveSchemaAndTableChildFindersMatchTheirListings` in gosmo and the new
  `internal/tui/live_propfinders_test.go` here. No by-name lookup in either
  repo now scans a listing.

- ~~**Every dialog is registered twice.**~~ **Fixed** 2026-08-19 —
  `registerDialog` (`app.go`) appends to `allDialogs` and hands the dialog
  back, so `buildUI` writes `a.x = registerDialog(a, NewX(a))` and there is no
  second list to forget. `TestEveryAppDialogFieldIsRegisteredInAllDialogs`
  stays: the helper makes skipping registration awkward, not impossible.
  Registration order is unchanged, which matters only as the same-tick
  tie-break `syncDialogStack` uses. See `docs/journal.md`.
