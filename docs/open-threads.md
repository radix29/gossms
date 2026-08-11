# Open threads

Work that was found, decided, or deferred but not finished. Aggregated
2026-07-30 from notes scattered across 22 session records (now in
`docs/journal.md`); re-verified against the code and pruned 2026-08-06.

Keep this file current: close an item by deleting it, and add one whenever
something is knowingly left undone. An open item recorded only in a session
note is invisible by the next session.

**This file holds only open work.** Fixed items do not accumulate here —
record a fix in `docs/journal.md` instead. The "do not re-raise" sections
below are the deliberate exception: they are not history, they are what stops
a settled question being reopened.

## By design — not issues, do not re-raise

- **Which databases a dropdown offers is settled, and lives in
  `internal/tui/database_list.go`.** The rule turns on when the name is
  resolved: a name stored now and used later (job step, alert, login default
  database, restore history) lists every database including system and
  non-ONLINE ones, because it is opened when the job runs, not now; a name
  acted on immediately lists only what the action will accept. Backup is the
  only dialog in the second class, and both its exclusions are hard server
  restrictions verified on win10cli 2026-08-09 — `BACKUP DATABASE tempdb` and
  a backup of an OFFLINE database each fail with "BACKUP DATABASE is
  terminating abnormally". Do not "unify" the two lists; they are different on
  purpose.
  The trap that comes with the filter, since it will be rediscovered: the
  Backup dialog is opened *on* a database from the Object Explorer, and its
  dropdown is swapped asynchronously afterwards. `setDatabaseItems` keeps a
  selection the incoming list doesn't contain, at the front. Without that,
  right-clicking an OFFLINE database and choosing Back Up silently retargets
  the dialog at whichever database sorts first and backs *that* one up —
  reproduced live, `gossms_p6_offline` became `HealthClinic`. Any future
  narrowing of a dropdown that a dialog can be opened on needs the same
  treatment.

- **The editor's redo stack is deliberately uncapped in bytes, and
  `applyStep`'s slice is deliberately unguarded.** Both were raised 2026-08-09
  and settled the same day; `maxUndoSteps` and `applyStep` in
  `internal/tuikit/controls/editor_undo.go` now carry the reasoning, and
  `TestEditorRedoStackBound` pins it.
  - Redo is bounded in *count* (one entry per undo, cleared on any new edit)
    and not in bytes. `maxUndoBytes` genuinely does not reach it — the inverse
    carries the lines being *replaced*, so on a growing document redo ends up
    above undo: 48.4 MB against 46.5 MB, measured. The undo stack's own byte
    cap is what bounds it, to within one document. A `redoBytes` cap would buy
    that one document back in exchange for silently dropping the deepest redo.
    Not worth it.
  - `applyStep` slicing `[st.row : st.row+st.newLen]` without a bounds check is
    the intended failure mode. The invariant is `pushUndoSpan`'s caller
    promise, and a violated promise means the document is about to be
    corrupted; clamping would turn that into an undo that quietly restores the
    wrong text. The panic is the more useful failure. Do not add a clamp.

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
  — never as a replacement.

- **Restructuring `internal/tui` is closed. No file-split or package-split
  candidates are outstanding.** Raised 2026-07-30, costed and then re-measured
  2026-08-05 against a type-checked cross-file reference graph (549 real symbol
  edges) and rejected on the numbers. What
  shipped instead is `internal/tui/sqlparse`, the only part of the package with
  *zero* outbound references. The earlier "P5" file-split list finished
  2026-08-04 — every file *on that list* came out under 400 lines, split on
  the draw/input seam. It was never a standing rule for the package, and
  isn't one now: 31 non-test files exceed 400 lines, fifteen of them directly
  in `internal/tui` (re-counted 2026-08-09). That is not a reason to re-open
  the question this paragraph closes.
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

