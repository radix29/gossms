package controls

import (
	"maps"
	"slices"
)

// SelectionBounds returns the inclusive row/col rectangle of the current
// selection — just the active cell when there's no multi-cell block selection.
// Exported so a host embedding the grid can tell the two apart.
func (g *DataGrid) SelectionBounds() (r0, c0, r1, c1 int) { return g.selectionBounds() }

// SelectedRows returns the rows the selection covers, ascending: the rows
// Ctrl+click marked while a discontiguous selection is in force, otherwise those
// the anchor/cursor rectangle spans (the one cursor row when nothing is
// extended). A host acting on whole objects reads this rather than
// SelectionBounds: a Ctrl+click selection is not a rectangle.
func (g *DataGrid) SelectedRows() []int { return g.selectedRows() }

func (g *DataGrid) selectedRows() []int {
	if g.marking {
		// Merge the run with the sorted flips: a flip inside the run unmarks its
		// row, one outside marks it. Flips are as many as Ctrl+clicks, so only
		// they are sorted, never the run.
		flips := slices.Sorted(maps.Keys(g.markFlips))
		rows := make([]int, 0, g.selectedRowCount())
		i := 0
		for ; i < len(flips) && flips[i] < g.markLo; i++ {
			rows = append(rows, flips[i])
		}
		for r := g.markLo; r <= g.markHi; r++ {
			if i < len(flips) && flips[i] == r {
				i++
				continue
			}
			rows = append(rows, r)
		}
		return append(rows, flips[i:]...)
	}
	r0, r1, ok := g.selectedRun()
	if !ok {
		return nil
	}
	rows := make([]int, 0, r1-r0+1)
	for r := r0; r <= r1; r++ {
		rows = append(rows, r)
	}
	return rows
}

// selectedRun is the inclusive row run the anchor/cursor rectangle spans,
// clipped to the rows that exist, ok false when it spans none. The cursor sits
// on (0,0) of a grid with no rows, so without the clip an empty result set
// "selects" row 0 and Ctrl+C indexed past the end of it — a crash.
func (g *DataGrid) selectedRun() (r0, r1 int, ok bool) {
	r0, _, r1, _ = g.selectionBounds()
	r1 = min(r1, g.rows.Len()-1)
	return r0, r1, r0 >= 0 && r1 >= r0
}

// SelectedRowCount is len(SelectedRows()) without building the slice: 0 on a
// grid with no rows, so a host's Ctrl+C can tell "nothing to copy" from a
// selected empty cell.
func (g *DataGrid) SelectedRowCount() int { return g.selectedRowCount() }

// selectedRowCount is len(selectedRows()) without building the slice — what
// the context menu asks, on every open, of a selection that may hold a
// million rows.
func (g *DataGrid) selectedRowCount() int {
	if g.marking {
		n := max(0, g.markHi-g.markLo+1)
		for r := range g.markFlips {
			if g.inMarkRun(r) {
				n--
			} else {
				n++
			}
		}
		return n
	}
	r0, r1, ok := g.selectedRun()
	if !ok {
		return 0
	}
	return r1 - r0 + 1
}

// ClearMarkedRows drops any Ctrl+click selection, leaving the cursor where it
// is.
func (g *DataGrid) ClearMarkedRows() {
	g.markFlips, g.marking = nil, false
}

// markRow adds the clicked row to the discontiguous selection, or removes it if
// already in: the toggle Ctrl+click means everywhere. The selection in force is
// folded in first, so Shift-selecting a run then Ctrl+clicking one more row
// keeps the run. That includes the lone cursor row: the grid always highlights
// one and hosts act on it when nothing else is picked, so a Ctrl+click dropping
// it would deselect a row the user can see is selected.
func (g *DataGrid) markRow(row int) {
	if !g.marking {
		r0, r1, ok := g.selectedRun()
		if !ok {
			r0, r1 = 0, -1
		}
		g.markLo, g.markHi, g.markFlips, g.marking = r0, r1, map[int]struct{}{}, true
	}
	if _, in := g.markFlips[row]; in {
		delete(g.markFlips, row)
		return
	}
	g.markFlips[row] = struct{}{}
}

// inMarkRun reports whether row is inside the base run of a discontiguous
// selection — marked unless flipped.
func (g *DataGrid) inMarkRun(row int) bool { return row >= g.markLo && row <= g.markHi }

// rowMarked reports whether row is in a discontiguous selection.
func (g *DataGrid) rowMarked(row int) bool {
	if !g.marking {
		return false
	}
	_, flipped := g.markFlips[row]
	return g.inMarkRun(row) != flipped
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
