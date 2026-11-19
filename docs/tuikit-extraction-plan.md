# Plan: gossms controls worth moving to tuikit

Survey of 2026-11-19 over `internal/tui` for code that is generic UI rather
than goSSMS behaviour, and so belongs in tuikit (`~/go/tuikit`). Nothing here
is implemented yet. Delete this file once its items are done or declined;
record a declined item in `docs/decisions.md`.

Ordered by value. Each item names what moves, what stays, and the tuikit
layering it needs (tuikit `README.md` § Dependency direction, enforced by
`tuikitLayers` in tuikit's `core/imports_test.go`).

## 1. `ToolRow.Draw` — `controls/tool_row.go` — DONE 2026-11-19

`ToolRow.Draw(s, r, disabled, moreDim)` is in tuikit (tests in
`controls/tool_row_test.go`); all seven gossms toolbars call it and
`viewerPanel.drawToolCells` is gone. Driven live on win10cli: Activity Monitor,
Replication Monitor ("Failed sessions only: on" now shows selected), Picture
("Fit" selected until 1:1), Log Viewer, XEvent Viewer, Query Store, Plan
Compare (two-statement .sqlplan files), each narrowed until More appeared
(Plan Compare has none: `NoOverflow`).

`controls.ToolRow` owns the geometry of a one-row text toolbar (Layout,
CellAt, OverflowItems, Run) but not its painting, so seven panels hand-roll
the same loop — fill the bar with `StyleMenuBar`, each laid-out cell in
`StyleTooltip`, dimmed with `TextDim` when disabled, then the "More ▾" cell:

- `viewer_panel.go` `drawToolCells` (Log Viewer, XEvent Viewer)
- `picture_panel_draw.go` `drawToolbar`
- `query_store_panel_draw.go` `drawToolRow`
- `plan_compare_panel.go` (the statement pickers)
- `replication_monitor_panel_draw.go` `drawToolRow`
- `activity_monitor_draw.go` `drawToolbar`

The copies differ in three ways only:

- where *disabled* comes from: a `func(int) bool` everywhere except Activity
  Monitor, which reads `ToolCell.Disabled`;
- when More is dimmed: never (Activity Monitor, Picture), while the whole row
  is busy (Query Store, Log Viewer), or never because it has no More (Plan
  Compare);
- whether `Selected` is painted: only Activity Monitor paints it
  (`StyleMenuSelected().Bold(true)`).

**Latent bug fixed by construction:** Replication Monitor sets `Selected` on
"Failed sessions only" (`replication_monitor_panel_draw.go` `refreshToolLabels`)
and Picture sets it on "Fit" (`picture_panel_input.go` `refreshToolLabels`),
but neither paints it, so the state is invisible on the toolbar.

Proposed API:

```go
// Draw paints the row laid out in r: the bar, every shown cell (dimmed when
// disabled, the selected look when Selected), then More, dimmed when moreDim.
// disabled nil reads each cell's Disabled field, as Run and OverflowItems do.
func (tr *ToolRow) Draw(s tcell.Screen, r core.Rect, disabled func(int) bool, moreDim bool)
```

Stays with the host: anything drawn in the bar around the cells — Activity
Monitor's prefix and collection state, Picture's info text after `Layout`'s
returned column, Log Viewer's filter field. These are drawn after `Draw` into
columns the cells do not occupy. `viewerPanel.drawToolCells` reduces to a call
or goes away (the decision record keeps the rest of `viewerPanel` as is).

Tests: tuikit — a cell with `Selected` is painted in the selected style, a
disabled one dimmed, `nil` disabled reads the field, More dimmed only on
`moreDim`. gossms — the existing toolbar tests keep passing.

## 2. Chart readout box — `chart_tooltip.go` → `tuikit/charts` — DONE 2026-11-19

`charts.ReadoutRow`, `ReadoutSize`, `PlaceReadout` and `DrawReadout` are in
tuikit `charts/readout.go` (tests in `charts/readout_test.go`); Activity
Monitor and the Details disk-usage strip call them and `chart_tooltip.go` is
gone. Driven live on win10cli: a readout pinned near the right edge of each
flips left; Activity Monitor's flips above to keep its time callout clear.

82 lines, no App or gosmo dependency, drawn entirely with `core` and `theme`
(`StyleTooltip`, `StyleTooltipBorder`). Two users: Activity Monitor
(`activity_monitor_tooltip.go`) and the Object Explorer Details disk-usage
strip (`detail_browser_charts.go`). Any tuikit chart host needs the same
"what is the colour under the pointer worth" box.

Moves as e.g. `charts.ReadoutRow` (label, value, colour), `ReadoutSize`,
`PlaceReadout` (flip to the side that fits, clamp into view, the keep-out
row) and `DrawReadout`, with the placement comment intact. `charts` already
imports only `core`/`theme`; no new layering edge. The tooltip state and pin
identity (a sample in time vs a chart panel) stay in each gossms panel.

## 3. Focus-ring helpers — `dialog_common.go` → `dialogs/focus_ring.go`

The helpers over a dialog's flat `[]core.Focusable` focus ring are generic,
and pair with what `dialogs.ModalDialog` already offers
(`DrawButtonsGated` has no keyboard counterpart in tuikit today):

- move: `setFocusIn`, `indexOfFocusable`, `focusedClipboardTarget`,
  `clipboardFieldAt` + `fieldHit`, `nextFocus`/`prevFocus`, `scrollToShow`,
  `buttonRowKey` + `stepButton`, `forwardReleaseToFocusedField`;
- stay: `runProgressButton` and `progressModeKey` (take `*Task`),
  `confirmDiscardChanges` (App).

Layering: `dialogs → widgets` is already allowed; nothing new. Reword the
comments that name gossms callers (`App.activeClipboardTarget`, "the seven
inline sites", the Connect/Backup/Restore list). Optional follow-up, separate
change: tuikit's own `FileDialog`/`PromptDialog`/`TypedConfirmDialog` adopt
the helpers instead of their private `setFocus`.

Tests move with the code (the `dialog_common_test.go` cases for these
functions).

## 4. Clipboard backend → new package `tuikit/clipboard`

The biggest move and the most useful to other tuikit hosts: every terminal app
needs the native clipboard with an OSC 52 fallback, and bracketed paste.

Move (synchronous, no goroutines — tuikit's § Threading rule holds; the host
still runs them off the UI goroutine):

- `os_clipboard.go` — tool detection (`detectClipboardMethod` with its
  `lookPath` seam), `Write`/`Read` via the platform tool;
- `os_clipboard_windows.go` — the Win32 `CF_UNICODETEXT` path;
- `terminalClipboardLikely` (`clipboard.go`) — DA1 attribute 52 or tmux by
  XTVERSION;
- the bracketed-paste key buffer (`bufferPastedKey`'s CR/LF/`KeyCtrlJ`
  rules) as a small type, e.g. `PasteBuffer{Begin, Key(ev), End() string}`;
- `stripBOMs`.

Stays in gossms (`clipboard.go`): the App wiring — `clipWriteSeq`/
`clipWriteMu` newest-write-wins, `safego`/`postAndWake`, the status
messages, `activeClipboardTarget`, the target/token check in `pasteInto`,
the clipboard menus.

Consequences:

- new package entry in `tuikitLayers` and tuikit `README.md` (package map and
  § Dependency direction): stdlib + tcell, imports nothing of tuikit, or `core`
  at most;
- goSSMS `CLAUDE.md`'s `runtime.GOOS` paragraph changes — gossms would have no
  GOOS-suffixed file left; the Win32 note moves to tuikit's docs;
- the `ARCHITECTURE.md` § Package map rows for `os_clipboard*.go` go;
- verify on Windows with `scripts/wintest.sh` and by hand on win10cli
  (copy/paste from the editor and a dialog field).

## 5. Pick-one menu builder — `qsMenuItems` → `controls.ChoiceItems` — DONE 2026-11-19

`controls.ChoiceItems[T]` and the `controls.ChoiceMark` constant ("• ") are in
tuikit `controls/choice_items.go` (tests in `controls/choice_items_test.go`);
`ToolRow.OverflowItems` uses the constant. `qsMenuItems` is gone: its nine
Query Store / Replication Monitor callers call `ChoiceItems`, and so does the
Log Viewer family menu. Two hand-rolled sites don't fit and only use
`ChoiceMark`: Plan Compare's statement menu (per-item gating, labels from the
index) and the Log Viewer file menu (bullets every merged file, not one).
Driven live on win10cli: Replication Monitor's Auto refresh menu (bullet
follows a new pick), Log Viewer's Log menu (bullet follows the family switch)
and File menu.

