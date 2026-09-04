# Open threads

Work that was found, decided, or deferred but not finished. Pruned 2026-09-04:
everything closed, and the history of how it was closed, was deleted.

Keep this file current: close an item by deleting it, and add one whenever
something is knowingly left undone. An open item recorded only in a session
note is invisible by the next session.

**This file holds only open work and settled decisions.** Fixed items do not
accumulate here — delete an item once it is done, and delete the account of how
it was verified with it. The "do not re-raise" sections are the deliberate
exception: they are not history, they are what stops a settled question being
reopened.

## Version support: gosmo is not version-aware

The target is **SQL Server 2016 SP1 and later**; the floor is 2016 SP1 rather
than RTM because `procedure.go`, `scripter.go` and gossms's
`internal/activity/block.go` emit `CREATE OR ALTER`. gosmo is written against
whatever the two instances in the house are (majors 14 and 17), and the first
run on anything older than 2019 found nine defects — four gosmo reads select
catalog columns that do not exist before 2019/2022 and fail outright (Column
Master Keys, Query Store options, `Table.Detail`'s `ledger_type_desc`, which
kills **Table Properties > General** on every table, and `Statistic.Header`,
where DBCC returns 10 columns and the code scans 11). For the 2016 target the
biggest item is **`STRING_AGG` in seven queries** — a 2017 function — which
would kill partition functions and schemes, server and database triggers,
foreign keys and audit specifications.

There is no major 13, 15 or 16 instance and no way to run one here (no Docker,
3 GB RAM), so the plan is built around verification that does not need them.
All findings, the gating mechanism, the three verification layers and the
reproduction recipe are in **`docs/version-support-plan.md`**, which is the
resume point — nothing is fixed yet.

## Unbuilt features README already promises

- **Reports** — server-level and database-level (top tables, disk usage). No
  entry point exists yet.

## Deferred scope (repeatedly, deliberately)

- **Windows / Microsoft Entra (Azure AD) authentication**, in Login Properties,
  New Login, and the External Provider login type generally. gosmo-side work
  needed first. Re-deferred on every properties/dialog pass; this is the
  standing answer to "why isn't this in the UI?".
- **Entra logins stay unverifiable here.** `CREATE LOGIN ... FROM EXTERNAL
  PROVIDER WITH OBJECT_ID` is emitted and its grammar is confirmed on a real
  server: on win10cli (no Entra) it and the bare `FROM EXTERNAL PROVIDER` fail
  with the *same* Msg 37525, so the parser accepted both. Whether a login is
  actually created needs an Entra-joined instance.

## Comment drift: the semantic half is unfinished

A mechanical comment-drift survey of all 605 `.go` files found and fixed the
mechanical classes — stale doc-comment names, references to identifiers that no
longer exist, doc-vs-signature mismatches, wrong numeric claims. Drift whose
*prose* misdescribes the logic without naming anything stale surfaces to no
mechanical detector; only reading each file against its code finds it.

**What is left is the per-file semantic read of 187 files of
`internal/tui/**`.** Read so far: all of `internal/tuikit/**`, the six small
packages (`internal/config`, `internal/fileutil`, `internal/db`,
`internal/query`, `internal/showplan`, `internal/activity`), and in
`internal/tui` only `app.go`, `app_events.go`, `app_explorer_data.go`,
`app_panel_actions.go`, `activity_monitor.go`, `detail_browser.go`,
`explorer_object_ops.go`, `log_viewer.go`, `new_endpoint_dialog.go`,
`object_explorer.go`, `permission_gate.go`, `prop_grid_helpers.go`,
`query_store_panel.go`, `tree_node.go` and the three subpackage `doc.go`s.

The recurring shapes, worth checking first in any later batch: a doc naming the
wrong caller after a helper was extracted; a package `doc.go` whose file list or
dependency claim predates a split; a count of anything (arguments, closures,
dialogs, menu items — nearly every one found was wrong); a claim about *which*
types implement an interface; and an inverted sentence that reads fluently
either way.

**Explicitly out of scope, and not to be re-proposed**: the long comment blocks
`CLAUDE.md` § Coding conventions protects, and the failure-naming comments in
`query_store_panel.go`, `prop_grid_helpers.go` and similar. Each stops a
regression a plausible simplification would reintroduce.

Not a comment fix, and left as is: `editor_search.go`'s `ensureColumnVisible`
duplicates `ensureCursorVisible`'s horizontal half (`selectMatch` calls both);
harmless, kept, its comment says what it does rather than claiming a reason that
was never true.

## Known-wrong wording, unfixed

- **SQL Server names the constraint blocking a DROP COLUMN, and gossms's
  warning still says it will not.** Found by gosmo's
  `TestLiveDropColumnAndTransferObject`, whose assertion claims the refusal does
  not name the blocker. Majors 14 and 17 both answer `The object
  'DF_Orders_flagged' is dependent on column 'flagged'. ALTER TABLE DROP COLUMN
  flagged failed because one or more objects access this column.` (two server
  messages, concatenated by the driver), so the assertion was wrong when
  written. Nothing is broken — the drop is still refused and gosmo passes the
  text through — but gossms's pre-drop confirmation describes the *classes* of
  blocker on the premise that the server names none, and could name the blocker
  instead. Two separate changes: re-aim the gosmo assertion rather than deleting
  it (that the dependency refusal arrives is still worth pinning), and reword
  the gossms confirmation.

