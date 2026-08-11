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
| `CLAUDE.md` | Agent-facing working rules: verification standards, coding conventions, the short enforceable form of each idiom |
| `ARCHITECTURE.md` | This file: package map, layering, data flow, threading, and the long-form *why* behind each idiom |
| `internal/tuikit/README.md` | Everything inside `internal/tuikit` — its package map, dependency direction, widget design rules |
| `PLAN.md` | What's next: release target, current priorities, feature backlog |
| `docs/open-threads.md` | Work knowingly left undone: unfixed bugs, deferred scope, release blockers |
| `docs/journal.md` | Dated archive of what was built and how bugs were found. Never required reading |

`README.md` is user-facing and owns features and the keyboard reference. When
a rule needs to appear in two places, the second one summarizes in a sentence
and links here — it does not restate the reasoning.

## How a query runs

The path from keystroke to result grid, which touches four packages:

1. **`internal/db`** (`connection.go`) owns a connection's *lifetime*.
   `Connect` builds the DSN from a `config.Connection` and returns a
   `ServerConn` wrapping a `gosmo.Server`. Its `ctx`, exposed by
   `Context()` and cancelled by `Close()`, is the parent every background
   load scoped to that connection must derive from — closing the underlying
   `*sql.DB` alone does not cancel a query already in flight, so a load
   rooted at `context.Background()` keeps a real SQL Server session alive
   after disconnect.
