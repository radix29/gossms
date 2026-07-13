package controls

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
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

	// showRowNumbers prepends a non-selectable, unlabelled row-number
	// column (see SetRowNumbers) — used by the query-results grid.
	showRowNumbers bool

	// maxCellWidth overrides defaultMaxCellWidth for computeColWidths's
	// upper clamp (see SetMaxCellWidth); 0 means "use the default".
	maxCellWidth int

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

// SetBounds positions the grid.
func (g *DataGrid) SetBounds(x, y, w, h int) { g.rect = core.Rect{X: x, Y: y, W: w, H: h} }

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

// Draw renders the data grid. If the built-in cell-content popup is open,
// call DrawOverlay afterward — once every other widget in the same frame
// has drawn — so the popup isn't painted over.
func (g *DataGrid) Draw(s tcell.Screen) {
	core.FillRect(s, g.rect, ' ', theme.StylePanel())
	if g.rect.H < 3 {
		return
	}
	gw := g.gutterWidth()
	if gw > 0 {
		g.drawGutterCell(s, g.rect.Y, "", theme.StyleGridHeader())
	}
	g.drawRow(s, g.rect.Y, g.columns, theme.StyleGridHeader(), gw)
	sep := tcell.StyleDefault.Background(theme.Active().GridHeader).Foreground(theme.Active().GridBorder)
	core.DrawHLine(s, g.rect.X, g.rect.Y+1, g.rect.W, sep)

	r0, c0, r1, c1 := g.selectionBounds()
	dataH := g.rect.H - 3
	for row := 0; row < dataH; row++ {
		dataIdx := g.scrollRow + row
		y := g.rect.Y + 2 + row
		if dataIdx >= g.rows.Len() {
			core.FillRect(s, core.Rect{X: g.rect.X, Y: y, W: g.rect.W, H: 1}, ' ', theme.StylePanel())
			continue
		}
		style := theme.StyleGridRow()
		if dataIdx%2 == 1 {
			style = theme.StyleGridRowAlt()
		}
		if dataIdx == g.selRow && !g.cellCursor {
			style = theme.StyleGridSelected()
		}
		if gw > 0 {
			g.drawGutterCell(s, y, core.Itoa(dataIdx+1), style)
		}
		cells := g.rows.Row(dataIdx)
		g.drawRow(s, y, cells, style, gw)
		if g.cellCursor && dataIdx >= r0 && dataIdx <= r1 {
			g.drawCellSelection(s, y, cells, gw, c0, c1)
		}
	}

	// Status bar
	p := theme.Active()
	statusStyle := tcell.StyleDefault.Background(p.GridHeader).Foreground(p.TextDim)
	if g.hasStatusStyle {
		statusStyle = g.statusStyle
	}
	core.FillRect(s, core.Rect{X: g.rect.X, Y: g.rect.Y + g.rect.H - 1, W: g.rect.W, H: 1}, ' ', statusStyle)
	core.DrawTextRight(s, g.rect.X+1, g.rect.Y+g.rect.H-1, g.rect.W-2, statusStyle, g.status)

	// Scrollbar
	if g.rows.Len() > dataH && dataH > 0 {
		sbStyle := tcell.StyleDefault.Background(p.GridHeader).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		core.DrawScrollbar(s, g.rect.Right()-1, g.rect.Y+2, dataH,
			g.rows.Len(), dataH, g.scrollRow, sbStyle, sbThumb)
	}
}

// drawGutterCell renders one row-number column cell (or the blank header
// cell above it) at y. text is right-aligned; the column is styled dim
// since — unlike every data column — it's never selectable.
func (g *DataGrid) drawGutterCell(s tcell.Screen, y int, text string, style tcell.Style) {
	w := g.gutterWidth()
	p := theme.Active()
	gstyle := style.Foreground(p.TextDim)
	core.FillRect(s, core.Rect{X: g.rect.X, Y: y, W: w, H: 1}, ' ', gstyle)
	core.DrawTextRight(s, g.rect.X, y, w-1, gstyle, text)
	s.SetContent(g.rect.X+w-1, y, '|', nil, style.Foreground(p.GridBorder))
}

