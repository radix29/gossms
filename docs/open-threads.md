# Open threads

Work that was found, decided, or deferred but not finished. Aggregated
2026-07-30 from notes scattered across 22 session records (now in
`docs/journal.md`); re-verified against the code and pruned 2026-08-06.

Keep this file current: close an item by deleting it, and add one whenever
something is knowingly left undone. An open item recorded only in a session
note is invisible by the next session.

**This file holds only open work.** Fixed items do not accumulate here —
record a fix in `docs/journal.md` instead (the 2026-08-03 archive of six
"fixed / do not re-open" sections is there under
`open-threads-closed-archive-2026-08-03`). The "do not re-raise" sections
below are the deliberate exception: they are not history, they are what stops
a settled question being reopened.

## By design — not issues, do not re-raise

- **gosmo untagged past `v0.0.6` with `go.mod`'s `replace` active is the
  intended development state**, not a release blocker. Tagging gosmo, bumping
  `require`, and commenting out the `replace`/`ignore` pair are steps of the
  release process itself (RELEASE.md). A CI release build not resolving gosmo
  mid-development is the expected consequence.

- **A Grid/Text query result can exhaust memory.** The Max Result Rows option
  and every `maxRows` parameter behind it were removed 2026-08-01: a result
  set is retained in full, so `SELECT * FROM` a billion-row table will OOM the
  process. SSMS parity of "you get what you asked for" was preferred to a
  silent cap. The retained form is already as small as it reasonably goes
  (`internal/query/arena.go`); the floor is the 16-byte string header per cell
  that `ResultSet.Rows [][]string` implies. Results To File never retained
  rows and is unaffected. Do not add a cap back.

- **The `Meta` (Output Column Metadata) block is a grid-only display aid.** It
  reads `Result.Sets`, which `ExecuteToSink` leaves empty by design — an export
  retains no rows, and the column *types* are not known to `RowSink.BeginSet`
  anyway. It also only takes effect on the *next* execution. Both intended; do
  not carry `ColumnTypes` through the sink interface to "fix" the first.
  Decided 2026-08-04.

- **The DataGrid cell-viewer popup is deliberately unhighlighted.** A "Show
  Value" on a cell whose trimmed text is bracketed by `<>`, `{}`, or a
  JSON-shaped `[]` opens its own query panel with
  `XMLHighlighter`/`JSONHighlighter` instead (`internal/tui/cell_value.go`, via
  `DataGrid.OnShowValue`); everything else gets the plain 60-column popup,
  unhighlighted, on purpose. Highlighting inside the popup is the thing not to
  do: wrap mode resolves each drawn column through `styleAt`
  (`editor_draw.go`), a linear scan of the logical line's runs, chosen so a
  `varchar(max)` cell costs work proportional to the ~15 visible rows rather
  than to the value. That scan is fine against SQL's few coarse runs and not
  against a highlighter emitting one run per token over a whole XML document.
  Routing to a panel, which draws unwrapped, is what sidesteps it. Decided
  2026-08-04.

- **The Databases folder's one round trip per database is intended.** Author's
  call 2026-08-05 on a costed proposal. `FILEPROPERTY` reports on the *current*
  database only, so Data/Log/Unallocated/AvailLog cannot come from a
  server-wide view the way the Tables folder's aggregates do. Collapsing it
  into a single dynamic batch does work and is faster (11-12ms vs 20-21ms over
  four databases), but one unreachable database fails the whole batch and every
  row loses its sizes, where the fan-out degrades to `N/A` in that one row
  alone. The fan-out is also already concurrent 8-wide, so it costs
  `ceil(N/8) x RTT`. If a user ever reports the folder being slow, the batch
  goes in as a *fast path* with the fan-out as the fallback on any batch error
  — never as a replacement. Full costing in `docs/proposals-2026-08-05.md` § 2.

