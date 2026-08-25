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
  environment and distribution caveats. Three of the four were closed in
  `v0.0.8` — Start/Stop Job read the job's state, Move Up / Move Down are on
  Job Properties > Steps, and a peer is reached with its own saved
  credentials; the no-rollback of a partly created group stands.

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
  the expected consequence. As of `v0.0.8` `require` names gosmo `v0.0.10`;
  comment the pair out for the release build and re-activate both when work
  resumes.

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

  ~~**New Database Mirroring Endpoint does not script.**~~ This was out of
  date and the real state was worse: `newObjectDialog.init` sets `OnScript`
  unconditionally, so the button was live and running the shell's version —
  one flat batch spanning two or more instances, with nothing saying only part
  of it runs here. **Fixed** 2026-08-21.

  Each instance now collects its own statements (`endpointPeer.ctx`/`script`,
  one `gosmo.WithScript` per peer), which is what makes the grouping possible
  at all: the pipeline is three phases over N instances with a skip possible at
  every step, so the positional mapping `NewAGDialog.annotateScript` uses —
  whose own comment admits how fragile it is — would not work here. Reads still
  go to the real servers, which is required rather than incidental: a
  certificate's public key can only be read from the instance that holds it, so
  a certificate that already exists scripts as a complete `FROM BINARY` import.

  Where one does not exist yet the script says so, naming the instance and the
  imports each other instance is therefore missing, and everything it does
  contain is runnable. Two statements had to be suppressed to keep that true —
  the `GRANT CONNECT` to a login the skipped phase never created, and a
  `CREATE USER` for a user that already exists. The second was found live: the
  pipeline created the user unconditionally and tolerated the already-exists
  error, which works when the statement actually runs and not under
  `WithScript`, where it never does. `importPeerCertificate` now looks the user
  up first, the way it already did the login.

  **The create dialog has no Read-Only Routing page**, unlike SSMS's. Routing
  is a per-replica setting that AG Properties already covers, and it is
  reachable two clicks after the group exists.

  **The endpoint dialog assumes a full mesh and one shared master key
  password.** Every instance gets every other instance's certificate, which is
  what an availability group needs and more than a plain mirroring pair does;
  and the one password field is used for whichever instances turn out to have
  no database master key yet, rather than one per instance.

  ~~**A replica that needs different credentials cannot be added.**
  `db.ServerConn.Peer` reuses the tree connection's login for every instance,
  so a topology with per-instance credentials surfaces as a connect error when
  Add Replica runs. Same limitation the Object Explorer's follow-the-primary
  already has.~~ **Fixed** 2026-08-21 for every `Peer` caller at once, since
  `peerOptions` is the single credential-derivation point behind all nine.
  `ServerConn.SetPeerCredentials` installs a resolver it consults before
  falling back to the parent's settings, and App answers it from the
  connections the user has already made — so connecting to a replica once via
  File > Connect is how it is given a different login, port, auth method or
  TLS setting. A saved connection is taken whole rather than field by field,
  so it cannot silently stop carrying whatever `config.Connection` gains next.
  `Peer` installs the resolver on each peer it opens, so it reaches the second
  hop of the follow-the-primary path too.

  Two things worth knowing about the lookup. It is keyed by `db.InstanceKey`,
  which normalizes case, port and named-instance spelling — the catalog says
  `UBUSQL2\PROD` and the user typed `ubusql2\prod,1433`. And because
  `@@SERVERNAME` is the short machine name while what people type on a domain
  network is the FQDN, a saved connection is *also* registered under its short
  host as a strictly lower-priority tier: an exact host match always wins, so
  `sql.a.example` and `sql.b.example` never hand each other their logins.

  **The resolver may never make an instance less reachable than it was before
  one existed**, which the first version could: it preferred a saved connection
  unconditionally and was seeded from disk rather than from connections proven
  to work. Closed 2026-08-22 (see `docs/journal.md`) in two halves —
  `Peer` retries once with `parentPeerOptions`, the pre-resolver derivation,
  when the resolver's answer will not connect; and `loadPeerCredentials` does
  not seed an entry whose password this session cannot decrypt, the state
  `config.Connection.PasswordUnreadable` reports and a replaced config key
  produces for every entry at once. A saved *low-privilege* login still wins
  over the parent's on purpose — that is the feature, not a defect.
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

  **An added replica's endpoint URL is editable as of 2026-08-13**, so an
  instance whose short name the other replicas cannot resolve can be given its
  FQDN. Connect still fills it and is still required — it is what proves the
  endpoint exists and is STARTED. ~~What is left open is coverage.~~ **Covered
  live 2026-08-22** — win10cli became the third instance the entry was waiting
  for (see § win10cli as a third instance). Against AAG1 on ubusql1, Connect on
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

  ~~**Add Database does not check for a full backup.**~~ **Fixed** 2026-08-23,
  and the reason it had been left out was wrong: checking it is not a
  per-database `msdb` query, and `msdb` is not where the answer is. gosmo
  gained `Server.DatabaseRecoveryStatuses`/`Context` and
  `Database.RecoveryStatus`/`Context` over
  `sys.database_recovery_status.last_log_backup_lsn`; both Add Database and the
  New Availability Group dialog make one server-wide read and pass the result
  into the shared `agEligibleDatabases`, so a database with no started log
  backup chain is listed under "Not offered" with the fix named rather than
  offered and refused on apply.

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

  ~~**`AVAILABILITY_MODE`/`FAILOVER_MODE` writes are unit tested, never run
  live.**~~ **Covered** 2026-08-21, on the throwaway `CLUSTER_TYPE = NONE`
  group `TestLiveAvailabilityGroupCreate` already builds and drops — no cluster
  manager watches it, so the reason `TestLiveAvailabilityGroupWrite` still
  skips both on AAG1 (dropping the last synchronous replica of an EXTERNAL
  group leaves Pacemaker unable to promote anything) does not apply there.
  `AVAILABILITY_MODE` round-trips SYNCHRONOUS_COMMIT to ASYNCHRONOUS_COMMIT and
  back against the server. `FAILOVER_MODE` cannot be round-tripped anywhere on
  this cluster and the test says so instead of pretending: a NONE group accepts
  MANUAL and refuses AUTOMATIC and EXTERNAL with "The cluster type of
  availability group '...' only supports MANUAL failover mode" — a semantic
  refusal, not `Msg 102`, which is what proves the statement still reaches the
  server well-formed. AUTOMATIC needs a WSFC and EXTERNAL needs an EXTERNAL
  group, which is AAG1, the one group that must not be touched. Also pinned
  there: a refused write does not move the in-memory field, since
  `setReplicaKeyword` runs `setIfApplied` only after the ALTER succeeds.

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

  ~~**Two known gaps stay as scope notes**: Start/Stop Job aren't gated on job
  state, and there's no step reordering (msdb has no documented procedure for
  it).~~ **Both closed** 2026-08-23, and the second one's stated reason was
  wrong.

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

