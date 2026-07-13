package controls

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// countingRowSource wraps a RowSource and counts calls to Row, so tests can
// pin how many rows computeColWidths actually inspects.
type countingRowSource struct {
	RowSource
	calls int
}

func (c *countingRowSource) Row(i int) []string {
	c.calls++
	return c.RowSource.Row(i)
}

func newTestDataGrid() *DataGrid {
	g := NewDataGrid()
	g.SetBounds(0, 0, 40, 10)
	return g
}

// TestDataGridEmptyBeforeSetData confirms a freshly constructed grid (rows
// still the zero-value SliceRowSource(nil) set in NewDataGrid) never panics
// on navigation or draw before SetData/SetSource is ever called.
func TestDataGridEmptyBeforeSetData(t *testing.T) {
	g := newTestDataGrid()
	if n := g.rows.Len(); n != 0 {
		t.Fatalf("Len() before any data = %d, want 0", n)
	}
	g.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	g.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModNone))
	g.HandleMouse(tcell.NewEventMouse(1, 3, tcell.Button1, tcell.ModNone))
}

// TestDataGridSetDataWrapsSlice confirms SetData (the shape every existing
// caller uses) reaches the grid through the same SetSource/RowSource path
// as a custom source would.
func TestDataGridSetDataWrapsSlice(t *testing.T) {
	g := newTestDataGrid()
	g.SetData([]string{"A", "B"}, [][]string{{"1", "2"}, {"3", "4"}})
	if n := g.rows.Len(); n != 2 {
		t.Fatalf("Len() = %d, want 2", n)
	}
	if got := g.rows.Row(1); got[0] != "3" || got[1] != "4" {
		t.Errorf("Row(1) = %v, want [3 4]", got)
	}
	if !strings.Contains(g.status, "2 rows") {
		t.Errorf("status = %q, want it to mention 2 rows", g.status)
	}
}

// TestDataGridSetSourceCustomImplementation verifies a RowSource that isn't
// SliceRowSource works end to end — the point of the interface.
func TestDataGridSetSourceCustomImplementation(t *testing.T) {
	src := SliceRowSource{{"x"}, {"y"}, {"z"}}
	g := newTestDataGrid()
	g.SetSource([]string{"Col"}, src)
	if n := g.rows.Len(); n != 3 {
		t.Fatalf("Len() = %d, want 3", n)
	}
	g.HandleKey(tcell.NewEventKey(tcell.KeyEnd, "", tcell.ModNone))
	if g.selRow != 2 {
		t.Errorf("selRow after End = %d, want 2 (last row)", g.selRow)
	}
}

// TestComputeColWidthsSamplesOnlyFirstRows pins the sampling cap: a source
// with far more than colWidthSampleRows rows must not have every row
// inspected for column width, and a wide cell beyond the sample window must
// not affect the computed width.
func TestComputeColWidthsSamplesOnlyFirstRows(t *testing.T) {
	rows := make([][]string, colWidthSampleRows+50)
	for i := range rows {
		rows[i] = []string{"short"}
	}
	// A very wide cell placed well past the sample window; if it were
	// inspected, colWidths[0] would grow to reflect it (clamped at 40).
	rows[colWidthSampleRows+10] = []string{strings.Repeat("w", 100)}

	counting := &countingRowSource{RowSource: SliceRowSource(rows)}
	g := newTestDataGrid()
	g.SetSource([]string{"Col"}, counting)

	if counting.calls > colWidthSampleRows {
		t.Errorf("computeColWidths called Row() %d times, want at most %d", counting.calls, colWidthSampleRows)
	}
	// "short" (5) + 2 padding = 7, clamped to [6,40] -> 7. If the wide row
	// beyond the sample had been read, this would be 40 instead.
	if g.colWidths[0] != 7 {
		t.Errorf("colWidths[0] = %d, want 7 (sampling must not reach the wide row)", g.colWidths[0])
	}
}

// TestDataGridSetErrorUsesRowSource confirms SetError's single error row
// goes through the same RowSource plumbing rather than a raw slice field.
func TestDataGridSetErrorUsesRowSource(t *testing.T) {
	g := newTestDataGrid()
	g.SetError(errTest{"boom"})
	if n := g.rows.Len(); n != 1 {
		t.Fatalf("Len() after SetError = %d, want 1", n)
	}
	if got := g.rows.Row(0); got[0] != "boom" {
		t.Errorf("Row(0) = %v, want [boom]", got)
	}
}

type errTest struct{ msg string }

func (e errTest) Error() string { return e.msg }
