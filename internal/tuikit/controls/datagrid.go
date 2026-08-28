package controls

import (
	"slices"
	"strconv"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// RowSource
// ---------------------------------------------------------------------------

// RowSource supplies the grid's rows. SetData wraps a materialized [][]string;
// a paged or streaming result implements this and uses SetSource.
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

// colWidthSampleRows caps how many rows computeColWidths inspects, so sizing
// columns never scans a million-row source.
const colWidthSampleRows = 200

// defaultMaxCellWidth caps the width a column is *given* unless SetMaxCellWidth
// overrides it (only the query-results grid does, from the Options dialog's
// "max default cell length"). A separator drag may still widen a column past
// it.
const defaultMaxCellWidth = 40

// minResizeWidth is the narrowest a column can be dragged to: a padding column
// each side of the text plus the separator.
const minResizeWidth = 4

// resizeDoubleClickInterval is how close two presses on one separator must fall
// to count as the double-click that restores its default width. tcell reports
// presses, not clicks, so the timing is done here.
const resizeDoubleClickInterval = 500 * time.Millisecond

// cellViewerW and cellViewerLines size the built-in "full cell content" popup,
// which is centred on the screen rather than the grid's rect so it reads as a
// modal even on a small grid.
const cellViewerW = 60
const cellViewerLines = 8

// viewerDimNum/viewerDimDen fade the screen behind the popup toward
// DialogOverlay. Duplicates dialogs.ModalDialog's dialogDimNum/Den, since
// controls must not depend on dialogs.
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

	// widthsDirty defers a RefreshColumnWidths request to the next Draw.
	widthsDirty bool

	selRow    int
	scrollRow int
	scrollCol int
	status    string
	active    bool

	// statusStyle overrides the status bar's default GridHeader/TextDim look
	// when hasStatusStyle is set — the query-results grid's yellow bar.
	statusStyle    tcell.Style
	hasStatusStyle bool

	// cellCursor is opt-in. Disabled (the default), Left/Right scroll wide
	// grids horizontally and whole rows highlight; enabled, they move selCol
	// and Draw highlights that one cell — property-sheet toggle grids
	// (permission Grant/Deny, role-membership checkboxes) use it.
	cellCursor bool
	selCol     int

	// selAnchorRow/selAnchorCol mark the fixed corner of a block selection,
	// selRow/selCol the moving one. blockSelecting is set by Shift+Arrow or a
	// drag, cleared by a plain arrow or a fresh non-Shift click. Cell-cursor
	// mode only, and only on a read-only grid (OnActivateCell == nil), so an
	// editable toggle grid's click-drag keeps painting the cells it crosses.
	blockSelecting             bool
	selAnchorRow, selAnchorCol int

	// markedRows is the discontiguous selection Ctrl+click builds — the rows
	// picked out one at a time, which no anchor/cursor rectangle can describe.
	// marking says the grid is in that mode, and is not the same as len()>0: a
	// Ctrl+click that unpicks the last row leaves an empty selection, which is
	// a selection of nothing rather than a fall back to the cursor's row.
	// Cleared by anything that is not a Ctrl+click, as a file manager's list
	// does — a plain or Shift+click, an arrow key, a new row set.
	markedRows map[int]bool
	marking    bool

	// mouseDragging distinguishes a fresh Button1 press (arm a new selection
	// anchor) from a continued drag (keep the anchor, move the cursor).
	mouseDragging bool

	// sbDragging latches a scrollbar-thumb drag from press to release: while
	// set, every Button1 event goes to the scrollbar regardless of x, never
	// falling through to row/cell hit-testing.
	sbDragging bool

	// colResizing latches a column-separator drag. resizeStartX/W record the
	// grab point and the width there, so each motion resolves against the
	// original grab instead of accumulating.
	colResizing  bool
	resizeCol    int
	resizeStartX int
	resizeStartW int

	// lastSepPressCol/lastSepPressAt time consecutive presses on one separator
	// to recognise the default-width-restoring double-click. -1 = no press
	// yet.
	lastSepPressCol int
	lastSepPressAt  time.Time

	// sbDraggingH is sbDragging's counterpart for the horizontal scrollbar on
	// the status row — a separate latch, since the bars sit on different
	// edges.
	sbDraggingH bool

	// toggleRow/toggleCol record the last cell a toggle grid's click-drag
	// activated, so tcell's Button1 resends at the same cell don't re-fire
	// OnActivateCell. A drag onto another cell still activates it.
	toggleRow, toggleCol int

	// showRowNumbers prepends a non-selectable, unlabelled row-number column —
	// used by the query-results grid.
	showRowNumbers bool

	// maxCellWidth overrides defaultMaxCellWidth for computeColWidths's upper
	// clamp; 0 means "use the default".
	maxCellWidth int

	// colWidthOverride holds each column's dragged width, or 0 for the computed
	// default. It survives computeColWidths, so a resized column keeps its
	// width across a bounds change or a backfill's RefreshColumnWidths, and
	// bypasses maxCellWidthOrDefault, which caps only the width a column is
	// *given*.
	colWidthOverride []int

	// fillLastColumn stretches the last column past its content width (and past
	// maxCellWidthOrDefault) to consume the rect's remaining width.
	fillLastColumn bool

	// ctxMenu is the right-click "Show Value" menu — offered on any cell whose
	// grid doesn't define OnActivateCell, never on an editable one.
	ctxMenu ContextMenu

	// viewOpen and viewEditor back the "full cell content" popup opened by
	// ctxMenu's "Show Value": a read-only, word-wrapped Editor over the cell's
	// untruncated text, navigable and copyable. Only Escape or its "[ Close ]"
	// button dismiss it — a stray click must not lose an in-progress
	// selection.
	viewOpen   bool
	viewHeader string
	viewEditor *Editor

	// viewCloseRect is the "[ Close ]" button's screen position, laid out by
	// DrawOverlay (the popup is screen-centred, so it isn't known before) and
	// hit-tested by HandleMouse.
	viewCloseRect core.Rect

	// viewDismissing latches from the press on that button to the release: the
	// press closes the popup, and tcell's Button1 resends while held would
	// otherwise land on the grid underneath as fresh cell clicks.
	viewDismissing bool

	// OnSelectRow fires whenever the selected row changes (keyboard or click).
	// OnActivateCell fires on Enter/Space, or a cell click, while cell-cursor
	// mode is enabled. Leave it nil for a read-only grid — right-click then
	// offers the built-in content viewer instead.
	OnSelectRow    func(row int)
	OnActivateCell func(row, col int)

	// OnCopyRequest receives clipboard-ready text from the menu's "Copy", "Copy
	// All" and "Copy All with Headers". controls may not reach the OS
	// clipboard, so the host wires it; nil hides those items.
	OnCopyRequest func(text string)

	// OnShowValue gets first refusal of "Show Value", handed the cell's column
	// index, name and full text; true means the host displayed it and the
	// built-in popup stays closed, false or nil opens the popup. The index
	// disambiguates duplicate column names — QueryPanel uses it to find the
	// declared type when routing an XML cell to a new query tab.
	OnShowValue func(col int, column, value string) bool

	// OnMenuItems contributes the host's own entries to the cell context menu,
	// appended below a divider after the built-in Copy / Show Value ones. It is
	// asked each time the menu opens, so an item can be built from — and gated
	// on — whatever the selection is at that moment. A grid the host has given
	// no actions leaves it nil and the menu is the built-in pair.
	OnMenuItems func() []MenuItem
}

