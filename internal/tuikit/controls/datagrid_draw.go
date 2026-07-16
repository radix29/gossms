package controls

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

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
