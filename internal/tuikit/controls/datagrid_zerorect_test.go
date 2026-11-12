package controls

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/tuikittest"
)

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
			s := tuikittest.NewScreen(40, 10)
			g.Draw(s)
			out := s.Snapshot()
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
