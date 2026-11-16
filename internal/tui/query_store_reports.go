package tui

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/gdamore/tcell/v3"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/charts"
	"github.com/radix29/tuikit/core"
)

// query_store_reports.go builds the Query Store folder's seven report leaves (the
// views SSMS shows under the same folder) and the rows behind each. The queries
// are gosmo's; see gosmo.Database.QueryStore*Context.
//
// One report layer serves two surfaces: the Detail Browser grid a leaf shows, and
// QueryStorePanel, which plots the same rows with a selectable metric, statistic
// and window. Both go through queryStoreReports.

// queryStoreDetailWindow is how far back a report reads in the Detail Browser.
// SSMS opens on the last hour, but the Detail Browser has no time selector and an
// hour of a development instance is usually empty, which reads as "Query Store is
// broken" rather than "nothing ran". The panel makes this selectable.
const queryStoreDetailWindow = 24 * time.Hour

// qsQueryColumn is the report column holding a query's text; "Show Value" on it
// opens the statement in its own query panel (App.showSQLCellValue).
const qsQueryColumn = "Query"

// qsResultRow is one row of a report: the cells the grid draws, the bar the chart
// plots, and the query it is about.
//
// One row type rather than parallel cell, bar and query-id tables: those could
// fall out of step, and a chart plotting one query's cost under another's label
// (or Force Plan acting on the wrong row) is invisible outside a live run.
type qsResultRow struct {
	cells []string

	// label and value are the bar this row plots as. value is in the metric's own
	// unit, so bars are comparable only within one report.
	label string
	value float64

	// queryID is the query the row is about, or 0 where rows are not queries (Overall
	// Resource Consumption's intervals, Query Wait Statistics' categories). Zero
	// disables the plan pane and both plan actions.
	queryID int64

	// queryText is the statement exactly as Query Store holds it, newlines and all,
	// which "Show Value" on the Query column opens. Kept beside the flattened cell
	// because queryStoreOneLine collapses the statement onto one line, turning a
	// trailing `-- comment` into one that swallows every later line; the cell is a
	// display rendering, never a statement to run. Empty where rows are not queries.
	queryText string

	// drill is what Enter or a double-click on this row opens in the panel: the
	// wait category whose queries it ranks, on a Query Wait Statistics row. nil
	// where a row opens nothing. The Detail Browser grid ignores it.
	drill *qsDrill
}

// qsDrill is one wait category to drill into: the category gosmo filters on and
// what the panel calls it. gosmo.QSWaitAllCategories is "" and reads as no
// category at all, hence the separate label.
type qsDrill struct {
	category gosmo.QSWaitCategory
	label    string
}

// all reports whether this is the every-category drill, which only Avg and Total
// can rank (gosmo.QueryStore.WaitingQueries).
func (d qsDrill) all() bool { return d.category == gosmo.QSWaitAllCategories }

// qsAllWaitCategories is the drill the panel's last category row opens.
var qsAllWaitCategories = qsDrill{category: gosmo.QSWaitAllCategories, label: "All categories"}

// qsStatValidForDrill reports whether statistic s can rank drill d's queries:
// Min, Max and Std dev are per-category measurements, so across categories they
// are undefined and gosmo refuses them.
func qsStatValidForDrill(s gosmo.QSStatistic, d qsDrill) bool {
	return !d.all() || s == gosmo.QSStatAvg || s == gosmo.QSStatTotal
}

// qsResult is one report's output: the grid's columns, rows, and what the
// chart's bars measure.
type qsResult struct {
	columns []string
	rows    []qsResultRow

	// chartLabel names the quantity qsResultRow.value carries, which is not always
	// the value column: the two ranking reports plot the regression and the variation
	// rather than the metric. Set by the loader that filled value, so the axis cannot
	// disagree with the bars.
	chartLabel string

	// valueLabel names what the *value column* carries, for the status line above the
	// rows. Set by the loader from the string it gave the column header; the panel's
	// own metric and statistic are not the authority. Query Wait Statistics proves it:
	// Query Store records only wait time per category, so the metric selector does not
	// reach it, and a status line built from p.metric read "Avg CPU time" over a grid
	// of milliseconds. Empty where rows are not a measurement.
	valueLabel string

	// note replaces the whole status line where the rows are an explanation rather
	// than a report (Query Store off, nothing tracked yet, server too old for wait
	// statistics). Otherwise those grids counted their explanation as rows and claimed
	// a metric and window for a query that never ran.
	note string
}

