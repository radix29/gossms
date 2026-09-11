# Review plan — gossms + gosmo (2026-09-11)

A whole-codebase review of gossms (`cf929d3`) and gosmo (`194ce3f`), and the
plan to fix what it found. **Nothing here is implemented yet.** Each item names
its evidence, the fix, the test that pins it, and a size (S < 1h, M ≈ half a
day, L ≈ a day or more).

Items already recorded in `docs/open-threads.md` (B1–B5, V2–V6 and every
"do not re-raise" decision) are not repeated. That includes the decision that
restructuring `internal/tui` is closed, so no file or package splits are
proposed for it.

## How the review was done

- `go build`, `go vet`, `gofmt -l`, `go test ./...` and `go test -race` on
  both repos: all clean and green. `staticcheck` found one real lint (R26).
  `deadcode`, and `govulncheck` (0 reachable vulnerabilities; the one module
  hit, `x/crypto/openpgp`, is never imported).
- AST scans written for this review, over non-test code:
  - every `fmt.Sprintf` that builds T-SQL in gosmo (≈220 sites), checking
    what each argument is: an identifier through `QuoteName`/`qualifiedName`,
    a literal through `escapeSingle`/`nStringLiteral`, or a keyword checked
    against an allowlist;
  - gosmo conventions: `FooContext` without `Foo`, a query with no
    `rows.Err()`, a query nested inside a `rows.Next()` loop, an exported
    error missing the `gosmo:` prefix;
  - gossms: a field assigned inside a `safego`/`safegoRepair`/`fanOut` body
    outside a posted closure, a bare `go` statement, a `SetData` +
    `SetSelectedRow` pair, and a write menu item with no `gate`.
- A token-window clone detector over both repos.
- Reading, in full: the app core, the async and threading layer, `db`,
  `query`, `config`, `fileutil`, `activity`'s collector and rates, the Object
  Explorer model, the Detail Browser, `PropDialog`/`newObjectDialog`,
  `TreeView`, `InputField`, `strutil`, the permission gate's declarations, the
  object ops, gosmo's connection/DSN/dialer/retry/script/error/quoting code.
  Everything else was sampled.
- Two checks outside the code, both since removed or read-only: a temporary
  unit probe for R1 (it failed as predicted, then the file was deleted), and a
  read-only `sqlcmd` query on win10cli for R7.

**The overall picture.** The conventions both repos write down are enforced
in practice. Every mechanical scan above came back empty, apart from the
Agent menu (R6): every T-SQL argument is quoted or allowlisted, there are no
queries nested in `rows.Next()` loops, no UI state is written from a
background goroutine, and none of the grid rules in `ui-rules.md` are broken.
The real defects share one cause: **when something is replaced or closed, who
releases what it owned?** A tree node's children, a panel, a dialog's
previous showing, a superseded fetch. R1–R5 and R13 are all versions of that
question, which is why R15 proposes one shared mechanism for it.

---

## P1 — Bugs

### R1 · Object Explorer selection jumps to a different node after an async load (HIGH, M)

**Done 2026-09-11** — tests in `object_explorer_test.go` and `treeview_test.go`,
mutation-checked; live A/B on the MI.

- **Where:** `internal/tuikit/controls/treeview.go:98` (`SetNodes`),
  `internal/tui/object_explorer.go:309` (`rebuild`).
- **What:** `SetNodes` keeps the selection by **index** and only clamps it.
  When rows are inserted or removed *above* the selection — children arriving
  for a folder that is expanded but still loading, a Refresh, a folder reload
  after a toggle — the index points at a different node. `Selected()` then
  returns that node, and `OnSelect` is not fired, so the Details pane and the
  status bar go on describing the old one.
- **Verified:** a temporary unit probe expanded Databases, selected Security
  below it, then delivered 3 databases. `Selected()` returned `db2` instead of
  `Security`.
- **Impact:** keyboard actions go to the wrong object: Ctrl+Space/Shift+F10
  menu → Delete/Rename/Properties/Script, F5, Enter. A reload after a toggle
  (`refreshExplorerNode(parent)`) also leaves `Selected()` on the wrong node
  while "Loading..." shows. The confirmation names the object, which is the
  only safeguard left. The window is widest on slow targets (MI ≈ 41 ms per
  round trip).
- **Fix:**
  1. `TreeView.SetNodes` keeps the selected `TreeNodeID` if it is still
     present, and keeps its row on the same screen line.
  2. `ObjectExplorer.rebuild` resolves selection itself, because a refresh
     makes **new** nodes with new IDs. Before rebuilding it records the
     selected `*explorerNode`. Afterwards it selects, in order: that node, if
     it is still in the tree; otherwise a child of the same parent with the
     same `(Type, Schema, Name)` (a reload that re-created it); otherwise the
     nearest ancestor that survived. It calls `SelectID` only when the
     resolved node differs, so `OnSelect` fires exactly then.
