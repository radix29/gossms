# Review fixes — 2026-11-15

Cross-repo review of gossms, gosmo and tuikit (~240k non-test lines).
Delete this file when the steps are done; settled items go to
`docs/decisions.md`, leftovers to `docs/open-threads.md`.

## Baseline (measured)

- `go vet`, `staticcheck`, `go test -count=1 ./...` clean in all three repos.
- `deadcode -test`: gossms clean; gosmo and tuikit list unreachable exported
  API (e.g. `ScriptSelect/Insert/…`, `ScriptMailAccount`, `QSStatistics`,
  `PosixPathRules.*`, `PropertySheet.SetHeader`). **Unused library surface —
  kept deliberately** (CLAUDE.md), not a finding.
- No TODO/FIXME in gossms/tuikit, one in gosmo; 3 bare `go func` in gossms
  (all justified in comments), 0 in tuikit.
- Breaking changes in gosmo/tuikit are allowed for this plan; none below
  *requires* one except R3.

**Honest scope note:** the tree has been reviewed repeatedly (see
`decisions.md` "do not re-raise"), so there are no confirmed correctness
bugs from this pass. Items are marked **[verified]** (read the code) or
**[candidate]** (a smell found by measurement; confirm before changing).
Nothing here duplicates `docs/open-threads.md`.

## A. Bugs / robustness

- **A1 [verified] `fileutil.WithLock` stale-lock race.**
  `internal/fileutil/lock.go`: `os.Remove(lock)` on a stale lock can delete a
  lock another waiter just created (documented as accepted). Cheap fix that
  removes the window: on stale, `os.Rename(lock, lock+".stale-<pid>")`
  then remove it (rename of an already-replaced file fails or moves the
  stale one only when the mtime still matches — re-`Stat` after rename and
  put it back if fresh), or embed pid+nonce in the lock and compare before
  removal. Also: `defer os.Remove(lock)` removes a lock a stale-breaker may
  now own. Low severity; decide if the "accepted" decision still holds.
- **A2 [verified] `OpenLogFile` swallows chmod/rotate errors**
  (`config/config.go:518-533`). Documented best-effort, but a log left 0644
  that fails to narrow is silent. Log once to the file itself or return a
  warning. Trivial.
- **A3 [candidate] `releaseAfterRestore` / `restoreAccessMode` errors
  discarded** (`gosmo/restore.go:137`, `database.go:448,504`,
  `database_attach.go:84`). The primary error is rightly returned, but a
  failed repair leaves a database RESTRICTED/SINGLE_USER with no trace.
  Wrap with `errors.Join(err, repairErr)` so the caller (and CLI exit
  message) can tell the user the access mode is still changed. Needs a live
  check on 13 before merging (`TestLiveVersionSweep` convention).
- **A4 [candidate] Unquoted interpolation in scripters.**
  `scripter_table.go:515` (`LockEscalation`), `database_scoped_config.go:117`
  (`value` guarded by `isSimpleSetValue`, OK). ~196 `Sprintf` SQL sites in
  total; most use `QuoteName`/`QuoteLiteral`. Audit the few that take raw
  strings from the catalog (`LockEscalation`, `index_management.go` `with`
  clauses) and make each go through a closed-set `switch` or an `enum →
  string` map. Add a lint test (grep-based, like
  `TestNoPropertySheetLabelIsTruncated`) that fails on `%s` next to a
  user-supplied parameter name not ending in `Quoted`/`Literal`.

## B. Inconsistencies

- **B1 [verified] "poison the pooled connection" is written 6 times**:
  `gosmo/bulkcopy.go:191`, `server.go:422`, `effective_permission.go:205`,
  `database_conn.go:131`, `gossms/internal/query/session.go:223`
  (`discardConn`). Add `gosmo` exported `DiscardConn(*sql.Conn)` (or
  unexported + `Server.Discard`) with the one explanatory comment; make
  gossms call it. Removes five copies of the same ErrBadConn comment.
