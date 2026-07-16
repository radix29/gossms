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

// closePanelAt must cancel an in-flight query — otherwise it keeps running
// server-side after the panel (and its results UI) is gone. The completion
// goroutine's own postEvent callback still checks panelHosted before doing
// any UI work, but the query itself should stop, not just its display.
func TestClosePanelAtCancelsInFlightQuery(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	i := a.panels.AddPanel(qp)

	cancelled := false
	qp.executing = true
	qp.cancel = func() { cancelled = true }

	a.closePanelAt(i)

	if !cancelled {
		t.Error("closePanelAt did not cancel the in-flight query")
	}
}

// Closing a panel with no query running must not panic on a nil qp.cancel.
func TestClosePanelAtNoQueryRunningDoesNotPanic(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	i := a.panels.AddPanel(qp)

	a.closePanelAt(i)
}
