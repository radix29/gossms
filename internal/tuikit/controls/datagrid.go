package controls

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// RowSource
// ---------------------------------------------------------------------------

// RowSource decouples DataGrid from any one in-memory shape for its data.
// Today every caller hands it a fully materialized [][]string (via SetData,
// which wraps it in SliceRowSource below), but a future paged or streaming
// query result — rows fetched a page at a time from a server-side cursor —
// can implement this directly and hand it to SetSource without DataGrid
// itself changing.
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

// colWidthSampleRows caps how many rows computeColWidths inspects. A source
// with millions of rows would make every SetSource call scan all of them
// just to size columns; SSMS itself only samples for the same reason.
const colWidthSampleRows = 200

// defaultMaxCellWidth is the column-width cap used when a grid doesn't opt
// into a configurable one via SetMaxCellWidth (e.g. property-page grids —
// only the query-results grid ties this to the Options dialog's "max cell
// length" setting).
const defaultMaxCellWidth = 40

// cellViewerW and cellViewerLines size the built-in "full cell content"
// popup (see openViewer/DrawOverlay): a fixed-width box showing a
// read-only, word-wrapped Editor 4 lines high, matching the spec. It's
// centred on the whole screen, not just the grid's own rect, so it reads
// as a proper modal even when the grid is small.
const cellViewerW = 60
const cellViewerLines = 8

// viewerDimNum/viewerDimDen fade the screen behind the popup toward
// DialogOverlay; 3/5 leaves it at ~40% of its own colour. Mirrors
// dialogs.ModalDialog's own dialogDimNum/Den, duplicated here since
// controls doesn't depend on dialogs (see tuikit/README.md).
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
	selRow    int
	scrollRow int
	scrollCol int
	status    string
	active    bool

	// statusStyle overrides the status bar's default GridHeader/TextDim
	// look when hasStatusStyle is set (see SetStatusStyle) — used by the
	// query-results grid to match SSMS's yellow execution-status bar
	// without changing the look of every other DataGrid in the app.
	statusStyle    tcell.Style
	hasStatusStyle bool

	// cellCursor and selCol are opt-in: when disabled (the default), Left/
	// Right scroll wide grids horizontally as before and only whole rows
	// are ever highlighted. When enabled (SetCellCursor(true)), Left/Right
	// move a per-cell cursor instead and Draw highlights that single cell —
	// used by property-sheet toggle grids (permission Grant/Deny columns,
	// role-membership checkboxes, …).
	cellCursor bool
	selCol     int

	// selAnchorRow/selAnchorCol mark the fixed corner of a multi-cell block
	// selection; selRow/selCol (the cell cursor above) mark the moving
	// corner. blockSelecting is true while such a selection is active — set
	// by Shift+Arrow or a mouse drag (see HandleKey/HandleMouse), cleared by
	// a plain arrow key or a fresh, non-Shift click. Only meaningful, like
	// selCol, in cell-cursor mode; gated in both places to a read-only grid
	// (OnActivateCell == nil) so an editable toggle grid's click-drag keeps
	// activating every cell it passes over, exactly as before this feature
	// existed, instead of drawing a selection block.
	blockSelecting             bool
	selAnchorRow, selAnchorCol int

	// mouseDragging distinguishes a fresh Button1 press (arm a new
	// selection anchor) from a continued drag (keep the anchor, move the
	// cursor) — mirrors Editor's own field of the same name and purpose.
	mouseDragging bool

	// toggleRow/toggleCol record the last cell an editable toggle grid's
	// click-drag activated (see blockSelecting's doc comment above), so a
	// resent Button1 event at that same cell — tcell resends on every
	// cursor motion while the button stays down, even without the mouse
	// actually leaving the cell — doesn't call OnActivateCell a second
	// time for one physical, stationary click. A drag that moves to a
	// genuinely different cell still activates it, preserving the
	// paint-as-you-drag behavior.
	toggleRow, toggleCol int

	// showRowNumbers prepends a non-selectable, unlabelled row-number
	// column (see SetRowNumbers) — used by the query-results grid.
	showRowNumbers bool

	// maxCellWidth overrides defaultMaxCellWidth for computeColWidths's
	// upper clamp (see SetMaxCellWidth); 0 means "use the default".
	maxCellWidth int

	// fillLastColumn stretches the last column past its content-based width
	// (and past maxCellWidthOrDefault's clamp) to consume the rect's full
	// remaining width, instead of leaving dead space to the right — see
	// SetFillLastColumn.
	fillLastColumn bool

	// ctxMenu is the right-click "Show Value" menu (see HandleMouse's
	// Button2 case) — offered on any cell whose grid doesn't define
	// OnActivateCell (i.e. a read-only grid), never on an editable one.
	ctxMenu ContextMenu

	// viewOpen and viewEditor back the built-in "full cell content" popup,
	// opened via ctxMenu's "Show Value" item: a read-only, word-wrapped
	// Editor showing the selected cell's untruncated text, so it can be
	// navigated, selected, and copied like any other text. Self-contained
	// so every DataGrid gets this for free with no wiring at the call site.
	viewOpen   bool
	viewHeader string
	viewEditor *Editor

	// OnSelectRow fires whenever the selected row changes (keyboard or
	// click). OnActivateCell fires on Enter/Space, or a click on a cell,
	// while cell-cursor mode is enabled. Leave it nil for a read-only grid
	// — right-click then offers the built-in content viewer instead.
	OnSelectRow    func(row int)
	OnActivateCell func(row, col int)

	// OnCopyRequest, if set, is called with clipboard-ready text whenever
	// the right-click (or Ctrl+Space) menu's "Copy" (current cell or block
	// selection), "Copy All", or "Copy All with Headers" (the row-number
	// gutter's blank header cell) is chosen. DataGrid has no OS clipboard
	// access itself — see tuikit/README's one-way dependency rule — so the
	// host app wires this to its own clipboard-write plumbing. Grids that
	// leave it nil (the default) simply don't offer these menu items, same
	// as before this feature existed.
	OnCopyRequest func(text string)
}