`qsMenuItems[T comparable](values, current, label, choose)`
(`query_store_panel_toolbar.go`, 9 callers across Query Store and Replication
Monitor) builds a menu marking the current value with "• " — the marker tuikit
already uses in `ToolRow.OverflowItems`. Move as `controls.ChoiceItems[T]`.
Check whether the three hand-rolled "• " sites fit it too:
`plan_compare_panel.go` (statement menu) and `log_viewer_files.go` (two
menus).

## Considered, not recommended

- **`latest.go`** — no UI dependency, but it is a context-cancel lifecycle for
  host loads; tuikit's § Threading leaves async lifecycles to the host, and it
  would be tuikit's first non-UI type.
- **`viewerPanel`** (`viewer_panel.go`) — wires App config (`copyWithStatus`,
  `MaxCellLength`); the decision record keeps LogViewer/XEventViewer divergent
  beyond it. Item 1 shrinks it further.
- **Find/Replace, Status History, Key Diagnostics dialogs** — generic-ish but
  need a new `dialogs → controls/editor` edge; Find/Replace's target
  resolution (query panel vs XEvent viewer) is App logic. Low value.
- **Help dialog** — its line-wrapping helpers are generic, the content is
  goSSMS's. Low value.
- **Grid sort** (`query_panel_sort.go`, `detail_browser_sort.go`) — compared by
  SQL Server type; DataGrid deliberately leaves reordering to the host.
