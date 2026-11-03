package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// newRowHeaderGrid is newMarkSelectGrid with the row-number gutter on — the
// query-results configuration.
func newRowHeaderGrid() *DataGrid {
	g := newMarkSelectGrid()
	g.SetRowNumbers(true)
	return g
}

// pressGutter sends one Button1 event on the row-number gutter of data row row.
func pressGutter(g *DataGrid, row int, mods tcell.ModMask) {
	g.HandleMouse(tcell.NewEventMouse(g.rect.X, g.rect.Y+2+row, tcell.Button1, mods))
}

func releaseGutter(g *DataGrid) {
	g.HandleMouse(tcell.NewEventMouse(0, 0, tcell.ButtonNone, tcell.ModNone))
}

func wantBounds(t *testing.T, g *DataGrid, r0, c0, r1, c1 int, what string) {
	t.Helper()
	if a, b, c, d := g.SelectionBounds(); a != r0 || b != c0 || c != r1 || d != c1 {
		t.Fatalf("%s: SelectionBounds() = (%d,%d,%d,%d), want (%d,%d,%d,%d)", what, a, b, c, d, r0, c0, r1, c1)
	}
}

// TestRowNumberClickSelectsWholeRow is the feature: a click on a row number
// selects every column of that row, as SSMS's row header does.
func TestRowNumberClickSelectsWholeRow(t *testing.T) {
	g := newRowHeaderGrid()
	pressGutter(g, 2, tcell.ModNone)
	releaseGutter(g)
	wantBounds(t, g, 2, 0, 2, 1, "click on row number 3")
	wantRows(t, g, []int{2}, "click on row number 3")
}

// TestRowNumberDragSelectsRowRun confirms a drag down the gutter selects the
// whole rows it crosses, and stays a row selection when it wanders onto cells.
func TestRowNumberDragSelectsRowRun(t *testing.T) {
	g := newRowHeaderGrid()
	pressGutter(g, 1, tcell.ModNone)
	pressGutter(g, 2, tcell.ModNone)
	x, y := rowXY(g, 3)
	g.HandleMouse(tcell.NewEventMouse(g.gutterWidth()+x, y, tcell.Button1, tcell.ModNone))
	releaseGutter(g)
	wantBounds(t, g, 1, 0, 3, 1, "drag from row 1 to a cell of row 3")
}

// TestRowNumberShiftClickExtends confirms Shift+click on a row number extends
// whole rows from the earlier anchor.
func TestRowNumberShiftClickExtends(t *testing.T) {
	g := newRowHeaderGrid()
	pressGutter(g, 1, tcell.ModNone)
	releaseGutter(g)
	pressGutter(g, 3, tcell.ModShift)
	releaseGutter(g)
	wantBounds(t, g, 1, 0, 3, 1, "Shift+click on row number 4")
}

// TestRowNumberCtrlClickMarksRows confirms Ctrl+click on row numbers builds the
// discontiguous selection, toggling once per press.
func TestRowNumberCtrlClickMarksRows(t *testing.T) {
	g := newRowHeaderGrid()
	pressGutter(g, 0, tcell.ModNone)
	releaseGutter(g)
	pressGutter(g, 2, tcell.ModCtrl)
	pressGutter(g, 2, tcell.ModCtrl) // tcell's resend while held
	releaseGutter(g)
	wantRows(t, g, []int{0, 2}, "Ctrl+click on row number 3")
}

// TestRowNumberDoubleClickFiresNoRowDoubleClick confirms a double-click on a
// row number stays a row selection rather than opening the cell under the
// cursor (Show Value, on query results).
func TestRowNumberDoubleClickFiresNoRowDoubleClick(t *testing.T) {
	g := newRowHeaderGrid()
	fired := false
	g.OnRowDoubleClick = func(int) { fired = true }
	for range 2 {
		pressGutter(g, 1, tcell.ModNone)
		releaseGutter(g)
	}
	if fired {
		t.Fatal("double-click on a row number fired OnRowDoubleClick")
	}
}
