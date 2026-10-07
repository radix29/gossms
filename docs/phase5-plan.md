# Phase 5 plan — Live Query Statistics, Compare Showplan, Replication, Full-Text

Source: `todo/todo.txt` Phase 5 (items 25–27). Plan only — nothing here is
built yet. Written 2026-10-06 against `main` @ `9b08a6b`.

## Where we start

| Item | Already there | Missing |
|---|---|---|
| 25a Live Query Statistics | Actual-plan capture (`gosmo.StartPlanCapture`, `query_panel_plan.go`), `internal/showplan` runtime counters, `planview.PlanView`, `query.Session.SPID()` | Polling `sys.dm_exec_query_profiles` / `sys.dm_exec_query_statistics_xml` while a batch runs; a live overlay in `planview`; the toggle |
| 25b Compare Showplan | `showplan.CompareStatements`/`CompareProperties`, `PlanComparePanel`, `App.openPlanComparePanel` — reached only from Query Store's Compare Plans | SSMS's entry points: compare the plan on screen against a `.sqlplan` file or another open plan; a statement picker for multi-statement plans |
| 26 Replication (read-only) | nothing — no gosmo surface, no OE node | Everything |
| 27 Full-Text | nothing — gosmo only names the permissions; `docs/decisions.md` records "no fulltext catalog node" | Everything |

## Cross-cutting rules (apply to every step)

- **gosmo first.** Every new catalog/DMV read lives in gosmo (`~/go/gosmo`,
  `replace` active — `dev-with-local-gosmo` skill). Follow `~/go/gosmo/CLAUDE.md`
  § Conventions: ctx first, `scanRows`/`readByName`, `%w` + `gosmo: ` prefix, a
  `Ref` handle beside every new family that gets writes, `Database()`/`Server()`
  back-pointers, no query inside `rows.Next()`, writes never via `query`.
- **Version floor is 2016 SP3** (`SQL2016` instance). Every new read gets a
  `version_gate.go` entry and a row in the version-sweep live test.
- **Edition gating** through `edition_gate.go` (`gateAzure`): Azure SQL
  Database, Managed Instance and Linux each differ per item (noted per step).
- **Permission gating** through `internal/tui/gate` per `docs/db-rules.md`
  § Permission gating — fail open, withhold only on a measured "no".
- **Async**: every poll/load is `safego` + `latest` + `postAndWake`
  (`ARCHITECTURE.md` § Async result delivery, § Latest-only loads).
- **Each new `internal/tui` file** gets its `ARCHITECTURE.md` § Package map row
  in the same change (`TestPackageMapListsEveryTUIFile`).
- **Done means driven**: tmux against the built binary + live server, per
  `docs/testing.md`; disposable objects only (`gossms_p5_*` names).
- Docs per step: `README.md` feature list, `help_dialog.go` for any key,
  `docs/decisions.md` for each settled exclusion, `docs/open-threads.md` for
  anything left. Not `CHANGELOG.md`/`RELEASE.md`.

---

## 25a — Live Query Statistics

**How SSMS does it.** With "Include Live Query Statistics" on, the batch runs
with `SET STATISTICS XML ON` (so 2016/2017 get standard profiling; 2019+ also
has lightweight profiling) and a second connection polls the running session.
At completion the live view becomes the actual plan.

**Design.**

- *Two reads, one per poll tick (~1 s, backoff on error):*
  - Plan shape: `sys.dm_exec_query_statistics_xml(@spid)` — the in-flight
    showplan with partial counters (2016 SP1+). Re-read only when the
    statement's `plan_handle`/`statement_start_offset` changes (multi-statement
    batches, multi-batch scripts).
  - Counters: `sys.dm_exec_query_profiles WHERE session_id = @spid`, summed per
    `node_id` across threads (rows, estimate, elapsed/CPU ms, logical reads,
    first/last active time, open/close).
- *Poller connection* is a pooled server connection, **never** the panel's
  `query.Session` (`docs/db-rules.md` § Query execution).
- *Rights*: `VIEW SERVER STATE` (`VIEW SERVER PERFORMANCE STATE` on 2022+;
  `VIEW DATABASE STATE` on Azure SQL Database). No right → the toggle still
  runs the query with the actual plan, and the live tab says why it is empty.
- *Progress*: per node `actual / estimated` (capped 99 % until closed); the
  statement's overall % as SSMS computes it (weighted by estimated rows of
  completed vs open nodes). Rows past the estimate show "n of m (over)".
- *Rendering*: `planview` gets a live mode — `SetLive(*showplan.Plan,
  map[int]showplan.LiveCounters)`. Graph tiles show `rows of est (pct)` and
  elapsed; running/finished/not-started styled distinctly; the Tree tab gains
  the same columns. No animation of edges (terminal), redraw only on new data.
- *Lifecycle*: poller starts when the batch is sent, stops on batch end,
  cancel, panel close or disconnect; on completion the live tab is replaced by
  the Actual Execution Plan tab (same tab position).

## 25b — Compare Showplan (SSMS entry points)

- Plan tab / popped-out `PlanPanel` / opened `.sqlplan` → context menu and
  Query menu **Compare Showplan…** → `dialogs.FileDialog` (local filesystem)
  picks a `.sqlplan`; or **Compare with ▸** lists other open plans.
- `PlanComparePanel` gets a statement picker (toolbar dropdown A-statement ↔
  B-statement) — `CompareStatements` already pairs one statement each; the
  Query Store path keeps its single-statement default.
- Enter on an operator row opens that side's plan in a `PlanPanel` with the
  node selected (`planview.selectNode` exported as `SelectNode`).