// reportRowCount is how many rows are the report's own, not counting the
// All categories row the panel appends (withAllCategoriesRow), which is a way in
// rather than a measurement.
func (r qsResult) reportRowCount() int {
	n := 0
	for _, row := range r.rows {
		if row.drill == nil || !row.drill.all() {
			n++
		}
	}
	return n
}

// drillable reports whether any row opens a drill-down.
func (r qsResult) drillable() bool {
	return slices.ContainsFunc(r.rows, func(row qsResultRow) bool { return row.drill != nil })
}

// withAllCategoriesRow appends the row that drills into every wait category at
// once, after a category list that has any. A row rather than a toolbar cell: it
// is reached the same way as each category, and both tool rows are already wider
// than the pane. Not plotted (value 0) and no figures: summing per-category
// averages is not the across-category average, and an execution waiting in two
// categories is counted in both.
func withAllCategoriesRow(res qsResult) qsResult {
	if !res.drillable() {
		return res
	}
	cells := make([]string, len(res.columns))
	for i := range cells {
		cells[i] = "-"
	}
	cells[0] = qsAllWaitCategories.label
	res.rows = append(res.rows, qsResultRow{cells: cells, label: qsAllWaitCategories.label,
		drill: new(qsAllWaitCategories)})
	return res
}

// cells renders the result as the Detail Browser's row table.
func (r qsResult) cells() [][]string {
	out := make([][]string, 0, len(r.rows))
	for _, row := range r.rows {
		out = append(out, row.cells)
	}
	return out
}

// bars renders the result as chart bars, dropping rows with nothing to plot. A
// zero-valued bar is not drawn but still consumes a chart row, so a report with an
// all-zero tail would push its real bars off the top. buf is reused across draws
// (called once per frame, result handed to BarChart.Draw and not kept); nil for a
// fresh slice.
func (r qsResult) bars(buf []charts.Bar, color tcell.Color) []charts.Bar {
	out := buf[:0]
	for _, row := range r.rows {
		if row.value <= 0 {
			continue
		}
		out = append(out, charts.Bar{Label: row.label, Short: row.label, Value: row.value, Color: color})
	}
	return out
}

// qsFilters is the set of toolbar controls one report honours; see
// queryStoreReport.filters.
//
// "Honours" means the control changes what the report returns, not merely that the
// option reaches gosmo: Tracked Queries carries a TOP like every per-query report,
// but its row count is the pinned set's size either way, so the Top selector is
// dead there and gated off.
type qsFilters uint8

const (
	// qsFilterExecs is gosmo's MinExecCount, the HAVING the ranking reports carry.
	// qsFilterRegression is MinRegressionPct, which needs the two windows only
	// Regressed Queries reads.
	qsFilterExecs qsFilters = 1 << iota
	qsFilterRegression
	// qsFilterTracked marks the one report whose rows are the user's pinned queries
	// rather than a ranking; it is read with Options.QueryIDs, which the caller must
	// supply.
	qsFilterTracked
	// qsFilterTop is Options.Top. Overall Resource Consumption ignores it (it was
	// asked for a time range, and dropping intervals from the middle would misdraw the
	// chart) and Tracked Queries cannot be capped below the pinned set's size.
	qsFilterTop
	// qsFilterMetric is Options.Metric. Query Wait Statistics ignores it: Query Store
	// records only wait time per category, so there is no runtime-stats column to
	// select.
	qsFilterMetric
)

// honours reports whether this report's query carries filter f.
func (r queryStoreReport) honours(f qsFilters) bool { return r.filters&f != 0 }

// effectiveOptions is the window this report's query really reads, which may
// differ from the caller's: Regressed Queries compares the two halves of it; see
// queryStoreRegressionOptions.
//
// Applied by the caller exactly once, not in the loader: the plan pane and status
// line need the same range, and three copies of the rule could disagree. Not
// idempotent (a second application splits the recent half again), so the loader
// takes the window as given.
func (r queryStoreReport) effectiveOptions(opts gosmo.QueryStoreReportOptions) gosmo.QueryStoreReportOptions {
	if r.honours(qsFilterRegression) {
		return queryStoreRegressionOptions(opts)
	}
	return opts
}

