# Release Notes

High-level summary of what changed in each goSSMS release, one entry per
version. For the detailed, file-by-file changes behind each entry, see
[CHANGELOG.md](CHANGELOG.md).

## v0.0.8 — 2026-08-25

The least-privilege release. goSSMS now works as a login that is not
`sysadmin` — it could not even connect as one before — and where a right is
missing it says which one rather than showing a zero or a driver error.
Alongside it: `Script <object> as ▸` on every node the tree shows, New Index
and New Statistics, four more object families in Object Explorer, a
missing-index banner on execution plans, and `.sqlplan` files. Updates `gosmo`
to v0.0.10, Go to 1.27 and `tcell` to v3.4.2.

### New

- **goSSMS works as a login that is not `sysadmin`.** A `db_owner` could not
  connect at all: the connect-time server read asked for a DMV in the same
  statement as thirteen public values, so one refused column failed the whole
  thing. That statement is split, and one round trip at connect now asks the
  server what this login may do.
- **A value that could not be read shows as `N/A`, never as `0`** — and never
  as a smaller number taken over the rows the login happens to see. A page
  whose main reads succeed opens even when one of them was refused, and a grid
  built from a visibility-filtered catalog view says so at the top.
- **A refusal is shown as the right it needs** — *Access denied — Requires
  SELECT on msdb.dbo.sysjobservers* — rather than the wrapped driver error,
  with the failed action's own text kept. Where SQL Server itself will not say
  whether an object is missing or merely invisible, goSSMS does not say either.
  A database this login cannot open expands to one *Access denied* line
  instead of nine folders that each fail on their own.
- **An action you cannot perform is withheld, not offered and then refused.**
  The menu item is greyed and names the permission it wants; a Properties page
  whose writes would be rejected opens read-only with the required right at the
  top and **Close / Script Changes** in place of OK/Apply, so the statements can
  still be generated for someone who can run them. The check is one-sided —
  an action is withheld only when the server answered "no" to *every* right
  that would permit it, so an unprobed connection behaves exactly as before.
  README gains a **Required rights** table; `docs/permissions-plan.md` carries
  the whole design.
- **`Script <object> as ▸` on every family the tree shows** — sixteen node
  types, with CREATE, ALTER, DROP, DROP And CREATE and the SELECT / INSERT /
  UPDATE / DELETE / EXECUTE templates, to a new query window, a file, or the
  clipboard. An index adds REBUILD, REORGANIZE and UPDATE STATISTICS; a
  statistics object its own UPDATE STATISTICS. The three hardcoded
  "Script Table as CREATE"-style items are gone.
- **New Index and New Statistics.** A table's Indexes folder creates any index
  type SQL Server has — clustered and nonclustered (unique, included columns,
  filter, fill factor, online, partition scheme), clustered and nonclustered
  columnstore, XML primary and secondary, and spatial with its tessellation,
  bounding box and grid. Its Statistics folder creates a statistics object with
  an ordered column list, a filter, FULLSCAN or a sample percentage,
  NORECOMPUTE and INCREMENTAL. Both offer Script Changes instead of running.
- **Four more object families in Object Explorer.** A database gains a
  **Storage** folder (Partition Functions, Partition Schemes), and its Security
  folder gains **Security Policies** — with Enable/Disable, the disable
  confirmed, since it stops filtering and every protected row becomes visible —
  and **Always Encrypted Keys**, with **New Column Master Key** and **New
  Column Encryption Key** dialogs. Eleven node types, each with Properties, a
  Detail Browser view, Delete and a Script entry.
- **A missing-index suggestion shows as a banner above the execution plan**;
  `m`, or a click on it, opens the `CREATE INDEX` script in a query window.
  **Plans open and save as `.sqlplan`**, the same files SSMS reads and writes.
- **Log File Viewer: Recycle, and a search the server runs** rather than a
  filter over what was already read. Enter or a double-click opens a log file
  from the tree instead of only its context menu.
