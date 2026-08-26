package controls

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// runeScreen captures the runes a Draw painted, so a test can assert a row's
// text actually reached the screen rather than only that Draw returned.
type runeScreen struct {
	tcell.Screen
	w, h  int
	cells map[[2]int]rune
}

func newRuneScreen(w, h int) *runeScreen {
	return &runeScreen{w: w, h: h, cells: map[[2]int]rune{}}
}
func (s *runeScreen) Size() (int, int) { return s.w, s.h }
func (s *runeScreen) SetContent(x, y int, primary rune, comb []rune, style tcell.Style) {
	s.cells[[2]int{x, y}] = primary
}
func (s *runeScreen) ShowCursor(x, y int) {}

// text returns everything painted, one string per row.
func (s *runeScreen) text() string {
	var b strings.Builder
	for y := range s.h {
		for x := range s.w {
			if r, ok := s.cells[[2]int{x, y}]; ok && r != 0 {
				b.WriteRune(r)
			} else {
				b.WriteByte(' ')
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// unlaidGrid is a small grid that has never been given bounds — the state a
// page's grid is in while it is still being built. rect.H is 0, so every
// ensureVisible call gets a negative data height.
func unlaidGrid() *DataGrid {
	g := NewDataGrid()
	g.SetData([]string{"Permission", "State"}, [][]string{
		{"Alter", "[ ]"},
		{"Connect", "[x]"},
		{"Control", "[ ]"},
	})
	g.SetCellCursor(true)
	return g
}

// Every selection setter runs ensureVisible, and on a grid with no viewport
// yet the scroll must not move: scrollRow = selRow - dataH + 1 with a negative
// dataH lands past the last row, and the next Draw paints the header and the
// "N rows" status over blank lines.
func TestSelectionOnAnUnlaidOutGridDoesNotScrollPastTheRows(t *testing.T) {
	cases := map[string]func(*DataGrid){
		"SetSelectedRow":  func(g *DataGrid) { g.SetSelectedRow(2) },
		"SetSelectedCell": func(g *DataGrid) { g.SetSelectedCell(2, 1) },
		"SetDataPreservingView": func(g *DataGrid) {
			g.SetSelectedCell(2, 1)
			g.SetDataPreservingView([]string{"Permission", "State"}, [][]string{
				{"Alter", "[ ]"},
				{"Connect", "[x]"},
				{"Control", "[x]"},
			})
		},
	}
	for name, act := range cases {
		t.Run(name, func(t *testing.T) {
			g := unlaidGrid()
			act(g)

			if got := g.ScrollRow(); got != 0 {
				t.Errorf("ScrollRow() = %d, want 0 — nothing to scroll before layout", got)
			}
			if got := g.ScrollCol(); got != 0 {
				t.Errorf("ScrollCol() = %d, want 0 — nothing to scroll before layout", got)
			}

			// The bug's visible form: give the grid real bounds, the size the
			// first layout pass would, and draw it.
			g.SetBounds(0, 0, 40, 10)
			s := newRuneScreen(40, 10)
			g.Draw(s)
			out := s.text()
			for _, want := range []string{"Alter", "Connect", "Control"} {
				if !strings.Contains(out, want) {
					t.Errorf("Draw painted no %q row; grid rendered blank:\n%s", want, out)
				}
			}
		})
	}
}

// The guard must not cost a laid-out grid its scrolling: the same selection on
// a real viewport still brings the row into view.
func TestSelectionOnALaidOutGridStillScrolls(t *testing.T) {
	g := preserveViewGrid() // 30 rows, 7 visible
	g.SetSelectedCell(20, 1)
	if got := g.ScrollRow(); got != 14 {
		t.Errorf("ScrollRow() = %d, want 14 (row 20 scrolled to the bottom edge)", got)
	}
}
