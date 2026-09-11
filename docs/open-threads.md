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
nothing.

Current state: 0 failures on all three majors (gosmo `c517d78`), with the
`sweepMustCall` coverage check passing on each. Refusals are gates working, not
defects: every on-premises major refuses the six Azure-only DMV reads, and 13
additionally refuses six 2017+ reads outright with `ErrUnsupportedVersion` —
the two Query Store wait reads, the three external-library reads and graph
tables. Call counts differ between instances for reasons other than version
(the logins and jobs swept are whatever each instance has), so compare
failures, not totals.

The gates, recorded because the next audit will otherwise re-derive them:

| Column or construct | Held by |
|---|---|
| `STRING_AGG` (2017) — partition functions and schemes, server and database triggers, foreign keys, audit specifications | `sql_agg.go` `commaList` renders the `FOR XML PATH`/`STUFF` form, valid from 2008. No raw `STRING_AGG` in non-test source. |
| Column Master Keys: `allow_enclave_computations`, `signature` (2019) | `security.go`, `colSince(major, SQLServer2019, …)` |
| Query Store options: the 2017 and 2019 columns | `query_store.go`, `colSince` per column |
| `Table.Detail`'s `ledger_type_desc` (2022) | `table.go`, `colSince(…, SQLServer2022, …)` |
| `Statistic.Header`: DBCC returns 10 columns before 2019, 11 after | `statistics.go` binds **by column name**, with the failure named in the comment |

A sweep of 0 failures is not proof on its own — it passes just as happily if a
read was never reached. When re-verifying, confirm the reads were actually
*called*: the sweep's `call` helper takes a label, and logging it lists every
method swept.

## Azure SQL Managed Instance

Supported. What is open is backup/restore `TO URL` and two Entra cases, below.

**MI reports `ProductVersion` `12.0.2000.8`** while running engine build 18.0,
so every `colSince` / `VersionMajor` gate in gosmo silently degrades or refuses
a feature the instance actually has. **Gate on `EngineEdition` first** —
`internal/tui/edition_gate.go` is the edition's counterpart to
`permission_gate.go`, and holds the UI gating of the operations MI rejects.

**Database Properties > Resource Governance is read-only and Azure-only, by
design.** Every value on it changes by resizing the instance or the database,
which is a control-plane operation no T-SQL statement from a dialog can
perform. It pairs `sys.dm_db_resource_stats` (the reading) with
`sys.dm_user_db_resource_governance` (the scale it is a percentage *of*),
because a bare "CPU 0.6%" against an unnamed limit says nothing. The two fail
*differently* for a login without the rights — Msg 262 from the first, zero
rows from the second — and the page turns both into a note rather than an
error; `pageDatabaseResourceGovernance`'s doc comment carries the reasoning.

Two things about the Activity Monitor's Instance tab that are easy to reopen:

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

**Open: backup and restore `TO URL` have never executed.** The statements are
built and validated, but executing one needs a shared access signature
credential on the container, and none exists on `t-qmi-01`. The first things to
check when one does:

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
  works is the worse error.

Settled: **Restore's Backup History source drops entries with no device.** MI's
automated backups are recorded with a NULL `physical_device_name`
(`device_type` 9) — on `t-qmi-01` they were every one of msdb's 102 rows — and
are restored by point-in-time restore through the control plane, never by a
`RESTORE` statement. `restorableHistory` (`restore_dialog_ops.go`) filters them
*before* the `maxHistorySets` cap, so they cannot crowd out a user's own URL
backup, and when nothing is left the status says so rather than "No backup
history". Filtered in the dialog, not gosmo: the Backup History viewer and
Database Properties show the same rows as true history.

Settled: MI answers `RESTORE VERIFYONLY FROM DISK = N'https://…'` with Msg
41902 ("Unsupported device type"), and the same statement spelled `FROM URL`
with Msg 3078 about the blob itself — so `URL` is the right device keyword.

**Every Entra method but Managed Identity is verified end to end** against
`t-qmi-01` with TenantID blank: one sign-in covers every later connection of
the session (Object Explorer, query windows, Activity Monitor), and each method
saves under its own name. Two facts worth keeping:

- **A failed Service Principal secret is not served from the `EntraCache`** —
  correcting it in the same dialog connects.
- **An Entra login whose account was deleted and recreated fails with 18456**
  "Could not find a user matching the name provided" *after* a successful
  token — the login's SID is the old object id; recreate the login.

