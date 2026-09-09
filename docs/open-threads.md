# Open threads

Open work and settled decisions — nothing else. Close an item by deleting it;
add one whenever something is knowingly left undone. The "do not re-raise"
sections are the exception: they are not history, they are what stops a settled
question being reopened.

## Version support: the policy, and how it is held

The target is **SQL Server 2016 SP1 and later**. SP1 rather than RTM because
`procedure.go`, `scripter.go` and gossms's `internal/activity/block.go` emit
`CREATE OR ALTER`.

Three real on-premises instances exist — majors **13** (`win10cli\SQL2016`,
SP3), **14** (`win10cli\SQL2017`) and **17**. There is no major **15 or 16**
and no way to run one here (no Docker, 3 GB RAM), so those two are argued from
the catalog documentation and pinned by tests. A live Azure SQL Managed
Instance is the fourth environment; it is not a major in this sense and is not
swept — see § Azure SQL Managed Instance.

**The standing check is `TestLiveVersionSweep`** (`~/go/gosmo/live_versionsweep_test.go`):
it calls every read gosmo exposes and reports what the server rejects. Run it
on the *oldest* instance available after any query change. A query naming a
column the instance lacks fails the whole read, and `go test ./...` says
nothing — which is how nine such defects shipped before the first 2016/2017 run.

The nine are **closed**, verified 2026-09-04 on all three instances: 219 calls
/ 0 failures on 13 and 14, 233 / 0 on 17.

**Those counts are stale — the sweep has grown since and needs re-running on
13 and 17.** gosmo's working tree carries ~340 uncommitted lines of new `call`
entries (the Azure MI reads, database-scoped credentials and triggers, the
Phase 3 families). The only run recorded after the extension is major 14 on
2026-09-10, by port, 0 failures — see § Environment for why that one must be
reached by port. Re-sweep **13** first: it is the floor.

The gates, recorded because the next audit will otherwise re-derive them:

| Was | Held by |
|---|---|
| `STRING_AGG` (2017) in seven queries — partition functions and schemes, server and database triggers, foreign keys, audit specifications | `sql_agg.go` `commaList` renders the `FOR XML PATH`/`STUFF` form, valid from 2008. No raw `STRING_AGG` remains in non-test source. |
| Column Master Keys: `allow_enclave_computations`, `signature` (2019) | `security.go`, `colSince(major, SQLServer2019, …)` |
| Query Store options: the 2017 and 2019 columns | `query_store.go`, `colSince` per column |
| `Table.Detail`'s `ledger_type_desc` (2022), which killed **Table Properties > General** on every table | `table.go`, `colSince(…, SQLServer2022, …)` |
| `Statistic.Header`: DBCC returns 10 columns before 2019, 11 after | `statistics.go` binds **by column name**, with the failure named in the comment |

Two reads are refused outright on 13 rather than gated per column — the gate
working, not a defect: `Database.QueryStoreWaitCategoriesContext` and
`Database.QueryStoreWaitingQueriesContext` return `ErrUnsupportedVersion`. The
sweep counts those separately.

A sweep of 0 failures is not proof on its own — it passes just as happily if a
read was never reached. When re-verifying, confirm the reads were actually
*called*: the sweep's `call` helper takes a label, and logging it lists every
method swept.

## Azure SQL Managed Instance: supported since v0.0.10

A live MI (`t-qmi-01…`, EngineEdition 8, General Purpose Gen5) was audited
**2026-09-08** and the work closed **2026-09-09**: seven defects and one
missing feature, all fixed and verified live. The plan document that carried
the write-ups has been deleted; what outlived it is here.

**MI reports `ProductVersion` `12.0.2000.8`** while running engine build 18.0,
so every `colSince` / `VersionMajor` gate in gosmo silently degrades or refuses
a feature the instance actually has. **Gate on `EngineEdition` first** —
`internal/tui/edition_gate.go` is the edition's counterpart to
`permission_gate.go`, and holds the UI gating of the operations MI rejects.

What is left open is the `TO URL` follow-up below and Entra authentication.

**Database Properties > Resource Governance is read-only and Azure-only, by
design.** Every value on it changes by resizing the instance or the database,
which is a control-plane operation no T-SQL statement from a dialog can
perform. It pairs `sys.dm_db_resource_stats` (the reading) with
`sys.dm_user_db_resource_governance` (the scale it is a percentage *of*),
because a bare "CPU 0.6%" against an unnamed limit says nothing. The two fail
*differently* for a login without the rights — Msg 262 from the first, zero
rows from the second — and the page turns both into a note rather than an
error; `pageDatabaseResourceGovernance`'s doc comment carries the reasoning.

Two things the Activity Monitor's Instance tab settled that are easy to reopen:

- **The Activity Monitor's tab bar is a slice, not the constant array.**
  `visibleTabs()` filters `amAllTabs`; `amTabLabels` and the per-tab scroll
  arrays stay indexed by `amTab` and sized `amTabCount`. A withheld tab still
  has a scroll position, it is simply unreachable. `setTab` is the one gate —
  keys, clicks and callers all go through it — so a new conditional tab needs
  nothing but an `azureOnly`-style predicate.
- **The Instance tab's charts are scaled by the server's 15-second window, not
  by the panel's refresh rate,** and `internal/activity.Poller` exists so a
  pre-aggregated source does not go through `rates.go`. Both are in
  `drawInterval`'s and `Poller`'s doc comments. The IO ceilings are section-bar
  KPIs rather than chart axes because MI reports a fixed per-instance limit,
  not a series.

**What the `TO URL` work did not reach.** It was driven live on both
instances, but only as far as building and validating the statement:
executing a backup needs a shared access signature credential on the
container, and none exists on `t-qmi-01`. So these are open, and are the first
things to check when one does:

- **`WITH INIT` on a URL device.** The Back Up dialog hardcodes `Init: true`
  (it has no "append to media set" option —
  `TestBackupOptionsBuildTheExpectedStatement`'s "init is always set" subtest
  pins it). Block-blob backup to URL overwrites through `WITH FORMAT`, not `INIT`,
  so MI may refuse the statement the dialog builds. Not guessed at, because
  changing it blind would change every on-premises backup too.
- **`RESTORE ... WITH MOVE` on MI.** The Restore dialog emits MOVE clauses
  whenever the target is renamed, and MI places database files itself — the
  same fact that makes `CREATE DATABASE`'s file clauses fail with Msg 41918.
  The relocation options are deliberately *not* gated: withholding one that
  works is the worse error, and this was never driven.
- **Restoring from MI's own automated backup history.** Those `backupset` rows
  carry a NULL `physical_device_name`, which `BackupHistoryContext` now reads
  as `""`, so the Backup History source of the Restore dialog offers an entry
  with no device. Restoring MI's managed backups is a different mechanism
  (point-in-time restore through the control plane), not a `RESTORE` statement.