- The two-grid design stays (settled in `plan_compare_panel.go`'s header).

## 26 — Replication (read-only)

**Scope.** What SSMS's Object Explorer and Replication Monitor *show*; no
create/alter/drop, no reinitialize, no start/stop agent, no tracer tokens, no
Generate Scripts. Record the exclusion in `docs/decisions.md`.

**Model (gosmo `replication.go`, `replication_monitor.go`).**

- `Server.ReplicationInfo(ctx)` — `sp_get_distributor` result (distributor
  installed, distribution server/db, is-publisher), `msdb..MSdistributiondbs`,
  published/subscribed databases from `sys.databases`
  (`is_published`, `is_merge_published`, `is_subscribed`, `is_distributor`).
- `Database.Publications(ctx)` — `syspublications` + `sysmergepublications`
  (type snapshot/transactional/peer-to-peer/merge, status, retention,
  snapshot options, allow push/pull/anonymous).
- `Publication.Articles(ctx)` — `sysarticles`/`sysschemaarticles`/
  `sysmergearticles` (object, type, destination, filter, schema options as
  decoded flags).
- `Publication.Subscriptions(ctx)` — `syssubscriptions`/`sysmergesubscriptions`
  (subscriber, db, push/pull, status, sync type).
- `Database.LocalSubscriptions(ctx)` — subscriber side:
  `MSreplication_subscriptions`, `MSsubscription_agents`, merge's
  `sysmergesubscriptions`.
- Monitor reads (distributor only): `sp_replmonitorhelppublisher`,
  `sp_replmonitorhelppublication`, `sp_replmonitorhelpsubscription`, agent rows
  from `MS{snapshot,logreader,distribution,merge}_agents` + latest
  `MS*_history`, `MSrepl_errors`. Need `replmonitor` role or sysadmin in the
  distribution DB — gate on it.

**UI.**

- OE: server › **Replication** (after Server Objects, as SSMS) ›
  **Local Publications** › `[db]: pub` (icon by type) and **Local
  Subscriptions** › `[db] - [publisher].[pubdb]: pub`. Hidden on Azure SQL
  Database; "Replication not configured" leaf when nothing is set up;
  "not visible" leaf on a refused read.
- Read-only Properties: Publication (General, Articles, Filter Rows,
  Snapshot, Subscription Options, Subscriptions), Subscription (General,
  Agent, Synchronization). Labels ≤ `propsheet.LabelWidth`.
- Detail Browser grids for the Replication folder (distributor config),
  Local Publications (one row per publication with status), one publication
  (articles + subscriptions).
- **Replication Monitor** panel (Replication folder menu, Tools menu):
  publishers › publications grid (status, worst latency, perf), subscriptions
  grid, agents grid with last action/history, error detail pane; auto-refresh
  via the `activity.Poller` pattern; reuses `panel_toolbar.go`.
- Linux: snapshot + transactional only (2017 CU18+); merge rows simply absent.
  MI: publisher/distributor supported; Azure SQL DB: no folder.

## 27 — Full-Text

Two halves: read-only browsing first (ships alone), writes second.

**Model (gosmo `fulltext.go`, `fulltext_write.go`, scripter entries).**

- `Server.FullTextInfo(ctx)` — `FULLTEXTSERVICEPROPERTY('IsFullTextInstalled')`,
  load OS resources, upgrade option; `sys.fulltext_languages`,
  `sys.fulltext_document_types`.
- `Database.FullTextCatalogs(ctx)` (`sys.fulltext_catalogs` +
  `FULLTEXTCATALOGPROPERTY` item count/size/populate status/merge status),
  `FullTextStoplists` (`sys.fulltext_stoplists`, `sys.fulltext_stopwords`),
  `SearchPropertyLists` (`sys.registered_search_property_lists`,
  `sys.registered_search_properties`).
- `Table.FullTextIndex(ctx)` (`sys.fulltext_indexes`,
  `sys.fulltext_index_columns`, `sys.dm_fts_index_population`,
  `OBJECTPROPERTYEX(…'TableFulltext*')`).
- Writes, each with a `Ref` handle: `CreateFullTextCatalog`/`Alter` (REBUILD
  with accent sensitivity, REORGANIZE, AS DEFAULT)/`Drop`;
  `CreateFullTextStoplist` (FROM SYSTEM / FROM another / empty), ADD/DROP
  stopword, Drop; `CreateSearchPropertyList`, ADD/DROP property, Drop;
  `CreateFullTextIndex` (key index, columns + TYPE COLUMN + LANGUAGE +
  STATISTICAL_SEMANTICS, catalog, filegroup, CHANGE_TRACKING, STOPLIST,
  SEARCH PROPERTY LIST), ALTER (ENABLE/DISABLE, ADD/DROP column, SET
  CHANGE_TRACKING/STOPLIST/SEARCH PROPERTY LIST, START FULL/INCREMENTAL/UPDATE
  POPULATION, STOP/PAUSE/RESUME POPULATION), DROP. Scripter verbs for each.

**UI.**

- OE: database › Storage › **Full Text Catalogs**, **Full Text Stoplists**,
  **Search Property Lists** (folders hidden when FTS isn't installed — one
  "Full-Text Search is not installed" leaf instead).
- Table context menu **Full-Text index ▸**: Define…/Properties, Enable/
  Disable, Start Full/Incremental Population, Stop Population, Track Changes ▸
  (Manual/Automatic/Off), Apply Tracked Changes, Delete — gated on table
  `ALTER` + catalog `REFERENCES`.
- Properties: Catalog (General: default, accent sensitivity, owner; Tables/
  Views; Population: Rebuild/Reorganize/Optimize), Stoplist (words per
  language grid, master-detail with `pending_edits.go`), Search Property List
  (property grid), Full-Text Index (General, Columns; no Schedules page —
  SSMS's schedules are Agent jobs, excluded per Q3).
- New dialogs (`newObjectDialog`): New Full-Text Catalog, New Stoplist,
  New Search Property List, New Full-Text Index.
- Detail Browser: catalogs folder with population status; one catalog's
  tables; one index's columns + population counters.
- Gate: securable classes 23 (catalog), 29 (stoplist), 31/32 (search property
  list) join the per-securable tables in `explorer_object_rights.go`; update
  the "No other class is reachable" bullet in `docs/decisions.md`.
- Editions: Azure SQL Database supports FTS (no semantic search); MI yes;
  Linux needs the `mssql-server-fts` package — read `IsFullTextInstalled`,
  don't assume.

---

## Decisions (questions answered 2026-10-06)

- **Q1 — Replication fixture: win10cli.** Authorized to configure the default
  instance as its own distributor + publisher (`sp_adddistributor`, a
  `distribution` DB, throwaway `gossms_p5_repl_*` publications + a pull
  subscription), torn down afterwards. This server-level change is the one
  exception to "disposable objects only"; nothing pre-existing is touched.
- **Q2 — Full-Text Search is installed on win10cli and win10cli\SQL2017.**
  Live FTS tests run there. SQL2016 and the Linux nodes are not assumed to have
  it: they exercise the "Full-Text Search is not installed" path, and the 2016
  floor is covered by version gates rather than a live FTS run.
- **Q3 — Full-Text writes: the full set.** Catalog, stoplist, search property
  list and index create/alter/drop, population control, Script as. **No**
  population schedules (SSMS's per-index/catalog Agent jobs): record this
  exclusion in `docs/decisions.md`.
- **Q4 — Include Live Query Statistics: menu + toolbar only**, no key
  binding (SSMS has no default one either).

---

## Plan Execution Order

Each step ends green (`gofmt`, `go vet`, `go test ./...` in both repos) and
driven in tmux against the live server; steps marked *(ships)* are a
releasable stopping point.

### 25b — Compare Showplan (smallest, reuses existing code)

- **W1** ✅ *done 2026-10-06* — Export `planview.SelectNode`; add a statement picker to
  `PlanComparePanel` (dropdown per side, default = first pair / Query Store's
  single statement). Unit-test pairing with a two-statement fixture.
  *As built:* `SelectNode(stmt, id int) bool` takes the statement too (NodeIds
  repeat per statement) and reveals collapsed ancestors; the internal
  `selectNode` stays. Pickers are an `A:`/`B:` toolbar row shown only when
  either plan has 2+ statements; keys `[ ]` step A, `{ }` step B (F1 help),
  skipping statements with no plan. Labels are cut to half the row so neither
  picker drops off a narrow pane. Driven live: Query Store Compare Plans
  unchanged (no picker row). The multi-statement picker has no UI entry point
  until W2 — it was checked rendered on a mock screen; drive it live in W2.
- **W2** ✅ *done 2026-10-06* — Entry points: **Compare Showplan…** (file picker → `.sqlplan`) and
  **Compare with ▸** (open plans) on query-panel plan tabs, `PlanPanel`,
  opened `.sqlplan` files, the Query menu; Enter on an operator row opens that
  side's plan at the node. Drive: estimated vs saved actual of the same
  query; two-statement batch vs file. *(ships)*
  *As built:* new `plan_compare_open.go` (+ package-map row). The active plan
  is A, the file or chosen plan B. Query menu has both items, context-gated
  with a Note ("no execution plan" / "no other open plan"); the same pair plus
  Save Execution Plan As... is the right-click menu of a plan's graph canvas or
  tree pane (new `planview.OnContextMenu`, latched per press; the summary grid,
  details pane and XML tab keep their own right-click). Compare with ▸ is
  rebuilt on every menu-bar open via a new generic `MenuBar.OnBeforeOpen` —
  submenus were otherwise frozen at the last `buildMenus`. Enter on an
  operator row opens the side of the column the cursor is on (`… B` columns
  → B), falling back to the side that has the operator; the ops grid's cell
  menu adds Open Plan A/B at Operator as the mouse route. Opened sides are
  titled by source (`Plan B: x.sqlplan`), not by the whole comparison title,
  which nested on a further Compare with. New `planview.SelectedOperator`
  (read side of `SelectNode`). Driven live on win10cli: estimated vs saved
  actual of a two-statement batch (W1's pickers incl. `[ ] { }`), Compare
  with from a right-click and by keyboard through the Query cascade, Enter on
  a B column landing on statement 2's Filter, a 1-statement A vs 2-statement
  file.

### 25a — Live Query Statistics

- **W3** ✅ *done 2026-10-06* — gosmo: `Server.QueryProfiles(ctx, sessionID)` and
  `Server.InFlightPlan(ctx, sessionID)` (+ `version_gate.go`, live test that
  runs a long cross join on a second connection and polls it on win10cli and
  SQL2016; Azure path reads under `VIEW DATABASE STATE`).
  *As built:* gosmo `query_profile.go` (+ `ARCHITECTURE.md` feature-map row).
  `QueryProfiles` returns raw per-thread rows (the node merge is W4's), each
  carrying `PlanHandle` + `StatementStart/End` (from `sys.dm_exec_requests`,
  since 2016's `dm_exec_query_statistics_xml` has no offsets) so the poller can
  tell when `InFlightPlan` is stale; the I/O/object columns are `ISNULL`ed
  (NULL on non-access operators). `*Time` fields are `ms_ticks`, 0 = not yet;
  `CloseTime != 0` means the node finished. Idle/unprofiled session: no rows,
  and `InFlightPlan` → `ErrNotFound`. **No version gate needed** — every column
  read exists on 13 SP3 (the 2019+ page-server and `row_requalification_count`
  columns are left out on purpose; the DMF itself is 2016 SP1, gosmo's floor).
  No right → Msg 300 refusal (`IsPermissionDenied`) on 2016 and 2025, not a
  narrowed read. Live `TestLiveQueryProfiles` green on win10cli (17),
  SQL2017 and SQL2016; both reads added to the version sweep
  (`sweepMustCall`), green on 2016 and 2025. **Not run:** Azure MI (login
  refused with 40532 even from sqlcmd — instance looks stopped) and Azure SQL
  Database (no instance); the Azure `VIEW DATABASE STATE` path is documented,
  not tested — W7 picks up MI.
- **W4** ✅ *done 2026-10-06* — `internal/showplan`: `LiveCounters` type and merge of per-thread
  profile rows into node-keyed counters; progress % (node and statement);
  pure tests incl. over-estimate rows, parallel threads, node not yet open.
  *As built:* new `showplan/live.go`. The package stays DB-free, so the input
  is `showplan.ProfileRow` (the `gosmo.QueryProfile` fields the merge reads;
  W6 copies them across and filters to the shown statement by plan handle +
  offsets — the merge keys on `NodeID` only). `MergeProfiles` →
  `map[int]LiveCounters` (`SetLive`'s argument); `NodeProgress`,
  `StatementProgress`, `LiveCounters.Over()`, `LiveState`
  (not started / running / done). Measured live on win10cli (DOP 4, recorded
  as the test fixture): a parallel operator has a **phantom thread-0 row**
  (all zeros, never opened) besides its workers, so state is judged on the
  threads that opened — done when all of those closed; `estimate_row_count`
  is **split per thread and already covers rebinds** (a spool under nested
  loops estimated 750 × 3000 per worker), so the node estimate is a plain sum.
  Elapsed/CPU are the slowest thread's, like `Runtime`, so nothing jumps when
  the live tab becomes the actual plan. Node % = rows/est capped 99 % until
  closed (0 with no estimate); statement % = Σ rows / Σ max(rows, est), a
  closed node counting its actual rows only, capped 99 % until all closed.
- **W5** ✅ *done 2026-10-06* — `planview` live mode: `SetLive`, tile/tree columns, styling for
  running/done/not-started, statement progress header; `cmd/plandemo` gets a
  replay of recorded profile snapshots for eyeballing.
  *As built:* new `planview/live.go`. `SetLive(p, counters)` — counters keyed
  by NodeID of the statement on screen (W6 filters by plan handle + offsets);
  the same `p` again only swaps counters (tab, selection, scroll kept), a new
  `p` installs fresh but keeps the tab, `nil` shows a waiting note;
  `SetPlan`/`SetPlanXML` leave live mode; `Live()` reports it. Live tiles are
  6 rows (`graphLiveTileH`; `layoutGraph` takes the height): name, object,
  `cost%  elapsed  ⇄`, then `rows of est (pct)` — "over" past the estimate,
  K/M/B-compacted, then pct dropped, to fit 18 columns. Border colour by state
  (running Info, done Success, not started/unreported dimmed, selection still
  wins); an operator the DMV never reports (Compute Scalar) shows "—". A
  progress row under the tab bar: bar, statement %, elapsed (slowest
  operator), running/done/not-started counts. Tree tab: right-hand
  `rows of est (pct)  elapsed` column, dropped below 50 columns; its header's
  CPU/Elapsed show "—" in live mode (the in-flight plan's QueryTimeStats is
  stale). Details/Properties gain a Live block (state, rows, elapsed, CPU,
  logical reads, threads) replacing the in-flight plan's partial Actual
  figures. Not done: the Tree tab's Operator Summary still sorts/shows the
  plan's own figures. `plandemo -live <recording.json>` replays
  `cmd/plandemo/testdata/live_crossjoin.json` — recorded on win10cli (17.0)
  with gosmo's `QueryProfiles`/`InFlightPlan`: DOP-4 cross join of a
  6000-row #temp, 13 snapshots at 500 ms, then the actual plan (Space pauses,
  F5 restarts). Driven in tmux at 140×45 and 70×20 (graph, tree, details,
  live → actual hand-over).
- **W6** ✅ *done 2026-10-06* — Query panel wiring: **Include Live Query Statistics** toggle
  (Query menu + toolbar, no key binding per Q4; turning it on also turns on
  Actual Plan capture);
  poller goroutine (`safego` + `latest` + `postAndWake`) on a pooled
  connection; stop on end/cancel/close/disconnect; live tab → Actual plan tab
  on completion; rights note when the DMV is refused. New file
  `query_panel_live.go` + package-map row.
  *As built:* `App.liveStatsEnabled`; Query menu "Live Query Statistics
  (ON/OFF)" and toolbar `Live[-OFF]` between Act.Plan and Meta. Turning live
  on turns the actual plan on; turning the actual plan off turns live off
  (live rides on its `SET STATISTICS XML ON`). A Results To File run gets no
  live tab (it captures no plan). `startRun` opens a "Live Query Statistics"
  tab (the plan-tab slot, `planTabLabel`) waiting for a plan, and the poller
  (`pollLiveStats`, on `p.conn.Server`'s pool, `QueryProfiles` 250 ms after
  the send then every 1 s, doubling to 8 s on a failed read) re-reads
  `InFlightPlan` only when the first request's plan handle + offsets change,
  drops a plan read that answers for another statement, filters rows to that
  statement and merges them with `showplan.MergeProfiles`. It stops when the
  run's `execDone` closes, and `setRunResult`/`Close`/`execPanicked` abandon
  its `latest` (a late poll can't put the actual plan back into live mode);
  disconnect cancels it through `Server.Context()`. Watching the live tab when
  the run ends lands on the Execution Plan tab unless the run had errors.
  Msg 300 on either read → one note and polling stops: new
  `planview.SetLiveNote`, wrapped in the content area, "not available" on the
  progress row. Driven live on win10cli (17.0): sa, a DOP cross join filling
  in to the hand-over at 140×45; cancel mid-run (no plan, live tab gone, no
  poller session left); a two-statement batch reloading the shape (Nested
  Loops/Hash Match → Top) and finishing with Results 1/2 + the 2-statement
  actual plan; a throwaway login without VIEW SERVER STATE (note shown, query
  ran on). Found, not fixed: the toolbar now overdraws Tools/Help below ~112
  columns (`docs/open-threads.md` B19). README/help left to W7 as planned.
- **W7** ✅ *done 2026-10-06* — Edge cases driven live: multi-statement batch (shape reload),
  multi-batch script with `GO`, cancel mid-run, error mid-run, parallel plan,
  a login without `VIEW SERVER STATE`, SQL2016 floor, Azure MI. README +
  help. *(ships)*
  *As built:* driven in tmux at 160×50 on win10cli (17.0) and
  win10cli\SQL2016 (13.0 SP3): a two-batch `GO` script reloads the shape per
  batch and hands over to a 2-statement actual plan; a `SELECT INTO #t` +
  DOP-4 cross join + `GO` + second query (3-statement actual plan, the DROP
  has none) on both versions; a divide-by-zero mid-statement — the live tab
  moves on to the next statement and the run lands on Messages, the failed
  statement having no actual plan (as SSMS); cancel mid-run on 2016 — live
  tab gone, nothing left in `dm_exec_query_profiles`, and repeated
  cancel/run cycles leave one idle pooled connection, no growth; a throwaway
  2016 login without `VIEW SERVER STATE` — note shown, actual plan arrives.
  Fixed: a tile's "over" was dropped whenever the compact form needed 19+
  columns (`78.8K of 2926`); `liveRowsText` now tries `78.8K of 2926 over`
  before dropping the mark. Seen, not a defect: mid-run, the DMV itself
  reports 0 rows/0 ms for a parallel Nested Loops whose scans and spool are
  counting (13 and 17 alike), so its tile reads `0 of 4000`. Help: a Query
  menu entry in F1's Execution Plan section; README § Required rights and the
  wiki's Execution Plans gain Live Query Statistics. **Not run: Azure MI** —
  t-qmi-01 refused the login (40532) again; `docs/open-threads.md` V6. Found,
  not fixed: F1 help clips lines over 57 columns (B20).
- **W8** ✅ *done 2026-10-06* *(optional)* — Activity Monitor: **Show Live Execution Plan** on a
  running request row (read-only view of another session; no actual-plan
  capture possible, so it relies on lightweight profiling — 2019+ only, or a
  session already under profiling; gate on version and say why when absent).
  *As built:* new `live_plan_panel.go` (+ package-map row): `LivePlanPanel`,
  W6's `pollLiveStats` pointed at another session on the Activity Monitor
  connection's pool (outlives the Activity Monitor, stops on close or
  disconnect; a second open of the same session brings the panel forward).
  The Sessions and Block grids' cell menu gains the item for the selected
  row's session (`session_id`, or the number ending `blktree`), disabled
  with a note on a row naming none. Not gated on version — whether a query
  is profiled is only known by asking — so the panel says why it is empty:
  on 2019+/Azure "not running a query, or LIGHTWEIGHT_QUERY_PROFILING off";
  before 2019, what profiles one (actual plan, TF 7412, a
  `query_thread_profile` XE session). It follows the session from statement
  to statement and, the query over, keeps its last reading, the title bar
  saying so. The poller now reports idle reads (the query panel ignores
  them). Found driving it, fixed for both live views: **lightweight
  profiling reports rows only** — every time column 0, open/close included —
  so W4's merge called every operator "not started"; a thread with rows now
  counts as opened (never closed), `LiveCounters.Timed` is false and times
  draw as "—" in tiles, tree, details and the progress row. And the DMV's
  `estimate_row_count` came back **0 on a re-run of a cached plan** (first
  run had them), so `showplan.FillPlanEstimates` falls back to the plan's
  `EstimateRows × (1 + rebinds + rewinds)`. Driven live at 160×48: win10cli
  (17.0) an unprofiled 2-minute serial cross join from sqlcmd — rows of
  2.1B, 11 running / 5 not started, untimed, then "no query running — last
  reading shown"; win10cli\SQL2016 (13.0 SP3) the same query unprofiled —
  the pre-2019 note — then with `SET STATISTICS XML ON` on the reused
  session id, picked up by the open panel with timed figures; Ctrl+W closes
  it. Not run: Azure MI (`docs/open-threads.md` V6), SQL2017, the Block tab
  live (unit-tested), the refused-DMV note (Activity Monitor itself needs
  `VIEW SERVER STATE`).

### 26 — Replication (read-only)

- **W9** ✅ *done 2026-10-06* — Test fixture on win10cli (Q1): gosmo `testdata` script that sets up a
  local distributor, a transactional and a merge publication on throwaway
  `gossms_p5_repl_*` databases with a pull subscription, and its teardown
  (`sp_removedbreplication`, `sp_dropdistributor`). Run once by hand; live
  tests skip with a message when absent.
  *As built:* gosmo `testdata/replication/setup.sql` + `teardown.sql`
  (sqlcmd, `-b` for setup). Distributor `distribution` on the instance
  itself; `gossms_p5_repl_tran` publishes `gossms_p5_tran_pub` (Customer with
  row filter `Region = N'EU'`, OrderLine, GetCustomers as proc schema only),
  `gossms_p5_repl_merge` publishes `gossms_p5_merge_pub` (Item, subset filter
  `Category = N'A'`), and `gossms_p5_repl_sub` holds a pull subscription to
  each. Setup runs both snapshot agents and both pull agents once (~1.5 min),
  so every agent type has history and the subscriber has rows. Setup refuses
  an instance that already has a distributor; teardown stops the fixture's
  agent jobs first (a running Log Reader made every drop fail Msg 18752),
  drops everything, and drops the distributor only if no other publisher's
  publication remains. Two win10cli traps, worked around in the script: the
  Agent account can't write `ReplData` (setup makes `ReplData\gossms_p5_repl`
  and grants it via `xp_cmdshell`/`icacls`; teardown deletes it), and
  `@sync_method = 'concurrent'` runs SQLCLR that fails "LCID 8192 is not
  supported" (the Agent account's custom locale) — the publication uses
  `native`. Go side: `live_replication_fixture_test.go` —
  `liveReplicationFixture` (skips with the setup command) and
  `TestLiveReplicationFixture` checking the shape; passes on win10cli, skips
  on SQL2016. Setup/teardown each cycled live three times, teardown leaving
  no database, job, linked server, `distributor_admin` or folder behind.
  **Left set up on win10cli** for W10–W13. For W10: `sys.databases.is_subscribed`
  stays 0 for the pull-subscribed database, so local subscriptions must be
  found from `MSreplication_subscriptions`/`sysmergesubscriptions`, not that
  flag; `sp_get_distributor`'s column count varies by version (the scripts
  test `sys.servers.is_distributor` instead).
- **W10** ✅ *done 2026-10-07* — gosmo `replication.go`: `ReplicationInfo`, `Publications`,
  `Articles`, `Subscriptions`, `LocalSubscriptions` + version gates + live
  tests (win10cli; SQL2016; Linux transactional-only).
  *As built:* `replication.go` (server config, publications, articles,
  `SchemaOption.Options()`) and `replication_subscription.go` (publisher-side
  `Publication.Subscriptions`, subscriber-side `LocalSubscription`). Beyond the
  plan: `Server.LocalPublications`/`LocalSubscriptions` (what W11's folders
  list) and `Database.PublicationByName`. Each read probes `OBJECT_ID` first,
  so a database or instance without the tables reads as empty, never "invalid
  object name"; Azure SQL Database is refused (`ErrUnsupportedVersion`).
  Local subscriptions are found by the subscriber tables a database holds
  (`is_subscribed` is 0 for a pull subscriber); the merge rows a subscriber's
  `sysmergepublications` keeps for its publisher are filtered out of its
  publications, and the publisher's own `sysmergesubscriptions` row out of its
  subscriptions. Server names compare with `UPPER` — the fixture stores
  `WIN10CLI` and `win10cli` for one instance. **No version gate**: every
  column read predates 2016. `ReplicationInfo` has no publisher-side answer for a
  remote distributor, so `IsPublisher` there means "a database here is
  published". `server.go`'s `Databases`/`DatabaseByName` now share
  `databaseSelect`/`scanDatabase`, which the two server-level listings reuse.
  Live: `live_replication_test.go` passes on win10cli against the fixture;
  2016 and ubusql1 pass the not-configured test; both version sweeps (17, 13)
  are 0-failed with the six new reads in `sweepMustCall`. 2016 has no
  replication components, so only its transactional subscriber read met
  rows — `docs/open-threads.md` V7. Class diagram `diagram/25-replication.mmd`.
- **W11** ✅ *done 2026-10-07* — OE: Replication folder, Local Publications/Subscriptions loaders
  and icons (`explorer_replication.go`, `tree_node.go`,
  `tree_node_icons.go`), edition hiding, not-configured/not-visible leaves,
  menus (Properties, Refresh, Launch Replication Monitor).
  *As built:* five node types (`NodeReplication`, `NodeLocalPublications`,
  `NodePublication`, `NodeLocalSubscriptions`, `NodeLocalSubscription`).
  Replication sits after Server Objects, hidden on Azure SQL Database only
  (`replicationHidden`, `edition_gate.go`; MI keeps it, unverified — V7). Both
  folders are always listed — a subscriber-only instance has no distributor.
  Labels are SSMS's: `[db]: pub` and `[db] - [publisher].[pubdb]: pub`. A
  publication's glyph shows its kind (`publicationIcon`: transactional,
  snapshot, peer-to-peer, merge); subscriptions have one glyph. Local
  Publications reads `ReplicationInfo` after the list to explain it: one
  "`[db]: not visible (offline, or no access)`" row per published database the
  login cannot read (`LocalPublications` skips them silently — gosmo gained
  `ReplicationDatabase.Readable` for this), and "Replication is not configured
  — no distributor" when nothing is listed and no distributor is named; a
  failed `ReplicationInfo` costs only those rows. An empty Local
  Subscriptions gets no row: an unreadable subscriber database leaves no trace
  to report. A subscription's `nodeKey` adds `ReplPublisher`/`ReplPublisherDB`
  (one database can subscribe to two publishers' same-named publication).
  Menus: Replication and Local Publications carry Launch Replication Monitor,
  a publication Launch Replication Monitor + Properties, a subscription
  Properties — all **withheld with the note "not yet"** (`replicationNotYet`)
  until W12/W13 wire them; folders are not filterable (SSMS offers no filter
  there). Details pane shows the generic grids until W12. Tests:
  `explorer_replication_test.go` (list + explanation rows on a fake, folder
  placement per edition, labels, key, glyph per kind), mutation-checked.
  Driven at 160×48: win10cli as sa (both publications with their glyphs, both
  subscriptions, menus, Refresh); win10cli as a throwaway login with VIEW
  SERVER STATE only (two not-visible rows, empty Local Subscriptions);
  SQL2016 (the not-configured row). README/wiki untouched — W12 ships the
  browsing.
- **W12** ✅ *done 2026-10-07* — Read-only Properties (`replication_props.go`) and Detail Browser
  grids (`detail_browser_replication.go`); `docs/decisions.md` records the
  read-only exclusion. *(ships — browsing)*
  *As built:* Publication Properties — General (status, retention,
  merge compatibility level), Articles (grid; the selected article's
  description, row filter, pre-creation command and decoded schema options
  follow the selection), Filter Rows (filtered articles only, a note when
  none), Snapshot (format, location, compression, pre/post scripts,
  transactional "keep snapshot available"), Subscription Options (push/pull/
  anonymous/copy, DDL; merge: web sync, partitions, conflicts; transactional:
  independent agent, init from backup, updatable), Subscriptions (the
  publisher's list). Subscription Properties — General, Agent (where it runs,
  job, distributor), Synchronization (last sync; merge: result, summary,
  subscriber type; transactional: update mode). Both menus' Properties are
  live; Launch Replication Monitor stays "not yet" for W13. Finders shared
  with the Details pane: `findPublication`, `findLocalSubscription` (publisher,
  publisher DB and publication, case-insensitive — the fixture stores
  `WIN10CLI` and `win10cli`). Details: Replication folder (distributor,
  distribution databases with retention, publishers, each database's roles),
  Local Publications (one row per publication, plus the tree's not-visible /
  not-configured rows), Local Subscriptions (one row per subscription), a
  publication (articles then subscriptions, a Kind column), a subscription
  (Property/Value). No `objs`: nothing replicated has Delete.
  `docs/decisions.md` § Replication: read-only. Tests:
  `replication_props_page_test.go` (selection-following detail on the second
  article, filter page, subscriptions, the second of two same-named
  subscriptions in another case, the Details arms, the empty-list rows),
  mutation-checked; the two dialogs are in `propPageSets`/`pagesThatOnlyRead`.
  Driven at 160×48: win10cli as sa — Replication/Local Publications/Local
  Subscriptions/both publications/both subscriptions in Details, all six
  Publication pages on both publications, all three Subscription pages on
  both subscriptions; win10cli\SQL2016 — not-configured rows in both tree
  and Details. Not run: a login that can't read a published database (the
  not-visible Details row is unit-tested; W11 drove the tree's), MI, Linux.
  README has no feature list to update.
- **W13** ✅ *done 2026-10-07* — gosmo `replication_monitor.go` (the `sp_replmonitor*` reads, agent
  status/history, errors, `replmonitor` gate) + live tests; then the
  **Replication Monitor** panel (`replication_monitor_panel*.go`, split
  state/draw/input like Query Store) with auto-refresh. Drive with a stopped
  agent and an injected error. *(ships)*
  *As built:* gosmo — `Server.MonitorPublishers` (none on a non-distributor:
  the procedure itself fails there on a missing msdb table, so the reflective
  version sweep needed it to read as empty); on a `*DistributionDatabase`:
  `CanMonitor` (sysadmin, or db_owner/`replmonitor`), `MonitorPublications`/
  `MonitorSubscriptions` (`sp_replmonitorhelppublication`/`…subscription`,
  read by column name; `MonitorWarning.Warnings()`), `Agents` →
  `ReplicationAgent` with its last run, `Sessions(hours, errorsOnly)` /
  `SessionActions(session)`, `Errors(id)`. **Not the `MS*` tables the plan
  named**: `replmonitor` has no SELECT on `MS*_agents`/`_history` (Msg 229,
  seen on 2025) — only the `sp_MSenum_*` (`_s`, `_sd`) and
  `sp_MSget_repl_error` procedures SSMS uses, which check the role
  themselves; their times are `fn_replformatdatetime` text. Merge's `_sd`
  finds a session by its *end* time, the others by start. **W10 bug fixed on
  the way**: `ReplicationInfo` failed outright for any non-sysadmin at a
  distributor (msdb's `MSdistributiondbs`/`MSdistpublishers` are sysadmin's,
  and metadata visibility makes them look absent) — it now skips them and says
  so (`DistributorDetailsHidden`), and `Server.DistributionDatabaseRef(name)`
  is the handle the monitor builds from `ReplicationInfo.Databases`. Live tests
  `live_replication_monitor_test.go` (fixture; a throwaway `replmonitor`
  login; error detail behind a failed session); version sweeps 0-failed on 17,
  14, 13. Diagram `25-replication.mmd`. gossms — four stacked grids:
  publications (status, type, counts, latency, last sync, warnings), the
  selected one's subscriptions with their Distribution/Merge Agent then its
  Snapshot and Log Reader agents (a subscription's status is the monitor's,
  not the agent's last history row), the selected agent's sessions, the
  selected session's actions with an Error details column joining each
  `MSrepl_errors` entry (Show Value for all of it). Toolbar: Refresh (F5),
  Auto refresh (Off/5 s/10 s/30 s/1 min, default 10 s), History (24 h/2 d/
  7 d/All), Failed sessions only. Each pane keeps its row by key across a
  refresh (worst-first order moves rows); a tick is skipped while a read is
  out or a grid's popup is open (a rebuild closed the error popup at the next
  tick — caught driving). One panel per server; Tools › Replication Monitor
  (hidden on Azure SQL Database) and Launch Replication Monitor on
  Replication, Local Publications and a publication (which selects it). Notes
  instead of rows: not configured, remote distributor (named), no right to
  monitor, nothing distributed. Not `activity.Poller`: its VIEW SERVER STATE
  prologue is the wrong gate here. Tests `replication_monitor_panel_test.go`
  (join by agent name across `WIN10CLI`/`win10cli`, the four notes, selection
  kept by key and the opened-for publication, error detail read once per id,
  the tick's three refusals and Close, the note's width), mutation-checked.
  Driven at 160×48 on win10cli: as sa from the folder and from Tools; an
  injected error (subscriber `OrderLine` renamed, a row published, the
  Distribution Agent run → Failed, `Invalid object name`, error 14151 in the
  popup), then fixed and re-run — auto refresh showed the recovery with the
  cursor kept on the failed session; the Log Reader job stopped (shows
  Succeeded, "The process was successfully stopped" — what the server
  records) and restarted; Failed sessions only; History All; a publication
  node's Launch selecting it in the open panel; a throwaway `replmonitor`
  login (everything, error detail included); a login with no distribution
  user (the rights note); win10cli\SQL2016 (not configured). Fixture restored
  (row removed, re-synced). Not run: remote distributor (unit-tested), MI,
  Linux — `docs/open-threads.md` V7.

### 27 — Full-Text

- **W14** ✅ *done 2026-10-07* — gosmo `fulltext.go` reads: `FullTextInfo`, catalogs, stoplists +
  stopwords, search property lists, `Table.FullTextIndex` + population state;
  version gates; live tests on a `gossms_p5_fts` database on win10cli and
  win10cli\SQL2017 (Q2), plus the not-installed path on an instance without
  FTS.
  *As built:* `Server.FullTextInfo` (installed, load OS resources, verify
  signature, `UpgradeOption`, languages, document types);
  `Database.FullTextCatalogs`/`FullTextCatalogByName` with the
  `FULLTEXTCATALOGPROPERTY` counters (index count, items, unique keys, size
  MB, `PopulateStatus`, merge, `LastPopulated` from `PopulateCompletionAge`)
  and `FullTextCatalog.Indexes` (W15's catalog tables);
  `FullTextStoplists`/`…ByName` + `Stopwords`; `SearchPropertyLists`/`…ByName`
  + `Properties`; `Database.FullTextIndexes` and `Table.FullTextIndex`
  (`ErrNotFound` without one, `ErrHandleNotLoaded` on a `TableRef`) — key
  index, catalog, filegroup, change tracking (`AUTO`/`MANUAL`/`OFF`, the DDL
  spelling), `StoplistKind` Off/System/User + name, property list, last
  crawl, the `OBJECTPROPERTYEX` `TableFulltext*` counters, columns (type
  column, language, statistical semantics; fetched in one second query and
  grouped in Go). **Population state is its own read**,
  `FullTextIndex.Populations` (`sys.dm_fts_index_population`): the DMV needs
  VIEW SERVER STATE, and folded in it would fail the index for any other
  login — live-tested with a throwaway login that reads the index and is
  refused only the DMV. No `Ref` handles yet — they come with W16's writes.
  **One gate**: `sys.fulltext_indexes.index_version` (2025's version-2 word
  breakers) — column lists compared on 13/14/17, it is the only difference;
  inventory, golden file `testdata/version_gates/fulltext_indexes.sql`.
  An instance without the component reads empty, not failed (catalog views
  exist regardless). **W16 trap found**: stoplist and search-property-list
  DDL must end in `;` (Msg 10736). Live: `live_fulltext_test.go` passes on
  17 and 14 (scratch `gossms_p5_fts`, dropped after), the not-installed test
  on SQL2016; version sweeps 0-failed on 17, 14, 13 (full-text objects made
  in the sweep's database where installed, six reads in `sweepMustCall`, a
  table without an index answering not-found tolerated); live gate-catalog
  check green on all three. gosmo `ARCHITECTURE.md` § Full-Text Search,
  diagram `26-fulltext.mmd`. Not run: Azure SQL Database, MI, Linux —
  `docs/open-threads.md` V8.
- **W15** ✅ *done 2026-10-07* — OE: three Storage subfolders + not-installed leaf
  (`explorer_fulltext.go`); Detail Browser grids
  (`detail_browser_fulltext.go`); read-only Properties for catalog, stoplist,
  property list, table's full-text index (`fulltext_props.go`). *(ships —
  browsing)*
  *As built:* six node types (`NodeFullTextCatalogs`/`…Catalog`,
  `…Stoplists`/`…Stoplist`, `NodeSearchPropertyLists`/`…List`). Storage
  lists them in SSMS's order — Full Text Catalogs, the two partition
  folders, Full Text Stoplists, Search Property Lists — and on an instance
  where `FullTextInfo.Installed` is false, one "Full-Text Search is not
  installed" row in place of the three (the catalog views exist there and
  would read empty); a failed `FullTextInfo` keeps the folders (fail open).
  Leaves are labelled by name; the three folders filter by Name. Menus:
  Properties on each leaf, and the table menu gains a **Full-Text index ▸**
  cascade holding Properties only (W18 fills it). Properties: Catalog
  (General — owner, default, accent sensitivity, population status, last
  populated, merge, counters; Tables/Views — the catalog's indexes, the
  selected one's columns following the selection), Stoplist (General,
  Stopwords), Search Property List (General, Properties), Full-Text Index
  (General — key index, catalog, filegroup, change tracking, stoplist as
  `<off>`/`<system>`/name, property list, index version on 2025, last crawl,
  the `TableFulltext*` counters, running populations; Columns). The
  populations DMV's refusal costs only that row and names VIEW SERVER STATE;
  a table with no full-text index gets a note on each page, not an "Error …
  Press F5" (caught driving — `findFullTextIndex` answers (nil, nil) for the
  index read's not-found only, a missing table stays an error). Details:
  catalogs with counters and population status, one catalog's indexed
  tables, stoplists, one stoplist's words, property lists, one list's
  properties; folder views apply the folder filter and map rows to objects.
  All eight pages are in `pagesThatOnlyRead` until W19. Tests
  `explorer_fulltext_test.go` (Storage installed / not installed / read
  failed, loader, selection-following columns, refused and running
  populations, `<off>` stoplist, the no-index note, stopwords Details,
  filtered catalogs Details, menus), mutation-checked. Driven at 160×48:
  win10cli (17.0) as sa on a scratch `gossms_p5_fts` (two catalogs, a
  stoplist from the system one plus `hereby`, a property list with Title,
  `dbo.Notes` AUTO/user stoplist and `sales.Docs` MANUAL/system stoplist/
  property list/TYPE COLUMN, `dbo.Plain` with none) — tree, every Details
  view, all four dialogs and both cascade cases; win10cli\SQL2016 (13.0,
  not installed) — the not-installed row in tree and Details. Database
  dropped after. Found, not fixed: Arabic stopwords with a shadda misdraw a
  column under tmux (`docs/open-threads.md` B21). Not run: a live running
  population, Azure SQL Database, MI, Linux (V8). README has no feature
  list to update; no key added.
- **W16** ✅ *done 2026-10-07* — gosmo writes with `Ref` handles + scripter verbs (Script as
  CREATE/DROP for catalog, stoplist, property list, index), `WithScript`
  tests, live write tests on disposable objects.
  *As built:* gosmo `fulltext_write.go` — handles `Database.FullTextCatalogRef`/
  `FullTextStoplistRef`/`SearchPropertyListRef` and `Table.FullTextIndexRef()`
  (a table has one; works from a `TableRef`). Catalog: `CreateFullTextCatalog`
  (`AccentSensitive *bool`, `IsDefault`, `Owner`), `Rebuild(ctx,
  accentSensitive *bool)`, `Reorganize` (SSMS's Optimize), `SetDefault`,
  `SetOwner`, `Drop`. Stoplist: `CreateFullTextStoplist` (empty / `FromSystem`
  / `From` + optional `FromDatabase`), `AddStopword`/`DropStopword(ctx, word,
  language)`, `DropLanguageStopwords`, `DropAllStopwords`, `SetOwner`, `Drop`
  — a language is an LCID (`"1033"`, `"0x0409"`, `"0"`) or a name, emitted
  bare or as a literal. Property list: `CreateSearchPropertyList` (empty /
  `From`), `AddProperty(SearchProperty)`, `DropProperty`, `SetOwner`, `Drop`.
  Index: `Table.CreateFullTextIndex` (`[]FullTextIndexColumnSpec` with TYPE
  COLUMN/LANGUAGE/STATISTICAL_SEMANTICS, key index, catalog, filegroup,
  change tracking, `NoPopulation` — refused unless tracking is OFF —
  `StoplistOff`/`Stoplist` with empty meaning SYSTEM, property list);
  `Enable`/`Disable`, `AddColumn`/`DropColumn(…, noPopulation)`,
  `SetChangeTracking`, `SetStoplist(kind, name)`, `SetSearchPropertyList`
  (`""` = OFF), `StartPopulation(Full|Incremental|Update)` (Update is Apply
  Tracked Changes), `Stop`/`Pause`/`ResumePopulation`, `Drop`. Setters mirror
  through `setIfApplied`; stoplist/property-list statements carry the `;`
  (Msg 10736), nothing else does. `scripter_fulltext.go`:
  `ScriptFullTextCatalog`, `ScriptFullTextStoplist` (CREATE + one ADD per
  word), `ScriptSearchPropertyList` (CREATE + one ADD per property),
  `ScriptFullTextIndex` (+ DISABLE when disabled); under IncludeIfNotExists
  every ADD is guarded too — a duplicate word fails Msg 30033, so an
  unguarded re-run stopped at the first — and the DROPs test the catalog
  views (no family has `DROP … IF EXISTS`). Tests: `fulltext_write_test.go`
  (every statement whole, quote-hostile names, from handles; the refusals
  send nothing; scripted writes don't mirror; the four builders), mutation-
  checked; `live_fulltext_write_test.go` on scratch `gossms_p5_fts_w`/`_w2`:
  create/alter/drop of every family, copies (incl. cross-database), the
  MANUAL-tracked insert applied by an UPDATE population, then each script
  run twice in the second database and compared, then the DROP scripts
  twice — passes on 17 and 14, skips on SQL2016 (not installed); both
  databases dropped. All four families' probe forms (`LANGUAGE N'English'`,
  stoplist DDL inside `IF`, `ON (catalog, FILEGROUP …)`) were checked by hand
  on 17 first. gosmo `ARCHITECTURE.md` § Full-Text Search + Scripter list,
  `CLAUDE.md` Ref list, diagram `26-fulltext.mmd`. Not run: PAUSE/RESUME on a
  running population, Azure SQL Database, MI, Linux (V8). No gossms change.
- **W17** — Gate: securable classes 23/29/31-32 in
  `explorer_object_rights.go`, Delete/Script wiring in
  `explorer_object_ops.go`/`scripting.go`; update `docs/decisions.md`'s
  "no fulltext catalog node" bullet.
- **W18** — Table menu **Full-Text index ▸** (enable/disable, population
  start/stop, track changes, apply tracked changes, delete) with population
  progress posted to the status bar / Background Tasks.
- **W19** — New dialogs: Full-Text Catalog, Stoplist, Search Property List,
  Full-Text Index (`new_fulltext_*_dialog.go`); writable Properties pages
  (catalog rebuild/reorganize, stoplist words master-detail, property list,
  index columns/change tracking/stoplist). Full set per Q3; the
  no-population-schedules exclusion goes into `docs/decisions.md`.
- **W20** — End-to-end drive: create catalog → stoplist → index on a
  disposable table → populate → `CONTAINS` query in a query window → alter →
  drop; Azure SQL Database pass (no semantic); README, help, open-threads.
  *(ships)*