// drawRow renders cells starting at the grid's scrollCol-th column, at
// screen x xOffset+g.rect.X — xOffset reserves room for the row-number
// gutter (0 when it's off), and scrollCol implements horizontal scrolling:
// like scrollRow, it's a data index (how many leading columns are hidden),
// not a pixel offset, so a scrolled grid's columns still start flush left
// and column boundaries never split mid-cell.
func (g *DataGrid) drawRow(s tcell.Screen, y int, cells []string, style tcell.Style, xOffset int) {
	p := theme.Active()
	col := g.rect.X + xOffset
	for i := g.scrollCol; i < len(cells) && i < len(g.colWidths); i++ {
		cell := cells[i]
		cw := g.colWidths[i]
		if col >= g.rect.Right() {
			break
		}
		cellStyle := style
		if cell == nullCellText {
			cellStyle = style.Foreground(p.TextDim)
		}
		avail := core.Min(cw, g.rect.Right()-col)
		core.FillRect(s, core.Rect{X: col, Y: y, W: avail, H: 1}, ' ', cellStyle)
		core.DrawTextClipped(s, col+1, y, avail-2, cellStyle, core.Truncate(cell, avail-2))
		if col+cw-1 < g.rect.Right() {
			s.SetContent(col+cw-1, y, '|', nil, style.Foreground(p.GridBorder))
		}
		col += cw
	}
}

// nullCellText is the literal string query results use for a SQL NULL (see
// internal/query's formatValue) — dimmed in drawRow/drawCellSelection so a
// NULL reads visually distinct from an empty or ordinary string value.
const nullCellText = "NULL"

// drawCellSelection highlights the selected block's cells in row y (data
// row dataIdx, though this function only needs the row's own cells) —
// every column in [c0,c1] that's actually on screen (scrollCol onward). A
// single selected cell (no block selection, c0 == c1 == selCol) draws
// exactly like the old single-cell cursor this replaces.
func (g *DataGrid) drawCellSelection(s tcell.Screen, y int, cells []string, xOffset, c0, c1 int) {
	p := theme.Active()
	st := theme.StyleGridSelected()
	if !g.active {
		st = tcell.StyleDefault.Background(p.GridRowAlt).Foreground(p.TextHighlight)
	}
	col := g.rect.X + xOffset
	for i := g.scrollCol; i < len(g.colWidths); i++ {
		cw := g.colWidths[i]
		if col >= g.rect.Right() {
			break
		}
		if i >= c0 && i <= c1 {
			var cellText string
			if i < len(cells) {
				cellText = cells[i]
			}
			cellSt := st
			if cellText == nullCellText {
				cellSt = st.Foreground(p.TextDim)
			}
			avail := core.Min(cw, g.rect.Right()-col)
			core.FillRect(s, core.Rect{X: col, Y: y, W: avail, H: 1}, ' ', cellSt)
			core.DrawTextClipped(s, col+1, y, avail-2, cellSt, core.Truncate(cellText, avail-2))
			if col+cw-1 < g.rect.Right() {
				s.SetContent(col+cw-1, y, '|', nil, cellSt.Foreground(p.GridBorder))
			}
		}
		col += cw
	}
}

// selectionScreenPos returns the screen coordinates of the selected cell —
// used to position the context menu / "Show Value" popup when it's opened
// via Ctrl+Space instead of a right-click (see HandleKey). Mirrors the
// column-width walk drawCellSelection uses to place the same cell on screen.
func (g *DataGrid) selectionScreenPos() (x, y int) {
	x = g.rect.X + g.gutterWidth()
	for i := g.scrollCol; i < g.selCol && i < len(g.colWidths); i++ {
		x += g.colWidths[i]
	}
	y = g.rect.Y + 2 + (g.selRow - g.scrollRow)
	return x, y
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

// selectionContains reports whether (row, col) falls within the current
// selection — used by the right-click handler to decide whether a click
// inside an existing block selection should preserve it (for a block copy)
// rather than collapsing it to the clicked cell.
func (g *DataGrid) selectionContains(row, col int) bool {
	r0, c0, r1, c1 := g.selectionBounds()
	return row >= r0 && row <= r1 && col >= c0 && col <= c1
}

// selectedCellsText returns the current selection's content as tab-
// separated columns and newline-separated rows — what the right-click
// menu's "Copy" item hands to OnCopyRequest.
func (g *DataGrid) selectedCellsText() string {
	r0, c0, r1, c1 := g.selectionBounds()
	var b strings.Builder
	for r := r0; r <= r1; r++ {
		if r > r0 {
			b.WriteByte('\n')
		}
		cells := g.rows.Row(r)
		for c := c0; c <= c1; c++ {
			if c > c0 {
				b.WriteByte('\t')
			}
			if c < len(cells) {
				b.WriteString(cells[c])
			}
		}
	}
	return b.String()
}

// allRowsText returns every row in the grid, tab-separated / newline-
// separated, optionally prefixed with a header row of column names — what
// the row-number gutter's blank header-cell menu's "Copy All"/"Copy All
// with Headers" hand to OnCopyRequest.
func (g *DataGrid) allRowsText(withHeaders bool) string {
	var b strings.Builder
	if withHeaders {
		b.WriteString(strings.Join(g.columns, "\t"))
		if g.rows.Len() > 0 {
			b.WriteByte('\n')
		}
	}
	for r := 0; r < g.rows.Len(); r++ {
		if r > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.Join(g.rows.Row(r), "\t"))
	}
	return b.String()
}

