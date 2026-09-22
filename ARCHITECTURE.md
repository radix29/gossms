# Architecture

goSSMS is split into an embeddable, application-agnostic TUI library
(`internal/tuikit`) and a thin application layer (`internal/tui`) that wires
it together with SQL Server domain logic via `gosmo`. See
[`internal/tuikit/README.md`](internal/tuikit/README.md) for the library's
design principles and dependency rules.

## Why split this way

`tuikit` contains every piece of rendering, focus, scrolling, and
drag/resize logic exactly once. None of it knows what a "database" or
"stored procedure" is — it operates on generic `Rect`s, `TreeNode`s with an
`any` `Tag` field, and string/string row data. The `tui` package never
re-implements widget mechanics; it only supplies SQL-Server-specific data
and callbacks (`OnExpand`, `OnSelect`, button `Action`s). `tuikit` could
therefore be extracted into its own module and reused by a different tcell
application unmodified.

**That is an invariant, not an observation: `internal/tuikit` must not
import `internal/tui` or `gosmo`.** Its only permitted external dependencies
are `tcell` and `displaywidth`. Check it with:

```bash
go list -f '{{range .Imports}}{{.}}{{"\n"}}{{end}}' ./internal/tuikit/... |
  grep '\.' | grep -v gossms | sort -u    # non-stdlib, non-repo imports
```

That must print exactly three lines — `github.com/gdamore/tcell/v3`,
`github.com/gdamore/tcell/v3/color`, `github.com/clipperhouse/displaywidth`.
Anything else is a layering violation.

## Which document owns what

The same rule stated twice will eventually be stated two different ways.
Before adding to any of them:

| Document | Authoritative for |
|---|---|
| `CLAUDE.md` | Agent-facing working rules that apply to every task: conventions, hygiene, and where to read next. Kept short — it loads every session |
| `docs/ui-rules.md` | The short enforceable form of every `internal/tui`/`internal/tuikit` idiom: widgets, grids, dialogs, clipboard, toolbars, mouse and async |
| `docs/db-rules.md` | Permission gating, the T-SQL a page emits, Object Explorer filters, query execution |
| `docs/testing.md` | What counts as verification: the tmux and live-server harnesses, and the `fakedb_test.go` rules |
| `ARCHITECTURE.md` | This file: package map, layering, data flow, threading, and the long-form *why* behind each idiom |
| `internal/tuikit/README.md` | Everything inside `internal/tuikit` — its package map, dependency direction, widget design rules |
| `docs/open-threads.md` | Work knowingly left undone: unfixed bugs, deferred scope, release blockers |
| `docs/decisions.md` | Settled decisions and deliberate exclusions — the "do not re-raise" record |

`README.md` is user-facing and owns features. The keyboard reference is the
F1 help dialog (`internal/tui/help_dialog.go`) — a key binding change updates
it. When a rule needs to appear in two places, the second one summarizes in a
sentence and links here — it does not restate the reasoning.

## How a query runs

The path from keystroke to result grid, which touches four packages:

1. **`internal/db`** (`connection.go`) owns a connection's *lifetime*.
   `ConnectContext(ctx, opts, role)` maps a `config.Connection` onto
   `gosmo.ConnectionOptions` in one place, `toGosmoOptions` — which the
   Connect dialog's preview (`BuildConnectionString`, a masked
   `ConnectionOptions.ConnectionString`) goes through too, so the preview is
   the DSN dialled — and returns a `ServerConn` wrapping a `gosmo.Server`.
   The `Role` names the session in `program_name` (`goSSMS`, `goSSMS -
   Query`, `goSSMS - Activity Monitor`); cancelling `ctx` aborts the dial. Its `ctx`, exposed by
   `Context()` and cancelled by `Close()`, is the parent every background
   load scoped to that connection must derive from — closing the underlying
   `*sql.DB` alone does not cancel a query already in flight, so a load
   rooted at `context.Background()` keeps a real SQL Server session alive
   after disconnect.
