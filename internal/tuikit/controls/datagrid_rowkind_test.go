package controls

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/theme"
)

// kindSource is a RowSource whose rows carry a kind: the Extended Events
// viewer's grouped view, reduced to its shape.
type kindSource struct {
	rows  [][]string
	kinds []RowKind
}

func (s kindSource) Len() int              { return len(s.rows) }
func (s kindSource) Row(i int) []string    { return s.rows[i] }
func (s kindSource) RowKind(i int) RowKind { return s.kinds[i] }

func groupedSource() kindSource {
	return kindSource{
		rows: [][]string{
			{"▾ name: sql_batch_completed (2 events) — SUM(duration) = 1500"},
			{"a", "1"},
			{"b", "2"},
		},
		kinds: []RowKind{RowGroup, RowNormal, RowMarked},
	}
}

// A group label is not a cell: sampled for widths it would widen the first
// column to the cap.
func TestDataGridGroupRowIsNotSampledForWidths(t *testing.T) {
	g := newTestDataGrid()
	g.SetSource([]string{"name", "n"}, groupedSource())
	if w := g.ColumnWidth(0); w != len("name")+2 {
		t.Errorf("column 0 is %d wide, want %d (header only; the group label must not count)", w, len("name")+2)
	}
}

// The label is drawn across the row from the left edge even with the grid
// scrolled right, where a cell would be scrolled out of sight.
func TestDataGridGroupRowDrawsAcrossTheRowWhateverTheScroll(t *testing.T) {
	g := NewDataGrid()
	g.SetBounds(0, 0, 60, 8)
	g.SetSource([]string{"name", "n", "c", "d", "e"}, groupedSource())
	g.scrollCol = 2
	s := newRuneScreen(60, 8)
	g.Draw(s)
	line := strings.Split(s.text(), "\n")[2]
	if !strings.Contains(line, "name: sql_batch_completed (2 events)") {
		t.Errorf("group row drew %q, want the whole label", line)
	}
	if strings.Contains(line, "|") {
		t.Errorf("group row drew column separators: %q", line)
	}
}

// A marked row is drawn in the warning colour; a group row in the header
// style, and in the selection style whole when selected — even in cell-cursor
// mode, where an ordinary row highlights only the cursor's cell.
func TestDataGridRowKindStyles(t *testing.T) {
	g := NewDataGrid()
	g.SetBounds(0, 0, 40, 8)
	g.SetCellCursor(true)
	g.Focus(true)
	g.SetSource([]string{"name", "n"}, groupedSource())
	s := newStyleScreen(40, 8)
	g.Draw(s)
	if st := s.styles[[2]int{20, 2}]; st != theme.StyleGridSelected() {
		t.Errorf("selected group row, far column: style %v, want the selection style", st)
	}
	g.SetSelectedRow(1)
	s = newStyleScreen(40, 8)
	g.Draw(s)
	if st := s.styles[[2]int{20, 2}]; st != theme.StyleGridHeader() {
		t.Errorf("unselected group row: style %v, want the header style", st)
	}
	fg := s.styles[[2]int{2, 4}].GetForeground()
	if fg != theme.Active().Warning {
		t.Errorf("marked row foreground %v, want the warning colour %v", fg, theme.Active().Warning)
	}
	fg = s.styles[[2]int{2, 3}].GetForeground()
	if fg == theme.Active().Warning {
		t.Error("an ordinary row is drawn as marked")
	}
}

// aggSource is a grouped source whose group row carries a cell beside its
// label: an aggregate under its column, the viewer's SUM(duration).
func aggSource() kindSource {
	return kindSource{
		rows: [][]string{
			{"▾ name: sql_batch_completed (2 events)", "", "", "SUM 1500"},
			{"a", "1", "2", "3"},
		},
		kinds: []RowKind{RowGroup, RowNormal},
	}
}

// groupLine draws aggSource at scroll col and returns the group row's text.
func groupLine(t *testing.T, scroll int) string {
	t.Helper()
	g := NewDataGrid()
	g.SetBounds(0, 0, 60, 8)
	g.SetSource([]string{"name", "n", "c", "d"}, aggSource())
	g.scrollCol = scroll
	s := newRuneScreen(60, 8)
	g.Draw(s)
	return strings.Split(s.text(), "\n")[2]
}

// The label spills across the empty cells and stops at the first non-empty
// one, clipped with "…"; that cell draws in its column, after a separator.
// Columns are 6, 6, 6 and 10 wide, so column d starts at x 18.
func TestDataGridGroupLabelSpillsToTheFirstNonEmptyCell(t *testing.T) {
	line := groupLine(t, 0)
	if want := " ▾ name: sql_bat…| SUM 1500|"; !strings.HasPrefix(line, want) {
		t.Errorf("group row %q, want it to start %q", line, want)
	}
	if strings.Count(line, "|") != 2 {
		t.Errorf("group row %q: want separators only around the aggregate cell", line)
	}
}

// Scrolled, the label stays at the left edge and the aggregate moves with its
// column; scrolled so its column comes first, the label has no room left.
func TestDataGridGroupCellsScrollAndTheLabelDoesNot(t *testing.T) {
	line := groupLine(t, 1)
	if !strings.HasPrefix(line, " ▾ name: s…| SUM 1500|") {
		t.Errorf("scrolled one column: %q", line)
	}
	line = groupLine(t, 3)
	if !strings.HasPrefix(line, " SUM 1500|") {
		t.Errorf("scrolled to the aggregate's column: %q", line)
	}
}

// A group row's cells beside the label are sampled for widths like any
// cell; the label still is not.
func TestDataGridGroupCellsAreSampledButNotTheLabel(t *testing.T) {
	g := newTestDataGrid()
	g.SetSource([]string{"name", "n", "c", "d"}, aggSource())
	if w := g.ColumnWidth(0); w != 6 {
		t.Errorf("column 0 is %d wide, want 6 (the label must not count)", w)
	}
	if w := g.ColumnWidth(3); w != len("SUM 1500")+2 {
		t.Errorf("column 3 is %d wide, want %d (the aggregate cell counts)", w, len("SUM 1500")+2)
	}
}

// A cell holding line breaks or a tab draws them as spaces, and is sized for
// them (T67): CR, LF and TAB measure nothing, so the words ran together.
func TestDataGridDrawsLineBreaksInACellAsSpaces(t *testing.T) {
	g := NewDataGrid()
	g.SetBounds(0, 0, 60, 6)
	g.SetData([]string{"v"}, [][]string{{"line1\r\nline2\tend"}})
	s := newRuneScreen(60, 6)
	g.Draw(s)
	if !strings.Contains(s.text(), "line1 line2 end") {
		t.Errorf("grid drew:\n%s\nwant the cell as %q", s.text(), "line1 line2 end")
	}
}