- **B2 [candidate] Error wrapping prefixes.** gosmo uses `gosmo: <verb>
  %q: %w`; verify gossms `internal/db` wrappers don't re-prefix
  (`"gosmo: …: gosmo: …"` in messages). Grep for `"%w"` chains and add a
  test that a representative error contains the prefix once.
- **B3 [candidate] Two "restore the setting" helpers**
  (`restoreWindow` in audit/extended_events_script, `restoreAccessMode`,
  `restoreAccessModeAfterRename`): same defer-and-ignore shape. Fold into
  one `restorer` helper after A3 settles the error policy.

## C. Simplifications / refactors

Long functions (non-test lines), ranked. Each is a split-by-section, no
behaviour change; keep the "why" comments (CLAUDE.md).

| Lines | Function | Suggested split |
|---|---|---|
| 418 | `tui.buildSecurablesMatrix` (`securables_matrix.go`) | model build / column layout / key+mouse handlers |
| 364 | `pageDatabaseFiles` | grid columns, add/remove row actions, validation |
| 362 | `pageRGGroups`, 312 `pageMailProfiles`, 269 `pageMailAccounts`, 204 `rgIntPage` | share a "list-editor page" builder in tuikit `propsheet` (add / remove / reorder / detail form) — same shape in all four |
| 347 | `gosmo.ScriptDatabaseObjectList` | one function per object kind, table-driven dispatch |
| 307 | `tuikit editor.applyKey` | key-table (`map[key]action`) + per-group handlers; also makes bindings introspectable for the F1 help |
| 201 | `tui.App.handleKey` | global-shortcut table vs focus routing |
| 173/164 | `gosmo.prefetchTables`, `buildDSN` | stage functions |

- **C1** list-editor page builder in tuikit (largest dedupe: ~1,200 lines
  across four property sheets).
- **C2** `applyKey` table. Breaking for any external tuikit user of
  internal key constants — acceptable.
- **C3** the remaining rows, opportunistically when a change lands there
  (the >900-line rule in CLAUDE.md already says this).
- **C4 [candidate]** `dialog-per-purpose` files over 800 lines
  (`new_endpoint_dialog.go` 828, `fulltext_props.go` 827,
  `query_store_reports.go` 824, `backup_dialog.go` 822,
  `activity_monitor.go` 864, `app.go` 864): split along existing section
  comments per CLAUDE.md procedure (byte-for-byte diff; add the
  `ARCHITECTURE.md` package-map row in the same change).

## D. Optimizations

Measure first; do not change without a benchmark.

- **D1 [candidate]** `gosmo/scripter_table_prefetch.go` `prefetchTables`:
  confirm it issues one batched query set per call, not per table
  (benchmark scripting 500 tables against `semanticsdb`).
- **D2 [candidate]** `WithLock` polls every 5 ms for up to 2 s. Fine for
  rare saves; no change unless profiling a tracked-queries save storm.
- **D3 [candidate]** Result-grid/`DisplayWidth` hot path: add a
  `BenchmarkDrawGrid` in tuikit with 100k rows × 30 cols wide-char data;
  only optimize if a frame exceeds ~5 ms.

## E. Architecture

- **E1 [verified] tuikit has no tag** (open-threads: release blocker).
  Do first among release work: tag tuikit, bump `require`, drop `replace`.
  The user commits/tags — plan only.
- **E2** Package-level mutable `var`s: 329 in gossms, 89 in gosmo, 11 in
  tuikit (many are `errors.New` sentinels or test-shortened timings like
  `lockWait`). Classify with a script; convert any that are real mutable
  state (caches, registries) into fields of an owning struct. [candidate]
- **E3** Resolve the two open-threads that are really architecture:
  overlay-first mouse routing (`App.handleMouse` offering the press to an
  `OverlayActive()` pane) and the `InputField.HandleKey` exception. Both
  fix a class of bugs rather than one instance — do E3 mouse fix first;
  needs a tmux drive per `gossms-tui-tmux-testing`.
