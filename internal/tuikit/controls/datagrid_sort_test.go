package controls

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// headerText reads the grid's header row off the screen.
func headerText(s tcell.Screen, w int) string {
	var b strings.Builder
	for x := range w {
		str, _, _ := s.Get(x, 0)
		b.WriteString(str)
	}
	return b.String()
}

// A press on a header cell fires OnHeaderClick once for that column, however
// many Button1 resends tcell sends while it is held, and again only after a
// release.
func TestDataGridHeaderClickFiresOncePerPress(t *testing.T) {
	g := newTestDataGrid()
	g.SetCellCursor(true)
	g.SetData([]string{"Name", "Rows"}, [][]string{{"a", "1"}, {"b", "2"}})
	var clicks []int
	g.OnHeaderClick = func(col int) { clicks = append(clicks, col) }

	x := g.ColumnWidth(0) + 1 // inside column 1, clear of its separator
	for range 3 {
		g.HandleMouse(tcell.NewEventMouse(x, 0, tcell.Button1, tcell.ModNone))
	}
	if len(clicks) != 1 || clicks[0] != 1 {
		t.Fatalf("clicks = %v after one held press, want [1]", clicks)
	}
	// A drag off the header onto the rows stays the header's.
	g.HandleMouse(tcell.NewEventMouse(1, 3, tcell.Button1, tcell.ModNone))
	if r, _ := g.SelectedCell(); r != 0 {
		t.Errorf("a drag from the header moved the cursor to row %d", r)
	}
	g.HandleMouse(tcell.NewEventMouse(1, 3, tcell.ButtonNone, tcell.ModNone))
	g.HandleMouse(tcell.NewEventMouse(1, 0, tcell.Button1, tcell.ModNone))
	if len(clicks) != 2 || clicks[1] != 0 {
		t.Fatalf("clicks = %v after a second press on column 0, want [1 0]", clicks)
	}
}

// The separator resizes, and a drag begun on a data cell is a selection: neither
// is a header click.
func TestDataGridHeaderClickSkipsSeparatorAndCellDrags(t *testing.T) {
	g := newTestDataGrid()
	g.SetCellCursor(true)
	g.SetData([]string{"Name", "Rows"}, [][]string{{"a", "1"}, {"b", "2"}})
	fired := false
	g.OnHeaderClick = func(int) { fired = true }

	sep := g.ColumnWidth(0) - 1
	g.HandleMouse(tcell.NewEventMouse(sep, 0, tcell.Button1, tcell.ModNone))
	g.HandleMouse(tcell.NewEventMouse(sep, 0, tcell.ButtonNone, tcell.ModNone))
	if fired {
		t.Error("a press on the column separator fired OnHeaderClick")
	}
	g.HandleMouse(tcell.NewEventMouse(1, 2, tcell.Button1, tcell.ModNone))
	g.HandleMouse(tcell.NewEventMouse(1, 0, tcell.Button1, tcell.ModNone))
	if fired {
		t.Error("a drag from a data cell onto the header fired OnHeaderClick")
	}
}

// The indicator is drawn after the sorted column's label, widens the column to
// fit, is cleared by SetData (a new column set) and kept by
// SetDataPreservingView.
func TestDataGridSortIndicator(t *testing.T) {
	s := nullTestScreen(t)
	g := newTestDataGrid()
	g.SetData([]string{"Name", "Row Count"}, [][]string{{"a", "1"}})
	before := g.ColumnWidth(1)

	g.SetSortIndicator(1, true)
	g.Draw(s)
	if h := headerText(s, 40); !strings.Contains(h, "Row Count"+sortDescGlyph) {
		t.Errorf("header = %q, want the descending arrow after Row Count", h)
	}
	if got, want := g.ColumnWidth(1), before+core.DisplayWidth(sortDescGlyph); got != want {
		t.Errorf("sorted column width = %d, want %d (label plus arrow)", got, want)
	}

	g.SetDataPreservingView([]string{"Name", "Row Count"}, [][]string{{"b", "2"}})
	if col, desc := g.SortIndicator(); col != 1 || !desc {
		t.Errorf("SetDataPreservingView left indicator (%d, %v), want (1, true)", col, desc)
	}
	g.SetData([]string{"Property", "Value"}, nil)
	if col, _ := g.SortIndicator(); col != -1 {
		t.Errorf("SetData left the indicator on column %d of a new column set", col)
	}
	g.Draw(s)
	if h := headerText(s, 40); strings.ContainsAny(h, "▲▼") {
		t.Errorf("header = %q after SetData, want no arrow", h)
	}
}