What *was* confirmed live, and settles the device-keyword question: MI answers
`RESTORE VERIFYONLY FROM DISK = N'https://…'` with Msg 41902 ("Unsupported
device type"), and the same statement spelled `FROM URL` with Msg 3078 about
the blob itself — the device type is accepted.

**Entra authentication on MI is untested**, and was out of scope of the MI
work deliberately — it needs an Entra-joined tenant, the same wall the Entra
*login* entry under Deferred scope describes.

## Release workflow: two jobs whose only failure mode is "did nothing"

**The `homebrew` push fix has still never run.** v0.0.10 shipped with an empty
tap: the job rendered `Formula/gossms.rb` and then guarded the push with
`git diff --quiet -- Formula/gossms.rb`, which reports no diff for a path git
has never tracked, so on the first release the job went **green having pushed
nothing**. Fixed in `.github/workflows/release.yml` (commit `3b566d0`) by
staging first and comparing against the index — the shape the `apt` job in the
same workflow already used. The v0.0.10 formula was then pushed to the tap by
hand (`radix29/homebrew-tap` commit `377fe7f`) and `brew install` driven end to
end on Linux, so the tap is correct today; the fix itself is unverified.
**Watch that job on the next tag and confirm the tap gained a `gossms <tag>`
commit — a green job is exactly what the bug looked like.**

Neither the tap nor the apt job asserts afterwards that what it publishes is
reachable, which is why the failure was invisible. Both artifacts *are*
reachable today (`https://radix29.github.io/apt/dists/stable/Release` and the
tap's `Formula/gossms.rb`, checked 2026-09-10); the assertion is still not in
the workflow.

Still untouched by any of this: `brew install` / `brew test` /
`brew audit --strict` **on an actual Mac** — nothing here has ever run macOS —
a `livecheck` block in the formula, and, on the Debian side, `dpkg -i` on a
clean container, arm64 execution and `lintian`. The formula is deliberately
**binary**, not build-from-source: `go.mod`'s active `replace` makes any source
build from a release tarball fail, and `go install …@<tag>` fail with it.

## Deferred scope (repeatedly, deliberately)

- **Changing a login's authentication kind in Login Properties.** New Login
  creates all five kinds (SQL, Windows, Entra, certificate- and
  asymmetric-key-mapped) and the Connect dialog offers the Windows and Entra
  methods, but Login Properties shows the kind as a static row: `ALTER LOGIN`
  cannot change it, so this would be a drop-and-recreate, losing the SID and
  orphaning every database user mapped to it. A deliberate refusal, not a gosmo
  gap. This is the standing answer to "why isn't this editable?".
- **No principal-browse picker.** A Windows login is typed as `DOMAIN\name`;
  there is no directory browse.
- **Entra logins stay unverifiable here.** `CREATE LOGIN ... FROM EXTERNAL
  PROVIDER WITH OBJECT_ID` is emitted and its grammar confirmed on a real
  server: on win10cli (no Entra) it and the bare `FROM EXTERNAL PROVIDER` fail
  with the *same* Msg 37525, so the parser accepted both. Whether a login is
  actually created needs an Entra-joined instance.
- **The Phase 3 tree families are read-only, and each for its own reason**
  (Properties shipped 2026-09-09; the plan document has been deleted, so this
  entry is the record). Read-only means *no create and no edit*: each family
  does have Script as, Delete, and — where SQL Server has the statement —
  Rename and Move to another schema. This is the standing answer to "why
  can't I create a rule?" and its four siblings:
  - **Rules and Defaults**: deprecated by Microsoft — `sp_bindrule` and
    `sp_bindefault` since SQL Server 2008 — and neither `CREATE RULE` nor
    `CREATE DEFAULT` has an ALTER. Offering a way to create one in a new tool
    would steer users onto a feature the server documents as going away. Use a
    CHECK or DEFAULT constraint.
  - **Assemblies**: `CREATE`/`ALTER ASSEMBLY` need the compiled binary, which
    no TUI dialog can supply. The two flags a form *could* set
    (`PERMISSION_SET`, `VISIBILITY`) are scripted rather than filled in.
  - **Alias, table and CLR types**: `CREATE TYPE` has no ALTER, so an editor
    would be a drop-and-recreate, and dropping a type is refused while any
    column, parameter or variable declares it.
  - **External data sources and file formats**: no ALTER on any supported
    major. **External libraries**: `ALTER EXTERNAL LIBRARY` replaces the R or
    Python package content — a binary, same wall as assemblies.
  - The exception is **Plan Guides**, whose General page enables and disables
    the guide (`sp_control_plan_guide`, gated on ALTER DATABASE). A disabled
    guide shapes no plan and is invisible in the tree but for its label suffix,
    so this one write is worth having; `sp_create_plan_guide` still has no
    ALTER, and its query text is matched character for character, so the page
    edits nothing else.
- **The CLR type, assembly and external-resource scripts are unit-tested
  only.** Every other Phase 3 script was generated on one throwaway database
  and executed against another on `win10cli` (major 17, 2026-09-10). These four
  could not be: the instance has no user assembly and CLR is off, and it has neither
  PolyBase nor Machine Learning Services, so there is no external data source,
  file format or library to script. Not a known defect — an untested path, and
  the one to exercise first if such an instance turns up.
- **External Tables and FileTables are untested in the TUI against a real
  one.** The Tables sub-folders shipped 2026-09-10. System Tables and
  Graph Tables were driven live on 13/14/17 with real rows; the External Tables
  *folder* needs PolyBase, which no instance here has, so its presence gate has
  only ever been seen answering "absent". A FileTable exists only inside
  gosmo's `TestLiveTableKinds` (major 17, FILESTREAM), never under the tree.
  Not a known defect — untested paths, and the first to exercise if such an
  instance turns up. Same class as the CLR/assembly/external scripts above.
- **A database snapshot's subtree still offers writes that the server
  refuses.** A snapshot gets Tables, Views and Programmability (Query Store,
  Storage and Security are deliberately withheld — a snapshot has no Query
  Store of its own, cannot be backed up, and shares its source's principals),
  and Delete or Rename
  on a table inside one is offered and then refused with "the database is
  read-only". Deliberate: the permission gate answers what the *login* may do,
  and a read-only database is not a permission — a third gate for it would have
  to cover every READ_ONLY database, not just snapshots. SSMS behaves the same
  way.
- **gosmo's README documents none of the Phase 3 families**, `TableKind` and
  the snapshot API included. One pass to make before the next gosmo tag.
- **System Data Types has no Properties dialog.** SSMS offers none either, and
  there is nothing to show about `int` that its name does not already say. The
  folder and its Detail Browser listing exist; the context menu deliberately
  omits the item.

## Permission gating: what is settled — do not re-raise

**Every securable class gossms can reach is gated, and the work is closed** —
classes 0, 1, 3 and 4 on 2026-09-04; 101, 105 and 108 on 2026-09-05. What is
kept is the live behaviour each gate rests on; every row is a *wrong* gate if
assumed the other way round.

### The behaviour table, probed live

Majors 13 and 17 for the server classes, 13, 14 and 17 for the database ones,
and the two-node Pacemaker cluster for class 108 — win10cli has no HADR.
Identical on every major measured, so a later difference is a behaviour change,
not a version gap.

| DENY ALTER on | withholds | does **not** withhold |
|---|---|---|
| `USER::u` (4) | `ALTER USER ... WITH NAME`, `DROP USER`, `ALTER ROLE ... ADD MEMBER u` | — |
| `ROLE::r` (4) | `ALTER ROLE r ADD/DROP MEMBER` | rename (`WITH NAME`), `DROP ROLE` |
| `LOGIN::x` (101) | `ALTER LOGIN` (rename, password), `DROP LOGIN` | being *added* to a server role |
| `SERVER ROLE::r` (101) | `ALTER SERVER ROLE ... ADD/DROP MEMBER` | rename (`WITH NAME`), `DROP SERVER ROLE` |
| `ENDPOINT::e` (105) | `ALTER ENDPOINT` | — |
| `AVAILABILITY GROUP::g` (108) | every `ALTER AVAILABILITY GROUP` — options `SET`, `ADD`/`REMOVE DATABASE`, `MODIFY REPLICA`, `FAILOVER` | `ALTER DATABASE ... SET HADR` (suspend, resume, join) |

Every refusal is Msg 15151 except the endpoint's, which is **Msg 6004**.
`HAS_PERMS_BY_NAME` reads 0 for the denied `ALTER` in every row, including the
ones the server goes on to allow — so the arm is asked **per action, not per
object**, which is what the paired rights (`rightAlterAnyDBRole` /
`rightAlterAnyDBRoleMembers`, `rightAlterAnyServerRole` /
`rightAlterAnyServerRoleMembers`) carry.

**Membership checks the member too at database scope and *not* at server
scope** — measured twice, reproduced outside the test, 2026-09-05. Adding a
class-4-denied *user* to an undenied database role is refused; adding a
class-101-denied *login* to an undenied server role goes through. So Database
User Properties > Membership carries the arm and Login Properties > Server
Roles deliberately does not (`TestLoginServerRolesDeclaresNoServerDenialArm`).

**There is no class 110.** Logins and server roles are both class **101
SERVER_PRINCIPAL**, told apart by the *principal's* `type_desc`
(`SQL_LOGIN`/`WINDOWS_LOGIN` vs `SERVER_ROLE`), as class 4 tells a user from a
database role. A design probing a separate class 110 gates nothing.

**Class 108 cannot be read from the catalog at all.** `sys.server_permissions`
records the DENY, but its `major_id` is an internal availability-group id that
**no supported view maps back to a name**. So gosmo asks `HAS_PERMS_BY_NAME`
once per group (`ProbedAvailabilityGroupPermissions`) — affordable at single
digits of groups where there are hundreds of logins. The consequence in gossms:
the answer is read as a *denial* only while the server-wide `ALTER ANY
AVAILABILITY GROUP` is held. A 0 otherwise means "holds nothing here", and
naming the group would replace a note the user can act on with one they cannot
(`TestAGroupIsNotDeniedWhenTheWideRightIsMissing`).

`ALTER AUTHORIZATION` is not evidence either way: it was refused on an
*undenied* role at both scopes, because changing an owner needs more than
`ALTER ANY` (CONTROL on the role, plus IMPERSONATE on the new owner).

### Where the per-row answers are deliberately not stated

Three pages list securables whose permissibility differs per row, and one
page-level banner cannot say that; each declares nothing rather than a banner
wrong for some rows: Login Properties > **User Mapping** (writes need `ALTER
ANY USER` in each mapped database), Login Properties > **Server Roles** (a DENY
on the role, not on this login), and Database User Properties > Membership's
per-role half.

`Server Role Properties > Owned Roles` also declares no arm: `ALTER
AUTHORIZATION` is refused on an undenied role too, so the DENY is not what
withholds it.

### The rest, unchanged

- **No other class is reachable.** gossms's `NodeType` list has no certificate,
  assembly, symmetric/asymmetric key, fulltext catalog, XML schema collection
  or Service Broker node, and column master/encryption keys, partition
  functions and schemes have no securable class of their own.
- **The column path is dormant in production — do not re-raise.**
  `ProbedObjectPermissions` is `ALTER`, which is not column-grantable, so
  `ColumnPermissions` is empty on every real connection and `DeniedOnAnyColumn`
  never withholds anything. Adding `SELECT` would not change that:
  `objectDenial` asks the column question only for the rights an action lists,
  and the only object-scoped right gossms declares is `rightAlterOnObject`.
  Firing the path needs a gossms *consumer* — an action gated on `SELECT` at
  object scope — and the only candidate, Select Top 1000 Rows, hands the user
  generated text rather than performing the read. The machinery stays, live
  T-SQL-verified. Two facts for whoever picks it up: `SELECT` in the probe list
  brings public's catalog-view grants back into `ObjectPermissions` (232 rows
  on HealthClinic) and they must **not** be filtered by `is_ms_shipped`, or a
  system view is withheld from a login that can read it; and
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
whose write the server still allows — hence the explicit sysadmin bypass.
**Ownership needs no exception**: SQL Server refuses a DENY aimed at a
securable's owner, and `ALTER AUTHORIZATION` *deletes* an existing DENY row as
it transfers ownership. Schema scope needs its own catalog read
(`explicitSchemaCapabilityQuery`, `E:` rows) because `HAS_PERMS_BY_NAME`
returns 0 for a schema permission never granted exactly as for one explicitly
denied, and withholding on that would empty the menus of every login working
through a database-wide grant.

## gosmo: deliberate API decisions

- **No `Drop*` write method carries `IF EXISTS`.** A review found the family
  split down the middle: dropping an already-gone view reported "View deleted"
  and dropping a sequence reported the server's refusal, from the same gesture.
  A bare DROP everywhere was chosen so that "deleted" means deleted; a caller
  wanting idempotence ignores the error, which the library cannot decide for
  it. `TestDropStatementsAreNotIdempotent` pins it. Scripter's *scripts* keep
  `IF EXISTS` — DROP-and-CREATE output exists to be re-run.
- **`CertificateByName` answers `(nil, nil)` on absence**, deliberately
  unchanged when `ErrNotFound` went in: making it error is a breaking change to
  a published contract, and its callers branch on absence as the ordinary case.
  The three surviving conventions are documented on `ErrNotFound` itself;
  `TestLiveCertificateNotFoundIsNilNil` pins both directions.
- **A missing principal and an invisible one are the same thing to
  `ErrNotFound` — SQL Server's doing, do not fix it in gosmo.** Metadata
  visibility hides a principal the caller lacks `VIEW ANY DEFINITION` on by
  returning **zero rows, not an error**, so an existing login reads as absent;
  pinned by `TestLiveNotFoundCannotSeePastMetadataVisibility`. The answer is
  idempotence at the write, not a better sentinel. Note `isAlreadyExists`
  matches by substring; its `15023` arm is the *user* code, and logins raise
  15025.
- **`JobStateCancelling` and `JobStateRunning` were removed** 2026-09-04 from
  gosmo's `HEAD`, the author's call being that `v0.0.x` is a boundary that can
  carry the break. They named states Agent's encoding does not have.
  `JobState`'s real encoding, from `xp_sqlagent_enum_jobs`: 1 Executing,
  2 WaitingForWorker, 3 BetweenRetries, 4 Idle, 5 Suspended,
  6 WaitingForStepToFinish, 7 PerformingCompletionActions, 0 meaning a job
  Agent does not run itself.

Three things about the job-state read are load-bearing and easy to undo:

- **The `INSERT ... EXECUTE` table shape must match the extended procedure's
  thirteen columns exactly** — hence `jobStateColumns` copied from
  `sp_get_composite_job_info` rather than trimmed to the two columns gosmo
  reads.
- **A failed state read is not a failed listing.** `applyJobStates` swallows
  the error and leaves the `sysjobactivity` derivation in place. With Agent
  stopped, `xp_sqlagent_enum_jobs` returns zero rows rather than failing, so the
  fallback is reached by covering no job. The derivation emits the same encoding
  (`THEN 1 ELSE 4`) so the two paths cannot disagree.
- **`jobIsRunning` answers "unknown" for `JobStateUnknown` and
  `JobStateSuspended`, and `jobStateRefusal` then refuses nothing.** Unknown
  means Agent had nothing to say; Suspended is genuinely ambiguous — sp_help_job
  groups it with idle, while a suspended job still holds a session. Letting the
  server answer beats refusing an action that would have worked.

## Environment: what the instances can and cannot do

- **win10cli can never be a third availability replica.**
  `SERVERPROPERTY('IsHadrEnabled')` and `IsClustered` are 0 and
  `sys.dm_os_cluster_nodes` is empty; the host is **Windows 10 Pro**, which has
  no Failover Clustering feature, and SQL Server will not enable Always On for a
  non-WSFC node. Even with it, a Windows replica in a Linux availability group
  is unsupported — a distributed AG is the only cross-platform shape. Do not try
  to add it to AAG1.
- **It can do everything an availability group is built *on top of*.**
  Mirroring endpoints, certificates, the principals owning them and the CONNECT
  grants between them are plain T-SQL with no HADR precondition, and gossms
  checks HADR only on the instance the endpoint dialog is *opened from*. That is
  what lets Add Replica's Connect read a real endpoint off a third instance.
- **It is ubusql1's mirroring peer, deliberately left that way.** win10cli holds
  a database master key, `win10cli_Cert`, ubusql1's imported `ubusql1_Cert`,
  `ubusql1_login`/`ubusql1_user`, and endpoint `AGEP` STARTED on 5022; ubusql1
  holds the matching `win10cli_*` principals and certificate. A third instance
  with a STARTED endpoint is exactly what Add Replica's Connect needs, and
  rebuilding it costs a live run. It is inert.
- **`xp_cmdshell` is on on win10cli and can never be on on ubusql1.**
  `sp_configure 'xp_cmdshell', 1` on Linux fails Msg 15392 ("not supported by
  this edition"); file moves on ubusql1/ubusql2 go through ssh. Anything
  depending on xp_cmdshell is Windows-only by construction. Linux still *lists*
  it in `sys.configurations` at 0, which is why `xpCmdshellRow` tests
  `ServerInfo.Platform` rather than the missing-option path.
- **A live AG test's teardown reaches `DROP AVAILABILITY GROUP` on a real
  cluster.** `liveDropGroupEverywhere` runs *before* the create as well as
  after, so `-liveag-create-name` is a flag that drops groups. It refuses,
  fatally, any group whose cluster type is not NONE. Know this before touching
  that test.
- **`TestLiveAvailabilityGroupOperations` deliberately skips Drop and
  RemoveReplica** against AAG1; only add/remove database, suspend/resume, the
  listener round trip and the failover refusal run there.
- **A named-instance connection to `win10cli\sql2017` fails on the *second*
  concurrent connection.** Connecting by name and then doing anything that
  needs a second pooled connection while the first is pinned — a listing whose
  rows are still open when a per-row read runs, `SecurityPoliciesContext` being
  the one the sweep hits — fails with `acquire connection: no instance matching
  'sql2017' returned from host 'win10cli.fritz.box'`. It is the SQL Browser
  declining the second resolution, not a gosmo defect: the identical run
  against `win10cli.fritz.box:55253` is 0 failures, and `sql2016`, whose
  Browser answers reliably, is clean by name. Reproduced twice, 2026-09-10.
  **Connect to 2017 by port for anything multi-connection**, and do not chase
  it as a query-compatibility finding — it names no column and no version.

## FILESTREAM: what the live run settled — do not re-raise

Database Properties > Files was confirmed against a real FILESTREAM database on
win10cli, 2026-09-05. `internal/tui/live_filestream_test.go` (build tag
`livedb`) is the run, and re-runs it.

- **`FileGroupsContext` returns a FILESTREAM filegroup like any other** — name,
  files and all. The Filegroup picker has therefore always listed it, and
  `preservingItems`' widening was never needed for this case.
- **The filegroup is the whole of what makes a file FILESTREAM.** ALTER
  DATABASE ADD FILE has no file-type keyword: the same clause aimed at a ROWS
  filegroup produces an ordinary data file — measured, a file added to PRIMARY
  with an extensionless path came back as ROWS. So the Type picker has nothing
  to offer and `addableFileTypes` stays two items.
- **SIZE and FILEGROWTH are refused on a FILESTREAM file** with Msg 5509.
  **MAXSIZE is accepted**, which the message's shape does not suggest — both
  measured, not reasoned about.
- That combination was reachable, so the page *had* a live defect: a file added
  into the FILESTREAM filegroup failed the whole Apply with 5509. The page now
  omits both clauses for such a file, greys the two spinners, and records the
  file's type as FILESTREAM so the grid does not claim ROWS.
- `gosmo.FileGroup` gained `Type` (`sys.filegroups.type_desc`) and
  `IsFileStream()`. Note `is_default` is per filegroup *type*: a database with a
  FILESTREAM filegroup reports two defaults, both true.
- The scripted fakes now report `max_size` -1 for a FILESTREAM file, not 0.

**Enabling FILESTREAM on win10cli is not something a SQL connection can do** —
the RsFx0800 filter driver and the share belong to SQL Server Configuration
Manager and need an OS administrator. Setting the registry `EnableLevel` and
`sp_configure` from `xp_cmdshell` succeeds and achieves nothing. The live test
skips with that explanation when the effective level is 0.

## Always On: what is deliberately out of scope

All seven phases are built. These are decisions taken while building them.

- **New Availability Group does not roll back a group whose CREATE succeeded**,
  and neither does Add Replica. Every ordinary reason a secondary's JOIN fails
  is checked *before* the CREATE — peer reachable, Always On enabled there,
  endpoint present, STARTED and still at the recorded address, rights to join —
  and any failure refuses with nothing created. What survives is the peer that
  dies between check and JOIN, and there the group is left alone: a rollback
  would destroy what the user asked for on the strength of one unreachable
  instance, and could not even be complete — a group dropped from the primary
  stays in the *secondary's* `sys.availability_groups` and needs a local DROP
  there, which is the residue a rollback exists to prevent.
- **The create dialog has no Read-Only Routing page**, unlike SSMS's. Routing is
  a per-replica setting AG Properties already covers.
- **The endpoint dialog assumes a full mesh and one shared master key
  password.** Every instance gets every other's certificate, which is what an
  availability group needs; the one password field is used for whichever
  instances have no database master key yet.
- **Add Replica does not offer initial data synchronization.** AUTOMATIC seeding
  covers the case SSMS's share-based route works around; MANUAL seeding means
  restoring each database by hand and then using Join to Availability Group on
  the secondary's copy, which is in the tree.
- **A listener address cannot be removed and a listener cannot be renamed.**
  `ALTER AVAILABILITY GROUP ... MODIFY LISTENER` has no statement for either, so
  both mean REMOVE LISTENER and ADD LISTENER. Listener Properties says so rather
  than offering buttons that would only work on unwritten rows. Under an
  EXTERNAL cluster type an added address is recorded OFFLINE, since the external
  cluster manager owns it.
- **Failover cannot be done in T-SQL under `cluster_type = EXTERNAL`** —
  handled, not open: `agFailoverRefusal` explains it and names Pacemaker instead
  of sending the statement. EXTERNAL rejects both `... FAILOVER` and
  `... FORCE_FAILOVER_ALLOW_DATA_LOSS` with `Msg 47104`; `NONE` rejects only the
  lossless form, with `Msg 47122`, and allows the forced one.
- **The New AG page offers no backup from inside the dialog.** Both
  `ALTER AVAILABILITY GROUP ... ADD DATABASE` and `CREATE AVAILABILITY GROUP ...
  FOR DATABASE` refuse an unbacked-up database with **Msg 1475**, so the page
  applies the same rule and its "Not offered" line names the Backup dialog. The
  backup history is the wrong signal in both directions — a database whose
  `msdb` history was deleted still joins, and one round-tripped through SIMPLE
  does not though its history still shows the full backup.

Rules about peer credentials that are easy to undo:

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
  login still wins over the parent's on purpose — that is the feature. Deleting
  the retry and ignoring the resolver each break a different half.
- **`ServerConn.Peer` blanks `Opts.Database`, deliberately.** A database named
  in the connection string fails the *connect*, at ping time, and everything
  `Peer` reaches is server-scoped. The other three `sc.Opts` clones — the query
  panel's and the Activity Monitor's two — target the same instance and keep the
  database on purpose.
- **The peer failure cache is short on purpose.** `recordPeerFailure` holds the
  last connect failure per `InstanceKey` for `peerFailureTTL` (30s) so a
  blackholed primary does not charge the driver's 15s connect timeout to every
  folder expansion. It is invalidated by a successful direct `File > Connect` to
  that instance and by a Refresh anywhere in the Always On subtree, both
  recursing into cached peers because a chained read records its failure on the
  peer, not on the connection the user is acting on. A Refresh elsewhere leaves
  the cache alone. The 15s timeout stays: a peer across a WAN may need it.

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

- **The Windows event log is out** — needs WMI, out of scope for a no-CGO
  portable build. It is the half of SSMS's left-pane checkbox tree the merged
  view (the file selector's **Select Files...**) does not cover, and this
  exclusion is not reopened by it.
- **A merged selection is one family.** Archive numbers are not comparable
  across families and the family selector is what the file list, Recycle and
  the enumeration all key off, so the checklist offers the current family's
  files only. `logFileRef` carries the family anyway, so a cross-family merge
  is a UI change rather than a data-model one.
- **A cycle re-anchors a merged selection to the current log, not a single-file
  one.** The cycle renumbers every archive and deletes the oldest, so a *set*
  chosen by number would silently come back as a different set. A single-file
  view keeps its number, which is what it has always done — the user asked for
  "Archive #1" and gets whatever is now Archive #1.
- **The toolbar's Filter box and "Search..." are different features — do not
  merge them.** Filter narrows what was read, instantly and with no round trip,
  reporting "N of M match". Search edits `xp_readerrorlog`'s own arguments 3-6
  and changes what the server returns, which is what a log too large to read in
  one go needs; the status line names it, because "no entries" on a searched
  read otherwise reads as an empty log. The client-side pass runs over whatever
  came back, so the two compose. Two things only a live run says: the date
  bounds must be sent as **text** (`YYYY-MM-DD HH:MM:SS`) — a typed datetime
  parameter is rejected with "The format for the date filter is incorrect" — and
  the two search strings are **AND**-ed, not alternatives.

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
  Properties page, as in SSMS; the folder's context menu opens it.
- **Plot History does not dim Top or the execution floor.** gosmo's
  `QueryStoreTrackedQueryContext` ignores `Top`, `MinExecCount`,
  `MinRegressionPct` and `QueryIDs` outright — it is one query over every
  interval — so neither selector changes what the chart plots. They stay live
  because the *report grid* below the chart is still on screen and still
  honours them: dimming them for the chart's sake would make the rows
  unfilterable while it is up. The chart's own title names the metric and the
  statistic it was read with, which are the two that do reach it.

- **The panel reads on demand only** — no auto-refresh timer. Every read is
  one-shot and bounded, so nothing queues behind the shared host connection.
- **One metric and one statistic across all seven views.** Each report carries
  its own default (Total for the two ranking by accumulated cost, Avg for the
  rest) and the panel opens on it, but switching views afterwards keeps what the
  user chose. SSMS keeps them per view.
- **The baseline of Regressed Queries stays inside the reported window.**
  gosmo's default — the equally long window immediately before `From` — is right
  for a caller that picked its own range and wrong for both surfaces here,
  because it makes the report need twice its window of history before it can
  show a row.

## Object Explorer folder filter: what is deliberately out of it

- **Only seven families push the filter down** — Tables, Views, Stored
  Procedures, Functions and the three System * variants, through gosmo's
  `ObjectFilter` and the `…FilteredContext` listings. The other filterable
  folders (sequences, synonyms, both trigger families, databases, logins, users, roles,
  schemas, partition functions/schemes, the Always Encrypted keys, security
  policies) stay client-side: they are small, and the clause builder is
  family-agnostic if that stops being true. The push-down rules a plausible
  simplification removes are in `docs/db-rules.md`.
- **Owner and Durability Type are not offered on Tables, deliberately.** SSMS
  offers both; each is one `TableDetail` query per table, so listing them means
  a folder-wide detail fetch before the pane can draw a row. The intended
  trade — do not re-raise.
- **Filters are per-session.** `App.savedFilters`, keyed by `filterKey` rather
  than by node pointer, brings a folder's filter back on a reconnect within the
  session. Writing filters to `config.json` stays out: SSMS keeps them for the
  session only, and restoring one at startup against a folder whose objects have
  changed is not wanted.

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
  Job...`** though Rename came off every system object: SSMS permits it and msdb
  raises no objection.
- **Neither audit object offers Rename from the tree.** A server audit
  specification has no `MODIFY NAME` form at all — a parse error, not a
  permission failure. An audit has one, but only while disabled, so a tree
  rename would silently stop auditing for its duration; the rename lives on the
  Properties page, where the disable is visible in Script Changes.
- **Multi-select delete lives in the Object Explorer Details pane and nowhere
  else.** `controls.TreeView` has a single selection, so SSMS's "Delete Object"
  dialog listing several objects will never come from the tree. Two limits are
  the design: **only schema-scoped objects are deleted as a set**, while a
  database, a login, a server role, a user, a database role or an Always
  Encrypted key is deleted on its own (`objectOp.solo`), because a typed
  confirmation asks for one object's name and a principal's drop reaches past the
  object further than one shared warning can describe; and **Rename and Move to
  Schema stay in the tree**, neither meaning anything applied to a set. The solo
  rule is the *selection's*: one login selected in the pane still deletes. A
  view whose rows are not objects — Property/Value, a Query Store report, System
  Databases, a log listing — offers nothing, which is correct rather than a gap.

## Clipboard in a dialog: what is deliberately out of it

- **A dialog with no text entry has an inert clipboard.** Help, About / Object
  Dependencies, Confirm, Query List and Background Tasks do not implement
  `core.ClipboardHost`, so Ctrl+C there does nothing. It previously copied
  whatever the panel behind the dialog had selected, which was never the thing
  the user was looking at.
- **Key Diagnostics' `syncIfDirty` must not rebuild the editor while it has a
  selection.** The log lives in a read-only `controls.Editor` and the dialog is a
  `core.ClipboardHost`. It records the very keys used to copy from it, so
  `Ctrl+A` is itself logged and the next frame's `SetText` — which resets cursor,
  scroll *and* selection — would drop the selection before the `Ctrl+C` arrived.
  Status History does not need this, and a "simplification" would undo it.

## Server-level families: what is deliberately out of them

- **Viewing audit records is absent.** `sys.fn_get_audit_file` is a feature the
  size of the Log File Viewer — a reader, a grid, a filter, paging over rolled-
  over files. SSMS's "View Audit Logs" command is therefore not offered at all
  rather than offered and empty.
- **Registering a cryptographic provider is not offered.** The folder lists
  what `sys.cryptographic_providers` records and stops there: CREATE
  CRYPTOGRAPHIC PROVIDER takes a DLL path on the *server's* filesystem, and
  SSMS answers that with a file browser this build has no way to offer. So the
  folder has no New item, no Delete and no Script verbs — a read-only family
  that declares no rights.
- **Creating TSQL, Service Broker and SOAP endpoints is out.** Endpoints are a
  read/state/drop family; New Database Mirroring Endpoint stays the only
  creation path.
- **Tape and virtual backup devices** are listed, scripted and dropped but not
  creatable — the New Backup Device dialog offers disk only, as SSMS's does.
- **Audit Properties is one page**, where SSMS has General and a separate Filter
  tab. `ALTER SERVER AUDIT` replaces every setting at once, so a second page with
  its own apply would either write a second ALTER reverting the first, or need
  the filter's value before the page holding it had been opened.
- **An audit's destination is editable from Properties**, where SSMS greys it.
  `ALTER SERVER AUDIT` accepts a new `TO` clause on a disabled audit and the
  page's apply already runs inside a disable window, so the only cost is the one
  the page's note states: switching away from FILE discards the file block, and a
  FILE audit resumed later starts a new audit file. The file rows are always
  present and gated on the dropdown rather than on the destination the audit
  loaded with; an empty path under FILE is refused before anything is disabled,
  because the server's own answer (Msg 33072) arrives only after the audit has
  been turned off.
- **A database audit specification's actions cannot be edited in place.** The
  Properties page unticks one to drop it and adds a replacement through its own
  four fields, because `ALTER DATABASE AUDIT SPECIFICATION` has no form that
  changes a clause — only ADD and DROP — and an "edit" would be a drop and an
  add whose failure between the two leaves the action gone.
- **One specification per audit per database is SQL Server's rule** (Msg 33230,
  "An audit specification for audit 'x' already exists"), so both the New dialog
  and Properties list only the audits still free in that database. The
  Properties page keeps the specification's own audit in the list, or it would
  open showing something other than what it is bound to.
- **All six filterable folders' filters are client-side only.** Credentials,
  Audits, Server Audit Specifications and Server DDL Triggers offer Name and
  Creation Date; Backup Devices and Endpoints offer Name alone, because neither
  `sys.backup_devices` nor `sys.endpoints` records a creation date and a
  criterion over a zero `nodeData.CreateDate` rejects every row. Cryptographic
  Providers offers no filter at all — a handful of rows at most, and
  `sys.cryptographic_providers` records no creation date either.

## Database-scoped credentials: what the design settled

- **CONTROL on the database is the whole right set, and `ALTER` is deliberately
  not beside it.** Probed live on win10cli (major 17, 2026-09-08) with a
  `WITHOUT LOGIN` user: CREATE/ALTER/DROP DATABASE SCOPED CREDENTIAL all went
  through under `GRANT CONTROL ON DATABASE` and all three were refused under
  `GRANT ALTER ON DATABASE`. Every other database-scoped set in
  `permission_gate.go` pairs its narrow right with `rightAlterDatabase`; adding
  it here "for symmetry" would offer New/Delete/Properties to a principal the
  server then refuses. `ALTER ANY CREDENTIAL` is not the narrower twin either —
  it is server-scope, and `HAS_PERMS_BY_NAME` asked of a *database* returns NULL
  rather than 0, which a gate built on it would read as "unknown" forever. See
  `dbScopedCredentialRights`.
- **No ALTER script verb, and no rename.** The secret cannot be read from any
  catalog view, so every generated script carries `<insert secret here>` in its
  place — an ALTER verb would be a statement that silently rewrites the stored
  secret to a placeholder. There is no `ALTER DATABASE SCOPED CREDENTIAL ... WITH
  NAME` and no `sp_rename` class for one, so Rename is not on the menu.
- **Changing the identity with the password blank is refused, not applied.**
  `ALTER DATABASE SCOPED CREDENTIAL` resets both halves every time and an
  omitted `SECRET` sets the stored secret to NULL, so a bare identity change
  destroys a secret nothing can restore. Same rule, same wording, as the
  server-level Credential Properties page.
- **`IDENTITY = N'SHARED ACCESS SIGNATURE'` cannot have its identity altered**
  once the credential is bound to an active database file — Msg 33253,
  "Failed to modify the identity field of the credential ... because the
  credential is used by an active database file", hit live on major 17. The
  page surfaces the server's message; there is nothing to gate on beforehand,
  since the binding is not visible from `sys.database_scoped_credentials`.

## Database-scope DDL triggers: what the design settled

- **A database has no flat Triggers folder any more, and that is the point.**
  A DML trigger belongs to one table or one view and is listed under that
  object's own Triggers folder, which is where SSMS puts it and where it was
  already listed — the database-wide roll-up listed every one of them a second
  time. Keeping it would have put a folder called "Triggers" (DML) beside one
  called "Database Triggers" (DDL, `parent_class = 0`), which reads as a
  distinction without a difference. `Database.TriggersContext` still exists in
  gosmo and is still the database-wide read; gossms simply has no folder for it.
- **A view is no longer a leaf.** It carries INSTEAD OF triggers, and the
  roll-up was the only place they had been reachable, so `NodeView` gained the
  same Triggers folder a table has. `Database.ObjectTriggers` is the by-name
  read behind both, because gosmo's `View` is a plain row struct with no
  back-pointer to its database and so can carry no method of its own.
- **The DDL family is read/enable/disable/script/drop — there is no New
  dialog**, the same shape as Server Triggers one scope up. A CREATE TRIGGER
  body is T-SQL a form cannot usefully build, and SSMS offers no such dialog
  either. Editing one is Script Database Trigger as > ALTER To.
- **The Database Triggers folder's filter is client-side and offers Name and
  Creation Date**, matching the Server DDL Triggers folder for the same reason
  the six server-level folders give.

## By design — not issues, do not re-raise

- **Comment prose is not a cleanup target.** The per-file semantic read of every
  `.go` file is done; what stands was checked against the code it describes. Two
  classes are protected outright: the long comment blocks `CLAUDE.md` § Coding
  conventions names (`app.go`, `datagrid.go`, `secret.go`,
  `propsheet/common.go`), and the failure-naming comments in
  `query_store_panel.go`, `prop_grid_helpers.go` and similar — each names a
  shipped bug a plausible simplification would bring back. What drift does recur,
  worth knowing when a comment is *edited*: a doc naming the wrong caller after a
  helper moved, a count of anything (nearly every count found was wrong), a claim
  about which types implement an interface, a cross-reference to the wrong
  document, and an inverted sentence that reads fluently either way.

- **Which databases a dropdown offers is settled** and lives in
  `internal/tui/database_list.go`. The rule turns on when the name is resolved: a
  name stored now and used later (job step, alert, login default database,
  restore history) lists every database including system and non-ONLINE ones,
  because it is opened when the job runs; a name acted on immediately lists only
  what the action will accept. Backup is the only dialog in the second class, and
  both its exclusions are hard server restrictions — `BACKUP DATABASE tempdb` and
  a backup of an OFFLINE database each fail with "BACKUP DATABASE is terminating
  abnormally". Do not "unify" the two lists.
  The trap that comes with the filter: the Backup dialog is opened *on* a
  database from the Object Explorer, and its dropdown is swapped asynchronously
  afterwards. `setDatabaseItems` keeps a selection the incoming list doesn't
  contain, at the front. Without that, right-clicking an OFFLINE database and
  choosing Back Up silently retargets the dialog at whichever database sorts
  first. Any future narrowing of a dropdown a dialog can be opened on needs the
  same treatment.

- **`indexOf` against a sentinel list is right for two classes and wrong for a
  third.** Right for the fixed vocabularies (recovery model, page verify, Query
  Store state and capture mode, compatibility level) — where the list is written
  out in the page, the write is `items[row.Selected()]`, and a value outside it
  means gossms is behind SQL Server — and for the New-X dialogs' own defaults,
  whose value is one of the list by construction. Wrong for any name the *server*
  supplied against a list read separately, which goes missing whenever the object
  is dropped between the two reads or the caller cannot see it; those read the row
  back with `preservedValue`/`changedTo` instead.
  `TestIndexOfSentinelListFallsBackToSentinel` pins the helper.

- **"A job whose owner login was dropped" is not reachable by dropping a
  login.** SQL Server *refuses* to drop a login that owns a job. A **schedule**
  has no such protection: dropping its owner login succeeds and
  `SUSER_SNAME(owner_sid)` goes NULL immediately. So an orphaned *job* owner
  needs a different route — an msdb restored from another instance, or a Windows
  principal removed from AD — and is rarer than the schedule case.

- **Start/Stop Job are deliberately not greyed out.** They read the job's state
  first and refuse in the app's own words ("Job X is already running" / "is not
  running"), refreshing the node either way. The read is free — both actions
  already fetched the job. Gating the menu item instead would hide a legitimate
  Stop for a job that started running since the folder was loaded.

- **`sp_delete_jobstep` is not symmetrical with `sp_add_jobstep`.** It silently
  resets a reference to a step at or after the deleted one to "quit with success"
  rather than following it, which is what `ReorderSteps`' repair pass exists for.
  That pass is invisible to a test that moves the *last* step, because then no
  reference points past the delete — the live test moves a middle step for
  exactly that reason.

- **Merging the two user-mapping page builders is a deliberate non-goal.** Both
  pages use `wireGridEditor`, which is where the duplication that actually caused
  a bug lived; merging the two page *builders* was costed and rejected (six
  injection points, two different row structs, two unrelated applies). Do not
  re-propose without new evidence.

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
    bounds it, to within one document. A `redoBytes` cap would buy that document
    back in exchange for silently dropping the deepest redo.
  - `applyStep` slicing `[st.row : st.row+st.newLen]` without a bounds check is
    the intended failure mode. The invariant is `pushUndoSpan`'s caller promise,
    and a violated promise means the document is about to be corrupted; clamping
    would turn that into an undo that quietly restores the wrong text. The panic
    is the more useful failure. Do not add a clamp.

- **gosmo untagged past its current tag with `go.mod`'s `replace` active is the
  intended development state**, not a release blocker. Tagging gosmo, bumping
  `require`, and commenting out the `replace`/`ignore` pair are steps of the
  release process itself (ARCHITECTURE.md § Developing against a local gosmo
  checkout). A CI release build not resolving gosmo mid-development is expected.

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
  the plain 60-column popup. Highlighting inside the popup is the thing not to
  do: wrap mode resolves each drawn column through `styleAt` (`editor_draw.go`),
  a linear scan of the logical line's runs, chosen so a `varchar(max)` cell costs
  work proportional to the ~15 visible rows rather than to the value. That scan
  is fine against SQL's few coarse runs and not against a highlighter emitting
  one run per token over a whole XML document. Routing to a panel, which draws
  unwrapped, sidesteps it.

- **The Databases folder's one round trip per database is intended.**
  `FILEPROPERTY` reports on the *current* database only, so
  Data/Log/Unallocated/AvailLog cannot come from a server-wide view the way the
  Tables folder's aggregates do. Collapsing it into a single dynamic batch does
  work and is faster (11-12ms vs 20-21ms over four databases), but one
  unreachable database fails the whole batch and every row loses its sizes,
  where the fan-out degrades to `N/A` in that one row alone. The fan-out is also
  concurrent 8-wide, so it costs `ceil(N/8) x RTT`. If the folder is ever
  reported slow, the batch goes in as a *fast path* with the fan-out as the
  fallback on any batch error — never as a replacement.

- **Restructuring `internal/tui` is closed. No file-split or package-split
  candidates are outstanding.** Costed and re-measured against a type-checked
  cross-file reference graph (549 real symbol edges) and rejected on the numbers.
  What shipped instead is `internal/tui/sqlparse`, the only part of the package
  with *zero* outbound references. The earlier "P5" file-split list was never a
  standing rule: 31 non-test files exceed 400 lines, fifteen directly in
  `internal/tui`. That is not a reason to re-open this.
  The negative results, each a proposal a future review will otherwise reinvent:
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
  transaction's text — and the `cross apply` is what keeps system sessions out.
  **Author's call: as is.**

- **The Activity Monitor probes `VIEW SERVER STATE` once per collector, so twice
  per panel open.** Hoisting it to a single shared check was dropped: the Retry
  control starts a *new* collector after a transient failure, and a cached
  permission answer would make that retry fail without asking the server. One
  extra round trip on open is the cheaper mistake.

- **`counterQueryFor`'s `RTRIM(instance_name) IN ('', '_Total')` filter drops no
  counter the panels read.** Raised twice on the grounds that `RTRIM(NULL)` is
  `NULL` and `NULL IN (...)` is false. win10cli (Windows) has no NULL
  `instance_name` rows at all; ubudock (Linux) has exactly five, all
  `SQLPAL:Host Memory` / `SQLPAL:Guest Memory` rows that are not in
  `counterNames` and have no gossms consumer. All 33 names in `counterNames`
  resolve through the filter on both builds. Do not add an
  `OR instance_name IS NULL` arm.

- **`formatValue`'s `case float32` is unreachable but kept.** go-mssqldb returns
  `float64` for both `REAL` and `FLOAT`. It is correct if the driver ever
  narrows, and `formatFloat` already takes the bit size.

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
  string's. The proposed fix was three extra round trips per grant and was
  reverted.

- **`charts.StackedHistoryChart.Draw` stays, though nothing in the binary
  reaches it.** `doc.go` advertises `Draw` as the entry point and all six chart
  types implement it. The dashboard uses `DrawFrame` only because it also wants
  the time row. Removing one of six would break the package's one uniform method
  for four lines. `HistoryChart.Draw` was in the same position until the Query
  Store panel's per-query history became its caller.

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
  login without it must still be able to create ordinary SQL and Windows
  logins — an empty picker becomes a refusal naming what is missing, not a
  broken dialog. Settled live alongside it: neither `DEFAULT_DATABASE` nor
  `DEFAULT_LANGUAGE` can be set for a certificate- or asymmetric-key-mapped
  login, in CREATE *or* ALTER, so the page refuses either rather than creating
  the login and then failing.

- **Per-file destinations in Restore (SSMS's editable "Restore As" column) were
  deliberately not built**; the folder-level choice covers what the dialog's
  width allows. Two rules from that work outlive it. **The backup set number
  must not get re-scattered**: the restore itself, the MOVE clauses and the Files
  Included panel all take it from `backupSetNumber` in `restore_dialog_ops.go`,
  and two of the three deriving it separately is what produced the "Logical file
  'x' is not part of database 'y'" failure on every rename-restore from an
  appended `.bak`. And **the relocation preview and the MOVE clauses must keep
  sharing `relocateFiles`**, or the paths the Files view lists stop describing
  what the restore does.

- **A whole-repo review on 2026-09-04 swept both repos for these and found
  nothing.** Recorded so the next review spends its time elsewhere. `go build`,
  `go vet`, `gofmt -l`, `go test ./...` and `go test -race ./...` were clean in
  both. `staticcheck ./...` reported only the two U1000s above and one ST1005 in
  a gosmo *test* error string. In gosmo: every `Query`/`query` pairs
  `defer rows.Close()` with a `rows.Err()` check — the fifteen apparent misses
  delegate to a shared scanner that checks it; there is no `FooContext` without
  a plain `Foo` wrapper; no query runs inside a `rows.Next()` loop. In gossms:
  exactly one goroutine is spawned outside `safego`/`safegoRepair`, the backfill
  worker, deliberately taking both the label and the recover by hand; no
  `context.Background()` appears in a request path, every fetch deriving from
  `sc.Context()` with a named timeout; every keyword-valued interpolation into
  SQL goes through an allowlist. Two things checked against real source rather
  than memory: `mssql.ServerError` really is fatal-only
  (`go-mssqldb@v1.9.4/error.go:79`, and `mssql.go:1352` "Ignore non-fatal server
  errors"), so gosmo's `IsRetryable` treating it as retryable is correct; and
  the `endpointRoles`/`endpointEncryption`/`endpointAlgorithms` allowlists are
  real and applied.

- **Distribution credentials, recorded once.** Homebrew:
  `HOMEBREW_TAP_DEPLOY_KEY` on `radix29/gossms`, a write-enabled deploy key on
  `radix29/homebrew-tap`, not a PAT. APT: `APT_REPO_DEPLOY_KEY` (write deploy
  key on `radix29/apt`, published through GitHub Pages at
  https://radix29.github.io/apt) and `APT_REPO_GPG_KEY` (dedicated RSA-4096
  signing key, fingerprint `468B0CE5FFDEE82439741EC393F25CAB61497D93`; the
  public half is `gossms.asc` in the repo root). `docs/homebrew.md` and
  `docs/ppa.md` were folded into the two release jobs and this entry; the jobs
  in `.github/workflows/release.yml` are the reference. What is still unrun is
  in § Release workflow above.

- A **Launchpad PPA** was rejected, not forgotten: builders have no network and
  Ubuntu's packaged Go is 1.22 on 24.04 LTS, 1.26 on 26.04 LTS, against
  `go.mod`'s 1.27. Two measurements worth not repeating — `go mod vendor` fully
  resolves the `replace ../gosmo` (builds with the sibling deleted and
  `GOPROXY=off`, +23 MB), and **neither gossms nor gosmo actually needs Go
  1.27**: a real offline `go1.26.0` builds both once the `go` directive is
  lowered in *both* `go.mod` files. Go 1.25 untested.