- **Tests:** the probe above, made permanent, plus three more: a refresh that
  re-creates the selected node (selection follows it by identity); a delete of
  the selected node (selection goes to its parent); a load below the
  selection (no change, and no `OnSelect`).
- **Live:** tmux against the MI or win10cli. Expand a large Tables folder and
  press Down twice before it lands. The highlight must stay put.

### R2 · Closing a Query Store panel doesn't cancel its reads (MED, S)

- **Status:** done 2026-09-11 (uncommitted) — `layout.Disposable`,
  `QueryPanel.Close`, `internal/tui/panel_dispose_test.go`.

- **Where:** `internal/tui/app_panel_actions.go:212` (`closePanelAt`),
  `internal/tui/query_store_panel.go:321`.
- **What:** `closePanelAt` type-switches over ActivityMonitor, AGDashboard,
  LogViewer and QueryPanel. `QueryStorePanel.Close()`, whose doc says "Called
  from App.closePanelAt", is called only by tests. A panel closed mid-read
  leaves its report, plan and series reads running on the shared Object
  Explorer pool until `qsReadTimeout`.
- **Fix:** add `layout.Disposable interface{ Close() }` and have
  `closePanelAt` call it on any panel that implements it. This replaces the
  four-way switch, so a new panel type with resources cannot be missed.
- **Test:** for each panel type with a `Close()` method (found by reflection
  over the constructors), `closePanelAt` must call it. Mutation check: drop
  the interface call and the test must fail.

### R3 · Refresh leaks every replaced node, and there are six copies of Refresh (MED, M)

**Done 2026-09-11** (uncommitted) — `ObjectExplorer.Reload`, tests in
`explorer_reload_test.go`, mutation-checked. `refreshExplorerNode` is gone;
its callers call `Reload`. One deviation from the fix below: replaced nodes
leave `byID` and the Detail Browser at the next *rebuild*, not at the Refresh.
Until the load lands their rows are still drawn, and `Selected()` must still
answer for them. Their loads are cancelled at once. A retired node refuses
`SetChildren` and `loadChildren`.

- **Where:** `object_explorer.go:152` (`RefreshDatabasesFolder`), `:175`
  (`RefreshLoginsFolder`), `:206` (`RefreshFolderByType`), `:276`
  (`RefreshSelected`); `app_explorer_data.go:234` (the context-menu Refresh);
  `agent_common.go:79` (`refreshExplorerNode`). Also `detail_browser.go:52`
  (`cache`) and `:74` (`pending`).
- **What:**
  - Each copy sets `n.children = nil` without `removeSubtree`, so every
    replaced node and its whole subtree stays in `ObjectExplorer.byID` for the
    life of the connection.
  - Replaced nodes with a fetch in flight are never cancelled (`cancelLoad`).
  - `DetailBrowser.cache` holds full detail rows keyed by `*explorerNode`, and
    entries for replaced nodes are only dropped on disconnect (`PurgeConn`).
  - `DetailBrowser.pending` is never deleted after a successful fetch, so it
    gains one entry per node ever selected, and keeps each node alive.
- **Impact:** memory grows with every Refresh of a large folder, and pointers
  to stale nodes stay reachable.
- **Fix:** one `ObjectExplorer.reload(n)` that:
  - calls `dropChildren(n)`: for each descendant, cancel `cancelLoad`, delete
    it from `byID`, and purge it from the Detail Browser's `cache`/`pending`
    (a new `DetailBrowser.Forget(nodes)`);
  - marks `n` unloaded, re-loads it if expanded, and invalidates its details.

  All six copies call it, and the connection-level extras (R4, and
  `forgetPeerFailuresForRefresh`) live there once. `postFinal*`/`cacheOnly*`
  delete `pending[node]` once they have cached.
- **Tests:** Refresh a folder of N children with grandchildren: `len(byID)`
  and `len(detailBrowser.cache)` go back to where they started. A replaced
  child's in-flight load context is cancelled.

### R4 · Capability refresh depends on how Refresh was invoked, and never re-reads server scope (MED, S–M)

**Done 2026-09-11** (uncommitted) — in `Reload`, tests in
`explorer_reload_test.go` and `db/capabilities_test.go`, mutation-checked,
`-race` clean. It also adds a generation counter, so a database probe in
flight across `ClearCapabilityCache` doesn't cache its pre-clear answer, and
it re-primes the selection's database. A failed re-probe keeps the previous
server answer.

