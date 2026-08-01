# Open threads

Work that was found, decided, or deferred but not finished. Aggregated
2026-07-30 from notes scattered across 22 session records (now in
`docs/journal.md`), each verified against the current code at that date.

Keep this file current: close an item by deleting it, and add one whenever
something is knowingly left undone. An open item recorded only in a session
note is invisible by the next session.

## Blocking the next release

- **gosmo is untagged past `v0.0.6`, and `go.mod`'s `replace` is active.**
  Intentional during development (see ARCHITECTURE.md, "Developing against a
  local gosmo checkout"), but a CI release build cannot resolve gosmo in this
  state, and `HEAD` calls gosmo code that no tag contains (e.g.
  `gosmo.Scripting`). Before the next gossms release tag: tag and push gosmo,
  bump `require`, comment out the `replace`/`ignore` pair, verify a clean
  build and test run against the tagged module. Raised 2026-07-18 as "P1",
  deprioritized by the user at the time; still outstanding 2026-07-30.

## Known bugs, deliberately unfixed

- **A Grid/Text query result can exhaust memory, by design.** The Max Result
  Rows option and every `maxRows` parameter behind it were removed 2026-08-01
  at the user's request: a result set is retained in full, so `SELECT * FROM`
  a billion-row table will OOM the process. Intended — SSMS parity of "you
  get what you asked for" was preferred to a silent cap. What was done
  instead is to make the retained form as small as it reasonably can be (see
  `internal/query/arena.go`); the floor is the 16-byte string header per cell
  that `ResultSet.Rows [][]string` implies, which can't come down without
  changing that type and every `DataGrid` that consumes it. Results To File
  is unaffected — it never retained rows.

- **Script Changes is broken on any create dialog whose later page depends on
  an earlier page having actually run.** New Schedule reports
  `gosmo: schedule "X" not found`: page 2 (Jobs) calls `AttachSchedule`, which
  resolves by name, but under `gosmo.WithScript` page 1's `CreateSchedule` only
  *collected* a statement, so there is nothing to look up. New Job's Schedules
  page and New Alert's Response page are the same shape and likely affected.
  Apply works correctly — script mode alone is broken. Confirmed identical on a
  pre-refactor binary, so the generic `New*Dialog` base didn't cause it.
  Unfixed because the fix is a design choice: script the dependent statement
  blindly, skip dependent pages, or have gosmo's script mode fake the lookup.
  Found 2026-07-28; still present.

- **A bare `GO` line inside a block comment is treated as a batch separator by
  IntelliSense scoping.** In

  ```sql
  SELECT * FROM dbo.Patients p
  /*
  GO
  */
  WHERE p.
  ```

  `p.` offers nothing: `lastGoBatchStart` matches GO lines textually, so the
  statement is scoped to start after the commented `GO` and the alias on line 1
  falls out of scope. Found 2026-07-30 while optimizing the prefix scan, and
  A/B-confirmed against a pre-change binary as **pre-existing** — the
  differential tests in `completion_prefix_scan_test.go` deliberately pin this
  behavior so the optimization stayed equivalence-preserving. Unfixed because
  making GO detection lexer-aware is a design choice that also touches
  `controls/sql_statement.go`'s `isGoSeparatorLine` (Ctrl+Enter statement
  selection has the same property), and the fix should change both together or
  neither.

