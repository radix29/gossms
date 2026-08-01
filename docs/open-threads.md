# Open threads

Work that was found, decided, or deferred but not finished. Aggregated
2026-07-30 from notes scattered across 22 session records (now in
`docs/journal.md`), each verified against the current code at that date.

Keep this file current: close an item by deleting it, and add one whenever
something is knowingly left undone. An open item recorded only in a session
note is invisible by the next session.

## By design — not issues, do not re-raise

- **gosmo being untagged past `v0.0.6` with `go.mod`'s `replace` active is
  the intended development state.** It was listed here as "blocking the next
  release" from 2026-07-18 to 2026-08-01; that was wrong framing. The
  `replace` directive is deliberately live during development (ARCHITECTURE.md,
  "Developing against a local gosmo checkout"), and tagging gosmo, bumping
  `require`, and commenting out the `replace`/`ignore` pair are steps of the
  release process itself (RELEASE.md) — not outstanding work. A CI release
  build not resolving gosmo mid-development is the expected consequence, not a
  defect.

- **A Grid/Text query result can exhaust memory.** Intended behaviour. The Max
  Result Rows option and every `maxRows` parameter behind it were removed
  2026-08-01: a result set is retained in full, so `SELECT * FROM` a
  billion-row table will OOM the process. SSMS parity of "you get what you
  asked for" was preferred to a silent cap. The retained form is already as
  small as it reasonably goes (`internal/query/arena.go`); the floor is the
  16-byte string header per cell that `ResultSet.Rows [][]string` implies.
  Results To File never retained rows and is unaffected. Do not add a cap back.

- **`Table.DropColumn` and `Table.AddColumn` are accepted as non-functional
  for now.** `DropColumn` fails on any column an index references (SQL Server:
  "failed because one or more objects access this column") — it drops the
  column's default constraint first but nothing else. A null/failing
  implementation is fine at this stage; neither is on the path to the next
  release, and neither may be removed from gosmo (CLAUDE.md's library rule).
  Decided 2026-08-01. Detail on what a real fix would have to choose between
  is in the follow-ons section below.

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

- **`Table.DropColumn` fails on a column that any index references.**
  Accepted as non-functional for now — see "By design" at the top of this
  file; kept here for the detail of what a fix would have to decide. It drops
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

  Check the cost before wiring it up. Wrap mode resolves each drawn column
  through `styleAt` (`editor_draw.go`), a linear scan of the logical line's
  runs, deliberately: the alternative is `runStyles`' per-rune map, which for
  a `varchar(max)` cell would be work proportional to the value rather than
  to the ~15 visible rows. The scan is fine against SQL's few coarse runs but
  not against a highlighter returning one run per token over a whole XML
  document, and this is the call site where that would land. It no longer
  compounds with a per-Draw document scan — `buildVisualLines` is memoised as
  of 2026-08-02 and a read-only viewer segments once — so this is now the
  only remaining cost to weigh.

- **`ExecProc` under `WithScript` is scripted but untested against a real
  server.** `scriptExecProc` (`gosmo/procedure.go`) renders the EXEC form,
  picking a DECLARE type per output parameter from the Go pointee type
  (`scriptDeclType`). The mapping is asserted by unit test but has not been
  run against SQL Server, and gossms has no `ExecProc` call site to exercise
  it — a procedure with, say, a `DECIMAL` OUTPUT gets `SQL_VARIANT`, which
  SQL Server may refuse. Worth a live check before anything depends on it.

## Left open by the second 2026-07-30 two-repo review

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