2. **`internal/query`** (`executor.go`) owns *execution*. `Execute` /
   `ExecuteWithPlan` / `ExecuteEstimatedPlan` take a `*sql.DB`, check out a
   single `*sql.Conn` via `acquireConn` (which retries a transient liveness
   failure 3 times with linear backoff, mirroring gosmo's own `retry.go`),
   optionally wrap the run in `SET STATISTICS XML ON` / `SET SHOWPLAN_XML
   ON`, then split the script on `GO` with `go-mssqldb/batch` and run each
   batch through `runBatch`. One `Result` accumulates every result set,
   every message, and the captured plan XML across all batches.
3. **The message stream** is where `sqlexp` matters: result sets and
   informational messages interleave on one connection, and `runBatch`
   walks them together. A speculative extra `rows.Next()` here consumes the
   return message and the grid comes up empty — verify against a live
   server, not just a unit test.
4. **`internal/showplan`** owns *parsing*, and nothing else — no TUI and no
   database imports, so it is testable from a file. `ParseAll` turns the
   captured ShowPlanXML documents into one navigable `Plan` of operator
   nodes, which `internal/tui/planview` renders as the Plan/Tree/XML tabs.

`query_panel_exec.go` is the only caller: it runs the executor on a
background goroutine and reports the `Result` back with `postAndWake`.

## Threading model

**All UI and widget state belongs to the UI goroutine** — the one running
`App.Run()`. `tuikit` does no locking anywhere (see
`internal/tuikit/README.md`), so touching a widget from any other goroutine
is a data race, not merely bad style.

Background work follows one shape:

- Derive a context from `ServerConn.Context()`, never `context.Background()`
  — see the lifetime rule above. For cancellable, user-visible work use
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

`Run()`'s loop clears `wakePending`, drains queued callbacks, syncs the
dialog stack, handles one event, then re-syncs and draws. The two idioms
below follow from that: `postAndWake` is how work crosses back onto the UI
goroutine, and the `mouseDragging`/gesture rules govern how one input event
is routed once it is already there.

## Package map

`internal/tui` is a flat package, so every file is listed individually with
its purpose; `internal/tuikit`, `internal/tui/planview`,
`internal/tui/sqlparse` and `internal/tui/dashboard` are summarized by
directory and documented in their own README and `doc.go`. A file absent from a summarized directory has not
been omitted — look there directly.

All three `tui` sub-packages are leaves: they depend on `tuikit` and the
standard library, never on `tui` itself. That is what makes each
extractable. `dashboard` is the newest, and it exists for a second reason:
`cmd/amdemo` has to draw the same dashboards the panel draws without
dragging in the whole application.

```
gossms/
├── cmd/
│   ├── gossms/               # main entry point
│   ├── plandemo/             # dev harness: hosts planview.PlanView full-screen against a plan file (not part of the release build)
│   └── amdemo/               # dev harness: hosts the Activity Monitor dashboards full-screen against deterministic mock data (not part of the release build)
├── internal/
│   ├── config/              # connection profiles (JSON, in $XDG_CONFIG_HOME/gossms/)
│   ├── db/                  # gosmo connection wrapper + DSN builder
│   │                        #   peer.go: cached same-credential connections to other instances (Always On: read the group from its primary)
│   ├── activity/            # Activity Monitor collection: DMV queries, cntr_type decode, wait categories, 30-minute store, collector goroutines — no TUI imports
│   │                        #   proc.go: helper-procedure lookup/install shared by the Block and Sessions tabs; block.go: sp_block; whoisactive.go + whoisactive.sql: the embedded GPL-3.0 sp_WhoIsActive
│   │                        #   tempdb.go + tempdb_collector.go: tempdb space/file/session usage on its own slower cadence
│   ├── query/               # SSMS-style script executor: GO batches, result sets, message stream, plan capture
│   │                        #   arena.go: chunk-packed cell storage for a retained result set; coltype.go: SSMS-style declared type names
│   ├── showplan/            # parses ShowPlanXML (estimated/actual) into a navigable operator tree; no TUI/DB deps
│   ├── version/             # gossms's own version metadata (mirrors gosmo/version); overridable via -ldflags -X
│   │
│   ├── tuikit/               # embeddable TUI library (no SQL Server / app knowledge) — see internal/tuikit/README.md
│   │   ├── theme/                # colour palette + derived tcell.Style helpers
│   │   ├── core/                 # Rect geometry, drawing primitives, string/int helpers
│   │   ├── widgets/               # InputField, DropDown, CheckBox, Button, RadioBox
│   │   ├── layout/                # Panel interface, PanelManager (tabs), Splitter
│   │   ├── dialogs/                # ModalDialog base (focus trap), Properties/Alert/Confirm/FileDialog
│   │   ├── charts/                 # terminal charts from generic series data: off-screen canvas, scales, block glyphs, axis/legend, history/stacked/bar/KPI types
│   │   ├── controls/                # MenuBar, ContextMenu, Toolbar, TabStrip, TreeView, DataGrid, ListBox, Editor (+SQL/XML highlighters)
│   │   └── propsheet/               # PropertySheet — multi-page editable properties dialog framework
│   │
│   └── tui/                  # goSSMS application layer (built on tuikit)
│       ├── dashboard/            # Activity Monitor dashboard layout: draws a HistoryView/SampleView with tuikit/charts; no App, no connection
│       ├── planview/             # reusable control rendering a parsed plan: Plan (graph)/Tree/XML tabs
│       ├── sqlparse/             # T-SQL lexer + statement-scope scanner behind IntelliSense (pure functions over runes; no App, no connection)
│       │
│       │  ── App core ──
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
│       ├── explorer_alwayson.go # loaders: Always On High Availability — Availability Groups, Replicas, Databases, Listeners; follows the primary via db.ServerConn.Peer
│       ├── explorer_drag.go      # drag a tree node into a query editor as a quoted T-SQL identifier
│       ├── tasks.go              # background task registry: Task (progress/cancel), App start/postProgress/postTaskDone
│       ├── safego.go             # App.safego/recoverPanic — every background goroutine in this package runs under one
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
│       │
│       │  ── Activity Monitor ──
│       ├── activity_monitor.go        # ActivityMonitor state: tabs, toolbar, per-tab scroll, teardown; implements layout.Panel
│       ├── activity_monitor_draw.go   # tab/toolbar rows, dashboard canvas blit, both scrollbars
│       ├── activity_monitor_input.go  # HandleKey/HandleMouse: tab switching, scrolling, gesture zones, scrollbar drags
│       ├── activity_monitor_history.go # activity.Store → HistoryView: one charts.Series per metric, colour roles
│       ├── activity_monitor_sample.go  # activity.Store.Latest() → SampleView: bars, KPIs, memory composition
│       ├── activity_monitor_tempdb.go # TempDB tab: space stack, per-file bars, top-session usage grid, configuration advisory
│       ├── activity_monitor_tooltip.go # click-pinned chart readout: hit-test against the canvas, frame + text drawn over the viewport
│       ├── activity_monitor_proctab.go # Block and Sessions tabs: own connection, procedure lookup/install, Refresh + Install in master, result grid
│       │
│       │  ── Detail Browser ──
│       ├── detail_browser.go            # Detail Browser, implements layout.Panel
│       ├── detail_browser_backfill.go   # bounded per-row backfill fan-out shared by the folder loaders below
│       ├── detail_browser_server.go     # Server node: version/edition/paths/CPU/memory, then NUMA + disk volumes
│       ├── detail_browser_databases.go  # Databases folder: name/state/recovery, then per-database size backfill
│       ├── detail_browser_logins.go     # Logins folder
│       ├── detail_browser_tables.go     # Tables folder: name, then per-table row count/space backfill
│       │
│       │  ── SQL Server Agent ──
│       ├── agent_common.go              # shared Job/Alert/Notify enum formatters, refreshExplorerNode, generic async enable/disable/delete plumbing for every Agent entity
│       ├── agent_menu.go                # Agent node context menus (Start/Stop/Enable/Disable/Delete/View History) + New Job/Schedule/Alert/Operator entry points
│       ├── agent_explorer.go            # loads the Agent subtree: Jobs (User/System split)/Schedules/Alerts/Operators/administration reports folder
│       ├── agent_detail.go              # Object Explorer Details grids for every Agent node type (server/job/schedule/alert/operator/activity/history/categories)
│       ├── agent_reports.go             # the "SQL-only administration" folder's canned reports, plus the View History query behind a job's History action
│       ├── agent_job_props.go           # Job Properties dialog: page-set wiring + General/Targets page definitions
│       ├── agent_job_props_steps.go     # Job Properties Steps page: T-SQL step grid/inline editor, Start at Step, ordered update/delete/add apply
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
│       ├── connect_dialog.go     # Connect dialog — form + saved-connection autocomplete + conn-string preview
│       ├── find_replace_dialog.go # Edit > Find/Replace — one dialog in two modes, over controls.Editor's search engine
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
│       ├── new_object_dialog.go  # newObjectDialog — the shell behind all six New <object> dialogs (one prefetch, all pages built at once, ordered create pipeline, Script Changes)
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
│       ├── login_props.go        # Login Properties page definitions
│       ├── table_props.go        # Table Properties page definitions
│       ├── schema_props.go       # Schema Properties page definitions
│       ├── role_props.go         # Database Role Properties page definitions
│       ├── user_props.go         # Database User Properties page definitions
│       ├── server_role_props.go  # Server Role Properties: General/Members/Owned Roles/Securables
│       ├── statistics_props.go   # Statistics Properties: General/Columns/Filter/Details/Histogram/Density Vector/Extended Properties
│       ├── index_props.go        # Index Properties: General/Options/Storage/Included Columns/Filter/Fragmentation/Extended Properties
│       ├── key_props.go          # Primary/Unique Key Properties, reusing most of Index Properties' pages
│       ├── fk_props.go           # Foreign Key Properties: single read-only General page
│       │
│       │  ── New <object> dialogs ──
│       ├── new_database_dialog.go # New Database — newObjectDialog config, runs CREATE DATABASE
│       ├── new_database_pages.go  # New Database's page definitions
│       ├── new_login_dialog.go    # New Login — newObjectDialog config, runs CREATE LOGIN
│       ├── new_login_pages.go     # New Login's page definitions
│       │
│       │  ── Backup & Restore ──
│       ├── backup_common.go      # helpers shared by the Backup and Restore dialogs
│       ├── backup_dialog.go      # Back Up Database dialog — options form + in-place progress
│       ├── backup_dialog_draw.go # Back Up Database rendering
│       ├── backup_dialog_input.go # Back Up Database HandleKey/HandleMouse
│       ├── restore_dialog.go     # Restore Database dialog — options form, backup-set inspection
│       ├── restore_dialog_draw.go  # Restore Database rendering
│       ├── restore_dialog_input.go # Restore Database HandleKey/HandleMouse
│       └── restore_dialog_ops.go # Restore Database's background-task execution + history/file-list lookups
```

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
the clearest example, and the 35 `*_props*.go` files all follow it. A page
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

`QueryPanel`'s elapsed-timer tick is the one legitimate bare
`wakeEventLoop()` caller: no callback to post, only a redraw to ask for.

### Starting the goroutine: safego

Start it with **`App.safego("what this was doing", fn)`**, never a bare
`go func()`. `safego` is the goroutine plus the `defer recoverPanic(what)`
that keeps a background panic from taking the process down; `what` names it
in the report. Writing the halves by hand works right up until one is
written without the `defer` — a panic nothing catches.

The one exception is `DetailBrowser.backfillRows`
(`detail_browser_backfill.go`), which carries its own `recover` so it can
queue `markFailed` *before* `wg.Done` releases the caller. It documents that
on the spot.

## Building & testing

The toolchain commands and the automatic version resolution are in
`CLAUDE.md` ("Build & verify") — plain `go`, no Makefile, nothing
hand-edited before a release.

**`go test ./...` passing is not verification.** The test suite is worth
keeping green, but nearly every real bug in this project was caught by
driving the built binary against a real SQL Server, not by a test. CLAUDE.md's
"Green tests are not verification" section is authoritative for how to do
that — the tmux harness for TUI behavior, disposable objects for database
behavior, A/B against a pre-fix binary for anything subtle.

## Developing against a local gosmo checkout

`gosmo` is a separate repository
([github.com/radix29/gosmo](https://github.com/radix29/gosmo)) that goSSMS
depends on as a tagged module, but the two are developed together, so
`go.mod` normally has the pair

```
ignore ../gosmo
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
`replace`/`ignore`, and confirm gossms builds and tests clean against the
tagged module before tagging gossms itself.

## Dependencies

All five direct requires in `go.mod`:

| Package | Purpose |
|---------|---------|
| [github.com/gdamore/tcell/v3](https://github.com/gdamore/tcell) | Terminal UI rendering, keyboard & mouse events |
| [github.com/radix29/gosmo](https://github.com/radix29/gosmo) | SQL Server management objects (databases, tables, scripts…) |
| [github.com/microsoft/go-mssqldb](https://github.com/microsoft/go-mssqldb) | The SQL Server driver itself, plus `batch` for `GO` splitting — `internal/query` |
| [github.com/golang-sql/sqlexp](https://github.com/golang-sql/sqlexp) | Interleaved result-set/message stream, so PRINT and errors arrive in order — `internal/query` |
| [github.com/clipperhouse/displaywidth](https://github.com/clipperhouse/displaywidth) | Terminal column width behind `core.DisplayWidth` — one of only two external modules `tuikit` imports |