Settled: **a token past its lifetime renews silently** (2026-09-11, built
binary, Azure CLI, against `t-qmi-01`, with an `az` wrapper logging every
call). Connected at 09:12 on a token expiring 10:26:55: one `az` call. A new
query window at 10:24:58, inside `entraTokenMargin` (5 min), made exactly one
more `az` call, got a token expiring 11:41:59, and connected as FEDERATED with
no prompt. After the first token's expiry, a third window and an Object Explorer
expand reused the renewed token with no further `az` call. The 09:12 window's
session (SPID 127) kept running on its original connection: a token is only
checked at login. gosmo's part is method-independent and is unit-tested by
`TestEntraCacheRenewsAnExpiringToken`. Only the credential's `GetToken`
differs by method — for Device Code/MFA it is azidentity's silent refresh,
which was not held open past an hour separately.

**Open:**

- **Managed Identity** — needs gossms running on an Azure-hosted machine. The
  dev box is not one (no IMDS at `169.254.169.254`, checked 2026-09-11). The
  mapping (`ManagedIdentityCredential`, resource ID over client ID) is
  unit-tested in gosmo's `TestEntraCredentialSpecPerMethod` only.

Settled: **with TenantID blank, MFA and Device Code sign in to the server's
tenant.** azidentity's default is `organizations`, where a personal Microsoft
account that is a member of the server's tenant is refused ("Selected user
account does not exist in tenant 'Microsoft Services'"; SSMS signs the same
account in). The tenant is only in the STS URL the server announces part-way
through a login, so gosmo's `Warm` opens a login, abandons it once the server
has named its SPN and STS URL, and signs in to that tenant; the answer is kept
per server in the `EntraCache`. On `t-qmi-01` the probe takes ~0.25 s and
announces SPN `https://database.windows.net/`. Each probe costs one
**Error 33155, severity 20** in MI's error log; on-prem without Entra it is
an immediate 18456. Do not "optimise" the probe back into a DNS-suffix guess:
the guess cannot know the tenant. `TestLiveEntraProbe` (gosmo, `-tags
livedb`) is the repeatable part.

## Release workflow: two jobs whose only failure mode is "did nothing"

**Open: neither release job has run in its current form** — the last tag,
v0.0.10, predates both the push fix and the Verify steps. The `homebrew` job
stages first and compares against the index (`git diff --quiet` reports no diff
for a path git has never tracked, which once let it go green having pushed
nothing), the shape the `apt` job uses. **Watch both jobs on the next tag and
confirm the tap gained a `gossms <tag>` commit.**

Each job ends in a **Verify** step that fetches the *remote* back and fails
unless it carries this tag: the tap's requires
`origin/<branch>:Formula/gossms.rb` to declare `version "<tag without v>"`, and
the apt job's requires both `.deb`s in `pool/`, a `Version:` line in each
architecture's `Packages`, and all three of `Release`, `InRelease` and
`Release.gpg`. The scripts have been driven by hand against the live repos in
both directions; the jobs themselves have not. Two things about those steps that
a plausible simplification undoes:

- **They read the remote with `git`, never over HTTP.** `raw.githubusercontent.com`
  and `https://radix29.github.io/apt` both serve a cached copy for minutes after
  a push, so an HTTP check would fail the step for a repository that is in fact
  correct. `git fetch` + `git show`/`git cat-file -e` sees the pushed commit.
- **They are separate steps, after the push, not extra lines inside it.** The
  push step's guard legitimately `exit 0`s when there is nothing to do; that
  ends the *step*, so a verify folded into it would be skipped by the very case
  it exists to catch.

**Open, never run:** `brew install` / `brew test` / `brew audit --strict` **on
an actual Mac** — nothing here has ever run macOS — and, on the Debian side,
`dpkg -i` on a clean container, arm64 execution and `lintian`.

**Open: `brew audit --strict --online` reports "`version …` is redundant with
version scanned from URL"** (on Linux Homebrew; `brew style` and the
`livecheck` block are clean). Not fixed — the verify step above greps for that
`version` line, so dropping it means changing the verify step too. Decide it
together with the Mac audit run.

The formula is deliberately **binary**, not build-from-source: `go.mod`'s active
`replace` makes any source build from a release tarball fail, and
`go install …@<tag>` fail with it.

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
  with the *same* Msg 37525, so the parser accepted both. On `t-qmi-01` the
  bare form, run by hand from `sqlcmd` as a SQL-auth sysadmin, created
  working logins for a user and a service principal; the New Login dialog's
  `WITH OBJECT_ID` form has still not been executed.
- **The Phase 3 tree families are read-only, and each for its own reason.**
  Read-only means *no create and no edit*: each family does have Script as,
  Delete, and — where SQL Server has the statement — Rename and Move to another
  schema. This is the standing answer to "why can't I create a rule?" and its
  four siblings:
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
- **Open: the CLR type, assembly and external-resource scripts are unit-tested
  only.** Every other Phase 3 script has been generated on one database and
  executed against another. These four cannot be here: no instance has a user
  assembly with CLR on, PolyBase, or Machine Learning Services, so there is no
  external data source, file format or library to script. Not a known defect —
  an untested path, and the one to exercise first if such an instance turns up.