- **Where:** `object_explorer.go:282` (F5 path),
  `app_explorer_data.go:234` (menu path), `db/capabilities.go:120`
  (`ClearCapabilityCache`) and `:137` (`ProbeCapabilities`).
- **What:**
  - F5 on the server node clears the per-database capability cache;
    right-click → Refresh on the same node does not.
  - Neither path re-probes **server-scope** capabilities (`sc.caps`), which
    are read once, in `ConnectContext`. So a `GRANT ALTER ANY LOGIN` given to
    a connected login is not seen until it reconnects.
  - The doc comment on `RefreshSelected` claims the Refresh "drops its cached
    capability answers", which is only half done.
- **Fix:** part of `reload(n)` from R3. For `NodeServer`, clear the database
  cache and re-probe server capabilities off the UI goroutine. `sc.caps`
  becomes an `atomic.Pointer[gosmo.Capabilities]`, because it is then written
  after the connection is shared.
- **Test:** a fake connection whose probe answer changes between two calls.
  Both Refresh paths must pick up the new answer, and `go test -race` must
  stay clean.

### R5 · Properties dialog: a previous showing's page loads can reach the next one (MED, S)

**Done 2026-09-11** — tests in `prop_dialog_reshow_test.go`, each of the three
fixes mutation-checked (the context test under `-race`). `newObjectDialog`'s
`d.ctx != sessionCtx` guard is kept: it also protects that dialog's own
`fetching`/`waiting` state, which the sheet's seq cannot see.

- **Where:** `internal/tuikit/propsheet/sheet.go:149` (`SetPages`) and `:244`
  (`startLoad`); `internal/tui/prop_dialog.go:275` and `:294`.
- **What:**
  1. **seq is per page and resets on every showing.** `SetPages` rebuilds the
     page slots with `seq` 0, so the first load of page *i* is seq 1 on every
     showing. A load or error posted late by the previous showing's page *i*
     passes the `SetPageForm`/`SetPageError`/`SetPageReadOnly` guards of the
     next showing's page *i*.
     - The usual symptom: close Properties on X, open it on Y, and Y's first
       page shows X's "context canceled" error until Y's own load lands.
     - The worst case: X's form and apply are installed on Y's page.
     - `newObjectDialog` already found this problem and patched it locally
       (`new_object_dialog.go:218`, `d.ctx != sessionCtx`). `PropDialog` has
       no such guard.
  2. `onLoadPage` writes `d.applyFn[page] = apply` **before** any staleness
     check, into the new showing's map.
  3. **Data race.** The closure `pageActionBody` returns reads `d.ctx` on the
     background goroutine, while `show()` rewrites `d.ctx` on the UI
     goroutine. An action still running from the previous showing also uses
     the new showing's context.
- **Fix:**
  - Make page seq a sheet-wide counter that `SetPages` never resets.
  - Make `SetPageForm` return whether it accepted the result, and write
    `applyFn` only when it did.
  - In `pageActionBody`, capture `ctx := d.ctx` before returning the closure.
  - Once the sheet-wide seq is in, `newObjectDialog`'s local guard is
    redundant but harmless. Keep it or drop it in the same change.
- **Tests:** open, close and reopen with a delayed fake load on page 0: the
  stale result is ignored, and `applyFn[0]` is the new showing's. The race is
  covered by `-race` on a test that reshows while a page action is running.

### R6 · Agent Start/Stop/Enable/Disable/Delete are offered without a permission gate (MED, S)

