package controls

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

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

// selectionContains reports whether (row, col) falls within the current
// selection — used by the right-click handler to decide whether a click
// inside an existing block selection should preserve it (for a block copy)
// rather than collapsing it to the clicked cell.
func (g *DataGrid) selectionContains(row, col int) bool {
	r0, c0, r1, c1 := g.selectionBounds()
	return row >= r0 && row <= r1 && col >= c0 && col <= c1
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
					if g.mouseDragging && row == g.toggleRow && col == g.toggleCol {
						// Still the same cell as the last press/drag-move
						// event — do not re-toggle on every resent motion
						// event from one physical, stationary click.
						return true
					}
					g.mouseDragging = true
					g.toggleRow, g.toggleCol = row, col
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
