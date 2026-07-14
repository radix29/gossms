package tui

import "testing"

// TestPanelHosted confirms panelHosted distinguishes a panel that's still
// in a.panels from one that's been removed — the check connectForQueryPanel
// relies on to avoid leaking a connection that resolves after its panel was
// already closed (see connectForQueryPanel's postEvent callback).
func TestPanelHosted(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	i := a.panels.AddPanel(qp)

	if !a.panelHosted(qp) {
		t.Fatal("panelHosted(qp) = false while qp is still in a.panels, want true")
	}

	a.panels.RemovePanel(i)
	if a.panelHosted(qp) {
		t.Error("panelHosted(qp) = true after qp was removed from a.panels, want false")
	}
}