- **`picture_panel*.go`** — depends on gotuipic, which tuikit may not import.
- **`planview`, `dashboard`, `sqledit`, `sqlparse`, `explorer_drag.go`** —
  SQL Server or goSSMS specific (the dashboard split is in
  `docs/decisions.md`).
- **`prop_grid_helpers.go`** (`redrawGrid`, `resetGrid`, `wireGridEditor`,
  `listAutoAlign`) — one-line wrappers or tied to the Details sort keys.

## Execution

Order: 1, 2, 5, 3, then 4. For each item:

1. Change tuikit first; `go build ./... && go vet ./... && gofmt -l .`,
   `go test ./...`, `staticcheck ./...` clean there.
2. Move gossms onto it (the `replace` directive is active); remove the gossms
   copy; `go test ./...`, `staticcheck ./...`, `deadcode -test ./...` clean.
3. Update in the same change: `ARCHITECTURE.md` § Package map,
   `docs/ui-rules.md` where it names the moved helper, tuikit `README.md`
   package map.
4. Drive the built binary (`docs/testing.md`):
   - item 1: every toolbar — Activity Monitor, Query Store, Replication
     Monitor, Picture, Log Viewer, XEvent Viewer, Plan Compare — at a narrow
     width so More appears; Replication's "Failed sessions only" and
     Picture's "Fit" now show selected;
   - item 2: pin a readout on Activity Monitor and on the disk-usage strip,
     near each edge;
   - item 3: Tab/Backtab and the button rows in Connect, Backup, Restore,
     Find/Replace, Log Search; right-click a dialog field;
   - item 4: copy/paste in editor and dialog fields locally, over ssh (OSC 52),
     in tmux, and on Windows; a bracketed paste containing CRLF.