// queryStoreReport is one of the seven views: its title, the sentence the
// folder's grid describes it with, the statistic it is read with where nothing
// chooses one, and its loader.
//
// One table rather than parallel title, description and dispatch lists: those
// could disagree, and a report under another's title is invisible until someone
// reads the SQL.
type queryStoreReport struct {
	Title       string
	Description string

	// filters are the toolbar controls that change what this report returns. It is
	// a property of the query, not a switch on title: Overall Resource Consumption
	// groups by interval and Query Wait Statistics by wait category, so neither has
	// a per-query execution count to floor.
	//
	// The panel dims every control a report does not honour and says why; a
	// selector the next read ignores is the silent wrong-thing the context-gating
	// rule exists to prevent.
	filters qsFilters

	// defaultStat is the report's default statistic: Total for the three read as
	// accumulated cost (Overall Resource Consumption, Top Resource Consuming Queries,
	// Query Wait Statistics), Avg for the other four (cost per execution). The Detail
	// Browser always uses it; the panel opens on it, then follows the toolbar.
	defaultStat gosmo.QSStatistic

	load func(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error)
}

// queryStoreReports is every Query Store view, in SSMS's folder order.
var queryStoreReports = []queryStoreReport{
	{"Regressed Queries",
		"Queries whose average duration grew across the reported window",
		qsFilterExecs | qsFilterRegression | qsFilterTop | qsFilterMetric,
		gosmo.QSStatAvg, regressedQueriesReport},
	{"Overall Resource Consumption",
		"Total duration and executions per Query Store interval",
		qsFilterMetric, gosmo.QSStatTotal, overallConsumptionReport},
	{"Top Resource Consuming Queries",
		"Queries ranked by total duration",
		qsFilterExecs | qsFilterTop | qsFilterMetric, gosmo.QSStatTotal, topResourceQueriesReport},
	{"Queries With Forced Plans",
		"Queries pinned to one plan, and which plan",
		qsFilterExecs | qsFilterTop | qsFilterMetric, gosmo.QSStatAvg, forcedPlanQueriesReport},
	{"Queries With High Variation",
		"Queries whose duration is least predictable, by coefficient of variation",
		qsFilterExecs | qsFilterTop | qsFilterMetric, gosmo.QSStatAvg, highVariationQueriesReport},
	{"Query Wait Statistics",
		"Wait time by category, for queries Query Store captured",
		qsFilterTop, gosmo.QSStatTotal, queryWaitStatisticsReport},
	// No floor and no cap: this view shows the pinned queries, and both would
	// silently drop one. The Statistic and Window selectors still apply.
	{"Tracked Queries",
		"The queries pinned to this view, most recently executed first",
		qsFilterTracked | qsFilterMetric, gosmo.QSStatAvg, trackedQueriesReport},
}

// queryStoreReportTitles is the leaf label for each report, in folder order.
var queryStoreReportTitles = func() []string {
	out := make([]string, 0, len(queryStoreReports))
	for _, r := range queryStoreReports {
		out = append(out, r.Title)
	}
	return out
}()

// queryStoreReportByTitle finds the report a NodeQueryStoreReport leaf names.
func queryStoreReportByTitle(title string) (queryStoreReport, bool) {
	for _, r := range queryStoreReports {
		if r.Title == title {
			return r, true
		}
	}
	return queryStoreReport{}, false
}

// queryStoreReportIndex is the position of the report title names, or 0 (where a
// panel opened from an unrecognised title lands).
func queryStoreReportIndex(title string) int {
	for i, r := range queryStoreReports {
		if r.Title == title {
			return i
		}
	}
	return 0
}

// queryStoreFolderDetail is the Query Store folder's own grid: what state
// Query Store is in, and what the seven leaves below it show.
func queryStoreFolderDetail(ctx context.Context, sc *db.ServerConn, dbName string) ([]string, [][]string, error) {
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(queryStoreReports)+3)
	if info, err := d.QueryStore().Options(ctx); err == nil {
		rows = append(rows,
			[]string{"State", queryStoreStateText(info)},
			[]string{"Storage used", fmt.Sprintf("%s MB of %s MB",
				core.FormatThousands(info.CurrentStorageMB), core.FormatThousands(info.MaxStorageMB))},
			[]string{"Capture mode", string(info.CaptureMode)},
		)
	}
	for _, r := range queryStoreReports {
		rows = append(rows, []string{r.Title, r.Description})
	}
	return []string{"Report", "Description"}, rows, nil
}