- **Job Properties > Steps: Move Up / Move Down.** A move is a delete plus an
  insert, so the step's whole definition has to survive it — proxy, run-as
  user, additional parameters, CmdExec success code, target server and priority
  are read and written now instead of being silently dropped. Start and Stop
  know the job's state and refuse locally rather than at the server.
- **Delete a column, drop a table with its foreign keys, move an object to
  another schema, and rename a column.** The cascade is a checkbox on the
  delete question rather than a second question; the column rename warns
  *before* the rename, not after.
- **Menu bar and context menus have submenus.** `MenuItem.Sub` opens a
  cascade, and the open chain is re-derived from the host's live menu every
  frame, so a rebuilt menu can never be navigated through a stale pointer.
- **Key Diagnostics can copy its log** — it is a real read-only editor now,
  with mouse drag-selection, `Shift`+arrows, `Ctrl+A`, a scrollbar and a Copy
  button, which is what makes it pasteable into a bug report.
- **A peer instance is reached with its own credentials.** Every Always On
  read that follows the primary used to sign in with whatever login the tree
  was registered with; a saved connection for that instance now wins, with the
  parent's settings retried once behind it so a resolver can only ever make an
  instance *more* reachable.
- **Add Database to an availability group checks the full-backup
  prerequisite** — over `sys.database_recovery_status`, not msdb, which cannot
  answer it — and the exclusion line says "back it up first". It used to offer
  the database and fail on apply with Msg 1475.

### Fixes

- **Connect was unreachable by keyboard on every terminal.** `Ctrl+Shift+O`
  was bound as an event tcell never produces, so legacy terminals opened a file
  instead and modern ones matched nothing; `Ctrl+Shift+U` was dead the same
  way, and its test passed only because it synthesized the impossible event.
  **`F9` is the Connect binding now**, and the chord is folded back app-wide
  where the terminal can encode it.
- **The Processors page turned off the instance's CPU affinity.** For a login
  that cannot read the CPU list the affinity grid was built with zero rows,
  which Apply compared against the live mask and wrote back as
  `sp_configure N'affinity mask', 0` — the instance unpinned from its
  processors, with nothing on screen having said so.
- **`Ctrl+X` in the Find dialog cut the query editor's text behind it**, with
  nothing on screen saying what was cut or from where. The clipboard target is
  now resolved by asking the frontmost dialog, and never falls past it.
- **A click on a Properties grid moved the highlight and told nobody** —
  eleven pages wire a selection callback onto a cell-cursor grid, and the
  detail panel below went on describing whatever row the *keyboard* had last
  left it on.
- **A grid redraw put the row the user was on at the bottom of the viewport**,
  and **a column the user had dragged snapped back to its default width.**
- **A menu bar dropdown taller than the terminal lost its last items,
  silently** — painted past the bottom edge, unclickable, with the keyboard
  highlight walking onto them where nothing showed it. Dropdowns scroll now,
  with ▲/▼ marks and wheel support; a context menu is clamped instead, because
  it can be moved and a dropdown cannot.
- **The panel drop-down ran off the bottom of the screen** — with more query
  panels than the terminal has rows, the ones past the edge were unreachable by
  mouse *and* keyboard, and the active panel was often among them. It scrolls,
  its scrollbar is draggable, and its drag latch no longer outlives it.
- **A paste followed focus to the next row.** Aimed at Name and delivered
  after the user tabbed on, it landed in Description — every clipboard read is
  asynchronous, and the guard against a moved target could not see through a
  property sheet that answers as itself.
- **A scripted partitioned table came back unpartitioned.** `ScriptTable`
  emitted no `ON` clause, so the script ran clean and produced a
  single-partition heap on PRIMARY.
- **A scripted login dropped the SID it already had**, orphaning every
  database user mapped to it on the target server — the main reason to script
  a login at all. And a login whose name contains " WITH " produced a statement
  that does not parse, because the builder decided its own syntax by grepping
  its own output.
