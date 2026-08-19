package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFixturePlan(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "showplan", "testdata", "actual_plan.sqlplan"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

// A .sqlplan opens as a plan panel, not as a query panel full of XML. The
// fixture is UTF-16 with a BOM, the shape SSMS writes — openPlanFile has to
// decode it before the parser sees it.
func TestOpenPlanFileShowsAPlanPanel(t *testing.T) {
	a := newTestApp()
	a.openPlanFile("/tmp/example.sqlplan", readFixturePlan(t))

	pp, ok := a.panels.ActivePanel().(*PlanPanel)
	if !ok {
		t.Fatalf("active panel = %T, want *PlanPanel", a.panels.ActivePanel())
	}
	if pp.Title() != "example.sqlplan" {
		t.Errorf("Title() = %q", pp.Title())
	}
	if pp.filePath != "/tmp/example.sqlplan" {
		t.Errorf("filePath = %q", pp.filePath)
	}
	if !strings.Contains(pp.PlanXML(), "ShowPlanXML") {
		t.Error("panel holds no plan XML")
	}
}

// A file that isn't a plan reports itself and opens nothing — better than a
// panel showing a parse error where a plan should be.
func TestOpenPlanFileOnGarbageOpensNothing(t *testing.T) {
	a := newTestApp()
	a.openPlanFile("/tmp/bad.sqlplan", []byte("not a plan"))
	if a.panels.ActivePanel() != nil {
		t.Errorf("active panel = %T, want none", a.panels.ActivePanel())
	}
	if !strings.Contains(a.statusText, "Could not parse bad.sqlplan") {
		t.Errorf("status = %q", a.statusText)
	}
}

// Save on a plan panel opened from a file writes straight back to it, with
// no prompt — the same rule File > Save follows for a query panel.
func TestSavePlanPanelWritesBackToItsFile(t *testing.T) {
	a := newTestApp()
	path := filepath.Join(t.TempDir(), "plan.sqlplan")
	a.openPlanFile(path, readFixturePlan(t))
	pp := a.panels.ActivePanel().(*PlanPanel)

	a.saveQuery(false)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("plan was not written: %v", err)
	}
	if string(data) != pp.PlanXML() {
		t.Error("written file does not match the panel's plan XML")
	}
}

// File > Save is offered for a query panel and a plan panel, and for
// nothing else.
func TestCanSaveActivePanel(t *testing.T) {
	a := newTestApp()
	if a.canSaveActivePanel() {
		t.Error("canSaveActivePanel() = true with no panels")
	}
	a.panels.SetActive(a.panels.AddPanel(NewQueryPanel(a, "Query 1")))
	if !a.canSaveActivePanel() {
		t.Error("canSaveActivePanel() = false for a query panel")
	}
	a.openPlanFile("/tmp/example.sqlplan", readFixturePlan(t))
	if !a.canSaveActivePanel() {
		t.Error("canSaveActivePanel() = false for a plan panel")
	}
	if a.activePlan() == nil {
		t.Error("activePlan() = nil for a plan panel")
	}
}
