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
and callbacks (`OnExpand`, `OnSelect`, button `Action`s).

This means `tuikit` could be extracted into its own module and reused by a
completely different tcell application without modification.

## Package map

```
gossms/
├── cmd/
│   ├── gossms/               # main entry point
│   └── plandemo/             # dev harness: hosts planview.PlanView full-screen against a plan file (not part of the release build)
├── internal/
│   ├── config/              # connection profiles (JSON, in $XDG_CONFIG_HOME/gossms/)
│   ├── db/                  # gosmo connection wrapper + DSN builder
│   ├── query/               # SSMS-style script executor: GO batches, result sets, message stream, plan capture
│   ├── showplan/            # parses ShowPlanXML (estimated/actual) into a navigable operator tree; no TUI/DB deps
│   ├── version/             # gossms's own version metadata (mirrors gosmo/version); overridable via -ldflags -X
│   │
│   ├── tuikit/               # embeddable TUI library (no SQL Server / app knowledge) — see internal/tuikit/README.md
│   │   ├── theme/                # colour palette + derived tcell.Style helpers
│   │   ├── core/                 # Rect geometry, drawing primitives, string/int helpers
│   │   ├── widgets/               # InputField, DropDown, CheckBox, Button, RadioBox
│   │   ├── layout/                # Panel interface, PanelManager (tabs), Splitter
│   │   ├── dialogs/                # ModalDialog base (focus trap), Properties/Alert/Confirm/FileDialog
│   │   ├── controls/                # MenuBar, ContextMenu, Toolbar, TabStrip, TreeView, DataGrid, ListBox, Editor (+SQL highlighter)
│   │   └── propsheet/               # PropertySheet — multi-page editable properties dialog framework
│   │
│   └── tui/                  # goSSMS application layer (built on tuikit)
│       ├── planview/             # reusable control rendering a parsed plan: Plan (graph)/Tree/XML tabs
│       │
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
│       ├── explorer_drag.go      # drag a tree node into a query editor as a quoted T-SQL identifier
│       ├── tasks.go              # background task registry: Task (progress/cancel), App start/postProgress/postTaskDone
│       ├── clipboard.go          # copy/cut/paste plumbing shared by editor and dialog text fields
│       ├── os_clipboard.go       # OS-native clipboard, shelled out per-platform (fallback path for clipboard.go)
│       │
│       ├── query_panel.go        # QueryPanel state/layout, implements layout.Panel
│       ├── query_panel_exec.go   # Execute/Execute Selection/Cancel, plan-capture wiring
│       ├── query_panel_tabs.go   # result-set tabs + Messages tab
│       ├── query_panel_plan.go   # Estimated/Actual Execution Plan tabs, backed by planview.PlanView
│       ├── query_panel_export.go # Results To File (Text/Grid/File modes)
│       ├── plan_panel.go         # pops an Execution Plan tab out into its own closable panel
│       ├── completion_provider.go   # SQL completion.Provider: cursor-context resolution (FROM-scope, qualifiers) against the cached inventory
│       ├── completion_inventory.go  # per-database + per-server(sys schema) catalog cache for IntelliSense, async load
│       ├── completion_tokenizer.go  # SQL tokenizer feeding the completion resolver
│       ├── completion_scope.go      # FROM-clause/alias resolution and statement-boundary detection
│       ├── completion_candidates.go # schema/table/column candidate lookup against the cached inventory
│       │
│       ├── detail_browser.go            # Detail Browser, implements layout.Panel
│       ├── detail_browser_server.go     # Server node: version/edition/paths/CPU/memory, then NUMA + disk volumes
│       ├── detail_browser_databases.go  # Databases folder: name/state/recovery, then per-database size backfill
│       ├── detail_browser_logins.go     # Logins folder
│       ├── detail_browser_tables.go     # Tables folder: name, then per-table row count/space backfill
│       │
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
│       ├── new_job_dialog.go            # New Job creation dialog shell: prefetch, page wiring, validate/apply pipeline, Script Changes
│       ├── new_job_pages.go             # New Job's General/Steps/Schedules/Notifications page builders
│       ├── new_schedule_dialog.go       # New Schedule creation dialog: General page from agent_schedule_form.go + a Jobs-to-attach page
│       ├── new_alert_dialog.go          # New Alert creation dialog: General (alert definition) + Response (operators to e-mail) pages
│       ├── new_operator_dialog.go       # New Operator creation dialog, a single General page
│       │
│       ├── connect_dialog.go     # Connect dialog — form + saved-connection autocomplete + conn-string preview
│       ├── options_dialog.go     # Tools > Options — icon style, cell/row limits, IntelliSense on/off, saved to config.json
│       ├── query_list_dialog.go  # Tools > Query List — switch between open query panels
│       ├── tasks_dialog.go       # Tools > Background Tasks — live task list + Cancel
│       ├── help_dialog.go        # F1 help modal (embeds dialogs.ModalDialog)
│       ├── key_diagnostics_dialog.go # Help > Key Diagnostics — shows tcell's decoded Key/Modifiers/rune per keypress
│       ├── status_history_dialog.go  # running, timestamped, capped log of status-bar messages
│       ├── update_check.go       # Help > Check for Updates — GitHub releases API + semver compare
│       ├── update_dialog.go      # UpdateDialog — shows installed vs. latest release
│       ├── properties_dialog.go  # About + Object Dependencies (wraps dialogs.PropertiesDialog, the flat viewer)
│       │
│       ├── prop_dialog.go        # PropDialog — app orchestration for propsheet.PropertySheet (async loads, Apply)
│       ├── prop_grid_helpers.go  # small cross-cutting helpers (boolStr, indexOf, orDefault, credNames, buildFilterInfoForm)
│       ├── extended_properties_form.go # generic extended-properties add/edit/delete grid, shared across Properties dialogs
│       ├── role_descriptions.go  # fixed descriptive text for built-in database/server roles
│       ├── securables_matrix.go  # generic database-securable Grant/Deny/Revoke grid, reused by Table/Schema/Role/User Properties
│       ├── server_permissions_matrix.go # server-scope securables grid, used by Server/Login/Server Role Properties
│       ├── server_props.go       # Server Properties: shared config-row plumbing + page registration
│       ├── server_props_general.go      # Server Properties > General page
│       ├── server_props_memory.go       # Server Properties > Memory page
│       ├── server_props_processors.go   # Server Properties > Processors page (affinity mask bit-twiddling)
│       ├── server_props_security.go     # Server Properties > Security page
│       ├── server_props_connections.go  # Server Properties > Connections page
│       ├── server_props_database_settings.go # Server Properties > Database Settings page
│       ├── server_props_advanced.go     # Server Properties > Advanced page
│       ├── server_props_permissions.go  # Server Properties > Permissions page
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
│       ├── new_database_dialog.go # New Database dialog — builds and runs CREATE DATABASE
│       ├── new_database_pages.go  # New Database's page definitions
│       ├── new_login_dialog.go    # New Login dialog — builds and runs CREATE LOGIN
│       ├── new_login_pages.go     # New Login's page definitions
│       │
│       ├── backup_common.go      # helpers shared by the Backup and Restore dialogs
│       ├── backup_dialog.go      # Back Up Database dialog — options form + in-place progress
│       ├── restore_dialog.go     # Restore Database dialog — options form, backup-set inspection
│       └── restore_dialog_ops.go # Restore Database's background-task execution + history/file-list lookups
```

