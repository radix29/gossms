package controls

import (
	"errors"
	"testing"
)

// The three entry points that replace a grid's contents outright — SetData
// (via SetSource), SetSource itself and SetError — must all leave the same
// view state behind. SetError shipped resetting three of the four scroll and
// cursor fields: it left scrollCol where it was, and since the error is one
// column at index 0, drawRow's `for i := g.scrollCol; i < len(g.colWidths)`
// never ran and the message drew blank on any grid the user had scrolled
// right. That is reachable from every DataGrid.SetError caller in the
// application — the Detail Browser, both Query Store grids and the Log File
// Viewer are all wide, horizontally scrollable grids.
//
// One table over all three rather than a test per function: the bug was not
// that SetError was wrong on its own terms, it was that it drifted from the
// other two, and only a shared assertion catches that.
func TestEveryResetPathClearsTheView(t *testing.T) {
	cols := []string{"a", "b", "c", "d", "e", "f"}
	// Enough rows to scroll vertically in the 7-data-row viewport
	// newTestDataGrid's height leaves, or SetScroll clamps the row back to 0.
	rows := make([][]string, 30)
	for i := range rows {
		rows[i] = []string{"1", "2", "3", "4", "5", "6"}
	}

	// scrolled builds a grid parked away from the origin in both axes, with a
	// dragged column width and a marked row, so every field a reset owes is
	// non-zero before it runs.
	scrolled := func() *DataGrid {
		g := newTestDataGrid()
		g.SetData(cols, rows)
		g.SetCellCursor(true)
		g.SetSelectedCell(12, 5)
		g.SetScroll(10, 4)
		g.SetColumnWidth(0, 25)
		// Ctrl+click's discontiguous selection, set directly — there is no
		// exported way in, and the mouse route is another test's subject.
		g.markedRows = map[int]bool{0: true, 12: true}
		if g.scrollCol == 0 || g.scrollRow == 0 || g.selCol == 0 {
			t.Fatalf("setup left the view at the origin: scroll=(%d,%d) sel=(%d,%d)",
				g.scrollRow, g.scrollCol, g.selRow, g.selCol)
		}
		return g
	}

	for _, tc := range []struct {
		name  string
		reset func(*DataGrid)
	}{
		{"SetData", func(g *DataGrid) { g.SetData([]string{"x"}, [][]string{{"1"}}) }},
		{"SetSource", func(g *DataGrid) { g.SetSource([]string{"x"}, SliceRowSource{{"1"}}) }},
		{"SetError", func(g *DataGrid) { g.SetError(errors.New("boom")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := scrolled()
			tc.reset(g)
			if g.selRow != 0 || g.selCol != 0 {
				t.Errorf("cell cursor = (%d,%d), want (0,0)", g.selRow, g.selCol)
			}
			if g.scrollRow != 0 || g.scrollCol != 0 {
				t.Errorf("scroll = (%d,%d), want (0,0) — a non-zero scrollCol draws the new first column off the left edge",
					g.scrollRow, g.scrollCol)
			}
			if g.colWidthOverride != nil {
				t.Errorf("colWidthOverride = %v, want nil — the widths belong to the old columns", g.colWidthOverride)
			}
			if n := len(g.SelectedRows()); n != 1 {
				t.Errorf("SelectedRows() = %d rows, want 1 — row marks from the old set name different objects", n)
			}
		})
	}
}

// SetError sizes its one column to the whole rect by hand, so it must also
// drop a RefreshColumnWidths queued before it: the Detail Browser's backfill
// queues one per row, and Draw would otherwise recompute over the top and clip
// the message to the width of the word "Error".
func TestSetErrorSurvivesAPendingWidthRefresh(t *testing.T) {
	g := newTestDataGrid()
	g.SetData([]string{"a", "b"}, [][]string{{"1", "2"}})
	g.RefreshColumnWidths()
	g.SetError(errors.New("a message far longer than the header"))

	want := g.rect.W - 2
	if g.widthsDirty {
		t.Fatal("widthsDirty still set: the next Draw recomputes over the full-width error column")
	}
	if len(g.colWidths) != 1 || g.colWidths[0] != want {
		t.Fatalf("colWidths = %v, want [%d] (the full rect)", g.colWidths, want)
	}
}
