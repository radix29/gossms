# Release Notes

High-level summary of what changed in each goSSMS release, one entry per
version. For the detailed, file-by-file changes behind each entry, see
[CHANGELOG.md](CHANGELOG.md).

## v0.0.6 — 2026-08-11

The Activity Monitor release. Five live tabs over DMV data arrive with a
terminal charting library behind them, the query editor gains Find and
Replace and real block editing, and permissions editing is finally complete
— `WITH GRANT OPTION`, column-level grants, and an Effective Permissions
page. goSSMS is now GPL-3.0-or-later. Updates `gosmo` to v0.0.8.

### New

- **Activity Monitor** (View > Activity Monitor) — **History** and **Sample**
  over live DMVs: batches/transactions/compiles, wait categories split into
  resource and signal time, memory composition and cache ratios, page
  activity, per-file I/O latency, log flushes and checkpoints. Selectable
  refresh rate (2/3/5/10 s), pause, and Retry on a feed that stopped. Thirty
  minutes of history is kept in memory; nothing is persisted.
- **TempDB tab** — tempdb space, per-file usage, temp tables and the version
  store, on its own slower schedule.
- **Block and Sessions tabs** — the current blocking chains and everything
  currently running, each one run of a stored procedure in a full result
  grid. `sp_block` / `sp_WhoIsActive` are used where they are found, or
  installed into tempdb, with an "Install in master" button to make either
  permanent.
- **A click pins a chart readout, and it tracks its sample** — the box follows
  the data left across the plot and closes itself when the point it names
  scrolls off.
- **Find and Replace** in the query editor — `Ctrl+F`, `F3`/`Shift+F3`,
  `Ctrl+F3` for the word under the caret, match case, whole word, regular
  expressions, and Replace All within the selection as one undo step.
- **Block (column) editing** — typing, `Tab`, `Backspace` and `Delete` now
  apply to every row of an `Alt+Shift+Arrow` selection, and a block copied
  that way pastes back rectangularly. Double-click selects a word.
- **Resizable result-grid columns** — drag a header separator to resize the
  column to its left, past the max default cell length; double-click it to
  restore.
- **Permissions editing completed** — `WITH GRANT OPTION` / `CASCADE` /
  `GRANT OPTION FOR` derived from each cell's transition, per-column grants on
  a table or view, filter boxes on the long grids, a securables search that
  asks the server instead of loading the whole catalog, and a read-only
  **Effective Permissions** page on Login and Database User Properties.

### Fixes

- **Every property-sheet grid was a partial keyboard trap** — Up at the first
  row, Down at the last, and `Left` back to the page list all did nothing on
  all 21 grid pages, and an empty grid ate every arrow key.
- **A restore from an appended `.bak` used the wrong backup set's file list**,
  so every rename-restore failed with "Logical file 'x' is not part of
  database 'y'"; the Files Included panel also kept showing set 1 whatever was
  selected.
- **The Job Steps page rewrote a PowerShell or CmdExec step as T-SQL**, and
  it — like New Job — silently retargeted a step at whichever database sorted
  first. Selecting a second step was enough to trigger both.
- **Column permissions were broken end-to-end for views**: existing grants
  read as "(none)", and "Load Columns" errored on every view.
- **Effective Permissions was offered on Database Role and Server Role
  Properties, where it can never work** — a role cannot be impersonated — and
  the server-scope query failed for exactly the restricted logins it is most
  useful on.
- **A disabled properties field looked live and silently ignored clicks**; a
  second click on an async page action put two round trips in flight; a
  permissions Apply issued its statements in map order and left a stale
  baseline behind; a filter matching nothing left the grids below it live.
- **A text-selection drag stopped selecting the moment it left the field**, in
  the Connect, Backup, Restore, Find and file dialogs.
- **Activity Monitor**: a stopped collector went on reporting itself as
  collecting, a zero refresh rate panicked the application, right-click did
  nothing on the Block and Sessions grids, and the pinned tooltip was by turns
  stale, self-dismissing, and drifting.
- Database Properties > Files showed `PRIMARY` as a log file's filegroup and
  committed it; the Backup dialog offered `tempdb` and offline databases; the
  Object Explorer Details refresh button refreshed the wrong node;
  `Editor.SetText` left one mouse latch armed.

### Changes

- **goSSMS is now GPL-3.0-or-later.** The Sessions tab embeds Adam Machanic's
  GPL-3.0 **sp_WhoIsActive**, carried with its own licence and a modification
  notice; see README § License and Acknowledgements.
- **Editor undo became per-edit deltas** — a keystroke in a 20,000-line
  script went from 4.5 ms / 5.25 MB / 20,002 allocations to 90 µs / 1.05 KB /
  5 allocations, and the stack is capped in bytes as well as steps.
- **The dashboards draw an order of magnitude cheaper** — 20,685 allocations
  down to 2,183 and 6.36 ms to 3.05 ms, with the rendered output proven
  byte-identical — and Find's draw path is no longer quadratic in matches.
- The two collectors and the two sample stores became one generic
  implementation each, the panel's duplicated per-feed state became one type
  held twice, and 36 hand-rolled panic-guarded goroutines became `safego`.
- Which databases a dropdown offers is now one documented rule, turning on
  whether the name is resolved now or later.
