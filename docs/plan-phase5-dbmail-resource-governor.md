# Plan — Phase 5, item 24: Database Mail and Resource Governor

Scope: the two **Management**-folder features SSMS has and goSSMS lacks —
**Resource Governor** (pools, workload groups, external pools, classifier) and
**Database Mail** (accounts, profiles, profile security, system parameters,
test mail, mail log). Estimate **4–5 weeks**, two independent halves:
Resource Governor ≈ 2 weeks, Database Mail ≈ 2.5 weeks. Neither has an
object model in gosmo today — **except** that `server_config.go` already has
`MailProfile`, `Server.MailProfiles` and `Server.SendMail` (found 2026-09-30,
after this was written; the grep below missed them). W9/W10 extend those
rather than add a second `MailProfile` — `grep -i 'sysmail\|resource_governor'` over
gosmo finds only the `Database Mail XPs` row in `server_config.go`'s callers
and the Azure-only `sys.dm_instance_resource_governance` reader in
`azure_resources.go`, which is a different thing (the *platform's* governor,
not the user-configurable one) and stays where it is.

What already exists and is reused, not rebuilt:

- `Management` folder (`explorer_management.go`, `loadManagementChildren`) —
  holds Extended Events and SQL Server Logs; its comment already names
  Database Mail as a missing SSMS sibling.
- Server Properties ▸ Advanced already edits `Database Mail XPs`
  (`server_props_advanced.go`, through gosmo `ApplyConfiguration`).
- The Log Viewer (`showLogViewerFor`, gosmo `ErrorLogType`) — the natural
  home for the Database Mail log.
- `gate.SQLAgentUser` / `gate.MsdbOwner` — the msdb-role-membership shape
  Database Mail's `DatabaseMailUserRole` gate copies.
- Agent operators and job/alert notification pages already carry the
  "mail delivery depends on Database Mail being configured" note
  (`new_job_pages.go`, `agent_job_props_alerts.go`); this work is what that
  note points at.

Out of scope: SQL Server Agent Properties ▸ Alert System (Agent's mail
profile — gossms has no Agent Properties dialog at all; `docs/open-threads.md`
N7), Resource Governor for Azure SQL Database (the platform governs; nothing is
configurable), and SSMS's step-by-step Database Mail *wizard* as such (replaced
by a Properties dialog, below). Deferred from this pass: pool affinity editing
(N6) and every Managed Instance probe and live run (V4) — § Decisions.

## The target tree

New nodes marked `+`.

```
<server>
  Management
      Extended Events
+     Resource Governor                (object node, expandable; Properties = RG config)
+       Resource Pools
+         <pool>                        (expandable)
+           Workload Groups
+             <group>                   (leaf)
+       External Resource Pools          (major ≥ 13, always — § Decisions D2)
+         <external pool>               (leaf)
+     Database Mail                     (leaf; Properties = the configuration dialog)
      SQL Server Logs
```

SSMS order under Management is Policy Management, Data Collection, Resource
Governor, Extended Events, Maintenance Plans, SQL Server Logs, Database Mail,
… — keep goSSMS's existing Extended Events / SQL Server Logs pair and insert
Resource Governor **before** Extended Events and Database Mail **after** SQL
Server Logs, which is SSMS's relative order for the four.

Workload groups nest under their pool, as SSMS does — not a flat folder.
`default` and `internal` pools and groups are listed and marked "(system)"
(ids 1 and 2 in both catalogs); `internal` is read-only everywhere.

**Database Mail is a leaf, not a folder of accounts and profiles.** SSMS has
no tree below it either — accounts and profiles are only reachable through the
wizard — and they are not independent objects: a profile is an ordered list
of accounts, and its security is a (principal, profile, default) triple. One
dialog with pages expresses that; three folders would need cross-node refresh
for every edit. Settled: § Decisions D1.

## Presence and edition gating (probe first — Stage 0)

Nothing below is assumed; each row is a probe to run before gosmo code.

| Question | Instances | Expected (unverified) |
|---|---|---|
| RG DDL on a non-Enterprise edition | win10cli\SQL2016, \SQL2017 (check `SERVERPROPERTY('Edition')` first), Linux ubusql1 | Enterprise/Developer only before 2025; **SQL Server 2025 Standard gained Resource Governor** — confirm on major 17 and record the major+edition rule |
| RG on Managed Instance | t-qmi-01 — **unavailable, not probed** (D5, open-threads V4) | catalog readable; `CREATE RESOURCE POOL` / `ALTER RESOURCE GOVERNOR` allowed — shipped shown and ungated, unverified |
| RG on Azure SQL Database | none in the estate | not designed for; folder hidden on EngineEdition 5 (`serverIsAzure` is too wide — MI is shown, D5) |
| Database Mail on Express | whichever instance is Express, if any | `sysmail_*` procs exist in msdb, but `DatabaseMail.exe` is not shipped — mail queues and never sends |
| Database Mail on Linux | ubusql1 | supported since 2019 CU; `sp_send_dbmail` path to verify end to end |
| Database Mail on MI | t-qmi-01 — **unavailable, not probed** (D5, V4) | supported; Agent notifications need the profile named `AzureManagedInstance_dbmail_profile` — surface as a Note, not a rule |
| `Database Mail XPs` = 0 | any | `sysmail_*` config procs still work; `sp_send_dbmail` and `sysmail_start_sp` refuse. The node shows state "Disabled" and offers to enable it |

