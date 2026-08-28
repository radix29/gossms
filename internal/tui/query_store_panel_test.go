package tui

import (
	"database/sql/driver"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// qsReportResponse answers the per-query reports. Matched on the query
// table's own FROM rather than on "sys.query_store_runtime_stats", which the
// plan read joins too — the two would otherwise be answered by whichever
// response was scripted first, and the plan pane would scan a report's rows.
func qsReportResponse(rows ...[]driver.Value) fakeResponse {
	return fakeResponse{match: "FROM   sys.query_store_query ", cols: 12, rows: rows}
}

// qsPlanRow is one row of the fourteen-column shape QueryStorePlansContext
// returns — see gosmo's QSPlan scan.
func qsPlanRow(planID, queryID int64, forced bool, xml string, execs int64, value float64) []driver.Value {
	return []driver.Value{
		planID, queryID, forced, "", int64(0), "",
		int64(160), false, false,
		time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC),
		xml, execs, value,
	}
}

func qsPlansResponse(rows ...[]driver.Value) fakeResponse {
	return fakeResponse{match: "FROM   sys.query_store_plan AS p", cols: 14, rows: rows}
}

// newQSPanel builds a panel over a scripted instance, already sized so both
// grids have rows to move a cursor through.
func newQSPanel(t *testing.T, title string, resp ...fakeResponse) (*QueryStorePanel, *App, *fakeInstance) {
	t.Helper()
	a := newTestApp()
	sc, inst := newFakeConn(t, resp...)
	p := NewQueryStorePanel(a, sc, "appdb", title)
	p.SetBounds(0, 0, 120, 40)
	p.SetActive(true)
	return p, a, inst
}

// TestQueryStorePanelOpensOnTheNamedReport. Every Query Store leaf opens the
// panel on its own view; a leaf that opened someone else's would be a tree
// entry that does the wrong thing, which is what the context-gating rule
// exists to prevent.
func TestQueryStorePanelOpensOnTheNamedReport(t *testing.T) {
	a := newTestApp()
	for _, title := range queryStoreReportTitles {
		p := NewQueryStorePanel(a, nil, "appdb", title)
		if got := p.report().Title; got != title {
			t.Errorf("a panel opened on %q shows %q", title, got)
		}
		// The statistic follows the report, not a fixed default: Top Resource
		// Consuming Queries ranked by Avg is a different report from the one
		// its leaf just showed.
		if got, want := p.stat, queryStoreReportByTitleOrFail(t, title).defaultStat; got != want {
			t.Errorf("%q opened with statistic %q, want its own default %q", title, got, want)
		}
	}
	// The folder's own menu item names no report.
	if got := NewQueryStorePanel(a, nil, "appdb", "").report().Title; got != queryStoreReportTitles[0] {
		t.Errorf("a panel opened with no report shows %q, want the first report", got)
	}
}

func queryStoreReportByTitleOrFail(t *testing.T, title string) queryStoreReport {
	t.Helper()
	r, ok := queryStoreReportByTitle(title)
	if !ok {
		t.Fatalf("no report named %q", title)
	}
	return r
}

// TestQueryStorePanelOptionsFollowTheToolbar. Four selectors feed one options
// struct, and a selector wired to the wrong field still redraws its own label
// — the toolbar would read "Window: 7 d" while the report kept reading an
// hour, with nothing on screen disagreeing.
func TestQueryStorePanelOptionsFollowTheToolbar(t *testing.T) {
	a := newTestApp()
	p := NewQueryStorePanel(a, nil, "appdb", "Top Resource Consuming Queries")
	p.metric = gosmo.QSMetricCPUTime
	p.stat = gosmo.QSStatMax
	p.windowIdx = len(qsWindows) - 1 // 30 d
	p.topIdx = len(qsTopCounts) - 1  // 100

	opts := p.options()
	if opts.Metric != gosmo.QSMetricCPUTime {
		t.Errorf("Metric = %q, want CPU time", opts.Metric)
	}
	if opts.Statistic != gosmo.QSStatMax {
		t.Errorf("Statistic = %q, want Max", opts.Statistic)
	}
	if opts.Top != qsTopCounts[len(qsTopCounts)-1] {
		t.Errorf("Top = %d, want %d", opts.Top, qsTopCounts[len(qsTopCounts)-1])
	}
	if got := opts.To.Sub(opts.From); got != qsWindows[len(qsWindows)-1].back {
		t.Errorf("window = %v, want %v", got, qsWindows[len(qsWindows)-1].back)
	}

	// And the labels name what was selected, not what was defaulted.
	p.refreshToolLabels()
	for i, want := range map[int]string{
		qsToolMetric:    "CPU time",
		qsToolStatistic: "Max",
		qsToolWindow:    qsWindows[len(qsWindows)-1].label,
		qsToolTop:       "100",
	} {
		if !strings.Contains(p.sel[i].label, want) {
			t.Errorf("selector %d reads %q, want it to name %q", i, p.sel[i].label, want)
		}
	}
}

// TestQueryStorePanelLoadsAReportThenTheSelectedQuerysPlans drives the panel
// end to end. The second query is the one the cursor is moved to on purpose:
// a panel that ignored the selection and always read the first row's plans
// would pass with a cursor left at the top.
func TestQueryStorePanelLoadsAReportThenTheSelectedQuerysPlans(t *testing.T) {
	p, a, inst := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(
			qsPlanRow(41, 12, false, "<ShowPlanXML/>", 5, 100),
			qsPlanRow(42, 12, true, "<ShowPlanXML/>", 7, 90),
		),
		qsReportResponse(
			qsStatRow(11, "dbo.p", "SELECT 1", 2500, 1234, 0, 1),
			qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 42, 2),
		))

	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")
	if len(p.res.rows) != 2 {
		t.Fatalf("got %d rows, want 2 (unanswered: %v)", len(p.res.rows), inst.unmatched)
	}

	// Down moves to the second query, which is what fires the plan read.
	if !p.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)) {
		t.Fatal("the report grid did not take Down")
	}
	if got := p.selectedQueryID(); got != 12 {
		t.Fatalf("selected query = %d after one Down, want the second row's query 12", got)
	}
	drainUntil(t, a, func() bool { return len(p.plans) > 0 }, "the plan pane to load")
	if p.queryID != 12 {
		t.Errorf("the plan pane is showing query %d, want the selected query 12", p.queryID)
	}
	if len(p.plans) != 2 {
		t.Fatalf("got %d plans, want 2", len(p.plans))
	}
	// The forced plan is the second one — a panel that reported the first
	// row's state would dim the wrong action.
	if p.plans[0].IsForced || !p.plans[1].IsForced {
		t.Errorf("forced flags = %v/%v, want only the second plan forced",
			p.plans[0].IsForced, p.plans[1].IsForced)
	}
}

// TestQueryStorePanelSaysWhenQueryStoreIsOff. Every report against a database
// with Query Store off comes back empty, which reads identically to a
// database nothing has run in — and sends the user looking for a bug instead
// of a setting. The panel must also not go on to run the report anyway.
func TestQueryStorePanelSaysWhenQueryStoreIsOff(t *testing.T) {
	p, a, inst := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("OFF", "OFF"),
		qsReportResponse(qsStatRow(11, "dbo.p", "SELECT 1", 2500, 1234, 0, 1)))
	before := inst.QueryCount()

	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to finish")

	if len(p.res.rows) == 0 {
		t.Fatal("a database with Query Store off produced an empty panel")
	}
	if p.res.columns[0] != "Property" {
		t.Errorf("columns = %v, want the Property/Value shape", p.res.columns)
	}
	if !strings.Contains(p.res.rows[0].cells[1], "OFF") {
		t.Errorf("first row = %v, want it to name the OFF state", p.res.rows[0].cells)
	}
	if n := inst.QueryCount() - before; n > 1 {
		t.Errorf("%d queries ran with Query Store off, want only the one that established it", n)
	}
}

// TestForcePlanWritesTheSelectedPlan pins the whole write path: the question,
// the statement, and the two ids it carries. sp_query_store_force_plan takes
// both as parameters, so the statement text alone says nothing about which
// plan was pinned — swapping the two arguments would read identically here
// and change a different query on the server.
func TestForcePlanWritesTheSelectedPlan(t *testing.T) {
	p, a, inst := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(
			qsPlanRow(41, 12, false, "", 5, 100),
			qsPlanRow(42, 12, false, "", 7, 90),
		),
		qsReportResponse(qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 0, 2)))

	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")
	drainUntil(t, a, func() bool { return len(p.plans) == 2 }, "the plan pane to load")

	// The second plan, not the first: a panel that ignored the plan cursor
	// would force plan 41 and pass with the cursor left at the top.
	p.setFocus(qsFocusPlans)
	if !p.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)) {
		t.Fatal("the plan grid did not take Down")
	}
	if got := p.selectedPlan().PlanID; got != 42 {
		t.Fatalf("selected plan = %d, want the second plan 42", got)
	}

	p.runAct(qsActForce)
	if !a.confirmDialog.Visible() {
		t.Fatal("Force Plan ran without asking — it changes what a live database does on every execution")
	}
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	drainUntil(t, a, func() bool { return len(inst.StatementsIn("appdb")) > 0 }, "the force to run")

	stmts := inst.StatementsIn("appdb")
	if !strings.Contains(stmts[0], "sp_query_store_force_plan") {
		t.Fatalf("statement = %q, want sp_query_store_force_plan", stmts[0])
	}
	args := inst.forceArgs(t, "sp_query_store_force_plan")
	if len(args) != 2 || args[0] != int64(12) || args[1] != int64(42) {
		t.Errorf("force plan arguments = %v, want the selected query 12 and plan 42", args)
	}
}

