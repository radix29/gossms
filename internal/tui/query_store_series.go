package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/gdamore/tcell/v3"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/charts"
)

// query_store_series.go is the Query Store panel's second chart mode: the
// selected query's per-plan history, one line per plan, interval by interval —
// SSMS plots it under Tracked Queries. The seven reports rank queries; this
// plots the one the report grid's cursor is on, so it belongs to the row the
// way the plan pane and the two plan actions do, and is a mode of the chart
// rather than an eighth report.
//
// The read is gosmo's QueryStoreTrackedQueryContext, with the same options the
// report was run with. Top, the execution floor and the tracked-query set do
// not reach that query at all — it is one query over every interval — so the
// selectors that carry them stay live for the report grid below, which still
// honours them.

// qsSeriesData is one query's per-plan series, already resolved onto a single
// interval axis, plus what a chart needs to label the time scale.
type qsSeriesData struct {
	queryID int64
	series  []charts.Series

	// interval is how much time one bucket covers, taken from the Query Store
	// interval width of a bucket that came back; newest is the end of the most
	// recent bucket, which is the timestamp the chart's right edge carries.
	interval time.Duration
	newest   time.Time
}

// buildQSSeries turns gosmo's per-plan interval rows into one charts.Series
// per plan.
//
// The rows are not dense: a QSPlanIntervalStat exists only for an interval in
// which that plan actually ran, so two plans of one query generally come back
// with different bucket counts and different gaps. HistoryChart indexes its
// series positionally and Series.At reads 0 outside the slice, so appending
// each plan's values in the order they arrived would plot two plans against
// two different time axes — plan B's Tuesday drawn in the column holding plan
// A's Monday, with nothing on screen saying so. One axis is built from the
// union of every plan's StartTime and each series is filled against it, where
// an interval the plan is missing from is a genuine zero: it did not run.
func buildQSSeries(queryID int64, stats []*gosmo.QSPlanIntervalStat, colors []tcell.Color) qsSeriesData {
	out := qsSeriesData{queryID: queryID}
	if len(stats) == 0 || len(colors) == 0 {
		return out
	}

	// The axis: every distinct interval start any plan reported, oldest first,
	// which is the order HistoryChart plots buckets in.
	starts := make([]time.Time, 0, len(stats))
	for _, st := range stats {
		starts = append(starts, st.StartTime)
	}
	slices.SortFunc(starts, func(a, b time.Time) int { return a.Compare(b) })
	axis := make([]time.Time, 0, len(starts))
	at := make(map[int64]int, len(starts))
	for _, t := range starts {
		if _, seen := at[t.UnixNano()]; seen {
			continue
		}
		at[t.UnixNano()] = len(axis)
		axis = append(axis, t)
	}

	// One series per plan, in the order the plans arrived — gosmo orders by
	// plan id, so the legend reads in plan order too.
	byPlan := make(map[int64]int, len(stats))
	for _, st := range stats {
		si, ok := byPlan[st.PlanID]
		if !ok {
			si = len(out.series)
			byPlan[st.PlanID] = si
			id := strconv.FormatInt(st.PlanID, 10)
			out.series = append(out.series, charts.Series{
				Label:  "Plan " + id,
				Short:  "P" + id,
				Color:  colors[si%len(colors)],
				Values: make([]float64, len(axis)),
			})
		}
		// Added rather than assigned: the query groups by plan and interval, so
		// one row per pair is expected, but a second row for a pair must add to
		// the bucket rather than replace it.
		out.series[si].Values[at[st.StartTime.UnixNano()]] += st.Value
		if st.EndTime.After(out.newest) {
			out.newest = st.EndTime
		}
		if d := st.EndTime.Sub(st.StartTime); d > 0 && out.interval == 0 {
			out.interval = d
		}
	}
	if out.newest.IsZero() && len(axis) > 0 {
		out.newest = axis[len(axis)-1]
	}
	return out
}

// empty reports whether there is nothing to plot.
func (d qsSeriesData) empty() bool { return len(d.series) == 0 }

// chart is the plot for this data. Interval and TimeLabel come from the
// buckets themselves, so the time scale describes the intervals Query Store
// actually kept rather than the range the Window selector asked for.
func (d qsSeriesData) chart() charts.HistoryChart {
	label := ""
	if !d.newest.IsZero() {
		label = d.newest.Format("01-02 15:04")
	}
	return charts.HistoryChart{
		Series:    d.series,
		Interval:  d.interval,
		TimeLabel: label,
	}
}

// qsSeriesColors give each plan a line colour, in the order the dashboard's
// own charts use them.
func qsSeriesColors() []tcell.Color {
	cyan, green, yellow, blue, red, purple, neutral := chartColors()
	return []tcell.Color{cyan, green, yellow, blue, red, purple, neutral}
}