// requestCopy hands text to OnCopyRequest, if set — see that field's doc
// comment for why DataGrid can't write to the OS clipboard itself.
func (g *DataGrid) requestCopy(text string) {
	if g.OnCopyRequest != nil {
		g.OnCopyRequest(text)
	}
}

// cellContextMenuItems builds the right-click (or Ctrl+Space) menu for a
// selected cell/block: "Copy" only when OnCopyRequest is wired (so a grid
// that hasn't opted in shows exactly what it always has), plus "Show
// Value" for a single cell — a block selection has no one cell's full
// content to show, so that item is omitted while blockSelecting is true.
func (g *DataGrid) cellContextMenuItems() []MenuItem {
	var items []MenuItem
	if g.OnCopyRequest != nil {
		items = append(items, MenuItem{Label: "Copy", Action: func() { g.requestCopy(g.selectedCellsText()) }})
	}
	if !g.blockSelecting {
		items = append(items, MenuItem{Label: showValueMenuItem, Action: g.openViewer})
	}
	return items
}

// HandleKey handles keyboard navigation.
func (g *DataGrid) HandleKey(ev *tcell.EventKey) bool {
	if g.ctxMenu.Visible() {
		g.ctxMenu.HandleKey(ev)
		return true
	}
	if g.viewOpen {
		if ev.Key() == tcell.KeyEscape {
			g.viewOpen = false
			return true
		}
		g.viewEditor.HandleKey(ev)
		return true
	}
	// Ctrl+Space is the keyboard equivalent of right-clicking the selected
	// cell — the same "Show Value" popup a read-only grid's mouse
	// right-click opens (see HandleMouse's Button2 case; an editable grid,
	// OnActivateCell != nil, has no context menu there either, so this
	// falls through to the default case below like it always has).
	if ev.Modifiers()&tcell.ModCtrl != 0 && core.EvRune(ev) == ' ' &&
		g.cellCursor && g.rows.Len() > 0 && g.OnActivateCell == nil {
		x, y := g.selectionScreenPos()
		g.ctxMenu.Show(x, y, g.cellContextMenuItems())
		return true
	}
	// Shift+Arrow extends a multi-cell block selection from the cell the
	// cursor was on before this key (the anchor stays fixed across repeated
	// Shift+Arrow presses); a plain arrow collapses back to a single cell.
	// Only meaningful for a read-only, cell-cursor grid — an editable
	// grid's Left/Right/Up/Down never selected a block before this feature
	// existed and still doesn't (see the field docs on blockSelecting).
	canBlockSelect := g.cellCursor && g.OnActivateCell == nil
	shiftHeld := ev.Modifiers()&tcell.ModShift != 0
	isArrowKey := false
	switch ev.Key() {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight:
		isArrowKey = true
	}
	if canBlockSelect {
		if shiftHeld && isArrowKey {
			if !g.blockSelecting {
				g.selAnchorRow, g.selAnchorCol = g.selRow, g.selCol
			}
			g.blockSelecting = true
		} else {
			g.blockSelecting = false
		}
	}
	dataH := g.rect.H - 3
	moved := false
	switch ev.Key() {
	case tcell.KeyUp:
		if g.selRow > 0 {
			g.selRow--
			g.ensureVisible(dataH)
			moved = true
		}
	case tcell.KeyDown:
		if g.selRow < g.rows.Len()-1 {
			g.selRow++
			g.ensureVisible(dataH)
			moved = true
		}
	case tcell.KeyPgUp:
		g.selRow = core.Max(0, g.selRow-dataH)
		g.ensureVisible(dataH)
		moved = true
	case tcell.KeyPgDn:
		g.selRow = core.Min(g.rows.Len()-1, g.selRow+dataH)
		g.ensureVisible(dataH)
		moved = true
	case tcell.KeyHome:
		g.selRow, g.scrollRow = 0, 0
		moved = true
	case tcell.KeyEnd:
		g.selRow = g.rows.Len() - 1
		g.ensureVisible(dataH)
		moved = true
	case tcell.KeyLeft:
		if g.cellCursor {
			if g.selCol > 0 {
				g.selCol--
				g.ensureVisibleCol()
			}
		} else if g.scrollCol > 0 {
			g.scrollCol--
		}
	case tcell.KeyRight:
		if g.cellCursor {
			if g.selCol < len(g.columns)-1 {
				g.selCol++
				g.ensureVisibleCol()
			}
		} else if g.scrollCol < len(g.columns)-1 {
			g.scrollCol++
		}
	case tcell.KeyEnter:
		if g.cellCursor && g.rows.Len() > 0 {
			g.activateCell()
		}
	default:
		if g.cellCursor && g.rows.Len() > 0 && core.EvRune(ev) == ' ' {
			if g.OnActivateCell != nil {
				g.OnActivateCell(g.selRow, g.selCol)
			}
			return true
		}
		return false
	}
	if moved && g.OnSelectRow != nil {
		g.OnSelectRow(g.selRow)
	}
	return true
}