## Permission gating: what remains open

- **A DENY at a class other than 1 (object) or 3 (schema) that the tree acts on
  is still invisible**, and so is a database-scope (class 0) DENY beneath a
  narrower object- or schema-scope grant — `Permits` sees it, but
  `rightAlterOnObject` answers yes beside it and SQL Server resolves the wider
  DENY over the narrower grant.
- **The column path is dormant in production, and that is settled — do not
  re-raise.** `ProbedObjectPermissions` is `ALTER`, which is not
  column-grantable, so `ColumnPermissions` is empty on every real connection
  and `DeniedOnAnyColumn` never withholds anything. Adding `SELECT` would not
  change that on its own: `objectDenial` asks the column question only for the
  rights an action lists, and the only object-scoped right gossms declares is
  `rightAlterOnObject`. Firing the path needs a gossms *consumer* — an action
  gated on `SELECT` at object scope — and the only candidate is Select Top 1000
  Rows, which hands the user generated text in a query panel rather than
  performing the read. The machinery stays, live T-SQL-verified, for the first
  action that genuinely reads an object's data. Two facts for whoever picks it
  up: `SELECT` in the probe list brings public's catalog-view grants back into
  `ObjectPermissions` (232 rows on HealthClinic) and they must **not** be
  filtered out by `is_ms_shipped`, because those grants are real and a system
  view would otherwise be withheld from a login that can read it; and
  `permission_gate_names_test.go` checks an object-scoped right against
  `ProbedDatabasePermissions`, not `ProbedObjectPermissions`, so a new one
  would gate nothing silently.
- **A schema *node* is deliberately excluded from the schema-scoped gate.**
  `objectOpRights` names `rightAlterOnSchema` beside the three database-wide
  rights, but ALTER on a schema does not permit dropping or renaming the schema
  itself.

Three facts the object-scope gate rests on, each a wrong gate if assumed the
other way: **db_owner is not exempt** from an object DENY (it reads
`HAS_PERMS_BY_NAME` 0 on the denied table) but **sysadmin is**, and the probe's
principal set includes `public`, so a DENY to public is recorded for a sysadmin
whose write the server still allows — hence the explicit sysadmin bypass. And
**ownership needs no exception**: SQL Server refuses a DENY aimed at the owner
of a securable, and `ALTER AUTHORIZATION` *deletes* an existing DENY row as it
transfers ownership. Schema scope needs its own catalog read
(`explicitSchemaCapabilityQuery`, `E:` rows) because `HAS_PERMS_BY_NAME`
returns 0 for a schema permission never granted exactly as it does for one
explicitly denied, and withholding on that would empty the menus of every login
working through a database-wide grant.

## gosmo: deliberate API decisions

- **No `Drop*` write method carries `IF EXISTS`, and that is the decision, not
  an omission.** A review found the family split down the middle: dropping a
  view that was already gone reported "View deleted" and dropping a sequence
  reported the server's refusal, from the same Object Explorer gesture. A bare
  DROP everywhere was chosen over `IF EXISTS` everywhere so that "deleted"
  means deleted; a caller that wants idempotence ignores the error, which is a
  choice the library cannot make for it. `TestDropStatementsAreNotIdempotent`
  pins it. The *scripts* Scripter generates keep `IF EXISTS` — DROP-and-CREATE
  output exists to be re-run.
- **`CertificateByName` answers `(nil, nil)` on absence and was deliberately
  not changed** when `ErrNotFound` went in. Making it error is a breaking
  change to a published contract, and its callers branch on absence as the
  ordinary case. The three surviving conventions are documented on
  `ErrNotFound` itself; `TestLiveCertificateNotFoundIsNilNil` pins both
  directions so "nil" cannot quietly start meaning "always nil".
- **A missing principal and an invisible one are the same thing to
  `ErrNotFound`, and that is SQL Server's doing — do not try to fix it in
  gosmo.** Metadata visibility hides a principal the caller lacks
  `VIEW ANY DEFINITION` on by returning **zero rows, not an error**, so an
  existing login reads as absent and no lookup can tell the difference; pinned
  by `TestLiveNotFoundCannotSeePastMetadataVisibility`. The answer is
  idempotence at the write, not a better sentinel. Note `isAlreadyExists`
  matches by the "already exists" substring; its `15023` arm is the *user*
  code, and logins raise 15025.
- **`JobStateCancelling` and `JobStateRunning` are deprecated constants** naming
  states Agent's encoding does not have. They carry negative values so no switch
  over the real encoding reaches them, and are kept only so existing callers
  compile (§ Changing gosmo) — worth retiring in a release that can take the
  break. The real encoding, read from `xp_sqlagent_enum_jobs`, is 1 Executing,
  2 WaitingForThread, 3 BetweenRetries, 4 Idle, 5 Suspended,
  6 WaitingForStepToFinish, 7 PerformingCompletionActions, with 0 meaning a job
  Agent does not run itself.

Three things about the job-state read are load-bearing and easy to undo:

- **The `INSERT ... EXECUTE` table shape must match the extended procedure's
  thirteen columns exactly**, which is why `jobStateColumns` is copied from
  `sp_get_composite_job_info` rather than trimmed to the two columns gosmo
  reads.
