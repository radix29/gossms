package controls

import "testing"

// preserveViewGrid is 30 rows of 3 columns in a 7-data-row viewport
// (SetBounds height 10, less the header and status rows), which is enough for
// the scroll position to be somewhere other than the top.
func preserveViewGrid() *DataGrid {
	g := newTestDataGrid()
	rows := make([][]string, 30)
	for i := range rows {
		rows[i] = []string{"name", "[ ]", "note"}
	}
	g.SetData([]string{"Permission", "State", "Note"}, rows)
	g.SetCellCursor(true)
	return g
}

// scrollToRowAtTop leaves row at the top of the viewport with the cursor on
// it — the position arrowing *up* through a list produces, and the one where
// a redraw's scroll loss is visible. Going past it first is what puts the
// scroll somewhere other than 0.
func scrollToRowAtTop(g *DataGrid, row, col int) {
	g.SetSelectedCell(row+6, col)
	g.SetSelectedCell(row, col)
}

// SetDataPreservingView is the whole contract in one place: the cell cursor,
// the scroll position and a dragged column width all survive a redraw of the
// same rows.
func TestSetDataPreservingViewKeepsCursorScrollAndWidths(t *testing.T) {
	g := preserveViewGrid()
	scrollToRowAtTop(g, 14, 1)
	g.SetColumnWidth(0, 25)

	if got := g.ScrollRow(); got != 14 {
		t.Fatalf("ScrollRow() before redraw = %d, want 14", got)
	}

	rows := make([][]string, 30)
	for i := range rows {
		rows[i] = []string{"name", "[ ]", "note"}
	}
	rows[14][1] = "[x]" // the toggle the redraw exists to show
	g.SetDataPreservingView([]string{"Permission", "State", "Note"}, rows)

	if row, col := g.SelectedCell(); row != 14 || col != 1 {
		t.Errorf("SelectedCell() = (%d,%d), want (14,1)", row, col)
	}
	if got := g.ScrollRow(); got != 14 {
		t.Errorf("ScrollRow() = %d, want 14 — the list moved under the user", got)
	}
	if got := g.ColumnWidth(0); got != 25 {
		t.Errorf("ColumnWidth(0) = %d, want 25 — the dragged width was dropped", got)
	}
}

// The behaviour SetDataPreservingView replaced, pinned so the difference stays
// visible: SetData followed by SetSelectedCell restores the cursor but scrolls
// to it from zero, which lands the row against the bottom edge instead of the
// top one it was on. A test that only checked the cursor would pass on both.
func TestSetDataThenSetSelectedCellStillMovesTheView(t *testing.T) {
	g := preserveViewGrid()
	scrollToRowAtTop(g, 14, 1)

	rows := make([][]string, 30)
	for i := range rows {
		rows[i] = []string{"name", "[ ]", "note"}
	}
	g.SetData([]string{"Permission", "State", "Note"}, rows)
	g.SetSelectedCell(14, 1)

	if row, col := g.SelectedCell(); row != 14 || col != 1 {
		t.Fatalf("SelectedCell() = (%d,%d), want (14,1)", row, col)
	}
	if got := g.ScrollRow(); got == 14 {
		t.Fatal("ScrollRow() = 14 — SetData no longer resets the scroll, so " +
			"SetDataPreservingView's restore may be redundant; re-check it")
	} else if got != 8 {
		t.Errorf("ScrollRow() = %d, want 8 (row 14 dragged to the bottom edge)", got)
	}
}

// A redraw that reshapes the grid must not carry the old view onto it: a width
// restored past the new column count is ignored, and the scroll and cursor
// clamp into the shorter list rather than pointing off the end.
func TestSetDataPreservingViewClampsOntoASmallerGrid(t *testing.T) {
	g := preserveViewGrid()
	scrollToRowAtTop(g, 14, 2)
	g.SetColumnWidth(2, 25)

	g.SetDataPreservingView([]string{"Permission"}, [][]string{{"one"}, {"two"}})

	if row, col := g.SelectedCell(); row != 1 || col != 0 {
		t.Errorf("SelectedCell() = (%d,%d), want (1,0) — clamped into the new shape", row, col)
	}
	if got := g.ScrollRow(); got != 0 {
		t.Errorf("ScrollRow() = %d, want 0 — two rows cannot be scrolled", got)
	}
	if got := g.ColumnWidth(2); got != 0 {
		t.Errorf("ColumnWidth(2) = %d, want 0 — column 2 no longer exists", got)
	}
}
