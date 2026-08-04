# Changelog

All notable changes to goSSMS are documented in this file. Format loosely
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); detailed
entries start with v0.0.2 onward.

## [0.0.5] - 2026-08-04

### Added

- **Output Column Metadata toggle** — the toolbar's `Meta` button (also
  Query > Output Column Metadata) appends a block to the Messages tab for
  every result set, naming each column and its declared type the way SSMS
  writes it (`nvarchar(50)`, `decimal(18,2)`, `varchar(max)`, …); a column
  the query didn't name shows its position instead. Backed by
  `internal/query/coltype.go` and `ResultSet.ColumnTypes`, filled once per
  result set rather than per row.
- **JSON syntax highlighting** (`controls.JSONHighlighter`) joins the SQL
  and XML highlighters. It deliberately has no `prefixStates` cache — no
  JSON token can span a line, so each line stands alone.
- **XML and JSON cell values open in their own highlighted query panel** —
  "Show Value" on a cell whose text is bracketed by `<>`, `{}`, or a
  JSON-shaped `[]` routes through the new `DataGrid.OnShowValue` hook to a
  panel with the matching highlighter instead of the plain 60-column popup.
  A bracket-quoted SQL Server name (`[dbo]`) still gets the popup.
- **Save-before-exit prompt** — `Ctrl+Q` and File > Exit discarded every
  unsaved query panel with no prompt. They now walk the dirty panels one at
  a time, and `dialogs.ConfirmDialog` gained a three-way
  `ShowConfirmCancel` (Yes/No/Cancel, Escape answering Cancel) for
  questions where *No* is itself the destructive answer.
- **Panic containment** — `App.safego`/`App.recoverPanic` wrap every
  background goroutine in the `tui` package, and `cmd/gossms` recovers on
  the UI goroutine after `screen.Fini()` has restored the terminal. A panic
  now leaves a stack trace in the log file and a readable line on the
  restored screen instead of taking the process down mid-alternate-screen
  with the user's unsaved query text. This is reachable in practice:
  go-mssqldb panics outright on a column type ID it doesn't know.
- **Saved passwords are bound to the connection they belong to** — each is
  sealed with AES-GCM additional authenticated data naming the
  server/user/auth-method triple, so a ciphertext copied onto another entry
  in `config.json` no longer decrypts. This closes an exfiltration path
  that needed no decryption at all: edit a saved entry to point at a host
  you control and goSSMS would dial out with the original password. Values
  written by earlier versions still open, and are rewritten bound on the
  next save.
- **Config and key-file damage is now recoverable** — a `config.json` that
  doesn't parse is preserved as `config.json.corrupt` before the app falls
  back to defaults; a password that fails to decrypt keeps its original
  ciphertext through subsequent saves instead of being overwritten with an
  encryption of `""`; and a `gossms.key` that isn't 32 bytes is an error
  rather than a reason to generate a fresh key over the only thing that
  could still have opened those passwords.
- **`Ctrl+C` on a focused results grid** copies the selected cell or block,
  matching the right-click "Copy" — it used to copy the *editor's*
  selection. Copy actions from the grid and the plan summary now
  acknowledge on the status bar.
- **Editor horizontal scrollbar**, and the results status line is now drawn
  on the Text, Plan, and Messages tabs, not just the grid.
- Reusable **Members** and **owner-transfer** property pages
  (`membership_page.go`, `owner_transfer_page.go`) behind Database Role /
  Server Role Members, Schema Ownership, and Owned Roles — all four are
  editable, applied on OK/Apply, and discarded on Cancel.
- New tuikit API: `TreeView.NodeIDAt`, `DataGrid.ScrollRow`/
  `SelectedCellsText`/`OnShowValue`, `propsheet.Hint()`,
  `core.ColumnOfRune`/`RuneIndexAtColumn`, and `controls.Document`.

### Changed

- **The Max Result Rows cap is gone entirely** — the Options field, the
  `max_result_rows` config key, and every `maxRows` parameter behind it.
  A result set is retained in full, matching SSMS's "you get what you asked
  for"; a large enough query is now limited only by available memory. An
  existing `config.json` carrying the old key loads cleanly and ignores it.
- **Result sets cost far less to keep.** A new `cellArena`
  (`internal/query/arena.go`) packs a set's cell text into 64 KiB chunks and
  its per-row slices into 4096-slot chunks, and every cell is rendered
  through one reused buffer. Measured over 50k rows × 5 columns: 800k
  allocations → 100k, 21.1 MB → 14.9 MB allocated, ~148 ms → ~101 ms.
  Results To File retains nothing and bypasses the arena entirely.