- **A failed state read is not a failed listing.** `applyJobStates` swallows the
  error and leaves the `sysjobactivity` derivation in place. With Agent stopped,
  `xp_sqlagent_enum_jobs` does *not* fail — it returns zero rows, so the
  fallback is reached by covering no job, not by an error. The derivation emits
  the same encoding (`THEN 1 ELSE 4`) so the two paths cannot disagree.
- **`jobIsRunning` answers "unknown" for `JobStateUnknown` and
  `JobStateSuspended`, and `jobStateRefusal` then refuses nothing.** Unknown
  means Agent had nothing to say; Suspended is genuinely ambiguous — sp_help_job
  groups it with idle, while a suspended job still holds a session. Sending the
  request and letting the server answer beats refusing an action that would have
  worked.

## Environment: what the instances can and cannot do

- **win10cli can never be a third availability replica.**
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
  That is what lets Add Replica's Connect read a real endpoint off a third
  instance.
- **It is configured as ubusql1's mirroring peer, deliberately left that way.**
  win10cli holds a database master key, `win10cli_Cert`, ubusql1's imported
  `ubusql1_Cert`, `ubusql1_login`/`ubusql1_user`, and endpoint `AGEP` STARTED on
  5022; ubusql1 holds the matching `win10cli_*` principals and certificate. Left
  in place because a third instance with a STARTED endpoint is exactly what Add
  Replica's Connect needs, and rebuilding it costs a live run. It is inert.
- **`xp_cmdshell` is on on win10cli and can never be on on ubusql1.**
  `sp_configure 'xp_cmdshell', 1` on the Linux instance fails Msg 15392 — "not
  supported by this edition" — because SQL Server on Linux has no xp_cmdshell at
  all; file moves on ubusql1/ubusql2 go through ssh instead. Anything in gossms
  that would depend on xp_cmdshell is therefore Windows-only by construction.
  Linux still *lists* it in `sys.configurations` at 0, which is why
  `xpCmdshellRow` has to test `ServerInfo.Platform` rather than rely on the
  missing-option path.
- **A live AG test's teardown reaches `DROP AVAILABILITY GROUP` on a real
  cluster.** `liveDropGroupEverywhere` runs *before* the create as well as after
  it, so `-liveag-create-name` is a flag that drops groups. It refuses, fatally,
  any group whose cluster type is not NONE. Know this before touching that test.
- **`TestLiveAvailabilityGroupOperations` deliberately skips Drop and
  RemoveReplica** against the standing group AAG1; only add/remove database,
  suspend/resume, the listener round trip and the failover refusal run there.

## Always On: what is deliberately out of scope

All seven phases are built. These are decisions taken while building them.

- **New Availability Group does not roll back a group whose CREATE succeeded**,
  and neither does Add Replica. Every ordinary reason a secondary's JOIN fails
  is checked *before* the CREATE — peer reachable, Always On enabled there,
  endpoint present, STARTED and still at the address the dialog recorded, rights
  to join — and any failure refuses with nothing created. What survives is the
  peer that dies between the check and the JOIN, and there the group is left
  alone: a rollback would destroy what the user asked for on the strength of one
  unreachable instance, and it cannot even be complete — a group dropped from
  the primary stays in the *secondary's* `sys.availability_groups` and needs a
  local DROP there, which is exactly the residue a rollback exists to prevent.
- **The create dialog has no Read-Only Routing page**, unlike SSMS's. Routing is
  a per-replica setting AG Properties already covers, two clicks after the group
  exists.
- **The endpoint dialog assumes a full mesh and one shared master key
  password.** Every instance gets every other instance's certificate, which is
  what an availability group needs and more than a plain mirroring pair does;
  the one password field is used for whichever instances turn out to have no
  database master key yet.
- **Add Replica does not offer initial data synchronization.** AUTOMATIC seeding
  covers the case SSMS's share-based route exists to work around; MANUAL seeding
  means restoring each database by hand and then using Join to Availability
  Group on the secondary's copy, which is in the tree.
- **A listener address cannot be removed and a listener cannot be renamed.** Not
  a gap: `ALTER AVAILABILITY GROUP ... MODIFY LISTENER` has no statement for
  either, so both mean REMOVE LISTENER and ADD LISTENER. Listener Properties
  says so rather than offering buttons that would only work on rows not yet
  written. Under an EXTERNAL cluster type an added address is recorded OFFLINE,
  since the external cluster manager owns it.
- **Failover cannot be done in T-SQL under `cluster_type = EXTERNAL`** —
  handled, not open: `agFailoverRefusal` explains it and names Pacemaker instead
  of sending the statement. EXTERNAL rejects both `ALTER AVAILABILITY GROUP ...
  FAILOVER` and `... FORCE_FAILOVER_ALLOW_DATA_LOSS` with `Msg 47104`; `NONE`
  rejects only the lossless form, with `Msg 47122`, and allows the forced one.
- **The New AG page offers no backup from inside the dialog.** Both
  `ALTER AVAILABILITY GROUP ... ADD DATABASE` and `CREATE AVAILABILITY GROUP ...
  FOR DATABASE` refuse an unbacked-up database with **Msg 1475**, so the page
  applies the same rule and lists it under "Not offered"; the exclusion line
  names the Backup dialog, two clicks away. Note the backup history is the wrong
  signal in both directions — a database whose `msdb` history was deleted still
  joins, and one round-tripped through SIMPLE does not though its history still
  shows the full backup.

Two rules about peer credentials that are easy to undo:

