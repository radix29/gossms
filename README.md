<p align="center">
  <img src="gossms_logo.png" alt="gossms" width="720">
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
required.

![Demo](demo.gif)

## Features

- **Object Explorer** — the familiar SSMS tree: databases, tables, views,
  procedures, functions, triggers, sequences, synonyms, server security
  (logins, server roles, credentials), database security (users, roles,
  schemas, row-level security policies, Always Encrypted keys),
  storage (partition functions and schemes), server objects (backup devices,
  linked servers), and Management. Right-click anything to rename, delete, see dependencies, or
  open Properties. **Script <object> as** covers every family the tree
  shows — CREATE, ALTER, DROP, DROP And CREATE, and the SELECT/INSERT/
  UPDATE/DELETE/EXECUTE templates — to a new query window, a file, or the
  clipboard; an index adds REBUILD, REORGANIZE and UPDATE STATISTICS, and a
  statistics object its own UPDATE STATISTICS. Delete a column, drop a table
  with its foreign keys, move an object to another schema, or rename one. In
  Object Explorer Details, Shift+click (or Alt+click, for terminals that keep
  Shift for themselves) extends the selection and Ctrl+click picks rows out one
  at a time, over several schema-scoped objects — tables, views, procedures,
  indexes — which are then deleted in one confirmation; databases, logins and
  roles are always deleted one at a time. Every delete confirmation has a
  **Script** button that opens the DROP statements in a query window instead of
  running them.
  Filter any folder by name, schema, or creation date — the filter is pushed
  into the server's own query where it can be expressed exactly. Drag a node
  into the editor to insert its name.
- **Query editor** — as many editor+results tabs as you like, `GO` batch
  splitting, a tab per result set, and a Messages tab for `PRINT`, row
  counts, and errors. Results are never row-capped. Syntax highlighting,
  undo/redo, Find & Replace with regex, block (column) selection, and
  IntelliSense for schemas, tables, views, and columns.
- **Execution plans** — estimated or actual, as a cost-weighted operator
  graph, a tree, or raw XML. A missing-index suggestion shows as a banner
  above the plan; `m` (or a click on it) opens the `CREATE INDEX` script in
  a query window. Plans open and save as `.sqlplan`, the same files SSMS
  reads and writes. XML and JSON result cells open in their own
  syntax-highlighted viewer.
- **Properties dialogs** — editable, multi-page SSMS-style Properties for
  Server, Database, Login, Table, Schema, roles, users, indexes, keys, and
  more, plus New Database and New Login — which creates a login of any of SQL
  Server's five kinds: SQL, Windows, Microsoft Entra, or mapped to a
  certificate or an asymmetric key. Full permissions editing including
  per-column grants and an Effective Permissions view.
- **New Index and New Statistics** — a table's Indexes folder creates any
  index type SQL Server has: clustered and nonclustered (unique, included
  columns, filter, fill factor, online, partition scheme), clustered and
  nonclustered columnstore, XML (primary and secondary), and spatial with
  its tessellation, bounding box and grid. Its Statistics folder creates a
  statistics object with an ordered column list, a filter, FULLSCAN or a
  sample percentage, NORECOMPUTE and INCREMENTAL. Both offer Script Changes
  instead of running the statement. A database's Security folder creates
  Always Encrypted column master and column encryption keys from the `0x…`
  blob SSMS or the PowerShell cmdlets print.
- **Backup & Restore** — full option dialogs (destination, type, media,
  compression, point-in-time, file relocation) run as cancellable
  background tasks with live progress. Browse picks paths on the *server's*
  filesystem, with that host's own path rules.
- **Detach & Attach** — Detach on a database runs sp_detach_db with its three
  options (drop connections, update statistics, drop the full-text index
  files) and shows the file paths it leaves behind on disk. Attach on the
  Databases folder browses the *server's* filesystem for a primary `.mdf`,
  reads the database's whole file list back out of it, lets the path of any
  file that has moved be corrected, and attaches it under whatever name and
  owner you choose — or rebuilds a lost log.
- **Activity Monitor** — live dashboards over DMV data: throughput, waits,
  memory, I/O latency, and checkpoints, plus TempDB, blocking chains, and a
  Sessions tab powered by sp_WhoIsActive.