- **`InputField` indexes by rune, not display width — the same constraint as
  `Editor` below, and for the same reason.** `widgets.InputField` holds its
  text as `[]rune` and treats each one as a single cell: the cursor, the
  horizontal scroll offset, and the click-to-position math
  (`input_field.go`'s `core.Clamp(f.scroll+(mx-ix-1), 0, len(f.value))`) are
  all rune counts, while `Draw` hands the string to the terminal, which lays
  it out by display width. A CJK or emoji rune in a connection name or a
  filter box puts the cursor one column left of where it renders. Recorded
  2026-07-31 during the two-repo review so it isn't rediscovered as a fresh
  bug independent of the `Editor` item; the two should be decided together,
  since a width-aware `InputField` is the smaller half of the same rework.

- **Editor indexes by rune, not display width, so a double-width character
  smears the rest of its line.** `Editor` treats every rune as exactly one
  screen cell: `scrollCol`, `cursorCol`, the draw loops in `editor_draw.go`,
  `longestLineLen`, and `wrapSegments` all count runes, and `hScrollbar`'s doc
  comment states the choice outright (it is what lets `core.HandleScrollbarDragH`
  drive the bar directly, since track width and visible count are then the same
  number). A CJK or emoji rune passed to `SetContent` occupies two terminal
  cells, so everything after it on that line renders one column left of where
  the editor thinks it is, and the cursor lands in the wrong place.

  This contradicts `internal/tuikit/README.md`'s "`core.DisplayWidth`, never
  `len()`" rule, which the rest of tuikit follows. Tabs — the common case — are
  already handled: `expandTabs` runs on both `SetText` and paste, and
  `indentWidth` spaces are what Tab inserts, so no literal tab reaches the draw
  loop. Left unfixed because making the editor width-aware means reworking
  cursor math, selection columns, horizontal scrolling and the wrap
  segmenter together, and SQL identifiers are overwhelmingly ASCII. Recorded
  2026-07-30 so it isn't rediscovered as a fresh bug; the constraint was only
  in a code comment before.

- **A `/*` inside a `--` line comment or a string literal poisons the syntax
  highlighting of every line after it.** In

  ```sql
  SELECT 4 -- line comment with /* inside it
  SELECT '/* in a string literal */' AS s
  ```

  line 2 renders entirely as a comment. `blockCommentToggleEnd`
  (`controls/sql_highlighter.go`) toggles on every `/*`/`*/` regardless of
  context, so the unmatched `/*` on line 1 leaves the scan "inside a comment"
  forever. The highlighter's own main loop is smarter — it stops at a `--` and
  skips string literals — but only for the line it is currently colouring.
  Pre-existing; found 2026-07-30 by the differential test added with the
  block-comment memo (`TestSQLHighlighterMemoMatchesFullReplayInDrawOrder`),
  which is why the memo deliberately reproduces the *simplified* scan rather
  than the main loop's more accurate one: the replay still runs on the first
  row of every Draw pass, so a memo that disagreed with it would recolour a
  line depending only on whether it was the first visible row. Unfixed because
  the fix is to make the toggle scan lexer-aware, which is the same design
  question as the bare-`GO`-in-a-block-comment item above and should be decided
  with it.

## Carried forward from the 2026-07-30 two-repo review

- **`ExecuteToSink` has no end-to-end unit test.** The rows-to-cells logic is
  covered by `internal/query/stream_test.go` (differentially, against
  `scanResultSet`), but the wiring — `Result.Sets` staying empty,
  `RowsWritten` totalling, the suppressed "Commands completed successfully."
  — runs through sqlexp's `ReturnMessage` protocol, which a fake driver
  cannot reproduce: a fake that ignores the `retmsg` out-param just ends the
  message loop immediately and the test passes without exercising anything.
  Verified live against `ubudock` instead (5-row export, correct CSV, correct
  messages). Anything that touches `runBatch`'s message loop needs the same
  live check.

- **The two lexer implementations in `completion_tokenizer.go` are still
  both maintained.** `flattenLines` and `tokenizeSQLPrefix` are dead in
  production — `flattenLinesInto` and `scanCompletionPrefix` replaced them —
  and survive only as the independent baselines the differential tests in
  `completion_prefix_scan_test.go` compare against. That is two copies of
  T-SQL lexing semantics to keep in sync forever. Deliberate for now, while
  the optimisation settles; the intended sunset is to delete the baselines
  after the next release tag and freeze their assertions as golden token
  streams. Raised 2026-07-30.

- **`internal/tui` is 140+ files with no sub-packages**, while `tuikit` is
  cleanly layered. The `agent_*`, `database_props_*` and `new_*` families are
  the natural seams. Judged not worth doing now — it is the one structural
  thing that keeps getting slowly worse. Raised 2026-07-30.

- **The Object Explorer Details refresh button refreshes the *explorer
  selection*, not the panel's own content** (`App.buildUI` wires
  `detailBrowser.OnRefresh = a.refreshSelected`). Consistent today, since the
  panel only ever shows what the explorer selected; fragile if the panel ever
  drills independently of the tree. Noted 2026-07-30.