// queryStoreReportDetail dispatches a NodeQueryStoreReport leaf's title to its
// loader, after checking there is anything to report.
func queryStoreReportDetail(ctx context.Context, sc *db.ServerConn, dbName, title string) ([]string, [][]string, error) {
	report, ok := queryStoreReportByTitle(title)
	if !ok {
		return nil, nil, fmt.Errorf("unknown Query Store report %q", title)
	}
	d, err := sc.Server.DatabaseByName(ctx, dbName)
	if err != nil {
		return nil, nil, err
	}
	// Query Store off answers every report with no rows, indistinguishable from a
	// database nothing ran in. Say which.
	if info, err := d.QueryStore().Options(ctx); err == nil && !info.IsReadable() {
		return propertyValueColumns, queryStoreOffRows(info), nil
	}
	to := time.Now()
	res, err := report.load(ctx, d, report.effectiveOptions(gosmo.QueryStoreReportOptions{
		Metric:    gosmo.QSMetricDuration,
		Statistic: report.defaultStat,
		From:      to.Add(-queryStoreDetailWindow),
		To:        to,
		QueryIDs:  trackedIDsFor(report, sc, dbName),
	}))
	if err != nil {
		return nil, nil, err
	}
	return res.columns, res.cells(), nil
}

// qsQueryIDColumn is the report column holding a query's id; the Detail Browser
// addresses a row by it to re-read the statement
// (DetailBrowser.showQueryStoreValue).
const qsQueryIDColumn = "Query ID"

// queryStoreQueryText reads one query's statement as Query Store holds it. The
// Detail Browser grid carries only the flattened cell, so "Show Value" asks the
// server for the real text rather than open a rendering a `-- comment` has turned
// into a mostly commented-out batch.
func queryStoreQueryText(ctx context.Context, sc *db.ServerConn, dbName string, queryID int64) (string, error) {
	text, _, err := sc.Server.DatabaseRef(dbName).QueryStore().QueryText(ctx, queryID)
	return text, err
}

// trackedIDsFor is the tracked-query set a report reads with, empty for the six
// that rank the whole database. Read from the file-backed set rather than passed
// in, so the Detail Browser grid and the panel (separate connections) show the
// same list.
func trackedIDsFor(report queryStoreReport, sc *db.ServerConn, dbName string) []int64 {
	if !report.honours(qsFilterTracked) || sc == nil {
		return nil
	}
	return config.Tracked().IDs(config.ConnectionAddress(sc.Opts), dbName)
}

// trackedQueriesChanged is what a pin or unpin must run: every view showing that
// database's tracked set on that server is now stale.
//
// The tree's Tracked Queries leaf needs it: its rows come from a per-node cached
// Detail Browser fetch, so the old set stays listed until refresh, which reads as
// the pin not working. Any Query Store panel on the same database is stale too,
// including the one the toggle came from; they are found by server address, not
// connection, because the set is keyed by it and two connections to one instance
// share it.
func (a *App) trackedQueriesChanged(server, dbName string) {
	a.detailBrowser.InvalidateWhere(a, func(n *explorerNode) bool {
		return isTrackedQueriesLeaf(n, server, dbName)
	})
	for i := range a.panels.Count() {
		qs, ok := a.panels.PanelAt(i).(*QueryStorePanel)
		if !ok || qs.conn == nil || qs.dbName != dbName ||
			!config.SameServer(config.ConnectionAddress(qs.conn.Opts), server) {
			continue
		}
		// Only the view whose rows are the set: the other six read the whole database and
		// a pin does not affect them.
		if qs.report().honours(qsFilterTracked) {
			qs.Refresh()
		}
	}
}

// isTrackedQueriesLeaf reports whether a node is the Tracked Queries leaf for one
// server and database. Keyed on the report's qsFilterTracked flag, not its title,
// so the two cannot disagree about which view reads the pinned set.
func isTrackedQueriesLeaf(n *explorerNode, server, dbName string) bool {
	if n.data.Type != NodeQueryStoreReport || n.data.DBName != dbName {
		return false
	}
	r, ok := queryStoreReportByTitle(n.data.Name)
	if !ok || !r.honours(qsFilterTracked) {
		return false
	}
	sc := resolveConn(n)
	return sc != nil && config.SameServer(config.ConnectionAddress(sc.Opts), server)
}
