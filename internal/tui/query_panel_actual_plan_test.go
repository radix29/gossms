package tui

import (
	"database/sql/driver"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/tuikittest"
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

// TestSetResultMessagesFallbackIndexAccountsForPlanTab pins down that
// setResult's errors-or-no-sets fallback lands on the real last tab, not on
// len(res.Sets): "Execution Plan" can sit between Results and Messages in
// Actual mode, which shifts the Messages index.
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

// TestRenderAndStatusDoNotPanicOnPlanTabWithResults pins down that
// renderActiveTab and updateResultsStatus handle the Execution Plan tab
// (activeTab == len(Sets), out of range for Sets) without panicking: in
// Actual mode planView != nil no longer implies result == nil, so neither
// may index res.Sets[activeTab] unconditionally once Messages/ResultsText
// are ruled out.
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

func TestFabricExecuteSkipsUnsupportedActualPlan(t *testing.T) {
	for _, fabric := range []bool{false, true} {
		for _, mode := range []struct {
			name         string
			actual, live bool
		}{
			{"off", false, false},
			{"actual", true, false},
			{"live", false, true},
			{"both", true, true},
		} {
			t.Run(fmt.Sprintf("Fabric=%v/%s", fabric, mode.name), func(t *testing.T) {
				a := newTestApp()
				a.actualPlanEnabled, a.liveStatsEnabled = mode.actual, mode.live
				stats := fakeResponse{match: "SET STATISTICS XML"}
				if fabric {
					stats.err = mssql.Error{Number: 15869, Class: 16, State: 2,
						Message: "STATISTICS is not supported for SET."}
				}
				block := make(chan struct{})
				releaseQuery := sync.OnceFunc(func() { close(block) })
				qp, inst := hostedSessionPanel(t, a, 0, stats,
					fakeResponse{match: "SELECT 42", cols: 1, colNames: []string{"answer"},
						rows: [][]driver.Value{{int64(42)}}, block: block},
					fakeResponse{match: "sys.dm_exec_query_profiles"})
				t.Cleanup(qp.closeConnection)
				t.Cleanup(releaseQuery)
				qp.conn.IsFabricDW, qp.conn.Server.IsFabricDW = fabric, fabric

				qp.startRun("SELECT 42", 0, "")
				openedLive := qp.planView != nil && qp.planView.Live()
				wantLive := mode.live && !fabric
				if openedLive != wantLive {
					t.Fatalf("opened live view = %v, want %v", openedLive, wantLive)
				}
				if wantLive {
					drainUntil(t, a, func() bool {
						return len(inst.Reads("sys.dm_exec_query_profiles")) > 0
					}, "the live profiling read")
				}
				releaseQuery()
				drainUntil(t, a, func() bool { return !qp.executing }, "the query result")
				if qp.result == nil || qp.result.HasErrors() {
					t.Fatalf("query failed: %+v", qp.result)
				}
				if len(qp.result.Sets) != 1 || len(qp.result.Sets[0].Rows) != 1 ||
					qp.result.Sets[0].Rows[0][0] != "42" {
					t.Errorf("result sets = %+v, want the normal query result", qp.result.Sets)
				}
				if got := len(inst.Reads("SELECT 42")); got != 1 {
					t.Errorf("query ran %d times, want exactly once", got)
				}
				statements := 0
				for _, q := range inst.Queries() {
					if strings.Contains(q.Text, "SET STATISTICS") {
						statements++
					}
				}
				wantStatements := 0
				if !fabric && (mode.actual || mode.live) {
					wantStatements = 2
				}
				if statements != wantStatements {
					t.Errorf("sent %d STATISTICS statements, want %d", statements, wantStatements)
				}
				if !wantLive && len(inst.Reads("sys.dm_exec_query_profiles")) != 0 {
					t.Error("started live polling with Live Query Statistics unavailable or off")
				}
				noted := false
				for _, m := range qp.result.Messages {
					if !m.IsError && strings.Contains(m.Text, "Fabric DW") {
						noted = true
					}
				}
				if want := fabric && (mode.actual || mode.live); noted != want {
					t.Errorf("plan-unavailable notice = %v, want %v", noted, want)
				}
				if a.actualPlanEnabled != mode.actual || a.liveStatsEnabled != mode.live {
					t.Error("Fabric execution changed global SQL Server plan settings")
				}
			})
		}
	}
}

func TestFabricPlanControlsFollowActiveQueryConnection(t *testing.T) {
	a := newTestApp()
	explorerConn := addTestConn(a, "Object Explorer")
	fabric := NewQueryPanel(a, "Fabric")
	fabric.conn = &db.ServerConn{IsFabricDW: true}
	fabricIdx := a.panels.AddPanel(fabric)
	sqlServer := NewQueryPanel(a, "SQL Server")
	sqlServer.conn = &db.ServerConn{}
	sqlIdx := a.panels.AddPanel(sqlServer)
	buttons := a.buildToolbar()
	items := queryMenuItems(t, a.buildMenus())
	for _, idx := range []int{fabricIdx, sqlIdx, fabricIdx} {
		a.panels.SetActiveIndex(idx)
		explorerConn.IsFabricDW = idx == sqlIdx
		want := idx == sqlIdx
		planButtons, estimatedButtons := 0, 0
		for _, button := range buttons {
			if button.Tooltip == "Show Estimated Execution Plan" {
				estimatedButtons++
				if button.Enabled != nil && !button.Enabled() {
					t.Error("Estimated Plan was disabled for a query panel")
				}
			}
			if button.Tooltip == "Include Actual Execution Plan" || button.Tooltip == "Include Live Query Statistics" {
				planButtons++
				if got := button.Enabled == nil || button.Enabled(); got != want {
					t.Errorf("panel=%d button=%s enabled=%v, want %v", idx, button.Icon, got, want)
				}
			}
		}
		if planButtons != 2 || estimatedButtons != 1 {
			t.Fatalf("found %d actual/live buttons and %d estimated buttons, want 2 and 1", planButtons, estimatedButtons)
		}
		estimated := findMenuItem(items, "Estimated Execution Plan")
		if estimated == nil || (estimated.Enabled != nil && !estimated.Enabled()) {
			t.Error("Estimated Plan menu was disabled for a query panel")
		}
		for _, label := range []string{actualExecutionPlanMenuLabel(false), liveQueryStatisticsMenuLabel(false)} {
			item := findMenuItem(items, label)
			if item == nil {
				t.Fatalf("missing %s", label)
			}
			if got := item.Enabled == nil || item.Enabled(); got != want {
				t.Errorf("panel=%d menu=%s enabled=%v, want %v", idx, label, got, want)
			}
		}
	}
}

func TestFabricPlanToggleActionsDoNotChangeGlobalSettings(t *testing.T) {
	a := &App{cfg: newTestApp().cfg}
	a.buildUI()
	a.setScreen(tuikittest.NewScreen(120, 40))
	qp := NewQueryPanel(a, "Fabric")
	qp.conn = &db.ServerConn{IsFabricDW: true}
	a.panels.SetActiveIndex(a.panels.AddPanel(qp))
	for _, on := range []bool{false, true} {
		a.actualPlanEnabled, a.liveStatsEnabled = on, on
		a.toggleActualExecutionPlan()
		if a.actualPlanEnabled != on || a.liveStatsEnabled != on || !strings.Contains(a.statusText, "Fabric DW") {
			t.Error("Actual Plan action changed global settings or did not explain Fabric refusal")
		}
		a.toggleLiveQueryStatistics()
		if a.actualPlanEnabled != on || a.liveStatsEnabled != on || !strings.Contains(a.statusText, "Fabric DW") {
			t.Error("Live Statistics action changed global settings or did not explain Fabric refusal")
		}
	}
}

func TestFabricPlanControlsFollowExplorerWithoutQueryConnection(t *testing.T) {
	for _, disconnectedQuery := range []bool{false, true} {
		t.Run(fmt.Sprintf("disconnectedQuery=%v", disconnectedQuery), func(t *testing.T) {
			a := newTestApp()
			sqlServer := addTestConn(a, "SQL Server")
			fabric := addTestConn(a, "Fabric")
			fabric.IsFabricDW = true
			if disconnectedQuery {
				a.panels.SetActiveIndex(a.panels.AddPanel(NewQueryPanel(a, "Not connected")))
			}
			buttons := a.buildToolbar()
			items := queryMenuItems(t, a.buildMenus())
			for _, sc := range []*db.ServerConn{fabric, sqlServer, fabric} {
				a.explorer.AddRoot(sc.Opts.Server, sc)
				want := !sc.IsFabricDW
				planButtons := 0
				for _, button := range buttons {
					if button.Tooltip == "Include Actual Execution Plan" || button.Tooltip == "Include Live Query Statistics" {
						planButtons++
						if got := button.Enabled == nil || button.Enabled(); got != want {
							t.Errorf("Fabric=%v button=%s enabled=%v, want %v", sc.IsFabricDW, button.Icon, got, want)
						}
					}
				}
				if planButtons != 2 {
					t.Fatalf("found %d actual/live buttons, want 2", planButtons)
				}
				for _, label := range []string{actualExecutionPlanMenuLabel(false), liveQueryStatisticsMenuLabel(false)} {
					item := findMenuItem(items, label)
					if item == nil {
						t.Fatalf("missing %s", label)
					}
					if got := item.Enabled == nil || item.Enabled(); got != want {
						t.Errorf("Fabric=%v menu=%s enabled=%v, want %v", sc.IsFabricDW, label, got, want)
					}
				}
			}
		})
	}
}

func TestFabricExplorerPlanActionsPreserveGlobalSettings(t *testing.T) {
	a := newTestApp()
	fabric := addTestConn(a, "Fabric")
	fabric.IsFabricDW = true
	if a.actualPlanAvailable() {
		t.Fatal("Fabric explorer permits plan toggles without a query")
	}
	for _, on := range []bool{false, true} {
		a.actualPlanEnabled, a.liveStatsEnabled = on, on
		a.toggleActualExecutionPlan()
		if a.actualPlanEnabled != on || a.liveStatsEnabled != on || !strings.Contains(a.statusText, "Fabric DW") {
			t.Error("Actual Plan action changed global settings or did not explain Fabric refusal")
		}
		a.toggleLiveQueryStatistics()
		if a.actualPlanEnabled != on || a.liveStatsEnabled != on || !strings.Contains(a.statusText, "Fabric DW") {
			t.Error("Live Statistics action changed global settings or did not explain Fabric refusal")
		}
	}
}

func TestFabricResultsToFileSkipsPlanCaptureAndNotice(t *testing.T) {
	for _, fabric := range []bool{false, true} {
		t.Run(fmt.Sprintf("Fabric=%v", fabric), func(t *testing.T) {
			a := newTestApp()
			a.actualPlanEnabled, a.liveStatsEnabled = true, true
			qp, inst := hostedSessionPanel(t, a, 0,
				fakeResponse{match: "SET STATISTICS", err: mssql.Error{Number: 15869, Class: 16,
					Message: "STATISTICS is not supported for SET."}},
				fakeResponse{match: "SELECT 42", cols: 1, colNames: []string{"answer"}, rows: [][]driver.Value{{int64(42)}}})
			t.Cleanup(qp.closeConnection)
			qp.conn.IsFabricDW, qp.conn.Server.IsFabricDW = fabric, fabric
			path := filepath.Join(t.TempDir(), "results.csv")
			qp.startRun("SELECT 42", 0, path)
			if qp.planView != nil {
				t.Error("file export opened a live plan view")
			}
			drainUntil(t, a, func() bool { return !qp.executing }, "the exported query result")
			if qp.result == nil || qp.result.HasErrors() || qp.result.RowsWritten != 1 || len(qp.result.Sets) != 0 {
				t.Fatalf("export result = %+v, want one streamed row and no retained sets", qp.result)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			records, err := csv.NewReader(strings.NewReader(string(data))).ReadAll()
			if err != nil || len(records) != 2 || len(records[0]) != 1 || records[0][0] != "answer" || records[1][0] != "42" {
				t.Fatalf("CSV records = %v, error = %v, want header and 42", records, err)
			}
			if got := len(inst.Reads("SELECT 42")); got != 1 {
				t.Errorf("query ran %d times, want exactly once", got)
			}
			for _, q := range inst.Queries() {
				if strings.Contains(q.Text, "SET STATISTICS") || strings.Contains(q.Text, "sys.dm_exec_query_profiles") {
					t.Errorf("file export sent profiling query: %s", q.Text)
				}
			}
			for _, message := range qp.result.Messages {
				if message.Text == fabricActualPlanUnavailable {
					t.Error("file export received an irrelevant plan-unavailable notice")
				}
			}
			if !a.actualPlanEnabled || !a.liveStatsEnabled {
				t.Error("file export changed global SQL Server plan settings")
			}
		})
	}
}