- **Autogrowth could not be turned off.** `FILEGROWTH = 0` was inexpressible:
  the value that disables growth and the value that omits the clause were the
  same value.
- **`CREATE COLUMN MASTER KEY`'s enclave clause was never valid syntax** —
  `ENCLAVE_COMPUTATIONS = YES` has no boolean spelling, and the parse error it
  raises cannot even be caught by TRY/CATCH. The signature is supplied by the
  caller now, because nothing inside the library can compute one.
- **The missing-index suggestion generated an index that could not seek.**
  INEQUALITY columns were folded in among the EQUALITY ones, which reads
  harmlessly and silently costs the seek the suggestion existed to buy.
- **New Endpoint's Script Changes ran the wrong statements against the wrong
  instances** — every collected statement in one batch, with nothing saying
  they belong to two or more servers. Each instance's statements are labelled
  and grouped now.
- **Two data races**: Object Explorer and Detail Browser loaders read the live
  tree node from a background goroutine, and the Status History ring was
  appended to — and its editor rewritten — from one.
- **A Database Scoped Configuration holding a keyword rendered as `0`**, a
  number the server never held, sitting on the dirty baseline so Apply would
  not write it back either.
- **A confirmation checkbox could be pressed once and then never again**, its
  drag latch stranded by a release outside the dialog; **Key Diagnostics
  stopped recording after its own Copy button**, on exactly the keys it exists
  to decode.
- **The Activity Monitor was gated on a different connection than it opens**,
  so with nothing selected in the tree the user got the server's refusal where
  a greyed item was due. **Recycle was gated in the tree and ungated in the Log
  File Viewer** — one action, two answers. **Physical memory was spelled two
  ways** on two pages, which read as two different readings.
- **A permission advice named a right the server never did** (Msg 297 is not
  only VIEW SERVER STATE), and three notes asked for the wide 2019-era right
  where SQL Server 2022's narrower one is the least privilege that works. Both
  are named now.
- A scripted sequence restart moved the handle anyway; a scripted mirroring
  endpoint create handed back nothing for the caller's next step; and status
  messages no longer end in a full stop where every other one does not.

### Changes

- `gosmo` v0.0.9 → v0.0.10 — the rest of the object families in the scripter
  (`ScriptTrigger`/`Index`/`CheckConstraint`/`ForeignKey`/`Sequence`/`Synonym`/
  `Schema`/`User`/`DatabaseRole`, the two server principals, the DML templates
  and `sys.parameters` behind them), ten by-name finders, `ObjectFilter` and
  seven filtered listings, `Schema.ObjectCountsByType`, `CycleLog`,
  `Table.DropColumn`/`RenameColumn`, `Database.TransferObject`,
  `CreateColumnEncryptionKey`, the job-step insert/move/reorder family, the
  data space behind a partitioned table's script, the capability probe, and
  `DatabaseCapabilities.Permits`.
- **Go 1.26 → 1.27**, `tcell` v3.4.1 → v3.4.2. Background goroutines now name
  their operation in the header of any traceback they appear in, so a panic in
  the log says what was running rather than naming an anonymous func literal.
- **The Object Explorer folder filter runs at the server** where it can be
  expressed exactly. It is an optimisation and never the meaning of the
  filter: the client-side pass still runs over whatever comes back, and a
  filter that cannot be reproduced exactly is refused rather than approximated.
- **Ten lookups stopped fetching a whole listing to keep one row** — schema,
  index, statistic, foreign key, tracked table, partition function, partition
  scheme, security policy and both Always Encrypted key families now resolve by
  name at the server. **Schema Properties' six-row Object summary went from six
  listings to one query**, five of which were database-wide and two of which
  dragged every view and procedure's full text across the wire to count them.
- **An execution plan carries every document a batch produced**, not only the
  last one. Settled live: every showplan result set holds exactly one row,
  across ten batch shapes.
