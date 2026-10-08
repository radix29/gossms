package controls

import (
	"slices"
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

// A click on a cell-cursor grid has to tell the page its selection moved.
// The Button1 branch used to set selRow/selCol, call activateCell and return
// without ever firing OnSelectRow, so every Properties page with a detail
// panel below its grid — eleven of them — kept describing whatever row the
// keyboard had last left it on while the highlight sat somewhere else.
func TestDataGridClickFiresOnSelectRowOnACellCursorGrid(t *testing.T) {
	g := newCellCursorGrid()
	g.OnActivateCell = func(row, col int) {}

	var selected []int
	g.OnSelectRow = func(row int) { selected = append(selected, row) }

	// Row 1, first column. rowAtY puts data row n at rect.Y+2+n.
	click(t, g, 1, g.rect.Y+3)

	if len(selected) != 1 || selected[0] != 1 {
		t.Fatalf("OnSelectRow calls = %v, want [1]", selected)
	}
	if row, _ := g.SelectedCell(); row != 1 {
		t.Errorf("SelectedCell row = %d, want 1", row)
	}
}

// Selection fires before activation, the order the keyboard already uses: a
// page syncs its detail widgets to the new row, then the toggle runs against
// that row.
func TestDataGridClickSelectsBeforeItActivates(t *testing.T) {
	g := newCellCursorGrid()
	var order []string
	g.OnSelectRow = func(int) { order = append(order, "select") }
	g.OnActivateCell = func(int, int) { order = append(order, "activate") }

	click(t, g, 1, g.rect.Y+3)

	if len(order) != 2 || order[0] != "select" || order[1] != "activate" {
		t.Errorf("callback order = %v, want [select activate]", order)
	}
}

// Clicking the row that is already selected activates without re-firing
// OnSelectRow. A page that redraws its grid from inside OnActivateCell —
// every toggle grid does — would otherwise have its selection callback
// re-entered on each toggle of the row it is already on.
func TestDataGridClickOnTheSelectedRowDoesNotRefireOnSelectRow(t *testing.T) {
	g := newCellCursorGrid()
	activations := 0
	g.OnActivateCell = func(int, int) { activations++ }
	var selected []int
	g.OnSelectRow = func(row int) { selected = append(selected, row) }

	click(t, g, 1, g.rect.Y+3) // row 1: a move
	click(t, g, 1, g.rect.Y+3) // row 1 again: not a move

	if len(selected) != 1 {
		t.Errorf("OnSelectRow calls = %v, want one", selected)
	}
	if activations != 2 {
		t.Errorf("OnActivateCell fired %d times, want 2 — the toggle still runs", activations)
	}
}

// click delivers one complete press/release on a cell. The release matters:
// it clears mouseDragging, which otherwise suppresses the next press on the
// same cell as a resent motion event.
func click(t *testing.T, g *DataGrid, x, y int) {
	t.Helper()
	g.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	g.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
}

// K3: a mouse press fired OnSelectRow on every Button1 event — each motion
// resend while the button was held, and every click on the row already
// selected — where the keyboard fires only on a move. Replication Monitor
// re-read an agent's history on each one. Both shapes of grid (plain, and a
// read-only cell-cursor grid that block-selects) now fire on a move only.
func TestDataGridMouseFiresOnSelectRowOnlyOnAMove(t *testing.T) {
	for _, tc := range []struct {
		name string
		grid func() *DataGrid
	}{
		{"plain", func() *DataGrid {
			g := newCellCursorGrid()
			g.SetCellCursor(false)
			return g
		}},
		{"read-only cell cursor", newCellCursorGrid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.grid()
			var selected []int
			g.OnSelectRow = func(row int) { selected = append(selected, row) }
			y1 := g.rect.Y + 3 // row 1
			g.HandleMouse(tcell.NewEventMouse(1, y1, tcell.Button1, tcell.ModNone))
			g.HandleMouse(tcell.NewEventMouse(2, y1, tcell.Button1, tcell.ModNone)) // held, same row
			g.HandleMouse(tcell.NewEventMouse(1, y1, tcell.Button1, tcell.ModNone))
			g.HandleMouse(tcell.NewEventMouse(1, y1, tcell.ButtonNone, tcell.ModNone))
			click(t, g, 1, y1) // the selected row again
			if !slices.Equal(selected, []int{1}) {
				t.Fatalf("OnSelectRow calls = %v, want [1]: one move", selected)
			}
			g.HandleMouse(tcell.NewEventMouse(1, y1, tcell.Button1, tcell.ModNone))
			g.HandleMouse(tcell.NewEventMouse(1, g.rect.Y+2, tcell.Button1, tcell.ModNone)) // dragged to row 0
			g.HandleMouse(tcell.NewEventMouse(1, g.rect.Y+2, tcell.ButtonNone, tcell.ModNone))
			if !slices.Equal(selected, []int{1, 0}) {
				t.Fatalf("OnSelectRow calls = %v, want [1 0]: a drag onto another row is a move", selected)
			}
		})
	}
}