- **Peer credentials are resolved from connections the user has already made.**
  `ServerConn.SetPeerCredentials` installs a resolver `peerOptions` consults
  before falling back to the parent's settings, so connecting to a replica once
  via File > Connect is how it is given a different login, port, auth method or
  TLS setting. A saved connection is taken whole rather than field by field, so
  it cannot silently stop carrying whatever `config.Connection` gains next. The
  lookup is keyed by `db.InstanceKey` (normalizing case, port and named-instance
  spelling), and because `@@SERVERNAME` is the short machine name while people
  type the FQDN, a saved connection is *also* registered under its short host as
  a strictly lower-priority tier — an exact host match always wins, so
  `sql.a.example` and `sql.b.example` never hand each other their logins.
- **The resolver may never make an instance less reachable than it was before
  one existed.** `Peer` retries once with `parentPeerOptions` when the
  resolver's answer will not connect, and `loadPeerCredentials` does not seed an
  entry whose password this session cannot decrypt. A saved *low-privilege*
  login still wins over the parent's on purpose — that is the feature. Both
  halves are needed: deleting the retry and ignoring the resolver each break a
  different one.
- **`ServerConn.Peer` blanks `Opts.Database`, deliberately.** A database named
  in the connection string fails the *connect*, at ping time, and everything
  `Peer` reaches is server-scoped. The other three `sc.Opts` clones — the query
  panel's and the Activity Monitor's two — target the same instance and keep the
  database on purpose; this rule is about the cross-instance clone only.
- **The peer failure cache is short on purpose.** `recordPeerFailure` holds the
  last connect failure per `InstanceKey` for `peerFailureTTL` (30s) so a
  blackholed primary does not charge the driver's full 15s connect timeout to
  every folder expansion. It is invalidated explicitly by a successful direct
  `File > Connect` to that instance and by a Refresh anywhere in the Always On
  subtree, both recursing into cached peers because a chained read records its
  failure on the peer, not on the connection the user is acting on. A Refresh
  elsewhere deliberately leaves the cache alone. The 15s connect timeout itself
  is left alone: a peer across a WAN may legitimately need it.

## Always Encrypted: what the two create dialogs leave out

- **Key material is pasted, never computed.** A column master key's enclave
  signature and a column encryption key's `ENCRYPTED_VALUE` are produced
  client-side from the master key's private key, which lives in a Windows
  certificate store, a CNG/CSP provider or a Key Vault — none reachable from a
  portable no-CGO build. Both dialogs take the `0x…` value SSMS or the
  `SqlColumnMasterKey`/`SqlColumnEncryptionKey` cmdlets print. A pasted
  signature with "Allow enclave computations" unticked is *refused* rather than
  dropped: the key would be created, and would not be the one being set up.
- **One encrypted value per *create*** — the only shape a key is created in. The
  second value a master-key rotation adds is Column Encryption Key Properties'
  job, through `AddValue`/`DropValue`.
- **`RSA_OAEP` is the only algorithm**, because it is the only one SQL Server
  accepts — a dropdown of one would say nothing, so it is a static row.

## Log File Viewer: what is deliberately out of it

- **One log file at a time, no merged view.** SSMS's left pane checkboxes let
  several logs (and the Windows event log) be merged into one date-sorted grid.
  The two selectors were chosen instead; merging means a source column, a merge
  sort, and N reads per refresh.
- **The Windows event log is out** — needs WMI, out of scope for a no-CGO
  portable build.
- **The toolbar's Filter box and "Search..." are different features — do not
  merge them.** Filter narrows what was read, instantly and with no round trip,
  and reports "N of M match". Search edits `xp_readerrorlog`'s own arguments 3-6
  and changes what the server returns, which is what a log too large to read in
  one go needs; the status line names it, because "no entries" on a searched
  read otherwise reads as an empty log. The client-side pass runs over whatever
  came back, so the two compose. Two things about those arguments only a live
  run says: the date bounds must be sent as **text**
  (`YYYY-MM-DD HH:MM:SS`) — a typed datetime parameter is rejected with "The
  format for the date filter is incorrect" — and the two search strings are
  **AND**-ed, not alternatives.

Two rules from that work: the toolbar's busy latch is taken **before** the
confirmation is shown, not in the answer (the confirm dialog takes input but
does not stop F5 reaching the panel, so a read begun while the question was up
would clear `busy` from under the cycle); and an open viewer is put through
`Refresh`, never `Load`, after a cycle started from the tree — `Load`
re-enumerates only the family on screen, so cycling the Agent log while the
viewer sits on the SQL Server family would hand the user the Agent's pre-cycle
numbering the moment they flipped the selector.

## Query Store: what is deliberately out of it

- **Plan comparison is two grids, not two plan graphs** — an operator tile is
  eighteen columns wide. Operators pair on physical operator plus object without
  the index, so a seek that changed index reads as one changed row; a seek that
  became a scan stays two one-sided rows. Also out: comparing a plan against a
  saved `.sqlplan`, and comparing two plans of *different* queries — refused,
  since they have no operators in common to pair.
- **No "Configure" button on the panel.** Query Store's settings are a Database
  Properties page, which is where SSMS puts them too; the folder's context menu
  opens it.
- **No per-query time series.** gosmo's `QueryStoreTrackedQueryContext` returns
  one query's per-plan values interval by interval — SSMS plots it under Tracked
  Queries. Ours shows the tracked queries and their plans; the series is read by
  nothing. Kept in gosmo deliberately (§ Changing gosmo).
