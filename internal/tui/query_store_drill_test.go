package tui

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
)

// Query Wait Statistics' drill-down: Enter or a double-click on a category row
// lists the queries that waited in it (gosmo's QueryStore.WaitingQueries), and
// Escape goes back.

// qsWaitDrillResponses scripts the category list (CPU, then Lock), the queries
// waiting on Lock, and the queries across every category. Order matters: the
// waiting-queries read contains "sys.query_store_wait_stats" too, so its
// answers come before the category list's.
func qsWaitDrillResponses() []fakeResponse {
	return []fakeResponse{
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(),
		// Only the across-category fold groups wait stats by plan and interval.
		{match: "GROUP BY plan_id, runtime_stats_interval_id, execution_type", cols: 12,
			rows: [][]driver.Value{qsStatRow(9, "dbo.all", "SELECT 9", 700, 5, 0, 1)}},
		// arg pins the category gosmo was asked for: a drill that read another
		// category's queries goes unanswered.
		{match: "AS baseline_exec_count", arg: "Lock", cols: 12,
			rows: [][]driver.Value{qsStatRow(7, "dbo.lock", "UPDATE t SET x = 1", 4200, 3, 0, 1)}},
		qsWaitResponse(
			[]driver.Value{"CPU", int64(10), float64(900)},
			[]driver.Value{"Lock", int64(3), float64(4200)},
		),
	}
}

func newQSWaitPanel(t *testing.T) (*QueryStorePanel, *App, *gFake) {
	t.Helper()
	p, a, inst := newQSPanel(t, "Query Wait Statistics", qsWaitDrillResponses()...)
	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the category list to load")
	return p, a, inst
}

func qsKey(p *QueryStorePanel, k tcell.Key) bool {
	return p.HandleKey(tcell.NewEventKey(k, "", tcell.ModNone))
}

func TestEnterOnAWaitCategoryListsItsQueriesAndEscapeGoesBack(t *testing.T) {
	p, a, _ := newQSWaitPanel(t)
	qsKey(p, tcell.KeyDown) // Lock
	if !qsKey(p, tcell.KeyEnter) {
		t.Fatal("Enter on a category row was not taken")
	}
	drainUntil(t, a, func() bool { return !p.busy }, "the drill-down to load")

	if p.drill == nil || p.drill.category != gosmo.QSWaitLock {
		t.Fatalf("drill = %v, want Lock", p.drill)
	}
	if len(p.res.rows) != 1 || p.res.rows[0].queryID != 7 {
		t.Fatalf("rows = %+v, want query 7", p.res.rows)
	}
	// The rows are queries, so the plan pane, Track and Plot have one to act on.
	if p.selectedQueryID() != 7 {
		t.Errorf("selected query = %d, want 7", p.selectedQueryID())
	}
	// Every surface names the category, and none claims the metric.
	if p.grid.ColumnIndex("Total Lock Wait Time") < 0 {
		t.Errorf("columns = %v, want a Total Lock Wait Time column", p.res.columns)
	}
	if got := p.summary(); !strings.Contains(got, "› Lock") || !strings.Contains(got, "Esc") {
		t.Errorf("summary = %q, want the category and the way back", got)
	}
	p.refreshToolLabels()
	if got := p.sel.Cells[qsToolReport].Label; !strings.Contains(got, "Query Wait Statistics › Lock") {
		t.Errorf("Report cell = %q, want it to name the category", got)
	}
	if p.res.rows[0].cells[2] != "4,200 ms" {
		t.Errorf("value cell = %q, want milliseconds of wait", p.res.rows[0].cells[2])
	}

	if !qsKey(p, tcell.KeyEscape) {
		t.Fatal("Escape in a drill-down was not taken")
	}
	drainUntil(t, a, func() bool { return !p.busy }, "the category list to reload")
	if p.drill != nil {
		t.Fatalf("drill = %v after Escape, want the category list", p.drill)
	}
	if row := p.grid.SelectedRow(); row != 1 {
		t.Errorf("cursor on row %d after going back, want 1 (Lock, the category that was open)", row)
	}
	// On the category list Escape is App's again.
	if qsKey(p, tcell.KeyEscape) {
		t.Error("Escape on the category list was consumed; App never sees it")
	}
}