// forceArgs returns the parameters of the first executed statement containing
// needle.
func (f *fakeInstance) forceArgs(t *testing.T, needle string) []driver.Value {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.execs {
		if strings.Contains(e.sql, needle) {
			return e.args
		}
	}
	t.Fatalf("no statement containing %q was executed", needle)
	return nil
}

// TestPlanActionsAreWithheldAndSayWhy. Force and Unforce are opposite halves
// of one state, and a panel offering both at once lets a click do nothing —
// or the wrong thing. Every withheld cell must also be able to say why: a
// disabled button swallows its click, and swallowing it silently is what the
// context-gating rule exists to prevent.
func TestPlanActionsAreWithheldAndSayWhy(t *testing.T) {
	a := newTestApp()
	p := NewQueryStorePanel(a, nil, "appdb", "Top Resource Consuming Queries")
	p.SetBounds(0, 0, 120, 40)

	// Nothing selected: both are withheld, and the reason names the pane to
	// select in rather than a permission the user may already hold.
	for _, i := range []int{qsActForce, qsActUnforce, qsActShowPlan} {
		if !p.actDisabled(i) {
			t.Errorf("action %d is live with no plan selected", i)
		}
	}
	if got := p.actReason(qsActForce); !strings.Contains(got, "plan") {
		t.Errorf("reason with nothing selected = %q, want it to name the plan pane", got)
	}

	p.plans = []*gosmo.QSPlan{{PlanID: 7, QueryID: 3, IsForced: false}}
	p.plansGrid.SetData(qsPlanColumns, qsPlanRows(p.plans, p.options()))
	if p.actDisabled(qsActForce) {
		t.Error("Force is withheld for a plan that is not forced")
	}
	if !p.actDisabled(qsActUnforce) {
		t.Error("Unforce is offered for a plan that is not forced")
	}
	if got := p.actReason(qsActUnforce); !strings.Contains(got, "not forced") {
		t.Errorf("Unforce reason = %q, want it to say the plan is not forced", got)
	}

	p.plans[0].IsForced = true
	if !p.actDisabled(qsActForce) {
		t.Error("Force is offered for a plan that is already forced")
	}
	if p.actDisabled(qsActUnforce) {
		t.Error("Unforce is withheld for a forced plan")
	}

	// And a read in flight greys the whole row, without naming a permission
	// that is not the reason.
	p.busy = true
	if !p.actDisabled(qsActUnforce) {
		t.Error("Unforce is live while the panel is busy")
	}
	if got := p.actReason(qsActUnforce); got != "" {
		t.Errorf("busy reason = %q, want none — the grey row already says it", got)
	}
}

// TestQueryStorePanelTabLeavesOnTheSecondPress. App only moves focus out of a
// panel when the panel declines the key, so a panel that always consumes Tab
// can be left only with the mouse.
func TestQueryStorePanelTabLeavesOnTheSecondPress(t *testing.T) {
	a := newTestApp()
	p := NewQueryStorePanel(a, nil, "appdb", "")
	p.SetBounds(0, 0, 120, 40)
	p.SetActive(true)

	tab := func() bool { return p.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone)) }
	if !tab() {
		t.Fatal("the first Tab was declined, want it to move to the plan pane")
	}
	if p.focus != qsFocusPlans {
		t.Fatalf("focus = %v after one Tab, want the plan pane", p.focus)
	}
	if tab() {
		t.Error("the second Tab was consumed — the panel is a keyboard trap")
	}
	if p.focus != qsFocusReport {
		t.Errorf("focus = %v after leaving, want it reset to the report grid", p.focus)
	}
}

// TestQueryStorePlanRowsRenderByColumn. Ten columns of mostly numbers and
// yes/no is the shape where two swap unnoticed: the grid still renders, and a
// plan's execution count reads as its compatibility level.
func TestQueryStorePlanRowsRenderByColumn(t *testing.T) {
	plans := []*gosmo.QSPlan{{
		PlanID: 42, QueryID: 12, IsForced: true, ForcingType: "MANUAL",
		ForceFailureCount: 3, CompatibilityLevel: 150,
		IsParallelPlan: true, IsTrivialPlan: false,
		ExecCount: 1234, Value: 2500,
		LastExecutionTime: time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC),
	}}
	rows := qsPlanRows(plans, gosmo.QueryStoreReportOptions{Metric: gosmo.QSMetricDuration})
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	want := map[string]string{
		"Plan ID":        "42",
		"Forced":         yesNo(true),
		"Forcing Type":   "MANUAL",
		"Executions":     "1,234",
		"Value":          "2.50 ms",
		"Parallel":       yesNo(true),
		"Trivial":        yesNo(false),
		"Compat":         "150",
		"Force Failures": "3",
	}
	for name, wantVal := range want {
		idx := -1
		for i, c := range qsPlanColumns {
			if c == name {
				idx = i
			}
		}
		if idx < 0 {
			t.Errorf("no %q column in %v", name, qsPlanColumns)
			continue
		}
		if rows[0][idx] != wantVal {
			t.Errorf("%s = %q, want %q", name, rows[0][idx], wantVal)
		}
	}
}

// TestRegressedQueriesPlotsTheRegressionNotTheCost. The chart and the grid are
// fed from one row, and the two ranking reports plot something other than
// their value column — a chart plotting the absolute duration under a report
// about change would put the slowest query on top and read as correct.
func TestRegressedQueriesPlotsTheRegressionNotTheCost(t *testing.T) {
	stats := []driver.Value{
		int64(11), "SELECT 1", "dbo.p", int64(40), int64(2), int64(0),
		time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC),
		float64(4000), float64(1000), int64(40), float64(3000), float64(0),
	}
	sc, _ := newFakeConn(t, qsDatabaseResponse(), qsReportResponse(stats))
	d, err := sc.Server.DatabaseByName("appdb")
	if err != nil {
		t.Fatalf("DatabaseByName: %v", err)
	}
	to := time.Now()
	res, err := regressedQueriesReport(t.Context(), d,
		gosmo.QueryStoreReportOptions{From: to.Add(-time.Hour), To: to})
	if err != nil {
		t.Fatalf("regressedQueriesReport: %v", err)
	}
	if len(res.rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(res.rows))
	}
	if got := res.rows[0].value; got != 3000 {
		t.Errorf("the bar plots %v, want the regression 3000 — not the value 4000", got)
	}
	if !strings.Contains(res.chartLabel, "Regression") {
		t.Errorf("chart label = %q, want it to say what the bars are", res.chartLabel)
	}
}

// TestQSResultBarsSkipRowsWithNothingToPlot. BarChart drops bars past the
// pane's height, so a zero-valued bar is not merely invisible — it takes a
// row from a real one and pushes it off the chart.
func TestQSResultBarsSkipRowsWithNothingToPlot(t *testing.T) {
	res := qsResult{rows: []qsResultRow{
		{label: "Q1", value: 0},
		{label: "Q2", value: 5},
		{label: "Q3", value: -1},
	}}
	bars := res.bars(nil, theme.Active().ChartCyan)
	if len(bars) != 1 {
		t.Fatalf("got %d bars, want only the one with a value: %v", len(bars), bars)
	}
	if bars[0].Label != "Q2" || bars[0].Value != 5 {
		t.Errorf("bar = %q/%v, want Q2/5", bars[0].Label, bars[0].Value)
	}
}

// TestRefreshKeepsThePlaceAndAReportChangeDoesNot. The reload after Force
// Plan runs through Refresh, and it is the row just acted on that the user is
// looking at — SetData would drop them back at the top of the report with the
// plan pane on someone else's query. A report or metric change is the other
// case: row 4 of the old ranking is not row 4 of the new one.
func TestRefreshKeepsThePlaceAndAReportChangeDoesNot(t *testing.T) {
	p, a, _ := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(qsPlanRow(41, 12, false, "", 5, 100)),
		qsReportResponse(
			qsStatRow(11, "dbo.p", "SELECT 1", 2500, 1234, 0, 1),
			qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 0, 2),
			qsStatRow(13, "dbo.r", "SELECT 3", 100, 4, 0, 1),
		))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

	p.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone))
	if p.grid.SelectedRow() != 1 {
		t.Fatalf("setup: cursor on row %d, want row 1", p.grid.SelectedRow())
	}

	p.Refresh()
	drainUntil(t, a, func() bool { return !p.busy }, "the refresh to land")
	if got := p.grid.SelectedRow(); got != 1 {
		t.Errorf("Refresh left the cursor on row %d, want it where the user was (row 1)", got)
	}

	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the reload to land")
	if got := p.grid.SelectedRow(); got != 0 {
		t.Errorf("a report change left the cursor on row %d, want the top of the new ranking", got)
	}
}

// TestThePanelOpensOnTheWindowTheDetailBrowserRead. A report leaf's grid and
// the panel it opens are the same report, and opening one from the other with
// a different range makes the rows change for no reason the user can see. The
// two ranges are separate constants, so nothing but this holds them together.
func TestThePanelOpensOnTheWindowTheDetailBrowserRead(t *testing.T) {
	if qsDefaultWindowIdx < 0 || qsDefaultWindowIdx >= len(qsWindows) {
		t.Fatalf("qsDefaultWindowIdx = %d, outside the %d windows offered", qsDefaultWindowIdx, len(qsWindows))
	}
	if got := qsWindows[qsDefaultWindowIdx].back; got != queryStoreDetailWindow {
		t.Errorf("the panel opens on %v, the Detail Browser reads %v", got, queryStoreDetailWindow)
	}
}

