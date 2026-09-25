# Architecture

goSSMS is an application-agnostic TUI library (`internal/tuikit`, see
[`internal/tuikit/README.md`](internal/tuikit/README.md)) plus a thin
application layer (`internal/tui`) that wires it to SQL Server via `gosmo`.

## Why split this way

`tuikit` holds all rendering, focus, scrolling and drag/resize logic exactly
once, over generic `Rect`s, `TreeNode`s with an `any` `Tag`, and string rows —
it knows nothing of databases. `tui` never re-implements widget mechanics; it
supplies data and callbacks (`OnExpand`, `OnSelect`, button `Action`s).

**Invariant: `internal/tuikit` must not import `internal/tui` or `gosmo`.** Its
only external dependencies are tcell and displaywidth:

```bash
go list -f '{{range .Imports}}{{.}}{{"\n"}}{{end}}' ./internal/tuikit/... |
  grep '\.' | grep -v gossms | sort -u    # non-stdlib, non-repo imports
```

must print exactly `github.com/gdamore/tcell/v3`,
`github.com/gdamore/tcell/v3/color`, `github.com/clipperhouse/displaywidth`.

## Which document owns what

A rule stated twice drifts. State it once, in its owner; elsewhere summarize in
a sentence and link.

| Document | Authoritative for |
|---|---|
| `CLAUDE.md` | Working rules for every task, and where to read next. Short — loads every session |
| `docs/ui-rules.md` | The enforceable form of every `internal/tui`/`tuikit` idiom: widgets, grids, dialogs, clipboard, toolbars, mouse, async |
| `docs/db-rules.md` | Permission gating, emitted T-SQL, Object Explorer filters, query execution |
| `docs/testing.md` | What counts as verification: tmux and live-server harnesses, `fakedb_test.go` rules |
| `ARCHITECTURE.md` | Package map, layering, data flow, threading, and the long-form *why* behind each idiom |
| `internal/tuikit/README.md` | Everything inside `internal/tuikit` |
| `docs/open-threads.md` | Work knowingly left undone: bugs, deferred scope, release blockers |
| `docs/decisions.md` | Settled decisions and exclusions — the "do not re-raise" record |

`README.md` is user-facing and owns features; the F1 help dialog
(`internal/tui/help_dialog.go`) is the keyboard reference.

## How a query runs

1. **`internal/db`** (`connection.go`) owns connection *lifetime*.
   `ConnectContext(ctx, opts, role)` maps `config.Connection` onto
   `gosmo.ConnectionOptions` in `toGosmoOptions` — the Connect dialog's masked
   preview (`BuildConnectionString`) goes through it too, so the preview is the
   DSN dialled — and returns a `ServerConn` wrapping a `gosmo.Server`. `Role`
   sets `program_name` (`goSSMS`, `goSSMS - Query`, `goSSMS - Activity
   Monitor`). `ServerConn.Context()`, cancelled by `Close()`, is the parent of
   every background load on that connection: closing the `*sql.DB` does not
   cancel an in-flight query, so a load rooted at `context.Background()` keeps
   a server session alive after disconnect.
2. **`internal/query`** (`executor.go`, `session.go`) owns *execution*. A query
   window runs on a **`Session`**: one `*sql.Conn` from `gosmo.AcquireConn` (up
   to 3 attempts on transient liveness failures, linear backoff) held for the
   panel's lifetime, so temp tables, SET options, `USE` and open transactions
   persist between runs, as in SSMS. Package-level `Execute`/`ExecuteWithPlan`
   check out a pooled connection per call — which database/sql resets on next
   checkout — so they suit one-shot callers only (Activity Monitor's procedure
   tab). Both share `runScript`: optional `SET STATISTICS XML`/`SHOWPLAN_XML`,
   `GO`-splitting via `sqltext.SplitBatches`
   (`internal/tuikit/sqltext/split.go`), `runBatch` per batch, all into one
   `Result` (result sets, messages, plan XML; for a Session also `DB_NAME()`,
   `@@TRANCOUNT` — read even after a cancel — and whether the session was
   lost). `Session.Close` *discards* the connection: pooled, an open
   transaction would sit idle holding locks.
3. **The message stream** (`sqlexp`): result sets and messages interleave on
   one connection and `runBatch` walks them together. A speculative extra
   `rows.Next()` consumes the return message and the grid comes up empty —
   verify against a live server.
4. **`internal/showplan`** only *parses* (no TUI, no DB — testable from a
   file): `ParseAll` builds one navigable `Plan`, rendered by
   `internal/tui/planview` as Plan/Tree/XML tabs.