// NewDataGrid creates a DataGrid.
func NewDataGrid() *DataGrid {
	return new(DataGrid{status: "Ready", rows: SliceRowSource(nil), lastSepPressCol: -1})
}

// SetBounds positions the grid and recomputes column widths, so a
// fillLastColumn grid's last column tracks the new width. Content-based widths
// don't depend on rect.W, so every other grid gets the same ones back.
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
	// Row indices from the old set name different objects in this one.
	g.ClearMarkedRows()
	// The dragged widths belong to the old columns; this result set's column 2
	// is unrelated to the last one's.
	g.colWidthOverride, g.colResizing = nil, false
	// Only Escape or Close dismiss the viewer, so leaving it open strands it
	// over stale text while it still claims every key and mouse event.
	g.closeViewer()
	g.computeColWidths()
	g.status = strconv.Itoa(rows.Len()) + " rows"
}

// SetDataPreservingView is SetData for a change that leaves the row set alone —
// a cell toggled, an edit reverted — where the grid must not jump under the
// user. The cell cursor, the scroll position and any dragged column width all
// go back where they were. SetData discards all three, which is right only for
// a fresh result set.
//
// The order is load-bearing. Widths first, because ensureVisibleCol picks the
// horizontal offset by walking colWidths and would otherwise walk the
// recomputed defaults. Scroll before the selection, because SetSelectedCell
// ends in ensureVisible: from the restored scroll it has nothing to do, from
// the zero SetSource left behind it drags the selected row to the viewport
// edge.
func (g *DataGrid) SetDataPreservingView(columns []string, rows [][]string) {
	selRow, selCol := g.SelectedCell()
	scrollRow, scrollCol := g.scrollRow, g.scrollCol
	widths := g.ColumnWidthOverrides()
	g.SetData(columns, rows)
	for i, w := range widths {
		if w > 0 {
			g.SetColumnWidth(i, w)
		}
	}
	g.SetScroll(scrollRow, scrollCol)
	g.SetSelectedCell(selRow, selCol)
}