Edition refusals go into `edition_gate.go` as entries, the same shape as its
existing live-refusal table; a pre-2025 non-Enterprise refusal for RG is a
*presence* gate (folder shows a single "Not supported on this edition" row,
the Stage B empty-state), not a greyed menu item.

## Rights (probe — Stage 0)

| Operation | Believed right | Probe with |
|---|---|---|
| Read RG catalog (`sys.resource_governor_*`) | VIEW ANY DEFINITION? catalog visibility rules | a login with nothing, then VIEW SERVER STATE, then VIEW ANY DEFINITION |
| Read RG runtime (`sys.dm_resource_governor_*`) | VIEW SERVER STATE (2022+: VIEW SERVER PERFORMANCE STATE) | same logins |
| Any RG DDL, incl. `ALTER RESOURCE GOVERNOR` | CONTROL SERVER | CONTROL SERVER alone; ALTER SETTINGS alone (expected **not** enough) |
| Configure Database Mail (`sysmail_add_*`, `_update_*`, `_delete_*`, `sysmail_configure_sp`) | sysadmin membership | CONTROL SERVER without sysadmin (expected refused: procs check `IS_SRVROLEMEMBER`) |
| Send test mail (`sp_send_dbmail`) | `DatabaseMailUserRole` in msdb, or sysadmin; plus access to the profile | a login mapped into msdb with the role and no profile grant |
| Read mail log / items (`sysmail_event_log`, `sysmail_allitems`) | sysadmin sees all; DatabaseMailUserRole sees own items | both |
| Start/stop (`sysmail_start_sp`/`_stop_sp`) | sysadmin, or msdb db_owner? | both |

If sysadmin-only is confirmed for Database Mail configuration, the gate is a
**server role** (`gate.Right{Name: "sysadmin", ServerRole: true}`, the
`DiskAdmin` shape) — **not** `ControlServer`: CONTROL SERVER without sysadmin
would see an editable page that every Apply refuses. That distinction is the
reason this probe runs before Stage D; `docs/db-rules.md` § permission gating
applies in full. `DatabaseMailUserRole` becomes
`gate.Right{Name: "DatabaseMailUserRole", Membership: true, InDB: "msdb"}`,
beside `SQLAgentUser`; add the role to gosmo's probed membership list or the
gate reads `CapabilityUnknown` forever.

## W1 results — Resource Governor probes (run 2026-09-30)

Instances: win10cli\SQL2016 (13.0.6500.1), win10cli\SQL2017 (14.0.2130.4),
win10cli (17.0.1135.8, Windows), ubusql1 (17.0.4065.4, Linux). All four were
found RG-disabled, nothing pending, no classifier, and were left that way.

**Edition gate — unprobeable here.** Every instance is Developer (EngineEdition
3), so no instance refuses RG DDL. The rule ships from documentation:
Enterprise/Developer on majors 13–16, plus Standard on 17 (2025). Pinned by
fake-driver tests only. It is recorded in `docs/open-threads.md` in W15 unless a
non-Enterprise instance shows up first.

**Columns by major.**

- 13 and 14 have identical catalogs and DMVs for configuration, pools, groups
  and external pools.
- 17 adds `request_max_memory_grant_percent_numeric`,
  `group_max_tempdb_data_percent` and `group_max_tempdb_data_mb` to
  `sys.resource_governor_workload_groups`. The DMV
  `dm_resource_governor_workload_groups` also gains `cache/compile/used_memory_kb`,
  `cap_cpu_percent`, `tempdb_data_space_kb` and more. Gate on the column, per the
  plan. 15/16 are unavailable, so the column test is what keeps them right.
- The tempdb columns are NULL when unset. The `_numeric` column is `25.0` beside
  the int `25`.
- `max_outstanding_io_per_volume`, `min/max_iops_per_volume` and `cap_cpu_percent`
  already exist on 13, which is the floor. They need no gate, so drop "(≥ 12)" in
  the implementation.
- External pools list only `default` (id 2); there is **no `internal`** external
  pool. Both system workload groups point at external pool 2.
- Linux (ubusql1) matches Windows 17: pool and group DDL with
  `GROUP_MAX_TEMPDB_DATA_MB` works.

**Rights** (same on 13 and 17; tested with `EXECUTE AS LOGIN` using a disposable
login per right):

| Login holds | Catalog views | `dm_resource_governor_*` | CREATE/DROP POOL, RESET STATISTICS |
|---|---|---|---|
| nothing | **0 rows, no error** | Msg 300 | Msg 262 / 10901 |
| VIEW SERVER STATE | **0 rows, no error** | ✓ | refused |
| VIEW SERVER PERFORMANCE STATE (17) | 0 rows | ✓ | refused |
| VIEW ANY DEFINITION | ✓ | Msg 300 | refused |
| ALTER SETTINGS | 0 rows | Msg 300 | refused (as expected) |
| CONTROL SERVER | ✓ | ✓ | ✓ |

- Catalog visibility is metadata visibility, so a login without VIEW ANY
  DEFINITION gets an **empty** list silently. `internal` and `default` always
  exist, so gosmo can tell: zero pool rows means "not visible", never "no pools".
  The tree must say so rather than render an empty folder.
