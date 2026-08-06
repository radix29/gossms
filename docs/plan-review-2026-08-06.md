# Cross-repo review, 2026-08-06

A review of goSSMS and gosmo for bugs, inconsistencies, optimisations, and
architecture. Both repos built, vetted, `gofmt`-ed and tested clean at the
time of writing, so almost everything below is in the ~10,000 lines of
uncommitted Activity Monitor / charts / block-editing work, plus two older
items that turned out to be worth measuring.

Numbers in this document were measured on the author's machine (Intel
i5-2500K, linux/amd64) with throwaway benchmarks that are not committed
unless a batch below says otherwise.

Batches are independent. Status is recorded per batch as work lands.

---

## Batch A — correctness fixes — DONE 2026-08-06

Small, self-contained, no behaviour change beyond the bug each one removes.
All four landed as described.

### A1. Pinned tooltip survives a scrollbar drag and then labels the wrong bucket

`internal/tui/activity_monitor_input.go`, `scrollbarDrag`.

The two scrollbars write `am.scrollY[am.tab]` / `am.scrollX[am.tab]`
directly instead of going through `scrollTo`. `scrollTo` clears
`am.tooltip`, and its comment states exactly why: the canvas has moved under
a box that is anchored to a spot in the viewport, so the box now names a
column whose numbers are not the ones it is showing. The wheel and every
scrolling key route through `scrollTo`; the scrollbars are the one path that
does not, which makes the documented failure reachable.

Fix: route both through `scrollTo`, keeping the `true` return — the gesture
was claimed whether or not the offset actually changed.

### A2. `NewCollector`'s `rate` parameter is silently discarded

`internal/activity/collector.go`.

The constructor takes `rate time.Duration` and never assigns it; the rate
that takes effect is the one passed to `Run`. It is correct today only
because the single caller passes `am.rate()` to both. A caller passing two
different values gets no error and no warning.

Fix: drop the parameter. `Run` already owns the rate, and `SetRate` changes
it.

### A3. `pressIsDouble` contradicts its own comment on modified clicks

`internal/tuikit/controls/editor_input.go`.

The comment says a Shift- or Alt-clicked press "still counts as 'the
previous press' for the one after it". The call site is
`pressIsDouble(...) && ev.Modifiers() == tcell.ModNone`, and `pressIsDouble`
clears `lastClickAt` on its true branch before the `&&` short-circuits — so
a modified press at a recently clicked spot records nothing, and the press
after it cannot pair.

Fix: pass the modifiers in and test them inside, so the "record this press"
path runs for a modified press as the comment intends.

### A4. `IsRetryable` reports `context.DeadlineExceeded` as retryable

`retry.go` in gosmo.

`errors.AsType[net.Error]` matches `context.DeadlineExceeded`, which
implements `net.Error`. Probed: `true` for `DeadlineExceeded` both plain and
wrapped, `false` for `Canceled`. Context errors are not in the doc comment's
list of what this function matches, so this is unintended.

Inside gosmo the blast radius is small — `withRetry`'s `ctx.Done()` select
catches the case where the expired context is the one passed in. It is not
caught when the deadline came from a sub-context (gosmo creates one itself
for connect, `server.go`), and `IsRetryable` is exported precisely so
callers can drive their own retry loops, where it will retry their own
timed-out queries three times with backoff.

Fix: return false for `context.Canceled` and `context.DeadlineExceeded`
before any other test, say so in the doc comment, and pin it with a test.
`Canceled` already returns false; naming it keeps the two from drifting
apart.

---

## Batch B — draw-path performance — DONE 2026-08-06

No behaviour change. The biggest user-visible win here.

`ActivityMonitor.drawDashboard` allocates a fresh 150x61 `charts.NewCanvas`
and re-renders all ~11 charts on *every* `Draw` — every keystroke anywhere
in the application, every mouse-motion event during a drag, not only on a
collector tick. Measured against a 900-sample store:

```
BenchmarkDrawHistory   8,796,050 ns/op   1,236,319 B/op   16,620 allocs/op
```

Profile attribution, so the work targets the right thing:

- **`Canvas.SetContent` — 62.7% of all allocations.** It builds each cell
  with `string(append([]rune{primary}, combining...))` (two allocations per
  cell) and then measures it with `displaywidth.String`, a full grapheme
  segmentation, per cell. The overwhelmingly common case is a single ASCII
  rune with no combining marks, where both are avoidable — `Fill` in the
  same file already takes the cheap `displaywidth.Rune` path.
- **`composeStack` — 24.2% of allocations.** It allocates `out` plus an
  `owner` slice of `len*8` ints per call. `HistoryChart.drawColumns` calls
  it once per series per column with exactly one segment, where the
  eighths-ownership machinery buys nothing.
- **`reflectlite.Swapper` — 5.4%**, from `sort.SliceStable` in
  `drawColumns`. `slices.SortStableFunc` is a drop-in with no reflection and
  no allocation, and matches the `slices` convention in `CLAUDE.md`.