// RefreshColumnWidths recomputes column widths without resetting scroll or
// selection, unlike SetData/SetSource. Call it after mutating row cells in
// place — a progressive backfill, where SetData on every update would throw the
// user's scroll away.
//
// Deferred to the next Draw, the first moment the widths matter: recomputing on
// the spot rescans up to colWidthSampleRows rows per call, where a burst of
// rows between two frames needs one rescan.
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
	g.ClearMarkedRows()
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

// ScrollRow returns the index of the topmost visible data row — view state a
// host reads rather than drives, so it can tell whether an event it meant to
// swallow reached the grid. See QueryPanel's mid-drag wheel handling.
func (g *DataGrid) ScrollRow() int { return g.scrollRow }

// ScrollCol returns the index of the leftmost visible column. A grid without a
// cell cursor reports nothing through SelectedCell, so this is how a host tells
// a Left/Right that scrolled the grid from one it ignored — see
// propsheet.GridRow.HandleKey.
func (g *DataGrid) ScrollCol() int { return g.scrollCol }

// SetScroll restores the scroll position (both clamped) after a
// SetData/SetSource that deliberately discarded it — the counterpart of
// SetColumnWidth/ColumnWidthOverrides, with the same one caller, redrawGrid.
//
// Not for driving the view: scrolling is the grid's own response to a wheel, a
// drag, or a selection moving out of sight, and ensureVisible undoes on the
// next selection change anything a host sets. SetSelectedRow/SetSelectedCell
// are how a host asks for a row to be shown.
//
// Call it before restoring the selection, not after: SetSelectedCell ends in
// ensureVisible, which from the restored scroll has nothing to do and from the
// zero SetSource left behind drags the selected row to the viewport edge.
func (g *DataGrid) SetScroll(row, col int) {
	// Bounded by the last row that can sit at the *top* of the viewport, the
	// bound the wheel uses — not by the last row. Clamping to rows.Len()-1
	// would let a redraw that shrank the list leave a two-row grid scrolled one
	// row down, blank line above its only visible row. Columns keep
	// scrollColBy's bound: varying widths give no "last column that fits".
	g.scrollRow = core.Clamp(row, 0, max(0, g.rows.Len()-(g.rect.H-3)))
	g.scrollCol = core.Clamp(col, 0, max(0, len(g.columns)-1))
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

// SetSelectedCell sets the selected row and column (both clamped) and scrolls
// them into view — the cell-cursor analogue of SetSelectedRow, for a caller
// (e.g. ToggleGridRow.activateCell) that rebuilt the rows via SetData/SetSource
// and lost selCol with them. Does not fire OnSelectRow.
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

// SelectedRows returns the rows the selection covers, ascending: the rows
// Ctrl+click marked while a discontiguous selection is in force, otherwise the
// rows the anchor/cursor rectangle spans — which is the one cursor row when
// nothing is extended.
//
// A host acting on whole objects reads this rather than SelectionBounds: the
// bounds describe a rectangle, and a Ctrl+click selection is not one.
func (g *DataGrid) SelectedRows() []int { return g.selectedRows() }

func (g *DataGrid) selectedRows() []int {
	if g.marking {
		rows := make([]int, 0, len(g.markedRows))
		for r := range g.markedRows {
			rows = append(rows, r)
		}
		slices.Sort(rows)
		return rows
	}
	r0, _, r1, _ := g.selectionBounds()
	if r0 < 0 || r1 < r0 {
		return nil
	}
	rows := make([]int, 0, r1-r0+1)
	for r := r0; r <= r1; r++ {
		rows = append(rows, r)
	}
	return rows
}

// ClearMarkedRows drops any Ctrl+click selection, leaving the cursor where it
// is.
func (g *DataGrid) ClearMarkedRows() {
	g.markedRows, g.marking = nil, false
}

// markRow adds the clicked row to the discontiguous selection, or removes it
// when it is already in — the toggle Ctrl+click means everywhere.
//
// The selection in force at the time is folded in first, so Shift-selecting a
// run and then Ctrl+clicking one more row keeps the run. That includes the lone
// cursor row: this grid always highlights one, and every host that acts on the
// selection acts on that row when nothing else is picked, so a Ctrl+click that
// dropped it would deselect a row the user can see is selected.
func (g *DataGrid) markRow(row int) {
	if !g.marking {
		g.markedRows = map[int]bool{}
		for _, r := range g.selectedRows() {
			g.markedRows[r] = true
		}
		g.marking = true
	}
	if g.markedRows[row] {
		delete(g.markedRows, row)
		return
	}
	g.markedRows[row] = true
}

// rowMarked reports whether row is in a discontiguous selection.
func (g *DataGrid) rowMarked(row int) bool { return g.marking && g.markedRows[row] }

// CellCursorEnabled reports whether cell-cursor mode is on.
func (g *DataGrid) CellCursorEnabled() bool { return g.cellCursor }

// SetRowNumbers shows or hides a non-selectable, unlabelled row-number column
// pinned left of the data columns. Off by default.
func (g *DataGrid) SetRowNumbers(v bool) { g.showRowNumbers = v }

// SetMaxCellWidth overrides the bound computeColWidths clamps every column's
// *default* width to — a cap on how wide content alone may make a column, not
// on the column, which a separator drag widens past it freely. n counts display
// columns including the padding either side of the text, so a
// maxCellLength-character content cap passes maxCellLength+2. n <= 0 restores
// defaultMaxCellWidth.
func (g *DataGrid) SetMaxCellWidth(n int) { g.maxCellWidth = n }

// SetFillLastColumn stretches the last column to fill the grid's remaining
// width — for a two-column Property/Value view, where a content-clamped Value
// column wastes most of a wide panel. Off by default, since on a grid of
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

// Row returns row i's raw cells, or nil if i is out of range — the data behind
// a selection rather than what is rendered.
func (g *DataGrid) Row(i int) []string {
	if i < 0 || i >= g.rows.Len() {
		return nil
	}
	return g.rows.Row(i)
}

// ColumnIndex returns the position of the column named name, or -1 if the grid
// has no such column. For a host that has to address a cell by column name
// rather than by position — the grids here are built from whatever a loader
// returned, so an index is only ever right by coincidence.
func (g *DataGrid) ColumnIndex(name string) int {
	return slices.Index(g.columns, name)
}

// computeColWidths sizes columns from their header plus up to
// colWidthSampleRows data rows, so a huge result set doesn't make SetSource
// slow.
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
	// A last column dragged to a width of its own keeps it; stretching it back
	// out to the rect would make the drag look ignored.
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

// setOverrideWidth records column i's dragged width (0 clears it), growing the
// override slice on demand to match the column count.
func (g *DataGrid) setOverrideWidth(i, w int) {
	if i < 0 || i >= len(g.colWidths) {
		return
	}
	for len(g.colWidthOverride) < len(g.colWidths) {
		g.colWidthOverride = append(g.colWidthOverride, 0)
	}
	g.colWidthOverride[i] = w
}

// ColumnWidthOverrides returns each column's dragged width, 0 for one still at
// its computed default — the inverse of SetColumnWidth, and the only way to
// carry drags across a SetData that discards them (see redrawGrid). The slice
// is a copy, as long as the column count at the time of the call.
func (g *DataGrid) ColumnWidthOverrides() []int {
	out := make([]int, len(g.colWidths))
	copy(out, g.colWidthOverride)
	return out
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

// growLastColumnToFill widens the last column to consume the width left once
// every other column is accounted for, bypassing maxCellWidthOrDefault: that
// cap keeps an ordinary column from swallowing the grid, which here is the
// point.
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

// gutterWidth returns the row-number column's on-screen width — the highest row
// number plus a padding column each side — or 0 unless SetRowNumbers is on.
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