`QueryPanel.launch` (`query_panel_exec.go`) is the Session's only caller and
the one run-start path (Execute, Results To File, estimated plan): executor on
a background goroutine, `Result` back via `postAndWake`. IntelliSense and
catalog reads use the `ServerConn` pool, never the session, so they never
queue behind a query. A lost session closes the panel's connection (Query >
Reconnect opens a new one); closing, reconnecting or quitting with
`@@TRANCOUNT > 0` asks to commit first (`confirmOpenTransactions`).

## Threading model

**All UI and widget state belongs to the UI goroutine** (the one in
`App.Run()`). `tuikit` does no locking, so touching a widget off it is a data
race. Background work has one shape:

- **Context**: derive from `ServerConn.Context()`, never
  `context.Background()` — including a dial that clones an existing connection
  (`connectForQueryPanel`, `connectForActivityMonitor`, `amProcTab.activate`),
  so disconnect cancels a reconnect in flight. The Connect dialog's first dial
  is the one exception (no `ServerConn` yet; `connect_dialog.go` says so). For
  cancellable user-visible work use `App.startTask(parent, label)`: a `*Task`
  plus derived context, listed in Background Tasks with a Cancel button.
- **Work** off-thread, touching nothing the UI owns.
- **Report** with **`App.postAndWake(fn)`** — `fn` runs on the UI goroutine.
  `postProgress`/`postTaskDone` wrap it.
- **Start** with **`App.safego(what, fn)`** (or `defer a.recoverPanic(what)`).
  An unrecovered background panic kills the process before `screen.Fini()`
  restores the terminal — trace and unsaved query text lost. go-mssqldb
  panics outright on an unknown column type ID, so this is not theoretical.
- **Supersede** with **`latest`** (`latest.go`) when a newer request replaces
  this one — § Latest-only loads: latest.

A panic on the UI goroutine itself is not recovered in place: `cmd/gossms/main.go`'s
`run` catches it after `Fini`, logs it, and calls **`App.EmergencySave`**
(`emergency_save.go`), writing every dirty query panel to
`<config dir>/recovered/<time>-<title>.sql` and printing the paths. Resuming
the loop was rejected — half-mutated state is worse than a clean exit with the
text saved. SIGHUP/SIGTERM take the same save via **`App.SaveOnSignal`**
(relayed by `cmd/gossms`'s `watchTermSignals`) as a `postAndWake` callback that
then quits; if the loop doesn't answer within 2 s the signal goroutine saves
anyway (a racy read beats losing text). Logged, never printed — after SIGHUP
there is no terminal.

`Run()`'s loop: clear `wakePending`, drain queued callbacks, sync the dialog
stack, handle one event, re-sync, draw.

## Package map

`internal/tui` is a flat package, so every file is listed. `internal/tuikit`,
`planview`, `sqlparse`, `dashboard` and `gate` are summarized by directory —
see their README/`doc.go`.

`planview`, `sqlparse` and `dashboard` are leaves (only `tuikit` + stdlib,
never `tui`); `dashboard` also lets `cmd/amdemo` draw the panel's dashboards
without the whole app. `gate` is not a leaf — it imports `internal/db` and
`gosmo` (it answers "what has this connection been probed to allow") and
`tuikit/controls` (`gate.Item` wraps a `controls.MenuItem`) — but never `tui`
or `App`. See § Why the permission gate is its own package.

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
│   ├── query/               # SSMS-style script executor: GO batches (split by tuikit/sqltext, the editor's own rule), result sets, message stream, plan capture
│   │                        #   arena.go: chunk-packed cell storage for a retained result set; coltype.go: SSMS-style declared type names
│   ├── showplan/            # parses ShowPlanXML (estimated/actual) into a navigable operator tree; compare.go pairs two plans of one query (Compare Showplan). No TUI/DB deps
│   ├── fileutil/            # WriteAtomic: temp file + Sync + rename + syncDir, behind config.json, gossms.key, saved .sql scripts and the log export; WithLock: the lock file around config.json's and tracked_queries.json's read-merge-write
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
│   │   ├── sqltext/                 # T-SQL text rules shared with internal/query and sqlparse: the "GO" separator line rule, SplitBatches; standard library only
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
│       ├── emergency_save.go     # App.EmergencySave / SaveOnSignal: after a UI-goroutine panic or on SIGHUP/SIGTERM, writes every dirty query panel to <config dir>/recovered/
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
│       ├── name_set.go           # nameSet: the New-object dialogs' "already exists" check, folding case only when the scope's collation (server, database or msdb) does
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
│       ├── new_user_dialog.go    # New User: every CREATE USER form (for login, with password, without login, Windows, certificate / asymmetric key, Entra on Azure and 2022+), plus Owned Schemas and Membership
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

`internal/tui/gate` is the one package carved out of the flat `tui`, as a pilot
for the 2026-09-17 question of whether `tui`'s size (228 files, 62 374 LOC,
47 % never touching `*App`) costs anything. Only `permission_gate.go` (1 127
LOC) and its names test moved; the other gate tests drive
`App.objectOpsMenuItems`/`explorerNode`/`propPage` and stayed, as did
`withRequires`/`withRequiresOn` (`prop_page_gate.go`, they take a `propPage`).