- **Status:** done 2026-09-11 (uncommitted) — `agentGate` in `agent_menu.go`;
  meta-test `TestEveryWriteMenuItemIsGated` in `menu_gate_test.go` walks every
  node type's full context menu (the spliced Script/Rename/Delete/Filter groups
  too), with one exemption, Remove Filter. It failed on exactly the ten Agent
  items before the fix; mutation-checked. Live on win10cli with a throwaway
  login in no msdb Agent role: the items were greyed out with "needs
  SQLAgentUserRole", and they came back after `ALTER ROLE SQLAgentUserRole ADD
  MEMBER` and a server Refresh.

- **Where:** `internal/tui/agent_menu.go:58-124`.
- **What:** on the job, schedule, alert and operator leaves, none of these
  items is gated. On the same nodes, Rename (through `serverScopedOpRights`
  → `agentWriteRights()`) and the folders' New … items are. That breaks
  ui-rules' "every menu item must be context-gated" and fails open for a login
  with no msdb Agent role. The page-gating meta-test covers pages only, so
  nothing caught it.
- **Fix:** wrap each item in `gate(…, sc, "msdb", agentWriteRights()...)`,
  the shape the New Job item already uses.
- **Test:** a meta-test over `nodeMenus` that builds each builder's items on
  a fake node and fails on any item whose label starts with a write verb
  (Delete, Drop, Enable, Disable, Start, Stop, Take, Bring, Remove, Suspend,
  Resume, Failover, Force, Cycle, Join) and carries no gate. It needs an
  explicit exemption list, and the Agent items must fail it before the fix.

### R7 · gosmo: an empty `[]byte` scripts as `0x00` (LOW–MED, S)

- **Where:** `gosmo/script.go:316-320` (`scriptLiteral`).
- **What:** an empty slice is rendered as `0x00`, with a comment saying "A
  bare 0x is not a valid T-SQL binary literal". It is valid: it is the
  empty binary string. **Verified live:** `DATALENGTH(0x)` = 0,
  `DATALENGTH(0x00)` = 1. So a scripted statement that binds an empty
  varbinary scripts a different value. `scripter_security.go:469` already
  renders the empty case as `0x`, correctly.
- **Fix:** return `"0x"` and correct the comment. Fold the four binary
  renderers into one `binaryLiteral([]byte) string`: `script.go:321`,
  `scripter_security.go:469-471`, `certificate.go:214`, `statistics.go:531`.
- **Test:** `scriptLiteral([]byte{})` == `"0x"`, a round trip through
  `bindScriptArgs`, and the existing certificate and SID pins unchanged.
- **Done (2026-09-11, with R20; gosmo uncommitted).** `binaryLiteral` in
  `helpers.go` replaces `hexLiteral` and the three inline renderers. The live
  probe found a second case: go-mssqldb sends a nil `[]byte` as NULL, so
  `scriptLiteral` now scripts nil as `NULL`, empty as `0x`. Pinned by
  `TestScriptLiteral`, `TestBindScriptArgsScriptsAnEmptyBinaryAsTheEmptyLiteral`
  and the live `TestLiveExecProcScriptBinaryMatchesTheBoundCall`, which
  compares the scripted EXEC with the bound call. All three fail on the old code.

---

## P2 — Consistency and UX

### R8 · Agent writes bypass the progress dialog (S)

**Done 2026-09-11** (uncommitted) — `setAgentEnabled` (now taking the
entity's noun) and `runAgentJobStateAction` both run through
`runWithProgress`. Tests in `agent_progress_test.go`, mutation-checked: all
six actions (Enable/Disable on job, schedule, alert and operator; Start Job;
Stop Job) fail on the old code with no dialog up while the write is in
flight. For Start/Stop, the state read and the `sp_start_job`/`sp_stop_job`
it guards run as one job, so Cancel stops whichever is in flight. A cancelled
toggle reloads the parent folder, because the change may have committed. One
addition: a successful Agent toggle now reports `Job "x" is now disabled`.
Before, it set no status, so a cancelled attempt's message stayed up beside a
tree showing the change done. Live on win10cli with a throwaway job: a Disable
blocked by a lock on its `sysjobs` row showed the dialog, and Cancel left it
enabled on the server. Unblocked Disable/Enable/Start/Stop drew no dialog, and
a second Start was refused ("already running"). The job was dropped afterwards.

- **Where:** `agent_common.go:95` (`setAgentEnabled`),
  `agent_menu.go:221` (`runAgentJobStateAction`).
- **What:** both are writes on a bare `safego` with `serverWriteContext`.
  Every other toggle goes through `toggleEnabledState` → `runWithProgress`,
  and `open-threads` § Progress dialog says the unconfirmed half of a toggle
  runs behind it too. The result is no spinner, no Cancel, and a tree left
  live under an `sp_update_job` that may be waiting on a lock.
- **Fix:** route both through `runWithProgress`, reusing
  `toggleEnabledState`'s run/done shape.
- **Decided (2026-09-11): no confirmation.** Enable, Disable, Start and Stop
  on Agent objects stay unconfirmed, as in SSMS. They only gain the progress
  dialog: the 250 ms reveal delay keeps a fast one invisible, and a slow one
  gets a spinner and Cancel.

### R9 · Agent screens show booleans as `true`/`false` (S)

**Done 2026-09-11** (uncommitted) — all ten sites switched to `boolStr`, plus
one the grep below missed because it formats a method call:
`detail_browser_databases.go:141`, the database Details "Read Only" row
(`d.IsReadOnly()`). A repo-wide grep for `Sprintf("%v", …Is*/Has*/Enabled…)`,
`%t` and `FormatBool` now finds nothing. Live on win10cli: the `test_job`
Details grid shows `Enabled | True`.

- **Where:** `agent_detail.go:76,109,145,188`,
  `agent_job_props_schedules.go:160,194`, `agent_reports.go:90,157,172`,
  `agent_schedule_props.go:139`.
- **What:** each uses `fmt.Sprintf("%v", bool)`. Every other screen uses
  `boolStr` (`True`/`False`).
- **Fix:** switch them to `boolStr`. The grep above is exhaustive for `%v`
  of `Enabled`/`Is*` fields.

### R10 · Version resolution changed under the docs; update check mis-ranks pre-releases (S)

**Done 2026-09-11** (uncommitted, with R24) — `compareVersions` now applies
semver 2.0.0 precedence (`comparePrerelease`); `isReleaseVersion` is kept,
since `(devel)` still appears for `go run` and `-buildvcs=false` builds.
Tests in `update_check_test.go`, five mutations checked. Live: the checkout
build's Check for Updates against v0.0.10 reads "newer than the latest
published release".

- **What:**
  - Since Go 1.24, a build from a checkout stamps a pseudo-version.
    **Verified:** `go build` + `--version` prints
    `gossms v0.0.11-0.20260911113756-cf929d309586`.
  - `internal/version/version.go`'s doc comment (step 3) and `CLAUDE.md`
    § Build & verify still say a checkout build falls back to the literal
    `"(devel)"`.
  - `update_check.go:138` `isReleaseVersion` no longer recognises a dev
    build.
  - `compareVersions` (`:100`) drops `-pre` and `+build` suffixes, so the
    pseudo-version `v0.0.11-0.…` compares **equal** to the release `v0.0.11`
    and reports "up to date".
- **Fix:** implement semver precedence (a pre-release, including a
  pseudo-version, ranks below its release) and correct both docs.
- **Test:** a table covering `v0.0.11-0.2026…` < `v0.0.11`,
  `v0.0.11-rc1` < `v0.0.11`, and `(devel)`.

### R11 · Suspected, verify first: Windows copy may garble non-ASCII text (S once confirmed)

**Decided (2026-09-11): ask the author when this item is reached.** Stop
there, ask for the Windows check below, and change nothing until the result
is in.

- **Where:** `os_clipboard.go:61`, which pipes UTF-8 into `clip`.
- **What:** `clip.exe` reads stdin in the console code page, not UTF-8, so
  "Müller" is expected to come out garbled. Not verified: no Windows desktop
  session was available.
- **Check first:** on a Windows desktop, copy a grid cell containing
  non-ASCII text and paste it into Notepad.
- **Fix if confirmed:** copy through `powershell -NoProfile -Command
  Set-Clipboard` with stdin read as UTF-8, or write UTF-16LE with a BOM to
  `clip`.

**Confirmed 2026-09-11** by the author on Windows: `öööö äää` copied from
the query editor pasted as `├╢├╢├╢├╢ ├ñ├ñ├ñ`, which is UTF-8 read as CP437.

**Done 2026-09-11** (uncommitted) — `os_clipboard.go`: copy sends
`clip` UTF-16LE with a BOM (`utf16LEWithBOM`). Paste had the matching
bug: PowerShell writes redirected stdout in the OEM code page. It now sets
`[Console]::OutputEncoding` to UTF-8 without a BOM before `Get-Clipboard`
(`windowsPasteScript`), and strips a stray BOM. Test:
`TestUTF16LEWithBOMEncodesNonASCII`. **Verified on Windows 2026-09-11** by
the author: `öööö äää` now round-trips.

### R12 · Design caveat: `InstanceKey` drops the port (decide)

- **Where:** `db/peer.go:234`.
- **What:** two instances on one host, reached as `host` and `host,55253`
  (win10cli's SQL2017 is reached this way), share one peer-credential key.
  The later save wins for both, and a peer read tries the wrong login first.
  The fallback retry recovers, but each wrong try is a failed login, which
  counts toward the lockout of a `CHECK_POLICY` login.
- **Decided (2026-09-11): include the port.** `InstanceKey` keeps `,port`
  whenever the address names no instance, so `host` and `host,55253` are two
  keys. A named instance still keys on `host\instance` alone, and the default
  port 1433 is spelled the same as no port. The short-host alias tier
  (`shortHostKey`) keeps the port under the same rule.
- **Test:** `host` and `host,55253` resolve to different saved connections;
  `host,1433` and `host` resolve to the same one; `host\inst,1500` and
  `host\inst` resolve to the same one.

**Done 2026-09-11** (uncommitted) — `InstanceKey` in `db/peer.go`, plus
`db.ConnectionAddress`, which folds the Connect dialog's separate Port field
into the address before it is keyed. Without it a saved `win10cli` / Port
55253 still keyed as `win10cli`. `rememberPeerCredentials` and the
post-connect `forgetPeerFailure` both key through it, and `shortHostKey` keeps
the `,port`. Tests: `TestInstanceKeyNormalizesSpellings`,
`TestConnectionAddressFoldsInTheDialogPort`,
`TestPeerCredentialsKeepTwoInstancesOnOneHostApart`, and new
`shortHostKey` cases. Mutation-checked: dropping the port from the key, or
keying on `conn.Server`, fails them. Trade-off: the catalog reports replica
names with no port, so a *default* instance saved only as `host,1500` no longer
answers a peer read for `HOST`. Peer falls back to the parent connection's
settings, port included. Not driven live, because no topology here has a peer
behind a non-default port.

---

## P3 — Performance

### R13 · The Detail Browser never cancels a fetch it has moved past (M)

**Done 2026-09-11** (uncommitted) — `DetailBrowser.inflight` holds the
fetch's cancel. `ShowNodeDetails` cancels it on every call. `Forget` and
`PurgeConn` cancel it for a node they drop, and `Invalidate` cancels through
`ShowNodeDetails`. Every loader and `backfillRows` now derive their read
contexts from the fetch's context, not `sc.Context()`. `backfillRows` skips
rows that have not started once the fetch is cancelled. `cancelInflight` also
deletes the fetch's `pending` entry, so a cancelled fetch caches nothing, not
even a good answer that lands late. Without that, its part-filled rows or
"context canceled" error would be cached for good. A landed fetch releases
its context (`endFetch`). One deviation: a cancelled fetch's posts still run,
as no-ops on the UI side, because the seq and pending guards already drop
them.

Tests in `detail_browser_cancel_test.go`: switching nodes, a folder's 8-wide
backfill, Invalidate, PurgeConn, Forget, and the backfill skip. Eight
mutations checked, `-race` clean.

**Live A/B on the MI:** an uncommitted `CREATE TABLE` in `GoTest01`, rolled
back afterwards, blocked the Tables folder's `sys.tables` read (`LCK_M_S`).
After pressing End, the pre-fix binary's read was still blocked at 2, 10 and
25 s, holding its pool connection until the 30 s timeout. The new binary's
read was gone within 1.5 s. A Databases folder switched away from and back to
mid-backfill refilled completely.

- **Where:** `detail_browser.go:144` and `:307`, and each
  `detail_browser_*.go` loader.
- **What:** `seq` only discards a superseded fetch's result.
  - Arrowing through N nodes starts N fetches, and the folder ones each fan
    out an 8-wide backfill. All of them run to completion on the shared
    20-connection pool, so the node the user stops on queues behind them.
  - `QueryStorePanel.cancelPlans` exists for exactly this reason.
- **Fix:** the DetailBrowser holds the in-flight fetch's `CancelFunc`.
  `ShowNodeDetails` cancels it whenever the node changes, and so do
  `PurgeConn` and `Invalidate`. A cancelled fetch posts nothing and caches
  nothing.
- **Test:** a scripted driver that blocks. Select A, then B: A's context is
  cancelled, and B's result shows.
- **Live:** on the MI, arrow through a Tables folder of 50+ tables and time
  how long the last selection takes to fill, before and after.

### R14 · The per-database capability probe is not single-flight (S)

**Done 2026-09-11** (uncommitted) — `capabilityFields.dbProbes` maps a
database to its probe in flight (a `chan struct{}`, hand-rolled rather than
`singleflight`: waiters need their own contexts and the gen rule). A caller
that finds one waits on it instead of probing, for no longer than its own
context allows. If the probe's own caller gave up (its context ended), a
waiter that still wants an answer runs a fresh probe rather than inherit a
failure that says nothing about the server. `ClearCapabilityCache` also
drops the in-flight map, so a caller after a Refresh never joins a pre-clear
probe. The probe's cleanup is deferred, so a panic still releases its
waiters. `ServerConn.HasDatabaseCapabilities` lets
`primeDatabaseCapabilities` skip the goroutine for a cached database. That
skip has no unit test, because the only visible difference is a goroutine
not started.

Tests in `capabilities_test.go`: five concurrent callers make one round trip;
a waiter outlives an abandoned probe; a waiter stops at its own context; a
post-clear caller does not join a pre-clear probe; `HasDatabaseCapabilities`.
Five mutations checked, `-race` clean.

**Live A/B on win10cli:** ten rapid selections of the Agent node (which
primes msdb) on a fresh connection. Counting the probe's `IS_ROLEMEMBER`
round trip in `sys.dm_exec_query_stats` gave 4 and 3 probes on the pre-fix
binary, and 1 and 1 on the new one.

- **Where:** `db/capabilities.go:56`, `app_explorer_data.go:149`.
- **What:** `primeDatabaseCapabilities` starts a goroutine on every
  selection. For a database not probed yet, arrowing through its nodes runs
  the same two-round-trip probe concurrently until the first one caches.
- **Fix:**
  - Dedupe in-flight probes per database: a map to a `chan struct{}`, or
    `x/sync/singleflight`, which is already in the module graph as an
    indirect dependency.
  - Skip starting the goroutine when the database is already cached (add
    `ServerConn.HasDatabaseCapabilities`).

---

## P4 — Simplification and refactoring

### R15 · One latest-only fetch helper (M–L; approved 2026-09-11)

"Start a load, cancel the one it replaces, drop stale results" is written by
hand six times, and three of the bugs above are variants of getting it wrong:
R3 (no cancel of replaced loads), R5 (a seq that resets) and R13 (no cancel of
superseded fetches). The six are:

- `explorerNode.beginLoad`/`endLoad`
- `DetailBrowser` `seq` + `pending`
- `QueryStorePanel` `cancel`/`planCancel`/`seriesCancel` + three seqs
- `LogViewer.cancelRead` + seq
- the `PropertySheet` slot seq
- `newObjectDialog`'s `sessionCtx` guard

**Proposal:** a small `latest` type in `internal/tui`:

- `Begin(parent) (ctx, token)` cancels the previous run;
- `Current(token) bool` says whether a result is still wanted;
- `Cancel()` stops the current run.

Adopt it site by site, after R3/R5/R13 have fixed behaviour with tests in
place, so every conversion is a pure refactor under green tests.

### R16 · Panel disposal interface

Delivered by R2; listed so the refactor is visible.

### R17 · A helper for the checkbox-toggle grid (S–M)

- **What:** the same page idiom is hand-rolled at 9 sites: `mapCell` +
  `OnActivateCell` toggle + `redrawGrid` + a block of `propsheet.Static`
  detail rows synced from the selection. The two largest copies are
  `agent_job_props_alerts.go:40-100` and `agent_job_props_schedules.go:150-200`.
- **Fix:** a `checkGrid` helper built on `wireGridEditor` and
  `propsheet.ToggleGridRow`.
- **Scope:** the builders for the two user-mapping pages stay as they are;
  merging those is the settled non-goal in open-threads. Only the grid wiring
  is shared.

### R18 · Remove the duplicate redraw ticker (S)

- **What:** `query_panel_exec.go:290` and `:368` (`tickExecuting`) duplicate
  `App.animateUntil` (`app.go:419`) at a 1-second period. It is also the only
  bare `go` statement in the package.
- **Fix:** replace it with `a.animateUntil("the query elapsed-time timer",
  time.Second, done)`.

### R19 · Remove dead gossms code (S)

**Remove:**

- `query.ExecuteWithPlan` and `query.ExecuteEstimatedPlan`
  (`executor.go:216-226`). Nothing reaches them, tests included. The
  comment at `app.go:77-80` still says the panel "chooses between
  query.Execute and query.ExecuteWithPlan"; the panels run `Session`.
- `sortLogEntriesDesc` (`log_viewer.go:982`). `sortLogRowsDesc` superseded
  it, and only its own test calls it.
- `DetailBrowser.cacheOnly` and the other one-line wrappers that only tests
  call. Point the tests at the `…Objects` forms.

**Keep:**

- the test oracles `startsInBlockComment` and `xmlOpenBlock`;
- `StackedHistoryChart.Draw` (open-threads);
- `db.Connect`, which the live tests use;
- `config.UseTrackedQueries`, which tests in other packages need.

### R20 · gosmo `binaryLiteral` helper

Delivered with R7 (done 2026-09-11).

### R21 · gosmo: split `server.go`, 2155 lines (M; approved 2026-09-11)

- **What:** one file holds ConnectionOptions/DSN/connector/ConnectionString
  (lines 22-970), the core Server, database lifecycle, logins, server roles
  and linked servers. gosmo's own rule is one file per subject area.
- **Proposal:**
  - `connection.go`: options, DSN, connector, `ParseServerAddress`;
  - `server_role.go`: `ServerRole` and its members;
  - `login.go`: `Server.Logins`, `CreateLogin`, `DropLogin`, which join the
    `Login` type already there;
  - `linked_server.go`;
  - `server.go`: the core and database lifecycle.
- **Rules:** no API change. Extract by exact line range and diff the moved
  text byte for byte (the `CLAUDE.md` rule). `README` needs no change, since
  its class diagrams are per type, not per file.

### R22 · gossms re-implements gosmo internals (S; approved 2026-09-11)

- **What:** `query/executor.go` copies `showplanColumnName` and the
  `scanPlanXML` shape from gosmo's `capturePlan`, and `acquireConn` copies
  `readRetryAttempts`/`readRetryDelay`. Each comment says "Mirrors gosmo's …".
- **Proposal:** export them from gosmo as a library feature. For example,
  `gosmo.ShowplanColumn` and a `gosmo.AcquireConn(ctx, db, database)` that
  retries a pinned connection. Then delete the copies.

---

## P5 — Docs and lint

### R23 · `ARCHITECTURE.md` (S)

- The package map omits `progress_job.go`.
- § Dependencies says "All five direct requires". `go.mod` has six: add
  `github.com/pkg/browser`, which `app.go`'s `installEntraSignIn` imports.
- The `app_explorer_data.go` line lists "Back Up Database/…/Rebuild All
  Indexes task consumers". Rebuild All Indexes only opens a query window
  (`explorer_objects.go:469`), and Back Up is launched from
  `app_panel_actions.go`.

### R24 · `CLAUDE.md` and `version.go`

**Done 2026-09-11** (uncommitted), with R10.

The `(devel)` claim; see R10.

### R25 · gosmo `errors.go:25-36` (S)

The `ErrNotFound` convention list:

- says `AgentJobByName`; the function is `JobByName`;
- says `AgentStatus`; the function is `AgentInfo`;
- omits `AsymmetricKeyByName`, which returns `(nil, nil)` as
  `CertificateByName` does. Its own doc says so, but the summary does not.

### R26 · Lint (S)

- `staticcheck` ST1005: `activity_monitor_proctab.go:325` builds an error
  whose message starts with a capital ("Install of … cancelled"). Lower-case
  it, or build the message where it is displayed.
- gosmo SA1019 (`entra.go:438,508`): azidentity has deprecated
  `UsernamePasswordCredential`, which ROPC/`AuthEntraPassword` uses.
  - Keep the method; it is supported and verified on MI.
  - Add `//lint:ignore SA1019` with that reason.
  - Add a line to gosmo `PLAN.md` to track azidentity's removal of it.

