package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/tuikit/controls"
)

// resetGrid is what a Properties page calls when an Add, a Remove or a Revert
// changes the row *set* and it wants a particular row selected afterwards.
//
// The tests below pin the one thing that distinguishes it from the
// SetData + SetSelectedRow pair seventeen pages used to hand-roll: DataGrid.
// SetSource clears colWidthOverride, so that pair silently threw away a column
// the user had dragged wider. Setting the cursor explicitly hid the loss —
// there was no cursor jump to notice — which is why it survived so long.

// gridWithWidths builds a grid the way a page does and drags column 0 wider,
// returning the width the drag settled on.
func gridWithWidths(t *testing.T, headers []string, rows [][]string, col, width int) (*controls.DataGrid, int) {
	t.Helper()
	g := controls.NewDataGrid()
	g.SetBounds(0, 0, 120, 20)
	g.SetData(headers, rows)
	g.SetCellCursor(true)
	g.SetColumnWidth(col, width)
	got := g.ColumnWidth(col)
	if got != width {
		t.Fatalf("dragging column %d to %d gave %d — the fixture cannot show the loss it exists to test", col, width, got)
	}
	return g, got
}

var resetGridHeaders = []string{"Member", "Type"}

// TestResetGridKeepsADraggedColumnWidth is the regression itself.
func TestResetGridKeepsADraggedColumnWidth(t *testing.T) {
	rows := [][]string{{"alice", "SQL user"}, {"bob", "SQL user"}}
	g, want := gridWithWidths(t, resetGridHeaders, rows, 0, 40)

	rows = append(rows, []string{"carol", "SQL user"})
	resetGrid(g, resetGridHeaders, rows, len(rows)-1)

	if got := g.ColumnWidth(0); got != want {
		t.Errorf("column 0 is %d wide after adding a row, want %d — the drag was discarded", got, want)
	}
}

// TestResetGridSelectsTheRequestedRow — the other half of the pair. Without
// it a caller could "fix" the width by calling redrawGrid and silently lose
// the selection an Add depends on.
func TestResetGridSelectsTheRequestedRow(t *testing.T) {
	rows := [][]string{{"alice", "SQL user"}, {"bob", "SQL user"}, {"carol", "SQL user"}}
	g, _ := gridWithWidths(t, resetGridHeaders, rows, 0, 40)
	g.SetSelectedRow(0)

	resetGrid(g, resetGridHeaders, rows, 2)

	if got := g.SelectedRow(); got != 2 {
		t.Errorf("SelectedRow() = %d after resetGrid(..., 2), want 2", got)
	}
}

// TestSetDataDiscardsADraggedColumnWidth pins the behaviour resetGrid exists
// to work around, so a future reader can see that SetData is not merely
// equivalent-but-longer. If this ever fails, DataGrid changed and resetGrid's
// docstring is out of date.
func TestSetDataDiscardsADraggedColumnWidth(t *testing.T) {
	rows := [][]string{{"alice", "SQL user"}, {"bob", "SQL user"}}
	g, want := gridWithWidths(t, resetGridHeaders, rows, 0, 40)

	g.SetData(resetGridHeaders, rows)

	if got := g.ColumnWidth(0); got == want {
		t.Errorf("SetData preserved column 0 at %d; resetGrid's reason for existing is gone", got)
	}
}