- ~~**Four gosmo methods still have no test: `BackupHeaders`, `BackupHistory`,
  `BackupFileList`, `BackupFileListForSet`.**~~ **Fixed** 2026-08-21 —
  `live_backupreads_test.go` (`-tags livedb`) backs two throwaway databases up
  to one device in three sets and reads all four back, including the file list
  of set *2* rather than the set a missing FILE clause would return. See
  `docs/journal.md`, including the msdb-history trap that made the first
  version of it pass only once.

- ~~**Five functions in gossms are unreachable, including from tests**~~
  **Closed** 2026-08-21, and the count was wrong in a way worth recording:
  `charts.HistoryChart.Plot`/`StackedHistoryChart.Plot` and
  `charts.historySpec.plotRect` were not dead surface but a missing assertion.
  `TimeRow`/`timeRowRect`, their exact siblings, were already reachable — as
  the independent oracle in `TestDrawFrameAgreesWithDrawAndTimeRow` — and
  `Plot` had simply been left out of it. It is in now (the test is
  `TestDrawFrameAgreesWithDrawPlotAndTimeRow`), which is what these accessors
  are for: a mouse hit-test asks where the chart's parts landed *without*
  drawing, and `DrawFrame` cannot answer that. `core.ClearRect` really was
  dead — a one-line wrapper over `FillRect(s, r, ' ', style)`, no callers
  anywhere — and is deleted. `theme.SetPalette` is kept and now covered
  (`theme/palette_test.go`), since `theme/doc.go` advertises it as the palette
  extension point for a host application. The general lesson: in a package
  that is deliberately an embeddable library (`internal/tuikit/README.md`
  opens by saying so), "no callers" is a question about the test suite before
  it is a deletion candidate.