- The live grid needs VIEW SERVER STATE. On 17, VIEW SERVER PERFORMANCE STATE is
  enough too.
- Every write needs `gate.ControlServer`. `HAS_PERMS_BY_NAME(NULL,NULL,'CONTROL
  SERVER')` answers correctly.

**Transactions and the pending flag.**

- Pool and group DDL **is transactional**: CREATE inside `BEGIN TRAN … ROLLBACK`
  leaves no row. `ALTER RESOURCE GOVERNOR RECONFIGURE` is **not** allowed in a
  user transaction (Msg 574). So Apply runs the DDL in one transaction, commits,
  then RECONFIGUREs outside it.
- Any metadata change sets `is_reconfiguration_pending = 1`, even with RG
  disabled, and create-then-drop still leaves it at 1.
- `ALTER RESOURCE GOVERNOR DISABLE` clears the flag **without applying**: a pool
  created while disabled is absent from the DMV after DISABLE. The next
  RECONFIGURE applies it.
- `RECONFIGURE` on a disabled governor **enables** it. So gossms's "always
  reconfigure on Apply" silently enables RG. The Properties Apply must re-issue
  DISABLE when the General page's Enabled box is off. The live-test teardown
  restores the state with DISABLE (verified to return 0/0).
- The node label shows "(Reconfiguration pending)" only when enabled. While
  disabled, the flag means only "changed since the last DISABLE", and the label
  stays "(Disabled)".

## W2 results — gosmo reads (2026-09-30)

Shipped in gosmo `resource_governor.go`; the Stage A1 table below is the
design, this is what it became.

- **Stored vs in force are separate calls**, the `ServerAudit.Status` split:
  `Server.ResourceGovernor(ctx)` (catalog: enabled, classifier id +
  schema/name resolved in master, stored max outstanding I/O) and
  `Server.ResourceGovernorStatus(ctx)` (DMV: `IsReconfigurationPending`,
  effective classifier and I/O). Live pool/group counters are
  `Server.ResourcePoolStats(ctx)` / `Server.WorkloadGroupStats(ctx)`, one
  server-wide round trip each, keyed by pool/group id. The catalog reads work
  without VIEW SERVER STATE; the four DMV reads fail without it — the Detail
  Browser's "degrade to blank" is the caller ignoring that error.
- **Not visible**: `ResourceGovernor` returns `ErrNotFound`; the pool and
  external-pool listings return empty (live-tested with a no-rights login).
  `WorkloadGroups` likewise — callers treat empty as "not visible".
- Finders: `ResourcePoolByName`, `WorkloadGroupByName` (names are unique
  server-wide), `ExternalResourcePoolByName`; `ResourcePool.WorkloadGroups`.
  `IsSystem()` on all three (id ≤ 2). No `Ref` handles yet — they land with
  the writes in W3.
- Affinity is read on both pool kinds (`Affinity` slices, empty = AUTO) from
  the `*_affinity` catalog views, grouped in Go.
- Gates: `request_max_memory_grant_percent_numeric` at 2019 (documented; 15/16
  unverifiable, falls back to the int column cast to float),
  `group_max_tempdb_data_percent`/`_mb` at 2025 (`*float64`, nil = unset).
  Inventory, golden file and arity test added. `max_outstanding_io_per_volume`
  and the IOPS columns are ungated (all on 13).
- Verified: `TestLiveResourceGovernorReads` (disposable pool with affinity,
  group, external pool; DISABLE teardown, governor left 0/0) and
  `TestLiveResourceGovernorInvisibleToALoginWithoutRights` pass on 13, 14,
  17; `TestLiveGatedColumnsMatchTheCatalog` agrees on all three;
  `TestLiveVersionSweep` calls all eleven RG reads clean on all three. Its one
  failure, `Database.EventSessions`, is pre-existing and unrelated (the view is
  Azure-only; it fails Msg 208 on-prem) — recorded in gosmo's open threads.
- Linux (ubusql1) and MI not run for W2.

## W3 results — gosmo writes + scripter (2026-09-30)

Shipped in gosmo `resource_governor_write.go` and
`scripter_resource_governor.go`.

- **API.** `CreateResourcePool` / `CreateWorkloadGroup` /
  `CreateExternalResourcePool(ctx, req)`; `.Alter(ctx, …Options)` and
  `.Drop(ctx)` on each; `ResourcePoolRef` / `WorkloadGroupRef` /
  `ExternalResourcePoolRef` / `ResourceGovernorRef()` handles. The singleton
  has `SetClassifier(schema, name)` (empty name = NULL),
  `SetMaxOutstandingIOPerVolume(n)` (0 = DEFAULT), `Reconfigure`, `Enable`
  (the same statement), `Disable`, `ResetStatistics`. The *Options structs use
  pointer fields (nil = leave out). Group tempdb limits clear through
  `ClearGroupMaxTempdbData{Percent,MB}`, which sends NULL. The workload
  group's `Pool` / `ExternalPool` is the USING clause, and on Alter it moves
  the group. `ClassifierFunctionCandidates(ctx)` is the picker's list for W5:
  schema-bound, parameterless `FN`s in master that return sysname.
