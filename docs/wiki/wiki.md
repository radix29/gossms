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

Current release: **v0.0.14**. See
[RELEASE.md](https://github.com/radix29/gossms/blob/main/RELEASE.md) for what
changed and
[CHANGELOG.md](https://github.com/radix29/gossms/blob/main/CHANGELOG.md) for
the detail behind it. Supported servers: **SQL Server 2016 SP1 and later**, on
Windows and Linux, and **Azure SQL Managed Instance**.

**New in v0.0.14:** **Resource Governor** pools, workload groups and
Properties, a Database Mail **Send Test E-Mail** dialog, cross-database and
linked-server IntelliSense, and Extended Events grouping.

Questions and feedback: [goSSMS on Discord](https://discord.gg/7YVKzB3vZ).

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

## Connection

### Connect to SQL Server

Recent connections are listed on the left; pick one
to fill the form, or type a new server. The Connection Properties tab covers
authentication (SQL Server, Windows and every Microsoft Entra method), database,
encryption and custom properties, and the Connection String tab shows the same
connection as a string.

![connect](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/01_connect.png)

## Object Explorer

### Browse your servers

A tree view of instances, databases and their
objects in every schema. Expand folders to inspect tables, views, stored
procedures, security, storage, server objects and auditing. Right-click to
rename, delete, enable/disable, script or open Properties.

![object explorer](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/02_object_explorer.png)

### Object detail browser

The right-hand pane shows properties for whichever
node you select in the tree. Click any object to see its details.

![object explorer detail](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/04_object_explorer_detail.png)

## Database & Server Properties

### Server properties

Details about the connected SQL Server instance:
capabilities, configuration settings and metadata.

![server properties](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/03_server_props.png)

### Database properties

Full property sheet for a database: general info,
files, options, recovery model and more.

![database properties](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/05_database_props.png)

## Query Editor

### Write and run queries

Syntax-aware editor with query execution buttons,
toolbar controls and result tabs below the text area.

![query editor](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/06_query_editor.png)

### Multiple result sets

Running a batch that returns several result sets
generates multiple tabs, each showing its own grid of rows.

![multi-result query](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/07_multi_results.png)

## Execution Plans

### Graphical execution plan

The visual plan renderer draws operators as nodes
with arrows showing data flow, plus cost percentages and row estimates.

![exec plan graph](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/08_exec_plan_graph.png)

### Tree-view execution plan

An alternative to the graphical renderer: a
collapsible tree that shows each operator's details in text form.

![exec plan tree](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/09_exec_plan_tree.png)

### Live Query Statistics

With **Query > Live Query Statistics** (toolbar **Live**) on, Execute opens a
live tab showing the running statement's plan: each operator's rows against
its estimate, elapsed time and state, under a progress bar for the statement.
It follows a multi-statement batch or `GO` script statement by statement, and
becomes the actual execution plan when the run ends. Needs `VIEW SERVER
STATE`; without it the query runs as usual and the tab says why it is empty.

## Activity Monitor

### Historical activity view

Thirty minutes of live DMV data as charts:
batches, transactions and compiles, wait categories split into their resource
and signal halves, memory composition, page activity and per-file I/O latency.

![act mon history](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/10_act_mon_hist.png)

### Real-time system health

The same instant as a snapshot rather than a
trend: throughput bars, cache-ratio KPIs, memory composition and the current
wait breakdown. Blocking chains and running sessions have tabs of their own.

![act mon sample](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/11_act_mon_sample.png)

### Live execution plan of a session

Right-click a row on the Sessions or Block tab and pick **Show Live Execution
Plan** to watch that session's running query the way Live Query Statistics
does: each operator's rows against its estimate, its state, and a progress bar
for the statement. The view follows the session from statement to statement
and keeps its last reading when the query ends. SQL Server 2019 and later
profile every query, so any running query shows; on 2016 and 2017 only one
already profiled does (run with an actual plan, trace flag 7412, or an
Extended Events `query_thread_profile` session), and the view says so.
Without an actual plan the server counts rows only, so times show as `—`.

### TempDB monitoring

A dedicated view for tempdb: file usage, session temp
table allocations and internal object churn in real time.

![act mon tempdb](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/11_act_mon_tempdb.png)

### Instance resources (Azure editions)

A sixth tab that appears only on an
Azure engine edition: the instance's own 15-second resource history — CPU
against its cap, storage against the quota, IO requests and bytes per second —
beside the resource governor's fixed limits and the engine's OS job object.

*(Screenshots for this section are not captured yet.)*

## Query Store

### The seven SSMS views

Regressed Queries, Overall Resource Consumption, Top
Resource Consuming Queries, Queries With Forced Plans, Queries With High
Variation, Query Wait Statistics and Tracked Queries, under every database.
Each is an Object Explorer leaf whose report shows in the details pane, and
opening one raises the Query Store panel: the metric, statistic, time window
and row count are selectable, the rows are charted, and the selected query's
plans are listed. Force Plan, Unforce Plan, Show Plan, Track Query and Compare
Plans act from there — each writing action confirmed, and each offering the
script instead of running it.

*(Screenshots for this section are not captured yet.)*

## Plan Comparison

### Compare Showplan

Two plans of one query paired over the operator tree,
as two grids: the operators with what differs about each row named, and the
statement-level properties side by side.

*(Screenshots for this section are not captured yet.)*

## Log File Viewer

### SQL Server and SQL Agent logs

The current log or any archive, searched
on the server or filtered in place, with the selected entry's full text in a
details pane. **Select Files...** merges several archives of one family into a
single date-sorted grid, each row naming the file it came from.

*(Screenshots for this section are not captured yet.)*

## Always On

### Availability Group configuration

Create or manage Always On Availability
Groups from the Properties sheet, including replica settings, listener config,
and failover policy.

![availability group](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/12_alway_on_AG.png)

## Service Broker

### The whole subtree

Under each database, all seven Service Broker
families: message types, contracts, queues, services, routes, remote service
bindings and conversation priorities. Expand one to list its objects, or
select the folder to see them side by side in the details pane — for queues,
that means status, retention, activation procedure, reader count and the
number of messages waiting. A disabled queue says so in the tree. The folder
appears whether or not the broker is enabled on the database, because every
one of these objects can be created, listed and dropped either way.

![service broker](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/13_service_broker.png)

### Queue properties

Queues and routes are the two families you can edit,
because their settings are the ones that change while an application runs: a
queue taken out of service, an activation procedure stopped, a reader count
raised under load, or a route repointed at a new address. Everything the page
changes is an `ALTER QUEUE` or `ALTER ROUTE` clause, and **Script Changes**
shows you exactly which before you apply it.

![queue properties](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/14_queue_props.png)

The other five families are read-only. What they define — a message type's
validation, a contract's messages and who may send each — is part of the
application's own schema rather than a setting, and changing one mid-flight
changes the meaning of conversations already under way. **Script as ▸ CREATE**
is offered for all seven.

## Extended Events

### XEvent Profiler and Watch Live Data

Alt+P starts the Standard trace (its
own `gossms_QuickSession*` session, created on first use) and opens it live;
the same viewer serves **Watch Live Data** on any running session and **View
Target Data** on an `event_file` or `ring_buffer` target. The events stream
into a grid over a details pane showing every field of the selected event,
the batch text included. Columns are chosen per session, events group by one
or more columns into collapsible groups with COUNT/SUM/AVG/MIN/MAX shown under
their columns, and the feed can be paused, filtered (`duration > 1000000 and
database_name = 'app'`), searched, bookmarked and exported. Here a Standard
trace is grouped by event name, with the average and maximum duration and the
total logical reads per group.

![xevent profiler](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/15_xevent_profiler.png)

### Sessions in Object Explorer

Under Management ▸ Extended Events, each
session with its targets, running or stopped at a glance; Start/Stop,
scripting and delete. **New Session** and **Session Properties** edit a
session on four pages — a template, the event library with its fields and
filters, the targets, and the buffer options — and Script Changes shows the
CREATE or the minimal ALTER.

## Resource Governor

Under Management: resource pools with their workload groups nested beneath,
and external resource pools. Pools and groups show live counters (active and
queued requests, CPU used) beside their limits, and the node's label says
whether the governor is disabled or has a reconfiguration pending. Enable,
Disable, Reconfigure, Reset Statistics, Delete and Script as are in the
right-click menu.

**Resource Governor Properties** edits the enabled state, the classifier
function (pick one in master, or start one from a template), the I/O limit,
and every pool and group on its own page. Apply runs the changes in dependency
order and then puts them in force — or keeps a disabled governor disabled.

![resource governor](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/16_resource_governor.png)

## Database Mail

Under Management, **Database Mail** shows its status, queues, profiles,
accounts and recent failed items. The right-click menu configures it, sends a
test e-mail (waiting for the outcome and showing the SMTP error on failure),
starts or stops it, opens the Database Mail log and scripts it.

**Database Mail Properties** replaces SSMS's wizard in one Apply: 'Database
Mail XPs', SMTP accounts (a blank password keeps the stored one), profiles
with their accounts in failover order, public and private profile security,
and the system parameters.

![database mail](https://github.com/radix29/gossms/raw/main/docs/wiki/screenshots/17_database_mail.png)

## Azure SQL Managed Instance

### A supported target, not just a reachable one

Object Explorer, the query
editor and execution plans, Server and Database Properties, Activity Monitor
and the error logs all work against a Managed Instance. Version gating follows
the engine edition rather than the `12.0.2000.8` a Managed Instance reports,
backup and restore emit `TO URL` / `FROM URL` for Azure Storage, and the
operations the edition refuses — Detach, Attach, Take Offline, the recovery
model, New Database's file rows — are disabled with a note rather than failing
at the server.

*(Screenshots for this section are not captured yet.)*