// TestOpeningTheFolderKeepsAnOpenPanelOnItsReport. The Query Store folder's
// own Open Query Store... names no report, and queryStoreReportIndex maps an
// unnamed report to the first one. That is right for a panel being created and
// wrong for one already open: the folder sits directly above the seven leaves,
// so the gesture most likely to follow "read Tracked Queries" is a click on the
// folder — which used to re-run the panel as Regressed Queries and lose the
// place. A leaf must still re-point it, which is the half a guard that skipped
// ShowReport outright would break.
func TestOpeningTheFolderKeepsAnOpenPanelOnItsReport(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t,
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(qsPlanRow(41, 11, false, "", 5, 100)),
		qsReportResponse(qsStatRow(11, "dbo.p", "SELECT 1", 2500, 1234, 0, 1)))

	// A leaf opens the panel on its own report.
	a.showQueryStorePanelFor(sc, "appdb", "Queries With Forced Plans")
	if a.panels.Count() != 1 {
		t.Fatalf("a leaf opened %d panels, want 1", a.panels.Count())
	}
	p := a.panels.PanelAt(0).(*QueryStorePanel)
	drainUntil(t, a, func() bool { return !p.busy }, "the first report to load")
	if got := p.report().Title; got != "Queries With Forced Plans" {
		t.Fatalf("setup: the panel opened on %q", got)
	}

	// The folder, naming no report, brings that same panel forward unchanged.
	a.showQueryStorePanelFor(sc, "appdb", "")
	if n := a.panels.Count(); n != 1 {
		t.Errorf("the folder opened a second panel (%d in all), want the one already showing appdb", n)
	}
	if got := p.report().Title; got != "Queries With Forced Plans" {
		t.Errorf("Open Query Store... moved the panel to %q, want it left on the report the user was reading", got)
	}

	// A leaf still re-points it rather than opening a tab per view.
	a.showQueryStorePanelFor(sc, "appdb", "Tracked Queries")
	drainUntil(t, a, func() bool { return !p.busy }, "the second report to load")
	if n := a.panels.Count(); n != 1 {
		t.Errorf("a second leaf opened %d panels, want the one panel re-pointed", n)
	}
	if got := p.report().Title; got != "Tracked Queries" {
		t.Errorf("a leaf left the panel on %q, want it re-pointed at Tracked Queries", got)
	}
}

// TestPlanPaneRecoversFromAPanickingRead. loadPlans latches the pane on
// "Reading plans for query N..." before its goroutine starts, and nothing else
// writes the pane until another query is picked — so a panic that skips the
// completion callback leaves it claiming to read a query it gave up on. This
// drives the safegoRepair step directly; the wiring is the one line in
// loadPlans that names it.
func TestPlanPaneRecoversFromAPanickingRead(t *testing.T) {
	p, _, _ := newQSPanel(t, "Regressed Queries", qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsReportResponse(), qsPlansResponse())
	p.plans = []*gosmo.QSPlan{{PlanID: 7}}
	p.planSeq = 3
	p.plansGrid.SetStatus("Reading plans for query 42...")

	p.plansPanicked(3)

	if p.plans != nil {
		t.Error("the plans of the read that panicked were left in place")
	}
	if got := p.plansGrid.Status(); !strings.Contains(got, "stopped unexpectedly") {
		t.Errorf("plan pane status = %q, want it to say the read stopped", got)
	}
}

// And the guard: a repair from a superseded read must not blank the pane a
// newer one is filling — the same rule readPanicked follows.
func TestAStalePlanPanicLeavesTheCurrentPaneAlone(t *testing.T) {
	p, _, _ := newQSPanel(t, "Regressed Queries", qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsReportResponse(), qsPlansResponse())
	p.plans = []*gosmo.QSPlan{{PlanID: 7}}
	p.planSeq = 4
	p.plansGrid.SetStatus("Query 42 — 1 plans")

	p.plansPanicked(3)

	if p.plans == nil {
		t.Error("a stale panic blanked the plans of the read that superseded it")
	}
	if got := p.plansGrid.Status(); strings.Contains(got, "stopped unexpectedly") {
		t.Errorf("plan pane status = %q, want the newer read's own status", got)
	}
}

// -- the two server-side filters ----------------------------------------------

// TestTheFilterSelectorsReachTheQuery: both are applied by the server, so what
// matters is that the toolbar's choice arrives in the options the read is made
// with — a filter applied to the rows after a read would be a filter the Top
// cap has already thrown the interesting rows away ahead of.
func TestTheFilterSelectorsReachTheQuery(t *testing.T) {
	a := newTestApp()
	p := NewQueryStorePanel(a, nil, "appdb", "Regressed Queries")
	p.minExecIdx = len(qsMinExecCounts) - 1
	p.regressIdx = len(qsRegressionPcts) - 1

	opts := p.options()
	if want := qsMinExecCounts[len(qsMinExecCounts)-1]; opts.MinExecCount != want {
		t.Errorf("MinExecCount = %d, want %d", opts.MinExecCount, want)
	}
	if want := qsRegressionPcts[len(qsRegressionPcts)-1]; opts.MinRegressionPct != want {
		t.Errorf("MinRegressionPct = %v, want %v", opts.MinRegressionPct, want)
	}

	// Unset, neither reaches the query at all: gosmo drops a zero rather than
	// rendering a predicate that admits everything.
	p.minExecIdx, p.regressIdx = 0, 0
	if opts := p.options(); opts.MinExecCount != 0 || opts.MinRegressionPct != 0 {
		t.Errorf("unset filters reached the options as %d / %v", opts.MinExecCount, opts.MinRegressionPct)
	}

	p.refreshToolLabels()
	for i, want := range map[int]string{qsActMinExec: "off", qsActRegression: "off"} {
		if !strings.Contains(p.acts[i].label, want) {
			t.Errorf("filter %d reads %q, want it to say %q", i, p.acts[i].label, want)
		}
	}
	p.minExecIdx, p.regressIdx = 3, 2
	p.refreshToolLabels()
	for i, want := range map[int]string{qsActMinExec: "10", qsActRegression: "≥25%"} {
		if !strings.Contains(p.acts[i].label, want) {
			t.Errorf("filter %d reads %q, want it to name %q", i, p.acts[i].label, want)
		}
	}
}

// TestTheExecutionFloorLandsInTheReportQuery drives the whole read, because
// the panel and gosmo agreeing on a field is not the same as the floor
// reaching the server: the report is loaded and the statement it produced is
// read back.
func TestTheExecutionFloorLandsInTheReportQuery(t *testing.T) {
	p, a, inst := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(),
		qsReportResponse(qsStatRow(11, "dbo.p", "SELECT 1", 2500, 1234, 0, 1)))
	p.minExecIdx = 3 // 10 executions
	p.regressIdx = 2 // ignored by this report

	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

	reads := inst.Reads("FROM   sys.query_store_query ")
	if len(reads) == 0 {
		t.Fatal("no report query reached the server")
	}
	got := reads[len(reads)-1]
	if !strings.Contains(got, "HAVING SUM(rs.count_executions) >=") {
		t.Errorf("the execution floor is not in the report query:\n%s", got)
	}
	// And the threshold this report does not carry is not smuggled in.
	if strings.Contains(got, "b.value") {
		t.Errorf("a one-window report applied the regression threshold:\n%s", got)
	}
}

// TestTheFilterSelectorsAreWithheldWhereTheQueryIgnoresThem. A selector that
// changed a number the next read drops is the silent wrong-thing every menu
// item in this application is gated against.
func TestTheFilterSelectorsAreWithheldWhereTheQueryIgnoresThem(t *testing.T) {
	a := newTestApp()
	for _, title := range queryStoreReportTitles {
		p := NewQueryStorePanel(a, nil, "appdb", title)
		p.SetBounds(0, 0, 120, 40)
		r := p.report()
		for _, c := range []struct {
			cell   int
			filter qsFilters
			name   string
		}{
			{qsActMinExec, qsFilterExecs, "Min execs"},
			{qsActRegression, qsFilterRegression, "Regression"},
		} {
			disabled := p.actDisabled(c.cell)
			if disabled == r.honours(c.filter) {
				t.Errorf("%s on %q: disabled=%v, honours=%v", c.name, title, disabled, r.honours(c.filter))
			}
			if !disabled {
				continue
			}
			// A dimmed cell says why rather than swallowing the click.
			if reason := p.actReason(c.cell); reason == "" {
				t.Errorf("%s on %q is dimmed with no reason", c.name, title)
			}
			p.setStatus("")
			p.runAct(c.cell)
			if p.grid.Status() == "" {
				t.Errorf("clicking the dimmed %s on %q did nothing at all", c.name, title)
			}
		}
	}
}