`touch internal/tui/app.go` → `go test -c ./internal/tui/`, eight interleaved
pairs: median 3.9 s before, 3.4 s after, ranges 3.08–4.56 s vs 2.56–4.66 s.
Spread exceeds the difference: **splitting `internal/tui` does not buy compile
time**, so the flat package stands. Don't re-open it on build speed without a
new measurement. The split stayed for the compiler-enforced boundary: `gate`
cannot reach `App`, so the fail-open rule cannot acquire a dependency on app
state.

## Common tasks

### Adding a new dialog

Give `App` a typed field, construct it in `App.buildUI`, append it to
`a.allDialogs`. `syncDialogStack` (`dialog_stack.go`) pushes it on top when
its `Show()` flips it visible and routes it all input until it closes; draw
order and key/mouse routing follow. For the widget, follow the `ModalDialog`
skeleton in `internal/tuikit/README.md`.

### Adding a Properties page

A `PropDialog` is a `[]propPage` (`prop_dialog.go`): a title plus a `load` that
builds rows and closes over pointers to them so Apply can diff. Pages load
lazily. Add the builder beside the object's `*_props*.go` files and register it
in its page slice (`server_props.go` is the clearest example). A page that
renames its object sets `propPage.renames` so its apply runs last, and threads
the name as a `*string` shared across pages, or later pages use the stale one.

### Adding an Object Explorer node type

Add the `NodeType` and icon/name in `tree_node.go`; register a `childLoader` in
`explorer_loaders.go`'s `childLoaders` (it gets a `loaderCtx` and the node, runs
off the UI goroutine) and put the loader with its peers (`explorer_*.go`). Its
context menu is a `menuBuilder` in the same file's `nodeMenus`, written beside
the loader; no entry gives New Query + Refresh, and a Properties-only leaf uses
`propertiesOnlyMenu`. Script as, Rename/Delete and Filter come from their own
tables via `contextMenuItemsForNode`.

### Adding a menu or toolbar item

`menu.go`/`toolbar.go` items gate on `Enabled: func() bool { return
a.selectedServerConn() != nil }`-style predicates. An item that can be invoked
when its action is impossible must be gated, never a silent no-op.

## The mouseDragging idiom

tcell's all-motion tracking resends the held button on every motion event, so
any widget acting on `Button1` (toolbar button, menu label, tree toggle) needs a
per-widget latch — `mouseDragging` — set on the press and cleared on the
matching `ButtonNone`, or the action refires on every motion while held. It
guards only a resend over the widget that armed it.

A positional router (`App.handleMouse`) sees a drag that began elsewhere drift
onto a latch-owning widget as a fresh-looking `Button1`. So routers keep a
gesture-wide flag (`App.mouseButtonDown`, set/cleared from the raw event before
positional branching) to tell fresh from continuation — and, since that doesn't
say where the continuation goes, **a press claims the gesture**: the router
records the claiming region and replays to it until release. Three routers,
one shape — `App.gestureOwner` (`app_events.go`), `QueryPanel.dragZone`
(`query_panel.go`), `propsheet.PropertySheet.dragZone` (`sheet_input.go`) —
each `armGesture`/`armDrag` at every claiming branch plus `routeGesture`/
`routeDrag`. Regions that already acted swallow repeats. `Splitter` starts a
resize only from a press on its bar, so a selection drag crossing it doesn't
grab it.

`App` also snapshots the modal layer per gesture
(`gestureOverlay`/`overlaySnapshot`) and drops held `Button1` across a change:
otherwise clicking a context-menu item that opens a dialog and twitching before
release fires whatever dialog button is under the pointer.

The mirror image is a latch outliving its gesture: a dialog button closes the
dialog on the *press*, so the release never reaches its reset (`HandleMouse`
returns on `!visible`, the dialog is already popped) and the next showing
refused its first click — looked frozen. `ModalDialog.Show()` clears both
latches.

Consequences: an overlay drawn last (tuikit README, "overlays drawn last") gets
first refusal of every event while open; and a host with an early `return` in
`HandleMouse` must forward `ButtonNone` to latch-bearing children first, or a
drag ending outside leaves a child latched and swallowing its next press.

