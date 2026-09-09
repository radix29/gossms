Welcome to the gossms wiki!

<p align="center">
  <img src="https://github.com/radix29/gossms/raw/main/gossms_logo.png" alt="gossms" width="720">
</p>

<p align="center">
  <strong>Manage SQL Server without leaving macOS or Linux</strong>
</p>

<p align="center">
  <a href="https://github.com/radix29/gossms/commits/main"><img src="https://img.shields.io/github/last-commit/radix29/gossms?style=flat" alt="Last commit"></a>
  <a href="https://github.com/radix29/gossms/blob/main/LICENSE"><img src="https://img.shields.io/github/license/radix29/gossms?style=flat" alt="License"></a>
</p>

# goSSMS

A terminal-based SQL Server Management Studio for Linux, macOS, and Windows.
One executable — no GUI, no installer, no SQL client tools or drivers
required. More content here soon.

Current release: **v0.0.10**. See
[RELEASE.md](https://github.com/radix29/gossms/blob/main/RELEASE.md) for what
changed and
[CHANGELOG.md](https://github.com/radix29/gossms/blob/main/CHANGELOG.md) for
the detail behind it. Supported servers: **SQL Server 2016 SP1 and later**, on
Windows and Linux, and **Azure SQL Managed Instance**.

## Install

```bash
brew install radix29/tap/gossms          # macOS and Linux
```

Debian, Ubuntu and derivatives install from the APT repository at
[radix29.github.io/apt](https://radix29.github.io/apt); the full instructions
are in
[README.md](https://github.com/radix29/gossms/blob/main/README.md#installation).
Single-file binaries for Windows, Linux and macOS are on the
[releases page](https://github.com/radix29/gossms/releases/latest).

# Gallery

### Connection

**Connect to SQL Server** — The connection dialog lets you pick an auth mode,
specify server, database and credentials.

![connect](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/01_connect.png)

### Object Explorer

**Browse your servers** — A tree view of instances, databases and their
objects in every schema. Expand folders to inspect tables, views, stored
procedures, security, storage, server objects and auditing. Right-click to
rename, delete, enable/disable, script or open Properties.

![object explorer](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/02_object_explorer.png)

**Server properties** — Details about the connected SQL Server instance:
capabilities, configuration settings and metadata.

![server properties](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/03_server_props.png)

**Object detail browser** — The right-hand pane shows properties for whichever
node you select in the tree. Click any object to see its details.

![object explorer detail](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/04_object_explorer_detail.png)

### Database & Server Properties

**Database properties** — Full property sheet for a database: general info,
files, options, recovery model and more.

![database properties](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/05_database_props.png)

### Query Editor

**Write and run queries** — Syntax-aware editor with query execution buttons,
toolbar controls and result tabs below the text area.

![query editor](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/06_query_editor.png)

**Multiple result sets** — Running a batch that returns several result sets
generates multiple tabs, each showing its own grid of rows.

![multi-result query](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/07_multi_results.png)

### Execution Plans

**Graphical execution plan** — The visual plan renderer draws operators as nodes
with arrows showing data flow, plus cost percentages and row estimates.

![exec plan graph](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/08_exec_plan_graph.png)

**Tree-view execution plan** — An alternative to the graphical renderer: a
collapsible tree that shows each operator's details in text form.

![exec plan tree](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/09_exec_plan_tree.png)

### Activity Monitor

**Historical activity view** — Thirty minutes of live DMV data as charts:
batches, transactions and compiles, wait categories split into their resource
and signal halves, memory composition, page activity and per-file I/O latency.

![act mon history](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/10_act_mon_hist.png)

**Real-time system health** — The same instant as a snapshot rather than a
trend: throughput bars, cache-ratio KPIs, memory composition and the current
wait breakdown. Blocking chains and running sessions have tabs of their own.

![act mon sample](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/11_act_mon_sample.png)

**TempDB monitoring** — A dedicated view for tempdb: file usage, session temp
table allocations and internal object churn in real time.

![act mon tempdb](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/11_act_mon_tempdb.png)

**Instance resources (Azure editions)** — A sixth tab that appears only on an
Azure engine edition: the instance's own 15-second resource history — CPU
against its cap, storage against the quota, IO requests and bytes per second —
beside the resource governor's fixed limits and the engine's OS job object.

*(Screenshots for this section are not captured yet.)*

### Query Store

**The seven SSMS views** — Regressed Queries, Overall Resource Consumption, Top
Resource Consuming Queries, Queries With Forced Plans, Queries With High
Variation, Query Wait Statistics and Tracked Queries, under every database.
Each is an Object Explorer leaf whose report shows in the details pane, and
opening one raises the Query Store panel: the metric, statistic, time window
and row count are selectable, the rows are charted, and the selected query's
plans are listed. Force Plan, Unforce Plan, Show Plan, Track Query and Compare
Plans act from there — each writing action confirmed, and each offering the
script instead of running it.

*(Screenshots for this section are not captured yet.)*

### Plan Comparison

**Compare Showplan** — Two plans of one query paired over the operator tree,
as two grids: the operators with what differs about each row named, and the
statement-level properties side by side.

*(Screenshots for this section are not captured yet.)*

### Log File Viewer

**SQL Server and SQL Agent logs** — The current log or any archive, searched
on the server or filtered in place, with the selected entry's full text in a
details pane. **Select Files...** merges several archives of one family into a
single date-sorted grid, each row naming the file it came from.

*(Screenshots for this section are not captured yet.)*

### Always On

**Availability Group configuration** — Create or manage Always On Availability
Groups from the Properties sheet, including replica settings, listener config,
and failover policy.

![availability group](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/12_alway_on_AG.png)

### Azure SQL Managed Instance

**A supported target, not just a reachable one** — Object Explorer, the query
editor and execution plans, Server and Database Properties, Activity Monitor
and the error logs all work against a Managed Instance. Version gating follows
the engine edition rather than the `12.0.2000.8` a Managed Instance reports,
backup and restore emit `TO URL` / `FROM URL` for Azure Storage, and the
operations the edition refuses — Detach, Attach, Take Offline, the recovery
model, New Database's file rows — are disabled with a note rather than failing
at the server.

*(Screenshots for this section are not captured yet.)*
