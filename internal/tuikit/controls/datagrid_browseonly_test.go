package controls

import (
	"slices"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// TestABrowseOnlyGridSelectsButNeverActivates. A read-only Properties page
// lets its grids be moved through so the detail rows follow the selection; the
// page stays safe only if no path into OnActivateCell survives — Space, Enter,
// a click, a drag across cells, and Ctrl+Space (which on an editable grid falls
// through to Space).
func TestABrowseOnlyGridSelectsButNeverActivates(t *testing.T) {
	g := newCellCursorGrid()
	activations := 0
	g.OnActivateCell = func(int, int) { activations++ }
	var selected []int
	g.OnSelectRow = func(row int) { selected = append(selected, row) }
	g.SetBrowseOnly(true)

	g.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	g.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone))
	g.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	click(t, g, 1, g.rect.Y+2)                                                      // row 0, col 0
	g.HandleMouse(tcell.NewEventMouse(1, g.rect.Y+3, tcell.Button1, tcell.ModNone)) // press row 1
	g.HandleMouse(tcell.NewEventMouse(g.colWidths[0]+1, g.rect.Y+3, tcell.Button1, tcell.ModNone))
	g.HandleMouse(tcell.NewEventMouse(1, g.rect.Y+3, tcell.ButtonNone, tcell.ModNone))

	if activations != 0 {
		t.Errorf("OnActivateCell fired %d times on a browse-only grid", activations)
	}
	if !slices.Contains(selected, 1) || !slices.Contains(selected, 0) {
		t.Errorf("OnSelectRow calls = %v, want the moves to rows 1 and 0 reported", selected)
	}

	// Ctrl+Space opens the read-only menu instead of reaching the Space path.
	g.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModCtrl))
	if activations != 0 {
		t.Error("Ctrl+Space activated a cell on a browse-only grid")
	}
	if !g.ctxMenu.Visible() {
		t.Error("Ctrl+Space did not open the Copy / Show Value menu on a browse-only grid")
	}

	// And the gate is a state the grid leaves.
	g.ctxMenu.Hide()
	g.SetBrowseOnly(false)
	g.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone))
	if activations != 1 {
		t.Errorf("OnActivateCell fired %d times after clearing browse-only, want 1", activations)
	}
}

// TestABrowseOnlyGridOffersShowValueButNotTheHostsItems. The grid cannot tell
// which of the host's menu entries edit, so it offers none of them; its own
// Show Value is read-only by construction.
func TestABrowseOnlyGridOffersShowValueButNotTheHostsItems(t *testing.T) {
	g := newCellCursorGrid()
	g.OnActivateCell = func(int, int) {}
	g.OnMenuItems = func() []MenuItem { return []MenuItem{{Label: "Remove"}} }
	g.SetBrowseOnly(true)

	g.HandleMouse(tcell.NewEventMouse(1, g.rect.Y+2, tcell.Button2, tcell.ModNone))
	if !g.ctxMenu.Visible() {
		t.Fatal("right-click opened no menu on a browse-only grid")
	}
	var labels []string
	for _, it := range g.ctxMenu.items {
		labels = append(labels, it.Label)
	}
	if !slices.Contains(labels, showValueMenuItem) {
		t.Errorf("menu = %v, want Show Value", labels)
	}
	if slices.Contains(labels, "Remove") {
		t.Errorf("menu = %v, offers the host's item on a browse-only grid", labels)
	}
}