### dialogs.FieldGesture

A click in a `widgets.InputField` starts a selection drag, and the dialog must
end it on release *wherever* the pointer lands, replay motion to the field
without hit-testing, and drop the latch on `Show`. Each call's placement is
non-local:

- `Release` at the very top of `HandleMouse`, **before** `ConsumeOutsideClick`
  and any mode early-return — both return without looking at the latch, and a
  release outside the dialog or after it switched to a progress view is exactly
  what strands it.
- `Replay` after `ConsumeOutsideClick`, before any hit-testing — hit-testing
  motion ends the selection when the pointer leaves the field, and lets a drag
  over the button row fire a button.
- `Clear` in `Show`, also dropping the field's own `mouseDragging`: Connect,
  Options, Find/Replace and Log Search reuse their fields, so a dialog dismissed
  mid-drag handed back a latched field whose next press armed no anchor.

Seven dialogs hand-rolled this; `FieldGesture` holds it once.
`TestFileDialogDragOutOfPathFieldKeepsExtending` and the five tests in
`internal/tui/dialog_drag_test.go` fail on a mutation of any of the three.

## Async result delivery: postAndWake

A background goroutine reports with `App.postAndWake(fn)` — never `postEvent`
then `wakeEventLoop` by hand. The wakeup must be sent *outside* the posted
closure, from the background goroutine: `Run()` drains callbacks only when it
wakes for an `EventQ()` event, so a wakeup nested in the closure waiting to be
drained never fires, and the result sits until an unrelated keypress. Shipped
bug: Object Explorer nodes stuck on "Loading...", in every async path at the
time.

A bare `wakeEventLoop()` is legitimate only for a clock-driven redraw with no
callback. `App.animateUntil` (`app.go`) is that loop, written once: the
`QueryPanel` elapsed-time ticker, create/properties spinners, the progress
dialog and `ConnectDialog`'s connect spinner.

### The other direction: FileDialog.showBusy

`dialogs.FileDialog` is the one place painting outside the draw cycle:
`dialogs.FileSystem` is synchronous, so a remote one (`internal/tui/serverFS`)
blocks the event handler for a round trip. `showBusy` draws a "Listing ..."
line and calls `Screen.Show()` before the call so the wait doesn't look like a
hang. It repaints only for a `dialogs.BlockingFileSystem` (`serverFS`, not
`LocalFileSystem` — locally it would just flicker). Don't fold it into the
normal draw cycle: there is no frame between keypress and blocked call. An
async `FileSystem` was deliberately not built — it turns Tab completion and
the overwrite check into callback chains for a wait the indicator explains.

Timings: the old "ten seconds for `C:\Windows\System32`" is history, fixed by
gosmo's `enumFileSystemDMF` `WHERE level = 0` filter (without it
`sys.dm_os_enumerate_filesystem` walks the subtree). Measured 2026-08-14 on
win10cli: System32 4551 entries 1.2 s, `C:\Windows` 35 ms, `C:\Program Files`
11 ms. Don't size `serverFileSystemTimeout` off the old number.

### Starting the goroutine: safego

Start with **`App.safego("what", fn)`**, never a bare `go func()` — it is the
goroutine plus `defer recoverPanic(what)`, and hand-written halves eventually
miss the `defer`.

The one exception is the bounded pool **`App.fanOut(n, what, work, onPanic)`**
(`safego.go`; Detail Browser backfill, Log File Viewer per-file reads). It
recovers each item separately, so a panic costs that item, not its worker's
queue; `onPanic` runs *before* `fanOut` returns, which is how backfill queues
`markFailed` ahead of the caller caching its rows.

### When the goroutine latched UI state first: safegoRepair