### R27 · `QueryStorePanel.Close` doc

The claim becomes true with R2.

---

## Checked and clean — no action

- gosmo T-SQL construction: every identifier, literal and keyword argument
  is quoted, escaped or allowlisted, including `SetDatabaseOption`, scoped
  configuration, recovery model, sequence and partition types, synonym bases
  and audit action names.
- gosmo `query`/`queryRow` + `useBatch`, `withRetry`, `IsRetryable`, the DSN
  builder, `mergeExtraParams`, secret masking, the Browser dialer and reply
  cache, and `withAllMessages`.
- gossms threading: no UI field is written off the UI goroutine;
  `postAndWake` is used throughout; `wakeEventLoop`/`quit` are serialised.
- Password sealing (AES-GCM with AAD), atomic writes, log rotation, and the
  unreadable-config guard.
- The executor's message loop and drain rule, `Session` lifecycle, the arena,
  and `EndTransactions`.
- The activity collector: backoff, counter reset handling, the stop latch.
- `InputField` (AltGr is filtered by tcell itself) and `strutil`
  width/wrap/pad.

---

## Implementation order

Order within each phase is the listed order. Every item follows
`docs/testing.md`: write the test first and see it fail on current code;
mutation-check it; `go test ./...` and `-race` on both repos; drive the built
binary under tmux for every item marked **Live**.

1. **Phase A — correctness:** R1, R2, R5, R3 + R4 (one change), R6, R7 + R20.
2. **Phase B — UX consistency:** R8, R9, R10 + R24, R12. At R11, stop and ask
   for the Windows check.
3. **Phase C — performance:** R13, R14.  -------------- done here ---------------------
4. **Phase D — cleanup and refactoring:** R18, R19, R17, R23, R25, R26, then
   R22 (gosmo exports first, then delete the gossms copies), R21 (gosmo
   file split) and last R15 (site by site, under the tests Phases A and C
   added).

## Decisions (2026-09-11)

| Item | Decision |
|---|---|
| R12 | Include the port in `InstanceKey` for an address with no instance name |
| R8 | No confirmation for Agent Enable/Disable/Start/Stop; progress dialog only |
| R15, R21, R22 | Approved: shared latest-only helper, `server.go` split, gosmo exports replacing the gossms copies |
| R11 | Ask the author during implementation, before changing anything |
