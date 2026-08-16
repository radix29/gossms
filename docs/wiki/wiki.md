Welcome to the gossms wiki!

<p align="center">
  <img src="https://github.com/radix29/gossms/raw/main/gossms_logo.png" alt="gossms" width="720">
</p>

<p align="center">
  <strong>Manage SQL Server without leaving macOS or Linux</strong>
</p>

<p align="center">
  <a href="https://github.com/radix29/gossms/commits/main"><img src="https://img.shields.io/github/last-commit/radix29/gossms?style=flat" alt="Last commit"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/radix29/gossms?style=flat" alt="License"></a>
</p>

# goSSMS

A terminal-based SQL Server Management Studio for Linux, macOS, and Windows.
One executable — no GUI, no installer, no SQL client tools or drivers
required.We'll put more content here soon.

# Gallery

### Connection

**Connect to SQL Server** — The connection dialog lets you pick an auth mode,
specify server, database and credentials.

![connect](screenshots/01_connect.png)

### Object Explorer

**Browse your servers** — A tree view of instances, databases and all their
dbo-schema objects. Expand folders to inspect tables, views, stored procedures
and more.

![object explorer](screenshots/02_object_explorer.png)

**Server properties** — Details about the connected SQL Server instance:
capabilities, configuration settings and metadata.

![server properties](screenshots/03_server_props.png)

**Object detail browser** — The right-hand pane shows properties for whichever
node you select in the tree. Click any object to see its details.

![object explorer detail](screenshots/04_object_explorer_detail.png)

### Database & Server Properties

**Database properties** — Full property sheet for a database: general info,
files, options, recovery model and more.

![database properties](screenshots/05_database_props.png)

### Query Editor

**Write and run queries** — Syntax-aware editor with query execution buttons,
toolbar controls and result tabs below the text area.

![query editor](screenshots/06_query_editor.png)

**Multiple result sets** — Running a batch that returns several result sets
generates multiple tabs, each showing its own grid of rows.

![multi-result query](screenshots/07_multi_results.png)

### Execution Plans

**Graphical execution plan** — The visual plan renderer draws operators as nodes
with arrows showing data flow, plus cost percentages and row estimates.

![exec plan graph](screenshots/08_exec_plan_graph.png)

**Tree-view execution plan** — An alternative to the graphical renderer: a
collapsible tree that shows each operator's details in text form.

![exec plan tree](screenshots/09_exec_plan_tree.png)

### Activity Monitor

**Historical activity view** — See past queries and their durations from the
activity monitor's historical data collection.

![act mon history](screenshots/10_act_mon_hist.png)

**Real-time system health** — The sampled view shows current blocking chains,
active sessions, resource waits and CPU/disk I/O trends.

![act mon sample](screenshots/11_act_mon_sample.png)

**TempDB monitoring** — A dedicated view for tempdb: file usage, session temp
table allocations and internal object churn in real time.

![act mon tempdb](screenshots/11_act_mon_tempdb.png)

### Always On

**Availability Group configuration** — Create or manage Always On Availability
Groups from the Properties sheet, including replica settings, listener config,
and failover policy.

![availability group](screenshots/12_alway_on_AG.png)