- **The editor and every input field are display-column aware.** Text
  positions stay rune indices; screen quantities (scroll, caret, click,
  scrollbar) are terminal columns, converted only through the new
  `core.ColumnOfRune`/`RuneIndexAtColumn`. Block (column) selection stays
  rune-indexed deliberately — a rectangle over mixed-width text has no
  single right answer, and that is the SSMS-parity choice.
- **Editor redraw is no longer O(document).** `controls.Document` is now
  the single mutation chokepoint for the buffer and carries a version
  counter, which the highlighters' block-comment state cache and the
  word-wrap cache are keyed on; the block-comment replay resumes from the
  edited line and stops as soon as it rejoins the previous scan, and line
  widths are cached per line. At 10,000 lines a keystroke went from 10.4 ms
  to 0.38 ms, and a redraw with no edit is 0.37 ms with no
  document-proportional work left.
- **A rename is now the last write of a Properties Apply or Script run**
  (`propPage.renames`), so sibling pages address the object by the name the
  server still has — and a generated script doesn't put statements naming
  the old object *below* the rename that invalidates it.
- **Mouse gestures have a single owner from press to release** —
  `App.gestureOwner` plus a modal-layer snapshot, `PropertySheet.dragZone`,
  and a `Splitter` press latch. An overlay that opened or closed while the
  button was already down no longer receives that button.
- `query_panel.go`, `restore_dialog.go`, `backup_dialog.go`, and
  `planview/planview.go` split along the existing draw/input seam
  (`*_draw.go`, `*_input.go`, plus `planview_clipboard.go`); every one is
  now under 400 lines. No file-split candidates remain outstanding.
- Deduplicated Properties pages: `pageUserSecurables`/`pageRoleSecurables`
  were byte-identical (now `pageDatabasePrincipalSecurables`), and seven
  `page*ExtendedProperties` wrappers collapsed into one
  `pageExtendedProperties`. All six "New \<object\>" dialogs now share one
  generic `newObjectDialog` shell.
- 33 hand-written `postEvent` + `wakeEventLoop` pairs became `postAndWake`;
  `QueryPanel`'s elapsed-timer tick is the only remaining bare wake.
- Toolbar labels compacted so the toggles stay equal-width (`Est.Plan`,
  `Act.Plan[ON--]`, `Meta[-OFF]`).
- `db.ConnectionError` now wraps the underlying failure (`Unwrap`), so a
  caller can reach the driver error, an `mssql.Error` number, or
  `gosmo.IsRetryable`.
- The log file is created `0600`, matching `config.json` and the key file
  beside it — it records server names, login names, and error text.
- The Connect dialog's connection-string preview omits `database=` when no
  database is set, matching the DSN actually dialed.
- `gosmo` dependency updated `v0.0.6` → `v0.0.7` — `Scripting(ctx)` and
  no-I/O Agent handles (which is what makes Script Changes work on the
  create dialogs), whole-database space/row-count reads in one round trip
  instead of one query per table, file/filegroup backup and restore, and a
  long list of scripting fixes (parameterised writes captured with `@p1`
  placeholders still in them, `ScriptTable`'s unparsable `IF NOT EXISTS`
  wrapper, unique constraints scripted as indexes, fragmentation reads
  returning numbers for the wrong table, `SetQueryStoreOptions` rejected
  outright, `BACKUP FILES` not being T-SQL). **BREAKING there:** every
  `*Seq()` iterator now takes a `context.Context`.
- `go-colorful` updated `v1.4.0` → `v1.4.1` (indirect).
- Documentation: `ARCHITECTURE.md` substantially expanded (which document
  owns what, how a query runs, threading model, common tasks, the
  `mouseDragging` idiom, `postAndWake`), and two new documents —
  `docs/journal.md` (why a design is the way it is) and
  `docs/open-threads.md` (known bugs, deferred scope, decisions not to
  re-raise).
- Test coverage expanded substantially across `tui`, `tuikit`, and `query`,
  including a golden-file freeze of the T-SQL completion lexer
  (`testdata/completion_prefix_scan.golden`) that retired a second,
  hand-maintained T-SQL state machine kept only as a test baseline.

### Fixed

