package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// query_store_panel_load.go is the report read: the options the toolbar's
// selections come to, the tracked-query set, the load itself and the summary
// line it leaves behind. The panel itself is in query_store_panel.go.

// ShowReport points the panel at one of the seven views and reads it.
// Reopening from another tree node comes through here, so an already-open
// panel switches view instead of a second one being created.
func (p *QueryStorePanel) ShowReport(title string) {
	p.reportIdx = queryStoreReportIndex(title)
	if !p.statChosen {
		p.stat = p.report().defaultStat
	}
	p.Load()
}

// Refresh re-reads the current report (F5 or the toolbar), keeping the user
// where they were. Rewriting the same rows under the cursor is what
// redrawGrid exists for — and after a Force Plan it is the row just acted on
// that the user is looking at.
func (p *QueryStorePanel) Refresh() { p.load(true) }

// options are what the toolbar currently asks for.
func (p *QueryStorePanel) options() gosmo.QueryStoreReportOptions {
	to := time.Now()
	return gosmo.QueryStoreReportOptions{
		Metric:    p.metric,
		Statistic: p.stat,
		From:      to.Add(-qsWindows[p.windowIdx].back),
		To:        to,
		Top:       qsTopCounts[p.topIdx],
		// Gated here, not left to gosmo: MinExecCount is carried by the same
		// gosmo query Tracked Queries reads, so a floor set on another view
		// would drop a query the user had pinned — and the row it left behind
		// says "Not in Query Store for this window", which is not why it went.
		// MinRegressionPct only ever reaches the two-window query, but it is
		// gated the same way so the table stays the single answer.
		MinExecCount:     filterValue(p, qsFilterExecs, qsMinExecCounts[p.minExecIdx]),
		MinRegressionPct: filterValue(p, qsFilterRegression, qsRegressionPcts[p.regressIdx]),
		QueryIDs:         p.trackedIDs(),
	}
}

// filterValue is v where the report on screen honours f, and the zero value —
// which every one of these options reads as "no filter" — where it does not.
// A function rather than a method: Go has no generic methods.
func filterValue[T int64 | float64](p *QueryStorePanel, f qsFilters, v T) T {
	if !p.report().honours(f) {
		return 0
	}
	return v
}

// trackedIDs is the pinned set for this database, empty on every report but
// Tracked Queries — QueryIDs restricts a report to those queries, and pinning
// one would otherwise empty every other view the moment it was tracked.
func (p *QueryStorePanel) trackedIDs() []int64 {
	if !p.report().honours(qsFilterTracked) || p.conn == nil {
		return nil
	}
	return config.Tracked().IDs(p.conn.Opts.Server, p.dbName)
}

// isTracked reports whether one query is pinned, for the action row's label.
func (p *QueryStorePanel) isTracked(queryID int64) bool {
	if queryID == 0 || p.conn == nil {
		return false
	}
	return config.Tracked().IsTracked(p.conn.Opts.Server, p.dbName, queryID)
}

// toggleTracked pins the selected query to the Tracked Queries view or unpins
// it. The set is per server and database and outlives the session — see
// config.TrackedQueries.
func (p *QueryStorePanel) toggleTracked() {
	id := p.selectedQueryID()
	if id == 0 || p.conn == nil {
		return
	}
	tracked, err := config.Tracked().Toggle(p.conn.Opts.Server, p.dbName, id)
	verb := "untracked"
	if tracked {
		verb = "tracked"
	}
	if err != nil {
		// The toggle applied in memory even though the file did not take it,
		// so say both halves: this session behaves as asked, the next does not.
		p.setStatus(fmt.Sprintf("Query %d %s for this session only — %v", id, verb, err))
	} else {
		p.setStatus(fmt.Sprintf("Query %d %s", id, verb))
	}
	// Every view of this set, not just this panel's: the tree's Tracked Queries
	// leaf caches its rows and would go on showing the old set. Run on the
	// failed save too — the in-memory set took the toggle either way, and it is
	// what every view reads.
	p.app.trackedQueriesChanged(p.conn.Opts.Server, p.dbName)
}

