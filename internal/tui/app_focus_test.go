package tui

import "testing"

// TestCycleFocusThreeWay confirms Ctrl+Tab (see App.cycleFocus) steps
// through Object Explorer -> the active query panel's editor -> its
// results pane -> Object Explorer again, once the panel actually has a
// results pane to stop at.
func TestCycleFocusThreeWay(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)
	qp.setResult(newTestResult(1, false), false)

	a.focusExplorer()

	a.cycleFocus() // explorer -> editor
	if a.focus != "panels" || qp.resultsFocused {
		t.Fatalf("after 1st cycleFocus: focus=%q resultsFocused=%v, want panels/false (editor)", a.focus, qp.resultsFocused)
	}

	a.cycleFocus() // editor -> results
	if a.focus != "panels" || !qp.resultsFocused {
		t.Fatalf("after 2nd cycleFocus: focus=%q resultsFocused=%v, want panels/true (results)", a.focus, qp.resultsFocused)
	}

	a.cycleFocus() // results -> explorer
	if a.focus != "explorer" {
		t.Fatalf("after 3rd cycleFocus: focus=%q, want explorer", a.focus)
	}
}

// TestCycleFocusTwoWayWithoutResults confirms a query panel with no
// results yet has nothing to cycle into and degrades to the plain
// explorer/editor toggle.
func TestCycleFocusTwoWayWithoutResults(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)

	a.focusExplorer()

	a.cycleFocus() // explorer -> editor
	if a.focus != "panels" {
		t.Fatalf("focus = %q, want panels", a.focus)
	}
	a.cycleFocus() // editor -> explorer directly: no results pane to stop at
	if a.focus != "explorer" {
		t.Fatalf("focus = %q, want explorer (no results pane to cycle through)", a.focus)
	}
}

// TestCycleFocusTwoWayForNonQueryPanel confirms a non-query panel (e.g.
// Object Explorer Details) has no editor/results split at all and also
// degrades to the plain explorer/panel toggle.
func TestCycleFocusTwoWayForNonQueryPanel(t *testing.T) {
	a := newTestApp()
	a.panels.AddPanel(NewDetailBrowser("Object Explorer Details"))

	a.focusExplorer()

	a.cycleFocus()
	if a.focus != "panels" {
		t.Fatalf("focus = %q, want panels", a.focus)
	}
	a.cycleFocus()
	if a.focus != "explorer" {
		t.Fatalf("focus = %q, want explorer", a.focus)
	}
}

// TestCycleFocusReverseThreeWay is cycleFocus's three-way test run
// backwards (Ctrl+Shift+Tab): Explorer -> Results -> Editor -> Explorer.
func TestCycleFocusReverseThreeWay(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)
	qp.setResult(newTestResult(1, false), false)

	a.focusExplorer()

	a.cycleFocusReverse() // explorer -> results
	if a.focus != "panels" || !qp.resultsFocused {
		t.Fatalf("after 1st cycleFocusReverse: focus=%q resultsFocused=%v, want panels/true (results)", a.focus, qp.resultsFocused)
	}

	a.cycleFocusReverse() // results -> editor
	if a.focus != "panels" || qp.resultsFocused {
		t.Fatalf("after 2nd cycleFocusReverse: focus=%q resultsFocused=%v, want panels/false (editor)", a.focus, qp.resultsFocused)
	}

	a.cycleFocusReverse() // editor -> explorer
	if a.focus != "explorer" {
		t.Fatalf("after 3rd cycleFocusReverse: focus=%q, want explorer", a.focus)
	}
}

// TestCycleFocusReverseTwoWayWithoutResults mirrors
// TestCycleFocusTwoWayWithoutResults for the reverse direction.
func TestCycleFocusReverseTwoWayWithoutResults(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)

	a.focusExplorer()

	a.cycleFocusReverse() // explorer -> editor directly: no results to stop at
	if a.focus != "panels" || qp.resultsFocused {
		t.Fatalf("focus=%q resultsFocused=%v, want panels/false (editor)", a.focus, qp.resultsFocused)
	}
	a.cycleFocusReverse() // editor -> explorer
	if a.focus != "explorer" {
		t.Fatalf("focus = %q, want explorer", a.focus)
	}
}

// TestCycleFocusThenReverseReturnsToStart runs cycleFocus forward and then
// cycleFocusReverse the same number of times, confirming the two are true
// inverses of each other rather than merely two independent two/three-way
// toggles that happen to both end up at explorer eventually.
func TestCycleFocusThenReverseReturnsToStart(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)
	qp.setResult(newTestResult(1, false), false)
	a.focusExplorer()

	a.cycleFocus() // explorer -> editor
	if a.focus != "panels" || qp.resultsFocused {
		t.Fatalf("after cycleFocus: focus=%q resultsFocused=%v, want panels/false", a.focus, qp.resultsFocused)
	}
	a.cycleFocusReverse() // editor -> explorer
	if a.focus != "explorer" {
		t.Fatalf("after cycleFocusReverse: focus=%q, want explorer", a.focus)
	}
}

