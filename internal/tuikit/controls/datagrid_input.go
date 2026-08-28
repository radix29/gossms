package controls

import (
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// selectionScreenPos returns the screen coordinates of the selected cell, to
// position the context menu or "Show Value" popup opened with Ctrl+Space.
// Mirrors drawCellSelection's column-width walk.
func (g *DataGrid) selectionScreenPos() (x, y int) {
	x = g.rect.X + g.gutterWidth()
	for i := g.scrollCol; i < g.selCol && i < len(g.colWidths); i++ {
		x += g.colWidths[i]
	}
	y = g.rect.Y + 2 + (g.selRow - g.scrollRow)
	return x, y
}

// selectionContains reports whether (row, col) falls within the current
// selection, so the right-click handler can preserve an existing block
// selection instead of collapsing it to the clicked cell.
func (g *DataGrid) selectionContains(row, col int) bool {
	r0, c0, r1, c1 := g.selectionBounds()
	return row >= r0 && row <= r1 && col >= c0 && col <= c1
}

// extendSelectionMods are the modifiers that make a click extend the selection
// from the anchor rather than start a new one.
//
// Alt as well as Shift, because Shift+click is not ours to rely on: a VTE
// terminal (xfce4-terminal, GNOME Terminal) holds Shift back for its own text
// selection whenever an application has mouse reporting on, so the app never
// sees the click at all. Alt+click is delivered, and is the same gesture
// wherever Shift is taken. Key Diagnostics logs mouse events, which is how a
// terminal that keeps a modifier is told apart from a binding that is wrong.
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
	// Ctrl+Space is the keyboard equivalent of right-clicking the selected cell.
	// An editable grid (OnActivateCell != nil) has no context menu there either,
	// so it falls through to the default case below.
	if ev.Modifiers()&tcell.ModCtrl != 0 && core.EvRune(ev) == ' ' &&
		g.cellCursor && g.rows.Len() > 0 && g.OnActivateCell == nil {
		x, y := g.selectionScreenPos()
		g.ctxMenu.Show(x, y, g.cellContextMenuItems())
		return true
	}
	// Shift+Arrow extends a block selection from the cell the cursor was on
	// before this key, the anchor staying fixed across repeats; a plain arrow
	// collapses back to one cell. Read-only cell-cursor grids only — see
	// blockSelecting.
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
	// Any move of the cursor drops a Ctrl+click selection, Shift+Arrow included
	// — the same rule a file manager's list follows, and the reason a marked set
	// can only ever be extended by more Ctrl+clicks.
	switch ev.Key() {
	case tcell.KeyUp, tcell.KeyDown, tcell.KeyLeft, tcell.KeyRight,
		tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyHome, tcell.KeyEnd:
		g.ClearMarkedRows()
	}
	dataH := g.rect.H - 3
	// The four whole-list jumps do nothing on an empty grid, the guard
	// SetSelectedRow/SetSelectedCell already carry. PgDn and End derive selRow
	// from rows.Len()-1, which is -1 with no rows; ensureVisible copies that into
	// scrollRow, and Draw's row loop bounds dataIdx only from above, so it
	// reaches rows.Row(-1) and panics on the UI goroutine, which has no recover.
	// Up/Down are already bounded by a live row index.
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
		// Everything else is swallowed and the popup stays open: Escape and the
		// Close button are the only ways out, so a stray click part-way through
		// selecting a long value can't throw it away.
		return true
	}
	// Reset the drag-vs-fresh-click tracker on every release, wherever it lands
	// and whether or not a block selection was in progress — as Editor does. A
	// side effect only: the return value is unaffected, so propsheet.Form's
	// "focused row gets first refusal" contract still holds.
	if ev.Buttons() == tcell.ButtonNone {
		g.mouseDragging = false
		g.sbDragging = false
		g.sbDraggingH = false
		g.colResizing = false
	}
	mx, my := ev.Position()
	// A column-resize drag, like the horizontal scrollbar's below, keeps control
	// once started even after the pointer leaves the grid, so it is checked ahead
	// of the bounds test.
	if g.resizeDrag(ev) {
		return true
	}
	// A horizontal-scrollbar drag keeps control once started even after the
	// pointer leaves the grid, so it is checked before the bounds test — unlike
	// the vertical bar, whose track spans the whole data area and is harder to
	// drag off.
	if g.hScrollbarDrag(ev) {
		return true
	}
	if !g.rect.Contains(mx, my) {
		return false
	}
	dataH := g.rect.H - 3

	// Scrollbar drag/click takes priority over the row/cell hit-testing below:
	// the bar is drawn at rect.Right()-1, which would otherwise read as a click
	// on whatever cell sits in that column.
	if core.HandleScrollbarDrag(ev, g.rect.Right()-1, g.rect.Y+2, dataH, g.rows.Len(), &g.sbDragging, &g.scrollRow) {
		return true
	}

	canBlockSelect := g.cellCursor && g.OnActivateCell == nil
	switch ev.Buttons() {
	case tcell.Button1:
		// rowAtY is -1 outside the data rows. rect covers the header, its
		// separator and the status bar too, so without the bound a click on the
		// status bar resolves to scrollRow+dataH — the first row *below* the
		// view — moving the selection out of sight.
		if row := g.rowAtY(my); row >= 0 {
			if canBlockSelect {
				if col, ok := g.colAt(mx); ok {
					// Ctrl+click picks one row out, and does it once per press:
					// tcell resends Button1 for as long as the button is held,
					// and a second toggle would undo the first before the user
					// let go. It never drags, either — the modifier says "this
					// row", not "this run".
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
						// A press without Ctrl starts a new selection, so
						// whatever Ctrl+click had marked is gone — including
						// under Shift, which extends from the anchor rather
						// than adding to the marked set.
						g.ClearMarkedRows()
						if ev.Modifiers()&extendSelectionMods != 0 {
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
			prevRow := g.selRow
			g.selRow = row
			if g.cellCursor {
				if col, ok := g.colAt(mx); ok {
					if g.mouseDragging && row == g.toggleRow && col == g.toggleCol {
						// Still the same cell as the last press or drag-move —
						// don't re-toggle on every resend from one stationary
						// click.
						return true
					}
					g.mouseDragging = true
					g.toggleRow, g.toggleCol = row, col
					g.selCol = col
					// Select, then activate — the order the keyboard uses.
					// Without the select, a click on a cell-cursor grid moves
					// the highlight and never tells the page, so a detail panel
					// wired to OnSelectRow goes on describing the row the
					// keyboard left it on. Gated on an actual move, like the
					// keyboard path: a page that redraws from inside
					// OnActivateCell must not have its selection callback
					// re-entered on every toggle of the row it is already on.
					if row != prevRow && g.OnSelectRow != nil {
						g.OnSelectRow(row)
					}
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
		// Right-click on a data cell: select it and, on a read-only grid, offer
		// "Copy"/"Show Value". A click inside an existing block selection
		// preserves it so "Copy" takes the whole block; otherwise it collapses
		// to the clicked cell, as a spreadsheet does.
		if row := g.rowAtY(my); g.cellCursor && row >= 0 {
			if col, ok := g.colAt(mx); ok {
				if !g.selectionContains(row, col) && !g.rowMarked(row) {
					g.selRow, g.selCol = row, col
					g.blockSelecting = false
					g.ClearMarkedRows()
				}
				if g.OnActivateCell == nil {
					g.ctxMenu.Show(mx, my, g.cellContextMenuItems())
				}
			}
		}
	case tcell.WheelUp:
		// Shift+wheel is the desktop convention for horizontal scroll, and some
		// terminals report it that way rather than as WheelLeft/WheelRight
		// below, so honour both.
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
// core.HandleScrollbarDragH can't serve: it treats the track's width as the
// visible count, and this track is characters wide while what it scrolls is a
// column index. Latches sbDraggingH for the rest of the gesture, so the thumb
// keeps following the pointer off the bar's row. Returns false for anything that
// doesn't qualify, so the caller can chain it ahead of its own hit-testing.
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
	// ScrollOffsetForDrag gives the character offset the track position asks
	// for, clamped so the last screenful can't be scrolled past; colAtOffset
	// rounds it to a column boundary, since Draw never splits a cell.
	g.scrollCol = g.colAtOffset(core.ScrollOffsetForDrag(mx-x, w, total, visible))
	return true
}

// resizeDrag handles a Button1 press or drag on a column separator in the header
// row, resizing the column to its left as SSMS does. A press latches colResizing
// for the rest of the gesture, so the edge keeps following the pointer off the
// one-column-wide separator; a second press on the same separator within
// resizeDoubleClickInterval restores that column's default width. Returns false
// for anything that doesn't qualify.
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
		// Latched even for the double-click, so tcell's resends while the button
		// is down are absorbed here rather than re-entering this branch against
		// the now-moved separator.
		g.colResizing = true
		g.resizeCol, g.resizeStartX, g.resizeStartW = col, mx, g.colWidths[col]
		return true
	}
	g.SetColumnWidth(g.resizeCol, max(minResizeWidth, g.resizeStartW+mx-g.resizeStartX))
	return true
}

// sepColAt returns the column whose right-hand separator is drawn at screen
// position (x, y) — the column a drag there resizes. Only the header row grabs:
// the separator glyph runs down every data row, and claiming it there would
// steal clicks from cell selection.
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
// follows a previous one closely enough to count as a double-click, and records
// this press for the next call.
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

// rowAtY returns the data row index drawn at screen row y, or -1 for the
// header, its separator, the status bar and any blank filler below the last
// row.
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

// colAtOffset returns the last column starting at or before character offset off
// — the inverse of the running sum hScrollbar reports.
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

// colAt returns the column index whose cell contains screen x in cell-cursor
// mode, honouring horizontal scroll and the row-number gutter. ok is false if x
// falls outside every column, the gutter included.
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

// ensureVisible scrolls vertically so selRow is on screen, given the height of
// the data area (rect.H less the header and status rows).
//
// A grid that has not been laid out yet has rect.H == 0, so dataH arrives
// negative and the second test below is true for every row: the scroll would
// jump past the whole list, and the next Draw paints the header and "N rows"
// over blank lines. Callers reaching a grid before its first SetBounds are
// legitimate — SetSelectedCell via SetDataPreservingView is one — so leave the
// scroll alone until there is a viewport to scroll within.
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

// ensureVisibleCol scrolls horizontally so selCol is on screen — ensureVisible's
// column analogue. Columns vary in width, so instead of one subtraction it walks
// scrollCol rightward until selCol fits the available width.
func (g *DataGrid) ensureVisibleCol() {
	if g.selCol < g.scrollCol {
		g.scrollCol = g.selCol
		return
	}
	// Before the first SetBounds there is no width to fit the columns into,
	// and a negative avail walks scrollCol all the way to selCol — the first
	// column scrolls off a grid nobody has drawn yet. See ensureVisible.
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
// permission-state cycling). A grid leaving it nil does nothing here;
// right-click's "Show Value" is how those open the full-content viewer.
func (g *DataGrid) activateCell() {
	if g.OnActivateCell != nil {
		g.OnActivateCell(g.selRow, g.selCol)
	}
}