// Load runs the current report in the background and applies the result on the
// UI goroutine.
func (p *QueryStorePanel) Load() { p.load(false) }

// load runs the current report. keepView carries the cursor and the scroll
// across the reload; a report, metric, statistic or window change does not,
// because the rows then mean something else and row 4 of the old ranking is
// not row 4 of the new one.
func (p *QueryStorePanel) load(keepView bool) {
	if !p.app.isConnected(p.conn) {
		p.applyResult(qsResult{}, false)
		p.setStatus("Not connected")
		return
	}
	// Begin supersedes and cancels whatever report read is out: this one
	// replaces it. Uncancelled, a superseded read goes on holding a connection
	// on the shared host until qsReadTimeout.
	ctx, seq := p.reportRead.Begin(p.conn.Context())
	p.busy = true
	p.setStatus("Running " + p.report().Title + "...")
	p.refreshToolLabels()

	report, sc, dbName := p.report(), p.conn, p.dbName
	// The window the report's query really reads, which for Regressed Queries
	// is half the one the toolbar names — see queryStoreReport.effectiveOptions.
	opts := report.effectiveOptions(p.options())
	// safegoRepair, not safego: busy is cleared in the callback below, which a
	// panic on the read goroutine never reaches, and both toolbars are gated
	// on it — every selector and Refresh would sit inert until the panel was
	// closed.
	p.app.safegoRepair("running a Query Store report", func() { p.readPanicked(seq) }, func() {
		readCtx, readCancel := context.WithTimeout(ctx, qsReadTimeout)
		defer readCancel()
		d := sc.Server.DatabaseRef(dbName)
		info, infoErr := d.QueryStore(readCtx)
		var res qsResult
		var err error
		switch {
		case infoErr == nil && !queryStoreIsOn(info):
			// A database with Query Store off answers every report with no
			// rows, which reads identically to a database nothing ran in.
			res = qsOffResult(info)
		default:
			res, err = report.load(readCtx, d, opts)
		}
		p.app.postAndWake(func() {
			if !p.reportRead.Done(seq) {
				return
			}
			p.busy = false
			if err != nil {
				p.res = qsResult{}
				p.grid.SetError(displayError(err))
				p.loadPlans(0)
				p.loadSeriesIfShown(0)
				return
			}
			p.applyResult(res, keepView)
		})
	})
}

// qsOffResult is the explanatory grid a database with Query Store off gets,
// in place of seven reports that would each come back empty.
func qsOffResult(info *gosmo.QueryStoreInfo) qsResult {
	res := qsResult{
		columns: propertyValueColumns,
		// Without this the two explanatory rows are counted as a report's rows,
		// and the status line claims a metric and a window for a query that
		// never ran.
		note: "Query Store is " + queryStoreStateText(info) + " — no report was run",
	}
	for _, cells := range queryStoreOffRows(info) {
		res.rows = append(res.rows, qsResultRow{cells: cells})
	}
	return res
}

// readPanicked releases the busy latch after a panic on the read goroutine —
// Load's safegoRepair step. Guarded by seq like the normal completion path: a
// newer Load set busy for itself, and clearing it here would re-enable a
// toolbar whose read is still out.
func (p *QueryStorePanel) readPanicked(seq int) {
	if !p.reportRead.Done(seq) {
		return
	}
	p.busy = false
	p.setStatus("The report stopped unexpectedly — see the log for details")
}

// applyResult puts a finished report on screen and follows it with the plans
// of whatever query the cursor lands on.
//
// keepView picks between redrawGrid, which carries the cursor, the scroll and
// any dragged column width across the reload, and SetData, which resets all
// three — right only where the rows now mean something else. Neither is ever
// called from the grid's own OnSelectRow, which would undo the move that
// fired it.
func (p *QueryStorePanel) applyResult(res qsResult, keepView bool) {
	p.res = res
	if keepView {
		redrawGrid(p.grid, res.columns, res.cells())
	} else {
		p.grid.SetData(res.columns, res.cells())
	}
	p.setStatus(p.summary())
	p.loadPlans(p.selectedQueryID())
	// The window, the metric and the statistic can all have changed with the
	// report, so a plotted series is re-read rather than left describing the
	// options it was read under.
	p.loadSeriesIfShown(p.selectedQueryID())
}

