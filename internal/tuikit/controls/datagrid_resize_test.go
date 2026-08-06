package controls

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// newResizeTestGrid returns a grid whose first column is content-clamped to
// the max default width of 10, so a drag on its separator has room to move
// in both directions.
func newResizeTestGrid() *DataGrid {
	g := NewDataGrid()
	g.SetBounds(0, 0, 60, 10)
	g.SetMaxCellWidth(10)
	g.SetData([]string{"A", "B"}, [][]string{{strings.Repeat("x", 100), "b"}})
	return g
}

// press/drag/release drive one mouse gesture at the given x on the header row.
func press(g *DataGrid, x int) bool {
	return g.HandleMouse(tcell.NewEventMouse(x, 0, tcell.Button1, tcell.ModNone))
}

func release(g *DataGrid, x int) {
	g.HandleMouse(tcell.NewEventMouse(x, 0, tcell.ButtonNone, tcell.ModNone))
}

// TestColumnResizeDragWidensLeftColumn confirms dragging a header separator
// right widens the column to its left (SSMS's behavior) — past the max
// default width, which caps only what content alone may claim.
func TestColumnResizeDragWidensLeftColumn(t *testing.T) {
	g := newResizeTestGrid()
	if g.colWidths[0] != 10 {
		t.Fatalf("initial colWidths[0] = %d, want 10 (max default clamp)", g.colWidths[0])
	}
	// Column 0 spans x 0..9, so its separator is drawn at x 9.
	if !press(g, 9) {
		t.Fatal("press on separator not handled")
	}
	g.HandleMouse(tcell.NewEventMouse(24, 0, tcell.Button1, tcell.ModNone))
	release(g, 24)
	if g.colWidths[0] != 25 {
		t.Errorf("colWidths[0] after dragging +15 = %d, want 25", g.colWidths[0])
	}
	if g.colResizing {
		t.Error("colResizing still latched after release")
	}
}

// TestColumnResizeDragNarrows confirms a leftward drag narrows the column
// and can't shrink it below minResizeWidth.
func TestColumnResizeDragNarrows(t *testing.T) {
	g := newResizeTestGrid()
	press(g, 9)
	g.HandleMouse(tcell.NewEventMouse(4, 0, tcell.Button1, tcell.ModNone))
	if g.colWidths[0] != 5 {
		t.Errorf("colWidths[0] after dragging -5 = %d, want 5", g.colWidths[0])
	}
	g.HandleMouse(tcell.NewEventMouse(-40, 0, tcell.Button1, tcell.ModNone))
	if g.colWidths[0] != minResizeWidth {
		t.Errorf("colWidths[0] after dragging far left = %d, want %d", g.colWidths[0], minResizeWidth)
	}
	release(g, -40)
}

// TestColumnResizeSurvivesRecompute confirms a dragged width outlives the
// recomputes a bounds change or a progressive backfill trigger — those must
// not silently pull the column back to its content-based default.
func TestColumnResizeSurvivesRecompute(t *testing.T) {
	g := newResizeTestGrid()
	press(g, 9)
	g.HandleMouse(tcell.NewEventMouse(29, 0, tcell.Button1, tcell.ModNone))
	release(g, 29)
	g.SetBounds(0, 0, 80, 10)
	if g.colWidths[0] != 30 {
		t.Errorf("colWidths[0] after SetBounds = %d, want 30", g.colWidths[0])
	}
	g.RefreshColumnWidths()
	g.computeColWidths()
	if g.colWidths[0] != 30 {
		t.Errorf("colWidths[0] after RefreshColumnWidths = %d, want 30", g.colWidths[0])
	}
	// New data is a new set of columns — widths go back to the default.
	g.SetData([]string{"A", "B"}, [][]string{{"x", "b"}})
	if g.colWidths[0] != 6 {
		t.Errorf("colWidths[0] after SetData = %d, want 6 (default restored)", g.colWidths[0])
	}
}

