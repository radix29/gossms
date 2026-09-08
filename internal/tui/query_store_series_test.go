package tui

import (
	"database/sql/driver"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
)

// qsSeriesResponse answers the per-query time-series read. Matched on the
// GROUP BY that only that query carries: its FROM is the same runtime-stats
// join every report reads, so a response scoped on the FROM would be answered
// by — or would answer — the report beside it, and responses match by
// substring in the order they were scripted.
func qsSeriesResponse(rows ...[]driver.Value) fakeResponse {
	return fakeResponse{match: "GROUP BY p.plan_id, rsi.runtime_stats_interval_id", cols: 5, rows: rows}
}

// qsIntervalRow is one row of the five-column shape QueryStoreTrackedQueryContext
// returns — see gosmo's QSPlanIntervalStat scan.
func qsIntervalRow(planID int64, start time.Time, execs int64, value float64) []driver.Value {
	return []driver.Value{planID, start, start.Add(time.Hour), execs, value}
}

func qsHour(h int) time.Time { return time.Date(2026, 3, 4, h, 0, 0, 0, time.UTC) }

// TestQueryStoreSeriesPutsEveryPlanOnOneTimeAxis is the one place this feature
// can silently lie. Query Store holds a row only for an interval a plan
// actually ran in, so two plans of one query come back with different bucket
// counts and different gaps; HistoryChart indexes its series positionally and
// Series.At reads 0 past the end, so appending each plan's values in arrival
// order plots the two against two different time axes — with nothing on screen
// saying so. Every series must land on the union of the intervals, and an
// interval a plan is missing from must be a zero rather than a shift.
func TestQueryStoreSeriesPutsEveryPlanOnOneTimeAxis(t *testing.T) {
	// Plan 1 ran at 09:00 and 11:00, plan 2 at 10:00 and 11:00 — disjoint
	// except for the last bucket, so a positional append would draw plan 2's
	// 10:00 in plan 1's 09:00 column.
	stats := []*gosmo.QSPlanIntervalStat{
		{PlanID: 1, StartTime: qsHour(9), EndTime: qsHour(10), ExecCount: 3, Value: 10},
		{PlanID: 1, StartTime: qsHour(11), EndTime: qsHour(12), ExecCount: 4, Value: 30},
		{PlanID: 2, StartTime: qsHour(10), EndTime: qsHour(11), ExecCount: 5, Value: 20},
		{PlanID: 2, StartTime: qsHour(11), EndTime: qsHour(12), ExecCount: 6, Value: 40},
	}
	d := buildQSSeries(77, stats, []tcell.Color{tcell.ColorRed, tcell.ColorBlue})

	if d.queryID != 77 {
		t.Errorf("queryID = %d, want 77", d.queryID)
	}
	if len(d.series) != 2 {
		t.Fatalf("got %d series, want one per plan", len(d.series))
	}
	if got, want := d.series[0].Label, "Plan 1"; got != want {
		t.Errorf("first series = %q, want %q", got, want)
	}
	if got, want := d.series[1].Label, "Plan 2"; got != want {
		t.Errorf("second series = %q, want %q", got, want)
	}
	// Three buckets — 09:00, 10:00, 11:00 — for both plans.
	want := [][]float64{{10, 0, 30}, {0, 20, 40}}
	for i, w := range want {
		got := d.series[i].Values
		if len(got) != len(w) {
			t.Fatalf("%s has %d buckets, want the %d of the union axis",
				d.series[i].Label, len(got), len(w))
		}
		for j := range w {
			if got[j] != w[j] {
				t.Errorf("%s bucket %d = %g, want %g (values %v)", d.series[i].Label, j, got[j], w[j], got)
			}
		}
	}
	// The time scale is read off the buckets, not off the Window selector.
	if d.interval != time.Hour {
		t.Errorf("interval = %v, want the 1 h the buckets cover", d.interval)
	}
	if !d.newest.Equal(qsHour(12)) {
		t.Errorf("newest = %v, want the end of the last bucket %v", d.newest, qsHour(12))
	}
	if d.chart().TimeLabel == "" {
		t.Error("the chart carries no time label, so the newest bucket is unlabelled")
	}
	// A plan drawn in another's colour is a legend that names the wrong line.
	if d.series[0].Color == d.series[1].Color {
		t.Error("both plans were given the same colour")
	}
}

