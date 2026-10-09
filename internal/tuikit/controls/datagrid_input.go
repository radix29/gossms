package controls

import (
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// selectionScreenPos returns the screen coordinates of the selected cell, to
// position the context menu Ctrl+Space opens as a right-click would. "Show
// Value" needs none (screen-centred). Mirrors drawCellSelection's width walk.
func (g *DataGrid) selectionScreenPos() (x, y int) {
	x = g.rect.X + g.gutterWidth()
	for i := g.scrollCol; i < g.selCol && i < len(g.colWidths); i++ {
		x += g.colWidths[i]
	}
	y = g.rect.Y + 2 + (g.selRow - g.scrollRow)
	return x, y
}

// selectionContains reports whether (row, col) is within the current selection,
// so right-click preserves an existing block selection instead of collapsing it.
func (g *DataGrid) selectionContains(row, col int) bool {
	r0, c0, r1, c1 := g.selectionBounds()
	return row >= r0 && row <= r1 && col >= c0 && col <= c1
}

// extendSelectionMods are the modifiers that make a click extend the selection
// from the anchor rather than start a new one. Alt as well as Shift: a VTE
// terminal (xfce4-terminal, GNOME Terminal) holds Shift back for its own text
// selection whenever the app has mouse reporting on, so the app never sees the
// click; Alt+click is delivered. Key Diagnostics logs mouse events, which tells
// a terminal that keeps a modifier from a wrong binding.
const extendSelectionMods = tcell.ModShift | tcell.ModAlt

// HandleKey handles keyboard navigation.
func (g *DataGrid) HandleKey(ev *tcell.EventKey) bool {
	if g.ctxMenu.Visible() {
		g.ctxMenu.HandleKey(ev)
		return true
	}
	if g.viewOpen {
		if ev.Key() == tcell.KeyEscape {
			g.closeViewer()
			return true
		}
		g.viewEditor.HandleKey(ev)
		return true
	}
	// Ctrl+Space is the keyboard equivalent of right-clicking the selected cell. An
	// editable grid (see editable) has no context menu there, so it falls through to
	// the default case.
	if ev.Modifiers()&tcell.ModCtrl != 0 && core.EvRune(ev) == ' ' &&
		g.cellCursor && g.rows.Len() > 0 && !g.editable() {
		x, y := g.selectionScreenPos()
		g.ctxMenu.Show(x, y, g.cellContextMenuItems())
		return true
	}
	// Shift+Arrow extends a block selection from the cell the cursor was on, anchor
	// fixed across repeats; a plain arrow collapses to one cell. Read-only
	// cell-cursor grids only; see blockSelecting.
	canBlockSelect := g.cellCursor && !g.editable()
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
	// Any cursor move drops a Ctrl+click selection, Shift+Arrow included (a file
	// manager's rule); a marked set is extended only by more Ctrl+clicks.
	switch ev.Key() {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight,
		tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyHome, tcell.KeyEnd:
		g.ClearMarkedRows()
	}
	dataH := g.rect.H - 3
	// The four whole-list jumps do nothing on an empty grid (the guard
	// SetSelectedRow/SetSelectedCell carry). PgDn and End derive selRow from
	// rows.Len()-1, -1 with no rows; ensureVisible copies that into scrollRow, and
	// Draw's row loop bounds dataIdx only from above, so it reaches rows.Row(-1) and
	// panics on the UI goroutine, which has no recover. Up/Down use a live index.
	switch ev.Key() {
	case tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyHome, tcell.KeyEnd:
		if g.rows.Len() == 0 {
			return true
		}
	}
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
		g.selRow = max(0, g.selRow-dataH)
		g.ensureVisible(dataH)
		moved = true
	case tcell.KeyPgDn:
		g.selRow = min(g.rows.Len()-1, g.selRow+dataH)
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
			g.activateCell()
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
	// The tail of the gesture that closed the popup — see viewDismissing.
	if g.viewDismissing {
		if ev.Buttons() == tcell.ButtonNone {
			g.viewDismissing = false
		}
		return true
	}
	if g.viewOpen {
		if px, py := ev.Position(); ev.Buttons() == tcell.Button1 && g.viewCloseRect.Contains(px, py) {
			g.closeViewer()
			g.viewDismissing = true
			return true
		}
		if g.viewEditor.HandleMouse(ev) {
			return true
		}
		// Everything else is swallowed and the popup stays open: Escape and Close are
		// the only ways out, so a stray click mid-selection can't discard a long value.
		return true
	}
	// Reset the drag-vs-fresh-click tracker on every release, wherever it lands and
	// whether or not a block selection was in progress, as Editor does. Side effect
	// only: the return value is unaffected, so propsheet.Form's "focused row gets
	// first refusal" contract holds.
	if ev.Buttons() == tcell.ButtonNone {
		g.mouseDragging = false
		g.sbDragging = false
		g.sbDraggingH = false
		g.colResizing = false
	}
	mx, my := ev.Position()
	// A column-resize drag, like the horizontal scrollbar's, keeps control once
	// started even after the pointer leaves the grid, so it is checked ahead of the
	// bounds test.
	if g.resizeDrag(ev) {
		return true
	}
	// A horizontal-scrollbar drag keeps control once started, so it is checked before
	// the bounds test (unlike the vertical bar, whose track spans the data area and
	// is harder to drag off).
	if g.hScrollbarDrag(ev) {
		return true
	}
	if !g.rect.Contains(mx, my) {
		return false
	}
	dataH := g.rect.H - 3

	// Scrollbar drag/click outranks row/cell hit-testing: the bar is drawn at
	// rect.Right()-1 and would read as a click on the cell in that column.
	if core.HandleScrollbarDrag(ev, g.rect.Right()-1, g.rect.Y+2, dataH, g.rows.Len(), &g.sbDragging, &g.scrollRow) {
		return true
	}

	canBlockSelect := g.cellCursor && !g.editable()
	switch ev.Buttons() {
	case tcell.Button1:
		// rowAtY is -1 outside the data rows. rect covers the header, separator and
		// status bar too; without the bound a click on the status bar resolves to
		// scrollRow+dataH, the first row below the view, moving the selection out of
		// sight.
		if row := g.rowAtY(my); row >= 0 {
			if canBlockSelect {
				if col, ok := g.colAt(mx); ok {
					// Ctrl+click picks one row out, once per press: tcell resends Button1 while held
					// and a second toggle would undo the first. It never drags: the modifier says
					// "this row", not "this run".
					if ev.Modifiers()&tcell.ModCtrl != 0 {
						if !g.mouseDragging {
							g.mouseDragging = true
							g.markRow(row)
							g.selRow, g.selCol = row, col
							g.selAnchorRow, g.selAnchorCol = row, col
							g.blockSelecting = false
							if g.OnSelectRow != nil {
								g.OnSelectRow(g.selRow)
							}
						}
						return true
					}
					if !g.mouseDragging {
						g.mouseDragging = true
						// A press without Ctrl starts a new selection, dropping what Ctrl+click marked,
						// including under Shift, which extends from the anchor rather than adding to the
						// marked set.
						g.ClearMarkedRows()
						if ev.Modifiers()&extendSelectionMods != 0 {
							if !g.blockSelecting {
								g.selAnchorRow, g.selAnchorCol = g.selRow, g.selCol
							}
						} else {
							g.selAnchorRow, g.selAnchorCol = row, col
						}
					}
					prevRow := g.selRow
					g.selRow, g.selCol = row, col
					g.blockSelecting = g.selRow != g.selAnchorRow || g.selCol != g.selAnchorCol
					// Only on a move, as the keyboard fires: tcell resends Button1 on every motion
					// while held, and a host reloading on selection (Replication Monitor's agent
					// history) re-read on each one.
					if g.selRow != prevRow && g.OnSelectRow != nil {
						g.OnSelectRow(g.selRow)
					}
				}
				return true
			}
			prevRow := g.selRow
			g.selRow = row
			if g.cellCursor {
				if col, ok := g.colAt(mx); ok {
					if g.mouseDragging && row == g.toggleRow && col == g.toggleCol {
						// Same cell as the last press or drag-move: don't re-toggle on resends from one
						// stationary click.
						return true
					}
					g.mouseDragging = true
					g.toggleRow, g.toggleCol = row, col
					g.selCol = col
					// Select, then activate, as the keyboard does: without the select, a click on a
					// cell-cursor grid moves the highlight without telling the page, so a detail
					// panel wired to OnSelectRow keeps describing the row the keyboard left it on.
					// Gated on an actual move like the keyboard path: a page that redraws from inside
					// OnActivateCell must not have its selection callback re-entered on every toggle
					// of the row it is already on.
					if row != prevRow && g.OnSelectRow != nil {
						g.OnSelectRow(row)
					}
					g.activateCell()
					return true
				}
			}
			if row != prevRow && g.OnSelectRow != nil {
				g.OnSelectRow(g.selRow)
			}
		}
	case tcell.Button2:
		// Right-click on the row-number gutter's blank header cell offers whole-grid copy
		// actions instead of a per-cell menu.
		if gw := g.gutterWidth(); gw > 0 && my == g.rect.Y && mx >= g.rect.X && mx < g.rect.X+gw {
			if g.OnCopyRequest != nil {
				g.ctxMenu.Show(mx, my, []MenuItem{
					{Label: "Copy All", Action: func() { g.requestCopy(g.allRowsText(false)) }},
					{Label: "Copy All with Headers", Action: func() { g.requestCopy(g.allRowsText(true)) }},
				})
			}
			return true
		}
		// Right-click on a data cell: select it and, on a read-only grid, offer
		// "Copy"/"Show Value". A click inside an existing block selection preserves it
		// so "Copy" takes the whole block; otherwise it collapses to the clicked cell, as
		// a spreadsheet does.
		if row := g.rowAtY(my); g.cellCursor && row >= 0 {
			if col, ok := g.colAt(mx); ok {
				if !g.selectionContains(row, col) && !g.rowMarked(row) {
					prevRow := g.selRow
					g.selRow, g.selCol = row, col
					g.blockSelecting = false
					g.ClearMarkedRows()
					// A move like any other: without it a grid-plus-detail page (Replication
					// Monitor) keeps describing the row the highlight just left. Fired before the
					// menu opens, so its actions see the page synced to the clicked row.
					if g.selRow != prevRow && g.OnSelectRow != nil {
						g.OnSelectRow(g.selRow)
					}
				}
				if !g.editable() {
					g.ctxMenu.Show(mx, my, g.cellContextMenuItems())
				}
			}
		}
	case tcell.WheelUp:
		// Shift+wheel is the desktop convention for horizontal scroll, and some
		// terminals report it that way rather than as WheelLeft/WheelRight; honour both.
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

// horizontalWheelCols is how many columns one horizontal wheel tick scrolls,
// matching the 1-row vertical step.
const horizontalWheelCols = 1

// scrollColBy shifts scrollCol by delta (negative scrolls left), clamped
// to the valid column range.
func (g *DataGrid) scrollColBy(delta int) {
	g.scrollCol = core.Clamp(g.scrollCol+delta, 0, max(0, len(g.columns)-1))
}

// hScrollbarDrag handles a Button1 press or drag on the horizontal scrollbar
// (see DataGrid.hScrollbar), translating a track position into a scrollCol.
// core.HandleScrollbarDragH can't serve: it treats the track width as the
// visible count, while this track is characters wide and scrolls a column index.
// Latches sbDraggingH so the thumb follows the pointer off the bar's row.
// Returns false for anything that doesn't qualify, so callers can chain it.
func (g *DataGrid) hScrollbarDrag(ev *tcell.EventMouse) bool {
	if ev.Buttons() != tcell.Button1 {
		return false
	}
	x, y, w, total, visible, _, ok := g.hScrollbar()
	if !ok {
		return false
	}
	mx, my := ev.Position()
	if !g.sbDraggingH && (my != y || mx < x || mx >= x+w) {
		return false
	}
	g.sbDraggingH = true
	// ScrollOffsetForDrag gives the character offset the track position asks for,
	// clamped so the last screenful can't be scrolled past; colAtOffset rounds it to
	// a column boundary since Draw never splits a cell.
	g.scrollCol = g.colAtOffset(core.ScrollOffsetForDrag(mx-x, w, total, visible))
	return true
}

// resizeDrag handles a Button1 press or drag on a column separator in the header
// row, resizing the column to its left as SSMS does. A press latches colResizing
// so the edge follows the pointer off the one-column separator; a second press
// on the same separator within resizeDoubleClickInterval restores the default
// width. Returns false for anything that doesn't qualify.
func (g *DataGrid) resizeDrag(ev *tcell.EventMouse) bool {
	if ev.Buttons() != tcell.Button1 {
		return false
	}
	mx, my := ev.Position()
	if !g.colResizing {
		col, ok := g.sepColAt(mx, my)
		if !ok {
			return false
		}
		if g.sepPressIsDouble(col, ev.When()) {
			g.SetColumnWidth(col, 0)
		}
		// Latched even for the double-click, so resends while the button is down are
		// absorbed here rather than re-entering this branch against the moved separator.
		g.colResizing = true
		g.resizeCol, g.resizeStartX, g.resizeStartW = col, mx, g.colWidths[col]
		return true
	}
	g.SetColumnWidth(g.resizeCol, max(minResizeWidth, g.resizeStartW+mx-g.resizeStartX))
	return true
}

// sepColAt returns the column whose right-hand separator is drawn at (x, y), the
// column a drag there resizes. Only the header row grabs: the glyph runs down
// every data row and claiming it there would steal clicks from cell selection.
func (g *DataGrid) sepColAt(x, y int) (col int, ok bool) {
	if y != g.rect.Y || g.rect.H < 3 {
		return 0, false
	}
	cx := g.rect.X + g.gutterWidth()
	for i := g.scrollCol; i < len(g.colWidths); i++ {
		cx += g.colWidths[i]
		// drawRow omits the separator once it would fall outside the rect.
		if cx-1 >= g.rect.Right() {
			break
		}
		if x == cx-1 {
			return i, true
		}
	}
	return 0, false
}

// sepPressIsDouble reports whether a press on column col's separator at time at
// closely follows a previous one (a double-click), and records this press.
func (g *DataGrid) sepPressIsDouble(col int, at time.Time) bool {
	double := col == g.lastSepPressCol && !g.lastSepPressAt.IsZero() &&
		at.Sub(g.lastSepPressAt) <= resizeDoubleClickInterval
	if double {
		// Don't let a third press pair with this one as well.
		g.lastSepPressCol, g.lastSepPressAt = -1, time.Time{}
		return true
	}
	g.lastSepPressCol, g.lastSepPressAt = col, at
	return false
}

// rowAtY returns the data row drawn at screen row y, or -1 for the header,
// separator, status bar and blank filler below the last row.
func (g *DataGrid) rowAtY(y int) int {
	dataH := g.rect.H - 3
	line := y - g.rect.Y - 2
	if line < 0 || line >= dataH {
		return -1
	}
	row := g.scrollRow + line
	if row >= g.rows.Len() {
		return -1
	}
	return row
}

// colAtOffset returns the last column starting at or before character offset
// off: the inverse of the running sum hScrollbar reports.
func (g *DataGrid) colAtOffset(off int) int {
	acc, col := 0, 0
	for i, cw := range g.colWidths {
		if acc > off {
			break
		}
		col = i
		acc += cw
	}
	return core.Clamp(col, 0, max(0, len(g.colWidths)-1))
}

// colAt returns the column whose cell contains screen x in cell-cursor mode,
// honouring horizontal scroll and the gutter. ok is false if x falls outside
// every column, the gutter included.
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

// ensureVisible scrolls vertically so selRow is on screen, given the data area's
// height (rect.H less the header and status rows).
//
// A grid not yet laid out has rect.H == 0, so dataH is negative and the second
// test below is true for every row: the scroll would jump past the whole list
// and the next Draw paints the header and "N rows" over blank lines. Callers
// legitimately reach a grid before its first SetBounds (SetSelectedCell via
// SetDataPreservingView), so the scroll is left alone until there is a viewport.
func (g *DataGrid) ensureVisible(dataH int) {
	if dataH <= 0 {
		return
	}
	if g.selRow < g.scrollRow {
		g.scrollRow = g.selRow
	}
	if g.selRow >= g.scrollRow+dataH {
		g.scrollRow = g.selRow - dataH + 1
	}
}

// ensureVisibleCol scrolls horizontally so selCol is on screen. Columns vary in
// width, so it walks scrollCol rightward until selCol fits.
func (g *DataGrid) ensureVisibleCol() {
	if g.selCol < g.scrollCol {
		g.scrollCol = g.selCol
		return
	}
	// Before the first SetBounds there is no width to fit into, and a negative avail
	// walks scrollCol all the way to selCol, scrolling the first column off a grid
	// nobody has drawn. See ensureVisible.
	avail := g.rect.W - g.gutterWidth()
	if avail <= 0 {
		return
	}
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

// activateCell fires OnActivateCell for grids with editable cells (toggle grids,
// permission-state cycling). A grid leaving it nil does nothing here
// (right-click's "Show Value" opens the viewer), nor does a browse-only grid; see
// SetBrowseOnly.
func (g *DataGrid) activateCell() {
	if g.editable() {
		g.OnActivateCell(g.selRow, g.selCol)
	}
}
