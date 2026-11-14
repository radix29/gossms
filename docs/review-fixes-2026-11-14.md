# Review fixes — 2026-11-14

Three bugs found reviewing gossms, gosmo and tuikit at gossms `c6c57ab`,
gosmo `ab53ba0`, tuikit `d9d800c`. gosmo came out clean in the areas read
(index/table/FK/check scripting, restore, DSN building and masking, quoting);
all three fixes land in gossms and tuikit. Delete this plan once the items are
done (`TestNoDanglingDocReference` then catches any pointer left to it).

## Recommended implementation order

1. ~~**Bug 3 — red test on HEAD.**~~ **Done.** One-line doc edit; gets `go test ./...` green
   so the other two can be verified against a clean baseline.
2. ~~**Bug 2 — comment injection in the XEvent INSERT export.**~~ **Done.** Small, local,
   security-relevant; no API change.
3. ~~**Bug 1 — stale multi-row selection kept by index.**~~ **Done.** tuikit API change
   (breaking) plus gossms call-site updates; largest, do it last on a green
   tree. Order inside it: tuikit first (build + test there), then gossms.

---

## Bug 1 — a rebuild that changes the rows keeps the old Shift/Ctrl selection by index — DONE

**Status:** fixed — tuikit `SetSourcePreservingView`/`SetDataPreservingView`
collapse the selection; the old keeping behaviour is
`SetSourceKeepingSelection` (tests moved, plus
`TestSetSourcePreservingViewCollapsesSelection`). gossms: `applyBatch`'s
`grew` branch and `catchUpAfterOverlay` use the keeping variant;
`rebuildRows` re-selects the cursor's event (`rebuild(anchor, sameRows)`),
`columnsChanged` goes through `rebuildColumns` (keeping, unless the front
event changed — a column toggle while paused takes in evictions);
`restoreSelection` always ends in `SetSelectedCell`; the sort paths' stale
comments dropped; `docs/ui-rules.md` and tuikit `ARCHITECTURE.md` updated.
Tests: `TestXEventViewerFilterCollapsesABlockOntoTheCursorsEvent`,
`…ResumeAfterEvictionCollapsesABlock`, `…ColumnChangeKeepsABlock` (the first
two fail without the fix). Driven live: View Target Data on win10cli
system_health, Shift-selected 3 rows, F7 `name <> xml_deadlock_report`,
Copy Rows with Headers → header plus one row.

**Where:** tuikit `controls/datagrid.go` (`SetSourcePreservingView`), gossms
`internal/tui/xevent_viewer_rows.go` (`rebuildRows`), and the other
`SetSourcePreservingView` / `SetDataPreservingView` / `redrawGrid` hosts.

**What:** tuikit `d9d800c` made `SetSourcePreservingView` keep a Shift-extended
block (its anchor) and Ctrl+click marks, on the contract that the rebuild
shows "the same rows". Several gossms callers rebuild with a *different* row
set and do not re-select afterwards, so the kept selection now covers other
rows:

- `XEventViewer.rebuildRows` (ungrouped) runs for a filter change
  (`setFilter`), Resume after Pause (`togglePause` — the store may have
  evicted and added events meanwhile), aggregate toggles, Clear and restart.
  It calls `SetSourcePreservingView` and then `followTail(true)`, which does
  nothing while more than one row is selected (`singleRowSelected`), so the
  block/marks survive untouched.
- Grouped: `restoreSelection` collapses only when it finds the anchor; when
  the anchor event is gone (`a.event == 0`, or an event no group holds), it
  returns without a `SetSelectedCell` and the stale block stays.