- **Open: External Tables and FileTables are untested in the TUI against a real
  one.** System Tables and Graph Tables have been driven live on 13/14/17; the
  External Tables *folder* needs PolyBase, which no instance here has, so its
  presence gate has only ever been seen answering "absent". A FileTable exists
  only inside gosmo's `TestLiveTableKinds` (major 17, FILESTREAM), never under
  the tree. Same class as the CLR/assembly/external scripts above.
- **A database snapshot's subtree still offers writes that the server
  refuses.** A snapshot gets Tables, Views and Programmability (Query Store,
  Storage and Security are deliberately withheld — a snapshot has no Query
  Store of its own, cannot be backed up, and shares its source's principals),
  and Delete or Rename on a table inside one is offered and then refused with
  "the database is read-only". Deliberate: the permission gate answers what the
  *login* may do, and a read-only database is not a permission — a third gate
  for it would have to cover every READ_ONLY database, not just snapshots. SSMS
  behaves the same way.
- **System Data Types has no Properties dialog.** SSMS offers none either, and
  there is nothing to show about `int` that its name does not already say. The
  folder and its Detail Browser listing exist; the context menu deliberately
  omits the item.

## Permission gating: what is settled — do not re-raise

**Classes 0, 1, 3, 4, 5, 6, 10, 101, 105 and 108 are gated.** What is kept is
the live behaviour each gate rests on; every row is a *wrong* gate if assumed
the other way round.

**Open: Move to Schema on a class-1 object is offered to principals the server
refuses.** `ALTER SCHEMA ... TRANSFER` of a table needs CONTROL on the table
itself (plus ALTER on the target schema); probed on 17 on 2026-09-11, it was refused
(Msg 15151) under db_ddladmin, ALTER on the database, ALTER ANY SCHEMA, ALTER
on the source schema and ALTER on the table, and went through only under
CONTROL on the table. Move to Schema for every sys.objects family still asks
the Rename/Delete set (`objectTransferRights` falls back to
`objectDataRights`), so it is offered to all five. Closing it needs a CONTROL
answer at class 1 — gosmo's object block reads explicit rows and ownership,
not CONTROL on the schema or database — unlike classes 5, 6 and 10 below,
which already have one.

**Every schemaless database-level family has an explicit set** (`dbScopedOpRights`, probed live on 13/14/17 with a
`WITHOUT LOGIN` user per right — see its comment), and
`TestSchemalessDatabaseOpsAreGated` fails on a new one without. The OBJECT-scope
plan guide is answered by the routine (`planGuideRights`), for Delete,
Enable/Disable and Properties alike.

**A security policy's Delete and Disable/Enable ask both halves.** `DROP
SECURITY POLICY` and `ALTER SECURITY POLICY ... WITH (STATE = ...)` each need
ALTER ANY SECURITY POLICY **and** ALTER on the policy's schema (Msg 3701 /
Msg 33268 with either missing). Probed 2026-09-11 on 13 and 17, 17 cases,
identical: with the policy right held, both went through exactly when
`HAS_PERMS_BY_NAME(schema, 'SCHEMA', 'ALTER')` read 1 — which folds in CONTROL
on or ownership of the schema, ALTER ANY SCHEMA, db_ddladmin and ALTER/CONTROL
on the database — and a schema DENY beat a database-wide ALTER. CONTROL on the
policy itself permits neither. So the schema half is `rightAlterOnSchema`
alone (`conjoinedOpRights`), required beside the policy set through
`gateOnAll`/`allowsAllOn`, whose note names the first *failing* group.

**Open (V5-adjacent): the external library set was not run live.** No instance
has Machine Learning Services, so `CREATE EXTERNAL LIBRARY` fails (Msg 39020)
and the drop could not be executed; the set rests on `HAS_PERMS_BY_NAME` on 14
and 17 and the documented permission.

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
scope.** Adding a class-4-denied *user* to an undenied database role is
refused; adding a class-101-denied *login* to an undenied server role goes
through. So Database User Properties > Membership carries the arm and Login
Properties > Server Roles deliberately does not
(`TestLoginServerRolesDeclaresNoServerDenialArm`).

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

`ALTER AUTHORIZATION` is not evidence either way: it is refused on an
*undenied* role at both scopes, because changing an owner needs more than
`ALTER ANY` (CONTROL on the role, plus IMPERSONATE on the new owner).

**Classes 5, 6 and 10 are answered by effective CONTROL, and have no DENY
arm.** Probed live 2026-09-11 on 13, 14 and 17 with a `WITHOUT LOGIN` user per
case, identical on all three; "CONTROL" is gosmo's `SecurablePermissions`
(`HAS_PERMS_BY_NAME(..., 'CONTROL')`), and ALTER on the target schema was
held for every transfer:

| Held | CONTROL | DROP | `ALTER SCHEMA ... TRANSFER` |
|---|---|---|---|
| CONTROL on the securable, or its ownership | 1 | yes | yes |
| CONTROL on, or ownership of, the schema (types, collections) | 1 | yes | yes |
| CONTROL on the database | 1 | yes | yes |
| ALTER on the database, db_ddladmin | 0 | yes | **no** |
| ALTER ANY SCHEMA, ALTER on the schema (types, collections) | 0 | yes | **no** |
| ALTER ANY ASSEMBLY (assemblies) | 0 | yes | — |
| ALTER on the securable alone | 0 (ALTER reads 1) | no | no |
| db_ddladmin or ALTER ANY ASSEMBLY + DENY ALTER on the securable (assemblies, collections; a type has no ALTER) | 0 | yes | no |
| ALTER or CONTROL on the database, db_ddladmin, ALTER ANY ASSEMBLY or ALTER on the schema + DENY CONTROL on the securable (db_ddladmin also with the DENY to public) | 0 | no | no |

An alias type's rename (sp_rename `USERDATATYPE`) matched the DROP column in
every grant row, on 13 and 17 — which is why Rename shares Delete's set.

So Move to Schema asks CONTROL alone (`securableTransferRights`), and
Delete/Rename ask it beside the wider rights (`securableOpRights`,
`dbScopedOpRights`). The DENY CONTROL row needs no gate: it takes VIEW
DEFINITION with it, and the securable vanishes from `sys.assemblies`,
`sys.types` and `sys.xml_schema_collections` for that principal — there is no
node to withhold anything on. That is also why gosmo reads no DENY rows at
these classes. And DENY ALTER withholds neither the drop nor anything else
gossms offers, while reading ALTER 0 — ALTER is the wrong question here.

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

### The rest

- **No other class is reachable.** Beyond the gated classes above, gossms's
  `NodeType` list has no certificate,
  symmetric/asymmetric key, fulltext catalog or Service Broker node, and
  column master/encryption keys, partition functions and schemes have no
  securable class of their own.
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

- **No `Drop*` write method carries `IF EXISTS`,** so that "deleted" means
  deleted; a caller wanting idempotence ignores the error, which the library
  cannot decide for it. `TestDropStatementsAreNotIdempotent` pins it. Scripter's
  *scripts* keep `IF EXISTS` — DROP-and-CREATE output exists to be re-run.
- **`CertificateByName` answers `(nil, nil)` on absence**, unlike the
  `ErrNotFound` readers: making it error is a breaking change to a published
  contract, and its callers branch on absence as the ordinary case. The three
  conventions are documented on `ErrNotFound` itself;
  `TestLiveCertificateNotFoundIsNilNil` pins both directions.
- **A missing principal and an invisible one are the same thing to
  `ErrNotFound` — SQL Server's doing, do not fix it in gosmo.** Metadata
  visibility hides a principal the caller lacks `VIEW ANY DEFINITION` on by
  returning **zero rows, not an error**, so an existing login reads as absent;
  pinned by `TestLiveNotFoundCannotSeePastMetadataVisibility`. The answer is
  idempotence at the write, not a better sentinel. Note `isAlreadyExists`
  matches by substring; its `15023` arm is the *user* code, and logins raise
  15025.
- **`JobState` follows Agent's real encoding**, from `xp_sqlagent_enum_jobs`:
  1 Executing, 2 WaitingForWorker, 3 BetweenRetries, 4 Idle, 5 Suspended,
  6 WaitingForStepToFinish, 7 PerformingCompletionActions, 0 meaning a job
  Agent does not run itself. There is no Cancelling or Running state.

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
  against `win10cli.fritz.box:55253` is clean, and `sql2016`, whose Browser
  answers reliably, is clean by name. **Connect to 2017 by port for anything
  multi-connection**, and do not chase it as a query-compatibility finding — it
  names no column and no version.

## FILESTREAM: what the live run settled — do not re-raise

`internal/tui/live_filestream_test.go` (build tag `livedb`) runs Database
Properties > Files against a real FILESTREAM database on win10cli.

- **`FileGroupsContext` returns a FILESTREAM filegroup like any other** — name,
  files and all — so the Filegroup picker lists it, and `preservingItems`'
  widening is not needed for this case.
- **The filegroup is the whole of what makes a file FILESTREAM.** ALTER
  DATABASE ADD FILE has no file-type keyword: the same clause aimed at a ROWS
  filegroup produces an ordinary data file — measured, a file added to PRIMARY
  with an extensionless path came back as ROWS. So the Type picker has nothing
  to offer and `addableFileTypes` stays two items.
- **SIZE and FILEGROWTH are refused on a FILESTREAM file** with Msg 5509.
  **MAXSIZE is accepted**, which the message's shape does not suggest — both
  measured, not reasoned about. So the page omits both clauses for a file added
  into a FILESTREAM filegroup, greys the two spinners, and records the file's
  type as FILESTREAM so the grid does not claim ROWS.
- `gosmo.FileGroup.Type` (`sys.filegroups.type_desc`) and `IsFileStream()`
  carry the distinction. `is_default` is per filegroup *type*: a database with a
  FILESTREAM filegroup reports two defaults, both true. The scripted fakes
  report `max_size` -1 for a FILESTREAM file, not 0.

**Enabling FILESTREAM on win10cli is not something a SQL connection can do** —
the RsFx0800 filter driver and the share belong to SQL Server Configuration
Manager and need an OS administrator. Setting the registry `EnableLevel` and
`sp_configure` from `xp_cmdshell` succeeds and achieves nothing. The live test
skips with that explanation when the effective level is 0.

## Always On: what is deliberately out of scope

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
- **A merged selection can span both families; the selectors cannot.**
  **Select Files...** lists the SQL Server and Agent logs together, and a mixed
  set puts the family into every file label ("Agent Current"), since archive
  numbers are not comparable across families. The family selector, the plain
  file list and Recycle still address exactly one family — a mixed selection
  leaves them where they were, a single-family one moves them to its family. A
  cycle of *either* family re-anchors a mixed selection. Every read enumerates
  both families so the checklist has both lists without a round trip behind a
  keypress; a family that cannot be enumerated is left out.
- **A cycle re-anchors a merged selection to the current log, not a single-file
  one.** The cycle renumbers every archive and deletes the oldest, so a *set*
  chosen by number would silently come back as a different set. A single-file
  view keeps its number — the user asked for "Archive #1" and gets whatever is
  now Archive #1.
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

Two rules: the toolbar's busy latch is taken **before** the confirmation is
shown, not in the answer (the confirm dialog takes input but does not stop F5
reaching the panel, so a read begun while the question was up would clear
`busy` from under the cycle); and an open viewer is put through `Refresh`,
never `Load`, after a cycle started from the tree — `Load` re-enumerates only
the family on screen, so cycling the Agent log while the viewer sits on the SQL
Server family would hand the user the Agent's pre-cycle numbering the moment
they flipped the selector.

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
  Job...`** though Rename is off every system object: SSMS permits it and msdb
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
  `core.ClipboardHost`, so Ctrl+C there does nothing rather than copying
  whatever the panel behind the dialog has selected, which is never the thing
  the user is looking at.
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
  not beside it.** Probed live with a `WITHOUT LOGIN` user: CREATE/ALTER/DROP
  DATABASE SCOPED CREDENTIAL all go through under `GRANT CONTROL ON DATABASE`
  and all three are refused under `GRANT ALTER ON DATABASE`. Every other
  database-scoped set in `permission_gate.go` pairs its narrow right with
  `rightAlterDatabase`; adding it here "for symmetry" would offer
  New/Delete/Properties to a principal the server then refuses. `ALTER ANY
  CREDENTIAL` is not the narrower twin either — it is server-scope, and
  `HAS_PERMS_BY_NAME` asked of a *database* returns NULL rather than 0, which a
  gate built on it would read as "unknown" forever. See
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
  credential is used by an active database file". The page surfaces the
  server's message; there is nothing to gate on beforehand, since the binding
  is not visible from `sys.database_scoped_credentials`.

## Database-scope DDL triggers: what the design settled

- **A database has no flat Triggers folder, and that is the point.** A DML
  trigger belongs to one table or one view and is listed under that object's
  own Triggers folder, which is where SSMS puts it; a database-wide roll-up
  would list every one of them a second time, beside a "Database Triggers"
  folder (DDL, `parent_class = 0`) — a distinction without a difference.
  `Database.TriggersContext` is gosmo's database-wide read; gossms simply has
  no folder for it.
- **A view is not a leaf.** It carries INSTEAD OF triggers, so `NodeView` has
  the same Triggers folder a table has. `Database.ObjectTriggers` is the by-name
  read behind both, because gosmo's `View` is a plain row struct with no
  back-pointer to its database and so can carry no method of its own.
- **The DDL family is read/enable/disable/script/drop — there is no New
  dialog**, the same shape as Server Triggers one scope up. A CREATE TRIGGER
  body is T-SQL a form cannot usefully build, and SSMS offers no such dialog
  either. Editing one is Script Database Trigger as > ALTER To.
- **The Database Triggers folder's filter is client-side and offers Name and
  Creation Date**, matching the Server DDL Triggers folder for the same reason
  the six server-level folders give.

## Connection settings: what the live run settled

Encrypt modes, Extra Properties, the Entra field mapping and IPv6 addresses.
`internal/db/live_connect_test.go` is the repeatable part; it passes on 13,
17.0 and MI.

- **Strict (TDS 8.0) was verified on Azure SQL MI only**, with the certificate
  validated and with Trust Server Certificate on. On win10cli (17.0) every
  Strict attempt fails `TLS Handshake failed: EOF`: the instance has only SQL
  Server's self-signed fallback certificate, which TDS 8.0 will not use. Server
  configuration, not a gossms defect — do not chase it without a provisioned
  certificate.
- **Mandatory with Trust Server Certificate off fails on every on-prem
  instance here** (`x509: certificate is not valid for any names`), for the
  same reason. That is why Trust stays ticked by default beside Mandatory.
- **CertHost was verified on MI by IP address**: the TLS validation that fails
  without it (`doesn't contain any IP SANs`) passes with it. The login that
  follows is then refused by Azure's gateway (40532: it routes on the server
  name), which is Azure's, not TLS's.