- **Log File Viewer** — SQL Server and SQL Agent error logs, current or
  archived, with filtering, a search the server runs across every archive,
  export, and Recycle.
- **Query Store** — SSMS's seven views (Regressed Queries, Overall Resource
  Consumption, Top Resource Consuming Queries, Queries With Forced Plans,
  Queries With High Variation, Query Wait Statistics, Tracked Queries) under
  each database. A leaf shows its report in Object Explorer Details; opening
  one raises the Query Store panel, where the metric, statistic, time window
  and row count are selectable, the rows are charted, and the selected
  query's plans are listed. From there: **Force Plan** / **Unforce Plan**
  (with a confirmation and a Script-to-editor alternative) and **Show Plan**,
  which opens the stored plan in the same graphical plan view an executed
  query gets.
- **SQL Server Agent** — Jobs, Schedules, Alerts, and Operators; multi-page
  Job Properties with add/remove/reorder on the step list, a step's command
  edited in the query editor itself — multi-line, SQL-highlighted, with line
  numbers — run/stop a job, and view run history.
- **Always On Availability Groups** — browse groups, replicas, databases,
  and listeners; a live dashboard with estimated data loss and recovery
  time; create groups, add replicas and databases, suspend/resume, fail
  over, and set up database mirroring endpoints across all replicas at once.
- **Authentication** — SQL Server, Windows Integrated, and Azure Entra ID
  (Default, Password, MSI, Service Principal, Interactive, Device Code,
  Azure CLI).