- **Restructuring `internal/tui` is closed. No file-split or package-split
  candidates are outstanding.** Raised 2026-07-30, costed in
  `docs/proposals-2026-08-05.md` § 1, re-measured 2026-08-05 against a
  type-checked cross-file reference graph and rejected on the numbers. What
  shipped instead is `internal/tui/sqlparse`, the only part of the package with
  *zero* outbound references. The earlier "P5" file-split list finished
  2026-08-04 — every file is under 400 lines, split on the draw/input seam.
  The negative results, each a proposal a future review will otherwise
  re-invent:

  - The `agent_*`/`database_props_*`/`new_*` name families are not a seam —
    they cut straight through the `App` dependency.
  - Neither is "the lines that never mention `App`". `grep -w App` misses
    `p.app`/`d.app` field access; the real figure is 48% (58 files, 12,618
    lines), not 56%, and nine files are misclassified by it.
  - A `props` package is **not** a five-method interface. The 41 `propPage`
    files have 110 outbound references to 44 symbols; only ~6 are `App`
    services, the rest shared helpers embedded in 146-221-reference hubs that
    cannot move. It needs a fourth package, not one interface.
  - Its stated benefit — "props becomes testable without an `App`" — already
    holds: five `*_test.go` files under `props` have zero `App` references.
  - `planview`, the precedent, has no `Host` interface at all. Both existing
    sub-packages are leaves, and that is why both worked.

- **`formatValue`'s `case float32` is unreachable but kept.** go-mssqldb
  returns `float64` for both `REAL` and `FLOAT`. It is correct if the driver
  ever narrows, and `formatFloat` already takes the bit size. Noted so it isn't
  "discovered" as live code.

- **Server-scope GRANT/DENY/REVOKE's `USE master;` prefix does not strand the
  pooled connection in master.** The 2026-07-31 review read
  `server_security.go`'s `"USE master; GRANT ..."` as pool contamination and
  proposed a pinned connection that reads `DB_NAME()`, switches, and switches
  back. **Live A/B on 2026-08-01 disproved it**: eight pooled connections all
  still reported the right database after a GRANT, with the *original* code.
  `database/sql` calls `driver.SessionResetter.ResetSession` before handing a
  pooled connection to its next user, and go-mssqldb implements it by flagging
  the next TDS batch as a connection reset (`Conn.ResetSession` ->
  `sendSqlBatch72`'s `resetSession`), restoring the session's database to the
  connection string's. The proposed fix was three extra round trips per grant
  to re-solve what the driver already handles, and was reverted.

## Unbuilt features README already promises

Each is a feature, not a defect.

- **Activity Monitor: the Sessions and Block tabs.** The panel, both
  dashboards, and live collection shipped 2026-08-06 (increment 1, see
  `docs/plan-activity-monitor.md`). The Sessions and Block tabs exist and are
  selectable but draw a placeholder and a manual Refresh that says it has
  nothing to load — increment 2 is the real session list
  (`sp_whoisactive`-shaped) and the blocking chains. Neither has opened a
  connection yet; `ActivityMonitor.adopt` is where a tab's own connection
  gets registered for teardown.
- **Reports** — server-level and database-level (top tables, disk usage). No
  entry point exists yet.
- **Always On Availability Groups** — viewing and managing topology and
  health. gosmo-side work needed first; nothing exists on either side.

## Reworks named in README's Known Issues

Both are the author's own assessment; neither has a defect list behind it yet.

- **The Database Restore dialog needs a rework.** `todo/todo.txt` names the
  concrete parts: show the *remote* server's directory rather than the
  client's, move-files handling, error messages, and text trimming. The dialog
  works today; this is a quality pass, not a break.

- **SQL Agent needs a complete rework.** The `agent_*` files (14 of them) are
  the largest untouched-by-review area in `internal/tui`. No specific defect is
  recorded — write one down here the next time one is found, rather than
  carrying "needs a rework" as the whole thread.

## Designed and deferred

- **`Editor` undo is whole-buffer snapshots, not per-edit deltas.**
  `Editor.snapshot` deep-copies every line on every `pushUndo`, which is every
  typed character: measured 3.2 ms and 5 MB per keystroke in a 20,000-line
  script (`BenchmarkPushUndo20k`, throwaway). The 2026-08-06 review's Batch D
  capped the stack by total bytes (`maxUndoBytes`, 64 MB) so the memory cliff
  is gone — the per-keystroke copy is not. The real fix is per-edit deltas: an
  undo step records the range replaced and the text it replaced, so a
  keystroke costs bytes rather than a document. It touches every `pushUndo`
  call site (~15) plus `undo`/`redo`, and wants live tmux verification, so it
  was deliberately not attempted in the same pass as the cap.

## Deferred scope (repeatedly, deliberately)

- **Windows / Microsoft Entra (Azure AD) authentication**, in Login Properties,
  New Login, and the External Provider login type generally. gosmo-side work
  needed first. Re-deferred on every properties/dialog pass; this is the
  standing answer to "why isn't this in the UI?".
