# Open threads

Work that was found, decided, or deferred but not finished. Aggregated
2026-07-30 from notes scattered across 22 session records (now in
`docs/journal.md`), each verified against the current code at that date.

Keep this file current: close an item by deleting it, and add one whenever
something is knowingly left undone. An open item recorded only in a session
note is invisible by the next session.

**This file holds only open work.** Fixed items do not accumulate here — the
six "fixed / do not re-open" sections that had grown to 28k of a 40k file
were moved to `docs/journal.md` on 2026-08-03 (search it for
`open-threads-closed-archive-2026-08-03`). Record a fix there, not here. The
two "do not re-raise" sections below are the deliberate exception: they are
not history, they are what stops a settled question being reopened.

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

- **The DataGrid cell viewer itself is still unhighlighted; XML and JSON
  cells no longer go through it.** A "Show Value" on a cell whose trimmed text
  is bracketed by `<>`, `{}`, or a JSON-shaped `[]` now opens its own query
  panel with `XMLHighlighter`/`JSONHighlighter` instead
  (`internal/tui/cell_value.go`, wired through `DataGrid.OnShowValue`), which
  covers what the popup would have been highlighting. Everything else still
  gets the plain 60-column popup, and that is deliberate.

  Do not "finish the job" by highlighting inside the popup without measuring
  first. Wrap mode resolves each drawn column through `styleAt`
  (`editor_draw.go`), a linear scan of the logical line's runs, deliberately:
  the alternative is `runStyles`' per-rune map, which for a `varchar(max)`
  cell would be work proportional to the value rather than to the ~15 visible
  rows. The scan is fine against SQL's few coarse runs but not against a
  highlighter returning one run per token over a whole XML document, and the
  popup is the call site where that would land. Routing to a panel — which
  draws unwrapped — is what sidesteps it. Updated 2026-08-04.

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

## Known gaps in shipped features

- **The `Meta` (Output Column Metadata) toggle produces nothing under Results
  To File.** It reads `Result.Sets`, which `ExecuteToSink` leaves empty by
  design — an export retains no rows, so there is no result set to describe.
  The column names are known there (`RowSink.BeginSet` receives them) but not
  their types, so closing this means carrying `ColumnTypes` through the sink
  interface. Not done because the block is a *display* aid for on-screen
  grids and an export has none. Noted 2026-08-04 when the toggle was
  implemented. Same limitation applies to the toggle only taking effect on
  the *next* execution — results already on screen are not re-rendered.

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

- No file-split candidates outstanding. The "P5" list raised 2026-07-14 —
  `datagrid.go`, then `query_panel.go`, `restore_dialog.go`,
  `planview/planview.go`, `backup_dialog.go` — is closed as of 2026-08-04;
  every one is now under 400 lines, split on the same draw/input seam
  (`*_draw.go`, `*_input.go`). Kept as a line rather than deleted so the next
  review doesn't re-raise files that are already done.
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