- **The panel reads on demand only** — no auto-refresh timer. Every read is
  one-shot and bounded, so nothing queues behind the shared host connection.
- **One metric and one statistic across all seven views.** Each report carries
  its own default (Total for the two that rank by accumulated cost, Avg for the
  rest) and the panel opens on it, but switching views afterwards keeps what the
  user chose. SSMS keeps them per view.
- **The baseline of Regressed Queries stays inside the reported window**, and
  this should not be re-raised. gosmo's default — the equally long window
  immediately before `From` — is right for a caller that picked its own range
  and wrong for both surfaces here, because it makes the report need twice its
  window of history before it can show a row.

## Object Explorer folder filter: what is deliberately out of it

- **Only seven families push the filter down** — Tables, Views, Stored
  Procedures, Functions and the three System * variants, through gosmo's
  `ObjectFilter` and the `…FilteredContext` listings. The other filterable
  folders (sequences, synonyms, triggers, databases, logins, users, roles,
  schemas, partition functions/schemes, the Always Encrypted keys, security
  policies) stay client-side: they are small, and the clause builder is
  family-agnostic if that ever stops being true. The push-down rules that a
  plausible simplification removes are in `CLAUDE.md` § Application rules.
- **Owner and Durability Type are not offered on Tables, deliberately.** SSMS
  offers both; each is one `TableDetail` query per table, so listing them means
  a folder-wide detail fetch before the pane can draw a single row. This is the
  intended trade — do not re-raise.
- **Filters are per-session and stay that way.** `App.savedFilters`, keyed by
  `filterKey` rather than by node pointer, brings a folder's filter back on a
  reconnect within the session. Writing filters to `config.json` stays out: SSMS
  keeps them for the session only, and restoring one at startup against a folder
  whose objects have since changed is not wanted.

## Delete/Rename: what is deliberately out of it

- **No partition or filegroup Delete from the tree.** Neither has a tree node:
  partition functions and schemes do and are deletable, and a filegroup is
  removed from Database Properties > Filegroups.
- **A trigger cannot be moved to another schema**, and must not be offered: it
  belongs to its table and moves with it, and `ALTER SCHEMA ... TRANSFER`
  refuses one. Indexes, statistics, keys and constraints are the same case.
- **Agent objects keep their own Delete** (`agent_menu.go`), whose per-type
  wording explains what blocks each one; only Rename comes from the shared
  table. Availability groups likewise. **A system Agent job still offers `Delete
  Job...`** though Rename came off every system object: SSMS permits deleting a
  system job and msdb raises no objection.
- **Neither audit object offers Rename from the tree.** A server audit
  specification has no `MODIFY NAME` form at all — a parse error, not a
  permission failure. An audit has one, but only while disabled, so a tree
  rename would silently stop auditing for its duration; the rename lives on the
  Properties page, where the disable is visible in Script Changes.
- **Multi-select delete lives in the Object Explorer Details pane and nowhere
  else.** `controls.TreeView` has a single selection, so SSMS's "Delete Object"
  dialog listing several objects will never come from the tree; do not propose
  it there. Two limits are the design: **only schema-scoped objects are deleted
  as a set**, while a database, a login, a server role, a user, a database role
  or an Always Encrypted key is deleted on its own (`objectOp.solo`), because a
  typed confirmation asks for one object's name and a principal's drop reaches
  past the object further than one shared warning can describe; and **Rename and
  Move to Schema stay in the tree**, neither meaning anything applied to a set.
  The solo rule is the *selection's*: one login selected in the pane still
  deletes. A view whose rows are not objects — Property/Value, a Query Store
  report, System Databases, a log listing — offers nothing, which is the correct
  answer rather than a gap.

## Clipboard in a dialog: what is deliberately out of it

- **A dialog with no text entry has an inert clipboard.** Help, About / Object
  Dependencies, Confirm, Query List and Background Tasks do not implement
  `core.ClipboardHost`, so Ctrl+C there does nothing. It previously copied
  whatever the panel behind the dialog had selected, which was never the thing
  the user was looking at.
- **Key Diagnostics' `syncIfDirty` must not rebuild the editor while it has a
  selection.** The log lives in a read-only `controls.Editor` and the dialog is
  a `core.ClipboardHost`. It records the very keys used to copy from it, so
  `Ctrl+A` is itself logged and the next frame's `SetText` — which resets
  cursor, scroll *and* selection — would drop the selection before the `Ctrl+C`
  arrived. Status History does not need this, and a "simplification" would undo
  it.

## Server-level families (Phase 3 item 12): what is deliberately out of them

- **Viewing audit records is absent.** `sys.fn_get_audit_file` is a feature the
  size of the Log File Viewer — a reader, a grid, a filter, paging over files
  the audit rolled over. SSMS's "View Audit Logs" command is therefore not
  offered at all rather than offered and empty.
- **Database Audit Specifications** are `todo.txt` item 15, not part of item 12.
  gosmo covers the server-scope half only; `sys.database_audit_specifications`
  has no reads.
- **Database-scope DDL triggers** (`parent_class = 0`) are item 13.
  `Database.triggersWhere`'s `parent_class = 1` was deliberately left alone —
  widening it would change what the existing per-database Triggers folder lists.