// NewDataGrid creates a DataGrid.
func NewDataGrid() *DataGrid {
	return new(DataGrid{status: "Ready", rows: SliceRowSource(nil)})
}

// SetBounds positions the grid. Recomputes column widths so a resize keeps
// a fillLastColumn grid's Value column matching the new width — for every
// other grid this just recomputes the same content-based widths, since
// those don't depend on rect.W.
func (g *DataGrid) SetBounds(x, y, w, h int) {
	g.rect = core.Rect{X: x, Y: y, W: w, H: h}
	g.computeColWidths()
}

// SetData populates the grid from a fully materialized slice of rows —
// the common case. It's a thin wrapper over SetSource for callers that
// don't have (or need) a custom RowSource.
func (g *DataGrid) SetData(columns []string, rows [][]string) {
	g.SetSource(columns, SliceRowSource(rows))
}

// SetSource populates the grid from any RowSource, e.g. a paged or streamed
// result that doesn't hold every row in memory at once.
func (g *DataGrid) SetSource(columns []string, rows RowSource) {
	g.columns = columns
	g.rows = rows
	g.selRow, g.selCol, g.scrollRow, g.scrollCol = 0, 0, 0, 0
	g.blockSelecting, g.mouseDragging = false, false
	g.computeColWidths()
	g.status = core.Itoa(rows.Len()) + " rows"
}

// RefreshColumnWidths recomputes column widths from the grid's current
// data without resetting scroll position or selection, unlike SetData/
// SetSource. Call after mutating row cells in place — e.g. a progressive
// background fetch backfilling columns one row at a time — where calling
// SetData again on every update would visually reset the user's scroll
// position each time.
func (g *DataGrid) RefreshColumnWidths() {
	g.computeColWidths()
}

// SetError shows an error row.
func (g *DataGrid) SetError(err error) {
	g.columns = []string{"Error"}
	g.rows = SliceRowSource{{err.Error()}}
	g.colWidths = []int{g.rect.W - 2}
	g.selRow, g.selCol, g.scrollRow = 0, 0, 0
	g.blockSelecting, g.mouseDragging = false, false
	g.status = "Error"
}

// SetStatus sets the status bar text.
func (g *DataGrid) SetStatus(msg string) { g.status = msg }