- **Every Properties page with a real Apply is now driven end to end by a
  test** — 52 pages audited, 17 covered at the start, and 24 mutations run
  against the ones added. `internal/activity` went from 65% to 97%, where the
  gap was every DMV reader, and a scan into the wrong field produces a
  plausible number and no error at all.
- Comment pass over 65 files, keeping the "why" only where getting it wrong
  reintroduces a shipped bug.

## v0.0.7 — 2026-08-19

The Always On release. Availability groups arrive complete — browse a
topology, edit it, watch it on a live dashboard, run every operation on it,
and create one across instances that were never registered — alongside a Log
File Viewer, an Object Explorer folder filter, Delete and Rename on any
object, and a Backup/Restore pair that finally browses the *server's*
filesystem rather than the client's. Updates `gosmo` to v0.0.9.

### New

- **Always On Availability Groups**, end to end. An `Always On High
  Availability` folder in Object Explorer over groups, replicas, availability
  databases and listeners; AG Properties (General, Backup Preferences,
  Read-Only Routing); and a live **dashboard** for one group or for all of
  them, with estimated data loss and estimated recovery time — the two numbers
  SQL Server does not report — and a selectable refresh rate.
- **Every Always On read follows the primary.** The `sys.dm_hadr_*` DMVs
  describe only what the connected instance can see, so a topology read from a
  secondary comes back blank where it matters most. goSSMS opens a connection
  to the primary with the same credentials, says so on screen, and degrades to
  the partial local view — naming the host it could not reach — rather than
  failing.
- **Always On operations**: add and remove a database, join and unjoin a
  secondary's own copy, suspend and resume data movement, add and remove a
  replica, add, modify and remove a listener, drop a group, and fail over,
  planned or forced with data loss. Where an operation runs is part of what it
  means, and each confirmation says which instance it is about to affect.
- **Create an availability group** across instances the user never registered
  — CREATE on the connected instance, then JOIN and the seeding grant run by
  each secondary against itself. Script Changes labels every statement with
  the instance it targets.
- **New Database Mirroring Endpoint** sets up certificates, logins, endpoints
  and grants across all replicas at once, exchanging **public** certificates
  over connections goSSMS already has instead of the documented
  back-up-to-a-file-and-copy-it route. No private key is read, transmitted or
  written, and a half-configured pair is completed rather than refused.
- **Log File Viewer** — SQL Server and SQL Agent error logs, current or
  archived, newest first, with a filter, a details pane for the entries that
  are four lines long, and export.
- **Object Explorer folder filter** — SSMS's Filter Settings over name,
  schema, creation date and memory-optimized. It survives Refresh,
  collapse/expand and a reconnect, and it reaches the Detail Browser beside
  the tree.
- **Delete and Rename on any object**, with the confirmation scaled to the
  blast radius — a plain Yes/No for one object, retype-the-name for a
  database. A database rename offers the forced form, since the tree's own
  connections are enough to deny it exclusive access.
- **Backup and Restore browse the server's filesystem**, with the path rules
  of the host being browsed rather than the client's. Browsing a Windows
  instance from Linux used to produce a destination that could never work,
  scripted with no error at all.
- **Restore File Locations** — a view of its own for relocation: keep the
  backup's paths, relocate when renaming, or move everything into two named
  folders, with a per-file preview and a button that fills in the server's own
  data and log paths.
- **`Ctrl+Z` reverts a Properties page** to the values it loaded with, without
  a round trip.
- **Text files keep their encoding.** UTF-8, UTF-8-with-BOM and UTF-16 are
  detected from a BOM and nothing else, and the file's line endings are
  preserved on save.

### Fixes

- **Every replica but the first was unreachable on six grid pages** — three AG
  Properties pages, three New Availability Group pages and the owner-transfer
  dropdown. Redrawing a grid from inside its own selection callback silently
  undid the move, so neither the mouse nor the keyboard could reach a second
  row, and the first arrow key threw focus out of the grid entirely.