// HandleMouse handles mouse events.
func (g *DataGrid) HandleMouse(ev *tcell.EventMouse) bool {
	if g.ctxMenu.Visible() {
		g.ctxMenu.HandleMouse(ev)
		return true
	}
	if g.viewOpen {
		if g.viewEditor.HandleMouse(ev) {
			return true
		}
		// The editor didn't act on this event — a click landed outside its
		// bounds (or, for a release, no drag was in progress there), so
		// only an actual outside press dismisses the popup; a stray
		// release or a wheel event elsewhere leaves it open.
		if ev.Buttons() == tcell.Button1 {
			g.viewOpen = false
		}
		return true
	}
	// Reset the drag-vs-fresh-click tracker on every release, regardless of
	// where it lands or whether a block selection was even in progress —
	// mirrors Editor's own mouseDragging reset. This is a side effect only;
	// the actual return value below (true, unconditionally, once a release
	// reaches the switch below without matching any case) is unchanged from
	// before this field existed, so it doesn't disturb propsheet.Form's
	// "focused row gets first refusal" contract (see tuikit/README.md).
	if ev.Buttons() == tcell.ButtonNone {
		g.mouseDragging = false
	}
	mx, my := ev.Position()
	if !g.rect.Contains(mx, my) {
		return false
	}
	dataH := g.rect.H - 3
	canBlockSelect := g.cellCursor && g.OnActivateCell == nil
	switch ev.Buttons() {
	case tcell.Button1:
		if row := g.scrollRow + (my - g.rect.Y - 2); row >= 0 && row < g.rows.Len() {
			if canBlockSelect {
				if col, ok := g.colAt(mx); ok {
					if !g.mouseDragging {
						g.mouseDragging = true
						if ev.Modifiers()&tcell.ModShift != 0 {
							if !g.blockSelecting {
								g.selAnchorRow, g.selAnchorCol = g.selRow, g.selCol
							}
						} else {
							g.selAnchorRow, g.selAnchorCol = row, col
						}
					}
					g.selRow, g.selCol = row, col
					g.blockSelecting = g.selRow != g.selAnchorRow || g.selCol != g.selAnchorCol
					if g.OnSelectRow != nil {
						g.OnSelectRow(g.selRow)
					}
				}
				return true
			}
			g.selRow = row
			if g.cellCursor {
				if col, ok := g.colAt(mx); ok {
					g.selCol = col
					g.activateCell()
					return true
				}
			}
			if g.OnSelectRow != nil {
				g.OnSelectRow(g.selRow)
			}
		}
	case tcell.Button2:
		// Right-click on the row-number gutter's blank header cell offers
		// whole-grid copy actions instead of a per-cell menu.
		if gw := g.gutterWidth(); gw > 0 && my == g.rect.Y && mx >= g.rect.X && mx < g.rect.X+gw {
			if g.OnCopyRequest != nil {
				g.ctxMenu.Show(mx, my, []MenuItem{
					{Label: "Copy All", Action: func() { g.requestCopy(g.allRowsText(false)) }},
					{Label: "Copy All with Headers", Action: func() { g.requestCopy(g.allRowsText(true)) }},
				})
			}
			return true
		}
		// Right-click on a data cell: select it and, for a read-only grid
		// (OnActivateCell unset), offer "Copy"/"Show Value". A click inside
		// an existing block selection preserves it (so "Copy" copies the
		// whole block); otherwise it collapses to just the clicked cell,
		// matching ordinary spreadsheet right-click behavior.
		if row := g.scrollRow + (my - g.rect.Y - 2); g.cellCursor && row >= 0 && row < g.rows.Len() {
			if col, ok := g.colAt(mx); ok {
				if !g.selectionContains(row, col) {
					g.selRow, g.selCol = row, col
					g.blockSelecting = false
				}
				if g.OnActivateCell == nil {
					g.ctxMenu.Show(mx, my, g.cellContextMenuItems())
				}
			}
		}
	case tcell.WheelUp:
		// Shift+wheel is the common desktop convention for horizontal
		// scroll; some terminals report it as WheelUp/WheelDown with a
		// Shift modifier rather than as WheelLeft/WheelRight below, so
		// honour both.
		if ev.Modifiers()&tcell.ModShift != 0 {
			g.scrollColBy(-horizontalWheelCols)
		} else if g.scrollRow > 0 {
			g.scrollRow--
		}
	case tcell.WheelDown:
		if ev.Modifiers()&tcell.ModShift != 0 {
			g.scrollColBy(horizontalWheelCols)
		} else if g.scrollRow < g.rows.Len()-dataH {
			g.scrollRow++
		}
	case tcell.WheelLeft:
		g.scrollColBy(-horizontalWheelCols)
	case tcell.WheelRight:
		g.scrollColBy(horizontalWheelCols)
	}
	return true
}