- **`net_packet_size` reads 58 bytes high on an encrypted session** (4154 for
  the driver's default 4096, on 17.0 and MI alike), and the server caps 8192 to
  8000. The live Extra Properties test asks for 16000 and allows for the 58.
- **Azure encrypts every session**: Optional still reads `encrypt_option` TRUE
  on MI. The live test checks Optional only off Azure.
- **On MI, a `packet size` below 4096 cannot connect at all** — `TLS Handshake
  failed: cannot read handshake packet: invalid packet size, it is longer than
  buffer size`, for both Optional and Mandatory. go-mssqldb sizes its handshake
  buffer from the requested packet size and MI's handshake does not fit. A
  driver limitation reached only through Extra Properties; 17.0 takes 2048
  fine.
- **`ApplicationIntent=ReadOnly` was verified on AAG1** with read-only routing
  set up for the run and reverted after: straight to the secondary, no intent is
  refused (Msg 978) and ReadOnly connects; through the listener `ubuaag`,
  ReadOnly is routed to ubusql2 (`Updateability` READ_ONLY) and the default
  stays on ubusql1 — from the Connect dialog too. AAG1 normally has no routing
  list and `ALLOW_CONNECTIONS = ALL`, so intent is unobservable there without
  that setup.
- **Entra's per-method field mapping is verified up to the driver for every
  method, and end to end for every method but Managed Identity** (V6):
  `TestEveryEntraMethodBuildsADriverConnector` runs each method's real DSN
  through `azuread.NewConnector` — § Azure SQL Managed Instance lists what
  that leaves.
- **An IPv6 literal with a named instance needs an explicit port**
  (`fe80::1\INST,1500`). gosmo refuses it without one: the driver keeps the
  brackets of a port-less literal in the Browser probe's address, so there is
  no URL form that works.
- **Extra Properties separators are `;`, `&` and line breaks, not spaces** —
  `packet size=8192 database=foo` is one entry whose value is
  `8192 database=foo`, which the driver rejects. The dialog's Enter is its
  Connect key, so in practice the separator is `;` or `&`.
- **A downgrade loses saved passwords until the next upgrade.** A release
  before v3 sealing reads a `v3:` value as unprefixed, fails to open it and
  keeps it sealed; it also drops `encrypt_mode` on its next save, so a Strict
  entry comes back Mandatory — a different AAD, so that password stays
  unreadable and is re-entered. The `encrypt` boolean is still written so the
  older release can read the file at all.

## By design — not issues, do not re-raise

- **A query window's session behaves like SSMS's, with
  three deliberate differences.** A panel closed *while a query is running* is
  not asked about its transaction: the run is cancelled and the session ended,
  which rolls back — its `@@TRANCOUNT` is from before the run, so a prompt
  would guess. A lost session (killed SPID, network drop, failover, or a plan
  capture whose `SET … OFF` failed) disconnects the panel instead of silently
  reconnecting on the next F5, as SSMS does, because the new session would not
  have the temp tables or the transaction the script assumes. And the
  commit-on-close loop is bounded by the starting `@@TRANCOUNT`, so a doomed
  transaction reports its error and leaves the window open rather than spinning.

- **Comment prose is not a cleanup target.** Two classes are protected
  outright: the long comment blocks `CLAUDE.md` § Coding conventions names
  (`app.go`, `datagrid.go`, `secret.go`, `propsheet/common.go`), and the
  failure-naming comments in `query_store_panel.go`, `prop_grid_helpers.go` and
  similar — each names a shipped bug a plausible simplification would bring
  back. What drift does recur, worth knowing when a comment is *edited*: a doc
  naming the wrong caller after a helper moved, a count of anything, a claim
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
  a bug lived; merging the two page *builders* costs six injection points, two
  different row structs and two unrelated applies. Do not re-propose without new
  evidence.

- **`Form.Revert()` is exposed, not retired.** `Ctrl+Z` on a `PropertySheet`
  calls `RevertPage`, which reaches `Form.Revert`, every row's `Revert` and all
  21 `RevertFn` closures. Two rules come with it, both easy to undo by accident:
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
  `require`, and commenting out the `replace` are steps of the
  release process itself (ARCHITECTURE.md § Developing against a local gosmo
  checkout). A CI release build not resolving gosmo mid-development is expected.

- **A Grid/Text query result can exhaust memory.** There is no Max Result Rows
  option and no `maxRows` cap: a result set is retained in full, so `SELECT *
  FROM` a billion-row table will OOM the process. SSMS parity of "you get what
  you asked for" is preferred to a silent cap. The retained form is already as
  small as it reasonably goes (`internal/query/arena.go`); the floor is the
  16-byte string header per cell that `ResultSet.Rows [][]string` implies.
  Results To File never retains rows and is unaffected. Do not add a cap back.

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
  candidates are outstanding.** Measured against a type-checked cross-file
  reference graph and rejected on the numbers; `internal/tui/sqlparse`, the
  only part of the package with *zero* outbound references, is the one split
  that pays. File length alone (31 non-test files exceed 400 lines) is not a
  reason to re-open this. The negative results, each a proposal a future review
  will otherwise reinvent:
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
  server shows exactly one row: its own. `and spr.spid <> @@spid` is
  **rejected — author's call**. Do not "fix" it.

- **`sp_block`'s `cross apply sys.dm_exec_sql_text` and its lack of an
  `ecid = 0` filter are intended.** `outer apply` plus `spid > 50 and ecid = 0`
  was proposed on the grounds that a blocker with no cached text takes its whole
  blocked subtree out of the tree and that a parallel plan is several
  `sys.sysprocesses` rows. Neither is reproducible — a sleeping blocker keeps a
  resolvable handle, and `DBCC FREEPROCCACHE` does not evict a live
  transaction's text — and the `cross apply` is what keeps system sessions out.
  **Author's call: as is.**

- **The Activity Monitor probes `VIEW SERVER STATE` once per collector, so twice
  per panel open.** Not hoisted to a single shared check: the Retry control
  starts a *new* collector after a transient failure, and a cached permission
  answer would make that retry fail without asking the server. One extra round
  trip on open is the cheaper mistake.

- **`counterQueryFor`'s `RTRIM(instance_name) IN ('', '_Total')` filter drops no
  counter the panels read**, though `RTRIM(NULL)` is `NULL` and `NULL IN (...)`
  is false. Windows has no NULL `instance_name` rows at all; Linux has exactly
  five, all `SQLPAL:Host Memory` / `SQLPAL:Guest Memory` rows that are not in
  `counterNames` and have no gossms consumer. All 33 names in `counterNames`
  resolve through the filter on both. Do not add an `OR instance_name IS NULL`
  arm.

- **`formatValue`'s `case float32` is unreachable but kept.** go-mssqldb returns
  `float64` for both `REAL` and `FLOAT`. It is correct if the driver ever
  narrows, and `formatFloat` already takes the bit size.

- **Server-scope GRANT/DENY/REVOKE's `USE master;` prefix does not strand the
  pooled connection in master.** gosmo's `"USE master; " + stmt`
  (`permission_options.go`, which `server_security.go`'s grant/deny/revoke
  methods route through) looks like pool contamination and is not — a live A/B
  showed eight pooled connections all still reporting the right database after
  a GRANT. `database/sql` calls `driver.SessionResetter.ResetSession` before
  handing a pooled connection to its next user, and go-mssqldb implements it by
  flagging the next TDS batch as a connection reset, restoring the session's
  database to the connection string's. A pinned connection that reads
  `DB_NAME()`, switches and switches back costs three extra round trips per
  grant for nothing.

- **`mssql.ServerError` is fatal-only, so gosmo's `IsRetryable` treating it as
  retryable is correct** (`go-mssqldb@v1.9.4/error.go:79`, and `mssql.go:1352`
  "Ignore non-fatal server errors").

- **One worker pool is spawned outside `safego`/`safegoRepair`** —
  `App.fanOut` (`safego.go`), which the Detail Browser backfill and the Log File
  Viewer's `readLogFiles` share — deliberately taking both the label and the
  recover by hand.

- **`charts.StackedHistoryChart.Draw` stays, though nothing in the binary
  reaches it.** `doc.go` advertises `Draw` as the entry point and all six chart
  types implement it. The dashboard uses `DrawFrame` only because it also wants
  the time row. Removing one of six would break the package's one uniform method
  for four lines.

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

- **Per-file destinations in Restore (SSMS's editable "Restore As" column) are
  deliberately not built**; the folder-level choice covers what the dialog's
  width allows. Two rules come with it. **The backup set number must not get
  re-scattered**: the restore itself, the MOVE clauses and the Files Included
  panel all take it from `backupSetNumber` in `restore_dialog_ops.go`, and two of
  the three deriving it separately produces "Logical file 'x' is not part of
  database 'y'" on every rename-restore from an appended `.bak`. And **the
  relocation preview and the MOVE clauses must keep sharing `relocateFiles`**,
  or the paths the Files view lists stop describing what the restore does.

- **Distribution credentials.** Homebrew: `HOMEBREW_TAP_DEPLOY_KEY` on
  `radix29/gossms`, a write-enabled deploy key on `radix29/homebrew-tap`, not a
  PAT. APT: `APT_REPO_DEPLOY_KEY` (write deploy key on `radix29/apt`, published
  through GitHub Pages at https://radix29.github.io/apt) and `APT_REPO_GPG_KEY`
  (dedicated RSA-4096 signing key, fingerprint
  `468B0CE5FFDEE82439741EC393F25CAB61497D93`; the public half is `gossms.asc` in
  the repo root). The jobs in `.github/workflows/release.yml` are the reference.

- **A Launchpad PPA is rejected, not forgotten:** builders have no network and
  Ubuntu's packaged Go is 1.22 on 24.04 LTS, 1.26 on 26.04 LTS, against
  `go.mod`'s 1.27. Two measurements worth not repeating — `go mod vendor` fully
  resolves the `replace ../gosmo` (builds with the sibling deleted and
  `GOPROXY=off`, +23 MB), and **neither gossms nor gosmo actually needs Go
  1.27**: a real offline `go1.26.0` builds both once the `go` directive is
  lowered in *both* `go.mod` files. Go 1.25 untested.

## Progress dialog: what is deliberately out of it

Every confirmed write from Object Explorer, the Details pane, Always On, Agent,
the Log File Viewer, Query Store and Activity Monitor runs behind
`dialogs.ProgressDialog` (`App.runWithProgress`, 2026-09-11). Not the rest:

- **Properties and New … keep their own spinner and live Cancel** on the sheet's
  button row: closing the sheet for a separate dialog would take the pages and
  the message line — the only account of a partial apply — off screen.
- **Back Up and Restore keep their progress view**, a Task with a percentage;
  the Restore dialog's overwrite confirmation hands off to it.
- **Script runs nothing**, so it never shows the dialog.
- **The unconfirmed half of a toggle runs behind it too** — Enable, Bring
  Online, Start Endpoint, Join, Resume — because it shares the confirmed half's
  run function. The 250 ms reveal delay keeps a fast one invisible.
- **Driven live on win10cli:** a single DROP blocked on a lock and cancelled
  (the trace shows it never committed, and the session held no locks or
  transaction afterwards), a three-table batch cancelled at the blocked third
  ("2 of 3 deleted"), a fast DROP with no dialog drawn, Restore from Snapshot's
  uninterruptible dialog, and snapshot / database deletes. **Not driven live:**
  the failover dialogs (need the AG cluster), and the "Cancelling …" state,
  which was never on screen long enough to capture: a cancelled DROP returned
  within one frame. `TestProgressDialogCancelAsksOnceAndStaysOpen` covers its
  drawing.

## Fix order

The work still outstanding, ordered by priority within each subsection: bugs
and suspected defects first, then verification gaps, then nice-to-have. Each
item is a pointer — the reasoning lives in the section it names.

**Maintained on request only.** Do not regenerate this list as part of ordinary
work; the author asks for a refresh. An item is closed by *deleting* it here
when the underlying issue is fixed.

### Bugs and suspected defects

- **B1** — Neither release job has run with the push fix or the Verify steps.
  The next tag closes this: a green `homebrew`/`apt` job is now proof.
  § Release workflow
- **B2** — `WITH INIT` is hardcoded on a URL backup device; block-blob backup
  overwrites through `WITH FORMAT`, so MI may refuse the statement the Back Up
  dialog builds. § Azure SQL Managed Instance
- **B3** — `RESTORE ... WITH MOVE` on MI: relocation options are deliberately
  ungated and were never driven; MI places its own files. § Azure SQL Managed
  Instance
- **B5** — Move to Schema on a class-1 object (table, view, procedure, …) is
  offered on the Rename/Delete set; the server wants CONTROL on the object.
  Needs a class-1 CONTROL answer from gosmo. § Permission gating

### Verification gaps

- **V2** — `brew install` / `brew test` / `brew audit --strict` on a real Mac;
  the audit's redundant-`version` note. § Release workflow
- **V3** — Debian side: `dpkg -i` on a clean container, arm64 execution,
  `lintian`. § Release workflow
- **V4** — External Tables folder and FileTables under the tree, against a real
  PolyBase / FILESTREAM instance. § Deferred scope
- **V5** — CLR type, assembly and external-resource scripts have never been
  executed against a server, and the external library right set was never run
  live (no Machine Learning Services). § Deferred scope, § Permission gating
- **V6** — Entra: Managed Identity (needs an Azure-hosted machine). Every
  other method, `CREATE LOGIN ... FROM EXTERNAL PROVIDER`, and token renewal
  past its lifetime (2026-09-11) are driven on MI. § Azure SQL Managed
  Instance
