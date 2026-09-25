# Decisions

Settled decisions and deliberate exclusions — what stops a settled question
being reopened. Each entry names what was decided and why re-raising it costs.
Work knowingly left undone is in `docs/open-threads.md`.

## Azure SQL Managed Instance

Supported; nothing open against it.

- **MI reports `ProductVersion` `12.0.2000.8`** while running engine 18.0, so
  every gosmo `colSince`/`VersionMajor` gate degrades or refuses features it
  has. **Gate on `EngineEdition` first** — `internal/tui/edition_gate.go` is the
  edition counterpart of `internal/tui/gate/gate.go`.
- **Database Properties > Resource Governance is read-only and Azure-only.**
  Every value changes only by resizing (control plane). It pairs
  `sys.dm_db_resource_stats` (reading) with
  `sys.dm_user_db_resource_governance` (the limit it's a percentage of). Missing
  rights fail differently — Msg 262 vs zero rows — and both become a note
  (`pageDatabaseResourceGovernance`'s doc comment).
- **Activity Monitor's tab bar is a slice**: `visibleTabs()` filters
  `amAllTabs`; `amTabLabels` and per-tab scroll arrays stay indexed by `amTab`,
  sized `amTabCount`. `setTab` is the one gate, so a new conditional tab needs
  only an `azureOnly`-style predicate.
- **The Instance tab's charts scale by the server's 15-second window**, not the
  refresh rate, and `internal/activity.Poller` keeps pre-aggregated sources out
  of `rates.go` (see `drawInterval`'s and `Poller`'s doc comments). IO ceilings
  are section-bar KPIs, not axes — MI reports a fixed limit, not a series.
- **`WITH INIT` is hardcoded on a URL device** — no "append to media set"
  (`TestBackupOptionsBuildTheExpectedStatement`, "init is always set"). Changing
  it for URL alone would change every on-prem backup.
- **`RESTORE ... WITH MOVE` isn't gated on MI**, though MI places files itself
  (why `CREATE DATABASE` file clauses fail Msg 41918). Withholding a working
  option is the worse error.
- **Restore's Backup History source drops entries with no device.** MI's
  automated backups have NULL `physical_device_name` (`device_type` 9 — all 102
  of msdb's rows on `t-qmi-01`) and restore only via control-plane PITR.
  `restorableHistory` (`restore_dialog_ops.go`) filters them *before* the
  `maxHistorySets` cap, and says so when nothing's left. Filtered in the dialog,
  not gosmo — history viewers show them as true history.
- **`URL` is the right device keyword**: MI answers `RESTORE VERIFYONLY FROM
  DISK = N'https://…'` with Msg 41902, and `FROM URL` with Msg 3078 about the
  blob.
- **Entra**: a failed Service Principal secret isn't served from `EntraCache` —
  correcting it in the same dialog connects. A deleted-and-recreated account
  fails 18456 "Could not find a user matching the name provided" *after* a good
  token (the login's SID is the old object id) — recreate the login.
- **An expiring token renews silently.** A window opened within
  `entraTokenMargin` (5 min) of expiry fetches one new token, connects as
  FEDERATED without prompting; later windows and expands reuse it. Connected
  sessions are unaffected (tokens are checked only at login). gosmo's part is
  method-independent (`TestEntraCacheRenewsAnExpiringToken`).
- **With TenantID blank, MFA and Device Code sign in to the server's tenant.**
  azidentity's default `organizations` refuses a personal account that is a
  member of the server's tenant (SSMS accepts it). The tenant is only in the STS
  URL the server announces mid-login, so gosmo's `Warm` opens a login, abandons
  it once SPN and STS URL are named, signs in to that tenant, and caches it per
  server in `EntraCache`. On `t-qmi-01`: ~0.25 s, SPN
  `https://database.windows.net/`. Each probe logs one **Error 33155, severity
  20** on MI; on-prem without Entra it's an immediate 18456. Don't "optimise"
  back to a DNS-suffix guess — it can't know the tenant. `TestLiveEntraProbe`
  (gosmo, `-tags livedb`).

## Deferred scope (repeatedly, deliberately)

- **No CI, by decision.** No `ci.yml` and no tag-only guard against an
  uncommented `replace`: a spare-time single-author project keeps mechanical
  checks as local discipline (`CLAUDE.md` § Build & verify; `staticcheck` is
  zero-output on both repos). The one thing only CI would catch — gosmo needing
  a tag first — is a release step (`CLAUDE.md` § What this is; `replace` under §
  By design). Re-raising needs a new reason.
- **`DatabaseByName` calls in `internal/tui` stay.** Many loaders could use
  `Server.DatabaseRef(name)` and save a round trip per expansion, but that's
  unmeasured and not mechanical — the `Ref` leaves `id` 0, so `IsSystem()`/
  `IsSnapshot()` answer `false`. Reopen only with a measured real expand (MI's
  ~41 ms round trip would decide it).
- **Five of seven Service Broker families are read-only — a deferral, not the
  Rules/Defaults refusal** (six have an ALTER). Each props file argues its own
  reason: message type and service ALTERs change a running application's wire
  contract; a contract has no ALTER; a binding's ALTER needs a user already
  owning the remote certificate (an import left to a query window, § Keys and
  certificates); a broker priority's write can't be gated on any published
  right but ALTER on the database, which would show read-only to everyone
  able. Queue and Route are editable (their settings change in operation).
  **No New-X dialogs for any of the seven**: each needs the others first, and
  services/contracts are authored with their application.
- **Login authentication kind isn't editable** in Login Properties (static
  row). New Login creates all five kinds, but `ALTER LOGIN` can't change it —
  drop-and-recreate loses the SID and orphans mapped users. Deliberate.
- **No principal-browse picker** — Windows logins are typed `DOMAIN\name`.
- **Entra logins stay unverifiable here.** `CREATE LOGIN ... FROM EXTERNAL
  PROVIDER WITH OBJECT_ID` parses (win10cli returns the same Msg 37525 for it
  and the bare form). On `t-qmi-01` the bare form, run by hand as SQL-auth
  sysadmin, created working user and service-principal logins; the dialog's
  `WITH OBJECT_ID` form hasn't been executed.
- **The Phase 3 tree families are read-only** — no create, no edit; they still
  have Script as, Delete, and (where SQL Server has it) Rename and Move to
  Schema:
  - **Rules and Defaults**: deprecated since 2008, no ALTER. Use CHECK/DEFAULT
    constraints.
  - **Assemblies**: need the compiled binary; `PERMISSION_SET`/`VISIBILITY` are
    scripted instead.
  - **Alias, table and CLR types**: no ALTER; dropping is refused while
    anything declares the type.
  - **External data sources/file formats**: no ALTER on any supported major.
    **External libraries**: `ALTER EXTERNAL LIBRARY` replaces a binary package.
  - Exception: **Plan Guides**' General page enables/disables
    (`sp_control_plan_guide`, gated on ALTER DATABASE) — a disabled guide is
    invisible but for its label suffix. Nothing else is editable (no ALTER;
    query text matches character for character).
- **A snapshot's subtree offers writes the server refuses.** It gets Tables,
  Views, Programmability (not Query Store, Storage, Security — none apply);
  Delete/Rename inside is offered and refused "read-only". The gate answers what
  the *login* may do; a read-only-database gate would have to cover every
  READ_ONLY database. SSMS does the same.
- **Service Broker details easy to undo:**
  - No rename in the family (no `sp_rename` class for any of the seven).
  - `azureRefusedScriptVerbs` withholds the remote service binding's CREATE and
    DROP And CREATE on Azure (compile-time Msg 41906), not its DROP. Withheld
    verbs draw greyed with `N/A`, no submenu arrow, unreachable by key or mouse
    (verified on MI with a fabricated node).
    `TestTheBindingsCreateIsWithheldOnAzure` (`service_broker_ops_test.go`).
  - **Route Msg 41943 is not gated**: `CREATE`/`ALTER ROUTE` with `ADDRESS =
    'TRANSPORT'` or any `MIRROR_ADDRESS` on MI (probed `t-qmi-01` 2026-09-17) is
    a *runtime* refusal. Nothing gossms emits hits it except user text typed
    into Route Properties, which gets the server's clear message. Gating one
    value of a free-text field per edition isn't a shape `edition_gate.go` has.
  - Both writable pages (and any future New-X) respect: nil field = leave
    alone, only changes are sent; `QueueSettings.Activation` is restated in full
    (the server refuses a partial ACTIVATION on a queue without one), so an
    emptied procedure name maps to `ACTIVATION (DROP)` and is an error on a
    queue with no activation; **`RouteSettings` can change but never clear** a
    setting (`= NULL` doesn't parse, empty is refused — drop and recreate), so
    Route Properties refuses an emptied row with a message.
- **System Data Types has no Properties dialog** (SSMS has none; `int` says it
  all). Folder and Detail Browser listing exist; the menu omits the item.

## Permission gating: what is settled — do not re-raise

- **A queue's Properties and Delete use different rights; the owner is
  knowingly withheld Delete.** Probed 2026-09-16 on 13/14/17: `ALTER ON
  OBJECT::<queue>` alters and is refused the drop (Msg 15151; drop needs
  CONTROL on the queue or ALTER on its schema). `gate.QueueAlterRights` is
  `gate.ObjectWriteRights()`; `queueDropRights` is that minus the object ALTER.
  They're told apart by `rightControlOnObject` (gosmo's
  `ProbedObjectPermissions` `O:CONTROL` beside `O:ALTER`; ALTER reads 1 for
  either grant, CONTROL only for a CONTROL grant or the owner).
- **Move to Schema on every class-1 family is gated on CONTROL on the object;
  one set serves all nine.** `TRANSFER` needs CONTROL on it plus ALTER on the
  target schema. Probed twice on 17 (queue 2026-09-16, table 2026-09-17), one
  `WITHOUT LOGIN` user per right, identical:

  | right held | `O:ALTER` | `O:CONTROL` | transfer |
  |---|---|---|---|
  | ALTER on the object | 1 | 0 | refused, Msg 15151 |
  | CONTROL on the object, or its ownership | 1 | 1 | went through |
  | CONTROL on the object, DENY ALTER on it | 0 | 1 | **went through** |
  | CONTROL on the database | 1 | 1 | went through |
  | db_ddladmin | 1 | 0 | refused, Msg 15151 |
  | CONTROL on the database, DENY CONTROL on the object | 0 | 0 | refused, Msg 15151 |

  So `objectTransferRights` falls back to `classOneTransferRights` (CONTROL on
  the object or database) for table, view, procedure, function, sequence,
  synonym, rule, default and queue — never `objectDataRights`, which offered
  rows one and five. Row three is why three ALTER-denial menu tests exempt Move
  to Schema. Knowingly excluded: CONTROL on the source schema reads through
  gosmo's schema probe as ALTER (indistinguishable from ALTER alone), so such a
  principal isn't offered a move the server would allow — the same trade as
  `queueDropRights`.

**Classes 0, 1, 3, 4, 5, 6, 10, 101, 105 and 108 are gated.** Each row below is
a *wrong* gate if assumed the other way round.

**Every schemaless database-level family has an explicit set**
(`dbScopedOpRights`, probed on 13/14/17 — see its comment);
`TestSchemalessDatabaseOpsAreGated` fails a new one without. The OBJECT-scope
plan guide is answered by the routine (`planGuideRights`) for Delete,
Enable/Disable and Properties.

**A security policy's Delete and Disable/Enable ask both halves**: ALTER ANY
SECURITY POLICY **and** ALTER on its schema (Msg 3701/33268 otherwise). Probed
2026-09-11 on 13 and 17, 17 cases: with the policy right, both worked exactly
when `HAS_PERMS_BY_NAME(schema, 'SCHEMA', 'ALTER')` was 1 (which folds in schema
CONTROL/ownership, ALTER ANY SCHEMA, db_ddladmin, database ALTER/CONTROL); a
schema DENY beat database ALTER; CONTROL on the policy permits neither. So the
schema half is `rightAlterOnSchema` alone (`conjoinedOpRights`), via
`gate.ItemOnAll`/`gate.AllowsAllOn`, whose note names the first failing group.

### The behaviour table, probed live

Majors 13/17 for server classes, 13/14/17 for database ones, the Pacemaker
cluster for 108 (win10cli has no HADR). Identical everywhere, so a future
difference is a behaviour change.

| DENY ALTER on | withholds | does **not** withhold |
|---|---|---|
| `USER::u` (4) | `ALTER USER ... WITH NAME`, `DROP USER`, `ALTER ROLE ... ADD MEMBER u` | — |
| `ROLE::r` (4) | `ALTER ROLE r ADD/DROP MEMBER` | rename (`WITH NAME`), `DROP ROLE` |
| `LOGIN::x` (101) | `ALTER LOGIN` (rename, password), `DROP LOGIN` | being *added* to a server role |
| `SERVER ROLE::r` (101) | `ALTER SERVER ROLE ... ADD/DROP MEMBER` | rename (`WITH NAME`), `DROP SERVER ROLE` |
| `ENDPOINT::e` (105) | `ALTER ENDPOINT` | — |
| `AVAILABILITY GROUP::g` (108) | every `ALTER AVAILABILITY GROUP` — options `SET`, `ADD`/`REMOVE DATABASE`, `MODIFY REPLICA`, `FAILOVER` | `ALTER DATABASE ... SET HADR` (suspend, resume, join) |

Refusals are Msg 15151, except the endpoint's **Msg 6004**. `HAS_PERMS_BY_NAME`
reads 0 for the denied ALTER in every row, including actions the server allows
— so the arm is asked **per action, not per object** (the paired rights
`rightAlterAnyDBRole`/`rightAlterAnyDBRoleMembers`,
`rightAlterAnyServerRole`/`rightAlterAnyServerRoleMembers`).

- **Membership checks the member at database scope, not server scope.** A
  class-4-denied user can't be added to an undenied role; a class-101-denied
  login can. So Database User > Membership carries the arm, Login > Server Roles
  deliberately doesn't (`TestLoginServerRolesDeclaresNoServerDenialArm`).
- **There is no class 110.** Logins and server roles are both class **101**,
  told apart by the principal's `type_desc` (as class 4 splits users from
  roles).
- **Class 108 can't be read from the catalog** — `sys.server_permissions`'
  `major_id` is an internal AG id no view maps to a name. gosmo asks
  `HAS_PERMS_BY_NAME` per group (`ProbedAvailabilityGroupPermissions`; groups
  are few). A 0 counts as a *denial* only while `ALTER ANY AVAILABILITY GROUP`
  is held; otherwise it means "holds nothing"
  (`TestAGroupIsNotDeniedWhenTheWideRightIsMissing`).
- `ALTER AUTHORIZATION` proves nothing: it's refused on an *undenied* role at
  both scopes (needs CONTROL on the role plus IMPERSONATE on the new owner).

**Classes 5, 6 and 10 are answered by effective CONTROL, with no DENY arm.**
Probed 2026-09-11 on 13/14/17, `WITHOUT LOGIN` user per case, identical;
"CONTROL" = gosmo's `SecurablePermissions` (`HAS_PERMS_BY_NAME(...,
'CONTROL')`); ALTER on the target schema held for every transfer:

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

An alias type's rename (sp_rename `USERDATATYPE`) matched DROP in every grant
row on 13 and 17, so Rename shares Delete's set. Move to Schema asks CONTROL
alone (`securableTransferRights`); Delete/Rename ask it beside the wider rights
(`securableOpRights`, `dbScopedOpRights`). DENY CONTROL needs no gate: it
removes VIEW DEFINITION and the securable vanishes from `sys.assemblies`/
`sys.types`/`sys.xml_schema_collections` — no node exists. Hence gosmo reads no
DENY rows at these classes. DENY ALTER withholds nothing gossms offers while
reading ALTER 0 — ALTER is the wrong question here.

### Where the per-row answers are deliberately not stated

Pages whose rows differ in permissibility declare nothing rather than a banner
wrong for some rows: Login > **User Mapping** (`ALTER ANY USER` per mapped
database), Login > **Server Roles** (a DENY on the role, not the login), and
Database User > Membership's per-role half. Server Role > **Owned Roles** also
declares no arm — `ALTER AUTHORIZATION` is refused on undenied roles too.

### The rest

- **No other class is reachable** — no fulltext catalog node. Certificates
  (class 25): Delete gated on `ALTER ANY CERTIFICATE`, database
  `ALTER`/`CONTROL`, or `CONTROL` on the certificate (per-securable probe — which
  offers it to the owner, including a `CREATE CERTIFICATE`-only principal);
  Owner row on object `CONTROL`; Script as ungated. Asymmetric keys (26) the
  same on `ALTER ANY ASYMMETRIC KEY`/`CREATE ASYMMETRIC KEY` + key `CONTROL`;
  symmetric keys (24) the same, Encryption page on the Delete set — all probed
  identically (§ Keys and certificates). Service Broker families gate on their
  database-wide `ALTER ANY …` rights (`internal/tui/gate/gate.go`); column
  master/encryption keys, partition functions and schemes have no securable
  class.
- **The column path is dormant — do not re-raise.** `ProbedObjectPermissions`
  is `ALTER` (not column-grantable), so `ColumnPermissions` is always empty and
  `DeniedOnAnyColumn` never fires. Adding `SELECT` wouldn't help:
  `gate.ObjectDenial` asks columns only for rights an action lists, and the only
  object right declared is `rightAlterOnObject`. Firing it needs an action gated
  on object `SELECT`; Select Top 1000 Rows only generates text. The machinery
  stays, T-SQL-verified. For whoever picks it up: `SELECT` in the probe brings
  public's catalog-view grants into `ObjectPermissions` (232 rows on
  HealthClinic), which must **not** be filtered by `is_ms_shipped`; and
  `internal/tui/gate/names_test.go` checks object rights against
  `ProbedDatabasePermissions`, not `ProbedObjectPermissions`, so a new one would
  silently gate nothing.
- **A schema *node* is excluded from the schema-scoped gate** — `objectOpRights`
  names `rightAlterOnSchema` beside the three database-wide rights, but ALTER
  on a schema doesn't permit dropping or renaming the schema itself.

Three facts the object-scope gate rests on: **db_owner is not exempt** from an
object DENY but **sysadmin is**, and the probe includes `public`, so a DENY to
public is recorded for sysadmin too — hence the explicit sysadmin bypass.
**Ownership needs no exception**: SQL Server refuses a DENY to an owner, and
`ALTER AUTHORIZATION` deletes existing DENY rows. Schema scope needs its own
catalog read (`explicitSchemaCapabilityQuery`, `E:` rows) because
`HAS_PERMS_BY_NAME` returns 0 alike for never-granted and denied — withholding
on that would empty menus for everyone working through a database-wide grant.

## gosmo: deliberate API decisions

- **No `Drop*` write method carries `IF EXISTS`** — "deleted" means deleted;
  idempotence is the caller's choice (`TestDropStatementsAreNotIdempotent`).
  Scripter's DROP-and-CREATE *scripts* keep `IF EXISTS`, being made to re-run.
- **`CertificateByName`/`AsymmetricKeyByName` return `ErrNotFound` on absence**
  like every `*ByName` (since 2026-09-22's breaking release; previously `(nil,
  nil)`). Callers treating absence as ordinary test `errors.Is(err,
  gosmo.ErrNotFound)` (the endpoint pipeline's `findCertificateIfAny`). The
  remaining conventions are on `ErrNotFound`;
  `TestLiveCertificateNotFoundIsErrNotFound` pins both directions.
- **Missing and invisible principals are the same `ErrNotFound` — SQL Server's
  doing, not gosmo's to fix.** Metadata visibility returns zero rows, not an
  error, without `VIEW ANY DEFINITION`
  (`TestLiveNotFoundCannotSeePastMetadataVisibility`). The answer is
  idempotence at the write. `isAlreadyExists` matches error numbers first
  (15025 logins, 15023 users, on the error and every entry of its `All`), and
  an "already exists" substring only for non-SQL-Server errors.
- **`JobState` follows Agent's real encoding** (`xp_sqlagent_enum_jobs`): 1
  Executing, 2 WaitingForWorker, 3 BetweenRetries, 4 Idle, 5 Suspended, 6
  WaitingForStepToFinish, 7 PerformingCompletionActions, 0 = not run by Agent.
  No Cancelling or Running state.
- **Lookup-free handles carry `Ref`; `*ByName` lookups keep their names** (55
  families — `~/go/gosmo/CLAUDE.md`'s `Ref` bullet lists them and owns the rule
  on when a handle is needed; "under a `WithScript` context" alone is
  over-broad, since `WithScript` intercepts writes only). **Two alternatives
  rejected:** giving the lookup the plain name splits it from every other
  `*ByName`; a distinct handle type with `Load(ctx)` would make zero-valued
  accessors impossible but isn't worth changing every call site — reopen only
  with a bug the suffix failed to prevent. The handle form itself stays.
- **`Database`'s catalog state is exported fields** (`Name`, `ID`, `State`,
  `RecoveryModel`, `CompatibilityLevel`, `Collation`, `IsReadOnly`,
  `CreateDate`, `SourceDatabaseID`), like every gosmo type. `IsSystem()`/
  `IsSnapshot()` are derivations and `Server()` a back-pointer, so methods.
  `Server.Name()` stays (it reads `s.info.Name`). The `Ref` trap
  (`DatabaseRef("master").IsSystem()` is `false`) is documented on
  `Server.DatabaseRef` and `Database`.
- **azidentity deprecated `UsernamePasswordCredential`** (no MFA), used by
  `AuthEntraPassword`/ROPC in `entra.go` (options literal and
  `NewUsernamePasswordCredential`). **Kept** — a supported mode verified live on
  MI; both sites carry `//lint:ignore SA1019`. Watch for *removal*
  (`~/go/gosmo/OPEN-THREADS.md` § azidentity).

The job-state read, load-bearing:

- **The `INSERT ... EXECUTE` table matches the extended procedure's 13 columns
  exactly** — `jobStateColumns` is copied from `sp_get_composite_job_info`, not
  trimmed to the two gosmo reads.
- **A failed state read isn't a failed listing**: `applyJobStates` swallows the
  error and keeps the `sysjobactivity` derivation (Agent stopped →
  `xp_sqlagent_enum_jobs` returns zero rows). The derivation uses the same
  encoding (`THEN 1 ELSE 4`).
- **`jobIsRunning` answers "unknown" for `JobStateUnknown` and
  `JobStateSuspended`; `jobStateRefusal` then refuses nothing.** Suspended is
  ambiguous (sp_help_job calls it idle; it still holds a session). Let the
  server answer.

## FILESTREAM: what the live run settled — do not re-raise

`internal/tui/live_filestream_test.go` (`livedb`) runs Database Properties >
Files against a real FILESTREAM database on win10cli.

- **`FileGroups` returns a FILESTREAM filegroup like any other**, so the picker
  lists it; `preservingItems`' widening isn't needed.
- **The filegroup alone makes a file FILESTREAM** — `ADD FILE` has no type
  keyword (a file added to PRIMARY with an extensionless path came back ROWS).
  So the Type picker offers nothing more; `addableFileTypes` stays two items.
- **SIZE and FILEGROWTH are refused on a FILESTREAM file (Msg 5509); MAXSIZE is
  accepted** — both measured. The page omits both clauses there, greys the two
  spinners, and records the type as FILESTREAM.
- `gosmo.FileGroup.Type` (`type_desc`) and `IsFileStream()` carry it.
  `is_default` is per filegroup *type* (two defaults, both true). Fakes report
  `max_size` -1 for a FILESTREAM file.

**Enabling FILESTREAM on win10cli needs an OS admin** (RsFx0800 driver and
share, via Configuration Manager); registry `EnableLevel` + `sp_configure` from
`xp_cmdshell` succeed and do nothing. The live test skips when the effective
level is 0.

## Always On: what is deliberately out of scope

- **New Availability Group and Add Replica don't roll back a group whose CREATE
  succeeded.** Every ordinary JOIN failure is checked *before* CREATE (peer
  reachable, Always On enabled, endpoint present/STARTED/at the recorded
  address, join rights), refusing with nothing created. Only a peer dying
  between check and JOIN remains, and then the group is left: a rollback would
  destroy what was asked for over one unreachable instance, and couldn't be
  complete (the secondary keeps it in `sys.availability_groups` and needs a
  local DROP).
- **No Read-Only Routing page in the create dialog** — AG Properties covers it.
- **The endpoint dialog assumes a full mesh and one shared master-key
  password** (used for instances without a database master key).
- **Add Replica offers no initial data sync.** AUTOMATIC seeding covers SSMS's
  share route; MANUAL means restore by hand, then Join to Availability Group
  (in the tree).
- **A listener address can't be removed, nor a listener renamed** — `MODIFY
  LISTENER` has neither; both mean REMOVE + ADD LISTENER, and Listener
  Properties says so. Under EXTERNAL an added address is recorded OFFLINE.
- **No T-SQL failover under `cluster_type = EXTERNAL`** — handled:
  `agFailoverRefusal` explains and names Pacemaker. EXTERNAL rejects both
  `FAILOVER` and `FORCE_FAILOVER_ALLOW_DATA_LOSS` (Msg 47104); NONE rejects only
  the lossless form (Msg 47122).
- **New AG offers no backup from inside the dialog.** `ADD DATABASE` and `FOR
  DATABASE` refuse an unbacked-up database (Msg 1475), so the page applies that
  rule and its "Not offered" line names the Backup dialog. Backup history is
  wrong both ways (deleted msdb history still joins; a SIMPLE round-trip doesn't
  though history shows a full backup).

Peer credentials, easy to undo:

- **Peer credentials come from connections already made.**
  `ServerConn.SetPeerCredentials` installs a resolver `peerOptions` consults
  before the parent's settings — connecting to a replica once via File >
  Connect gives it its own login/port/auth/TLS. A saved connection is taken
  whole (so new `config.Connection` fields aren't dropped). Keyed by
  `db.InstanceKey` (case, port, instance spelling); since `@@SERVERNAME` is the
  short name, a saved connection is also registered under its short host at
  strictly lower priority, so `sql.a.example`/`sql.b.example` never swap logins.
- **The resolver never makes an instance less reachable.** `Peer` retries once
  with `parentPeerOptions` when the resolver's answer won't connect;
  `loadPeerCredentials` skips entries whose password can't be decrypted. A saved
  low-privilege login still wins on purpose. Removing the retry or ignoring the
  resolver each break a half.
- **`ServerConn.Peer` blanks `Opts.Database`** — a named database fails the
  connect at ping, and everything `Peer` reaches is server-scoped. The other
  three `sc.Opts` clones (query panel, Activity Monitor ×2) keep it.
- **The peer failure cache is short on purpose.** `recordPeerFailureLocked`
  keeps the last failure per `InstanceKey` for `peerFailureTTL` (30 s) so a
  blackholed primary doesn't cost the 15 s connect timeout per expansion.
  Invalidated by a successful direct File > Connect to it and by Refresh
  anywhere in the Always On subtree (both recurse into cached peers, where
  chained reads record failures). The 15 s timeout stays (WAN peers).

## Always Encrypted: what the two create dialogs leave out

- **Key material is pasted, never computed** — the CMK signature and CEK
  `ENCRYPTED_VALUE` come from a private key in a Windows store, CNG/CSP or Key
  Vault, unreachable from a portable no-CGO build. Both dialogs take the `0x…`
  SSMS or the `SqlColumnMasterKey`/`SqlColumnEncryptionKey` cmdlets print. A
  signature with "Allow enclave computations" unticked is *refused*, not
  dropped (the key would be the wrong one).
- **One encrypted value per create**; rotation's second value is Column
  Encryption Key Properties' `AddValue`/`DropValue`.
- **`RSA_OAEP` is a static row** — the only algorithm SQL Server accepts.

## Log File Viewer: what is deliberately out of it

- **No Windows event log** — needs WMI. The merged view (**Select Files...**)
  doesn't reopen this.
- **A merged selection can span both families; the selectors can't.** **Select
  Files...** lists SQL Server and Agent logs together; a mixed set labels files
  with their family ("Agent Current") since archive numbers aren't comparable.
  The family selector, file list and Recycle address one family — a mixed
  selection leaves them, a single-family one moves them. A cycle of either
  family re-anchors a mixed selection. Every read enumerates both families (no
  round trip behind a keypress); an unenumerable family is left out.
- **A cycle re-anchors a merged selection to the current log**, since
  renumbering would silently change a numbered set. A single-file view keeps
  its number ("Archive #1" is whatever is now #1).
- **Filter and "Search..." are different features — don't merge.** Filter
  narrows what was read, instantly ("N of M match"). Search edits
  `xp_readerrorlog` arguments 3-6 and changes what the server returns (for logs
  too big to read); the status line names it so "no entries" isn't read as an
  empty log. They compose. Live-only facts: date bounds go as **text**
  (`YYYY-MM-DD HH:MM:SS`; typed datetime is rejected), and the two strings are
  **AND**-ed.
- The busy latch is taken **before** the cycle confirmation is shown (F5 still
  reaches the panel while it's up, and would clear `busy` under the cycle); after
  a tree-started cycle an open viewer gets `Refresh`, never `Load` (`Load`
  re-enumerates only the family on screen, leaving the other's pre-cycle
  numbering).

## Query Store: what is deliberately out of it

- **Plan comparison is two grids, not two graphs** (a tile is 18 columns).
  Operators pair on physical operator + object without the index (an index
  change is one changed row; seek→scan stays two one-sided rows). Out: comparing
  against a saved `.sqlplan`, and plans of *different* queries (refused).
- **No "Configure" button** — settings are a Database Properties page (folder
  menu opens it), as in SSMS.
- **Plot History doesn't dim Top or the execution floor.** gosmo's
  `QueryStoreTrackedQuery` ignores `Top`, `MinExecCount`, `MinRegressionPct`,
  `QueryIDs`, but the report grid below still honours them. The chart title
  names the metric and statistic, the two that reach it.
- **Reads on demand only** — no auto-refresh; each read is one-shot and bounded.
- **One metric and statistic across all seven views.** Each report opens on its
  own default (Total for the two cost rankings, Avg otherwise), but switching
  keeps the user's choice (SSMS keeps per view).
- **Regressed Queries' baseline stays inside the reported window.** gosmo's
  default (the equal window before `From`) would need twice the history before
  showing a row.

## Object Explorer folder filter: what is deliberately out of it

- **Only seven families push down** — Tables, Views, Stored Procedures,
  Functions and their three System variants (gosmo `ObjectFilter`,
  `…FilteredContext`). The rest (sequences, synonyms, both trigger families,
  databases, logins, users, roles, schemas, partition functions/schemes, AE
  keys, security policies) stay client-side: small, and the clause builder is
  family-agnostic if that changes. Push-down rules: `docs/db-rules.md`.
- **No Owner or Durability Type filter on Tables** — each is a per-table
  `TableDetail` query, i.e. a folder-wide fetch before the first row. Do not
  re-raise.
- **Filters are per-session** (`App.savedFilters`, keyed by `filterKey`, survive
  reconnects). Not saved to `config.json`, as in SSMS.

## Delete/Rename: what is deliberately out of it

- **No partition or filegroup Delete from the tree** — neither has a node
  (partition functions/schemes do and are deletable; filegroups via Database
  Properties > Filegroups).
- **A trigger is never offered Move to Schema** — it moves with its table and
  `TRANSFER` refuses it. Same for indexes, statistics, keys, constraints.
- **Agent objects keep their own Delete** (`agent_menu.go`, per-type wording);
  only Rename is shared. Likewise AGs. **A system Agent job still offers
  `Delete Job...`** (SSMS does; msdb allows) though Rename is off every system
  object.
- **Neither audit object offers tree Rename.** A server audit specification has
  no `MODIFY NAME` (parse error). An audit renames only while disabled, so it
  lives on Properties, where the disable shows in Script Changes.
- **Multi-select delete is only in the Object Explorer Details pane**
  (`controls.TreeView` is single-select). Only schema-scoped objects delete as a
  set; a database, login, server role, user, database role or AE key deletes
  alone (`objectOp.solo` — a typed confirmation names one object, and principal
  drops reach further than one warning can say). The solo rule is about the
  selection: one login selected still deletes. Rename and Move to Schema stay in
  the tree. Views whose rows aren't objects offer nothing — correct.

## Clipboard in a dialog: what is deliberately out of it

- **A dialog with no text entry has an inert clipboard** (Help, About/Object
  Dependencies, Confirm, Query List, Background Tasks aren't
  `core.ClipboardHost`) rather than copying the panel behind it.
- **Key Diagnostics' `syncIfDirty` must not rebuild the editor while it has a
  selection** — it logs the very `Ctrl+A` used to copy, and the next `SetText`
  would drop the selection before `Ctrl+C`. Status History doesn't need this;
  "simplifying" would undo it.

## Server-level families: what is deliberately out of them

- **No viewing audit records** — `sys.fn_get_audit_file` is a Log-Viewer-sized
  feature; "View Audit Logs" isn't offered rather than offered empty.
- **No registering cryptographic providers** — `CREATE CRYPTOGRAPHIC PROVIDER`
  takes a server-side DLL path. The folder is read-only: no New, Delete or
  Script, no rights declared.
- **No creating TSQL, Service Broker or SOAP endpoints** — New Database
  Mirroring Endpoint is the only creation path.
- **Tape and virtual backup devices** are listed, scripted and dropped, not
  created (disk only, as SSMS).
- **Audit Properties is one page** (SSMS: General + Filter). `ALTER SERVER
  AUDIT` replaces everything, so a second page's apply would revert the first or
  need an unopened page's value.
- **An audit's destination is editable** (SSMS greys it): `ALTER SERVER AUDIT`
  accepts a new `TO` on a disabled audit, and apply already runs in a disable
  window. Cost, stated in the page note: leaving FILE discards the file block;
  a resumed FILE audit starts a new file. File rows are gated on the dropdown,
  and an empty FILE path is refused before disabling (Msg 33072 arrives only
  after).
- **Database audit specification actions aren't edited in place** — only ADD
  and DROP exist, so an edit would be drop + add that can fail between. The page
  unticks to drop and adds via its own four fields.
- **One specification per audit per database** (Msg 33230), so New and
  Properties list only free audits; Properties keeps its own.
- **All six filterable folders filter client-side.** Credentials, Audits,
  Server Audit Specifications, Server DDL Triggers: Name + Creation Date; Backup
  Devices and Endpoints: Name only (no creation date; a zero
  `nodeData.CreateDate` rejects every row). Cryptographic Providers: no filter.

## Database-scoped credentials: what the design settled

- **CONTROL on the database is the whole right set; `ALTER` is deliberately
  not beside it.** Probed with a `WITHOUT LOGIN` user: CREATE/ALTER/DROP work
  under `GRANT CONTROL ON DATABASE`, all refused under `GRANT ALTER`. Adding
  `rightAlterDatabase` "for symmetry" offers actions the server refuses. `ALTER
  ANY CREDENTIAL` is server-scope, and `HAS_PERMS_BY_NAME` on a database
  returns NULL for it ("unknown" forever). See `dbScopedCredentialRights`.
- **No ALTER script verb, no rename.** The secret is unreadable, so scripts
  carry `<insert secret here>` — an ALTER verb would silently overwrite the
  secret with the placeholder. No `WITH NAME` and no `sp_rename` class.
- **An identity change with a blank password is refused** — `ALTER` resets both
  halves and an omitted `SECRET` sets NULL. Same rule and wording as server
  Credential Properties.
- **A SAS credential bound to an active database file can't change identity**
  (Msg 33253). The binding isn't visible in `sys.database_scoped_credentials`,
  so the page just surfaces the server's message.

## Keys and certificates: what the design settled — do not re-raise

A database's Security folder holds Asymmetric Keys, Certificates, Symmetric
Keys (in that order, after Schemas), on every database including `master`.
Every result below was probed 2026-09-22 with `WITHOUT LOGIN` users on 13, 14,
17 and MI — identical on all four — and the UI driven on 17 and MI.

- **Keys are generated, never imported from a file.** `FROM FILE`, `EXECUTABLE
  FILE`, `ASSEMBLY` and `WITH PRIVATE KEY (FILE = …)` read the *server's*
  filesystem, which a client dialog can't browse. Import is a query window's
  job.
- **EKM (`FROM PROVIDER`) is offered only with an *enabled* cryptographic
  provider** (`providerKeyFields`, `key_actions.go`; a failed read counts as
  none). Never run against a real provider — follows documented grammar.
- **No Rename, no Move to Schema** — no `WITH NAME`, no `sp_rename` class, not
  schema-scoped.
- **The owner is the one General-page write**, gated on object `CONTROL`
  (effective, folding in database `CONTROL`). `ALTER AUTHORIZATION` drops
  explicit permissions (the note says so). `IMPERSONATE` on the new owner is
  left to the server (Msg 15151). Certificates and asymmetric keys can't be
  role-owned (Msg 15345), so only the symmetric key's picker lists roles. The
  current owner stays listed even if not a user/role (certificate-mapped user,
  application role).
- **Nothing else on a certificate or asymmetric key is a Properties row** —
  other ALTERs are private-key operations (filesystem or irreversible → own
  context-menu actions) or `ACTIVE FOR BEGIN_DIALOG` (left to a scripted ALTER).
- **Signatures page**: `ADD SIGNATURE` needs `CONTROL` on the signer + `ALTER`
  on the module; `DROP` only `ALTER` on the module. Gated on signer `CONTROL`
  or database-wide rights carrying module `ALTER`; the module half is the
  server's (Msg 15151). Counter signatures listed and removable, not addable.
- **Back Up Certificate is an ungated dialog.** The public cert backs up for
  anyone who sees it (`db_securityadmin` too); the private key needs `CONTROL`
  (Msg 15247), stated and server-enforced. Paths are server-side, written by the
  service account (Msg 15240 if it can't), never overwritten, readable only by
  that account. A password-protected key's password is checked before sending.
  No asymmetric-key backup (no such statement).
- **Remove Private Key is a confirmation**, gated on effective object `ALTER`
  (`gate.AlterOnCertificate`/`AlterOnAsymmetricKey`, per object; Msg 15151 to
  `db_securityadmin`), withheld with "no private key" when
  `nodeData.HasPrivateKey` is false. Only the certificate's warning mentions a
  prior backup.
- **Certificate Script as ▸ CREATE emits `FROM BINARY` on every version** (works
  on 13+, so no `WITH SUBJECT` fallback); the private key isn't scripted and the
  script says so. Asymmetric and symmetric key CREATE scripts make a *new* key
  and say so (`KEY_SOURCE`/`IDENTITY_VALUE` are the only way to recreate a
  symmetric key).
- **Expired certificates show `(Expired)`**, like `(Disabled)`. SSMS lacking it
  is a gap, not a decision.
- **The database master key is a node** — first child of Symmetric Keys, only
  when it exists and is visible (`MasterKey` nil otherwise). Properties: read-only
  General + Encryption (add/drop service-master-key and password encryptions);
  Regenerate and Back Up are dialogs. Every write needs database `CONTROL`
  (database `ALTER`, `ALTER ANY SYMMETRIC KEY` etc. are refused, Msg 15151
  "Cannot find the symmetric key 'master key'"); a key the SMK no longer
  encrypts must be opened by password first (Msg 15581) — page and dialogs ask.
  **No Delete** (Msg 15580 while anything depends on it; otherwise a query
  window's job). New Certificate/New Asymmetric Key create it on demand
  (`ensureMasterKey`); New Symmetric Key doesn't need it.
- **New Symmetric Key doesn't offer encryption by another symmetric key** (the
  parent must be open — the Encryption page's decryptor does that). Create with
  a password and add it there. `KEY_SOURCE` and `IDENTITY_VALUE` are required
  together.
- **The Encryption page's decryptor is a section, not a modal**; symmetric
  chains resolve only when every link opens without a password (password-only
  parents → query window). Removing a password encryption needs the password
  (found by value, Msg 15313). Remove disabled on the last encryption is a
  convenience; the server refuses anyway (Msg 15558).
- **Classify key encryptions by `crypt_type_desc` prefix, never `crypt_type`**
  — codes vary by major (password `ESKP` on 13, `ESP2` on 14/17; certificate
  `EPUC` on 13/14, `C256` on 17) and MI shows 13/14 codes while reporting 12.

**Rights, per verb** (identical for the three families):

| Holder | CREATE | DROP | Sees others' rows | Sym key ADD/DROP ENCRYPTION |
|---|---|---|---|---|
| `CREATE X` | yes | only its own | no | only its own |
| `ALTER ANY X`, `ALTER`/`CONTROL` on the database, `db_ddladmin` | yes | yes | yes | yes |
| `db_securityadmin` | no | no | yes | no |
| `ALTER` on the object | no | **no** | yes | yes |
| `CONTROL` on the object, or its owner | no | yes | yes | yes |
| `VIEW DEFINITION` on the object | no | no | yes | no |

- **Gate sets follow the table.** New: `CREATE X`, `ALTER ANY X`, database
  `ALTER`/`CONTROL`. Delete: `ALTER ANY X`, database `ALTER`/`CONTROL`, or
  object `CONTROL` from gosmo's per-securable probe (classes 25/26/24 — which
  offers Delete to a `CREATE X`-only owner). `dbScopedOpRights` in
  `explorer_object_rights.go`.
- **The symmetric key's Encryption page is gated on effective key `ALTER`
  alone** (`gate.AlterOnSymmetricKey`). Live on 13 and 17: works exactly when it
  reads 1 (including `ALTER`-without-`CONTROL`), refused (Msg 15151) for
  `CONTROL` with `ALTER` denied — the Delete set is wrong both ways.
- **The second securable is the server's call.** Encrypting by a certificate
  needs `VIEW DEFINITION`/`REFERENCES`/`ALTER` on it; dropping that or opening
  with it needs `CONTROL`. Pickers can't know; Msg 15151 comes back.
- **Passed through, not pre-checked**: Msg 15559 (dropping a cert/key a
  principal is mapped to), 15352 (dropping one that encrypts a symmetric key),
  15581 (master-key-protected key without a master key — New dialogs prevent
  it). No right on a key = no row, not an error.
- **`HasMasterKey` also reads `sys.databases.is_master_key_encrypted_by_server`**
  — a `CREATE X`-only principal can't see the master key in
  `sys.symmetric_keys` and was asked to create one that existed. One whose SMK
  encryption was dropped stays invisible to them (documented on the method).

## Database-scope DDL triggers: what the design settled

- **No flat database Triggers folder, deliberately.** DML triggers are listed
  under their table/view (as SSMS); a roll-up would list them twice beside
  "Database Triggers" (DDL, `parent_class = 0`). `Database.Triggers` is gosmo's
  database-wide read; gossms has no folder for it.
- **A view isn't a leaf** — it carries INSTEAD OF triggers, so `NodeView` has a
  Triggers folder. `Database.ObjectTriggers` serves both (gosmo's `View` has no
  database back-pointer for a method of its own).
- **The DDL family is read/enable/disable/script/drop — no New dialog**, like
  Server Triggers; a trigger body isn't form-buildable. Edit via Script Database
  Trigger as > ALTER To.
- **Its filter is client-side, Name + Creation Date**, like Server DDL
  Triggers.

## Connection settings: what the live run settled

Encrypt modes, Custom Properties, the Entra mapping, IPv6.
`internal/db/live_connect_test.go` is the repeatable part (passes on 13, 17.0,
MI).

- **Strict (TDS 8.0) verified on MI only** (validated cert, and with Trust
  Server Certificate). On win10cli every Strict attempt fails `TLS Handshake
  failed: EOF` — only SQL Server's self-signed fallback cert, which TDS 8.0
  refuses. Server config, not a defect; don't chase without a real cert.
- **Mandatory with Trust off fails on every on-prem instance here** (`x509:
  certificate is not valid for any names`) — why Trust defaults on beside
  Mandatory.
- **Host Name In Certificate verified on MI by IP**: fixes `doesn't contain any
  IP SANs`; the login is then refused by Azure's gateway (40532, routes on
  name) — Azure's, not TLS's.
- **`net_packet_size` reads 58 bytes high when encrypted** (4154 for 4096, 17.0
  and MI); 8192 is capped to 8000. The live test asks 16000 and allows 58.
- **Azure encrypts every session** (Optional reads `encrypt_option` TRUE); the
  test checks Optional only off Azure.
- **On MI a `packet size` below 4096 can't connect** (`invalid packet size, it
  is longer than buffer size`, Optional and Mandatory) — go-mssqldb sizes its
  handshake buffer from it. Driver limit via Custom Properties; 17.0 takes
  2048.
- **`ApplicationIntent=ReadOnly` verified on AAG1** with temporary routing:
  direct to the secondary, no intent is Msg 978 and ReadOnly connects; via
  listener `ubuaag`, ReadOnly routes to ubusql2 (READ_ONLY), default stays on
  ubusql1 — from the Connect dialog too. AAG1 normally has no routing list and
  `ALLOW_CONNECTIONS = ALL`, so it's unobservable without that setup.
- **Entra per-method mapping verified for every method** — to the driver by
  `TestEveryEntraMethodBuildsADriverConnector` (real DSN through
  `azuread.NewConnector`) and end to end by hand.
- **IPv6 literal + named instance needs an explicit port**
  (`fe80::1\INST,1500`); gosmo refuses without — the driver keeps the brackets
  in the Browser probe address.
- **Custom Properties separate on `;`, `&` and line breaks, not spaces**
  (`packet size=8192 database=foo` is one bad entry). Enter is Connect, so in
  practice `;` or `&`.
- **A downgrade loses saved passwords until the next upgrade.** Older releases
  read the current sealing prefix (`v4:` since Entra tenant/client binding;
  `v3:` before) as unprefixed and keep it sealed. Pre-v3 also drops
  `encrypt_mode` on save (Strict → Mandatory, a different AAD, so re-enter).
  The `encrypt` boolean is still written so old releases can read the file.

## Connect dialog: what the SSMS 21 redesign settled — do not re-raise

A History pane plus a two-tab form (Connection Properties / Connection
String). Not to be reopened without asking the author:

- **A password is stored only with Remember Password ticked (off for new
  connections).** `Config.AddOrUpdate` blanks `Password` when
  `!RememberPassword`, dropping existing ciphertext but keeping the entry (the
  History pane lists it). It takes the connection by value, so
  `App.rememberPeerCredentials` still works for live peer connects. An entry
  with a password pre-fills ticked, so pre-checkbox entries keep working.
- **`RememberPassword` isn't in `connectionAAD`** — toggling must not
  invalidate a sealed password (as `secret.go` says of `Database` and
  `ExtraProperties`).
- **The port folds into Server Name for display only; `config.Connection.Port`
  stays stored.** It's bound into `connectionAAD`, part of `GeneratedName` (the
  dedup key and completion-inventory key), and `db.ResolveServer` drops 0 and
  1433 so named instances resolve via Browser. `currentOptions` splits with
  `gosmo.ParseServerAddress`; `PreFill` re-joins with `db.ResolveServer` (what's
  shown is what's dialled). `TestConnectDialogFoldingThePortKeepsTheConnectionIdentity`
  guards the round trip. No hand-rolled string surgery.
- **The History pane replaced the autocomplete overlay; no filter box.**
  `config.Config.MatchByServer("")` remains the most-recent-first ordering the
  pane depends on. The dialog opens on the first row, but only into an empty
  form (fields persist across Show/Hide).
- **Below ~96 columns the History pane is dropped**, not clipped or scrolled;
  only the most recent connection pre-fills, others are typed. The author's
  call — no narrow-mode picker without asking.
- **Out by the author's decision**: Browse tab, Favorites, Copy/Paste/Apply/
  Reset under the connection string, Name/Color custom-property rows. The
  connection string stays a live masked preview. The "Server Type" line is
  dropped (one fixed value).

## By design — not issues, do not re-raise

- **A query window's session is SSMS-like, with three deliberate
  differences.** A panel closed *mid-run* isn't asked about its transaction —
  the run is cancelled and the session ended (rollback), since its
  `@@TRANCOUNT` predates the run. A lost session (killed SPID, network drop,
  failover, a plan capture whose `SET … OFF` failed) disconnects the panel
  rather than silently reconnecting on F5, since the new session lacks the temp
  tables and transaction. The commit-on-close loop is bounded by the starting
  `@@TRANCOUNT`, so a doomed transaction reports and leaves the window open.
- **Comment prose is not a cleanup target** — the long blocks `CLAUDE.md` §
  Coding conventions names, and failure-naming comments in
  `query_store_panel.go`, `prop_grid_helpers.go` and similar. Drift that does
  recur when *editing* comments: wrong caller after a helper moved, counts,
  claims about which types implement an interface, wrong-document
  cross-references, and inverted sentences that read fine either way.
- **Which databases a dropdown offers** is settled in
  `internal/tui/database_list.go`, by when the name is resolved: stored for
  later (job step, alert, login default database, restore history) → every
  database incl. system and non-ONLINE; acted on now → only what the action
  accepts. Backup is the only dialog in the second class (tempdb and OFFLINE
  both fail "BACKUP DATABASE is terminating abnormally"). Don't unify. Trap:
  Backup opened *on* a database swaps its dropdown asynchronously, so
  `setDatabaseItems` keeps a selection missing from the new list at the front —
  else Back Up on an OFFLINE database silently retargets. Any future narrowed
  dropdown a dialog opens on needs the same.
- **`indexOf` against a sentinel list** is right for fixed vocabularies
  (recovery model, page verify, Query Store state/capture mode, compatibility
  level — written in the page, written back as `items[row.Selected()]`) and for
  New-X defaults; wrong for server-supplied names against a separately read
  list (they vanish if dropped between reads or invisible) — use
  `preservedValue`/`changedTo`. `TestIndexOfSentinelListFallsBackToSentinel`.
- **A job with a dropped owner login can't be made by dropping the login** —
  SQL Server refuses to drop a job owner. A **schedule's** owner can be dropped
  (`SUSER_SNAME(owner_sid)` goes NULL). Orphaned job owners come only from a
  foreign msdb restore or a Windows principal removed from AD.
- **Start/Stop Job aren't greyed.** They read state first and refuse in the
  app's words ("Job X is already running"/"is not running"), refreshing the node
  — the read is free. Gating would hide a legit Stop for a job started since the
  folder loaded.
- **`sp_delete_jobstep` isn't symmetrical with `sp_add_jobstep`** — it resets
  references to steps at/after the deleted one to "quit with success", hence
  `ReorderSteps`' repair pass. A test moving the *last* step can't see it; the
  live test moves a middle one.
- **Merging the two user-mapping page builders is a non-goal** — the
  duplication that caused a bug was in `wireGridEditor`, now shared; merging the
  builders costs six injection points, two row structs, two applies.
- **`Form.Revert()` is exposed.** `Ctrl+Z` on a `PropertySheet` →
  `RevertPage` → `Form.Revert`, every row's `Revert`, all 25 `RevertFn`s.
  **`Ctrl+Z` is handled before the zone switch, beside `F5`** (works from page
  list and buttons), but `PropertySheet.HandleKey` gives the focused row first
  refusal via `focusedRowHandles`, so undo in a job step's T-SQL box doesn't
  discard the page. That refusal exists for `propsheet.EditorRow` (three sites:
  `prop_grid_helpers.go`'s `sqlBodyRow`, `type_props.go`'s XML schema Documents
  box, `agent_job_step_panel.go`'s Command box). Read-only editors reject
  `Ctrl+Z` (`controls.readOnlySafeKey`), so it falls to `RevertPage`.
  `widgets.InputField` takes `Ctrl+A`/`Ctrl+U`, not `Ctrl+Z`. A new
  `EditorRow` inherits this; a new row type with its own `Ctrl+Z` and no
  `KeyHandler` needs a different key.
- **The editor's redo stack is uncapped in bytes, and `applyStep`'s slice is
  unguarded, deliberately** (`maxUndoSteps`/`applyStep` in
  `internal/tuikit/controls/editor_undo.go`; `TestEditorRedoStackBound`).
  Redo is bounded by count (cleared on any edit); `maxUndoBytes` can't reach it
  because the inverse carries replaced lines (48.4 MB redo vs 46.5 MB undo,
  measured) — the undo cap bounds it to within one document, and a `redoBytes`
  cap would silently drop the deepest redo. `applyStep` slicing
  `[st.row : st.row+st.newLen]` unchecked is intended: a violated
  `pushUndoSpan` promise means corruption, and a clamp would quietly restore the
  wrong text. Don't add one.
- **gosmo untagged past its tag with `replace` active is the development
  state**, not a release blocker; tagging, bumping `require` and commenting out
  `replace` are release steps (ARCHITECTURE.md § Developing against a local
  gosmo checkout). A CI release build failing to resolve gosmo mid-development
  is expected.
- **A Grid/Text result can exhaust memory** — no Max Result Rows, no `maxRows`:
  results are retained in full, as SSMS ("you get what you asked for"). Storage
  is already compact (`internal/query/arena.go`; the floor is the 16-byte string
  header per cell of `ResultSet.Rows [][]string`). Results To File retains
  nothing. Don't add a cap.
- **The `Meta` (Output Column Metadata) block is grid-only** — it reads
  `Result.Sets`, which `ExecuteToSink` leaves empty (and `RowSink.BeginSet`
  lacks types); it takes effect on the *next* run. Don't thread `ColumnTypes`
  through the sink.
- **The DataGrid cell-viewer popup is unhighlighted.** Values bracketed by
  `<>`, `{}` or JSON-shaped `[]` open in their own panel with
  `XMLHighlighter`/`JSONHighlighter` (`internal/tui/cell_value.go`); the rest
  get the plain 60-column popup. In wrap mode each drawn column resolves via
  `styleAt` (`editor_draw.go`), a linear scan of the line's runs — fine for
  SQL's coarse runs, not per-token over a whole XML value. The panel draws
  unwrapped; with Word Wrap (Alt+Z) `drawWrapped` passes only runs overlapping
  each visual row (`runsInSpan`).
- **The Databases folder's per-database round trip is intended** —
  `FILEPROPERTY` reports only on the current database. One dynamic batch is
  faster (11-12 ms vs 20-21 ms over four databases) but one unreachable database
  fails every row, whereas the fan-out (8-wide, `ceil(N/8) × RTT`) degrades to
  `N/A` in one row. If ever slow, add the batch as a fast path with the fan-out
  as fallback — never as a replacement.
- **Restructuring `internal/tui` is closed**; no split candidates outstanding.
  Measured on a type-checked cross-file reference graph; the splits that paid
  (zero references back into `tui`) are done — `sqlparse`, `planview`,
  `dashboard`, `gate`. File length alone doesn't reopen it. Negative results:
  - `agent_*`/`database_props_*`/`new_*` families aren't a seam — they cut
    through the `App` dependency.
  - "Lines never mentioning `App`" isn't either: `grep -w App` misses
    `p.app`/`d.app`; the real figure is 48% (58 files, 12,618 lines), not 56%.
  - A `props` package isn't a five-method interface: 41 `propPage` files have
    110 outbound references to 44 symbols, ~6 of them `App` services, the rest
    helpers in 146-221-reference hubs. It'd need a fourth package.
  - "Testable without `App`" already holds (five such `*_test.go` files).
  - `planview`, the precedent, has no `Host` interface; both precedents are
    leaves.
- **The Block tab listing its own session is intended** — `sp_block` drops only
  idle *and* non-blocking sessions, so an unblocked server shows one row: the
  monitor. `and spr.spid <> @@spid` is **rejected — author's call**.
- **`sp_block`'s `cross apply sys.dm_exec_sql_text` and no `ecid = 0` filter
  are intended.** The proposed `outer apply` + `spid > 50 and ecid = 0` fixes
  weren't reproducible (sleeping blockers keep a handle; `DBCC FREEPROCCACHE`
  doesn't evict a live transaction's text), and `cross apply` keeps system
  sessions out. **Author's call: as is.**
- **Activity Monitor probes `VIEW SERVER STATE` once per collector** (twice per
  open) — Retry starts a new collector, and a cached answer would fail without
  asking. One extra round trip is cheaper.
- **`counterQueryFor`'s `RTRIM(instance_name) IN ('', '_Total')` drops no counter
  the panels read**, NULL notwithstanding: Windows has no NULL rows; Linux has
  five (`SQLPAL:Host/Guest Memory`), not in `counterNames`. All 33 names resolve
  on both. No `OR instance_name IS NULL` arm.
- **Both cache hit ratios are `cntr_value / base`, neither a delta** (live on
  win10cli 17.0.1135.8, 2026-09-18). `cntrFraction` in
  `internal/activity/counters.go` not using `prev` (unlike
  `cntrAverageBulk`) looks like an oversight and isn't:
  - **Buffer cache hit ratio is a window over recent lookups**, not cumulative —
    across `DBCC DROPCLEANBUFFERS` and a 1.5 GB scan the base read 104, 3728,
    33057, 29511, 87411, 21375, 126, 218. `cur/base` is already live; a delta
    read 100.09% on 29511 → 87411.
  - **Plan Cache's `_Total` Cache Hit Ratio sums five cache stores** (Bound
    Trees, Extended Stored Procedures, Object Plans, SQL Plans, Temporary
    Tables & Table Variables), each resetting when trimmed; churn steps the sum
    backwards (-1229 value vs -1717 base) and a delta read 104% and 672%. Not
    frozen either: 90.68% → 54.24% over one ad-hoc burst.

  "Buffer cache hit ratio is always 99.9%" is the engine's counting, not this
  arithmetic. `TestNeitherCacheHitRatioIsReadAsADelta` pins the readings. Don't
  re-propose a `prev` arm.
- **`appendValue`'s `case float32` is unreachable but kept** (go-mssqldb returns
  `float64` for `REAL` and `FLOAT`); correct if that changes.
- **Server-scope GRANT/DENY/REVOKE's `USE master;` prefix doesn't strand the
  pooled connection** (gosmo `permission_options.go`, via `server_security.go`).
  A live A/B showed eight pooled connections on the right database after a
  GRANT: `database/sql` calls `ResetSession`, which go-mssqldb implements as a
  TDS reset restoring the connection-string database. A pinned
  read-switch-restore would cost three round trips for nothing.
- **`mssql.ServerError` is fatal-only, so gosmo's `IsRetryable` treating it as
  retryable is right.** go-mssqldb v1.11.2 (checked 2026-09-23): its doc says
  fatal/severs the connection, and both rows loops routing token errors through
  `Conn.checkBadConn` still "Ignore non-fatal server errors". Cited by symbol —
  lines move.
- **`App.fanOut` (`safego.go`) is the one pool outside
  `safego`/`safegoRepair`**, taking label and recover by hand (Detail Browser
  backfill, Log File Viewer `readLogFiles`).
- **`charts.StackedHistoryChart.Draw` stays though unreached** — `doc.go`
  advertises `Draw` on all six chart types; the dashboard uses `DrawFrame` for
  the time row. `Plot`/`TimeRow` mirror `HistoryChart`'s pair.
- **`theme.SetPalette` and `widgets.SpinnerByName` stay in production files**
  though only tests reach them (`deadcode ./cmd/gossms`) — documented tuikit API.
  `config.UseTrackedQueries` likewise (another package's tests can't see a
  `config` `_test.go`).
- **Five `staticcheck` U1000 findings in `clipboard_host_test.go` and
  `dialog_gesture_test.go` are suppressed, not deleted** — the reflection walk
  reads them; they *are* the fixture. Each has `//lint:ignore U1000` naming the
  reader.
- **`rightAlterAnyLinkedSrv` and `rightCreateTable` read as unused**,
  deliberately (`internal/tui/gate/names_test.go`).
- **`scanPlanXML`'s per-set append isn't live-testable**: every showplan result
  set holds one row (seven batch shapes, both SET options,
  `TestLivePlanEveryShowplanSetHoldsOneRow`), so only
  `TestScanNextKeepsEveryShowplanRow` (scripted driver) kills an overwriting
  mutant.
- **`masterMappableNames` swallows its two reads' errors** — `sys.certificates`/
  `sys.asymmetric_keys` need master permission, and ordinary SQL/Windows logins
  must still be creatable; an empty picker becomes a refusal naming what's
  missing. Also settled live: `DEFAULT_DATABASE`/`DEFAULT_LANGUAGE` can't be set
  for certificate- or key-mapped logins (CREATE or ALTER), so the page refuses
  them up front.
- **Per-file Restore destinations (SSMS's "Restore As") aren't built** — the
  folder-level choice covers it. Rules: the backup set number comes only from
  `backupSetNumber` (`restore_dialog_ops.go`) for the restore, the MOVE clauses
  and Files Included — deriving it separately gave "Logical file 'x' is not part
  of database 'y'" on appended `.bak`s; and the relocation preview and MOVE
  clauses share `relocateFiles`.
- **Distribution credentials.** Homebrew: `HOMEBREW_TAP_DEPLOY_KEY` on
  `radix29/gossms`, a write deploy key on `radix29/homebrew-tap` (not a PAT).
  APT: `APT_REPO_DEPLOY_KEY` (write deploy key on `radix29/apt`, GitHub Pages at
  https://radix29.github.io/apt) and `APT_REPO_GPG_KEY` (RSA-4096, fingerprint
  `468B0CE5FFDEE82439741EC393F25CAB61497D93`; public half `gossms.asc` in the
  repo root). `.github/workflows/release.yml` is the reference.
- **A Launchpad PPA is rejected:** builders have no network, and Ubuntu's Go is
  1.22 (24.04) / 1.26 (26.04) vs `go.mod`'s 1.27. Measured: `go mod vendor`
  fully resolves `replace ../gosmo` (+23 MB, builds with `GOPROXY=off` and no
  sibling), and **neither repo needs Go 1.27** — `go1.26.0` builds both with the
  `go` directive lowered in both. 1.25 untested.
- **The two search dialogs' key-routing skeleton stays duplicated**
  (`FindReplaceDialog.HandleKey`, `LogSearchDialog.HandleKey`). Only the
  Tab/Backtab half is identical; Escape, `F3` and field dispatch differ. A
  twelve-line helper for two callers isn't worth it — extract for a third
  dialog or opportunistically. `log_search_dialog.go`'s `HandleMouse` comment
  ("the same shape as `FindReplaceDialog`'s") goes stale if either moves.
- **`fileutil.WithLock`'s stale-lock race is accepted.** Two waiters finding
  the same stale lock (older than `lockStale`, 10 s) can race; it needs a dead
  holder plus two saves within one poll, and costs only a lost edit, never
  corruption (writes stay atomic). Closing it needs flock/`LockFileEx`, a GOOS
  branch. Documented on `WithLock` (`internal/fileutil/lock.go`); 2026-09-24
  review U9.
- **The editor expands every tab to spaces** (`Editor.expandTabs`,
  `internal/tuikit/controls/editor_actions.go`, on `SetText`, `Paste`, block
  insert, Replace) — including in string literals, and Save writes spaces.
  2026-09-23 review S5, withdrawn: **author's call.** No tab-preserving buffer
  or tab-stop rendering.

## Release workflow

Both publishing jobs guard against going green having pushed nothing: the
`homebrew` job stages first and compares against the index (`git diff --quiet`
sees no diff for an untracked path), as `apt` did. Each ends in a **Verify**
step that fetches the *remote* and fails unless it carries this tag — the tap:
every archive URL in `origin/<branch>:Formula/gossms.rb` on
`releases/download/<tag>/`; apt: both `.deb`s in `pool/`, a `Version:` line in
each architecture's `Packages`, and `Release`, `InRelease`, `Release.gpg`.

- **Verify reads the remote with `git`, never HTTP** —
  `raw.githubusercontent.com` and the Pages site serve stale copies for minutes.
- **Verify is a separate step after the push** — the push step's guard `exit
  0`s when there's nothing to do, which would skip a folded-in verify in exactly
  the case it exists for.

The formula has **no `version` line** (Homebrew scans the URL; `brew audit
--strict` flags it), so Verify checks the four URLs' tag. The formula is
**binary**, not from source: the active `replace` breaks source builds from a
tarball and `go install …@<tag>`.

## Progress dialog: what is deliberately out of it

Every confirmed write from Object Explorer, the Details pane, Always On, Agent,
Log File Viewer, Query Store and Activity Monitor runs behind
`dialogs.ProgressDialog` (`App.runWithProgress`). Not:

- **Properties and New …** — keep their spinner and live Cancel on the sheet;
  closing the sheet would hide the pages and message line (the only account of
  a partial apply).
- **Back Up and Restore** — keep their percentage progress view; Restore's
  overwrite confirmation hands off to it.
- **Script** — runs nothing.

Also: **the unconfirmed half of a toggle** (Enable, Bring Online, Start
Endpoint, Join, Resume) runs behind it too, sharing the confirmed half's run
function; the 250 ms reveal delay hides fast ones. Likewise Agent writes SSMS
never confirms (Enable/Disable job, schedule, alert, operator; Start/Stop Job).
**Not driven live**: failover dialogs (need the cluster) and "Cancelling …"
(a cancelled DROP returned within a frame);
`TestProgressDialogCancelAsksOnceAndStaysOpen` covers its drawing.

## Details pane: the "Not connected" branch — settled, do not re-raise

`ShowNodeDetails`'s "Not connected" branch (`detail_browser.go`) is **defensive,
unreachable through the UI today, and kept**:

- Every node's connection is its root's (`explorer_loaders.go`'s `l.node` sets
  `conn: l.sc`); peers from `resolveAGView`/`alwayson_menu.go` are only read
  through, never carried by a node.
- `App.disconnect` is the only `ServerConn.Close` on a tree connection: close,
  purge (`PurgeConn` nils `currentNode`, calls `showEmpty`), then
  `RemoveRootByConn`. Other `Close` calls are on connections no node references.

Staged, the pane keeps the title, shows one `Status | Not connected` row, and
drops the previous node's rows, chart strip, pinned tooltip and menu verbs
(`TestShowNodeDetailsNotConnectedDropsThePreviousNode`). Not dead code.

## No top-level plan document — do not re-raise

Neither repo has, or should get, a top-level plan file. State and package map:
`ARCHITECTURE.md`; unfinished work: `docs/open-threads.md`; shipped per tag:
`CHANGELOG.md`; settled questions: here.