// TestTheFlagsTableMatchesTheQueryEachReportRuns is the parallel-table check:
// filters is written beside each report by hand, and a report flagged for a
// filter its query has no room for would offer a selector that changes
// nothing. Only the execution floor can be checked this way — the regression
// threshold's two windows exist in one report by construction.
func TestTheFlagsTableMatchesTheQueryEachReportRuns(t *testing.T) {
	tq := useTempTracked(t)
	for _, title := range queryStoreReportTitles {
		p, a, inst := newQSPanel(t, title,
			qsOptionsResponse("READ_WRITE", "READ_WRITE"),
			qsPlansResponse(),
			qsReportResponse(qsStatRow(11, "dbo.p", "SELECT 1", 2500, 1234, 0, 1)),
			fakeResponse{match: "sys.query_store_wait_stats", cols: 3,
				rows: [][]driver.Value{{"CPU", int64(3), float64(12)}}},
			fakeResponse{match: "rsi.start_time,", cols: 4, rows: [][]driver.Value{{
				time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC),
				time.Date(2026, 3, 4, 11, 0, 0, 0, time.UTC), int64(5), float64(20)}}},
		)
		p.minExecIdx = 3 // 10 executions
		p.metric = gosmo.QSMetricCPUTime
		p.topIdx = queryStoreTopIndex(t, 25)
		// Tracked Queries reads nothing at all with an empty set, which would
		// leave this check silently covering six of the seven reports. More
		// than the smallest row cap, so its own Top is raised past 25 and the
		// toolbar's value provably does not reach the query — the reason the
		// Top selector is gated off there.
		for id := int64(101); id < 131; id++ {
			if _, err := tq.Toggle(p.conn.Opts.Server, "appdb", id); err != nil {
				t.Fatalf("Toggle: %v", err)
			}
		}
		p.Load()
		drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

		// The first runtime-stats read, not the last: the plan pane reads the
		// same views straight afterwards, and its query has no floor in it by
		// design.
		var ran string
		if reads := inst.Reads("sys.query_store_runtime_stats"); len(reads) > 0 {
			ran = reads[0]
		}
		if ran == "" {
			t.Fatalf("%q ran no runtime-stats query", title)
		}
		report := queryStoreReportByTitleOrFail(t, title)

		hasFloor := strings.Contains(ran, "HAVING SUM(rs.count_executions) >=")
		if hasFloor != report.honours(qsFilterExecs) {
			t.Errorf("%q: query carries a floor=%v, table says %v:\n%s", title, hasFloor,
				report.honours(qsFilterExecs), ran)
		}

		// The metric selects a runtime-stats column by name, so the column the
		// chosen metric maps to is either in the query or it is not.
		hasMetric := strings.Contains(ran, "cpu_time")
		if hasMetric != report.honours(qsFilterMetric) {
			t.Errorf("%q: query reads the metric's column=%v, table says %v:\n%s", title, hasMetric,
				report.honours(qsFilterMetric), ran)
		}

		// Top is pinned by the value that reaches the server, not by the text:
		// Tracked Queries carries a TOP like the other per-query reports and
		// raises it past whatever the toolbar asked for, so "the query says
		// SELECT TOP" would call a dead selector live.
		args, ok := inst.ReadArgs("sys.query_store_runtime_stats")
		if !ok {
			t.Fatalf("%q: no arguments recorded for the report read", title)
		}
		// The first parameter, not any parameter: every per-query report binds
		// TOP as @p1, and a scan of the whole list would also match a tracked
		// query whose id happened to be the row cap.
		top, isInt := args[0].Value.(int64)
		hasTop := isInt && top == 25
		if hasTop != report.honours(qsFilterTop) {
			t.Errorf("%q: the toolbar's Top reached the query=%v, table says %v\nargs %v:\n%s",
				title, hasTop, report.honours(qsFilterTop), args, ran)
		}
	}
}

// queryStoreTopIndex finds the Top selector entry offering n, so a test naming
// a row cap does not depend on the table's order.
func queryStoreTopIndex(t *testing.T, n int) int {
	t.Helper()
	for i, c := range qsTopCounts {
		if c == n {
			return i
		}
	}
	t.Fatalf("no row cap %d in %v", n, qsTopCounts)
	return 0
}

// TestTheSummaryNamesTheFiltersItApplied, and only those: a floor left set
// from another view must not be claimed by a report whose query drops it,
// which would explain an empty grid with a filter that had nothing to do
// with it.
func TestTheSummaryNamesTheFiltersItApplied(t *testing.T) {
	a := newTestApp()
	p := NewQueryStorePanel(a, nil, "appdb", "Regressed Queries")
	p.minExecIdx, p.regressIdx = 3, 2

	got := p.summary()
	for _, want := range []string{"10 executions", "≥25%"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary = %q, want it to name %q", got, want)
		}
	}

	p.reportIdx = queryStoreReportIndex("Overall Resource Consumption")
	if got := p.summary(); strings.Contains(got, "executions)") || strings.Contains(got, "25%") {
		t.Errorf("summary = %q, want no filter claimed on a report that drops both", got)
	}

	p.reportIdx = queryStoreReportIndex("Top Resource Consuming Queries")
	got = p.summary()
	if !strings.Contains(got, "10 executions") {
		t.Errorf("summary = %q, want the floor this report does apply", got)
	}
	if strings.Contains(got, "25%") {
		t.Errorf("summary = %q, want no regression threshold on a one-window report", got)
	}
}

// -- Tracked Queries ----------------------------------------------------------

// useTempTracked gives one test its own empty tracked-query set. TestMain
// already keeps the whole package off the user's real file; this keeps two
// tests in the package from seeing each other's pins.
func useTempTracked(t *testing.T) *config.TrackedQueries {
	t.Helper()
	tq := config.LoadTrackedQueriesFrom(filepath.Join(t.TempDir(), "tracked_queries.json"))
	config.UseTrackedQueries(tq)
	return tq
}

// TestTrackingAQueryPutsItInTheTrackedView drives the whole path: track the
// *second* query in a report — a panel that ignored the cursor and tracked the
// first row would pass with the cursor left at the top — then read the Tracked
// Queries view and check the id reached the query.
func TestTrackingAQueryPutsItInTheTrackedView(t *testing.T) {
	tq := useTempTracked(t)
	p, a, inst := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(),
		qsReportResponse(
			qsStatRow(11, "dbo.p", "SELECT 1", 2500, 1234, 0, 1),
			qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 0, 2),
		))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

	p.grid.SetSelectedCell(1, 0)
	if got := p.selectedQueryID(); got != 12 {
		t.Fatalf("the cursor is on query %d, want 12", got)
	}
	// The button says what the press will do, before and after.
	p.refreshToolLabels()
	if got := p.acts[qsActTrack].label; got != "Track Query" {
		t.Errorf("the action reads %q over an untracked query", got)
	}
	p.runAct(qsActTrack)
	if !tq.IsTracked(p.conn.Opts.Server, "appdb", 12) {
		t.Fatal("the query was not tracked")
	}
	if tq.IsTracked(p.conn.Opts.Server, "appdb", 11) {
		t.Error("the row above the cursor was tracked instead")
	}
	p.refreshToolLabels()
	if got := p.acts[qsActTrack].label; got != "Untrack Query" {
		t.Errorf("the action still reads %q over a tracked query", got)
	}

	// And the Tracked Queries view now reads with that id.
	p.ShowReport("Tracked Queries")
	drainUntil(t, a, func() bool { return !p.busy }, "the tracked view to load")
	reads := inst.Reads("FROM   sys.query_store_query ")
	if len(reads) == 0 {
		t.Fatal("the tracked view ran no report query")
	}
	if got := reads[len(reads)-1]; !strings.Contains(got, "q.query_id IN (") {
		t.Errorf("the tracked view read the whole database:\n%s", got)
	}

	// Untracking takes it back out, and the view says so rather than going
	// blank.
	p.ShowReport("Top Resource Consuming Queries")
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")
	p.grid.SetSelectedCell(1, 0)
	p.runAct(qsActTrack)
	if tq.IsTracked(p.conn.Opts.Server, "appdb", 12) {
		t.Error("the query is still tracked after a second press")
	}
}

// TestTheTrackedViewSaysWhenNothingIsTracked, rather than showing the empty
// grid a failed report shows.
func TestTheTrackedViewSaysWhenNothingIsTracked(t *testing.T) {
	useTempTracked(t)
	p, a, inst := newQSPanel(t, "Tracked Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(),
		qsReportResponse(qsStatRow(11, "dbo.p", "SELECT 1", 2500, 1234, 0, 1)))
	before := inst.QueryCount()

	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the tracked view to load")

	if len(p.res.rows) == 0 {
		t.Fatal("the tracked view is empty with nothing tracked")
	}
	var text string
	for _, r := range p.res.rows {
		text += strings.Join(r.cells, " ") + "\n"
	}
	if !strings.Contains(text, "Track") {
		t.Errorf("the tracked view does not say how to track a query:\n%s", text)
	}
	// And it did not rank the database behind the user's back: one options
	// read, no report query.
	if got := inst.Reads("FROM   sys.query_store_query "); len(got) != 0 {
		t.Errorf("an empty tracked set still ran a report query:\n%s", got[0])
	}
	if inst.QueryCount() == before {
		t.Error("the tracked view read nothing at all, not even Query Store's state")
	}
}

// TestATrackedQueryThatIsGoneStillHasARow. A query drops out of a report for
// two very different reasons — it did not run in the window, or Query Store no
// longer holds it — and four rows for five tracked queries reads as a bug in
// the report.
func TestATrackedQueryThatIsGoneStillHasARow(t *testing.T) {
	tq := useTempTracked(t)
	p, a, inst := newQSPanel(t, "Tracked Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(),
		qsReportResponse(qsStatRow(11, "dbo.p", "SELECT 1", 2500, 1234, 0, 1)))
	for _, id := range []int64{11, 99} {
		if _, err := tq.Toggle(p.conn.Opts.Server, "appdb", id); err != nil {
			t.Fatalf("Toggle: %v", err)
		}
	}

	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the tracked view to load")

	if len(p.res.rows) != 2 {
		t.Fatalf("got %d rows for two tracked queries", len(p.res.rows))
	}
	gone := p.res.rows[1]
	if gone.cells[0] != "99" {
		t.Errorf("the missing query's row is %v, want it to name query 99", gone.cells)
	}
	if !strings.Contains(gone.cells[len(gone.cells)-1], "Not in Query Store") {
		t.Errorf("the missing query's row does not say why it is empty: %v", gone.cells)
	}
	// Its queryID is set: the row is the only place a query that has left the
	// store still appears, and Untrack Query acts on the selected query id —
	// without one the pin could never be removed.
	if gone.queryID != 99 {
		t.Errorf("the missing query's row carries query id %d, want 99", gone.queryID)
	}
	// The whole tracked set is asked for, not the toolbar's row cap.
	got := inst.Reads("FROM   sys.query_store_query ")
	if len(got) == 0 || !strings.Contains(got[len(got)-1], "q.query_id IN (@") {
		t.Fatalf("the tracked view did not read by id: %v", got)
	}
}