When the *caller* latched UI state before the `go` — a busy flag,
`SetApplying(true)`, a `"Loading..."` placeholder — the release lives in the
goroutine body or its posted callback, and a panic skips both: the latch lives
forever (Execute disabled for the panel's lifetime, a Properties dialog inert
to its Cancel button, a task counted as running forever). Use
**`App.safegoRepair(what, repair, fn)`**: `safego` plus `repair` queued on the
UI goroutine only when `fn` panics, before the panic is reported (so the panic
is the status bar's last word). `repair` already runs on the UI goroutine —
call the UI directly, no second `postAndWake` (`App.markTaskDone` is
`postTaskDone`'s body split out for this).

A resource the same panic would leak — a `CancelFunc`, a channel a ticker
selects on — is released by `defer` *inside* `fn`, not by `repair`.
`QueryPanel.startRun` is the example: `defer cancel()` and `defer close(done)`
on entry, else `tickExecuting` wakes the loop every second forever.

Test like `TestPageActionLatchClearsWhenTheActionPanics`: panic the action,
then assert the *next* one still runs — a flag check passes on a dead latch.

## Latest-only loads: latest

`internal/tui/latest.go` owns "start a load, cancel the one it replaces, drop
stale results". Almost every async read is latest-only, and each of these owns
a `latest`: an Object Explorer node's children (`object_explorer.go`), the
completion inventory, the Query Store panel's report/plan pane/series, the Log
File Viewer's read, a `newObjectDialog` prefetch, the Detail Browser fetch, and
Results to Text formatting of a large set (`QueryPanel.showResultsText` — CPU,
not a read, but superseded the same way).

**Both halves matter.** The token drops a superseded result, so a slow fetch
can't overwrite a fresher one; the cancel stops its queries so they release
their connection now, not at timeout — else holding Down through a folder
queues the final row behind one read per row passed. Three shipped bugs
brought this here: Refresh left replaced nodes' loads running, a Properties
dialog's page loads reached its next showing, and the Detail Browser never
cancelled fetches it moved past.

- **`Begin(parent)`** derives from `parent` — the connection's `Context()`,
  never `context.Background()`. **`BeginTimeout(parent, d)`** adds its own
  deadline (node fetch `childFetchTimeout`, property pages `propFetchTimeout`,
  completion inventory).
- **`Done(token)`** reports whether `token` is current and, if so, releases the
  run's context — cancelled, not dropped, or it stays registered on its parent
  (timer armed, for `BeginTimeout`) for every run ever started.
- **`Cancel`** stops the run **without** superseding it — a result already in
  flight still lands (a panel's `Close`; `Begin` does it before starting the
  replacement). **`Abandon`** supersedes too — for a result with nowhere to go
  (node left the tree, selection cleared). Using `Cancel` for `Abandon` is the
  easy mistake: the run stops but its `Done` still reports current.
- **`seq` is never reset.** A per-showing counter restarting at 0 lets the
  previous showing's first result pass the next showing's guard (the R5 bug).
  The zero value is usable; clear with `Abandon`, never re-zero.

All methods run on the UI goroutine; no locking.

`PropDialog` is the one site keeping the halves apart: `propsheet.PropertySheet`
already numbers page loads with its own `seq`, so a `latest` per page would
shadow it. The cancel, which `tuikit` must not learn about, is `PropDialog`'s
`pageRuns` (a `CancelFunc` per page index; re-armed by `onLoadPage`, drained by
`show`/`onClose`). `PropertySheet.Refresh` refuses to re-dispatch a page still
loading, since the host was never told the first load was superseded.

**A site needing more bookkeeping wraps `latest`.** `detailRuns`
(`detail_browser.go`) embeds one and adds the run's node and the per-node
`pending` map; its `stop`/`supersede` shadow `Cancel`/`Abandon` so a caller
can't stop a run and leave its pending entry behind.

## Developing against a local gosmo checkout

The `dev-with-local-gosmo` skill and `CLAUDE.md` own this. In short: `go.mod`'s
`replace github.com/radix29/gosmo => ../gosmo` is **active** in development and
`require` is only a floor, so a clone without the sibling may not build, and
odd behaviour may come from uncommitted or untagged gosmo code (`git -C ../gosmo
status`/`log`). At release: tag and push gosmo, bump `require`, comment out
`replace`, build and test clean, then tag gossms.

## Dependencies

All six direct requires in `go.mod`:

| Package | Purpose |
|---------|---------|
| [github.com/gdamore/tcell/v3](https://github.com/gdamore/tcell) | Terminal rendering, keyboard & mouse events |
| [github.com/radix29/gosmo](https://github.com/radix29/gosmo) | SQL Server management objects |
| [github.com/microsoft/go-mssqldb](https://github.com/microsoft/go-mssqldb) | The SQL Server driver, plus `batch` for `GO` splitting — `internal/query` |
| [github.com/golang-sql/sqlexp](https://github.com/golang-sql/sqlexp) | Interleaved result-set/message stream, so PRINT and errors arrive in order — `internal/query` |
| [github.com/clipperhouse/displaywidth](https://github.com/clipperhouse/displaywidth) | Terminal column width behind `core.DisplayWidth` — one of `tuikit`'s two external modules |
| [github.com/pkg/browser](https://github.com/pkg/browser) | Opens the Entra sign-in page. `installEntraSignIn` sends its `Stdout`/`Stderr` to `io.Discard` — output would land on tcell's terminal |
