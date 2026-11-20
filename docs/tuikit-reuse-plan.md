# Plan: gossms controls worth moving to tuikit

Status: **proposal; items 1–6 done** (2026-11-20), the rest not started (2026-11-19). Survey of
`internal/tui` (353 non-test files) and its leaves `dashboard`, `planview`,
`sqledit`, `gate`, against tuikit's package map (`~/go/tuikit/README.md`).

Test for a move: the code knows no SQL Server, gosmo, `App` or config. It is a
drawing/interaction rule, not a product opinion. It has, or would plausibly
have, a second user. And tuikit's dependency rule (tcell + displaywidth only)
holds after the move.

Most of the app is already built on tuikit, and what is left in gossms is
mostly domain code. The findings below are small. They are ranked by value.

---

## 1. `propsheet`: a Select row that never misreports a value — **DONE (2026-11-20)**

**Done:** `~/go/tuikit/propsheet/preservingselect.go`: the distinct type
`PreservingSelectRow` (embeds `*SelectRow`, stores `unset`), built by
`SelectPreserving`, with `PreservedValue`, nil-safe `ChangedTo`, `Unset`,
`SetChoices` (the repoint form, which replaced the AG pages'
`agSetSelect`/`agSelectValue`) and the exported `PreservingItems`. The tests
moved to `preservingselect_test.go`. gossms's four helpers are gone. The
`selectRow` test helper (`form_helpers_test.go`) learned the new type through
`asSelectRow`, because a type assertion on `*SelectRow` misses the embedding
type. Driven live: User Properties for a user whose default schema does not
exist showed `nosuchschema`, and changing it to `dbo` scripted only
`ALTER USER … WITH DEFAULT_SCHEMA = [dbo]`. The `(None)` login stand-in was
not written. The AG replica pages (`SetChoices`) are covered by unit tests
only and were not driven.


**Today:** `internal/tui/prop_grid_helpers.go`: `preservingItems`,
`selectPreserving`, `preservedValue`, `changedTo` (plus `indexOfOK`, which
they use). 15 production files call `selectPreserving`.

**Why it belongs in tuikit:** this is a property-sheet correctness rule, not a
SQL rule. A dropdown seeded with a value missing from its list must show that
value rather than fall back to `items[0]`. Its stand-in for "no value" must
never be read back as a real value, and writes are gated on "dirty and not on
the stand-in". Any propsheet host editing externally-sourced values hits the
same trap: `propsheet.Select`'s index API puts it right in the path.

**Shape (proposal):**

```go
// propsheet/choicerows.go (or preservingselect.go)
func SelectPreserving(label string, base []string, value, unset string) *SelectRow
func (r *SelectRow) PreservedValue() string       // "" when on the stand-in
func (r *SelectRow) ChangedTo() (string, bool)    // dirty and not the stand-in
```

The row stores `unset`, so callers stop passing it twice. That removes a class
of mismatch, and the nil-row case of `changedTo` stays a gossms wrapper or
becomes a nil-receiver-safe method. Alternatively a distinct
`*PreservingSelectRow` type embeds `*SelectRow`, so only rows built that way
expose the read-back. **Author's call:** whether to use a method on `SelectRow`
or a distinct type. The distinct type is safer: `ChangedTo` on a plain Select
row means nothing.

**gossms after:** the four helpers go and the 15 call sites are renamed.
`indexOf`/`indexOfOK`/`boolIdx` stay because they are trivial and gossms-local.
Their doc comments (the "job owned by a dropped login" rationale) move with
the code, reworded neutrally.

**Tests:** move/port the existing ones covering these helpers into
`propsheet`. Drive a Properties page with an orphaned owner in the binary.

---

## 2. `charts.DrawSectionBar`: one section title strip — **DONE (2026-11-20)**

**Done:** `~/go/tuikit/charts/section_bar.go` (`DrawSectionBar`,
`SectionBarHeight`), tests in `section_bar_test.go`. All three gossms copies
call it. Decision: the AG dashboard adopted the shared look (recorded in
`docs/decisions.md`).


**Today:** three hand-drawn copies of the same strip:

| Site | Style | KPIs |
|---|---|---|
| `internal/tui/dashboard/common.go` `drawSectionBar` | `theme.StyleChartSection()` | right-aligned `charts.KPI`s |
| `internal/tui/detail_browser_charts.go` `drawCharts` (inline) | `theme.StyleChartSection()` | none |
| `internal/tui/ag_dashboard_draw.go` `drawSectionBar` | `ChartSectionBg` + `pal.Text`, **bold** | none |

The AG dashboard's comment says "matching the Activity Monitor", but it uses
a different foreground (`Text` vs `ChartHeading`) and bold. That is drift.

**Shape:** `charts.DrawSectionBar(s, r core.Rect, title string, kpis []KPI)`,
next to `charts.DrawPanelTitle` (`charts/panel_title.go`): the dashboard
version verbatim, with the `LabelBg` override included.

**Decision for the author:** should the AG dashboard adopt the shared look
(`ChartHeading`, not bold) or keep its own? The plan assumes it adopts it.