- ~~**`internal/tuikit/layout` is the least-covered tuikit package (46.6%)**~~
  **No longer true** — re-measured 2026-08-20 at **73.7%**, second-highest in
  tuikit, after the drop-down scroll work and its tests. The three below it are
  now `core` (60.5%), `dialogs` (61.0%) and `propsheet` (62.5%); `theme` is 0%
  and stays there, being a palette table. The point behind the original entry
  still stands and is worth keeping: `layout` is where `Splitter` and
  `PanelManager` keep their drag latches, the class of bug that has shipped
  here before — most recently `comboSbDragging` surviving a close (fixed
  2026-08-20). ~~**`internal/activity` (54.8%) is the real gap now**~~ —
  **closed 2026-08-21 at 97.4%**: the gap turned out to be the DMV readers
  rather than the collector, and `internal/activity/fakedb_test.go` (a scripted
  driver keyed by each query constant) now drives `Collect` and `collectTempDB`
  end to end. See `docs/journal.md`.

- **The Properties pages now have a seam, and nineteen pages use it.** The
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

  **Nineteen pages are done**, chosen for what their apply can destroy: Files,
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
  Membership, and Change Tracking. Server Properties' Connections, Database
  Settings and Security, Query Store and Login > General went in the same day
  — see the paragraph below and `docs/journal.md`.

  **Eight more went in 2026-08-21 (phase 1)** — the Tier 1 pages whose
  apply reissues an object or rewrites a permission graph — Index Options and
  Included Columns, Key General and Key Options, the four permission matrices
  (Server Properties, Database Properties, Schema, Table) in one table-driven
  file, the database Securables page including its column editor, and Database
  Properties > General. The harness grew two things for them: `newFakeDialog`/
  `drainDialog`, which build the `*PropDialog` a page with an on-demand fetch
  button needs (its action runs through `App.safego` and reports back through
  `postAndWake`, so with no screen the posted callback has to be drained by
  hand), and parameter recording on `fakeExec` with `argsFor`/`assertArgs` —
  without which a write through `sp_rename` or any `sp_add_job*` reads as
  `@p1` and says nothing about what it acted on. See `docs/journal.md`
  (2026-08-21) for the mutants killed and the two that survived for good
  reasons.

  **The nine SQL Server Agent pages went in 2026-08-21 (phase 2)** — Job
  Properties' General, Steps, Schedules, Alerts and Notifications, Alert
  Properties' General and Response, Operator Properties > General, and
  Schedule Properties > General, over a shared `agent_fakedb_test.go`. Twenty
  mutants died, including both directions of Steps' three-pass ordering and a
  swap in `weekdayBits` — `CLAUDE.md`'s standing example of what a round-trip
  test cannot see, now pinned by ticking days by name and asserting the
  `@freq_interval` that reaches `sp_update_schedule`. One correction to the
  plan that was written here: these pages are **not** `db:`-scoped. gosmo
  addresses msdb three-part (`EXEC msdb.dbo.sp_update_job`) and never issues a
  `USE`, so every Agent write is read back with `Statements()`, and
  `StatementsIn("msdb")` returns nothing.

  **What is still open is the remaining write pages.** Five more went in
  2026-08-21 — Server Properties' Connections, Database Settings and Security
  (onto the existing label-to-`sp_configure`-name table, plus FILESTREAM's
  index-valued Select), Query Store (thirteen editors through one statement,
  and the two destructive action checkboxes), and Login Properties > General
  (the blank-password rule, the rename ordering, the credential unmap, and the
  Windows/built-in refusals). The harness costs one
  `fakeResponse` per query a page reads, matched by substring, and a page whose
  script is incomplete fails naming the query it missed. Nothing on the
  "destructive or silent" list is outstanding any more; what remains is the
  ordinary tail — the New-X dialogs' pages and the read-mostly
  Index/Table/Statistics pages. Not worth doing for a page that only reads.

  **The principal and ownership pages went in 2026-08-21 (phase 3)** — Role,
  Server Role, User and Schema General, the three owner-transfer pages, the
  shared Extended Properties page, and Table Change Tracking. Seventeen mutants
  died and none survived. Two things generalize. A page that returns a nil
  `apply` for a built-in principal is testable as such, and the assertion is
  "the row is not editable" rather than "the row is absent", since the same
  field is rendered as a `StaticRow`. And an owner-transfer page's filter —
  `Owner == this principal` — is worth a test of its own: an object that leaks
  into that list is handed away on a page the user thinks is about something
  else.

  **The three Always On pages went in 2026-08-21 (phase 4)**, which finishes the
  sweep: every `propPage` with a real `apply` outside the New-X dialogs now has
  a page test. Seventeen mutants died. Two things to know before adding another
  AG test. The fixture's primary replica must be named what
  `serverInfoResponse` calls the instance (`FAKE\SQL`) — `IsLocalPrimary`
  compares the two, and `agOnPrimary` opens a peer connection the fake cannot
  serve when they differ. And the per-replica routing-list read needs `arg:`
  scoping on the replica id, or all three replicas report the primary's list.

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