- **Cryptographic Providers** get no folder, and **database-scoped credentials**
  (`sys.database_credentials`) get no reads. A credential's provider *binding*
  is shown and scripted; registering a provider is not offered.
- **Creating TSQL, Service Broker and SOAP endpoints** is out. Endpoints are a
  read/state/drop family; the existing New Database Mirroring Endpoint dialog
  stays the only creation path.
- **Tape and virtual backup devices** are listed, scripted and dropped but not
  creatable — the New Backup Device dialog offers disk only, as SSMS's does.
- **Audit Properties is one page**, where SSMS has General and a separate Filter
  tab. `ALTER SERVER AUDIT` replaces every setting at once, so a second page
  with its own apply would either write a second ALTER reverting the first, or
  need the filter's value before the page holding it had been opened.
- **An audit's destination is editable from Properties**, where SSMS greys it.
  `ALTER SERVER AUDIT` accepts a new `TO` clause on a disabled audit and the
  page's apply already runs inside a disable window, so the only cost is the one
  the page's note states: switching away from FILE discards the file block, and
  a FILE audit resumed later starts a new audit file. The file rows are always
  present and gated on the dropdown rather than on the destination the audit
  loaded with; an empty path under FILE is refused before anything is disabled,
  because the server's own answer (Msg 33072) arrives only after the audit has
  been turned off.
- **All six folders' filters are client-side only**: none of the six listings
  takes a `gosmo.ObjectFilter`. Credentials, Audits, Server Audit Specifications
  and Server DDL Triggers offer Name and Creation Date; Backup Devices and
  Endpoints offer Name alone, because neither `sys.backup_devices` nor
  `sys.endpoints` records a creation date and a criterion over a zero
  `nodeData.CreateDate` rejects every row.

## By design — not issues, do not re-raise

- **Which databases a dropdown offers is settled, and lives in
  `internal/tui/database_list.go`.** The rule turns on when the name is
  resolved: a name stored now and used later (job step, alert, login default
  database, restore history) lists every database including system and
  non-ONLINE ones, because it is opened when the job runs, not now; a name acted
  on immediately lists only what the action will accept. Backup is the only
  dialog in the second class, and both its exclusions are hard server
  restrictions — `BACKUP DATABASE tempdb` and a backup of an OFFLINE database
  each fail with "BACKUP DATABASE is terminating abnormally". Do not "unify" the
  two lists; they are different on purpose.
  The trap that comes with the filter, since it will be rediscovered: the Backup
  dialog is opened *on* a database from the Object Explorer, and its dropdown is
  swapped asynchronously afterwards. `setDatabaseItems` keeps a selection the
  incoming list doesn't contain, at the front. Without that, right-clicking an
  OFFLINE database and choosing Back Up silently retargets the dialog at
  whichever database sorts first and backs *that* one up. Any future narrowing
  of a dropdown that a dialog can be opened on needs the same treatment.

- **`indexOf` against a sentinel list is right for two classes and wrong for a
  third.** Right for the fixed vocabularies (recovery model, page verify, Query
  Store state and capture mode, compatibility level) — where the list is written
  out in the page, the write is `items[row.Selected()]`, and a value outside it
  means gossms is behind SQL Server, not that an object vanished — and for the
  New-X dialogs' own defaults, whose value is one of the list by construction.
  Wrong for any name the *server* supplied against a list read separately, which
  goes missing whenever the object is dropped between the two reads or the
  caller cannot see it; those read the row back with `preservedValue`/`changedTo`
  instead. `TestIndexOfSentinelListFallsBackToSentinel` pins the helper.

- **"A job whose owner login was dropped" is not reachable by dropping a
  login.** SQL Server *refuses* to drop a login that owns a job — `This login is
  the owner of 1 job(s). You must delete or reassign these jobs before the login
  can be dropped.` A **schedule** has no such protection: dropping its owner
  login succeeds and `SUSER_SNAME(owner_sid)` goes NULL immediately. So an
  orphaned *job* owner needs a different route — an msdb restored from another
  instance, or a Windows principal removed from AD — and is rarer than the
  schedule case. Worth knowing before anyone tries to reproduce it and concludes
  the code is fine.

- **Start/Stop Job are deliberately not greyed out.** They read the job's state
  first and refuse in the app's own words ("Job X is already running" / "is not
  running"), refreshing the node either way. The read is free — both actions
  already fetched the job. Gating the menu item instead would hide a legitimate
  Stop for a job that started running since the folder was loaded; the check
  belongs where the data is one query old.

- **`sp_delete_jobstep` is not symmetrical with `sp_add_jobstep`.** It silently
  resets a reference to a step at or after the deleted one to "quit with
  success" rather than following it, which is what `ReorderSteps`' repair pass
  exists for. That pass is invisible to a test that moves the *last* step,
  because then no reference points past the delete — the live test moves a
  middle step for exactly that reason.

- **Merging the two user-mapping page builders is a deliberate non-goal.** Both
  pages use `wireGridEditor`, which is where the duplication that actually
  caused a bug lived; merging the two page *builders* was costed and rejected
  (six injection points, two different row structs, two unrelated applies). Do
  not re-propose without new evidence.