---

## 3. A determinate progress bar — **DONE (2026-11-20)**

**Done:** `~/go/tuikit/widgets/progress_bar.go` (`DrawProgressBar`,
`ProgressBarLabelWidth`), tests in `progress_bar_test.go`, as the function
form. One change from the gossms copy: `pct` over 100 now clamps, so the
label can no longer overrun `w`. `drawTaskProgress` calls it. Driven live: a
Back Up of a throwaway database showed the indeterminate (all `░`, no label)
frame, then `█…█ 100%`.

**Today:** `internal/tui/backup_common.go` `drawProgressBar`: a `█`/`░` bar
with a trailing percentage, `pct < 0` meaning indeterminate. It is used by
`drawTaskProgress` (Backup and Restore) and tested in
`task_progress_draw_test.go`. tuikit has no determinate bar.
`dialogs.ProgressDialog` is spinner-only.

**Shape:** `widgets.DrawProgressBar(s, x, y, w, pct int, st tcell.Style)` as a
function, or a `widgets.ProgressBar` value with `SetBounds(x, y)`/`SetWidth`
to match the other leaf widgets. Prefer the function: the bar is stateless
and never takes focus.

`drawTaskProgress`/`taskProgressView` **stay** in gossms because they read
`*Task`.

---

## 4. `core.SplitColumns`: equal panels with a gutter — **DONE (2026-11-20)**

**Done:** `SplitColumns` in `~/go/tuikit/core/geometry.go`, tests in
`geometry_test.go`. Both decisions taken as proposed (recorded in
`docs/decisions.md`): the last column absorbs the remainder everywhere, and
the dashboard's collapse threshold now counts the gutters.


**Today:** two implementations that differ in policy:

- `dashboard/common.go` `splitColumns(r, n)`: collapses to **one** full-width
  panel when `n` won't fit at `minPanelW`, and the last panel absorbs the
  rounding.
- `detail_browser_charts.go` `detailChartPanels(body, n)`: fits **as many as
  fit**, and the remainder is left unused on the right.

**Shape:** `core.SplitColumns(r Rect, n, gutter, minW int) []Rect`, which fits
as many as fit and lets the last column absorb the remainder. The dashboard's
"all or one" becomes two lines at its call site
(`if len(cols) < n { cols = []core.Rect{r} }`).

**Decision for the author:** the Detail Browser strip would gain the
remainder columns on its last panel, which is a visible change of 0 to n-1
cells. Accept it, or keep the current geometry with a `lastAbsorbs` flag.
Skip the item if neither is wanted: two callers is a weak case.

---

## 5. `charts`: map a click on a plot back to a bucket — **DONE (2026-11-20)**

**Done:** `~/go/tuikit/charts/plot_hit.go` (`PlotHit`), tests in
`plot_hit_test.go`. `dashboard.ChartHit` is gone; the `Draw*` functions
return `[]charts.PlotHit`.


**Today:** `internal/tui/dashboard/view.go` `ChartHit` (`Plot`, `Series`,
`TimeRow`, `Snapshot`; `Bucket(x)`, `Column(idx)`, `Value(ser, idx)`). It is
pure `charts` arithmetic over `BucketAt`/`ColumnAt`/`AtBucket`, and its
`Value` doc names a real trap (`At` vs `AtBucket` disagreeing for a shorter
series). Its only consumer is the Activity Monitor tooltip.

**Why consider it:** tuikit already ships the readout box
(`charts/readout.go`), but the hit model it needs comes from gossms. A tuikit
user who wants click-to-read on a `HistoryChart` would rewrite `ChartHit` and
likely get `Value` wrong.

**Shape:** `charts.PlotHit` with the same fields and methods. `Title` stays
as a generic label. The `Draw*` functions in `dashboard` keep returning a
slice of them.

**Weakness:** there is one consumer, so do it only together with item 2,
which touches the same package.

---

## 6. Hand-rolled form dialogs: a shared focus-ring keyboard — **DONE (2026-11-20)**

**Done:** `dialogs.FieldRingKey` and `dialogs.RingStep` in
`~/go/tuikit/dialogs/focus_ring.go`, tests in `focus_ring_test.go`, beside the
existing `ButtonRowKey`. That helper already set the de facto canonical ring
(Connect, Backup, Restore, Profile, `ListDialog`, `propsheet.Form`), so the
four outliers moved onto it rather than onto a new model. Decision recorded
in `docs/decisions.md`: the row is one stop with arrows inside it, full wrap,
widget first and then Up/Down move focus. Find/Replace (tuikit), Log Search,
Filter and Options converted. The Options Tab trap was confirmed in code and
is gone.


`docs/decisions.md` records the Find/Replace vs Log Search key skeleton as
"extract for a third dialog". There are now four hand-rolled focus rings with
**divergent** semantics:

| Dialog | Tab past last button | Backtab at first field | Up/Down |
|---|---|---|---|
| tuikit `dialogs.FindReplaceDialog` | `NextFocus` (wraps) | wraps | field's own |
| `log_search_dialog.go` | `NextFocus` (wraps) | wraps | field's own |
| `filter_dialog.go` | wraps (`moveFocus`) | wraps | move focus |
| `options_dialog.go` | cycles **OK/Cancel only**, never back to the fields | **does not wrap** | move focus |

Options' Tab-on-buttons loop comes from reading
`OptionsDialog.HandleKey` and is **not verified in the binary**. Drive it
before calling it a bug, and check `docs/open-threads.md` first.

**Proposal:** decide the canonical ring semantics first: SSMS-like full wrap,
Up/Down moving focus outside multi-line controls, and an open dropdown getting
first refusal. Then add `dialogs.RingKey(ev, ring *Ring) bool` (or extend
`dialogs/focus_ring.go`) and convert the four. This is a behaviour change for
Options at least, so it is the author's call. If declined, record that in
`docs/decisions.md` beside the existing entry.

---

## 7. Scrollable details pane (weakest; conflicts partly with a decision)

`log_viewer_draw.go` and `xevent_viewer_draw.go` share a near-identical draw
loop: placeholder when nothing is selected, lines from `detailScroll`, and a
`▾ more (Alt+↓)` hint. `viewer_panel.go` `scrollDetailLines` already
shares the scroll clamp. `planview/details.go` has a third, header-style
variant (`canUp`/`canDown`).

`docs/decisions.md` (duplication review, "LogViewer vs XEventViewer beyond
`viewerPanel`") records that "details text and cache … diverge". A tuikit
`controls.TextPane` (lines + per-line style func + scroll + "more" hint)
would leave the text and cache with the host, so it does not contradict the
entry. But the gain is ~25 lines across two callers. **Recommend not doing
it** unless a third read-only pane appears. Listed so the next review doesn't
re-derive it.

---

## Considered and kept in gossms

| Code | Why it stays |
|---|---|
| `latest.go`, `safego.go`, `postAndWake` | App threading (`ARCHITECTURE.md` § Latest-only loads, § Async result delivery). tuikit leaves goroutines to the host and has no async layer to put them in. |
| `viewer_panel.go` shell | A composition of tuikit parts plus `App` config (`copyWithStatus`, `MaxCellLength`); see the decisions entry above. |
| `picture_panel*.go` | Depends on `gotuipic`; tuikit allows tcell + displaywidth only. |
| `cell_value.go` `classifyCellValue` | The `[…]` rule exists because of SQL Server bracket-quoted names. |
| `detail_browser_sort.go` sort key | Parses gossms's own `formatMB`/`formatBytes` output and SQL placeholders. tuikit's `datagrid_sort.go` deliberately leaves comparison to the host. |
| `pending_edits.go`, `name_set.go` | Keyed by gosmo collation (`gosmo.NameKey`). tuikit's `propsheet.ListEditor` already covers the generic part. |
| `redrawGrid`/`resetGrid` | One-line wrappers over `DataGrid.SetDataPreservingView` (+ `SetSelectedRow`); not worth API. |
| `HelpDialog`, `TasksDialog`, `QueryListDialog`, `StatusHistoryDialog` | Already thin wrappers over tuikit `TextDialog`/`ListDialog`/`LogDialog`. |
| Chart tooltip pins (Activity Monitor, Detail Browser) | They already share `charts.ReadoutSize`/`PlaceReadout`/`DrawReadout`. Their lifecycles (time-keyed vs one-shot) differ. |
| `dashboard` `drawHeader` | Monitoring-specific fields (instance, version, paused, resolution). |
| `dialog_common.go` progress mode | Bound to `*Task`. |

**Side note (gossms-internal cleanup, not a move):** `planview/graph.go`'s
`putClipped`/`hlineClipped`/`vlineClipped` re-implement clipping that
`core.ClipScreen` provides.

---

## How to carry out any item

1. Load the `dev-with-local-gosmo` skill (it covers tuikit). Work in
   `~/go/tuikit` first: code, port tests, `go test ./...`, `staticcheck`,
   `gofmt`. Keep `TestTuikitImportsOnlyItsDeclaredDependencies` and
   `TestTuikitLayering` green.
2. Update `~/go/tuikit/README.md` § Package map for every new file or
   symbol.
3. Switch gossms to the tuikit symbol and delete the local copy. If an
   `internal/tui` file disappears, update `ARCHITECTURE.md` § Package map
   (`TestPackageMapListsEveryTUIFile`).
4. `go test ./...`, `staticcheck ./...`, `deadcode -test ./...` in gossms.
   Then drive the binary per `docs/testing.md`: Properties page with an
   orphaned owner (1), Activity Monitor + AG dashboard + Database Detail
   Browser strip (2, 4, 5), Backup/Restore progress (3), the four dialogs'
   Tab/Backtab/Up/Down/Esc (6).
5. Record the outcome of each decision (2, 4, 6, 7) in `docs/decisions.md`.

Suggested order: 1, then 2+5 together (both `charts`), then 3, then 4, then 6.
Item 7 is not recommended.
