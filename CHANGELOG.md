# Changelog

All notable changes to goSSMS are documented in this file. Format loosely
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); detailed
entries start with v0.0.2 onward.

## [0.0.3] - 2026-07-20

### Added

- **Execution Plan Viewer** — Query > Estimated Execution Plan, or toggle
  Include Actual Execution Plan on before running a query. A Plan tab
  renders the graphical operator plan (cost-weighted icons, a draggable
  Properties strip for the selected operator), a Tree tab gives an
  expandable operator tree plus a summary grid, and an XML tab shows the
  raw ShowPlanXML; any of the three can be popped out into its own
  closable panel. Backed by two new packages: `internal/showplan` (parses
  ShowPlanXML, UTF-8 or UTF-16LE-with-BOM, into a navigable operator tree)
  and `internal/tui/planview` (the reusable tabbed viewer control), plus a
  `cmd/plandemo` dev harness for checking it against real plan files
  outside the full application.
- **Detail Browser now shows real, progressively-loaded data** for the
  Server node and the Databases/Logins/Tables folders — fast columns
  (name, state, recovery model, connect-time server info) appear as soon
  as the cheap list query returns, then per-row size/row-count/available-
  memory/disk-volume figures backfill concurrently as their own round
  trips complete, instead of the previous flat placeholder rows.
- **Backup Database / Restore Database dialogs** — full SSMS-style option
  forms (destination, backup type including differential, media,
  compression; restore source, backup-set/file-list inspection, and
  point-in-time/history browsing) in front of the existing cancellable
  background-task execution.
- **Table, Schema, Database Role, and Database User Properties** —
  multi-page editable dialogs matching the SSMS mockups, joining the
  existing Server/Database/Login Properties on the same `PropertySheet`
  framework.
- **New Database / New Login dialogs** — the same multi-page form pattern
  as Properties, but building a `CREATE DATABASE`/`CREATE LOGIN` statement
  from scratch instead of diffing an existing object.
- **File browser dialog** (`tuikit/dialogs.FileDialog`) — directory
  listing, up/new folder, and overwrite confirmation — now backs every
  file path prompt (Open, Save, Save As, Results To File), replacing the
  old plain path-entry prompt.
- **Generic completion/IntelliSense popup** (`controls.Editor`'s
  `editor_completion.go`) — the autocomplete popup's open/close,
  keyboard/mouse navigation, and overlay drawing are now a reusable
  `Editor` capability driven by any `CompletionProvider`, not SQL-specific
  logic baked into the editor; gossms's own SQL provider is the first of
  potentially several.
- **Check for Updates** (Help menu) — compares the running version against
  the latest GitHub Release and shows the result in a new UpdateDialog.
- **Status History dialog** — a read-only, timestamped log of every
  status-bar message, capped at the most recent 256.
- **Object Explorer drag-and-drop** — drag a database or object node into
  a query editor to insert its quoted T-SQL name (schema-qualified for
  tables, views, procs, functions, sequences, synonyms, and triggers).
- **Take Database Offline / Bring Database Online** — new Object Explorer
  context-menu action and icon for a database node.
- **System Views / System Stored Procedures / System Functions folders**
  under a database's Views/Programmability nodes, matching SSMS.
- **Context-gated menu bar and toolbar** — every `MenuItem`/`ToolbarButton`
  can now take an `Enabled func() bool` predicate; items that don't apply
  to the current selection or state (Disconnect with nothing selected,
  Cancel Executing Query with nothing running, Save with no query panel
  active, and others) grey out and refuse both keyboard and mouse
  activation instead of silently no-op'ing.
- **Max Result Rows** option (Tools > Options) — caps how many rows a
  Grid/Text result set keeps in memory (default 100,000); Results To File
  ignores the cap and still writes every row a query returns.

### Changed

- `gosmo` dependency updated `v0.0.4` → `v0.0.5` — notably backs the new
  Table/Schema/Role/User Properties pages, Query Store and Database Scoped
  Configuration pages, Take/Bring Database Offline/Online, backup/restore
  diagnostics (verify, header/file-list, differential backups), and
  disk-volume/processor info; also fixes a login-creation bug (passwords
  were sent as `HASHED` hex instead of a plain string literal, so a
  created login could never authenticate) and an Agent Job dialog bug
  (last-run outcome/duration and running-state always read as empty).
- `tcell` dependency updated `v3.4.0` → `v3.4.1`.
- `internal/tui`, `internal/tuikit/controls` (`DataGrid`, `Editor`,
  completion), and `internal/tui/database_props.go` split further along
  the project's one-file-per-type/purpose convention as they grew — see
  the Architecture section in `README.md` and `internal/tuikit/README.md`.

### Fixed

- **Data race in the Databases/Tables folder detail loaders** — per-row
  background goroutines wrote size/row-count results directly into the
  results grid's shared row slice before notifying the UI, racing against
  the UI goroutine's own redraw; all writes now happen exclusively inside
  the existing `postEvent` callback, serializing every mutation onto the
  UI goroutine.
- **TreeView click/drag flicker** — tcell's all-motion mouse tracking
  resends the same `Button1` press while the button is held, causing a
  tree node's expand/collapse to double-fire or flicker; fixed with the
  same `mouseDragging` idiom already used by the editor, `DataGrid`,
  `MenuBar`, and `Toolbar`. Clicking an already-selected row no longer
  toggles its expand state either.
- **Menu bar auto-closing** — hovering the mouse outside an open dropdown
  no longer closes it; only an actual click outside does.
- **Right-click context menu divider navigation** — Up/Down could
  previously land the highlight on a `────` divider row in any tree
  node's context menu.