- **E4** Theme: nine sub-WCAG palette pairs (`knownLowContrast`). Visual
  change; decide per palette in the binary. Low priority.

## Recommended implementation order

1. ~~**B1** — DiscardConn helper (gosmo + gossms; smallest, mechanical).~~
   **DONE 2026-11-15**: `gosmo.DiscardConn` (`retry.go`, beside
   `AcquireConn`; test `TestDiscardConnClosesInsteadOfPooling`,
   mutation-checked; `ARCHITECTURE.md` note). Six copies replaced — gosmo
   `bulkcopy.go`, `effective_permission.go`, `server.go`,
   `database_conn.go`, `live_application_role_test.go`; gossms
   `internal/query` `discardConn` removed (`session.go`, `executor.go`).
2. ~~**A2, A4** — log-chmod warning; SQL-interpolation audit + lint test.~~
   **DONE 2026-11-15**: A2 — `config.OpenLogFile` writes each failed
   rotate/chmod into the log itself; `.1` narrowed only when a regular file
   (test `TestOpenLogFileRecordsAFailedRotation`, mutation-checked; driven in
   the binary). A4 — gosmo `catalog_keyword.go` (`scriptKeyword`,
   `scriptInteger`, `scriptObjectName`; unknown → `<unrecognized …>`
   placeholder) applied to lock escalation, history retention unit, AE
   encryption type, recovery model, XML/spatial index kinds, data
   compression, sequence bounds, synonym base object, queue activation
   procedure, endpoint state/role/auth method/algorithm (now also accepts the
   `AES, RC4` comma desc). Lint `TestNoRawStringFieldIsInterpolatedIntoSQL`
   (exported string field via bare `%s` in SQL; exemptions name their
   validator, stale ones fail) + `TestAnUnrecognizedCatalogValueBecomesAPlaceholder`;
   both mutation-checked. Live scripting/endpoint tests pass on win10cli
   (17.0). gosmo `CLAUDE.md` § Quoting notes the rule. Unexported fields
   (in-package constants) are out of the lint's scope by design.
3. ~~**A3 → B3** — restore-repair error policy, then the shared restorer
   (live test on 13 via `TestLiveVersionSweep`).~~
   **DONE 2026-11-15**: policy — a failed repair is never discarded: the
   write's error first, the repair's joined after it as an exported
   `*gosmo.RepairError` (`withRepair`, `server.go`). B3 — one `repair` helper
   (off cancellation, `repairTimeout` 10 s) replaces `restoreWindow`,
   `windowRestoreTimeout`, `multiUserRepairTimeout`; the three access-mode
   repairs share `repairAccessMode`, whose error reads `database "x" may still
   be SINGLE_USER: setting it back to MULTI_USER failed: …`. Applied at Drop,
   Detach, Rename, Restore, both audit windows, audit Rename, EventSession
   stop window. gossms: a cancelled task whose repair failed now says so
   (`Task.repairNote`: status bar, Tasks dialog, backup/restore progress), and
   the status bar draws with `DrawTextLine` (a joined error's line break was
   dropped, running the two messages together; overflow now ends in `…`).
   Tests `TestAFailedAccessModeRepairIsReportedAfterTheWritesError`,
   `TestAFailedWindowKeepsTheReenableError`, `TestCancelledTaskReadsCancelled`
   case — all mutation-checked. Live on 13.0.6500 and 17: `TestLiveSingleUser*`,
   `TestLiveRestoreClose*`, audit live tests, `TestLiveVersionSweep`; a
   throwaway probe on 13 (another session holding the single-user slot) gave
   the real joined message. Gap: the EventSession stop-window join has no unit
   test (no executing fake for xevents; same two lines as the audit windows).
   gosmo `ARCHITECTURE.md` § audits notes the rule.
