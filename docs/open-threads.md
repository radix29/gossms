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

## Closed since being recorded (verified 2026-07-30, do not re-open)

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