func TestTheAllCategoriesRowIsAWayInNotAMeasurement(t *testing.T) {
	p, _, _ := newQSWaitPanel(t)
	if n := len(p.res.rows); n != 3 {
		t.Fatalf("got %d rows, want the two categories and All categories", n)
	}
	last := p.res.rows[2]
	if last.drill == nil || !last.drill.all() || last.cells[0] != "All categories" {
		t.Fatalf("last row = %+v, want All categories", last)
	}
	if last.cells[1] != "-" {
		t.Errorf("All categories value = %q, want no figure (category averages do not sum)", last.cells[1])
	}
	if got := p.summary(); !strings.Contains(got, "2 rows") {
		t.Errorf("summary = %q, want the two categories counted, not the way in", got)
	}
	if bars := p.res.bars(nil, 0); len(bars) != 2 {
		t.Errorf("plotted %d bars, want the two categories only", len(bars))
	}
}

func TestTheDetailBrowsersWaitGridHasNoAllCategoriesRow(t *testing.T) {
	sc, _ := newGConn(t, append([]fakeResponse{qsDatabaseResponse()}, qsWaitDrillResponses()...)...)
	_, rows, err := queryStoreReportDetail(t.Context(), sc, "appdb", "Query Wait Statistics")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want the two categories", len(rows))
	}
	for _, r := range rows {
		if r[0] == "All categories" {
			t.Errorf("the Detail Browser grid lists %v, which opens nothing there", r)
		}
	}
}

func TestAllCategoriesRanksOnlyByAvgOrTotal(t *testing.T) {
	p, a, _ := newQSWaitPanel(t)
	p.grid.SetSelectedRow(2)

	// Max is per category: refused with a reason, and no read goes out.
	p.stat = gosmo.QSStatMax
	if !qsKey(p, tcell.KeyEnter) {
		t.Fatal("Enter on All categories was not taken")
	}
	if p.drill != nil || p.busy {
		t.Fatalf("drilled under Max (drill %v, busy %v); gosmo refuses it", p.drill, p.busy)
	}
	if got := p.grid.Status(); !strings.Contains(got, "Avg or Total") {
		t.Errorf("status = %q, want why", got)
	}

	p.stat = gosmo.QSStatTotal
	qsKey(p, tcell.KeyEnter)
	drainUntil(t, a, func() bool { return !p.busy }, "the drill-down to load")
	if len(p.res.rows) != 1 || p.res.rows[0].queryID != 9 {
		t.Fatalf("rows = %+v, want the across-category query 9", p.res.rows)
	}
	if p.grid.ColumnIndex("Total Wait Time") < 0 {
		t.Errorf("columns = %v, want Total Wait Time", p.res.columns)
	}

	// In there, the Statistic menu greys the three it would refuse.
	p.showStatisticMenu()
	for _, it := range a.contextMenu.Items() {
		enabled := it.Enabled == nil || it.Enabled()
		label := strings.TrimPrefix(it.Label, "• ")
		want := label == string(gosmo.QSStatAvg) || label == string(gosmo.QSStatTotal)
		if enabled != want {
			t.Errorf("Statistic %q enabled = %v, want %v", label, enabled, want)
		}
	}
	a.contextMenu.Hide()
}

func TestADoubleClickOnACategoryDrillsIn(t *testing.T) {
	p, a, _ := newQSWaitPanel(t)
	g := p.planSplit.FirstRect()
	x, y := g.X+2, g.Y+2+1 // the Lock row, below the header and its separator
	for range 2 {
		p.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
		p.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	}
	drainUntil(t, a, func() bool { return !p.busy }, "the drill-down to load")
	if p.drill == nil || p.drill.category != gosmo.QSWaitLock {
		t.Fatalf("drill = %v after a double-click on Lock, want Lock", p.drill)
	}
}

func TestPickingAReportLeavesTheDrillDown(t *testing.T) {
	p, a, _ := newQSWaitPanel(t)
	qsKey(p, tcell.KeyEnter) // CPU
	if p.drill == nil {
		t.Fatal("Enter on CPU did not drill in")
	}
	drainUntil(t, a, func() bool { return !p.busy }, "the drill-down to load")
	// The Report menu is the mouse's way back, its own report included.
	p.ShowReport("Query Wait Statistics")
	if p.drill != nil {
		t.Errorf("drill = %v after picking the report again, want the category list", p.drill)
	}
	drainUntil(t, a, func() bool { return !p.busy }, "the category list to load")
}
