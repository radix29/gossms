# Activity Monitor — implementation plan

Plan of record for building the Activity Monitor panel described in
`todo/mockups/ACTIVITY_MONITOR.md`. That work order is the requirements
document; this is how goSSMS implements it, including the four places the
implementation deliberately deviates from it.

## Status

| Phase | State |
|---|---|
| 1 — charts, canvas, theme roles | **Done.** `internal/tuikit/charts` with 71 tests; chart colour roles and styles in `tuikit/theme`. |
| 2 — `cmd/amdemo` and the dashboard renderers | **Done.** `internal/tui/dashboard` (both dashboards, 14 tests) and `cmd/amdemo`; canvas sizes pinned below. |
| 3 — panel shell | **Done.** `internal/tui/activity_monitor{,_draw,_input}.go` with 13 tests; `showActivityMonitorFor` opens it, one panel per server. |
| 4 — live collection | **Done.** `internal/activity` (10 files, 20 tests); History and Sample both live against `sys.dm_os_*` and `sys.dm_io_virtual_file_stats`. |

## Decisions taken up front

| Decision | Choice | Why |
|---|---|---|
| Where DMV collection lives | `internal/activity` in gossms | gosmo has no DMV/perf-counter surface today (no hits for `dm_os_performance_counters`, `dm_os_wait_stats`, `dm_io_virtual_file_stats`, `dm_os_schedulers`, `dm_os_memory_clerks`). A dashboard-shaped collection API is not SMO-shaped, so it stays out of gosmo's public surface. |
| Mock/demo mode | `cmd/amdemo`, built in phase 2 | Mirrors `cmd/plandemo`; lets the layout be matched to the mockups and screenshotted without a live server, and gives the charts package a real driver. Not part of the release build. |
| CPU source | `sys.dm_os_schedulers` only | No `sys.dm_os_ring_buffers`, no XML shredding, no edition-specific fallbacks. Revisit only if the CPU panel reads thin. |
| Sample tab data | The newest sample in the History store | See "Deviation 1". |
| `VIEW SERVER STATE` | Assumed present | Checked once at collector start; missing permission shows one message and stops the collector. |

## Deviations from the work order

These are intentional and were agreed before implementation started. Each
one contradicts a line in the work order's acceptance criteria.

### 1. One collector, not two — Sample is History's newest sample

The work order specifies independent History and Sample collectors, each
with its own connection, timer, refresh rate, and pause state, with Sample
refreshing only while its tab is active.

Instead: a single collector produces one full `activity.Snapshot` per tick.
History renders a series across the stored buckets; Sample renders
`store.Latest()`. One connection, one rate selector, one Pause/Continue
governing both tabs.

Why: the two tabs otherwise query the same counters twice over two
connections, and the numbers on the two tabs come from different instants
even though they describe the same server. Coupling them makes the tabs
consistent by construction, halves the query load, and makes "Sample does no
work while inactive" true structurally rather than through suspension logic.

Cost, and how it is bounded: the stored record must now be wide enough to
render the Sample dashboard, not just History's series. Retention is split —
the full-fidelity record (per-database file I/O, per-clerk memory
breakdown, wait detail) is kept for the **last 60 buckets**, and only
aggregate/total series are kept for the whole 30-minute window. Every
History chart plots totals, so nothing is lost.

### 2. Both dashboards scroll on both axes over a fixed canvas

Each tab renders into an off-screen canvas of fixed size; the panel blits
the visible window and scrolls it. Canvas sizes are taken from the mockups:

| Tab | Canvas (cols × rows) | Derivation |
|---|---|---|
| History | 150 × 61 | header row plus 4 sections of 15 (a 1-row bar and a 14-row body: panel title, chart, time label, legend) |
| Sample | 150 × 50 | 2 header rows plus 4 sections of 12 (a 1-row bar and an 11-row body) |

The row counts were estimated from the images' pixel pitch (~17 px per row,
~9.5 px per column) and then pinned in phase 2 against a real tmux render at
150 columns. Below either dimension the viewport scrolls rather than
reflowing. Above 150 columns the canvas widens to the viewport and the
history charts show more time buckets — the canvas grows, it does not
stretch.

### 3. Connections are per tab but opened lazily

The work order allows either eager or lazy. Eager would open four
connections, two of them for placeholder tabs that issue no queries. So:
the collector connection opens when the panel opens; Sessions and Block
open theirs when they gain real queries (increment 2).

### 4. `VIEW SERVER STATE` is assumed, not worked around

One check, one message, collector stopped. No per-metric permission
fallbacks. A counter this SQL Server version doesn't publish is simply
absent from the reading and its series plots zero — `counterSet.value`
returns 0 for a key it doesn't hold, so one missing counter costs its own
panel and nothing else. Memory-composition groups behave the same way: a
clerk group with no clerks (In-Memory OLTP, columnstore on an edition
without them) is absent from the bar rather than drawn as a zero-height
slice.

## Package layout