## Always Encrypted: what the two create dialogs deliberately leave out

Both dialogs went in 2026-08-21 (see `docs/journal.md`), closing "neither key
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

- ~~**The peer-certificate mismatch check is not exercised live.**~~
  **Covered** 2026-08-22, and the cost recorded here was wrong: this entry said
  reproducing it "needs two instances and one of them reinstalled". It needs
  neither. The check is `existing.Thumbprint != other.cert.Thumbprint` on a
  certificate looked up **by name**, so creating a `ubusql2_Cert` on win10cli
  with its own key material is the whole setup — one CREATE CERTIFICATE, no
  reinstall and no DROP. The dialog then refused with `win10cli already has a
  different certificate named ubusql2_Cert than the one ubusql2 presents — drop
  it there and run this again`.

  **Order the instance list so the mismatched peer is reached before any peer
  that would be written to**, which is what made this run leave nothing behind.
  `configure`'s exchange is `for p, for other` in list order, and the first
  error aborts the pipeline; with the list ordered ubusql1 → win10cli →
  ubusql2, the pair `p=win10cli, other=ubusql2` raises before
  `p=ubusql2, other=win10cli` can create a login, user and certificate on
  ubusql2. Verified: ubusql2 held no `win10cli_*` principal afterwards. The
  reverse order writes to ubusql2 first and then fails, which is correct
  behaviour but a dirtier test.

- **The named-instance principal names are handled but have never run live.**
  Fixed 2026-08-13: `endpointPrincipalBase` maps `@@SERVERNAME`'s backslash to
  `$`, so a named instance contributes `HOST$INSTANCE_login` rather than
  `[HOST\INST_login]`, which is the spelling of a Windows principal. It is
  deliberately not truncated to the host the way `endpointURL` does — two named
  instances on one machine would then share every principal name in the
  exchange. `TestEndpointPrincipalBaseSurvivesANamedInstance` is the only thing
  pinning it; the test cluster is all default instances, so no named instance
  has ever gone through the exchange.

