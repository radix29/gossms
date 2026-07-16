package planview

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
)

func namedKey(k tcell.Key) *tcell.EventKey { return tcell.NewEventKey(k, "", tcell.ModNone) }

func newTreeTabView(t *testing.T) *PlanView {
	t.Helper()
	v := New()
	v.SetBounds(0, 0, 120, 40)
	v.SetPlan(loadTestPlan(t))
	v.setActiveTab(TabTree)
	return v
}

func TestTree_DefaultSelectionIsRoot(t *testing.T) {
	v := newTreeTabView(t)
	root := v.currentStatement().Root
	if v.selectedID != root.ID {
		t.Errorf("selectedID = %d, want root ID %d", v.selectedID, root.ID)
	}
	if len(v.treeSt.rows) != 12 {
		t.Fatalf("len(treeSt.rows) = %d, want 12 (fully expanded)", len(v.treeSt.rows))
	}
}

func TestTree_ArrowNavigationMovesSelection(t *testing.T) {
	v := newTreeTabView(t)
	start := v.selectedID
	if !v.HandleKey(namedKey(tcell.KeyDown)) {
		t.Fatal("HandleKey(Down) returned false")
	}
	if v.selectedID == start {
		t.Error("selectedID did not change after Down")
	}
	if !v.HandleKey(namedKey(tcell.KeyUp)) {
		t.Fatal("HandleKey(Up) returned false")
	}
	if v.selectedID != start {
		t.Errorf("selectedID = %d after Up, want back to %d", v.selectedID, start)
	}
}

func TestTree_CollapseHidesChildren(t *testing.T) {
	v := newTreeTabView(t)
	full := len(v.treeSt.rows)
	v.HandleKey(namedKey(tcell.KeyDown)) // select node 1 (Nested Loops, has children)
	v.HandleKey(namedKey(tcell.KeyLeft))
	if len(v.treeSt.rows) >= full {
		t.Errorf("row count = %d after collapsing a node with children, want fewer than %d", len(v.treeSt.rows), full)
	}
	v.HandleKey(namedKey(tcell.KeyRight))
	if len(v.treeSt.rows) != full {
		t.Errorf("row count = %d after re-expanding, want back to %d", len(v.treeSt.rows), full)
	}
}

func TestTree_BottomModeCycles(t *testing.T) {
	v := newTreeTabView(t)
	if v.bottomMode != bottomHidden {
		t.Fatalf("bottomMode = %v, want bottomHidden initially", v.bottomMode)
	}
	v.HandleKey(keyRune('o'))
	if v.bottomMode != bottomProperties {
		t.Errorf("bottomMode = %v, want bottomProperties after one 'o'", v.bottomMode)
	}
	v.HandleKey(keyRune('o'))
	if v.bottomMode != bottomSummary {
		t.Errorf("bottomMode = %v, want bottomSummary after two 'o'", v.bottomMode)
	}
	v.HandleKey(keyRune('o'))
	if v.bottomMode != bottomHidden {
		t.Errorf("bottomMode = %v, want bottomHidden after three 'o'", v.bottomMode)
	}
}

func TestSummary_SortAndJumpToTree(t *testing.T) {
	v := newTreeTabView(t)
	v.HandleKey(keyRune('o'))
	v.HandleKey(keyRune('o')) // bottomSummary

	v.HandleKey(keyRune('r')) // sort by rows
	if v.summarySt.sort != sortByRows {
		t.Fatalf("sort = %v, want sortByRows", v.summarySt.sort)
	}
	if len(v.summarySt.rows) != 12 {
		t.Fatalf("len(summarySt.rows) = %d, want 12", len(v.summarySt.rows))
	}
	for i := 1; i < len(v.summarySt.rows); i++ {
		if nodeRows(v.summarySt.rows[i-1]) < nodeRows(v.summarySt.rows[i]) {
			t.Fatalf("summary rows not sorted descending by rows at index %d", i)
		}
	}

	v.summarySt.grid.SetSelectedRow(0)
	if !v.HandleKey(namedKey(tcell.KeyTab)) {
		t.Fatal("HandleKey(Tab) returned false while the summary table is shown")
	}
	if !v.bottomFocused {
		t.Fatal("bottomFocused = false after Tab, want true")
	}
	v.HandleKey(namedKey(tcell.KeyEnter))
	if v.selectedID != v.summarySt.rows[0].ID {
		t.Errorf("selectedID = %d after Enter, want %d (top summary row)", v.selectedID, v.summarySt.rows[0].ID)
	}
	if v.bottomFocused {
		t.Error("bottomFocused should return to false after Enter jumps to the tree")
	}
}

func TestTree_HasSelectionAndSelectedText(t *testing.T) {
	v := newTreeTabView(t)
	if !v.HasSelection() {
		t.Fatal("HasSelection() = false on the Tree tab with a selected root")
	}
	text := v.SelectedText()
	if !strings.Contains(text, "Physical Operator") || !strings.Contains(text, "Top") {
		t.Errorf("SelectedText() = %q, want it to mention the selected operator's type", text)
	}
}

// TestSplitter_ClickAfterDragSelectsTree is a regression test: dragging the
// tree|details splitter and releasing it used to leave layout.Splitter's
// internal "dragging" flag stuck true, because handleTreeTabMouse's switch
// on ev.Buttons() had no case forwarding the release (ButtonNone) event to
// treeSplit.HandleMouse. The very next plain click anywhere in the tab was
// then misread as a drag continuation — moving the divider again instead of
// selecting the clicked tree row.
func TestSplitter_ClickAfterDragSelectsTree(t *testing.T) {
	v := newTreeTabView(t)
	r0 := v.treeSplit.Ratio()

	pos, barY := v.treeSplit.SplitPos(), v.treePaneRect.Y
	v.HandleMouse(tcell.NewEventMouse(pos, barY, tcell.Button1, tcell.ModNone))       // press on the bar
	v.HandleMouse(tcell.NewEventMouse(pos+10, barY, tcell.Button1, tcell.ModNone))    // drag
	v.HandleMouse(tcell.NewEventMouse(pos+10, barY, tcell.ButtonNone, tcell.ModNone)) // release
	r1 := v.treeSplit.Ratio()
	if r1 == r0 {
		t.Fatal("ratio unchanged after a drag — test setup didn't actually drag the splitter")
	}

	// A plain click well inside the tree pane, away from the bar, at the row
	// that should select treeSt.rows[3].
	row := 3
	clickY := v.treePaneRect.Y + row
	v.HandleMouse(tcell.NewEventMouse(2, clickY, tcell.Button1, tcell.ModNone))

	if got := v.treeSplit.Ratio(); got != r1 {
		t.Errorf("ratio changed to %v after a plain tree click, want unchanged %v — splitter is still eating clicks", got, r1)
	}
	want := v.treeSt.rows[row].node.ID
	if v.selectedID != want {
		t.Errorf("selectedID = %d after clicking tree row %d, want %d — click was swallowed by the splitter", v.selectedID, row, want)
	}
}
