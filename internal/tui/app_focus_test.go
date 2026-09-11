package tui

import "testing"

// Ctrl+Tab (App.cycleFocus) steps Explorer → editor → results → Explorer when
// the panel has results.
func TestCycleFocusThreeWay(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)
	qp.setResult(newTestResult(1, false), false)

	a.focusExplorer()

	a.cycleFocus() // explorer -> editor
	if a.focus != focusOnPanels || qp.resultsFocused {
		t.Fatalf("after 1st cycleFocus: focus=%q resultsFocused=%v, want panels/false (editor)", a.focus, qp.resultsFocused)
	}

	a.cycleFocus() // editor -> results
	if a.focus != focusOnPanels || !qp.resultsFocused {
		t.Fatalf("after 2nd cycleFocus: focus=%q resultsFocused=%v, want panels/true (results)", a.focus, qp.resultsFocused)
	}

	a.cycleFocus() // results -> explorer
	if a.focus != focusOnExplorer {
		t.Fatalf("after 3rd cycleFocus: focus=%q, want explorer", a.focus)
	}
}

// Without results, it degrades to the Explorer/editor toggle.
func TestCycleFocusTwoWayWithoutResults(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)

	a.focusExplorer()

	a.cycleFocus() // explorer -> editor
	if a.focus != focusOnPanels {
		t.Fatalf("focus = %q, want panels", a.focus)
	}
	a.cycleFocus() // editor -> explorer directly: no results pane to stop at
	if a.focus != focusOnExplorer {
		t.Fatalf("focus = %q, want explorer (no results pane to cycle through)", a.focus)
	}
}

// A non-query panel (e.g. Object Explorer Details) degrades to the
// Explorer/panel toggle.
func TestCycleFocusTwoWayForNonQueryPanel(t *testing.T) {
	a := newTestApp()
	a.panels.AddPanel(NewDetailBrowser("Object Explorer Details"))

	a.focusExplorer()

	a.cycleFocus()
	if a.focus != focusOnPanels {
		t.Fatalf("focus = %q, want panels", a.focus)
	}
	a.cycleFocus()
	if a.focus != focusOnExplorer {
		t.Fatalf("focus = %q, want explorer", a.focus)
	}
}

// Ctrl+Shift+Tab: Explorer → Results → Editor → Explorer.
func TestCycleFocusReverseThreeWay(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)
	qp.setResult(newTestResult(1, false), false)

	a.focusExplorer()

	a.cycleFocusReverse() // explorer -> results
	if a.focus != focusOnPanels || !qp.resultsFocused {
		t.Fatalf("after 1st cycleFocusReverse: focus=%q resultsFocused=%v, want panels/true (results)", a.focus, qp.resultsFocused)
	}

	a.cycleFocusReverse() // results -> editor
	if a.focus != focusOnPanels || qp.resultsFocused {
		t.Fatalf("after 2nd cycleFocusReverse: focus=%q resultsFocused=%v, want panels/false (editor)", a.focus, qp.resultsFocused)
	}

	a.cycleFocusReverse() // editor -> explorer
	if a.focus != focusOnExplorer {
		t.Fatalf("after 3rd cycleFocusReverse: focus=%q, want explorer", a.focus)
	}
}

// The reverse of TestCycleFocusTwoWayWithoutResults.
func TestCycleFocusReverseTwoWayWithoutResults(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)

	a.focusExplorer()

	a.cycleFocusReverse() // explorer -> editor directly: no results to stop at
	if a.focus != focusOnPanels || qp.resultsFocused {
		t.Fatalf("focus=%q resultsFocused=%v, want panels/false (editor)", a.focus, qp.resultsFocused)
	}
	a.cycleFocusReverse() // editor -> explorer
	if a.focus != focusOnExplorer {
		t.Fatalf("focus = %q, want explorer", a.focus)
	}
}

// Forward then reverse the same number of times returns to the start, so the
// two are true inverses.
func TestCycleFocusThenReverseReturnsToStart(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	a.panels.AddPanel(qp)
	qp.setResult(newTestResult(1, false), false)
	a.focusExplorer()

	a.cycleFocus() // explorer -> editor
	if a.focus != focusOnPanels || qp.resultsFocused {
		t.Fatalf("after cycleFocus: focus=%q resultsFocused=%v, want panels/false", a.focus, qp.resultsFocused)
	}
	a.cycleFocusReverse() // editor -> explorer
	if a.focus != focusOnExplorer {
		t.Fatalf("after cycleFocusReverse: focus=%q, want explorer", a.focus)
	}
}

// Ctrl+Shift+Right/Left (nextPanel/prevPanel) re-sync the new panel's
// Activatable state to a.focus: switching while Explorer has focus must not
// make the new panel look focused (see syncActivePanelFocus).
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
	if a.focus != focusOnExplorer {
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

// Switching while a panel has focus keeps the new one focused.
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
	if a.focus != focusOnPanels {
		t.Fatalf("focus = %q after nextPanel, want panels", a.focus)
	}
}

// Ctrl+0..9 (jumpToPanel) switches to that index from the left; out-of-range is
// a no-op.
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

// editorHasFocus/resultsHasFocus across every active/resultsFocused
// combination; they decide which sub-region's bar is highlighted (see
// syncFocusVisuals).
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
