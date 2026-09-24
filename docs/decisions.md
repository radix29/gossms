# Decisions

Settled decisions and deliberate exclusions — nothing else. These are not
history: they are what stops a settled question being reopened, so an entry
earns its place by naming what was decided and why re-raising it would cost.
Work knowingly left undone lives in `docs/open-threads.md`.

## Azure SQL Managed Instance

Supported; nothing is open against it.

**MI reports `ProductVersion` `12.0.2000.8`** while running engine build 18.0,
so every `colSince` / `VersionMajor` gate in gosmo silently degrades or refuses
a feature the instance actually has. **Gate on `EngineEdition` first** —
`internal/tui/edition_gate.go` is the edition's counterpart to
`internal/tui/gate/gate.go`, and holds the UI gating of the operations MI
rejects.

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

Two things about backup/restore `TO URL` that are settled and easy to undo:

- **`WITH INIT` is hardcoded on a URL device.** The Back Up dialog has no
  "append to media set" option — `TestBackupOptionsBuildTheExpectedStatement`'s
  "init is always set" subtest pins it. Do not change it for URL alone: it
  would change every on-premises backup too.
- **`RESTORE ... WITH MOVE`'s relocation options are deliberately not gated on
  MI**, though MI places database files itself — the same fact that makes
  `CREATE DATABASE`'s file clauses fail with Msg 41918. Withholding an option
  that works is the worse error.

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

Two Entra facts worth keeping:

- **A failed Service Principal secret is not served from the `EntraCache`** —
  correcting it in the same dialog connects.
- **An Entra login whose account was deleted and recreated fails with 18456**
  "Could not find a user matching the name provided" *after* a successful
  token — the login's SID is the old object id; recreate the login.

Settled: **a token past its lifetime renews silently.** A window opened inside
`entraTokenMargin` (5 min) of expiry fetches exactly one new token and connects
as FEDERATED with no prompt; later windows and Object Explorer expands reuse it.
An already-connected session is unaffected — a token is only checked at login.
gosmo's part is method-independent (`TestEntraCacheRenewsAnExpiringToken`); only
the credential's `GetToken` differs by method.

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

## Deferred scope (repeatedly, deliberately)

- **There is no CI, and that is the decision, not an omission.** No `ci.yml`
  (build/vet/`gofmt -l`/test/`-race`/`staticcheck` on push) and no tag-only
  `release-guard` against an uncommented `replace github.com/radix29/gosmo`:
  this is a spare-time single-author project with no other automation, and the
  mechanical checks stay local discipline — `CLAUDE.md`
  § Build & verify lists them, and `staticcheck` is now zero-output on both
  repos, so running it is one command with a yes/no answer. The one thing CI
  would have caught and nothing else does — gosmo needing a tag before gossms
  can have one — is a release step instead (`CLAUDE.md` § What this is, and
  the `replace` entry under § By design below). Re-raising CI needs a new reason, not the same one.
- **The `DatabaseByName` calls in `internal/tui` stay as they are.**
  Most loaders resolve the database with a real `sys.databases` read where
  `Server.DatabaseRef(name)`'s handle would do, costing a round trip per folder
  expansion. Unmeasured: the saving is one round trip against reads that are themselves round trips, and
  the swap is not mechanical — `DatabaseRef(name)` leaves `id` at 0, so
  `IsSystem()` and `IsSnapshot()` answer `false`, which is the quiet failure
  `CLAUDE.md` names. Reopen it only with a measurement from a real expand
  (the Managed Instance's ~41 ms round trip is the figure that would decide
  it), not from the call count.