// TestColumnResizeDoubleClickRestoresDefault confirms two quick presses on
// the same separator put the column back to the width it was given
// initially.
func TestColumnResizeDoubleClickRestoresDefault(t *testing.T) {
	g := newResizeTestGrid()
	press(g, 9)
	g.HandleMouse(tcell.NewEventMouse(29, 0, tcell.Button1, tcell.ModNone))
	release(g, 29)
	if g.colWidths[0] != 30 {
		t.Fatalf("colWidths[0] after drag = %d, want 30", g.colWidths[0])
	}
	// The separator moved with the column: it now sits at x 29.
	press(g, 29)
	release(g, 29)
	press(g, 29)
	release(g, 29)
	if g.colWidths[0] != 10 {
		t.Errorf("colWidths[0] after double-click = %d, want 10 (max default clamp)", g.colWidths[0])
	}
}

// TestColumnResizeLastColumnBeatsFill confirms a dragged last column keeps
// the width it was given instead of being stretched back out to the rect by
// SetFillLastColumn (the Property/Value detail grids).
func TestColumnResizeLastColumnBeatsFill(t *testing.T) {
	g := NewDataGrid()
	g.SetBounds(0, 0, 60, 10)
	g.SetFillLastColumn(true)
	g.SetData([]string{"Property", "Value"}, [][]string{{"a", "b"}})
	if g.colWidths[1] <= 10 {
		t.Fatalf("filled last column width = %d, want the rect's remainder", g.colWidths[1])
	}
	sepX := g.colWidths[0] + g.colWidths[1] - 1
	press(g, sepX)
	g.HandleMouse(tcell.NewEventMouse(sepX-20, 0, tcell.Button1, tcell.ModNone))
	release(g, sepX-20)
	if want := g.colWidths[1]; want != 60-g.colWidths[0]-20 {
		t.Errorf("last column after drag = %d, want %d", want, 60-g.colWidths[0]-20)
	}
	g.SetBounds(0, 0, 80, 10)
	if g.colWidths[1] != 60-g.colWidths[0]-20 {
		t.Errorf("last column re-filled to %d after SetBounds; the drag must win", g.colWidths[1])
	}
}

// TestColumnResizeIgnoresDataRows confirms the separator only grabs in the
// header row — the same glyph runs down every data row, and claiming it
// there would break cell selection.
func TestColumnResizeIgnoresDataRows(t *testing.T) {
	g := newResizeTestGrid()
	// y 2 is the first data row (header, separator line, then data).
	if g.HandleMouse(tcell.NewEventMouse(9, 2, tcell.Button1, tcell.ModNone)); g.colResizing {
		t.Error("press on a data row's separator started a resize")
	}
	g.HandleMouse(tcell.NewEventMouse(20, 2, tcell.Button1, tcell.ModNone))
	if g.colWidths[0] != 10 {
		t.Errorf("colWidths[0] = %d, want 10 (unchanged by data-row drag)", g.colWidths[0])
	}
}

// TestColumnResizeWithRowNumbers confirms separator hit-testing accounts for
// the row-number gutter, which shifts every column right (query-results
// grid).
func TestColumnResizeWithRowNumbers(t *testing.T) {
	g := NewDataGrid()
	g.SetRowNumbers(true)
	g.SetBounds(0, 0, 60, 10)
	g.SetMaxCellWidth(10)
	g.SetData([]string{"A", "B"}, [][]string{{strings.Repeat("x", 100), "b"}})
	gw := g.gutterWidth()
	if col, ok := g.sepColAt(gw+g.colWidths[0]-1, 0); !ok || col != 0 {
		t.Fatalf("sepColAt(gutter+w-1) = (%d, %v), want (0, true)", col, ok)
	}
	if _, ok := g.sepColAt(g.colWidths[0]-1, 0); ok {
		t.Error("sepColAt hit a separator at the pre-gutter position")
	}
}