- **`File > Open` was lossy and `File > Save` destroyed the original.** A file
  with a tab, a CRLF or a BOM opened marked dirty, so closing it prompted to
  save — and the save rewrote it. A UTF-8 BOM went to the server inside the
  first batch, and a UTF-16 script came back U+FFFD-laden and unrecoverable.
- **`End` on an empty results grid took the application down**, three
  keystrokes from any query returning no rows.
- **Delete and Rename were offered on system objects**, and renaming a system
  database dropped every connection to it on the way to an error the server
  was always going to return. Login Properties could likewise rename a
  `##MS_*` login.
- **A panic left the UI latched for the object's lifetime**, at 16 sites: a
  query panel refusing every later Execute with a ticker still running, a
  Properties dialog ignoring every button including Cancel, an inert Log File
  Viewer toolbar, IntelliSense never returning for a database, a backup
  counted as running forever, and tree nodes stuck on "Loading...".
- **Long words were never broken and every caller clipped them**, so a
  connection error to a too-long hostname showed forty characters of it and
  never the informative end.
- **A dialog did not follow a terminal resize** — the box kept the rect it was
  centred into, so its entire button row could sit off-screen while it went on
  swallowing every key — and dialog content overflowed the box on a terminal
  too small for it.
- **Ten property-page dropdowns displayed the first option as fact** when the
  server's value wasn't in the list, on exactly the objects an admin opens the
  page to investigate; one of the ten wrote it back.
- **`Ctrl+Z` lied on four grids**, reporting a revert while the grid kept
  every change — enough to create an availability group with a database the
  user had just told the app to forget.
- **Config could be lost two ways**: an unreadable `config.json` read as "no
  config" and was then overwritten with emptiness, and `gossms.key` — the one
  file that can read it — was the only thing not written atomically.
  `WriteAtomic` also stopped re-widening file modes and stopped replacing
  symlinks with regular files.
- **The Detail Browser parked one goroutine per row** waiting on an 8-slot
  gate, and could deadlock the folder outright if a worker died.
- Backup and Restore failure messages no longer clip to one line, the Restore
  script's `MOVE` clauses are visible rather than running off a 300-column
  line, the Destination field no longer draws empty for a short path, the
  endpoint pipeline no longer reads any error as "the login isn't there" or
  trusts a peer certificate by name, "Jobs Without Schedules" no longer hides
  the jobs it couldn't check, and F5 reaches a dashboard panel.

### Changes

- `gosmo` v0.0.8 → v0.0.9 — availability groups end to end, certificates and
  the binary certificate exchange, the database mirroring endpoint, the error
  log, the server's filesystem, an `ErrNotFound` sentinel, and the `Drop` /
  `Rename` gaps filled in across the object families.
- **`Table.Indexes` went from N+1 to two queries** whatever the index count —
  ~640 ms to ~50-120 ms over an 18-index database, output byte-for-byte
  identical.
- **A per-database fan-out was built, measured, and thrown away**: it was
  slower than serial at every width, because each worker needs a physical
  connection and the handshake costs fifty times the query. What was kept is
  that a database whose fetch fails is now skipped rather than failing the
  whole page.
- **`Drop` no longer means "or it was already gone"** — gosmo's drop methods
  lost `IF EXISTS`, so "deleted" means deleted. Generated scripts keep it,
  since they exist to be re-run.
- One implementation each, where there had been several: the grid-plus-editor
  idiom, the dropdown that must not misreport a server value, the
  schema-scoped tree loaders, the two history charts, and the panel toolbar
  geometry. `core.Min`/`core.Max` gave way to Go's builtins across 145 call
  sites.
- The T-SQL of every gosmo write method is now pinned by test — 84 of 183 had
  no coverage at all — and the dialog registry is pinned by reflection over
  `App`'s own fields.

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