// TestTrackedIDsReachOnlyTheTrackedView. QueryIDs restricts a report to those
// queries, so leaking the set into another view would empty it the moment the
// user pinned anything.
func TestTrackedIDsReachOnlyTheTrackedView(t *testing.T) {
	tq := useTempTracked(t)
	a := newTestApp()
	sc, _ := newFakeConn(t)
	if _, err := tq.Toggle(sc.Opts.Server, "appdb", 12); err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	for _, title := range queryStoreReportTitles {
		p := NewQueryStorePanel(a, sc, "appdb", title)
		ids := p.options().QueryIDs
		if want := queryStoreReportByTitleOrFail(t, title).honours(qsFilterTracked); (len(ids) > 0) != want {
			t.Errorf("%q reads with QueryIDs %v, want tracked-only=%v", title, ids, want)
		}
	}
}

// -- Compare Plans -------------------------------------------------------------

// qsComparePlanXML is one query's plan, differing only in the index it seeks —
// the shape a plan regression takes.
func qsComparePlanXML(index string) string { return comparePlanXML(index, 10, 1) }

// TestComparePlansTakesTwoPressesAndOpensAPanel. The two plans of one query are
// two rows of the same pane, so the first press marks and the second compares —
// and the marked plan must survive the reload the second selection triggers.
func TestComparePlansTakesTwoPressesAndOpensAPanel(t *testing.T) {
	p, a, _ := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(
			qsPlanRow(41, 12, false, qsComparePlanXML("IX_date"), 5, 100),
			qsPlanRow(42, 12, true, qsComparePlanXML("IX_customer"), 7, 90),
		),
		qsReportResponse(qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 42, 2)))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")
	drainUntil(t, a, func() bool { return len(p.plans) == 2 }, "the plans to load")

	before := a.panels.Count()
	p.plansGrid.SetSelectedCell(0, 0)
	p.runAct(qsActCompare)
	if p.cmpPlanID != 41 {
		t.Fatalf("the first press marked plan %d, want 41", p.cmpPlanID)
	}
	if a.panels.Count() != before {
		t.Error("the first press already opened a panel")
	}
	// The button says which press it is on.
	p.refreshToolLabels()
	if got := p.acts[qsActCompare].label; !strings.Contains(got, "41") {
		t.Errorf("the action reads %q, want it to name the marked plan", got)
	}

	p.plansGrid.SetSelectedCell(1, 0)
	p.runAct(qsActCompare)
	if a.panels.Count() != before+1 {
		t.Fatalf("panel count %d after the second press, want %d", a.panels.Count(), before+1)
	}
	cmp, ok := a.panels.PanelAt(a.panels.Count() - 1).(*PlanComparePanel)
	if !ok {
		t.Fatalf("the new panel is %T, want a comparison", a.panels.PanelAt(a.panels.Count()-1))
	}
	if !strings.Contains(cmp.Title(), "41") || !strings.Contains(cmp.Title(), "42") {
		t.Errorf("the comparison is titled %q, want both plan ids", cmp.Title())
	}
	// The two plans really were the two selected: only the index differs, and
	// it differs in the direction the rows were picked.
	seek := gridRowStartingWith(t, cmp.ops, "  Index Seek")
	if !strings.Contains(seek[len(seek)-1], "IX_date → IX_customer") {
		t.Errorf("the comparison shows %q, want the marked plan on the left", seek[len(seek)-1])
	}
	// And the mark is spent: a third press starts a new comparison.
	if p.cmpPlanID != 0 || p.cmpPlan != nil {
		t.Error("the mark survived the comparison it opened")
	}
}

// TestComparePlansUnmarksOnASecondPressOnTheSamePlan, so a mark made by
// accident is undone the way it was made.
func TestComparePlansUnmarksOnASecondPressOnTheSamePlan(t *testing.T) {
	p, a, _ := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(qsPlanRow(41, 12, false, qsComparePlanXML("IX_date"), 5, 100)),
		qsReportResponse(qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 0, 1)))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")
	drainUntil(t, a, func() bool { return len(p.plans) == 1 }, "the plans to load")

	before := a.panels.Count()
	p.runAct(qsActCompare)
	p.runAct(qsActCompare)
	if p.cmpPlanID != 0 {
		t.Errorf("plan %d is still marked after a second press on it", p.cmpPlanID)
	}
	if a.panels.Count() != before {
		t.Error("comparing a plan with itself opened a panel")
	}
	if !strings.Contains(p.grid.Status(), "unmarked") {
		t.Errorf("status = %q, want it to say the mark was dropped", p.grid.Status())
	}
}

// TestComparePlansRefusesAPlanOfAnotherQuery. Two plans of different queries
// have no operators in common to pair, and the result would read as one plan
// replaced wholesale rather than as the mistake it is.
func TestComparePlansRefusesAPlanOfAnotherQuery(t *testing.T) {
	p, a, _ := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(
			qsPlanRow(41, 12, false, qsComparePlanXML("IX_date"), 5, 100),
			qsPlanRow(77, 13, false, qsComparePlanXML("IX_other"), 5, 100),
		),
		qsReportResponse(qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 0, 2)))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")
	drainUntil(t, a, func() bool { return len(p.plans) == 2 }, "the plans to load")

	before := a.panels.Count()
	p.plansGrid.SetSelectedCell(0, 0)
	p.runAct(qsActCompare)
	p.plansGrid.SetSelectedCell(1, 0)
	p.runAct(qsActCompare)

	if a.panels.Count() != before {
		t.Error("two plans of different queries were compared")
	}
	if !strings.Contains(p.grid.Status(), "query 13") {
		t.Errorf("status = %q, want it to name the mismatched query", p.grid.Status())
	}
	if p.cmpPlanID != 0 {
		t.Error("the refused comparison left a mark behind")
	}
}

// TestComparePlansIsWithheldWithoutPlanXML, and says why: Query Store drops a
// plan's XML when its storage fills, and a comparison of nothing against
// nothing is an empty pane with no explanation.
func TestComparePlansIsWithheldWithoutPlanXML(t *testing.T) {
	p, a, _ := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(qsPlanRow(41, 12, false, "", 5, 100)),
		qsReportResponse(qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 0, 1)))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")
	drainUntil(t, a, func() bool { return len(p.plans) == 1 }, "the plans to load")

	if !p.actDisabled(qsActCompare) {
		t.Error("Compare is offered for a plan with no XML")
	}
	if got := p.actReason(qsActCompare); !strings.Contains(got, "no plan XML") {
		t.Errorf("reason = %q, want it to say the XML is missing", got)
	}
	p.runAct(qsActCompare)
	if p.cmpPlanID != 0 {
		t.Error("the dimmed action still marked a plan")
	}
}

// TestATrackedQueryThatIsGoneCanStillBeUntracked. Its row is the only place it
// still appears, so if Untrack cannot act on that row the pin is permanent.
func TestATrackedQueryThatIsGoneCanStillBeUntracked(t *testing.T) {
	tq := useTempTracked(t)
	p, a, _ := newQSPanel(t, "Tracked Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(),
		qsReportResponse())
	if _, err := tq.Toggle(p.conn.Opts.Server, "appdb", 99); err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the tracked view to load")

	p.grid.SetSelectedCell(0, 0)
	p.refreshToolLabels()
	if got := p.acts[qsActTrack].label; got != "Untrack Query" {
		t.Errorf("the action reads %q over the missing query's row", got)
	}
	if p.actDisabled(qsActTrack) {
		t.Fatalf("Untrack is withheld on the missing query's row: %s", p.actReason(qsActTrack))
	}
	p.runAct(qsActTrack)
	if tq.IsTracked(p.conn.Opts.Server, "appdb", 99) {
		t.Error("the missing query is still tracked")
	}
}

// -- Show Value, and cancelling a plan read -----------------------------------

// qsCommentedQuery is a statement whose first line ends in a line comment.
// Flattened onto one line it becomes "SELECT 1 -- pick one FROM dbo.t", which
// is valid SQL that returns something else entirely — the whole FROM clause is
// inside the comment. Every assertion below is about that statement surviving.
const qsCommentedQuery = "SELECT 1 -- pick one\nFROM dbo.t\nWHERE id = 2"

// TestShowValueOpensTheStatementNotTheFlattenedCell. The grid cell is a
// one-line rendering — it has to be, since a raw newline breaks the row — but
// "Show Value" opens a runnable query panel, so handing it the cell shipped a
// batch with most of the query commented out.
func TestShowValueOpensTheStatementNotTheFlattenedCell(t *testing.T) {
	p, a, _ := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(),
		qsReportResponse(qsStatRow(12, "dbo.q", qsCommentedQuery, 900, 40, 0, 1)))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

	// The cell really is flattened: the fix must not have widened it into
	// something the grid would draw across two rows.
	cells := p.grid.Row(0)
	queryCol := p.grid.ColumnIndex(qsQueryColumn)
	if queryCol < 0 {
		t.Fatalf("the report has no %q column: %v", qsQueryColumn, p.res.columns)
	}
	if strings.Contains(cells[queryCol], "\n") {
		t.Errorf("the cell is %q, want it flattened onto one line", cells[queryCol])
	}

	before := a.panels.Count()
	p.grid.SetSelectedCell(0, queryCol)
	if !p.showValue(queryCol, qsQueryColumn, cells[queryCol]) {
		t.Fatal("showValue declined the Query column")
	}
	if a.panels.Count() != before+1 {
		t.Fatalf("panel count %d, want %d — no panel opened", a.panels.Count(), before+1)
	}
	qp, ok := a.panels.PanelAt(a.panels.Count() - 1).(*QueryPanel)
	if !ok {
		t.Fatalf("the new panel is %T, want a query panel", a.panels.PanelAt(a.panels.Count()-1))
	}
	got := qp.editor.Text()
	if got != qsCommentedQuery {
		t.Errorf("the panel holds:\n%q\nwant the statement as Query Store holds it:\n%q",
			got, qsCommentedQuery)
	}
	// The point of all of it: FROM is on its own line, not inside the comment.
	if strings.HasPrefix(strings.SplitN(got, "\n", 2)[0], "SELECT 1 -- pick one FROM") {
		t.Error("the FROM clause is inside the line comment")
	}
}

