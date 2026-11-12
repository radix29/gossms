package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// OnRowDoubleClick is a read-only grid's mouse spelling of the host's Enter
// (the Query Store panel's wait-category drill-down). tcell reports presses,
// not clicks, and resends Button1 on every motion while it is held, so the
// grid has to tell a second press from a held first one.

// newDoubleClickGrid is a read-only cell-cursor grid with three rows, counting
// the rows OnRowDoubleClick reports.
func newDoubleClickGrid() (*DataGrid, *[]int) {
	g := newTestDataGrid()
	g.SetData([]string{"Category", "Wait"}, [][]string{{"CPU", "1"}, {"Lock", "2"}, {"Latch", "3"}})
	g.SetCellCursor(true)
	var got []int
	g.OnRowDoubleClick = func(row int) {
		// Called once the press has moved the selection onto the row: the host reads
		// the row from the grid, not the argument alone.
		if g.SelectedRow() != row {
			panic("OnRowDoubleClick fired before the selection reached the row")
		}
		got = append(got, row)
	}
	return g, &got
}

// pressRow and releaseRow are one Button1 press on data row row (rows start below the
// header and its separator) and the release after it.
func pressRow(g *DataGrid, row int, mods tcell.ModMask) {
	g.HandleMouse(tcell.NewEventMouse(2, 2+row, tcell.Button1, mods))
}

func releaseRow(g *DataGrid, row int) {
	g.HandleMouse(tcell.NewEventMouse(2, 2+row, tcell.ButtonNone, tcell.ModNone))
}

func TestARowDoubleClickFiresOnceOnTheSecondPress(t *testing.T) {
	g, got := newDoubleClickGrid()
	pressRow(g, 1, tcell.ModNone)
	releaseRow(g, 1)
	if len(*got) != 0 {
		t.Fatalf("fired %v on the first press", *got)
	}
	pressRow(g, 1, tcell.ModNone)
	releaseRow(g, 1)
	if len(*got) != 1 || (*got)[0] != 1 {
		t.Fatalf("fired %v, want once for row 1", *got)
	}
	// A third press starts a new pair rather than firing again.
	pressRow(g, 1, tcell.ModNone)
	releaseRow(g, 1)
	if len(*got) != 1 {
		t.Errorf("fired %v after a third press, want still once", *got)
	}
}

func TestAHeldPressIsNotADoubleClick(t *testing.T) {
	g, got := newDoubleClickGrid()
	// tcell's resends while the button stays down.
	pressRow(g, 0, tcell.ModNone)
	pressRow(g, 0, tcell.ModNone)
	pressRow(g, 0, tcell.ModNone)
	releaseRow(g, 0)
	if len(*got) != 0 {
		t.Errorf("fired %v on one held press", *got)
	}
}

func TestPressesOnTwoRowsAreNotADoubleClick(t *testing.T) {
	g, got := newDoubleClickGrid()
	pressRow(g, 0, tcell.ModNone)
	releaseRow(g, 0)
	pressRow(g, 2, tcell.ModNone)
	releaseRow(g, 2)
	if len(*got) != 0 {
		t.Errorf("fired %v for presses on rows 0 and 2", *got)
	}
}

func TestAModifiedPressIsASelectionNotAHalfOfADoubleClick(t *testing.T) {
	for _, mod := range []tcell.ModMask{tcell.ModCtrl, tcell.ModShift} {
		g, got := newDoubleClickGrid()
		pressRow(g, 1, tcell.ModNone)
		releaseRow(g, 1)
		pressRow(g, 1, mod)
		releaseRow(g, 1)
		if len(*got) != 0 {
			t.Errorf("mod %v: fired %v", mod, *got)
		}
	}
}

func TestANewRowSetForgetsThePendingPress(t *testing.T) {
	g, got := newDoubleClickGrid()
	pressRow(g, 1, tcell.ModNone)
	releaseRow(g, 1)
	g.SetData([]string{"Query"}, [][]string{{"a"}, {"b"}})
	pressRow(g, 1, tcell.ModNone)
	releaseRow(g, 1)
	if len(*got) != 0 {
		t.Errorf("fired %v across a SetData", *got)
	}
}

func TestAnEditableGridNeverReportsADoubleClick(t *testing.T) {
	g, got := newDoubleClickGrid()
	g.OnActivateCell = func(int, int) {}
	for range 2 {
		pressRow(g, 1, tcell.ModNone)
		releaseRow(g, 1)
	}
	if len(*got) != 0 {
		t.Errorf("fired %v on an editable grid", *got)
	}
}

// The query results grid binds a double-click to ShowValue, which opens the popup
// mid-press: the release then lands on the popup, never reaching the grid's own
// release handling. After the popup closes, the grid's next double-click must
// still be two presses, and its first click a fresh one, not a drag continuation.
func TestShowValueFromADoubleClickLeavesNoPressLatched(t *testing.T) {
	g, got := newDoubleClickGrid()
	g.OnRowDoubleClick = func(row int) {
		*got = append(*got, row)
		g.ShowValue()
	}
	pressRow(g, 0, tcell.ModNone)
	releaseRow(g, 0)
	pressRow(g, 0, tcell.ModNone)
	if !g.viewer.open {
		t.Fatal("the double-click did not open the value popup")
	}
	releaseRow(g, 0)
	g.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone))
	if g.viewer.open {
		t.Fatal("Escape did not close the popup")
	}
	pressRow(g, 2, tcell.ModNone)
	releaseRow(g, 2)
	if r, _ := g.SelectedCell(); r != 2 {
		t.Fatalf("the first click after closing selected row %d, want 2", r)
	}
	pressRow(g, 2, tcell.ModNone)
	if len(*got) != 2 || (*got)[1] != 2 {
		t.Errorf("fired %v, want [0 2]: a press latch outlived the popup", *got)
	}
}
