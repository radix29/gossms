package controls

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/vt"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// nullRows is a source whose cells all read "NULL" but only some are one.
type nullRows struct {
	rows  [][]string
	nulls map[[2]int]bool
}

func (s nullRows) Len() int                 { return len(s.rows) }
func (s nullRows) Row(i int) []string       { return s.rows[i] }
func (s nullRows) IsNull(row, col int) bool { return s.nulls[[2]int{row, col}] }

func nullTestScreen(t *testing.T) tcell.Screen {
	t.Helper()
	s, err := tcell.NewTerminfoScreenFromTty(vt.NewMockTerm(vt.MockOptSize{X: 40, Y: 10}))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Fini)
	return s
}

// cellDimmed reports whether column col's text in data row row (no scroll, no
// gutter) is drawn in the dim colour.
func cellDimmed(s tcell.Screen, g *DataGrid, row, col int) bool {
	x := 1
	for i := range col {
		x += g.colWidths[i]
	}
	_, st, _ := s.Get(x, 2+row)
	return st.GetForeground() == theme.Active().TextDim
}

// TestGridDimsOnlyCellsTheSourceMarksNull. The grid used to dim any cell
// whose text was "NULL", so the string 'NULL' looked like a SQL NULL. Now
// only a NullSource's mark counts, in an ordinary row and under a cell
// selection alike, and a source without the capability has no NULLs.
func TestGridDimsOnlyCellsTheSourceMarksNull(t *testing.T) {
	src := nullRows{
		rows:  [][]string{{"NULL", "NULL"}, {"NULL", "NULL"}},
		nulls: map[[2]int]bool{{0, 0}: true, {1, 1}: true},
	}
	for _, cursor := range []bool{false, true} {
		s := nullTestScreen(t)
		g := newTestDataGrid()
		g.SetSource([]string{"a", "b"}, src)
		g.SetCellCursor(cursor)
		if cursor {
			g.SetSelectedCell(1, 0)
			g.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModShift))
		}
		g.Draw(s)
		for r := range 2 {
			for c := range 2 {
				if got, want := cellDimmed(s, g, r, c), src.nulls[[2]int{r, c}]; got != want {
					t.Errorf("cellCursor %v, cell (%d,%d): dimmed = %v, want %v", cursor, r, c, got, want)
				}
			}
		}
	}

	s := nullTestScreen(t)
	g := newTestDataGrid()
	g.SetData([]string{"a"}, [][]string{{"NULL"}})
	g.Draw(s)
	if cellDimmed(s, g, 0, 0) {
		t.Error(`a plain source's "NULL" text is dimmed as a SQL NULL`)
	}
}

// TestShowValueOfANullSkipsTheHost. A NULL has no value for OnShowValue to
// open (an xml column's NULL went to a new XML tab before the host matched
// the text); the built-in popup shows it. A 'NULL' string still goes to the
// host.
func TestShowValueOfANullSkipsTheHost(t *testing.T) {
	g := newTestDataGrid()
	g.SetSource([]string{"x"}, nullRows{
		rows:  [][]string{{"NULL"}, {"NULL"}},
		nulls: map[[2]int]bool{{0, 0}: true},
	})
	g.SetCellCursor(true)
	called := false
	g.OnShowValue = func(int, string, string) bool { called = true; return true }

	g.openViewer()
	if called {
		t.Error("OnShowValue was offered a NULL")
	}
	if !g.viewOpen || g.viewEditor.Text() != "NULL" {
		t.Errorf("popup open %v, text %q; want the NULL in the built-in popup", g.viewOpen, g.viewEditor.Text())
	}

	g.closeViewer()
	g.SetSelectedCell(1, 0)
	g.openViewer()
	if !called {
		t.Error("OnShowValue was not offered the string 'NULL'")
	}
}