4. ~~**A1** — lock race (only if the "accepted" decision is reopened).~~
   **DONE 2026-11-15** (decision reopened; `decisions.md` entry rewritten):
   `fileutil.WithLock` writes a pid+random token into the lock; a stale lock
   is broken only under `.lock.break` with staleness re-checked inside it
   (`breakStaleLock`), and release removes the lock only while it holds the
   token (`releaseLockFile`). A stale `.break` (crash mid-break) is removed
   outright. Tests `TestBreakingAStaleLockRechecksUnderTheBreakLock`,
   `TestABreakLockSerializesBreakers`, `TestReleaseLeavesASuccessorsLock`,
   `TestWaitersOnAStaleLockTakeTurns` — mutation-checked (no re-check,
   unconditional release); the stress test also fails on the old `lock.go`.
   `-race` clean. Driven in the binary: Options save over a stale lock
   succeeds and leaves no `.lock`/`.break`; a fresh foreign lock blocks the
   save (logged) and is left in place.
5. ~~**E3** — overlay-first mouse routing, then the InputField exception
   decision (tmux drive).~~
   **Mouse routing DONE 2026-11-15**: tuikit `layout.OverlayPanel`
   (`OverlayActive`) + `PanelManager.OverlayActive`; `PanelManager.HandleMouse`
   hands an overlay-active panel every event wherever it lands, and a press
   outside its columns now closes the tab drop-down too. gossms: the eleven
   overlay-holding panels implement it (compile-time list in
   `panel_overlay_routing_test.go`); `App.handleMouse` offers `a.panels` first
   after the menu bar, and `overlaySnapshot` includes it. Tests
   `TestAPressOutsideReachesTheActivePanelsOverlay`,
   `TestAPressOutsideClosesTheTabDropDown`,
   `TestAPressInTheExplorerClosesTheDetailsCellMenu` — all mutation-checked.
   Driven on win10cli: right-click on a tree node closes the Details cell menu
   and opens the tree's; a left click closes it and stops (as every menu).
   Behaviour change: an open Show Value popup now swallows clicks on Object
   Explorer/menu row too (it already swallowed every click on the panel)
   until Esc/Close — kept (user, `decisions.md`). InputField exception: kept
   (user, 2026-11-15) — `decisions.md` § By design; open thread removed.
6. ~~**C1** — tuikit list-editor page builder; convert the four property
   sheets one per commit.~~
   **DONE 2026-11-15**: tuikit `propsheet.ListEditor[E]` (`listeditor.go`):
   grid + selection-follows-detail (commit before the cursor moves, view
   kept), name field, Add/Remove with the standard hints, Revert that forgets
   the selection *before* the caller's items revert, `Changed` hook for
   cross-page publishing, `NewForm` wiring the commit hook. Items and pending
   state stay in gossms (`pendingEdits`, collation-aware checks — tuikit
   cannot import gosmo). Converted `rgIntPage` (pools + external pools),
   `pageRGGroups`, `pageMailAccounts`, `pageMailProfiles`: −195 net lines in
   gossms (+249 library). The ~1,200-line estimate was the four functions'
   whole length; most of it is page-specific detail rows and apply, which
   stay. Tests `TestListEditor*` (7), mutation-checked (commit on revert;
   `SetData` redraw). All gossms tests pass unchanged. Driven on win10cli:
   pool add (empty/duplicate `DEFAULT` hints), offered at once on Workload
   Groups, group added in it, MAXDOP committed by the form hook → Script
   Changes `CREATE RESOURCE POOL` / `CREATE WORKLOAD GROUP … WITH (MAX_DOP =
   4) USING [c1pool]`; Mail account added and published to Profiles, profile
   added, account linked, live rename redraws → script add account / add
   profile / add profileaccount. Nothing applied. Behaviour change: the pool
   pages now publish on selection moves too (no-op unless names changed). Not
   split per commit — the user commits.
