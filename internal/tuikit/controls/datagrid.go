package controls

import (
	"strconv"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// RowSource
// ---------------------------------------------------------------------------

// RowSource decouples DataGrid from any one in-memory shape for its data.
// Callers hand it a materialized [][]string via SetData; a paged or streaming
// result can implement this directly and go through SetSource instead.
type RowSource interface {
	// Len returns the number of rows.
	Len() int
	// Row returns row i's cells. i is always in [0, Len()).
	Row(i int) []string
}

// SliceRowSource adapts a plain [][]string to RowSource.
type SliceRowSource [][]string

func (s SliceRowSource) Len() int           { return len(s) }
func (s SliceRowSource) Row(i int) []string { return s[i] }

// colWidthSampleRows caps how many rows computeColWidths inspects, so a source
// with millions of rows doesn't make every SetSource scan all of them just to
// size columns. SSMS samples for the same reason.
const colWidthSampleRows = 200

// defaultMaxCellWidth is the column-width cap for a grid that doesn't opt into
// a configurable one via SetMaxCellWidth (only the query-results grid does,
// tying it to the Options dialog's "max default cell length"). It bounds the
// width a column is *given*, not the width it may have: a separator drag can
// widen a column past it.
const defaultMaxCellWidth = 40

// minResizeWidth is the narrowest a column can be dragged to — one column of
// padding each side of the text plus the separator, below which there is no
// room for content at all.
const minResizeWidth = 4

// resizeDoubleClickInterval is how close two presses on the same separator
// must fall to count as the double-click that restores a column's default
// width. tcell reports presses, not clicks, so the timing is done here.
const resizeDoubleClickInterval = 500 * time.Millisecond

// cellViewerW and cellViewerLines size the built-in "full cell content" popup.
// It is centred on the whole screen, not the grid's rect, so it reads as a
// modal even when the grid is small.
const cellViewerW = 60
const cellViewerLines = 8

// viewerDimNum/viewerDimDen fade the screen behind the popup toward
// DialogOverlay; 3/5 leaves it at ~40% of its own colour. Mirrors
// dialogs.ModalDialog's dialogDimNum/Den, duplicated because controls must
// not depend on dialogs.
const viewerDimNum, viewerDimDen = 3, 5

// ---------------------------------------------------------------------------
// DataGrid
// ---------------------------------------------------------------------------

// DataGrid renders a scrollable, column-aligned tabular dataset.
type DataGrid struct {
	rect      core.Rect
	columns   []string
	rows      RowSource
	colWidths []int

	// widthsDirty defers a RefreshColumnWidths request to the next Draw
	// instead of recomputing on the spot — see there.
	widthsDirty bool

	selRow    int
	scrollRow int
	scrollCol int
	status    string
	active    bool

	// statusStyle overrides the status bar's default GridHeader/TextDim look
	// when hasStatusStyle is set — used by the query-results grid to match
	// SSMS's yellow execution-status bar without changing every other
	// DataGrid.
	statusStyle    tcell.Style
	hasStatusStyle bool

	// cellCursor and selCol are opt-in. Disabled (the default), Left/Right
	// scroll wide grids horizontally and only whole rows highlight. Enabled,
	// Left/Right move a per-cell cursor and Draw highlights that single cell —
	// used by property-sheet toggle grids (permission Grant/Deny columns,
	// role-membership checkboxes, …).
	cellCursor bool
	selCol     int

	// selAnchorRow/selAnchorCol mark the fixed corner of a multi-cell block
	// selection; selRow/selCol the moving corner. blockSelecting is true while
	// one is active — set by Shift+Arrow or a mouse drag, cleared by a plain
	// arrow key or a fresh non-Shift click. Cell-cursor mode only, and gated
	// to a read-only grid (OnActivateCell == nil) so an editable toggle grid's
	// click-drag keeps painting every cell it passes over.
	blockSelecting             bool
	selAnchorRow, selAnchorCol int

	// mouseDragging distinguishes a fresh Button1 press (arm a new selection
	// anchor) from a continued drag (keep the anchor, move the cursor).
	mouseDragging bool

	// sbDragging latches a scrollbar-thumb drag from press to release.
	// Separate from mouseDragging: once set, HandleMouse gives every Button1
	// event to the scrollbar regardless of x, never falling through to
	// row/cell hit-testing.
	sbDragging bool

	// colResizing latches a column-separator drag the way sbDragging does for
	// the scrollbar. resizeStartX/W record the grab point and the column's
	// width there, so each motion resolves against the original grab instead
	// of accumulating from the last one.
	colResizing  bool
	resizeCol    int
	resizeStartX int
	resizeStartW int

	// lastSepPressCol/lastSepPressAt time consecutive presses on the same
	// separator, which is how the default-width-restoring double-click is
	// recognised. -1 means "no press yet".
	lastSepPressCol int
	lastSepPressAt  time.Time

	// sbDraggingH is sbDragging's counterpart for the horizontal scrollbar
	// drawn along the status row — a separate latch since the two bars occupy
	// different edges of the grid.
	sbDraggingH bool

	// toggleRow/toggleCol record the last cell an editable toggle grid's
	// click-drag activated, so tcell's Button1 resends at that same cell don't
	// call OnActivateCell again for one stationary click. A drag onto a
	// different cell still activates it, keeping paint-as-you-drag.
	toggleRow, toggleCol int

	// showRowNumbers prepends a non-selectable, unlabelled row-number column —
	// used by the query-results grid.
	showRowNumbers bool

	// maxCellWidth overrides defaultMaxCellWidth for computeColWidths's upper
	// clamp; 0 means "use the default".
	maxCellWidth int

	// colWidthOverride holds the width each column was last dragged to by its
	// right-hand separator, or 0 for "use the computed default". These survive
	// computeColWidths — a resized column keeps its width across a bounds
	// change or a progressive backfill's RefreshColumnWidths — and bypass
	// maxCellWidthOrDefault, which caps the width a column is *given*, not the
	// width the user may drag it to.
	colWidthOverride []int

	// fillLastColumn stretches the last column past its content-based width
	// (and past maxCellWidthOrDefault's clamp) to consume the rect's remaining
	// width instead of leaving dead space to the right.
	fillLastColumn bool

	// ctxMenu is the right-click "Show Value" menu — offered on any cell whose
	// grid doesn't define OnActivateCell, never on an editable one.
	ctxMenu ContextMenu

	// viewOpen and viewEditor back the built-in "full cell content" popup
	// opened by ctxMenu's "Show Value": a read-only, word-wrapped Editor over
	// the cell's untruncated text, so it can be navigated, selected and
	// copied. Dismissed by Escape or its "[ Close ]" button only, never by a
	// click elsewhere — the popup exists for selecting a long value, and a
	// stray click during that must not lose it.
	viewOpen   bool
	viewHeader string
	viewEditor *Editor

	// viewCloseRect is the "[ Close ]" button's screen position, laid out by
	// DrawOverlay (the popup is screen-centred, so it isn't known until then)
	// and hit-tested by HandleMouse.
	viewCloseRect core.Rect

	// viewDismissing latches from the press on that button to the release.
	// The button closes the popup on the press, but tcell resends Button1 on
	// every motion while held, and by then the popup is gone — the resends
	// would land on the grid underneath as a fresh cell click.
	viewDismissing bool

	// OnSelectRow fires whenever the selected row changes (keyboard or click).
	// OnActivateCell fires on Enter/Space, or a cell click, while cell-cursor
	// mode is enabled. Leave it nil for a read-only grid — right-click then
	// offers the built-in content viewer instead.
	OnSelectRow    func(row int)
	OnActivateCell func(row, col int)

	// OnCopyRequest, if set, is called with clipboard-ready text whenever the
	// right-click (or Ctrl+Space) menu's "Copy", "Copy All", or "Copy All with
	// Headers" is chosen. DataGrid has no OS clipboard access of its own — the
	// one-way dependency rule — so the host app wires this to its own
	// clipboard plumbing. Grids leaving it nil don't offer those items at all.
	OnCopyRequest func(text string)

	// OnShowValue, if set, gets first refusal of the "Show Value" menu item,
	// handed the cell's column index, column name and full text; true means
	// the host displayed it its own way and the built-in popup stays closed,
	// false (or nil) opens the popup. QueryPanel routes an XML cell to a new
	// highlighted query tab this way — the index is what lets it find the
	// column's declared type, since duplicate names make the name ambiguous.
	OnShowValue func(col int, column, value string) bool
}

// NewDataGrid creates a DataGrid.
func NewDataGrid() *DataGrid {
	return new(DataGrid{status: "Ready", rows: SliceRowSource(nil), lastSepPressCol: -1})
}

// SetBounds positions the grid, recomputing column widths so a resize keeps a
// fillLastColumn grid's Value column matching the new width. Every other grid
// gets the same content-based widths back, since those don't depend on rect.W.
func (g *DataGrid) SetBounds(x, y, w, h int) {
	g.rect = core.Rect{X: x, Y: y, W: w, H: h}
	g.computeColWidths()
}

// SetData populates the grid from a fully materialized slice of rows — a thin
// wrapper over SetSource for callers with no custom RowSource.
func (g *DataGrid) SetData(columns []string, rows [][]string) {
	g.SetSource(columns, SliceRowSource(rows))
}

// SetSource populates the grid from any RowSource, e.g. a paged or streamed
// result that doesn't hold every row in memory at once.
func (g *DataGrid) SetSource(columns []string, rows RowSource) {
	g.columns = columns
	g.rows = rows
	g.selRow, g.selCol, g.scrollRow, g.scrollCol = 0, 0, 0, 0
	g.blockSelecting, g.mouseDragging, g.sbDragging, g.sbDraggingH = false, false, false, false
	// The widths were dragged for a different set of columns; column 2 of
	// the next result set has nothing to do with column 2 of this one.
	g.colWidthOverride, g.colResizing = nil, false
	// Only Escape or Close dismiss the viewer, so leaving it open strands a
	// popup over stale text that still claims every key and mouse event.
	g.closeViewer()
	g.computeColWidths()
	g.status = strconv.Itoa(rows.Len()) + " rows"
}

// RefreshColumnWidths recomputes column widths from the grid's current data
// without resetting scroll position or selection, unlike SetData/SetSource.
// Call after mutating row cells in place — a progressive background fetch
// backfilling columns row by row, where SetData on every update would reset
// the user's scroll position.
//
// Deferred to the next Draw, the first moment the new widths matter:
// recomputing on the spot rescans up to colWidthSampleRows rows per call, when
// a burst of rows between two frames needs one rescan.
func (g *DataGrid) RefreshColumnWidths() {
	g.widthsDirty = true
}

// SetError shows an error row.
func (g *DataGrid) SetError(err error) {
	g.columns = []string{"Error"}
	g.rows = SliceRowSource{{err.Error()}}
	g.colWidths = []int{g.rect.W - 2}
	g.selRow, g.selCol, g.scrollRow = 0, 0, 0
	g.blockSelecting, g.mouseDragging, g.sbDragging, g.sbDraggingH = false, false, false, false
	g.colWidthOverride, g.colResizing = nil, false
	// See SetSource: an open viewer would strand itself over stale text.
	g.closeViewer()
	g.status = "Error"
}

// SetStatus sets the status bar text.
func (g *DataGrid) SetStatus(msg string) { g.status = msg }

// Status returns the current status bar text.
func (g *DataGrid) Status() string { return g.status }

// SetStatusStyle overrides the status bar's background/foreground in place of
// the theme's default GridHeader/TextDim look, for this grid only.
func (g *DataGrid) SetStatusStyle(style tcell.Style) {
	g.statusStyle = style
	g.hasStatusStyle = true
}

// SelectedRow returns the currently selected row index, or -1 if empty.
func (g *DataGrid) SelectedRow() int {
	if g.rows.Len() == 0 {
		return -1
	}
	return g.selRow
}

// ScrollRow returns the index of the topmost visible data row. Read-only view
// state — it moves on a wheel tick, a scrollbar drag, or a selection scrolled
// into view, and there is deliberately no setter. Exists so a host can assert
// an event it meant to swallow never reached the grid; see QueryPanel's
// mid-drag wheel handling.
func (g *DataGrid) ScrollRow() int { return g.scrollRow }

// ScrollCol returns the index of the leftmost visible column — ScrollRow's
// horizontal counterpart, read-only for the same reason. A host needs it to
// tell a Left/Right that scrolled the grid from one the grid ignored, since a
// grid without a cell cursor reports neither through SelectedCell; see
// propsheet.GridRow.HandleKey.
func (g *DataGrid) ScrollCol() int { return g.scrollCol }

// SetSelectedRow sets the selected row (clamped) and scrolls it into view.
// Does not fire OnSelectRow.
func (g *DataGrid) SetSelectedRow(i int) {
	if g.rows.Len() == 0 {
		return
	}
	g.selRow = core.Clamp(i, 0, g.rows.Len()-1)
	g.ensureVisible(g.rect.H - 3)
}

// SetSelectedCell sets the selected row and column (both clamped) and scrolls
// both into view — the cell-cursor analogue of SetSelectedRow, for a caller
// (e.g. ToggleGridRow.activateCell) that rebuilds the rows via
// SetData/SetSource, which resets selCol as well as selRow. Does not fire
// OnSelectRow.
func (g *DataGrid) SetSelectedCell(row, col int) {
	if g.rows.Len() == 0 {
		return
	}
	g.selRow = core.Clamp(row, 0, g.rows.Len()-1)
	g.selCol = core.Clamp(col, 0, max(0, len(g.columns)-1))
	g.ensureVisible(g.rect.H - 3)
	g.ensureVisibleCol()
}

// Focus sets the focused state, dimming the selection highlight when false.
func (g *DataGrid) Focus(v bool) { g.active = v }

// SetCellCursor enables or disables per-cell (rather than whole-row)
// selection. See the cellCursor field doc for what changes.
func (g *DataGrid) SetCellCursor(enabled bool) {
	g.cellCursor = enabled
	if enabled {
		g.selCol = core.Clamp(g.selCol, 0, max(0, len(g.columns)-1))
	}
}

// SelectedCell returns the selected row and column. col is only meaningful
// when cell-cursor mode is enabled.
func (g *DataGrid) SelectedCell() (row, col int) { return g.selRow, g.selCol }

// SelectionBounds returns the inclusive row/col rectangle of the current
// selection — just the active cell when there's no multi-cell block selection.
// Exported so a host embedding the grid can tell the two apart.
func (g *DataGrid) SelectionBounds() (r0, c0, r1, c1 int) { return g.selectionBounds() }

// CellCursorEnabled reports whether cell-cursor mode is on.
func (g *DataGrid) CellCursorEnabled() bool { return g.cellCursor }

// SetRowNumbers shows or hides a non-selectable, unlabelled row-number column
// pinned left of every data column. Off by default; only the query-results
// grid turns it on.
func (g *DataGrid) SetRowNumbers(v bool) { g.showRowNumbers = v }

// SetMaxCellWidth overrides the upper bound computeColWidths clamps every
// column's *default* width to — a cap on how wide content alone may make a
// column, not on the column: a separator drag widens it past this freely. n is
// a display-column count including the 1-column padding on each side of the
// text, so a maxCellLength-character content cap passes maxCellLength+2.
// n <= 0 restores defaultMaxCellWidth.
func (g *DataGrid) SetMaxCellWidth(n int) { g.maxCellWidth = n }

// SetFillLastColumn stretches the last column to fill the grid's remaining
// width — for a two-column Property/Value view, where a content-clamped Value
// column wastes most of a wide panel. Off by default: on a grid of
// similarly-important columns it gives the last one outsized width.
func (g *DataGrid) SetFillLastColumn(v bool) {
	g.fillLastColumn = v
	g.computeColWidths()
}

// maxCellWidthOrDefault returns the effective column-width clamp.
func (g *DataGrid) maxCellWidthOrDefault() int {
	if g.maxCellWidth > 0 {
		return g.maxCellWidth
	}
	return defaultMaxCellWidth
}

// Row returns row i's raw cells, or nil if i is out of range — the underlying
// data behind a selection rather than what's rendered.
func (g *DataGrid) Row(i int) []string {
	if i < 0 || i >= g.rows.Len() {
		return nil
	}
	return g.rows.Row(i)
}

// computeColWidths sizes columns from their header plus up to
// colWidthSampleRows data rows — not every row, so a huge result set
// doesn't make SetSource itself slow.
func (g *DataGrid) computeColWidths() {
	g.widthsDirty = false
	g.colWidths = make([]int, len(g.columns))
	for i, col := range g.columns {
		g.colWidths[i] = core.DisplayWidth(col) + 2
	}
	n := min(g.rows.Len(), colWidthSampleRows)
	for r := 0; r < n; r++ {
		row := g.rows.Row(r)
		for i, cell := range row {
			if i < len(g.colWidths) {
				if w := core.DisplayWidth(cell) + 2; w > g.colWidths[i] {
					g.colWidths[i] = w
				}
			}
		}
	}
	maxW := g.maxCellWidthOrDefault()
	for i := range g.colWidths {
		g.colWidths[i] = core.Clamp(g.colWidths[i], 6, maxW)
		if w := g.overrideWidth(i); w > 0 {
			g.colWidths[i] = w
		}
	}
	// A last column the user has dragged to a width of their own keeps it —
	// stretching it back out to the rect would make the drag look ignored.
	if g.fillLastColumn && len(g.colWidths) > 0 && g.overrideWidth(len(g.colWidths)-1) == 0 {
		g.growLastColumnToFill()
	}
}

// overrideWidth returns column i's user-dragged width, or 0 if it has none.
func (g *DataGrid) overrideWidth(i int) int {
	if i < 0 || i >= len(g.colWidthOverride) {
		return 0
	}
	return g.colWidthOverride[i]
}

// setOverrideWidth records column i's dragged width (0 clears it), growing
// the override slice on demand so it always matches the column count.
func (g *DataGrid) setOverrideWidth(i, w int) {
	if i < 0 || i >= len(g.colWidths) {
		return
	}
	for len(g.colWidthOverride) < len(g.colWidths) {
		g.colWidthOverride = append(g.colWidthOverride, 0)
	}
	g.colWidthOverride[i] = w
}

// SetColumnWidth sets column i's width explicitly, as a separator drag would —
// bypassing the max-default clamp and surviving later recomputes. w <= 0
// restores the computed default.
func (g *DataGrid) SetColumnWidth(i, w int) {
	if i < 0 || i >= len(g.colWidths) {
		return
	}
	if w > 0 {
		g.setOverrideWidth(i, max(w, minResizeWidth))
	} else {
		g.setOverrideWidth(i, 0)
	}
	g.computeColWidths()
}

// ColumnWidth returns column i's current on-screen width, or 0 if i isn't a
// column.
func (g *DataGrid) ColumnWidth(i int) int {
	if i < 0 || i >= len(g.colWidths) {
		return 0
	}
	return g.colWidths[i]
}

// growLastColumnToFill widens the last column to consume whatever width is
// left once every other column is accounted for, bypassing
// maxCellWidthOrDefault's clamp — that cap keeps an ordinary column from
// swallowing the grid, which here is the point.
func (g *DataGrid) growLastColumnToFill() {
	avail := g.rect.W - g.gutterWidth()
	last := len(g.colWidths) - 1
	used := 0
	for _, w := range g.colWidths[:last] {
		used += w
	}
	if rem := avail - used; rem > g.colWidths[last] {
		g.colWidths[last] = rem
	}
}

// gutterWidth returns the on-screen width of the row-number column, sized
// to fit the highest row number plus one column of padding on each side, or
// 0 when SetRowNumbers(true) hasn't been called.
func (g *DataGrid) gutterWidth() int {
	if !g.showRowNumbers {
		return 0
	}
	return core.DisplayWidth(strconv.Itoa(max(1, g.rows.Len()))) + 2
}

// selectionBounds returns the inclusive row/col rectangle of the current
// selection: just (selRow, selCol) when no block selection is active.
func (g *DataGrid) selectionBounds() (r0, c0, r1, c1 int) {
	if !g.blockSelecting {
		return g.selRow, g.selCol, g.selRow, g.selCol
	}
	r0, r1 = g.selAnchorRow, g.selRow
	if r0 > r1 {
		r0, r1 = r1, r0
	}
	c0, c1 = g.selAnchorCol, g.selCol
	if c0 > c1 {
		c0, c1 = c1, c0
	}
	return r0, c0, r1, c1
}