- **`Ctrl+V` did nothing in the query editor** whenever the results pane
  happened to be showing Messages, the execution plan, or Results To Text —
  any `PRINT`, `SET`, error, or non-`SELECT` batch left it there. Paste was
  routed by which tab was visible, ignoring which half of the panel had
  keyboard focus, and every one of those views is read-only, so the paste
  silently went nowhere.
- **A terminal paste was replayed as typing.** tcell brackets a paste but
  still delivers its content as ordinary key events, and nothing handled the
  markers — so each pasted newline arrived as `Enter`, and with the
  IntelliSense popup open it committed the selected candidate instead.
  Pasting `objects\nWHERE type = 'U'` over `sys.` produced
  `sys.objectsWHERE type = 'U'`. A paste is now applied as one edit and one
  undo step, and anything that isn't a rune, newline, or tab is dropped
  rather than acted on.
- **Wide characters were being eaten** by the editor and every input field.
  Recorded as a cursor-position bug, it was worse: typing `世界ab` into the
  Connect dialog's Server field rendered `世ab`, and 32 ideographs in a
  wrap-mode field collapsed onto one row of 16 glyphs. Every second rune was
  overwritten by the previous one's continuation cell.
- **A `GO` inside a block comment or string literal started a new batch**
  for IntelliSense scoping, so `SELECT * FROM dbo.Patients p /* GO */ WHERE
  p.` offered no completions. Batch detection now falls out of the lexer's
  own state machine, which covers comments, string literals, and bracketed
  identifiers in one rule.
- **A `/*` inside a `--` comment or a string literal** left the SQL
  highlighter believing it was inside a block comment for the rest of the
  document.
- **A scrollbar drag in a Properties dialog could fire Script Changes** —
  the form's scrollbar column and `[ Script Changes ]`'s last column are the
  same column, and the button row was hit-tested before the form.
- **Script Changes on the create dialogs failed with "not found."** gosmo's
  Agent `Create*` methods ended with a read-back that went to a real server
  under scripting mode, looking for an object whose `sp_add_*` had merely
  been collected. Fixed in both repos; the dialogs now use name-only
  handles.
- **Date and time columns ignored their declared scale** — `time(0)`
  rendered as `.0000000`.
- **A create dialog re-ran a successful create on a second Apply**, coming
  back with the server's "already exists"; a fast page switch could also
  start a second prefetch and rebuild every form from it.
- **The status bar stuck on "Executing query…" forever** after a query
  panel was closed mid-execution.
- **A modal dialog's mouse latch survived into its next showing**, freezing
  the first click of every reopened dialog.
- **A press on the Object Explorer's scrollbar or empty space started a
  drag** of whatever node happened to be selected; drag arming now asks what
  the press actually landed on (`TreeView.NodeIDAt`).
- **The "Show Value" popup was dismissed by any stray click** while reading
  a long value, and could be left stranded over stale text when new results
  arrived — it now closes only on Escape or its own Close button, and is
  closed explicitly when the grid's data is replaced.
- **`Form.scroll` was never re-clamped on resize**, and
  `DetailBrowser.PurgeConn` left a disconnected connection's rows on screen.
- Long messages could overflow a dialog's height on a short terminal,
  drawing over the separator and button row.

## [0.0.4] - 2026-07-28

### Added

- **XML syntax highlighting** for the Execution Plan Viewer's XML tab
  (`controls.xml_highlighter.go`) — tags, attribute names/values, comments,
  and CDATA sections are now colour-highlighted instead of plain text.
- **`TypedConfirmDialog`** promoted to a reusable `tuikit/dialogs` control
  (retype-to-confirm), backing the app's confirm-typed flows from a single
  shared instance instead of ad-hoc per-caller logic.
- **File dialog path tab-completion** — completes the current path segment
  against matching entries in the directory being browsed.
- **`PropertySheet` clipboard support** — copy/paste text directly in a
  Properties form field.
- **"SQL Server Agent (Stopped)" label** on the Object Explorer root —
  a background `AgentInfoContext` check appends it once the service is
  confirmed not running; a failed or inconclusive check leaves the label
  alone rather than guessing.
- Cell-level selection (`DataGrid.SetCellCursor`) extended to more result
  and detail grids that previously only supported whole-row selection.
- **`ServerConn.Login`** — the real server login a connection authenticated
  as (`SUSER_NAME()`), fetched once at connect time and now shown in the
  connection label instead of just the configured (often empty, for
  Windows/Entra auth) `Opts.User`.