// horizontalWheelCols is how many columns a single horizontal wheel tick
// (WheelLeft/WheelRight, or Shift+WheelUp/WheelDown) scrolls — matches
// DataGrid's own 1-row vertical wheel step.
const horizontalWheelCols = 1

// scrollColBy shifts scrollCol by delta (negative scrolls left), clamped
// to the valid column range.
func (g *DataGrid) scrollColBy(delta int) {
	g.scrollCol = core.Clamp(g.scrollCol+delta, 0, core.Max(0, len(g.columns)-1))
}

// colAt returns the column index whose cell contains screen x, in
// cell-cursor mode, honouring horizontal scroll and the row-number gutter
// (if enabled). ok is false if x falls outside every column — including
// inside the gutter itself, which is never a selectable column.
func (g *DataGrid) colAt(x int) (col int, ok bool) {
	cx := g.rect.X + g.gutterWidth()
	for i := g.scrollCol; i < len(g.colWidths); i++ {
		w := g.colWidths[i]
		if x >= cx && x < cx+w {
			return i, true
		}
		cx += w
	}
	return 0, false
}

func (g *DataGrid) ensureVisible(dataH int) {
	if g.selRow < g.scrollRow {
		g.scrollRow = g.selRow
	}
	if g.selRow >= g.scrollRow+dataH {
		g.scrollRow = g.selRow - dataH + 1
	}
}

// ensureVisibleCol scrolls horizontally, if needed, so selCol is on screen
// — the column analogue of ensureVisible. Columns vary in width, so unlike
// the row case this can't be a single subtraction: it walks scrollCol
// rightward until selCol's column fits within the available width.
func (g *DataGrid) ensureVisibleCol() {
	if g.selCol < g.scrollCol {
		g.scrollCol = g.selCol
		return
	}
	avail := g.rect.W - g.gutterWidth()
	for g.scrollCol < g.selCol {
		w := 0
		for i := g.scrollCol; i <= g.selCol && i < len(g.colWidths); i++ {
			w += g.colWidths[i]
		}
		if w <= avail {
			break
		}
		g.scrollCol++
	}
}

// activateCell fires OnActivateCell for grids that define editable
// cell-activation behavior (toggle grids, permission-state cycling, …).
// Grids that leave it nil — every plain read-only display grid — do
// nothing here; right-click's "Show Value" (see HandleMouse) is how those
// open the full-content viewer.
func (g *DataGrid) activateCell() {
	if g.OnActivateCell != nil {
		g.OnActivateCell(g.selRow, g.selCol)
	}
}

