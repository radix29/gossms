# Changelog

All notable changes to goSSMS are documented in this file. Format loosely
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); detailed
entries start with v0.0.2 onward.

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
