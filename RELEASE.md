# Release Notes

High-level summary of what changed in each goSSMS release, one entry per
version. For the detailed, file-by-file changes behind each entry, see
[CHANGELOG.md](CHANGELOG.md).

## v0.0.3 — 2026-07-20

The big one so far: a full Execution Plan Viewer (estimated and actual,
graphical/tree/XML tabs), Detail Browser folders now backed by real
progressively-loaded server/database/table data instead of placeholders,
Backup and Restore Database dialogs with full option forms, four new
Properties dialogs (Table, Schema, Database Role, Database User) plus New
Database/New Login creation dialogs, a proper file-browser dialog, a
generalized completion popup framework, Check for Updates, Status History,
Object Explorer drag-and-drop and Take/Bring Database Offline/Online, and
context-gated (grey-out) menu bar and toolbar items. Updates `gosmo` to
v0.0.5 and `tcell` to v3.4.1, and fixes a real data race in the detail
loaders along with a long list of smaller interaction bugs (menu/tree
click flicker, context-menu divider navigation, offline-database
expansion, stale Databases folder, statement-boundary selection, several
Execution Plan Viewer issues, and a config-directory permissions bug).

## v0.0.2 — 2026-07-14

First publicly released build. Adds a toolbar and an SSMS-style results
status bar, SQL syntax highlighting with statement/batch selection and
word-wrap editing, full Server/Database/Login Properties dialogs, object
dependency viewing, background tasks (backup, index rebuild) with
progress and cancellation, encrypted saved passwords, and an automated
GitHub Actions release pipeline.

## v0.0.1 — 2026-07-09

Initial internal milestone: the core TUI skeleton — Object Explorer,
Connect dialog with full gosmo authentication support, a basic T-SQL
editor and query execution, and local config persistence. Not published
as a GitHub Release.