- `redrawGrid` callers that re-run a query into the same grid (Replication
  Monitor's publication/agent/session/action grids, Query Store) keep a block
  over refreshed rows whenever their identity re-select misses.

**Failure scenario:** Watch Live Data, ungrouped. Shift+Down to select rows
5–9, then Filter → `duration > 1000`. The grid now shows the filtered events;
rows 5–9 of the *filtered* list are still selected. Copy Rows with Headers /
Copy Event Details (`selectedEvents` → `grid.SelectedRows`) copies five events
the user never selected. Same with Ctrl+click marks (kept while every marked
index is below the new row count).

**Fix (tuikit, breaking — allowed):**

1. `SetSourcePreservingView` / `SetDataPreservingView` go back to keeping
   cursor, scroll, column widths and sort indicator only, and collapse the
   selection onto the restored cursor (`collapseSelection`). Safe default:
   a host that does not know whether its rows moved cannot copy the wrong
   ones.
2. Add an explicit opt-in for the one case that wants the old behaviour —
   rows identical, only columns or trailing rows changed:
   `SetSourceKeepingSelection(columns, rows)` (same body as today's
   `SetSourcePreservingView`). Update both doc comments, the "The same
   rows is the contract" paragraph moving to the new method.
3. Move the existing `datagrid_selection_test.go` cases for kept
   block/marks onto the new method; add one asserting the plain
   preserving call collapses a block and drops marks.

**Fix (gossms):**

1. `xevent_viewer_rows.go` `applyBatch` `grew` branch (new column, same rows
   plus appended ones) and `catchUpAfterOverlay`'s ungrouped branch: switch
   to `SetSourceKeepingSelection`. `trimShown` already shifts the selection
   with `DropLeadingRows` before these run.
2. `rebuildRows` (ungrouped): before the rebuild, take the cursor's event ID
   (`selectionAnchor`); after it, re-find that event in `shown` and
   `SetSelectedCell` it, as `setGrouping` already does — falling back to
   `SetSelectedCell` on the clamped old index so the selection always
   collapses. `columnsChanged` alone may use the keeping variant (rows
   unchanged): give `rebuildRows` a `sameRows bool` parameter or split a
   `rebuildColumns` path.
3. `restoreSelection` (grouped): on a miss, still `SetSelectedCell` the
   current cursor so the selection collapses.
4. Grep the remaining `SetSourcePreservingView` / `SetDataPreservingView`
   callers (activity monitor, detail browser sort, query results sort,
   `redrawGrid`, tuikit `propsheet/togglegrid.go`): after step 1 of the
   tuikit fix they collapse by default, which is right for all of them —
   the sort paths already collapse explicitly and can drop their
   "Unconditionally: … kept a Shift-extended block" comments.

**Verify:** tuikit `go test ./...`; gossms tests for: filter change with a
Shift block → one row selected, on the cursor's event; Resume after eviction
→ same; column hide/show with a block → block kept. Then drive the binary
(docs/testing.md, tmux harness): Watch Live Data, Shift-select, filter,
Copy Rows with Headers, check the clipboard.

---

## Bug 2 — XEvent "Export as INSERT script" puts the session name / file path in a `--` comment unescaped — DONE

**Status:** fixed — `insertScript`'s header and the three endpoint-script
comment lines in `new_endpoint_dialog.go` go through `tsql.CommentSafe`;
`TestXEventInsertScriptHeaderIsCommentSafe` (session name with LF, file path
with CR/LF) fails without the fix. The optional repo-wide `"-- …%s"` guard
test was not added.

**Where:** gossms `internal/tui/xevent_viewer_export.go`, `insertScript`:

```go
source := "event session " + v.session
if v.files != "" { source = "event files " + v.files }
fmt.Fprintf(&b, "-- %d events from %s, exported by goSSMS %s.\n", ...)
```

**What:** an event session name is a delimited identifier and may contain
CR/LF; a file path may too. A line break ends the `--` comment and the rest
of the name runs as T-SQL when the exported `.sql` is executed. Every gosmo
scripter routes names in comments through `tsql.CommentSafe` for exactly
this reason (its doc names the attack); this site was missed.

**Failure scenario:** a login with `ALTER ANY EVENT SESSION` creates
`CREATE EVENT SESSION [x<LF>EXEC sp_addsrvrolemember 'evil','sysadmin';--] …`.
A DBA opens it in Watch Live Data, exports as INSERT script and runs the
file to load the events: the second line executes with the DBA's rights.

**Fix:** `tsql.CommentSafe(source)` (gossms already imports
`github.com/radix29/gosmo/tsql`). While there, apply the same to the
endpoint script's `-- on %s` / `-- %s has no certificate yet` /
`-- Missing here: … %s` lines in `internal/tui/new_endpoint_dialog.go`
(instance names typed by the user or reported by the server — low risk,
same rule).

**Verify:** unit test: session name with `\n` and `\r` → the header is one
line and `sqltext.SplitBatches`/the lexer sees only a comment before
`CREATE TABLE`. Optionally add a repo-wide test in the style of the doc-link
test that greps `"-- …%s` format strings in non-test Go for a missing
`CommentSafe` — a guard for the next one.

---

## Bug 3 — `TestNoDanglingDocReference` fails on HEAD — DONE

**Status:** fixed — the pointer in `docs/decisions.md` replaced with the
date; `TestNoDanglingDocReference` and `go test ./...` green.

**Where:** `docs/decisions.md`, the "What the 2026-11-14 duplication review
left duplicated, on purpose" entry.

**What:** gossms `054735e` deleted the duplication plan document, but the
decisions entry still cites it ("the plan was `docs/duplication-plan` …"),
so `go test ./internal/tui/` fails:

```
doc_links_test.go:132: docs/decisions.md:1741: references docs/duplication-plan.<md>,
which does not exist — replace the pointer with the fact it was pointing at
```

`go test ./...` is red on the branch, which hides any new failure.

**Fix:** drop the pointer and keep the fact: "(`dupl -t 90` over gossms,
gosmo, tuikit, 2026-11-14)". The plan's content is in git history
(`git show 4c06815:docs/duplication-plan…`) if anything else in the entry
needs it; the bullets below already carry the outcome.

**Verify:** `go test ./internal/tui/ -run TestNoDanglingDocReference`, then
`go test ./...` green across gossms.

---

## Checked and not bugs (do not re-raise)

- gosmo `QuoteName` refactor (`ab53ba0`): `quoteIdent` was already a
  forwarder; identical output.
- tuikit `DropLeadingRows`: all-rows-evicted case resets marks correctly
  (`markHi < markLo` → empty run; every flip `< k` dropped).
- tuikit `blockPasteSpan` counts only `\n` while `blockPaste` now also splits
  on a lone `\r`: unreachable, since block paste triggers only for
  `text == blockClip`, which the editor builds from its own (already
  LF-normalized) lines.
- gossms XEvent INSERT export numeric columns: emitted unquoted only when
  `isSQLNumber` held for every shown value.
- `CycleLog` nil-server panic in the `internal/tui` test log: deliberate
  (`log_viewer_recycle_test.go`).