// TestShowValueFallsBackToTheCellWithoutAStatement covers the rows that have
// no query text to open — a wait category, and a tracked query Query Store no
// longer holds. Handing openValuePanel an empty string would open a blank
// panel over a cell that did have something to show.
func TestShowValueFallsBackToTheCellWithoutAStatement(t *testing.T) {
	p, a, _ := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(),
		qsReportResponse(qsStatRow(12, "dbo.q", "", 900, 40, 0, 1)))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

	queryCol := p.grid.ColumnIndex(qsQueryColumn)
	p.grid.SetSelectedCell(0, queryCol)
	// An empty statement is nothing to show, so the hook declines and the
	// grid's own popup handles it.
	if p.showValue(queryCol, qsQueryColumn, "") {
		t.Error("showValue claimed a row with no statement")
	}
	// A non-Query column is never claimed either.
	before := a.panels.Count()
	if p.showValue(0, "Query ID", "12") {
		t.Error("showValue claimed the Query ID column")
	}
	if a.panels.Count() != before {
		t.Error("a panel was opened for a column that is not the statement")
	}
}

// TestASupersededPlanReadIsCancelled. planSeq already discards the stale
// *result*, but the query itself keeps running on the shared host connection
// until qsReadTimeout — and a plan read fires from the report grid's
// OnSelectRow, so holding Down through a ranking starts one per row.
//
// The plan response is gated so the read is still in flight when it is
// superseded: ungated it finishes, and its own defer cancels the context,
// which would let a panel that never cancelled anything pass.
func TestASupersededPlanReadIsCancelled(t *testing.T) {
	plans := qsPlansResponse(qsPlanRow(41, 12, false, "", 5, 100))
	gate := make(chan struct{})
	plans.block = gate
	defer close(gate)

	p, a, inst := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		plans,
		qsReportResponse(
			qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 0, 1),
			qsStatRow(13, "dbo.r", "SELECT 3", 800, 30, 0, 1)))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

	const planRead = "FROM   sys.query_store_plan AS p"
	drainUntil(t, a, func() bool { _, ok := inst.ReadContext(planRead); return ok },
		"the plan read to reach the server")
	ctx, _ := inst.ReadContext(planRead)
	if err := ctx.Err(); err != nil {
		t.Fatalf("the plan read was cancelled while it was still the current one: %v", err)
	}
	if p.planCancel == nil {
		t.Fatal("no cancel was kept for the in-flight plan read")
	}

	// Moving to another query supersedes it.
	p.loadPlans(13)
	if err := ctx.Err(); err == nil {
		t.Error("the superseded plan read is still running against the server")
	}

	// And closing the panel cancels the one that replaced it, which a panel
	// whose Close only reached the report read would leave running.
	if p.planCancel == nil {
		t.Fatal("the replacement read kept no cancel")
	}
	p.Close()
	if p.planCancel != nil {
		t.Error("Close left a plan cancel behind")
	}
}

// TestClosingThePanelCancelsAnInFlightPlanRead pins Close's half on its own:
// the report read was already cancelled there, and the plan read beside it was
// not.
func TestClosingThePanelCancelsAnInFlightPlanRead(t *testing.T) {
	plans := qsPlansResponse(qsPlanRow(41, 12, false, "", 5, 100))
	gate := make(chan struct{})
	plans.block = gate
	defer close(gate)

	p, a, inst := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		plans,
		qsReportResponse(qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 0, 1)))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

	const planRead = "FROM   sys.query_store_plan AS p"
	drainUntil(t, a, func() bool { _, ok := inst.ReadContext(planRead); return ok },
		"the plan read to reach the server")
	ctx, _ := inst.ReadContext(planRead)

	p.Close()
	if err := ctx.Err(); err == nil {
		t.Error("the plan read outlived the panel that started it")
	}
}

// -- what the status line claims ----------------------------------------------

// qsWaitResponse answers the wait-category report.
func qsWaitResponse(rows ...[]driver.Value) fakeResponse {
	return fakeResponse{match: "sys.query_store_wait_stats", cols: 3, rows: rows}
}

// TestTheSummaryDoesNotClaimTheMetricOnAWaitReport. Query Store records wait
// time and nothing else per category, so the metric selector never reaches
// this report — but the status line was built from p.metric and announced
// "Avg CPU time" over a grid of milliseconds.
func TestTheSummaryDoesNotClaimTheMetricOnAWaitReport(t *testing.T) {
	p, a, _ := newQSPanel(t, "Query Wait Statistics",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(),
		qsWaitResponse([]driver.Value{"CPU", int64(3), float64(12)}))
	p.metric = gosmo.QSMetricCPUTime
	p.stat = gosmo.QSStatTotal
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

	got := p.summary()
	if strings.Contains(got, string(gosmo.QSMetricCPUTime)) {
		t.Errorf("summary = %q, want no claim about the metric — it does not reach this report", got)
	}
	if !strings.Contains(got, "Wait Time") {
		t.Errorf("summary = %q, want it to name the wait time the rows carry", got)
	}
	// And it agrees with the column header the rows were rendered under.
	if p.grid.ColumnIndex(p.res.valueLabel) < 0 {
		t.Errorf("summary names %q, which is not one of the grid's columns %v",
			p.res.valueLabel, p.res.columns)
	}
}

// TestTheSummaryNamesTheSplitWindowOnRegressedQueries. The report compares the
// two halves of the window rather than reading all of it, so naming the
// selector's range claimed twice what the rows cover.
func TestTheSummaryNamesTheSplitWindowOnRegressedQueries(t *testing.T) {
	p, a, _ := newQSPanel(t, "Regressed Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(),
		qsReportResponse(qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 0, 1)))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

	got := p.summary()
	if !strings.Contains(got, "second half against first") {
		t.Errorf("summary = %q, want it to say the window was split", got)
	}

	// Every other report reads the whole window and must not say so.
	p.reportIdx = queryStoreReportIndex("Top Resource Consuming Queries")
	if got := p.summary(); strings.Contains(got, "second half") {
		t.Errorf("summary = %q, want no split claimed on a one-window report", got)
	}
}

// TestTheExplanatoryGridsDoNotCountAsAReport. Query Store switched off, and
// nothing tracked yet, both answer with two rows of prose — which the status
// line counted as a report's rows and gave a metric and a window.
func TestTheExplanatoryGridsDoNotCountAsAReport(t *testing.T) {
	t.Run("query store off", func(t *testing.T) {
		p, a, _ := newQSPanel(t, "Top Resource Consuming Queries",
			qsOptionsResponse("OFF", "OFF"),
			qsPlansResponse())
		p.Load()
		drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

		got := p.summary()
		if strings.Contains(got, "2 rows") {
			t.Errorf("summary = %q, want the explanation not counted as rows", got)
		}
		if strings.Contains(got, "Duration") || strings.Contains(got, "over the last") {
			t.Errorf("summary = %q, want no metric or window for a query that never ran", got)
		}
		if !strings.Contains(got, "OFF") {
			t.Errorf("summary = %q, want it to say Query Store is off", got)
		}
	})

	t.Run("nothing tracked", func(t *testing.T) {
		useTempTracked(t)
		p, a, _ := newQSPanel(t, "Tracked Queries",
			qsOptionsResponse("READ_WRITE", "READ_WRITE"),
			qsPlansResponse())
		p.Load()
		drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

		got := p.summary()
		if strings.Contains(got, "2 rows") || strings.Contains(got, "over the last") {
			t.Errorf("summary = %q, want no report claimed before anything is tracked", got)
		}
	})
}

// TestEveryReportsSummaryNamesItsValueColumn. valueLabel is set by each loader
// beside the column header it built, so a new report that forgets it would
// leave the status line reading "25 rows,  over the last 24 h" — and the panel
// has no way to fill it in, since the metric is not the authority.
func TestEveryReportsSummaryNamesItsValueColumn(t *testing.T) {
	tq := useTempTracked(t)
	for _, title := range queryStoreReportTitles {
		p, a, _ := newQSPanel(t, title,
			qsOptionsResponse("READ_WRITE", "READ_WRITE"),
			qsPlansResponse(),
			// Before the per-query answer: responses match by substring in
			// order, and every report's query contains the per-query FROM —
			// so the interval report would be handed twelve-column rows and
			// come back empty, leaving this check covering six of the seven.
			fakeResponse{match: "rsi.start_time,", cols: 4, rows: [][]driver.Value{{
				time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC),
				time.Date(2026, 3, 4, 11, 0, 0, 0, time.UTC), int64(5), float64(20)}}},
			qsWaitResponse([]driver.Value{"CPU", int64(3), float64(12)}),
			qsReportResponse(qsStatRow(11, "dbo.p", "SELECT 1", 2500, 1234, 0, 1)),
		)
		// Tracked Queries reports on an empty set with prose, which would leave
		// this check silently covering six of the seven.
		if _, err := tq.Toggle(p.conn.Opts.Server, "appdb", 11); err != nil {
			t.Fatalf("Toggle: %v", err)
		}
		p.Load()
		drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

		if len(p.res.rows) == 0 {
			t.Fatalf("%q loaded no rows, so the summary it produces proves nothing", title)
		}
		if p.res.valueLabel == "" {
			t.Errorf("%q set no valueLabel; summary reads %q", title, p.summary())
			continue
		}
		// The label is a column the grid really has, so the status line and the
		// header cannot drift apart.
		if p.grid.ColumnIndex(p.res.valueLabel) < 0 {
			t.Errorf("%q: valueLabel %q is not one of its columns %v",
				title, p.res.valueLabel, p.res.columns)
		}
	}
}