## Follow-ons from the 2026-07-30 two-repo review

- **`Table.DropColumn` fails on a column that any index references.** It drops
  the column's default constraint first — SQL Server refuses otherwise, and
  that is what its doc comment says it is for — but an index over the column
  blocks the `ALTER TABLE ... DROP COLUMN` the same way, with "failed because
  one or more objects access this column". Found 2026-07-30 while live-testing
  the script-argument fix against `ubudock`, and confirmed **pre-existing and
  not script-specific**: the real (non-`WithScript`) path fails identically.
  Unfixed because "drop the indexes too" is a policy choice, not an oversight —
  dropping a user's index as a side effect of dropping a column is exactly the
  kind of thing SSMS asks about first. The honest options are to extend the
  prologue to drop dependent indexes, or to detect them and return an error
  naming what blocks the drop instead of passing SQL Server's through.

- **The DataGrid cell viewer could now be syntax-highlighted and isn't.**
  `Editor` wrap mode applies a `Highlighter` as of this review (it silently
  ignored one before — `drawWrapped` never called it), so `DataGrid`'s
  full-cell popup (`datagrid_overlay.go`, the one wrap-mode call site that
  shows query data) could render an XML or JSON cell with `XMLHighlighter`
  instead of as plain text. Not wired up because deciding *which* highlighter
  a cell gets means sniffing its content or reading the column's SQL type
  through to the grid, and neither is currently plumbed. The capability is
  there whenever that's worth doing.

  Check the cost before wiring it up. `styleAt` (`editor_draw.go`) is a
  linear scan of the logical line's runs *per drawn column*, which is fine
  against SQL's few coarse runs but not against a highlighter that returns
  one run per token over a `varchar(max)` XML document — this is the call
  site where that would land, and it compounds with the whole-document scan
  below.

- **`ExecProc` under `WithScript` is scripted but untested against a real
  server.** `scriptExecProc` (`gosmo/procedure.go`) renders the EXEC form,
  picking a DECLARE type per output parameter from the Go pointee type
  (`scriptDeclType`). The mapping is asserted by unit test but has not been
  run against SQL Server, and gossms has no `ExecProc` call site to exercise
  it — a procedure with, say, a `DECIMAL` OUTPUT gets `SQL_VARIANT`, which
  SQL Server may refuse. Worth a live check before anything depends on it.

## Left open by the second 2026-07-30 two-repo review

- **The syntax highlighters' block-comment replay is still O(document) on the
  first row of every Draw pass.** The memo added 2026-07-30
  (`sql_highlighter.go`, `xml_highlighter.go`) makes rows 2..H of a pass O(1),
  but row 1 never hits the fast path and cannot: a pass starts at `scrollRow`
  and the previous pass ended at `scrollRow+H-1`, so `idx == lastIdx+1` is
  false by construction at every pass boundary. That is exactly what makes the
  memo safe across edits — the invariant is load-bearing, not an oversight —
  but it also means `startsInBlockComment` replays the whole document on every
  keystroke (~1.4ms at 10,000 lines, as measured when the memo landed).
  Fixing it needs a cached prefix-state array invalidated by a content-version
  counter, which is the *same* blocker as `buildVisualLines` below: `e.lines`
  is mutated at 26 sites across five files, and one missed bump renders stale
  colours. Do both together behind a single mutation chokepoint or neither.
  Raised 2026-07-31.

- **`Editor.buildVisualLines` still walks the whole document on every Draw.**
  The allocations are gone (it builds into `Editor.vlScratch`/`segScratch`
  rather than growing two slices from nil per call), but the *scan* is still
  O(document), not O(viewport) — every logical line is re-segmented on every
  event the app processes, however little of it is on screen. Draw needs only
  rows `[scrollRow, scrollRow+H)`, and `visualIndexForCursor` only the
  cursor's line. Not memoised because the honest version needs a
  content-version counter, and `e.lines` is mutated at 26 sites across five
  files: one missed bump renders stale text, which is a far worse failure
  than the cost being fixed. The real fix is either a single mutation
  chokepoint or lazy per-viewport segmentation. Raised 2026-07-30.

