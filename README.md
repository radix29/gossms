# goSSMS

**goSSMS** is a cross-platform, console-based SQL Server Management Studio clone written in Go. 

It runs entirely in the terminal — no GUI, no X11, no CGO, no installation — and works on Linux, macOS, and Windows.
It is just one executable file without dependencies.
It requires no SQL client tools or SQL Server drivers to be installed — connectivity is pure Go.

Open Source SQL Serever Management Studio for Linux, macOS and Windows.

![Demo](demo.gif)


## Known Issues

- Windows 10 terminal (PowerShell, cmd) double character inputs
- Some Linux terminals (e.g. xfce4-terminal) eat some key shortcuts
- Windows and Entra authentication not tested at the moment — no infrastructure available to test against
- Not tested on macOS yet — no Mac available

## Features

- **Object Explorer** — full server/database/table/view/proc/function/trigger/sequence/synonym/security tree; system databases (master, tempdb, model, msdb) are grouped under their own "System Databases" folder, matching SSMS, with system views/procs/functions similarly split into their own folders under Views/Programmability. Drag a database or object node into a query editor to insert its quoted T-SQL name (schema-qualified for tables/views/procs/functions/sequences/synonyms/triggers). A database node's context menu also has Take Database Offline / Bring Database Online.
- **Configurable tree icons** — Emoji (default), Symbols, Portable, or None, picked from Tools > Options and saved to `config.json`
- **Multiple Query Panels** — open as many T-SQL editor+results panels as you need, switched by tab
- **SSMS-style query execution** — scripts are split on `GO` (all batches share one connection, so temp tables and `SET` options survive), every result set gets its own tab, and a Messages tab collects `PRINT` output, "(n rows affected)" counts, and errors
- **Database context per query window** — New Query on a database (or Select Top 1000 Rows on its tables/views) runs in that database, not the login's default
- **Detail Browser** — shows a detail grid for the selected tree node; the Server node, and the Databases/Logins/Tables folders, load progressively — fast columns (name, state, recovery model, …) appear as soon as the cheap list query returns, then row count/size/available-memory/disk-volume figures backfill concurrently, one round trip per row, as they complete
- **Resizable splitters** — drag or keyboard-resize the explorer width and editor/results split
- **Toolbar** — icon-only quick-action row sharing the menu bar's line, right-aligned (New Query, Execute, Execute Selection, Stop Execution, Estimated Plan, Actual Plan toggle, Activity Monitor); hover shows a tooltip styled like SSMS's query-status bar
- **Context-gated menu bar and toolbar** — items that don't apply to the current selection or state (e.g. Disconnect with nothing selected, Cancel Executing Query with nothing running, Save with no query panel active) grey out and refuse both keyboard and mouse activation instead of silently doing nothing
- **SQL editor** — syntax highlighting (keywords, strings, comments, numbers), optional word-wrap, line duplicate/move/indent/comment, word-aware navigation and deletion, undo/redo, and `Ctrl+Enter` T-SQL statement selection (splits on `;` and `GO` batches, skipping comments/strings)
- **IntelliSense (autocomplete)** — suggests schemas, tables, views, and columns as you type (opens once a word starts — a letter or `[` — so a bare space, `.`, digit, or blank line never pops it open on its own), or on demand with `Ctrl+Space`; understands `schema.`/`alias.` dot-qualifiers, unterminated `[bracket` identifiers, and FROM-clause alias resolution against the whole current statement, not just what's typed before the cursor — so `SELECT |` above a `FROM Customers c` typed later in the same statement still resolves. Statement boundaries are recognised at `;`, a bare `GO` line, and a new top-level `SELECT`/`INSERT`/`UPDATE`/`DELETE`/`MERGE`/`WITH` — so several ad hoc queries stacked in the editor with no `;` between them (SSMS never requires one) don't leak each other's columns, while `UNION`/`EXCEPT`/`INTERSECT`, a CTE's own main query, and `INSERT ... SELECT` are correctly kept as one statement. The table/column inventory is fetched once per opened database (not persisted), shared by every query panel on that database, and reloaded with `Ctrl+R`; the `sys` schema's catalog views (`sys.tables`, `sys.columns`, `sys.objects`, ...) are fetched once per server connection instead, since they're identical in every database (`Ctrl+R` retries that one only if its load failed). Enabled by default — toggle it off in Tools > Options
- **Results grid status bar** — SSMS-style bar under each results tab showing elapsed time, selected row/column, and row count (live-updating while a query executes)
- **Execution Plan Viewer** — Query > Estimated Execution Plan, or toggle Include Actual Execution Plan on before running a query; a Plan tab renders the operator graph (cost-weighted icons, a draggable Properties strip for the selected operator), a Tree tab gives an expandable operator tree plus a summary grid, and an XML tab shows the raw ShowPlanXML — any of them can be popped into their own closable panel via the tab's Expand button
- **Modal dialogs** — Connect (with a read-only connection-string preview), Options, Help, Key Diagnostics (shows tcell's decoded Key/Modifiers/rune for every keypress — useful for diagnosing a terminal that isn't delivering an expected shortcut), Background Tasks, Status History (a timestamped, capped log of every status-bar message), Check for Updates (compares the running version against the latest GitHub Release), About
- **Server / Database / Login / Table / Schema / Database Role / Database User Properties** — multi-page, editable SSMS-style properties dialogs (page list on the left, OK/Cancel/Apply/Script Changes below); each page loads asynchronously the first time it's shown, F5 refreshes the current page, edited values are cached only while the dialog is open, any value can be copied to the clipboard, and Script Changes opens the SQL for the pending edits in a new query window instead of running it
- **New Database / New Login dialogs** — same multi-page form pattern as Properties, but building a `CREATE DATABASE`/`CREATE LOGIN` statement from scratch instead of diffing an existing object
- **Backup Database / Restore Database dialogs** — full SSMS-style option forms (destination, backup type, media, compression, restore source and point-in-time/history browsing); both run as cancellable background tasks with live progress
- **File browser dialog** — used for every file path prompt (Open, Save, Save As, Results To File): directory listing, up/new folder, overwrite confirmation
- **Context menus** — right-click any tree node for contextual actions, or `Shift+F10`/the `Menu` key for the keyboard equivalent
- **Show Value** — right-click any results-grid cell (query results, Detail Browser, property-page grids) for a "Show Value" popup: the full, untruncated cell text in a read-only editor you can navigate, select (keyboard or mouse), and copy
- **Full authentication support** via [gosmo](https://github.com/radix29/gosmo):
  - SQL Server Authentication
  - Windows Integrated Authentication
  - Azure Entra ID (Default, Password, MSI, Service Principal, Interactive, Device Code, Azure CLI)
- **Script objects** — Script Table/View/Proc/Function as CREATE or DROP into a new query window
- **Background tasks** — Back Up Database, Restore Database, and Rebuild All Indexes run as cancellable background tasks (Tools > Background Tasks shows progress, including live percent-complete for backups, and lets you cancel one)
- **Object Dependencies** — View Dependencies on a table/view/procedure lists what it depends on and what depends on it
- **Cross-platform** — Linux, macOS, Windows (pure Go, no CGO)

## Future Plans

- **Activity Monitor** — SSMS's live view of current sessions, blocking chains, and resource waits
- **Reports** — a handful of the most useful built-in SSMS reports, not the full set
- **Always On Availability Groups (AAG)** — viewing and managing availability group topology and health

## Prerequisites

- Go 1.26 or later
- Access to a SQL Server instance (local, Azure SQL, or remote for testing)
- A terminal emulator that supports 256 colours (virtually all modern ones do)
- A terminal emulator that supports UTF-8 (most modern ones do) — goSSMS works without it, but tree icons and box-drawing characters won't render correctly

## Installation

`gosmo` is a regular tagged dependency (`github.com/radix29/gosmo v0.0.5`)
— no local sibling checkout required:

```bash
git clone https://github.com/radix29/gossms.git
cd gossms
go build -o gossms ./cmd/gossms

# Or install directly
go install github.com/radix29/gossms/cmd/gossms@latest
```

`go.mod` keeps a commented-out `replace github.com/radix29/gosmo => ../gosmo`
directive for local development against an unpublished gosmo checkout —
uncomment it (and the matching `ignore ../gosmo` line) if you need to work
on both repos together. When it's active, make sure `../gosmo` actually
exists as a sibling checkout, or `go build` will fail to resolve it.

## Usage

```bash
./gossms
```

On first launch the screen is empty. Press **Ctrl+Shift+O** or use **File → Connect** to open the connection dialog.

## Keyboard Reference

| Key | Action |
|-----|--------|
| `F1` | Help |
| `F10` | Activate menu bar (Left/Right switch menus, Up/Down select item, Enter activates) |
| `Ctrl+Q` | Quit |
| `Ctrl+Shift+O` | Connect to server (on terminals that can't distinguish Shift on a Ctrl+letter combo, this behaves like plain `Ctrl+O` instead — see Open, below) |
| `Ctrl+O` | Open a `.sql` file as a new query |
| `Ctrl+N` | New query panel |
| `Ctrl+W` | Close active query |
| `Ctrl+S` | Save query |
| `Ctrl+C` / `Ctrl+X` / `Ctrl+V` | Copy / cut / paste (works in the query editor and any dialog text field) |
| `Tab` | Switch focus explorer ↔ panels |
| `Ctrl+Tab` / `Ctrl+Shift+Tab` | Cycle to next / previous panel |
| `F5` | Execute query — runs only the selected text if there is a selection, otherwise the whole query. Also refreshes the selected Object Explorer node, or the current page of an open Properties dialog. |
| `Ctrl+Enter` | Select the T-SQL statement at the cursor — boundaries are `;`, a `GO` batch separator, and a top-level `SELECT`/`INSERT`/`UPDATE`/`DELETE`/`MERGE`/`WITH` (so stacked ad hoc statements with no `;` between them still split correctly; `UNION`/`EXCEPT`/`INTERSECT` chains, a CTE's own main query, and `INSERT ... SELECT` stay one statement) — does not execute it. Only reported as distinct from plain `Enter` on terminals with a modern keyboard protocol; elsewhere use Query > Execute at Cursor instead. |
| `Ctrl+Left` | Narrow object explorer |
| `Ctrl+Right` | Widen object explorer |
| `Ctrl+Up` | Grow query editor (shrink results) |
| `Ctrl+Down` | Shrink query editor (grow results) |
| `Ctrl+PgUp` / `Ctrl+PgDn` | Previous / next result tab (result grids and Messages); tabs are also clickable |
| `Ctrl+Z` / `Ctrl+Y` | Undo / Redo in editor |
| `Ctrl+Space` (query editor) | Open/force IntelliSense suggestions — auto-completes immediately if there's exactly one match. Elsewhere (or in an editor with no completion provider), opens the Cut/Copy/Paste context menu instead |
| `Tab` / `Enter` (suggestions open) | Accept the selected suggestion |
| `Escape` (suggestions open) | Dismiss — won't reopen for that word until the cursor moves off it |
| `Ctrl+R` (query editor) | Refresh the cached table/column list for the panel's database |
| `Shift+Arrow` | Select text (F5 then runs only the selection). `Shift+Up`/`Shift+Down` extend from the cursor to the end/start of the line and on to the same column on the next/previous line — Notepad++/Scintilla-style, remembering that column across shorter in-between lines |
| Click + drag | Select text with the mouse (editor and dialog fields) |
| Mouse wheel (results grid) | Scroll rows. `Shift+wheel`, or a wheel/trackpad that reports `WheelLeft`/`WheelRight` directly, scrolls columns instead |
| Arrow keys | Navigate tree / grid |
| `Enter` / `+` | Expand tree node |
| `-` / `Backspace` | Collapse tree node |
| `Shift+F10` / `Menu` key | Open the selected tree node's context menu (keyboard equivalent of right-click) |
| Right-click (grid cell) | "Show Value" — open the full cell text in a read-only, selectable, copyable popup |

## Architecture

goSSMS is split into an embeddable, application-agnostic TUI library
(`internal/tuikit`) and a thin application layer (`internal/tui`) that wires
it together with SQL Server domain logic via `gosmo`. See
[`internal/tuikit/README.md`](internal/tuikit/README.md) for the library's
design principles and dependency rules.

```
gossms/
├── cmd/
│   ├── gossms/               # main entry point
│   └── plandemo/             # dev harness: hosts planview.PlanView full-screen against a plan file (not part of the release build)
├── internal/
│   ├── config/              # connection profiles (JSON, in $XDG_CONFIG_HOME/gossms/)
│   ├── db/                  # gosmo connection wrapper + DSN builder
│   ├── query/               # SSMS-style script executor: GO batches, result sets, message stream, plan capture
│   ├── showplan/            # parses ShowPlanXML (estimated/actual) into a navigable operator tree; no TUI/DB deps
│   ├── version/             # gossms's own version metadata (mirrors gosmo/version); overridable via -ldflags -X
│   │
│   ├── tuikit/               # embeddable TUI library (no SQL Server / app knowledge) — see internal/tuikit/README.md
│   │   ├── theme/                # colour palette + derived tcell.Style helpers
│   │   ├── core/                 # Rect geometry, drawing primitives, string/int helpers
│   │   ├── widgets/               # InputField, DropDown, CheckBox, Button, RadioBox
│   │   ├── layout/                # Panel interface, PanelManager (tabs), Splitter
│   │   ├── dialogs/                # ModalDialog base (focus trap), Properties/Alert/Confirm/FileDialog
│   │   ├── controls/                # MenuBar, ContextMenu, Toolbar, TabStrip, TreeView, DataGrid, ListBox, Editor (+SQL highlighter)
│   │   └── propsheet/               # PropertySheet — multi-page editable properties dialog framework
│   │
│   └── tui/                  # goSSMS application layer (built on tuikit)
│       ├── planview/             # reusable control rendering a parsed plan: Plan (graph)/Tree/XML tabs
│       │
│       ├── app.go                # root App orchestrator, event loop, SQL Server object tree fetch
│       ├── app_events.go         # key/mouse dispatch, resize/redraw, top-level event loop plumbing
│       ├── app_connections.go    # connect/disconnect lifecycle, saved-connection bookkeeping, selectedServerConn helper
│       ├── app_explorer_data.go  # background fetch orchestration, context menus, Script object, View Dependencies, Back Up Database/Take-Bring Offline-Online/Rebuild All Indexes task consumers
│       ├── app_panel_actions.go  # panel-level actions: new/open/save/close query, execute/cancel query, launch Properties/New Database/New Login dialogs
│       ├── dialog_stack.go       # z-ordered Dialog stack: draw/input routing for every modal dialog
│       ├── menu.go               # top menu bar structure (File/Edit/View/Query/Tools/Help), context-gated via each MenuItem's Enabled predicate, + About dialog
│       ├── toolbar.go            # icon-only quick-action toolbar sharing the menu bar's row, same Enabled-predicate gating
│       ├── tree_node.go          # NodeType enum + style-aware icon lookup (Emoji/Symbols/Portable/None) + name lookup
│       ├── object_explorer.go    # owns the SQL Server tree model; drives controls.TreeView
│       ├── explorer_loaders.go   # childLoader registry (NodeType → fetch func) + shared loader helpers
│       ├── explorer_databases.go # loaders: server root, Databases/System Databases, one database's folders
│       ├── explorer_objects.go   # loaders: Tables/Views/Procs/Functions/Triggers/Sequences/Synonyms + System Views/Procedures/Functions folders + table columns
│       ├── explorer_security.go  # loaders: server Security folder — Logins, Server Roles
│       ├── explorer_management.go # loaders: Server Objects folder — Agent Jobs, Linked Servers
│       ├── explorer_drag.go      # drag a tree node into a query editor as a quoted T-SQL identifier
│       ├── tasks.go              # background task registry: Task (progress/cancel), App start/postProgress/postTaskDone
│       ├── clipboard.go          # copy/cut/paste plumbing shared by editor and dialog text fields
│       ├── os_clipboard.go       # OS-native clipboard, shelled out per-platform (fallback path for clipboard.go)
│       │
│       ├── query_panel.go        # QueryPanel state/layout, implements layout.Panel
│       ├── query_panel_exec.go   # Execute/Execute Selection/Cancel, plan-capture wiring
│       ├── query_panel_tabs.go   # result-set tabs + Messages tab
│       ├── query_panel_plan.go   # Estimated/Actual Execution Plan tabs, backed by planview.PlanView
│       ├── query_panel_export.go # Results To File (Text/Grid/File modes)
│       ├── plan_panel.go         # pops an Execution Plan tab out into its own closable panel
│       ├── completion_inventory.go  # per-database + per-server(sys schema) catalog cache for IntelliSense, async load
│       ├── completion_tokenizer.go  # SQL tokenizer feeding the completion resolver
│       ├── completion_scope.go      # FROM-clause/alias resolution and statement-boundary detection
│       ├── completion_candidates.go # schema/table/column candidate lookup against the cached inventory
│       │
│       ├── detail_browser.go            # Detail Browser, implements layout.Panel
│       ├── detail_browser_server.go     # Server node: version/edition/paths/CPU/memory, then NUMA + disk volumes
│       ├── detail_browser_databases.go  # Databases folder: name/state/recovery, then per-database size backfill
│       ├── detail_browser_logins.go     # Logins folder
│       ├── detail_browser_tables.go     # Tables folder: name, then per-table row count/space backfill
│       │
│       ├── connect_dialog.go     # Connect dialog — form + saved-connection autocomplete + conn-string preview
│       ├── options_dialog.go     # Tools > Options — icon style, cell/row limits, IntelliSense on/off, saved to config.json
│       ├── query_list_dialog.go  # Tools > Query List — switch between open query panels
│       ├── tasks_dialog.go       # Tools > Background Tasks — live task list + Cancel
│       ├── help_dialog.go        # F1 help modal (embeds dialogs.ModalDialog)
│       ├── key_diagnostics_dialog.go # Help > Key Diagnostics — shows tcell's decoded Key/Modifiers/rune per keypress
│       ├── status_history_dialog.go  # running, timestamped, capped log of status-bar messages
│       ├── update_check.go       # Help > Check for Updates — GitHub releases API + semver compare
│       ├── update_dialog.go      # UpdateDialog — shows installed vs. latest release
│       ├── properties_dialog.go  # About + Object Dependencies (wraps dialogs.PropertiesDialog, the flat viewer)
│       │
│       ├── prop_dialog.go        # PropDialog — app orchestration for propsheet.PropertySheet (async loads, Apply)
│       ├── prop_grid_helpers.go  # permissions Grant/Deny/Revoke cycling + shared small helpers (boolStr, indexOf, ...)
│       ├── server_props.go       # Server Properties page definitions
│       ├── database_props.go               # Database Properties: General/Owner page definitions
│       ├── database_props_files.go          # Database Properties > Files page
│       ├── database_props_filegroups.go     # Database Properties > Filegroups page
│       ├── database_props_options.go        # Database Properties > Options page
│       ├── database_props_permissions.go    # Database Properties > Permissions page
│       ├── database_props_query_store.go    # Database Properties > Query Store page
│       ├── database_props_scoped_config.go  # Database Properties > Scoped Configuration page
│       ├── database_props_change_tracking.go # Database Properties > Change Tracking page
│       ├── login_props.go        # Login Properties page definitions
│       ├── table_props.go        # Table Properties page definitions
│       ├── schema_props.go       # Schema Properties page definitions
│       ├── role_props.go         # Database Role Properties page definitions
│       ├── user_props.go         # Database User Properties page definitions
│       │
│       ├── new_database_dialog.go # New Database dialog — builds and runs CREATE DATABASE
│       ├── new_database_pages.go  # New Database's page definitions
│       ├── new_login_dialog.go    # New Login dialog — builds and runs CREATE LOGIN
│       ├── new_login_pages.go     # New Login's page definitions
│       │
│       ├── backup_common.go      # helpers shared by the Backup and Restore dialogs
│       ├── backup_dialog.go      # Back Up Database dialog — options form + in-place progress
│       ├── restore_dialog.go     # Restore Database dialog — options form, backup-set inspection
│       └── restore_dialog_ops.go # Restore Database's background-task execution + history/file-list lookups
```

### Adding a new dialog

Construct it in `App.buildUI` and append it to `a.allDialogs` — that's the
only App-level change needed. `dialog_stack.go`'s `syncDialogStack` notices
it the moment its own `Show()` (or `Prompt()`/`ShowXxx()`) flips it
visible, pushes it to the top of the z-order, and routes it all input
until it closes itself; draw order, key routing, and mouse routing all
follow from the stack without touching `app.go` or `app_events.go` again.

### Why split this way

`tuikit` contains every piece of rendering, focus, scrolling, and
drag/resize logic exactly once. None of it knows what a "database" or
"stored procedure" is — it operates on generic `Rect`s, `TreeNode`s with an
`any` `Tag` field, and string/string row data. The `tui` package never
re-implements widget mechanics; it only supplies SQL-Server-specific data
and callbacks (`OnExpand`, `OnSelect`, button `Action`s).

This means `tuikit` could be extracted into its own module and reused by a
completely different tcell application without modification.

## Dependencies

| Package | Purpose |
|---------|---------|
| [github.com/gdamore/tcell/v3](https://github.com/gdamore/tcell) | Terminal UI rendering, keyboard & mouse events |
| [github.com/radix29/gosmo](https://github.com/radix29/gosmo) | SQL Server management objects (databases, tables, scripts…) |

## Configuration

A successful connection is saved automatically — there's no "Save As" step.
Each saved profile is named `server,port,database,user` and the list is
capped at the 15 most recently used (config.MaxSavedConnections), oldest
evicted first. In the Connect dialog, typing 4+ characters into the Server
field looks up saved profiles by server-name prefix and shows matches in a
list below it; arrow keys + Enter or a click fills the whole form from the
selected one.

Tools > Options sets the Object Explorer tree's icon style — Emoji
(default), Symbols, Portable, or None — the query results grid's max cell
length and max result rows, and whether the SQL editor's IntelliSense is
enabled (default: on) — all saved to the config file and applied
immediately.

Connection profiles and these settings live in:

- **Linux/macOS**: `~/.config/gossms/config.json`  
- **Windows**: `%APPDATA%\gossms\config.json`

The config file is human-readable JSON, except saved passwords, which are
AES-256-GCM encrypted and base64-encoded — the random encryption key lives
in a separate `gossms.key` file (mode `0600`) alongside `config.json`.
Delete either file to reset all saved connections.

## Contributing

The codebase is currently unstable and going through regular refactoring,
so I'm not accepting pull requests at this time — please open an issue
instead. I'll start accepting PRs once the project reaches a released,
more stable state. In the near future I'm planning to update the project
regularly.


## License

MIT