// toggleSeriesMode switches the chart between the report's ranking and the
// selected query's history, reading the series the first time it is needed:
// it is a second round trip per row, and the panel already reads plans on
// every cursor move.
func (p *QueryStorePanel) toggleSeriesMode() {
	p.seriesMode = !p.seriesMode
	if !p.seriesMode {
		p.cancelSeries()
		p.series, p.seriesNote = qsSeriesData{}, ""
		return
	}
	p.loadSeries(p.selectedQueryID())
}

// loadSeriesIfShown re-reads the series for a query when the chart is in
// series mode, and does nothing when it is not — every caller that changes
// which query or which window the panel is on goes through here.
func (p *QueryStorePanel) loadSeriesIfShown(queryID int64) {
	if p.seriesMode {
		p.loadSeries(queryID)
	}
}

// cancelSeries aborts the in-flight series read, on close and when a newer one
// supersedes it — same reason cancelPlans exists, and the same sharpness: the
// read fires from the report grid's cursor, so holding Down through a ranking
// starts one per row.
func (p *QueryStorePanel) cancelSeries() {
	if p.seriesCancel != nil {
		p.seriesCancel()
		p.seriesCancel = nil
	}
}

// loadSeries reads one query's per-plan history into the chart, or empties it
// for a zero id. Its own sequence rather than the report's or the plan pane's,
// for the reason there are already two: the three reads are independent, and a
// report reload that has not landed must not blank a series that has.
func (p *QueryStorePanel) loadSeries(queryID int64) {
	p.cancelSeries()
	p.seriesSeq++
	seq := p.seriesSeq
	p.series = qsSeriesData{}
	if queryID == 0 {
		p.seriesNote = "Select a query in the report below to plot its history"
		return
	}
	// Said separately from the line above: a disconnected panel that asked for
	// a selection would send the user clicking at rows that cannot answer.
	if !p.app.isConnected(p.conn) {
		p.seriesNote = "Not connected"
		return
	}
	p.seriesNote = fmt.Sprintf("Reading the history of query %d...", queryID)
	// The report's effective window, not the toolbar's, for the reason the plan
	// pane uses it: on Regressed Queries the rows cover the second half of the
	// window, and a chart over the whole of it would plot intervals the rows
	// beside it do not report on.
	opts, sc, dbName := p.report().effectiveOptions(p.options()), p.conn, p.dbName
	p.seriesLabel = qsValueLabel(opts)
	ctx, cancel := context.WithCancel(sc.Context())
	p.seriesCancel = cancel
	// safegoRepair, not safego: the "Reading..." note is replaced by the
	// callback below, which a panic on the read goroutine never reaches, and
	// nothing else writes it until another query is selected — so the chart
	// would claim to be reading a query it gave up on.
	p.app.safegoRepair("reading a Query Store query history", func() { p.seriesPanicked(seq) }, func() {
		defer cancel()
		readCtx, readCancel := context.WithTimeout(ctx, qsReadTimeout)
		defer readCancel()
		stats, err := sc.Server.Database(dbName).QueryStoreTrackedQueryContext(readCtx, queryID, opts)
		p.app.postAndWake(func() {
			if seq != p.seriesSeq {
				return
			}
			p.seriesCancel = nil
			if err != nil {
				p.series = qsSeriesData{}
				p.seriesNote = fmt.Sprintf("History failed: %v", displayError(err))
				return
			}
			p.series = buildQSSeries(queryID, stats, qsSeriesColors())
			p.seriesNote = ""
			if p.series.empty() {
				p.seriesNote = fmt.Sprintf("Query Store holds no intervals for query %d in this window", queryID)
			}
		})
	})
}

// seriesPanicked replaces the chart's "Reading..." note after a panic on the
// read goroutine — loadSeries' safegoRepair step. Guarded by seriesSeq like
// the normal completion path: a newer read owns the chart, and blanking it
// here would drop a series that is still on its way.
func (p *QueryStorePanel) seriesPanicked(seq int) {
	if seq != p.seriesSeq {
		return
	}
	p.seriesCancel = nil
	p.series = qsSeriesData{}
	p.seriesNote = "Reading the query history stopped unexpectedly — see the log for details"
}

// seriesTitle names what the lines measure, from the options the series was
// read with rather than from the toolbar — the toolbar can have moved on since,
// and a title built from it would relabel a chart nothing re-read.
func (p *QueryStorePanel) seriesTitle() string {
	if p.series.queryID == 0 {
		return "Query history"
	}
	return fmt.Sprintf("Query %d — %s by plan, per interval", p.series.queryID, p.seriesLabel)
}