7. ~~**C2** — editor `applyKey` table.~~
   **DONE 2026-11-15**: tuikit `controls/editor/editor_input.go` —
   `editorKeys` (`map[tcell.Key]keyBinding`: handler + `keyTrait` bits
   arrow/motion/read-only/goal-column/not-typed) replaces the 240-line
   switch; a key absent from it goes to `keyText` (typing, Ctrl+/,
   Ctrl+Space). Handlers return a `keyOutcome` (acted / kept selection /
   fired / unbound / declined) that the shared prologue/epilogue in
   `applyKey` (now ~50 lines) acts on; `readOnlySafeKey` reads the table.
   Backspace/Delete share `keyErase`, Up/Down `keyVertical`. No behaviour
   change. 13 mutants of traits and outcomes run; 7 survived the existing
   suite, so `editor_keys_test.go` adds `TestEditorPgDnKeepsTheGoalColumn`,
   `TestEditorRawDelDoesNotOpenCompletion` (0x7f keeps `Str`, so it would
   read as typed), `TestEditorShiftHomeExtendsTheSelection`,
   `TestEditorKeysThatKeepTheSelection` — all 13 now killed. Driven in the
   binary: auto-indent Enter, Ctrl+A/Tab/Backtab, Ctrl+D/L/Z, Ctrl+/,
   Shift+Down/End + Backspace. Not done: exporting the table for the F1
   help — its rows are keys, not modifier variants with descriptions, so it
   can't generate help text without a second per-variant table; left until
   someone wants it.
8. ~~**C3/C4** — remaining long functions and file splits, opportunistic.~~
   **DONE 2026-11-15**: C4 — the six files split by exact line range along
   their sections, each verified line-for-line against the original:
   `app_focus.go` (from `app.go`), `activity_monitor_toolbar.go`,
   `new_endpoint_configure.go`, `fulltext_stoplist_props.go` +
   `search_property_list_props.go` (from `fulltext_props.go`),
   `query_store_report_loaders.go`, `backup_dialog_actions.go`; package-map
   rows added, `readTimeoutSites` re-counted for the moved backup probe. C3 —
   `buildSecurablesMatrix` → `securablesMatrix` struct, one method per
   closure (select/load columns/candidates/add/search/dirty/revert/apply);
   `pageDatabaseFiles` → `databaseFilesPage` likewise; `App.handleKey` →
   routing plus `handleGlobalKey` (the shortcut switch); gosmo
   `ScriptDatabaseObjectList` → `objectListBuild`, one method per documented
   stage; `prefetchTables` → `tablePrefetchQuery` + `resultReaders`;
   `buildDSN` → `setEntraDSNValues` + `setClassicDSNAuth`. No behaviour
   change. Mutation runs found coverage gaps the closures already had; new
   tests `TestSecurablesFilterMatchingNothingEmptiesThePermissions`,
   `TestSecurablesShowAViewsExistingColumnGrant`,
   `TestFilesPageKeepsAnExistingFilesTypeAndPath`,
   `TestFilesPageGreysSizeForASelectedFilestreamFile` kill them. Still
   surviving (not new, left): `ScriptDatabaseObjectList`'s `late` flag on a
   non-inline function and the bare schemas' `IncludeIfNotExists` (no
   offline test reaches the function; the live ones don't pin those two), and
   Add's `selected = -1` reset on the Securables page (equivalent in every
   path found). gosmo live: `TestLiveScriptDatabaseObjects*`,
   `TestLiveRoundTrip*` and the `-short` suite on win10cli (17.0). Driven in
   the binary on win10cli: Files page rename → Script Changes `MODIFY FILE
   (… NEWNAME …)`; user Securables search/Add/cycle/Load Columns → `GRANT
   SELECT ON [dbo].[Patients]`; F1, Ctrl+N, Ctrl+Shift+Left/Right, Alt+Z.
   Nothing applied.