- **Least privilege** — goSSMS works as a login that is not `sysadmin`. A
  value it cannot read shows as `N/A` rather than `0`, a refusal names the
  right it needs rather than showing a driver error, and an action you cannot
  perform is greyed out rather than offered and then rejected. See
  [Required rights](#required-rights).

## Install

Download the binary for your platform and run it:

https://github.com/radix29/gossms/releases

Or build from source (needs Go 1.27+):

```bash
go install github.com/radix29/gossms/cmd/gossms@latest
```

You need a terminal with 256 colours and UTF-8 support (most modern ones)
and access to a SQL Server instance.

## Usage

```bash
./gossms
```

goSSMS opens the Connect to Server dialog on startup. Fill it in to connect,
or press `Escape` to work offline — `F9` reopens it any time.

## Required rights

goSSMS is a client — every button does what SQL Server lets your login do, and
nothing more. The minimum right per area, so you can tell "goSSMS can't" from
"this login can't":

| Feature | Minimum rights |
|---|---|
| Connect; browse the tree | `CONNECT SQL`; `VIEW ANY DATABASE` to see a database listed, `CONNECT` on it to expand it |
| Definitions, dependencies, `Script <object> as`; Logins and Server Roles | `VIEW DEFINITION` on the object, or `VIEW ANY DEFINITION` |
| Server Properties (read) | `public`, plus `VIEW SERVER STATE` for live figures |
| Server Properties (apply) | `ALTER SETTINGS` (`serveradmin`/`sysadmin`) |
| Database Properties (read) | `CONNECT` on the database; `VIEW DATABASE STATE` for space figures |
| Database Properties (apply), New Index, New Statistics | `ALTER` on the database or object (`db_owner`, `db_ddladmin`) |
| New Database | `CREATE ANY DATABASE` or `dbcreator` |
| New Login, Login Properties (apply) | `ALTER ANY LOGIN` (`securityadmin`) |
| Backup / Restore | `db_backupoperator` or `BACKUP DATABASE`/`BACKUP LOG` / `dbcreator` plus `db_owner` |
| Detach | `CONTROL` on the database (`db_owner`) or `ALTER ANY DATABASE` (`dbcreator`); the session count also needs `VIEW SERVER STATE` |
| Attach | `CREATE ANY DATABASE` or `dbcreator`; reading a detached file's file list also needs the rights `DBCC` needs (`sysadmin`) |
| Server filesystem picker | `public` on SQL Server 2017+; `sysadmin` on older (`xp_dirtree`) |
| Activity Monitor | `VIEW SERVER STATE`; Blocking tab also needs `CREATE PROCEDURE` in `master` |
| Log File Viewer | `public` to list; `EXECUTE` on `xp_readerrorlog` to read; `sysadmin` to Recycle |
| Query Store (read / force a plan) | `VIEW DATABASE STATE` on the database / `ALTER` on it (`db_owner`) |
| SQL Server Agent | `msdb` access plus `SQLAgentUserRole`; `SQLAgentReaderRole` for others' jobs, `SQLAgentOperatorRole` to start/stop them |
| Always On (browse / change) | `VIEW ANY DEFINITION` + `VIEW SERVER STATE` / `ALTER ANY AVAILABILITY GROUP` + `ALTER ANY ENDPOINT` |
| Query editor | whatever the query itself needs — goSSMS adds nothing |

On SQL Server 2022+ `VIEW SERVER STATE` is split into `VIEW SERVER PERFORMANCE
STATE` and `VIEW SERVER SECURITY STATE`; either narrower grant covers its half.

How a missing right shows up:

- An unreadable value is `N/A`, never `0` and never a number taken over only the
  visible rows. A page opens if its main reads succeed; a grid built from a
  filtered catalog view says so at the top.
- A database you cannot open expands to a single *Access denied* line, and a
  refused read names the right it needs — *Requires SELECT on
  msdb.dbo.sysjobservers* — rather than the raw driver error.
- Where SQL Server itself will not distinguish missing from invisible
  (*"does not exist or you do not have permission"*), goSSMS doesn't either.
- An action you lack rights for is withheld, not offered then refused: the menu
  item is greyed and names the permission, and a Properties page that could not
  save opens read-only with **Close / Script Changes** in place of OK/Apply.

The check is one-sided — an action is withheld only when the server answered
"no" to *every* right that would permit it, so anything unprobed stays offered.
A grant made directly on one object counts alongside the wider rights, so a
login granted ALTER on a single table keeps that table's write actions. For the
limits that remain, see [docs/open-threads.md](docs/open-threads.md)
§ Permission gating.

## Configuration

Successful connections are saved automatically (most recent first, up to
15); type 4+ characters in the Server field to look one up. Tools > Options
sets the tree icon style, the results grid's default cell width, and
IntelliSense on/off.

Settings live in `~/.config/gossms/config.json` (Linux/macOS) or
`%APPDATA%\gossms\config.json` (Windows). Saved passwords are encrypted
(AES-256-GCM) with a key in `gossms.key` alongside it, sealed against the
server, login, and auth method they belong to. Delete either file to reset
all saved connections.

## Known Issues

Environment and distribution:

- Entra authentication untested — no infrastructure available
- Not tested on macOS yet — no Mac available
- Release binaries are unsigned; checksums are provided

Functional gaps:

- Reports (server- and database-level) aren't built yet

## Contributing

The codebase is still unstable and refactoring often, so I'm not accepting
pull requests yet — please open an issue instead. For the internal package
layout and design rationale, see [ARCHITECTURE.md](ARCHITECTURE.md).

## Acknowledgements

The Activity Monitor's Sessions tab runs **sp_WhoIsActive**, Adam Machanic's
activity-monitoring procedure — https://github.com/amachanic/sp_whoisactive —
with thanks. goSSMS carries a copy in `internal/activity/whoisactive.sql` so
it can install one on a server that hasn't got it.

## License

goSSMS is © 2026 radix29 and licensed **GPL-3.0-or-later**; see `LICENSE`
for the full text.

    goSSMS is free software: you can redistribute it and/or modify it under
    the terms of the GNU General Public License as published by the Free
    Software Foundation, either version 3 of the License, or (at your
    option) any later version.

    goSSMS is distributed in the hope that it will be useful, but WITHOUT
    ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
    FITNESS FOR A PARTICULAR PURPOSE. See the GNU General Public License
    for more details.

The bundled copy of sp_WhoIsActive is © 2007-2026 Adam Machanic and licensed
**GPL-3.0** under its own copyright, whose text is carried alongside it as
`internal/activity/LICENSE.sp_whoisactive`. It is used unmodified apart from
the two changes its own header records — the release script's SET-options and
stub-CREATE batches removed, and its declaration rewritten at install time so
the tempdb copy can be named `usp_WhoIsActive`. It is aggregated with goSSMS,
not derived from it; its copyright stays with its author.