- **`Form.Revert()` is exposed, not retired.** `Ctrl+Z` on a `PropertySheet`
  calls `RevertPage`, which reaches `Form.Revert`, every row's `Revert` and all
  21 `RevertFn` closures. Two rules came with it, both easy to undo by accident:
  **`Ctrl+Z` is handled ahead of the zone switch, beside `F5`** — a sheet-level
  command, so it works from the page list and the button row — but
  `PropertySheet.HandleKey` gives the focused row first refusal through
  `focusedRowHandles`, so undo inside a job step's T-SQL box does not discard
  every other row's edits. And **`Ctrl+Z` must stay free inside a form row**:
  `widgets.InputField` takes `Ctrl+A`/`Ctrl+U` and no propsheet row hosts a
  `controls.Editor`, the one widget with a `Ctrl+Z` of its own. A row that ever
  embeds a full editor takes this key back and needs a different one.

- **The editor's redo stack is deliberately uncapped in bytes, and `applyStep`'s
  slice is deliberately unguarded.** `maxUndoSteps` and `applyStep` in
  `internal/tuikit/controls/editor_undo.go` carry the reasoning;
  `TestEditorRedoStackBound` pins it.
  - Redo is bounded in *count* (one entry per undo, cleared on any new edit) and
    not in bytes. `maxUndoBytes` genuinely does not reach it — the inverse
    carries the lines being *replaced*, so on a growing document redo ends up
    above undo: 48.4 MB against 46.5 MB, measured. The undo stack's own byte cap
    is what bounds it, to within one document. A `redoBytes` cap would buy that
    one document back in exchange for silently dropping the deepest redo.
  - `applyStep` slicing `[st.row : st.row+st.newLen]` without a bounds check is
    the intended failure mode. The invariant is `pushUndoSpan`'s caller promise,
    and a violated promise means the document is about to be corrupted; clamping
    would turn that into an undo that quietly restores the wrong text. The panic
    is the more useful failure. Do not add a clamp.

- **gosmo untagged past its current tag with `go.mod`'s `replace` active is the
  intended development state**, not a release blocker. Tagging gosmo, bumping
  `require`, and commenting out the `replace`/`ignore` pair are steps of the
  release process itself (ARCHITECTURE.md § Developing against a local gosmo
  checkout). A CI release build not resolving gosmo mid-development is the
  expected consequence.

- **A Grid/Text query result can exhaust memory.** The Max Result Rows option
  and every `maxRows` parameter behind it were removed: a result set is retained
  in full, so `SELECT * FROM` a billion-row table will OOM the process. SSMS
  parity of "you get what you asked for" was preferred to a silent cap. The
  retained form is already as small as it reasonably goes
  (`internal/query/arena.go`); the floor is the 16-byte string header per cell
  that `ResultSet.Rows [][]string` implies. Results To File never retained rows
  and is unaffected. Do not add a cap back.

- **The `Meta` (Output Column Metadata) block is a grid-only display aid.** It
  reads `Result.Sets`, which `ExecuteToSink` leaves empty by design — an export
  retains no rows, and the column *types* are not known to `RowSink.BeginSet`
  anyway. It also only takes effect on the *next* execution. Both intended; do
  not carry `ColumnTypes` through the sink interface to "fix" the first.

- **The DataGrid cell-viewer popup is deliberately unhighlighted.** A "Show
  Value" on a cell whose trimmed text is bracketed by `<>`, `{}`, or a
  JSON-shaped `[]` opens its own query panel with `XMLHighlighter`/
  `JSONHighlighter` instead (`internal/tui/cell_value.go`); everything else gets
  the plain 60-column popup, unhighlighted, on purpose. Highlighting inside the
  popup is the thing not to do: wrap mode resolves each drawn column through
  `styleAt` (`editor_draw.go`), a linear scan of the logical line's runs, chosen
  so a `varchar(max)` cell costs work proportional to the ~15 visible rows
  rather than to the value. That scan is fine against SQL's few coarse runs and
  not against a highlighter emitting one run per token over a whole XML
  document. Routing to a panel, which draws unwrapped, is what sidesteps it.

- **The Databases folder's one round trip per database is intended.**
  `FILEPROPERTY` reports on the *current* database only, so
  Data/Log/Unallocated/AvailLog cannot come from a server-wide view the way the
  Tables folder's aggregates do. Collapsing it into a single dynamic batch does
  work and is faster (11-12ms vs 20-21ms over four databases), but one
  unreachable database fails the whole batch and every row loses its sizes,
  where the fan-out degrades to `N/A` in that one row alone. The fan-out is also
  already concurrent 8-wide, so it costs `ceil(N/8) x RTT`. If a user ever
  reports the folder being slow, the batch goes in as a *fast path* with the
  fan-out as the fallback on any batch error — never as a replacement.

- **Restructuring `internal/tui` is closed. No file-split or package-split
  candidates are outstanding.** Costed and re-measured against a type-checked
  cross-file reference graph (549 real symbol edges) and rejected on the
  numbers. What shipped instead is `internal/tui/sqlparse`, the only part of the
  package with *zero* outbound references. The earlier "P5" file-split list was
  never a standing rule for the package and isn't one now: 31 non-test files
  exceed 400 lines, fifteen of them directly in `internal/tui`. That is not a
  reason to re-open the question this paragraph closes.
  The negative results, each a proposal a future review will otherwise
  re-invent:
  - The `agent_*`/`database_props_*`/`new_*` name families are not a seam — they
    cut straight through the `App` dependency.
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
  server shows exactly one row: its own. `and spr.spid <> @@spid` was proposed
  and **rejected — author's call**. Do not "fix" it.

