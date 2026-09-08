# Plan — Azure SQL Managed Instance support

Audit run **2026-09-08** against a live General Purpose Gen5 (4 vCore) MI,
`t-qmi-01.public.686f4bf5ea41.database.windows.net,3342`, SQL auth `testgo`
(sysadmin), compared with the on-prem baseline `win10cli` (17.0.1125.2).
Entra authentication is **out of scope here** — tested separately, later.

Estimate: **1–2 weeks**, roughly two thirds in gosmo. Nothing found is a
redesign; the work is one systemic gate, four broken reads/emits, and one new
Activity Monitor tab.

## What the instance reports

| | Value |
|---|---|
| `SERVERPROPERTY('EngineEdition')` | **8** (Managed Instance) |
| `SERVERPROPERTY('ProductVersion')` | **12.0.2000.8** |
| `SERVERPROPERTY('Edition')` | `SQL Azure` |
| `@@VERSION` | `Microsoft SQL Azure (RTM) - 12.0.2000.8 … ` — **no ` on Windows`/` on Linux` suffix** |
| `SERVERPROPERTY('ServerName')` | `t-qmi-01.686f4bf5ea41.database.windows.net` (no `,port`) |
| `SERVERPROPERTY('IsHadrEnabled')` | 0 |
| `SERVERPROPERTY('InstanceDefaultBackupPath')` | `https://…blob.core.windows.net/managedserverbac-…/backup` |
| Real engine build (from `sp_readerrorlog`) | **18.0.131.5220** — "SQL Server vNext" |

`12.0.2000.8` is the fixed compatibility fiction Azure returns; it is **not**
the feature level. The instance has every catalog column gosmo gates on 2016,
2017, 2019 and 2022, and databases are created at compatibility level **170**.
This one fact is the root of most of the work below.

## Verdict: what works today

Driven live in the TUI (tmux harness, see `docs/testing.md`).

- **Connect** — SQL auth, `TrustServerCertificate=true`, `encrypt=false`; the
  connect dialog needs Server/Port/User/Password and nothing else. Object
  Explorer Details populates.
- **Object Explorer** — every folder enumerates: Databases (system + user),
  Security (Logins incl. `##MS_*` and Entra principals, Server Roles incl. the
  `##MS_*` fixed roles, Credentials, Cryptographic Providers, Audits, Server
  Audit Specifications), Server Objects (Backup Devices, Endpoints, Linked
  Servers, Triggers), Management > SQL Server Logs, Always On (correctly
  reports "not enabled"), SQL Server Agent (Jobs, Schedules, Alerts,
  Operators, Error Logs, SQL-only administration).
- **Query editor** — execution, results grid, IntelliSense metadata tooltips,
  estimated and actual execution plans (Plan / Tree / XML) all correct.
- **Activity Monitor** — all five tabs (History, Sample, TempDB, Sessions,
  Block) collect and draw. Every DMV they use exists and is readable:
  `dm_os_ring_buffers`, `dm_os_schedulers`, `dm_os_performance_counters`
  (4050 rows), `dm_os_memory_clerks`, `dm_exec_sessions`, `dm_exec_requests`,
  `dm_io_virtual_file_stats`, `dm_os_wait_stats`, `dm_db_session_space_usage`.
  `sp_WhoIsActive` installs and runs.
- **Server Properties** — all seven pages render; General correctly shows
  "Engine edition: Managed Instance".
- **Database Properties** — Files, Filegroups, Options, Change Tracking,
  Query Store, Permissions, Extended Properties, Database Scoped
  Configurations all render. (General does not — see below.)
- **Error log viewer** — both SQL Server and SQL Server Agent logs, with the
  file picker, search, export.

## Defects found

### 1. `BackupHistory` scan failure kills Database Properties > General

**gosmo**, `backup.go` `BackupHistoryContext`. MI's automated backups write
`NULL` into `backupmediafamily.physical_device_name` and into
`backupset.user_name` / `.server_name`; the scan targets are plain `string`,
so the read dies with

```
sql: Scan error on column index 7, name "physical_device_name"
```

and the whole **Database Properties > General** page renders as an error. The
Backup History viewer and Restore's "Backup History" source take the same path.