- **No write reconfigures.** The caller (the W5 Apply) runs the DDL, then
  `Reconfigure`, then `Disable` when Enabled is off.
- **Probed on 17 and 13:**
  - `ALTER RESOURCE GOVERNOR WITH` takes **one option per statement**
    (Msg 102 for two).
  - `MAX_OUTSTANDING_IO_PER_VOLUME = 0` is Msg 1040. `DEFAULT` resets it.
  - The classifier's two-part name resolves in master from any database.
  - ALTER with no WITH, or with `WITH ()`, is a syntax error, so an empty Alter
    issues nothing.
  - None of the three DROPs has IF EXISTS.
  - A fractional grant % is a syntax error on 13. gosmo refuses it below 2019,
    and refuses tempdb options below 2025, with `ErrUnsupportedVersion`.
  - Dropping an external pool a group uses is Msg 10916, the same as for a
    pool.
  - `DROP FUNCTION` takes no database prefix (Msg 166).
- **Scripts** write only non-default options.
  - A per-object script ends in a comment, not RECONFIGURE, because
    RECONFIGURE enables the governor.
  - `ScriptResourceGovernor` emits the classifier, the I/O setting, then
    RECONFIGURE or DISABLE.
  - Built-in objects script as ALTER of their non-default options. Their DROP
    returns `ErrUnsupported`.
  - Affinity outside processor group 0 returns `ErrUnsupported` (gosmo
    OPEN-THREADS).
- **Verified:** `TestLiveResourceGovernorWrites` covers every write, the
  server refusals, and the scripts run back, recreating identical rows. It
  passes on 13, 14, 17 and ubusql1 (Linux 17). All four instances were left at
  0/0/0/not pending with only the built-in objects.
  - The version sweep calls `ClassifierFunctionCandidates` clean on 13, 14 and
    17. It stays at its one pre-existing failure (`Database.EventSessions`).
  - Found on the way: the sweep's reflective half called the new
    `ResourcePool.Drop` on internal/default. The server refused both. It is now
    in `sweepSkip`.
- gosmo is still **uncommitted** (W2 + W3 together).

## W4 results — RG tree + Detail Browser (2026-09-30)

Shipped in `explorer_resource_governor.go` and
`detail_browser_resource_governor.go`; the edition rule is
`resourceGovernorSupported` / `resourceGovernorHidden` in `edition_gate.go`.
`tree_node.go` passed 900 lines and its glyph tables moved to
`tree_node_icons.go`.