// TestQueryStoreSeriesOfNothingIsEmpty. A query with no intervals in the
// window must leave the chart empty so the panel draws its note, rather than
// producing a series of no buckets that plots as a blank axis.
func TestQueryStoreSeriesOfNothingIsEmpty(t *testing.T) {
	if d := buildQSSeries(1, nil, qsSeriesColors()); !d.empty() {
		t.Errorf("an empty read produced %d series", len(d.series))
	}
}

// TestPlotHistoryIsWithheldOnARowThatIsNotAQuery. Overall Resource
// Consumption's rows are intervals and Query Wait Statistics' are wait
// categories: neither has a query id to read a history for. A button that
// swallowed the press there is the silent wrong-thing the context-gating rule
// exists to prevent — and once the mode is on it must always be switchable
// off, including from such a row.
func TestPlotHistoryIsWithheldOnARowThatIsNotAQuery(t *testing.T) {
	a := newTestApp()
	p := NewQueryStorePanel(a, nil, "appdb", "Overall Resource Consumption")
	p.SetBounds(0, 0, 120, 40)
	p.applyResult(qsResult{
		columns: []string{"Interval Start", "Executions"},
		rows:    []qsResultRow{{cells: []string{"09:00", "3"}}},
	}, false)

	if !p.actDisabled(qsActPlot) {
		t.Error("Plot History is live on a row with no query")
	}
	if got := p.actReason(qsActPlot); got == "" {
		t.Error("Plot History was refused without saying why")
	}
	// Pressing it changes nothing: the gate the label is dimmed on is the gate
	// the press goes through.
	p.runAct(qsActPlot)
	if p.seriesMode {
		t.Fatal("Plot History turned the mode on from a row with no query")
	}

	p.seriesMode = true
	if p.actDisabled(qsActPlot) {
		t.Errorf("the mode cannot be switched off from this row: %s", p.actReason(qsActPlot))
	}
	p.refreshToolLabels()
	if got := p.acts[qsActPlot].label; got != "Plot Report" {
		t.Errorf("label with the history plotted = %q, want it to offer the report back", got)
	}
}

// TestPlotHistoryReadsTheSelectedQuery drives the whole mode: the toggle reads
// the query the report cursor is on, the series lands on the chart, and moving
// the cursor re-reads it for the new query. A mode that kept the first query's
// series would plot one query's history under another's title.
func TestPlotHistoryReadsTheSelectedQuery(t *testing.T) {
	// The series response is scripted before the report's: the tracked-query
	// read shares the report's FROM, so the report's response would answer it
	// with twelve columns and the read would fail on the scan.
	p, a, inst := newQSPanel(t, "Top Resource Consuming Queries",
		qsSeriesResponse(
			qsIntervalRow(41, qsHour(9), 3, 10),
			qsIntervalRow(41, qsHour(10), 4, 20),
		),
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		qsPlansResponse(qsPlanRow(41, 11, false, "<ShowPlanXML/>", 5, 100)),
		qsReportResponse(
			qsStatRow(11, "dbo.p", "SELECT 1", 2500, 1234, 0, 1),
			qsStatRow(12, "dbo.q", "SELECT 2", 900, 40, 0, 1),
		))

	p.Load()
	drainUntil(t, a, func() bool { return !p.busy }, "the report to load")
	if len(p.res.rows) != 2 {
		t.Fatalf("got %d rows, want 2 (unanswered: %v)", len(p.res.rows), inst.unmatched)
	}
	if p.seriesMode {
		t.Fatal("the panel opened on the history rather than the report chart")
	}

	p.runAct(qsActPlot)
	if !p.seriesMode {
		t.Fatalf("Plot History was refused on the first row: %s", p.actReason(qsActPlot))
	}
	drainUntil(t, a, func() bool { return !p.series.empty() }, "the history to load")
	if got := p.series.queryID; got != 11 {
		t.Errorf("plotted query %d, want the selected row's query 11", got)
	}
	if got := p.series.series[0].Values; len(got) != 2 {
		t.Errorf("plotted %v, want the two intervals the query ran in", got)
	}
	if p.seriesTitle() == "" || p.seriesNote != "" {
		t.Errorf("title %q / note %q, want a titled chart and no note", p.seriesTitle(), p.seriesNote)
	}

	// Down moves to the second query, which must re-read rather than leave the
	// first query's lines on screen.
	if !p.HandleKey(tcell.NewEventKey(tcell.KeyDown, "", tcell.ModNone)) {
		t.Fatal("the report grid did not take Down")
	}
	drainUntil(t, a, func() bool { return p.series.queryID == 12 }, "the history of the second query")
}
