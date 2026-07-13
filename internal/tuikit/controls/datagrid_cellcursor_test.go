package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

func newCellCursorGrid() *DataGrid {
	g := newTestDataGrid()
	g.SetData([]string{"Grant", "Deny"}, [][]string{
		{"[ ]", "[ ]"},
		{"[x]", "[ ]"},
	})
	g.SetCellCursor(true)
	return g
}

func TestDataGridCellCursorNavigation(t *testing.T) {
	g := newCellCursorGrid()
	if row, col := g.SelectedCell(); row != 0 || col != 0 {
		t.Fatalf("initial SelectedCell() = (%d,%d), want (0,0)", row, col)
	}
	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
	if _, col := g.SelectedCell(); col != 1 {
		t.Fatalf("col after Right = %d, want 1", col)
	}
	// Right again must clamp at the last column, not wrap or overflow.
	g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
	if _, col := g.SelectedCell(); col != 1 {
		t.Fatalf("col after second Right = %d, want 1 (clamped)", col)
	}
	g.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	if row, _ := g.SelectedCell(); row != 1 {
		t.Fatalf("row after Down = %d, want 1", row)
	}
}

func TestDataGridOnActivateCellViaSpaceAndEnter(t *testing.T) {
	g := newCellCursorGrid()
	var got []struct{ row, col int }
	g.OnActivateCell = func(row, col int) { got = append(got, struct{ row, col int }{row, col}) }

	g.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone))
	g.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	if len(got) != 2 {
		t.Fatalf("OnActivateCell fired %d times, want 2", len(got))
	}
	if got[0].row != 0 || got[0].col != 0 {
		t.Errorf("first activation = %+v, want (0,0)", got[0])
	}
}

func TestDataGridOnActivateCellViaClick(t *testing.T) {
	g := newCellCursorGrid()
	var activated bool
	var gotRow, gotCol int
	g.OnActivateCell = func(row, col int) { activated = true; gotRow, gotCol = row, col }

	// colWidths for "Grant"/"Deny" headers (5,4) + 2 padding, clamped to
	// [6,40] -> col0 width 7, col1 width 6. Row 1 is at rect.Y+2+1.
	x := g.colWidths[0] + 2 // land inside column 1
	y := g.rect.Y + 2 + 1   // data row index 1
	g.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	if !activated {
		t.Fatal("click on a cell in cell-cursor mode did not fire OnActivateCell")
	}
	if gotRow != 1 || gotCol != 1 {
		t.Fatalf("activated cell = (%d,%d), want (1,1)", gotRow, gotCol)
	}
}

func TestDataGridSelectedRowClampsAndScrolls(t *testing.T) {
	rows := make([][]string, 20)
	for i := range rows {
		rows[i] = []string{"x"}
	}
	g := newTestDataGrid()
	g.SetData([]string{"Col"}, rows)
	g.SetSelectedRow(15)
	if g.SelectedRow() != 15 {
		t.Fatalf("SelectedRow() = %d, want 15", g.SelectedRow())
	}
	if g.scrollRow == 0 {
		t.Error("SetSelectedRow(15) on a 10-tall grid should have scrolled, scrollRow is still 0")
	}
	g.SetSelectedRow(100)
	if g.SelectedRow() != 19 {
		t.Fatalf("SelectedRow() after out-of-range set = %d, want 19 (clamped)", g.SelectedRow())
	}
}

func TestDataGridRowReturnsUnderlyingCells(t *testing.T) {
	g := newTestDataGrid()
	g.SetData([]string{"A", "B"}, [][]string{{"1", "2"}})
	if got := g.Row(0); got[0] != "1" || got[1] != "2" {
		t.Errorf("Row(0) = %v, want [1 2]", got)
	}
	if got := g.Row(5); got != nil {
		t.Errorf("Row(5) out of range = %v, want nil", got)
	}
}