- `maxStackTotal` / `maxValue` are recomputed by both `Draw` and `Plot` on
  every stacked chart.

Then cache the rendered canvas in `drawDashboard`, invalidated on sample
count, canvas size, or header state — the view models only change in
`applySample`.

Verification: keep the benchmark as a committed one, plus a golden-canvas
test asserting a cached canvas is byte-identical to a freshly rendered one.

### What landed

All four profile items plus the canvas cache. `Canvas` now holds a cell as a
primary rune plus the rest of its grapheme instead of one string, so the
common single-rune write allocates nothing and measures with
`displaywidth.Rune`; `Blit` reads cells directly rather than through `Get`.
`composeStack` has a single-segment fast path. `drawColumns` sorts with
`slices.SortStableFunc`. The `maxValue` / `maxStackTotal` double pass went
away by having both history charts' `Draw` return the plot rect it used, so
`dashboard.drawChart` no longer calls `Plot` after drawing.

`ActivityMonitor` caches the rendered canvas, keyed on tab, canvas size,
header, interval, and a `viewGen` counter bumped wherever the view models are
rebuilt. Sample count could not be the invalidator the batch assumed:
`Store.Len` stops moving once retention starts pruning while the contents keep
scrolling.

Measured on the same machine, same 900-sample worst case
(`BenchmarkDrawHistory`, committed in `internal/tui/dashboard/bench_test.go`;
the numbers above came from a differently shaped throwaway, so this is its own
before/after pair taken by reverting the three chart optimisations in place):

```
before   6,357,351 ns/op   1,302,486 B/op   20,685 allocs/op
after    3,051,370 ns/op     818,934 B/op    2,183 allocs/op
```

And per frame, which is what a keystroke actually costs now that a redraw
without new data is a blit —
`BenchmarkActivityMonitorDraw`, `internal/tui/activity_monitor_cache_test.go`:

```
761,824 ns/op   2,151 B/op   164 allocs/op
```

Behaviour was checked by capturing `cmd/amdemo` under tmux with SGR codes
(`capture-pane -p -e`) before and after: both dashboards byte-identical,
colours included.

---

## Batch C — documentation and dead surface — DONE 2026-08-06

gossms only. No gosmo surface is proposed for removal; the no-removal rule
in `CLAUDE.md` stands.

Mechanical, except the first two, which need a design decision (wire the
promised behaviour up, or trim it and correct the comment). The
recommendation is to wire both up: `cmd/amdemo` already renders them, so
they are the intended design rather than speculation.

- **`fileRow.isLog` is written and never read** (`internal/activity/fileio.go`).
  The query comment claims the column exists "which the log panel needs
  separated from data-file I/O", but `fileDeltas` aggregates log and data
  files together.
- **`SampleView.WaitLegend` and split wait `Parts` are only populated by
  `cmd/amdemo` and tests.** `buildSampleView` / `waitBars` set a plain
  `Value`, so the live Sample tab draws a strictly poorer waits panel than
  the mock — while `dashboard/view.go` documents the resource/signal split
  as what it draws.
- **Two `HistoryView` doc/code mismatches** (`internal/tui/dashboard/view.go`):
  `Activity` is annotated "stacked" but drawn with the overlaid
  `HistoryChart`; `CPU` is annotated "SQL, other processes, idle — stacked
  to 100" but `CPUUsage` deliberately omits idle, and `activity/cpu.go`
  explains why.
- **"four tabs" in two comments, five tabs in the code**
  (`activity_monitor.go`, `activity_monitor_draw.go`). The first also still
  says collection "lands in the next phase" and that the panel "currently
  draws both dashboards' chrome with no data", which shipped 2026-08-06.
- **Nine counters are collected every tick and never surfaced**
  (`internal/activity/counters.go`): `Logins/sec`, `Logouts/sec`,
  `Full Scans/sec`, `Page Splits/sec`, `Workfiles Created/sec`,
  `Worktables Created/sec`, `Page lookups/sec`, `Readahead pages/sec`,
  `Memory Grants Outstanding`. Cheap — one query — but nothing reads them.
  Four of five `SessionStats` fields are the same, and look like deliberate
  groundwork for the Sessions tab; those want a comment saying so rather
  than deletion.
- **`sessions.go` uses two different user-session cut-offs** —
  `is_user_process = 1` for sessions, `session_id > 50` for requests, under
  one comment calling `> 50` "the conventional user-session cut-off".
- **Grid-dot comment says "every other column"; the loop steps by 3**
  (`internal/tuikit/charts/common.go`, `drawPlotBackground`).

### What landed

Both design decisions went the way the batch recommended: wired up, not
trimmed.

`fileRow.isLog` now reaches the view. `fileDeltas` groups by database *and*
file kind instead of by database alone, so a database contributes a data row
and a log row; `FileIO` carries `IsLog` and a `Label()` that renders the log
half as `"db (log)"`, which is what the Sample tab's DATABASE IO bars show.
The total still covers every file. Verified live against ubudock — the panel
names `test_new_db (log)` separately from its data file.

