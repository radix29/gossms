package propsheet

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// gridForm builds a form of [before, grid, after] with grid holding rows,
// focused on the grid, so a test can check both directions out of it.
func gridForm(rows [][]string) (*Form, *GridRow) {
	g := controls.NewDataGrid()
	if rows != nil {
		g.SetData([]string{"Name"}, rows)
	}
	gr := NewGridRow(g, 10)
	f := NewForm(Text("Before", "b", 10), gr, Text("After", "a", 10))
	f.SetBounds(0, 0, 40, 40)
	f.Focus(true)
	f.FocusNext() // Before -> grid
	if _, ok := f.Focused().(*GridRow); !ok {
		panic("setup: the grid isn't focused")
	}
	return f, gr
}

func sendKey(f *Form, k tcell.Key) { f.HandleKey(key(k, tcell.ModNone)) }

// A grid row must hand back the arrow keys it can't act on, or it traps
// keyboard focus: Form falls back to its own Up/Down navigation only when
// the focused row returns false. Down at the last data row, Up at the first,
// and every arrow in an empty grid are the cases that stuck.
func TestGridRowReleasesFocusAtItsBoundaries(t *testing.T) {
	three := [][]string{{"a"}, {"b"}, {"c"}}

	t.Run("down past the last row moves on", func(t *testing.T) {
		f, _ := gridForm(three)
		for range 5 { // walk to the last row, then past it
			sendKey(f, tcell.KeyDown)
		}
		if _, stuck := f.Focused().(*GridRow); stuck {
			t.Error("Down at the last data row never left the grid")
		}
	})

	t.Run("up past the first row moves back", func(t *testing.T) {
		f, _ := gridForm(three)
		for range 5 {
			sendKey(f, tcell.KeyUp)
		}
		if _, stuck := f.Focused().(*GridRow); stuck {
			t.Error("Up at the first data row never left the grid")
		}
	})

	t.Run("an empty grid holds nothing", func(t *testing.T) {
		f, _ := gridForm(nil)
		sendKey(f, tcell.KeyDown)
		if _, stuck := f.Focused().(*GridRow); stuck {
			t.Error("Down in an empty grid was swallowed")
		}
	})

	// The other half of the contract: a key the grid *can* use must not
	// escape, or the selection would jump a row and change focus at once.
	t.Run("down inside the grid stays in the grid", func(t *testing.T) {
		f, gr := gridForm(three)
		sendKey(f, tcell.KeyDown)
		if _, ok := f.Focused().(*GridRow); !ok {
			t.Fatal("Down moved focus off a grid that had rows left to walk")
		}
		if got := gr.Grid.SelectedRow(); got != 1 {
			t.Errorf("SelectedRow = %d, want 1 — the grid didn't act on the key it kept", got)
		}
	})
}

// Left is how PropertySheet gets back to its page list — a zone change Form
// itself knows nothing about, so this asserts the row's return value rather
// than where focus ended up. A grid with nowhere left to scroll must give
// the key back; one that scrolled must keep it.
func TestGridRowLeftReleasesOnlyWhenTheGridCannotScroll(t *testing.T) {
	_, gr := gridForm([][]string{{"a"}, {"b"}})
	if gr.HandleKey(key(tcell.KeyLeft, tcell.ModNone)) {
		t.Error("Left at column 0 was reported handled; PropertySheet can never leave the grid")
	}

	// Narrow enough that three columns don't fit, so Right has somewhere to go.
	_, wide := gridForm(nil)
	wide.Grid.SetData([]string{"column one", "column two", "column three"},
		[][]string{{"1", "2", "3"}})
	wide.Layout(0, 0, 12)
	if !wide.HandleKey(key(tcell.KeyRight, tcell.ModNone)) {
		t.Fatal("Right over a grid with columns off-screen was reported unhandled")
	}
	if !wide.HandleKey(key(tcell.KeyLeft, tcell.ModNone)) {
		t.Error("Left escaped a grid that still had somewhere to scroll back to")
	}
}

// Keys that aren't arrows are forwarded untouched — the movement check must
// not become a filter on everything the grid handles.
func TestGridRowForwardsNonArrowKeys(t *testing.T) {
	f, gr := gridForm([][]string{{"a"}, {"b"}, {"c"}})
	sendKey(f, tcell.KeyEnd)
	if _, ok := f.Focused().(*GridRow); !ok {
		t.Fatal("End moved focus off the grid")
	}
	if got := gr.Grid.SelectedRow(); got != 2 {
		t.Errorf("SelectedRow after End = %d, want the last row 2", got)
	}
}