```
internal/tuikit/charts/       reusable chart primitives — no SQL, no tui import
  doc.go
  common.go                   Series, Segment, shared plot helpers
  canvas.go                   off-screen cell buffer + viewport blit
  scale.go                    nice-number linear scale, ticks, tick formatting
  glyph.go                    eighth-block ramps, half/partial-cell encoding
  axis.go                     Y labels, ┆ vertical grid, · dot grid
  legend.go                   ■ legend renderer: shorten → wrap → clip
  history.go                  overlay/grouped time-series column chart
  stacked_history.go          stacked time-series columns, no internal holes
  barchart.go                 horizontal eighth-block bars, plain and stacked
  vbar.go                     vertical eighth-block bars
  kpi.go                      compact numeric value box

internal/activity/            SQL Server collection — no TUI import
  doc.go
  counters.go                 sys.dm_os_performance_counters + cntr_type decode
  waits.go                    sys.dm_os_wait_stats, categories, benign filter
  fileio.go                   sys.dm_io_virtual_file_stats
  memory.go                   memory clerks grouped into the composition bar
  sched.go                    sys.dm_os_schedulers
  sessions.go                 dm_exec_requests / dm_exec_sessions counts
  snapshot.go                 one tick's full record
  rates.go                    cumulative deltas → per-second rates
  store.go                    30-minute ring; full detail for last 60 buckets
  collector.go                single ticker goroutine + rate/pause control

internal/tui/dashboard/       dashboard layout — charts + a view model, no App, no DB
  doc.go
  view.go                     HistoryView / SampleView: what a dashboard draws
  common.go                   section bars, panel titles, KPI rows, canvas sizes
  history.go                  the History dashboard's four sections
  sample.go                   the Sample dashboard's four sections

internal/tui/
  activity_monitor.go         Panel: state, tabs, toolbar, bounds, lifecycle
  activity_monitor_draw.go    tab/toolbar rows, canvas blit, scrollbars, placeholders
  activity_monitor_input.go   HandleKey/HandleMouse, gesture arming, scrolling
  activity_monitor_history.go History tab: metrics → charts.Series
  activity_monitor_sample.go  Sample tab: store.Latest() → charts

cmd/amdemo/                   deterministic mock harness (not in the release build)
```

Dependency direction holds: `charts` knows nothing about SQL Server,
`activity` knows nothing about the TUI, `internal/tui` glues the two.

`internal/tui/dashboard` is a third `tui` leaf, alongside `planview` and
`sqlparse`: it depends on `tuikit` and the standard library only, never on
`tui` itself. It exists because both the panel and `cmd/amdemo` have to draw
the same dashboards, and `amdemo` must not drag in the whole application the
way importing `internal/tui` would. Its input is a plain view model, so
`activity` never imports it and it never imports `activity` — the mapping
from a collected snapshot to a view model lives in `internal/tui`, which
already depends on both.

## Rendering rules

- **Stacked columns use partial-cell fg/bg encoding.** A cell drawn with an
  eighth-block glyph paints the lower fraction in the foreground colour and
  the remainder in the background colour, so a segment boundary inside a
  cell carries both series' colours. Interiors are `█`. A partial glyph
  appears only at a segment's top edge — never inside one — which is what
  keeps a stack continuous with no internal holes.
- **A small non-zero value never renders as nothing** — it clamps to `▁` or
  `▏` rather than rounding to zero.
- Axes: five Y levels on a nice-number scale, muted `·` dot grid across the
  plot, `┆` at time divisions, right edge = now.
- **The collector's state is repeated on the toolbar row.** The dashboard
  header carries it too, but the header lives on the canvas, and on a
  terminal narrower than 150 columns its right-hand end — the sample time
  and the PAUSED marker — has scrolled off. The toolbar row never scrolls,
  so it shows the status message, whether collection is running or paused,
  the sample time, and the interval, dropping them longest-first as the
  panel narrows. Found by driving the panel at 100 columns.
- Legends: full label → short label (`Trans`, `Comp`, `Recomp`) → wrap when
  there are spare rows → clip. The `■` is never dropped.
- Charts render deterministically into a canvas, so they are tested against
  golden cell dumps without a screen.

## Theme

`theme.Palette` gains chart roles — `ChartCyan`, `ChartGreen`,
`ChartYellow`, `ChartBlue`, `ChartRed`, `ChartPurple`, `ChartNeutral`,
`ChartGrid`, `ChartAxis`, `ChartPlotBg`. The work order's semantic table
(cyan = primary activity, green = transactions/CPU, yellow = disk/log/
compile, blue = memory/cache, red = recompiles/pressure, purple = PLE)
becomes a role map in `internal/tui`, so each metric's colour is fixed once
and stays stable across refreshes and across tabs.

## Session, connection, and timer model

- The panel owns an `activity.Session`: the store, the collector, and the
  per-tab connections.
- The collector is one goroutine started under `App.safego`, driven by a
  `time.Ticker` plus a control channel carrying rate changes and
  pause/resume. Its context derives from the collector connection's
  `ServerConn.Context()`. The ticker is never touched from the UI goroutine.