- **The Databases folder still issues one round trip per database.** Unlike
  the Tables folder — now two aggregate queries for the whole folder, see
  below — the Databases folder can't be collapsed the same way: only
  `TotalMB` comes from a server-wide view (`sys.master_files`), while
  Data/Log/Unallocated/AvailLog all derive from `FILEPROPERTY`, which reports
  on the *current* database only. So the fan-out there is a real constraint,
  not an oversight. Recorded so it isn't re-derived.

- **`formatValue`'s `case float32` is unreachable.** go-mssqldb returns
  `float64` for both `REAL` and `FLOAT`, so only the `float64` arm ever runs.
  Kept rather than deleted: it is correct if the driver ever narrows, and
  `formatFloat` already takes the bit size. Noted so it isn't "discovered" as
  live code.

## Reachable UI with no implementation behind it

- **Activity Monitor has three enabled entry points and is a stub.**
  `showActivityMonitorFor` (`app_panel_actions.go`) shows an
  "Feature not implemented yet. Coming soon!" alert. It's reachable from
  Tools > Activity Monitor (`menu.go`), the toolbar's 📈 button
  (`toolbar.go`, enabled whenever any connection exists), and the Object
  Explorer server node's context menu (`app_explorer_data.go`). README.md
  lists it correctly under "Future Plans", so this isn't a documentation bug
  — it's recorded here only so the enabled-but-empty entry points are
  tracked rather than rediscovered. It does tell the user rather than
  silently no-op'ing, which is why the context-gated-actions rule isn't
  being violated. Noted 2026-07-30.

## Deferred scope (repeatedly, deliberately)

These have been re-deferred on every properties/dialog pass. They are choices,
not oversights — but they are also the standing answer to "why isn't this in
the UI?"

- **Windows / Microsoft Entra (Azure AD) authentication**, in Login Properties,
  New Login, and the External Provider login type generally. gosmo-side work
  needed first.
- **WITH GRANT OPTION** on any permissions grid — gosmo's
  `Grant`/`Deny`/`RevokePermission` have no with-grant parameter.
- **Effective permissions** (the SSMS tab that resolves role membership) —
  nothing in gosmo resolves it or calls `fn_my_permissions`.
- **Filter / search boxes** on the long securables and permissions grids.
- **Column-level permissions** in Database Role / User Properties.
- Assorted mockup pickers and warning modals in the create dialogs (owner
  picker, owner-change warning, and similar).

## Soft / housekeeping

- `query_panel.go` (685 lines) remains a file-split candidate, along with
  `restore_dialog.go` (696), `planview/planview.go` (685) and
  `backup_dialog.go` (660). Raised 2026-07-14 as "P5"; `datagrid.go` from the
  same item **is done** (split into `datagrid_draw/_input/_overlay.go`, now 450
  lines). Not urgent — CLAUDE.md's file-organization convention already names
  these.

## Investigated and found NOT to be a bug (do not re-raise)

- **Server-scope GRANT/DENY/REVOKE's `USE master;` prefix does not strand the
  pooled connection in master.** The 2026-07-31 review read
  `server_security.go`'s `"USE master; GRANT ..."` as a pool-contamination bug
  — `USE` is session state, so on the face of it the connection goes back to
  the pool sitting in master, and gossms shares that pool (a query panel
  opened with no database, as `openQueryWithText(sc, "", ...)` creates, runs
  only `SELECT 1` as its prologue). It proposed replacing the prefix with a
  pinned connection that reads `DB_NAME()`, switches, and switches back.

  **Live A/B on 2026-08-01 disproved it.** Against a connection opened with
  `Database` set, eight pooled connections all still reported that database
  after a GRANT — with the *original* code. The reason is the driver:
  `database/sql` calls `driver.SessionResetter.ResetSession` before handing a
  pooled connection to its next user, and go-mssqldb implements it by flagging
  the next TDS batch as a connection reset (`Conn.ResetSession` ->
  `sendSqlBatch72`'s `resetSession`), which restores the session's database to
  the connection string's. The proposed fix was three extra round trips per
  grant to re-solve what the driver already handles, and was reverted.

  The original doc comment reached the right conclusion for the wrong reason
  ("that next call is a fresh statement/batch that sets its own context if it
  needs to" — an assumption about callers). It now names the actual mechanism,
  so the next review doesn't re-derive the same wrong conclusion.

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