// TestThePlanPaneReadsTheWindowTheRowsCover. On Regressed Queries the report
// covers the second half of the toolbar's window; the plan pane read the whole
// of it, so one plan reported more executions than the query above it had
// altogether.
//
// Asserted on the times that reach the server, which is the only place the two
// windows meet — both are derived from time.Now(), so they are close but never
// equal, and the bug was a twelve-hour gap.
func TestThePlanPaneReadsTheWindowTheRowsCover(t *testing.T) {
	p, a, inst := newQSPanel(t, "Regressed Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(qsPlanRow(41, 12, false, "", 5, 100)),
		qsReportResponse(qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 0, 1)))
	p.windowIdx = queryStoreWindowIndex(t, "24 h")
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")
	drainUntil(t, a, func() bool { return len(p.plans) == 1 }, "the plans to load")

	// The regressed query binds top, then the recent window, then the baseline
	// window — see gosmo's windowCTE, rendered recent-first for exactly this.
	report, ok := inst.ReadArgs("WITH recent AS")
	if !ok || len(report) < 5 {
		t.Fatalf("the report bound %v, want top plus two windows", report)
	}
	recentFrom, _ := report[1].Value.(time.Time)
	baselineFrom, _ := report[3].Value.(time.Time)

	// The plans query binds From, To, then the query id.
	plans, ok := inst.ReadArgs("FROM   sys.query_store_plan AS p")
	if !ok || len(plans) < 3 {
		t.Fatalf("the plan read bound %v, want a window and a query id", plans)
	}
	planFrom, _ := plans[0].Value.(time.Time)

	if planFrom.Sub(recentFrom).Abs() > 2*time.Second {
		t.Errorf("the plan pane read from %v; the rows above it start at %v",
			planFrom, recentFrom)
	}
	// The shape of the old bug: the plan pane starting where the baseline did.
	if planFrom.Sub(baselineFrom).Abs() < time.Hour {
		t.Errorf("the plan pane read the whole window from %v, not the half the rows cover (%v)",
			planFrom, recentFrom)
	}
}

// queryStoreWindowIndex finds the Window selector entry labelled label, so a
// test naming a range does not depend on the table's order.
func queryStoreWindowIndex(t *testing.T, label string) int {
	t.Helper()
	for i, w := range qsWindows {
		if w.label == label {
			return i
		}
	}
	t.Fatalf("no window labelled %q in %v", label, qsWindows)
	return 0
}

// -- the selector row's gating ------------------------------------------------

// TestTheSelectorsAreWithheldWhereTheQueryIgnoresThem, and say why. A selector
// that changes a number the next read drops is the silent wrong-thing the
// context-gating rule exists to prevent — the two filters on the action row
// were already gated, and Metric and Top were not.
func TestTheSelectorsAreWithheldWhereTheQueryIgnoresThem(t *testing.T) {
	for _, tc := range []struct {
		report   string
		cell     int
		disabled bool
		reason   string
	}{
		{"Query Wait Statistics", qsToolMetric, true, "wait time"},
		{"Query Wait Statistics", qsToolTop, false, ""},
		{"Overall Resource Consumption", qsToolTop, true, "every interval"},
		{"Overall Resource Consumption", qsToolMetric, false, ""},
		{"Tracked Queries", qsToolTop, true, "every query you pinned"},
		{"Tracked Queries", qsToolMetric, false, ""},
		{"Top Resource Consuming Queries", qsToolTop, false, ""},
		{"Top Resource Consuming Queries", qsToolMetric, false, ""},
		// The Statistic and Window selectors reach every report.
		{"Query Wait Statistics", qsToolStatistic, false, ""},
		{"Overall Resource Consumption", qsToolWindow, false, ""},
	} {
		a := newTestApp()
		p := NewQueryStorePanel(a, nil, "appdb", tc.report)
		if got := p.selDisabled(tc.cell); got != tc.disabled {
			t.Errorf("%s cell %d disabled=%v, want %v", tc.report, tc.cell, got, tc.disabled)
			continue
		}
		if !tc.disabled {
			continue
		}
		// A dimmed cell must explain itself; a click that only greys out is
		// the same dead press with extra steps.
		reason := p.selReason(tc.cell)
		if !strings.Contains(reason, tc.reason) {
			t.Errorf("%s cell %d says %q, want it to mention %q", tc.report, tc.cell, reason, tc.reason)
		}
		// And pressing it says so rather than silently doing nothing.
		p.setStatus("")
		p.runSel(tc.cell)
		if p.grid.Status() != reason {
			t.Errorf("%s cell %d pressed: status %q, want %q", tc.report, tc.cell, p.grid.Status(), reason)
		}
	}
}

// TestAWithheldSelectorStillOpensWhereItApplies guards the gate from the other
// side: a dimmed cell that was dimmed everywhere would pass every check above.
func TestAWithheldSelectorStillOpensWhereItApplies(t *testing.T) {
	a := newTestApp()
	p := NewQueryStorePanel(a, nil, "appdb", "Top Resource Consuming Queries")
	for _, cell := range []int{qsToolMetric, qsToolTop, qsToolStatistic, qsToolWindow, qsToolReport} {
		p.runSel(cell)
		if !a.contextMenu.Visible() {
			t.Errorf("selector cell %d opened no menu on a report that honours it", cell)
		}
		a.contextMenu.Hide()
	}
}

// TestTheExecutionFloorDoesNotReachTheTrackedView. Tracked Queries reads the
// same gosmo query the rankings do, so a floor left set on another view was
// carried into it and dropped a pinned query — which then appeared as "Not in
// Query Store for this window", an explanation that had nothing to do with why
// it went.
func TestTheExecutionFloorDoesNotReachTheTrackedView(t *testing.T) {
	tq := useTempTracked(t)
	p, a, inst := newQSPanel(t, "Tracked Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(),
		qsReportResponse(qsStatRow(11, "dbo.p", "SELECT 1", 2500, 1234, 0, 1)))
	if _, err := tq.Toggle(p.conn.Opts.Server, "appdb", 11); err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	p.minExecIdx = len(qsMinExecCounts) - 1 // the highest floor on offer
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

	reads := inst.Reads("sys.query_store_runtime_stats")
	if len(reads) == 0 {
		t.Fatal("the tracked view ran no query")
	}
	if strings.Contains(reads[0], "HAVING SUM(rs.count_executions) >=") {
		t.Errorf("a floor set on another view reached the tracked query:\n%s", reads[0])
	}
	// And the summary does not claim a filter it did not apply.
	if strings.Contains(p.summary(), "executions") {
		t.Errorf("summary = %q, want no floor claimed on the tracked view", p.summary())
	}
}

// TestTheTrackedViewIsNeverCappedBelowThePinnedSet is why the Top selector is
// gated off there: whatever the toolbar asks for, the query is raised to fit
// every pinned query, so the selector cannot change the answer.
func TestTheTrackedViewIsNeverCappedBelowThePinnedSet(t *testing.T) {
	tq := useTempTracked(t)
	p, a, inst := newQSPanel(t, "Tracked Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(),
		qsReportResponse(qsStatRow(101, "dbo.p", "SELECT 1", 2500, 1234, 0, 1)))
	const pinned = 12 // more than the smallest row cap on offer
	for id := int64(101); id < 101+pinned; id++ {
		if _, err := tq.Toggle(p.conn.Opts.Server, "appdb", id); err != nil {
			t.Fatalf("Toggle: %v", err)
		}
	}
	p.topIdx = 0 // the smallest cap
	if qsTopCounts[0] >= pinned {
		t.Fatalf("the smallest cap %d is not below the %d pinned, so this proves nothing",
			qsTopCounts[0], pinned)
	}
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

	args, ok := inst.ReadArgs("sys.query_store_runtime_stats")
	if !ok {
		t.Fatal("no arguments recorded for the tracked read")
	}
	top, _ := args[0].Value.(int64)
	if top < pinned {
		t.Errorf("the query capped at %d rows with %d queries pinned — one would be dropped", top, pinned)
	}
}

// -- the statistic a report opens on ------------------------------------------

// TestAReportLeafOpensOnItsOwnStatistic, whether or not a panel is already
// open. defaultStat applied at construction only, so opening the Top Resource
// leaf gave Total on a new panel and whatever the previous view left behind on
// one already open — the same click, two different numbers.
func TestAReportLeafOpensOnItsOwnStatistic(t *testing.T) {
	fresh := NewQueryStorePanel(newTestApp(), nil, "appdb", "Top Resource Consuming Queries")
	if fresh.stat != gosmo.QSStatTotal {
		t.Fatalf("a new panel opened on %q, want the report's own default Total", fresh.stat)
	}

	// The same leaf reached through an already-open panel sitting on a report
	// whose default is different.
	open := NewQueryStorePanel(newTestApp(), nil, "appdb", "Regressed Queries")
	if open.stat != gosmo.QSStatAvg {
		t.Fatalf("Regressed Queries opened on %q, want Avg", open.stat)
	}
	open.ShowReport("Top Resource Consuming Queries")
	if open.stat != fresh.stat {
		t.Errorf("the open panel shows %q where a new one shows %q — the same leaf, two answers",
			open.stat, fresh.stat)
	}
}