- **Mouse wheel over a grid inside a Properties dialog** scrolled the
  whole form instead of the grid under the cursor.
- **Expanding an offline database** used to cascade a raw per-folder error
  into the tree; it now collapses to a single, clear "(offline)" leaf.
- **`DropDown` closing on mouse move** — moving the mouse over an open
  `DropDown`'s list could close it on its own, without an actual click
  outside.
- **Databases folder going stale** — creating or dropping a database left
  the Object Explorer's Databases folder showing the old list until a
  manual refresh; it now refreshes itself automatically.
- **`Ctrl+Enter`/Execute at Cursor statement-boundary ambiguity** — picking
  the T-SQL statement at the cursor could pick the wrong adjacent
  statement when two segments' boundaries were ambiguous (first-match-wins
  picked incorrectly).
- **Execution Plan Viewer fixes**: `showplan.Indent` no-op'd on a
  single-line plan with a trailing end-of-file newline; the Tree tab's
  `Enter` always toggled node expand instead of letting the summary grid
  jump to a node; the Tree tab's raw Properties view never showed
  Warnings (the parser rejected `"1"`/`"0"` boolean attributes, only
  `"true"`/`"false"`); and a real, deeply-nested plan's root tile could
  land off-screen.
- **Object Explorer selection after first connect** — `TreeView.SetNodes`
  clamped the selection index but never fired `OnSelect`, leaving the
  Detail Browser empty until a second click; a new `SelectID` method fixes
  it.
- **Options dialog width** increased to stop text overflow.
- **Config directory permissions** — `~/.config/gossms` (and
  `%APPDATA%\gossms` on Windows) is now created `0700`, not `0755`,
  matching the encryption key file's already-private posture.

## [0.0.2] - 2026-07-14

### Added

- **Toolbar** — icon-only quick-action row sharing the menu bar's line,
  right-aligned, with hover tooltips styled like SSMS's query-status bar
  (New Query, Execute, Execute Selection, Stop Execution).
- **Results grid status bar** redesigned to match SQL Server Management
  Studio's style — elapsed time, selected row/column, and row count,
  live-updating while a query executes.
- **SQL syntax highlighter** (keywords, strings, comments, numbers) and
  `Ctrl+Enter` **T-SQL statement/batch selection**, splitting on `;` and
  `GO` while ignoring comments and string literals.
- **Word-wrap mode** for the editor — soft-wrap segmentation, visual-row
  mapping, and mouse handling.
- **Editor actions**: line duplication, deletion, movement, indentation,
  and commenting; word-aware navigation and deletion; more undo/redo
  coverage.
- **`PropertySheet` framework** (`internal/tuikit/propsheet`) and the
  first multi-page editable Properties dialogs built on it — Server,
  Database, and Login: page list on the left, OK/Cancel/Apply/Script
  Changes below, async per-page loads, F5 refresh, any value copyable to
  the clipboard, Script Changes opens the generated SQL in a new query
  window instead of running it.
- **Object Dependencies** viewer — View Dependencies on a table, view, or
  procedure lists what it depends on and what depends on it.
- **Background Tasks** — Back Up Database and Rebuild All Indexes run as
  cancellable background tasks; Tools > Background Tasks shows live
  progress, including percent-complete for backups.
- **Key Diagnostics dialog** (Help > Key Diagnostics) — shows tcell's
  decoded Key/Modifiers/rune for every keypress, for diagnosing terminals
  that don't deliver an expected shortcut.
- **Configurable Object Explorer tree icons** — Emoji (default), Symbols,
  Portable, or None — picked from Tools > Options and persisted to
  `config.json`.
- **Dialog stack** (`dialog_stack.go`) to manage nested, z-ordered modal
  dialogs with correct draw and input routing.
- **OS clipboard integration** shared between the editor and dialog text
  fields.
- **Encrypted saved passwords** — AES-256-GCM, with the key stored in a
  separate `gossms.key` file (mode `0600`) alongside `config.json`.
- **Recent-connections lookup** in the Connect dialog — typing 4+
  characters into Server shows matching saved profiles.
- Connect dialog: editable Extra Properties field and a read-only
  connection-string preview that updates on focus loss.
- Query panel: multiple result sets per execution, a Messages tab
  collecting `PRINT` output/row counts/errors, and "execute selected
  text" as distinct from "execute whole script".
- **GitHub Actions release workflow** (`.github/workflows/release.yml`) —
  builds and cosign-signs binaries for every Go-supported target on a
  `v*` tag push and publishes a GitHub Release.

### Changed

- `gosmo` dependency updated `v0.0.3` → `v0.0.4`.
- Menu, context-menu, and shortcut wording harmonized in several places
  (Object Explorer and connection nodes).
- `internal/tuikit` package layout split further for maintainability —
  see `internal/tuikit/README.md`.

### Fixed

- Object Explorer node expand/collapse and icon rendering — closing nodes
  via mouse or Backspace now behaves correctly.
- Focus-navigation edge cases in `DropDown`, `InputField`, and `RadioBox`
  no longer swallow unhandled keys, so focus cycling works correctly
  inside `PropertySheet` forms.

## [0.0.1] - 2026-07-09

Initial internal milestone: the core TUI skeleton — Object Explorer tree,
Connect dialog (SQL Server Authentication, Windows Integrated, and Azure
Entra ID via gosmo), a T-SQL query editor with basic execution and a
results grid, local config persistence
(`~/.config/gossms/config.json`), and Copy/Cut/Paste in the editor. No
GitHub Release was published for this tag — the release workflow didn't
exist yet.