// TestNextPrevPanelSyncActiveFocusFromExplorer confirms Ctrl+Shift+Right/
// Left (App.nextPanel/prevPanel) re-sync the newly active panel's
// Activatable state to a.focus — switching panels while Object Explorer
// has real keyboard focus must not make the newly-selected panel appear
// focused (see App.syncActivePanelFocus).
func TestNextPrevPanelSyncActiveFocusFromExplorer(t *testing.T) {
	a := newTestApp()
	qp1 := NewQueryPanel(a, "Query 1")
	qp2 := NewQueryPanel(a, "Query 2")
	a.panels.AddPanel(qp1)
	a.panels.AddPanel(qp2)

	a.focusExplorer()
	if qp1.active {
		t.Fatalf("qp1.active = true right after focusExplorer, want false")
	}

	a.nextPanel()
	if a.panels.ActiveIndex() != 1 {
		t.Fatalf("ActiveIndex = %d, want 1 after nextPanel", a.panels.ActiveIndex())
	}
	if qp2.active {
		t.Fatalf("qp2.active = true after nextPanel while explorer has focus, want false")
	}
	if a.focus != "explorer" {
		t.Fatalf("focus = %q after nextPanel, want explorer (unchanged)", a.focus)
	}

	a.prevPanel()
	if a.panels.ActiveIndex() != 0 {
		t.Fatalf("ActiveIndex = %d, want 0 after prevPanel", a.panels.ActiveIndex())
	}
	if qp1.active {
		t.Fatalf("qp1.active = true after prevPanel while explorer has focus, want false")
	}
}

// TestNextPanelKeepsActiveFocusFromPanels is the mirror case: switching
// panels while a panel already has real focus should leave the newly
// active panel focused too.
func TestNextPanelKeepsActiveFocusFromPanels(t *testing.T) {
	a := newTestApp()
	qp1 := NewQueryPanel(a, "Query 1")
	qp2 := NewQueryPanel(a, "Query 2")
	a.panels.AddPanel(qp1)
	a.panels.AddPanel(qp2)

	a.focusPanels()
	a.nextPanel()
	if !qp2.active {
		t.Fatalf("qp2.active = false after nextPanel while panels have focus, want true")
	}
	if a.focus != "panels" {
		t.Fatalf("focus = %q after nextPanel, want panels", a.focus)
	}
}

// TestJumpToPanelByDigit confirms Ctrl+0..9 (App.jumpToPanel) switches
// directly to the panel at that index, counted from the left, and that an
// out-of-range digit is a harmless no-op.
func TestJumpToPanelByDigit(t *testing.T) {
	a := newTestApp()
	a.panels.AddPanel(NewDetailBrowser("Object Explorer Details")) // index 0
	qp1 := NewQueryPanel(a, "Query 1")
	a.panels.AddPanel(qp1) // index 1
	a.focusPanels()

	a.jumpToPanel(1)
	if a.panels.ActiveIndex() != 1 {
		t.Fatalf("ActiveIndex = %d, want 1", a.panels.ActiveIndex())
	}
	if !qp1.active {
		t.Fatalf("qp1.active = false after jumpToPanel(1) while panels have focus, want true")
	}

	a.jumpToPanel(0)
	if a.panels.ActiveIndex() != 0 {
		t.Fatalf("ActiveIndex = %d, want 0 (Object Explorer Details)", a.panels.ActiveIndex())
	}

	a.jumpToPanel(9) // out of range — only 2 panels exist
	if a.panels.ActiveIndex() != 0 {
		t.Fatalf("ActiveIndex = %d after out-of-range jumpToPanel(9), want unchanged 0", a.panels.ActiveIndex())
	}
}

// TestQueryPanelFocusHelpers covers editorHasFocus/resultsHasFocus across
// every active/resultsFocused combination — the source of truth for which
// of the two sub-regions' title/header bar gets the focused-highlight
// style in Draw (see syncFocusVisuals).
func TestQueryPanelFocusHelpers(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")

	cases := []struct {
		active, resultsFocused  bool
		wantEditor, wantResults bool
	}{
		{false, false, false, false},
		{false, true, false, false},
		{true, false, true, false},
		{true, true, false, true},
	}
	for _, c := range cases {
		qp.active = c.active
		qp.resultsFocused = c.resultsFocused
		if got := qp.editorHasFocus(); got != c.wantEditor {
			t.Errorf("active=%v resultsFocused=%v: editorHasFocus() = %v, want %v", c.active, c.resultsFocused, got, c.wantEditor)
		}
		if got := qp.resultsHasFocus(); got != c.wantResults {
			t.Errorf("active=%v resultsFocused=%v: resultsHasFocus() = %v, want %v", c.active, c.resultsFocused, got, c.wantResults)
		}
	}
}