- "Show Value" routes by the column's declared type rather than by the shape
  of the text, so an `xml` column opens highlighted even when its brackets
  come back entity-escaped.
- The T-SQL tokenizer and scope scanner moved out to `internal/tui/sqlparse`;
  Options' "Max cell length" is now "Max *default* cell length".
- `gosmo` v0.0.7 → v0.0.8 (permission options, column-level and effective
  permissions, securable search, a view's columns, per-set backup file lists,
  and job-step fixes). **BREAKING there:** `Table.AddColumn`/`DropColumn` are
  gone.

## v0.0.5 — 2026-08-04

A hardening release. The result path stops capping and starts costing
less, the editor learns that a character can be two columns wide, and the
two things that could destroy a user's work — an unprompted exit and a
config write over an unreadable password — are both closed. Updates
`gosmo` to v0.0.7.

### New

- **Output Column Metadata** — the toolbar's `Meta` toggle (also Query >
  Output Column Metadata) lists each result set's columns and their
  declared types in the Messages tab.
- **JSON cell values** open in their own syntax-highlighted panel, the way
  XML ones now do, instead of the plain text popup.
- **Exit asks about unsaved queries.** `Ctrl+Q` and File > Exit used to
  discard every unsaved query panel silently; they now prompt per panel,
  with a real Cancel that backs out of quitting altogether.
- **A panic no longer takes the terminal with it.** Background and UI
  goroutines both recover, restore the screen, and leave a stack trace in
  the log file.
- **Saved passwords are bound to their connection.** A password blob moved
  onto a different entry in `config.json` no longer decrypts — previously
  it could be retargeted at an attacker's host without decrypting anything.
- **Damaged config is recoverable**: an unparsable `config.json` is kept
  aside before defaults are used, an undecryptable password is preserved
  rather than overwritten, and a wrong-sized key file is an error instead of
  being replaced.
- `Ctrl+C` on the results grid copies the selected cell or block, an
  editor horizontal scrollbar, and a results status line on every tab.
- Editable Members, Schema Ownership, and Owned Roles pages across the
  Database Role, Server Role, and Database User Properties dialogs.

### Fixes

- **`Ctrl+V` did nothing in the query editor** after any execution that
  left the results pane on Messages, the plan, or Results To Text — paste
  was being routed to a read-only view.
- **A terminal paste was replayed as keystrokes**, so newlines were lost
  and an open IntelliSense popup committed a completion mid-paste.
- **Wide characters were eaten**: `世界ab` typed into a dialog field
  rendered as `世ab`. Every second wide rune was overwritten by the
  previous one's continuation cell.
- **A `GO` inside a block comment or string** silently scoped IntelliSense
  to the wrong statement, and a `/*` inside a line comment left the
  highlighter stuck in comment colour for the rest of the document.
- **A scrollbar drag in a Properties dialog could fire Script Changes** —
  the scrollbar and that button share a screen column.
- **Script Changes on the New Job / Schedule / Alert / Operator dialogs
  failed with "not found"**, because a write-only path was still reading
  back from the server.
- **`time(0)` columns rendered as `.0000000`** — the declared scale was
  ignored.
- A second Apply re-ran a successful create; a reopened dialog's first
  click was swallowed; the status bar stuck on "Executing query…" after a
  panel closed mid-run; a press on the Object Explorer scrollbar started a
  drag; the "Show Value" popup was lost to any stray click.

### Changes

- **The Max Result Rows cap is gone.** Every row a query returns is kept,
  matching SSMS; the limit is now available memory, deliberately.
- **Keeping them costs much less** — result-set cells are packed into an
  arena rather than allocated one at a time: 800k allocations down to 100k,
  and ~30% faster, over 50k rows.
- **The editor got fast where it was quietly slow**: with a single buffer
  chokepoint and a version counter behind the highlight and wrap caches, a
  keystroke in a 10,000-line script went from 10.4 ms to 0.38 ms.
- **A rename is applied last** in a Properties Apply or Script run, so
  sibling pages — and generated scripts — still address the object by the
  name the server has.
- Mouse gestures now belong to whatever claimed the press until the
  release, across the app, the property sheet, and the splitter.
- Four oversized files split along the established draw/input seam; no
  split candidates remain. Several duplicated Properties pages collapsed
  into shared ones, and all six create dialogs onto one shell.
- `gosmo` v0.0.6 → v0.0.7 (Agent handles and `Scripting(ctx)`, one-round-trip
  space and row counts, file/filegroup backup and restore, and a broad
  scripting-correctness pass). `go-colorful` v1.4.0 → v1.4.1.
- New `docs/journal.md` and `docs/open-threads.md`, and a substantially
  expanded `ARCHITECTURE.md`.

## v0.0.4 — 2026-07-28

An interaction and reliability pass: a shared `ContextMenu`/`MenuBar`
control now backs every right-click and top-menu, the Execution Plan
Viewer's XML tab gets syntax highlighting, and file dialogs gained path
tab-completion. Connections now carry their real login identity and a
lifecycle tied to disconnect, fixing a stale-fetch race in the Detail
Browser and IntelliSense cache along with several double-firing widgets
and smaller Properties bugs. Updates `gosmo` to v0.0.6 (SQL Server Agent
completed; a connection-pool leak fixed).

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