- **`sp_block`'s `cross apply sys.dm_exec_sql_text` and its lack of an
  `ecid = 0` filter are intended.** `outer apply` plus `spid > 50 and ecid = 0`
  was proposed on the grounds that a blocker with no cached text takes its whole
  blocked subtree out of the tree and that a parallel plan is several
  `sys.sysprocesses` rows. Neither was reproducible — a sleeping blocker kept a
  resolvable handle, and `DBCC FREEPROCCACHE` does not evict a live
  transaction's text — and the `cross apply` is what keeps system sessions out
  today. **Author's call: as is.**

- **The Activity Monitor probes `VIEW SERVER STATE` once per collector, so twice
  per panel open.** Hoisting it to a single shared check was proposed and
  dropped: the Retry control starts a *new* collector after a transient failure,
  and a cached permission answer would make that retry fail without asking the
  server. One extra round trip on open is the cheaper mistake.

- **`counterQueryFor`'s `RTRIM(instance_name) IN ('', '_Total')` filter drops no
  counter the panels read.** Raised twice on the grounds that `RTRIM(NULL)` is
  `NULL` and `NULL IN (...)` is false. win10cli (Windows) has no NULL
  `instance_name` rows at all; ubudock (Linux) has exactly five, all
  `SQLPAL:Host Memory` / `SQLPAL:Guest Memory` rows that are not in
  `counterNames` and have no gossms consumer. Every one of the 33 names in
  `counterNames` resolves through the filter on both builds. Do not add an
  `OR instance_name IS NULL` arm.

- **`formatValue`'s `case float32` is unreachable but kept.** go-mssqldb returns
  `float64` for both `REAL` and `FLOAT`. It is correct if the driver ever
  narrows, and `formatFloat` already takes the bit size. Noted so it isn't
  "discovered" as live code.

- **Server-scope GRANT/DENY/REVOKE's `USE master;` prefix does not strand the
  pooled connection in master.** A review read gosmo's `"USE master; " + stmt`
  (`permission_options.go`, which `server_security.go`'s grant/deny/revoke
  methods route through) as pool contamination and proposed a pinned connection
  that reads `DB_NAME()`, switches, and switches back. **Live A/B disproved
  it**: eight pooled connections all still reported the right database after a
  GRANT, with the original code. `database/sql` calls
  `driver.SessionResetter.ResetSession` before handing a pooled connection to
  its next user, and go-mssqldb implements it by flagging the next TDS batch as
  a connection reset, restoring the session's database to the connection
  string's. The proposed fix was three extra round trips per grant to re-solve
  what the driver already handles, and was reverted.

- **`charts.HistoryChart.Draw` and `StackedHistoryChart.Draw` stay, though
  nothing in the binary reaches them.** `doc.go` advertises `Draw` as the entry
  point — "every chart draws into a `tcell.Screen` through a `core.Rect`" — and
  all six chart types implement it. The dashboard uses `DrawFrame` only because
  it also wants the time row. Removing two of six would break the package's one
  uniform method for four lines.

- **The five `staticcheck` U1000 findings in `clipboard_host_test.go` and
  `dialog_gesture_test.go` are suppressed, not deleted.** The fields are read by
  the reflection walk each test exists to exercise, so `type page`/`field p`/
  `field row` *are* the fixture — deleting `p` leaves `lazy` with nothing behind
  the nil pointer and the test asserts nothing. Each carries a
  `//lint:ignore U1000` naming the reader.

- **`rightAlterAnyLinkedSrv` and `rightCreateTable` still read as unused.**
  Deliberate; pinned by `permission_gate_names_test.go`.

- **The per-set append in `scanPlanXML` is not live-testable in either
  direction.** Every showplan result set holds exactly one row — probed against
  win10cli over seven batch shapes under both SET options, and pinned by
  `TestLivePlanEveryShowplanSetHoldsOneRow` — so an overwriting `scanPlanXML`
  passes every live test. `TestScanNextKeepsEveryShowplanRow` (a scripted
  driver) is the only thing that kills that mutant, and the cross-*set* append
  is the only half the live tests pin.

- **`masterMappableNames` swallows its two reads' errors on purpose.**
  `sys.certificates` and `sys.asymmetric_keys` need permission on master, and a
  login without it must still be able to create ordinary SQL and Windows logins
  — an empty picker becomes a refusal naming what is missing, not a broken
  dialog. Related and settled live: neither `DEFAULT_DATABASE` nor
  `DEFAULT_LANGUAGE` can be set for a certificate- or asymmetric-key-mapped
  login, in CREATE *or* ALTER, so the page refuses either rather than creating
  the login and then failing.

- **Per-file destinations in Restore (SSMS's editable "Restore As" column) were
  deliberately not built**; the folder-level choice covers what the dialog's
  width allows. Two rules from that work outlive it. **The backup set number
  must not get re-scattered**: the restore itself, the MOVE clauses and the
  Files Included panel all take it from `backupSetNumber` in
  `restore_dialog_ops.go`, and two of the three deriving it separately is
  exactly what produced the "Logical file 'x' is not part of database 'y'"
  failure on every rename-restore from an appended `.bak`. And **the relocation
  preview and the MOVE clauses must keep sharing `relocateFiles`**, or the paths
  the Files view lists stop describing what the restore does.