// summary is the status line under the report grid.
func (p *QueryStorePanel) summary() string {
	// A grid whose rows are an explanation rather than a report says so itself:
	// counting them and naming a window would describe a query that never ran.
	if p.res.note != "" {
		return p.res.note
	}
	if len(p.res.rows) == 0 {
		// The filters are named here above all: an empty report reads as "the
		// server was idle" unless it says a floor was applied.
		return fmt.Sprintf("%s — no rows in %s%s", p.report().Title, p.windowSummary(), p.filterSummary())
	}
	// The value label comes from the result, not from p.stat and p.metric: the
	// metric selector does not reach every report — see qsResult.valueLabel.
	return fmt.Sprintf("%s — %d rows, %s over %s%s",
		p.report().Title, len(p.res.rows), p.res.valueLabel, p.windowSummary(), p.filterSummary())
}

// windowSummary names the range the rows on screen actually cover. Not simply
// the Window selector's label: Regressed Queries compares the two halves of
// that range, so "the last 24 h" would name twice what its rows report on.
func (p *QueryStorePanel) windowSummary() string {
	label := qsWindows[p.windowIdx].label
	if p.report().honours(qsFilterRegression) {
		return "the last " + label + ", second half against first"
	}
	return "the last " + label
}

// filterSummary names the filters this report actually applied, or "" when it
// applied none. Gated on the report the same way the selectors are, so a floor
// left set from another view is not claimed by one whose query ignores it.
func (p *QueryStorePanel) filterSummary() string {
	var parts []string
	if p.report().honours(qsFilterExecs) && qsMinExecCounts[p.minExecIdx] > 0 {
		parts = append(parts, "≥"+qsMinExecLabel(p.minExecIdx)+" executions")
	}
	if p.report().honours(qsFilterRegression) && qsRegressionPcts[p.regressIdx] > 0 {
		parts = append(parts, "regression "+qsRegressionLabel(p.regressIdx)+" of baseline")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// selectedQueryID is the query the report grid's cursor is on, 0 for a report
// whose rows are not queries and for an empty grid.
func (p *QueryStorePanel) selectedQueryID() int64 {
	row := p.grid.SelectedRow()
	if row < 0 || row >= len(p.res.rows) {
		return 0
	}
	return p.res.rows[row].queryID
}

// selectedQueryChanged reloads the plan pane when the report grid's cursor
// moves to a different query. Guarded on the id, not the row: an arrow key
// through a wait-category report would otherwise start a plan read per row.
func (p *QueryStorePanel) selectedQueryChanged() {
	if id := p.selectedQueryID(); id != p.queryID {
		p.loadPlans(id)
		p.loadSeriesIfShown(id)
	}
}

// loadPlans reads one query's plans into the plan pane, or empties it for a
// zero id. It has its own sequence rather than sharing the report's: the two
// reads are independent, and the plan pane must not be blanked by a report
// reload that has not landed yet.
func (p *QueryStorePanel) loadPlans(queryID int64) {
	p.queryID = queryID
	if queryID == 0 || !p.app.isConnected(p.conn) {
		// Abandon, not Cancel: the pane is being emptied, so a read already on
		// its way must not fill it back in.
		p.planRead.Abandon()
		p.plans = nil
		resetGrid(p.plansGrid, qsPlanColumns, nil, 0)
		p.plansGrid.SetStatus("No query selected")
		return
	}
	p.plansGrid.SetStatus(fmt.Sprintf("Reading plans for query %d...", queryID))
	// The report's effective window, not the toolbar's: on Regressed Queries
	// the rows cover the second half of it, and a plan pane reading the whole
	// window reported more executions for one plan than the query above it had
	// altogether.
	opts, sc, dbName := p.report().effectiveOptions(p.options()), p.conn, p.dbName
	// A plan read fires from the report grid's OnSelectRow, so holding Down
	// through a ranking starts one per row: without Begin's cancel every
	// superseded query still runs on the shared host, each until qsReadTimeout.
	ctx, seq := p.planRead.Begin(sc.Context())
	// safegoRepair, not safego: the "Reading plans..." placeholder is replaced
	// by the callback below, which a panic on the read goroutine never reaches,
	// and nothing else writes the pane until another query is selected — so the
	// pane would claim to be reading a query it gave up on.
	p.app.safegoRepair("reading Query Store plans", func() { p.plansPanicked(seq) }, func() {
		readCtx, readCancel := context.WithTimeout(ctx, qsReadTimeout)
		defer readCancel()
		plans, err := sc.Server.DatabaseRef(dbName).QueryStorePlans(readCtx, queryID, opts)
		p.app.postAndWake(func() {
			if !p.planRead.Done(seq) {
				return
			}
			if err != nil {
				p.plans = nil
				p.plansGrid.SetError(displayError(err))
				return
			}
			p.plans = plans
			// resetGrid, not SetData: the plan pane's columns are qsPlanColumns
			// on every load, so a column dragged wider stays meaningful — and
			// SetData drops it every time the report cursor moves to another
			// query. resetGrid rather than redrawGrid because these are a
			// different query's plans, so the old cursor means nothing.
			resetGrid(p.plansGrid, qsPlanColumns, qsPlanRows(plans, opts), 0)
			p.plansGrid.SetStatus(fmt.Sprintf("Query %d — %d plans", queryID, len(plans)))
		})
	})
}

// plansPanicked replaces the plan pane's "Reading plans..." placeholder after a
// panic on the read goroutine — loadPlans' safegoRepair step. Guarded by
// planSeq like the normal completion path: a newer load owns the pane, and
// blanking it here would drop a result that is still on its way.
func (p *QueryStorePanel) plansPanicked(seq int) {
	if !p.planRead.Done(seq) {
		return
	}
	p.plans = nil
	resetGrid(p.plansGrid, qsPlanColumns, nil, 0)
	p.plansGrid.SetStatus("Reading plans stopped unexpectedly — see the log for details")
}

// qsPlanColumns are the plan pane's columns.
var qsPlanColumns = []string{"Plan ID", "Forced", "Forcing Type", "Executions", "Value",
	"Parallel", "Trivial", "Compat", "Last Execution", "Force Failures"}

// qsPlanRows renders one query's plans under qsPlanColumns.
func qsPlanRows(plans []*gosmo.QSPlan, opts gosmo.QueryStoreReportOptions) [][]string {
	rows := make([][]string, 0, len(plans))
	for _, pl := range plans {
		rows = append(rows, []string{
			strconv.FormatInt(pl.PlanID, 10),
			yesNo(pl.IsForced),
			pl.ForcingType,
			core.FormatThousands(pl.ExecCount),
			formatQSValue(opts.Metric, pl.Value),
			yesNo(pl.IsParallelPlan),
			yesNo(pl.IsTrivialPlan),
			strconv.Itoa(pl.CompatibilityLevel),
			dashIfZero(pl.LastExecutionTime),
			core.FormatThousands(pl.ForceFailureCount),
		})
	}
	return rows
}

// selectedPlan is the plan the plan grid's cursor is on, nil when the pane is
// empty. Indexed against plans, which is what the grid was built from.
func (p *QueryStorePanel) selectedPlan() *gosmo.QSPlan {
	row := p.plansGrid.SelectedRow()
	if row < 0 || row >= len(p.plans) {
		return nil
	}
	return p.plans[row]
}

// setStatus writes the panel's one-line state into the report grid's own
// status bar, so it sits with the rows it describes.
func (p *QueryStorePanel) setStatus(s string) { p.grid.SetStatus(s) }