- ~~**The endpoint dialog's own branch is still only compiler-checked.**~~
  **Driven end to end** 2026-08-22, against ubusql1 (local) and win10cli (peer).
  `importPeerCertificate` still needs a live `*gosmo.Server` and still has no
  unit test; what changed is that the whole pipeline has now run for real, on a
  peer that had nothing — no database master key, no certificate, no endpoint —
  which is the case ubusql1/ubusql2 could never produce because both were
  already configured. Every phase landed and was verified in the catalogs:
  win10cli got a master key, `win10cli_Cert` with a private key, `ubusql1_Cert`
  imported, `ubusql1_login`/`ubusql1_user`, endpoint `AGEP` STARTED on 5022, and
  the CONNECT grant; ubusql1 got `win10cli_Cert`, `win10cli_login`/`_user` and
  its grant, with its own endpoint untouched. **The imported thumbprints equal
  the presenting instance's on both sides** — the assertion that says the public
  key actually crossed rather than a fresh key pair being generated at each end.

  **The direction is forced and is not arbitrary**: `fetchPrefetch` blocks the
  dialog when the *local* instance has `IsHADREnabled` false, so the run has to
  originate from ubusql1 with win10cli as the peer, never the other way round.
  Nothing else in the pipeline asks about HADR, which is why a Windows instance
  with Always On disabled can still take a full mirroring endpoint and take part
  in the exchange.

  **The scripted path was run first and is the more valuable half**, because it
  produces the partial script that a live run on already-configured instances
  never can: with win10cli having no certificate yet, `ensureCertificate` set
  `certPending`, and the script came out headed `THIS SCRIPT IS INCOMPLETE`,
  with ubusql1's block reduced to `-- Missing here: the certificate, login and
  user for win10cli, and the CONNECT grant that goes with them.` and win10cli's
  block complete and runnable — including `CREATE CERTIFICATE [ubusql1_Cert]
  ... FROM BINARY = 0x3082...`, ubusql1's real public key, read from the live
  server under `WithScript`. Both deliberate suppressions held: no GRANT to a
  login the skipped phase never created.

## Log File Viewer: what is deliberately out of it

Built 2026-08-12 (see `docs/journal.md`). One thing SSMS's own viewer has is
still left out on purpose; the other two were closed 2026-08-21:

- **One log file at a time, no merged view.** SSMS's left pane checkboxes let
  several logs (and the Windows event log) be merged into one date-sorted grid.
  The two selectors were chosen instead; merging means a source column, a merge
  sort, and N reads per refresh.
- ~~**The filter is client-side.**~~ **Both now exist, and they are different
  features — do not merge them.** The toolbar's Filter box still narrows what
  was read, instantly and with no round trip, and still reports "N of M match".
  "Search..." (2026-08-21) edits `xp_readerrorlog`'s own arguments 3-6 and
  changes what the server returns, which is what a log too large to read in one
  go needs; the status line names it, because "no entries" on a searched read
  otherwise reads as an empty log. The client-side pass runs over whatever came
  back, so the two compose.
  Two things about those arguments that only a live run says: the date bounds
  must be sent as **text** (`YYYY-MM-DD HH:MM:SS`) — a typed datetime parameter
  is rejected with "The format for the date filter is incorrect" — and the two
  search strings are **AND**-ed, not alternatives.
- ~~**No Enter / double-click on a log-file leaf.**~~ **Fixed** 2026-08-21 —
  `controls.TreeView.OnActivate`, fired by Enter and by a second click on the
  same row within `doubleClickInterval`, falling back to expand/collapse when
  the host declines. Only the two log leaves claim it: an object node's menu
  has several actions and none of them is obviously "the" one, so guessing
  would make Enter unpredictable across the tree.

Also not built: the Windows event log (needs WMI, out of scope for a no-CGO
portable build).

~~`sp_cycle_errorlog` / `sp_cycle_agent_errorlog` as a "Recycle" action —
gosmo has `CycleErrorLog`, but nothing in the UI calls it.~~ **Built**
2026-08-21. gosmo gained `CycleLog`/`CycleLogContext(logType)` over a
statement table (`sp_cycle_errorlog` for the SQL Server family,
`msdb.dbo.sp_cycle_agent_errorlog` — three-part, so it works from any current
database — for the Agent's); `CycleErrorLog` stays as a delegate. gossms has
it on the Log File Viewer's toolbar and on both log folders' Object Explorer
menus, each confirming first and naming that the archives renumber and the
oldest is dropped.

Two rules came out of it. The toolbar's busy latch is taken **before** the
confirmation is shown, not in the answer: the confirm dialog takes input but
does not stop F5 reaching the panel, so a read begun while the question was up
would clear `busy` from under the cycle. And an open viewer is put through
`Refresh`, never `Load`, after a cycle started from the tree — `Load`
re-enumerates only the family on screen, so cycling the Agent log while the
viewer sits on the SQL Server family would leave the Agent's pre-cycle
numbering cached and hand it to the user the moment they flipped the selector.

## Object Explorer folder filter: what is deliberately out of it

Built 2026-08-13 (see `docs/journal.md`). Two gaps left, both deliberate; the
other two were fixed 2026-08-15.

- ~~**The filter is client-side.**~~ **Pushed down** 2026-08-21 for the seven
  families where folder size can matter — Tables, Views, Stored Procedures,
  Functions and the three System * variants — through gosmo's `ObjectFilter`
  and the `…FilteredContext` listings. Three things about it are load-bearing:
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

Built 2026-08-13; column Delete, the drop-with-cascade option and Move to
Schema went in 2026-08-21 (see `docs/journal.md`). What is left out is:

- **No partition or filegroup Delete from the tree.** Neither has a tree node:
  partition functions and schemes do and are deletable, and a filegroup is
  removed from Database Properties > Filegroups.
- ~~**A column has Delete but no Rename.**~~ **Built** 2026-08-23. gosmo gained
  `Table.RenameColumn`/`Context` (`sp_rename`'s `COLUMN` class, three-part
  `@objname`), and the column node offers Rename behind a warning: nothing that
  names the column is updated, so views, procedures, computed columns, check
  constraints and filtered indexes keep the old name and break at their next
  use. The warning is asked *before* the rename because SQL Server's own
  caution arrives after a rename that already succeeded.
- **A trigger cannot be moved to another schema**, and must not be offered: it
  belongs to its table and moves with it, and `ALTER SCHEMA ... TRANSFER`
  refuses one. Indexes, statistics, keys and constraints are the same case.
- **Agent objects keep their own Delete** (`agent_menu.go`), whose per-type
  wording explains what blocks each one; only Rename comes from the shared
  table. Availability groups likewise.
- **No multi-select delete.** `controls.TreeView` has a single selection, so
  SSMS's "Delete Object" dialog listing several objects has no equivalent. The
  author's own answer to this is that it belongs in the Object Explorer Details
  pane, whose grid already has block selection — not in the tree.
- **The server does not say what blocks a column drop.** Verified live
  2026-08-21: `ALTER TABLE DROP COLUMN note failed because one or more objects
  access this column.` — no constraint, index or statistic named, even when a
  single default constraint is the whole reason. The delete confirmation
  therefore names the *classes* of blocker itself; do not "simplify" it to
  promise the error will identify one.

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

- ~~**Key Diagnostics cannot copy its log.**~~ **Fixed** 2026-08-21 — the log
  moved into a read-only `controls.Editor`, the way Status History has always
  held its own, and the dialog is a `core.ClipboardHost`. One rule came out of
  it that Status History does not need and a "simplification" would undo:
  **`syncIfDirty` must not rebuild the editor while it has a selection.** This
  dialog records the very keys used to copy from it, so Ctrl+A is itself logged
  and the next frame's `SetText` — which resets cursor, scroll *and* selection —
  would drop the selection before the Ctrl+C arrived. See `docs/journal.md`.

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

## Left open by the 2026-08-22 cross-repo review

Both items below were taken to a decision and implemented the same day — see
`docs/journal.md` § 2026-08-22 "The two open design calls, decided". What
remains of each is recorded here.

- ~~**Execution plans: done, one caveat.**~~ **Closed** 2026-08-22. The
  caveat was that gossms's `scanPlanXML` appending every row of a showplan set
  was correctness by construction, with no live shape to prove it. Probed
  against win10cli over seven batch shapes — multi-statement, `EXEC` of a
  procedure, a nested procedure, `WITH RECOMPILE`, control flow, a `WHILE`
  loop, cursors, `sp_executesql` and `EXEC()` — under both SET options: **every
  showplan result set holds exactly one row**, always. `SET SHOWPLAN_XML`
  answers a batch with one combined document holding every statement (the
  called procedure's included); `SET STATISTICS XML` answers each executed
  statement with a document in a result set of its own.
  `TestLivePlanEveryShowplanSetHoldsOneRow` now runs that probe as a test, so
  a server or driver that ever splits a set says so.

  Two things the pass corrected. Both `scanPlanXML`'s comment and gosmo's
  `capturePlan` comment asserted the opposite — "SHOWPLAN_XML returns one row
  per statement in a single result set" — which is not a shape SQL Server
  sends, and was the reason the caveat could not be checked. Both now describe
  the observed shapes and keep the loop as an explicit tolerance. And the
  per-set append is *not* live-testable in either direction: an overwriting
  `scanPlanXML` passes every live test, because no set has a second row for it
  to lose. `TestScanNextKeepsEveryShowplanRow` (a scripted driver) is the only
  thing that kills that mutant, and the cross-*set* append is the only half the
  live tests pin — both verified by mutation.

- **Login sources: the gosmo half is done, the gossms UI is not.**
  `CreateLoginOptions.Source` covers SQL, Windows, external provider,
  certificate and asymmetric key, and the login read/script path covers all
  seven `type_desc` values. The **New Login dialog still offers only SQL and
  Windows** — that stays part of § Deferred scope's standing Entra item, and
  is now purely a UI question. Two things are still unpinned by a live run:
  `FROM EXTERNAL PROVIDER` (win10cli has no Entra, so its statement text is
  pinned by unit test only) and `CREATE LOGIN ... FROM EXTERNAL PROVIDER WITH
  OBJECT_ID = '...'`, which SQL Server 2022+ accepts and gosmo does not emit —
  nobody has asked for it, and adding it blind to a form nothing can exercise
  is how the unreachable `EXTERNAL` scripter branch happened in the first
  place.

## Permission gating: what P3 deliberately leaves out

Added 2026-08-25 with P3 of `docs/permissions-plan.md`. Each of these is a
known limit of the gate layer, not an oversight.

- **A read-only Properties page still *looks* editable.** `Form.SetReadOnly`
  makes every row unfocusable and refuses to route a click into one, so
  nothing can be typed or toggled and the page can never become dirty — which
  is what makes Apply and Script Changes safe. But a Text row still draws its
  `[value]` box and a Check row still draws its `[ ]`. The plan's §3.2 asked
  for those rows to render as `propsheet.Static` instead; doing it properly
  means a read-only draw state on every row type, not a change at any one of
  the forty-odd pages. The note at the top of the page and the collapsed
  button row are what carry the message meanwhile.

- **A schema-scoped Rename/Move/Delete is gated on rights that are sufficient,
  not necessary.** SQL Server checks ALTER on the object's *schema*, which the
  database-scope probe cannot see, so `objectOpRights` asks for database-wide
  ALTER, CONTROL or ALTER ANY SCHEMA instead. A login granted ALTER on one
  schema and nothing else loses those three menu items on objects it could in
  fact rename; the query editor still works. Closing this needs a per-schema
  probe, which is a query per schema per database.

- **SQL Agent's New Job / New Schedule / New Alert / New Operator are not
  gated at all**, deliberately. What permits them is membership of
  `SQLAgentUserRole` (or above) in msdb, which grants EXECUTE on individual
  procedures — not the database-scope EXECUTE the probe reads. Gating on the
  latter would withhold them from every legitimate Agent user, which is worse
  than today's behaviour of failing at the server with a now-readable message.
  A gate here needs the msdb role probe, which `gosmo.ProbedDatabaseRoles`
  already collects; what is missing is deciding what "or above" means.

- **A few write actions have no probe-visible right and are left ungated**:
  the two Always Encrypted key dialogs (ALTER ANY COLUMN MASTER KEY), Security
  Policies' Enable/Disable (ALTER ANY SECURITY POLICY), New Index and New
  Statistics (ALTER on the table). Adding the permission names to gosmo's
  `ProbedServerPermissions`/`ProbedDatabasePermissions` is the prerequisite,
  and each costs a row in every probe.

- **`requiresText` repeats a role it has already named** — "Requires ALTER
  (db_owner) or CONTROL (db_owner) or ALTER ANY DATABASE (dbcreator)" on
  Database Properties. Accurate, and wordier than it needs to be.

- **Two refusals the P4 mapping cannot reach, both by design of P3.** The
  `withPermissionAdvice` append is wired at Apply, Delete/Rename/Move, Recycle,
  New-object creation and every background task — but on the live matrix the
  gates now withhold those actions before they can be attempted, so only the
  *read* refusals (tree node, Log File Viewer grid, a failed page load) were
  verifiable end to end. The append itself is unit-tested against
  live-captured message text. Reaching it live needs a login that holds a right
  the gate reads but not the one the statement checks — a schema-scoped ALTER
  is the obvious candidate, and is the same gap as the objectOpRights item
  above.

- **The `xp_dirtree` silent-empty guard is unit-tested only.** Both test
  servers are 2017 or later and so never take that path;
  `legacyListingRefusal` is exercised across all five combinations by test but
  has never run against a real pre-2017 instance.