- **Five of the seven Service Broker families ship read-only, and it is a
  deferral, not the Rules/Defaults refusal.** Six of the seven *do* have an
  ALTER, so the argument that withheld editing from rules, defaults, types and
  assemblies — no ALTER means an editor is a drop-and-recreate — does not apply
  here. Each is withheld for its own reason, argued in its props file: a
  message type's and a service's ALTER changes a running application's wire
  contract (the validation a conversation is measured against, or the queue its
  messages arrive on) rather than a setting; a contract has no ALTER at all; a
  binding's ALTER names a user that must already own the remote service's
  certificate, an import this build leaves to a query window (§ Keys and
  certificates); and a broker priority's write cannot be gated on any right
  the server publishes — only ALTER on the database, which would show a
  read-only banner to every
  principal who can in fact perform it. Queue and Route are editable because
  their settings change in operation: a queue taken out of service, a stuck
  activation procedure, a route repointed at a new address.
  There are also no New-X dialogs for any of the seven: each object needs the
  others to exist first, and a service or a contract is authored with its
  application rather than clicked together. This is the standing answer to "why
  can't I create a contract?".

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
- **A database snapshot's subtree still offers writes that the server
  refuses.** A snapshot gets Tables, Views and Programmability (Query Store,
  Storage and Security are deliberately withheld — a snapshot has no Query
  Store of its own, cannot be backed up, and shares its source's principals),
  and Delete or Rename on a table inside one is offered and then refused with
  "the database is read-only". Deliberate: the permission gate answers what the
  *login* may do, and a read-only database is not a permission — a third gate
  for it would have to cover every READ_ONLY database, not just snapshots. SSMS
  behaves the same way.
- **Two details of the Service Broker shape are easy to undo.** **Nothing in
  the family has a rename** — no `sp_rename` class exists for
  any of the seven — and `azureRefusedScriptVerbs` withholds the remote service
  binding's CREATE and DROP And CREATE on an Azure edition, which MI refuses at
  compile time with Msg 41906, while leaving its DROP alone.

  The withheld verbs draw greyed out with the `N/A` note and no submenu arrow,
  and can be reached by neither keyboard nor mouse — verified on MI, where the
  node had to be fabricated because `CREATE REMOTE SERVICE BINDING` aborts the
  batch with Msg 41906 and no binding can exist to right-click.
  `TestTheBindingsCreateIsWithheldOnAzure` (`service_broker_ops_test.go`)
  covers the mapping.

  **A second MI refusal is not gated**: `CREATE ROUTE` and `ALTER ROUTE` with
  `ADDRESS = 'TRANSPORT'` or with any `MIRROR_ADDRESS` come back **Msg 41943**,
  "does not support creating route with TRANSPORT or MIRROR address" (probed
  on `t-qmi-01`, 2026-09-17). An ordinary TCP address is accepted, and unlike
  the binding's 41906 this one is a *runtime* refusal — a statement before it
  in the batch runs, the ones after it do not. Nothing gossms emits hits it
  today: Script as ▸ CREATE can only reproduce a route that already exists,
  and on MI no route can carry either form. The Route Properties page *can*:
  a user who types `TRANSPORT` or a mirror address into it on MI gets the
  server's 41943 rather than a disabled field. Left ungated deliberately — the
  fields are free text, the message is clear, and gating one value of one field
  per edition is not a shape `edition_gate.go` has.

  Three things about the two writes that both Properties pages respect, and
  that any later New-X dialog must: a nil field means "leave this setting
  alone", so the page sends only what changed; `QueueSettings.Activation` is
  restated in full, because the server refuses a partial ACTIVATION block on a
  queue that has none — so emptying the procedure name is mapped to
  `ACTIVATION (DROP)`, and on a queue that has no activation it is an error
  rather than a statement; and **`RouteSettings` can change a setting but never
  clear one** — `= NULL` does not parse, an empty value is refused, and a route
  that must lose its broker instance, mirror address or lifetime is dropped and
  created again. Route Properties therefore refuses an emptied row with a
  message rather than treating it as "no change".

- **System Data Types has no Properties dialog.** SSMS offers none either, and
  there is nothing to show about `int` that its name does not already say. The
  folder and its Detail Browser listing exist; the context menu deliberately
  omits the item.

## Permission gating: what is settled — do not re-raise

- **A queue's Properties and its Delete are gated on different rights, and the
  queue's owner is knowingly withheld the Delete.** Probed live 2026-09-16 on
  majors 13, 14 and 17, identically: `ALTER ON OBJECT::<queue>` alters the
  queue and is *refused* the drop (Msg 15151), which needs CONTROL on the queue
  or ALTER on its schema. So `gate.QueueAlterRights` is `gate.ObjectWriteRights()` and
  `queueDropRights` is that set minus the object-scoped ALTER — never one entry
  for both.
  The two are told apart by `rightControlOnObject`, which reads the `O:CONTROL`
  answer gosmo's `ProbedObjectPermissions` carries beside `O:ALTER`: the ALTER
  map reads 1 for either grant, while the CONTROL map answers only for a CONTROL
  grant and for the object's owner — exactly the set the server allows the drop
  to. `classOneTransferRights` rests on the same answer: Move to Schema on a
  class-1 object needs CONTROL on it and is refused to ALTER.

- **Move to Schema on every class-1 family is gated on CONTROL on the object,
  and one set serves all nine.** `ALTER SCHEMA ... TRANSFER` of a class-1 object
  needs CONTROL on it plus ALTER on the target schema. Probed live twice on
  major 17, one `WITHOUT LOGIN` user per right and the two runs agreeing
  exactly — a queue on 2026-09-16, a table on 2026-09-17:

  | right held | `O:ALTER` | `O:CONTROL` | transfer |
  |---|---|---|---|
  | ALTER on the object | 1 | 0 | refused, Msg 15151 |
  | CONTROL on the object, or its ownership | 1 | 1 | went through |
  | CONTROL on the object, DENY ALTER on it | 0 | 1 | **went through** |
  | CONTROL on the database | 1 | 1 | went through |
  | db_ddladmin | 1 | 0 | refused, Msg 15151 |
  | CONTROL on the database, DENY CONTROL on the object | 0 | 0 | refused, Msg 15151 |

  So `objectTransferRights` falls back to `classOneTransferRights` — CONTROL on
  the object, or CONTROL on the database — for the table, view, procedure,
  function, sequence, synonym, rule, default and queue alike, and never to
  `objectDataRights`, which offered the move to rows one and five.
  The third row is the one worth keeping: a DENY of ALTER on the object does
  *not* reach a transfer, because the transfer asks CONTROL and CONTROL is what
  the principal still holds. Three ALTER-denial menu tests exempt Move to Schema
  for that reason rather than by oversight.
  Two permitting rights stay out of the set, knowingly: CONTROL on the source
  schema reads through gosmo's schema probe as ALTER, which ALTER alone also
  reads, so asking for it would offer the move to the principals the server
  refuses. A principal holding only CONTROL on the source schema is therefore
  not offered a move the server would allow — the same trade `queueDropRights`
  accepts.

**Classes 0, 1, 3, 4, 5, 6, 10, 101, 105 and 108 are gated.** What is kept is
the live behaviour each gate rests on; every row is a *wrong* gate if assumed
the other way round.

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
`gate.ItemOnAll`/`gate.AllowsAllOn`, whose note names the first *failing* group.

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
  `NodeType` list has no fulltext catalog node.
  Certificates (a database's Security > Certificates) are class 25: Delete
  is gated on `ALTER ANY CERTIFICATE`, `ALTER`/`CONTROL` on the database, or
  `CONTROL` on the certificate itself from gosmo's per-securable probe — which
  is what offers it to the owner, a `CREATE CERTIFICATE`-only principal
  included. Properties' Owner row is gated on `CONTROL` on the object, and
  Script as is ungated. Asymmetric keys
  (class 26) are gated the same way on `ALTER ANY ASYMMETRIC KEY` / `CREATE
  ASYMMETRIC KEY` and `CONTROL` on the key, and symmetric keys (class 24)
  the same way again, their Encryption page on the Delete set — every holder
  and verb probed identically for the three (§ Keys and certificates). The
  Service Broker families are gated by their database-wide
  `ALTER ANY …` rights (`internal/tui/gate/gate.go`), not a class probe, and
  column master/encryption keys, partition functions and schemes have no
  securable class of their own.
- **The column path is dormant in production — do not re-raise.**
  `ProbedObjectPermissions` is `ALTER`, which is not column-grantable, so
  `ColumnPermissions` is empty on every real connection and `DeniedOnAnyColumn`
  never withholds anything. Adding `SELECT` would not change that:
  `gate.ObjectDenial` asks the column question only for the rights an action lists,
  and the only object-scoped right gossms declares is `rightAlterOnObject`.
  Firing the path needs a gossms *consumer* — an action gated on `SELECT` at
  object scope — and the only candidate, Select Top 1000 Rows, hands the user
  generated text rather than performing the read. The machinery stays, live
  T-SQL-verified. Two facts for whoever picks it up: `SELECT` in the probe list
  brings public's catalog-view grants back into `ObjectPermissions` (232 rows
  on HealthClinic) and they must **not** be filtered by `is_ms_shipped`, or a
  system view is withheld from a login that can read it; and
  `internal/tui/gate/names_test.go` checks an object-scoped right against
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
- **`CertificateByName` and `AsymmetricKeyByName` answer absence with
  `ErrNotFound`, like every other `*ByName` reader** (2026-09-22). They
  answered `(nil, nil)` until then, kept only because changing a published
  contract was breaking; the 2026-09-22 review's compatibility waiver removed
  that reason, and the change shipped in the same breaking release as the
  `…Context` rename. A caller that branches on absence as the ordinary case
  tests `errors.Is(err, gosmo.ErrNotFound)` — the endpoint pipeline's
  `findCertificateIfAny` wraps exactly that. The two remaining conventions are
  documented on `ErrNotFound` itself; `TestLiveCertificateNotFoundIsErrNotFound`
  pins both directions.
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
- **The lookup-free handles carry a `Ref` suffix; the `*ByName` lookups keep
  their names.** Twenty-six families pair this way
  (`Server.Database` → `Server.DatabaseRef`, `Login`, `Table`, the four Agent
  ones, the audit/credential/trigger/snapshot/plan-guide/backup-device/AG
  ones, and `Database.CertificateRef`, `Database.AsymmetricKeyRef` and
  `Database.SymmetricKeyRef`, and `Table.IndexRef` since 2026-09-23). An un-suffixed handle compiles, issues no
  query and answers the zero value, which is the trap the suffix makes visible.
  **Two alternatives are rejected.** Giving the *lookup* the plain name
  (`DatabaseByName` → `Database`) would split the convention with the
  library's other `*ByName` methods. Giving the handle its own distinct
  type, with `Load(ctx)` to populate it, is the only form that makes the
  zero-valued accessors impossible rather than merely visible, and is not worth
  the change to every call site; reopen it only with a bug the suffix failed
  to prevent. The lightweight form itself is not removable — it is
  the only one that works when there is nothing to read yet, per
  `~/go/gosmo/CLAUDE.md` § Conventions, the `Ref` bullet, which owns that
  rule — and *not* "under a `WithScript`-derived context", which is
  over-broad: `WithScript` intercepts writes only.
- **`Database`'s catalog state is exported fields, not accessors.** `Name`,
  `ID`, `State`, `RecoveryModel`, `CompatibilityLevel`, `Collation`,
  `IsReadOnly`, `CreateDate` and `SourceDatabaseID` are exported fields,
  matching every other gosmo type. `IsSystem()`
  and `IsSnapshot()` stay methods because they are derivations over
  `ID`/`SourceDatabaseID`, and `Server()` stays one because it is a
  back-pointer, as `Table.Database()` is. `Server.Name()` is left alone: it reads `s.info.Name`, not its own storage.
  One shape across the library is the point: a caller must not have to guess
  which form a type uses. The `Ref` trap (`DatabaseRef("master").IsSystem()`
  answers `false`) is stated on `Server.DatabaseRef` and on the `Database` type
  itself.
- **azidentity has deprecated `UsernamePasswordCredential`** (no MFA), which
  gosmo's `AuthEntraPassword` / ROPC mode uses at `entra.go` — both the options
  literal and `NewUsernamePasswordCredential`. The method is **kept**: it is a
  supported gosmo auth mode, verified live on Managed Instance. Both sites carry
  `//lint:ignore SA1019` with that reason. The thread to watch is azidentity
  *removing* the type, not deprecating it — at which point ROPC needs a
  replacement or the mode goes. Tracked in gosmo, which now has somewhere to
  track it: `~/go/gosmo/OPEN-THREADS.md` § azidentity.

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

## FILESTREAM: what the live run settled — do not re-raise

`internal/tui/live_filestream_test.go` (build tag `livedb`) runs Database
Properties > Files against a real FILESTREAM database on win10cli.

- **`FileGroups` returns a FILESTREAM filegroup like any other** — name,
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
- **The peer failure cache is short on purpose.** `recordPeerFailureLocked`
  holds the last connect failure per `InstanceKey` for `peerFailureTTL` (30s) so
  a blackholed primary does not charge the driver's 15s connect timeout to every
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
  `QueryStoreTrackedQuery` ignores `Top`, `MinExecCount`,
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
  database-scoped set in `internal/tui/gate/gate.go` pairs its narrow right with
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

## Keys and certificates: what the design settled — do not re-raise

A database's Security folder holds Asymmetric Keys, Certificates and Symmetric
Keys (in that order, after Schemas), on every database, `master` included.
Every rights and behaviour result below was probed live on 2026-09-22 with
`WITHOUT LOGIN` users on majors 13, 14 and 17 and on Managed Instance, and was
identical on all four; the finished UI was then driven on 17 and on MI.

- **Keys are generated, never imported from a file.** Every file import path —
  `FROM FILE`, `EXECUTABLE FILE`, `ASSEMBLY`, a certificate's `WITH PRIVATE KEY
  (FILE = …)` — reads the *server's* filesystem, which a client dialog cannot
  browse or check. Import is a query window's job. This is the standing answer
  to "why can't I import a key?".
- **EKM (`FROM PROVIDER`) is offered only where a provider exists.** New
  Asymmetric Key and New Symmetric Key show an Extensible Key Management
  section only when the instance has an *enabled* cryptographic provider
  (`providerKeyFields`, `key_actions.go`); a failed provider read counts as
  none. It has never run against a real provider — no test instance has one —
  so the statement follows the documented grammar and the server has the last
  word.
- **No Rename and no Move to Schema**: none of the three has a `WITH NAME`
  form or an `sp_rename` class, and none is schema-scoped.
- **The owner is the one write on each family's General page**, gated on
  `CONTROL` on the object (the effective answer folds in `CONTROL` on the
  database). `ALTER AUTHORIZATION` drops every explicit permission on the
  object, which the page's note says. Giving ownership to another principal
  also needs `IMPERSONATE` on them — carried implicitly only by `CONTROL` on
  the database — and that half is left to the server (Msg 15151 naming the
  principal). A certificate or asymmetric key cannot be owned by a role (Msg
  15345), so only the symmetric key's picker lists roles. The current owner is
  kept in the list even when it is not a user or role there (a
  certificate-mapped user, an application role).
- **Nothing else on a certificate or asymmetric key is a Properties row.**
  Every other `ALTER CERTIFICATE` / `ALTER ASYMMETRIC KEY` is a private-key
  operation (remove it, re-protect it, back it up) or `ACTIVE FOR
  BEGIN_DIALOG`; the private-key ones touch the server's filesystem or are
  irreversible, so they are context-menu actions of their own, and `ACTIVE FOR
  BEGIN_DIALOG` is left to a scripted ALTER.
- **Signatures page** (certificate and asymmetric key): `ADD SIGNATURE` needs
  `CONTROL` on the signer and `ALTER` on the module; `DROP SIGNATURE` needs
  only `ALTER` on the module. The page is gated on `CONTROL` on the signer or
  the database-wide rights that carry `ALTER` on every module — either lets
  part of it work — and the module half is left to the server (Msg 15151).
  Counter signatures are listed and removable, not addable.
- **Back Up Certificate is a dialog, and ungated.** The public certificate
  backs up for anyone who can see it, `db_securityadmin` included; the private
  key needs `CONTROL` on it (Msg 15247), which the dialog says and the server
  enforces. Paths are the server's, written by its service account (Msg 15240
  when it cannot write the directory), the server never overwrites an existing
  file, and the files come out readable by that account alone. A
  password-protected private key needs its password typed, which the dialog
  checks before sending. There is no asymmetric-key backup: SQL Server has no
  statement for one.
- **Remove Private Key is a confirmation, not a page,** gated on the effective
  `ALTER` on the object (`gate.AlterOnCertificate` / `AlterOnAsymmetricKey`,
  probed per object; refused Msg 15151 to `db_securityadmin`), and withheld
  with the note "no private key" on a node that has none — `nodeData.HasPrivateKey`,
  read by the loader. The warning differs by family: only a certificate's key
  can have been backed up first.
- **Certificate Script as ▸ CREATE emits `FROM BINARY` on every version**,
  reproducing the public certificate exactly — it works on 13 and later, so no
  `WITH SUBJECT` fallback exists. The private key is not scripted (it cannot
  be read), and the script says so. An asymmetric key's and a symmetric key's
  CREATE script creates a *new* key and says so: key material cannot be read
  back, and `KEY_SOURCE` / `IDENTITY_VALUE` are the only way to recreate the
  same symmetric key.
- **Expired certificates carry `(Expired)`** in the tree, like `(Disabled)`.
  SSMS's lack of it is a gap, not a decision.
- **The database master key is a node, the first child of Symmetric Keys,
  present only when the key exists and the caller can see it** (`MasterKey`
  answers nil otherwise). Properties has a read-only General page and an
  Encryption page that adds and drops the service master key's encryption and
  password encryptions; Regenerate and Back Up are dialogs. Every write needs
  `CONTROL` on the database — `ALTER` on it, `ALTER ANY SYMMETRIC KEY` and
  `CONTROL` on everything else are refused (Msg 15151, "Cannot find the
  symmetric key 'master key'") — and a master key the service master key no
  longer encrypts must be opened by password first (Msg 15581), which the page
  and both dialogs ask for. **No Delete**: `DROP MASTER KEY` is refused while
  anything is encrypted by it (Msg 15580), and otherwise is rarely wanted — a
  query window's job. New Certificate / New Asymmetric Key create the master
  key on demand (`ensureMasterKey`); New Symmetric Key has no master-key
  section, since a key encrypted by a certificate uses the public half and a
  password needs none.
- **New Symmetric Key does not offer encryption by another symmetric key**:
  that needs the parent open, which is the Encryption page's decryptor
  section. Create with a password and add it there. `KEY_SOURCE` and
  `IDENTITY_VALUE` are required together — one alone recreates nothing.
- **The Encryption page's decryptor is a page section, not a modal**, and
  symmetric-key chains resolve only when every link opens without a password.
  A parent reachable only by password is left to a query window rather than
  prompting per link. Removing a password encryption needs the password typed
  first (the server finds it by value, Msg 15313). The page's disabled Remove
  on the last encryption is a convenience; the server refuses it anyway (Msg
  15558).
- **Classify a key encryption by `crypt_type_desc` prefix, never by the
  `crypt_type` code.** The codes differ by major (password `ESKP` on 13,
  `ESP2` on 14/17; certificate `EPUC` on 13/14, `C256` on 17) and MI shows the
  13/14 certificate codes while reporting major 12, so a version-keyed mapping
  would be wrong twice over.

**Rights, per verb** (identical for the three families):

| Holder | CREATE | DROP | Sees others' rows | Sym key ADD/DROP ENCRYPTION |
|---|---|---|---|---|
| `CREATE X` | yes | only its own | no | only its own |
| `ALTER ANY X`, `ALTER`/`CONTROL` on the database, `db_ddladmin` | yes | yes | yes | yes |
| `db_securityadmin` | no | no | yes | no |
| `ALTER` on the object | no | **no** | yes | yes |
| `CONTROL` on the object, or its owner | no | yes | yes | yes |
| `VIEW DEFINITION` on the object | no | no | yes | no |

- **The gate sets follow the table.** New: `CREATE X`, `ALTER ANY X`, or
  `ALTER`/`CONTROL` on the database. Delete: `ALTER ANY X`, `ALTER`/`CONTROL`
  on the database, or `CONTROL` on the object from gosmo's per-securable probe
  (classes 25 / 26 / 24), which is what offers Delete to an owner holding only
  `CREATE X`. See `dbScopedOpRights` in `explorer_object_rights.go`.
- **The symmetric key's Encryption page is gated on the effective `ALTER` on
  the key, alone** (`gate.AlterOnSymmetricKey`, probed per key beside
  `CONTROL`). Live on 13 and 17: ADD / DROP ENCRYPTION succeeds exactly when it
  reads 1, including for an `ALTER`-on-key grantee with no `CONTROL`, and is
  refused (Msg 15151) for `CONTROL` with `ALTER` denied — so the Delete set is
  wrong for it in both directions.
- **The second securable is left to the server.** Creating or adding an
  encryption by a certificate needs any of `VIEW DEFINITION` / `REFERENCES` /
  `ALTER` on it; *dropping* that encryption, or opening the key with it, needs
  `CONTROL`. The pickers cannot know every certificate's rights up front, so
  the refusal comes back as the server's Msg 15151.
- **Server refusals passed through, not pre-checked**: Msg 15559 dropping a
  certificate or asymmetric key a login or user is mapped to; Msg 15352
  dropping one that encrypts a symmetric key, or a symmetric key that encrypts
  another; Msg 15581 creating a master-key-protected key with no master key
  (the New dialogs prevent it). A principal with no right on a key sees no
  row — an empty listing, not an error.
- **`HasMasterKey` also reads `sys.databases.is_master_key_encrypted_by_server`**:
  a `CREATE X`-only principal cannot see the master key's row in
  `sys.symmetric_keys`, and was asked to create one the database already had.
  A master key whose service-master-key encryption was dropped is still
  invisible to such a principal — documented on the method.

## Database-scope DDL triggers: what the design settled

- **A database has no flat Triggers folder, and that is the point.** A DML
  trigger belongs to one table or one view and is listed under that object's
  own Triggers folder, which is where SSMS puts it; a database-wide roll-up
  would list every one of them a second time, beside a "Database Triggers"
  folder (DDL, `parent_class = 0`) — a distinction without a difference.
  `Database.Triggers` is gosmo's database-wide read; gossms simply has
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

Encrypt modes, Custom Properties, the Entra field mapping and IPv6 addresses.
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
- **Host Name In Certificate was verified on MI by IP address**: the TLS validation that fails
  without it (`doesn't contain any IP SANs`) passes with it. The login that
  follows is then refused by Azure's gateway (40532: it routes on the server
  name), which is Azure's, not TLS's.
