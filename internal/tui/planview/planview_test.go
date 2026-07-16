package planview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/showplan"
)

func loadTestPlan(t *testing.T) *showplan.Plan {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "showplan", "testdata", "actual_plan.sqlplan"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	p, err := showplan.Parse(data)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	return p
}

func keyRune(r rune) *tcell.EventKey {
	return tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone)
}

func TestNew_EmptyState(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	if v.Plan() != nil {
		t.Error("Plan() should be nil before any load")
	}
	if v.HasSelection() {
		t.Error("HasSelection() should be false with no plan loaded")
	}
}

func TestSetPlanXML_InvalidReportsError(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	if err := v.SetPlanXML("not xml at all"); err == nil {
		t.Fatal("SetPlanXML(garbage) returned nil error")
	}
	if v.Plan() != nil {
		t.Error("Plan() should stay nil after a failed SetPlanXML")
	}
}

func TestSetPlan_InstallsAndDefaultsToPlanTab(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	v.SetPlan(loadTestPlan(t))
	if v.Plan() == nil {
		t.Fatal("Plan() is nil after SetPlan")
	}
	if v.activeTab != TabPlan {
		t.Errorf("activeTab = %v, want TabPlan", v.activeTab)
	}
	// The fixture is a single-statement plan, so no statement bar.
	if v.stmtRect.H != 0 {
		t.Errorf("stmtRect.H = %d, want 0 for a single-statement plan", v.stmtRect.H)
	}
}

func TestHandleKey_TabSwitching(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	v.SetPlan(loadTestPlan(t))

	if !v.HandleKey(keyRune('3')) {
		t.Fatal("HandleKey('3') returned false")
	}
	if v.activeTab != TabXML {
		t.Errorf("activeTab = %v, want TabXML after '3'", v.activeTab)
	}
	if !v.HandleKey(keyRune('2')) {
		t.Fatal("HandleKey('2') returned false")
	}
	if v.activeTab != TabTree {
		t.Errorf("activeTab = %v, want TabTree after '2'", v.activeTab)
	}
	if !v.HandleKey(keyRune('1')) {
		t.Fatal("HandleKey('1') returned false")
	}
	if v.activeTab != TabPlan {
		t.Errorf("activeTab = %v, want TabPlan after '1'", v.activeTab)
	}
}

func TestHandleKey_UnhandledReturnsFalse(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	v.SetPlan(loadTestPlan(t))
	// On the Plan tab (placeholder, no XML editor routing), an arbitrary
	// key must be refused so a host can route it elsewhere — see the
	// keyboard-conventions rule (tuikit widgets must return false for keys
	// they don't act on).
	if v.HandleKey(keyRune('z')) {
		t.Error("HandleKey('z') on the Plan tab returned true, want false")
	}
}

func TestXMLTab_SelectionAndCopy(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	v.SetPlan(loadTestPlan(t))
	v.setActiveTab(TabXML)
	v.SetActive(true)

	v.xml.SelectAll()
	if !v.HasSelection() {
		t.Fatal("HasSelection() = false after SelectAll on the XML tab")
	}
	text := v.SelectedText()
	if text == "" {
		t.Error("SelectedText() is empty after SelectAll")
	}
	if v.Cut() != text {
		t.Error("Cut() should return the same text as SelectedText() (read-only view)")
	}

	// The XML tab's own text selection must not leak into the Plan tab's
	// notion of "selection" — the Plan tab has a legitimate selection of
	// its own (the highlighted operator node), reported as that node's
	// details rather than the XML text.
	v.setActiveTab(TabPlan)
	if !v.HasSelection() {
		t.Error("HasSelection() = false on the Plan tab with a selected operator, want true")
	}
	if planText := v.SelectedText(); planText == text || planText == "" {
		t.Errorf("SelectedText() on the Plan tab = %q, want the selected operator's details, not the XML tab's leftover text", planText)
	}
}

func TestStatementSelector_MultiStatement(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	plan := loadTestPlan(t)
	// Synthesize a second statement to exercise the multi-statement path
	// without needing a second real fixture.
	plan.Statements = append(plan.Statements, plan.Statements[0])
	v.SetPlan(plan)

	if v.stmtRect.H != 1 {
		t.Fatalf("stmtRect.H = %d, want 1 for a multi-statement plan", v.stmtRect.H)
	}
	if v.stmtIdx != 0 {
		t.Fatalf("stmtIdx = %d, want 0 right after load", v.stmtIdx)
	}
	if !v.HandleKey(keyRune(']')) {
		t.Fatal("HandleKey(']') returned false")
	}
	if v.stmtIdx != 1 {
		t.Errorf("stmtIdx = %d, want 1 after ']'", v.stmtIdx)
	}
	if !v.HandleKey(keyRune(']')) {
		t.Fatal("HandleKey(']') returned false")
	}
	if v.stmtIdx != 0 {
		t.Errorf("stmtIdx = %d, want 0 after wrapping past the last statement", v.stmtIdx)
	}
}

func TestOpenInPanelButton_ClickNoopWithoutCallback(t *testing.T) {
	v := New()
	v.SetBounds(0, 0, 80, 24)
	v.SetPlan(loadTestPlan(t))
	// openBtnRect is zero until Draw runs (untested here — tcell v3 has no
	// SimulationScreen, so Draw itself isn't exercised by any test in this
	// codebase; see controls/editor_test.go for the same state-only
	// convention). Clicking where the button would be must still be a
	// harmless tab-bar click, not a panic, with OnOpenInPanel nil.
	if v.HandleMouse(newClick(1, 0)) == false {
		t.Fatal("HandleMouse on the tab bar row returned false, want true (tab click)")
	}
}

func newClick(x, y int) *tcell.EventMouse {
	return tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone)
}