## Adding a new dialog

Construct it in `App.buildUI` and append it to `a.allDialogs` — that's the
only App-level change needed. `dialog_stack.go`'s `syncDialogStack` notices
it the moment its own `Show()` (or `Prompt()`/`ShowXxx()`) flips it
visible, pushes it to the top of the z-order, and routes it all input
until it closes itself; draw order, key routing, and mouse routing all
follow from the stack without touching `app.go` or `app_events.go` again.

## The mouseDragging idiom

tcell's all-motion mouse tracking resends the held button on every motion
event, not just on an actual click — so any widget that fires an action on
`Button1` (a toolbar button, a menu label, a tree-node toggle) needs a latch
(conventionally named `mouseDragging`) that's set on the triggering press
and cleared on the matching `ButtonNone` release, or the same action refires
on every motion event while the button stays down. This latch is per-widget:
it only guards a resend that stays over the widget that armed it.

A router that dispatches events by screen position (`App.handleMouse`) sees
every event regardless of where a gesture started, so a drag that begins
elsewhere and merely drifts across a latch-owning widget's row arrives there
as a fresh-looking `Button1` — the per-widget latch doesn't help, because it
was never armed for *this* gesture. Routers need their own gesture-wide flag
(e.g. `App.mouseButtonDown`, set/cleared from the raw event at the very top
of the dispatcher, before any positional branching) to tell a genuine fresh
press from a continuation.

