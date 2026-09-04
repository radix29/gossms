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

// TestQueryActionsReportWhenThereIsNoQueryPanel drives every Query-menu action
// and toolbar button that acts on the editor with no query panel active.
//
// The Enabled predicates gate these ahead of the click, so this is the second
// line: an action reached anyway must say why nothing happened rather than
// doing nothing (docs/ui-rules.md on context-gating). Each of the
// seven carried its own copy of the check, which is the arrangement where one
// gets added without it.
func TestQueryActionsReportWhenThereIsNoQueryPanel(t *testing.T) {
	actions := map[string]func(*App){
		"Execute":                (*App).executeActiveQuery,
		"Execute Selection":      (*App).executeSelectedQuery,
		"Display Estimated Plan": (*App).showEstimatedExecutionPlan,
		"Cancel Executing Query": (*App).cancelExecutingQuery,
		"Reconnect":              (*App).reconnectActiveQuery,
		"Refresh IntelliSense":   (*App).refreshCompletionCache,
		"Results To Grid":        func(a *App) { a.setResultsMode(ResultsModeGrid) },
	}
	for name, act := range actions {
		t.Run(name, func(t *testing.T) {
			a := newTestApp() // no panels at all, so no active query panel
			act(a)
			if a.statusText != noActiveQueryPanelMessage {
				t.Errorf("status after %s with no query panel = %q, want %q",
					name, a.statusText, noActiveQueryPanelMessage)
			}
		})
	}
}

// TestWithQueryPanelRunsOnTheActivePanel is the other half: a guard that
// refused everything would pass the table above.
func TestWithQueryPanelRunsOnTheActivePanel(t *testing.T) {
	a := newTestApp()
	other := NewQueryPanel(a, "Query 1")
	active := NewQueryPanel(a, "Query 2")
	a.panels.AddPanel(other)
	// Act on a panel that is not the first one added: a helper that reached
	// for a.panels[0] would pass with only one.
	a.panels.SetActive(a.panels.AddPanel(active))

	var got *QueryPanel
	a.withQueryPanel(func(qp *QueryPanel) { got = qp })

	if got != active {
		t.Errorf("withQueryPanel ran on %v, want the active panel", got)
	}
	if a.statusText != "" {
		t.Errorf("status = %q, want nothing said when there is a panel to act on", a.statusText)
	}
}