Fix: `ISNULL(…,'')` in the SELECT, or `sql.NullString` destinations, for those
three columns. This is a plain nullability bug that on-prem never exposes —
worth auditing the neighbouring `backupset` reads at the same time.

### 2. Version gating treats MI as SQL Server 2014

**gosmo**, `version_gate.go` `serverMajorVersion()` returns `info.VersionMajor`
= **12** on MI, below every gate. Confirmed consequences:

| Gate | Site | What MI actually has |
|---|---|---|
| `colSince(…, SQLServer2017, "is_value_default", …)` | `database_scoped_config.go:43` | column present |
| `colSince(…, SQLServer2019, "allow_enclave_computations"/"signature", …)` | `security.go:45-46` | columns present |
| `colSince(…, SQLServer2017/2019, query-store options)` | `query_store.go:56-60` | columns present |
| `colSince(…, SQLServer2022, "t.ledger_type_desc", …)` | `table.go:103` | column present |
| `colSince(…, SQLServer2016/2017/2022, AG columns)` | `availability_group.go` | n/a — no AGs on MI |
| `unsupportedVersionf(… "requires SQL Server 2017 or later")` | `query_store_reports.go:970` | `sys.query_store_wait_stats` present — **the call is refused outright** |
| `EnumFileSystemIsLegacy()` — `VersionMajor < 14` | `filesystem.go:78` | `sys.dm_os_enumerate_filesystem` present; MI falls back to `xp_dirtree`, losing Size and LastModified |
| `FixedDrivesContext` — `VersionMajor < 15` | `filesystem.go:171` | `sys.dm_os_enumerate_fixed_drives` present; MI falls back to `xp_fixeddrives` |

Both `xp_dirtree` and `xp_fixeddrives` *do* work on MI, so nothing errors —
everything silently degrades, which is the worse failure mode.

Fix: gate on the **engine edition** before the version number. Add
`ServerInfo.IsAzure` (EngineEdition 5 SQL Database, 6 Synapse, 8 Managed
Instance, 9 Azure SQL Edge, 11 Synapse serverless) in `server.go` `loadInfo`,
and make `serverMajorVersion()` return `0` for those — gosmo's established
"never read ⇒ treat as newest" convention, already documented on `colSince`.
Leave `info.VersionMajor` at 12 for display. Apply the same test to the two
`filesystem.go` gates and to `query_store_reports.go:970`.

Do this **before** anything else; several later items sit on top of it.

### 3. Backup and Restore emit `DISK` where MI requires `URL` — **done 2026-09-09**

Scripted from the live Back Up Database dialog on MI:

```sql
BACKUP DATABASE [GoTest01] TO DISK = N'https://wasdpsecabakmi4314.blob.core.windows.net/…/GoTest01_full.bak'
```

The path is right — it comes from `InstanceDefaultBackupPath`, which MI
populates with the blob container — but the device type is not. MI answers any
`TO DISK`/`FROM DISK` with

```
Msg 41902 … SQL Database Managed Instance supports database restore from URI backup device only.
```

Fixed in gosmo's `backup.go` and the two gossms dialogs.

**gosmo.** `IsBackupURL(device)` is the exported test — an `http`/`https`
scheme, so a UNC path stays a DISK device — and `backupDeviceClause` applies it
to every entry of `BackupOptions.Devices` / `RestoreOptions.Devices`, which are
plain strings and so have nothing else to go on. `BackupTarget` gained a `url`
arm and a `URLTarget` constructor, and `DiskTarget` classifies a URL for
itself: every RESTORE-side read (`VerifyBackup`, `BackupHeaders`,
`BackupFileList`) funnels through it, and MI refuses all three as DISK. Both
option structs also gained `Credential`, the `WITH CREDENTIAL = N'…'`
storage-account-key form; the SAS form MI uses leaves it empty, because there
the credential's *name* is the container URL and SQL Server finds it itself.

**gossms.** `BackupDialog.applyDeviceRules` (and the Restore dialog's
counterpart) runs on every `syncAutoDest` pass, since the destination is a text
field the user can retype into a URL:

| Rule | Applies when |
|---|---|
| Backup Type pinned to Full, greyed | the engine edition is Azure |
| Copy-only forced checked, greyed | the engine edition is Azure |
| **Browse** disabled | the destination is a URL, on any edition — it walks the *server's* filesystem, which a blob container is not — or the edition is Azure |
| Status line: "To URL: the container needs a credential named for it." | the destination is a URL, on any edition; a real message (an error, Validate's statement) outranks it |

Disabling those needed `SetEnabled`/`Enabled` on `widgets.Button`, `CheckBox`
and `RadioBox`, mirroring `InputField`'s contract exactly — greyed *and*
refusing keys and clicks, keeping its place in the caller's focus ring so Tab
order does not shift under the user.

Verified live on both instances. On `t-qmi-01`, Back Up Database on `GoTest01`
draws Full and Copy-only grey and Validate reports
`BACKUP DATABASE [GoTest01] TO URL = N'https://wasdpsecabakmi4314.blob…'`; on
`win10cli` nothing is greyed until the destination is retyped as a URL, at
which point only Browse and the hint change and the type radio stays live —
an on-premises server can back up a differential or a log to URL. The
device-keyword question was settled directly in the query editor: MI answers
`RESTORE VERIFYONLY FROM DISK = N'https://…'` with Msg 41902 ("Unsupported
device type") and the same statement spelled `FROM URL` with Msg 3078 about
the blob itself.

The live run also caught the one real bug in the change, which no unit test
had: `IsBackupURL` sliced `d[:8]` after checking only `len(d) >= 7`, and
panicked the moment a user typing a URL passed through exactly `"http://"`.
Every prefix of a URL is now a test case.

**Not reached, because a SAS credential on the container does not exist yet:**
executing a backup or restore. `WITH INIT` on a URL device, `RESTORE ... WITH
MOVE` on MI, and restoring from MI's automated backup history are open — see
`docs/open-threads.md` § Azure SQL Managed Instance for why none of them was
guessed at.

### 4. SQL Server Agent status reads "Unknown" — **done 2026-09-09**

`sys.dm_server_services` returns **0 rows** on MI (and `xp_servicecontrol`
fails with "The specified service does not exist as an installed service"), so
gosmo's `AgentInfoContext` (`agent_job.go:36`) falls to its `"Unknown"` branch.
The Agent is in fact running — its error log shows
`[100] Microsoft SQLServerAgent version 18.0.131.5220 … Process ID 15268`.

Fixed in gosmo `agent_job.go` `agentInfoAzure`, which `AgentInfoContext` routes
to on an Azure edition: `sys.dm_exec_sessions` rows with `program_name LIKE
'SQLAgent%'` decide Running (MI holds two: "Generic Refresher" and "Email
Logger"), and `MAX(msdb.dbo.syssessions.agent_start_date)` supplies the last
startup time. Both are SQL-visible state, so the "no WMI" contract holds.
Verified live: Object Explorer Details for SQL Server Agent now reads
Status **Running**, Last startup 2026-09-08 21:28:20.

### 5. Disk-space rows are nonsense, and duplicated — **done 2026-09-09**

Server Properties > Database Settings and Object Explorer Details both show

```
Disk (C:\)   65,344 MB free of 192 MB
Disk (C:\)   98,112 MB free of 192 MB
```

This is what the instance reports —
`sys.dm_os_volume_stats` gives `total_bytes` = 192 MB against
`available_bytes` of 64/96 GB, twice for the same mount point. Not a gossms
arithmetic bug, but shipping "free of" a smaller number, twice, is worse than
saying nothing.

Fixed with one helper, `serverDiskSpaceRows` in
`internal/tui/detail_browser_server.go`, that both Object Explorer Details and
Server Properties > Database Settings now call, so the two cannot disagree.
`usableDiskVolumes` beside it drops a volume whose total is zero or smaller
than its available figure and dedupes by mount point (an unnamed volume is
kept as it comes — the sample path is per file, not per volume), covered by
`TestUsableDiskVolumesDropsNonsenseAndDuplicates`. On an Azure edition the
helper skips `dm_os_volume_stats` entirely and shows one row from
`sys.server_resource_stats` instead. Verified live: both pages now read
`Storage  65,344 MB free of 65,536 MB`, and `win10cli` still shows its single
real `C:\` volume.

The gosmo side is the new `azure_resources.go`: `ServerResourceStat`,
`ServerResourceStatsContext(ctx, max)` (history, oldest first) and
`LatestServerResourceStatsContext` (the newest row — SKU, hardware generation,
vCores, quota). Both refuse on a non-Azure edition with an
`ErrUnsupportedVersion` error. This is the read item 7's Instance tab is built
on, landed early because item 5 needs the same row.

### 6. Platform shows "Unknown" — **done 2026-09-09**

`platformFromVersionString` (`server.go`) matched ` on Windows` / ` on Linux`;
MI's `@@VERSION` has neither. It now returns `"Azure"` for a banner containing
`Microsoft SQL Azure`, `ServerInfo.Platform`'s doc says so, and
`TestPlatformFromVersionString` carries the real MI banner. Verified live:
Object Explorer Details and Server Properties > General both read
Platform **Azure**.

No consumer regressed — `server_props_advanced.go` tests only for `"Linux"`,
and `serverIsWindows` (`server_filesystem.go`) already fell through to the
default backup path for an empty Platform, which on MI is a blob URL and so
still picks POSIX rules.

### 7. Unsupported operations are offered without gating — **done 2026-09-09**

Present in the UI, rejected by MI:

| Offered | MI's answer |
|---|---|
| Database context menu > **Detach Database…** | `Could not find stored procedure 'sp_detach_db'` |
| Database context menu > **Take Database Offline** | `Msg 5008 … ALTER DATABASE statement is not supported` |
| Database Properties > Options > Recovery model | same `Msg 5008` — MI is FULL-only for user databases |
| New Database > file logical names, paths, sizes, growth; Filegroups page | `Msg 41918 … Specifying files and filegroups in CREATE DATABASE is not supported` |
| Attach Database dialog | same class |

New Database *does* succeed when the file fields are left blank, which is the
documented "server default" path — so the fix is to disable the file/filegroup
rows on Azure editions, not to block the dialog.

Fixed in `internal/tui/edition_gate.go`, which asks the edition's question in
the shape `permission_gate.go` already uses — a disabled item with a short note
— and composes with it: `gateAzure(gate(item, …), sc)`, the edition's note
winning because no permission gets a user past an engine that does not
implement the statement. `serverIsAzure` fails open on a connection with no
server info, matching `allowsActionOn`.

| Site | On an Azure edition |
|---|---|
| Databases > Attach Database… | disabled, "not on Managed Instance" |
| Database > Take Database Offline / Bring Online | disabled, same note |
| Database > Detach Database… | disabled, same note |
| Database Properties > General > Recovery model | read-only, with "Recovery model is fixed on Managed Instance." |
| New Database > General: the eight file rows and Recovery model | read-only, with a note saying the instance places and sizes files itself |
| New Database > Filegroups | one note; the page stays in the list rather than renumbering the dialog's fixed pages/forms/applyFns triple |

The note names the engine edition (`engineEditionName`) rather than "Azure", so
it stays true on SQL Database or SQL Edge.

Covered by `internal/tui/edition_gate_test.go` — both directions, including
that an on-prem menu loses nothing — on `newFakeConnOnAzureMI`, a fake
answering the real MI's connect-time row. Verified live on both instances:
every row above is grey or flat on `t-qmi-01` and untouched on `win10cli`.

## The new Activity Monitor tab — **done 2026-09-09**

MI exposes instance-level resource and hardware views that no existing tab
covers, and that on-prem has no analogue for. All are readable by `testgo` and
verified live.

### `sys.server_resource_stats` — the history source

One row per **15-second** interval, retained ~14 days (302 rows present at
audit time). Columns:

```
start_time, end_time            datetime2
resource_type                   nvarchar   'SQL managed instance'
resource_name                   nvarchar   't-qmi-01'
sku                             nvarchar   'GeneralPurpose'
hardware_generation             nvarchar   'Gen5'
virtual_core_count              int        4
avg_cpu_percent                 decimal
reserved_storage_mb             bigint     65536
storage_space_used_mb           decimal    192.00
io_requests, io_bytes_read, io_bytes_written   bigint
```

This is a *pre-aggregated history*, not a counter to be sampled — which makes
it a natural fit for the History tab's charting code but **not** for the
`internal/activity` sampler's delta-per-second model. Read it whole on refresh
and plot `end_time` against value; do not run it through `rates.go`.

### `sys.dm_instance_resource_governance` — the static limits (1 row)

`instance_cap_cpu`, `instance_max_log_rate`, `instance_max_worker_threads`,
`volume_local_iops`, `volume_managed_xstore_iops`,
`user_data_directory_space_quota_mb`, `user_data_directory_space_usage_mb`,
`tempdb_log_file_number`, `bufferpool_extension_size_gb`, and the
`volume_*_max_oustanding_io` triple (Microsoft's spelling, keep it).

### `sys.dm_os_job_object` — the Windows job object the engine runs inside (1 row)

`cpu_rate`, `cpu_affinity_mask`, `memory_limit_mb`, `process_memory_limit_mb`,
`workingset_limit_mb`, `low_mem_signal_threshold_mb`, `total_user_time`,
`total_kernel_time`, `read_operation_count`, `write_operation_count`,
`peak_process_memory_used_mb`, `peak_job_memory_used_mb`.

### `sys.dm_db_resource_stats` — per-database, 15-second, ~1 hour retained

Database-scoped (71 rows for `GoTest01`): `end_time`, `avg_cpu_percent`,
`avg_data_io_percent`, `avg_log_write_percent`, `avg_memory_usage_percent`,
`xtp_storage_percent`, `max_worker_percent`, `max_session_percent`,
`cpu_limit`, `used_storage_mb`, `allocated_storage_mb`, `replica_role`,
`avg_instance_cpu_percent`, `avg_instance_memory_percent`.

### `sys.dm_user_db_resource_governance` — per-database limits (one row per db)

`slo_name`, `cpu_limit`, `max_dop`, `min/max_memory`, `max_sessions`,
`max_db_max_size_in_mb`, `db_file_growth_in_mb`, `log_size_in_mb`,
`instance_max_log_rate`, `primary_group_max_workers`, `primary_max_log_rate`,
`checkpoint_rate_mbps`, and ~40 more.

### Proposed shape

A sixth tab, **`Instance`** (label fits the existing bar: History, Sample,
TempDB, Sessions, Block, Instance), **shown only when
`EngineEdition` is an Azure edition** — `amTabCount` and `amTabLabels` in
`activity_monitor.go:21-30` are fixed-size arrays indexed by `amTab`, so
conditional tabs need a visible-tabs slice rather than a constant; that
refactor is the first task of this item. `dashboardTab()` /`canvasTab()`
(lines 35-40) and the per-tab `scrollX`/`scrollY` arrays follow.

Layout, top to bottom:

1. **Header strip** — SKU, hardware generation, vCores, instance name, from
   the newest `server_resource_stats` row. Static text, refreshed on tick.
2. **CPU %** — `avg_cpu_percent` history line, fixed 0-100 scale, sharing
   `charts` with the existing History panels.
3. **Storage** — `storage_space_used_mb` against `reserved_storage_mb` as a
   filled area with the quota as the scale max; this is the number that
   actually stops an MI working.
4. **IO** — `io_requests` and `io_bytes_read`/`io_bytes_written` as two
   stacked panels, against `volume_local_iops` from
   `dm_instance_resource_governance` as the ceiling.
5. **Limits** — a static key/value grid: instance CPU cap, max worker
   threads, max log rate, memory limit and working-set limit from
   `dm_os_job_object`, data-directory quota and usage.

Per-database (`dm_db_resource_stats`, `dm_user_db_resource_governance`) is
deliberately **out of scope for this tab** — it is database-scoped, wants a
database picker, and belongs with Database Properties or a later tab. Note it
and move on.

New gosmo reads, in a new `azure_resources.go`: `ServerResourceStatsContext`,
`InstanceResourceGovernanceContext`, `OSJobObjectContext`, each returning a
typed struct, each refusing with `ErrUnsupportedVersion`-style guidance on a
non-Azure edition. Follow `~/go/gosmo/CLAUDE.md` § This is a library — these
are additions, nothing is narrowed.

### What was built

**gosmo.** All three reads are in `azure_resources.go`, every column of every
one scanned through a `sql.Null*` destination — `workingset_limit_mb` really is
NULL on a live General Purpose instance. Column names were taken from
`sys.all_columns` on the instance rather than from documentation, which is how
`volume_local_max_oustanding_io` (Microsoft's spelling, missing the first `t`)
is spelled correctly in the query. `live_azure_resources_test.go` covers all
three, and asserts the `ErrUnsupportedVersion` refusal instead when pointed at
a non-Azure DSN; it passes against `t-qmi-01` and against `win10cli`.

**The visible-tabs refactor,** which was the first task and is what the rest
sits on. `amTabLabels` and the per-tab scroll arrays stay indexed by `amTab` —
a withheld tab still has a scroll position, it is simply unreachable — and only
the *bar* became a slice: `visibleTabs()` filters `amAllTabs` by
`serverIsAzure`, `tabSegments` pairs with it by index, and `stepTab` walks it
so Tab and Backtab cannot stop on a tab the bar does not draw. `setTab` refuses
anything outside it, which is the one gate every key, click and caller goes
through.

**The feed.** The Instance tab is the one dashboard whose source is already
aggregated: `sys.server_resource_stats` is the server's own 15-second history,
kept for two weeks, so one tick reads the whole thing and there is no
`activity.Store` beside it. Running it through `rates.go` would average an
average. `internal/activity` gained `Poller[S]` for exactly this — the shared
ticking half (rate selector, Pause, Stop, VIEW SERVER STATE prologue, failure
backoff) over a caller-supplied probe, which is what lets the reads live in
gosmo without `internal/activity` importing it.

Two consequences worth keeping:

- `drawInterval()` returns the *server's* 15-second window on this tab, not the
  panel's poll rate. Scaling the axis by a 30-second poll would label every
  column with twice the span it covers. `resolution()` follows it, so the
  header says what a column is; the toolbar's own "Instance rate:" selector
  says how often the panel re-reads.
- The IO ceilings are section-bar KPIs, not chart axes — a deliberate departure
  from the layout sketched above. An axis pinned to a 6,000 IOPS limit renders
  an ordinary workload as a flat line on the baseline: true, and useless for
  reading the shape of the IO. The number that says how much headroom is left
  reads better beside the chart than as its scale. The storage chart *does*
  take the quota as its maximum, because there the proportion is the point.

Verified live on both instances: on `t-qmi-01` the tab draws CPU %, storage
against the 65,536 MB quota, IO requests and bytes per second, and a
three-column limits grid whose every value matches `sqlcmd` (100 % CPU cap,
1,220 workers, 6,000 local IOPS, 500 outstanding, 20,892 MB job memory,
"not set" for the NULL working-set limit). On `win10cli` the bar carries the
same five tabs it always did and Tab cycles through those five only.

## Order of work

1. ~~`ServerInfo.IsAzure` + engine-edition-aware `serverMajorVersion()` (item 2).~~ **done**
2. ~~`BackupHistory` nullability (item 1).~~ **done**
3. ~~Agent status (item 4) and Platform (item 6).~~ **done 2026-09-09**
4. ~~Disk-space rows (item 5).~~ **done 2026-09-09** — brought
   `ServerResourceStatsContext` forward from step 7 with it.
5. ~~UI gating of unsupported operations (item 7).~~ **done 2026-09-09**
6. ~~`TO URL` / `FROM URL` backup and restore (item 3).~~ **done 2026-09-09** —
   built and validated live on both instances; executing one still needs a SAS
   credential on the test instance.
7. ~~The Instance tab, starting with the fixed-array → visible-slice refactor.~~
   **done 2026-09-09**

## Testing notes

- The instance is reachable with `/opt/mssql-tools18/bin/sqlcmd -S
  't-qmi-01.public.686f4bf5ea41.database.windows.net,3342' -U testgo -C -N`.
  `testgo` is **sysadmin**, so nothing here is masked by permissions —
  re-check against a lower-privileged login before calling permission gating
  done.
- `TestLiveVersionSweep` (`~/go/gosmo/live_versionsweep_test.go`) is the right
  standing check for item 2: run it against MI once `IsAzure` lands, the same
  way it is run against majors 13/14/17. Expect it to surface reads this audit
  did not reach.
- A throwaway database **`GoTest01`** was created on the instance for this
  audit and **left in place** for the follow-up work (backup/restore needs a
  real user database). Drop it when this plan closes.
- Entra authentication is untested and out of scope; retest the whole connect
  path once it is added.
