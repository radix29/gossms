# goSSMS

**goSSMS** is a cross-platform, console-based SQL Server Management Studio clone written in Go.  
It runs entirely in the terminal — no GUI, no X11, no CGO — and works on Linux, macOS, and Windows.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ File  Edit  View  Query  Tools  Help                                        │
├────────────────────┬────────────────────────────────────────────────────────┤
│ Object Explorer    │  [Query 1] [Object Explorer Details] [v]               │
│                    ├────────────────────────────────────────────────────────┤
│ [-] S myserver     │  SELECT TOP 1000 *                                     │
│   [-] D Databases  │  FROM [dbo].[Orders]                                   │
│     [-] D AdventureWorks                                                    │
│       [+] Tables   │ ─── Results ─── (drag or Ctrl+Up/Down to resize) ─── │
│       [+] Views    │  OrderID  | CustomerID | OrderDate  | Total            │
│       [+] Procs    │  ──────── | ────────── | ────────── | ─────────        │
│   [+] Security     │  1001     | C-001      | 2024-01-05 | 1250.00          │
│   [+] Server Obj.  │  1002     | C-007      | 2024-01-06 | 89.99            │
│                    │                                                        │
│                    │  2 rows returned                                       │
├────────────────────┴────────────────────────────────────────────────────────┤
│ Connected to myserver  |  SQL Server 2022 (16.0.x) Developer Edition        │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Features

- **Object Explorer** — full server/database/table/view/proc/function/trigger/sequence/synonym/security tree
- **Multiple Query Panels** — open as many T-SQL editor+results panels as you need, switched by tab
- **Detail Browser** — shows properties of the selected tree node
- **Resizable splitters** — drag or keyboard-resize the explorer width and editor/results split
- **SQL syntax highlighting** — keywords, strings, comments, numbers
- **Modal dialogs** — Connect, Connection String, Help, Server Properties, Database Properties, About
- **Context menus** — right-click any tree node for contextual actions
- **Full authentication support** via [gosmo](https://github.com/radix29/gosmo):
  - SQL Server Authentication
  - Windows Integrated Authentication
  - Azure Entra ID (Default, Password, MSI, Service Principal, Interactive, Device Code, Azure CLI)
- **Connection string editor** — view and manually edit the DSN
- **Script objects** — Script Table/View/Proc/Function as CREATE or DROP into a new query window
- **Cross-platform** — Linux, macOS, Windows (pure Go, no CGO)

## Prerequisites

- Go 1.22 or later
- Access to a SQL Server instance (local, Azure SQL, or remote)
- A terminal emulator that supports 256 colours (virtually all modern ones do)

## Installation

`go.mod` resolves `github.com/radix29/gosmo` to a local sibling checkout
via a `replace` directive (`replace github.com/radix29/gosmo => ../gosmo`),
not a tagged remote release. Clone both repositories side by side:

```bash
mkdir -p ~/src && cd ~/src
git clone https://github.com/radix29/gosmo.git
git clone https://github.com/radix29/gossms.git
cd gossms
go build -o gossms ./cmd/gossms
```

`go install .../gossms@latest` will **not** work until `gosmo` is tagged
and published as a standalone module (or the `replace` directive is
removed in favour of a versioned `require`).

## Usage

```bash
./gossms
```

On first launch the screen is empty. Press **Ctrl+O** or use **File → Connect** to open the connection dialog.

## Keyboard Reference

| Key | Action |
|-----|--------|
| `F1` | Help |
| `Ctrl+Q` | Quit |
| `Ctrl+O` | Connect to server |
| `Ctrl+N` | New query panel |
| `Ctrl+W` | Close active query |
| `Tab` | Switch focus explorer ↔ panels |
| `Ctrl+Tab` | Cycle to next panel |
| `F5` | Execute query / Refresh node |
| `Ctrl+Left` | Narrow object explorer |
| `Ctrl+Right` | Widen object explorer |
| `Ctrl+Up` | Grow query editor (shrink results) |
| `Ctrl+Down` | Shrink query editor (grow results) |
| `Ctrl+Z` / `Ctrl+Y` | Undo / Redo in editor |
| Arrow keys | Navigate tree / grid |
| `Enter` / `+` / `-` | Expand / collapse tree node |

## Architecture

goSSMS is split into an embeddable, application-agnostic TUI library
(`internal/tuikit`) and a thin application layer (`internal/tui`) that wires
it together with SQL Server domain logic via `gosmo`. See
[`internal/tuikit/README.md`](internal/tuikit/README.md) for the library's
design principles and dependency rules.

```
gossms/
├── cmd/gossms/              # main entry point
├── internal/
│   ├── config/              # connection profiles (JSON, in $XDG_CONFIG_HOME/gossms/)
│   ├── db/                  # gosmo connection wrapper + DSN builder
│   │
│   ├── tuikit/               # embeddable TUI library (no SQL Server / app knowledge)
│   │   ├── theme/                # colour palette + derived tcell.Style helpers
│   │   ├── core/                 # Rect geometry, drawing primitives, string/int helpers
│   │   ├── widgets/               # InputField, DropDown, CheckBox, Button
│   │   ├── layout/                # Panel interface, PanelManager (tabs), Splitter
│   │   ├── dialogs/                # ModalDialog base (focus trap), Properties/Alert/Confirm
│   │   └── controls/                # MenuBar, ContextMenu, TreeView, DataGrid, Editor (+SQL highlighter)
│   │
│   └── tui/                  # goSSMS application layer (built on tuikit)
│       ├── app.go                # root App orchestrator, event loop, SQL Server object tree fetch
│       ├── tree_node.go          # NodeType enum + icon/name lookup (domain data only)
│       ├── object_explorer.go    # owns the SQL Server tree model; drives controls.TreeView
│       ├── query_panel.go        # editor + results, implements layout.Panel
│       ├── detail_browser.go     # object details, implements layout.Panel
│       ├── connect_dialog.go     # Connect + Connection String dialogs (embed dialogs.ModalDialog)
│       ├── help_dialog.go        # F1 help modal (embeds dialogs.ModalDialog)
│       └── properties_dialog.go  # Server/Database properties (wraps dialogs.PropertiesDialog)
```

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

Connection profiles are saved automatically to:

- **Linux/macOS**: `~/.config/gossms/config.json`  
- **Windows**: `%APPDATA%\gossms\config.json`

The config file is human-readable JSON. You can delete it to reset all saved connections.

## License

MIT
