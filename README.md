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
  procedures, functions, triggers, sequences, synonyms, security (users,
  roles, schemas, row-level security policies, Always Encrypted keys),
  storage (partition functions and schemes), and Management. Right-click anything to rename, delete, see dependencies, or
  open Properties. **Script <object> as** covers every family the tree
  shows — CREATE, ALTER, DROP, DROP And CREATE, and the SELECT/INSERT/
  UPDATE/DELETE/EXECUTE templates — to a new query window, a file, or the
  clipboard; an index adds REBUILD, REORGANIZE and UPDATE STATISTICS, and a
  statistics object its own UPDATE STATISTICS. Delete a column, drop a table
  with its foreign keys, move an object to another schema, or rename one.
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
  more, plus New Database and New Login. Full permissions editing including
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
- **Activity Monitor** — live dashboards over DMV data: throughput, waits,
  memory, I/O latency, and checkpoints, plus TempDB, blocking chains, and a
  Sessions tab powered by sp_WhoIsActive.
- **Log File Viewer** — SQL Server and SQL Agent error logs, current or
  archived, with filtering, a search the server runs across every archive,
  export, and Recycle.
- **SQL Server Agent** — Jobs, Schedules, Alerts, and Operators; multi-page
  Job Properties with add/remove/reorder on the step list, run/stop a job, and
  view run history.
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

## Keyboard Reference

| Key | Action |
|-----|--------|
| `F1` / `F10` / `Ctrl+Q` | Help / menu bar / quit |
| `F9` | Connect to server |
| `Ctrl+N` / `Ctrl+O` / `Ctrl+S` / `Ctrl+W` | New / open / save / close query |
| `F5` | Execute query (selection if any); also refreshes a tree node or Properties page |
| `Ctrl+Enter` | Select the statement at the cursor without executing |
| `Tab` | Switch focus explorer ↔ panels |
| `Ctrl+Tab` / `Ctrl+Shift+Tab` | Cycle focus forward / back (explorer, editor, results) |
| `Ctrl+Shift+Right` / `Left` | Next / previous panel |
| `Ctrl+0`..`9` | Jump to panel N from the left (Object Explorer Details is 0) |
| `Ctrl+PgUp` / `Ctrl+PgDn` | Previous / next result tab |
| `Ctrl+Left` / `Right` | Narrow / widen Object Explorer |
| `Ctrl+Up` / `Down` | Grow / shrink the query editor |
| `Ctrl+C` / `X` / `V` / `Z` / `Y` | Copy / cut / paste / undo / redo |
| `Ctrl+Z` (Properties) | Revert the current page to the values it loaded with |
| `Ctrl+F`, `F3` / `Shift+F3` | Find & Replace, find next / previous |
| `Ctrl+F3` | Find next occurrence of the word at the cursor |
| `Ctrl+D` / `Ctrl+L` | Duplicate / delete the current line |
| `Ctrl+/` | Comment or uncomment the line |
| `Ctrl+Space` / `Ctrl+R` | IntelliSense suggestions / refresh its cache |
| `Shift+Arrow`, `Alt+Shift+Arrow` | Select text, block (column) select |
| `Enter` / `+` / `-` / `Backspace` | Expand / collapse tree node |
| `Shift+F10` / `Menu` key | Context menu for the selected tree node |
| Right-click a grid cell | "Show Value" — full cell text, copyable |
| Drag / double-click a header separator | Resize / reset a grid column |

Replace is Edit > Replace... or the Replace fields in the Find dialog.
`Ctrl+H` is deliberately unbound — many terminals send it as the same byte
as Backspace. `Ctrl+Shift+O` also opens Connect, but only on terminals with a
modern keyboard protocol; `F9` is the binding that works everywhere. Help >
Key Diagnostics shows what your terminal actually sent for any keypress.

## Required rights

goSSMS is a client — every button does what SQL Server lets the login you
connected with do, and nothing more. This table is the minimum right for each
area, so you can tell "goSSMS can't" from "this login can't".

