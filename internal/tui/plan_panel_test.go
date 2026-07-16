package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/showplan"
)

func loadTestPlan(t *testing.T) *showplan.Plan {
	t.Helper()
	plan, err := showplan.Parse([]byte(testPlanXML))
	if err != nil {
		t.Fatalf("showplan.Parse(testPlanXML) failed: %v", err)
	}
	return plan
}

// TestNewPlanPanelTitle checks the title passed to NewPlanPanel comes back
// unchanged from Title() — PanelManager's tab bar and combo dropdown read
// it directly.
func TestNewPlanPanelTitle(t *testing.T) {
	pp := NewPlanPanel("Execution Plan — Query 1", loadTestPlan(t))
	if got := pp.Title(); got != "Execution Plan — Query 1" {
		t.Errorf("Title() = %q, want %q", got, "Execution Plan — Query 1")
	}
}

// TestPlanPanelDelegatesToWrappedPlanView checks SetActive, SetBounds, and
// the clipboardTarget methods all forward to the wrapped PlanView without
// panicking — mirrors DetailBrowser's own delegation, which this type is
// modeled on.
func TestPlanPanelDelegatesToWrappedPlanView(t *testing.T) {
	pp := NewPlanPanel("Execution Plan — Query 1", loadTestPlan(t))
	pp.SetBounds(0, 0, 80, 24)
	pp.SetActive(true)

	_ = pp.HasSelection()
	_ = pp.SelectedText()
	_ = pp.Cut()
	pp.Paste("ignored")
	pp.SelectAll()
}

// TestOpenPlanPanelAddsAndActivatesPanel checks App.openPlanPanel — the
// Execution Plan tab's "[ Expand ]" button's action — adds a new panel to
// a.panels, makes it the active one, and preserves the title it was given.
func TestOpenPlanPanelAddsAndActivatesPanel(t *testing.T) {
	a := newTestApp()
	before := a.panels.Count()

	a.openPlanPanel("Execution Plan — Query 1", loadTestPlan(t))

	if got := a.panels.Count(); got != before+1 {
		t.Fatalf("panels.Count() = %d, want %d", got, before+1)
	}
	active := a.panels.ActivePanel()
	if active == nil {
		t.Fatal("ActivePanel() = nil, want the newly opened plan panel")
	}
	if got := active.Title(); got != "Execution Plan — Query 1" {
		t.Errorf("ActivePanel().Title() = %q, want %q", got, "Execution Plan — Query 1")
	}
	if _, ok := active.(*PlanPanel); !ok {
		t.Errorf("ActivePanel() = %T, want *PlanPanel", active)
	}
}

// TestOpenPlanPanelAlwaysCreatesNew checks two calls from what would be the
// same originating query panel add two independent panels, not one reused
// panel — the confirmed "always create new" behavior (see the plan file),
// matching how newQueryPanel always adds a new panel too.
func TestOpenPlanPanelAlwaysCreatesNew(t *testing.T) {
	a := newTestApp()
	plan := loadTestPlan(t)

	a.openPlanPanel("Execution Plan — Query 1", plan)
	first := a.panels.ActivePanel()
	a.openPlanPanel("Execution Plan — Query 1", plan)
	second := a.panels.ActivePanel()

	if first == second {
		t.Error("second openPlanPanel call reused the first panel, want a distinct new one")
	}
	if got := a.panels.Count(); got != 2 {
		t.Errorf("panels.Count() = %d, want 2", got)
	}
}