Waits are split into their resource and signal halves per category.
`waitDeltas` no longer folds every category's signal time into `WaitCPU`: it
returns the whole wait per category plus a parallel signal-per-category
array (`Sample.WaitsSignal`), and the Sample tab draws each bar as a red
resource part under a green signal part with a Resource/Signal legend. This
is a deliberate reversal of the old attribution — a test pinned the old
behaviour and was rewritten. `CPUPctOfWaits` is unchanged, and the History
waits stack now plots total wait per category rather than resource-only.
Verified live under generated load: the CPU bar rendered red-bottom,
green-top, and the legend drew.

The rest were comment and query corrections: two `HistoryView` annotations
(`Activity` is overlaid, not stacked; `CPU` omits idle deliberately), "four
tabs" → five plus the removal of the shipped "lands in the next phase" text,
the nine unsurfaced counters and four unsurfaced `SessionStats` fields now
documented as increment-2 groundwork rather than left looking accidental,
and the grid-dot comment corrected to "every third column". `sessions.go`
now uses `is_user_process = 1` for the request counts as well as the session
count — one definition instead of two — via a `LEFT JOIN` from sessions to
requests; the rewritten query was run against ubudock before it was
committed.

---

## Batch D — undo memory — DONE 2026-08-07

`Editor.snapshot` deep-copies every line of the document on every
`pushUndo`, which is every typed character:

```
BenchmarkPushUndo1k       233,882 ns/op     248,589 B/op    1,001 allocs/op
BenchmarkPushUndo20k    3,248,575 ns/op   4,963,613 B/op   20,001 allocs/op
BenchmarkPushUndo100k  11,929,413 ns/op  24,800,264 B/op  100,001 allocs/op
```

3.2 ms of copying per keystroke in a 20,000-line script. `maxUndoSteps` is
500, and its comment is right that a step cap "bounds memory to a fixed
multiple of one snapshot's size" — but the multiple is 500x, so that same
script has a 2.5 GB worst-case undo stack. This is pre-existing and was not
recorded in `docs/open-threads.md`.

Options:

1. **Cap the stack by total bytes rather than by step count.** Small,
   keeps the current model, removes the memory cliff but not the
   per-keystroke copy.
2. **Per-edit deltas instead of whole-buffer snapshots.** The correct fix.
   Touches every `pushUndo` call site and the undo/redo machinery, and is
   the kind of change that wants live tmux verification rather than unit
   tests alone.

Recommendation: do (1) now, record (2) in `docs/open-threads.md` as designed
and deferred, rather than attempting both in one pass.

Separately and independently: `e.undoStack = e.undoStack[1:]` keeps dropped
snapshots reachable through the backing array. `Store.Append` in
`internal/activity/store.go` avoids exactly this and documents why; the
editor should copy into a fresh slice the same way.

### What landed

Option (1) as recommended, plus the aliasing fix; (2) is now in
`docs/open-threads.md` § Designed and deferred.

`editorState` carries the snapshot's approximate heap size, measured once
while it is copied, and `Editor.undoBytes` tracks the stack's total across
every push and pop. `trimUndo` drops oldest-first until the stack is inside
both `maxUndoSteps` (500, unchanged) and the new `maxUndoBytes` (64 MB),
always keeping the newest step even when it alone exceeds the budget — one
undo has to work on any document. Survivors are copied into a fresh slice
rather than resliced, the same reason `Store.Append` does.

Two tests pin it: a ~13 MB document where the byte cap binds first and undo
still walks back one character at a time through the retained steps, and a
document whose single snapshot is over budget where the one step survives and
restores. The step-count test is unchanged and still passes. Live-checked
under tmux: typing, six undos and a redo in a query panel step character by
character as before.

---

## Settled — do not re-raise

Each of these looked like a finding and is not. Recorded so the next review
does not re-derive them. Move these into `docs/open-threads.md`
§ "By design — not issues" when the batches land.

- **`scanResultSet` / `streamResultSet` have no `rows.Err()` of their own,
  and that is fine.** The message loop in `internal/query/executor.go`
  checks it once for the whole batch and records it on the result, so a
  result set aborted part-way through is reported, not silently truncated.
- **The `append(line[:lo], line[hi:]...)` in-place mutation throughout
  `editor_block.go` cannot corrupt the undo stack.** `Editor.snapshot` deep
  copies every line, so no snapshot shares a backing array with the live
  document.
- **`blockPaste` does replace an existing selection.** Its `switch` handles
  the `HasSelection` case before inserting.
- **The `buildTools` closures over the loop variable `i` are correct** under
  Go 1.22+ per-iteration loop variables.
- **gosmo's identifier and literal quoting is correct at every site
  checked**, including the `escapeSingle(t.FullName())` calls that
  `identifier_quoting_test.go` exists to pin. gosmo also has no `rows.Err()`
  omissions, no `defer` inside a loop, and one `sync.Mutex` as its entire
  concurrency surface.
