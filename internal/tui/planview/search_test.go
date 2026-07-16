package planview

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// typeString feeds each rune of s through HandleKey, as if typed.
func typeString(v *PlanView, s string) {
	for _, r := range s {
		v.HandleKey(keyRune(r))
	}
}

func TestSearch_FindsAndJumpsToMatch(t *testing.T) {
	v := newTreeTabView(t)
	if !v.HandleKey(keyRune('/')) {
		t.Fatal("HandleKey('/') returned false")
	}
	if !v.searchSt.active {
		t.Fatal("searchSt.active = false after '/'")
	}
	typeString(v, "specialization")
	if !v.HandleKey(namedKey(tcell.KeyEnter)) {
		t.Fatal("HandleKey(Enter) returned false")
	}
	if v.searchSt.active {
		t.Error("searchSt.active = true after confirming, want false")
	}
	if len(v.searchSt.matches) == 0 {
		t.Fatal("no matches found for \"specialization\", want at least one (Object.Table Specializations)")
	}
	n := v.selectedNode()
	if n == nil || n.Object.Table != "Specializations" {
		t.Errorf("selected node = %+v, want the Specializations operator", n)
	}
}

func TestSearch_NoMatchesReportsStatus(t *testing.T) {
	v := newTreeTabView(t)
	var status string
	v.OnStatus = func(msg string) { status = msg }
	v.HandleKey(keyRune('/'))
	typeString(v, "nonexistentoperator")
	v.HandleKey(namedKey(tcell.KeyEnter))
	if len(v.searchSt.matches) != 0 {
		t.Fatalf("matches = %v, want none", v.searchSt.matches)
	}
	if status == "" {
		t.Error("OnStatus was never called to report no matches")
	}
}

func TestSearch_EscapeCancelsWithoutMovingSelection(t *testing.T) {
	v := newTreeTabView(t)
	start := v.selectedID
	v.HandleKey(keyRune('/'))
	typeString(v, "doctors")
	if !v.HandleKey(namedKey(tcell.KeyEscape)) {
		t.Fatal("HandleKey(Escape) returned false while searching")
	}
	if v.searchSt.active {
		t.Error("searchSt.active = true after Escape, want false")
	}
	if v.selectedID != start {
		t.Errorf("selectedID changed to %d after cancelling search, want unchanged %d", v.selectedID, start)
	}
}

func TestSearch_DigitDuringSearchDoesNotSwitchTabs(t *testing.T) {
	v := newTreeTabView(t)
	v.HandleKey(keyRune('/'))
	v.HandleKey(keyRune('1')) // would switch to TabPlan if not swallowed by search
	if v.activeTab != TabTree {
		t.Errorf("activeTab = %v after typing '1' during search, want it to stay TabTree", v.activeTab)
	}
	if v.searchSt.query != "1" {
		t.Errorf("searchSt.query = %q, want \"1\" to have been appended", v.searchSt.query)
	}
}

func TestSearch_NextPreviousCycle(t *testing.T) {
	v := newTreeTabView(t)
	v.HandleKey(keyRune('/'))
	typeString(v, "clustered index seek") // matches 4 leaf operators in the fixture
	v.HandleKey(namedKey(tcell.KeyEnter))
	if len(v.searchSt.matches) < 2 {
		t.Fatalf("matches = %v, want at least 2 to test cycling", v.searchSt.matches)
	}
	first := v.selectedID
	v.HandleKey(keyRune('n'))
	if v.selectedID == first {
		t.Error("selectedID unchanged after 'n', want it to advance to the next match")
	}
	v.HandleKey(keyRune('N'))
	if v.selectedID != first {
		t.Errorf("selectedID = %d after 'N', want back to %d", v.selectedID, first)
	}
}

func TestJumpToWarning_MovesToWarnedOperator(t *testing.T) {
	v := newTreeTabView(t)
	// Force a warning onto a specific node so the test doesn't depend on
	// the fixture happening to carry one.
	target := v.currentStatement().Nodes()[5]
	target.Warnings = []string{"Test Warning"}
	v.HandleKey(keyRune('w'))
	if v.selectedID != target.ID {
		t.Errorf("selectedID = %d after 'w', want %d (the only warned operator)", v.selectedID, target.ID)
	}
}

func TestEstimatedToggle_TileRowsText(t *testing.T) {
	v := newTreeTabView(t)
	n := v.currentStatement().Nodes()[0]
	if n.Runtime == nil {
		t.Fatal("fixture's first node has no Runtime — test assumption broken")
	}
	before := v.tileRowsText(n)
	v.HandleKey(keyRune('p'))
	if !v.showEstimated {
		t.Fatal("showEstimated = false after 'p', want true")
	}
	after := v.tileRowsText(n)
	if before == after {
		t.Errorf("tileRowsText unchanged (%q) after toggling showEstimated, want the estimate instead of the actual", before)
	}
}