- **Connection pool caps and per-connection cancellation** —
  `MaxOpenConns`/`MaxIdleConns` (20/10) bound the pool a connection opens,
  and the new `ServerConn.Context()` is cancelled by `Close`, so
  disconnecting now promptly cancels every background load still scoped to
  that connection instead of leaving it to idle out on its own timeout.

### Changed

- `internal/tuikit/controls`' `menu.go` split and rebuilt into
  `menu_bar.go`, `context_menu.go`, and `menu_item.go` — `MenuBar` and
  right-click `ContextMenu`s (Object Explorer and tree nodes) now share one
  `MenuItem`/`Menu` model instead of separate implementations.
- `internal/tuikit/dialogs` split further: `file_dialog.go` (state) now
  has `file_dialog_draw.go`/`file_dialog_input.go`/`file_dialog_complete.go`
  siblings, and a new `common.go` holds `fitMessage`, the shared
  message-driven dialog-sizing logic `AlertDialog`/`ConfirmDialog` both use.
- `propsheet.PropertySheet` split further: `sheet.go` (state/page list) now
  has `sheet_draw.go`/`sheet_input.go`/`sheet_clipboard.go` siblings.
- The `mouseDragging` idiom (see `ARCHITECTURE.md`) extended to
  `Button`/`CheckBox`/`DropDown`/`RadioBox` in `tuikit/widgets` — the same
  tcell all-motion resend issue already fixed elsewhere could fire these
  widgets' actions more than once per physical click.
- Background loads — tree-node expand (`loadChildren`), the IntelliSense
  completion inventory, and Detail Browser fetches — now derive their
  context from the owning `ServerConn.Context()` instead of
  `context.Background()`, so a disconnect actually cancels them.
- Detail Browser and completion-inventory loaders now track a per-entry
  sequence number so an older in-flight fetch can never overwrite a newer
  one's already-landed result.
- Per-row detail backfill (Databases/Tables folders) and other row-level
  fan-out loaders are now bounded by a shared `maxRowFetchConcurrency` (8)
  semaphore instead of unbounded per-row goroutine fan-out.
- `gosmo` dependency updated `v0.0.5` → `v0.0.6` — completes SQL Server
  Agent (alerts, operators, shared schedules, categories, and a rounded-out
  Job surface: rename/description/category/owner/start step/auto-delete/
  completion-email, in-place step edit, cross-job history); fixes a
  connection-pool leak where every rows-returning read permanently
  consumed one connection (an app capping `MaxOpenConns` would wedge
  completely; an uncapped one grew connections without bound), extends
  automatic retry to server-level reads and to failures single-row reads
  previously couldn't catch, and adds index/statistics administration and
  diagnostics, server-role administration parity, and DDL-value validation
  wherever a value has to be spliced into SQL text.

### Fixed

- **Stale in-flight fetch could win a race** — reselecting a tree node (or
  refreshing IntelliSense) while its previous fetch was still in flight let
  the older fetch's result land after the newer one's, silently overwriting
  good data with stale or erroneous data. Fixed in both the Detail Browser
  and the completion-inventory loader with the sequence-number guard above.
- **`ConnectDialog`'s auth-method dropdown** didn't get first refusal of
  clicks landing on its open list where it visually overlapped the
  Connect/Cancel button row — the same overlay-ordering fix already applied
  to the Backup/Restore dialogs.
- **`Button`/`CheckBox`/`DropDown`/`RadioBox`** could fire their action more
  than once from a single click, because tcell resends `Button1` on every
  motion event while the mouse button stays physically down.
- **Database Properties Owner field** showed an unrelated real login as the
  owner when a database's `owner_sid` didn't resolve to any login on the
  server (`SUSER_SNAME` returns NULL) — now shows a clear
  "(unresolved owner)" placeholder instead.
- **Database User Properties stale name after rename** — renaming a user
  left every other page looking up the old, now-nonexistent name on the
  next reload; same bug class already fixed in Key Properties and Server
  Role Properties, now fixed here too.
- **File dialog path completion** computed the common prefix of matching
  names byte-by-byte instead of rune-by-rune, which could cut a multi-byte
  UTF-8 filename mid-character.
- **`PropertySheet` `Form`** let `PgUp`/`PgDn` scroll the row list out from
  under an open dropdown or "Show Value" popup, leaving it floating at a
  stale position instead of tracking its row.
- **Take/Bring Database Offline/Online** didn't refresh the Object
  Explorer node's own state immediately — it caught up only on the next
  unrelated refresh.

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
