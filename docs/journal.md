# Engineering journal

Dated record of the work behind goSSMS and gosmo: what was built, what bugs
were found and how, and which decisions were made deliberately. Migrated
2026-07-30 from the Claude Code memory store, where it had accumulated as
per-session notes; entries before that date carry the date the note was
written.

This file is an archive — nothing here is required reading. `CLAUDE.md`
carries the rules that still apply; `docs/open-threads.md` carries the work
that is still open. Newest entries at the bottom. The `slug` under each
heading is the name the note had in the memory store, for older
cross-references.


---

## 2026-07-30 — Actual estimated plan toggle

`actual-estimated-plan-toggle-2026-07`

*Built the toolbar 'Include Actual Execution Plan' toggle + Query menu items; gosmo's ActualPlanContext discards real result rows, so gossms's own query executor gained plan capture instead*

2026-07-16, same day as [[estimated-execution-plan-binding-2026-07]] and
[[planview-bottom-properties-warnings-2026-07]]. User request (`todo/
todo.txt`): relabel the toolbar's inert "Show Execution Plan" button to
"Include Actual Execution Plan", turn it into a mouse-toggleable ON/OFF
button (`Actual Exec. Plan [--OFF]`/`[ON---]`, no shortcut yet — user said
they'll add one later that doesn't collide with the editor), add
"Estimated Execution Plan"/"Actual Execution Plan" to the Query menu, and
when the toggle is on, show the actual plan after Results, before
Messages, on every Execute.

**Key discovery that shaped the whole design:** `gosmo.Database.
ActualPlanContext` (`~/go/gosmo/executionplan.go`'s `capturePlan`) already
runs the statement for real via `SET STATISTICS XML ON`, but its scan loop
*discards every result set except the showplan one* — it returns only
`*ExecutionPlan{XML}`, never the real rows. Calling it a second time after
a normal `query.Execute` would re-run the statement — unacceptable for
anything with side effects (a second INSERT, etc.). Found by reading the
real source before designing, not assumed — this is exactly the kind of
gotcha CLAUDE.md's "verify against real source" rule exists for.
Consequence: "Results + Actual Plan together" couldn't be built by calling
gosmo's existing method — needed one round trip capturing both. Since
gossms's own `internal/query/executor.go` already reads results via
`sqlexp.ReturnMessage`'s message stream (bypassing gosmo's `Database`
methods entirely for this path), the showplan-column detection (`cols[0]
== "Microsoft SQL Server 2005 XML Showplan"`) went there instead, as a
small addition to `runBatch`'s existing `MsgNext` case — not in gosmo.
`query.ExecuteWithPlan` wraps a shared `execute(..., capturePlan bool)`
that adds `SET STATISTICS XML ON`/`OFF` around the batch loop; `Result`
gained a `PlanXML []string` field (one document per statement — actual
mode's STATISTICS XML emits one complete document per statement, unlike
estimated mode's SHOWPLAN_XML, which compiles the whole batch up front
into one combined multi-statement document).

**Two real bugs found and fixed before they could ship**, by reasoning
through the new "both `p.result` and `p.planView` non-nil at once" state
that Actual mode requires (Estimated mode kept them strictly mutually
exclusive, so this invariant never existed before): `QueryPanel.
renderActiveTab()` and `updateResultsStatus()` both did
`res.Sets[p.activeTab]`/`p.result.Sets[p.activeTab]` unconditionally once
Messages/ResultsText were ruled out — correct only because `planView !=
nil` used to imply `result == nil`. Selecting the new Execution Plan tab
(`activeTab == len(Sets)`, out of range for `Sets`) would have panicked in
both. Fixed with an explicit `planTabActive()` guard in each — caught by
reading the current code carefully during planning, then confirmed with a
dedicated regression test
(`TestRenderAndStatusDoNotPanicOnPlanTabWithResults`) rather than
discovered live. Also had to fix `setResult`'s Messages-tab fallback index
(hardcoded `len(res.Sets)`, wrong once "Execution Plan" can sit between
Results and Messages — now `len(p.resultTabs())-1`) and rewrite
`onMessagesTab()`/`planTabActive()`/`textTabActive()` to be index-based
formulas correct for all three tab-bar shapes (none/Estimated/Actual)
instead of the old two-branch special-casing.

**`buildToolbar()`/`buildMenus()` are only ever called once, at app
startup** (`app.go`'s `Run`) — confirmed by grep before assuming a toggle
could just flip a bool. Making the toolbar icon text and Query menu label
reflect live state required new plumbing:
`toggleActualExecutionPlan()` (`app_panel_actions.go`) flips
`App.actualPlanEnabled` then calls `a.toolbar.SetButtons(a.buildToolbar())`
+ `a.menuBar.SetMenus(a.buildMenus())` + `a.layoutAll()` to force both to
regenerate. `MenuItem` has no `Checked` field (would've touched shared
`MenuBar`/`ContextMenu` drawing code for cosmetic gain only) — state is
folded into the label text instead (`"Actual Execution Plan (ON)"`), same
trick as the toolbar icon (`actualPlanToggleIcon`/
`actualExecutionPlanMenuLabel`, both deliberately equal-width per ON/OFF
state so toggling never reflows neighboring toolbar buttons).

**Live-verified end-to-end against ubudock** (see
[[gossms-live-test-server]]): toggle click flips the toolbar label and
Query menu text together; toggle ON + a real SELECT shows exactly
Results/Execution Plan/Messages with real rows and a real (not estimated)
graphical plan (confirmed actual row counts in the operator tiles, e.g.
"10 rows" scanned vs. estimated); toggle ON + a syntax error shows no
Execution Plan tab (nothing executed) and the error lands on Messages
normally; toggle OFF reverts to plain Results/Messages. **Correctness
check that mattered most:** ran `CREATE TABLE ...; INSERT ... VALUES
(1);` as one batch with the toggle on, then a fresh `SELECT COUNT(*)`
confirmed exactly one row — the single-round-trip design's whole reason
for existing (no duplicated side effects) held. Noted but did **not**
chase further: SQL Server emitted `"(1 row affected)"` *twice* in
Messages for that one INSERT under `STATISTICS XML ON` — purely a
message-stream cosmetic quirk of that SET option (still exactly one row
in the table afterward), not a duplicate execution. If this resurfaces
and looks alarming, re-verify actual row count before assuming a
regression — it isn't one.

**Scope cuts, deliberate:** a script/selection that captures more than
one actual plan (multiple statements or GO batches) only shows the first,
with a Messages notice about the rest — merging multiple `<ShowPlanXML>`
documents into one multi-statement view (the way Estimated mode already
supports, for free, since SHOWPLAN_XML naturally emits one combined
document) was deferred as real additional work, not an oversight.

**Connect dialog field-focus order** (hit live while setting up this
verification, worth remembering for future tmux sessions — see
[[gossms-tui-tmux-testing]]): `ConnectDialog.rebuildFocusable()`'s order
is `fServer, fPort, ddAuth, fDatabase, fUser, fPassword, fTenantID,
fClientID, cbTrust, cbEncrypt, fExtraProps, fConnStrPreview` — reaching
Database from Server needs **3** Tabs (Server→Port→Auth→Database), not 2;
undercounting silently types into the wrong field (typed "HealthClinic"
into the Auth dropdown, which no-opped, then subsequent fields all landed
one field early). Default Auth is already "SQL Server Authentication"
with Trust Server Certificate pre-checked and Encrypt unchecked, matching
ubudock's requirements — no need to touch the dropdown or checkboxes for
that server. `Ctrl+Shift+O` (File > Connect's documented shortcut) is
indistinguishable from plain `Ctrl+O` (File > Open) under tmux's `C-S-o`
in this terminal — opened Open Query File instead; use `F10` then `Enter`
(File is the default first menu, Connect... is its first item) instead of
trusting the Ctrl+Shift shortcut headlessly.


---

## 2026-07-30 — Addroot selection bug

`addroot-selection-bug-2026-07-20`

*Fixed: Object Explorer Details stayed empty after first connect until the user reselected — TreeView.SetNodes clamps sel into bounds but never fires OnSelect; added TreeView.SelectID for real programmatic selection*

User-reported bug, fixed 2026-07-20: after connecting to a server for the
first time, Object Explorer Details stayed blank until the user selected a
different node and then reselected the server — only then did it populate.

**Root cause**: `controls.TreeView.SetNodes` (`internal/tuikit/controls/treeview.go`)
only clamps `tv.sel` into `[0, len(nodes)-1]` — it never calls `fireSelect()`/
`OnSelect`. `ObjectExplorer.AddRoot` (`internal/tui/object_explorer.go`) adds
the new server root and calls `rebuild()` → `SetNodes`, so on the very first
connection `tv.sel`'s zero value (0) happens to clamp onto the new root — the
row *renders* selected, giving the visual impression it's selected, but no
selection event ever fired. Only `HandleKey`/`HandleMouse`/`toggleExpand`
call `fireSelect()`, which is the only path that reaches
`onNodeSelected` → `DetailBrowser.ShowNodeDetails`
(`internal/tui/app_explorer_data.go`). **General lesson: `SetNodes`'
sel-clamp is a bounds check, not a "this is now selected" signal — don't
assume "the index lands somewhere valid" means "the right thing is
selected."**

**Fix**: added `TreeView.SelectID(id TreeNodeID)` — finds the node by ID,
sets `tv.sel`, calls `ensureVisible` + `fireSelect()`, i.e. does what a real
click/arrow-key selection does. `ObjectExplorer.AddRoot` now calls
`oe.view.SelectID(n.id)` right after `rebuild()`, so a newly connected
server is both visually selected *and* triggers `onNodeSelected` →
`ShowNodeDetails`, matching real SSMS (connecting an additional server also
steals Object Explorer focus onto it, not just the first one — verified
this is the correct, not incidental, behavior to match).

**Test fallout worth remembering**: this exposed two things existing tests
had been coasting on:
1. `TestDisconnectActiveUsesSelectedRoot` had literally pinned the *old*,
   incidental clamp-to-first-row behavior in its own doc comment ("After
   SetNodes the TreeView selection defaults to the first row") — it wasn't
   testing real intent, just formalizing what happened to fall out of the
   bug. Updated to assert the corrected behavior (connecting sc1 then sc2
   leaves sc2 selected, matching SSMS).
2. `addTestConn`'s fake `*db.ServerConn{Opts: ...}` (`app_connections_test.go`)
   has `closed=false` by zero value, so `IsOpen()` reports true despite a
   nil `gosmo.Server` — harmless before since nothing walked into `sc.Server`
   from these lifecycle-only tests, but `AddRoot` now firing a real
   selection immediately reaches `DetailBrowser.loadServerDetails`'s
   goroutine calling `sc.Server.Info()` → nil-pointer panic on a background
   goroutine. **Did not fix this by making the fixture more "realistic"**
   (there's no way to construct a working fake `*gosmo.Server` from outside
   the package — `Info()` reads an unexported cached field) **or by adding a
   nil/scenario guard for "an open connection with a nil Server"** (that
   state is genuinely impossible for a real `db.Connect` success — guarding
   for it would be exactly the kind of defensive code CLAUDE.md says not to
   add). Instead made `DetailBrowser.ShowNodeDetails` nil-safe on its
   receiver, extending the *already-established* convention from
   `Invalidate` (whose doc comment already says "nil-safe ... so call sites
   (and tests that build an App without a DetailBrowser) don't need their
   own nil check") — `newTestApp()` still builds an `App` with no
   `detailBrowser` at all, and `onNodeSelected`'s call into it now just
   no-ops for these tests instead of ever reaching the fake `sc.Server`.
   **How to apply**: when a lifecycle/bookkeeping test builds a minimal
   `App`/`ServerConn` fixture and a new code path starts touching a
   dependency that fixture never populated, prefer extending that
   dependency's existing nil-safety convention over bulking up the fixture
   to be "more real" — especially when making the fixture fully real isn't
   even possible from outside the package.


---

## 2026-07-30 — Agent stopped label

`agent-stopped-label-2026-07`

*SQL Server Agent tree node shows ' (Stopped)' when the service is confirmed not running, plain label when running; fixed a real gosmo bug (wrong DMV column name) found while live-testing this*

User request: "if the SQL Agent is not running add (Stopped) near the label
in the object explorer. if it is running show only the label." Small,
direct follow-up to [[sql-agent-schedule-alert-operator-props-2026-07]] —
no plan mode needed, implemented directly.

**Design constraint discovered mid-implementation**: `loadServerChildren`
(`explorer_databases.go`, builds the server root's top-level folders
including "SQL Server Agent") is deliberately a *static*, no-DB-query
loader — `TestSQLServerAgentIsSiblingOfDatabases` and its siblings call
`childLoaders[NodeServer]` directly against a bare `db.ServerConn{Opts:...}`
with `sc.Server == nil`, so any gosmo call inside it panics (nil-pointer
method call). First attempt (querying `AgentInfoContext` inline in
`loadServerChildren`) broke that test. Fixed by keeping the loader static
and instead adding `(*App).refreshAgentRootLabel` in `app_explorer_data.go`,
called from `loadChildren` right after `SetChildren` only when
`node.data.Type == NodeServer` — a background goroutine fetches
`AgentInfoContext`, then on the UI goroutine (via the standard
`postEvent`+`wakeEventLoop`-outside-the-closure pattern) mutates the
already-created "SQL Server Agent" child node's `.label` in place and calls
`a.explorer.rebuild()` (same idiom as `agent_common.go`'s
`setAgentEnabled`). Only appends " (Stopped)" when the status is positively
confirmed not-running (`err == nil && StatusText not "" or "Unknown" &&
!Running`) — a failed/inconclusive check silently leaves the plain label,
never guesses.

**Real gosmo bug found live-testing, fixed same session**: ubudock's Agent
service is actually Stopped, but the tree kept showing the plain label with
no suffix, and the pre-existing "SQL Server Agent" Details panel (unrelated
code, `agent_detail.go`, written in an earlier session) had *also* always
silently shown "Status: Unknown" — nobody had noticed because "Unknown"
looks like a plausible value on a lightly-instrumented Linux container.
Root cause: `agent_job.go`'s `AgentInfoContext` query selected a column
named `startup_time` from `sys.dm_server_services`, but the DMV's real
column is `last_startup_time` — confirmed via `SELECT TOP 1 *` and MS
docs. The query threw `mssql: Invalid column name 'startup_time'`, which
`AgentInfoContext`'s own error handling only distinguishes from genuine
"no matching row" via `errors.Is(err, sql.ErrNoRows)` — a real driver error
takes the *other* branch and returns a non-nil `error` — but
`agentServerDetail` (`agent_detail.go`) swallows any non-nil `AgentInfo()`
error down to the same "Unknown" default text, so the two cases were
visually indistinguishable in the UI. Caught only by writing a standalone
throwaway Go program that called `AgentInfoContext` directly and printed
the raw `err` — the layered swallowing in gossms's own display code would
never have surfaced it. One-line fix in `~/go/gosmo/agent_job.go`:
`startup_time` → `last_startup_time`. This also fixed the pre-existing
Details-panel "Status: Unknown" bug as a side effect — same query, two
call sites.

**Live-tested against ubudock** (real Agent state: Stopped, confirmed via
direct `sqlcmd` against `sys.dm_server_services` before touching any code):
after the gosmo fix, both the tree ("⏱ SQL Server Agent (Stopped)") and the
Details panel ("Status: Stopped") show the correct state; Refresh on the
server root re-derives the label once (no "(Stopped) (Stopped)"
duplication, since each refresh rebuilds the child node list from
scratch). Could not verify the "Running" case by actually starting the
service — `xp_servicecontrol 'START', 'SQLServerAGENT'` timed out on this
container (service control isn't available there, consistent with
[[sql-agent-schedule-alert-operator-props-2026-07]]'s already-documented
Agent-XPs/environment limitation) — verified the Running branch by code
inspection instead (`status.Running` true → early return, label
untouched).

**Process note**: this is a good example of why the project's live-testing
discipline catches things unit tests structurally can't — the "Unknown"
default in `agentServerDetail` was written and shipped in an earlier
session, passed every test and build check, and looked correct in the UI
(a real, if unhelpful, status value) until this session's live test showed
"Unknown" when direct SQL proved the true answer was "Stopped" and a raw
diagnostic program surfaced the swallowed driver error underneath.


---

## 2026-07-30 — Backup restore script and history

`backup-restore-script-and-history-2026-07`

*Backup/Restore dialogs gained a Script button + a typed overwrite-confirm gate; new 'View Backup History' context menu item; a real dialog layout bug found+fixed live*

Implemented from a terse todo.txt note ("backup / restore dialog -> Scripts
button. ok -> do the task, for restore, ask for confirmation for overwrite
(type first 4 chars of the database)" / "view backup history (database
level, context menu) -> new query window with the pre-filled query &
execute it"), no mockup, scoped and built autonomously under Auto Mode.

**Why:** user's own reminder note for standing backup/restore feature gaps.

**What shipped:**

- **`internal/tuikit/dialogs/typed_confirm_dialog.go`** (new, generic,
  tuikit-level): `TypedConfirmDialog` — a "retype text to confirm" prompt,
  distinct from the existing plain-Yes/No `ConfirmDialog`. Confirm only
  fires once typed text matches `Required` case-insensitively; a mismatch
  refuses in place with a status message rather than silently doing
  nothing. Wired into `App` as `a.confirmTypedDialog`, same pattern as
  `a.confirmDialog`. Reusable for any future "dangerous, hard-to-reverse
  action" gate in this app (Drop Database would be a natural next user).
- **Backup/Restore "Script" buttons** (`backup_dialog.go`,
  `restore_dialog.go`/`restore_dialog_ops.go`): both dialogs already had
  `gosmo.BuildBackupStatement`/`BuildRestoreStatement`; Backup's existing
  "Validate" button only echoed the statement to the status line. Script
  is new and opens the built T-SQL in a real query window via
  `app.openQueryWithText`, matching the established Script-Changes
  convention elsewhere (`PropDialog.runScript`, `NewLoginDialog.runScript`)
  — dialog stays open, nothing executes. Restore's Script needed the same
  async backup-metadata read (headers + file list, for MOVE/relocate
  clauses on a renamed target) that `runRestore` already did, so that logic
  was extracted into a shared `RestoreDialog.buildRestoreOptions` used by
  both the real restore and the dry-run script — a genuine dedup, not
  gratuitous refactor.
- **Restore overwrite confirmation**: `startRestore` now checks
  `DatabasesContext` for the target name before doing anything; if it
  already exists, `confirmOverwrite` shows `TypedConfirmDialog` requiring
  the target's first 4 characters before `beginRestore` (the renamed old
  `startRestore` body) runs. A non-existent target skips the gate
  entirely. `runRestore` itself still does its own fresh `exists` check
  right before running (needed for the close-connections/SINGLE_USER step)
  — deliberately *not* threaded through from the earlier check, since the
  state could change while the user was staring at the confirm dialog and
  the check right before executing is the one that actually matters.
- **"View Backup History"** (`app_explorer_data.go`'s `NodeDatabase` menu,
  `app_panel_actions.go`'s `showBackupHistoryFor`, new
  `backup_common.go` helpers `sqlStringLiteral`/`backupHistoryQuery`):
  opens a new query panel scoped to `msdb` (where `backupset`/
  `backupmediafamily` actually live, regardless of which database node was
  right-clicked), pre-filled with a human-readable rewrite of
  `gosmo.Server.BackupHistoryContext`'s own query (literal database name
  substituted in, not a `sqlserver` driver parameter — this is meant to be
  edited/reused by the user, not just displayed), and runs it immediately.
- **New `App.connectForQueryPanel` parameter**: `onConnected func()`,
  nil-safe, called once `qp.conn` is set — needed because a freshly opened
  query panel's connection is async and `qp.Execute()` before it resolves
  just reports "Not connected". New `App.openQueryWithTextAndExecute`
  wraps `openQueryWithText` + this callback for "open and run immediately"
  call sites (today, only View Backup History). All 5 pre-existing
  `connectForQueryPanel` call sites updated to pass `nil`.

**Real bug found and fixed during live testing** (not pre-existing —
introduced this session, caught before shipping): `TypedConfirmDialog`'s
first draft placed the confirmation `InputField` at `inner.Y+4` and the
error status line at `d.ButtonRowY()-2`, which for this dialog's fixed
10-row height are the *same screen row* — a mismatch's error text visually
painted over the input box (and whatever the user had typed became
invisible, though still present in the field's actual state). Only visible
by actually driving it in tmux, not from code review or `go vet`; fixed by
moving the input to `inner.Y+3`, freeing the row the status line already
used. Live-verified after the fix: mismatch shows the error *and* keeps
the typed text visible on its own row.

**Live-tested against ubudock** (`HealthClinic`, real `.bak` at
`/var/opt/mssql/data/HealthClinic_full.bak` from earlier sessions'
backups): View Backup History opened+ran automatically, returned 4 real
rows (Size column shows as hex — pre-existing, already-documented
DECIMAL-rendering bug from
[[detail-browser-widevalue-and-tables-2026-07-19]], not touched here).
Backup dialog Script produced a correct `BACKUP DATABASE ... WITH
COMPRESSION, CHECKSUM, INIT` in a new panel, dialog stayed open. Restore
dialog Script produced a correct `RESTORE DATABASE ... FROM DISK = ...
WITH RECOVERY` (no MOVE clauses, since source==target in the test).
Overwrite-confirm gate: typing a wrong prefix correctly refused with the
dialog staying open; typing the correct prefix ("Heal") then clicking
**Cancel** (not Confirm) correctly aborted with zero writes — deliberately
did *not* click Confirm live, since that immediately starts a real
`RESTORE` against the shared `HealthClinic` test database other sessions'
live tests depend on; the match-then-Confirm and Escape-cancels-without-
match-required paths are instead covered by new unit tests in
`typed_confirm_dialog_test.go` (4 cases: mismatch refuses+stays open,
match confirms+closes+fires callback, match is case-insensitive/
whitespace-trimmed, Escape cancels without requiring a match).


---

## 2026-07-30 — Context gated menu toolbar build

`context-gated-menu-toolbar-build-2026-07`

*Built real visual Enabled/grey-out support for the menu bar + toolbar (2026-07-20) — the cross-cutting change context-gated-actions-rule deferred*

Implemented the visual disabled/grey-out mechanism that
[[context-gated-actions-rule]] explicitly deferred as "a bigger
cross-cutting change... surface it as an explicit design decision before
doing it." That decision: `MenuItem`/`ToolbarButton` (`internal/tuikit/controls`)
gained an `Enabled func() bool` field (nil = always enabled), evaluated
live at draw/nav time — not a static bool, so none of the ~15 state-mutating
call sites (connect, disconnect, select node, execute, close panel...)
need to rebuild the menu/toolbar to stay correct. Disabled items stay
visible, render in a new `theme.StyleDisabled()` (found
`Palette.TextDisabled` already existed, declared but unused anywhere in
the codebase — used it), are skipped by `firstSelectableItem`/
`stepSelectableItem` keyboard nav, and clicking/Enter-ing one closes the
menu without firing (same treatment a divider already got). Toolbar
buttons keep showing their tooltip while disabled (`buttonAt` still
returns them for hover purposes) — only the click-firing guard changed.

**Why:** user wanted every menu/toolbar action to be unclickable, not just
reactively guarded, whenever its precondition isn't met (e.g. Server/Database
Properties with no connection) — explicitly framed as "first find all these
dependencies and show me, let me validate them, then create an implementation
plan" (full plan-mode flow used: dependency audit → AskUserQuestion for two
scope decisions → Plan subagent → written plan → approval → implementation).

**Scope decisions locked in before implementation** (both "recommended"
options, both confirmed by the user): (1) both the menu bar AND toolbar get
gated, not menu-bar-only; (2) disabled items stay visible/greyed and are
skipped in nav, rather than being removed from the list (which is what the
*already-correct* Object Explorer right-click context menu does instead —
that one was explicitly left untouched, it never needed this).

**Also fixed while wiring predicates:** a 4th instance of the "silent
no-op with no status message" bug class from the original 2026-07-16 audit
— `executeActiveQuery` (backs Query > Execute and the toolbar's ▶) had no
`else { setStatus(...) }`, unlike every sibling. Also exported
`layout.panelClosable` → `PanelClosable` (was already a private helper) and
added a `selectedServerConn()` helper to deduplicate a resolve-selected-
Object-Explorer-node-to-a-connection pattern that had been copy-pasted
across three call sites.

**How to apply:** any future new menu item or toolbar button should get an
`Enabled` closure from the start if it has any real precondition — the
mechanism exists now, so a new reactive-only guard would be a regression,
not just a missed nice-to-have. Live-tested end-to-end against the real
ubudock server (0 connections → connect → select a database node → open a
query panel → run a real 3s query → close the panel), confirming the
closure design needs zero rebuild plumbing: Cancel/Stop Execution ungated
mid-query and greyed back out the instant it finished, with no extra
keypress. See [[gossms-live-test-server]] for the server used.


---

## 2026-07-30 — Database properties mockup gapfill

`database-properties-mockup-gapfill-2026-07`

*Database Properties was brought in line with todo/mockups/ssms_database_properties_tui_mockup.md (2026-07-15) — what shipped, what's deferred, and two real bugs live-testing caught*

Implemented, spanning both `~/go/gosmo` and `~/go/gossms` (via the active
`replace` in `gossms/go.mod`), after auditing `internal/tui/database_props.go`
against `todo/mockups/ssms_database_properties_tui_mockup.md` — the same
treatment as [[server-properties-mockup-gapfill-2026-07]] one session earlier.

- gosmo: `CompatLevel2025 = 170` (verified live — the ubudock test server's
  databases are actually compat 170, not 160; the old cap silently defaulted
  the dropdown to "100" for any of them), `BackupActionDifferential` +
  mapping `sys.backupset.type = 'I'` to it, `DatabaseOptions.IsEncrypted`
  (`sys.databases.is_encrypted`), `FileGroup.IsReadOnly`
  (`sys.filegroups.is_read_only` — previously hardcoded "N/A" in the UI),
  `Database.SetUserAccessContext` (MULTI_USER/SINGLE_USER/RESTRICTED_USER,
  mirrors `SetReadOnlyContext`'s shape), and two new files:
  `database_scoped_config.go` (`DatabaseScopedConfigsContext`/
  `SetDatabaseScopedConfigContext`, the latter running through `d.exec`
  with a `USE` prefix since `ALTER DATABASE SCOPED CONFIGURATION` is
  current-database-scoped, *not* `d.server.execContext` like
  `ALTER DATABASE` itself) and `query_store.go`
  (`QueryStoreContext`/`SetQueryStoreOptionsContext`/`FlushQueryStoreContext`/
  `ClearQueryStoreContext`).
- gossms: General page's Containment section + Last differential backup row,
  Options page's Restrict Access made editable + compat-170 added to the
  dropdown, Extended Properties' missing Note, and three substantial
  reworks: **Files** and **Filegroups** went from fully-read-only dumps to
  editable pages with Add/Remove (gosmo already had every needed method —
  `AddFileContext`/`AlterFileContext`/`RemoveFileContext`/
  `AddFileGroupContext`/`RemoveFileGroupContext`/`SetDefaultFileGroupContext`/
  `SetFileGroupReadOnlyContext` — this was gossms UI work only); **Database
  Scoped Configurations** and **Query Store** are brand new pages following
  the Server Properties "Advanced" page pattern (grouped editable rows for
  well-known options + read-only dump grid underneath for the rest).

**Deliberately not done, consistent with the Server Properties pass:**
filter/search boxes (Options', Database Scoped Config's), the Options page's
"confirm risky change" warning modal, WITH GRANT OPTION, Effective
Permissions (Permissions page was already at parity — it reuses
`buildPermissionsMatrix` built for Server Properties), Transaction Log
Shipping and Mirroring pages (legacy/deprecated, no gosmo support, large
multi-server surface — recommended skipping outright rather than half-
building), and Database Scoped Config's "secondary replica values" (Always
On readable-secondary overrides — `SetDatabaseScopedConfigContext`'s
`forSecondary` param already supports this if ever needed).

**Two real bugs live-testing caught, not just cosmetic ones:**
1. Files page `apply()` originally called `AlterFileContext` for *every*
   existing file unconditionally, always including `MaxSizeKB` even for
   files nobody touched (gosmo's `FileModify` treats `MaxSizeKB == 0` as
   "no change" but any real max-size value is always non-zero) — Script
   Changes showed a spurious `MODIFY FILE ... MAXSIZE=...` for an untouched
   log file. Fixed by gating the whole `AlterFileContext` call behind a
   per-file "did anything actually change" check, not just gating individual
   `FileModify` fields.
2. Files page's "Add" button called `commitCurrent()` first (copying
   Extended Properties' pattern), which writes the shared `nameField`'s text
   into whichever file was last *selected in the grid* — safe for Extended
   Properties (whose `commitCurrent` only ever touches `.value`, never
   `.name`, because properties can't be renamed) but not for Files, which
   *does* support rename. Typing a brand-new name intending to Add silently
   renamed the previously-selected file instead. Fixed by dropping the
   `commitCurrent()` call from Add entirely (a `fileEdit` mid-edit but not
   yet re-selected loses that unsaved edit if Add is clicked without
   reselecting first — an acceptable trade-off vs. the corruption).
3. (gosmo, not gossms) `Database.QueryStoreContext`'s scan crashed with
   `converting NULL to int is unsupported` on `capture_policy_execution_count`
   whenever query capture mode isn't `CUSTOM` (the common case, verified —
   all four `capture_policy_*` columns are NULL then) — original scan used
   plain `int`/`int64` instead of `sql.NullInt64`. This would have made the
   Query Store page error out on almost every real database.

All three were caught only because of end-to-end live testing against
[[gossms-live-test-server]] (ubudock) on a disposable `claude_test_db`
(created via `Server.CreateDatabase`, dropped via `DropDatabase` at the
end) — Script Changes' generated SQL was inspected after every edit, not
just the widget's visual state, which is what surfaced bug #1 (the spurious
statement was invisible in the UI, only visible in the scripted output).
See [[gossms-tui-tmux-testing]] for the Tab/Down/zone-navigation gotchas
this session ran into repeatedly while driving the dialog headlessly.


---

## 2026-07-30 — Database role properties mockup build

`database-role-properties-mockup-build-2026-07`

*Database Role Properties built from scratch against todo/mockups/ssms_database_role_properties_tui_mockup.md (2026-07-15) — gossms had zero role-properties support before this; a gosmo field-semantics bug and an invalid permission name were found and fixed via live testing*

Same shape as [[table-properties-mockup-build-2026-07]]: **Database Role
Properties did not exist at all in gossms** before this session — no
`internal/tui/role_props.go`, no "Properties..." on `NodeDatabaseRole`'s
context menu (the node/tree listing itself already existed via
`gosmo.DatabaseRolesContext`). Built from scratch spanning `~/go/gosmo`
and `~/go/gossms` (active `replace` in `gossms/go.mod`), following
`table_props.go`'s pattern.

**Shipped, 6 pages**: General (name/owner editable unless the role is
fixed or `public`; SID/dates/summary counts), Members (add/remove via a
`Select` dropdown of non-member users+roles, reusing
`AddRoleMember`/`RemoveRoleMemberContext`), Owned Schemas (transfer
ownership, object count via new `Schema.ObjectCountContext`), Owned Roles
(transfer ownership via new `DatabaseRole.ChangeOwnerContext` — same
method serves both General's own-owner field and this page's "roles this
role owns"), Securables (new `buildSecurablesMatrix` in
`prop_grid_helpers.go` — the structural inverse of
`buildPermissionsMatrix`: one principal x many securables, instead of one
securable x many principals — covering Table/View/Schema/Database
securable classes), Extended Properties (direct reuse of
`buildExtendedPropertiesForm` with `Level0Type: "USER"`, per the mockup's
own footnote that roles are classed as USER principals).

**Deliberately not built**: application roles (different principal type,
no tree node/listing exists for them at all — `DatabaseRolesContext`
hardcodes `type = 'R'`), nested-role circular-membership
detection/warning, stored-procedure/function securables in the Securables
page (`PermissionsForPrincipalContext` restricts `OBJECT_OR_COLUMN` rows
to `type IN ('U','V')` — procs/functions need their own EXECUTE-centric
catalog, flagged for later, same shape as the Table Properties session's
deferred Column Permissions gap), Add Securables modal/Column
Permissions/Effective Permissions/WITH GRANT OPTION/filter-search (same
four deferred every prior pass), Delete Database Role modal (mockup
itself frames it as optional/separate from the Properties dialog).

**gosmo additions**: `DatabaseRole.SID/CreateDate/ModifyDate` fields +
`RoleByNameContext` (single-role fetch, mirrors `LoginByNameContext`) +
`RenameContext`/`ChangeOwnerContext`; `RoleMembersContext` (member name +
`type_desc`, `DatabaseRolesContext`'s existing concatenated-name string
has no type per member); `Schema.ObjectCountContext` (count of
`sys.objects`, `is_ms_shipped = 0` — genuinely includes constraint objects
like a PK, not just tables/views, matching SSMS's own count semantics —
verified live a 2-object schema (1 table + 1 view) reports "3" because of
the table's PK constraint); `SchemaPermissionNames()` +
`Grant/Deny/RevokeSchemaPermissionContext` (schema-level GRANT/DENY/REVOKE
`ON SCHEMA::x` — new capability, catalog = `objectPermissionNames` **plus
EXECUTE**, verified live all 11 permissions grant successfully on a
schema where EXECUTE specifically fails on a table);
`Database.PermissionsForPrincipalContext` + `PrincipalSecurable` (new
principal-centric permissions query — the inverse of the existing
object-centric `PermissionsContext` — resolving `DATABASE`/`SCHEMA`/
`OBJECT_OR_COLUMN` class rows into one normalized shape).

**Two real bugs found and fixed via live testing**:
1. **Field-semantics bug in the new `PermissionsForPrincipalContext`**:
   for a `SCHEMA`-class row, the query's `schema_name` column (needed to
   resolve an `OBJECT_OR_COLUMN` row's containing schema) was scanned into
   `PrincipalSecurable.Schema` for every row uniformly — but for a SCHEMA
   row itself, that value *is* the securable's own name, and `.Name`
   should hold it instead (matching how `.Name` already means "the
   securable's own name" for TABLE/VIEW rows). Left unfixed, this
   produced an empty `[]` label for schema securables in the UI **and** a
   duplicate: the securable-list-building code in `role_props.go` keys
   in-use securables by `Type+Schema+Name`, so a schema with an existing
   grant (keyed wrong, with the name in `.Schema`) didn't match the same
   schema appearing in the "available to add" list (built correctly, name
   in `.Name`) — it showed up in both places at once. Fixed by
   normalizing in `PermissionsForPrincipalContext` itself: for `SCHEMA`
   class, `e.Name = e.Schema; e.Schema = ""`.
2. **`databasePermissionNames` included a permission this SQL Server
   version rejects**: `GRANT ADMINISTER DATABASE BULK OPERATIONS` failed
   live with "The permission 'ADMINISTER DATABASE BULK OPERATIONS' is not
   supported in this version of SQL Server. Alternatively, use the server
   level 'ADMINISTER BULK OPERATIONS' permission." — `serverPermissionNames`
   already has the correct server-level name; the database-level one was
   simply wrong/deprecated for this build and has been removed from the
   allowlist with a comment citing the exact error. This wasn't new code
   from this session — it was sitting unnoticed in `databasePermissionNames`
   since the Database Properties pass — surfaced only because Database
   Role Properties' Securables page is the first UI to actually iterate
   and grant every name in that catalog end-to-end.

**One known, accepted (not fixed) architectural gap, not new to this
session**: renaming a role via General's Apply succeeds for real (verified
— the role really is renamed in `sys.database_principals`), but the SAME
open dialog's `propPage` closures are all built once at dialog-open time
with the *original* role name baked in as a parameter — so General's own
post-Apply reload calls `RoleByNameContext` with the now-stale old name
and shows "not found: Press F5 to retry" (reopening the dialog fresh works
fine). This is the identical trade-off `login_props.go`'s own rename
handling already has; not introduced or fixed here, just re-confirmed by
hitting it live.

**Live-verified SQL Server facts worth remembering**:
- `public`'s `is_fixed_role` is **`False`** in `sys.database_principals`
  (unlike every other fixed role) despite being just as unrenameable/
  unownable — `ALTER ROLE public WITH NAME = ...` and `ALTER AUTHORIZATION
  ON ROLE::public TO ...` are both flat **syntax errors** (SQL Server
  special-cases the `public` keyword), not permission errors. `role_props.go`'s
  `isBuiltinRole()` treats `role.IsFixedRole || role.Name == "public"` as
  the read-only/general-page-uneditable condition for exactly this reason.
- SQL Server **auto-creates a schema matching every fixed database role's
  name** (`db_owner`, `db_accessadmin`, ... — one schema per fixed role,
  each owned by the matching principal), in addition to `dbo`/`guest`/
  `sys`/`INFORMATION_SCHEMA`. Surprising the first time it showed up as an
  "addable securable" candidate in the Securables page — confirmed via a
  direct `sys.schemas` query, not a bug.
- Extended properties at `USER` level (the level roles use, since they're
  principals) **survive a rename** — `sp_addextendedproperty`'s binding
  is by principal identity, not the literal name string passed at
  creation time; verified `MS_Description` stayed attached after renaming
  `claude_reader` → `claude_reader_renamed`.

Verified end-to-end against a disposable `claude_role_test` database with
hand-built (direct-SQL, not gosmo) test principals `claude_u1`/
`claude_u2`/`claude_owner`/`claude_reader`, a `claude_schema` schema, and
a `Widgets` table/`WidgetsView` view — every Apply round-trip
(member add/remove, schema-ownership transfer, role-ownership transfer,
rename, database- and schema-level Grant, extended-property add) verified
via direct re-query against `sys.database_*` catalog views afterward, not
just by trusting the UI's own redisplay. Database dropped at the end. See
[[gossms-tui-tmux-testing]] for a new gotcha this session added: computing
a mouse-click column from a `tmux capture-pane` line containing box-
drawing/emoji characters via `awk`'s `index()` undercounts real screen
columns badly (`index()` counts bytes, and multi-byte UTF-8 chars before
the target inflate the byte count well past the true column) — use
Python's `str.find()` (codepoint-based) instead.


---

## 2026-07-30 — Database user properties mockup build

`database-user-properties-mockup-build-2026-07`

*Database User Properties built from scratch against todo/mockups/ssms_database_user_properties_tui_mockup.md (2026-07-15) — gossms had zero user-properties support before this; almost entirely reused Database Role Properties' machinery from the same session's earlier pass; one real User-type classification bug found and fixed via live testing*

Same shape as [[database-role-properties-mockup-build-2026-07]]: **Database
User Properties did not exist at all in gossms** before this session — no
`internal/tui/user_props.go`, no "Properties..." on `NodeUser`'s context
menu (the tree listing itself already existed via `gosmo.Database.
UsersContext`). Built from scratch, following `role_props.go`'s pattern —
and this time **almost the entire Securables/Extended Properties/Owned
Schemas machinery from the Database Role Properties pass one turn earlier
in the same session was reused completely unchanged**, since
`buildSecurablesMatrix`/`grantSecurable`/`denySecurable`/`revokeSecurable`/
`PermissionsForPrincipalContext` were already built generic over
"principal name," not specifically "role name."

**Shipped, 5 pages** (literally all five the mockup itself calls out as
"the common SSMS pages"): General (editable except for the 4 fixed system
users), Owned Schemas (direct copy of `pageRoleOwnedSchemas`, keyed by
user name), Membership (the *inverse* of Role Properties' Members page —
"which roles is this user in," built with `propsheet.NewToggleGrid`
mirroring `login_props.go`'s `pageLoginServerRoles` almost verbatim, plus
a new `fixedRoleDescriptions` lookup map in `prop_grid_helpers.go` for the
mockup's "Manage access"/"Back up DB"/... blurbs), Securables (direct
reuse, zero new gossms code), Extended Properties (direct reuse,
`Level0Type: "USER"` — same level type roles use, since both are `USER`-
classed principals).

**gosmo additions**: `User.SID`/`LoginName`/`LoginDisabled` fields +
`UserByNameContext` (single-user fetch, `LEFT JOIN sys.server_principals
sp ON sp.sid = dp.sid` — the same join `Login.UserMappingsContext`
already uses, inverted, answering "is this user login-mapped, and is that
login disabled" in one query); `User.RenameContext`/
`SetDefaultSchemaContext`/`SetLoginContext` (`ALTER USER ... WITH
NAME/DEFAULT_SCHEMA/LOGIN = ...`, one-line pairs matching existing
`Login`/`DatabaseRole` conventions exactly).

**One real bug found and fixed, from an incorrect premise carried over
from planning**: the plan assumed (based on inspecting one pre-existing
user, `gosmo_hashed_bug_test` in `HealthClinic`) that SQL Server's catalog
metadata can't distinguish a genuine `CREATE USER ... WITHOUT LOGIN` from
an orphaned user (one created `FOR LOGIN` whose login was later dropped)
— both looked like "has a SID, no matching `sys.server_principals` row."
**Live testing with a freshly-created `WITHOUT LOGIN` user disproved
this**: a genuine WITHOUT LOGIN user reports `authentication_type_desc =
'NONE'`, while an orphaned FOR-LOGIN user keeps `authentication_type_desc
= 'INSTANCE'` with no matching login — the two cases *are* reliably
distinguishable after all, just not by the single field (`LoginName`
being empty) the plan focused on. `gosmo_hashed_bug_test` was apparently
an orphaned-shaped leftover from unrelated bug testing, not a WITHOUT
LOGIN example, and looking at only that one user led to the wrong
generalization. Fixed the "User type" classification in
`pageUserGeneral` (`user_props.go`) to read `AuthType` correctly
(`NONE` → "SQL user without login", `INSTANCE` + no login → "SQL user
with login (not found)" — i.e. orphaned) and corrected the now-wrong
"can't distinguish" claim in `User.LoginName`'s doc comment
(`schema_user.go`) to state the real, verified distinction instead. A
good reminder that a single example (especially a leftover/synthetic one
from prior bug-hunting) can't be safely generalized from — verify with a
deliberately-constructed, known-provenance test case before asserting a
platform limitation.

**Also hit, correctly recognized as not-a-bug**: the test setup's first
`GRANT SELECT ON claude_user_schema.Items TO claude_login_user` silently
had no effect (no row landed in `sys.database_permissions`) because
`claude_login_user` *owned* `claude_user_schema` (`CREATE SCHEMA ...
AUTHORIZATION claude_login_user`) — the exact same "granting to the
schema/object owner is a genuine SQL Server no-op" pattern already
documented in [[gossms-live-test-server]] from the Table Properties
session, this time triggered by accident via test-data setup rather than
deliberately. Recognized quickly this time (no wasted debugging) and
fixed by granting to a different, non-owner test user instead.

**Live-verified SQL Server facts**:
- `ALTER USER` on any of `dbo`/`guest`/`sys`/`INFORMATION_SCHEMA` fails
  with a clean, specific error (`"Cannot rename the user 'guest'."`,
  `"Cannot alter the user 'dbo'."`) — unlike `public`'s role-side syntax
  error, these are ordinary SQL errors, but the UX principle from
  `role_props.go`'s `isBuiltinRole` still applies: don't offer an edit
  that's guaranteed to fail. `user_props.go`'s `isSystemUser(name)` gates
  General's name/login/default-schema fields to read-only `Static`s for
  these four.
- Renaming a user, then having the *same* open dialog reload with the
  stale pre-rename name, hits the identical known/accepted gap already
  documented for roles and logins (Apply genuinely succeeds; the reload
  fails with "not found: Press F5 to retry" since `propPage` closures
  capture the name once at dialog-open time). Re-confirmed here, not
  fixed, consistent with precedent.

**Deferred, same reasoning as every prior pass**: contained/password
users (server-wide `contained database authentication` is `value_in_use
= 0` on ubudock — verified live that `ALTER DATABASE ... SET CONTAINMENT
= PARTIAL` fails outright; flipping that flag is a persistent shared-
infrastructure change out of scope), external Microsoft Entra users/
groups (no Azure AD auth on this on-prem test server; `UsersContext`'s
`type IN ('S','U','G')` filter doesn't even include `'E'`/`'X'` yet — a
real, separate gosmo gap, flagged for later), the "Orphaned User" fix-up
modal (its practically useful half — remapping a user to a different
login — ships as a plain `Select` + `SetLoginContext` on General instead,
without a modal or an "orphaned" diagnosis that turned out to actually be
makeable after all, per the bug above — worth reconsidering building the
dedicated modal in a future pass now that the diagnosis is known-reliable),
Add Securables/Column Permissions/Effective Permissions modals, WITH
GRANT OPTION, filter/search boxes (same set deferred on every pass).

Verified end-to-end against a disposable `claude_user_test` database with
two disposable server logins (`claude_test_login`, `claude_test_login2`)
and hand-built users (`claude_login_user` FOR LOGIN, `claude_nologin_user`
WITHOUT LOGIN) — every Apply round-trip (rename, default-schema change,
login remap all in one General Apply; schema-ownership transfer; role-
membership toggle via the new Membership page; a database-level
Securables grant; an extended-property add) verified via direct
`sys.database_principals`/`sys.database_role_members`/
`sys.database_permissions` re-query afterward. Database and logins
dropped at the end. See [[gossms-tui-tmux-testing]] for the tmux
mechanics (all still held up unchanged this pass).


---

## 2026-07-30 — Db connectivity stability review

`db-connectivity-stability-review-2026-07-24`

*Deep review+fix of gossms/gosmo DB connectivity stability: disconnect-doesn't-cancel-in-flight-loads (18 sites), gosmo retry-coverage gaps, plus 2 unrelated UI bugs; all live/race-verified*

Third review pass today (distinct from [[review-both-repos-2026-07-24]] and
[[object-explorer-login-and-connect-error-dialog-2026-07-24]]), explicitly
scoped by the user to "bugs, inconsistencies, optimizations, refactoring"
across both repos with a deep dive on **database connectivity stability**.
Planned first (EnterPlanMode), approved, then fully implemented in the same
session — see the approved plan for the full write-up; this memory is what's
non-obvious enough to be worth keeping after the plan file itself decays.

**Root cause found (the big one): disconnecting didn't cancel in-flight
background loads.** `db.ServerConn.Close()` only called `sc.Server.Close()`
(`(*sql.DB).Close()`), which per Go's `database/sql` semantics closes only
*idle* pooled connections immediately — a connection checked out by an
in-flight query stays open (live SQL Server session and all) until that
query naturally finishes/errors, since every background load built its own
`context.WithTimeout(context.Background(), ...)` completely independent of
the connection object. This was already known for one call site
(`completion_inventory.go`, found earlier today) but turned out to be the
same root cause at **24 call sites across 14 files** — essentially every
async load in `internal/tui`. Fixed by giving `db.ServerConn` a cancellable
`ctx`/`cancel` (created in `Connect`, exposed via `sc.Context()`, cancelled
in `Close()`), then re-rooting every one of those sites on `sc.Context()`
instead of `context.Background()`. Also extended to `PropDialog.ctx` itself
(rooted on `sc.Context()` now) and `App.startTask`'s `parent` param (backup/
restore Tasks, previously unbounded + not connection-scoped at all).

**Live-verified the fix's actual mechanism against real ubudock**, not just
unit tests: wrote a throwaway `internal/db/live_verify_test.go` (deleted
after the run, not committed — hardcoded creds, real network dependency)
that connects as a disposable login, starts a `WAITFOR DELAY '00:00:15'`
under `sc.Context()`, calls `Close()`, and confirms both the query returns
almost instantly (context cancellation reached the driver) and `DROP LOGIN`
from a second session succeeds within ~336ms — down from the previously
observed ~30s lingering-session bug. This is the way to verify this class of
fix in the future: simulate the slow query explicitly rather than trying to
time a race through the interactive TUI.

**gosmo-side findings, verified by direct code reading (not just agent
reports):**
- `Database.queryRow` had the same Scan-time retry gap `Server.queryRow`'s
  own doc comment already called out by name — it handed back a live
  `*sql.Row` from *inside* the retried closure, so `withRetry` saw `err ==
  nil` and returned before the failure that only surfaces at `Scan()` ever
  happened. Fixed by changing its signature to take a `scan func(*sql.Row)
  error` callback like `Server.queryRow`, and updating all ~19 call sites.
- **Caught mid-refactor: `Sequence.NextValueContext`'s `NEXT VALUE FOR` is
  not idempotent** — it advances server-side sequence state as a side
  effect of being read. Blindly applying the queryRow retry fix there would
  have made a transient failure *after* the value was consumed retry the
  whole query, silently skipping a value. Special-cased to use `withConn`
  instead (retries acquire+USE, never the query itself) — the exact same
  distinction `Database.exec`'s write path already relies on, just applied
  to a read that happens to mutate. Worth remembering as a category: not
  every `SELECT`-shaped call is actually a safe-to-retry read.
- 9 read call sites (`agent_alert.go`, `agent_job.go`×2, `agent_operator.go`
  ×2, `agent_schedule.go`×2, `database_options.go`, `change_tracking.go`,
  `login.go`) called `s.db.QueryContext`/`QueryRowContext` directly,
  bypassing `Server.query`/`queryRow` entirely — zero retry coverage, not
  just the Scan-time gap. Routed through the shared helpers.
- `ScriptCollector.Statements` (`script.go`) was an unsynchronized
  `[]string` — added a mutex-guarded `append` method. No confirmed
  exploitable path in gossms today (its own script-collection runs
  sequentially), but a latent hazard given `WithScript`'s own contract
  invites reuse of one collector across calls.

**Two unrelated UI bugs found+fixed+verified, same review pass:**
- `MenuBar`'s dropdown item click (`menu_bar.go`) fired on drag-*into* the
  item rather than release, missing the `mouseDragging` guard its own
  header-toggle branch two lines above already used — a natural
  press-header-drag-to-item gesture fired whichever item the cursor first
  crossed. Fixed with the same guard; tmux-verified interactively (raw SGR
  mouse press/motion/release sequences) that the drag-through no longer
  misfires and a normal separate click still does.
- `PropertySheet.SelectPage` (`propsheet/sheet.go`) never checked
  `p.applying`, so navigating to a not-yet-loaded page while an Apply/OK
  background goroutine was writing a shared rename `*string` (User/Login/
  Role/Server-Role Properties' General page) could start a second goroutine
  reading that same pointer with no synchronization — a real data race, not
  just staleness. Fixed at the one structural chokepoint: `SelectPage`
  refuses a new load while `applying`; `SetApplying(false)` retries the
  load if the still-selected page is still unloaded.

Also set `MaxOpenConns`/`MaxIdleConns` (20/10) in `db.Connect` — previously
unset (gosmo default: unlimited open, 2 idle), while `detail_browser_tables.
go`/`detail_browser_databases.go` deliberately fan out one dedicated
connection per row concurrently; uncapped, a big server could open hundreds
of raw connections on one folder expand, then tear nearly all back down
again on the next refresh.

**How to apply:** if a future session needs to check whether a background
load in `internal/tui` is properly tied to connection lifetime, the pattern
to look for is `context.WithTimeout(sc.Context(), ...)` — a
`context.WithTimeout(context.Background(), ...)` scoped to a `*db.ServerConn`
call is the bug class to flag. `os_clipboard.go`/`update_check.go` are the
only legitimate `context.Background()` holdouts (genuinely unrelated to SQL
connections). Full build/vet/test/`-race`/gofmt clean in both repos after
every change; nothing committed to git per explicit instruction.

**Same-day follow-up review found+fixed one real regression from the A1 fix
itself.** `completion_inventory.go`'s catalog/sys-catalog caches
(`App.completionInventories`/`sysCompletionInventories`) are keyed by
server+login(+database) and **deliberately shared** across every
`*db.ServerConn` that resolves to the same key — Object Explorer's connection
and every query panel's own dedicated connection routinely collide onto one
entry, and `ensureCompletionInventory`/`ensureSysCompletionInventory` only
start a fetch when no entry exists yet, so whichever `sc` wins that race owns
the fetch goroutine. Re-rooting that goroutine's context on `sc.Context()`
(the A1 fix) meant disconnecting *that one* `sc` while its fetch was still in
flight now caused the goroutine's resulting error to get cached under the
shared key — poisoning autocomplete for every *other*, perfectly healthy
connection sharing it, with no eviction on disconnect and no automatic
recovery short of a manual Ctrl+R. Fixed by checking `!sc.IsOpen()` before
caching the error and deleting the (now-stale) entry instead, so the next
lookup from any connection just retries fresh. Two-agent independent
full-diff review (one per repo) is what surfaced this — gosmo's diff came
back clean, gossms's agent traced the shared-cache doc comments and found
this. **Non-obvious catch:** the first fix attempt checked
`errors.Is(err, context.Canceled)`, which looked right but was wrong —
`sql.DB.Conn` checks its own `closed` flag *before* checking context
cancellation, so if `sc.Close()` (which closes the pool right after
cancelling the context) wins the race before the goroutine even acquires a
connection, the actual error is `"sql: database is closed"`, not
`context.Canceled`. Proved this by writing a throwaway live test against
ubudock that failed with exactly that error message under the
`errors.Is`-based fix, which is what motivated switching to `!sc.IsOpen()` —
checking the actual condition ("is the fetch's owning connection gone")
instead of pattern-matching one specific error shape. Confirmed the fix
fail→pass by temporarily disabling the guard, rerunning the live test (fail,
same "database is closed" message), then restoring it (pass) — the live test
was then deleted, not committed, per the same throwaway-test discipline as
A1's original verification. **General lesson for future `sc.Context()`
wiring:** before re-rooting a background load's context onto a specific
connection, check whether the resource it populates is scoped to that one
connection or shared across connections/keys — a shared resource needs an
`IsOpen()`-style guard before caching that connection's own teardown as if
it were the resource's own failure, not just a straight context swap.


---

## 2026-07-30 — Detail browser widevalue and tables

`detail-browser-widevalue-and-tables-2026-07-19`

*DataGrid gained SetFillLastColumn for wide Property/Value detail grids; Tables folder rebuilt with async per-table Row Count/Data/Index/Unused MB, Type column dropped; found an unrelated DECIMAL-renders-as-hex query-results bug*

Built 2026-07-19, same day as [[object-explorer-detail-async-2026-07-19]] (context: two
follow-up asks after that session — make Property/Value detail grids use the
full panel width, and give the Tables folder the same progressive-load
treatment the Databases folder already had).

**`controls.DataGrid.SetFillLastColumn(bool)`** (new, `internal/tuikit/controls/datagrid.go`):
stretches the *last* column past its content-based width and past
`maxCellWidthOrDefault`'s clamp to consume the rect's remaining width —
`growLastColumnToFill`, called from `computeColWidths` when the flag is set.
Off by default (a multi-column grid — query results, Databases folder,
Logins — would look wrong with one arbitrarily stretched column). `SetBounds`
now also calls `computeColWidths()` on every resize so a fillLastColumn
grid's Value column keeps matching the panel's current width instead of
freezing at whatever width was current when `SetData` last ran (previously
`SetBounds` didn't recompute widths at all — harmless before since widths
were purely content-driven, but this feature makes them width-driven too).

**`internal/tui/detail_browser.go`**: added `isPropertyValueColumns(cols)`
(`len(cols)==2 && cols[0]=="Property" && cols[1]=="Value"`) as the single
choke point deciding fill-on/off, called from `postPartial`, `applyResult`,
and the two inline `SetData` calls in `ShowNodeDetails` (nil-node, not-
connected) — every other loader (Databases/Logins/Tables folders, which are
list-shaped, not single-record) automatically gets `fillLastColumn=false`
with no per-loader changes needed, since it's driven by the columns each
call passes rather than a flag callers must remember to set/reset.

**Tables folder** (`internal/tui/detail_browser_tables.go`, new file):
columns are now `Name, Row Count, Data (MB), Index (MB), Unused (MB)` — the
`Type` column (always the hardcoded literal `"User Table"`, no real per-row
value) is gone. Mirrors `loadDatabasesFolderDetails`'s progressive pattern:
`Database.TablesContext` returns the fast list, `postPartial` shows Name
immediately with `"…"` placeholders, then one goroutine per table calls
`Table.RowCountContext` + `Table.SpaceUsedContext` concurrently and
`RefreshColumnWidths` backfills in place. **Both gosmo methods already
existed** (`RowCountContext` in `table.go`, `SpaceUsedContext` — returning
`*TableSpaceInfo{ReservedKB,DataKB,IndexKB,LOBKB,UnusedKB,FileGroup}`, KB not
MB — in `partition.go`, built for Table Properties' Storage page per
[[table-properties-mockup-build-2026-07]]) — no gosmo changes needed this
time; first instinct was to add a new `Table.SpaceUsed`, which collided
(`TableSpaceInfo`/`SpaceUsed` already declared) and was reverted. **Lesson:
grep gosmo for an existing method before assuming a needed capability is
missing — this codebase iterates fast enough that "add gosmo groundwork"
from an even earlier gap-fill can already cover a later ask.**

**Live-tested against [[gossms-live-test-server]] (ubudock) end to end**:
Server node and Database node Property/Value grids both genuinely fill the
panel width (verified at 220 and 260 terminal columns, including a live
resize while the Server node was showing); HealthClinic's Tables folder
showed real row counts (Medications 220, Patients 210, Doctors 10, etc.)
with Data/Index/Unused all correctly rounding to "0 MB" for these small demo
tables (~70KB actual size, confirmed via a manual `sys.allocation_units`
query) — not a bug, `formatMB` rounds to the nearest whole MB by design.
Caught the `"…"` placeholder mid-flight on RetailShop's Tables (all genuinely
0 rows, a schema-only demo db) confirming the progressive backfill, not just
its already-settled end state.

**Incidental, unrelated bug found while sanity-checking table sizes via a
manual query**: running `SELECT SUM(a.total_pages)*8.0/1024 AS mb, ... ` in
the query editor rendered the results grid cells as `0x302E303730333132`
(hex-encoded ASCII of the literal string `"0.070312"`) instead of a decimal
number — i.e. a T-SQL expression whose result type is DECIMAL/NUMERIC
(the `8.0` literal makes it one) comes back from the query executor's row
formatter as a raw byte dump, not a readable number. Not touched or
diagnosed further (unrelated to this session's task, scope not explored past
confirming it wasn't specific to this one query) — **worth a dedicated
follow-up session**: any user query with DECIMAL/NUMERIC arithmetic in its
result set likely renders unreadably today. Check `internal/query`'s value
formatter for how it type-switches (or fails to) on whatever type
`database/sql`'s `Scan` yields for a DECIMAL/NUMERIC column via the mssql
driver.


---

## 2026-07-30 — Estimated execution plan binding

`estimated-execution-plan-binding-2026-07`

*Wired Show Estimated Execution Plan toolbar button into QueryPanel; fixed a real planview scroll-into-view bug caught only via live testing*

Implemented 2026-07-16: the toolbar's "Show Estimated Execution Plan"
button (previously a no-op icon) now works end to end — this is the first
half of [[execution-plan-viewer-design-2026-07]]'s "future binding, out of
scope" section. Selection-or-full-text like Execute; fetches via gosmo's
pre-existing `Database.EstimatedPlanContext` (SET SHOWPLAN_XML ON,
compile-only — no gosmo changes needed, it already had both Estimated and
Actual variants); installs a `planview.PlanView` into a new "Execution
Plan" tab that replaces any Results tabs (only Execution Plan + Messages
while a plan is shown); reverts to Results/Messages on the next normal
Execute (mutual exclusion via `p.planView = nil` in `setResult` /
`p.result = nil` in `setEstimatedPlan`). Actual Execution Plan (the
second toolbar icon, `SET STATISTICS XML ON`, keeps Results tabs too) is
still unwired — deliberately out of scope for this pass.

**Found and fixed a real, pre-existing bug in `internal/tui/planview`
itself while live-testing this, not caught by any existing test or by
`cmd/plandemo`'s own prior manual checks:** `PlanView.selectFirstNode()`
sets `selectedID` to the plan's root node but never scrolled the graph
canvas or tree pane to bring it into view; `ensureTileVisible`/
`ensureTreeRowVisible` were only ever called from `selectNode` (the
user-driven-navigation path), never from the initial-load path. This was
invisible with small/shallow fixture plans (root always happens to land
near canvas origin `(0,0)`) — including `cmd/plandemo`'s own prior manual
verification — but a real multi-join plan (tested live against
`SELECT TOP 5 name, object_id FROM sys.tables ORDER BY name` at
[[gossms-live-test-server]], which expands into a 12+ node nested-loop
tree through system catalog views) centers its root tile well outside
the default scroll position, so the Plan tab rendered completely blank
with no error and no hint to scroll — the Tree tab looked fine only
because a tree's flat row-list always puts root at row 0 regardless of
subtree shape, so its own identical missing-scroll bug never manifested.
Fixed by adding `v.ensureTreeRowVisible()` / `v.ensureTileVisible(v.selectedID)`
to the end of `PlanView.layout()` itself (not `selectFirstNode`) — cheap
no-ops when the selection is already visible, and this is the one choke
point both the constructor-time `SetPlanXML`-before-first-`SetBounds`
sequence (used by both `cmd/plandemo` and `QueryPanel`) and every later
resize flow through, since `selectFirstNode`'s own call happens too early
(before the host's first real `SetBounds`, when `graphCanvasRect`/
`treePaneRect` are still zero and the ensure-visible calls silently
no-op).

**How to apply:** this is the second time in this project a `planview`
bug survived unit tests + `cmd/plandemo` and only surfaced through a real
multi-node plan from live SQL Server — see
[[indent-trailing-newline-bug-2026-07]] and
[[planview-tree-focus-model-bug-2026-07]] for the same pattern. When
extending `planview` further (the Actual Execution Plan binding is next),
test against a real, non-trivial live query plan before calling it done,
not just the small synthetic fixtures — small trees hide scroll/geometry
bugs that only a deep, unbalanced tree exposes.


---

## 2026-07-30 — Execute at cursor statement boundary

`execute-at-cursor-statement-boundary-2026-07-17`

*Reused IntelliSense's DML-keyword statement-boundary heuristic in Ctrl+Enter/Execute-at-Cursor's SelectStatementAtCursor; live-testing caught a real pre-existing segment-lookup boundary-ambiguity bug*

Same-day follow-up to [[intellisense-review-2026-07-17]]'s statement-boundary
fix. User asked to reuse that same DML-keyword boundary detection in
`Ctrl+Enter`/Query > Execute at Cursor's "select the statement at the
cursor" behaviour (`controls.Editor.SelectStatementAtCursor`,
`internal/tuikit/controls/sql_statement.go`), which previously only
recognised `;` and bare `GO` lines — the same documented gap
`dmlStatementStarts` closed for completion, but never applied here.

**Architecture constraint forced a port, not a shared call**: `tuikit` must
never import `tui` (CLAUDE.md's one-way dependency rule), so
`internal/tui/completion_provider.go`'s `dmlStatementStarts`/
`dmlStatementLeaders` couldn't be called directly from
`tuikit/controls/sql_statement.go`. Ported a minimal, self-contained version
instead — paren-depth tracking, a small `dmlBoundaryKeywords` set (just
SELECT/INSERT/UPDATE/DELETE/MERGE/WITH/VALUES/UNION/EXCEPT/INTERSECT/ALL,
not the tui side's full ~60-keyword table — sufficient because the
UNION-chain-continuation check only ever needs the *immediately preceding*
keyword to be one of those, and any other keyword between doesn't corrupt
that), same `pendingMainSelect`/`continuesUnion` state machine, reset at
every `;`/GO boundary. This duplication is consistent with existing
precedent already in the file (`statementStartOffset`'s own doc comment
already reimplements the `;`/GO rule in a second, simpler form for the same
architectural reason).

**Real bug found via live tmux testing (not introduced by this change, but
made far more likely to be hit by it)**: `sqlStatementAt`'s segment lookup
used inclusive comparisons on both ends of each `[sr,sc]-[er,ec]` span, so
adjacent segments share their boundary point (segment *i*'s end equals
segment *i+1*'s start) and a cursor sitting exactly there matched **both**
— the loop returned the *first* match (the trailing statement) instead of
the one the cursor is positioned at the *start* of. Nearly unreachable with
`;`/GO-only boundaries (a `;` split lands mid-line, a GO split lands on a
different row entirely), but DML-leader splits commonly land at column 0 of
a line — exactly where a user's cursor naturally sits (Home, or a mouse
click on the new statement's first line). Caught live: three semicolon-less
statements stacked, cursor at Home of the 2nd line — Execute at Cursor kept
selecting statement 1 instead. Fixed by taking the *last* matching segment,
not the first (`sqlStatementAt`'s final loop) — correct because a cursor
sitting on a shared boundary should resolve to what it's about to type
into, not what it trails.

**Live-testing note**: Ctrl+Enter itself isn't distinguishable from plain
Enter in this terminal/tmux stack (same class of gap as
[[gossms-user-terminal-env]]'s other Ctrl-key gaps) — sending it inserted a
newline instead of selecting. Verification went through Query menu >
"Execute at Cursor" instead (F10 → Right×3 → Down×2 → Enter), which calls
the identical `SelectStatementAtCursor()`, so it's an equally valid path
and doesn't need a real terminal with the Kitty/modern keyboard protocol.

**How to apply**: any future change to either boundary heuristic
(`dmlStatementStarts` in tui, or its port here) must be mirrored in the
other by hand — there's no shared source, this is intentional per the
architecture, not an oversight. If `SelectStatementAtCursor` ever
misselects again, check the segment-lookup's first-vs-last-match direction
before assuming the boundary keyword logic itself is wrong — an off-by-one
in that area is the harder-to-spot half of this kind of bug, and the
symptom (wrong *adjacent* statement selected, right statement count) looks
identical either way. Also note: Execute at Cursor is a **two-step** feature
by design (pre-existing, not touched this session) — Ctrl+Enter/the menu
item only selects; F5/Execute then runs whatever is selected (or the whole
script if nothing is).


---

## 2026-07-30 — Execution plan viewer design

`execution-plan-viewer-design-2026-07`

*Execution-plan viewer control — implemented (2026-07-16) per todo/plan/planview-architecture.md; internal/showplan parser + internal/tui/planview control, 3 tabs, all phases green; splitter-click bug fixed, Operator Details redesign, Plan tab strip made resizeable/summary-free/visible-by-default, ❌/⚠/⇄ status icons added, all same day*

Execution-plan viewer feature: architecture agreed with the user on
2026-07-15, **implemented 2026-07-16** across 5 phases, all green
(`gofmt`/`go vet`/`go build`/`go test ./...`, 40 tests). Design doc at
`todo/plan/planview-architecture.md` was kept in sync with the actual
implementation, including the deviations below. **Still out of scope:**
binding into QueryPanel/menus/plan generation — the control exists but
nothing in the app wires it in yet.

What shipped: `internal/showplan` (pure ShowPlanXML parser+model, UTF-16
via stdlib `encoding/binary`+`unicode/utf16` — not `x/text` as originally
planned, turned out unnecessary) and `internal/tui/planview` (the
control — subpackage of `internal/tui`, compiler-enforced to never import
`App`). **Three tabs**: Plan (SSMS-style graph, root-left, default), Tree
(expand/collapse + operator-details pane + Properties/Summary bottom
section), XML (read-only `controls.Editor`). Cross-cutting: `/`+`n`/`N`
operator search, `w` warning-jump, `p` estimated/actual toggle — all
shared between Plan and Tree tabs via `PlanView.selectedID`.
`cmd/plandemo` is a permanent dev harness (`go run ./cmd/plandemo
<file.sqlplan>`) for eyeballing any plan file without the full app.

Deviations from the original design (all noted in the doc itself):
`OnCopyRequest` callback dropped (App intercepts Ctrl+C globally before
any panel sees it — the real integration is `HasSelection`/
`SelectedText`/`Cut`/`Paste`/`SelectAll`, matching `DetailBrowser`'s
precedent); `SetIconSet`/`IconSet` dropped (no operator icons — text-only,
nothing needed the glyph-set abstraction); `style.go`/`icons.go` never
created (color logic stayed inline in tree.go/graph.go, too small to
extract).

Three real bugs caught during implementation (not designed around —
found live):
1. [[indent-trailing-newline-bug-2026-07]] — `Indent`'s "already
   multi-line" check matched on the input's own trailing EOF newline
   alone, silently no-op'ing on any genuinely single-line plan XML. Only
   caught by actually running `cmd/plandemo` in tmux, not by unit tests
   (which were too weak — passed even on the no-op'd output).
2. [[planview-tree-focus-model-bug-2026-07]] — Enter was unconditionally
   intercepted by the tree's own expand/collapse handler, so it never
   reached the Summary table's "jump to operator" action. Caught by a
   state-level test asserting the actual outcome (`selectedID` changed),
   not just "HandleKey returned true". Fixed with an explicit
   `bottomFocused` field + Tab to toggle.
3. Graph tile height: mockup said "4–5 rows"; 4 wasn't enough for 3
   interior text lines (`Rect.Inner(1)` only gives 2 interior rows at
   H=4), so the third line silently overwrote the bottom border. Fixed
   by using 5 (`graphTileH` in graph_layout.go). Caught visually via
   `cmd/plandemo`, not tests (pure-layout tests only check geometry, not
   rendered text collisions).

Pattern across all three: state-level tests catch behavioral/outcome
bugs; visual tmux inspection of `cmd/plandemo` catches rendering bugs
tests can't express. Neither alone would have caught all three — do
both before calling any UI-producing phase done.

**Post-ship fixes (2026-07-16), from user-reported issues in
`todo/todo.txt`** (a real live bug + a mockup-fidelity gap, not new
scope): (1) the tree|details `layout.Splitter`'s `dragging` flag got
stuck `true` forever after any drag, because `handleTreeTabMouse`'s
`switch ev.Buttons()` had no `ButtonNone` case — the release event never
reached `treeSplit.HandleMouse`, so the next plain click anywhere in the
tab kept moving the divider instead of selecting a tree row. Fixed by
adding a `ButtonNone` case that forwards to the splitter first, matching
the pattern `App`/`QueryPanel` already used elsewhere. Caught by a new
state-level regression test (`TestSplitter_ClickAfterDragSelectsTree`),
not tmux — reinforces the same lesson as the three bugs above. (2) The
user pasted the original mockup's "Properties"-styled panel and said
"make the right panel too look like this" — "the right panel" meant the
Operator Details pane (right of the splitter; the architecture doc
itself calls it that), not the bottom Properties/Summary section, which
already existed under a different name. Reformatted it: a one-row
header ("Operator Details" + right-aligned "Scroll ▲/▼", shown only on
overflow), a curated/expanded field list (`detailKVs` in details.go —
was previously a much shorter ad hoc list), every label padded to the
widest one (`core.PadRight`) so `:` separators line up in a column
(matching the mockup exactly), plus mouse-wheel scrolling
(`detailsScroll`). Ambiguity was resolved by re-reading the doc's own
mockup snippet, which explicitly labels that pane "Right of a
layout.Splitter" — worth checking the architecture doc's own wording
before asking the user, when in doubt about "which panel."

**Plan tab detail strip redesign (2026-07-16, same session, follow-up
ask):** user pasted a second `todo/todo.txt` mockup — "Plan view: split
the pane horizontally 70/30 and add the properties for the selected
node as [Properties+Operator Summary mockup]" — and confirmed via a
one-line reply ("it is the plan tab") that this targeted the Plan
(graph) tab's existing toggleable "Selected Operator" strip, not the
Tree tab just redone. Replaced the old single-view strip
(`graphStripHeight`, capped ~12 rows, `drawDetails` only) with a fixed
70/30 canvas/strip split (`graphCanvasRatio`) further split 65/35
(`graphStripPropsRatio`) into Properties (reusing the Tree tab's just-
built `detailLines`/`drawDetailsHeader`, retitled "Properties") over
Operator Summary (the same shared `summarySt` grid the Tree tab uses).
Required generalizing two Tree-tab-only helpers for reuse:
`drawDetailsHeader`/`detailsHeaderText` gained a `title` param, and
`drawSummary` an explicit `rect` param instead of always reading
`v.bottomRect`. `bottomFocused` (Tab-to-focus-the-summary-table) is now
shared control-level state across both tabs, not Tree-tab-specific,
since both point at the same `summarySt.grid`. No new bugs found this
round — straightforward reuse of infrastructure built minutes earlier
in the same session made it low-risk.

**Plan tab strip revised again (2026-07-16, same session, second
follow-up):** two more `todo/todo.txt` asks, the second delivered as a
genuine mid-turn message while the first was still in progress: (1)
"make the panel resizeable, remove operator summary and move the
information in the properties" and (2) "also make the properties panel
visible from begining". Changes: the fixed 70/30 ratio became a
draggable `graphSplit` (`layout.NewHorizontalSplitter`, default ratio
0.7 — the same widget/pattern as `treeSplit`, including forwarding
`ButtonNone` unconditionally so a drag can't get stuck, matching the
splitter-click bugfix above); the Operator Summary grid was removed from
the Plan tab entirely (it's Tree-tab-only again); the one field it
carried that Properties didn't already have — **Cost %** — was folded
directly into the shared `detailKVs`, so it now shows on both tabs
instead of a second grid duplicating Rows/Time/Operator/Object/Status
under different names; and `graphSt.detailOpen` now defaults `true`
(Enter still toggles it, just starts open). Also dropped
`summaryHeaderStyleAndText`'s now-unused `cycleHint` bool param once the
Plan tab was no longer a second caller — a direct, in-scope cleanup of a
parameter that became dead as a consequence of the ask, not an unrelated
refactor. No new bugs found; verified via the full test suite (45 tests
in `planview`, including a new mouse-drag resize test and a
visible-from-start test) plus a `cmd/plandemo` tmux session confirming
the strip renders open on load with Cost % present and no Operator
Summary block.

**Status/parallelism icons (2026-07-16, same session, third follow-up):**
user asked for three specific icons "in the plan and tree" — warning
(yellow triangle, already ⚠/`pal.Warning`, unchanged), ❌ U+274C for
error, ⇄ U+21C4 for parallelism. Mapped "error" onto the existing
"expensive operator" condition (cost ≥ 30%) that already drove red
border/text color but had no glyph of its own, and "parallelism" onto
`n.Parallel`, which previously had no visual indicator at all outside
the Properties text field. Implemented in both `graph.go`'s `drawTile`
(❌/⚠ as a single corner badge, same priority as the color switch;
right-aligned by `core.DisplayWidth` of the glyph itself — ❌ is
double-width, ⚠ isn't, so a fixed 1-column offset would have pushed ❌
into the tile's border and corrupted it, caught by checking
`displaywidth.String` on both glyphs before wiring it up, not by trial
and error) and `tree.go`'s `treeRowText` (both icons appended inline,
matching how the single ⚠ marker already worked there). Deliberately
left `summary.go`'s Status column and `details.go`'s Properties text
untouched — the ask was scoped to "the plan and tree" (the two
visualization tabs' node renderings), not every place a warning is
surfaced. Verified via 3 new state-level tests in `tree_test.go`
(`TestTreeRowText_ErrorBadgeTakesPriorityOverWarning`,
`_WarningBadgeWhenNotExpensive`, `_ParallelIcon` — built with synthetic
`showplan.Node` literals since neither test fixture has a node that's
both parallel and warned, or cost ≥ 30% without also being warned) plus
a `cmd/plandemo` tmux run against `internal/showplan/testdata/
estimated_plan.xml`, whose one non-root node is ~98% cost *and* carries
a `ColumnsWithNoStatistics` warning — confirming ❌ correctly wins over
⚠ in a real rendered tile and tree row, not just in the synthetic test.

Related: [[gossms-tui-tmux-testing]], [[gossms-keyboard-conventions]],
[[model-switch-after-planning]].


---

## 2026-07-30 — Expand plan panel

`expand-plan-panel-2026-07`

*Built the Execution Plan tab's [ Expand ] button that pops the plan into its own closable top-level panel, reusing the existing App.panels/layout.PanelManager window system*

2026-07-16, same day as [[actual-estimated-plan-toggle-2026-07]] and
[[estimated-execution-plan-binding-2026-07]]. User request (`todo/
todo.txt`): add an `[ Expand ]` button to the Execution Plan tab (Estimated
or Actual — both already shipped) that opens the same plan, via the same
`planview.PlanView` control, in its own detached top-level window — "like
the Object Explorer Detail or Query window" — appearing in the app's
window list and closable via `[x]`. Each query panel's expand is its own
independent panel, never shared/reused across query panels.

**Key discovery that made this cheap:** `internal/tui/planview/planview.go`
already had a half-wired, never-connected hook from an earlier session —
`OnOpenInPanel func()` and `openBtnRect`, drawing a right-aligned button in
`PlanView`'s own tab bar and already routing clicks to it in `HandleMouse`
— plus `SetPlan(p *showplan.Plan)`, whose doc comment literally said it
existed "to avoid re-parsing (used by 'Open in Panel' to hand the same
`*showplan.Plan` to a freshly created PlanView)". Nothing in `internal/tui`
ever set the callback, so the button had never actually appeared. Since
`planview.go` was still uncommitted, the whole hook was renamed
`OnOpenInPanel`→`OnExpand`, `openBtnRect`→`expandBtnRect`, and the button
label `"[Open in Panel]"`→`"[ Expand ]"` (the user's exact requested text)
at zero cost — no compatibility concern, nothing else referenced the old
names outside a couple of doc comments and one test name (also renamed).

**The app's panel system turned out to already be exactly the "window
list" the user described** — no new UI needed. `internal/tuikit/layout`
has a generic `Panel` interface (`SetBounds`/`Draw`/`HandleKey`/
`HandleMouse`/`Title`) plus optional `Activatable`/`Dirty`/`Closable`, and
`PanelManager` (`App.panels`) already draws a tab strip with a per-tab
`[x]` close glyph (`tabCloseGlyph`) and a `[v]` combo dropdown listing
every open panel. Every existing "window" — Query panels (`newQueryPanel`,
always adds a new one), the singleton Object Explorer Details
(`showObjectExplorerDetails`, `FindIndex`-reuses one) — goes through
`a.panels.AddPanel`/`RemovePanel`. `internal/tui/detail_browser.go`'s
`DetailBrowser` was the exact template to copy: a thin Panel wrapper that
draws its own one-row title bar and forwards everything else (including
the clipboard-target methods `HasSelection`/`SelectedText`/`Cut`/`Paste`/
`SelectAll`) to the wrapped control. The new `PlanPanel`
(`internal/tui/plan_panel.go`) is that same shape wrapping a
`*planview.PlanView` instead of a `*controls.DataGrid`.

**Design choice confirmed via AskUserQuestion, not assumed:** every
`[ Expand ]` click creates a brand-new panel, even repeated clicks from the
same query panel — no per-origin reuse/reactivate tracking. This matches
`newQueryPanel`'s own behavior (Ctrl+N always adds a new panel) and kept
the implementation simple (no bookkeeping of "does this query panel
already have an expanded panel open"). Verified live: clicking Expand
twice from "Query 1" produced two independent "Execution Plan — Query 1"
tabs; closing one via `[x]` left the other untouched and returned focus to
it, with no confirm-dialog prompt (the panel is never dirty, and
`requestClosePanel`'s existing dirty-check branch only applies to
`*QueryPanel`, so `PlanPanel` — which implements no `Closable` override —
falls straight through to `closePanelAt`, already correct with zero new
logic there).

**Wiring is centralized in one place, not duplicated:** both
`setResultPlan` (Actual mode, `query_panel.go`) and `setEstimatedPlan`
(Estimated mode, `query_panel_plan.go`) used to independently do
`p.planView = planview.New(); p.planView.OnStatus = ...`. Factored into
one `QueryPanel.newPlanView()` helper that also sets `OnExpand` (calling
`p.app.openPlanPanel("Execution Plan — "+p.Title(), plan)` off
`v.Plan()`), so both plan modes got the button for free from one change.
The detached `PlanPanel`'s own inner `PlanView` deliberately leaves
`OnExpand` nil — no "expand the already-expanded" recursion, verified live
(no button visible on the detached panel).

**Scope cuts:** no menu item or keyboard shortcut, just the button (all
that was asked); `Tools > Query List` stays `*QueryPanel`-only, unchanged
— `PlanPanel` shows up in the generic `PanelManager` tab strip/combo
instead, same as Object Explorer Details also isn't in Query List.

**Live-verified end-to-end against ubudock** (see
[[gossms-live-test-server]], [[gossms-tui-tmux-testing]]): `[ Expand ]`
button visible and right-aligned in the Estimated Execution Plan tab's own
tab bar; click opens a new active panel titled "Execution Plan — Query 1"
showing the full graphical plan; a second click from the same query panel
adds a genuinely independent second panel (confirmed via git status/panel
count reasoning, not just visually); closing one via `[x]` is instant, no
prompt, leaves the sibling panel and the original embedded tab intact.
All new/renamed unit tests pass; `gofmt`/`vet`/build clean across the
whole tree.


---

## 2026-07-30 — Explorer drag drop

`explorer-drag-drop-2026-07-16`

*Object Explorer → query editor drag-and-drop, built from scratch 2026-07-16; cross-widget drag idiom, quoting classification, live-tested end-to-end*

Built mouse drag-and-drop from Object Explorer into the active query editor's SQL text: press-drag a database or a non-folder database object (table, view, column, etc.) and release inside a `QueryPanel`'s editor to insert its quoted T-SQL identifier at the drop point. User's spec, verbatim: "db name, db objects, no folders. use quotes."

**Why:** requested as a new feature, not a bug fix — matches real SSMS drag-and-drop behavior.

**Files:** new `internal/tui/explorer_drag.go` (`isDraggableNode`, `explorerDragText`, `App.dropExplorerNode`); `App.dragNode *explorerNode` field added to `app.go`; drag interception block added to `App.handleMouse` in `app_events.go`; two small additions to `controls.Editor` — `Bounds() core.Rect` (editor.go) and `SetCursorFromScreen(x, y int)` (editor_input.go), both new public API, needed because nothing in tuikit previously let an outside caller hit-test or reposition the cursor by screen coordinate without synthesizing a fake mouse event.

**Cross-widget drag idiom (the hard part):** `TreeView`/`Editor`/`DataGrid` all only expose `HandleMouse` per-event, routed by rect at the `App.handleMouse` level — a widget normally only sees events that land inside its own rect. A drag that starts in Object Explorer and ends in the query panel breaks that assumption. Reused the *exact* idiom the `Splitter` already uses for the same problem (`app_events.go`'s unconditional `a.explorerSplit.HandleMouse(ev)` call before rect-based routing): `a.dragNode` is armed on a Button1 press over a draggable node (checked *after* the normal `a.explorer.HandleMouse(ev)` call, via `a.explorer.Selected()`), then checked at the *top* of `handleMouse` on every subsequent event — Button1-while-armed swallows the motion instead of forwarding it to whichever widget's rect it's over (so the editor doesn't start its own text-selection drag), and a `ButtonNone` release calls `dropExplorerNode` regardless of which rect it's in. Never needed an actual intermediate motion event in testing — arming happens on the initial press, so a raw press-then-release pasted as one SGR sequence is enough to drive the whole gesture headlessly.

**Quoting classification** (`explorerDragText`): only `NodeTable/View/StoredProcedure/Function/Sequence/Synonym/Trigger` get the two-part `fqn(schema, name)` form — these are the SQL Server object types genuinely addressable as `schema.name` in real T-SQL (confirmed triggers belong here too: `DROP TRIGGER schema.name` is valid syntax, same schema as their owning table always). Everything else — `NodeDatabase`, `NodeSchema`, and every table-nested detail type (`NodeColumn/Key/Index/Statistic/ForeignKey/Check`) — gets a bare single-part `fqn("", name)`. The detail types are the subtle case: their `nodeData.Schema` field holds the *owning table's* schema (propagated down by `loadTableChildren`), not a schema this object itself is addressed by — qualifying a dropped column as `[schema].[colname]` would be both misleading (reads as schema.column) and not valid SQL on its own. `isDraggableNode` excludes folders (`isContainerNode`), `NodeServer` (no name of its own), and the synthetic `NodeLoading`/`NodeError` placeholder rows.

**Live-tested end-to-end** against the real ubudock SQL Server (see [[gossms-live-test-server]]) via tmux + raw SGR mouse paste sequences (see [[gossms-tui-tmux-testing]]) — confirmed: table drop → `[dbo].[Customers]`; column drop → bare `[FirstName]` (not schema-qualified); database-node drop → `[GoSMODemo]`; dragging a folder node is correctly inert (never arms, falls through to ordinary click-select); dropping back onto Object Explorer itself silently cancels (no status spam, matches "release without moving" UX); dropping into a non-`QueryPanel` panel (e.g. Object Explorer Details) shows "Drop target must be a query window" — matches the [[context-gated-actions-rule]] convention of guard + `setStatus` feedback rather than a silent no-op. No bugs found this time — worked correctly on first live test after unit build/vet/test passed clean.

**How to apply:** if a future feature needs another cross-widget mouse gesture (anything spanning two independently-`HandleMouse`'d widgets), reuse this exact "arm a field, check it first on every subsequent event, unconditionally handle release" idiom rather than inventing a new one — it's now used identically by `Splitter` drag and this feature.


---

## 2026-07-30 — File dialog build

`file-dialog-build-2026-07`

*FileDialog (Open/Save file picker) built from scratch in tuikit/dialogs, replacing PathPromptDialog everywhere*

Built `internal/tuikit/dialogs/file_dialog.go` — a reusable Open/Save file
picker (path bar, Name/Size/Modified directory listing, filename field,
shell-style Tab completion, type-ahead jump-to-entry) — and used it to
completely replace `internal/tui/path_prompt_dialog.go` (deleted), which
had been a bare free-text path prompt. One `*dialogs.FileDialog` instance
on `App` (`a.fileDialog`) now backs File > Open, File > Save/Save As,
Query > Results To File, and Object Explorer's Back Up Database — every
call site `PathPromptDialog.Prompt` used to serve.

**Why FileDialog lives in `tuikit/dialogs`, not `internal/tui`:** every other
concrete dialog with no SQL Server domain knowledge (`ConnectDialog`,
`QueryListDialog`, the old `PathPromptDialog`) still lives in `internal/tui`
per that package's own established convention. FileDialog is the first
exception — it touches only the local filesystem (`os`/`path/filepath`),
never gosmo/goSSMS, and `tuikit/README.md` explicitly frames the library as
vendorable into any tcell app, so a generic file picker fits there like
`ConfirmDialog`/`AlertDialog` already do. Uses an `OnConfirmOverwrite(path,
proceed)` callback (Save mode only) so the host app supplies its own
confirmation UI (wired to `a.confirmDialog` in `internal/tui/app.go`)
without FileDialog needing any upward knowledge — the existing dialog-stack
(`dialog_stack.go`) handles the resulting nested-dialog stacking (confirm
popup on top of FileDialog) automatically, no special-casing needed.

**Real bug caught live-testing (tmux, see [[gossms-tui-tmux-testing]]):** the
Modified column's last 1-2 characters (minutes digits) were being
overwritten by the list's own scrollbar thumb/track, because the column
math filled the full content width right up to the scrollbar's column.
Fixed by reserving one gutter column (`nameColWidth(contentW - 1)`) so
there's always a 1-column gap before the scrollbar, whether or not it's
actually drawn that frame.

Tab completion (`completeField`) and type-ahead jump (`typeaheadJump`) are
new patterns — no prior tuikit control had either. Both return/no-op
cleanly when there's nothing to complete/match, letting Tab fall through to
ordinary focus-cycling per [[gossms-keyboard-conventions]].


---

## 2026-07-30 — Fk props build

`fk-props-build-2026-07`

*Foreign Key Properties dialog — mockup generated first (none existed), then built read-only, single-page; no bugs found live-testing*

Built Foreign Key Properties (`internal/tui/fk_props.go`) for `NodeForeignKey`
(the `🔗` entries in a table's Keys folder, alongside [[key-props-build-2026-07]]'s
`NodeKey` PK/UNIQUE entries — the two node types share a folder but have
always had separate dialogs in this codebase, matching real SSMS giving
foreign keys a completely different multi-relationship editor). User asked
for a mockup first this time (no source mockup existed, unlike every prior
Properties dialog build this month) — wrote
`todo/mockups/foreign_key_properties_tui_mockup.txt` matching the established
mockup style (`properties.md`'s box-drawing/legend conventions) before
writing any Go code, then implemented exactly what it showed.

**Why:** user request was explicit and two-part: "generate a mockup first"
then build from it, "read-only", "show the FK definition, what tables and
columns are involved."

**How to apply / what's non-obvious for next time:**

- **This is the first strictly single-page, fully read-only Properties
  dialog in the app** (`fkPropPages` returns one page, "General", with
  `apply == nil`). Every prior page-level "read-only" case (Index
  Properties' Filter/Storage, Statistics' Columns, etc.) still lived
  inside a dialog where *other* pages were editable. Confirmed via mouse
  click (see below) that Apply/OK are harmless no-ops and Script Changes
  correctly reports `"No changes to script."` when nothing in the whole
  dialog is ever dirty — no special-casing was needed in `PropDialog`
  itself, the existing `dirtyApplyFns()`/`runPipeline` machinery already
  handles an all-read-only page set correctly.
- **No new gosmo code needed** — `gosmo.Table.ForeignKeysContext` already
  had every field the mockup asked for (`Columns`/`ReferencedColumns`
  pairs, `ReferencedSchema`/`ReferencedTable`, `DeleteAction`/
  `UpdateAction`, `IsDisabled`, `IsNotForReplication`). Added
  `findForeignKey` to `fk_props.go` (list + match by name, same shape as
  `findIndex`/`findStatistic` — no `ForeignKeyByName` API exists) but
  nothing to gosmo itself.
- `nodeData.TableName` wasn't set for `NodeForeignKey` nodes before this
  session (`loadKeysChildren` only set it for the `NodeKey` loop, added in
  [[key-props-build-2026-07]], not the FK loop below it) — fixed in the
  same `explorer_objects.go` edit.
- Live-tested against ubudock's `FK_Doctors_Specializations`
  (`HealthClinic.dbo.Doctors` → `dbo.Specializations` on
  `SpecializationID`) — every field matched real data on the first load:
  Name, Enabled=True, Not for replication=False, both table names, the
  one-row column-mapping grid, On delete/On update=NO_ACTION. Also
  verified the read-only claim for real: clicked Apply (mouse SGR click,
  same technique as [[key-props-build-2026-07]]) — no error, dialog stayed
  open, nothing sent to the server; clicked Script Changes — got "No
  changes to script."; Escape closed cleanly. No bugs found.
- Composite-key and disabled-FK cases in the mockup (`FK_OrderItems_Orders`
  with two column pairs, `FK_Appointments_Doctors` disabled +
  `WITH NOCHECK` note) are illustrative only — no such objects exist on
  ubudock, not live-verified. The single-column, enabled case
  (`FK_Doctors_Specializations`) is the only one actually round-tripped
  against a real server; the multi-column column-mapping grid and the
  conditional disabled-state Note row are trusted from code reading
  (`fk.Columns[i]`/`fk.ReferencedColumns[i]` pairing, `if fk.IsDisabled`
  branch in `pageForeignKeyGeneral`) rather than live observation — worth
  a real check against a disabled or composite FK if one shows up on
  ubudock later.


---

## 2026-07-30 — Four fixes

`four-fixes-2026-07-15`

*Four independent fixes/features in one pass (2026-07-15): dropdown-closes-on-mouse-move bug, Databases-folder auto-refresh after New Database, editable Recovery Model/Owner on Database Properties General, new Take Offline/Bring Online action + icon*

Four unrelated user-reported items done in one pass, all live-verified against
ubudock (see [[gossms-live-test-server]]):

**1. Combo box (DropDown) closed on mere mouse move, not just on click
elsewhere.** Root cause was in `propsheet.Form.HandleMouse`
(`internal/tuikit/propsheet/form.go`), not `DropDown` itself:
`s.EnableMouse()` (`internal/tuikit/core/screen.go`) defaults to
`MouseMotionEvents|MouseDragEvents|MouseButtonEvents` in tcell v3 (confirmed
in the module cache), so tcell delivers a continuous stream of
`EventMouse` with `Buttons() == ButtonNone` as the mouse moves, no click
needed. `Form.HandleMouse`'s band-matching fallback (added by the earlier
[[propsheet-form-wheel-scroll-fix-2026-07]] pass) ran unconditionally for
*any* mouse event once the focused row (the open `DropDown`) declined it —
and `DropDown`'s own overlay list is drawn extending below its row, over
the *next* rows' static bands. So merely moving the mouse down toward an
open dropdown's list (to click an item) crossed into a lower row's band,
which is `Focusable()`, so `Form` called `setFocus` on it — blurring the
dropdown, whose `Focus(false)` sets `open = false`. Fixed by returning
`false` immediately from `Form.HandleMouse` when `ev.Buttons() ==
tcell.ButtonNone`, right after the focused-row first-refusal check —
motion events no longer fall through to the click-routing/focus-shifting
logic. A real click (`Button1`) still selects correctly. Verified via tmux
raw SGR motion sequences (`\033[<35;COL;ROWM`, code 35 = motion + no
button) against the New Database dialog's Owner dropdown: list stayed
open across multiple hover moves, and a genuine click still selected an
item.

**2. Object Explorer's "Databases" folder didn't show a newly-created
database until manually refreshed.** Added
`ObjectExplorer.RefreshDatabasesFolder(sc)` (`internal/tui/object_explorer.go`)
— finds `sc`'s root, then its `NodeDatabases` child (if the folder was
ever loaded; a never-expanded folder needs no action, its first expand
fetches fresh anyway), clears its `Loaded`/`children` cache, and reloads
immediately if currently expanded. Called from
`NewDatabaseDialog.runApply`'s `onSuccess` closure
(`internal/tui/new_database_dialog.go`) right after `setStatus`. Verified:
creating a database while the Databases folder was already expanded made
it appear in the tree immediately, no F5 needed.

**3. Database Properties' General page had Recovery Model (and Owner)
hardcoded `Static` (read-only) despite gosmo already supporting
`SetRecoveryModelContext`/`SetOwnerContext`** (both used already by
[[new-database-dialog-build-2026-07]]'s General-page equivalent). Changed
both to `propsheet.Select` rows in `pageDatabaseGeneral`
(`internal/tui/database_props.go`) — Owner over a freshly-fetched
`sc.Server.LoginsContext` list (General previously fetched no login list
at all), Recovery model over SIMPLE/FULL/BULK_LOGGED — and added a
`propApply` closure (General previously returned a `nil` apply, since it
was pure info) that applies each only if `.Dirty()`. Compatibility
level/Page verify/Auto close/Auto shrink etc. were deliberately **left
Static** on General — they're already editable on the Options page
(`pageDatabaseOptions`), and duplicating an edit control in two places on
different `Form` instances (different load timing, different `Dirty()`
baselines, ambiguous apply order between pages) would be a real
regression risk, not a value-add; only Recovery Model and Owner had no
edit surface anywhere in Database Properties before this. Verified: set
Owner to a disposable login and Recovery Model to SIMPLE via the General
page, clicked OK, confirmed via `sys.databases`/`SUSER_SNAME(owner_sid)`
that both landed for real.

**4. New "Take Database Offline"/"Bring Database Online" context menu
action, with an icon change for offline databases** — didn't exist in
gosmo at all (no `SET OFFLINE`/`SET ONLINE` method on `Database`), so
added per CLAUDE.md's "change gosmo, don't work around it": `SetOffline`/
`SetOfflineContext` (`ALTER DATABASE ... SET OFFLINE WITH ROLLBACK
IMMEDIATE`) and `SetOnline`/`SetOnlineContext` (`... SET ONLINE`) in
`database.go`, following the exact pattern of the adjacent
`SetRecoveryModelContext`/`SetUserAccessContext` (a bare
`d.server.execContext` call, not `d.exec`/`withConn` — ALTER DATABASE SET
OFFLINE must not run from a connection scoped inside the database being
taken offline). No new unit test, matching every other sibling method in
that section (`SetReadOnlyContext`/`SetUserAccessContext`), which are
also untested SQL-string-wise. gossms side: added `nodeData.IsOffline
bool` (`internal/tui/tree_node.go`), populated in
`loadDatabasesChildren`/`loadSystemDatabasesChildren`
(`internal/tui/explorer_databases.go`) from `d.State() != "ONLINE"`
following the exact `n.data.IsPrimaryKey = ...` precedent already used
for columns; `nodeIcon` gained an `offlineDatabaseIcon` override (hollow
`⬡` vs. the online `⬢` in the geometric styles, `📴` for Emoji) alongside
the existing primary-key-icon override, so the icon swap needed **zero**
changes to the generic `tuikit` layer (`controls.TreeNode`/`TreeView`
already just render whatever rune `nodeIcon` computed at `rebuild()`
time — confirmed `Icon` is baked in at rebuild, not recomputed per frame,
so toggling `IsOffline` requires an explicit `oe.rebuild()` call, not just
a data mutation). Added `App.toggleDatabaseOffline(sc, node)`
(`internal/tui/app_explorer_data.go`) — confirms first (via the existing
`confirmDialog`) only when going offline (destructive: rolls back every
existing connection), not when coming back online; runs for real
immediately (unlike `backupDatabase`, which only generates a script) using
the lightweight `sc.Server.Database(dbName)` no-IO handle (no
`DatabaseByNameContext` read needed first — same
established-this-project reasoning as
[[new-database-dialog-build-2026-07]]'s `Server.Database` fix, since
`SetOfflineContext`/`SetOnlineContext` only touch `name`/`server`); on
success mutates `node.data.IsOffline` directly and calls
`a.explorer.rebuild()` (same direct-rebuild-call precedent as
`options_dialog.go:232`) rather than a full folder reload, since nothing
was added/removed/renamed. Menu label flips between "Take Database
Offline"/"Bring Database Online" based on current `node.data.IsOffline`.
Deliberately did **not** add any client-side restriction for
master/tempdb/model/msdb — SQL Server's own rejection of `ALTER DATABASE
master SET OFFLINE` surfaces through the same `setStatus` error path,
consistent with this project's general "let the server be the authority,
don't duplicate its validation" pattern. Verified end-to-end on a
disposable `claude_offline_test` database: took it offline (menu label
correct, confirm dialog appeared, icon changed to 📴, status bar
message, confirmed `OFFLINE` via direct `sys.databases` query), brought it
back online (label flipped back, icon reverted to 🛢, confirmed `ONLINE`
via query).

**Cleanup gotcha repeated from [[new-database-dialog-build-2026-07]]**:
dropping the disposable test database afterward hit the same Msg 3702
("currently in use") because gossms itself still held a session against
it — needed `ALTER DATABASE claude_offline_test SET SINGLE_USER WITH
ROLLBACK IMMEDIATE` before `DROP DATABASE`. Worth just doing this
preemptively before every disposable-database drop in future sessions
rather than hitting the error first.

gosmo's `replace ../gosmo` directive in gossms's `go.mod` was already left
uncommented from the prior session (New Database dialog build) — still
not tagged/pushed as of this session; flagged to the user again, still
pending their go-ahead since tagging/pushing is a release action.


---

## 2026-07-30 — Full review implementation

`full-review-implementation-2026-07-23`

*4th same-day 2026-07-23 pass — planned via EnterPlanMode then fully implemented (P1-P5); 1 gosmo SQL injection, 1 real blank-tree bug, 2 dropdown/overlay-ordering bugs, 3 missing mouseDragging latches, 4 file splits*

Follow-up to the same day's [[review-both-repos-2026-07-23]] and
[[review-followup-mousedrag-2026-07-23]] and [[mouse-routing-review-2026-07-23]] —
a 4th pass, this time a from-scratch full sweep (3 parallel Explore-subagent
audits: gosmo API conformance, `internal/tui`, `internal/tuikit`) rather than
just the day's uncommitted diff. Planned via `EnterPlanMode` with a written
plan file, approved, then the user said "implement P1 to P5" — proceeded
without a second model-switch prompt since [[model-switch-after-planning]]'s
reminder had already been given once at plan approval.

**Why:** user wanted a full bugs/inconsistencies/optimization/refactor sweep
of both repos. **How to apply:** this took ~20 fixes across both repos in one
sitting — evidence that a fresh 3-way parallel Explore audit still finds real
bugs even on a codebase reviewed multiple times the same day, as long as it's
scoped to areas the prior passes didn't specifically target.

**Real bugs found + fixed (all build/vet/test clean, plus live-tested on
[[gossms-live-test-server]]):**

1. **gosmo SQL injection** — `User.Grant/Deny/RevokeContext` (`schema_user.go`)
   spliced the caller's `permission` string straight into SQL with no
   allowlist check, unlike every sibling `Database.Grant/Deny/RevokePermissionContext`
   in `security.go`. Fixed by adding the same `validObjectPermission` guard.
2. **`TreeView.SetNodes` never re-clamped `scroll`** — a collapse/refresh
   shrinking the flat node list below the old scroll offset made Object
   Explorer render completely blank (Draw's loop breaks on its first
   iteration) until the next arrow key recomputed scroll as a side effect.
   Fixed by calling `ensureVisible` in `SetNodes`. Verified real by
   temporarily reverting the fix and confirming a new test
   (`TestSetNodesClampsScrollWhenListShrinks`) fails, then restoring it.
3. **`backup_dialog.go`/`restore_dialog.go` checked `ButtonClicked` before
   their own open dropdown** despite a comment claiming the dropdown "gets
   first refusal of every click" — same bug class as that morning's
   already-fixed status-row/menu-row ordering in `app_events.go`. Fixed by
   reordering both `HandleMouse` methods.
4. **3 widgets missing the `mouseDragging` latch** every other click-position
   widget in the package has: `file_dialog.go`'s list click (double
   overwrite-confirm dialogs), `editor_completion.go`'s popup click (phantom
   completion commit), `panel_manager.go`'s combo-arrow/tab-close click
   (double `OnCloseTab`, i.e. duplicate save-prompts). Same tcell
   resent-Button1-while-held root cause documented in
   [[treeview-menubar-drag-flicker-fixes-2026-07]].
5. **gosmo `executionplan.go`'s cleanup used `context.Background()`** (never
   times out) instead of a bounded context derived from the caller's `ctx`,
   and silently discarded its error. Fixed with
   `context.WithTimeout(context.WithoutCancel(ctx), 5s)`.
6. **`propsheet.PropertySheet.HandleMouse`** checked its button row/page list
   before the current page's `Form`, even though the form's open-dropdown/
   grid-popup overlay draws last — same "overlay must get first refusal"
   violation one level up from #3. Fixed by adding `Form.OverlayActive()` +
   a new `propsheet.OverlayActiver` interface (`SelectRow`/`GridRow` both
   implement it) and checking it before the button-row/page-list routing.
7. **`agent_menu.go`'s Start/Stop/Enable/Disable/Delete Job/Schedule/Alert/
   Operator actions had no `isConnected` guard**, unlike the file's own
   `showNew*Dialog`/`showAgentJobHistory` functions. Centralized the fix by
   adding the guard to the two shared helpers (`setAgentEnabled`,
   `deleteAgentEntity` in `agent_common.go`, now taking `sc` as a param)
   rather than repeating it at all 10 call sites.

**Convention gap-fills:** 4 missing gosmo `Seq` variants
(`SearchSeq`/`FragmentationStatsSeq`/`ActiveSessionSeq`/`ReadErrorLogSeq`) —
**note**: [[review-followup-mousedrag-2026-07-23]] *deliberately excluded*
these exact 4 methods as "diagnostics-shaped, not real collections worth
iterating." This session's audit re-flagged them as gaps and they were added
anyway, reasoning that CLAUDE.md's gosmo convention ("Add a Seq variant
alongside any new collection-returning method") has no documented
diagnostics exception. **Flagging the conflict here rather than silently
re-deciding it** — if a future session wants to revert, that prior memory
entry has the original reasoning. Also: `listbox.go` missing
`mouseDragging` latch (not currently exploitable, `propsheet.pageList` is
its one idempotent caller), `restore_dialog.go`/`restore_dialog_ops.go`
column alignment moved off `fmt.Sprintf("%-Ns", …)` onto a new
`core.PadLeft` (mirrors existing `core.PadRight`) for `DisplayWidth`
correctness, and `datagrid_input.go`'s editable "toggle grid" cell click
gained a `toggleRow`/`toggleCol` last-cell tracker so a resent Button1 at
the *same* cell doesn't double-toggle it while a genuine drag across
*different* cells still paints each one (preserves the documented
click-drag-paints behavior in `datagrid.go`'s `blockSelecting` comment).

**Investigated and explicitly skipped**: `input_field.go`'s
one-rune-per-column rendering, flagged by the audit as a Unicode gap
"unlike Editor's DisplayWidth-aware draw path." Read `editor_draw.go`
directly — all three of its draw paths (plain/wrapped/highlighted) use the
exact same `ci := e.scrollCol + col; ch := line[ci]` one-rune-per-column
indexing as `InputField`. `core.DisplayWidth` is used elsewhere in `Editor`
(gutter width, word-wrap splitting) but never in the actual glyph-drawing
loop. The audit's premise didn't survive verification — `InputField`
matches the codebase's one actual convention, not an outlier. **Lesson**:
an audit agent's claim about "file X already does Y correctly" is worth
directly verifying by reading X, not just trusting the comparison.

**Refactors** (file splits, all via exact-`sed`-line-range extraction +
byte-for-byte diff verification per CLAUDE.md's file-split instructions):
`file_dialog.go` (758→383 lines) → `_complete.go`/`_input.go`/`_draw.go`;
`propsheet/sheet.go` (612→331) → `_draw.go`/`_input.go`/`_clipboard.go`;
`controls/menu.go` (489 lines) → `menu_item.go`/`menu_bar.go`/`context_menu.go`;
`server_props.go` (622→155) → one file per page
(`_general`/`_memory`/`_processors`/`_security`/`_connections`/
`_database_settings`/`_advanced`/`_permissions`.go), matching
`database_props.go`'s existing per-page split; `prop_grid_helpers.go`
(887 lines, a real grab-bag) → `server_permissions_matrix.go` /
`extended_properties_form.go` / `securables_matrix.go` /
`role_descriptions.go`, keeping only the truly-generic one-liner helpers
(`boolStr`, `engineEditionName`, `orDefault`, …) plus the small
`buildFilterInfoForm` in the original file.

**Live-tested on ubudock**: FileDialog list nav post-split, MenuBar/Tools
menu post-split, Connect, deep Object Explorer expand (couldn't naturally
reproduce the exact TreeView scroll-clamp boundary with this server's real
data — pinned it with a unit test instead, verified failing pre-fix), all 8
Server Properties pages post-split (General/Memory/Processors/Security/
Connections/Database Settings/Advanced/Permissions, the last showing a real
28-row live grid). No stderr output, no crashes.


---

## 2026-07-30 — Gosmo database query conn leak

`gosmo-database-query-conn-leak-2026-07-24`

*Found+fixed a severe gosmo bug: Database.query leaked a pooled *sql.Conn on every call, wedging the app after ~20 reads; live-verified before/after against ubudock*

Second 2026-07-24 review pass, separate from [[review-both-repos-2026-07-24]]
(different session, different method: read-heavy + a throwaway scratch Go
module hitting the live [[gossms-live-test-server]] directly, rather than
parallel Explore agents). User asked for "review both projects... plan only,
ask before implementing"; after the plan was presented, user did `/model
sonnet` then said "implement all" — implemented without re-asking, per
[[model-switch-after-planning]]'s established precedent.

**The headline bug — `gosmo` `Database.query` (database.go)**: pinned a
`*sql.Conn` from the pool, ran `USE`, and returned only the bare `*sql.Rows`
to the caller, with a comment claiming "closing the rows... releases the
underlying conn automatically." False — closing rows obtained from a
`*sql.Conn` only RUnlocks the conn's internal mutex; the `*sql.Conn` itself
stays checked out until its own `.Close()`, and nothing calls that. All 44
call sites just did `defer rows.Close()`. Verified live at gossms's actual
pool settings (`maxOpenConns=20`): a plain sequence of Object Explorer tree
expansions wedges permanently at expansion #21 (`inUse=20`, every later read
times out) — this is a *real, easily-reproduced* app-breaking bug, not a
theoretical one, and every earlier "connection stability" review
([[db-connectivity-stability-review-2026-07-24]],
[[gosmo-server-level-retry-gap-2026-07-24]]) missed it because it's specific
to `Database`-scoped reads (`Server`-scoped reads go through the pool
directly via `s.db.QueryContext` and are fine).

**Fix**: a `dbRows` wrapper (`*sql.Rows` + the pinned `*sql.Conn`) whose
`Close()` closes both. `Database.query` returns `*dbRows` instead of
`*sql.Rows` — embedding means every one of the 44 call sites needed zero
changes; only `scanExtProps` (the one helper taking rows as a parameter,
fed exclusively by `Database.query`) needed its signature updated. Added
`TestDatabaseQueryReleasesConnection`, a hermetic test using a minimal
in-file fake `database/sql/driver` (no mocking library existed in gosmo;
none was added) — asserts `db.Stats().InUse == 0` after `rows.Close()`.
Verified the test actually catches the regression by temporarily reverting
the fix and confirming it fails, then restored.

**Why this one was missed until now**: it's not a race, not a
context-cancellation edge case, not a rare failure path — it's the *normal*
success path of the single most common gosmo read helper, and only shows up
after enough calls to exhaust `MaxOpenConns`, which unit tests never do and
casual live-testing (a handful of tree expansions) doesn't either. Only
showed up because this pass specifically wrote a throwaway loop hammering
`Database`-scoped reads in sequence and watched `sql.DB.Stats()`.

**Other fixes in the same pass** (gossms, all live-verified via build/
vet/test/-race, no live-server dependency needed):
- 8 sites still deriving background work from `context.Background()`
  instead of `sc.Context()` — the 6 `New*Dialog.show(sc)` methods (job/
  alert/login/database/operator/schedule) and `QueryPanel`'s
  `runQuery`/`runEstimatedPlan` — missed by the earlier
  [[db-connectivity-stability-review-2026-07-24]] sweep. `PropDialog.show`
  already did this correctly and was the reference pattern.
- Unbounded per-row fan-out in `loadTablesFolderDetails`/
  `loadDatabasesFolderDetails` (one goroutine per table/database, no cap) —
  added a shared `maxRowFetchConcurrency=8` semaphore
  (`rowFetchSemaphore()` in detail_browser.go).
- `wakeEventLoop` coalescing: added a `wakePending atomic.Bool`, cleared at
  the top of `Run()`'s loop before `drainPending()`, so a burst of
  concurrent background completions (now more likely given the semaphore
  still allows 8 at once) collapses into one `EventInterrupt` instead of one
  per goroutine.
- Latent (unreproduced) send-on-closed-channel panic: `tcell` v3's `Fini()`
  synchronously `close()`s `EventQ()`; `wakeEventLoop`'s old nil-screen-only
  guard didn't cover a background goroutine (e.g. `tickExecuting`'s ticker)
  landing after quit. Fixed with a `quitMu sync.Mutex` + `quitting bool`
  pair held across *both* `quit()`'s flag-set+`Fini()` call and
  `wakeEventLoop`'s flag-check+send — a plain atomic bool checked before
  sending would still race (TOCTOU against `Fini()` closing the channel
  concurrently); the mutex is what actually closes that race, not the flag
  itself.
- Dedup: `App.postAndWake(fn)` replacing 7 identical `post(fn)` bodies
  across `PropDialog` + the 6 `New*Dialog`s; `cancelIfSet(cancel)` replacing
  7 identical 3-line `onClose()` bodies.

**Explicitly NOT done**: a shared generic base struct/embedding across the
6 `New*Dialog` types (they differ in `forms`/`applyFns` array size — [2]
through [5] — and per-dialog name-closure fields), even though the review
flagged the boilerplate. Judged that forcing shared generics onto 6
not-quite-identical structs was the kind of premature abstraction
CLAUDE.md's "three similar lines is better than a premature abstraction"
rule warns against — did the safe mechanical dedup (post/onClose) and left
the struct shapes alone rather than guessing the user wanted the riskier
version.

**How to apply**: when reviewing `gosmo`, don't stop at reading — for
anything touching connection/pool lifecycle, write a real (if throwaway)
Go program against [[gossms-live-test-server]] and watch `sql.DB.Stats()`
across a realistic call volume; static reading alone missed this bug in at
least 3 earlier review passes. When embedding a wrapper type to fix a
leak like this, `grep` for every place the original concrete type
(`*sql.Rows` here) is used as a parameter/field type elsewhere in the
package — embedding covers method calls transparently but not explicit
type usage, and Go's compiler will only catch the mismatch at those sites,
not silently miscompile.


---

## 2026-07-30 — Gosmo server level retry gap

`gosmo-server-level-retry-gap-2026-07-24`

*real user-reported bug — gosmo's retry infra existed but only covered Database-level reads, never Server-level ones; fixed by extending it*

User hit a live bug: "gosmo: list databases: read tcp ...: wsarecv: An
existing connection was forcibly closed by the remote host" right after
connecting and expanding Object Explorer's Databases folder.

Root cause: `gosmo`'s `retry.go` (`withRetry`/`IsRetryable`) already retries
transient connection failures (net.Error, driver.ErrBadConn, io.EOF,
mssql.StreamError/ServerError) up to 3x with backoff — but it was wired
only into `Database`'s query helpers (`database.go`'s `query`/`queryRow`/
`withConn`), never into `Server`'s. Every `Server`-scoped read
(`DatabasesContext`, `LoginsContext`, `ServerRolesContext`, Agent
job/schedule/alert/operator reads, `Configurations`, `MemoryStats`, etc. —
~35 call sites across `server.go`, `server_config.go`,
`server_security.go`, `backup.go`, `agent_*.go`) called
`s.db.QueryContext`/`QueryRowContext` directly on the pool with zero retry
coverage, so a dropped pooled connection surfaced a raw error straight to
the UI instead of silently recovering the way a `Database`-level call
already did.

**Why:** the retry infrastructure was clearly a deliberate design decision
(idempotent reads only, writes explicitly excluded — see
`Database.withConn`'s doc comment: retrying a write after a partial
failure risks re-applying side effects) but was only ever applied to half
the codebase's read surface.

**Fix applied same session**: added `Server.query`/`Server.queryRow`
helpers (server.go, near `loadInfo`) mirroring `Database.query`/`queryRow`
minus the USE-a-database step (Server-scope has none), migrated all ~35
read call sites, left `Server.execContext` (script.go, the write
chokepoint) untouched. `Server.queryRow`'s shape ended up as `func(ctx,
scan func(*sql.Row) error, q string, args ...any) error` rather than a
`dest []any` — discovered mid-implementation that 3 files
(agent_schedule.go/agent_alert.go/agent_operator.go) use a
`scanX(server, row.Scan)` helper-function pattern that a fixed dest-slice
signature can't express, so the signature had to generalize to a scan
callback and every already-migrated call site got revisited for
consistency. One helper shape for the whole file, not two.

No dedicated retry-wiring test added for the new `Server.query`/`queryRow`
— matches `Database.query`/`queryRow`'s own established precedent
(`retry_test.go` unit-tests `withRetry`/`IsRetryable` in isolation only;
the wrapper wiring was never separately tested there either). Verified
instead via a throwaway scratchpad Go program hitting all 25 migrated
`Server` methods live against [[gossms-live-test-server]] (ubudock) — all
passed, no regressions. Could not reproduce the original wsarecv reset
itself from this sandbox (ubudock held up fine over dozens of calls from
here) — it's a genuine transient network condition on the user's own path
to the server, which is exactly the class of failure this fix protects
against going forward.

**How to apply**: if a future `Server`-level read method is added to
gosmo, it must go through `s.query`/`s.queryRow`, not `s.db.QueryContext`/
`QueryRowContext` directly — the same discipline `Database`'s methods
already follow via `d.query`/`d.queryRow`/`d.withConn`.


---

## 2026-07-30 — Help check for updates

`help-check-for-updates-2026-07`

*Help > Check for Updates built from scratch: GitHub releases API + version compare + new UpdateDialog*

2026-07-17: added Help > Check for Updates, built from scratch (no prior
placeholder).

**Implementation:**
- `internal/tui/update_check.go`: `App.checkForUpdates()` opens
  `UpdateDialog` in a loading state, then GETs
  `https://api.github.com/repos/radix29/gossms/releases/latest` on a
  background goroutine (same `postEvent`+`wakeEventLoop` handoff as
  `connectServer` in `app_connections.go` — no new async pattern
  introduced). Requires a `User-Agent` header or GitHub's API 403s.
  `compareVersions`/`parseVersionParts` do a tiny inline numeric
  `vMAJOR.MINOR.PATCH` compare (pre-release/build suffix stripped, `.`
  split, `strconv.Atoi` per segment) — deliberately not a new
  `golang.org/x/mod/semver` dependency, since this project's tags are
  always plain `v*` (see `.github/workflows/release.yml`'s
  `tags: ["v*"]` trigger) with no need for a general-purpose semver
  library. Unparseable strings (`(devel)`, a `go build`-without-ldflags
  pseudo-version like `v0.0.3-0.2026...+dirty`) degrade to comparing as
  `v0.0.0` for the parts that don't parse, which is exactly right for the
  common "ahead of the last tag, dirty checkout" case — verified live,
  see below.
- `internal/tui/update_dialog.go`: new `UpdateDialog` (`dialogs.ModalDialog`
  embed, same shape as `AlertDialog`/`HelpDialog` — no scrolling needed,
  content is always <10 short lines). Three states: checking, result
  (up-to-date / update-available / "newer than latest" for a dev build
  ahead of the last tag), or network error. The releases-page URL line
  gets an `Underline(true)` style as a "this is a link" visual cue — it
  isn't a real clickable hyperlink (tcell has no such primitive; embedding
  raw OSC 8 escapes would fight `Screen.Show()`'s own cell-by-cell
  redraws), same non-goal as the About dialog's plain-text repository URL.
- Wired into `App` (new `updateDialog` field, `buildUI`, `allDialogs`) and
  `Help` menu (`internal/tui/menu.go`, between Key Diagnostics and About).

**Live-tested against the real repo** (`github.com/radix29/gossms` does
have a published release, `v0.0.2`, confirmed via `curl`): building the
dialog from a local ahead-of-tag checkout correctly showed installed
`v0.0.3-0.20260717075048-602e2a338a9b+dirty` > available `v0.0.2` and the
"newer than the latest published release" message — this is the numeric,
not lexical, comparison working as intended on a real pseudo-version.
Initial dialog width (66) clipped that one longer message
("...published release." landed one word short); widened to 74 and
re-verified it fits. Close button and Escape both verified working.

**How to apply:** if a future feature needs "is version A newer than
version B" logic again, reuse `compareVersions`/`parseVersionParts` in
`update_check.go` rather than adding a semver dependency — it already
handles this project's actual tag shapes including devel/pseudo-versions.

**Same-day follow-up:** added a "Copy Link" button (alongside Close) once
a release resolves, so the releases-page URL can go to the clipboard
without a real terminal hyperlink (which tcell can't do — see above). Not
wired through the global `activeClipboardTarget()`/Ctrl+C machinery
(`clipboard.go`) — that's keyed off focused `InputField`/`Editor` widgets
and the dialog's link is just a drawn string, not one of those — instead
it's a plain third `DrawButtons` entry that calls `App.writeClipboard`
directly, mirroring `ConfirmDialog`'s existing btnFocus/Tab/Left/Right
pattern for >1 button. Button set is conditional: `["Close"]` while
checking/on error, `["Copy Link", "Close"]` once a link exists — default
focus stays on Close (last button either way) so Enter's meaning doesn't
change contexts. Live-verified end-to-end: Tab to Copy Link, Enter, then
`xclip -selection clipboard -o` showed the real release URL; dialog stays
open afterward so Close is still a separate step.


---

## 2026-07-30 — Indent trailing newline bug

`indent-trailing-newline-bug-2026-07`

*A real bug in showplan.Indent (treated any trailing EOF newline as \"already multi-line\") was caught only by visually inspecting plandemo output, not by the unit tests that \"passed\"*

While building the execution-plan viewer (2026-07-16, Phase 2 of
[[execution-plan-viewer-design-2026-07]]), `internal/showplan.Indent`'s
"is this XML already multi-line, so leave it alone" heuristic checked
`strings.Contains(x, ">\n")` — which is true for *any* single-line XML
file that simply ends with an ordinary trailing newline (nearly every
text file does). Indent was silently returning such input completely
unindented, a total no-op.

**Why the unit test didn't catch it:** `TestIndent_RealPlanRoundTrips`
asserted `strings.Contains(out, "\n")` and an unchanged `<RelOp>` count —
both trivially true even when `out == input` verbatim, because the
input's own trailing newline satisfies the first check and an unchanged
string trivially satisfies the second. The bug was only caught by
building `cmd/plandemo`, running it in tmux, and looking at the actual
rendered XML tab for a genuinely single-line fixture — a multi-line
`.sqlplan` fixture (SSMS always saves those pre-formatted) couldn't have
exposed it either, since Indent's early-return is correct for that case
regardless of the bug.

**Fix:** trim trailing `\r\n \t` before the "already multi-line" check,
so only a newline *between* two tags counts.

**How to apply:** for any "is this input already in the target shape"
heuristic, explicitly consider trailing-whitespace/EOF-newline as a
degenerate case before trusting a substring-match test — and don't treat
"my assertions passed" as proof; when a control produces visible output,
render it for real (this session's tmux-based plandemo check) before
calling a phase done. Reinforces [[gossms-tui-tmux-testing]].


---

## 2026-07-30 — Index statistics props build

`index-statistics-props-build-2026-07`

*Index Properties + Statistics Properties dialogs built from scratch from mockups; new async page-action pattern; DBCC SHOW_STATISTICS-backed pages; live-tested clean*

Built Index Properties (`internal/tui/index_props.go`) and Statistics
Properties (`internal/tui/statistics_props.go`) from
`todo/mockups/index_properties_tui_mockup.txt` /
`statistics_properties_tui_mockup.txt` — neither dialog existed before.
7 pages each: Index = General/Options/Storage/Included Columns/Filter/
Fragmentation/Extended Properties; Statistics = General/Columns/Filter/
Details/Histogram/Density Vector/Extended Properties. Both mockups' final
"Permissions" page was dropped — indexes and statistics are not SQL Server
securable classes, GRANT/DENY/REVOKE against one isn't valid T-SQL (unlike
[[table-properties-mockup-build-2026-07]]'s Permissions page, which is real
for tables).

**Why:** user asked to implement both dialogs "using the same design like
the existing properties dialogs" from the two mockup files.

**How to apply / what's non-obvious for next time:**

- **`nodeData` had no way to carry an Index/Statistic node's owning table
  name** — `loadIndexesChildren`/`loadStatisticsChildren` in
  `explorer_objects.go` built `NodeIndex`/`NodeStatistic` nodes with
  Schema=table's schema, Name=index/stat's own name, losing the table name
  entirely. Added `nodeData.TableName`, set directly on those two loaders
  (not threaded through `loaderCtx.node`'s fixed signature, to avoid
  touching every other call site).
- **New "immediate action" button pattern for Properties pages**: every
  existing page's buttons (Add/Remove in Files/Extended
  Properties/Securables) only mutate local form state, applied later via
  OK/Apply. Fragmentation's Rebuild/Reorganize/Update Statistics and
  Filter's Check Syntax/Estimate Rows are the first buttons that hit the
  server *immediately*, independent of Apply — added
  `PropDialog.runPageAction`/`asyncStatusButton` in `prop_dialog.go` for
  this (background goroutine + `d.post`, same external-wake rule as
  everything else — see the `wakeEventLoop` doc in this file's CLAUDE.md).
  Pages needing this take `d *PropDialog` (not `sc`-only); `showXFor`
  callers pass `a.propDialog`. No auto-refresh after an action — user
  presses F5, matching the framework's existing per-page refresh.
- **DBCC SHOW_STATISTICS is the correct, real backing for Details/
  Histogram/Density Vector** — verified live against ubudock
  (`HealthClinic.dbo.Patients`) before writing any Scan code: `WITH
  STAT_HEADER` (Name/Updated/Rows/Rows Sampled/Steps/Density/Average key
  length/String Index/Filter Expression/Unfiltered Rows/Persisted Sample
  Percent — all NULL except Name when the stat has never been populated,
  handle via `sql.Null*`), `WITH DENSITY_VECTOR` (one row per leading-column
  prefix — a 3-column stat gave 3 rows), `WITH HISTOGRAM` (RANGE_HI_KEY's
  SQL type follows the leading key column — int, varchar, whatever — scan
  into `any` and `fmt.Sprintf("%v", ...)`, verified clean for both int and
  varchar). Added to gosmo's `statistics.go`: `Statistic.HeaderContext`,
  `DensityVectorContext`, `HistogramContext`, `ColumnsContext` (via
  `sys.stats_columns`), plus `NoRecompute`/`IsIncremental`/
  `ModificationCounter` fields on `Statistic` (from `sys.stats`/
  `sys.dm_db_stats_properties`, all live-verified columns).
- **No recompute/Incremental are read-only, not Apply-tracked toggles** —
  SQL Server has no `ALTER STATISTICS` to flip them in isolation, only
  `UPDATE STATISTICS ... WITH NORECOMPUTE`, which also re-samples. Showing
  them editable and silently no-oping on Apply would be worse than not
  showing them editable at all.
- **Included Columns is genuinely editable** (unlike Filter, which is
  read-only + live Check Syntax/Estimate Rows since SQL Server only accepts
  a filter predicate at CREATE time) — added `Index.SetIncludedColumns` to
  gosmo, which reissues the whole index via `CREATE INDEX ... WITH
  (DROP_EXISTING = ON)` from the index's own key columns/uniqueness/type/
  filter. **Live-tested the actual write path**: toggled `PatientID` on
  for `IX_Patients_LastName`, Applied, verified via a direct
  `sys.index_columns` query that it really landed, then reverted the same
  way — round-tripped correctly with key columns preserved.
- Also added to gosmo: `Table.CountWhereContext`/`CheckWhereSyntaxContext`
  (shared by both dialogs' Filter pages — `COUNT_BIG(*)`/`SELECT TOP(0) 1`
  against the table with the index/stat's own filter predicate),
  `Index.StorageInfoContext` (filegroup/partition/space/allocation units,
  live-verified against real `sys.dm_db_index_physical_stats(...,
  'SAMPLED')` for avg record size), `Index.FragmentationContext`
  (single-index version of the existing table-wide
  `FragmentationStatsContext`, adds page density), `Index.SetOptions`/
  `RebuildWithOptions` (SET-able options vs. rebuild-only options — fill
  factor/pad index/data compression only take effect on rebuild, so
  Options page's Apply issues a combined `SET` and/or `REBUILD WITH`
  depending which group is dirty), `Index.UpdateStatistics`.
- Live-tested end-to-end against ubudock (`HealthClinic.dbo.Patients`,
  index `IX_Patients_LastName`, all 3 statistics): every page on both
  dialogs loaded real data correctly on the first try — **no bugs found**.
  Also live-tested Update Statistics (Fragmentation page) and Script as
  CREATE (Statistics Details page, opened a new query panel with a
  syntactically valid `CREATE STATISTICS` statement) — both worked.


---

## 2026-07-30 — Intellisense followups

`intellisense-followups-2026-07-17`

*Four IntelliSense follow-up requests after the initial autocomplete build — disable toggle, sys-schema cache, whole-statement FROM-scope, blank-char gating*

Follow-up session to [[gossms-live-test-server]]-based IntelliSense work
(the original build is documented only in conversation history, not yet a
memory file). User asked for four things in one message, all implemented
same day:

1. **Options dialog toggle** — `config.IntelliSenseDisabled bool` (stored
   inverted so the zero value keeps it on by default), checkbox in
   `internal/tui/options_dialog.go`. Single gate at the top of
   `sqlCompletionCandidates` in `completion_provider.go` disables both
   auto-trigger and Ctrl+Space.

2. **`sys` schema cached once per server connection, shared across every
   database** — new `App.sysCompletionInventories map[string]*completionInventory`
   keyed by `sysCompletionInventoryKey` (server+login, no database
   component), proactively loaded from both `connectServer` and
   `connectForQueryPanel` (not lazily). Query source is always `"master"`
   since the schema is identical everywhere.

   **Real gosmo bug found via live testing**: `sys.objects`/`sys.columns`
   — despite the generic names — only ever surface *user*-created objects;
   `is_ms_shipped=1` rows (every built-in catalog view, including
   `sys.tables`, `sys.columns`, `sys.objects` itself) are invisible through
   them. `SELECT ... FROM sys.objects WHERE SCHEMA_NAME(schema_id)='sys'`
   returns **zero rows** — confirmed live against ubudock. Fix: gosmo's new
   `SystemCatalogContext` queries `sys.all_objects`/`sys.all_columns`
   instead (622 rows on ubudock vs. 0). `CatalogContext` (user objects)
   correctly keeps `sys.objects`/`sys.columns`. This is a genuinely
   surprising SQL Server naming trap — `sys.objects` sounds like "every
   object" but isn't.

3. **Whole-statement FROM-scope** — `SELECT | FROM Customers c` (cursor
   still in the column list, FROM typed after) now resolves columns/alias
   just as well as FROM typed above the cursor, matching how SSMS parses
   the whole current statement regardless of cursor position. Implemented
   by generalizing the tokenizer (`tokenizeSQLPrefix` → `tokenizeSQLRange`
   with a `stopAtSemicolon` flag) and adding `statementEndOffset` (mirrors
   `statementStartOffset`, forward instead of backward) — `refs` is now
   built from prefix tokens + a forward scan bounded to the statement's
   end (next top-level `;` or `GO` line), while `currentClause` still uses
   prefix-only tokens (cursor's own clause position must not look ahead).

4. **Blank-char gate on auto-open** — SUPERSEDED same day by the stricter
   word-start gate (`Editor.canAutoOpenCompletion`): auto-open now requires
   a typed character AND a word fragment touching the cursor that starts
   with a letter or '[' — space, '.', digits, Enter, Backspace/Delete,
   undo/redo never open it fresh; Ctrl+Space still bypasses. See
   [[intellisense-review-2026-07-17]] for the full bug-review session that
   changed this.

**Why:** User's own words — "add option to disable intellisense, by
default is enabled", "sys schema... cached at connection time... loaded
only once", "after typing select the intellisense should show the column
context instead the tables/views", "to activate intellisense context at
least one non blank character needs to be typed or keyboard shortcut". All
four were terse one-liners in a single message with no follow-up
clarification — interpreted from context/prior design rather than asked
about, consistent with auto-mode bias toward proceeding.

**How to apply:** If sys-schema completion ever looks empty again, check
gosmo's query source first (`sys.all_objects`/`sys.all_columns`, not
`sys.objects`/`sys.columns`) before assuming a gossms-side bug — this bit
once already. See [[gossms-user-terminal-env]] for the Ctrl+Shift+O
tmux-testing gotcha hit while verifying this.


---

## 2026-07-30 — Intellisense review

`intellisense-review-2026-07-17`

*Full IntelliSense bug review after user hit 'paPatients' append + misplaced popup — one offset/column contract bug caused both; 8 more fixes shipped*

User reported two IntelliSense bugs ("Enter appends instead of replacing
the typed word", "popup sometimes appears away from the cursor") plus a
review request; mid-turn added two behavior changes. All fixed same day,
unit-tested, live-verified on [[gossms-live-test-server]].

**The root cause of both reported bugs was one contract mismatch**:
`controls.CompletionProvider` documents `replaceFrom` as a **column on the
cursor's row** (the editor replaces `[replaceFrom, cursorCol)` on that row
and anchors the popup X at it), but the SQL provider returned token starts
as **flattened whole-buffer rune offsets**. Row 0 works by coincidence
(offset == column) — every pre-existing test was single-line SQL, so tests
passed and the demo looked fine; on any later row the commit clamps to
end-of-line (→ "from paPatients") and the popup shifts right by the length
of all preceding lines. Fix: subtract `offsetForCursor(lines, row, 0)`
once in `sqlCompletionCandidates`. **Lesson: when two layers exchange a
position, test the case where the two coordinate systems diverge (row > 0
here) — the coincidence row is exactly what demos and quick tests use.**

Other real bugs found by the review (all in the same commit-batch):
- **Keyword-colliding prefixes**: "sys.all|" lexes ALL as a keyword, so it
  wasn't treated as the prefix → commit appended ("sys.allall_objects").
  Same for OR→Orders, IN→Invoices. `completionTokenContext` now treats a
  trailing keyword token as the word being typed.
- **Modifier hijack**: `handleCompletionKey` ignored modifiers — Ctrl+Up/
  Down (resize), Ctrl+Shift+Up/Down (move lines) moved the popup selection
  while open. Now any Ctrl/Alt/Shift-modified key falls through.
- Popup lifecycle: closes on SetText (file open), on focus loss
  (SetActive(false)), on wheel-scroll outside it, on right-click (was
  stacking under the context menu); popup X clamped inside the editor
  rect; completionScroll clamped when the item list shrinks.
- Undo/redo/Delete/Backspace could pop the popup open from closed; now
  only typed characters can (they still re-sync an open popup).

User-requested behavior changes (both in `internal/tuikit/controls`):
- **Word-start gate** (`canAutoOpenCompletion`): auto-open only when the
  word fragment touching the cursor starts with a letter or '[' — bare
  '.', digits, space, blank lines never auto-open (a '.' member list now
  waits for the member's first letter); Ctrl+Space always forces.
- **'[' opens completion**, which required the provider to complete inside
  an *unterminated* bracket identifier instead of suppressing (lexer state
  sqlLexBracket): prefix = text after '[', replaceFrom = the '[' itself,
  `tokenizeSQLRange` grew a 4th return (quoteStart), and the forward
  FROM-scope scan skips past the bracket's ']' before resuming. "[Order
  De|" matches "Order Details" and commits as "[Order Details]".
- **Ctrl+R must NOT reload the sys-schema inventory unless its load
  failed** (user said so explicitly): `retrySysCompletionInventory` keeps
  a successful snapshot, only retries on `err != nil`; sys load failures
  now also set a status message.

**How to apply:** popup position math lives in `completionRect`
(editor_completion.go) and is rune-column-based like the whole Editor (one
rune = one column, tabs expanded on SetText) — don't "fix" it with
DisplayWidth unless the Editor itself changes. The provider→editor
coordinate conversion is the one `rowStart` subtraction in
`sqlCompletionCandidates`; anything new the provider returns as a position
must go through the same conversion.

**Same-day follow-up**: user reported columns from an unrelated statement
leaking into a bare column context — live-confirmed with two SELECTs
stacked with no `;` between them (`select * from Patients` / `select *
from Doctors` / `select ` — the third line's popup showed both tables'
columns). Root cause: `statementStartOffset`/`statementEndOffset` only
recognise `;` and bare `GO` lines as statement boundaries, but SSMS never
requires a `;` between ad hoc statements, so stacking several without one
is completely normal and the existing scoping did nothing for it. Fix:
new `dmlStatementStarts`/`narrowToDMLStatement` in
`completion_provider.go` additionally split on a top-level `SELECT`/
`INSERT`/`UPDATE`/`DELETE`/`MERGE`/`WITH`, with explicit exceptions so
`UNION`/`EXCEPT`/`INTERSECT`-chained SELECTs, a CTE's own main SELECT
after `WITH ... AS (...)`, and `INSERT ... SELECT` all still count as one
statement (only `INSERT ... VALUES` followed by a later semicolon-less
SELECT is a known, documented miss — rare and no full parser exists here
anyway). Side-effect fix found while wiring this up: `EXCEPT`/
`INTERSECT` weren't in `sqlKeywords` at all (lexed as plain identifiers),
and `parseFromScope`'s `expectRef` flag was never reset at `UNION`, so
`... UNION SELECT Id FROM B` could mis-parse the second SELECT's own
column (`Id`) as a table reference — both fixed in the same pass, covered
by `TestParseFromScopeResetsAfterUnion`.


---

## 2026-07-30 — Key props build

`key-props-build-2026-07`

*Primary/Unique Key Properties dialog built from scratch, no mockup (SSMS used as reference); found+fixed a real stale-name-after-rename bug shared by every rename-capable Properties dialog*

Built Key Properties (`internal/tui/key_props.go`) for the Object Explorer's
"Keys" folder (`NodeKey` — a table's PRIMARY KEY and UNIQUE constraint
entries; NOT `NodeForeignKey`, which sits in the same folder but was left
out of scope — real SSMS gives foreign keys a completely different
multi-relationship editor, not a single-object property sheet). No mockup
existed for this one — user said "use SSMS as example" — so this session
verified every real-SQL-Server behavior live against ubudock
(`HealthClinic.dbo.Doctors`, which has `PK_Doctors` (clustered),
`UQ_Doctors_LicenseNumber`/`UQ_Doctors_Email` (nonclustered) — a good mix
of both key types for testing) rather than guessing from memory of SSMS's
UI.

**Why:** direct continuation of [[index-statistics-props-build-2026-07]] —
same user, same "build the properties dialog the existing dialogs' way"
request, just for Keys instead of Index/Statistics.

**How to apply / what's non-obvious for next time:**

- **A PRIMARY KEY/UNIQUE constraint *is* a `sys.indexes` row** (`is_primary_key`/
  `is_unique_constraint` = 1) — so `key_props.go` reuses Index Properties'
  own `findIndex`/`pageIndexStorage`/`pageIndexFragmentation`/
  `pageIndexExtendedProperties` directly rather than duplicating them.
  Only two pages are Key-specific: `pageKeyGeneral` (identity + editable
  rename, vs. Index's read-only name) and `pageKeyOptions` (Index's Options
  page minus Ignore Duplicate Keys — see next point). Included Columns and
  Filter are dropped entirely: `ALTER TABLE ADD CONSTRAINT` has no
  `INCLUDE`/`WHERE` clause, so a constraint-backed index can never have
  either, live-confirmed on `sys.indexes` (no filtered/included
  constraint-backed row exists anywhere in the DB, and there is no valid
  T-SQL to create one).
- **Live-caught real SQL Server restriction: `ALTER INDEX ... SET
  (IGNORE_DUP_KEY = ...)` is rejected outright on any index backing a
  PRIMARY KEY or UNIQUE constraint** — `"Cannot use index option
  ignore_dup_key to alter index '...' as it enforces a primary or unique
  constraint"`, thrown even when setting it to its own existing value (not
  a no-op-tolerant restriction, an unconditional one). `SetOptions`
  (Index Properties' existing method, bundles IGNORE_DUP_KEY +
  ALLOW_ROW_LOCKS + ALLOW_PAGE_LOCKS in one `ALTER INDEX SET`) can't be
  reused as-is for Keys. Added `Index.SetLockOptions`/
  `SetLockOptionsContext` to gosmo (`ALLOW_ROW_LOCKS`/`ALLOW_PAGE_LOCKS`
  only) instead of trying to make the existing method conditionally omit
  a clause. Everything else (`RebuildWithOptions` for fill factor/pad
  index/data compression, `Rebuild`/`Reorganize`/`UpdateStatistics`,
  `StorageInfo`, extended properties) all live-verified working
  identically on a constraint-backed index, including the clustered
  `PK_Doctors` — no other special-casing needed.
- **Added `Index.Rename`/`RenameContext` to gosmo** (`sp_rename
  '[schema].[table].[indexname]', newname, 'INDEX'`) — also the correct,
  live-verified mechanism for renaming a PK/UNIQUE constraint itself, since
  its name *is* the backing index's name in `sys.indexes`. Round-tripped
  live (rename → verify via direct query → rename back → verify) on both a
  plain index (from the earlier Index Properties session) and a real
  `UNIQUE` constraint this session.
- **Found and fixed a real bug this surfaced, not scoped to Keys**: every
  `xPropPages(sc, ..., name string)` in this codebase (Index, Key, and
  the pre-existing Login/User/Role Properties, which already had rename)
  bakes `name` into every page's `load` closure as a plain, immutable
  string at dialog-open time. `PropDialog.runApply` calls
  `d.InvalidateAll()` after a successful Apply, which reloads every page —
  including, for Apply (not OK, which just closes), the very page whose
  edit just renamed the object. That reload calls `findIndex`/`findLogin`/
  etc. with the *old*, now-nonexistent name and errors
  (`"index \"X\" not found on ..."`), live-caught immediately after the
  first real Key rename Apply in this session. **Fixed only for Key
  Properties** (the dialog actively being built): `keyPropPages` now boxes
  `name` in a `*string` shared by every page in that dialog instance;
  `pageKeyGeneral`'s rename apply closure does `*name = newName` on
  success, so the next reload looks up the right row. This required
  changing `pageIndexStorage`/`pageIndexFragmentation`/
  `pageIndexExtendedProperties`'s shared signatures from `name string` to
  `name *string` (dereferenced internally) since Key Properties reuses
  them unchanged — `indexPropPages` (Index Properties has no rename) just
  passes `&name` for a value that's never mutated, so its behavior is
  identical to before. **Login/User/Role Properties were NOT touched and
  have the identical latent bug** (verified by code inspection — same
  `name string`-closure shape, same `RenameContext`-then-`InvalidateAll`
  sequence) — out of scope for this session, left for a future pass. To
  reproduce there: open Login Properties, rename the login, click Apply
  (not OK), watch its own General page reload fail.
- Live-tested end-to-end against ubudock: all 5 pages (General/Options/
  Storage/Fragmentation/Extended Properties) loaded correctly for both
  `PK_Doctors` (clustered, Type shows "Primary Key") and
  `UQ_Doctors_Email`/`UQ_Doctors_LicenseNumber` (nonclustered, Type shows
  "Unique Key") — dialog title itself is dynamic ("Primary Key Properties"
  vs. "Unique Key Properties"), read off `nodeData.IsPrimaryKey` (now
  dual-purpose — see `tree_node.go`'s doc comment — set from the tree
  loader's own already-fetched `idx.IsPrimaryKey` rather than a second
  round trip). Two real write paths round-tripped clean: rename
  (`UQ_Doctors_Email` → `_tmp` → back, verified via `sys.indexes` each
  time) and Options' Pad Index toggle (`ALTER INDEX REBUILD`, verified via
  `sys.indexes.is_padded` each time). Database left in its original state
  afterward — tree label re-synced correctly via Refresh too, confirming
  the rename fix didn't leave anything stale beyond the dialog itself.
- Tmux testing note for next time: clicking a `PropertySheet`'s own
  OK/Cancel/Apply/Script Changes row via `Tab`-counting is fragile in a
  way not fully captured by the existing tmux-testing memory — the exact
  Tab count to reach it depends on whether you're tabbing in from the
  page-list zone fresh vs. already mid-form, and a `Note` row (never
  focusable) mixed into a `Section`-heavy page makes miscounting easy. The
  mouse-click approach (`printf` a raw SGR click at the button's
  `capture-pane`-derived column, same technique already documented for
  `DataGrid` cells) worked reliably every time and is worth defaulting to
  for this specific button row instead of Tab-counting.


---

## 2026-07-30 — Login properties gapfill

`login-properties-gapfill-2026-07`

*Login Properties gap-fill vs. ssms_login_properties_tui_mockup.md (2026-07-15) — unlike Table/Role/User Properties, this dialog already existed; found and fixed a real ContextMenu divider-navigation bug and a real gosmo NULL-scan bug along the way*

Unlike the from-scratch builds documented in [[table-properties-mockup-build-2026-07]],
[[database-role-properties-mockup-build-2026-07]], and
[[database-user-properties-mockup-build-2026-07]], **Login Properties already
existed** in gossms (`internal/tui/login_props.go`, 5 pages) before this
session — this was a gap-fill pass, comparing the existing implementation
against `todo/mockups/ssms_login_properties_tui_mockup.md` line by line and
fixing real deltas, mostly by wiring up gosmo capability that already
existed but wasn't exposed in the UI.

**Shipped**:
- **General**: added a Summary section (Login type/SID/Created/Modified,
  same fields/formatting `role_props.go`/`user_props.go` already use — zero
  new gosmo work). Turned the read-only `MustChangePassword` Static into an
  editable Check, and added a new "Unlock login" Check — both wired into
  `gosmo.Login.ChangePasswordWithOptionsContext`'s `mustChange`/`unlock`
  params, which the code had **always hardcoded to `false`** even though
  gosmo fully implemented them. This was dead wiring in an already-complete
  API, not new capability — the kind of gap this style of pass is for.
- **Server Roles**: added `fixedServerRoleDescriptions`/`serverRoleImpact`
  lookup maps (`prop_grid_helpers.go`, same shape as `fixedRoleDescriptions`
  from the Database User Properties pass) plus an Impact column and a
  "Selected role" detail panel synced via `OnSelectRow` — same pattern as
  `pageUserMembership`.
- **User Mapping**: added a Schema column to the mapping grid and a
  "Selected mapping" panel below it (Default schema + a per-database
  "Database role membership" `ToggleGrid`) — `gosmo.LoginUserMapping`
  already returned `DefaultSchema`/`Roles []string` per mapping (the query
  already joined `sys.database_role_members`); this was purely a
  surfacing-existing-data problem, not a new gosmo query. Applying schema
  changes reuses `User.SetDefaultSchemaContext` (from the User Properties
  pass); role toggles reuse `Database.AddRoleMemberContext`/
  `RemoveRoleMemberContext` (from the Role Properties pass) — zero new
  gosmo writers needed for this page either.
- **Securables**: was previously **explicitly read-only** ("edit via the
  Server Properties Permissions page"), even though
  `gosmo.Server.GrantServerPermissionContext`/`DenyServerPermissionContext`/
  `RevokeServerPermissionContext`/`ServerPermissionNames()` already existed
  and the Status page's Connect-permission field already called the same
  trio. Made it a real editable Grant/Deny/(none) grid. Deliberately **not**
  a reuse of `buildSecurablesMatrix`/`buildPermissionsMatrix` — both of
  those are shaped around a *list* of securables/principals; a login has
  exactly one target (the server itself), so a small dedicated grid
  (mirroring the bottom half of those two helpers, reusing their already-
  shared `nextPermState`/`displayPermState` cycling functions) was simpler
  than forcing a fit.
- **Status**: added "Bad password time" (one new `LoginDetails` field +
  one `LOGINPROPERTY(...,'BadPasswordTime')` query column — gosmo addition,
  same shape as every other `LoginDetails` field) and an "Active sessions"
  count using `gosmo.Server.ActiveSessionsContext` (already existed,
  previously unused anywhere in gossms — see the bug below).

**gosmo changes**: `LoginDetails.BadPasswordTime` (new field/query column,
`login.go`) plus a real NULL-handling bug fix in `ActiveSessionsContext`
(`server_config.go`) — see below.

**Two real, pre-existing bugs found and fixed via live testing, neither
introduced this session**:

1. **`internal/tuikit/controls/menu.go`'s `ContextMenu.HandleKey`** (Object
   Explorer's right-click menu, used by *every* node type) moved `hover`
   with raw `hover++`/`hover--`, never skipping `Divider` entries — landing
   keyboard focus *on* a divider shows no visible highlight (dividers never
   render `hoverStyle`) and pressing Enter there closes the menu with no
   action (`Hide()` runs before the `!item.Divider` check). `MenuBar`'s own
   dropdown had already solved this correctly via `firstSelectableItem`/
   `stepSelectableItem` (both already free functions, not `MenuBar`
   methods) — `ContextMenu` just never got the same treatment. Fixed by
   routing `ContextMenu.HandleKey`'s Up/Down through those same two
   functions. This silently broke keyboard navigation to "Properties..."
   on any node whose menu has a divider before it (which is all of them,
   by convention) — worth remembering this was pre-existing and affects
   every prior menu-wiring pass in this conversation (Table/Role/User
   Properties' new `Properties...` entries), not just Login.
2. **`gosmo.Server.ActiveSessionsContext`** (`server_config.go`) scanned
   `s.login_name`/`s.host_name`/`s.program_name` directly into non-nullable
   `string` fields, even though all three can be NULL in
   `sys.dm_exec_sessions` for system/background sessions. This function
   existed in gosmo already (`README.md` documents it, an example uses it)
   but had **never been called from anywhere in gossms** until this
   session's Status page change — so the bug was real but dormant. Hit
   immediately on first live test (`Error: sql: Scan error on column index
   2, name "host_name": converting NULL to string`). Fixed by wrapping the
   three columns in `ISNULL(..., '')` in the query, matching the
   `blocking_session_id`/`wait_type`/`wait_time` columns in the same query
   that already used that approach (rather than Go-side `sql.NullString`,
   which the *other* half of the same query's columns use — this file
   already mixes both styles per-column, so either fix would have been
   consistent; picked the simpler one-line version).

**A real technical constraint discovered while implementing, not in the
approved plan**: the plan called for "Default schema" on the User Mapping
page's per-selection panel to be a `propsheet.Select`. Built it, then found
`propsheet.SelectRow`/`widgets.DropDown` have **no way to change their item
list after construction** — items are fixed at `Select(label, items,
selected)` call time, and since each mapped database can have a different
schema list, a single shared `Select` widget can't serve every row's
distinct options without a new tuikit capability (a `SetItems` method).
Rather than extend `DropDown`/`SelectRow` for this one call site, shipped
"Default schema" as a plain `propsheet.Text` field instead — consistent
with how every rename/identity field elsewhere in this codebase is a Text,
and technically fine since **SQL Server itself doesn't validate
`DEFAULT_SCHEMA` against existing schema names at `ALTER USER` time**
(setting it to a not-yet-created schema name is legal; it just doesn't
resolve until that schema exists) — so no validation was skipped that SQL
Server itself would have required anyway.

**tmux testing note**: [[gossms-tui-tmux-testing]] already documented the
`0;122;204` (true focus) vs. `0;86;153` (grid has focus, showing its own
cursor row) vs. `40;40;42` (grid's cursor row when the grid *doesn't* have
Form focus) distinction from a prior session — re-hit and initially
mis-diagnosed several of these as "Tab skipped rows" bugs in my own new
code before re-deriving the same distinction from source (`form.go`,
`sheet.go`) the hard way. The fix for next time: read that memory file's
existing bullets *before* debugging a focus-navigation "bug" in a
freshly-tmux-tested page, rather than re-discovering the same coloring
rules through trial and error — this cost significant time this session
that a five-minute memory read would have saved.

**Live-verified** against a disposable SQL login (`claude_login_test`) and
throwaway database (`claude_login_test_db`, one schema, one role, one
FOR-LOGIN user) on ubudock: must-change/unlock applied together with a
password reset (`sys.sql_logins.is_policy_checked`/`LOGINPROPERTY(...,
'IsMustChange')` re-queried directly), `sysadmin` server-role membership
toggle (`sys.server_role_members` re-queried), User Mapping's default
schema change and a new database-role membership both persisted
(`sys.database_principals.default_schema_name`/
`sys.database_role_members` re-queried), a server-level Securables GRANT
(`sys.server_permissions` re-queried), and Status loading cleanly
post-fix. Disposable login/database dropped at the end.

**Deferred, consistent with every prior pass**: Windows/Microsoft Entra
login variants (nothing to live-test on this Linux, non-Entra server);
every dedicated modal the mockup proposes (password-change, default-DB
picker, high-impact confirmations, Add Securables, Effective Permissions,
disable/locked-login modals, orphaned/SID-mismatch mapping repair,
script-pending-changes — superseded by the dialog-wide Script Changes
button); a full session list + Kill Sessions action (only the count
shipped — a proper Activity-Monitor-shaped feature doesn't exist anywhere
in gossms yet); securable types beyond SERVER on the Securables page
(gosmo has no `GRANT ... ON LOGIN::x`/`ON ENDPOINT::x` support — a real,
separate gosmo gap for a future pass).


---

## 2026-07-30 — Mousedragging full sweep and dialog sizing

`mousedragging-full-sweep-and-dialog-sizing-2026-07-23`

*full-codebase mouseDragging re-fire sweep across every remaining widget/dialog, 2 smaller bugs fixed, plus a new text-fit dialog sizing feature*

Session did a fresh two-repo review (separate from the earlier
[[review-both-repos-2026-07-23]] pass, which found different bugs), then
fixed everything found plus a user-requested feature.

## The mouseDragging re-fire class was NOT fully swept — now is

[[treeview-menubar-drag-flicker-fixes-2026-07]] fixed Editor/DataGrid/
MenuBar/TreeView/Toolbar for tcell's held-motion Button1 resend, and its
memory said "check any new single-row button widget for the same latent
bug." That check was never done for the widget/dialog layer. This session
found and fixed it in: `widgets.Button`, `widgets.DropDown`,
`widgets.RadioBox`, `widgets.CheckBox` (all in `internal/tuikit/widgets/`),
`dialogs.ModalDialog.ButtonClicked` (shared by every Backup/Restore/
Connect/Options-style dialog), and `planview.PlanView` (tab bar/Expand
button/statement-selector arrows). Each got its own `mouseDragging bool`
field, reset on a `ButtonNone` event, gating the action so a held click
that twitches a cell fires once, not once per resent motion event.

`ModalDialog` needed a different reset point than the widgets: individual
dialogs (`BackupDialog`, `RestoreDialog`, etc.) sometimes short-circuit a
`ButtonNone` event before it ever reaches `ButtonClicked` (forwarding it to
a focused input field instead), so the reset lives in
`ConsumeOutsideClick` — the one call every dialog's `HandleMouse` makes
unconditionally first, regardless of mode.

`propsheet.CheckRow`/`SelectRow`/`RadioRow` needed no fix of their own —
they already delegate to the underlying widget's `HandleMouse`, so fixing
the widget fixed the row wrapper for free. Same for `propsheet.ButtonsRow`
(delegates to `widgets.Button`).

**Bonus catch**: `OptionsDialog`'s hand-rolled `cbIntelliSense` hit-test had
no button-state check at all (not even the re-fire bug — it would toggle
on a pure hover motion event with no click). Fixed by the same
consolidation (replaced with `cb.HandleMouse(ev)`).

**How to check for this class in the future**: grep any `HandleMouse` that
does `if ev.Buttons() == tcell.Button1 { <fire action>; return true }`
without a `mouseDragging`-style guard alongside it.

## Two smaller bugs, one per repo

- gosmo `Index.RebuildWithOptionsContext` interpolated `dataCompression`
  into `ALTER INDEX ... REBUILD WITH (...)` unvalidated — every sibling
  keyword-arg method in the file (`FragmentationContext`,
  `SetDatabaseScopedConfigContext`) whitelists its value first. Added the
  same whitelist (NONE/ROW/PAGE/COLUMNSTORE/COLUMNSTORE_ARCHIVE).
- gossms `detail_browser.go`'s single-shot `fetchNodeDetails` path (all of
  `agent_detail.go` + `agent_reports.go`) ran on plain, no-context gosmo
  calls while every progressive loader used a `childFetchTimeout`-bounded
  context — a hung server would leak the goroutine+connection forever with
  no UI symptom beyond a frozen "Loading...". Threaded `ctx` through both
  files (every call converted to its `*Context` variant) and gave the
  `fetch()` dispatcH's default-case goroutine a `childFetchTimeout` context.

## New feature: dialogs auto-size to their message text

User asked: dialogs should grow to fit their text, capped at 2/3 of the
screen's width, wrapping onto more lines instead of growing wider past
that cap. Landed as `ModalDialog.fitMessage(message, minW, baseH) (w, h
int, lines []string)` in new `internal/tuikit/dialogs/common.go` — `minW`/
`baseH` are each dialog's old fixed size, now used as a floor (so a short
message keeps the old look) rather than a ceiling. Wired into
`AlertDialog`, `ConfirmDialog`, `TypedConfirmDialog` (the only 3 dialogs
that show a free-form message string — deliberately did NOT touch
BackupDialog/RestoreDialog/ConnectDialog/OptionsDialog/PropertiesDialog,
whose layouts are driven by many fixed-position widgets, not a single text
blob). `TypedConfirmDialog` needed extra care: its "Type ... to confirm:"
line, input field, and status line are top-anchored (`inner.Y+N` literals),
not bottom-anchored like Alert/Confirm's button row (`ButtonRowY` is
already relative to `rect.H`, so those two needed zero position math
beyond the message itself) — so every one of those offsets got a
`+extra` (`extra := len(msgLines)-1`) added.

Word-wrap logic (`core.WrapText`) was promoted from a propsheet-private
`wrapText` (used by `propsheet.Note` rows) up to `core/strutil.go` so both
`dialogs` and `propsheet` — which don't import each other per tuikit's
one-way dependency graph (`core ◄── widgets ◄── layout ◄── dialogs ◄──
propsheet`) — can share one implementation.

Verified with a throwaway `cmd/_scratch_dialogdemo` binary (underscore
prefix so `go build ./...`/`go vet ./...` skip it) run under tmux at
120x30 — confirmed real word-wrap into 3 lines + correct button
repositioning for a long Confirm message, and no size regression for short
Alert/Confirm messages — then deleted before finishing. Also added
`internal/tuikit/dialogs/common_test.go` covering the floor/grow/cap/wrap/
nil-screen cases using the existing `sizedScreen` test fake from
`modal_test.go`.


---

## 2026-07-30 — Mouse routing review

`mouse-routing-review-2026-07-23`

*3rd same-day review pass: fixed StatusHistoryDialog + app-level mouse routing bugs; caught a real gap in my own first fix via live testing*

Third 2026-07-23 review pass (after [[review-both-repos-2026-07-23]] and
[[review-followup-mousedrag-2026-07-23]]), via EnterPlanMode → plan file →
approved → implemented on Sonnet. Found+fixed:

1. **StatusHistoryDialog** was the one dialog the morning's ButtonNone-
   forwarding sweep missed — mirrored the same fix already applied to
   `TypedConfirmDialog`/`FileDialog` (forward release to `d.editor` before
   `ConsumeOutsideClick`).
2. **`app_events.go` `handleMouse`**: `dragNode` first-refusal block was
   sitting *below* the menu-row/status-row branches despite its own comment
   claiming otherwise — a drag crossing either row popped a menu or the
   Status History dialog mid-drag, and a release landing on either row never
   reached `dropExplorerNode`. Fixed by moving the block up, adding a
   ButtonNone broadcast to all latch-owning widgets (menuBar/toolbar/
   explorerSplit/explorer/panels) ahead of positional branching, and gating
   the status-row dialog-open on a fresh press.
3. **ctx straggler**: `database_props_permissions.go`'s
   `DatabaseExtendedProperties()` missed the ctx threading sweep because its
   name doesn't match the six `ExtendedProperties(level)` sites that sweep
   fixed — same bug class, different method name, easy to miss with a
   pattern-based grep instead of an exhaustive one.
4. **gosmo**: renamed `Database.DatabaseTriggerSeq` → `Database.TriggerSeq`
   (redundant type-name prefix broke the mechanical `FooSeq`-from-`Foo()`
   convention).

**Why:** user asked for a bugs/inconsistencies/optimization/refactor pass on
both repos, planning-only with a model-switch pause before implementation —
same request shape as the two earlier same-day passes, this time covering
what those hadn't reached (full dialog inventory, app-level router, gosmo
Context/Seq completeness).

**Real self-caught bug in my own fix, via live tmux testing:** my first pass
at #2 gated the status-row `Show()` call with a *per-row* latch
(`statusBarPressed`, set true after first open, cleared on release) —
copying the `mouseDragging` idiom's shape without its actual invariant. That
idiom works *per-widget* because the widget only ever sees events once the
press already originated there. At the App router, a drag that starts in the
query editor and merely drifts onto the status row arrives at that row as
`Button1` with the per-row latch still `false` (never having fired there
before) — so the dialog popped anyway, on the very first crossing. Caught
immediately in tmux: a synthetic press-in-editor → motion-on-status-row →
release-on-status-row sequence popped Status History every time. Fixed by
replacing the per-row bool with an App-wide `mouseButtonDown` set/cleared at
the very top of `handleMouse` from *this event's* button state (before any
dispatch) — `freshPress := ev.Buttons()==Button1 && !a.mouseButtonDown` is
then true only for the actual first press of a gesture, wherever it
originated, not just the first time *this particular row* has seen a
Button1.

**How to apply:** when porting the `mouseDragging`/fresh-press idiom from a
leaf widget (which naturally only sees events after a press starts inside
it) to a *router* that sees every event regardless of where a drag began,
the guard must be a single flag tracking "is the button down at all right
now", set at the top of the dispatcher before any positional branching — not
a per-branch/per-widget copy of the leaf pattern, which silently only guards
repeats, not the first spurious fire. Verify this class of fix with a
synthetic press-elsewhere → drag-across-target-row → release sequence in
tmux, not just a direct click on the target.

**Red herring during testing, unrelated to the fix:** spent real time
confused why clicking the toolbar's 📈 (Activity Monitor, disabled with no
connections) seemed to update the status bar — it didn't. `Toolbar.
DrawOverlay` shows a hovered button's tooltip regardless of `enabled()` (by
design, see `buttonAt`'s doc comment), and the query panel's own
`resultsNotice` line (a leftover "Not connected" from an earlier F5 press,
rendered inside the panel's own rect) sits one row above the app-wide status
bar in a 30-row terminal — visually adjacent, completely unrelated widgets.
Confirmed by reproducing identically on the pre-fix binary and by grepping
every `a.statusText =` site (only `setStatus`/`logStatus` write it). Lesson:
before concluding a click changed app state, identify which specific widget
owns the line of text that changed, especially when two adjacent rows belong
to different widgets in a plain-text capture.

Verification: `go build/vet/test` + `gofmt -l` clean in both repos; live
tmux tests for both dialog fixes (drag-select in query editor released on
status row → no popup, editor cursor lands cleanly afterward; drag-select
inside Status History's own editor released outside the dialog rect →
Close still responds first click, reopen shows clean state). No git commits
made (user instruction).


---

## 2026-07-30 — New database dialog build

`new-database-dialog-build-2026-07`

*New Database dialog built from scratch vs. ssms_new_database_tui_mockup.md (2026-07-15) — gossms's first creation (not edit-existing) dialog; found and fixed a real Script Changes bug (scripted CREATE DATABASE + read-based Database handle lookup)*

Built New Database from scratch (didn't exist before) — Object Explorer's
server node and "Databases" folder node both get a "New Database..."
context menu item (`internal/tui/app_explorer_data.go`'s
`contextMenuItemsForNode`, which previously had **no `case NodeDatabases:`
at all**, falling to `default:`). Three pages, matching the mockup's real
pages: General, Options, Filegroups.

**Why this couldn't reuse `PropDialog`** (the shared engine behind Server/
Database/Login/Table/Role/User Properties): `PropDialog`'s whole model —
`dirtyApplyFns`/`runPipeline` — is "collect only *dirty* pages, run their
independent apply closures in whatever order `DirtyPages()` returns,"
built for editing an *existing* object where every page reloads current
server state independently. New Database has no existing object: General's
apply (the actual `CREATE DATABASE`) must always run, first,
unconditionally, before Options/Filegroups' own applies can target a
database that now exists. Built a new, dedicated `NewDatabaseDialog`
(`internal/tui/new_database_dialog.go`) that still embeds
`*propsheet.PropertySheet` directly (the widget layer — Form/rows/grids/
page-list/zone-navigation — is fully reusable as-is, zero changes needed
there) but runs a **fixed three-step apply sequence** instead of a
discovered dirty set, and does **one shared prefetch** (existing database
names, server logins, `model`'s current options/recovery model/compat
level, server default data/log paths) instead of PropDialog's per-page
independent async load — built once, all three pages' forms/applies
constructed synchronously from it and cached, so visiting Options/
Filegroups for the first time is instant (no second network round trip),
and OK/Apply/Script Changes always have all three apply closures ready
regardless of which pages the user actually visited.

**The `model`-as-baseline trick**: every Options-page row's `Dirty()`
baseline is seeded from `model`'s *current* settings, not blank/zero — a
real `CREATE DATABASE` already inherits every one of these from `model`
implicitly, so "the row is dirty" now means exactly "the user chose
something other than what this database would have inherited anyway,"
and only those need an explicit follow-on `ALTER DATABASE SET`. This let
`database_props.go`'s existing `pageDatabaseOptions` row-building/apply
idiom (`dbOptSelectRow`/`dbOptBoolRow` + `tracked []dbOptRow`) be reused
almost verbatim, just fed from `model.OptionsContext()` instead of an
edited database's own. Same trick applied to General's Recovery Model/
Compatibility Level/Owner rows (also `Dirty()`-gated against `model`'s/the
connecting login's defaults) — a bug in the first draft had Recovery
Model/Compat Level *always* included in `CreateDatabaseOptions`
regardless of whether the user touched them, producing needless
`SET RECOVERY FULL`/`SET COMPATIBILITY_LEVEL = 170` no-op statements in
every Script Changes preview; fixed to gate on `.Dirty()` like everything
else, caught via visual inspection of a generated script, not live
testing.

**gosmo changes**:
1. `Server.CreateDatabaseOptions` gained `PrimaryFile`/`LogFile
   *DatabaseFileSpec` (nil = server default, unchanged from before file
   support existed) — `CreateDatabaseContext` now emits an
   `ON PRIMARY (...) LOG ON (...)` clause when either is set, reusing
   `DatabaseFileSpec` as-is. Extracted `buildCreateDatabaseStatement`
   (pure, testable, mirrors `buildAddFileStatement`'s existing shape) and a
   shared `writeFileSizeClauses`/`buildFileDefClause` helper so the
   SIZE/MAXSIZE/FILEGROWTH clause syntax is written once, shared between
   `ALTER DATABASE ADD FILE`'s flat clause list and `CREATE DATABASE`'s
   parenthesized file definitions. New table-driven test in
   `server_test.go`. Deliberately the *only* new file/filegroup capability
   at CREATE time — additional filegroups/files beyond the initial
   primary+log pair are added **after** creation via the already-existing
   `AddFileGroupContext`/`AddFileContext` (functionally identical end
   state, avoids CREATE DATABASE's multi-FILEGROUP clause syntax
   entirely, and avoids needing shared pending-filegroup-list state
   between General and Filegroups pages — General only ever has exactly
   two files, a plain field group each, not a grid).
2. **Real bug found via live testing**: `Server.Database(name) *Database`
   — a new, lightweight, no-IO handle constructor
   (`&Database{server: s, name: name}`). Before this existed, the
   Options/Filegroups pages' apply closures called
   `sc.Server.DatabaseByNameContext(ctx, dbName())` to get a `*Database`
   handle before issuing writes — a **real read** (`SELECT ... FROM
   sys.databases WHERE name = ...`), which is not part of gosmo's
   `WithScript`/`ScriptCollector` interception (only `exec`/`execContext`
   check `scriptFrom(ctx)`, not query/read methods). Clicking Script
   Changes failed outright with `gosmo: database "X" not found`, because
   General's `CreateDatabaseContext` was captured-not-executed (correctly,
   under the scripted context) so the database genuinely didn't exist yet
   when Options' apply closure tried to really look it up. Verified every
   write method this dialog uses (`SetDatabaseOptionContext`,
   `SetUserAccessContext`, `AddFileGroupContext`, `AddFileContext`,
   `SetFileGroupReadOnlyContext`, `SetDefaultFileGroupContext`,
   `SetOwnerContext`) only ever touches `d.name`/`d.server`, never the
   other cached fields (`state`/`recoveryModel`/`collation`/etc.) that a
   real lookup would populate — so the lightweight handle is exactly
   sufficient, for both the scripted path and (a minor efficiency win)
   the real-execution path too, since it skips an unnecessary read
   immediately after creating the database.

**Live-verified** end-to-end against ubudock, twice: once via Script
Changes (confirmed the generated script was minimal — only genuinely
customized fields appeared — and confirmed the `Database()` fix resolved
the "not found" error), once via a real OK-run creating
`claude_newdb_test2` with a custom 48 MB data file size, Auto Shrink
turned ON, and a new `FG_TEST2` filegroup, then re-queried directly
(`sys.database_files`, `sys.filegroups`, `DATABASEPROPERTYEX(...,
'IsAutoShrink')`) — every value landed exactly as specified, log file and
Recovery Model/Compat Level stayed at server/model defaults since
untouched. Also verified the name-uniqueness preflight check (typing
`master` and clicking OK) rejects instantly with no server round trip.
Also verified "New Database..." fires correctly from **both** the server
node's and the "Databases" folder's context menus (the two entry points
the user explicitly asked for). Disposable databases dropped afterward —
needed `ALTER DATABASE ... SET SINGLE_USER WITH ROLLBACK IMMEDIATE` first
for the second one, since a leftover query tab still had an open session
scoped to it via an earlier `USE` (Msg 3702, "currently in use") — not a
gossms/gosmo bug, just normal SQL Server session-blocking behavior worth
remembering for future cleanup after live-testing a New-Database-style
feature.

**Deferred, consistent with every prior pass**: every picker/warning/
progress modal in the mockup (owner/collation/compatibility-level/path
pickers → plain `Select`/`Text`; autogrowth modal → inline Int rows;
"Add Filegroup" modal → inline mini-form under the Filegroups page's
Add button, mirroring [[database-user-properties-mockup-build-2026-07]]'s
owned-schema-editor precedent; script preview modal → the dialog-wide
Script Changes button; create-progress modal → the existing
`SetApplying`/"Applying…" hint-row state; create-failed modal →
`SetMessage`); FILESTREAM filegroups, memory-optimized filegroups, Ledger
database, Query Store configuration, Service Broker options,
containment-related full-text/language settings, Azure SQL MI/DB
platform-adjusted view (gosmo has no support for any of these — a real,
separate gosmo gap for a future pass); multiple data files under PRIMARY
or non-PRIMARY-filegroup files at CREATE-DATABASE time (General only has
the fixed primary+log pair; extra filegroups/files go through Filegroups,
post-create); "Use full-text indexing" checkbox (vestigial in modern SQL
Server, no gosmo action to wire it to); no gosmo method exists to list
available server collations, so Collation is a plain free-text field, no
picker — same "no picker modal" simplification as every principal/schema/
path field in every prior pass.


---

## 2026-07-30 — Object explorer detail async

`object-explorer-detail-async-2026-07-19`

*Object Explorer Detail rebuilt for async/progressive load + caching; server CPU/Mem/NUMA/disk, per-db sizes, Logins listing; 2 gosmo additions, 1 near-miss wakeEventLoop bug caught by live testing*

Built 2026-07-19: Object Explorer Details (`DetailBrowser`) gained a per-node
result cache (keyed by `*explorerNode` pointer — a folder reload naturally
replaces child node objects, which invalidates their cache entries for
free; the *currently viewed* node's own pointer survives its own "refresh"
action though, so every refresh call site — F5/`RefreshSelected`, the
context-menu `refresh` item, `RefreshDatabasesFolder`/`RefreshLoginsFolder`
— explicitly calls the new `DetailBrowser.Invalidate(app, node)`), plus
progressive multi-stage loading for the four node kinds the user asked for:

- **Server node**: CPU count + physical memory are free (already cached on
  `gosmo.Server.Info()` from connect time) — shown instantly. NUMA node
  count, available memory, and per-volume disk free space are genuine extra
  DMV round trips, backfilled after.
- **Databases folder**: Name/State/Recovery shown as soon as the one list
  query returns; each database's Total/Data/Log/Avail. Data/Avail. Log (MB,
  thousands-separated) backfills its own row concurrently (one goroutine
  per db, reusing the `*gosmo.Database` handles already returned by the
  list call — no redundant `DatabaseByName` lookup).
- **Database node**: extended with Data/Log/Avail. Log rows alongside the
  existing Size.
- **Logins folder**: was previously an unhandled default case showing
  nothing useful; now lists Name/Type/Status/Default Database/Created from
  one `Server.Logins()` call.

**gosmo additions** (both live-tested against [[gossms-live-test-server]]):
`SpaceInfo` gained `AvailLogMB` (same `FILEPROPERTY(name,'SpaceUsed')`
technique as the existing `UnallocatedMB`/avail-data, just filtered to
`type_desc = 'LOG'` instead of `<> 'LOG'`) — **FILEPROPERTY's 'SpaceUsed'
works correctly for LOG files too**, contrary to folklore that it's
data-file-only; verified the number against the
`Log File(s) Used Size (KB)` perf counter as a cross-check, they agree.
`Server.DiskVolumes()/DiskVolumesContext()` is new, querying
`sys.master_files CROSS APPLY sys.dm_os_volume_stats(...)` — genuinely
cross-platform (same DMV shape on Windows and Linux SQL Server) but **on
this containerized Linux test instance, `volume_mount_point` and
`logical_volume_name` both come back as empty strings, not populated** — so
the struct also carries a `SamplePath` (one file's `physical_name` on that
volume) as a display fallback when both real names are blank.

**tuikit additions**: `core.FormatThousands(int64) string` (no dependency
added — hand-rolled rather than pulling in `golang.org/x/text/number` for
one function). `controls.DataGrid.RefreshColumnWidths()` — recomputes
column widths from mutated-in-place row data *without* resetting
scroll/selection the way `SetData`/`SetSource` do; needed because a
progressive per-row backfill (Databases folder) mutates cells directly on
the same `[][]string` backing array already installed via `SetData` (Go's
`SliceRowSource(rows)` conversion doesn't copy — mutating `rows[i][j]`
after the fact is visible on next `Draw` for free) and calling `SetData`
again on every row's arrival would jump the user's scroll position back to
top on every single completion.

**A live-testing catch worth remembering**: the first version of
`loadServerDetails` computed its "instant" first-stage rows (from
`sc.Server.Info()`, already cached, no query) *synchronously* on the
calling goroutine — which is the UI goroutine, since `ShowNodeDetails` is
called directly from the keypress/mouse-click handler — then called
`postPartial` (which ends in `app.wakeEventLoop()`) from there. This
technically violates the documented rule in this project's CLAUDE.md
("`wakeEventLoop` must be called from the background goroutine, calling it
from the UI goroutine itself would deadlock") — except it didn't visibly
deadlock; tcell's `EventQ()` channel apparently has enough buffer that the
self-send doesn't block outright. What it actually caused, live-tested via
tmux (see [[gossms-tui-tmux-testing]]), was the exact symptom the CLAUDE.md
history describes for this bug class: selecting the Server node showed
nothing until an unrelated subsequent keypress. Fix: wrap the *entire*
loader body, including the "instant" first stage, in one `go func(){}()` —
matching the shape the Databases-folder and Logins-folder loaders already
had from the start. **How to apply**: when writing a new progressive/
staged async loader in this codebase, never let any stage's `postPartial`/
`postFinal`/`wakeEventLoop` call happen synchronously on the goroutine that
initiated the load — always inside a `go func(){}()`, even for a stage that
itself does no I/O and "should be instant."

Server Properties' Database Settings page also gained a "Disk space"
section using the same new `DiskVolumes()` call, between "Default
locations" and "Recovery".


---

## 2026-07-30 — Object explorer login and connect error dialog

`object-explorer-login-and-connect-error-dialog-2026-07-24`

*Object Explorer root label now uses gosmo SUSER_NAME() instead of Opts.User; Connect dialog failures now show an AlertDialog, not just status bar text*

Two small, user-requested fixes, both live-tested against the real ubudock
server (see [[gossms-live-test-server]]):

1. **Object Explorer login display**: added `gosmo.Server.CurrentLogin()`/
   `CurrentLoginContext()` (server.go, mirrors the existing
   `CurrentDatabase`/`CurrentDatabaseContext` pattern — `SELECT
   SUSER_NAME()`). `db.ServerConn` gained a `Login string` field, populated
   once in `Connect()` right after `gosmo.Connect` succeeds. `ServerConn.Label()`
   (internal/db/connection.go) now prefers `sc.Login` over `sc.Opts.User`,
   falling back to `Opts.User` then the auth-method name if the query
   failed/was empty. Live-verified genuinely: created a disposable login
   `TestClaudeLogin`, connected typing the lowercase `testclaudelogin`, and
   the Object Explorer root showed the canonical-cased `TestClaudeLogin` —
   proof it's really querying the server, not echoing `Opts.User` back.

2. **Connect error dialog**: `connectServer` (internal/tui/app_connections.go)
   now calls the already-existing `a.alertDialog.ShowAlert("Connection
   Error", ...)` (the same `*dialogs.AlertDialog` used for the Activity
   Monitor stub) in addition to the existing `setStatus` status-bar
   message, on both the typed `*db.ConnectionError` and generic-error
   branches. Live-verified with a wrong password against ubudock — dialog
   showed "Could not connect to ubudock: gosmo: ping: mssql: login error:
   Login failed for user 'sa'." with an OK button; status bar also updated.
   Scope was deliberately narrow — did not change ConnectDialog's
   Hide()-before-connect timing (SSMS keeps the dialog open on failure for
   retry; this wasn't asked for and would be a bigger UX change).

**Found but NOT fixed (out of scope, flagged to user)**: disconnecting a
server root shortly after connecting can leave a real SQL session alive for
up to `completionInventoryTimeout` (30s, completion_inventory.go) — the
background sys-schema completion-inventory load kicked off by
`ensureSysCompletionInventory` in `connectServer`/`connectForQueryPanel`
uses `context.Background()` with its own timeout, not tied to `sc`'s
lifetime at all, so `disconnect()`'s `sc.Close()` only closes *idle* pooled
connections — an in-flight catalog query keeps its connection (and SQL
Server session) alive until it finishes or times out. Reproduced live:
disconnected a login immediately after connecting, then `DROP LOGIN` from
another session failed with "user is currently logged in" against a
session that only disappeared after ~30s / an explicit `KILL`. This is the
same class of gap as the many "background goroutine not tied to connection
lifetime" bugs already fixed elsewhere in the app (see
[[full-review-implementation-2026-07-23]] and the `wakeEventLoop`/postEvent
async-pattern list in CLAUDE.md) — a real fix would need a per-`ServerConn`
cancellable context threaded through `ensureSysCompletionInventory`/
`ensureCompletionInventory` and cancelled in `disconnect()`, which is a
larger, separate piece of work, not attempted here.

**How to apply:** if the user reports "can't drop/alter a login right after
disconnecting it in gossms," this is the known cause — either wait ~30s,
`KILL` the session manually, or (better) do the cancellable-context fix
described above as its own task.


---

## 2026-07-30 — Offline db fix and new login

`offline-db-fix-and-new-login-2026-07-15`

*Second 2026-07-15 pass: fixed offline-database Object Explorer expand showing a raw error cascade; built New Login dialog from scratch (mockup-driven), needed one new gosmo capability (Server.Login lightweight handle)*

Two more items in the same day as [[four-fixes-2026-07-15]], same session continued after a `/compact`.

**1. "An offline database's tree node is collapsed and cannot be opened."**
Root cause, found only after extensive live reproduction (several plausible
hypotheses — stuck expand, connection-pool exhaustion, a hang in
`DatabaseByName` — were tested live and ruled out one by one before landing
on the real one): `loadDatabaseChildren` (`internal/tui/explorer_databases.go`)
never actually depended on the database being reachable — it always
returned the static Tables/Views/.../Security folder list regardless of
state, so the *database node itself* always expanded fine. The real,
reproducible problem was one level deeper: expanding any of those folders
(Tables, Views, Users, ...) against an *offline* database runs gosmo's
`Database.withConn`, which does `USE [dbname]` first — and SQL Server
rejects that outright for an offline database (Msg 942), so every single
folder shows its own raw wrapped Go error the moment it's expanded. Fixed
by making `loadDatabaseChildren` check `node.data.IsOffline` (the field
added by [[four-fixes-2026-07-15]]'s Take Offline feature) and, when true,
short-circuit straight to one leaf node reading `⚠ (Database is offline)`
instead of the normal eight-folder list — matches real SQL Server
authority (nothing under an offline database is genuinely browsable) while
avoiding the "expand eight folders one at a time to discover they all fail
the same way" bad UX. Zero `tuikit` changes; the database node itself is
still expandable (matching SSMS convention of not hiding the affordance),
it just now shows the true state immediately. Verified live end-to-end:
created a disposable database, took it offline (via the existing feature),
confirmed the tree previously showed 8 individually-failing folders,
rebuilt with the fix, confirmed it now shows the single clear leaf.

**2. New Login dialog**, `todo/mockups/ssms_new_login_tui_mockup.md`,
Security > Logins context menu. Built from scratch, same architecture as
[[new-database-dialog-build-2026-07]] (a dedicated `NewLoginDialog` type
wrapping `propsheet.PropertySheet`, one prefetch, a **fixed** apply
sequence — General's `CREATE LOGIN` always runs first, not a `PropDialog`
dirty-diff — Script Changes via `gosmo.WithScript`), not `PropDialog`. 5
pages matching the mockup's own "common SSMS pages" list: General, Server
Roles, User Mapping, Securables, Status — every other mockup screen
(principal-search modal, password-validation modal, default-database
picker, high-impact-role warning, add-securables modal, effective-
permissions modal, script/progress/failure modals) deliberately folded
into inline fields/existing `SetMessage`/Script-Changes mechanisms, the
same simplification made on every prior dialog in this project.

- **Needed exactly one new gosmo capability**: `Server.Login(name) *Login`
  (`server.go`), a lightweight no-IO handle mirroring `Server.Database(name)`
  — every write method on `*Login` (`AddServerRoleMemberContext`,
  `SetPasswordPolicyContext`, `DisableContext`, ...) only ever touches
  `l.server`/`l.Name`, both unexported, so gossms (a different package)
  had no way to construct a `*Login` for a login it just created without
  a real re-lookup query. `CreateLoginContext`/`CreateLoginOptions`
  themselves needed **no changes at all** — already fully supported SQL
  Server auth + Windows auth (password `""` = `FROM WINDOWS`), which is
  all this dialog offers.
- **Deliberately deferred: External Provider (Entra ID/Azure AD) auth.**
  ubudock is a plain Linux SQL Server, not Azure SQL MI, so `FROM EXTERNAL
  PROVIDER` can't be live-tested there — same "declare the platform gap,
  implement the testable subset" judgment made for FILESTREAM/Ledger/Query
  Store platform features in [[new-database-dialog-build-2026-07]]. Not
  implemented in gosmo either; a real, separate future gap.
- **Password policy/expiration apply via existing `SetPasswordPolicyContext`
  post-create, not new `CREATE LOGIN`-time options** — seeded the two
  checkboxes' baselines to SQL Server's *real* defaults (policy=ON,
  expiration=OFF) so the existing `Dirty()`-gated apply pattern (from
  [[new-database-dialog-build-2026-07]]'s Options page) produces a minimal
  Script Changes output with no ALTER LOGIN at all when untouched —
  verified: Script Changes for an untouched-defaults login showed only
  `CREATE LOGIN ... WITH PASSWORD = ...` and nothing else.
- **User Mapping page's "User" column bug, found and fixed during this
  session's own live testing**: showed *blank* for every row instead of
  the typed login name. Cause: all 5 pages' `propsheet.Form`s are built
  **once**, synchronously, right after the one prefetch completes —
  before the user has necessarily typed anything on General — and a
  `DataGrid`'s cell text set via `SetData` never gets recomputed on its
  own when the row is later revisited; there is no "page shown again"
  hook in `PropertySheet` to rebuild it from a live closure. Since the
  mapped username is never independently editable anyway (mirrors
  `pageLoginUserMapping`'s own existing behavior for an *existing* login —
  no username `TextRow` there either, just Database/Default schema), fixed
  by displaying a fixed placeholder (`"(same as login name)"`) instead of
  attempting to show the live value — always true, never stale. `apply`
  itself reads `loginName()` fresh at apply time, so the real created
  username was correct even before this display fix; this was purely a
  displayed-text bug, not a data-correctness one.
- Reused, verbatim in spirit: `mapCell`/`serverRoleImpact`/
  `fixedServerRoleDescriptions`/`displayPermState`/`nextPermState`/
  `connectPermissionItems` (all already package-level in
  `prop_grid_helpers.go`/`login_props.go`), and Login Properties' own
  page-building idioms for Server Roles/User Mapping/Securables/Status —
  just with an empty/all-false baseline (nothing exists yet) instead of a
  real login's current state, and applying only checked/changed rows.
- New `ObjectExplorer.RefreshLoginsFolder(sc)` (`object_explorer.go`),
  mirroring [[four-fixes-2026-07-15]]'s `RefreshDatabasesFolder` one level
  deeper (Security > Logins, not directly under the server root) — wired
  into `NewLoginDialog.runApply`'s success path so a newly created login
  shows up immediately if the folder is already expanded.
- Verified end-to-end against ubudock: created `claude_test_login` (SQL
  auth, default policy/expiration, mapped to `HealthClinic` with default
  schema `dbo`), confirmed via `sys.server_principals`/`sys.sql_logins`
  (`is_disabled=0`, `default_database_name=master`, `is_policy_checked=1`,
  `is_expiration_checked=0`) and `HealthClinic.sys.database_principals`
  (`SQL_USER`, `default_schema_name=dbo`); confirmed Script Changes preview
  was minimal/correct before running it for real; confirmed the tree
  showed the new login under Security > Logins without a manual refresh;
  dropped the disposable login/user afterward.

**New tmux-testing gotcha, worth adding to
[[gossms-tui-tmux-testing]]-style knowledge**: typing into a
`propsheet.PropertySheet`-based dialog's fields **immediately** after the
key that opens it (before its one background prefetch completes and
"Loading…" clears) silently drops every keystroke — the fields render
correctly once loading finishes, but whatever was typed during the loading
window is simply gone, with no error. Always capture-and-check for
"Loading…" having cleared before sending any fill-in keystrokes to a newly
opened New Database/New Login-style dialog, not just after the dialog
visually appears.

Both repos build/vet/test clean (`gofmt -l .`, `go build ./...`,
`go vet ./...`, `go test ./...` in both `~/go/gosmo` and `~/go/gossms`).
gosmo's `replace ../gosmo` directive in gossms's `go.mod` is still
active/untagged (unchanged from prior sessions) — still pending the user's
go-ahead to tag and push a new gosmo version.


---

## 2026-07-30 — Planview bottom properties warnings

`planview-bottom-properties-warnings-2026-07`

*Fixed planview's Tree-tab bottom Properties never showing Warnings, a dead scroll field alongside it, and a deeper showplan parser bug (boolean attrs as \"1\"/\"0\" not just \"true\"/\"false\") that dropped real warnings/Parallel entirely*

2026-07-16, same day as [[estimated-execution-plan-binding-2026-07]]: user
report "the warning texts are not shown in the detail view. when a control
is marked as warning show the warning text" turned out to be about a
*different* pane than the one already fixed that day. `planview` has two
distinct "properties" views for a selected operator:

1. The **"Operator Details" side pane** (Tree tab) / **"Properties" strip**
   (Plan tab) — both driven by `detailKVs`/`detailLines`
   (`internal/tui/planview/details.go`), which always synthesizes a
   "Warnings" row (even "—" when empty). This one was already correct —
   verified live against both `cmd/plandemo`'s fixture and a real ubudock
   query before touching anything.
2. The **Tree tab's bottom "Properties" section** (`'o'` to cycle:
   hidden → properties → summary), which renders `n.Props` — a literal,
   unmodified mirror of the plan XML's own attributes
   (`showplan.Node.Props`, built by `appendAttrs` in
   `internal/showplan/parse.go`). `decodeWarnings` consumes the
   `<Warnings>` XML element into `n.Warnings` separately and never
   appends to `Props`, so this raw-attribute view structurally could
   never show warning text, no matter how a node was marked (❌/⚠
   border+badge on its tile/tree row).

Fixed in `internal/tui/planview/details.go` via a new
`nodePropsForDisplay(n)` — returns `n.Props` verbatim when there are no
warnings, else a fresh slice with a synthetic `{Warnings, joined text}`
KV **prepended** (visible without scrolling) and styled in the warning
color, same convention `drawDetails` already used. Deliberately did not
mutate `n.Props` itself — its doc comment promises a literal XML mirror,
and other code (Copy, presumably future XML-diffing work) may rely on
that.

**Found a second, compounding bug while fixing the first:** the bottom
Properties section's own scroll field, `propsSt.scroll`, was completely
dead — `handleTreeTabMouse`'s `WheelUp`/`WheelDown` cases only checked
`treePaneRect` and `detailsPaneRect`, never `bottomRect` while
`bottomMode == bottomProperties`. Any node with more attributes than the
section's fixed height (`bottomSectionHeight = 12`, so ~11 data rows) had
the rest permanently unreachable — confirmed live: a `Clustered Index
Scan` node's raw attributes (`AvgRowSize`, `EstimateCPU`, ... down through
`PhysicalOp`, `EstimatedTotalSubtreeCost`, `TableCardinality`) ran well
past 11 entries. Fixed by wiring `scrollBottomProps` (mirrors the existing
`scrollDetails`/`scrollGraphProps` wheel-only pattern — no keyboard
binding either, matching those two) and adding the same
`detailsHeaderText`-style "Scroll ▲▼" hint to this section's header,
which it was missing (the other two panes already had it).

**How to apply:** this is now the *third* time in this project a real
`planview` bug survived unit tests and prior manual checks and only
surfaced from close, deliberate live re-testing of something that looked
done — see [[indent-trailing-newline-bug-2026-07]],
[[planview-tree-focus-model-bug-2026-07]], and
[[estimated-execution-plan-binding-2026-07]]'s root-tile-scroll bug from
earlier the same day. When a user reports something not showing "in the
detail view," check whether `planview` has more than one view claiming
that name before assuming it's the same one already fixed — `detailKVs`
(curated) and `n.Props` (raw XML mirror) are two genuinely separate code
paths that both call themselves "Properties" in the UI, and a fix to one
says nothing about the other.

**Immediately after this fix shipped, the user reported it still didn't
work** — supplied a database-neutral repro query (`sys.all_objects`
cross-joined against itself + `sys.all_columns`, no app schema needed) to
get a real plan with warnings on demand. That exposed a deeper,
*upstream* bug in `internal/showplan/parse.go`, not another UI gap:
`decodeWarnings` only recognized a `<Warnings>` boolean attribute whose
value was the literal string `"true"` — but the live server (SQL Server
17.0.4055.5) emits `NoJoinPredicate="1"` for a CROSS JOIN's Nested Loops
operator, and `Parallel="0"`/`"1"` the same way on every `RelOp`, not
`"false"`/`"true"`. XSD's boolean lexical space allows both `{true,
false}` and `{1, 0}` — different SQL Server builds/attributes apparently
use either — so the `"1"` form was silently parsed as "no warning at
all," no error, nothing to notice. Fixed with a new `attrBool(el, name)`
helper (accepts both forms) used by both `Node.Parallel` and
`decodeWarnings`'s attribute loop — `Parallel` had the exact same latent
bug, just never reported because it only affects a border-case display
(the Properties row's "Yes"/"No" and, for the badge work earlier this
day, the tile/row's ⇄ icon), not a big obviously-missing chunk of
content the way Warnings did. Regression test:
`TestParse_BooleanAttrsAcceptXSDOneZeroForm` in
`internal/showplan/parse_test.go`, built from a minimal inline XML
fixture rather than checking in the full live-captured document.

**Reusable technique:** when live-testing needs a specific plan shape
(warnings, parallelism, a particular operator) and no existing app table
naturally produces it, a tiny throwaway `go run` script calling
`gosmo.ConnectContext`/`sc.Database(name).EstimatedPlanContext` directly
and printing `plan.XML` to a file is much faster than driving the TUI
through tmux to *find* the right node — reserve the tmux/live-app dance
for confirming the fix actually renders correctly, not for XML spelunking.
`gosmo.ConnectionOptions` takes `Server: "host:port"` (not a separate
`Port` field) and `Auth: gosmo.AuthSQLServer` (not `gosmo.SQLAuth`);
`gosmo.ConnectContext(ctx, opts)` returns `*Server` directly (no `.Server`
field to unwrap).


---

## 2026-07-30 — Planview tree focus model bug

`planview-tree-focus-model-bug-2026-07`

*A dedicated test (not tmux) caught a second real PlanView bug — Enter was unconditionally intercepted by the tree's own expand/collapse handler, starving the summary table's jump-to-node action*

While building the Tree tab of the execution-plan viewer (2026-07-16,
Phase 3 of [[execution-plan-viewer-design-2026-07]]), a state-level test
(`TestSummary_SortAndJumpToTree` in `internal/tui/planview/tree_test.go`)
caught a real bug before any visual check was needed: `handleTreeTabKey`'s
`switch ev.Key() { case tcell.KeyEnter: v.toggleSelectedExpand() ... }`
ran unconditionally, so pressing Enter while the Summary bottom section
was open always toggled the tree's own selected node instead of ever
reaching the summary grid's "jump the tree selection to this row" action
— there was no focus concept distinguishing "the tree has keyboard focus"
from "the bottom section has keyboard focus".

**Fix:** added an explicit `bottomFocused bool` field, toggled by Tab
only while the Summary section is shown (Properties has nothing to
navigate). Sort keys (c/r/t) stay reachable regardless of focus — they
don't collide with anything the tree binds — but Up/Down/PgUp/PgDn/Enter
route to the summary grid only while `bottomFocused` is true, and Enter
clears it afterward (jump-and-return, like k9s/lazygit's convention).

**How to apply:** whenever a control has two or more independently
navigable regions sharing the same key namespace (arrows, Enter), an
explicit focus-zone flag is required from the start — don't assume "the
active tab" is a fine enough granularity. This complements
[[indent-trailing-newline-bug-2026-07]]: that one was caught by visual
inspection because the test assertions were too weak; this one was caught
because the test asserted the actual cross-component *outcome*
(`selectedID` changed) rather than just "HandleKey returned true".


---

## 2026-07-30 — Propsheet form wheel scroll fix

`propsheet-form-wheel-scroll-fix-2026-07`

*Fixed a real bug: mouse wheel over a grid inside a Properties dialog scrolled the whole form instead of the grid (2026-07-15)*

**User-reported bug, reproduced and fixed 2026-07-15**: in any `propsheet`-based
Properties dialog with a grid control (`GridRow`/`ToggleGridRow`, e.g. Login
Properties' Server Roles/User Mapping/Securables pages, or any other
Properties page built on `propsheet.NewGridRow`/`NewToggleGrid`), mouse wheel
scrolled the whole dialog's form instead of the grid underneath the pointer —
even with the cursor sitting inside the grid's own rect.

**Root cause**: `propsheet.Form.HandleMouse` (`internal/tuikit/propsheet/form.go`)
handled `tcell.WheelUp`/`WheelDown` in a `switch` at the very top of the
function, unconditionally scrolling `f.scroll` (the form's own row-index
scroll) before ever checking mouse position against the row bands built
during `Draw`. `controls.DataGrid.HandleMouse` already has its own correct
internal wheel-scroll logic (`datagrid.go`'s `case tcell.WheelUp/WheelDown`,
gated on `g.rect.Contains(mx, my)` so it declines events outside its own
bounds) and `GridRow.HandleMouse` already forwards straight to it — the grid
side of the plumbing was always correct and never got a chance to run,
because `Form` stole every wheel event before dispatch.

**Fix**: `Form.HandleMouse` now locates the row under the pointer first (new
`Form.rowAt` helper, reusing the existing `bands` slice) and gives it first
refusal via its `MouseHandler.HandleMouse`, for `WheelUp`/`WheelDown`
specifically. Only if that row isn't a `MouseHandler` or declines does
`Form` fall back to scrolling itself. Every other row type (`StaticRow`,
`CheckRow`, `TextRow`'s `widgets.InputField`, `SelectRow`'s `DropDown`,
`RadioRow`, `ButtonsRow`) already returns `false` from `HandleMouse` for
non-`Button1` events, so wheel-over-those-rows still falls through to
scrolling the form exactly as before — the fix is additive, narrowly
targeted at rows with their own internal scroll (currently only
`GridRow`/`ToggleGridRow`, since `ListBox`/`TreeView` are never embedded as
Form rows, only used standalone by `PropertySheet`'s own page list).

**Live-verified** via tmux against Login Properties' Securables page on
`sa`@ubudock (35-row permission grid, only ~9 rows visible): a raw SGR wheel
event (`\033[<65;COL;ROWM`, xterm wheel-down code 65) at a screen position
inside the grid scrolled the grid's own visible permission list by one row
(`ADMINISTER BULK OPERATIONS` dropped off top, `ALTER ANY LOGIN` appeared at
bottom) while the Section header above the grid ("Explicit server-level
permissions"), the page list, and the grid's own header row stayed in
exactly the same screen position — confirming the form itself did not
scroll, only the grid did.

**Confirmed to cover every Properties dialog, not just Login**: all six
SSMS-style Properties dialogs (Server, Database, Login, Table, Database
Role, Database User) are built on the exact same shared `tui.PropDialog`
(`internal/tui/prop_dialog.go`), which embeds `*propsheet.PropertySheet`
directly with no per-dialog override — `internal/tui/app.go` constructs
**one** `PropDialog` instance (`a.propDialog`), reused for every one of
them (`app_panel_actions.go`'s six `show*Properties` methods all call
`a.propDialog.show(...)`). So the fix in `Form.HandleMouse` is structural,
not per-page — there was nothing to change in `table_props.go`,
`database_props.go`, `role_props.go`, `user_props.go`,
`login_props.go`, or `server_props.go` themselves. Spot-verified live
against a second, different case beyond Login's Securables page — Server
Properties' Permissions page, which stacks **two independent grids in one
form** (`Principals`, a plain row-selection `GridRow`, 28 rows; `Explicit
permissions for <principal>`, a cell-cursor `GridRow`, 35 rows, below a
`Section` header): wheeling over the Principals grid scrolled only that
grid's rows (Section header and the second grid below stayed put), and
wheeling over the Explicit-permissions grid scrolled only that grid,
confirming the fix correctly resolves to whichever grid is under the
pointer even when a form hosts more than one.

**How to apply**: any future `propsheet` row type that wraps something with
its own internal vertical scroll (a future `ListBoxRow`, embedded
multi-line `Editor`, etc.) gets this behavior for free as long as its
`HandleMouse` follows the same contract — return `true` only when it
actually consumed the wheel event at that position, `false` otherwise —
per [[gossms-keyboard-conventions]]'s existing rule, which this bug is a
mouse-side instance of (that memory only called out `HandleKey` traps;
`Form.HandleMouse`'s own wheel handling was the trap here, not a leaf
widget's).


---

## 2026-07-30 — Quit and confirm data loss

`quit-and-confirm-data-loss-2026-07-29`

*Third review pass 2026-07-29 — Ctrl+Q and Escape-on-confirm both silently discarded unsaved query panels; ConfirmDialog gained a three-way Yes/No/Cancel variant, plus a pre-existing ModalDialog latch bug that froze every reopened dialog's first click*

Third pass on 2026-07-29, after
[[review-both-repos-2026-07-29]] and [[gesture-ownership-rule-2026-07-29]].
Nothing found in gosmo this round. Six fixed in gossms, all live-reproduced
against ubudock first:

- **Ctrl+Q / File > Exit discarded every unsaved query panel with no prompt.**
  `App.quit()` was unconditional, even though closing *one* panel had always
  asked. New `requestQuit` → `dirtyQueryPanels` → `askSaveBeforeQuit`, which
  recurses through the dialog's own callback (each prompt must be answered,
  and a Yes must finish writing, before the next is asked).
- **Escape on "Save before closing?" threw the query away.**
  `ConfirmDialog` mapped Escape to `finish(false)` = No, and for this one
  question No is the destructive answer. Checked all six `ShowConfirm` call
  sites — the other five ("Discard Changes" ×2, Take Offline, Save As, agent
  confirm) are all safe on false. New `ShowConfirmCancel` (Yes/No/Cancel,
  Escape → Cancel) used by Close Query and the new exit prompt only.
- **`ModalDialog` latch survived into the next showing** — pre-existing, and
  the one I nearly misdiagnosed as bad test coordinates. See
  [[modal-dialog-latch-across-showings-2026-07-29]].
- **Status bar stuck on "Executing query..." forever** after closing a panel
  mid-query: `runQuery`'s callback correctly bails on `!panelHosted(p)`, but
  `setResult` was the only thing that ever replaced the message.
- **Editor had no horizontal scrollbar** — the same gap just fixed in
  DataGrid, in the widget directly above it. Unlike the grid it needs a
  reserved row (`contentH()`), not an overdraw: a whole line of hidden text
  is too much to give up. Its scroll unit *is* rune columns, so
  `core.HandleScrollbarDragH` fits directly, unlike DataGrid's.
- **Results status line was blank on the Text/Plan/Messages tabs** — it lived
  on the DataGrid, which isn't drawn there. Now `QueryPanel.resultsStatusText`
  with a panel-owned `statusRect` for the other three.

**Why:** the two data-loss paths had shipped for a long time and no test
covered either; both were found in under a minute of actually pressing keys.

**How to apply:** when a Yes/No question's *No* commits to something
irreversible, it needs three buttons — Escape has to be able to mean "I
didn't mean to ask". Also: `dirtyQueryPanels`/`askSaveBeforeQuit` re-check
`panelHosted` and `Dirty()` as they reach each panel, since an earlier
answer can close or clean a later one. `quit()` now nil-guards `screen` so
headless tests can assert on `a.quitting` — same convention as `setStatus`.
See [[gossms-tui-tmux-testing]].


---

## 2026-07-30 — Results to text editor

`results-to-text-editor-2026-07`

*Query > Results To Text now renders into a read-only Editor widget (line numbers, selectable, copyable) instead of flattening rows into the DataGrid*

`QueryPanel` (`internal/tui/query_panel.go`) gained a third results-area
sub-widget, `resultsText *controls.Editor` (read-only), alongside the
existing `results` (DataGrid) and `messages` (read-only Editor, Messages
tab). Previously Results To Text flattened each row into a single
pipe-joined string and displayed it *inside the DataGrid* as a one-column
grid — a workaround the code's own comment called out as "a distinct,
genuinely plain-text-shaped presentation without a separate text widget".
User asked for a real editor control instead (line numbers, read-only,
selectable, copy) — same shape Messages already had, just never extended
to Results To Text.

**Pattern to reuse:** `results`/`messages`/`resultsText` all share the same
rect (`layoutChildren`); exactly one draws/receives input at a time, chosen
by `onMessagesTab()` / new `textTabActive()` (`!onMessagesTab() &&
resultsMode == ResultsModeText`). Every dispatch point that used to be a
2-way `onMessagesTab()` branch (`Draw`, `HandleKey`, `HandleMouse`,
`updateResultsStatus`, `clipboard.go`'s `activeClipboardTarget`) became a
3-way switch adding the `textTabActive()` case — grep for `onMessagesTab`
in `query_panel.go`/`clipboard.go` to find every site if a 4th results view
is ever added.

New `formatResultsAsText(set query.ResultSet) string` builds the actual
SSMS look: header row, dashed separator, then data rows, each column
`core.PadRight`-ed to its widest value — real column alignment, not just
pipe-joining. Ctrl+C/Ctrl+A copy works for free since `controls.Editor`
already implements `clipboardTarget` (`HasSelection`/`SelectedText`/`Cut`/
`Paste`/`SelectAll`) and `Cut`/`Paste` already no-op under `SetReadOnly`.

No bugs found — first-attempt-correct, verified with real query results
against [[gossms-live-test-server]] (ubudock/HealthClinic) via tmux: Ctrl+A
highlighted every line, Ctrl+C reported "Copied to clipboard", typing while
selected did nothing (read-only held), and switching back to Results To
Grid still rendered correctly.


---

## 2026-07-30 — Review both repos

`review-both-repos-2026-07-23`

*Full review of both repos' pending changes; 3 real bugs found (all live-confirmed on ubudock) + 3 consistency fixes, all fixed same day*

2026-07-23 review of all uncommitted work in gossms + gosmo (~13k lines). Baseline was clean (build/vet/gofmt/tests); most files had been reviewed in earlier per-feature sessions, so the new findings were in the *untested corners*:

1. **gosmo `sp_add_category @type`**: `LOCAL` is only valid for JOB class; ALERT/OPERATOR require `NONE` ("valid values are: NONE", live). Fixed via `addCategoryType(class)`.
2. **gosmo `SetCategory("")` on Alert/Operator**: msdb's `sp_verify_category` rejects `''` (Msg 14262); clearing requires the real `[Uncategorized]` category (id 98/99). **Jobs are different: `sp_update_job @category_name=''` succeeds** — only alert/operator needed the mapping. Never hit before because operator writes are blocked on ubudock (Agent XPs=0) and an uncategorized alert reads back as `[Uncategorized]` via the join, so "(None)" was rarely the loaded state.
3. **gossms Job Properties Steps multi-delete**: `sp_delete_jobstep` renumbers later steps' step_ids down by one (live-verified), so the old interleaved apply loop deleted/updated the wrong steps when >1 delete (or delete + edit-of-later-step) shared one Apply. Fixed with three passes: updates first (IDs still valid), deletes in descending step_id, adds last.

Consistency fixes: `Statistic` iter methods renamed to `ColumnSeq`/`DensityVectorSeq`/`HistogramSeq` (were `StatisticColumnSeq`… breaking the `Table.ColumnSeq` convention); stale `IndexFragmentation` doc ("always SAMPLED" — it takes a mode); histogram `RANGE_HI_KEY` NULL rendered as `<nil>` → now `NULL`, []byte → 0x-hex.

**Why:** the three real bugs all lived in paths no live test had exercised — the lesson from [[sql-agent-schedule-alert-operator-props-2026-07]] ("environment quirk" blocked operator writes) is that a blocked live test leaves the path unverified, not verified-by-absence.

**How to apply:** when a live-test pass skips a write path for environment reasons, probe the equivalent msdb proc directly via sqlcmd against [[gossms-live-test-server]] (works even with Agent XPs off for *alert* procs); prefix scratch objects `revw_` and always verify zero leftovers after.


---

## 2026-07-30 — Review both repos

`review-both-repos-2026-07-24`

*Full review of both repos' uncommitted work (scrollbar-drag sweep + gosmo Server-retry/allowlist sweep); 3 real bugs fixed, 1 audit finding correctly reversed after verification*

2026-07-24 review of all uncommitted work in gossms + gosmo, continuing the
same "3 parallel Explore-agent audits" method as
[[full-review-implementation-2026-07-23]] (gosmo diff; gossms
`internal/tuikit` diff; gossms `internal/tui` diff), plus a direct personal
read of `internal/tui/planview`'s diff and `internal/tuikit/core/drawing.go`'s
new scrollbar helpers, which no single agent had deeply covered. Baseline was
clean (build/vet/gofmt/test/-race all green) before and after.

**Real bugs found + fixed:**
1. **gosmo SQL injection — `partition.go` `CreatePartitionFunctionContext`**:
   `req.InputType` was spliced unguarded into `CREATE PARTITION FUNCTION`,
   the one sibling this diff's `validDataType` sweep (table.go,
   sequence_synonym.go) missed. `req.Boundaries`/`SplitRange`/`MergeRange`'s
   `value` were also raw-interpolated with zero validation. Fixed with
   `validDataType` + a new `validPartitionBoundary` regex allowlisting
   literal *shape* (int/decimal/hex/quoted-string/NULL) since boundary
   values can't be parameterized in DDL and aren't a fixed enum.
2. **gosmo consistency — `agent_category.go` `CategoriesContext`** missing
   the `validCategoryClass` guard its siblings `CreateCategoryContext`/
   `DeleteCategoryContext` already had.
3. **gossms perf bug — `Editor.HandleMouse` wrap mode**: every `Button1`
   event called `buildVisualLines()` (O(document length), explicitly
   "not cached") once for the scrollbar hit-test then again inside
   `handleMouseWrapped` — doubled per-event cost during a drag-select over a
   large wrapped document. Fixed by threading the precomputed slice through
   `handleMouseWrapped`'s signature instead of it recomputing.

**Also done (optional items the user approved as part of "do ALL"):**
gosmo `Server.queryRowScan` convenience wrapper added, ~11 repetitive
`func(row *sql.Row) error { return row.Scan(...) }` call sites across
server.go/server_config.go/server_security.go/agent_job.go collapsed onto
it (the 3 `scanAlert`/`scanOperator`/`scanSchedule` sites correctly kept
their custom closures); `core.HandleScrollbarDrag`/`HandleScrollbarDragH`
got a doc-comment noting they conflate track-length and visible-count into
one param (harmless today — verified via grep that all ~20 call sites
across both repos pass equal values — but worth flagging for a future
caller); `key_diagnostics_dialog.go` got the scrollbar-drag test its 3
siblings already had.

**Explicitly reversed after verification — the one lesson worth keeping**:
the tuikit audit agent flagged `ContextMenu.HandleMouse` not clearing
`cm.hover` when the pointer moves onto a divider/disabled row as a
"cosmetic bug." Before fixing it, I checked `MenuBar`'s dropdown
(`menu_bar.go:197-201`) for comparison and found **the exact same
behavior** — `selectedItem` isn't cleared over a divider/disabled dropdown
row either. Fixing only `ContextMenu` would have *introduced* an
inconsistency with its just-de-duplicated sibling, the opposite of this
diff's own goal. Left both alone.

**Why:** matches [[full-review-implementation-2026-07-23]]'s closing
lesson ("an audit agent's claim about 'file X already does Y correctly' is
worth directly verifying by reading X") — same lesson, opposite direction:
here the agent's claim was "file X does Y *incorrectly* unlike sibling Z,"
and reading Z directly showed Z has the identical behavior, so it wasn't a
bug at all, just consistent design repeated in two places.

**How to apply:** before fixing anything an audit agent calls out as
"inconsistent with a sibling," read the sibling yourself. Don't take
"unlike X" at face value in either direction (bug-exists or bug-doesn't).

**Process note**: user gave the model-switch reminder once at plan
approval per [[model-switch-after-planning]], then said "do ALL" without
addressing it — proceeded straight to implementation without asking a
second time, consistent with that memory's established precedent.


---

## 2026-07-30 — Review both repos

`review-both-repos-2026-07-28`

*Full two-repo review 2026-07-28 — 3 real bugs fixed (detail-cache placeholder, wakeEventLoop/quit deadlock, non-atomic config save), gosmo iter.Seq made ctx-aware (breaking), 6 New*Dialog types collapsed onto a generic base*

Review + full implementation pass over gossms and gosmo on 2026-07-28. User
picked "everything incl. gosmo" and, for the `iter.Seq` decision, "change
Seq to take ctx" (the breaking option, over adding `SeqContext` twins or
deleting `iter.go`) — that settles the conflict flagged in
[[full-review-implementation-2026-07-23]].

**Why:** the three real bugs were each invisible to the existing tests and
to every earlier review pass, and the two biggest wins came from asking
"what is this code's *contract*", not from reading it line by line.

**How to apply:**

- **The detail-cache bug is the reusable lesson.** The progressive
  Databases/Tables backfills guarded the whole posted closure with
  `if seq != db.seq { return }` — but that closure did two different
  things: write `rows[i]`, and redraw. Only the *redraw* is
  selection-dependent; the write belongs to the fetch that owns `rows`.
  Skipping both meant `cacheOnly` cached rows still showing "…", and
  permanently, since reselecting is then a cache hit. When a staleness
  guard wraps a closure doing more than one thing, check each thing
  separately.
- **`wakeEventLoop` must never block while holding `quitMu`.** It sends on
  `screen.EventQ()` (tcell buffers 128) under the same lock `quit()` takes
  from the UI goroutine, which is by then not draining. Now a
  `select`/`default`. Dropping the send is safe *because* a full queue
  guarantees the loop wakes for those events, and `Run()`'s top clears
  `wakePending` + calls `drainPending()` on every iteration regardless of
  what woke it. See [[gossms-tui-tmux-testing]] — verified live via a real
  connect-failure dialog appearing with no extra keypress.
- **`Config.Save` was `os.WriteFile` (truncate-in-place).** Any interrupted
  write ⇒ invalid JSON ⇒ `Load`'s `json.Unmarshal` error branch silently
  returned an empty config, losing every saved connection. Now temp file +
  `Sync` + `Rename` in the same dir, and `Load` keeps a `.corrupt` copy.
  Also `onDisk := *c` instead of a hand-listed field literal, which had
  been silently dropping any newly added `Config` field.
- **gosmo `iter.go`: all 75 `*Seq` now take `ctx` and wrap the `...Context`
  method.** They previously wrapped the *plain* variants, i.e.
  `context.Background()` — the one part of the public API the
  context-everywhere work never reached. gossms uses none of them, so the
  break costs nothing internally. `seqFrom` now takes
  `(ctx, func(context.Context) ([]T, error))`, keeping the no-extra-arg
  cases to one line each.
- **Six `New*Dialog` types were one dialog with different contents.**
  Collapsed onto a generic `newObjectDialog[P any]` +
  `newObjectConfig[P]` in `new_object_dialog.go`: −862/+87 across the six,
  each now just a prefetch, an embed, a config literal, and `buildPages`.
  Deliberately *not* merged with `PropDialog` — that one loads pages
  lazily one at a time and applies a dirty-diff, which a create dialog has
  no use for. The merge exposed two live inconsistencies it then fixed
  (Operator's `runPipeline` dropped `Validate`'s page index; `show()`
  open-coded the nil check `onClose` did via `cancelIfSet`) and two dead
  `NewJobDialog` fields.
- **Deferring `DataGrid.RefreshColumnWidths` to the next `Draw`** turns a
  progressive backfill's N per-row recomputes (each rescanning up to 200
  rows) into one per frame. The dirty-flag pattern is worth reusing for
  anything a background fill calls per row.
- Also: `ContextMenu` outside-click now consumes (it used to dismiss *and*
  fall through, activating whatever was underneath); `date`/`time`/
  `datetime2`/`datetimeoffset` columns render with their own layout
  instead of one fixed datetime mask; `requireConn`/`connOrFirst` replaced
  32 copies of the not-connected guard; 18 deprecated `tcell.ColorWhite`
  &c. → `color.White` from `tcell/v3/color`; `DetailBrowser.PurgeConn` +
  `purgeCompletionInventories` on disconnect (both caches outlived the
  connection — the inventory one is keyed by server/db *name*, so a
  reconnect was served the pre-disconnect catalog).

**Live testing completed** against ubudock once SQL Server came back up.
The A/B against a pre-fix binary built in a throwaway `git worktree` is the
technique worth repeating: it turned "the fix looks right" into "4 of 5
rows are permanently stuck on `…` without it, 0 with it". Also verified
live: all six date/time layouts (incl. datetime2's 7 digits and
datetimeoffset's `+02:00`, both of which the old fixed mask destroyed),
New Database through both Script Changes and OK, New Schedule through
Apply, New Login through Script Changes, disconnect→reconnect in one
session, and a clean Ctrl+Q.

**Found live (pre-existing, NOT fixed): Script Changes is broken on any
create dialog whose later page depends on an earlier page having really
run.** New Schedule reports `gosmo: schedule "X" not found` — page 2
(Jobs) calls `AttachSchedule`, which resolves the schedule by name, but
under `gosmo.WithScript` page 1's `CreateSchedule` only *collected* a
statement, so nothing exists to look up. Confirmed identical on the
pre-refactor binary, so the generic base didn't cause it. New Job's
Schedules page and New Alert's Response page are the same shape and
likely affected. Apply works fine — script mode alone is broken. Fixing it
is a design question (script the dependent statement blindly? skip
dependent pages? make gosmo's script mode fake the lookup?), so it was
left for the user to decide.

Driving the TUI over tmux: `Ctrl+Space` is the reliable keyboard route to
a tree node's context menu (`Shift+F10` does not survive tmux), and
left-clicks *inside* an open ContextMenu don't get delivered while
right-clicks elsewhere do — use arrow keys + Enter there. `capture-pane -e`
plus grepping for `48;2;0;122;204` is how to confirm which item/button is
focused before pressing Enter. Propsheet dialogs: ~25 Tabs walks out of the
form onto the buttons, then Left/Right picks one. See
[[gossms-tui-tmux-testing]].

Both repos are gofmt/vet/test/`-race`/staticcheck clean and deadcode is
unchanged from baseline. Test artifacts (`reviewtest_db`,
`reviewtest_sched`) dropped; Options settings I perturbed restored.
Nothing committed.


---

## 2026-07-30 — Review both repos

`review-both-repos-2026-07-29`

*P1-P3 review implementation — propsheet drag stole the button row (live A/B proved it fired Script Changes), datetime scale, 2 dedup collapses, postAndWake sweep; my own scanNext drain was a regression, caught live*

Review of gossms + gosmo on 2026-07-29, planned then implemented P1-P3.
All live-verified against ubudock; nothing committed (user's instruction).

Real bugs fixed:
- **PropertySheet stole any drag armed inside the form.** `ButtonClicked`
  ran before the form in `sheet_input.go`, and `ModalDialog.mouseDragging`
  only arms on a press that *lands* on a button. Live measurement showed
  the form's scrollbar column and `[ Script Changes ]`'s last column are
  **the same column** — a straight vertical thumb drag onto the button row
  fired Script Changes. A/B against a binary with only that fix reverted
  confirmed it. Fixed with a `dragZone` latch on PropertySheet: the zone
  that claims a Button1 press keeps every event until the release,
  outranking even `ConsumeOutsideClick`.
- **Date/time columns ignored their declared scale** — `time(0)` rendered
  `.0000000`. `ColumnType.DecimalSize()` reports scale for exactly
  datetime2/time/datetimeoffset.
- `Form.scroll` was never re-clamped to `maxScroll` on resize.
- Create dialogs re-ran a successful create on a second Apply (new
  `created` guard in `newObjectDialog`); also a double-prefetch on a fast
  page switch, and `DetailBrowser.PurgeConn` leaving stale rows on screen.

Dedup: `pageUserSecurables`/`pageRoleSecurables` were byte-identical (→
`pageDatabasePrincipalSecurables`), and 7 `page*ExtendedProperties`
wrappers collapsed to one `pageExtendedProperties(sc, dbName, level func())`
— the closure is required because a rename changes the level mid-dialog.
33 hand-written `postEvent`+`wakeEventLoop` pairs became `postAndWake`;
only QueryPanel's elapsed-timer tick legitimately calls the bare wake now.

gosmo: the BREAKING ctx-aware-`*Seq` entry was being written into the
already-pushed `## v0.0.6` section — split out a `v0.0.7 (unreleased)` in
both CHANGELOG.md and RELEASE.md, and added the first `iter_test.go`
(`seqFrom` had 75 public callers and zero tests).

**Why:** the one thing that went wrong was my own — see
[[sqlexp-extra-next-breaks-message-loop]]. An unconditional drain I added
while fixing an error path broke every result set, and only live testing
caught it; `go test -race ./...` was clean throughout.

**How to apply:** `go.mod`'s `replace ... => ../gosmo` is still
uncommented (correct while gosmo is untagged — see the
`dev-with-local-gosmo` skill) and **must be commented out before
committing**, or `go install .../cmd/gossms@<tag>` breaks for everyone.


---

## 2026-07-30 — Review followup mousedrag

`review-followup-mousedrag-2026-07-23`

*Second 2026-07-23 review pass (after review-both-repos-2026-07-23): found+fixed a real regression in that morning's uncommitted mouseDragging sweep, plus DECIMAL/MONEY-as-hex, Login/User/Role Properties stale-name-after-rename, planview drag-across-tab-row, fitMessage height cap, gosmo Seq backfill*

Ran via EnterPlanMode with a written plan file (`/home/radu/.claude/plans/silly-swinging-cosmos.md`),
approved, then implemented same session. Distinct from the earlier same-day
[[review-both-repos-2026-07-23]] — different findings, later pass, focused on
the *uncommitted* `mouseDragging` widget sweep from
[[mousedragging-full-sweep-and-dialog-sizing-2026-07-23]] plus older
known-but-unfixed items from memory.

**Why:** user asked for a full bugs/inconsistencies/optimization/refactor
review of both repos, planning-only, with an explicit model switch before
bulk implementation (recorded per [[model-switch-after-planning]]).

**Findings, all fixed + build/vet/test clean in both repos:**

1. **Real regression in the just-written mouseDragging sweep**: the new
   `mouseDragging` latch on `CheckBox`/`RadioBox`/`DropDown`/`Button` only
   resets on a `ButtonNone` event reaching that widget's own `HandleMouse`.
   `ConnectDialog`/`BackupDialog`/`RestoreDialog`'s `HandleMouse` forward
   `ButtonNone` only to the currently-focused `InputField`/`Editor`, never to
   the checkboxes/dropdowns/radios/buttons — so each of those widgets fired
   **exactly once per dialog session**, then went dead (checkbox toggles once
   then stops; dropdown opens but can never pick an item again). Same root
   cause, milder, in every dialog including `OptionsDialog` and
   `propsheet.PropertySheet`: a release landing **outside** the dialog rect is
   consumed by `ModalDialog.ConsumeOutsideClick` before any child widget ever
   sees it. **Fix**: at the top of each affected `HandleMouse` (before
   `ConsumeOutsideClick`), on `ButtonNone` forward to every latch-owning child
   widget unconditionally — each returns `false` on `ButtonNone` so this is a
   no-op besides resetting the latch. Applied to all 5 sites above.
   **Lesson**: any new host that owns latch-bearing widgets and has an early
   `return` in its `HandleMouse` before those widgets get a chance to see the
   release needs this same forwarding — check new dialogs against this.

2. **DECIMAL/NUMERIC/MONEY/SMALLMONEY query results rendered as hex**
   (`0x302E303730333132` instead of `0.070312`) — known since
   [[detail-browser-widevalue-and-tables-2026-07-19]], root-caused and fixed
   this session. go-mssqldb v1.10.0 reports `DatabaseTypeName()` as
   `"DECIMAL"` for both DECIMAL and NUMERIC, `"MONEY"`/`"SMALLMONEY"` for
   money types (verified in `$GOMODCACHE/.../types.go`), and — confirmed via
   `decodeDecimal`/`decodeMoney` in that same source — the driver already
   decodes these to a `[]byte` holding the literal ASCII digit string, not a
   binary blob, unlike `[]byte` from a real `varbinary`. Fixed in
   `internal/query/executor.go`'s `scanResultSet`/`formatValue`: mark
   decimal-like columns from `ColumnTypes()` (same technique as the existing
   UNIQUEIDENTIFIER special case) and render their `[]byte` via `string(x)`
   instead of hex-encoding.

3. **Stale-name-after-rename+Apply**, previously fixed only in
   [[key-props-build-2026-07]] and Server Role Properties, was still latent
   in Login/User/Role (database) Properties exactly as that memory predicted.
   Ported the identical `*string`-boxing pattern to `login_props.go`,
   `user_props.go`, `role_props.go`: the page-set builder boxes the
   name/dbName pair in a shared `*string`, every page function takes
   `*string` instead of `string`, and the General page's rename-apply closure
   does `*namePtr = newName` on success so `PropDialog.InvalidateAll`'s next
   reload looks up the right row. Also had to convert `pageLoginSecurables`
   from an eager `return pagePrincipalServerPermissions(sc, loginName)` (baked
   the name in at page-*construction* time, before any rename could happen)
   to the lazy wrapper shape `pageServerRoleSecurables` already used
   (`pagePrincipalServerPermissions(sc, *loginName).load(ctx)`, called fresh
   on every reload).

4. **ctx/timeout threading gap**, following up
   [[object-explorer-detail-async-2026-07-19]]'s `childFetchTimeout` pattern:
   3 originally-scoped unbounded calls (`detail_browser_tables.go`'s
   `DatabaseByName`, `properties_dialog.go`'s `fetchDependencyRows`,
   `app_connections.go`'s `defaultDatabaseName`/`CurrentDatabase`) plus **6
   more found while fixing #3** — every `pageXExtendedProperties` load closure
   (`schema_props.go`, `table_props.go`, `statistics_props.go`,
   `index_props.go`, `role_props.go`, `user_props.go`) called
   `d.ExtendedProperties(level)` instead of `ExtendedPropertiesContext(ctx,
   level)` despite `ctx` already being in scope — trivial one-line fix each,
   same bug class, expanded scope past the plan since it was mechanical and
   zero-risk.

5. **planview drag-across-tab-row**: a Button1 drag *starting* in the content
   area (XML text selection, tree, graph) never set `mouseDragging`, so if the
   cursor drifted up into the tab bar or statement-selector row mid-drag
   (tcell resends `Button1` on every motion event while held), it misfired a
   tab switch / `OnExpand` (opens a second panel!) / statement step. Fixed
   with a `routeToContent` helper shared by the `ButtonNone` branch, the
   already-latched tab/stmt branches, and the final default case — the latter
   now also sets `mouseDragging = true` on `Button1`.

6. **`fitMessage` height cap**: the function (new this same morning, part of
   [[mousedragging-full-sweep-and-dialog-sizing-2026-07-23]]) capped width at
   2/3 of screen but never height, so a long message on a short terminal
   could word-wrap into more lines than
   `ModalDialog.recentre()`'s height clamp leaves room for, drawing the
   message tail over the separator/button row. Fixed by capping line count to
   `sh - baseH + 1` and ellipsizing the last kept line (join the dropped
   lines' text back in and run it through the existing `core.Truncate`, so
   the ellipsis reflects real cut-off content rather than just chopping the
   line list).

7. **gosmo `iter.go` Seq coverage backfill**: added ~19 missing `Seq`
   variants (`ServerRoleSeq`, `ServerRoleMemberSeq`, `LinkedServerSeq`,
   `MailProfileSeq`, `ConfigurationSeq`, `CategorySeq`, `JobHistorySeq`
   (Server) + `HistorySeq` (Job), `DatabaseRoleSeq`, `RoleMemberSeq`,
   `FileGroupSeq`, `DatabaseScopedConfigSeq`, `UserDefinedFunctionSeq`,
   `TablesBySchemaSeq`, `DependencySeq`/`DependentSeq`,
   `ExtendedPropertySeq`, `SchemaPermissionSeq`, `Table.CheckConstraintSeq`)
   using the existing `seqFrom`/closure pattern. Deliberately skipped
   diagnostics-shaped methods (`ReadErrorLog`, `ActiveSessions`,
   `FragmentationStats`, `Search`) as not real "collections" worth iterating.

**Process note**: used `EnterPlanMode` → wrote plan file → `ExitPlanMode` for
approval → user then ran `/model sonnet` before saying "go", matching
[[model-switch-after-planning]]'s expected flow exactly.


---

## 2026-07-30 — Review plan

`review-plan-2026-07-14`

*2026-07-14 dual-repo review; Priorities 1-4 fixed (gosmo HASHED bug, gossms conn leak/cancel/writeCSV/race panic, gofmt+errors.Is+misc cleanups, query-results row cap); priority 5 still pending*

On 2026-07-14 a full review of gossms + gosmo produced a 5-priority fix plan. **Priorities 1-4 were implemented and verified the same day**; priority 5 is still pending (see list below).

## Priority 1 — DONE (gosmo, ~/go/gosmo)

`CreateLoginContext` (server.go), `ChangePasswordContext` (login.go), and `buildChangePasswordStatement` (login.go) used to splice UTF-16LE-encoded *plaintext* into `PASSWORD = 0x… HASHED`. SQL Server treats HASHED values as its own hash format and rejects/mis-stores this — logins could never authenticate. Fixed by quoting the password as `N'...'` (new `nStringLiteral` helper in helpers.go, escapeSingle-based) instead of HASHED; removed `passwordHexLiteral` and the now-unused `encoding/hex`/`unicode/utf16` imports in server.go.

**A second, real grammar bug was found only by live-testing against a real SQL Server** (not caught by unit tests, which only assert SQL string shape): `MUST_CHANGE` and `UNLOCK` are password-clause modifiers that must follow `PASSWORD = '...'` space-separated (like `PASSWORD = N'x' MUST_CHANGE UNLOCK`), not comma-separated `<set_option>` items — `PASSWORD = '...', UNLOCK` is rejected with "Incorrect syntax near 'UNLOCK'". `CHECK_EXPIRATION = ON` (required whenever `MUST_CHANGE` is used) *is* comma-separated and goes after the modifiers. Fixed in `buildChangePasswordStatement`. **Lesson: for gosmo, SQL-statement unit tests only prove string shape, not that SQL Server accepts the grammar — validate write paths against a real server when one is available.**

Verified via:
1. Unit tests updated (login_write_test.go, server_test.go) and passing.
2. A live end-to-end test against a real SQL Server (server `ubudock`, sa/HealthClinic — see [[gossms-live-test-server]]) exercising `CreateLogin`, `ChangePassword`, and `ChangePasswordWithOptions` in all 4 mustChange/unlock combinations — confirmed by actually re-authenticating with old (rejected) and new (accepted) passwords.
3. gossms itself (linked via `replace ../gosmo`) builds/vets/tests clean against the fix.
4. Drove the actual TUI: connected to ubudock, Object Explorer → Security → Logins → a disposable test login → Login Properties (Shift+F10 for context menu since NodeLogin's menu has no keyboard-reachable shortcut otherwise) → changed password through the real dialog form → confirmed old password now rejected and new one authenticates.

Gotchas hit while driving the TUI via tmux (worth remembering for future TUI testing beyond [[gossms-tui-tmux-testing]]):
- `Ctrl+O` is File > Open, not Connect — Connect is `Ctrl+Shift+O`, and tmux's `C-S-o` notation doesn't reliably reach the app as Ctrl+Shift+O (arrived as plain Ctrl+O); use F10 → menu navigation instead for Shift-modified letter shortcuts.
- `ContextMenu.HandleKey`'s Up/Down (menu.go) do **not** skip `Divider` items — landing on a divider and pressing Enter is a silent no-op. Count dividers when computing how many Down-presses reach a target item, or verify the target row's background is the selection blue (`48;2;0;122;204` via `tmux capture-pane -e`) before pressing Enter.
- propsheet.Form's Tab exhausts every focusable row before yielding to the sheet's button row (Form.HandleKey's `FocusNext` returns false only past the last focusable row) — don't assume Tab reaches the OK/Cancel/Apply row after just one page's worth of fields; keep tabbing (checking button-row highlight color) until arrival.

## Priority 2 — DONE (gossms, ~/go/gossms)

Of the 4 items originally flagged, 3 were real bugs and got fixed; 1 turned out to be a false positive on closer analysis — worth recording the reasoning so it isn't re-flagged:

- **`p.database` "race" in query_panel.go runQuery — investigated, NOT a real bug, left as-is.** Traced every write site (`app_connections.go`'s `connectForQueryPanel`, `query_panel.go`'s `setResult`) and confirmed each is strictly sequenced before any concurrent read could occur: `p.conn`/`p.database` are set once at connect time and gate all query execution via `isConnected`; `setResult`'s write to `p.database` happens in the same postEvent closure that flips `p.executing` back to false, so a new query can never start before that write lands (the whole app is single-threaded via the postEvent/drainPending event loop — see app.go). Concluded the original review flagged this defensively (pattern-matching the legitimate `sc := p.conn` capture just above it) without confirming an actual interleaving. Did not add unnecessary defensive capture per CLAUDE.md's anti-overengineering guidance.
- **Connection leak on fast panel close — fixed.** `app_connections.go`'s `connectForQueryPanel` postEvent callback now checks `a.panelHosted(qp)` (new helper in `app_panel_actions.go`, mirrors `closePanelByPointer`'s own lookup) before assigning `qp.conn`; if the panel was already closed while the connection was resolving, `newConn.Close()` runs instead of leaking it. `TestPanelHosted` added.
- **`cancel()` never called after normal completion — fixed** in both `query_panel.go`'s `runQuery` (calls `cancel()` right after reading `ctx.Err()` into `cancelled` — order matters, since calling cancel first would make `cancelled` always true) and `tasks.go`'s `postTaskDone` (now calls `t.Cancel()`, reusing the existing nil-safe method).
- **`writeCSV` ignoring `f.Close()` error — fixed.** Switched to named returns so a deferred `f.Close()` error surfaces as the function's own error when nothing earlier already failed. `TestWriteCSVWritesHeaderRowsAndBlankLineBetweenSets` added, pinning the CSV shape (header + rows per set, blank line between sets, row count excludes headers).

All of gossms's existing tests plus the 2 new ones pass; `go build`/`go vet`/`gofmt -l` clean on every touched file.

**Side discovery from P2, fixed same day:** `go test -race ./internal/tui/...` crashed with a nil-pointer panic in `wakeEventLoop` — confirmed pre-existing on unmodified code (via `git stash push -u` + rerun), root-caused to `TestObjectContextMenuBuildsCorrectFQN`'s "New Query" action spawning a real background goroutine (`connectForQueryPanel`) that outlives the test and later calls `wakeEventLoop()` against a `newTestApp()` App that never sets `a.screen`. Fixed with a nil-guard in `wakeEventLoop` (app.go) — deliberately a no-op rather than a panic when `a.screen == nil`, since that's exactly the state `newTestApp()`'s own doc comment says it's in ("no screen, no event loop") — a screen-less App legitimately has no event loop to wake. While there, also consolidated `tasks.go`'s `postProgress`/`postTaskDone` — previously the "equivalent inline pattern" CLAUDE.md's async-wakeup section calls out — onto the shared `wakeEventLoop()` helper too, so the nil-guard covers them and the duplicate inline `a.screen.EventQ() <- ...` sends are gone (dropped the now-unused `tcell` import from tasks.go as a result); updated that CLAUDE.md passage to note the consolidation. Verified with 5x repeated `go test -race -count=1 ./internal/tui/...` (all clean) plus a full `go test -race ./...` across every package.

## Priority 3 — DONE (both repos)

- **gofmt drift**: applied in both repos. gossms: 7 files, all trailing-blank-line-only except `widgets/dropdown.go` (a collapsed one-liner `Focus` method reformatted to multi-line — no logic change). gosmo: 5 files, all struct-tag/comment realignment only (`auth.go`, `partition.go`, `statistics.go`, `table.go`, `types.go`) — no logic changes anywhere.
- **`DrawBoxTitle` used `len()` not `DisplayWidth`** (core/drawing.go) — fixed. Reachable with real wide-rune content: `d.title` (any ModalDialog) and `g.viewHeader` (DataGrid's cell-viewer popup, built from a column name a DB schema could give non-ASCII characters).
- **`config.Save`'s `MkdirAll` used 0755, `loadOrCreateKey`'s (secret.go) used 0700`** — fixed to 0700 in config.go, matching secret.go's stated owner-only intent. Since `Save()` runs (and creates the dir) before `loadOrCreateKey` on a fresh install, and `MkdirAll` never chmods an existing dir, whichever ran first used to decide the real permissions — always 0755 in practice. `TestSaveLoadRoundTrip` (config_test.go) now asserts the dir is 0700 after `Save()`.
- **gosmo `err == sql.ErrNoRows` → `errors.Is(err, sql.ErrNoRows)`**: 11 sites (not ~8 as first estimated) across agent_job.go, change_tracking.go, database.go, database_options.go, login.go, scripter.go (×3), server.go (×2), server_config.go — added the `errors` import to each. All were direct post-`row.Scan()` checks, safe mechanical rewrites.
- **executionplan.go's inner `fmt.Errorf("enable %s: %w", ...)` was the only unprefixed error message in all of gosmo (241/242 sites already said `"gosmo: ..."`)** — fixed to `"gosmo: enable %s: %w"`. Confirmed via precedent (`database.go`'s `ListSchemas` + `d.query`'s internal closure) that this codebase's established convention is to prefix every wrap even when it produces a nested "gosmo: ...: gosmo: ..." message once composed — not just outermost/exported-facing wraps — so this was a genuine, sole outlier, not a stylistic judgment call.
- **Package doc said "Package smo"** (types.go, gosmo's only package-doc comment) — fixed to "Package gosmo". Confirmed via `go doc github.com/radix29/gosmo`.
- **go.mod diffs — deliberately left alone, not a bug**: gossms's `replace github.com/radix29/gosmo => ../gosmo` needs to stay active for local dev until these gosmo fixes are tagged/released (per CLAUDE.md's own gosmo-development workflow); gosmo's `toolchain go1.26.5` line is auto-added by the Go tooling itself when it detects the installed toolchain differs from the module's `go 1.26` directive — reverting it would just have it reappear on the next `go build`.

Verified: `go build`/`go vet`/`gofmt -l`/`go test ./...` clean in both repos; `go test -race ./...` also clean in gossms (still holding from the P2 fix).

## Priority 4 — DONE (gossms, ~/go/gossms)

Added a configurable cap on how many rows one result set keeps in memory, so a runaway `SELECT` can't OOM the TUI:

- **`config.Config.MaxResultRows`** (config.go), default `DefaultMaxResultRows` (raised from 10000 to 100000 on 2026-07-14 per user request, after the initial implementation), same non-positive-coerces-to-default pattern as the existing `MaxCellLength`. New tests: `TestLoadMissingFileReturnsEmptyConfig` extended to check the default; `TestLoadCoercesNonPositiveMaxResultRows` (0 and -5 both coerce) — both reference the constant, not a hardcoded literal, so they didn't need updating for the default change.
- **`internal/query.Execute`/`runBatch`/`scanResultSet`** all gained a `maxRows int` parameter (0 or negative = unlimited). Design decision: the query still executes and drains fully either way (row/rows-affected counts and later batches in the same GO-separated script are unaffected) — `scanResultSet` just stops calling `Scan`/formatting/appending past the cap while still calling `rows.Next()` until that result set's stream is exhausted, so the sqlexp message-based driver iteration `runBatch` depends on (which needs a result set fully drained before the next message can be read) stays correct. A truncation notice (`"Only the first N row(s) are shown — increase Max Result Rows in Tools > Options to see more."`) is appended to Messages when it fires.
- **Scope decision:** the cap applies to the interactive Grid/Text view only. `query_panel.go`'s `runQuery` passes `0` (unlimited) when `p.resultsMode == ResultsModeFile`, since "Results To File" is a deliberate "export everything" action — capping it would silently truncate exports, which is a worse failure mode than the memory-growth risk it opted into by design.
- **Options dialog** (options_dialog.go): new "Max result rows (Query Results):" field, mirroring the existing "Max cell length" field's zone/layout/apply() pattern exactly (new `zoneMaxResultRows`, fits in the dialog's existing 15-row height with no resize needed — there were 3 unused rows between the last existing field and the button-row separator).
- **Verified live against a real SQL Server** (ubudock — see [[gossms-live-test-server]]), via a temporary `cmd/livetest_rowcap` program (removed after): confirmed (a) a cap smaller than the actual row count retains exactly N rows and adds the truncation notice, (b) **capping one result set does not corrupt reading of a second, later statement in the same batch** — the single most important correctness risk of the "keep draining without scanning" approach, and the one thing that couldn't be verified from documentation alone, (c) a cap larger than the actual count and `maxRows=0` both retain everything with no spurious notice. Then re-verified through the actual running TUI end-to-end: set the cap to 3 via the real Options dialog, ran a query generating 100 rows against ubudock, confirmed the grid showed exactly 3 rows, the Messages tab showed the truncation notice, and — critically — `(100 rows affected)` still reported the true count, proving the cap doesn't distort SSMS-style reporting.

All builds/vet/gofmt/tests (including `-race`) clean in gossms.

## Priority 5 — still pending

- **P5 optional refactors**: split query_panel.go (744 lines, now slightly larger) and datagrid.go (963 lines) per the one-file-per-purpose convention.


---

## 2026-07-30 — Review plan

`review-plan-2026-07-18`

*Dual-repo (gossms+gosmo) review 2026-07-18/19: P2-P5 all done, no bugs found; P1 (gosmo release tagging) explicitly deprioritized*

Second full dual-repo review (gossms + gosmo), following up the 2026-07-14 one
([[review-plan-2026-07-14]]). Headline: **no confirmed functional bugs found**
in the review itself — both repos were gofmt/vet clean, all tests passing,
every high-risk pattern (postEvent/wakeEventLoop pairing, gosmo Foo/FooContext
conventions, SQL-splice quoting) already correct. The resulting plan was pure
housekeeping/polish/tests/refactoring, ranked P1-P5.

**P1** (gosmo release tagging + go.mod `replace` directive removal — the
committed-active `replace github.com/radix29/gosmo => ../gosmo` breaks any CI
release build) — user said "ignore P1", explicitly deprioritized. Still
outstanding as of 2026-07-19; must be done before the next gossms release tag.

**P2** (small polish, done): `UpdateDialog`'s dev-build ("(devel)") version
comparison no longer falsely claims "a new version is available"
(`isReleaseVersion` in `internal/tui/update_check.go`); `StatusHistoryDialog`
gained a dirty flag so `Record()` doesn't rebuild its editor text while
hidden. A user-reported follow-up bug (release link invisible in
`UpdateDialog`) was root-caused as a row-collision — a 2-line dev-build
message pushed the link row onto the same screen row `DrawSeparator()` draws
on (drawn after content, painting over it) — fixed by bumping the dialog
height 13→15.

**P3** (test-coverage lifts, done): `internal/tuikit/core` 16.1%→52.8%
(pure helpers only — `DisplayWidth`/`Truncate`/`PadRight`/`Rect`/mathutil/
`Itoa`/`EvRune`/`JoinPath`; **tcell v3.4.0 has no `SimulationScreen`** unlike
v2, so the plan's drawing-primitive-testing stretch goal was dropped rather
than hand-rolling a 30-method fake `Screen`). `internal/query` 20.4%→21.2%
(`ErrorMessages`/`isShowplanResultSet`/`formatValue`/`formatGUID` were already
100%; the rest needs a live `*sql.DB`). gosmo 13.9%→14.7% (`BuildBackupStatement`/
`BuildRestoreStatement` had only one narrow test case each — filled in every
WITH-clause branch, `backupTypeFromHeader`'s full mapping, two missed
`writeFileSizeClauses`/`buildAlterFileStatement` branches).

**P4** (file splits, done, byte-for-byte verified per CLAUDE.md's procedure):
`database_props.go` (1199→8 files, one per Properties page, 60-354 lines
each), `completion_provider.go` (970→4 files: provider surface, tokenizer,
cursor-context/FROM-scope, candidate-resolution+icons), `restore_dialog.go`
(953→2 files: UI/state/draw/handlers vs. the background `loadHistory*`/
`analyze`/`startRestore`/`runRestore` operations). No logic changes; every
split confirmed by reconstructing the exact original file from extracted
line-range chunks and diffing byte-for-byte before deleting the source.

**P5** (live ubudock verification, done 2026-07-19, see
[[gossms-tui-tmux-testing]] for the tmux-specific gotchas found along the
way): Backup Database dialog, Restore dialog's Analyze Backup
(HEADERONLY/FILELISTONLY) and restore-under-a-new-name (automatic file
relocation), System Views/Procedures/Functions folders, and sys-schema
IntelliSense all verified end-to-end against the real server — everything
worked on the first try, no bugs found. Scratch databases (`ClaudeP5Test`,
`ClaudeP5Test_Restored`) created and dropped cleanly per the disposable-
resource discipline in [[gossms-live-test-server]].


---

## 2026-07-30 — Schema properties mockup build

`schema-properties-mockup-build-2026-07`

*Schema Properties dialog built from scratch (didn't exist before) from ssms_schema_properties_tui_mockup.md; one new gosmo read capability; discovered real SQL Server behavior — ALTER AUTHORIZATION on a schema wipes its explicit permission grants*

Built Schema Properties from scratch (`internal/tui/schema_props.go`, new
file — no prior Properties dialog existed for `NodeSchema`), wired under
Object Explorer's Database > Security > Schemas > *schema* > Properties...
(`case NodeSchema:` added to `contextMenuItemsForNode`,
`app_explorer_data.go`; `showSchemaPropertiesFor` added to
`app_panel_actions.go`, following the exact `showRolePropertiesFor`
pattern — reuses `PropDialog`, not a new dialog type, since this is
edit-existing with a dirty-diff apply, same as
[[table-properties-mockup-build-2026-07]]/
[[database-role-properties-mockup-build-2026-07]]).

**3 pages, matching the mockup's own "General/Permissions/Extended
Properties" page list** — every picker/warning/effective-permissions/
object-inventory/Drop-Schema-safety modal in the mockup folded into inline
fields and the existing Script Changes/SetMessage mechanisms, same
simplification as every prior Properties dialog this project has built.

- **General**: schema name (`Static` — always, not just for system
  schemas: SQL Server has no RENAME SCHEMA facility at all, confirmed by
  gosmo having no such method, so unlike Table/Role/User Properties this
  field is never editable, not just built-in-gated), Owner (editable
  `Select` over `principalNames(users, roles)` for non-system schemas,
  applied via the pre-existing `gosmo.Schema.ChangeOwnerContext` — zero
  new gosmo write capability needed), Principal type (friendly-cased only
  for the "Database role" case per this project's existing convention,
  raw `UserType` string otherwise — matches
  `role_props.go`'s/`user_props.go`'s own precedent, not a new formatting
  helper), Schema ID, Object summary (Tables/Views/Stored procedures/
  Functions/Synonyms/Sequences counts — `TablesBySchemaContext` filters
  server-side, the other 5 types have no BySchema variant so are fetched
  in bulk and filtered client-side by `.Schema ==`, avoiding any new gosmo
  method), Permission summary (Explicit principals/grants/denies/With
  grant option — all computed by counting the same `SchemaPermissions`
  entries the Permissions page uses, including real
  `GRANT_WITH_GRANT_OPTION` rows if any exist even though this project's
  UI never sets that state itself, consistent with the WITH GRANT OPTION
  deferral already established in
  [[server-properties-mockup-gapfill-2026-07]]).
  **Dropped entirely, not faked**: the mockup's "Created"/"Modified"
  General rows — `sys.schemas` has no create_date/modify_date columns at
  all (unlike `sys.tables`/`sys.procedures`/...), confirmed before writing
  any code, not discovered as a live-test surprise.
- **Permissions**: reused `buildPermissionsMatrix`
  (`prop_grid_helpers.go`) verbatim — the exact "one securable, every
  principal" two-pane editor `pageTablePermissions`/
  `pageDatabasePermissions` already use — wired to
  `GrantSchemaPermissionContext`/`DenySchemaPermissionContext`/
  `RevokeSchemaPermissionContext` (all pre-existing in gosmo) and
  `gosmo.SchemaPermissionNames()` (pre-existing catalog).
- **Extended Properties**: reused `buildExtendedPropertiesForm` verbatim
  with `gosmo.ExtendedPropertyLevel{Level0Type: "SCHEMA", Level0Name:
  schemaName}` — no Level1, and the generic `fn_listextendedproperty`/
  `sp_addextendedproperty`/etc. level-struct plumbing already handled a
  single-level (no Level1/Level2) securable correctly with zero gosmo
  changes, verified live (add/edit round-tripped correctly).

**One new gosmo capability, additive**: `Database.SchemaPermissions`/
`SchemaPermissionsContext` (`security.go`) — the schema-scoped analog of
the pre-existing `Permissions`/`PermissionsContext`, which resolves its
securable via `OBJECT_ID(schema.name)` and therefore only works for
table/view securables; a schema has no `OBJECT_ID`, so this new method
queries `sys.database_permissions` keyed on `dp.class_desc = 'SCHEMA' AND
dp.major_id = SCHEMA_ID(@p1)` instead. Same `PermissionEntry` return type
as `Permissions`, so it slots into the existing `permEntry`-conversion
code in `pageTablePermissions`'s shape unchanged.

**Real SQL Server behavior discovered via live testing, not a code bug**:
`ALTER AUTHORIZATION ON SCHEMA::x TO y` — the one and only statement gosmo
already had for changing a schema's owner
(`Schema.ChangeOwnerContext`, pre-existing, unchanged) — **silently wipes
every existing explicit GRANT/DENY entry recorded on that schema**, for
every principal, not just the old/new owner. Verified live and in
isolation: created a disposable schema owned by `dbo`, granted SELECT to
a throwaway user, confirmed the grant existed in
`sys.database_permissions`, ran `ALTER AUTHORIZATION ... TO
claude_test_owner`, re-queried — zero rows. This is genuine, documented
(if obscure) SQL Server semantics, not a gosmo/gossms bug — SSMS itself
generates the exact same `ALTER AUTHORIZATION` statement for its own
Owner field with no client-side workaround, so there was nothing to fix.
Worth knowing before ever debugging "my schema permissions vanished after
an unrelated-looking Properties > General > Apply" in this app or in raw
T-SQL — the mockup's own "Owner change warning" modal (#4, deferred here
like every other modal) gestures at ownership-chaining risk but doesn't
call out this specific side effect either.

**tmux testing gotcha, worth folding into
[[gossms-tui-tmux-testing]]**: after using a Properties dialog's **Script
Changes** button (which opens a new Query panel as the active/focused
panel, by design, so the generated script can be reviewed/run
immediately), keyboard input **no longer reaches the still-open
Properties dialog behind it** — `Down`/`Tab` etc. silently do nothing
useful to the dialog until it's given focus back with a direct mouse
click on it (same "click to refocus" workaround already documented for
Object-Explorer-vs-query-editor focus fights). This is expected/by-design
behavior (Script Changes intentionally hands focus to the new query tab),
not a bug — but it's easy to misread as "my keystrokes are being eaten"
if you don't expect it. A right-click/Properties context-menu item on an
Object Explorer node also needs a **fuller `capture-pane` range than you
might expect** — a 4-item menu (`New Query`/divider/`Refresh`/
`Properties...`) can render 6+ screen rows tall depending on where it
pops up, and truncating the capture at the row you *think* is the bottom
of the menu will make a real, present "Properties..." item look
"missing" — spent real time chasing a false "menu item didn't get added"
alarm this way before just capturing more rows.

Verified end-to-end against ubudock: built a disposable schema
(`claude_test_schema`, owned by `dbo`, 2 tables + 1 view), two disposable
users and a role, a real `GRANT ... WITH GRANT OPTION`/`DENY`/plain
`GRANT` mix set up via raw SQL beforehand to confirm the General page's
summary counts and the Permissions page's initial load render real
pre-existing state correctly (not just states this session's own Apply
created); toggled a new GRANT through the Permissions page UI and
confirmed it landed via direct query; changed Owner through the General
page, previewed Script Changes (`ALTER AUTHORIZATION ON SCHEMA::[...] TO
[...]`, nothing else — confirming the Owner-only dirty-diff stayed
minimal), applied it for real, confirmed via query (and, as a side
effect, confirmed the permission-wipe behavior above); edited the
extended property and confirmed via query. All disposable objects
(schema, tables, view, users, role) dropped afterward.

Both repos build/vet/test clean (`gofmt -l .`, `go build ./...`, `go vet
./...`, `go test ./...` in both `~/go/gosmo` and `~/go/gossms`). gosmo's
`replace ../gosmo` directive in gossms's `go.mod` is still active/untagged
— still pending the user's go-ahead to tag and push a new gosmo version,
now with this session's `SchemaPermissions`/`SchemaPermissionsContext`
addition included alongside the prior sessions' unreleased changes
(`Server.Login`, offline-database fix has no gosmo component).

Noticed but out of scope for this session, worth flagging: a
`test_new_db` database is still present on ubudock under Databases —
appears to be leftover disposable test data from an earlier session's New
Database dialog testing that didn't get cleaned up; not touched here
since it wasn't this session's responsibility, but worth dropping next
time it's noticed if still unused.


---

## 2026-07-30 — Server properties mockup gapfill

`server-properties-mockup-gapfill-2026-07`

*Server Properties was brought in line with todo/mockups/ssms_server_properties_tui_mockup.md (2026-07-15) — what shipped and what's still deliberately deferred*

Implemented, spanning both `~/go/gosmo` and `~/go/gossms` (via the active `replace` in `gossms/go.mod`), after auditing `internal/tui/server_props.go` against `todo/mockups/ssms_server_properties_tui_mockup.md`:

- gosmo: `ServerInfo.EngineEdition`/`IsSingleUser` (server.go `loadInfo`), a new `Server.ProcessorInfoContext` (CPU/NUMA/hyperthread via `sys.dm_os_sys_info` + `sys.dm_os_schedulers`), and exported `ServerPermissionNames()`/`DatabasePermissionNames()` (the allowlists already existed internally, just weren't public).
- gossms: General page's Availability section, Security's `c2 audit mode`, 5 new Connections rows, Database Settings' `media retention`/`backup checksum default`, Processors' NUMA/hyperthread header + NUMA grid column + auto-affinity checkboxes, and an Advanced page rework (grouped editable rows for options with no other home, on top of the existing read-only full dump).
- Permissions page (both Server and Database Properties — they share `buildPermissionsMatrix` in `internal/tui/prop_grid_helpers.go`) was rebuilt from a flat "existing grants only" grid into a two-pane principal-list + full-permission-catalog editor. `propsheet.SectionRow` gained a `SetTitle` method to support the pane's dynamic "Explicit permissions for X" heading.

**Deliberately not done, per explicit user instruction:** filter/search boxes (Advanced's "Filter", Permissions' "Search") — no such widget exists anywhere in tuikit and the user said skip it.

**Deliberately deferred, needs more gosmo work first:** WITH GRANT OPTION (gosmo's Grant/Deny/RevokePermission methods have no with-grant parameter) and "Effective permissions" (nothing in gosmo resolves role membership or calls `fn_my_permissions`). Processor NUMA is done; if a 64-CPU+ server ever matters, note the pre-existing `affinity mask`/`affinity I/O mask` limitation (only covers the first 32 CPUs, `affinity64 mask` not wired up) still applies untouched.

Verified live end-to-end against [[gossms-live-test-server]] (ubudock) via tmux, including a real GRANT on a disposable throwaway login (created, granted, verified via `sys.server_permissions`, then dropped) — see [[gossms-tui-tmux-testing]] for the mouse-click-via-tmux-paste-buffer trick this required for Object Explorer tree navigation.


---

## 2026-07-30 — Server role props build

`server-role-props-build-2026-07`

*Server Role Properties built from scratch (SSMS as reference, no mockup); found+fixed a real cross-cutting gosmo bug (server-scope GRANT/DENY/REVOKE requires master context) that also silently broke Login Properties' Securables page*

Built Server Role Properties (`internal/tui/server_role_props.go`) for
`NodeServerRole` (Security > Server Roles), from "add server roles
properties. use sql management studio as example" — no mockup, SSMS as
reference, same pattern as [[key-props-build-2026-07]].

**Why:** user's own next todo.txt item, same session as the Backup/Restore
Script-button work.

**Shape — General/Members/Owned Roles/Securables, no Owned Schemas/no
Extended Properties:**

- Modeled directly on [[database-role-properties-mockup-build-2026-07|Database
  Role Properties]] (`role_props.go`), the closest existing analogue —
  same page names, same builtin/user-defined split, same rename/owner-change
  UI. Two of that dialog's pages don't apply at server scope and were
  dropped rather than faked: **Owned Schemas** (schemas are database-scoped,
  a server role can't own one) and **Extended Properties** — live-verified
  `sp_addextendedproperty` rejects both `@level0type=N'SERVER ROLE'` and
  `N'LOGIN'` outright ("An invalid parameter or option was specified") —
  server-level principals don't support extended properties at all, a real
  SQL Server limitation, not a gosmo/gossms gap.
- **Securables** reuses a newly-extracted shared helper,
  `pagePrincipalServerPermissions(sc, principalName)` (`prop_grid_helpers.go`)
  — pulled out of the pre-existing `pageLoginSecurables` (Login Properties
  already had this exact page, unmodified logic, just parameterized).
  Login and a server role are both server-level principals holding the same
  kind of `sys.server_permissions` entries, so this is a real dedup, not
  premature abstraction — confirmed needed the moment I started writing
  Server Role's own Securables page and it would've been the identical ~90
  lines. Naming collision note: `pageServerPermissions` was already taken
  by Server Properties' own multi-principal Permissions page
  (`server_props.go`) — the new shared single-principal one is
  `pagePrincipalServerPermissions`.
- **gosmo additions** (`server.go`'s "Server roles" section): `ServerRole`
  struct grew `server *Server, ID, Owner, SID, CreateDate, ModifyDate`
  (was just `Name, IsFixedRole, Members []string`) — mirrors
  `DatabaseRole`'s shape exactly. New `ServerRoleByNameContext` (deep single
  fetch), `ServerRole.RenameContext`/`ChangeOwnerContext`,
  `Server.ServerRoleMembersContext` (typed members, reusing the existing
  `RoleMember{Name,Type}` type), `Server.AddServerRoleMemberContext`/
  `RemoveServerRoleMemberContext` (generic, by name — a role can gain
  either a login or another role as a member). `Login.Add/RemoveServerRoleMemberContext`
  (pre-existing, used by Login Properties' "Server Roles" checkbox-grid
  page) now delegate to the new `Server`-level methods instead of
  duplicating the `ALTER SERVER ROLE ... ADD/DROP MEMBER` SQL.

**Same stale-name-after-rename bug as Key Properties, caught and fixed
before shipping:** first draft passed `roleName string` (plain) into all
four page builders, same shape as `rolePropPages` — renamed
`claude_role_a` → `claude_role_a_renamed`, clicked Apply, and every page's
reload failed with `server role "claude_role_a" not found` (the rename
itself had succeeded; only the reload used the stale captured name).
Fixed identically to [[key-props-build-2026-07]]: `serverRolePropPages`
boxes `roleName` in a shared `*string`, `pageServerRoleGeneral`'s apply
updates `*roleName` in place on success. One wrinkle beyond Key
Properties: `pageServerRoleSecurables` can't just forward to
`pagePrincipalServerPermissions(sc, *roleName)` at page-construction time —
that would freeze the name at dialog-open time, before any rename. It has
to wrap and defer the dereference into its own `load` closure, calling
`pagePrincipalServerPermissions(sc, *roleName).load(ctx)` fresh each
reload.

**Real bug found and fixed, bigger than the new feature itself:** while
live-testing the Securables page's Grant, saw the grid show "GRANT" persist
through Apply + reload with no error banner (only visible on a fuller
screen capture — the error line sits *below* the visible grid rows on a
35-row permission catalog and I initially only captured the grid area) —
but a completely independent read-only SQL check immediately after showed
the grant never actually landed server-side. Root cause: SQL Server
rejects `GRANT`/`DENY`/`REVOKE` at server scope unless the session's
*current database* is literally `master` — real error text: "Permissions
at the server scope can only be granted when the current database is
master." Every gossms connection defaults to whatever database the user
picked (e.g. `HealthClinic`), so this had been silently failing since
`GrantServerPermissionContext`/`DenyServerPermissionContext`/
`RevokeServerPermissionContext` were first added — meaning **Login
Properties' pre-existing Securables page has been non-functional for
Grant/Deny/Revoke this whole time**, not just my new Server Role one;
confirmed by reproducing the identical silent-failure on
`claude_login_a` before touching any code. Fixed in gosmo
(`server_security.go`) by prefixing all three statements with
`USE master; ` in the same batch/exec call — exactly the same idiom
`GrantDatabasePermissionContext` already uses for its own `USE [db]; `
prefix (confirmed via `script_test.go`'s existing assertion), so this
wasn't a new pattern, just applying an established one to the server-scope
sibling that had been missing it. Live re-verified clean after the fix:
Grant → Apply → close dialog → independent SQL read confirms the row
exists; cycle to Deny; cycle to none (Revoke) → Apply → independent SQL
read confirms 0 rows.

**What was and wasn't live-verified:** General (rename+Apply, the fix
above) and Members (Add+Apply, Remove+Apply) both round-tripped for real
against a throwaway `claude_role_a`/`claude_login_a` pair on ubudock, with
every write independently confirmed via `sys.server_role_members`/
`sys.server_permissions` reads. Securables' Grant/Deny/Revoke cycle fully
verified post-fix, both for the new Server Role page and (as the
regression check) the pre-existing Login page. Owned Roles' *empty*-state
render was confirmed live (`sysadmin` and a fresh role both show "0 rows"
correctly); its owner-*transfer* write path itself wasn't separately
exercised through the UI — instead confirmed the underlying
`ALTER AUTHORIZATION ON SERVER ROLE::role TO principal` statement doesn't
share the master-context restriction (works fine from `HealthClinic`
context, direct SQL check), and the UI wiring is the exact same
`propsheet.Select` + `ChangeOwnerContext` pattern already proven via
General's page and Database Role Properties' own Owned Roles page — judged
low-risk enough not to need a third live round-trip in an already very
long session. All test objects (`claude_role_a`, `claude_login_a`) dropped
clean at the end; `sys.server_principals` confirmed empty of `claude%`
afterward.

**Testing-technique note:** this session's Securables bug was only found
because a read-only SQL check was run *independently* of the UI's own
apparent success — the UI's "Apply → reload → still shows GRANT" looked
identical whether or not the write actually landed, since a probe script
run moments earlier happened to (accidentally) issue the same real GRANT
via a master-context connection, briefly making the server state agree
with the UI's stale in-memory render and masking the bug on the first
pass. Always verify a write with a connection/script that has touched
nothing else, not one that's already run the same operation for an
unrelated reason.


---

## 2026-07-30 — Sql agent job props review

`sql-agent-job-props-review-2026-07`

*Post-Phase-4 self-review of Job Properties (Phase 3) found and fixed 4 real bugs, all scoped to Job Properties only; validated by a Plan agent before implementing*

After finishing [[sql-agent-phase4-2026-07]] (all 4 SQL Server Agent
phases done), the user asked for a bug review + plan. Rather than
spawning an Explore/code-reviewer agent, did a direct manual re-read of
[[sql-agent-phase3-2026-07]]'s Job Properties files (I'd just written
them, so no re-derivation cost) and found 3 real issues, then validated
root causes and fix design with **one Plan agent** (given full context,
exact line numbers, and my own root-cause analysis to *confirm or
correct*, not to re-explore from scratch) before writing the plan file
and using `EnterPlanMode`/`ExitPlanMode` for user approval. The Plan
agent's validation caught two things I'd missed (documented below) —
worth the one delegation.

**Bug 1 — Notifications page silently assigns a phantom e-mail operator.**
`pageJobNotifications`'s apply (`agent_job_props_alerts.go`) called
`SetEmailNotifyContext` unconditionally whenever the *page* was dirty
(`PropertySheet.DirtyPages()` is page-level, not per-row). `operatorSelect`'s
baseline falls back to `indexOf`'s index-0 default (an arbitrary real
operator) whenever the job has none configured. Net effect: editing only
the unrelated "Delete job" section on a job with no notify operator, then
Apply, silently wrote a phantom operator into
`sysjobs.notify_email_operator_id`. Fixed by gating each write behind its
own rows' `.Dirty()` (not `.Checked()` — unchecking an already-checked box
must still fire the call to write `NotifyNever`).

**Bug 2 — renaming a job on General broke every other dirty page in the
same Apply/OK.** Every Job Properties page closed over `jobName` as a
frozen `string`; `RenameContext` on General changed the name server-side
but nothing updated the other pages' already-captured copies, so their
`findAgentJob(ctx, sc, jobName)` calls looked up a name that no longer
existed. The Plan agent found this is *worse* than I'd described: even a
**single-page** rename-only + Apply (not OK) reproduces it, since
`PropDialog.runApply`'s `InvalidateAll()` immediately reloads the current
page using the same stale string. Fixed by changing every page function's
signature to `jobName *string`, one shared cell created in `jobPropPages`
(`name := &jobName`), with `pageJobGeneral`'s apply setting `*jobName =
nameRow.Value()` the moment rename succeeds. Confirmed the identical
"plain string across pages, rename on page 0" pattern is unfixed today in
every other `*_props.go` file (login/role/server_role/user/key) —
deliberately left alone, out of scope, Job-Properties-only fix per user
instruction.

**Residual bug found only during live re-verification (not part of the
original plan)**: fixing Bug 2 via the shared `*jobName` cell was not
sufficient for editing/deleting an *existing* step on the same page —
`gosmo.JobStep.UpdateContext`/`DeleteContext` build their SQL from an
**unexported, cached `job *Job` field** set once when the step was first
fetched (`Job.StepsContext`), completely independent of the shared
`*jobName` cell. Live-caught: renamed a job + edited an existing step's
retry_attempts in the same Apply → rename succeeded, step update failed
with a real `job_name does not exist` error (confirmed via direct SQL:
name changed, `retry_attempts` didn't). New-step adds, and every other
page (Schedules/Alerts/Notifications — none of which cache a job
reference the way `JobStep` does), were unaffected. This is a case where
live-testing a fix immediately surfaced a gap the design-validation pass
didn't — **stopped and used `AskUserQuestion` rather than silently
patching around it**, since it was genuinely outside the approved plan's
listed call sites and had a gosmo-vs-gossms scope decision attached. User
picked the gossms-only option: in Steps' apply, lazily re-fetch the
current step list under the freshly-renamed job (`j.StepsContext(ctx)`)
and call Update/Delete on *that* fresh `*gosmo.JobStep`, discarding the
page-load-time-stale `e.orig` for existing-step writes. No gosmo change.

**Bug 3 — Schedules page displayed (and could commit) wrong values
across FreqTypes.** `syncFieldsFromSelection` set `dayOfMonthField`,
`setWeekdayGrid(e.freqInterval)`, `relativeSelect`, `relativeDaySelect`
unconditionally regardless of the selected schedule's actual `FreqType`,
even though `FreqInterval`'s meaning is entirely FreqType-dependent (a
Weekly bitmask vs. a Monthly day-of-month vs. a MonthlyRelative day
code). Mostly cosmetic (e.g. a Monthly day-of-month `8` showing
"Wednesday" checked, since `WeekdayWednesday == 8`) — but the Plan agent
found a real data-corruption path I'd missed: select a schedule, then
change *Occurs* (converting FreqType) without touching the now-relevant
field — `SetValue` resets that field's own dirty baseline, so
`Form.Validate` (dirty-rows-only) never catches the stale out-of-range
value before it's committed. Fixed by wrapping all five fields
(including `recurEveryField`, already correctly gated) in one `switch
e.freqType`, defaulting inapplicable fields to safe in-range values (a
new `defaultWeekdayMask` package var, reused at the pre-existing
nil-selection default too). Confirmed `new_schedule_dialog.go` has no
equivalent bug — it's a fixed single form with no selection-driven
resync, unlike this grid-browsing page.

**Live-verified all 4 fixes end-to-end against ubudock** with disposable
fixtures (`ClaudeReviewJob` + step, two schedules of different FreqTypes,
one operator) — direct SQL queries before/after each Apply, not just UI
state. All fixtures dropped afterward.

**Process note**: `EnterPlanMode` → one `Plan` agent (given my own
analysis to validate, not blank exploration) → `ExitPlanMode` → implement
→ live-test → hit an out-of-plan residual bug mid-verification →
`AskUserQuestion` (didn't guess) → implement the chosen option → re-verify
→ clean up. This is the first time this session used the formal plan-mode
workflow rather than just diving into implementation — appropriate here
since it was a bug-fix pass on already-shipped, live-tested code where an
unreviewed regression risk mattered more than usual.


---

## 2026-07-30 — Sql agent phase1 2

`sql-agent-phase1-2-2026-07`

*SQL Server Agent feature — Phase 1 (gosmo data layer) and Phase 2 (gossms browse tree/detail/actions) built from the SQL-only mockups; 3 real live-caught gosmo schema bugs*

Building SQL Server Agent support in gossms/gosmo from
`todo/mockups/sql_agent_object_explorer_sql_only_tui.txt` and
`job_properties_tui_mockup.txt`, phased: 1) gosmo data + tests, 2) gossms
browse (tree/detail/context actions), 3) edit (Job Properties + Step/
Schedule dialogs), 4) create + reports. Packaging decision: **not** a
standalone tuikit-style subpackage — native `agent_*.go` file group inside
package `tui`/gosmo root, reusing `PropDialog`/`propsheet`/`db.ServerConn`
directly (user explicitly rejected the subpackage option that mirrors
planview, since Agent UI is deeply App-integrated unlike planview's
self-contained showplan input).

**Phase 1 (gosmo)**: extended `agent_job.go` (AgentStatus/AgentInfo via
`sys.dm_server_services`, widened JobStep, Job setters, JobStep Update/
Delete, cross-job `Server.JobHistory`); new `agent_schedule.go` (Schedule
CRUD + `Schedule.Description()` pure recurrence-to-English formatter, unit
tested across every FreqType branch), `agent_alert.go`, `agent_operator.go`,
`agent_category.go`. All `Foo`/`FooContext` pairs, `Seq` variants in
`iter.go`.

**Phase 2 (gossms)**: `agent_explorer.go` (loaders, `isSystemAgentJob`
name-heuristic for syspolicy_ jobs), `agent_detail.go`, `agent_reports.go`
(7 SQL-only admin reports), `agent_menu.go` (Start/Stop/Enable/Disable/
Delete/View History — generic `setAgentEnabled`/`deleteAgentEntity`
helpers in `agent_common.go` shared across Job/Schedule/Alert/Operator).
17 new NodeTypes in tree_node.go.

**Real bugs found via live testing (ubudock, SQL Server 2025 on Linux)**:
1. Phase 1 code review: `Enable()`/`Disable()` across all 4 new gosmo types
   only had no-context wrappers calling an unexported `setEnabled(ctx,bool)`
   — violated CLAUDE.md's Foo/FooContext pairing. Fixed before Phase 2
   even started (caught by gossms compile error, not runtime).
2. **`sysalerts.wmi_query`/`wmi_namespace` don't exist** on this build —
   WMI is Windows-only and this instance is Linux; the columns are
   genuinely absent (`COL_LENGTH` returns NULL), not just unused. Fixed by
   dropping them from the SELECT/struct entirely and redefining
   `Alert.IsEventAlert()` to rely solely on `event_source = 'WMI'` (which
   *is* universal — SQL Server sets it regardless of build).
3. **`sysalerts.include_event_description_in` doesn't exist either** — the
   real *column* is `include_event_description` (no `_in` suffix), but the
   `sp_add_alert`/`sp_update_alert` *stored-procedure parameter* genuinely
   is `@include_event_description_in` (confirmed via `sys.parameters`) —
   column and proc-param names diverge for this one field. Go field kept
   named `IncludeEventDescriptionIn` (matches the more-visible proc param)
   with a doc comment explaining the divergence.

Live-tested: full tree (Jobs→User/System, Schedules, Alerts→Event Alerts,
Operators, 7 admin reports) against real data (7 Data Collector schedules,
0 jobs/alerts/operators on this instance); `Schedule.Description()`
verified correct for both Daily+subday-minutes and FreqAutoStart branches
on live rows; Disable/Enable Schedule context-menu action verified via
direct DB query before/after (real toggle, not just UI-state). Schedule
state restored to original (all re-enabled) after testing.

See [[gossms-tui-tmux-testing]] for the ContextMenu-hover tmux-testing
gotcha this session re-hit the hard way instead of checking first.


---

## 2026-07-30 — Sql agent phase3

`sql-agent-phase3-2026-07`

*SQL Server Agent Phase 3 — Job Properties dialog (7 editable pages) built, live-tested; 4 real bugs found (3 gosmo, 1 gossms)*

Phase 3 of [[sql-agent-phase1-2-2026-07]]: Job Properties dialog, the
first *editable* SQL Server Agent UI (Phases 1-2 were read-only browse).
New `internal/tui/agent_job_props*.go` files (native, same package —
still following the user's "no subpackage" ruling from Phase 0):
`agent_job_props.go` (dispatcher + General + Targets), `_steps.go`,
`_schedules.go`, `_alerts.go` (Alerts + Notifications), `_history.go`.
7 pages: General, Steps, Schedules, Alerts, Notifications, Targets,
History. Extended Properties (the mockup's 8th page) dropped — SQL Server
Agent jobs have no native extended-properties mechanism, and the mockup's
own notes admit it assumes an app-owned metadata table that doesn't exist.

**Architectural deviations from the mockup, deliberate:** Step Properties
and Schedule Properties are **not** separate modal dialogs — they're
inline grid + "selected row" edit-panel pages, reusing
`database_props_files.go`'s established Add/Remove-button idiom (pending
`isNew`/`pendingRemove`/`orig *T` edit-state structs, diffed at Apply
time). Alerts page redesigned entirely: instead of mockup's read-only
list + New/Edit/Remove-Link buttons, it's a toggle-grid over *every*
event alert (Linked column), mirroring `login_props.go`'s User Mapping
page — simpler code, and Alert authoring (New/Edit) is deferred to a
future Alert Properties dialog that doesn't exist yet. Schedules page
supports New (create+attach) and Remove (detach) but not "Pick..."
(attach an existing unrelated shared schedule) — deferred, cut for scope.
gosmo needed **zero** new methods — everything Job Properties needed was
already built in Phase 1, a good sign the original data-layer design was
right.

**4 real bugs found live-testing against ubudock, all in code this
session hadn't yet exercised with a real job/alert/operator present**
(Phase 2 tested against 0 jobs/alerts/operators — these bugs were
invisible until Phase 3 created real ones):

1. **gosmo, `agent_job.go` `JobsContext`/`JobByNameContext`**:
   `ISNULL(ja.next_scheduled_run_date, '19000101')` produced a fake
   `1900-01-01` sentinel instead of real SQL `NULL`, so `NextRunDate`
   was *never* Go's zero `time.Time` — every `.IsZero()` check downstream
   (this page's General "Next run", Phase 2's `agentJobDetail`,
   `agentJobActivityDetail`, `agent_reports.go`'s `dashIfZero`) silently
   showed `1900-01-01 00:00:00` instead of `—` for any job without a
   scheduled run. Predates Phase 3; just never had a real job to expose
   it. Fixed by dropping the `ISNULL` wrapper — real `NULL` now flows
   through `sql.NullTime` correctly.

2. **gosmo, `agent_job.go`/`agent_schedule.go`, `job_id` round-trip**:
   `SELECT j.job_id` (a `uniqueidentifier`) scanned directly into a Go
   `string` field (`Job.JobID`) captures the driver's raw 16-byte GUID
   binary as "text" (go-mssqldb returns `[]byte` for uniqueidentifier;
   database/sql's default `[]byte`→`string` conversion is a byte-cast,
   not hex formatting). Re-using that corrupted value as a bound `@p1`
   parameter in a later query (`Job.StepsContext`, `Job.HistoryContext`,
   `Job.SchedulesContext`) failed with `mssql: Conversion failed when
   converting from a character string to uniqueidentifier`. This is a
   systemic Phase 1 bug — every one of those methods was broken from day
   one, just never round-tripped `JobID` as a parameter before Phase 3's
   Steps page did. Fixed by wrapping every `SELECT j.job_id` with
   `CONVERT(varchar(36), j.job_id)` in `JobsContext`, `JobByNameContext`,
   and `Schedule.JobsContext`. **Any future gosmo `uniqueidentifier`
   column that gets scanned into a Go string and later reused as a query
   parameter needs the same `CONVERT(varchar(36), ...)` treatment** —
   worth grepping for before adding new ones.

3. **gosmo, `agent_job.go` `CreateJobContext`**: never called
   `sp_add_jobserver` after `sp_add_job`, so any job created through it
   (or via raw `sp_add_job`, which is what the live-test fixture used)
   couldn't be started (`sp_start_job`: "does not have any job server or
   servers defined") or targeted by an alert (`sp_update_alert`: "cannot
   be used by an alert. It should first be associated with a server").
   SSMS's own New Job dialog does this enlistment invisibly. Fixed by
   adding `EXEC sp_add_jobserver @job_name=..., @server_name=N'(local)'`
   right after the job is created — multi-server (MSX/TSX) target
   selection stays out of scope, `(local)` is the only target. This
   predates Phase 3 (Phase 1 code, unused until now — `CreateJob` has no
   gossms UI caller yet, Phase 4's future New Job dialog will be the
   first).

4. **gossms, `agent_job_props_alerts.go` (this session's own bug)**: the
   Alerts page's toggle grid was missing `grid.SetCellCursor(true)` —
   without it, `DataGrid.HandleKey`'s `case tcell.KeyEnter: if
   g.cellCursor && ... { g.activateCell() }` never fires, so the Linked
   checkbox silently didn't toggle on Enter/Space (mouse click has the
   same gate). Caught immediately via live testing since the checkbox
   visibly never flipped; `login_props.go`'s User Mapping page (the
   pattern being followed) *does* call it — just missed transcribing it.
   One-line fix.

**Live-tested end-to-end against ubudock** with disposable fixtures
(`ClaudeTestJob` + 2 steps, `ClaudeTestAlert`, `ClaudeTestOperator`,
created via raw SQL since no New Job dialog exists yet) — General page's
bug-fixed "Next run" display, Steps page edit+Apply (retry_attempts
3→verified via direct query), Schedules page New (create+attach,
freq_type/freq_interval verified via query), Alerts page Linked toggle+
Apply (verified `sysalerts.job_id` correctly points at the job), Notifi-
cations page e-mail checkbox+operator+condition+Apply (verified
`notify_level_email`/`notify_email_operator_id`), Targets and History
pages render correctly read-only. All fixtures dropped afterward,
counts verified back to 0.

**Tmux-testing note not yet in [[gossms-tui-tmux-testing]]**: typing into
`ConnectDialog`'s Server field opens a "recent connections" autocomplete
dropdown overlay (new since that memory was last written) — `Tab` while
it's open navigates the dropdown instead of the dialog's next field,
scrambling every subsequent field with garbage connection-string text.
Send a lone `Escape` right after typing the server name (closes only the
dropdown, not the dialog) before tabbing onward. Also re-hit: `Tab`
count to a `ButtonsRow` deep in a long form is easy to miscount by one —
verify with a per-keystroke `-e` capture grepping for the target button's
text preceded by `48;2;0;122;204`, don't just count and trust it (burned
real time once landing on Cancel-not-New, once on a Static row instead
of a grid).

See [[gossms-live-test-server]] for the ubudock connection details this
session reused.


---

## 2026-07-30 — Sql agent phase4

`sql-agent-phase4-2026-07`

*SQL Server Agent Phase 4 — New Job/Schedule/Alert/Operator creation dialogs, all 4 live-tested end-to-end; no new bugs found*

Phase 4 (final phase) of [[sql-agent-phase1-2-2026-07]] and
[[sql-agent-phase3-2026-07]]: four creation dialogs — New Job, New
Schedule, New Alert, New Operator — reached from "New ...\" on the
matching Object Explorer folder (Jobs > User Jobs, Schedules, Alerts >
SQL Server Event Alerts, Operators). Each follows `new_login_dialog.go`/
`new_database_dialog.go`'s established shape exactly: a bespoke struct
embedding `*propsheet.PropertySheet`, a one-time "prefetch" fetched lazily
on first page load, pages/applies built into fixed-size arrays (not a
discovered dirty set — page 0's apply always creates the entity first),
`preflight()` for name-required/uniqueness checks Form.Validate can't
express, and OK/Apply/Script Changes/Cancel wired identically to every
other dialog. New files: `new_operator_dialog.go` (1 page),
`new_alert_dialog.go` (General + Response — notify-operators toggle
grid), `new_schedule_dialog.go` (General + Jobs — attach-to-existing-jobs
toggle grid), `new_job_dialog.go` + `new_job_pages.go` (General, Steps,
Schedules, Notifications). Also added `ObjectExplorer.RefreshFolderByType`
(object_explorer.go) — one generic depth-first-search refresh helper
replacing what would've been 4 more hand-written `RefreshXFolder` walks
like `RefreshLoginsFolder`, since Agent folders sit at varying depths
under SQL Server Agent (Jobs > User Jobs is 3 levels, Schedules is 2).

**Scope cuts, deliberate:** New Job has no Alerts/Targets/History pages
(nothing meaningful to configure before the job exists — alert-job
linking still lives on Job Properties' own Alerts page, matching Phase
3's same call for `pageJobAlerts`). New Schedule's Jobs page and New
Job's Schedules page only *attach existing* schedules/jobs (a toggle
grid) — no "pick from a filtered list" or nested creation; create the
other side first, then attach, either here or from its own Properties
page afterward. New Alert has no response-job field at all (same
reasoning). gosmo needed **zero** new methods again — `CreateJobContext`/
`CreateScheduleContext`/`CreateAlertContext`/`CreateOperatorContext` from
Phase 1 were exactly what each dialog's General-page apply needed.

**Reuse note**: New Schedule's and New Job's frequency/step-editing code
reuses agent_job_props_schedules.go's/agent_job_props_steps.go's package-
level vars and types directly (`scheduleOccursItems`, `jobStepEdit`,
`jobStepOnActionItems`, `parseAgentClock`, `atLeast1`, etc.) — no
duplication, since a shared schedule's definition and a T-SQL step's
definition are identical whether created standalone or inline on an
existing job's Properties page.

**Live-tested end-to-end against ubudock, all 4 dialogs, no new bugs
found** (a first for this project's Agent phases — Phases 1-3 each
turned up real bugs) — likely because every code path here reuses
already-live-tested idioms from Phase 3 rather than introducing new SQL
generation. New Operator (name/email/enabled → verified via
`sysoperators` query), New Alert (severity-triggered since an arbitrary
`message_id` needs `sp_addmessage` pre-registration first — expected SQL
Server behavior, not a bug, same gotcha hit and already known from Phase
2's live testing), New Schedule (freq_type/freq_interval verified),
New Job (name/enabled/1 step/**enlisted via `sysjobservers`** all
verified — confirms Phase 3's `CreateJobContext` → `sp_add_jobserver` fix
actually works for a job created through the real UI path, not just the
manual-SQL fixture Phase 3's live test used). All fixtures dropped
afterward, counts verified back to 0.

**Two new tmux-testing gotchas, not yet in [[gossms-tui-tmux-testing]]**
(added there directly, cross-referenced here):
1. `propsheet.PropertySheet.HandleKey` checks `KeyF5` **unconditionally
   first**, before any zone/visibility routing to whatever's "supposed"
   to have focus — an `F5` meant for a query editor that lands on a
   still-open Properties/New-X dialog instead (e.g. because a Cancel/OK
   click a moment earlier didn't actually register) triggers that
   dialog's own page Refresh, which pops a "Discard Changes" confirm if
   the page is dirty. Burned real time here: a `[ OK ]` click coordinate
   computed from a stale capture missed the button, the dialog silently
   stayed open, and a subsequent `F5`+typing sequence intended for SQL
   all landed inside the still-open dialog instead — the giveaway was a
   verification query never producing new results and a "Discard
   Changes" popup appearing out of nowhere several steps later.
2. **Always verify a dialog actually closed by capturing the full pane
   height, not just the top rows** — a Properties/New-X dialog renders
   starting around row 10, so a capture limited to rows 1-5 (e.g. to
   check the tree) shows a false "it's gone" even when it's very much
   still open and eating every subsequent keystroke.

No further phases planned — this completes the SQL Server Agent feature
from the original 4-phase plan (data layer → browse → edit → create).


---

## 2026-07-30 — Sql agent schedule alert operator props

`sql-agent-schedule-alert-operator-props-2026-07`

*Built Schedule/Alert/Operator Properties dialogs from scratch and redesigned Job Properties' Schedules page to an attach-toggle grid, closing the SQL Agent 'can't edit or see schedules' gaps the user reported; found+fixed a real cross-cutting ToggleGrid rendering bug*

Follow-up to [[sql-agent-job-props-review-2026-07]]. The user reported two
concrete SQL Agent gaps: no way to edit/delete an existing schedule (Delete
existed, Edit didn't, anywhere), and Job Properties' Schedules page only
listed schedules already attached to the job — with 8 shared schedules on
the test server and 0 attached to the only user job, the page looked
empty with no way to discover what was available. Asked for a full review
with freedom to redesign; used `EnterPlanMode` (research done directly,
no Explore agent needed since I'd built this code), two `AskUserQuestion`
calls to settle the two real design decisions (all three of Schedule/
Alert/Operator Properties, not just Schedules; redesign to an attach-
toggle grid mirroring `pageJobAlerts` rather than bolt on a Pick button),
then implemented.

**What shipped** (all in `internal/tui`, zero gosmo changes — every needed
setter already existed):
- `agent_schedule_form.go` (new): extracted the Occurs/Recurs-every/Day-
  of-month/Weekdays/Relative/Daily-frequency/Duration field set, previously
  duplicated between `new_schedule_dialog.go` and the old inline Schedules-
  page editor, into one `scheduleFreqForm` reused by both New Schedule and
  the new Schedule Properties.
- `agent_schedule_props.go` / `agent_alert_props.go` / `agent_operator_props.go`
  (all new): standalone edit dialogs via the existing shared `a.propDialog`,
  same pattern as Job Properties — "Properties..." added to each entity's
  Object Explorer context menu. Built the shared `*string` name-cell fix
  from [[sql-agent-job-props-review-2026-07]] (Bug 2) into all three
  **from the start** this time, rather than shipping the rename-breaks-
  same-pipeline bug again and fixing it in a later pass.
- `agent_job_props_schedules.go`: `pageJobSchedules` rewritten from a ~15-
  field inline "selected schedule" editor to a grid of **every** shared
  schedule with an Attached toggle (mirrors `pageJobAlerts`'s shape
  exactly) + a read-only detail panel. This is the direct fix for the
  "can't see available schedules" complaint — schedule *editing* moved
  entirely to the new Schedule Properties dialog, so a shared schedule's
  definition has exactly one place it's edited.
- Deliberately dropped from the plan: an inline `[New...]` button on the
  Schedules-attach grid (would need cross-dialog-stack refresh plumbing
  with no precedent elsewhere in the app) and a "Used by jobs" count in
  the attach grid's detail panel (would cost one extra round trip per
  schedule, N+1-style, just to duplicate what Schedule Properties' own
  Jobs page already shows one click away).

**Real bug found live-testing, fixed same session — `propsheet.ToggleGrid`
single-column misuse.** `NewToggleGrid(columns, toggleCols, height)`
requires at least one column NOT in `toggleCols` for `SetRows`'s `text`
parameter to render into — `ToggleGridRow.render()` only writes into a
row slot when `slices.Index(toggleCols, c) < 0`, i.e. a non-toggle column
must exist for the label to ever appear. Every call site written as
`NewToggleGrid([]string{"X"}, []int{0}, n)` (one column, and that one
column IS the toggle) silently drops the label text passed via `SetRows`'s
`text` argument — renders a column of bare `[ ]`/`[x]` cells with **no
indication of what each row is**. Confirmed live: Alert Properties'
Response page (operators-to-notify grid, new this session) and Schedule
Properties' Weekdays grid (extracted verbatim from pre-existing code)
both showed exactly this. Grepped every `NewToggleGrid` call site and
found 3 more pre-existing instances of the identical bug, unrelated to
today's work: `new_alert_dialog.go`'s own Response page (the page my new
Alert Properties page was told to mirror — so I'd have shipped a working
copy of a broken original had I not caught it), `new_job_pages.go`'s
"attach existing schedule at creation" grid, and `new_schedule_dialog.go`'s
"attach to jobs" grid. Used `AskUserQuestion` since 3 of the 5 sites were
outside this session's plan; user picked "fix everywhere now" over
"fix only my new code" or "document only". Fix is a one-line change per
site — add a second column header, e.g.
`NewToggleGrid([]string{"Notify"}, ...)` → `NewToggleGrid([]string{"Notify",
"Operator"}, ...)` — since every call site already passed the right label
text via `SetRows`, just into a grid with nowhere to put it.

**Live-tested against ubudock**, direct SQL before/after every write, not
just UI state: Schedule Properties rename + frequency + enable-toggle in
one Apply (confirmed via `sysschedules`), Script Changes generated correct
`sp_update_schedule` T-SQL gated only by actually-dirty fields, Job
Properties' redesigned Schedules page attach and detach both round-tripped
through `sysjobschedules` correctly, Alert Properties General field edits
(`sysalerts.notification_message`) and Response page's operator-notify
toggle (`sysnotifications`) both landed correctly post-fix (showed the
bare-checkbox bug pre-fix, confirming the ToggleGrid diagnosis was real
before writing the fix).

**Real environment quirk found, not a code bug — noted for future ubudock
sessions**: `gosmo.Operator`'s entire write surface (`Rename`/`Enable`/
`Disable`/`SetEmailAddress`/`SetCategory`) routes through the single
`sp_update_operator` proc, which SQL Server itself blocks — `SQL Server
blocked access to procedure 'dbo.sp_update_operator' of component 'Agent
XPs' because this component is turned off` — on ubudock's *current*
config (`sys.configurations` confirms `Agent XPs` = 0). Reproduced
directly via `sqlcmd`, outside any of my code, so this isn't a gossms/gosmo
defect — Operator Properties' Apply correctly surfaced SQL Server's real
error message and left the dialog open with the edit intact rather than
silently failing. `sp_add_operator`/`sp_delete_operator` and every other
Agent entity's update procs (`sp_update_schedule`, `sp_update_job`,
`sp_update_alert`) tested fine in the same session under the same `Agent
XPs=0` condition — the block is specific to `sp_update_operator`. If a
future session hits an unexplained `sp_update_operator` failure on
ubudock, check `SELECT value_in_use FROM sys.configurations WHERE
name='Agent XPs'` before assuming a code regression.

**Process note**: this is the first EnterPlanMode pass this session that
started from a broad, open-ended "review + you can redesign everything"
ask rather than a specific bug list — used `AskUserQuestion` twice in
plan mode (edit-dialog scope, Schedules-page redesign shape) to pin down
the two real judgment calls before writing the plan, then a third
mid-implementation `AskUserQuestion` when live-testing surfaced the
ToggleGrid bug, consistent with [[sql-agent-job-props-review-2026-07]]'s
established pattern of pausing on genuinely new, out-of-plan findings
instead of silently expanding scope.


---

## 2026-07-30 — System catalog folders

`system-catalog-folders-2026-07-18`

*System Views/Procedures/Functions folders added under Views/Stored Procedures/Functions in Object Explorer, backed by 3 new gosmo Database methods*

Added "System Views", "System Procedures", "System Functions" folders as
the first child under each database's Views/Stored Procedures/Functions
nodes in Object Explorer — the sys-schema catalog objects (identical in
every database on a server), populated lazily only when that folder is
first expanded (standard on-demand `childLoader`, no eager/connect-time
load).

**gosmo additions** (`~/go/gosmo/database.go`, `iter.go`): three new
paired methods mirroring the existing `Views`/`StoredProcedures`/
`UserDefinedFunctions` family — `SystemViews(Context)`,
`SystemStoredProcedures(Context)`, `SystemFunctions(Context)` — all
querying `sys.all_objects`/`sys.all_sql_modules` (not the non-`all_`
catalog views, which hide `is_ms_shipped=1` rows) filtered to
`SCHEMA_NAME(schema_id) = 'sys'`. Type filters chosen to mirror the
existing user-object methods exactly: Views→`'V'`, Procedures→`'P','PC'`
(matching what `sys.procedures` itself documents — extended procs `'X'`
deliberately excluded), Functions→`'FN','TF','IF'` (same set
`UserDefinedFunctionsContext` already uses, so `AF`/`FS` are excluded
there too). Live-verified against ubudock: 607 system views, 1485 system
procs, 138 system functions in `HealthClinic`, byte-identical counts in
`GoSMODemo` — confirms "same for every database" empirically, not just
by assumption. Plus `SystemViewSeq`/`SystemStoredProcedureSeq`/
`SystemFunctionSeq` in iter.go per gosmo's per-method Seq convention.

**gossms additions** (`internal/tui/tree_node.go`, `explorer_objects.go`,
`explorer_loaders.go`): 3 new folder-only `NodeType`s
(`NodeSystemViews`/`NodeSystemProcedures`/`NodeSystemFunctions`), each
with its own `childLoader` hitting the new gosmo methods directly — a
fresh per-node DB query on first expand, like every other Object Explorer
folder, **not** a share of the in-process sys-schema completion-inventory
cache (`sysCompletionInventories`, see [[intellisense-followups-2026-07-17]]).
Deliberately rejected literal cache-sharing: `fetchChildren`/childLoaders
run on a background goroutine (see `explorer_loaders.go`'s own doc
comment on that constraint), while `sysCompletionInventories` is
mutated only from the UI goroutine via `postEvent` — touching it from a
loader would be a real data race. Also would have meant folding
procedures/functions into `gosmo.Catalog`/`CatalogObject`, which
`completion_provider.go` treats as FROM-clause-selectable objects —
mixing in non-selectable procs/functions would have polluted "sys."
member-completion. Leaf nodes reuse the existing `NodeView`/
`NodeStoredProcedure`/`NodeFunction` types (schema="sys") rather than
inventing new ones — context menu, drag-to-editor FQN quoting, and icons
all work unmodified as a result; live-verified "Select Top 1000 Rows" on
`sys.all_columns` executes for real (1000 rows back).

**How to apply:** if a future ask wants Object Explorer to reuse
in-memory App-level caches (intellisense or otherwise) from inside a
childLoader, the background-goroutine constraint above is the reason
that needs a different plumbing approach (e.g. resolve on the UI
goroutine before spawning the fetch), not a straight passthrough.


---

## 2026-07-30 — Table properties mockup build

`table-properties-mockup-build-2026-07`

*Table Properties built from scratch against todo/mockups/ssms_table_properties_tui_mockup.md (2026-07-15) — gossms had zero table-object-properties support before this; one real ListBox bug fixed, plus two Add-button corruption bugs of the same shape as Files' earlier fix*

Unlike Server/Database Properties (partial implementations, gap-filled),
**Table Properties did not exist at all in gossms** before this session —
no `internal/tui/table_props.go`, no menu entry, nothing. Built from
scratch spanning `~/go/gosmo` and `~/go/gossms` (active `replace` in
`gossms/go.mod`), following `internal/tui/login_props.go`'s pattern (the
closest prior "new object type" precedent).

**Shipped, 6 of the mockup's 12 pages**: General, Columns (read-only grid +
inline "selected column" detail, no popup modal), Storage (space usage +
partitions grid), Change Tracking (per-table, reusing gosmo's existing
`TableChangeTrackingContext`/`SetTableChangeTrackingContext`), Permissions
(two-pane matrix reusing `buildPermissionsMatrix` from
[[server-properties-mockup-gapfill-2026-07]]/[[database-properties-mockup-gapfill-2026-07]]),
Extended Properties (now via a **shared** `buildExtendedPropertiesForm` in
`prop_grid_helpers.go`, extracted from `pageDatabaseExtendedProperties`
since a second caller needed the identical UI — same rationale
`buildPermissionsMatrix` was extracted for one session earlier).

**Deliberately not built, all 6 confirmed empty/unused on the live test
server** (`~/go/gossms` memory: [[gossms-live-test-server]]): Temporal,
Ledger, Memory Optimization, FileTable, External Table, Stretch — verified
live that every real table on ubudock is a plain disk-based user table.
Also skipped, consistent with both prior passes: filter/search, WITH GRANT
OPTION, Effective Permissions, and a Column Permissions modal (gosmo's
`sys.database_permissions` query hardcodes `minor_id = 0`, excluding
column-level grants — a real, separate gosmo gap flagged for later).
**Table Dependencies needed zero new work** — `a.showDependencies(node)` /
`PropDialog.ShowDependencies` was already wired for `NodeTable`.

**gosmo additions**: `ObjectPermission` catalog expanded from 6 to 10
names (added `PermAlter`/`PermReferences`/`PermTakeOwnership`/
`PermViewChangeTracking`, **deliberately excluding `PermExecute`** — verified
live that `GRANT EXECUTE ON <table>` fails with "Granted or revoked
privilege EXECUTE is not compatible with object"; EXECUTE only makes sense
for procs/functions, which would need their own catalog) +
`ObjectPermissionNames()`/allowlist validation mirroring
`Server`/`DatabasePermissionNames()`; `PermissionEntry.Grantor` (was
missing — `Database`/`ServerPermissionEntry` already had it);
`Table.SpaceUsedContext` (new, `sys.allocation_units` breakdown);
`Table.PartitionsContext` (added — `Partitions()` existed with no Context
variant, breaking the `Foo`/`FooContext` convention); `scriptColType`
exported as `ColumnTypeString` (was scripter-only, now reused for the
Columns page's Type column — one canonical column-type renderer instead of
two); `Table.DetailContext` (new `TableDetail` struct — owner, lock
escalation, ANSI NULLs, replicated/CDC flags, temporal/ledger/durability
descriptors, PK name, data-space filegroup — kept off the base `Table`
struct since that's queried on every Object Explorer tree expand, and
these are only needed once, when the General page opens).

**One real, pre-existing propsheet/tuikit bug found and fixed** (not
introduced by this session, present since at least the Database Properties
pass one session earlier, silently unnoticed until now): `propsheet.
PropertySheet.Show()` calls `pageList.SetSelected(0)` *before* the first
`Draw()`/`Layout()` pass ever calls `pageList.SetBounds(...)` — so
`ListBox.ensureVisible()`'s very first invocation runs against a
zero-value `rect.H`, scrolling the page list one row too far and hiding
the actually-selected first page (`▸ General`) above the visible window.
Fixed by making `ListBox.SetBounds` re-run `ensureVisible()` whenever real
bounds become known — self-corrects on first render and on any later
resize. Confirmed retroactively that the Database Properties session's
very first "Files, Filegroups, ..." capture (no visible "▸ General") had
the exact same symptom and went unrecognized as a bug at the time.

**Two bugs of the identical shape found in Add buttons that share input
fields between "edit the currently selected row" and "create a new row"**:
the Files page's Add (fixed the session before this one) and — newly found
here, via reuse — `buildExtendedPropertiesForm`'s Add (inherited unchanged
from `pageDatabaseExtendedProperties`, so this was **already live** in
Database Properties, just never exercised with a pre-existing selected row
during that session's testing). Root cause both times: `commitCurrent()`
called at the top of Add's `onClick` writes the shared field's live text
into whichever row was last *selected in the grid* — safe only if that
shared field is never meant to double as "new row" input too. Fixed the
same way both times: don't call `commitCurrent()` from Add at all; a
not-yet-committed edit to the previously selected row is silently dropped
if Add is clicked without reselecting first, which is an acceptable
trade-off next to silently corrupting the wrong row's data.

Verified end-to-end against a disposable `claude_test_tbl` database
(`Server.CreateDatabase`/`DropDatabase`, plus a disposable `claude_test_user`
principal via `CREATE USER ... WITHOUT LOGIN` for permission-grant testing
— granting to `dbo` itself is a **guaranteed real SQL Server no-op**, since
the owner already has implicit control and GRANT to it creates no
`sys.database_permissions` row; this cost real debugging time before being
recognized as expected behavior, not a bug — pick a genuine non-owner
principal for any future permission-apply verification). See
[[gossms-tui-tmux-testing]] for the tmux mechanics.

---

## 2026-08-01 — Max Result Rows removed; scan path made memory-lean

*Deleted the row cap end to end at user request (all rows returned, OOM
accepted), then cut the cost of retaining them: cell text and per-row slices
packed into a `cellArena`, cell rendering moved to append form.*

**Removal.** `config.Config.MaxResultRows`, `config.DefaultMaxResultRows`,
the Options dialog's "Max result rows" field and `zoneMaxResultRows` (dialog
height back to 15 from 17), the `maxRows` parameter on `Execute`/
`ExecuteWithPlan`/`execute`/`executeWithSink`/`runBatch`/`scanNext`/
`scanResultSet`, `scanResultSet`'s `truncated` return, and the "Only the
first N row(s) are shown" notice. `QueryPanel.runMode` stayed — its other
reason to exist (the Query menu can switch modes while the save dialog is
open) is unaffected — but its comment lost the row-cap rationale.
`config.Load` no longer coerces the field; a `config.json` still carrying
`max_result_rows` just ignores it, pinned by
`TestLoadIgnoresRemovedMaxResultRows`.

**What replaced it.** With no cap, the retained form is the whole cost, so:

- `cellArena` (`internal/query/arena.go`) packs a result set's cell text into
  64 KiB chunks and its per-row `[]string` into 4096-slot chunks — one
  allocation per chunk instead of one per cell plus one per row. Strings are
  cut from a chunk with `unsafe.String`, which is safe **only** because a
  chunk is fixed-size and append-only: every later append writes past the
  bytes already handed out, and the chunk stays alive as long as any string
  cut from it. Replacing the fixed chunk with a plain growing `append` would
  corrupt every earlier string on the first reallocation —
  `TestCellArenaStrSurvivesChunkReuse` is what catches that. `row(n)` returns
  a three-index slice so an append to one row can't reach the next row's
  cells.
- `formatValue`/`formatGUID`/`formatFloat` gained `appendValue`/`appendGUID`/
  `appendFloat` forms and became thin wrappers over them (the tests still
  drive the wrappers). `rowScanner` renders every cell through one reused
  `buf` and copies it into the arena, so nothing per-cell is allocated on the
  way. `appendHexUpper` replaces `hex.EncodeToString` + `strings.ToUpper`,
  which built two throwaway copies of every binary cell.
- `rowScanner.scan` now writes into a caller-supplied row slice and nils
  `sc.vals[i]` after rendering, so the driver's own copy of the last row's
  cells isn't pinned for the life of the `Result`.
- `streamResultSet` (Results To File) reuses one row buffer for the whole
  set and passes a nil arena — it retains nothing, so packing would be pure
  overhead. `TestStreamResultSetWritesEveryRow` now checks all 500 rows are
  distinct, which is what would fail if the reused buffer leaked to a sink.

**Measured** (`BenchmarkScanResultSetArena` vs `…Naive`, 50k rows × 5 cols):
800k allocs → 100k, 21.1 MB → 14.9 MB allocated, ~148 ms → ~101 ms. Retained
heap for 200k rows fell ~15% (37.4 → 31.8 MiB). The remaining floor is the
16-byte string header per cell that `ResultSet.Rows [][]string` implies —
recorded in `docs/open-threads.md`, not worth changing the type and every
`DataGrid` consumer for.

## 2026-08-01 — Paste in the query editor: two independent bugs

Reported as "Ctrl+V doesn't work in the query editor (xfce4-terminal), the
terminal's own Paste does — and IntelliSense rewrites what it pastes." Two
separate causes, both reproduced live against ubudock and A/B'd against a
pre-fix binary.

**1. Paste hit the wrong widget.** `activeClipboardTarget` chose among the
Messages view / execution plan / Results To Text / results grid purely by
which tab was showing, ignoring which half of the panel had keyboard focus.
Those views are read-only, and `Editor.Paste` returns silently when
`readOnly` — so any execution that left the pane on Messages (a `PRINT`, a
`SET`, an error, any non-SELECT batch) redirected Ctrl+V and Edit > Paste
away from the SQL editor the user was typing in, with no feedback at all.
It looked terminal-specific only because the terminal's own paste never
goes through this code. Fixed by gating the whole results-side branch on
`qp.resultsHasFocus()`; the grid's "Show Value" viewer still wins from
either side, since it's a modal overlay with its own selection. Ctrl+C on a
focused results grid used to copy the *editor's* selection through the same
hole — it now copies the grid's selected cell/block via the new exported
`DataGrid.SelectedCellsText`, matching its right-click "Copy".

**2. Bracketed paste was replayed as typing.** `core.Init` calls
`EnablePaste`, but tcell only brackets the paste with `EventPaste`
start/end markers — the content still arrives as ordinary `EventKey`s, and
`Run` had no case for the markers. So a terminal paste ran the full typing
path: each pasted newline arrived as `KeyEnter`, and with the completion
popup open on the token the paste passed through, `handleCompletionKey`
committed the selected candidate instead of inserting the newline.
Live repro: popup open on `sys.`, pasting `objects\nWHERE type = 'U'`
produced `sys.objectsWHERE type = 'U'` — the newline gone, a syntax error.
`Run` now buffers keys between the markers (`beginBracketedPaste` /
`bufferPastedKey` / `endBracketedPaste` in `clipboard.go`) and applies them
through `clipboardTarget.Paste` as one edit — one undo step, and anything
that isn't a rune/newline/tab is dropped rather than acted on, so a paste
can't trigger a command. `Editor.Paste` also closes the completion popup
now and never re-queries the provider.

---

## 2026-08-01 — Triage of the top open threads: two closed, three re-classified

*User asked for the top five known issues, then ruled on each: gosmo tagging
and the unbounded result set are by design, DropColumn/AddColumn may stay
non-functional, and the two lexer-awareness bugs get fixed.*

**Re-classified, not fixed.** The `replace`-directive/untagged-gosmo item had
sat at the top of `open-threads.md` as "blocking the next release" since
2026-07-18. It isn't a defect: tagging gosmo and commenting out the `replace`
pair are steps of `RELEASE.md`, and the state it describes is the intended
development state. Likewise the unbounded Grid/Text result set — intended
since the Max Result Rows removal earlier the same day, SSMS parity over a
silent cap. `Table.DropColumn`/`AddColumn` may stay non-functional for now;
they aren't on the path to a release and gosmo's no-removal rule keeps them
in place regardless. All three moved to a new "By design — not issues" section
at the top of `open-threads.md`, which is where the next review should look
before re-raising any of them.

**Script Changes on multi-page create dialogs.** Reported as
`gosmo: schedule "X" not found` from New Schedule; the root cause turned out
to be two independent reads on a path that was supposed to be write-only.
gosmo's four Agent `Create*Context` methods end with a `...ByNameContext`
read-back to populate the object they return — and `WithScript` only
intercepts the exec chokepoints, so under script mode that read went to a real
server looking for an object whose `sp_add_*` had merely been *collected*.
The gossms side then repeated the same lookup in the dependent page's apply
(`JobByNameContext` in three places, `AlertByNameContext` in one). Fixed at
both layers: gosmo gained `Server.Schedule/Job/Alert/Operator` — name-only
handle constructors, the Agent counterparts of the existing `Server.Database`,
*added* alongside the `ByName` forms rather than replacing them — and returns
one under `Scripting(ctx)`; gossms's pages go through new
`scriptSafeJob`/`scriptSafeAlert` helpers. Every write reachable from those
handles builds its statement from the name alone, which is what makes the
lightweight form sufficient. Caught in passing:
`Job.SetEmailNotifyContext` assigned `NotifyEmailOperatorName` directly,
bypassing `setIfApplied` — the last survivor of the 2026-07-30 sweep that put
all 39 mirrored writes behind it. The gossms test runs with a nil `Server` on
purpose, so a helper that still queries panics instead of quietly passing.

**GO inside a block comment.** The completion scan's GO detection was a
separate textual pass over `lines`, so a commented-out `GO` scoped completion
to the wrong statement and `SELECT * FROM dbo.Patients p /* GO */ WHERE p.`
offered nothing. Moved the detection into `lexSQL` itself: a line is a
separator only if it *begins* in `sqlLexNormal`, which falls out of the state
machine for free and covers block comments, string literals and bracketed
identifiers in one rule. That also collapsed the two-pass scan's resume logic
— both boundaries `scanCompletionPrefix` can pick are normal-state positions
by construction, so the second pass always resumes in `sqlLexNormal` and the
`mark`/`stateAtMark` plumbing went away with `lastGoBatchStart` and
`statementStartOffset`. Slightly faster than before, since the backwards
per-line scan is gone (5.71ms vs 6.01ms at 1,000 statements).

The differential tests were the delicate part: they existed to pin the
*old* behavior, so their baselines had to be made lexer-aware
*independently* — `referenceLineStartsNormal` is a plain
character-at-a-time walk with no shared code with `lexSQL`. The 400
generated scripts still check production against something written
separately, and reverting the baseline to the textual rule makes the corpus
fail on exactly the commented-out and quoted GO cases, which is the A/B.

`controls/sql_statement.go` needed no change at all. `open-threads.md` had
claimed Ctrl+Enter shared the bug and that the two should be fixed together;
its `isGoSeparatorLine` test was already inside the state machine, guarded by
`state == stNormal`. Pinned by a test now rather than left as an assumption.

**`/*` inside a `--` comment or a string literal.** Same shape, different
scanner: `blockCommentToggleEnd` toggled on every `/*`/`*/` regardless of
context, so one in a line comment left the highlighter "inside a comment" for
the rest of the document. It now skips `--` and `'...'` exactly as the
highlighter's main loop does, sharing the extracted `stringLiteralEnd` with
it so the two can't drift. The memo/replay invariant is untouched, which was
the constraint that made this look risky: both paths still call the one
function, so a line's colour still can't depend on whether it happened to be
the first visible row of a Draw pass. Quotes deliberately carry no meaning
*inside* a comment — `'*/'` still closes one — which is the T-SQL rule and is
pinned by its own test.

**Live check against `ubudock`.** A throwaway job, then New Schedule's two
applies driven under `WithScript`: the collector produced `sp_add_schedule` +
`sp_attach_schedule`, both ran clean when executed for real, and the schedule
came back attached to the job. A/B: reverting the `CreateScheduleContext`
guard reproduces the reported `gosmo: schedule "zz_throwaway_sched" not
found` at page 1 — the same message the original report carried. Throwaway
objects dropped afterward (`sp_delete_job` takes the now-unused schedule with
it, which is also incidental confirmation the attach was real).

One thing the check turned up: a name-only handle is sufficient for *writes*
only, exactly as documented — `Job.SchedulesContext` reads by `JobID` and
fails on one with "Conversion failed when converting from a character string
to uniqueidentifier". That is the intended boundary, not a gap.

**Still to do:** the dialog's own Script button in front of a human, and the
New Job / New Alert equivalents — same code shape, unit-tested only.

---

## 2026-08-02 — Editor and InputField display width, and the Document chokepoint

`editor-display-width-and-document-2026-08`

*Four open-threads items closed as one change: both text widgets became
column-aware, and the two O(document)-per-Draw scans got a version counter
they could finally be keyed on*

Asked for the top five known issues, then for 1-4 of them. Those four were
`Editor` and `InputField` indexing by rune rather than display width, the
highlighters' block-comment replay being O(document) on the first row of
every Draw pass, and `buildVisualLines` walking the whole document on every
Draw. They came out as one change because open-threads had already recorded
why: the two width items were "the smaller half of the same rework", and the
two performance items were gated on "a single mutation chokepoint or
neither".

**Width.** The model chosen was to keep every *text* position a rune index
(cursor, selection anchor, `ColorRun` bounds, wrap segments) and make every
*screen* quantity a terminal column (`scrollCol`, caret x, click x, the
horizontal scrollbar), with `core.ColumnOfRune` / `core.RuneIndexAtColumn`
as the only conversion — new in `core/runecol.go` beside `RuneWidth` and
`RunesWidth`. Converting the indices themselves would have touched far more
code for no gain; this is also what Scintilla does. `Editor`'s plain and
highlighted draw loops collapsed into one width-aware `drawLineRow`, and
`InputField.Draw` got the same treatment.

`RuneIndexAtColumn` snaps to the start of a wide rune from either of its
columns, because an index between the two cells has no text position behind
it. Clipping a wide rune at either viewport edge draws blanks rather than the
glyph: tcell writes the continuation cell itself, so emitting half a pair
makes the terminal paint the full character over its neighbour.

That last point turned out to be the *visible* bug, and bigger than the item
described. Recorded as "puts the cursor one column left of where it renders",
it actually **ate characters**: A/B against a `HEAD` build, typing `世界ab`
into the Connect dialog's Server field rendered `世ab`, `'世界世界'` in the
query editor rendered `'世世'`, and 32 ideographs in the wrap-mode Extra
Properties box collapsed onto one unwrapped row of 16 glyphs. Every second
rune was being overwritten by the previous one's continuation cell. Verified
live 2026-08-02 under tmux, both builds side by side, including caret x after
ten `Right` presses (49 with rune counting, 51 with columns).

Block (column) selection stays rune-indexed deliberately — a rectangle over
mixed-width text has no single right answer, and this is the SSMS-parity
choice. Noted on `Editor` itself, not just here.

**The chokepoint.** `Document` (`controls/document.go`) now owns the buffer
and a version counter, writable only through `setLine` and `edit`. The
open-threads note counted 26 mutation sites; converting them found two more
it had missed, and both are the interesting kind — `transformSelection`
rewriting runes in place and `MoveLinesUp`/`Down` reordering in place, neither
of which changes a slice header, so neither would have bumped a counter placed
by hand next to the assignments. That is the argument for the chokepoint in
one sentence.

`Highlighter` changed shape from `func([][]rune, int)` to
`func(*Document, int)` so a highlighter can see the version at all. Both
built-in ones dropped their one-line memo for `prefixStates`
(`controls/common.go`), which holds every line's carried-in state and is keyed
on the `*Document` as well as the number — two Documents count versions
independently from zero, so the pointer is what stops a second document being
answered with the first one's states.

**Two costs only visible once the first was gone.** Removing the per-Draw
replay exposed that `maxDisplayWidth` is O(every rune) where the old rune
count was O(lines), so a keystroke re-measured the whole buffer: 10.4ms per
keystroke at 10,000 lines, worse than the problem being fixed. Per-line width
caching plus moving `insertRune` off `edit` onto `setLine` took it out. Then
the prefix replay itself was made resumable: `Document.dirtyFrom` records the
line a single `setLine` touched, and the replay walks forward from there and
stops as soon as a recomputed state matches the stored one — the carried state
has rejoined the previous scan, so no later line can differ. Typing outside a
comment converges on the very next line.

Measured (`editor_bench_test.go`, 10,000 lines, 40-row viewport): a redraw
following no edit 0.37ms, a keystroke 10.4ms -> 0.38ms. A profile of the
redraw shows no O(document) work left — it is per-cell drawing and nothing
else.

**A/B, since none of this is provable by green tests.** Freezing the version
counter fails all 19 cases of `TestDocumentVersionChangesOnEveryMutation` plus
the three highlighter-invalidation tests and the wrap-cache test. Forcing
`core.RuneWidth` to 1 fails all seven width tests. An off-by-one in the resume
point and a missing convergence-index guard are each caught by
`TestPrefixStatesIncrementalReplayMatchesFullReplay`, which compares the
incremental cache against `startsInBlockComment`'s assumption-free replay over
edits that open, close and move block comments.

One mutant is *not* caught: removing the line-count test from `prefixStates.at`
changes nothing, because only `setLine` leaves a non-zero `dirtyFrom` and
`setLine` cannot change the line count, so the resume branch is already
unreachable for a document that grew or shrank. Kept as one comparison's worth
of insurance, and labelled as redundant in the code rather than left to look
load-bearing.

---

## 2026-08-03 — Closed-thread archive moved out of open-threads.md

`open-threads-closed-archive-2026-08-03`

`docs/open-threads.md` had grown to 40k, of which 28k was six "fixed / do not
re-open" sections. `CLAUDE.md` sends every session to that file to check
whether something is newly found, so the closed history was being read on
every visit at more than twice the cost of `CLAUDE.md` itself. The six
sections were moved here verbatim (extracted by line range and diffed
byte-for-byte, per `CLAUDE.md`'s file-splitting rule), leaving open-threads.md
at 12k and holding only work that is actually open.

Retained in `open-threads.md` on purpose: "By design — not issues, do not
re-raise" and "Investigated and found NOT to be a bug (do not re-raise)".
Those two are not history — they are the file's defence against a session
re-raising a settled question, which is exactly what a session consults it
for.

The sections below are in their original order, which runs newest-first —
the reverse of this journal's convention, preserved rather than reordered so
the move stayed byte-exact.

## Fixed 2026-08-02 (do not re-open)

All four were one change, because they had one blocker. The two performance
items were explicitly gated on "a single mutation chokepoint or neither", and
the two width items on the same rework of cursor/selection/scroll/wrap math.

- ~~`Editor` and `InputField` index by rune, not display width~~ — fixed.
  Both now keep text positions (cursor, selection anchor, `ColorRun` bounds,
  wrap segments) as rune indices and everything on screen (`scrollCol`, the
  caret's x, a click's x, the horizontal scrollbar) as terminal columns, and
  convert between the two through `core.ColumnOfRune` /
  `core.RuneIndexAtColumn` — new in `core/runecol.go` alongside `RuneWidth`
  and `RunesWidth`. `Editor`'s two draw paths collapsed into one width-aware
  `drawLineRow`; `wrapSegments` breaks on columns; `longestLineLen` became
  `Document.maxDisplayWidth`.

  A wide rune clipped by either edge of the viewport is drawn as blanks:
  tcell owns both cells of a double-width character, so emitting half of one
  makes the terminal paint the whole glyph over its neighbour. That was the
  visible symptom — **the old build did not merely misalign wide text, it
  ate it**: typing `世界ab` into the Connect dialog's Server field rendered
  `世ab`, and `'世界世界'` in the query editor rendered `'世世'`.

  Verified live 2026-08-02, A/B against a `HEAD` build: the connection field,
  the query editor, caret x after ten `Right` presses (49 = rune count, vs 51
  = columns), and the Connect dialog's wrap-mode Extra Properties box, where
  32 ideographs previously collapsed onto one unwrapped row of 16 glyphs and
  now wrap correctly at the box edge.

  Deliberately still rune-indexed: block (column) selection, whose rectangle
  is defined by rune columns. Rectangular selection over mixed-width text has
  no single right answer; this is the SSMS-parity choice, and it is noted on
  `Editor` itself.

- ~~The highlighters' block-comment replay is O(document) on the first row of
  every Draw pass~~ and ~~`Editor.buildVisualLines` walks the whole document
  on every Draw~~ — both fixed, behind the mutation chokepoint they were
  gated on. `Document` (`controls/document.go`) now owns the buffer and a
  version counter, reachable for writing only through `setLine` and `edit`.
  The 26 mutation sites route through those — including two the original
  count missed, `transformSelection`'s in-place rune rewrite and
  `MoveLinesUp`/`Down`'s in-place reorder, neither of which changes a slice
  header and so neither of which would have bumped a hand-placed counter.

  `Highlighter` changed shape from `func([][]rune, int)` to
  `func(*Document, int)` so a highlighter can see the version. Both built-in
  ones replaced their one-line memo with `prefixStates` (`controls/common.go`),
  which holds every line's carried-in state, keyed on the *Document and its
  version. `buildVisualLines` memoises its flattening the same way.

  Measured on a 10,000-line script, 40-row viewport (`editor_bench_test.go`):
  a redraw that follows no edit went from a full replay to **0.37ms**, and a
  keystroke from **10.4ms to 0.38ms**. A profile of the redraw now shows no
  O(document) work at all — it is per-cell drawing.

  Two further costs surfaced only once the first was removed, and are fixed
  here too rather than left as new open items:
  - `maxDisplayWidth` is O(every rune) where the old rune count was O(lines),
    so a keystroke re-measured the whole buffer. `Document` caches per-line
    widths and `setLine` drops one entry; `insertRune` was moved off `edit`
    onto `setLine` so typing takes that path.
  - The prefix replay itself resumes rather than restarts. `Document.dirtyFrom`
    records the line a single `setLine` touched, and `prefixStates.replay`
    walks forward from there, stopping as soon as a recomputed state matches
    the stored one — the carried state has rejoined the previous scan, so no
    later line can differ. Typing outside a comment converges on the next
    line. Pinned differentially by
    `TestPrefixStatesIncrementalReplayMatchesFullReplay` against
    `startsInBlockComment`'s assumption-free replay, over edits that open,
    close and move block comments; A/B, an off-by-one in the resume point and
    a missing convergence-index guard are both caught.

  The invariant everything rests on is pinned by
  `TestDocumentVersionChangesOnEveryMutation`, which drives 19 editing paths
  and fails on all 19 if the counter is frozen.

## Fixed 2026-08-01 (do not re-open)

- ~~Script Changes broken on any create dialog whose later page depends on an
  earlier page having actually run~~ — fixed across both repos. Two causes,
  both of them a *read* standing in the middle of a write-only path:

  1. gosmo's four Agent create methods (`CreateScheduleContext`,
     `CreateJobContext`, `CreateAlertContext`, `CreateOperatorContext`) ended
     with a `...ByNameContext` read-back to populate the returned object.
     `WithScript` only intercepts the exec chokepoints, so that read went to
     the server, found nothing — the `sp_add_*` had merely been collected —
     and the whole Script Changes run failed with
     `gosmo: schedule "X" not found`. Each now returns a name-only handle
     under `Scripting(ctx)`, from the new `Server.Schedule/Job/Alert/Operator`
     constructors — the Agent-side counterparts of `Server.Database`, and
     added, not substituted for the `ByName` forms.
  2. gossms's dependent pages then did the *same* lookup themselves
     (`JobByNameContext`/`AlertByNameContext` in `new_job_pages.go`,
     `new_alert_dialog.go`, `new_schedule_dialog.go`). Both now go through
     `scriptSafeJob`/`scriptSafeAlert` (`new_object_dialog.go`), which take
     the lightweight handle under script mode and the real read otherwise.

  Every write reached from those handles addresses its object by name, so
  nothing needs the fields the read-back would have filled. Also fixed in
  passing: `Job.SetEmailNotifyContext` assigned `NotifyEmailOperatorName`
  directly, bypassing `setIfApplied` — the one survivor of the 2026-07-30
  sweep. Pinned by `TestScriptedAgentCreatesReturnNameOnlyHandles` (gosmo,
  A/B: the old form panics/queries) and
  `TestScriptSafeLookupsDoNotQueryUnderScriptMode` (gossms, which runs with a
  nil `Server` so a helper that still queries fails loudly).

  **Verified live against `ubudock` 2026-08-01.** A throwaway job, then New
  Schedule's two applies run under `WithScript`: the collector produces
  `sp_add_schedule` + `sp_attach_schedule`, both statements run clean when
  executed for real, and the schedule comes back genuinely attached to the
  job. A/B: reverting `CreateScheduleContext`'s guard reproduces the reported
  `gosmo: schedule "zz_throwaway_sched" not found` at page 1. Job and schedule
  were dropped afterward. Not covered: the dialog's own Script button in
  front of a human, and the New Job / New Alert equivalents (same code shape,
  unit-tested only).

- ~~A bare `GO` line inside a block comment treated as a batch separator by
  IntelliSense scoping~~ — fixed. GO detection moved out of the separate
  textual pass over `lines` and into `lexSQL` itself (`goScan` bounds,
  `lexResult.firstGo`/`lastGo`, `goSeparatorLineAt`), so a line is only a
  separator if it *begins* in `sqlLexNormal` — never inside a block comment, a
  string literal, or a bracketed identifier. `lastGoBatchStart` and
  `statementStartOffset` are gone; `scanCompletionPrefix` now always resumes
  its second pass in `sqlLexNormal`, because both boundaries it can pick are
  normal-state positions by construction, which removed the `mark`/
  `stateAtMark` machinery the old resume needed. Marginally *faster* than
  before (the backwards per-line scan is gone): 5.71ms vs 6.01ms on
  `BenchmarkCompletionPrefixScan_1000Stmts`.

  The differential baselines in `completion_prefix_scan_test.go` were made
  lexer-aware independently (`referenceLineStartsNormal`, a plain
  character-at-a-time walk), so the 400 generated scripts still check
  production against something written separately. A/B: reverting the
  reference to the textual rule makes the corpus tests fail on exactly the
  commented-out and quoted GO cases.

  `controls/sql_statement.go` needed **no change** — Ctrl+Enter's
  `isGoSeparatorLine` test was already inside the state machine, guarded by
  `state == stNormal`. The claim that it shared the bug was wrong; it is now
  pinned by `TestSelectStatementAtCursorIgnoresGoInsideBlockComment`.

- ~~A `/*` inside a `--` line comment or a string literal poisoned the syntax
  highlighting of every line after it~~ — fixed.
  `blockCommentToggleEnd` (`controls/sql_highlighter.go`) now skips `--`
  comments and `'...'` literals exactly as the highlighter's main loop does,
  sharing the extracted `stringLiteralEnd` with it so the two cannot drift.
  The memo/replay invariant is untouched: both still call the one function, so
  a line's colour still cannot depend on whether it was the first visible row.
  A genuinely multi-line string literal is still mis-scanned, consistently
  with the main loop. A/B-confirmed by
  `TestSQLHighlighterIgnoresBlockCommentOpenerInsideCommentsAndStrings`, which
  fails on both cases against the old form, plus
  `TestSQLHighlighterRealBlockCommentStillSwallowsFollowingLines` for the
  inverse — quotes carry no meaning *inside* a comment, so `'*/'` still closes
  one.

## Fixed by the 2026-07-31 two-repo review (do not re-open)

All verified live against `ubudock` on 2026-08-01 unless noted; every
throwaway database, login and backup file created for the checks was dropped.

- ~~`Script Table as CREATE` emitted a script that cannot parse~~ — fixed
  2026-07-31 in gosmo. With `IncludeIfNotExists` (**on in
  `DefaultScriptOptions`**, which is exactly what `App.scriptObject` passes),
  `ScriptTableContext` wrapped the CREATE TABLE *and* every index and foreign
  key in one `IF ... BEGIN ... END` whose body contained `GO` separators. `GO`
  is a client-side batch break, so the block was split across batches: batch
  one carried an unclosed `BEGIN`, the last batch was a bare `END`, and the
  whole script failed. The guard is now per statement and never spans a `GO`.
  The assembly was extracted into `buildTableScript` first — it had zero test
  coverage because it was welded to four catalog reads — and
  `TestBuildTableScriptKeepsBlocksInsideOneBatch` pins the invariant. A/B
  confirmed: the assertion flags the old shape (2 unbalanced batches) and
  passes the new one.

- ~~Columnstore / XML / spatial indexes scripted as B-tree DDL~~ — fixed
  2026-07-31, same pass. `scriptIndex` pasted `sys.indexes.type_desc` into the
  ordinary `CREATE <type> INDEX ... (col ASC)` form, which is invalid for
  every one of them: a clustered columnstore takes no column list, a
  nonclustered columnstore rejects ASC/DESC, and XML/spatial have their own
  grammar. Columnstore now gets its correct form; XML and spatial are emitted
  as a comment naming what was skipped, rather than a statement that cannot
  run. A unique *constraint* also no longer scripts as `CREATE INDEX` — it is
  now the `ALTER TABLE ... ADD CONSTRAINT ... UNIQUE` it really is, so the
  constraint isn't silently lost.

- ~~`ALTER DATABASE SCOPED CONFIGURATION ... FOR SECONDARY` was a syntax
  error~~ — fixed 2026-07-31 in gosmo. The clause was appended after the
  assignment; it precedes `SET`. `forSecondary: true` was therefore unusable
  outright. gossms only ever passed `false`, so this was library-only — which
  is exactly why it survived. Statement building moved into
  `buildScopedConfigStatement` so the clause order is assertable without a
  server.

- ~~`BackupActionFiles` validated, then emitted a verb that does not exist~~ —
  fixed 2026-07-31 in gosmo. `BACKUP FILES [db] TO ...` is not T-SQL; a
  file/filegroup backup is a `BACKUP DATABASE` carrying `FILE =` /
  `FILEGROUP =` clauses. The action was on the allowlist, so callers were told
  it worked. `BackupOptions`/`RestoreOptions` gained `Files`/`FileGroups`, and
  the action with neither is now an error rather than a silent degrade to a
  full backup. Per CLAUDE.md the constant was implemented, not removed.

- ~~Restore always used backup set 1~~ — fixed 2026-07-31 across both repos.
  `gosmo.RestoreOptions` had no `FILE = n` at all, and
  `RestoreDialog.buildRestoreOptions` read `headers[0]` unconditionally — so a
  `.bak` written with NOINIT (full at position 1, differential at 2) could not
  restore the differential, while the inspect view cheerfully listed both.
  `RestoreOptions.FileNumber` now renders `WITH FILE = n`, and the inspect view
  selects a set with ←/→. The number sent is the header's own `Position`, not
  the slice index: they only coincide when a device's sets are contiguous from
  1. The selection is snapshotted on the UI goroutine before the background
  build, since `headerIdx` is UI state.

- ~~`Result.Database` could come back holding an execution plan~~ — fixed
  2026-07-31 in gossms. `executeWithSink` read `SELECT DB_NAME()` back off the
  connection while `SET SHOWPLAN_XML ON` was still in effect — the `SET ... OFF`
  is deferred — and under SHOWPLAN_XML nothing executes, so that query returned
  the plan document, which `Scan` wrote into `res.Database` verbatim. Latent
  rather than shipped: `setEstimatedPlan` ignores the field where `setResult`
  would have used it. The decision moved into `planCapture.readsCurrentDatabase`
  to be unit-testable, same treatment as `Result.shouldReportSuccess` — this
  path still can't be driven end to end by a fake driver.

## Live verification results (2026-08-01, `ubudock`)

Everything in the section above was found by reading source. These are the
checks that turned each one from a claim into a fact — worth recording
because one of the six *disproved* its finding.

- **Script Table as CREATE** — throwaway table with an identity PK, a unique
  constraint, a defaulted column, a filtered nonclustered index with INCLUDE,
  an FK with ON DELETE SET NULL, and a primary XML index. Generated script ran
  clean, then ran clean a second time (every existence guard exercised). A/B:
  the pre-fix scripter fails on the first batch with
  **`Incorrect syntax near ';'`** — the unclosed `BEGIN`.
- **Columnstore** — a second table with a clustered columnstore index scripts
  as `CREATE CLUSTERED COLUMNSTORE INDEX [x] ON [t];` and runs clean twice.
  The XML index is emitted as a skip comment, as intended.
- **`FOR SECONDARY`** — both `SET MAXDOP = 4` and
  `FOR SECONDARY SET MAXDOP = PRIMARY` accepted. (The instance is standalone,
  so this confirms the statement parses and is accepted, not replica
  behaviour.)
- **`BACKUP ... FILEGROUP =`** — against a throwaway database with a second
  filegroup: `BACKUP DATABASE [db] FILEGROUP = N'FG_Archive' TO DISK = ...`
  ran. The action with neither a file nor a filegroup is rejected.
- **Restore of backup set 2** — device written with a full backup (marker row
  `SET-ONE`), the marker updated, then a second set appended with NOINIT.
  `WITH FILE = 1` restores `SET-ONE`, `WITH FILE = 2` restores `SET-TWO`.
  Without the clause both would have been `SET-ONE`, which is exactly the bug.
- **Server-scope GRANT** — see "Investigated and found NOT to be a bug" above.
  This is the one that came back negative.

Not covered live, and still worth doing when the UI is next exercised: the
Restore dialog's ←/→ backup-set selector was verified only by unit test
(`restore_dialog_test.go`) plus the gosmo-level restore above — the dialog
path itself needs a device with several sets in front of a human.

## Fixed by the second 2026-07-30 two-repo review (do not re-open)

- ~~The mid-gesture wheel swallow was in one router of three~~ — fixed
  2026-07-30. `App.handleMouse` swallowed a wheel tick arriving while
  `gestureOwner` was armed, but `propsheet.PropertySheet.dragZone` and
  `QueryPanel.dragZone` — the other two routers CLAUDE.md's
  gesture-ownership rule names — still let one fall through to their
  positional dispatch. The property sheet's was *reachable*: `App` checks
  `topDialog()` before its own gesture check and never arms a gesture for a
  click inside a dialog, so wheeling while dragging a form's scrollbar both
  scrolled the form under the drag and moved the focus zone (`setZone` is
  called on every positional branch). A/B-confirmed against the pre-fix
  router. `QueryPanel`'s was latent — `App` arms `ownerPanels` for any press
  in that column and ate the tick first — but is now pinned where the
  invariant belongs. Both covered by tests that fail against the old form.

- ~~A zero-row export printed two contradictory messages~~ — fixed
  2026-07-30. The "Commands completed successfully." gate read
  `res.RowsWritten == 0` as "no result set happened", which is wrong for an
  *empty* one: `SELECT ... WHERE 1=0` to a file emitted both
  `(0 row(s) written)` and `Commands completed successfully.`, while the same
  query through `Execute` emitted neither. A `Result.sinkSets` counter now
  answers the question the gate was actually asking. The decision moved into
  `Result.shouldReportSuccess` to be unit-testable at all — `ExecuteToSink`
  itself still can't be driven end to end by a fake driver (see
  `stream_test.go`).

- ~~A panicking detail-browser fan-out goroutine cached a permanently blank
  row~~ — fixed 2026-07-30. The per-row `recoverPanic` added earlier that day
  stopped the crash but not the consequence: `wg.Done` still fired, `wg.Wait`
  returned, and `cacheOnly` cached the row still showing its `…` placeholder
  — permanently, since reselecting the node is a cache hit that never
  refetches. The recovery is now registered *after* `wg.Done` (so it runs
  before it) and queues a `markFailed` closure that writes `N/A`. A/B-confirmed:
  the old defer ordering caches `…`.

- ~~The Tables detail folder issued two round trips per table~~ — fixed
  2026-07-30. `Table.RowCount` + `Table.SpaceUsed` were fanned out
  `maxRowFetchConcurrency` at a time, so a 300-table database cost 600
  queries. gosmo gained `Database.TableRowCounts` and
  `Database.TableSpaceUsedAll` — the same aggregates and joins, grouped by
  `object_id` instead of filtered to one — and the folder now costs two
  queries total. Verified live against `ubudock`: 0 mismatches against the
  per-table forms across every table of every user database, and again every
  round on a throwaway 300-table database, where the warmed best-of-three was
  **32.9ms vs 380.1ms (11.6x)**. The throwaway database was dropped.

  A table with no allocated pages is *absent* from either map rather than
  present as zero; both call sites treat a missing key as zero.

- ~~`bindScriptArgs` let a purely named-parameter statement script
  unbound~~ — fixed 2026-07-30 in gosmo. The `sql.NamedArg` rejection lived
  in `scriptLiteral`'s type switch, which is only reached for an argument
  that has a matching `@pN` placeholder — and a named argument's placeholder
  is `@name`, which `placeholderPat` doesn't match. A statement parameterised
  purely by name would therefore have scripted with every parameter silently
  unbound. No gosmo method binds one today (`ExecProc` renders its own EXEC
  form), which is exactly why the guard had to move up front. Also:
  `scriptLiteral([]byte{})` rendered `0x`, which is not a valid T-SQL binary
  literal — now `0x00`.

## Closed since being recorded (verified 2026-07-30, do not re-open)

- ~~Script Changes dropped every query parameter, producing scripts that
  can't run~~ — fixed 2026-07-30 in gosmo. `Database.exec` captured only the
  statement text under `WithScript` and discarded `args`, so the four
  parameterised write methods (`Database.RenameTable`, `Index.Rename`,
  `Table.DropColumn`, `Database.DropTable` with cascade) each scripted an
  `@p1`/`@p2` the user's query window has no binding for — "Must declare the
  scalar variable '@p1'". `bindScriptArgs` now substitutes literals into the
  text (a `DECLARE` preamble would collide instead of compose: a collector's
  statements are concatenated into one batch). `ExecProc` was worse — its real
  path is an RPC whose statement text is the bare procedure name, so it
  scripted an object name with no `EXEC` and no parameters; it now renders the
  statement itself via `scriptExecProc`. Reachable from Index/Key Properties'
  rename through `PropDialog.runScript`. An argument with no literal form is
  now an error rather than a `%v` guess.

- ~~gosmo objects mirrored a write back onto themselves even under
  `WithScript`~~ — fixed 2026-07-30. 39 write methods assigned the new value
  to the receiver (`idx.Name = newName`, `l.IsDisabled = true`, …) after an
  exec that, in script mode, only recorded the statement — leaving the object
  claiming state the server does not have, so the next call built from it
  targeted a nonexistent object. All now go through `setIfApplied` (or, for
  `JobStep.Update`'s multi-field block, an explicit `if !Scripting(ctx)`).
  `Scripting` already documented this hazard for *callers*; gosmo now honours
  it for its own objects.

- ~~`float`/`real` columns displayed in scientific notation~~ — fixed
  2026-07-30. `formatValue` had no float case, so Go's `%v` (`%g` rule)
  rendered a float column holding 1000000 as `1e+06`. `formatFloat` now uses
  plain decimal across the range SSMS shows plainly and an exponent only
  outside it, keeping shortest-round-trip precision so a copied value pastes
  back as the same float64.

- ~~`RowSink.EndSet` was skipped when a set failed part-way~~ — fixed
  2026-07-30. `streamResultSet` returned early on a scan/`Row` error, so
  `EndSet` never ran for a set `BeginSet` had opened. Harmless for `csvSink`
  (whose `Close` flushes anyway) but it made the interface's contract
  "`EndSet` may not be called", which is not what it says. Now deferred, with
  the scan/`Row` error preferred over `EndSet`'s.

- ~~Results To File could start a run on a dead connection~~ — fixed
  2026-07-30. `runQuery` checked `isConnected` before opening the save dialog
  but the callback re-checked only `p.executing`, so disconnecting while the
  dialog was up took `startRun` into `sc.Server.DB()` on a closed connection
  (recovered by `recoverPanic`, but as a crash rather than a message).

- ~~`Editor` wrap mode silently ignored its `Highlighter`~~ — fixed
  2026-07-30. `drawWrapped` never called it, so setting both `SetWrapMode` and
  `SetHighlighter` lost the highlighting without failing. Runs are now fetched
  per *logical* line (not per visual row, which would also defeat the
  highlighters' memo) and resolved per column through `styleAt`.

- ~~Disconnecting a server root shortly after connecting leaves a real SQL
  session alive for up to 30s~~ — the completion-inventory load used
  `context.Background()`; `completion_inventory.go` now derives from
  `sc.Context()`.
- ~~Stale-name-after-rename in Login / User / Role / Server Role / Key
  Properties~~ — all now thread the name as `*string` through their page
  closures, as Job Properties did first.
- ~~`datagrid.go` needs splitting~~ — done.

- ~~Results To File could write a silently truncated CSV~~ — fixed
  2026-07-30. The row cap was decided from a snapshot of `resultsMode` but
  the export decision re-read the live field, so switching Grid→File mid-query
  wrote the capped result out with nothing saying so. Both now read
  `QueryPanel.runMode`, snapshotted once per run.

- ~~Results To File materialised every row before writing a byte~~ — fixed
  2026-07-30. `query.ExecuteToSink`/`RowSink` stream rows straight to
  `csvSink` as they are scanned, so an export is bounded by the file rather
  than by memory (it was held twice over: once in `Result.Sets`, once in the
  grid). The path prompt moved *before* execution as a consequence, which
  also matches SSMS. Verified live against `ubudock`.

- ~~A panic on a background goroutine killed the process and left the
  terminal in raw mode~~ — fixed 2026-07-30. Every `go func()` in
  `internal/tui` now carries `defer <app>.recoverPanic(...)` (see
  `safego.go`), and `cmd/gossms` recovers at the top level. Reachable, not
  theoretical: go-mssqldb's `makeGoLangTypeName` panics on an unknown column
  type ID, which `scanResultSet` reaches for every column of every result set.

  The original sweep missed the *inner* per-row fan-out goroutines in
  `detail_browser_databases.go` and `detail_browser_tables.go` — covering the
  outer loader goroutine is not enough, since a panic unwinds only the
  goroutine it happens on. Both now carry their own `recoverPanic`; a nested
  `go func()` needs one of its own, not its parent's.

- ~~A password that failed to decrypt was destroyed by the next Save~~ —
  fixed 2026-07-30. `decryptPassword` now reports success, `Load` stashes the
  original ciphertext in `Connection.sealed`, and `Save` writes it back
  untouched instead of re-encrypting the `""` it stood in for. Triggered by
  hand-editing `server`/`user` (which the AAD binds to) or replacing the key
  file.

- ~~Add/Remove/New/Delete handlers on the grid-backed property pages
  silently did nothing~~ — fixed 2026-07-30 across
  `securables_matrix.go`, `database_props_files.go`,
  `database_props_filegroups.go`, `agent_job_props_steps.go`,
  `extended_properties_form.go`, `new_job_pages.go`,
  `new_database_pages.go`, and both membership pages. They now report why via
  the new `propsheet.HintRow`, and a duplicate Add selects the existing row.
  The Database Role and Server Role Members pages, which were ~95 identical
  lines each, were extracted into `membership_page.go` in the process.

- ~~`1 + indexOf(...)` shows the wrong item when the server's value isn't in
  the list~~ — fixed 2026-07-30. It was never really a UX choice: `user_props.go`
  and `login_props.go` already answered it by searching the sentinel-inclusive
  list, so a missing value lands on the leading `(None)`. The four offset sites
  (`agent_alert_props.go` database/category/job, `agent_operator_props.go`
  category) now do the same and the `!= ""` guards are gone.
  `prop_grid_helpers_test.go` pins the fallback, including that the old
  `1 + indexOf` form picked the first real item.

- `newOwnerTransferPage` guards an owner missing from the list, but no current
  caller can hit it. Noted 2026-07-30 during the extraction into
  `owner_transfer_page.go`. The helper appends an unlisted `origOwner` to the
  Select items, because the page commits whatever the row displays and
  `indexOf`'s not-found 0 would otherwise read as "the first principal" — a
  page opened and OK'd would transfer ownership without being asked. All three
  current call sites filter items by `Owner == *principalName`, and that
  principal is by construction in `principalNames`/`serverPrincipalNames`, so
  `origOwner` is always present and the guard is dead code today (confirmed
  live: the dropdowns for `rev_user`, `rev_role` and `rev_srv1` each contained
  the principal itself). Kept as an invariant guard for a future caller that
  lists objects it doesn't filter by owner; pinned by
  `TestOwnerTransferPageKeepsAnOwnerMissingFromTheList`.

- **The toolbar's `Meta[-OFF]` toggle is a stub.** Added 2026-08-02 at the
  user's request, ahead of the feature itself. It flips `App.metaEnabled`,
  relabels itself, and says "not implemented yet" on the status line
  (`App.toggleOutputColumnMeta`); nothing reads the flag. What it should
  drive: showing a result set's output column metadata — type, nullability,
  source table/column — alongside the rows. The state is deliberately not
  persisted to config yet, since no behaviour depends on it.
  **Superseded 2026-08-04** — the toggle now drives a real Messages-tab
  listing; see [[output-column-metadata-2026-08-04]] below.

---

## 2026-08-04 — Output column metadata toggle

`output-column-metadata-2026-08-04`

*The `Meta[ON--]`/`Meta[-OFF]` stub grew its feature: each result set's
columns and declared types are listed in the Messages tab*

User request (`todo/todo.txt`): when the toggle is ON, every displayed
result set adds a block to the Messages window naming its columns and their
data types, and a column the query didn't name shows its position instead.

Three pieces:

- `internal/query/coltype.go` — `columnTypeName` renders one column's
  declared type the way SSMS writes it, from what the driver reports
  (`DatabaseTypeName`, `Length`, `DecimalSize`). Only the char/binary types
  take a length suffix; text/ntext/image/xml also report a length, but it is
  their *capacity* (2147483647 and friends), so writing it would produce
  "text(2147483647)" — a type nobody declared. `(max)` is a sentinel length,
  and it differs per type: `varchar`/`varbinary` report 2147483645,
  `nvarchar` half that, since go-mssqldb divides by the two bytes per
  character (`types.go`, `makeGoLangTypeLength`). Read out of the module
  cache rather than assumed.
- `ResultSet.ColumnTypes`, filled by `newRowScanner`, which already had the
  `[]*sql.ColumnType` in hand for its date/time-layout analysis — so this
  costs one extra pass per result set, not per row, and is populated
  unconditionally rather than being gated on the toggle.
- `internal/tui/column_meta.go` — `columnMetaMessages` formats the block,
  folded into `res.Messages` **once, in `setResult`**. Not at render time:
  `renderActiveTab` re-runs `setMessages` on every tab switch, so building it
  there would have repeated the block on each visit to the Messages tab.

Verified live against `ubudock`, not just by test: a throwaway
`TestLiveColumnTypes` (deleted afterward) ran a 15-column SELECT covering
every suffix shape and confirmed `nvarchar(50)`, `decimal(18,2)`,
`datetime2(3)`, `time(2)`, `varchar(max)`, `nvarchar(max)`,
`varbinary(8)`, `uniqueidentifier`, `xml`, `money`, `date`, `bit`, `float`
— and that an unnamed expression column comes back with `""` as its name,
which is what the position fallback keys off. Then end to end under tmux:
toggled the toolbar button on, ran a two-statement script, and read the
Messages tab back —

```
(1 row affected)
(1 row affected)

Result 1
col1 nvarchar(50)
col2 int

Result 2
col1 float
2 datetime
3 int
```

— and switched tabs away and back to confirm the block appears once, not
twice.

---

## 2026-08-04 — Three quick wins: golden lexer freeze, file splits, JSON cell viewer

`quickwins-2026-08-04`

*Retired the duplicate T-SQL lexer baselines behind a golden file, split the
last four oversized files, and added a JSON highlighter for structured cell
values*

Three items picked off `docs/open-threads.md` in one pass, at the user's
request, after listing the known issues.

### 1. The two lexer implementations are down to one

`flattenLines` and `tokenizeSQLPrefix` were dead in production and survived
only as differential-test baselines — but the real duplicate was never those
two one-line wrappers. It was `referenceLineStartsNormal` +
`referenceStatementStartOffset` + `referenceScanCompletionPrefix` in
`completion_prefix_scan_test.go`: a second, independently written T-SQL state
machine, kept in sync by hand forever. That is what is gone.

What replaced it is `testdata/completion_prefix_scan.golden` (113 KB), built
by `TestScanCompletionPrefixGolden` and regenerated with `-update-golden`:

- **`[cursor-sweep]`** — the curated 31-script corpus, every cursor position,
  written out in full: state, batch start, quoteStart, and the whole token
  stream per position. This is the section a human diffs when the scan
  changes.
- **`[typing]`** and **`[generated]`** — one digest per script over the same
  per-position stream, for the keystroke-by-keystroke sweep and the 400
  seeded random scripts. Full streams there would have run to megabytes and
  nobody would read the diff; as digests they stay tripwires.

`tokenizeSQLPrefix`'s callers became direct `tokenizeSQLRange(buf, 0, …,
false)` calls (it was an alias for exactly that), and `flattenLines` became a
test-local `flattenFresh`. The reference benchmark was rebuilt out of
production pieces — it still shows the contrast it exists for, 1 alloc/op
against 2409 at 100 statements.

**The trade, stated because it is real:** a golden file pins current
behaviour. It catches a regression; it cannot find a bug that was always
there. The differential sweep could. That is the cost of not maintaining two
lexers, and `TestCommentedOutGoDoesNotStartANewBatch` /
`TestRealGoStillStartsANewBatch` stay as hand-written assertions precisely
because a golden would happily freeze the broken answer if someone
regenerated without reading the diff.

**Verified the golden actually bites**, rather than assuming: two mutations
injected into `lexSQL` and reverted — `batchStart := max(r.boundary,
r.lastGo)` reduced to `r.boundary`, and `]]` made to close a bracket instead
of escaping. Both failed with a readable first-differing-line diagnostic. A
third mutation (`firstGo < 0` → `firstGo < -1`) did *not* fail, correctly:
`firstGo` feeds `statementEndOffset`, which this corpus never exercises.

### 2. The P5 file-split list is closed

`query_panel.go` 713→289, `restore_dialog.go` 710→296, `backup_dialog.go`
662→391, `planview/planview.go` 685→329. All on the same seam the existing
`datagrid_draw/_input/_overlay.go` split established: `*_draw.go` for
rendering, `*_input.go` for HandleKey/HandleMouse, plus
`planview_clipboard.go` for the clipboardTarget methods.

Followed CLAUDE.md's procedure literally — extract by exact line range with
`sed`, never retype — and then **verified byte-for-byte** by reassembling the
original from the pieces and diffing it against the source. `query_panel.go`
came back differing from `HEAD` by exactly the one known uncommitted hunk and
nothing else; the other three came back `IDENTICAL`. Only the import blocks
changed afterwards, via goimports.

No file-level header comments on the new files: the repo's existing split
files (`datagrid_draw.go`, `query_panel_exec.go`) don't carry one, and a
comment block above `package` is one blank line away from silently becoming a
package doc comment.

### 3. JSON cell values

`controls.JSONHighlighter` joins the SQL and XML ones. It is the only one of
the three with **no `prefixStates` cache, and that is a property of JSON
rather than an omission**: no JSON token can span a line (RFC 8259 §7 — a
string may not contain a literal newline, and there are no comments), so each
line's highlighting depends only on that line. Worth knowing before someone
"fixes" the missing cache.

`internal/tui/xml_value.go` became `cell_value.go`, with `looksLikeXML`
generalised into `classifyCellValue` returning `cellPlain`/`cellXML`/
`cellJSON`. The sniff stays O(1) on the trimmed text — first and last
character — for the same reason it always did: it runs on the UI goroutine
and the cell can be megabytes.

The one non-obvious guard is `jsonArrayLike`. A `[…]` shape alone is not
enough to mean JSON: result sets are full of bracket-quoted SQL Server names
(`[dbo]`, `[Ord.ers]`, anything through `QUOTENAME`), and opening a whole
panel for one instead of the grid's popup would be a regression in the common
case. So the first non-space character inside the bracket has to be something
a JSON array element can actually start with.

Routing to a panel rather than highlighting the popup is deliberate and is
what `docs/open-threads.md` warned about: the popup is an Editor in wrap
mode, whose `styleAt` is a linear scan of the line's runs — fine against
SQL's few coarse runs, wrong against one run per token across a whole
document. A panel draws unwrapped.

Live-checked against `ubudock` with a single query returning a `FOR JSON
PATH` column, an `xml` column, and `'[dbo]'`: the JSON cell opened as
`doc.json` with keys bold blue, numbers green, strings orange and `true` in
literal colour (read out of the SGR capture, not eyeballed); the XML cell
opened as `x.xml`; and `[dbo]` correctly fell through to the built-in
60-column popup with no new tab.

---

## 2026-08-04 — gosmo examples rebuilt, and the three bugs they found

`~/go/gosmo/examples/` was one 357-line `main.go`. It is now nine programs —
the tour plus `backup`, `bulkcopy`, `diagnostic`, `iterators`, `jobs`,
`maintain`, `scripting`, `security` — over a shared `examples/internal/demo`
package holding the env-driven connection factory, a `TempDatabase` helper,
and `Must`/`Value`/`Section`.

The interesting part is what writing them turned up. Every one was found by
running the example against `ubudock`, none by `go test ./...`, and two were
live in gossms UI paths:

- **`SetQueryStoreOptions` never worked for any non-OFF state.** It emitted
  `STALE_QUERY_THRESHOLD_DAYS` as a top-level option of
  `ALTER DATABASE ... SET QUERY_STORE = ON (...)`; the parser only accepts it
  inside `CLEANUP_POLICY = (...)`, so the whole statement failed with
  "Incorrect syntax near 'STALE_QUERY_THRESHOLD_DAYS'". gossms's Database
  Properties > Query Store Apply
  (`internal/tui/database_props_query_store.go:125`) went through it.
- **`Alert.SetJobResponse("")` could not clear a job response.** gosmo mapped
  `""` to a placeholder `[UNSPECIFIED]`, which `sp_verify_alert` looks up as a
  real job and rejects. `msdb.dbo.sp_update_alert`'s own sentinel — read out
  of `OBJECT_DEFINITION` on the live server, not guessed — is an empty
  `@job_name`, which it maps to `job_id = 0x00`. Two gossms call sites.
- **An aborted `BulkInsert` poisoned a pooled connection.** Returning early
  when the row iterator yields an error left the connection mid-bulk-copy;
  the pool handed it to the next caller, whose first statement died with
  "Bulk load data was expected but not sent". It now marks the connection bad
  via `sql.Conn.Raw` returning `driver.ErrBadConn` so the pool discards it.

The first two are pinned by new `WithScript`-based statement tests
(`query_store_test.go`, `agent_alert_test.go`) — the existing tests only
checked the allowlists, which is exactly why a statement SQL Server cannot
parse shipped green. The third has no offline test; it needs a real server.

Two corrections to the examples themselves, same origin: `Database.Search`
wraps its own `%` and escapes the caller's, so it takes bare text and not a
pattern; and `iter.go`'s `*Seq` iterators are **deferred, not streaming** —
they run the whole `...Context` fetch and then yield from the slice. The
first draft of `examples/iterators` demonstrated early-break stopping the
query and an error arriving mid-scan, neither of which happens. `iter.go`'s
package comment says so plainly; the live run is what forced reading it.

Standing gap, not fixed: `Database.CreateStoredProcedure` writes the
`CREATE OR ALTER PROCEDURE <name> AS` header itself, so there is no way to
create a procedure **with parameters** through gosmo. The examples work
around it with parameterless procedures and demonstrate `ExecProc`'s
In/Out/InOut against `sp_executesql`. Adding a parameter list is a gosmo API
decision left to the author.

---

## 2026-08-05 — Permissions gap-fill: WITH GRANT OPTION, effective, column-level

`permissions-gapfill-2026-08-05`

Five items sat under `open-threads.md`'s "Deferred scope (repeatedly,
deliberately)" heading — re-deferred on every properties pass since the
dialogs were built. All five shipped in one sitting, plus two smaller
carried-forward threads.

### gosmo

Three new files, no existing signature touched (the library rule):

- `permission_options.go` — `PermissionOptions{WithGrantOption, Cascade,
  GrantOptionOnly}` and twelve `...WithOptions` method pairs across object,
  schema, database and server scope, all rendering through one
  `permissionStmt.render`. The zero value renders byte-identically to the
  plain trio, which is what lets the UI take one path for every state; a test
  pins that equivalence at four scopes. A modifier the verb has no form for
  (WITH GRANT OPTION on a DENY) is an error rather than a silently dropped
  field.
- `column_permission.go` — `ColumnPermissions`, `ColumnPermissionsForPrincipal`,
  the grant/deny/revoke trio taking a `[]string` of columns, and
  `ColumnPermissionNames()` (SELECT/UPDATE/REFERENCES only — `GRANT DELETE
  (col)` is a syntax error). An empty column list is refused rather than
  quietly widening into an object-level grant.
- `effective_permission.go` — `fn_my_permissions` under `EXECUTE AS` for
  database, object and schema scope, plus `EXECUTE AS LOGIN` for server
  scope. The impersonation has to be in the same batch as the SELECT
  (`fn_my_permissions` only ever answers for the current execution context),
  which `Database.query`'s pinned connection makes safe.

Six matching `*Seq` iterators, and the README feature map + class diagram +
byte-identical `gosmo.mermaid` copy updated.

### gossms

- `internal/tui/perm_state.go` is the new shared layer: a four-state cycle
  (none → Grant → Grant With Grant → Deny), and `permTransition(orig,
  current)` deciding the modifiers from the *pair* of states. That pairing is
  the load-bearing part — SQL Server refuses to revoke or deny a permission
  granted WITH GRANT OPTION unless CASCADE is present, so every transition out
  of Grant With Grant carries it, and the Grant With Grant → Grant step is a
  `REVOKE GRANT OPTION FOR ... CASCADE` rather than a re-GRANT (a re-GRANT
  leaves the grant option standing and changes nothing).
- Both permission matrices took a single `permApplyFn` in place of the
  grant/deny/revoke triple — a three-way split has nowhere to put a decision
  that depends on two states.
- Filter boxes on every securables/permissions grid, live as you type. Needed
  `TextRow.SetOnChange` in tuikit, and `SetDirtyTracked(false)`: a filter box
  is a view control, and left dirty-tracked it made read-only pages report
  unsaved changes they had no way to save.
- Column permissions are edited inline on the Securables page rather than
  behind SSMS's modal, with an explicit "Load Columns" button through
  `PropDialog.runPageAction` — auto-loading on selection would be a query per
  arrow-key press.
- A new Effective Permissions page on all four principal dialogs.
- The create-dialog picker gap turned out to be New Login's "Default schema",
  a free-text box whose typo only surfaced as a failed CREATE USER at apply
  time; now a picker over the selected database's own schemas
  (`DropDown.SetItems`/`SelectRow.SetItems`). The owner-change warning is a
  `HintRow` on the owner-transfer pages rather than a modal — the change is
  staged until Apply, so there is no moment where a blocking "do it now?"
  prompt would be answering anything.

### Verified live against ubudock

Unit tests do not prove any of the modifier logic. Against a throwaway
`ClaudeTmpDB` with a `tmpuser` holding `GRANT SELECT ... WITH GRANT OPTION`
and `GRANT UPDATE (Salary)`:

- the object grant read back as "Grant With Grant" in the grid;
- cycling it to Deny and hitting Apply succeeded — that is the statement that
  fails outright without CASCADE, so it is the real test of `permTransition`;
- Load Columns listed the table's three columns, and cycling one to Grant
  produced a genuine `sys.database_permissions` row (`UPDATE`/`GRANT`/`Id`);
- Effective Permissions at object scope returned exactly the two column-level
  UPDATEs with their column in the Subentity column, and *no* SELECT — the
  DENY correctly absent rather than listed and overridden.

Database dropped afterward. One harness note worth keeping: the app's own
pooled connections hold a database open, so `DROP DATABASE` from a query
panel in the same session fails with "currently in use" — drop it from a
separate process (`Server.DropDatabase(name, true)`).

### Also closed this sitting

- **Object Explorer Details refresh.** The title-bar button ran
  `App.refreshSelected` (the *explorer's* selection); it now runs
  `DetailBrowser.RefreshCurrent`, which re-fetches the node the panel is
  actually showing. Same node today, correct if the panel ever drills on its
  own.
- **`ExecuteToSink` end-to-end coverage.** The old note claimed a fake driver
  could not reproduce sqlexp's `ReturnMessage` protocol. It can: implement
  `driver.NamedValueChecker`, intercept the `*ReturnMessage`, and drive it
  with `ReturnMessageInit`/`ReturnMessageEnqueue` exactly as the sqlexp docs
  specify. `internal/query/executor_sink_test.go` now pins `Result.Sets`
  staying empty, `RowsWritten`, the per-set notice, and the `sinkSets`
  success-notice decision — that last one A/B-verified by reverting it to
  read `RowsWritten` and watching the test fail. What a fake still cannot
  reach is `runBatch`'s drain gate (deleting the drain loop still passes), so
  that one stays a live check and remains recorded in `open-threads.md`.

## 2026-08-05 — `internal/tui/sqlparse`, and the measurement that killed the rest of the split

`sqlparse-extraction-2026-08-05`. Asked to review § 1 of
`docs/proposals-2026-08-05.md` — the three-step `internal/tui` restructuring —
in detail before acting on it. The review rejected two of its three steps and
shipped a fourth thing it had not proposed.

### Re-measuring with types instead of grep

The proposal's numbers all came from `grep -E '\bApp\b'` over the package.
Re-deriving them from a real cross-file reference graph — `go/types`, package
loaded with `Defs`/`Uses`, every use edge attributed to its declaring file,
549 edges — changed the conclusion:

- **The headline "56% of lines never mention `App`" is wrong.** `\bApp\b`
  does not match `p.app`/`d.app`, the lowercase field. The real number is
  48% (58 files, 12,618 lines). Nine files are misclassified by it, two of
  them inside the proposal's own Step 1.
- **Step 1 was not the "pure lift with no interface needed" it claimed.**
  The four completion files carry 30 outbound references to 11 symbols —
  `QueryPanel` ×11 (`completion_candidates.go` is mostly `*QueryPanel`
  methods), `nodeIcon`/`nodeData`/`Node*`, `p.app.cfg.IconStyle` ×3,
  `isConnected`, `completionInventory` ×6. And its "densest test suite,
  travelling unchanged" is `newTestApp()`-based for 811 of its 1,407 lines.
- **Step 2's "one interface, five methods" is off by ~8×.** 110 outbound
  references, 44 symbols, 16 files. Six are `App` services; the rest are
  shared helpers (`formatSQLDate` ×19, `fqn` ×10, `formatHMS`, `dashIfZero`,
  `intRowValue0`, the agent formatters) sitting in files the rest of `tui`
  references 146-221 times, which therefore cannot move. It needs a fourth
  package.
- **Step 2's benefit already existed.** Five props tests run with zero `App`
  references today.

The generalisable lesson, and the reason this is written down: **a grep for a
type name does not measure coupling to that type.** It misses field access,
it misses methods on types that themselves hold the dependency, and it misses
transitive reach entirely. Two minutes of `go/types` gave a different answer
to every number in a document that had already been reviewed and parked.

The other lesson is about the precedent the proposal cited for itself.
`planview` has no `Host` interface and no callbacks — it is a pure leaf, and
that is *why* it worked. A proposal that quotes an existing success as
precedent should be checked for having the same shape as it; Step 2 had the
opposite shape.

### What actually shipped

The reference graph found one set with **zero** outbound edges — the only one
in the package: the tokenizer and the scope scanner. Those became
`internal/tui/sqlparse` (`token.go`, `scope.go`, `doc.go`), 786 lines plus
596 lines of tests, 16 symbols exported. Worth doing on its own merits, not
as a probe: a T-SQL lexer has no business in the application shell.

Done to `CLAUDE.md`'s file-split rule — `git mv` first, `sha1sum` all five
files against their pre-move hashes (all identical), *then* rename
identifiers with an explicit sed map, and let the compiler find the rest.
`sqlToken`→`Token`, `scanCompletionPrefix`→`ScanPrefix`,
`completionTokenContext`→`TokenContext`, and so on; `lexSQL`, `goScan`,
`lexResult`, `sqlKeywordCanon` stay unexported.

The proof it was behaviour-preserving is the golden file. `TestScanCompletionPrefixGolden`
sweeps every cursor position in a script corpus; after the move its diff was
**exactly two lines**, both header text (`scanCompletionPrefix` → `ScanPrefix`
in the title, and the regeneration command's package path). All 1,560 lines of
actual scan output were byte-identical. Benchmarks unchanged at 1 alloc/op.

### Live verification

Green tests are not verification, so all of it was driven against ubudock
under tmux, on the real IntelliSense path:

- `SELECT * FROM dbo.` → schema members (tables and views, correctly iconed)
- `... FROM dbo.Patients AS p WHERE p.` → alias resolution backwards through
  `ParseFromScope`
- `SELECT d. FROM dbo.Doctors AS d`, cursor before the FROM → the forward
  scan (`StatementEndOffset` + forward tokenize) still resolves the alias
- `SELECT * FROM dbo.Invoices AS inv` / `-- GO` / `WHERE inv.` → columns
  offered: a commented-out GO still does not split the batch (this is the
  shipped bug the lexer's `goScan` comment names)
- the same with a real `GO` → no popup, alias correctly out of scope
- `dbo.[Pat` → bracket-identifier completion (the `LexBracket`/`QuoteStart`
  branch), and `sys.objec` → the sys inventory

### Rejected, and why it is written down rather than left open

Steps 2 and 3 are closed in `open-threads.md` with their measurements, not
just their verdict — a future review that only sees "120 files, one package"
will re-propose exactly this, and the four negative results are what stop it
costing another day. The proposal's own best contribution is one of them:
the `agent_*`/`new_*`/`*_props_*` name families are not a seam, because they
cut through the `App` dependency rather than along it.

---

## 2026-08-05 — Find and Replace in the query editor

Ctrl+F / F3 / Shift+F3 / Ctrl+F3, plus Edit > Find.../Replace..., built in
four layers: a search engine on `controls.Editor`
(`internal/tuikit/controls/editor_search.go`), a highlighting pass in
`editor_draw.go`, a modal `FindReplaceDialog`
(`internal/tui/find_replace_dialog.go`) in two modes, and the app-side key
and menu wiring.

### Decisions worth keeping

**One engine, not two.** A literal term goes through `regexp.QuoteMeta`,
whole word wraps it in `\b(?:…)\b`, case-insensitivity prefixes `(?i)`. So
match iteration, replacement, and the group-reference path have exactly one
implementation regardless of which options are ticked.

**Matches are per line, in rune indices.** The pattern is applied one
logical line at a time, so a match can never span a line break — which keeps
the per-line match list directly usable by the drawing path, and keeps every
position in the same units (`cursorCol`, selection bounds, `ColorRun.Start`)
the rest of the editor already uses. `byteRuneIndex` converts the regexp
engine's byte offsets once per line; a byte offset that reached a selection
bound would land mid-character on the first non-ASCII line.

**Zero-width matches are dropped at scan time.** `x*`, `^`, `\b` have
nothing to select or replace, and Find Next would stall on one forever.

**Replace All is one undo step, applied right-to-left per line.** Both are
pinned by tests: replacing left-to-right with a longer replacement shifts
the offsets of the matches still to come on the same line, and a per-match
`pushUndo` would take one Ctrl+Z per occurrence to undo.

**"In selection only" captures the selection at `SetSearch` time**, not at
`ReplaceAll` time — the first replacement moves the selection, so a range
read later is already wrong. With the option ticked and nothing selected it
replaces *nothing* rather than falling back to the whole document.

**The current match is the selection.** The drawing path paints all matches
in `Palette.EditorMatch` and lets the selection style win in `styleForRune`,
so the current hit stays distinct without a second "current match" colour or
a special case in the row renderer.

### Ctrl+H is deliberately unbound

tcell decodes the byte a legacy terminal sends for Ctrl+H (`0x08`) as plain
`KeyBackspace` with no Ctrl bit — indistinguishable from the Backspace key
on terminals that send `0x08` for it. Binding it would break Backspace
there, so Replace is reached through Edit > Replace... or the Find dialog's
own Replace mode. This was the user's call after the trade-off was laid out.

### Live verification

Driven against ubudock under tmux, not just unit tests: literal and regex
finds with the match counter tracking (`Match 3 of 4 — line 2, col 24`),
wrap-around at both ends, F3/Shift+F3 with the dialog closed, Ctrl+F3
switching the search to the word under the caret (verified by clicking onto
a *different* word first — plain F3 would have looked identical otherwise),
Replace then Replace All, both undone by exactly one Ctrl+Z each, "in
selection only" leaving the unselected line untouched, an invalid regex
reported in the dialog, and mouse clicks on the checkboxes and buttons.

Two layout bugs only the live run showed: Replace mode's four-button row
drew over the dialog's own left border at width 56 (now 62), and both modes
carried a spare blank row.

### Harness note

The Edit menu's Down-count is not stable — `Find Next`/`Find Previous` are
`Enabled`-gated on there being a search to repeat, and disabled items are
skipped during menu navigation. Counting Downs from a previous run activated
`Delete Line` instead of `Replace...`. Verify the highlight per keystroke
(the `48;2;0;122;204` match) rather than trusting a count, exactly as the
`ButtonsRow` note in the tmux-testing memory already says.


---

## 2026-08-05 — Effective Permissions: roles can't be impersonated, and the server scope needed USE master

`effective-permissions-role-fix-2026-08-05`

Raised by a two-repo review as item A1 of a plan, then confirmed live. The
Effective Permissions page shipped the sitting before onto "all four principal
dialogs" (see `permissions-gapfill-2026-08-05`). Two of those four could never
have worked.

### A role cannot be impersonated, so it has no effective permissions to show

Everything under `gosmo/effective_permission.go` resolves permissions by
impersonating the principal and asking `fn_my_permissions`, because
`fn_my_permissions` only ever answers for the *current* execution context —
there is no principal argument to pass instead. SQL Server refuses to
impersonate a role, at either scope:

| statement | result |
|---|---|
| `EXECUTE AS USER = N'<database user>'` | 3 permissions |
| `EXECUTE AS USER = N'<database role>'` | **Msg 15517** — "this type of principal cannot be impersonated" |
| `EXECUTE AS LOGIN = N'<login>'` | 5 permissions |
| `EXECUTE AS LOGIN = N'<server role>'` | **Msg 15406** — same, server-principal wording |

The error names three possible causes ("does not exist, this type of principal
cannot be impersonated, or you do not have permission"), which is what makes it
easy to misread as a missing principal. It isn't: the roles in the A/B plainly
existed and the connection was `sa`.

So the page is now gone from Database Role Properties and Server Role
Properties, and both gosmo doc comments — which promised roles outright — say
user/login. It is not a gap to fill later; there is nothing to fill it with
short of reimplementing SQL Server's own permission resolution in Go. SSMS
doesn't offer it for roles either. `rolePropPages` and `serverRolePropPages`
each carry a comment saying why, so it doesn't get "restored" as an oversight.

Dropping the page left `serverRolePropPages`' `*PropDialog` parameter unused —
it existed only for that page's `runPageAction` — so it went too.

### The server-scope query needed the USE master prefix

Found while building the A/B, not predicted by the review. `EXECUTE AS LOGIN`
keeps the session's current database, so `EffectiveServerPermissionsContext`
failed with **Msg 916** ("The server principal %q is not able to access the
database %q under the current security context") whenever the pooled
connection sat in a database the target login has no user in — which is the
normal case for exactly the restricted logins the page is most useful on.

Reproduced through gosmo itself, same binary, one line changed:

```
[conn db=master   ] LOGIN -> 5 permissions          [conn db=master   ] LOGIN -> 5 permissions
[conn db=gossms_a1] LOGIN -> ERROR: mssql: ... Msg  [conn db=gossms_a1] LOGIN -> 5 permissions
        916 ... not able to access the database
   (without USE master)                                  (with USE master)
```

`Server.GrantServerPermissionContext` and every other server-scoped statement
already carried this prefix for the same reason; the effective-permissions one
was simply missed. Worth noting the prefix works fine at the head of a *query*
batch and not just an exec — the `USE` produces an ENVCHANGE, not a rowset, and
go-mssqldb returns the SELECT's result set unchanged. That was checked rather
than assumed.

### Verified live against ubudock

Throwaway `gossms_a1` database, `gossms_a1_login`, `gossms_a1_srvrole`,
`gossms_a1_user`, `gossms_a1_dbrole`; all dropped afterward. Driving the built
binary under tmux, **connected to `gossms_a1`** so the Msg 916 path was live:

- Server Role Properties — pages are General, Members, Owned Roles,
  Securables. No Effective Permissions.
- Database Role Properties — General, Members, Owned Schemas, Owned Roles,
  Securables, Extended Properties. No Effective Permissions.
- Login Properties — Effective Permissions still present, and Show reported
  "5 effective permission(s) for gossms_a1_login" with the rows listed. Before
  the prefix, that same click was a Msg 916 error.


---

## 2026-08-05 — Column permissions were broken end-to-end for views

`column-permissions-views-2026-08-05`

Item A2 of the same two-repo review as `effective-permissions-role-fix-2026-08-05`.
A view carries column permissions exactly like a table — `GRANT UPDATE (Name)
ON dbo.SomeView` is legal and `sys.database_permissions` records it as an
`OBJECT_OR_COLUMN` row with a non-zero `minor_id`, indistinguishable from a
table's. Nothing on the Securables page handled that.

Two separate failures, one root cause: `ColumnPermissionEntry` reported no
object type, so gossms had nothing to key on and hardcoded `"TABLE"` in the two
places it built a `securable` from a column entry.

1. **Existing grants on a view showed as "(none)".** The seed keyed the entry
   under `TABLE` while the securable list gave the same object `VIEW`, so
   `columnEditKey` never matched. The grant was there, the grid said it wasn't
   — and cycling the cell would then have issued a fresh GRANT computed from
   the wrong `orig`.
2. **"Load Columns" hard-errored on any view.** It went through
   `TableByNameContext`, whose query reads `sys.tables`, so it returned
   `gosmo: table [dbo].[X] not found` for every view.

### gosmo side

- `ColumnPermissionEntry.ObjectType`, selected from `obj.type_desc` in both
  column-permission queries and mapped through the **existing**
  `securableObjectTypeNames` (security.go) — deliberately reusing that map
  rather than adding a second one, so `ObjectType` and
  `PrincipalSecurable.SecurableType` cannot drift apart. They are keyed
  against each other by callers.
- `Database.ObjectColumns`/`ObjectColumnsContext`, resolving through
  `OBJECT_ID` so it reaches a view. `Table.ColumnsContext`'s query body was
  extracted to a shared `columnSelect` const plus a `scanColumns` helper;
  each caller appends its own WHERE, because a `Table` already holds an
  `object_id` while the Database-scoped form has only a name.
  `Table.ColumnsContext` keeps its exact signature, results and error string.
- Zero rows means the object doesn't exist rather than "has no columns" —
  every table and view has at least one — so that case returns a not-found
  error instead of an empty slice.
- `Database.ObjectColumnSeq` in iter.go, keeping the one-Seq-per-listing
  convention.

The joins that supply identity/computed/default/primary-key data simply do not
match for a view, so those fields come back zero-valued. Name, ordinal, type,
length/precision/scale, nullability and collation are all real. Said so on the
method rather than leaving it to be discovered.

### Verified live against ubudock

Throwaway `gossms_a2` with `dbo.Employee`, `dbo.EmployeeView` over it, and a
non-owner `gossms_a2_user` (an owner's GRANT is a silent no-op — the trap
already recorded in the live-test-server notes). One column grant on each:
`SELECT (Salary)` on the table, `UPDATE (Name)` on the view.

Through gosmo directly:

```
ColumnPermissionsForPrincipal:  TABLE dbo.Employee     col=Salary SELECT GRANT
                                VIEW  dbo.EmployeeView col=Name   UPDATE GRANT
ObjectColumns Employee:      3 columns  Id(int) Name(nvarchar) Salary(money)
ObjectColumns EmployeeView:  3 columns  Id(int) Name(nvarchar) Salary(money)
ObjectColumns NoSuchThing:   gosmo: table or view [dbo].[NoSuchThing] not found
TableByName  EmployeeView:   gosmo: table [dbo].[EmployeeView] not found   <- the old path
```

That last line is the bug reproducing: it is exactly what Load Columns called.

Then the built binary under tmux, Database User Properties > Securables. Both
objects are listed off column grants alone (neither has an object-level entry),
which is the reconstruction path that was hardcoded:

- `[dbo].[Employee] | TABLE` and `[dbo].[EmployeeView] | VIEW` — the view typed
  correctly rather than as TABLE.
- Load Columns on the **view** succeeded ("3 columns"), where it previously
  errored.
- With UPDATE selected, the view's columns read `Id (none)` / `Name Grant` /
  `Salary (none)` — the grant found, not "(none)".
- Regression check on the table: with SELECT selected, `Id (none)` /
  `Name (none)` / `Salary Grant`.

Database dropped afterward.

## permissions-apply-and-drag-fixes-2026-08-05

Items A3–A6 of the 2026-08-05 two-repo review, in one pass. No commits.

### A3 — apply order and the stale baseline

Both permissions editors ranged a Go map to issue their statements
(`editsByPrincipal`, `editsBySecurable`, `colEdits`), and neither moved a
cell's `orig` after the statement succeeded.

Ordering is now a walk of the `principals` / `securables` slice, plus
`slices.Sorted(maps.Keys(colEdits))` for the column edits. Every map key comes
from `editsFor(<member of that slice>)`, so the walk is exhaustive.

`commitApplied` (perm_state.go) moves `orig` onto `current` after a successful
statement, guarded by `!gosmo.Scripting(ctx)` — committing under Script Changes
would mark the page clean and leave the following real Apply with nothing to
do, the same trap `commitRename` documents. `applyPermEdit` pairs the two so a
caller can't do one without the other.

**The plan's stated rationale for A3 was wrong and is corrected here.** It
claimed re-issuing an already-applied transition is "wrong, not merely
redundant" because `permTransition` derives `REVOKE GRANT OPTION FOR` and
`CASCADE` from `orig`. Checked against the live server, every one of those
statements is idempotent:

```
seed                                        GRANT_WITH_GRANT_OPTION
REVOKE GRANT OPTION FOR ... CASCADE   x1    GRANT
REVOKE GRANT OPTION FOR ... CASCADE   x2    GRANT      <- replay is a no-op
DENY ... CASCADE                      x1    DENY
DENY ... CASCADE                      x2    DENY
REVOKE ... CASCADE                    x2    (no row)
```

So the replay costs round trips, not correctness. The defect a stale baseline
*does* cause is the page misreporting server state, and it bites when the user
undoes an edit that already landed: cell downgraded from Grant With Grant to
Grant, Apply issues the REVOKE GRANT OPTION FOR and then fails on a later
cell, user puts the cell back to Grant With Grant and presses Apply — with a
stale `orig` the cell reads clean, nothing is issued, and the grid claims a
grant option the server lost. `Dirty()` and `Revert()` are wrong for the same
reason, both being `orig`-versus-`current` comparisons. That is what
`TestPermissionsMatrixUndoOfAnAppliedEditIsReissued` pins.

### A4 — a filter matching nothing left the lower grids live

`loadSecurable`/`loadPrincipal` returned early on an out-of-range row, so
filtering to zero rows left the previous selection's grid on screen and
editable. Both now call a `clearSelection` that empties the lower grid(s),
resets the section titles and drops `selectedEdits`. Edits already made are
kept — only the display is cleared.

Live A/B, Server Properties > Permissions, filter `zzz`:

```
pre-fix   top grid 0 rows | "Explicit permissions for ##MS_PolicyEventProcessingLogin##", 35 rows
post-fix  top grid 0 rows | "Explicit permissions", 0 rows
```

### A5 — TextRow.Revert contradicted its own doc

`SetDirtyTracked(false)` promised "Revert leaves it alone"; `Revert` reverted
anyway and never fired `onChange`, so `Form.Revert` blanked a filter box while
the grid it filters stayed narrowed on the old term. Fixed on `TextRow` and on
`SelectRow`, which had the identical defect and whose `SetDirtyTracked` doc
points at `TextRow`'s.

### A6 — a drag that leaves the field stops selecting

Two halves. `FindReplaceDialog` hit-tested every `Button1`, so motion outside
the Find field's rect never reached it; it now has a `dragField` press-owner
(cleared on the release and on `Show`, invariants 1 and 4). That alone changed
nothing, because `InputField.HandleMouse` hit-tests too — its own
`mouseDragging` latch now takes priority, which is the actual mechanism.

Live A/B, press on the field's first cell and drag five rows below it:

```
pre-fix   [a]bcdefghij     — caret only, no selection; the drag never arrived
post-fix  [abcde]fghij     — selection extended, SGR bg 48;2;0;122;204
```

The widget half is general but only reaches hosts that forward off-rect
motion; every other dialog still hit-tests first. Recorded in
`docs/open-threads.md` rather than widened here.

### Verification

Unit tests were A/B'd against the pre-fix code rather than just written green.
Each failed first with the message it was written to produce —
`TestPermissionsMatrixApplyOrderIsStable` caught map order on run 4 of 20,
`TestPermissionsMatrixEmptyFilterClearsSelection` caught the stale grid *and*
the cycle reaching the hidden edit ("issued `GRANT ... [WITH GRANT OPTION]`",
i.e. it had cycled a cell the page no longer showed).

`gofmt -l`, `go build ./...`, `go vet ./...`, `go test ./...` clean in both
repos. gosmo untouched this pass. Throwaway database `gossms_a3` dropped,
confirmed at zero.

## gosmo-legacy-permission-delegation-2026-08-05

Item C1 of the 2026-08-05 review. gosmo had two renderers for the same
statement: `permissionStmt.render` (permission_options.go) and a hand-rolled
`fmt.Sprintf` inside each of the twelve plain
`Grant/Deny/RevokePermission*Context` methods — three each at object, schema,
database and server scope. `PermissionOptions`' doc already claimed the zero
value "renders exactly the statement those trios render"; nothing enforced it.

Each of the twelve is now a one-line delegation to its `…WithOptionsContext`
counterpart with a zero `PermissionOptions`. 84 net lines gone, one renderer,
and the doc claim is true by construction. No exported signature changed, so
the gosmo library rule is satisfied — this removes duplication, not
capability.

### Proving it behaviour-preserving

Two harnesses, because neither alone reaches the whole surface.

`TestLegacyPermissionMethodsRenderAndReject` pins, for all twelve, the exact
rendered statement (via `gosmo.WithScript`, which captures writes without a
server) and the exact validation error for an unrecognized permission name.
Written and run green against the **pre-delegation** code first, so it pins
existing behaviour rather than the refactor's.

Scripting can't reach the exec-error wrap — under `WithScript` the write
succeeds. Those needed a real server, calling each scope against an object
that doesn't exist, captured before and after:

```
object grant    gosmo: grant SELECT on [dbo].[NoSuchTable] to "NoSuchUser": mssql: Cannot find the object ...
object revoke   gosmo: revoke SELECT on [dbo].[NoSuchTable] from "NoSuchUser": ...
schema deny     gosmo: deny UPDATE on schema "NoSuchSchema" to "NoSuchUser": ...
database grant  gosmo: grant CREATE TABLE to "NoSuchUser" in "master": ...
server grant    gosmo: grant VIEW SERVER STATE to "NoSuchLogin": ...
server revoke   gosmo: revoke VIEW SERVER STATE from "NoSuchLogin": ...
```

`diff before.txt after.txt` — identical. `fromOrTo` reproduces the to/from
split exactly, and `lower := strings.ToLower(verb)` reproduces each verb's
lower-cased prefix.

Nothing was created on the server: every call names an object that does not
exist, so all six failed by design and wrote nothing.

### A note on the older test

`TestZeroPermissionOptionsMatchesPlainStatement` compared the plain and
WithOptions statements for four pairs. After the delegation both sides run the
same code, so it can no longer detect two renderers drifting — it now asserts
the delegation is still in place. Its comment says so, and the literal
statements moved to the new table test.

## editor-search-draw-optimizations-2026-08-05

B1 and B2 from `docs/proposals-2026-08-05.md` — the two find/replace hot paths,
both in `internal/tuikit/controls`.

### B1 — `styleForRune` scanned the whole match list per drawn column

`lineRow.styleForRune` ran `for _, m := range r.matches` for every rune it
styled, so a Draw pass cost `visible columns × matches on that line` per row.
Searching a common short string in a wide script is exactly that shape.

Replaced with `lineRow.inMatch`, an advancing cursor: `drawLineRow` only ever
asks for a non-decreasing `i`, and `matches` is sorted and non-overlapping, so
each match is stepped past once per row. The first lookup binary-searches
rather than walking from index 0 — a horizontally scrolled row, and every wrap
segment after the first, starts part-way along the line, and under wrap mode
everything left of the segment is most of the list. `matchCur`/`matchPrimed`
are scratch, not caller input, which is why the zero value has to mean *not
primed*: `lineRow` is built as a composite literal at two call sites and
neither should have to remember to initialise them.

### B2 — `byteRuneIndex` allocated for every line, ASCII included

`scanMatches` built a `len(line)+1` `[]int` per line per scan to convert the
regexp engine's byte offsets to rune indices. For a pure-ASCII line — the
overwhelmingly common case in T-SQL — that map is the identity. It now returns
`nil` there and the caller uses the byte offset directly. An edit invalidates
the scan, so this ran once per line of the whole document on the next Draw.

### Measured, `-count 6`, i5-2500K

| benchmark | before | after |
|---|---|---|
| `EditorDrawManyMatchesWideLine` | 131 µs/op | 37 µs/op |
| `EditorDrawManyMatchesWrapped` | 1.31 ms/op | 147 µs/op |
| `SearchScanASCII` | 36.3 ms, 3.84 MB, 22010 allocs | 32.5 ms, 1.43 MB, 17004 allocs |
| `SearchScanNonASCII` | 36.4 ms, 4.18 MB, 22009 allocs | 36.5 ms, 4.05 MB, 21509 allocs |

The 5006 allocations `SearchScanASCII` loses are one per line of the 5000-line
fixture. `SearchScanNonASCII` is unchanged by design; it drops a few hundred
because some lines of that fixture are still pure ASCII.

### How it was checked

Five style-per-column tests in `editor_matchstyle_test.go`, run green against
the pre-change code first so they pin existing behaviour: every match on a
line painted (not just the first), the current match in the selection style
while the rest stay in the match style, matches under horizontal scroll, every
segment in wrap mode, and a match spanning wide runes. A `styleScreen` records
the style of each cell and reads a row back as `m`/`s`/`.`, so these assert the
resolved style rather than that Draw survived.

B2 splits ASCII and non-ASCII into different code paths, which had no test at
all before: three added in `editor_search_test.go` cover rune-index bounds on a
non-ASCII line, a document mixing the two kinds of line (one line's mapping
must not carry into the next), and a replacement on a non-ASCII line, where
bounds off by the multi-byte prefix corrupt the text rather than merely
mis-highlighting it.

Live: connected to ubudock, `SELECT col, col FROM dbo.T; -- col héllo col
wörld col`, Ctrl+F `col`, Enter, Escape. `capture-pane -p -e` shows the first
hit on the selection background (`48;2;0;122;204`) and the other four on the
match background (`48;2;88;76;22`), including both hits that follow a
multi-byte rune.

## page-action-latch-and-scope-rows-2026-08-06

A7 and C2 from `docs/proposals-2026-08-05.md`.

### A7 — a second click put two round trips in flight

`runPageAction` launches a goroutine and reports back through `d.post`; no
caller guarded against being clicked again while the first was still out. Two
goroutines then both write the captured result variable and both fill the same
grid, so the picture that survives is whichever finished last — which can be
the older request.

Added `PropDialog.runPageActionOnce(&inFlight, fn, onDone)`, which sets the
latch on the way in and clears it in the callback. `inFlight` is a plain bool
because both halves run on the UI goroutine: the click handler, and `onDone`
via `d.post`. Wired into the two Effective Permissions Show buttons, the
Securables page's Load Columns, and `asyncStatusButton` — the last had the same
defect and covers Check Syntax, Rebuild, Reorganize and Update Statistics in
one place. A blocked click is not a silent no-op: the hint or status row
already reads "Resolving..." / the busy text from the first one.

Pinned by `prop_dialog_action_test.go`, which plays the event loop's part
itself — with no screen the wake half of `postAndWake` is a no-op, so the test
drains `pending` in a loop. It asserts the second click launches nothing, the
latch clears afterwards, a later click runs, and the latch clears on the error
path too. Stubbing out the guard fails it with "a second click ran while the
first was still in flight".

### C2 — the header literal, and rows that do nothing

`effectivePermsCols` hoisted: `SetData` takes the header with the rows, so two
literals were two things to keep in step.

Schema and Table-or-view are now enabled only for the scope that resolves
against them, re-synced from the picker's `OnChange`. The `Show` guards stay —
a row can be enabled and still blank, and dropping live validation to save two
branches would let a blanked Schema box reach the query.

**That change exposed a real bug and could not ship without fixing it.**
`TextRow.SetEnabled`'s doc claims a disabled row is "drawn dim", but
`InputField` had no notion of being disabled: the row merely became
unfocusable, so it rendered *identically* to a live field while ignoring every
click — the "click does nothing" failure `CLAUDE.md` § Application rules is
about. Three shipped callers were already in that state
(`database_props_scoped_config.go`, `login_props.go`, `server_props.go`), so
this was not new, but adding a fourth knowingly was not an option.

`InputField` now has `SetEnabled`/`Enabled`. A disabled field drops the input
background entirely (`theme.StyleInputDisabled`, dialog background + `TextDim`)
rather than only dimming its text, keeps the unfocused border, paints no
caret, and refuses keys and clicks — the mouse guard sits *above* the
`ButtonNone` branch, since a disabled field never latched a press and so has no
drag to end. `TextRow.SetEnabled` forwards to it, which makes its own doc true
and fixes the three existing pages as a side effect.

### How it was checked

`TestDisabledInputFieldRefusesInput` covers keys, press, release and
re-enabling; `TestDisabledInputFieldDrawsDifferently` asserts the two states do
not render identically, which is the half that was missing before.
`fieldScreen` now records styles as well as runes.

Live, against ubudock: Database User Properties for `clinic_app_user` →
Effective Permissions. Under Database scope both lower rows draw on the dialog
background (`48;2;45;45;48`) with no input background anywhere; switching the
picker to Table or view brings `48;2;51;51;55` back on both; Schema scope
brings it back on Schema only. Show still resolves (13 permissions on schema
dbo), three rapid clicks produce one result, and the button is not wedged
afterwards.

## securables-server-side-search-2026-08-06

B3 from `docs/proposals-2026-08-05.md`, taken as the server-side option
rather than the cheap client-side filter.

The Securables page used to call `TablesContext` + `ViewsContext` +
`SchemasContext` on open and turn the entire result into one dropdown. On a
database with thousands of tables that is slow to open and useless to pick
from, and the existing "Filter securables" box only narrowed the *top grid*,
never the picker.

### gosmo: `Database.FindSecurables`

New `securable_search.go`. `FindSecurablesContext(ctx, SecurableSearch{Name,
Limit})` is one query over `sys.schemas`/`sys.tables`/`sys.views`, matching
`Name` case-insensitively as a substring of the *qualified* name — so both
"ord" and "dbo.ord" find `dbo.Orders` — and ordering schemas, then tables,
then views, each by qualified name, so a capped search returns a stable
prefix rather than an arbitrary subset. `SecurableRef` carries only Type,
Schema and Name: a picker needs the identity, and materialising `Table`/`View`
values for thousands of candidates is work no caller wants.

`escapeLikePattern` makes the term literal under `ESCAPE '\'`. Identifiers
legally contain `_` and `%`, and a name containing `[` turns the pattern into
a character class that matches nothing — the search box would just come up
empty with no explanation. Verified live against HealthClinic: `_` returns
only names literally containing an underscore (13), `%` and `[a` return
nothing, `pat` returns `dbo.Patients` and `dbo.vw_PatientHistory`, `dbo.pat`
returns only the table, `PAT` matches both regardless of collation.

### gossms: the search box

`buildSecurablesMatrix` takes `candidates []securable` plus a
`securableFindFn` instead of a fixed `available` list. The page loader runs one
capped search so the picker has content before anything is typed; a new
"Search to add" row re-queries as the user types.

**The searches coalesce rather than queue.** At most one is in flight; if the
term moved on while it was out, the completion starts the next one. A query
per keystroke puts five round trips behind "Order" *and lets an early one land
last* — the A/B against an unguarded version shows exactly that, the recorder
receiving `[ord o]` and the picker ending up repopulated from a term the user
had already typed past.

`FindSecurables` returns at most `securableSearchLimit+1`; the extra row is how
the page knows to trim to 200 and say "More than 200 matches — type more to
narrow the list." A trimmed list that says nothing reads as complete, and the
user concludes the object does not exist. The database itself is a securable no
name search returns, so the page offers it whenever the typed term matches its
label — the same `matchesFilter` the top grid uses.

### How it was checked

Four tests in `securables_search_test.go`, driving a real built form with a
recording find function: coalescing, the cap and its hint, the database entry
appearing only when the term matches it, and a failed search reporting through
the hint while leaving the picker alone (emptying it would read as "no such
object"). They run under `-race`; the recorder locks, since the find function
runs on a background goroutine.

Live, against ubudock → HealthClinic → clinic_app_user → Securables: the page
opens with no catalog load, typing "pat" narrows the picker to the two
matching objects and drops "(database)", picking the view and clicking Add
gives it a row typed VIEW with the middle grid switching to its permission
catalog, and "pazzqt" leaves "(no matches)" with "Nothing on the server matches
that name."

Note for a future reader: `Database.Tables`/`Views`/`Schemas` are untouched —
gossms simply no longer calls them from this page. They are library surface,
not dead code.

## grid-column-resize-2026-08-06

Mouse-resizable `DataGrid` columns, SSMS-style: drag the separator in the
header row and the column *to its left* changes width; double-click that
separator and it goes back to the width it was given.

The Options dialog's "Max cell length (Query Results)" is now "Max default
cell length" — the same number, but it now caps only what `computeColWidths`
hands a column from its content. A dragged width (`colWidthOverride`) is
applied after that clamp and ignores it, so a 24-character cap no longer
means a 60-character value can never be read in the grid.

### What the state has to survive

`computeColWidths` runs far more often than the data changes — every
`SetBounds`, and every frame after a progressive backfill's
`RefreshColumnWidths`. So the drag can't just write `colWidths`: it records
an override and lets the recompute re-apply it, or a window resize would
silently undo the user's drag. Two consequences fall out of that:

- `SetSource`/`SetError` clear the overrides. Column 2 of the next result set
  is not column 2 of this one.
- `growLastColumnToFill` is skipped for a last column that has an override —
  otherwise the Property/Value detail grids would stretch a deliberately
  narrowed Value column straight back out.

The drag itself follows the scrollbar's shape exactly: `colResizing` latches
on the press and every Button1 event belongs to the resize until the release,
checked ahead of `rect.Contains` so the edge keeps following a pointer that
has left the grid. `resizeStartX`/`resizeStartW` are the grab point, so each
motion resolves against the original grab instead of accumulating.

Hit-testing is the header row only. The separator glyph runs down every data
row, and grabbing it there would eat cell-selection clicks for a
one-column-wide target the user is unlikely to have aimed at.

tcell reports presses, not clicks, so the double-click is timed here
(`sepPressIsDouble`, 500ms, same separator). The press that completes a
double-click still latches `colResizing`: tcell resends Button1 on every
motion while the button is down, and by then the separator has moved, so
those resends have to be absorbed by the latch rather than re-entering the
hit test against a stale position.

### How it was checked

Seven tests in `datagrid_resize_test.go` — widen past the max default, narrow
down to `minResizeWidth`, survive `SetBounds`/`RefreshColumnWidths` but not
`SetData`, double-click restore, header-row-only hit testing, the row-number
gutter's offset, and fill-last-column losing to a drag.

Live against ubudock, driving the built binary under tmux with raw SGR mouse
sequences: `select name, type_desc, create_date from sys.objects`, dragging
the `name` separator from column 74 to 100 took the column from 26 to 52 and
double-clicking it put it back at 26. With a 60-character value and the
default 24-character max, widening the column showed the whole value —
`abcdefghij`×6, untruncated — which is the point of the change. Cell
selection on a data row still worked afterward.

---

## 2026-08-06 — Drag hosts closed, and the drain gate settled live

`drag-hosts-and-live-gate-2026-08-06`

Two open threads closed: the text-selection-drag hosts, and the two
live-only verifications that had been carried since 2026-07-30.

### The drag hosts

`widgets.InputField` has honoured its own `mouseDragging` latch ahead of its
`HitTest` since 2026-08-05, so a field extends a selection wherever the
pointer goes — but only if its host forwards the off-rect motion. Four hosts
still hit-tested every `Button1` first and got a `dragField` press-owner,
the same shape `FindReplaceDialog` already had: `connect_dialog.go` (seven
fields), `backup_dialog_input.go` (`fDest`), `restore_dialog_input.go`
(`fTarget`/`fFile`), `tuikit/dialogs/file_dialog_input.go`
(`pathField`/`nameField`).

Placement mattered in two of them. In `file_dialog_input.go` the replay has
to go *above* `ButtonClicked`, not inside the `Button1` switch below it —
otherwise a selection drag that wandered over the button row fired the
button. In `restore_dialog_input.go` the release has to be handled ahead of
both `ConsumeOutsideClick` and the mode switch, either of which returns
early and would strand the latch.

**Two of the six hosts the open thread listed did not need fixing**, which
is the part worth remembering, because the entry had been carried for a day
naming them:

- `propsheet.Form` gives `Focused()` first refusal of every non-wheel button
  *before* band routing (`form.go:387`), so a focused `TextRow` already saw
  off-band motion.
- `options_dialog.go` calls `fMaxCellLen.HandleMouse` unconditionally — the
  field self-hit-tests, so the drag worked. It did have a real latch bug,
  found while checking: its `ButtonNone` reset list covered `rbIconStyle`
  and `cbIntelliSense` but not `fMaxCellLen`, so a release outside the
  dialog (eaten by `ConsumeOutsideClick`) left the field armed and it
  swallowed the next press. One line.

Each new test was A/B'd against the pre-fix code and fails on it — the
connect and file-dialog ones on `SelectedText()` being empty or the list
stealing the gesture, the options one on the stranded latch.

### The live gate

`executor_sink_test.go`'s fake driver implements the sqlexp contract but not
TDS, so deleting `runBatch`'s drain loop still passes it. Both halves of the
gate were finally run against the real server
(`internal/query/live_drain_test.go`, build tag `livedb`, skipped without
`-livedb`):

- **An extra `Next()` past an exhausted set does swallow the pending
  message — confirmed, and worse than recorded.** The control run saw both
  result sets and the `PRINT` between them; with one extra `Next()`, the
  entire second result set never arrived. That is exactly the shipped
  failure CLAUDE.md forbids reintroducing (empty grid, no error, no Messages
  tab), now reproducible on demand.
- **The drain loop after an abandoned set is *not* load-bearing on this
  server/driver.** Without it, go-mssqldb still advanced past the abandoned
  set and delivered everything after it. Keep the loop — it is what makes
  the behaviour independent of a driver detail — but its removal is no
  longer an unquantified live-only risk.

### ExecProc under WithScript

`gosmo/live_execproc_script_test.go` (same tag) scripts the EXEC form and
then hands the text to the server the way a user pasting it would. The open
thread's specific worry was wrong: a `decimal(18,4) OUTPUT` does not get
`SQL_VARIANT` when the caller passes the natural `*float64` — it gets
`FLOAT`, the server accepts it, and `1234.5678` round-trips exactly. `INT`,
`BIGINT`, `NVARCHAR(MAX)`, `FLOAT`, `BIT` and `DATETIME2` all ran and
round-tripped.

The fallthrough underneath it is a real defect, though: an unmapped pointee
type gets `DECLARE @v SQL_VARIANT`, and against a decimal OUTPUT the server
refuses — "Implicit conversion from data type sql_variant to decimal is not
allowed." The scripted EXEC is handed to the user as text they cannot run.
Left unfixed and recorded in `docs/open-threads.md`: erroring out of
`scriptExecProc` for an unmapped pointee is probably right, but it is a
behaviour change on a published gosmo API and the author's call.

---

## 2026-08-06 — scriptDeclType's SQL_VARIANT gap

`execproc-decltype-fix-2026-08-06`

Follow-on from the live ExecProc check the same day. The first pass reported
the `SQL_VARIANT` fallthrough as a defect and left it for the author; the
fix took a different shape than either option offered there, because two
live probes changed the picture.

### What the probes settled

- **`DECLARE @v SQL_VARIANT` is correct and accepted** against a procedure
  whose parameter really is `sql_variant`. So erroring out of
  `scriptExecProc` on the fallthrough — the option that looked right — would
  have broken the one case the fallthrough exists for. It is a working
  capability, not a bug.
- **The destination type that triggered the original report wasn't reachable
  through gosmo's API anyway.** The probe used a `*struct{X int}`, which the
  driver rejects on the real RPC path too ("unsupported type ..., a struct"),
  so the script path emitting bad SQL for it was moot.

Which left the actual question: which destination types are *valid* on the
RPC path and still fall through to `SQL_VARIANT`? Eleven of them, and they
are not exotic:

```
*sql.NullInt64  *sql.NullInt32  *sql.NullInt16  *sql.NullByte
*sql.NullString *sql.NullBool   *sql.NullFloat64 *sql.NullTime
*mssql.UniqueIdentifier  *mssql.NullUniqueIdentifier
```

All accepted by the driver (`rpc=<nil>`), all scripted as `SQL_VARIANT`, all
refused by the server: *"Implicit conversion from data type sql_variant to
int / nvarchar / uniqueidentifier / decimal is not allowed."* `sql.Null*` is
*the* way to receive a nullable OUTPUT parameter, so this was the ordinary
path, not a corner.

### Why the kind switch missed them

`scriptDeclType` switched on `reflect.Kind`. The `sql.Null*` family and
`NullUniqueIdentifier` are structs — the `Struct` branch only knew
`time.Time` — and `UniqueIdentifier` is a `[16]byte` array, which had no
branch at all. Their Go *kind* cannot give their T-SQL type away, so the fix
is a type-keyed lookup (`declTypeByName`) consulted ahead of the kind
switch. Purely additive: the kind switch is untouched and `SQL_VARIANT`
remains the fallthrough.

`scriptLiteral` needed nothing — it already handles `driver.Valuer`, which
every `sql.Null*` implements, so the `InOut` seed value was always fine.

### Verification

`script_test.go` pins all eleven mappings plus the two `SQL_VARIANT` cases
(unmapped struct, non-pointer) in the ordinary suite.
`live_execproc_script_test.go` (tag `livedb`) runs each scripted `EXEC`
against a procedure with the matching parameter type, and separately
confirms `SQL_VARIANT` still binds to a `sql_variant` parameter — the
regression that widening-instead-of-erroring exists to avoid.

One oddity worth knowing: `*mssql.NullUniqueIdentifier` scripts correctly but
the *driver* rejects it as an `sql.Out` destination ("Data type 0x00 is
unknown"). The mapping is kept — the scripted form is right, and the
limitation is go-mssqldb's.

---

## column-ddl-removal-2026-08-06

Author's call: `Table.AddColumn`/`AddColumnContext` and
`Table.DropColumn`/`DropColumnContext` removed from gosmo outright, rather
than left in as the accepted-non-functional pair `docs/open-threads.md` had
carried since 2026-08-01. `DropColumn` failed on any column an index
referenced, and the fix was a policy choice (drop dependent indexes, or
detect and refuse) nobody wanted to make; neither method had a gossms caller
or a UI path.

This is the standing exception to CLAUDE.md's "never remove a gosmo
capability" rule — that rule binds *me*, not the author, and the removal was
directed explicitly.

`AlterColumn` stays, and so does `CreateTable`, which is the only remaining
user of `ColumnDefinition`'s `IsIdentity`/`IdentitySeed`/`IdentityIncr`/
`DefaultValue` fields — they are not dead now.

Follow-on edits, all of which named the removed methods:

- `table.go` — `AlterColumn`'s doc comment pointed at "DropColumn/AddColumn"
  as the way to change a default; now says "the column, or its default
  constraint", naming no method.
- `script.go` — `bindScriptArgs`'s comment listed the four parameterised
  write methods. `Table.DropColumn` was one; three remain (`Index.Rename`,
  `Database.RenameTable`, `Database.DropTable(cascade=true)`), verified by
  grepping `@p1` across non-test sources and discarding the read paths.
- `script_test.go` — dropped the `DropColumn` case from
  `TestWithScriptBindsParametersIntoTheStatement`. It was the only case
  covering *two* placeholders in one statement, but `Index.Rename` and
  `RenameTable` both bind `@p1`+`@p2`, so the coverage is not lost.
- `table_test.go` — `TestAddColumnRequiresName` deleted; its shared header
  comment now covers `TestAlterColumnRequiresName` alone.
- `permission_allowlist_test.go` —
  `TestAddAndAlterColumnRejectUnknownDataType` is now
  `TestAlterColumnRejectsUnknownDataType`. The `validDataType` allowlist is
  still exercised, by `AlterColumn` and `CreateTable`.
- `README.md` and `gosmo.mermaid` — both carry the same generated `class
  Table` block; both lost the two method lines, plus the two rows of the
  Table method table in README.

`CHANGELOG.md`'s v0.0.x entry still names `Table.DropColumn` as one of the
four methods the script-argument fix repaired. Left alone deliberately: it
records what was true of a released version.

Verified: `gofmt -l`, `go vet ./...`, `go test ./...` clean in gosmo; gossms
builds and its full suite passes against the local checkout through the
active `replace`.

`docs/open-threads.md` compacted in the same pass — 245 lines to ~135. Both
column-DDL entries removed, the closed `ExecProc` line removed (the fix is
recorded in `execproc-decltype-fix-2026-08-06` above), the closed
text-selection-drag paragraph removed, and the now-empty "Follow-ons from the
2026-07-30 two-repo review" and "Left open by the second review" headings
folded away. Activity Monitor had two entries (a stub section and an unbuilt
feature) and is now one. The `formatValue` `float32` note and the `USE
master;` finding moved into "By design — do not re-raise", where they always
belonged: both are do-not-re-raise records, not open work.