Relatedly: any overlay-owning widget drawn last (see
`internal/tuikit/README.md`'s "overlays drawn last" rule) must also get
first refusal of every key/mouse event while open, and every host that owns
a latch-bearing child widget with an early `return` in its own `HandleMouse`
must forward a `ButtonNone` release to that child before the early return —
otherwise a drag that ends outside the host's bounds leaves the child's
latch stuck, silently swallowing its next press.

## Async result delivery: postEvent + wakeEventLoop

A background goroutine that reports its result via `App.postEvent(fn)` must
send the wakeup — `a.screen.EventQ() <- tcell.NewEventInterrupt(nil)`, or
the named helper `a.wakeEventLoop()` — **outside** the `postEvent` closure,
right after the `postEvent(...)` call, still on the background goroutine.
`Run()`'s event loop only drains queued callbacks when it wakes up for some
event on `EventQ()`; if the wakeup send is nested inside the very closure
that's waiting to be drained, nothing will ever wake the loop up to drain
it — the result sits queued, invisible, until an unrelated keypress happens
to arrive and drains it as a side effect.

## Building & testing

```bash
go build -o gossms ./cmd/gossms   # build
go run ./cmd/gossms                # run without building a binary
go test ./...                      # test
gofmt -w .                         # format in place
go vet ./...                       # vet
```

No Makefile — plain `go` toolchain only.

Version/Commit/Date (`internal/version`) resolve automatically, in priority
order: `-ldflags -X` (set by `.github/workflows/release.yml` from the pushed
git tag) → `debug.BuildInfo.Main.Version` (populated by `go install
.../cmd/gossms@<tag>`) → the literal `"(devel)"` default for a plain
`git clone && go build`/`go run`. Nothing here is hand-edited before a
release.

## Developing against a local gosmo checkout

`gosmo` is a separate repository
([github.com/radix29/gosmo](https://github.com/radix29/gosmo)) that goSSMS
depends on as a regular tagged module — no local sibling checkout is needed
for a normal build. To work on both repos together, `go.mod` keeps a
commented-out `replace github.com/radix29/gosmo => ../gosmo` directive
(and a matching `ignore ../gosmo` line above it): uncomment both so `go
build` picks up local edits from a `../gosmo` sibling checkout instead of
the tagged release. Build and test there too (`go build ./...`, `go test
./...` inside `gosmo`) before relying on a change from gossms. Once a change
is tagged and pushed, comment the `replace`/`ignore` pair back out and bump
the version in `go.mod`'s `require` line to match.

## Dependencies

| Package | Purpose |
|---------|---------|
| [github.com/gdamore/tcell/v3](https://github.com/gdamore/tcell) | Terminal UI rendering, keyboard & mouse events |
| [github.com/radix29/gosmo](https://github.com/radix29/gosmo) | SQL Server management objects (databases, tables, scripts…) |