// Status returns the current status bar text.
func (g *DataGrid) Status() string { return g.status }

// SetStatusStyle overrides the status bar's background/foreground, in
// place of the theme's default GridHeader/TextDim look. Only this grid is
// affected — other DataGrids (property sheets, detail browser, …) keep
// the default unless they opt in too.
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

// SetSelectedRow sets the selected row (clamped) and scrolls it into view.
// Does not fire OnSelectRow.
func (g *DataGrid) SetSelectedRow(i int) {
	if g.rows.Len() == 0 {
		return
	}
	g.selRow = core.Clamp(i, 0, g.rows.Len()-1)
	g.ensureVisible(g.rect.H - 3)
}

// Focus sets the focused state, dimming the selection highlight when false
// (mirrors the convention other tuikit controls use for a focus ring).
func (g *DataGrid) Focus(v bool) { g.active = v }

// SetCellCursor enables or disables per-cell (rather than whole-row)
// selection. See the cellCursor field doc for what changes.
func (g *DataGrid) SetCellCursor(enabled bool) {
	g.cellCursor = enabled
	if enabled {
		g.selCol = core.Clamp(g.selCol, 0, core.Max(0, len(g.columns)-1))
	}
}

// SelectedCell returns the selected row and column. col is only meaningful
// when cell-cursor mode is enabled.
func (g *DataGrid) SelectedCell() (row, col int) { return g.selRow, g.selCol }

// SelectionBounds returns the inclusive row/col rectangle of the current
// selection — just the single active cell (SelectedCell()) when there's no
// multi-cell block selection (see Shift+Arrow / mouse-drag in HandleKey/
// HandleMouse). Exported so a host embedding the grid (and its tests) can
// tell a block selection from an ordinary single-cell one.
func (g *DataGrid) SelectionBounds() (r0, c0, r1, c1 int) { return g.selectionBounds() }

// CellCursorEnabled reports whether cell-cursor mode is on.
func (g *DataGrid) CellCursorEnabled() bool { return g.cellCursor }

// SetRowNumbers shows or hides a non-selectable, unlabelled row-number
// column pinned to the left of every data column (see gutterWidth). Off by
// default; only the query-results grid turns it on.
func (g *DataGrid) SetRowNumbers(v bool) { g.showRowNumbers = v }

// SetMaxCellWidth overrides the upper bound computeColWidths clamps every
// column to (defaultMaxCellWidth otherwise). n is a display-column count
// that already includes the 1-column padding on each side of a cell's text
// — a caller wanting a maxCellLength-character content cap should pass
// maxCellLength+2. n <= 0 restores the default.
func (g *DataGrid) SetMaxCellWidth(n int) { g.maxCellWidth = n }

// SetFillLastColumn enables or disables stretching the last column to fill
// the grid's remaining width — used for a two-column Property/Value detail
// view, where a narrow, content-clamped Value column wastes most of a wide
// panel. Off by default, since it would misalign a grid with several
// similarly-important columns (e.g. a results grid or a Databases-folder
// list) by giving the last one outsized, arbitrary width.
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

// Row returns row i's raw cells, or nil if i is out of range. Useful for
// callers that need the underlying data behind the current selection, not
// just what's rendered (e.g. building a clipboard copy of a row or cell).
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
	g.colWidths = make([]int, len(g.columns))
	for i, col := range g.columns {
		g.colWidths[i] = core.DisplayWidth(col) + 2
	}
	n := core.Min(g.rows.Len(), colWidthSampleRows)
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
	}
	if g.fillLastColumn && len(g.colWidths) > 0 {
		g.growLastColumnToFill()
	}
}

// growLastColumnToFill widens the last column to consume whatever width is
// left over once the rect and every other column's own width are accounted
// for, bypassing maxCellWidthOrDefault's clamp (that cap exists to keep an
// ordinary column from swallowing the grid; here it's exactly the point).
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
	return core.DisplayWidth(core.Itoa(core.Max(1, g.rows.Len()))) + 2
}

// selectionBounds returns the inclusive row/col rectangle of the current
// selection: just the single active cell (selRow, selCol) when there's no
// multi-cell block selection.
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
