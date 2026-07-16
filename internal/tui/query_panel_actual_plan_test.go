package tui

import (
	"strings"
	"testing"
)

// TestSetResultPlanInstallsExecutionPlanTab checks a normal Execute whose
// Result carries a captured actual-plan XML gets an Execution Plan tab
// inserted between the Results tab(s) and Messages — unlike Estimated
// mode, Results stay in place; see setResultPlan's doc comment.
func TestSetResultPlanInstallsExecutionPlanTab(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	res := newTestResult(2, false)
	res.PlanXML = []string{testPlanXML}

	qp.setResult(res, false)

	tabs := qp.resultTabs()
	want := []string{"Results 1", "Results 2", "Execution Plan", "Messages"}
	if len(tabs) != len(want) {
		t.Fatalf("resultTabs() = %v, want %v", tabs, want)
	}
	for i := range want {
		if tabs[i] != want[i] {
			t.Errorf("resultTabs()[%d] = %q, want %q", i, tabs[i], want[i])
		}
	}
	if qp.planView == nil {
		t.Fatal("planView = nil, want it installed")
	}
	if qp.result == nil {
		t.Error("result = nil, want it still set (Actual mode keeps Results, unlike Estimated)")
	}
	planIdx := 2
	qp.setActiveTab(planIdx)
	if !qp.planTabActive() {
		t.Errorf("planTabActive() = false at tab %d, want true", planIdx)
	}
}

// TestSetResultPlanNoPlanXMLClearsPlanView checks a normal Execute (no
// actual-plan capture) clears any Execution Plan tab left over from an
// earlier run with the toggle on.
func TestSetResultPlanNoPlanXMLClearsPlanView(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	first := newTestResult(1, false)
	first.PlanXML = []string{testPlanXML}
	qp.setResult(first, false)
	if qp.planView == nil {
		t.Fatal("planView = nil after first run, want it installed")
	}

	qp.setResult(newTestResult(1, false), false)

	if qp.planView != nil {
		t.Error("planView != nil after a run with no PlanXML, want it cleared")
	}
	tabs := qp.resultTabs()
	want := []string{"Results", "Messages"}
	if len(tabs) != len(want) || tabs[0] != want[0] || tabs[1] != want[1] {
		t.Errorf("resultTabs() = %v, want %v", tabs, want)
	}
}

// TestSetResultPlanMultipleCapturesAreMerged checks a script that captures
// more than one actual plan (multiple statements/GO batches) merges every
// captured document into one Plan instead of showing only the first — SET
// STATISTICS XML ON appends a separate showplan document per statement
// (unlike SHOWPLAN_XML ON's single combined document), so setResultPlan
// merges them with showplan.ParseAll and hands the result to PlanView,
// whose own statement selector lets the user step through all of them.
func TestSetResultPlanMultipleCapturesAreMerged(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	res := newTestResult(1, false)
	res.PlanXML = []string{testPlanXML, testPlanXML}

	qp.setResult(res, false)

	if qp.planView == nil {
		t.Fatal("planView = nil, want it installed")
	}
	plan := qp.planView.Plan()
	if plan == nil || len(plan.Statements) != 2 {
		t.Fatalf("planView.Plan().Statements = %v, want 2 statements (one per captured document)", plan)
	}
	for _, m := range res.Messages {
		if strings.Contains(m.Text, "Only the first") {
			t.Errorf("res.Messages contains a stale 'only the first' notice: %q", m.Text)
		}
	}
}

// TestSetResultPlanParseErrorLeavesNoPlanTab checks a PlanXML entry that
// fails to parse doesn't leave a broken/empty Execution Plan tab reachable
// — it falls back to reporting the error on Messages instead, same as a
// fetch error would.
func TestSetResultPlanParseErrorLeavesNoPlanTab(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	res := newTestResult(1, false)
	res.PlanXML = []string{"not valid xml"}

	qp.setResult(res, false)

	if qp.planView != nil {
		t.Error("planView != nil after a parse failure, want nil")
	}
	hasError := false
	for _, m := range res.Messages {
		if m.IsError {
			hasError = true
		}
	}
	if !hasError {
		t.Errorf("res.Messages = %+v, want an error message about the failed parse", res.Messages)
	}
}

// TestSetResultMessagesFallbackIndexAccountsForPlanTab is a regression test
// for the bug this feature would otherwise introduce: setResult's
// errors-or-no-sets fallback used to hardcode len(res.Sets) as the
// Messages tab's index, which is wrong once "Execution Plan" can sit
// between Results and Messages (Actual mode) — it must land on the real
// last tab instead.
func TestSetResultMessagesFallbackIndexAccountsForPlanTab(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	res := newTestResult(2, true) // withError = true forces the Messages fallback
	res.PlanXML = []string{testPlanXML}

	qp.setResult(res, false)

	tabs := qp.resultTabs()
	wantIdx := len(tabs) - 1
	if qp.activeTab != wantIdx {
		t.Errorf("activeTab = %d, want %d (Messages, the real last tab of %v)", qp.activeTab, wantIdx, tabs)
	}
	if !qp.onMessagesTab() {
		t.Error("onMessagesTab() = false, want true")
	}
}

// TestRenderAndStatusDoNotPanicOnPlanTabWithResults is a regression test
// for the second bug this feature would otherwise introduce:
// renderActiveTab and updateResultsStatus both indexed res.Sets[activeTab]
// unconditionally once Messages/ResultsText were ruled out — correct only
// because planView != nil used to imply result == nil. Actual mode breaks
// that invariant, so selecting the Execution Plan tab (activeTab ==
// len(Sets), out of range for Sets) must not panic either function.
func TestRenderAndStatusDoNotPanicOnPlanTabWithResults(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	res := newTestResult(1, false)
	res.PlanXML = []string{testPlanXML}
	qp.setResult(res, false)

	planIdx := len(res.Sets) // 1 — where Actual mode places Execution Plan
	qp.setActiveTab(planIdx)
	if !qp.planTabActive() {
		t.Fatalf("planTabActive() = false at tab %d, want true (test setup is wrong)", planIdx)
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panicked selecting the Execution Plan tab: %v", r)
		}
	}()
	qp.renderActiveTab()
	qp.updateResultsStatus()
}

// TestQueryMenuActualPlanLabelReflectsToggleState checks
// actualExecutionPlanMenuLabel and actualPlanToggleIcon produce distinct,
// equal-width ON/OFF text — equal width so the toolbar toggle button never
// needs its neighbors to shift.
func TestQueryMenuActualPlanLabelReflectsToggleState(t *testing.T) {
	if got := actualExecutionPlanMenuLabel(false); got != "Actual Execution Plan (OFF)" {
		t.Errorf("actualExecutionPlanMenuLabel(false) = %q", got)
	}
	if got := actualExecutionPlanMenuLabel(true); got != "Actual Execution Plan (ON)" {
		t.Errorf("actualExecutionPlanMenuLabel(true) = %q", got)
	}
	off, on := actualPlanToggleIcon(false), actualPlanToggleIcon(true)
	if off == on {
		t.Errorf("actualPlanToggleIcon(false) == actualPlanToggleIcon(true) = %q, want distinct text", off)
	}
	if len(off) != len(on) {
		t.Errorf("actualPlanToggleIcon widths differ: off=%q (%d), on=%q (%d) — toggling would shift the toolbar's other buttons", off, len(off), on, len(on))
	}
}
