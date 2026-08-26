package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
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