- Every tick's result reaches the UI through `App.postAndWake`, guarded by
  `panelHosted` before it is applied — the same shape as
  `connectForQueryPanel`.
- Closing the panel cancels the collector, closes every tab-owned
  connection, and drops the store. Nothing is persisted.

## Counter decoding

`sys.dm_os_performance_counters` is decoded by `cntr_type`, never read raw.
`counterSet.value` switches on the type on the row itself rather than on
what the caller expects, so a counter whose type changes between releases is
still read correctly:

| `cntr_type` | Meaning | Handling |
|---|---|---|
| 65792 | Raw gauge | Use as-is (PLE, User Connections, Total/Target Server Memory) |
| 272696576, 272696320 | Cumulative per-second counter | Delta ÷ elapsed seconds |
| 537003264 | Fraction | ÷ its 1073939712 base × 100 |
| 1073874176 | Average bulk | Delta ÷ base delta |

On a named instance `object_name` is `MSSQL$INST:SQL Statistics`, not
`SQLServer:SQL Statistics`, so counters are matched on the portion after the
colon, never by equality against the whole string. Both rules produce
plausible-looking wrong numbers when guessed, so both are pinned by unit
tests over fixture rows.

## Phases

Increment 1 is phases 1–4. Each phase builds, runs, and is independently
verifiable.

1. **charts + canvas + theme roles.** Golden cell-dump tests per primitive,
   plus zero-width, zero-height, empty-series, single-bucket, and all-zero
   cases asserting rendered output rather than absence of a panic.
2. **`cmd/amdemo`.** Deterministic synthetic data covering the five named
   operational patterns (workload wave, reporting window, maintenance
   window, I/O burst, CPU/wait spike), driving the real History and Sample
   renderers. This is where the 150×60 and 150×52 canvas sizes are pinned.
3. **Panel shell.** Tabs, toolbar (rate selector plus Pause/Continue for
   History and Sample; Refresh for Sessions and Block), two-axis scrolling
   with scrollbars, lazy connections, teardown, replacing the "Coming soon!"
   alert in `showActivityMonitorFor`.
4. **Live collection.** Counter decode, waits, memory, page activity, file
   I/O, log, checkpoints, schedulers; rate conversion; the store. History
   and Sample both go live, since Sample is nearly free once the store
   exists.

Increment 2: the real Sessions and Block tabs.

## Wait exclusions

The waits panel is only readable if the idle background waits are excluded,
and excluding them by name does not hold: SQL Server 2025's
`PWAIT_EXTENSIBILITY_CLEANUP_TASK` sleeps for five minutes and then reports
all 300,000 ms of it against a single two-second sample — 150,000 ms of wait
per second, against real waits of a few hundred — which flattened the entire
waits panel to one column. Found live, not by a test.

So `waits.go` excludes two ways: `benignWaits` names the one-off background
waits, and `benignFamilies` excludes whole prefixes (`SLEEP%`, `QDS\_%`,
`XE\_%`, `BROKER\_%`, `HADR\_%`, `PWAIT\_%`, `FT\_%`,
`PARALLEL\_REDO\_%`, `DBMIRROR%`, `SQLTRACE\_%`, `CLR\_%`,
`WAIT\_XTP\_%`) by `LIKE ... ESCAPE`. Every release adds background waits;
a list of names can only ever be out of date. Both directions are pinned by
tests — the background waits are excluded, and the waits worth showing
survive.

## Verification

Unit tests are not verification here; the dashboard is driven.

- `cmd/amdemo` under tmux at 100, 120, and 150 columns and at 24, 30, and 50
  rows. Captures with `-p -e` so stacked-segment colours can be told apart.
- Live server (phase 4): a throwaway workload that moves batches, waits, and
  log flushes; History confirmed still collecting while another tab is
  active; pause confirmed to freeze both tabs; `sys.dm_exec_sessions`
  compared before and after closing the panel to prove every connection is
  released.

## Application rules this feature must respect

- `HandleKey`/`HandleMouse` return `true` only for events actually acted on.
  Scrolling at a boundary returns `false`, so the panel never becomes a
  keyboard trap.
- Every toolbar control is context-gated; the panel is unreachable without a
  connection, and the existing entry points already gate on one.
- Mouse presses arm a gesture owner and a per-widget `mouseDragging` latch,
  cleared on the matching `ButtonNone`, which is forwarded even when
  `HandleMouse` returns early.
- Background work runs under `safego` and reports through `postAndWake`.

## Documentation to update when increment 1 lands

- `ARCHITECTURE.md` — package map entries for `charts`, `activity`, and the
  `activity_monitor_*.go` files.
- `docs/open-threads.md` — drop the Activity Monitor stub entry; add the
  pending Sessions/Block tabs.
- `docs/journal.md` — what was built, and the four deviations above.
- `README.md` — Activity Monitor moves out of the unbuilt list.
