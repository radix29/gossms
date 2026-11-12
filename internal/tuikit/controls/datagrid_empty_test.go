package controls

import "testing"

// newEmptyResultGrid is a read-only results grid holding a result set with
// columns but no rows — what `SELECT … WHERE 1 = 0` returns. Its cursor still
// sits on (0,0), the cell that does not exist.
func newEmptyResultGrid() *DataGrid {
	g := newTestDataGrid()
	g.SetData([]string{"A", "B"}, nil)
	g.SetCellCursor(true)
	return g
}

// TestEmptyGridSelectsNothing: Ctrl+C on an empty results grid indexed row 0
// and crashed the application, because the selection reported the
// cursor's row whether or not it existed.
func TestEmptyGridSelectsNothing(t *testing.T) {
	g := newEmptyResultGrid()
	if got := g.SelectedRows(); got != nil {
		t.Errorf("SelectedRows() = %v, want nil", got)
	}
	if got := g.SelectedRowCount(); got != 0 {
		t.Errorf("SelectedRowCount() = %d, want 0", got)
	}
	if got := g.SelectedCellsText(); got != "" {
		t.Errorf("SelectedCellsText() = %q, want \"\"", got)
	}
}

// TestCellMenuOutlivingItsRowsDoesNothing: the menu is built on a populated
// grid, then a refresh empties it while the menu is open (the Always On
// dashboard reloads on a timer). Copy must not blank the clipboard and Show
// Value must not index the missing row.
func TestCellMenuOutlivingItsRowsDoesNothing(t *testing.T) {
	g := newTestDataGrid()
	g.SetData([]string{"A", "B"}, [][]string{{"a0", "b0"}, {"a1", "b1"}})
	g.SetCellCursor(true)
	g.SetSelectedCell(1, 1)
	copied := false
	g.OnCopyRequest = func(string) { copied = true }
	items := g.cellContextMenuItems()

	g.SetDataPreservingView([]string{"A", "B"}, nil)
	for _, it := range items {
		switch it.Label {
		case "Copy", Strings.ShowValue:
			it.Action()
		}
	}
	if copied {
		t.Error("Copy on an emptied grid reached OnCopyRequest")
	}
	if g.viewer.open {
		t.Error("Show Value on an emptied grid opened the viewer")
	}
}

// TestShrunkSelectionCopiesOnlyRowsThatExist: a block selection over rows 1–3
// of a source now holding two rows copies just row 1.
func TestShrunkSelectionCopiesOnlyRowsThatExist(t *testing.T) {
	g := newTestDataGrid()
	g.SetData([]string{"A", "B"}, [][]string{{"a0", "b0"}, {"a1", "b1"}})
	g.SetCellCursor(true)
	g.selAnchorRow, g.selAnchorCol, g.blockSelecting = 1, 0, true
	g.selRow, g.selCol = 3, 1
	if got, want := g.SelectedCellsText(), "a1\tb1"; got != want {
		t.Errorf("SelectedCellsText() = %q, want %q", got, want)
	}
}