// TestAChosenStatisticSurvivesAReportChange: following the report's default is
// only for a statistic the user has not picked. One they did pick is theirs —
// the metric behaves that way unconditionally, and a statistic that snapped
// back on every report change would be the opposite annoyance.
func TestAChosenStatisticSurvivesAReportChange(t *testing.T) {
	a := newTestApp()
	p := NewQueryStorePanel(a, nil, "appdb", "Regressed Queries")

	// Pick Max through the menu, the way the toolbar does.
	p.runSel(qsToolStatistic)
	chooseMenuItem(t, a, string(gosmo.QSStatMax))
	if p.stat != gosmo.QSStatMax {
		t.Fatalf("the menu set %q, want Max", p.stat)
	}

	p.ShowReport("Top Resource Consuming Queries")
	if p.stat != gosmo.QSStatMax {
		t.Errorf("the chosen statistic became %q on a report change, want it kept", p.stat)
	}
}

// TestThePanelOpensOnGosmosOwnRowCap pins qsDefaultTopIdx the way
// TestThePanelOpensOnTheWindowTheDetailBrowserRead pins the window: the
// constant is an index, and reordering qsTopCounts would silently change what
// the panel opens on while its doc comment still claimed gosmo's default.
func TestThePanelOpensOnGosmosOwnRowCap(t *testing.T) {
	if qsDefaultTopIdx < 0 || qsDefaultTopIdx >= len(qsTopCounts) {
		t.Fatalf("qsDefaultTopIdx = %d, outside the %d caps offered", qsDefaultTopIdx, len(qsTopCounts))
	}
	if got := qsTopCounts[qsDefaultTopIdx]; got != gosmo.QSDefaultTop {
		t.Errorf("the panel opens on Top %d, want gosmo's own QSDefaultTop %d", got, gosmo.QSDefaultTop)
	}
	p := NewQueryStorePanel(newTestApp(), nil, "appdb", "Top Resource Consuming Queries")
	if got := p.options().Top; got != gosmo.QSDefaultTop {
		t.Errorf("a new panel asks for Top %d, want %d", got, gosmo.QSDefaultTop)
	}
}

// chooseMenuItem runs the open context menu's entry whose label contains want.
// The entry in force is bulleted, so matching is on a substring.
func chooseMenuItem(t *testing.T, a *App, want string) {
	t.Helper()
	if !a.contextMenu.Visible() {
		t.Fatalf("no menu is open to choose %q from", want)
	}
	for _, it := range a.contextMenu.Items() {
		if strings.Contains(it.Label, want) {
			a.contextMenu.Hide()
			it.Action()
			return
		}
	}
	t.Fatalf("no menu entry matching %q", want)
}

// -- the toolbar's overflow ---------------------------------------------------

// TestNoToolbarButtonIsUnreachableAtAnyWidth. A button that does not fit its
// row gets a zero rect, which is neither painted nor hit-tested — and none of
// these actions has a key binding, so Track Query and Compare Plans simply
// could not be invoked below a 170-column terminal. Every button must now be
// drawn or be in its row's More menu.
func TestNoToolbarButtonIsUnreachableAtAnyWidth(t *testing.T) {
	a := newTestApp()
	for panelW := 20; panelW <= 160; panelW += 4 {
		p := NewQueryStorePanel(a, nil, "appdb", "Regressed Queries")
		p.SetBounds(0, 0, panelW, 40)
		p.refreshToolLabels()
		p.layoutToolRows()

		for _, row := range []struct {
			name   string
			tools  []toolButton
			hidden []int
			more   toolButton
		}{
			{"selector", p.sel, p.hiddenSel, p.selMore},
			{"action", p.acts, p.hiddenActs, p.actMore},
		} {
			inMenu := map[int]bool{}
			for _, i := range row.hidden {
				inMenu[i] = true
			}
			for i, tb := range row.tools {
				if tb.rect.IsZero() == inMenu[i] {
					continue // drawn, or reachable through the menu
				}
				t.Fatalf("width %d: %s cell %d (%q) is neither drawn nor in the More menu",
					panelW, row.name, i, tb.label)
			}
			if len(row.hidden) > 0 && row.more.rect.IsZero() && panelW > 12 {
				t.Fatalf("width %d: %s row hides %d buttons with no More cell to reach them",
					panelW, row.name, len(row.hidden))
			}
			// The stand-in must itself be inside the row it stands in.
			if !row.more.rect.IsZero() && row.more.rect.Right() > p.rect.Right() {
				t.Fatalf("width %d: %s row's More cell runs past the pane", panelW, row.name)
			}
		}
	}
}

// TestTheHiddenButtonsAreTheRowsTail: the More menu must hold a suffix, not a
// scattered subset. layoutToolButtons skips a button that does not fit and
// carries on, so a later, shorter one is squeezed in after it — which would
// leave the menu holding the middle of the row and the row itself with a gap
// in the order the user reads it.
//
// Swept across widths rather than checked at one: the squeeze needs a long
// button followed by a shorter one at exactly the wrong boundary, and a single
// width passes whether or not the layout guards against it.
func TestTheHiddenButtonsAreTheRowsTail(t *testing.T) {
	a := newTestApp()
	sawOverflow := false
	for panelW := 20; panelW <= 170; panelW++ {
		p := NewQueryStorePanel(a, nil, "appdb", "Regressed Queries")
		p.SetBounds(0, 0, panelW, 40)
		p.refreshToolLabels()
		p.layoutToolRows()

		for _, row := range []struct {
			name   string
			tools  []toolButton
			hidden []int
		}{
			{"selector", p.sel, p.hiddenSel},
			{"action", p.acts, p.hiddenActs},
		} {
			if len(row.hidden) == 0 {
				continue
			}
			sawOverflow = true
			want := len(row.tools) - len(row.hidden)
			for n, i := range row.hidden {
				if i != want+n {
					t.Fatalf("width %d: hidden %s buttons %v are not the row's tail starting at %d",
						panelW, row.name, row.hidden, want)
				}
			}
		}
	}
	if !sawOverflow {
		t.Fatal("nothing overflowed at any width, so this proves nothing")
	}
}

// TestTheOverflowMenuRunsTheHiddenAction end to end: the button is not drawn,
// the More cell is, and choosing the entry runs the action the button would.
func TestTheOverflowMenuRunsTheHiddenAction(t *testing.T) {
	tq := useTempTracked(t)
	p, a, _ := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(),
		qsReportResponse(qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 0, 1)))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")

	// Narrow enough that Track Query is off the row.
	p.SetBounds(0, 0, 92, 40)
	p.refreshToolLabels()
	p.layoutToolRows()
	if !p.acts[qsActTrack].rect.IsZero() {
		t.Fatal("Track Query still fits at 92 columns, so this proves nothing")
	}
	if p.actMore.rect.IsZero() {
		t.Fatal("no More cell to reach it through")
	}

	// Press the More cell where the mouse would, then choose the entry.
	if !p.handleToolbarPress(p.actMore.rect.X+1, p.actMore.rect.Y) {
		t.Fatal("the More cell did not take the press")
	}
	chooseMenuItem(t, a, "Track Query")

	if !tq.IsTracked(p.conn.Opts.Server, "appdb", 12) {
		t.Error("choosing Track Query from the More menu did not track the query")
	}
}

// TestTheOverflowMenuKeepsTheGateAndSaysWhy. A hidden action must not become
// reachable in a state its button would have refused — and a withheld entry
// still has to explain itself, which MenuItem.Note does precisely while the
// item is disabled.
func TestTheOverflowMenuKeepsTheGateAndSaysWhy(t *testing.T) {
	useTempTracked(t) // the pin set is process-wide; another test's pin flips a label here
	p, a, _ := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(), // no plans, so every plan action is withheld
		qsReportResponse(qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 0, 1)))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")
	p.SetBounds(0, 0, 92, 40)
	p.refreshToolLabels()
	p.layoutToolRows()

	p.handleToolbarPress(p.actMore.rect.X+1, p.actMore.rect.Y)
	var found bool
	for _, it := range a.contextMenu.Items() {
		if !strings.Contains(it.Label, "Compare Plans") {
			continue
		}
		found = true
		if it.Enabled == nil || it.Enabled() {
			t.Error("Compare Plans is offered in the More menu with no plan selected")
		}
		if !strings.Contains(it.Note, "plan pane") {
			t.Errorf("the withheld entry's note is %q, want the reason its button gives", it.Note)
		}
	}
	if !found {
		t.Fatalf("Compare Plans is not in the More menu; it holds %v", a.contextMenu.Items())
	}
}

// TestTheScriptLabelSaysWhichStatement. Script scripts whichever of Force and
// Unforce applies, and the label said neither — the same reason Track Query's
// label follows the cursor.
func TestTheScriptLabelSaysWhichStatement(t *testing.T) {
	p, a, _ := newQSPanel(t, "Top Resource Consuming Queries",
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(
			qsPlanRow(41, 12, false, "<p/>", 5, 100),
			qsPlanRow(42, 12, true, "<p/>", 7, 90)),
		qsReportResponse(qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 42, 2)))
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")
	drainUntil(t, a, func() bool { return len(p.plans) == 2 }, "the plans to load")

	p.plansGrid.SetSelectedCell(0, 0) // plan 41, not forced
	p.refreshToolLabels()
	if got := p.acts[qsActScript].label; got != "Script Force" {
		t.Errorf("over an unforced plan the label is %q, want Script Force", got)
	}
	p.plansGrid.SetSelectedCell(1, 0) // plan 42, forced
	p.refreshToolLabels()
	if got := p.acts[qsActScript].label; got != "Script Unforce" {
		t.Errorf("over a forced plan the label is %q, want Script Unforce", got)
	}
}