- **State label is read by the Management loader**, not by an async
  follow-up like the Agent's: Management already loads off the UI goroutine,
  and a failed read leaves the label bare. So a state change shows after a
  Refresh of **Management**, not of the Resource Governor node. W6's
  Enable/Disable/Reconfigure must reload Management (the node's parent).
  `IsEnabled` on the node carries the governor's enabled flag for W6.
- **Detail split, a deviation from Stage C1's wording**: the Resource Governor
  node shows the configuration, stored beside in force (enabled, classifier,
  I/O per volume, pending, statistics start); the pools grid with live
  counters is the **Resource Pools** folder's view. One grid cannot hold
  both shapes.
- Live columns: Active/Queued are summed from the group DMV (the pool DMV
  counts memory grants, not requests); Grant waits, CPU ms, Used KB from the
  pool DMV. Blank without VIEW SERVER STATE, and blank for a pool or group
  created but not yet applied. External pools have no live columns (gosmo
  reads no external-pool DMV).
- Each half of the RG node's view degrades alone: VIEW SERVER STATE without
  VIEW ANY DEFINITION shows the in-force rows; pending while disabled reads
  "Yes — applied when enabled".
- Not visible (empty catalog) is a single ⚠ row in each folder, and an
  error in the folder's Details view. Unsupported edition: the node is still
  listed and expands to "Resource Governor is not supported on this edition".
- Name filter on Resource Pools, Workload Groups and External Resource Pools;
  `filterKey` gained `pool` so one pool's group filter doesn't restore onto
  another's.
- Menus are the default New Query + Refresh until W6; the Details pane's
  Delete is withheld (no `objs`) until W6 wires drop.
- **Verified live** on win10cli (17, Developer) under the tmux harness:
  tree, all seven views, 12.5 % grant, tempdb rows on 2025, "(Disabled)" →
  bare → "(Reconfiguration pending)" through RECONFIGURE and an ALTER, and a
  VIEW SERVER STATE-only login (not-visible rows, in-force half shown).
  Fixtures dropped and the governor left disabled, no classifier, not
  pending. Not run on 13/14 or Linux (the reads are gosmo's, swept there in
  W2); the Standard/Express gate is fake-driver only (W1).

## Part 1 — Resource Governor

### Stage A1 — gosmo reads and writes (≈5 days, critical path)

New file `resource_governor.go`, hanging off `*Server`:

| Type | Catalog / DMV | Notes |
|---|---|---|
| `ResourceGovernor` (singleton) | `sys.resource_governor_configuration` + `sys.dm_resource_governor_configuration` | `IsEnabled`, `ClassifierFunction` (schema-qualified name in master, resolved from `classifier_function_id`), `MaxOutstandingIOPerVolume`, `IsReconfigurationPending` (DMV) |
| `ResourcePool` | `sys.resource_governor_resource_pools` (+ `sys.dm_resource_governor_resource_pools` for live stats) | min/max CPU %, cap CPU %, min/max memory %, min/max IOPS per volume, affinity (`sys.dm_resource_governor_resource_pool_affinity` — SCHEDULER vs NUMANODE; read, script, **not edited** this pass) |
| `WorkloadGroup` | `sys.resource_governor_workload_groups` (+ DMV) | importance, request max memory grant % (and the `_numeric` column where present — probe the major), request max CPU time sec, memory grant timeout sec, MAXDOP, group max requests, pool, external pool (≥ 13); **2025: `GROUP_MAX_TEMPDB_DATA_MB` / `_PERCENT`** — version-gate on the column, not the major, and probe on win10cli (major 17) |
| `ExternalResourcePool` | `sys.resource_governor_external_resource_pools` | major ≥ 13; max CPU %, max memory %, max processes, affinity |

API, following the existing family shape (`…Context` list, `…ByName` finder,
`…Ref` handle only where a lookup-free handle is actually needed — see
`~/go/gosmo/CLAUDE.md` § Conventions):

- `Server.ResourceGovernor(ctx)`, `ResourcePools(ctx)`, `ResourcePoolByName`,
  `WorkloadGroups(ctx)` / `ResourcePool.WorkloadGroups(ctx)`,
  `ExternalResourcePools(ctx)`.
- Writes: `CreateResourcePool`, `ResourcePool.Alter`, `ResourcePool.Drop`,
  same for groups and external pools; `ResourceGovernor.SetClassifier`,
  `Enable`, `Disable`, `Reconfigure`, `ResetStatistics`.
- **Every write leaves the change pending until `ALTER RESOURCE GOVERNOR
  RECONFIGURE`.** Do not auto-reconfigure inside the write methods — the
  caller decides (a batch of pool+group edits reconfigures once). gossms
  always reconfigures on Apply; the Script button shows the RECONFIGURE line
  so the script is faithful.
- Traps to pin in tests: `internal` pool/group reject every ALTER; `default`
  pool/group accept ALTER but not DROP; dropping a pool that still has groups
  fails; the classifier must be schema-bound, in master, and **cannot be
  dropped while it is the classifier** (need `WITH (CLASSIFIER_FUNCTION =
  NULL)` + RECONFIGURE first); `MIN` > `MAX` combinations are refused by the
  server — let the message through, don't pre-validate beyond the obvious.

Scripter: extend `scripter_server.go` (or a new `scripter_resource_governor.go`
if it passes ~300 lines) — `CREATE RESOURCE POOL` / `WORKLOAD GROUP` /
`EXTERNAL RESOURCE POOL`, DROP, and `ALTER RESOURCE GOVERNOR WITH
(CLASSIFIER_FUNCTION = …)` for the singleton. Only non-default options are
emitted. Add verbs to `scripter_verb_test.go`.

Live tests: `live_resource_governor_test.go` — create a disposable pool
`gossms_test_rg_pool` + group, a schema-bound classifier in master, set it,
reconfigure, read back, script, drop in reverse order, **restore the original
classifier and enabled state** (the instance is shared; the teardown must
leave RG exactly as found, read at test start). Extend
`live_versionsweep_test.go` with every read on majors 13, 14, 17 (MI deferred:
open-threads V4).

### Stage B1 — tree (≈2 days)

1. `tree_node.go`: `NodeResourceGovernor`, `NodeResourcePools`,
   `NodeResourcePool`, `NodeWorkloadGroups`, `NodeWorkloadGroup`,
   `NodeExternalResourcePools`, `NodeExternalResourcePool` — icon (both
   styles), name, `hasChildren`.
2. `tree_node_wiring_test.go` — one entry each; it fails by construction
   until done.
3. New `explorer_resource_governor.go` for the loaders + menus;
   `loadManagementChildren` gains the node (skip on Azure SQL Database, keep
   on MI, unverified — D5). **New `internal/tui` file ⇒ `ARCHITECTURE.md`
   § Package map row in the same commit.**
4. The Resource Governor node's label carries its state, the way the Agent
   node carries "(Stopped)": `Resource Governor (Disabled)` /
   `(Reconfiguration pending)`.
5. `explorer_filter.go`: name filter on the pool and group folders.

### Stage C1 — Detail Browser + Properties (≈4 days)

- Detail Browser, new `detail_browser_resource_governor.go`: the RG node
  shows configuration + a pools grid with **live** columns from the DMVs
  (active requests, CPU usage ms, used memory KB, queued requests) — the one
  place a DBA answers "which pool is starved". Pool and group leaves show the
  same row set as their Properties.
- **Resource Governor Properties** — one dialog, SSMS's shape, but paged:
  - *General*: Enabled, Classifier function (dropdown of schema-bound
    scalar functions in master returning `sysname`; list read by gosmo),
    plus **New classifier…** (D4): opens a query window on master with a
    schema-bound `CREATE FUNCTION … RETURNS sysname WITH SCHEMABINDING`
    template; the dialog stays a picker (Refresh re-reads the list),
    Max outstanding I/O per volume (≥ 12), a Note when reconfiguration is
    pending.
  - *Resource Pools*: a grid (`propsheet` grid row) of pools with editable
    percent columns; `internal` row read-only; add/remove rows.
  - *Workload Groups*: a pool dropdown at the top, grid of that pool's
    groups below (D6) — no selection state carried across pages.
  - *External Resource Pools* (≥ 13, always — D2).
  - Pool affinity is shown read-only and scripted, not edited (D3).
  - Apply emits one batch: all DDL, then one `ALTER RESOURCE GOVERNOR
    RECONFIGURE`. Pages share one pending model; use the latest-only load
    (`ARCHITECTURE.md` § Latest-only loads) for the page reads.
- **Pool Properties / Group Properties** from the leaf nodes open the same
  dialog preselected on that row — not separate dialogs; one editor per
  concept.
- Labels ≤ 30 columns (`propsheet.LabelWidth`); already long at authoring
  time: "Request max memory grant percent" (32), "Request memory grant timeout
  (sec)" (35), "Maximum outstanding I/O per volume" (34). Shorten.

### Stage D1 — gating, ops, scripting (≈2 days)

- `gate.ControlServer` on every write verb and on the Properties pages (not in
  `pagesThatOnlyRead`); the Detail Browser's live columns degrade to blank,
  not an error, without VIEW SERVER STATE.
- Menus: New Resource Pool…, New Workload Group… (on a pool), Enable / Disable,
  **Reconfigure** (only enabled when pending), Reset Statistics, Script as ▸
  CREATE/DROP (pool, group, external pool), Delete (not on system rows).
- `scriptables` entries per leaf; `explorer_object_ops.go` `drop` for pool /
  group / external pool with the "has groups" refusal passed through; **no
  rename** (none exists in T-SQL).

## Part 2 — Database Mail

### Stage A2 — gosmo (≈6 days, critical path)

New file `database_mail.go`, hanging off `*Server`. All state is in msdb and
all writes are `msdb.dbo.sysmail_*` procedures (like `agent_operator.go`);
there is no DDL.

| Type | Source | Notes |
|---|---|---|
| `MailAccount` | `sysmail_account` + `sysmail_server` (+ `sysmail_servertype`) | name, description, email, display name, reply-to, server, port, SSL, auth (anonymous / basic / Windows `use_default_credentials`), user name. **Password is never readable** (stored as a credential) — the type has no password field on read |
| `MailProfile` | `sysmail_profile` + `sysmail_profileaccount` (sequence) | ordered account list |
| `MailPrincipalProfile` | `sysmail_principalprofile` | (principal, profile, is_default); `principal_sid = 0x00` is **public** |
| `MailConfiguration` | `sysmail_configuration` (via `sysmail_help_configure_sp` or direct) | `AccountRetryAttempts`, `AccountRetryDelay`, `DatabaseMailExeMinimumLifeTime`, `DefaultAttachmentEncoding`, `LoggingLevel`, `MaxFileSize`, `ProhibitedExtensions` |
| `MailStatus` | `sysmail_help_status_sp`, `sysmail_help_queue_sp` | STARTED/STOPPED, queue lengths |
| `MailItem` / `MailEvent` | `sysmail_allitems`, `sysmail_event_log` | log reads, bounded (TOP n, newest first) |

Writes: `CreateMailAccount`, `MailAccount.Alter`, `.Drop`; `CreateMailProfile`,
`.Alter`, `.Drop`, `.SetAccounts(ordered)`; `GrantProfile(principal, default)`,
`RevokeProfile`; `SetMailConfiguration`; `StartDatabaseMail`,
`StopDatabaseMail`; `SendTestMail(profile, to, subject, body)` (returns the
`mailitem_id` so the caller can poll its status); `DeleteMailLog(before)`,
`DeleteMailItems(before, status)`.

Traps:

- **Password on update.** Probe `sysmail_update_account_sp` with `@password`
  omitted vs NULL vs empty on 13 and 17 — the whole "leave blank to keep"
  behavior of the Properties page rests on the answer. Whatever it is, the
  password travels through gossms's secret handling (`secret.go`) and
  **never appears in a generated script** — Script emits
  `@password = N'<password>'` as a placeholder, as SSMS does.
- **`sysmail_update_account_sp` requires every parameter** in some majors
  (believed: it overwrites unspecified columns with NULL) — so `Alter` must
  read-modify-write the full row, not send deltas. Probe.
- Profile ↔ account sequence numbers must stay contiguous — reordering is
  delete + re-add of the profileaccount rows, in one transaction.
- `sysmail_add_principalprofile_sp` takes `@principal_name = 'public'` for
  public; a *login* or *msdb user*? — it is an **msdb database user** name.
  The principal picker lists msdb users (and `public`), not server logins.
- The `DatabaseMailUserRole` role members must also exist as msdb users for
  a grant to mean anything; show a Note, don't auto-create users.
- Anything with `sysmail_*` requires **msdb access** — gosmo reads fail on a
  login with no msdb user; treat as "not visible", not as an error, as the
  Agent reads do.

Live test `live_database_mail_test.go`: disposable account `gossms_test_mail`
pointing at a non-routable SMTP host, disposable profile, grant to public,
send a test mail (expect it to reach `sysmail_allitems` with `failed` status
and a `sysmail_event_log` row — that is the observable, since there is no SMTP
server in the estate), read everything back, script, delete in reverse order,
delete the test's mail items and log rows, restore configuration parameters
changed. If `Database Mail XPs` was 0 at start, enable and restore it.

### Stage B2 — tree (≈1 day)

`NodeDatabaseMail` (leaf) in `loadManagementChildren` after SQL Server Logs;
label shows state: `Database Mail (Stopped)` / `(Disabled)` when
`Database Mail XPs` = 0. Wiring-test entry; icon; name. Loader lives in
`explorer_management.go` (no new file — it is one node).

### Stage C2 — Detail Browser + Configuration dialog (≈6 days)

- Detail Browser: status, XPs flag, queue lengths, profiles grid (name,
  accounts, default-for-public), accounts grid (name, email, server:port,
  SSL, auth), and the last 20 failed items.
- **Database Mail Properties** (menu "Configure Database Mail…", also the
  node's Properties), pages:
  1. *General*: Status (read-only), Enable `Database Mail XPs` (checkbox;
     writes via gosmo `ApplyConfiguration`, sharing the path Server
     Properties ▸ Advanced uses), Start/Stop buttons.
  2. *Accounts*: list on the left, form on the right (SMTP server, port,
     SSL, auth radio: Windows / Basic / Anonymous, user, **password as a
     secret field, blank = unchanged**). New / Delete.
  3. *Profiles*: list + ordered account list (move up/down), New / Delete.
  4. *Profile Security*: two tabs as SSMS has them — Public profiles (toggle
     + default) and Private profiles (principal dropdown from msdb users,
     toggle + default per profile).
  5. *System Parameters*: the seven `sysmail_configuration` values;
     `LoggingLevel` as a dropdown Normal/Extended/Verbose (1/2/3).
  Apply runs the page deltas in dependency order: accounts → profiles →
  profile-accounts → principal-profiles → parameters. Script button emits
  the same, with the password placeholder.
- **Send Test E-Mail…** dialog: profile dropdown, To, Subject/Body
  prefilled like SSMS ("Database Mail Test" / host + time). After sending,
  poll the item's `sent_status` via the async path (`ARCHITECTURE.md`
  § Async result delivery: postAndWake) for up to ~30 s and show
  sent / failed / still unsent, with the `sysmail_event_log` description on
  failure — this is the actual diagnostic users want.
- **View Database Mail Log**: a new gosmo `ErrorLogType`
  (`ErrorLogDatabaseMail`) read from `sysmail_event_log`, so the existing Log
  Viewer shows it unchanged (filter, search, details pane). Cycling does not
  apply — the log is purged by `sysmail_delete_log_sp`; offer "Delete log
  older than…" instead of Recycle. Check the viewer's recycle-menu wiring
  treats the new type as non-cyclable (`log_viewer_recycle_test.go`).
- Labels ≤ 30 columns. Long at authoring time: "Database Mail executable
  minimum lifetime (seconds)" — use "Exe minimum lifetime (sec)".

### Stage D2 — gating, menus (≈2 days)

- Configuration pages and Start/Stop: sysadmin **role** gate (see § Rights),
  pending the probe. A non-sysadmin gets the dialog read-only where the reads
  succeed, and a Note naming the role.
- Send Test E-Mail: `DatabaseMailUserRole` membership or sysadmin.
- Enable `Database Mail XPs`: `ALTER SETTINGS` (same as Server Properties).
- Node menu: Configure Database Mail…, Send Test E-Mail…, View Database Mail
  Log, Start / Stop, Script Configuration (whole config as one script —
  SSMS has no equivalent; cheap once the scripter exists), Refresh.

## Stage E — verification (interleaved, not last)

`docs/testing.md` first. Green `go test ./...` is not verification.

- gosmo: both live tests plus the version sweep on majors 13, 14, 17 (MI
  deferred — V4),
  and ubusql1 for Linux Database Mail; confirm each new read was *called*.
- gossms under the tmux harness (memory: TUI tmux testing): expand Resource
  Governor on 17 (Developer — everything) and on the lowest edition in the
  estate (the presence gate); create a pool + group + classifier through the
  dialog, watch the pending label, Reconfigure, verify with
  `sys.dm_resource_governor_workload_groups` from a query window, then
  revert and drop. Database Mail: configure an account/profile from scratch
  on an instance with XPs off, send a test mail, see it fail with the
  SMTP error in the dialog and in the Log Viewer, delete everything.
- A low-privilege pass for each: VIEW SERVER STATE only; CONTROL SERVER
  without sysadmin (the Database Mail gate's reason to exist);
  DatabaseMailUserRole only.
- Every fixture object is disposable and named `gossms_test_*`; the RG
  classifier and enabled state and the Database Mail XPs / configuration
  values are read at start and restored at end.

## Recommended implementation order

Resource Governor goes first: it is pure DDL over a small catalog, the
closest to families that already ship, and its dialog's grid-editing patterns
are what Database Mail's Accounts/Profiles pages reuse. Stage E runs inside
each W, not at the end.

Mark a step done by ticking its box and appending the date and commit(s),
e.g. `- [x] **W3** … — done 2026-10-04, gosmo a1b2c3d`. Each W is one commit
boundary (two where it spans gosmo and gossms; gosmo lands first, built and
tested there — the `dev-with-local-gosmo` skill). Step IDs are permanent: a
step added later takes the next unused number and is inserted by position.

**Status (2026-09-30): 4 of 15 done. Next: W5.**

- [x] **W1 — Stage 0 probes, Resource Governor.** Edition/major presence
  (incl. 2025 Standard), rights for catalog, DMVs and DDL; the
  `_numeric` and tempdb-governance columns by major. Record results in this
  plan. *Blocks W2.* — done 2026-09-30, gossms 394a094 (§ W1 results; no
  code).
- [x] **W2 — gosmo RG reads** (Stage A1): the four types, version sweep on
  13/14/17. — done 2026-09-30, gosmo uncommitted (§ W2 results).
- [x] **W3 — gosmo RG writes + scripter** (Stage A1): create/alter/drop,
  classifier, enable/disable/reconfigure; `live_resource_governor_test.go`
  with state-restoring teardown. — done 2026-09-30, gosmo uncommitted
  (§ W3 results).
- [x] **W4 — RG tree + Detail Browser** (Stages B1, C1 first half): node
  types, wiring test, loaders, state label, live pool grid; `ARCHITECTURE.md`
  package-map rows. — done 2026-09-30, gossms uncommitted (§ W4 results).
- [ ] **W5 — RG Properties dialog** (Stage C1): General / Pools / Groups /
  External pools pages (Groups by pool dropdown), classifier picker +
  New classifier… template window, one batch + RECONFIGURE on Apply, leaf
  Properties preselecting their row.
- [ ] **W6 — RG gating, menus, ops, scripting** (Stage D1): `ControlServer`
  gates, New/Delete/Enable/Disable/Reconfigure/Reset Statistics, Script as.
- [ ] **W7 — RG live verification** (Stage E): tmux harness on 17 and the
  lowest edition, low-privilege pass, fixtures restored.
- [ ] **W8 — Stage 0 probes, Database Mail.** Express, Linux (ubusql1),
  XPs = 0 behavior; sysadmin vs CONTROL SERVER for config procs;
  `DatabaseMailUserRole` for send/log; `sysmail_update_account_sp` password
  and omitted-parameter semantics. *Blocks W9.*
- [ ] **W9 — gosmo Database Mail reads** (Stage A2): accounts, profiles,
  principal profiles, configuration, status, items, event log; version sweep.
- [ ] **W10 — gosmo Database Mail writes + scripter** (Stage A2): all
  `sysmail_*` wrappers, `SendTestMail`, log/item purge, password placeholder
  in scripts; `ErrorLogDatabaseMail`; `live_database_mail_test.go`.
- [ ] **W11 — Database Mail node + Detail Browser + Log Viewer** (Stages B2,
  C2 part): leaf with state label, status/profiles/accounts/failed-items
  view, View Database Mail Log with Delete-older-than instead of Recycle.
- [ ] **W12 — Database Mail Properties dialog** (Stage C2): the five pages,
  secret password field, dependency-ordered Apply, Script.
- [ ] **W13 — Send Test E-Mail + gating + menus** (Stages C2, D2): the
  polling test-mail dialog, sysadmin / `DatabaseMailUserRole` /
  `ALTER SETTINGS` gates, node menu.
- [ ] **W14 — Database Mail live verification** (Stage E): configure from
  scratch with XPs off, failed test mail visible in dialog and log,
  low-privilege pass, everything removed and settings restored.
- [ ] **W15 — Docs and close-out.** `README.md`, F1 help if keys were added,
  update `docs/open-threads.md` N6/N7/V4 (already recorded) with what shipped
  and any new deferrals, delete this
  plan.

Docs to update as it lands: `README.md` (features), `ARCHITECTURE.md`
§ Package map (every new `internal/tui` file), F1 help if any key binding is
added (`help_dialog.go`), `docs/open-threads.md` N6/N7/V4 (recorded 2026-09-30)
kept current. Delete this plan when both halves ship.

## Decisions (settled 2026-09-30)

Asked and answered before Stage A; don't re-raise.

- **D1 — Database Mail is a leaf + Properties dialog**, not a folder of
  Accounts / Profiles (argued under § The target tree).
- **D2 — External Resource Pools shown always on major ≥ 13**, like SSMS; no
  ML Services probe.
- **D3 — Pool affinity: read + script this pass, editing deferred** — it needs
  a scheduler/NUMA picker nothing else in gossms has. `docs/open-threads.md`
  N6.
- **D4 — Classifier: picker + "New classifier…" template.** The dialog lists
  existing schema-bound functions in master; New classifier… opens a query
  window with a template.
- **D5 — Managed Instance is unavailable; ship MI shown, ungated,
  unverified.** Gate on `EngineEdition` (5 hides RG; 8 behaves like on-prem),
  never `serverIsAzure`. All MI probes (W1, W8), the MI version sweep and the
  MI live runs are deferred to `docs/open-threads.md` V4; fake-driver tests
  pin the EngineEdition 8 branch meanwhile. The
  `AzureManagedInstance_dbmail_profile` Note ships as written.
- **D6 — RG Properties ▸ Workload Groups uses a pool dropdown** at the top, not
  a grid filtered by the Pools page's selection.
- **D7 — Order unchanged: Resource Governor first**, then Database Mail.
- **D8 — Agent Properties ▸ Alert System stays out**; recorded as
  `docs/open-threads.md` N7.