9. ~~**D1–D3** — benchmarks first; act only on evidence.~~
   **DONE 2026-11-16**: D1 — a live probe
   (`gosmo/live_script_scaling_test.go`,
   `TestLiveScriptingManyTablesReadsPerCallNotPerTable`: Extended Events
   `event_counter` on the scripting pool's app name, 50 then 500 tables, each
   with key/default/check/index/FK and four extended properties) found the
   prefetch was *not* per call: `ScriptDatabaseObjectList` read extended
   properties once per table and again per foreign key (~1 query per scripted
   object; 238 → 2,038 events). Fixed: `prefetchTables` reads every table's
   properties as one more result set (`tableExtendedPropertiesSelect`,
   driven from `sys.extended_properties` so its cost follows the property
   count; same body as `objectExtendedProperties` via `objectExtPropBody`), and
   `scriptExtendedProperties` uses it for a covered table. Now 20 queries at
   both 50 and 500 tables (40 events with `sp_reset_connection`); the test
   also checks a sample of tables against `ScriptTable` alone. Mutation-checked
   (lookup disabled → per-table growth; constraints off → FK property lost).
   Live on 17 and on 2016 (13): the new test, `TestLiveScriptDatabaseObjects*`,
   the extended-property and `TestLiveRoundTrip*` tests. gossms: the CLI
   fake's rule matched the old `CROSS  JOIN (` — now `AS me (sch, l1type,
   l1name)` (`internal/cli/script_server_test.go`). Driven: `gossms script db
   --objects --script-file` on a throwaway database (table, column, index,
   FK, check, trigger and an `int` property) replayed into an empty one gave
   identical `sys.extended_properties`. Remaining cost (~9 s warm for 500
   tables) is server-side compile of the batch, not round trips or client CPU
   — recorded in `~/go/gosmo/OPEN-THREADS.md` § the prefetch batch is slow to
   compile, not acted on. D2 — `internal/fileutil/lock_bench_test.go`:
   uncontended `WithLock` 0.13 ms, four concurrent savers ~0.35–0.4 ms per
   save; no change. D3 — tuikit `BenchmarkFrameDataGridWideChars`
   (`controls/frame_bench_test.go`: 100k × 30 CJK/emoji/combining cells,
   scrolled to the middle both ways, guarded): 1.4–1.9 ms per frame, under
   the 5 ms bar; `SetData` of the same is 6.5 ms once. No change.
10. ~~**E2, E4** — classify globals; theme recolour.~~
    **DONE 2026-11-16**: E2 — a go/types pass (package-level vars, writes
    outside `init` and `_test.go`, cross-module) over all three repos: ~800
    vars, all sentinels/regexps/tables/test seams except the few written at
    run time, each already documented as deliberately process-wide
    (`config.Tracked`, the Entra cache/prompt, `savesInFlight`, the clipboard
    lookup, gosmo `browserReplies` and `version`, tuikit `theme.active` and
    `editor.defaultIndentWidth`). No conversion; `decisions.md` § Code and
    dependency hygiene records it. E4 — all nine pairs recoloured in tuikit
    `theme/palette.go`: Default DialogAccent 86,156,214 (keyword blue),
    DialogError 244,135,113; Light DialogAccent 0,106,177, Menu/DialogTextDim
    105; mc skins MenuTextDim Black (mc's `menuinactive`), new
    `mcColors.DialogDim`/`DialogRed` (Tango unchanged; VGA 60,60,60 /
    128,0,0). `knownLowContrast` and its "now passes" branch removed, so
    `TestEveryStyleMeetsTheContrastFloor` has no exemptions; open-threads §
    Theme removed, `decisions.md` entry added. Driven in the binary in all
    four themes: menu shortcut suffix, Options radio accent, file-dialog
    listing error (dim), and the prompt's "Enter a value." (error, Dark and
    mc - retro, on win10cli; nothing applied).
11. **E1** — tag tuikit and gosmo, drop `replace`, then release (user's
    action).

Per step: build/test inside the library first, then gossms
(`go test ./...`, `gofmt`, `vet`, `staticcheck`, `deadcode -test`), then
drive the binary (docs/testing.md). Do not commit; the user does.

## Next

Plan only, as asked. Before implementing, switch models with `/model`
(see memory: model-switch-after-planning).