| Feature | Minimum rights |
|---|---|
| Connect; browse the Object Explorer tree | `CONNECT SQL` (every login has it) |
| See a database listed | `VIEW ANY DATABASE` (granted to `public` by default) |
| Expand a database's folders; size and space figures | `CONNECT` on that database |
| Object definitions, dependencies, `Script <object> as` | `VIEW DEFINITION` on the object, or `db_owner`, or `VIEW ANY DEFINITION` |
| Security > Logins, Server Roles | `VIEW ANY DEFINITION` — without it you see only your own login |
| Server Properties: General, Memory, Processors, Advanced (read) | `public` (`sys.configurations`) plus `VIEW SERVER STATE` for the live figures |
| Server Properties: applying a change | `ALTER SETTINGS` (`serveradmin` or `sysadmin`) |
| Server Properties > Permissions (read) | `VIEW ANY DEFINITION`; without it only your own grants are visible |
| Database Properties (read) | `CONNECT` on the database; `VIEW DATABASE STATE` for space figures |
| Database Properties (apply), New Index, New Statistics | `ALTER` on the database or the object (`db_owner`, `db_ddladmin`) |
| New Database | `CREATE ANY DATABASE` or `dbcreator` |
| New Login, Login Properties (apply) | `ALTER ANY LOGIN` (`securityadmin`) |
| Backup | `db_backupoperator`, or `BACKUP DATABASE`/`BACKUP LOG` on the database |
| Restore | `dbcreator` plus `db_owner`, or `sysadmin` |
| Browse the *server's* filesystem (Backup/Restore path picker) | `public` on SQL Server 2017+; `sysadmin` on older instances, which fall back to `xp_dirtree` |
| Activity Monitor — all dashboards, TempDB, Sessions | `VIEW SERVER STATE` |
| Activity Monitor — Blocking tab helper install | `CREATE PROCEDURE` in `master` (effectively `sysadmin`) |
| Log File Viewer — list the logs | `public` (`sp_enumerrorlogs`) |
| Log File Viewer — read a log | `EXECUTE` on `xp_readerrorlog` (`securityadmin` or `sysadmin`) |
| Log File Viewer — Recycle | `sysadmin` |
| SQL Server Agent — Jobs, Schedules, Alerts, Operators | access to `msdb` plus `SQLAgentUserRole`; `SQLAgentReaderRole` to see other owners' jobs |
| SQL Server Agent — start/stop a job | `SQLAgentOperatorRole` (`SQLAgentUserRole` covers your own jobs) |
| Always On — browse groups, replicas, dashboard | `VIEW ANY DEFINITION` plus `VIEW SERVER STATE` |
| Always On — create, add replica, fail over, endpoints | `ALTER ANY AVAILABILITY GROUP` and `ALTER ANY ENDPOINT`, or `CONTROL SERVER` |
| Query editor | whatever the query itself needs — goSSMS adds nothing |

On SQL Server 2022 and later `VIEW SERVER STATE` is split into
`VIEW SERVER PERFORMANCE STATE` and `VIEW SERVER SECURITY STATE`; either of the
narrower grants covers the corresponding half, and a denial names the narrow
permission rather than the old one.

A value this login cannot read shows as `N/A` — CPU count and memory without
`VIEW SERVER STATE`, for instance, or a database's user count without
`VIEW DEFINITION`. It never shows as `0`, and never as a smaller number taken
over the rows the login happens to be able to see. A page whose main reads
succeed opens even when one of them was refused; a grid built from a
visibility-filtered catalog view says so at the top.

A database this login cannot open keeps its place in Object Explorer but
expands to a single *Access denied* line rather than nine folders that each
fail on their own. A read refused on permission grounds is shown as the right
it needs — *Access denied — Requires SELECT on msdb.dbo.sysjobservers.* —
rather than the wrapped driver error, and a failed action keeps its own text
with that sentence appended.

Where SQL Server itself will not say whether an object is missing or merely
invisible — *"does not exist or you do not have permission"* — goSSMS does not
say either, and never calls it access denied. On a server whose messages are
not in English the right cannot be read out of the wording, and the server's
own sentence is shown unchanged.

An action you do not have the rights for is withheld rather than offered and
then refused: the menu item is greyed and names the permission it wants, and a
Properties page whose writes would be rejected opens read-only, with the
required right at the top and **Close / Script Changes** in place of
OK/Apply — so you can still generate the statements for someone who can run
them.

The check is deliberately one-sided. An action is withheld only when the
server has answered "no" to *every* right that would permit it; anything not
measured leaves the action offered, so a connection that could not be probed
behaves exactly as it did before. Some writes are still ungated because no
permission the probe can read decides them — SQL Agent's New Job/Schedule/
Alert/Operator among them; see
[docs/open-threads.md](docs/open-threads.md) § Permission gating, and
[docs/permissions-plan.md](docs/permissions-plan.md) for the whole design.

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

- Some terminals (e.g. xfce4-terminal) eat some key shortcuts
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
