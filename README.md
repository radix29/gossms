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
  statistics object its own UPDATE STATISTICS. Filter any folder by name,
  schema, or creation date. Drag a node into the editor to insert its name.
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
  instead of running the statement.
- **Backup & Restore** — full option dialogs (destination, type, media,
  compression, point-in-time, file relocation) run as cancellable
  background tasks with live progress. Browse picks paths on the *server's*
  filesystem, with that host's own path rules.
- **Activity Monitor** — live dashboards over DMV data: throughput, waits,
  memory, I/O latency, and checkpoints, plus TempDB, blocking chains, and a
  Sessions tab powered by sp_WhoIsActive.
- **Log File Viewer** — SQL Server and SQL Agent error logs, current or
  archived, with filtering and export.
- **SQL Server Agent** — Jobs, Schedules, Alerts, and Operators; multi-page
  Job Properties, run/stop a job, and view run history.
- **Always On Availability Groups** — browse groups, replicas, databases,
  and listeners; a live dashboard with estimated data loss and recovery
  time; create groups, add replicas and databases, suspend/resume, fail
  over, and set up database mirroring endpoints across all replicas at once.
- **Authentication** — SQL Server, Windows Integrated, and Azure Entra ID
  (Default, Password, MSI, Service Principal, Interactive, Device Code,
  Azure CLI).

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
| `F9` | Connect to server (`Ctrl+Shift+O` too, where the terminal can encode it) |
| `Ctrl+N` / `Ctrl+O` / `Ctrl+S` / `Ctrl+W` | New / open / save / close query (`Ctrl+O` also opens a `.sqlplan`) |
| `1` `2` `3`, `[` `]`, `m` (plan) | Graph / tree / XML, previous / next statement, missing-index details |
| `F5` | Execute query (selection if any); also refreshes a tree node or Properties page |
| `Ctrl+Enter` | Select the statement at the cursor without executing |
| `Tab` | Switch focus explorer ↔ panels |
| `Ctrl+Tab` / `Ctrl+Shift+Tab` | Next / previous panel |
| `Ctrl+PgUp` / `Ctrl+PgDn` | Previous / next result tab |
| `Ctrl+Left` / `Right` | Narrow / widen Object Explorer |
| `Ctrl+Up` / `Down` | Grow / shrink the query editor |
| `Ctrl+C` / `X` / `V` / `Z` / `Y` | Copy / cut / paste / undo / redo |
| `Ctrl+Z` (Properties) | Revert the current page to the values it loaded with |
| `Ctrl+F`, `F3` / `Shift+F3` | Find & Replace, find next / previous |
| `Ctrl+F3` | Find next occurrence of the word at the cursor |
| `Ctrl+Space` / `Ctrl+R` | IntelliSense suggestions / refresh its cache |
| `Shift+Arrow`, `Alt+Shift+Arrow` | Select text, block (column) select |
| `Enter` / `+` / `-` / `Backspace` | Expand / collapse tree node |
| `Shift+F10` / `Menu` key | Context menu for the selected tree node |
| Right-click a grid cell | "Show Value" — full cell text, copyable |
| Drag / double-click a header separator | Resize / reset a grid column |

Replace is Edit > Replace... or the Replace fields in the Find dialog.
`Ctrl+H` is deliberately unbound — many terminals send it as the same byte
as Backspace.

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