// showValueMenuItem is the sole context-menu entry offered on a
// right-clicked cell — see HandleMouse's Button2 case.
const showValueMenuItem = "Show Value"

// openViewer shows the full-content popup for the currently selected
// cell's text in a read-only Editor, so it can be navigated, selected, and
// copied like any other text.
func (g *DataGrid) openViewer() {
	cells := g.rows.Row(g.selRow)
	if g.selCol < 0 || g.selCol >= len(cells) {
		return
	}
	g.viewHeader = ""
	if g.selCol < len(g.columns) {
		g.viewHeader = g.columns[g.selCol]
	}
	if g.viewEditor == nil {
		g.viewEditor = NewEditor(nil)
		g.viewEditor.SetGutterVisible(false)
		g.viewEditor.SetWrapMode(true)
		g.viewEditor.SetReadOnly(true)
	}
	g.viewEditor.SetText(cells[g.selCol])
	g.viewEditor.SetActive(true)
	g.viewOpen = true
}

// DrawOverlay renders the right-click context menu and the full-content
// popup, if either is open. Must be called after every other widget in the
// same frame has drawn, so nothing paints over them — see Draw.
func (g *DataGrid) DrawOverlay(s tcell.Screen) {
	if g.viewOpen {
		sw, sh := s.Size()
		w := core.Min(cellViewerW, core.Max(20, sw-4))
		h := cellViewerLines + 4 // border top/bottom + cellViewerLines text rows + 1 hint row
		x := core.Max(0, (sw-w)/2)
		y := core.Max(0, (sh-h)/2)
		rect := core.Rect{X: x, Y: y, W: w, H: h}

		p := theme.Active()
		core.DimArea(s, core.Rect{X: 0, Y: 0, W: sw, H: sh}, p.DialogOverlay, viewerDimNum, viewerDimDen)
		core.FillRect(s, rect, ' ', theme.StyleDialog())
		borderSt := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.DialogBorder)
		titleSt := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.DialogTitle).Bold(true)
		core.DrawBoxTitle(s, rect, g.viewHeader, borderSt, titleSt)

		g.viewEditor.SetBounds(x+2, y+1, w-4, cellViewerLines)
		g.viewEditor.Draw(s)

		hintSt := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		core.DrawTextClipped(s, x+2, y+h-2, w-4, hintSt, "Esc to close — Shift+arrows to select, Ctrl+C to copy")
	}
	g.ctxMenu.Draw(s)
}

// ---------------------------------------------------------------------------
// Clipboard target — active only while the content viewer is open
// ---------------------------------------------------------------------------

// HasSelection, SelectedText, Cut, Paste, and SelectAll make *DataGrid
// itself a clipboard target (see internal/tui/clipboard.go's
// clipboardTarget and propsheet.ClipboardRow), forwarding to the built-in
// viewer's read-only Editor while it's open. HasSelection is always false
// otherwise, so a host that falls back to its own row/cell copy behavior
// (e.g. propsheet.GridRow.CopyText) when there's "no selection" keeps doing
// exactly that whenever the viewer isn't showing.
func (g *DataGrid) HasSelection() bool {
	return g.viewOpen && g.viewEditor.HasSelection()
}

func (g *DataGrid) SelectedText() string {
	if !g.viewOpen {
		return ""
	}
	return g.viewEditor.SelectedText()
}

// Cut degrades to Copy: the viewer is read-only, so there's nothing to
// remove.
func (g *DataGrid) Cut() string { return g.SelectedText() }

// Paste is a no-op: the viewer is read-only.
func (g *DataGrid) Paste(text string) {}

func (g *DataGrid) SelectAll() {
	if g.viewOpen {
		g.viewEditor.SelectAll()
	}
}

// OverlayActive reports whether the right-click context menu or the
// full-content popup is currently showing. A host that lays the grid out
// alongside another focusable widget (e.g. QueryPanel's SQL editor) must
// check this and give the grid exclusive first refusal of every key and
// mouse event while it's true — both overlays are centred/positioned
// independently of the grid's own rect (see DrawOverlay), so ordinary
// position- or focus-based routing would otherwise hand their input to
// whatever widget happens to occupy those screen coordinates underneath.
func (g *DataGrid) OverlayActive() bool {
	return g.viewOpen || g.ctxMenu.Visible()
}