2. **`internal/query`** (`executor.go`, `session.go`) owns *execution*. A
   query window runs on a **`Session`**: one `*sql.Conn` taken out of the
   panel's pool by `Open` (via `gosmo.AcquireConn`, which retries a transient
   liveness failure 3 times with linear backoff, on gosmo's own read-retry
   budget) and held for the panel's lifetime, so temp tables, SET options,
   `USE` and open transactions survive from one Execute to the next, as in
   SSMS. The package-level `Execute` / `ExecuteWithPlan` / … take a `*sql.DB`
   and check a connection out per call instead — database/sql resets a
   returned connection (the TDS reset-connection bit) on its next checkout, so
   they suit one-shot callers only (the Activity Monitor's procedure tab). Both
   share `runScript`: optionally wrap the run in `SET STATISTICS XML ON` /
   `SET SHOWPLAN_XML ON`, then split the script on `GO` with
   `go-mssqldb/batch` and run each batch through `runBatch`. One `Result`
   accumulates every result set, every message, and the captured plan XML
   across all batches; a Session run adds the state it left
   (`DB_NAME()`, `@@TRANCOUNT`, read even after a cancel) and whether the
   session was lost. `Session.Close` *discards* the connection rather than
   pooling it: pooled, a session with an open transaction sits idle holding
   its locks.
3. **The message stream** is where `sqlexp` matters: result sets and
   informational messages interleave on one connection, and `runBatch`
   walks them together. A speculative extra `rows.Next()` here consumes the
   return message and the grid comes up empty — verify against a live
   server, not just a unit test.
4. **`internal/showplan`** owns *parsing*, and nothing else — no TUI and no
   database imports, so it is testable from a file. `ParseAll` turns the
   captured ShowPlanXML documents into one navigable `Plan` of operator
   nodes, which `internal/tui/planview` renders as the Plan/Tree/XML tabs.

`query_panel_exec.go` is the Session's only caller: `QueryPanel.launch` is
the one run-start path (Execute, Results To File, estimated plan) — it runs
the executor on a background goroutine and reports the `Result` back with
`postAndWake`. IntelliSense and catalog reads use the panel's `ServerConn`
pool, never the session, so they never queue behind a running query. A lost
session closes the panel's connection (Query > Reconnect opens a new one);
closing, reconnecting or quitting with `@@TRANCOUNT > 0` asks to commit first
(`confirmOpenTransactions`).

## Threading model

**All UI and widget state belongs to the UI goroutine** — the one running
`App.Run()`. `tuikit` does no locking anywhere (see
`internal/tuikit/README.md`), so touching a widget from any other goroutine
is a data race, not merely bad style.

Background work follows one shape:

- Derive a context from `ServerConn.Context()`, never `context.Background()`
  — see the lifetime rule above. That includes a *dial* that clones an
  existing connection (`connectForQueryPanel`, `connectForActivityMonitor`,
  `amProcTab.activate`): `ConnectContext`'s ctx covers only the attempt, so
  scoping it to the parent cancels a reconnect in flight on disconnect
  without shortening the new connection's own life. The Connect dialog's
  first dial is the one documented exception — there is no `ServerConn` yet
  to derive from, and `connect_dialog.go` says so at the call. For
  cancellable, user-visible work use
  `App.startTask(parent, label)`, which returns a `*Task` and a derived
  context, registers it for the Background Tasks dialog, and gives the user
  a Cancel button.
- Do the work off-thread. Touch nothing the UI owns.
- Report back with **`App.postAndWake(fn)`** — `fn` runs on the UI goroutine.
  `postProgress` and `postTaskDone` are the task-registry wrappers around it
  and follow the identical rule.
- Start it with **`App.safego(what, fn)`** (or `defer a.recoverPanic(what)`
  for a goroutine that isn't a bare `func()`). The UI goroutine can't
  recover a background panic, so without this the process dies before
  `Run`'s `defer screen.Fini()` restores the terminal — the trace lands on
  the alternate screen and vanishes with it, along with unsaved query text.
  `safego` turns it into a status message plus a stack trace in the log.
  Not theoretical: go-mssqldb panics outright on a column type ID it doesn't
  know, and every result set calls `DatabaseTypeName()` on every column.

- If a newer request supersedes this one, own the lifecycle with **`latest`**
  (`latest.go`) rather than a hand-rolled token or cancel — see
  § Latest-only loads: latest.

`Run()`'s loop clears `wakePending`, drains queued callbacks, syncs the
dialog stack, handles one event, then re-syncs and draws. The two idioms
below follow from that: `postAndWake` is how work crosses back onto the UI
goroutine, and the `mouseDragging`/gesture rules govern how one input event
is routed once it is already there.

## Package map

`internal/tui` is a flat package, so every file is listed individually with
its purpose; `internal/tuikit`, `internal/tui/planview`,
`internal/tui/sqlparse`, `internal/tui/dashboard` and `internal/tui/gate` are
summarized by directory and documented in their own README and `doc.go`. A file
absent from a summarized directory has not been omitted — look there directly.

`planview`, `sqlparse` and `dashboard` are leaves: they depend on `tuikit` and
the standard library, never on `tui` itself. That is what makes each
extractable. `dashboard` exists for a second reason: `cmd/amdemo` has to draw
the same dashboards the panel draws without dragging in the whole application.

`gate` is the fourth sub-package and the one that is not a leaf: it imports
`internal/db`, `gosmo` and `internal/tuikit/controls` — `internal/db` and
`gosmo` because the question it answers is "what has this connection been
probed to allow", and `controls` because `gate.Item` wraps a
`controls.MenuItem` in the answer. It knows nothing about `App` — that is what
made it extractable — and the one-way rule it keeps is the same one: `gate`
never imports `tui`. See § Why the permission gate is its own package.

```
gossms/
├── cmd/
│   ├── gossms/               # main entry point
│   ├── plandemo/             # dev harness: hosts planview.PlanView full-screen against a plan file (not part of the release build)
│   ├── amdemo/               # dev harness: hosts the Activity Monitor dashboards full-screen against deterministic mock data (not part of the release build)
│   └── spindemo/             # dev harness: renders every widgets.Spinner side by side, for picking one by eye (not part of the release build)
├── packaging/linux/         # gossms.desktop launcher; the release job ships it with docs/ico/linux's hicolor icons in the Linux archives, the Homebrew formula and the .deb
├── internal/
│   ├── config/              # connection profiles (JSON, in $XDG_CONFIG_HOME/gossms/); tracked.go is the Query Store panel's pinned-query sets, its own file beside config.json
│   ├── db/                  # gosmo connection wrapper: config.Connection → gosmo.ConnectionOptions (toGosmoOptions), per-role application name, masked preview
│   │                        #   peer.go: cached connections to other instances (Always On: read the group from its primary), reached with that instance's own saved credentials
│   │                        #   capabilities.go: the connect-time capability probe (what this login may do) + the lazy per-database one, cached on ServerConn
│   │                        #   entra.go: the process-wide gosmo.EntraCache every Entra connection shares, the device-code prompt hook, and the sign-in phase (SignIn) run before the dial
│   ├── activity/            # Activity Monitor collection: DMV queries, cntr_type decode, wait categories, 30-minute store, collector goroutines, and Poller for a feed whose source is already aggregated — no TUI imports
│   │                        #   proc.go: helper-procedure lookup/install shared by the Block and Sessions tabs; block.go: sp_block; whoisactive.go + whoisactive.sql: the embedded GPL-3.0 sp_WhoIsActive
│   │                        #   tempdb.go + tempdb_collector.go: tempdb space/file/session usage on its own slower cadence
│   ├── query/               # SSMS-style script executor: GO batches, result sets, message stream, plan capture
│   │                        #   arena.go: chunk-packed cell storage for a retained result set; coltype.go: SSMS-style declared type names
│   ├── showplan/            # parses ShowPlanXML (estimated/actual) into a navigable operator tree; compare.go pairs two plans of one query (Compare Showplan). No TUI/DB deps
│   ├── fileutil/            # WriteAtomic: temp file + Sync + rename + syncDir, behind config.json, gossms.key, saved .sql scripts and the log export
│   │                        #   keeps an existing file's narrower mode (perm is a ceiling) and writes through a symlink, dangling or not
│   ├── version/             # gossms's own version metadata (mirrors gosmo/version); overridable via -ldflags -X
│   │
│   ├── tuikit/               # embeddable TUI library (no SQL Server / app knowledge) — see internal/tuikit/README.md
│   │   ├── theme/                # colour palette + derived tcell.Style helpers
│   │   ├── core/                 # Rect geometry, drawing primitives, string/int helpers
│   │   ├── widgets/               # InputField, DropDown, CheckBox, Button, RadioBox, Spinner (a busy indicator as a pure function of elapsed time)
│   │   ├── layout/                # Panel interface, PanelManager (tabs), Splitter
│   │   ├── dialogs/                # ModalDialog base (focus trap), Properties/Alert/Confirm/Progress/FileDialog (+ FileSystem: local or remote), FieldGesture (the text-field drag latch)
│   │   ├── charts/                 # terminal charts from generic series data: off-screen canvas, scales, block glyphs, axis/legend, history/stacked/bar/KPI types
│   │   ├── controls/                # MenuBar, ContextMenu, Toolbar, TabStrip, TreeView, DataGrid, ListBox, Editor (+SQL/XML highlighters)
│   │   └── propsheet/               # PropertySheet — multi-page editable properties dialog framework (rows incl. EditorRow, the embedded multi-line controls.Editor)
│   │
│   └── tui/                  # goSSMS application layer (built on tuikit)
│       ├── dashboard/            # Activity Monitor dashboard layout: draws a HistoryView/SampleView/TempDBView/InstanceView with tuikit/charts; no App, no connection
│       ├── gate/                 # the permission gate: gate.RightsAllow — the right(s) each action needs (server-, database-, schema- or object-scoped), the object/column/schema DENY asked first, and the fail-open rule that withholds a menu/toolbar/context item only on a measured "no". The banner's check and the menus' gate are this one function
│       ├── planview/             # reusable control rendering a parsed plan: Plan (graph)/Tree/XML tabs
│       ├── sqlparse/             # T-SQL lexer + statement-scope scanner behind IntelliSense: flat FROM/clause scan, ScopeAt's Query tree (CTEs, derived tables, sub-SELECTs, PIVOT) and ScanBindings' batch-wide temp-table/table-variable declarations. Functions over runes; no App, no connection — pure but for PrefixCache (prefix_cache.go), the one stateful type, which makes the completion prefix scan incremental and is owned by the QueryPanel that calls it
│       │
│       │  ── App core ──
│       ├── app.go                # root App orchestrator, event loop, SQL Server object tree fetch
│       ├── app_events.go         # key/mouse dispatch, resize/redraw, top-level event loop plumbing
│       ├── app_connections.go    # connect/disconnect lifecycle, saved-connection bookkeeping, activeServerConn/selectedServerConn helpers
│       ├── app_peer_creds.go     # App's db.PeerCredentials answer: which saved connection to reach a given instance with
│       ├── app_explorer_data.go  # background fetch orchestration, context-menu assembly (nodeMenuItems + insertBeforeRefresh), Script object, View Dependencies, Take Offline/Bring Online task consumer
│       ├── app_panel_actions.go  # opening panels and files: new query panel, open a .sql or .sqlplan, save a plan back out
│       ├── app_panel_close.go    # closing a panel and quitting: Disposable release, the open-transaction commit prompt, the unsaved-query prompts quit walks, activeQueryPanel/withQueryPanel
│       ├── app_query_actions.go  # what the toolbar and Query menu do to the active query panel: execute/cancel, estimated + actual plan, reconnect, results mode, save its text
│       ├── app_show_panels.go    # opens the non-query panels: Object Explorer Details, query list, Activity Monitor, Log Viewer, Query Store — reusing one already open for the same target
│       ├── app_show_properties.go # one entry point per Properties and per New dialog, taking the connection and the names its page needs
│       ├── progress_job.go       # progressJob + App.runWithProgress: one long write run behind dialogs.ProgressDialog, from the answered confirmation until the server comes back; reveal delay, cancellation, and the reasons an uninterruptible job greys Cancel
│       ├── dialog_stack.go       # z-ordered Dialog stack: draw/input routing for every modal dialog
│       ├── menu.go               # top menu bar structure (File/Edit/View/Query/Tools/Help), context-gated via each MenuItem's Enabled predicate, + About dialog
│       ├── toolbar.go            # icon-only quick-action toolbar sharing the menu bar's row, same Enabled-predicate gating
│       ├── tree_node.go          # NodeType enum + style-aware icon lookup (Emoji/Symbols/Portable/None) + name lookup
│       ├── object_explorer.go    # owns the SQL Server tree model; drives controls.TreeView
│       ├── explorer_loaders.go   # childLoader and nodeMenus registries (NodeType → fetch func, NodeType → menu builder) + shared loader helpers
│       ├── explorer_databases.go # loaders: server root, Databases/System Databases, one database's folders
│       ├── explorer_objects.go   # loaders: Tables/Views/Procs/Functions/Triggers/Sequences/Synonyms + System Views/Procedures/Functions folders + table columns
│       ├── explorer_security.go  # loaders: server Security folder — Logins, Server Roles, Credentials, Audits, Server Audit Specifications
│       ├── explorer_storage.go   # loaders: a database's Storage folder — Partition Functions, Partition Schemes
│       ├── explorer_management.go # loaders: Server Objects folder (Backup Devices, Endpoints, Linked Servers, Server Triggers), Management folder, SQL Server Logs / Agent Error Logs file lists
│       ├── explorer_alwayson.go # loaders: Always On High Availability — Availability Groups, Replicas, Databases, Listeners; follows the primary via db.ServerConn.Peer
│       ├── explorer_programmability.go # loaders: Programmability > Types (five sub-folders), Assemblies, Rules, Defaults, Plan Guides
│       ├── explorer_external.go  # loaders: External Resources — External Data Sources, File Formats, Libraries (Libraries omitted before 2017)
│       ├── explorer_service_broker.go # loaders: a database's Service Broker folder — Message Types, Contracts, Queues, Services, Routes, Remote Service Bindings, Broker Priorities (listed whether or not the broker is enabled)
│       ├── explorer_drag.go      # drag a tree node into a query editor as a quoted T-SQL identifier
│       ├── explorer_filter.go    # per-folder filter model (SSMS Filter Settings): properties, operators, matching; applied in fetchChildren
│       ├── explorer_object_ops.go # general Delete/Rename/Move to Schema: the per-NodeType drop/rename table and the gosmo adapters behind it
│       ├── explorer_object_menu.go # the Rename/Delete menu pair a node offers, and the schema/object name a gate question is scoped by
│       ├── explorer_object_rights.go # what permits Rename/Move/Delete per NodeType: the securable, principal, database- and server-scoped right tables
│       ├── explorer_object_actions.go # the flows themselves: confirmation (incl. the cascade checkbox), script-instead-of-run, the prompt, and the parent-folder refresh
│       ├── scripting.go          # Object Explorer's "Script <Noun> as ▸" cascade: which verbs each NodeType offers, how each is generated, and the three destinations
│       ├── system_principals.go  # which of the principals SQL Server creates for itself count as built-in (no Delete, no Rename)
│       ├── db_scan.go            # eachDatabase / onlineDatabases: the shared per-database fetch a page runs over every database it can query
│       ├── tasks.go              # background task registry: Task (progress/cancel), App start/postProgress/postTaskDone
│       ├── latest.go              # the shared latest-only fetch lifecycle: supersede the run in flight, cancel it, drop its stale result — see § Latest-only loads
│       ├── safego.go             # App.safego/safegoRepair/recoverPanic, and fanOut — the one bounded worker pool; every background goroutine runs under one
│       ├── prop_page_gate.go    # withRequires/withRequiresOn: attaches a gate.Right set to a propPage, the one tie between the gate package and the Properties dialogs
│       ├── edition_gate.go       # gateAzure: what the *engine edition* refuses, in the gate package's shape and composed outside it — the edition's note wins, since no permission gets a user past a statement the edition does not implement
│       ├── permission_display.go # capabilitySet + knownDenied: what a page renders when a value could not be read (N/A, never 0)
│       ├── permission_error.go   # classifies a SQL Server refusal and names the right it wants, instead of the wrapped driver error
│       ├── panel_toolbar.go      # the one-row toolbar shared by Activity Monitor, the Log File Viewer and Query Store, incl. the "More ▾" overflow menu a too-narrow row collapses into (not App's own toolbar)
│       ├── dialog_common.go      # focus/layout behaviour shared by the hand-rolled dialogs (Connect, Backup, Restore, Tasks, Query List)
│       ├── text_encoding.go      # decodeTextFile/encodeTextFile — BOM-detected encoding and the file's own line endings, so File > Save writes back what File > Open read
│       ├── database_list.go      # the one rule for which databases a dropdown offers: all of them when the name is resolved later, only backup-able ones when acted on now
│       ├── clipboard.go          # copy/cut/paste plumbing shared by editor and dialog text fields, incl. bracketed-paste buffering
│       ├── os_clipboard.go       # OS-native clipboard, shelled out per-platform (fallback path for clipboard.go)
│       │
│       │  ── Query panel & IntelliSense ──
│       ├── query_panel.go        # QueryPanel state/layout, implements layout.Panel
│       ├── query_panel_draw.go   # QueryPanel rendering: editor/results split, tab strip, results status line
│       ├── query_panel_input.go  # QueryPanel HandleKey/HandleMouse, incl. the results-side drag zones
│       ├── query_panel_exec.go   # Execute/Execute Selection/Cancel, plan-capture wiring
│       ├── query_panel_tabs.go   # result-set tabs + Messages tab
│       ├── query_panel_plan.go   # Estimated/Actual Execution Plan tabs, backed by planview.PlanView
│       ├── query_panel_export.go # Results To File (Text/Grid/File modes)
│       ├── column_meta.go        # the Output Column Metadata block folded into a result's Messages
│       ├── cell_value.go         # classifies a grid cell as plain/XML/JSON and routes the last two to their own panel
│       ├── plan_panel.go         # pops an Execution Plan tab out into its own closable panel
│       ├── completion_provider.go   # SQL completion.Provider: cursor-context resolution (FROM-scope, qualifiers) against the cached inventory
│       ├── completion_inventory.go  # per-database + per-server(sys schema) catalog cache for IntelliSense, async load
│       ├── completion_candidates.go # schema/table/column candidate lookup against the cached inventory
│       ├── completion_relations.go  # resolves FROM-scope refs (CTEs, derived tables) to columns, with depth/cycle guards
│       │
│       │  ── Activity Monitor ──
│       ├── activity_monitor.go        # ActivityMonitor state: tabs, toolbar, per-tab scroll, teardown; implements layout.Panel
│       ├── activity_monitor_draw.go   # tab/toolbar rows, dashboard canvas blit, both scrollbars
│       ├── activity_monitor_input.go  # HandleKey/HandleMouse: tab switching, scrolling, gesture zones, scrollbar drags
│       ├── activity_monitor_history.go # activity.Store → HistoryView: one charts.Series per metric, colour roles
│       ├── activity_monitor_sample.go  # activity.Store.Latest() → SampleView: bars, KPIs, memory composition
│       ├── activity_monitor_tempdb.go # TempDB tab: space stack, per-file bars, top-session usage grid, configuration advisory
│       ├── activity_monitor_instance.go # Instance tab (Azure editions only): the server's own pre-aggregated resource history, governor limits, job object
│       ├── activity_monitor_tooltip.go # click-pinned chart readout: hit-test against the canvas, frame + text drawn over the viewport
│       ├── chart_tooltip.go      # the pinned chart readout box itself, shared by Activity Monitor and the Detail Browser's disk-usage strip
│       ├── activity_monitor_proctab.go # Block and Sessions tabs: own connection, procedure lookup/install, Refresh + Install in master, result grid
│       │
│       │  ── Log File Viewer ──
│       ├── log_viewer.go              # LogViewer state, construction and layout: the selectors, the filter field and the grid; implements layout.Panel
│       ├── log_viewer_toolbar.go      # the toolbar cells: labels, disabled reasons, and the wording that names the file or files on screen
│       ├── log_viewer_load.go         # the read: which files the panel is pointed at, the concurrent read of each, and the recycle that renumbers them
│       ├── log_viewer_rows.go         # what the read becomes on screen: grid columns, the client-side filter, the summary line, the text helpers both use
│       ├── log_viewer_files.go        # choosing files: the family menu, the file menu, the multi-file checklist, Export, and the App-level recycle/refresh entry points
│       ├── log_viewer_draw.go         # toolbar row, entry grid, splitter, selected-entry details pane
│       ├── log_viewer_input.go        # HandleKey/HandleMouse: filter/grid focus, details scroll, gesture zones
│       ├── log_search_dialog.go       # Log File Viewer search: a query the server runs across the archives, not a filter over what was read
│       │
│       │  ── Query Store ──
│       ├── query_store_reports.go     # the seven SSMS views: one table of title/description/default statistic/loader, and the typed rows (qsResult) both surfaces render — the Detail Browser's grid and the panel's chart
│       ├── query_store_panel.go       # QueryStorePanel state, construction and layout: the selectors' current values, the grids and the two splitters; implements layout.Panel
│       ├── query_store_panel_toolbar.go # the two tool rows: each cell's label, its disabled reason, and the menu each selector pops
│       ├── query_store_panel_load.go  # the report read: the options the selections come to, the tracked-query set, the load, the plan pane's own read and the summary line
│       ├── query_store_panel_plans.go # what the plan pane's selection can do: Force/Unforce, Show Plan, Compare Plans, Script the force
│       ├── query_store_panel_draw.go  # the two toolbar rows, the bar chart, and the two grids either side of the splitters
│       ├── query_store_panel_input.go # HandleKey/HandleMouse: grid focus, splitter keys, gesture zones
│       ├── query_store_series.go      # the panel's second chart mode: the cursor's query plotted per plan, interval by interval — a mode of the chart, not an eighth report
│       ├── plan_compare_panel.go     # Compare Showplan: two plans of one query as two grids (operators, statement properties); implements layout.Panel
│       │
│       │  ── Detail Browser ──
│       ├── detail_browser.go            # Detail Browser, implements layout.Panel
│       ├── detail_browser_backfill.go   # bounded per-row backfill fan-out shared by the folder loaders below
│       ├── detail_browser_server.go     # Server node: version/edition/paths/CPU/memory, then NUMA + disk volumes
│       ├── detail_browser_databases.go  # Databases folder: name/state/recovery, then per-database size backfill
│       ├── detail_browser_logins.go     # Logins folder
│       ├── detail_browser_tables.go     # Tables folder: name, then per-table row count/space backfill
│       ├── detail_browser_storage.go    # Storage folders: partition functions and schemes
│       ├── detail_browser_security.go   # server Security families that are not logins: Credentials, Audits, Server Audit Specifications
│       ├── detail_browser_programmability.go # Programmability families: the Types folders and members, Assemblies, Rules, Defaults, Plan Guides
│       ├── detail_browser_external.go   # External Resources: external data sources, file formats, libraries
│       ├── detail_browser_snapshots.go  # Database Snapshots folder and one snapshot
│       ├── detail_browser_service_broker.go # the seven Service Broker families: each folder and its leaves, each leaf reusing its Properties page's finder
│       ├── detail_browser_charts.go     # composition bars under the grid (a database's disk usage) and their pinned tooltip
│       ├── detail_browser_ops.go        # the pane's write path: Delete over the grid's block/Ctrl+click selection (SelectedRows, never SelectionBounds)
│       │
│       │  ── SQL Server Agent ──
│       ├── agent_common.go              # shared Job/Alert/Notify enum formatters, generic async enable/disable/delete plumbing for every Agent entity
│       ├── agent_menu.go                # Agent node context menus (Start/Stop/Enable/Disable/Delete/View History) + New Job/Schedule/Alert/Operator entry points
│       ├── agent_explorer.go            # loads the Agent subtree: Jobs (User/System split)/Schedules/Alerts/Operators/administration reports folder
│       ├── agent_detail.go              # Object Explorer Details grids for every Agent node type (server/job/schedule/alert/operator/activity/history/categories)
│       ├── agent_reports.go             # the "SQL-only administration" folder's canned reports, plus the View History query behind a job's History action
│       ├── agent_job_props.go           # Job Properties dialog: page-set wiring + General/Targets page definitions
│       ├── agent_job_props_steps.go     # Job Properties Steps page: step grid, Start at Step, ordered update/delete/add/reorder apply
│       ├── agent_job_step_panel.go       # the "Selected step" edit panel shared by Job Properties > Steps and New Job > Steps — the one mapping to and from a jobStepEdit
│       ├── agent_job_props_schedules.go # Job Properties Schedules page: attach/detach toggle grid against every shared schedule on the server
│       ├── agent_job_props_alerts.go    # Job Properties Alerts (job-response link toggle) and Notifications (e-mail operator/auto-delete condition) pages
│       ├── agent_job_props_history.go   # Job Properties read-only History page: recent run-level outcomes + selected-run message detail
│       ├── agent_schedule_props.go      # Schedule Properties dialog: General (identity/frequency/owner) built on agent_schedule_form.go + read-only Jobs page
│       ├── agent_schedule_form.go       # shared Occurs/Recurs-every/Weekdays/Relative/Daily-frequency/Duration form used by both New Schedule and Schedule Properties
│       ├── agent_alert_props.go         # Alert Properties dialog: General (identity/trigger/response scope/notification) + Response (operators to e-mail/response job)
│       ├── agent_operator_props.go      # Operator Properties dialog: General (identity/e-mail/category) + read-only Notifications (linked alerts/jobs) page
│       ├── new_job_dialog.go            # New Job — newObjectDialog config (prefetch + create) for a job
│       ├── new_job_pages.go             # New Job's General/Steps/Schedules/Notifications page builders
│       ├── new_schedule_dialog.go       # New Schedule: General page from agent_schedule_form.go + a Jobs-to-attach page
│       ├── new_alert_dialog.go          # New Alert: General (alert definition) + Response (operators to e-mail) pages
│       ├── new_operator_dialog.go       # New Operator, a single General page
│       │
│       │  ── Standalone dialogs ──
│       ├── connect_dialog.go     # Connect dialog — state, focus ring, History pane, options/prefill, connect lifecycle
│       ├── connect_dialog_draw.go  # Connect dialog — two-pane/one-pane layout, tab bar, History pane and section rules
│       ├── connect_dialog_input.go # Connect dialog — keys, buttons and the mouse ordering (overlays, tabs, then panes)
│       ├── device_code_dialog.go # Microsoft Entra Device Code sign-in: the code + URL, open for exactly as long as the sign-in
│       ├── find_replace_dialog.go # Edit > Find/Replace — one dialog in two modes, over controls.Editor's search engine
│       ├── filter_dialog.go      # Object Explorer > Filter Settings — one operator/value row per filterable property
│       ├── options_dialog.go     # Tools > Options — icon style, max cell length, IntelliSense on/off, saved to config.json
│       ├── query_list_dialog.go  # Tools > Query List — switch between open query panels
│       ├── tasks_dialog.go       # Tools > Background Tasks — live task list + Cancel
│       ├── help_dialog.go        # F1 help modal (embeds dialogs.ModalDialog)
│       ├── key_diagnostics_dialog.go # Help > Key Diagnostics — shows tcell's decoded Key/Modifiers/rune per keypress
│       ├── status_history_dialog.go  # running, timestamped, capped log of status-bar messages
│       ├── update_check.go       # Help > Check for Updates — GitHub releases API + semver compare
│       ├── update_dialog.go      # UpdateDialog — shows installed vs. latest release
│       ├── properties_dialog.go  # About + Object Dependencies (wraps dialogs.PropertiesDialog, the flat viewer)
│       │
│       │  ── Properties dialogs (propsheet-based) ──
│       ├── prop_dialog.go        # PropDialog — app orchestration for propsheet.PropertySheet on an existing object (lazy per-page loads, dirty-diff Apply)
│       ├── new_object_dialog.go  # newObjectDialog — the shell behind the New <object> dialogs (one prefetch, all pages built at once, ordered create pipeline, Script Changes)
│       ├── prop_grid_helpers.go  # small cross-cutting helpers (boolStr, indexOf, orDefault, credNames, buildFilterInfoForm)
│       ├── extended_properties_form.go # generic extended-properties add/edit/delete grid + the shared Extended Properties page every in-database object uses
│       ├── role_descriptions.go  # fixed descriptive text for built-in database/server roles
│       ├── perm_state.go        # the Grant/Grant With Grant/Deny/(none) cell state, the orig→current transition each one needs, and the per-scope gosmo adapters — shared by every permissions grid below
│       ├── securables_matrix.go  # generic database-securable Grant/Deny/Revoke grid + the shared Securables page for a user or database role
│       ├── membership_page.go    # shared Members page (add/remove principals) for Database Role and Server Role Properties
│       ├── owner_transfer_page.go # shared owner-transfer page behind Schema Ownership and Owned Roles
│       ├── server_permissions_matrix.go # server-scope securables grid, used by Server/Login/Server Role Properties
│       ├── effective_perms_page.go # read-only Effective Permissions page for a database principal (database/schema/object scope) and for a login
│       ├── server_props.go       # Server Properties: shared config-row plumbing + page registration
│       ├── server_props_general.go      # Server Properties > General page
│       ├── server_props_memory.go       # Server Properties > Memory page
│       ├── server_props_processors.go   # Server Properties > Processors page (affinity mask bit-twiddling)
│       ├── server_props_security.go     # Server Properties > Security page
│       ├── server_props_connections.go  # Server Properties > Connections page
│       ├── server_props_database_settings.go # Server Properties > Database Settings page
│       ├── server_props_advanced.go     # Server Properties > Advanced page
│       ├── server_props_permissions.go  # Server Properties > Permissions page
│       ├── ag_dashboard.go                  # Always On dashboard panel: refresh loop, estimated data loss / recovery time, replica issues
│       ├── ag_dashboard_draw.go             # Always On dashboard: layout and drawing
│       ├── ag_dashboard_all.go              # Always On dashboard's all-groups view (the Always On root's Show Dashboard): per-group rollup and its issues column
│       ├── ag_props.go                      # Availability Group Properties: page set, General page, agOnPrimary (every page reads and writes through the primary)
│       ├── ag_props_backup.go               # Availability Group Properties > Backup Preferences page
│       ├── ag_props_routing.go              # Availability Group Properties > Read-Only Routing page; routing-list text parser and the three-phase apply order
│       ├── alwayson_menu.go                 # Always On context menus and operations: add/remove database, suspend/resume, listener, remove replica, delete group, failover (with the cluster-type refusal)
│       ├── ag_add_database_dialog.go        # Add Database to Availability Group: eligibility split and the reason each database was left out
│       ├── ag_add_replica_dialog.go         # Add Replica: the endpoint URL read through Connect (the instance's own @@SERVERNAME), then made editable
│       ├── ag_add_listener_dialog.go        # New Availability Group Listener: DNS name, port, DHCP or one address per subnet
│       ├── ag_listener_props.go             # Availability Group Listener Properties: port and added addresses, the only two things MODIFY LISTENER can change
│       ├── new_ag_dialog.go                  # New Availability Group: prefetch, shared page state, and the CREATE/JOIN/GRANT pipeline across three instances
│       ├── new_ag_pages.go                   # New Availability Group's General and Backup Preferences pages; the cluster-type/failover-mode rules
│       ├── new_endpoint_dialog.go            # New Database Mirroring Endpoint: master key, certificate and login per instance, and the public-certificate exchange between them
│       ├── database_props.go               # Database Properties: General/Owner page definitions
│       ├── database_props_files.go          # Database Properties > Files page
│       ├── database_props_filegroups.go     # Database Properties > Filegroups page
│       ├── database_props_options.go        # Database Properties > Options page
│       ├── database_props_permissions.go    # Database Properties > Permissions page
│       ├── database_props_query_store.go    # Database Properties > Query Store page
│       ├── database_props_scoped_config.go  # Database Properties > Scoped Configuration page
│       ├── database_props_change_tracking.go # Database Properties > Change Tracking page
│       ├── database_props_resource_governance.go # Database Properties > Resource Governance page (Azure editions only): governor limits and current usage
│       ├── login_props.go        # Login Properties page definitions
│       ├── table_props.go        # Table Properties page definitions
│       ├── schema_props.go       # Schema Properties page definitions
│       ├── role_props.go         # Database Role Properties page definitions
│       ├── user_props.go         # Database User Properties page definitions
│       ├── server_role_props.go  # Server Role Properties: General/Members/Owned Roles/Securables
│       ├── role_general_page.go  # the General page both role dialogs share, over a deliberately narrow roleWriter (rename + change owner, nothing else)
│       ├── statistics_props.go   # Statistics Properties: General/Columns/Filter/Details/Histogram/Density Vector/Extended Properties
│       ├── index_props.go        # Index Properties: General/Options/Storage/Included Columns/Filter/Fragmentation/Extended Properties
│       ├── key_props.go          # Primary/Unique Key Properties, reusing most of Index Properties' pages
│       ├── fk_props.go           # Foreign Key Properties: single read-only General page
│       ├── partition_props.go    # read-only Properties for a partition function and a partition scheme
│       ├── security_policy_props.go # read-only Properties for a row-level security policy (state is the context menu's Enable/Disable)
│       ├── column_key_props.go   # Properties for the two Always Encrypted keys — the master key read-only, the column encryption key rotating its master key via ADD/DROP VALUE
│       ├── credential_props.go   # Credential Properties: identity and the secret (write-only)
│       ├── audit_props.go        # Server Audit Properties: single General page; its ALTER runs inside a disable window, so a failed re-enable is an applyCommitted error
│       ├── audit_specification_props.go # Server Audit Specification Properties: single General page
│       ├── backup_device_props.go # Backup Device Properties: General + Media Contents, the only place RESTORE HEADERONLY is run
│       ├── endpoint_props.go     # Endpoint Properties: General + Type Properties (protocol and payload)
│       ├── server_trigger_props.go # Server Trigger Properties: General + Definition, which a Detail Browser grid row cannot show
│       ├── database_trigger_props.go # Database Trigger Properties (database-scope DDL): General + Definition
│       ├── database_audit_specification_props.go # Database Audit Specification Properties: the audit it binds to, its action groups and its per-securable actions
│       ├── database_credential_props.go # Database Scoped Credential Properties: identity and the secret (write-only, and never blank-alterable — an omitted SECRET sets the stored one to NULL)
│       ├── certificate_props.go  # Properties for a certificate: General (the owner is its one write) and Signatures
│       ├── certificate_backup_dialog.go # Back Up Certificate: BACKUP CERTIFICATE to a file on the server, the private key to a second one when named
│       ├── new_certificate_dialog.go  # New Certificate: a generated certificate, private key under the master key or a password
│       ├── asymmetric_key_props.go  # Properties for an asymmetric key: General (the owner is its one write) and Signatures
│       ├── key_actions.go        # what the three key families share: the Owner row (ALTER AUTHORIZATION drops explicit permissions) and Remove Private Key
│       ├── key_signatures_page.go # the Signatures page of Certificate / Asymmetric Key Properties (ADD / DROP SIGNATURE), and a module's "Signed by" Details rows
│       ├── new_asymmetric_key_dialog.go  # New Asymmetric Key: a generated RSA_2048/3072/4096 key pair, private key under the master key or a password
│       ├── symmetric_key_props.go  # Properties for a symmetric key: General (the owner is its one write; algorithm and material are fixed at CREATE), and Encryption — add / remove its encryptions, each opening the key in the same batch
│       ├── new_symmetric_key_dialog.go  # New Symmetric Key: AES_128/192/256, encrypted by a certificate, an asymmetric key and/or a password, optional KEY_SOURCE / IDENTITY_VALUE (scripted as placeholders)
│       ├── master_key_props.go   # the database master key's node: Properties (General, and Encryption — the service master key's and password encryptions) and its Details view
│       ├── master_key_dialogs.go # Back Up Master Key and Regenerate Master Key, opening the key by password when the service master key does not
│       ├── master_key.go         # ensureMasterKey — the create-the-database-master-key-if-absent step the endpoint and key dialogs share — and keyProtectionFields, the private-key / master-key sections of New Certificate and New Asymmetric Key
│       ├── database_snapshot_props.go # read-only Properties for a database snapshot: General + Files
│       ├── assembly_props.go     # read-only Properties for a CLR assembly: General, Files, Routines
│       ├── type_props.go         # read-only Properties for the four user-type families, system data types and XML schema collections
│       ├── rule_default_props.go # read-only Properties for a standalone rule and a standalone default
│       ├── plan_guide_props.go   # Plan Guide Properties: General (enable/disable) + read-only Query
│       ├── external_resource_props.go # read-only Properties for external data sources, file formats and libraries
│       ├── queue_props.go        # Queue Properties (writable): the ALTER QUEUE settings that change in operation; its ALTER rights are not its Delete's
│       ├── route_props.go        # Route Properties (writable): ALTER ROUTE, which cannot clear a setting — emptying a row is an error, never a no-op
│       ├── service_props.go      # read-only Properties for a Service Broker service
│       ├── contract_props.go     # read-only Properties for a contract (there is no ALTER CONTRACT at all)
│       ├── message_type_props.go # read-only Properties for a message type
│       ├── remote_service_binding_props.go # read-only Properties for a remote service binding (works on MI; only its CREATE is edition-gated)
│       ├── broker_priority_props.go # read-only Properties for a conversation priority: the right that would gate its ALTER cannot be read
│       │
│       │  ── New <object> dialogs ──
│       ├── new_database_dialog.go # New Database — newObjectDialog config, runs CREATE DATABASE
│       ├── new_database_pages.go  # New Database's page definitions
│       ├── detach_database_dialog.go # Detach Database — newObjectDialog config, runs sp_detach_db and shows the files it leaves on disk
│       ├── attach_database_dialog.go # Attach Database — browses the server for a primary .mdf, reads its file list back with DBCC CHECKPRIMARYFILE, runs CREATE DATABASE ... FOR ATTACH
│       ├── new_login_dialog.go    # New Login — newObjectDialog config, runs CREATE LOGIN
│       ├── new_login_pages.go     # New Login's page definitions
│       ├── new_index_dialog.go    # New Index — the six index types the Indexes folder offers, one newObjectDialog config
│       ├── new_index_pages.go     # New Index's page definitions; which pages exist follows the index type
│       ├── new_statistics_dialog.go # New Statistics — column list, filter, sampling, NORECOMPUTE, INCREMENTAL
│       ├── new_column_master_key_dialog.go     # New Column Master Key — the 0x… signature is pasted; it cannot be computed here
│       ├── new_column_encryption_key_dialog.go # New Column Encryption Key — likewise for ENCRYPTED_VALUE
│       ├── new_credential_dialog.go            # New Credential
│       ├── new_audit_dialog.go                 # New Server Audit — file/application-log/security-log destination and the queue-delay rule
│       ├── new_audit_specification_dialog.go   # New Server Audit Specification — the audit to bind to and its action groups
│       ├── new_backup_device_dialog.go         # New Backup Device — the disk or tape alias a backup destination can name
│       ├── new_database_audit_specification_dialog.go # New Database Audit Specification — the audit to bind to, its action groups and its per-securable actions
│       ├── new_database_scoped_credential_dialog.go   # New Database Scoped Credential
│       ├── new_snapshot_dialog.go              # New Snapshot — CREATE DATABASE … AS SNAPSHOT OF, data files only
│       │
│       │  ── Backup & Restore ──
│       ├── backup_common.go      # helpers shared by the Backup and Restore dialogs
│       ├── server_filesystem.go  # dialogs.FileSystem over gosmo — what Browse lists is the SQL Server host's disks, not this machine's
│       ├── backup_dialog.go      # Back Up Database dialog — options form + in-place progress
│       ├── backup_dialog_draw.go # Back Up Database rendering
│       ├── backup_dialog_input.go # Back Up Database HandleKey/HandleMouse
│       ├── restore_dialog.go     # Restore Database dialog — options form, backup-set inspection
│       ├── restore_dialog_draw.go  # Restore Database rendering
│       ├── restore_dialog_input.go # Restore Database HandleKey/HandleMouse
│       ├── restore_dialog_ops.go # Restore Database's background-task execution + history/file-list lookups
│       └── restore_dialog_files.go # Restore's File Locations view: the three relocation choices, the per-file preview and the MOVE clauses behind it
```

### Why the permission gate is its own package

`internal/tui/gate` is the one piece carved out of the flat package, and the
measurement behind it is the reason nothing else is. The 2026-09-17 review
asked whether `internal/tui`'s size costs anything real: 228 non-test files and
62 374 LOC, with 132 of them — 47 % — never mentioning `*App` at all. The gate
was the pilot, chosen because it is the largest App-free cluster with a clean
seam.

What actually moved is smaller than it looks. `permission_gate.go` (1 127 LOC)
and its names test (316 LOC) are App-free and moved whole; the other seven gate
test files — 3 200 LOC — drive `App.objectOpsMenuItems`, `explorerNode` and
`propPage`, so they are menu tests, not gate tests, and stayed. `withRequires`
and `withRequiresOn` stayed too, in `prop_page_gate.go`: they take a `propPage`.

The number, `touch internal/tui/app.go` → `go test -c ./internal/tui/`, eight
interleaved pairs on one machine:

| | median | mean | range |
|---|---|---|---|
| Before | 3.9 s | 3.90 s | 3.08 – 4.56 s |
| After | 3.4 s | 3.51 s | 2.56 – 4.66 s |

The run-to-run spread is larger than the difference, and 1 127 LOC is 1.8 % of
the package, so there is nothing here to extrapolate from: **splitting
`internal/tui` does not buy back compile time.** The remaining clusters
(`new_*`, `agent_*`, `detail_*`, `database_*`) are not worth the same treatment
on that argument, and the flat package stands. Do not re-open it on a
build-speed premise without a new measurement.

What the extraction did buy is a boundary the compiler enforces: `gate` cannot
reach `App`, so the fail-open rule cannot quietly acquire a dependency on
application state. That, not the clock, is why it stayed split.

## Common tasks

### Adding a new dialog

Give `App` a typed field for it, construct it in `App.buildUI`, and append
it to `a.allDialogs` — those three are the whole App-level change.
`dialog_stack.go`'s `syncDialogStack` notices it the moment its own `Show()`
(or `Prompt()`/`ShowXxx()`) flips it visible, pushes it to the top of the
z-order, and routes it all input until it closes itself; draw order, key
routing, and mouse routing all follow from the stack without touching
`app.go` or `app_events.go` again. For the widget itself, follow the
`ModalDialog` skeleton in `internal/tuikit/README.md`.

### Adding a Properties page

A `PropDialog` is a `[]propPage` (`prop_dialog.go`): each entry is a title
plus a `load` func that builds the page's rows and closes over pointers to
them, so Apply can diff what changed. Pages load lazily on first visit. Add
a builder alongside the object's existing `*_props*.go` files and register
it in that object's page slice — `server_props.go`'s page registration is
the clearest example, and every `*_props*.go` file follows it. A page
that renames its object marks `propPage.renames` so its apply runs last, and
must thread the name as a `*string` shared across pages, or every later page
uses the stale one.

### Adding an Object Explorer node type

Add the `NodeType` and its icon/name in `tree_node.go`, then register a
`childLoader` for it in `explorer_loaders.go`'s `childLoaders` map. The
loader receives a `loaderCtx` and the node, and returns child nodes; it runs
off the UI goroutine, so it obeys the threading model above. Group the
loader itself with its peers (`explorer_databases.go`, `explorer_objects.go`,
`explorer_security.go`, `explorer_management.go`, `explorer_alwayson.go`).
Its context menu is a `menuBuilder` registered in the same file's `nodeMenus`
map and written beside the loader; a type with no entry gets New Query and
Refresh, and a leaf whose only command is Properties uses
`propertiesOnlyMenu`. Script as, Rename/Delete and Filter are not part of the
builder — `contextMenuItemsForNode` adds them from their own tables.

### Adding a menu or toolbar item

Both are built in `menu.go` / `toolbar.go` and both gate on an `Enabled`
predicate — `Enabled: func() bool { return a.selectedServerConn() != nil }`
is the common shape. An item that can be invoked when its action is
impossible must be gated, not left to no-op silently.

## The mouseDragging idiom

tcell's all-motion mouse tracking resends the held button on every motion
event, not just on an actual click — so any widget that fires an action on
`Button1` (a toolbar button, a menu label, a tree-node toggle) needs a latch
(conventionally named `mouseDragging`) that's set on the triggering press
and cleared on the matching `ButtonNone` release, or the same action refires
on every motion event while the button stays down. This latch is per-widget:
it only guards a resend that stays over the widget that armed it.

A router that dispatches by screen position (`App.handleMouse`) sees every
event regardless of where the gesture started, so a drag that begins
elsewhere and drifts across a latch-owning widget's row arrives as a
fresh-looking `Button1` — the per-widget latch was never armed for *this*
gesture. Routers need a gesture-wide flag (`App.mouseButtonDown`,
set/cleared from the raw event above all positional branching) to tell a
fresh press from a continuation.

That only says a press isn't fresh, not where the continuation goes. So
every positional router records the region that claimed the fresh press and
replays to it until the release, and a gesture can't change owner halfway
through. Three, all the same shape — `App.gestureOwner` (`app_events.go`),
`QueryPanel.dragZone` (`query_panel.go`), `propsheet.PropertySheet.dragZone`
(`sheet_input.go`) — each an `armGesture`/`armDrag` at every branch that
claims a press plus a `routeGesture`/`routeDrag` that replays held events.
Regions that already acted (a toolbar button, a tab switch) swallow the
repeats. `Splitter` is the per-widget half: it starts a resize only from a
press landing on its bar, so a selection drag crossing it doesn't grab it.

`App` also snapshots the modal layer per gesture
(`gestureOverlay`/`overlaySnapshot`) and drops held `Button1` events across
a change. A dialog sees no events until shown, so the first `Button1`
reaching it reads as a press to `ModalDialog.ButtonClicked` — without the
snapshot, clicking a context-menu item that opens a dialog and twitching
before release fires whichever button the pointer landed on.

The mirror image is a latch outliving its gesture. A dialog button closes
its dialog on the *press*, so the release never reaches
`ConsumeOutsideClick`'s reset — `HandleMouse` returns early on `!visible`
and `syncDialogStack` has already popped the dialog, so `App` routes the
release elsewhere. `mouseDragging` then survived into the next showing and
`ButtonClicked` refused its first click: the dialog looked frozen.
`ModalDialog.Show()` clears both latches for that reason.

Two consequences: an overlay-owning widget drawn last (see
`internal/tuikit/README.md`'s "overlays drawn last") gets first refusal of
every key/mouse event while open; and a host with an early `return` in
`HandleMouse` must forward `ButtonNone` to any latch-bearing child before
returning, or a drag ending outside its bounds leaves the child's latch
stuck and silently swallowing its next press.

### dialogs.FieldGesture

A dialog with a text field is the second half of that last consequence, and
it is common enough to have a type. A click inside a `widgets.InputField`
starts a text-selection drag, and the dialog must (1) end it on the release
*wherever the pointer landed*, (2) replay motion to the owning field without
hit-testing, and (3) drop the latch on `Show`. Each of the three has a
placement that is not local to the call:

- `Release` goes at the very top of `HandleMouse`, **before**
  `ConsumeOutsideClick` and before any early return for a dialog mode. Both
  return without looking at the latch, and a release outside the dialog — or
  one arriving after the dialog switched to a progress view — is precisely
  the event that strands it.
- `Replay` goes after `ConsumeOutsideClick` and before any hit-testing.
  Hit-testing a motion event ends the selection the moment the pointer leaves
  the field's rect; letting it reach `ButtonClicked` fires a button the moment
  a selection drag wanders over the button row.
- `Clear` goes in `Show`, per invariant 4 above. It drops the field's own
  `mouseDragging` too: Connect, Options, Find/Replace and Log Search build
  their fields once, so a dialog dismissed mid-drag hands back a field still
  latched, and its first press after the reopen took the continued-drag branch
  and armed no anchor.

Seven dialogs had hand-rolled this, each with a comment restating a different
part of the reasoning; `dialogs.FieldGesture` now holds it once and each
dialog keeps only its own hit-testing and focus handling, which is where they
legitimately differ. `TestFileDialogDragOutOfPathFieldKeepsExtending` and the
five in `internal/tui/dialog_drag_test.go` are the coverage — a mutation to
any of the three methods fails all of them.

## Async result delivery: postAndWake

A background goroutine reports its result with `App.postAndWake(fn)`, which
queues `fn` for the UI goroutine and wakes the event loop to run it — never
its two halves (`postEvent` then `wakeEventLoop`) by hand.

It is one helper because the wakeup must be sent **outside** the `postEvent`
closure, right after the `postEvent(...)` call, still on the background
goroutine. `Run()`'s loop only drains queued callbacks when it wakes for an
event on `EventQ()`; nest the wakeup inside the very closure waiting to be
drained and nothing ever wakes the loop to drain it — the result sits
queued and invisible until an unrelated keypress drains it as a side
effect. Shipped bug: Object Explorer nodes stuck on "Loading...", in every
async operation in `internal/tui` at the time.

A bare `wakeEventLoop()` is legitimate only where there is no callback to
post, just a frame to redraw on a clock. `App.animateUntil` (`app.go`) is that
loop, written once: the `QueryPanel` elapsed-time ticker, the create dialog's
spinner, the properties spinner and the progress dialog all go through it.
`ConnectDialog`'s connect spinner (`connect_dialog.go`) is the one site that
still hand-rolls the same ticker instead of calling it — its own comment says
as much ("A bare wake, the way `QueryPanel`'s elapsed timer does it"), and its
`attempt` channel is already `animateUntil`'s `done`.

### The other direction: FileDialog.showBusy

`dialogs.FileDialog` is the one place that paints *outside* the app's draw
cycle, and it is not an exception to the rule above so much as the absence of
one: `dialogs.FileSystem` is synchronous, so a remote implementation
(`internal/tui/serverFS`) spends a network round trip inside the event
handler and the loop cannot post anything until it returns. `showBusy` draws
the dialog with a "Listing ..." line and calls `Screen.Show()` before the
call, so the wait is legible instead of looking like a hang — a listing of
`C:\Windows\System32` over the wire used to sit there for ten seconds with
the *previous* directory still on screen.

**That ten seconds is history, not a current figure**, and this paragraph
said otherwise until 2026-08-14. gosmo's `enumFileSystemDMF` has since gained
a `WHERE level = 0` filter, without which
`sys.dm_os_enumerate_filesystem` walks the whole subtree under the path
rather than listing one directory. Re-measured live on win10cli through
`EnumFileSystemContext`, best of three: `C:\Windows\System32` 4551 entries in
**1.2s**, `C:\Windows` 101 in 35ms, `C:\Program Files` 29 in 11ms. `showBusy`
still earns its place — a second of frozen UI is worth labelling, and the
call is still synchronous — but do not size anything off the old number. It
was cited in a review as evidence that `serverFileSystemTimeout` needed
raising, which the real timings do not support.

It repaints only for a `dialogs.BlockingFileSystem`, which `serverFS`
implements and `LocalFileSystem` does not: on the local disk the extra frame
would only flicker. Do not "simplify" this into the normal draw cycle — there
is no frame between the keypress and the blocked call for the normal cycle to
run in. The real fix is an asynchronous `FileSystem`, deliberately not built:
it turns Tab completion and the save-overwrite check into callback chains for
a wait the indicator already explains.

### Starting the goroutine: safego

Start it with **`App.safego("what this was doing", fn)`**, never a bare
`go func()`. `safego` is the goroutine plus the `defer recoverPanic(what)`
that keeps a background panic from taking the process down; `what` names it
in the report. Writing the halves by hand works right up until one is
written without the `defer` — a panic nothing catches.

The one exception is the bounded worker pool, **`App.fanOut(n, what, work,
onPanic)`** (`safego.go`), which the Detail Browser's per-row backfill and the
Log File Viewer's per-file reads both run on. It spawns with a bare `go` and
takes the label and the recover by hand, recovering each item on its own so a
panic costs that item and not the rest of its worker's queue; `onPanic` runs
*before* `fanOut` returns, which is how the backfill gets `markFailed` queued
ahead of the caller caching its rows.

### When the goroutine latched UI state first: safegoRepair

`safego` alone reports the panic and stops there, which is not enough for the
common shape where the *caller* latched something on the UI goroutine before
the `go` — a busy flag, a `SetApplying(true)`, a `"Loading..."` placeholder, a
toolbar the flag dims. The release is a plain statement inside the goroutine
body, or lives in the callback it posts when it finishes, and a panic unwinds
straight past both. The latch then survives for the object's lifetime.

Use **`App.safegoRepair(what, repair, fn)`**: identical to `safego`, plus it
queues `repair` on the UI goroutine when — and only when — `fn` panics, before
`recoverPanic` reports it, so the panic stays the status bar's last word.
`repair` runs on the main goroutine already, so it calls the UI directly and
must not add a second `postAndWake` hop of its own (`App.markTaskDone` is
`postTaskDone`'s body split out for exactly that reason).

Two passes over this codebase (2026-08-13, 2026-08-14) converted sixteen
sites: query execution and the estimated plan, both `runPipeline`s and
`New …`'s page loader, the Activity Monitor's two collectors, the backup and
restore tasks, the AG dashboard, the update check, dependencies, and the log
viewer, completion inventory and property-page actions before them. The
symptoms were all the same shape — Execute disabled for the panel's lifetime,
a Properties dialog inert down to its Cancel button, a task the status bar
counts as running forever.

A goroutine that also owns a resource the same panic would leak — a
`context.CancelFunc`, a channel a ticker is selecting on — releases it with
`defer` *inside* `fn`, not from `repair`. `QueryPanel.startRun` is the worked
example: `defer cancel()` and `defer close(done)` on entry, because a panic
past `close(done)` leaves `tickExecuting` waking the event loop once a second
for the rest of the process's life.

Cover a new one the way `TestPageActionLatchClearsWhenTheActionPanics` does:
panic the action, then assert the *next* one still runs. A test that only
checks the flag flipped passes on a latch nothing can use again.

## Latest-only loads: latest

`internal/tui/latest.go` is the "start a load, cancel the one it replaces,
drop stale results" lifecycle, owned once. Nearly every asynchronous read in
the application is latest-only — the newest request is the only one whose
result anyone wants — and each of these owns a `latest` rather than its own
copy: an Object Explorer node's children (`object_explorer.go`), the
completion inventory's catalog, the Query Store panel's report, plan pane and
series, the Log File Viewer's read, a `newObjectDialog`'s prefetch, and the
Detail Browser's fetch.

`PropDialog`'s page loads are the one site that keeps the two halves apart,
because the sheet already owns one of them: `propsheet.PropertySheet` numbers
every page load with its own `seq` and drops a result that no longer matches
it, so a `latest` per page would carry a second counter shadowing it. What the
framework cannot own is the cancel — `tuikit` knows nothing about
`context`-scoped fetches, and must not learn — so `PropDialog` holds it in
`pageRuns`, a `context.CancelFunc` per page index, cancelled and re-armed by
`onLoadPage` and drained by `show`/`onClose`. `PropertySheet.Refresh` is the
other half of the same rule: it refuses to dispatch a second load for a page
that is still loading, since the host it would dispatch to was never told the
first one was superseded.

**Both halves matter, and a copy with only the first is a bug.** The token
discards a superseded result, so a slow fetch cannot overwrite the fresher one
that replaced it. The cancel stops the superseded fetch's queries, so they
release their pool connection now rather than at their timeout — without it,
holding Down through a folder starts one read per row and the row the user
stops on queues behind all of them. Three shipped bugs are the ones that
brought this here: Refresh left every replaced node's load running, the
Properties dialog let a previous showing's page loads reach the next one, and
the Detail Browser never cancelled a fetch it had moved past.

The surface, and what each member is for:

- **`Begin(parent)`** derives from `parent` — the owning connection's
  `Context()`, never `context.Background()`, so disconnecting cancels the run.
  **`BeginTimeout(parent, d)`** is the same with a deadline of its own, for a
  run that must not outlive its own timeout even while the connection stays up;
  it is what the node fetch (`childFetchTimeout`), the property pages
  (`propFetchTimeout`) and the completion inventory use.
- **`Done(token)`** is the completion path: it reports whether `token` is still
  current and, on true, releases the finished run's context. The cancel is
  called rather than dropped — the result is in hand, but the context stays
  registered on its parent, with its timer still armed for `BeginTimeout`, for
  every run ever started, until something cancels it.
- **`Cancel`** stops the run **without** superseding it: the token stays
  current, so a result already on its way still lands. That is what a panel's
  `Close` wants, and it is what `Begin` does before starting the replacement.
  **`Abandon`** is the one that supersedes — for a run whose result now has
  nowhere to go, a tree node leaving the tree or a selection that cleared
  without starting a new read. Reaching for `Cancel` where `Abandon` is meant
  is the easy mistake: the run stops, and its eventual `Done` still reports it
  current.
- **`seq` is never reset.** A per-showing counter that restarts at 0 lets the
  previous showing's first result pass the next showing's first guard — the
  R5 bug, and the reason `latest`'s zero value is usable but a `latest` is
  never re-zeroed to "clear" it. `Abandon` is how you clear one.

Every method runs on the UI goroutine, like all other widget state
(§ Threading model); `latest` does no locking.

**A site needing more bookkeeping wraps it rather than growing it.**
`DetailBrowser`'s `detailRuns` (`detail_browser.go`) embeds a `latest` and adds
the node each run is for plus the per-node `pending` map a cancel has to evict,
and its own `stop`/`supersede` shadow the embedded `Cancel`/`Abandon` so a
caller cannot stop a run and leave its pending entry behind. Adding those two
fields to `latest` itself would put Detail-Browser-shaped state in the eight
sites that do not want it.

## Building & testing

The toolchain commands and the automatic version resolution are in
`CLAUDE.md` ("Build & verify") — plain `go`, no Makefile, nothing
hand-edited before a release.

**`go test ./...` passing is not verification.** The test suite is worth
keeping green, but nearly every real bug in this project was caught by
driving the built binary against a real SQL Server, not by a test.
`docs/testing.md` is authoritative for how to do that — the tmux harness
for TUI behavior, disposable objects for database behavior, A/B against a
pre-fix binary for anything subtle.

## Developing against a local gosmo checkout

`gosmo` is a separate repository
([github.com/radix29/gosmo](https://github.com/radix29/gosmo)) that goSSMS
depends on as a tagged module, but the two are developed together, so
`go.mod` normally has

```
replace github.com/radix29/gosmo => ../gosmo
```

**active** — the intended state during development, not an oversight.
Builds resolve gosmo from the `../gosmo` sibling checkout, and `require` is
only a floor: `HEAD` of gossms routinely calls gosmo code that isn't tagged
yet, so a clone without the sibling checkout may not build.

- A gossms behavior that looks wrong may be coming from uncommitted or
  untagged gosmo code. Check `git -C ../gosmo status`/`log` before
  blaming the pinned release.
- Build and test inside `gosmo` itself before relying on a change from
  gossms — a gossms-side build only compiles the packages it imports.

Only at release time does the pair get commented back out: tag and push
gosmo, bump `go.mod`'s `require` to the new tag, comment out
`replace`, and confirm gossms builds and tests clean against the
tagged module before tagging gossms itself.

## Dependencies

All six direct requires in `go.mod`:

| Package | Purpose |
|---------|---------|
| [github.com/gdamore/tcell/v3](https://github.com/gdamore/tcell) | Terminal UI rendering, keyboard & mouse events |
| [github.com/radix29/gosmo](https://github.com/radix29/gosmo) | SQL Server management objects (databases, tables, scripts…) |
| [github.com/microsoft/go-mssqldb](https://github.com/microsoft/go-mssqldb) | The SQL Server driver itself, plus `batch` for `GO` splitting — `internal/query` |
| [github.com/golang-sql/sqlexp](https://github.com/golang-sql/sqlexp) | Interleaved result-set/message stream, so PRINT and errors arrive in order — `internal/query` |
| [github.com/clipperhouse/displaywidth](https://github.com/clipperhouse/displaywidth) | Terminal column width behind `core.DisplayWidth` — one of only two external modules `tuikit` imports |
| [github.com/pkg/browser](https://github.com/pkg/browser) | Opens the Microsoft Entra sign-in page in the user's browser. Its `Stdout`/`Stderr` are redirected to `io.Discard` in `installEntraSignIn` — anything it writes would land on the terminal tcell is drawing on |