- **`net_packet_size` reads 58 bytes high on an encrypted session** (4154 for
  the driver's default 4096, on 17.0 and MI alike), and the server caps 8192 to
  8000. The live Custom Properties test asks for 16000 and allows for the 58.
- **Azure encrypts every session**: Optional still reads `encrypt_option` TRUE
  on MI. The live test checks Optional only off Azure.
- **On MI, a `packet size` below 4096 cannot connect at all** — `TLS Handshake
  failed: cannot read handshake packet: invalid packet size, it is longer than
  buffer size`, for both Optional and Mandatory. go-mssqldb sizes its handshake
  buffer from the requested packet size and MI's handshake does not fit. A
  driver limitation reached only through Custom Properties; 17.0 takes 2048
  fine.
- **`ApplicationIntent=ReadOnly` was verified on AAG1** with read-only routing
  set up for the run and reverted after: straight to the secondary, no intent is
  refused (Msg 978) and ReadOnly connects; through the listener `ubuaag`,
  ReadOnly is routed to ubusql2 (`Updateability` READ_ONLY) and the default
  stays on ubusql1 — from the Connect dialog too. AAG1 normally has no routing
  list and `ALLOW_CONNECTIONS = ALL`, so intent is unobservable there without
  that setup.
- **Entra's per-method field mapping is verified for every method**, up to the
  driver by `TestEveryEntraMethodBuildsADriverConnector` — which runs each
  method's real DSN through `azuread.NewConnector` — and end to end by hand.
- **An IPv6 literal with a named instance needs an explicit port**
  (`fe80::1\INST,1500`). gosmo refuses it without one: the driver keeps the
  brackets of a port-less literal in the Browser probe's address, so there is
  no URL form that works.
- **Custom Properties separators are `;`, `&` and line breaks, not spaces** —
  `packet size=8192 database=foo` is one entry whose value is
  `8192 database=foo`, which the driver rejects. The dialog's Enter is its
  Connect key, so in practice the separator is `;` or `&`.
- **A downgrade loses saved passwords until the next upgrade.** A release
  that predates the current sealing prefix (`v4:` since the Entra tenant and
  client were bound; `v3:` before that) reads such a value as unprefixed, fails
  to open it and keeps it sealed. A release before v3 also drops
  `encrypt_mode` on its next save, so a Strict entry comes back Mandatory — a
  different AAD, so that password stays unreadable and is re-entered. The `encrypt` boolean is still written so the
  older release can read the file at all.

## Connect dialog: what the SSMS 21 redesign settled — do not re-raise

The dialog is a History pane plus a two-tab form (Connection Properties /
Connection String). Four things about it are decided and are not to be
re-opened without asking the author.

- **A password is stored only when Remember Password is ticked, and the box is
  off for a new connection.** `Config.AddOrUpdate` blanks
  `Password` when `!RememberPassword`, so connecting with the box unticked also
  drops any ciphertext the entry already held — the entry itself stays, because
  it is what the History pane lists. The caller's copy is untouched
  (`AddOrUpdate` takes the connection by value), which is what keeps
  `App.rememberPeerCredentials` working for live peer connects. A saved entry
  that carries a password pre-fills with the box ticked, so entries saved
  before the box existed keep working until one is deliberately unticked.
- **`RememberPassword` is deliberately not in `connectionAAD`.** Toggling it
  must not invalidate a password sealed before the toggle — the same rule
  `secret.go` already states for `Database` and `ExtraProperties`.
- **The port is folded into Server Name for display only;
  `config.Connection.Port` stays a stored field.** It is bound into
  `connectionAAD` (so respelling a saved entry's host and port stops every
  stored password for it decrypting), it is part of `GeneratedName` — the
  saved-connection dedup key and the key the completion inventories share —
  and `db.ResolveServer` drops 0 and 1433 so a named instance still resolves
  its dynamic port through SQL Browser. `currentOptions` splits the field with
  `gosmo.ParseServerAddress`, `PreFill` re-joins it with `db.ResolveServer`
  (the same join dialling uses, so what is shown is what is dialled), and
  `TestConnectDialogFoldingThePortKeepsTheConnectionIdentity` is the guard that
  the round trip leaves `GeneratedName` unchanged. Do not "simplify" either
  side into hand-rolled string surgery.
- **The History pane replaced the server field's autocomplete overlay, and
  there is no filter box.** The list is the picker.
  `config.Config.MatchByServer` stays, called with `""`: it is still the
  "most recent first" ordering source, and that guarantee is now what the pane
  depends on. The dialog opens on the pane's first row — the most recent
  connection — but only into an empty form, since the fields persist across
  Show/Hide and a reopened dialog must not overwrite a half-typed server name.
- **Below the two-pane threshold the dialog drops the History pane; it does not
  clip or scroll it.** A terminal under ~96 columns gets the tabbed form alone.
  The consequence is accepted: on such a terminal only the most recent saved
  connection is offered (it still pre-fills), and any other has to be typed.
  That is the author's call, not an oversight — do not add a narrow-mode picker
  without asking.
- **Out of scope by the author's decision**, from the SSMS 21 mockup: the
  Browse tab, the Favorites group, the Copy/Paste/Apply/Reset buttons under the
  connection string, and the Name/Color custom-property rows. The connection
  string stays a live, masked preview, not an editable field with Apply. The
  "Server Type: SQL Server Database Engine" line is dropped — one fixed value,
  and the two-pane header row is worth more.

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
  25 `RevertFn` closures. Two rules come with it, both easy to undo by accident:
  **`Ctrl+Z` is handled ahead of the zone switch, beside `F5`** — a sheet-level
  command, so it works from the page list and the button row — but
  `PropertySheet.HandleKey` gives the focused row first refusal through
  `focusedRowHandles`, so undo inside a job step's T-SQL box does not discard
  every other row's edits. And **the rows that host a `controls.Editor` — the
  one row widget with a `Ctrl+Z` of its own — are what that first refusal is
  for**: `propsheet.EditorRow`, at three live call sites
  (`prop_grid_helpers.go`'s `sqlBodyRow` on every Definition page,
  `type_props.go`'s XML schema collection Documents box, and
  `agent_job_step_panel.go`'s step Command box). The two read-only ones can
  never fire it — `controls.readOnlySafeKey` rejects `Ctrl+Z`, so the key falls
  through to `RevertPage` — and the writable one is reached first by
  `focusedRowHandles`, which is what keeps page-level revert and in-box undo
  apart. `widgets.InputField`, the other row widget that claims keys, takes
  `Ctrl+A`/`Ctrl+U` and not `Ctrl+Z`, so ordinary rows leave it free. A fourth
  `EditorRow`, writable or not, inherits all of this for free; a *new row type*
  with a `Ctrl+Z` of its own and no `KeyHandler` does not, and needs a
  different key.

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
  unwrapped, sidesteps it. With Word Wrap (Alt+Z) on the panel draws wrapped
  too; `drawWrapped` hands `styleAt` only the runs overlapping each visual
  row (`runsInSpan`), which keeps the scan to the tokens on screen.

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
  reference graph and rejected on the numbers; the splits that paid are the
  parts with *zero* outbound references back into `tui`, and they are done —
  `sqlparse`, `planview`, `dashboard` and `gate`. File length alone is not a
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

- **Both cache hit ratios are read as `cntr_value / base`, and neither is a
  since-startup average.** Settled by a live run against win10cli (SQL Server
  17.0.1135.8) on 2026-09-18, because the `cntrFraction` arm in
  `internal/activity/counters.go` never touches `prev` while the
  `cntrAverageBulk` arm right below it does, which reads as an oversight. It is
  not one, and the two counters are not even the same shape:

  - **Buffer Manager's "Buffer cache hit ratio" is a window over recent page
    lookups**, not a running total. Twelve readings three seconds apart across a
    `DBCC DROPCLEANBUFFERS` and a 1.5 GB scan gave bases of 104, 3728, 33057,
    29511, 87411, 21375, 126, 218 — it falls as often as it rises. `cur/base` is
    therefore already a live number; a delta divides one window by the change in
    another window's *size*, which reads 100.09% across the 29511 → 87411 pair.
  - **Plan Cache's "Cache Hit Ratio" at `_Total` is the sum of its five cache
    stores' own rows** (Bound Trees, Extended Stored Procedures, Object Plans,
    SQL Plans, Temporary Tables & Table Variables — they add up exactly), and
    each restarts at zero when that store is trimmed. Ordinary churn steps the
    sum backwards by an unrelated mix of hits and lookups (-1229 value against
    -1717 base, observed mid-run with no explicit flush), and a delta reads 104%
    and 672% when only some of the stores restart. It is not frozen either: the
    same trimming keeps the base small, so the average decayed 90.68% → 54.24%
    across one burst of ad-hoc batches.

  So the delta form is wrong for one counter and unsafe for the other, and the
  "Buffer cache hit ratio is always 99.9%" complaint is about what the engine
  counts, not about this arithmetic. `TestNeitherCacheHitRatioIsReadAsADelta`
  pins both with the readings above. Do not re-propose a `prev`-based arm for
  `cntrFraction`.

- **`appendValue`'s `case float32` is unreachable but kept.** go-mssqldb returns
  `float64` for both `REAL` and `FLOAT`. It is correct if the driver ever
  narrows, and `appendFloat` already takes the bit size.

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
  retryable is correct.** Both repos require go-mssqldb v1.11.2 (re-checked
  2026-09-23; v1.11.1 and v1.11.2 changed only DSN parsing), where
  `ServerError`'s doc comment still reads "returned when the server got a fatal
  error that aborts the process and severs the connection", and the two rows
  loops that route a token error through `Conn.checkBadConn` still discard the
  plain `mssql.Error` case with "Ignore non-fatal server errors". Cited by
  symbol, not by line: both have moved across releases.

- **One worker pool is spawned outside `safego`/`safegoRepair`** —
  `App.fanOut` (`safego.go`), which the Detail Browser backfill and the Log File
  Viewer's `readLogFiles` share — deliberately taking both the label and the
  recover by hand.

- **`charts.StackedHistoryChart.Draw` stays, though nothing in the binary
  reaches it.** `doc.go` advertises `Draw` as the entry point and all six chart
  types implement it. The dashboard uses `DrawFrame` only because it also wants
  the time row. Removing one of six would break the package's one uniform method
  for four lines. Its `Plot` and `TimeRow` stay for the same reason: they mirror
  `HistoryChart`'s pair.

- **`theme.SetPalette` and `widgets.SpinnerByName` stay in production files,
  though only tests reach them** (`deadcode ./cmd/gossms`). Both are tuikit
  API that `internal/tuikit/README.md` documents — reskinning at start-up, and
  resolving a spinner from a config string — not test helpers; moving either
  into a `_test.go` file would leave the README describing a feature the
  package no longer has. Likewise `config.UseTrackedQueries`: it is called by
  `internal/tui`'s tests, and a `_test.go` file in `config` is invisible to
  another package's tests.

- **The five `staticcheck` U1000 findings in `clipboard_host_test.go` and
  `dialog_gesture_test.go` are suppressed, not deleted.** The fields are read by
  the reflection walk each test exists to exercise, so `type page`/`field p`/
  `field row` *are* the fixture — deleting `p` leaves `lazy` with nothing behind
  the nil pointer and the test asserts nothing. Each carries a
  `//lint:ignore U1000` naming the reader.

- **`rightAlterAnyLinkedSrv` and `rightCreateTable` still read as unused.**
  Deliberate; pinned by `internal/tui/gate/names_test.go`.

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

- **The two search dialogs' key-routing skeleton stays duplicated.**
  `FindReplaceDialog.HandleKey` (`internal/tui/find_replace_dialog.go`) and
  `LogSearchDialog.HandleKey` (`internal/tui/log_search_dialog.go`) share the
  Tab/Backtab `nextFocus`/`prevFocus` + `syncFocus` pair, Escape, Enter →
  `pressButton(btnFocus())` and the trailing `fields()[focusIdx]` dispatch.
  Left alone: only the Tab/Backtab half has no
  per-dialog variation, and everything around it genuinely differs — Escape
  hides one and dismisses the other, `FindReplaceDialog` has an extra `F3` arm,
  and the field switch handles several widget types in one dialog and only
  `*widgets.InputField` in the other. A `dialogFocusKey` helper would be twelve
  lines with two callers. Extract it if a third search dialog appears, or
  opportunistically if a change lands in either file anyway; do not re-propose
  it on the duplication alone. `log_search_dialog.go`'s `HandleMouse` comment
  ("the same shape as `FindReplaceDialog`'s") is the cross-reference that goes
  stale — check it when either file moves.

- **The editor expands every tab to spaces, on purpose.** `Editor.expandTabs`
  (`internal/tuikit/controls/editor_actions.go`) runs on `SetText`, `Paste`,
  block insert and Replace, so the buffer never holds a tab — including one
  inside a string literal, and File > Save writes the spaces back. Raised as a
  bug by the 2026-09-23 review (S5) and withdrawn: **author's call, as is.**
  Do not re-propose tab-preserving buffers or tab-stop rendering.

## Release workflow

The failure mode the two publishing jobs guard against is a job that goes green
having pushed nothing: the `homebrew` job stages first and compares against the
index (`git diff --quiet` reports no diff for a path git has never tracked),
which is what the `apt` job already did. Each ends in a **Verify** step that
fetches the *remote* back and fails unless it carries this tag — the tap's
requires every archive URL in `origin/<branch>:Formula/gossms.rb` to be on
`releases/download/<tag>/`, and the apt job's requires both `.deb`s in `pool/`, a `Version:` line in
each architecture's `Packages`, and all three of `Release`, `InRelease` and
`Release.gpg`. Two things about those steps a plausible simplification undoes:

- **They read the remote with `git`, never over HTTP.** `raw.githubusercontent.com`
  and `https://radix29.github.io/apt` both serve a cached copy for minutes after
  a push, so an HTTP check would fail the step for a repository that is in fact
  correct. `git fetch` + `git show`/`git cat-file -e` sees the pushed commit.
- **They are separate steps, after the push, not extra lines inside it.** The
  push step's guard legitimately `exit 0`s when there is nothing to do; that
  ends the *step*, so a verify folded into it would be skipped by the very case
  it exists to catch.

The formula carries **no `version` line**. Homebrew scans the version from the
release URL, and `brew audit --strict` flags an explicit line as redundant. So
the tap's Verify step checks that all four archive URLs are on this tag, and
does not grep for a `version` line.

The formula is deliberately **binary**, not build-from-source: `go.mod`'s active
`replace` makes any source build from a release tarball fail, and
`go install …@<tag>` fail with it.

## Progress dialog: what is deliberately out of it

Every confirmed write from Object Explorer, the Details pane, Always On, Agent,
the Log File Viewer, Query Store and Activity Monitor runs behind
`dialogs.ProgressDialog` (`App.runWithProgress`). Not the rest:

- **Properties and New … keep their own spinner and live Cancel** on the sheet's
  button row: closing the sheet for a separate dialog would take the pages and
  the message line — the only account of a partial apply — off screen.
- **Back Up and Restore keep their progress view**, a Task with a percentage;
  the Restore dialog's overwrite confirmation hands off to it.
- **Script runs nothing**, so it never shows the dialog.
- **The unconfirmed half of a toggle runs behind it too** — Enable, Bring
  Online, Start Endpoint, Join, Resume — because it shares the confirmed half's
  run function. The 250 ms reveal delay keeps a fast one invisible. So do the
  Agent writes SSMS never confirms: Enable/Disable on a job, schedule, alert or
  operator, and Start/Stop Job.
- **Not driven live:** the failover dialogs (need the AG cluster), and the
  "Cancelling …" state, which was never on screen long enough to capture — a
  cancelled DROP returned within one frame.
  `TestProgressDialogCancelAsksOnceAndStaysOpen` covers its drawing.

## Details pane: the "Not connected" branch — settled, do not re-raise

`ShowNodeDetails`'s "Not connected" branch (`detail_browser.go`) is **defensive
and unreachable through the UI as it stands**, and kept:

- Every tree node's connection is its root's. `explorer_loaders.go`'s `l.node`
  sets `conn: l.sc`, and a peer opened by `resolveAGView`/`alwayson_menu.go` is
  only ever *read* through — no node carries one, so "a closed peer under a node
  still in the tree" cannot be staged either.
- `App.disconnect` is the only caller of `ServerConn.Close` on a tree
  connection, and it closes, purges (`PurgeConn` nils `currentNode` and calls
  `showEmpty`), then `RemoveRootByConn` — the root never outlives its
  connection. The other `Close` calls are on connections no node references: a
  cancelled connect, a query panel's, Activity Monitor's.

Staged on screen, the pane keeps the node title, shows the single `Status | Not connected`
row, and drops the previous node's rows, chart strip, pinned tooltip and its
context-menu verbs. `TestShowNodeDetailsNotConnectedDropsThePreviousNode`
asserts exactly that — do not delete the branch as dead code.

## No top-level plan document — do not re-raise

Neither repo has a top-level plan file, and one is not to be created as a
working file. Project state and the package map live in `ARCHITECTURE.md`,
unfinished work in `docs/open-threads.md`, what shipped per tag in
`CHANGELOG.md`, settled questions here.