- **The Block tab listing the monitoring session itself is intended.**
  `sp_block`'s final `WHERE` drops only sessions that are idle *and* blocking
  nobody, and the session running the procedure is neither, so an unblocked
  server shows exactly one row: its own. Raised by the 2026-08-07 second
  review with `and spr.spid <> @@spid` as the fix (sp_WhoIsActive's
  `@show_own_spid = 0` is the precedent) and **rejected — author's call, on
  purpose**. Do not "fix" it.

- **`sp_block`'s `cross apply sys.dm_exec_sql_text` and its lack of an
  `ecid = 0` filter are intended.** The same review proposed `outer apply`
  plus `spid > 50 and ecid = 0`, on the grounds that a blocker with no cached
  text takes its whole blocked subtree out of the tree and that a parallel
  plan is several `sys.sysprocesses` rows. Neither was reproducible on
  ubudock — a sleeping blocker kept a resolvable handle, and `DBCC
  FREEPROCCACHE` does not evict a live transaction's text — and the
  `cross apply` is what keeps system sessions out today. **Author's call: as
  is.** The one part of that finding that was accepted, the unused
  `dm_exec_cursors` join, is already gone.

- **The Activity Monitor probes `VIEW SERVER STATE` once per collector, so
  twice per panel open.** Hoisting it to a single shared check was proposed
  and dropped: the Retry control added in the same pass starts a *new*
  collector after a transient failure, and a cached permission answer would
  make that retry fail without asking the server. One extra round trip on
  open is the cheaper mistake.

- **`counterQueryFor`'s `RTRIM(instance_name) IN ('', '_Total')` filter drops
  no counter the panels read.** Raised twice on the grounds that
  `RTRIM(NULL)` is `NULL` and `NULL IN (...)` is false, so a NULL
  `instance_name` would be silently dropped. **Verified live 2026-08-07 on
  both servers.** win10cli (Windows) has no NULL `instance_name` rows at all;
  ubudock (Linux) has exactly five, and all five are `SQLPAL:Host Memory` /
  `SQLPAL:Guest Memory` rows that are not in `counterNames` and have no
  gossms consumer. Every one of the 33 names in `counterNames` resolves
  through the filter on both builds, 37 rows each (the extra four are names
  published under both `''` and `'_Total'`). Do not add a
  `OR instance_name IS NULL` arm.

- **`formatValue`'s `case float32` is unreachable but kept.** go-mssqldb
  returns `float64` for both `REAL` and `FLOAT`. It is correct if the driver
  ever narrows, and `formatFloat` already takes the bit size. Noted so it isn't
  "discovered" as live code.

- **Server-scope GRANT/DENY/REVOKE's `USE master;` prefix does not strand the
  pooled connection in master.** The 2026-07-31 review read
  gosmo's `"USE master; " + stmt` (now `permission_options.go:348`, which
  `server_security.go`'s grant/deny/revoke methods route through) as pool
  contamination and
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
  One part of "move-files handling" is **done** (2026-08-08): the MOVE clauses
  and the Files Included panel were both built from backup set 1 regardless of
  which set was selected, which failed every rename-restore from an appended
  `.bak` with "Logical file 'x' is not part of database 'y'". See
  `gosmo.Server.BackupFileListForSetContext` and `docs/journal.md`. The
  remaining parts are untouched.
  The rework must not re-scatter the set number: since 2026-08-09 the restore
  itself, the MOVE clauses and the Files Included panel all take it from
  `backupSetNumber` in `restore_dialog_ops.go`, which carries the rule and the
  live evidence behind it. Two of the three deriving it separately is what
  produced the bug above.

- **SQL Agent needs a complete rework.** The `agent_*` files (14 of them) are
  the largest untouched-by-review area in `internal/tui`. No specific defect is
  recorded — write one down here the next time one is found, rather than
  carrying "needs a rework" as the whole thread.

## Deferred scope (repeatedly, deliberately)

- **Windows / Microsoft Entra (Azure AD) authentication**, in Login Properties,
  New Login, and the External Provider login type generally. gosmo-side work
  needed first. Re-deferred on every properties/dialog pass; this is the
  standing answer to "why isn't this in the UI?".
