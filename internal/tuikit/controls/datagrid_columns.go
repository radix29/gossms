package controls

import (
	"github.com/radix29/gossms/internal/tuikit/core"
	"strconv"
)

// computeColWidths sizes columns from their header plus up to colWidthSampleRows
// data rows, so a huge result set doesn't slow SetSource. A cell is measured only
// as far as the clamp can see: past max(maxW, 6) columns every width clamps to
// the same result. Measuring whole cells cost 0.3-0.7 s per call for one column
// of 256 KB XML values, on the UI goroutine, and this runs on every SetSource,
// SetBounds and column drag.
func (g *DataGrid) computeColWidths() {
	g.widthsDirty = false
	if g.errorMode {
		g.colWidths = []int{g.rect.W - 2}
		if w := g.overrideWidth(0); w > 0 {
			g.colWidths[0] = w
		}
		return
	}
	g.colWidths = make([]int, len(g.columns))
	for i := range g.columns {
		g.colWidths[i] = core.DisplayWidth(g.headerLabel(i)) + 2
	}
	maxW := g.maxCellWidthOrDefault()
	cellLimit := max(maxW, 6) - 2
	n := min(g.rows.Len(), colWidthSampleRows)
	for r := range n {
		// A group row's label (cell 0) spills across the row; its other
		// cells are ordinary.
		first := 0
		if g.RowKindAt(r) == RowGroup {
			first = 1
		}
		row := g.rows.Row(r)
		for i, cell := range row {
			if i >= first && i < len(g.colWidths) {
				// Measured as drawn: TruncateLine shows a line break as a
				// space, which DisplayWidth counts as nothing.
				if w := core.DisplayWidthAtMost(core.TruncateLine(cell, cellLimit), cellLimit) + 2; w > g.colWidths[i] {
					g.colWidths[i] = w
				}
			}
		}
	}
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

// restoreOverrideWidths reinstates a column set's dragged widths and recomputes
// once, for SetDataPreservingView. Calling SetColumnWidth per column runs a full
// computeColWidths (a rescan of up to colWidthSampleRows rows) per column, and
// that runs on every redrawGrid, i.e. every keystroke on a grid-backed
// Properties page. Applies SetColumnWidth's minResizeWidth floor so a restored
// width is what a drag would have set.
//
// The recompute is conditional because widths is always column-count long
// (ColumnWidthOverrides pads it), all zeros on an unresized grid; recomputing
// unconditionally doubled the scan on the hot path, since SetData has already
// computed the widths.
func (g *DataGrid) restoreOverrideWidths(widths []int) {
	restored := false
	for i, w := range widths {
		if w > 0 {
			g.setOverrideWidth(i, max(w, minResizeWidth))
			restored = true
		}
	}
	if restored {
		g.computeColWidths()
	}
}

// ColumnWidthOverrides returns each column's dragged width, 0 for one at its
// computed default: the inverse of SetColumnWidth, and the only way to carry
// drags across a SetData that discards them (see SetDataPreservingView). The
// slice is a copy, as long as the column count.
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
