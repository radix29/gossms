package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
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
// Applied by the caller exactly once, not inside the loader. The plan pane and the
// status line must also know the range the rows cover, and three copies of the
// rule could disagree. It is not idempotent (a second application splits the
// recent half again), so the loader takes the window as given.
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

	// filters are the toolbar controls that change what this report returns. Listed
	// here rather than the panel switching on title, because the answer is a property
	// of the query: Overall Resource Consumption groups by interval and Query Wait
	// Statistics by wait category, so neither has a per-query execution count to
	// floor.
	//
	// The panel dims every control a report does not honour and says why; a selector
	// that changes a number the next read ignores is the silent wrong-thing the
	// context-gating rule exists to prevent.
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
	if info, err := d.QueryStore(ctx); err == nil {
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
	if info, err := d.QueryStore(ctx); err == nil && !info.IsReadable() {
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
	text, _, err := sc.Server.DatabaseRef(dbName).QueryStoreQueryText(ctx, queryID)
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
// The tree's Tracked Queries leaf needs it because its rows come from a Detail
// Browser fetch cached per node, so the old set stays listed until refresh, which
// reads as the pin not working. Any Query Store panel on the same database is
// stale too, including the one the toggle came from; they are found by server
// address, not connection, because that is what the set is keyed by and two
// connections to one instance share it.
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

// queryStoreOffRows explains a Query Store that is not collecting and where to
// turn it on. Shared by the Detail Browser and the panel.
func queryStoreOffRows(info *gosmo.QueryStoreInfo) [][]string {
	return [][]string{
		{"Query Store", queryStoreStateText(info)},
		{"To enable", "Database Properties > Query Store — set Operation mode to Read write"},
	}
}

// queryStoreStateText renders Query Store's state as the Database Properties page
// does, naming the mismatch when the actual state differs from the desired one: a
// Query Store that hit its storage quota reads READ_ONLY while still desiring
// READ_WRITE, which explains a report that stopped growing.
func queryStoreStateText(info *gosmo.QueryStoreInfo) string {
	if info.ActualState == info.DesiredState || info.DesiredState == "" {
		return string(info.ActualState)
	}
	return string(info.ActualState + " (requested " + info.DesiredState + ")")
}

// -- the seven reports ---------------------------------------------------------

// queryStoreQueryColumns is the column list every per-query report shares, with
// the value column named by the caller.
func queryStoreQueryColumns(value string) []string {
	return []string{"Query ID", "Object", value, "Executions", "Plans", "Forced Plan", "Last Execution", qsQueryColumn}
}

// queryStoreQueryRow renders a QSQueryStat under queryStoreQueryColumns.
func queryStoreQueryRow(s *gosmo.QSQueryStat, value string) []string {
	return []string{
		strconv.FormatInt(s.QueryID, 10),
		s.ObjectName,
		value,
		core.FormatThousands(s.ExecCount),
		strconv.Itoa(s.PlanCount),
		planIDOrDash(s.ForcedPlanID),
		dashIfZero(s.LastExecutionTime),
		queryStoreOneLine(s.QueryText),
	}
}

// queryStatResult renders a per-query report under the shared columns, each row
// plotting the value it was ranked by.
func queryStatResult(stats []*gosmo.QSQueryStat, opts gosmo.QueryStoreReportOptions) qsResult {
	label := qsValueLabel(opts)
	res := qsResult{columns: queryStoreQueryColumns(label), chartLabel: label, valueLabel: label}
	for _, s := range stats {
		res.rows = append(res.rows, qsResultRow{
			cells:     queryStoreQueryRow(s, formatQSValue(opts.Metric, s.Value)),
			label:     qsQueryBarLabel(s),
			value:     s.Value,
			queryID:   s.QueryID,
			queryText: s.QueryText,
		})
	}
	return res
}

func topResourceQueriesReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	stats, err := d.QueryStoreTopResourceQueries(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	return queryStatResult(stats, opts), nil
}

func forcedPlanQueriesReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	stats, err := d.QueryStoreForcedPlanQueries(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	return queryStatResult(stats, opts), nil
}

// trackedQueriesReport reports the pinned queries, and only those. The ids arrive
// in Options.QueryIDs; the caller holds the set because the Detail Browser grid
// and the panel must show the same list and neither owns the other.
func trackedQueriesReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	if len(opts.QueryIDs) == 0 {
		return qsNoTrackedQueriesResult(), nil
	}
	// Top would otherwise cap a set larger than the toolbar's row count and drop
	// tracked queries from the one report that is not a ranking.
	if opts.Top < len(opts.QueryIDs) {
		opts.Top = len(opts.QueryIDs)
	}
	stats, err := d.QueryStoreTopResourceQueries(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	// Most recently executed first: a tracked list shows what happened lately, not
	// cost rank.
	sortByLastExecution(stats)
	res := queryStatResult(stats, opts)
	res.rows = append(res.rows, qsMissingTrackedRows(stats, opts)...)
	return res, nil
}

// qsMissingTrackedRows accounts for every tracked id the report did not return. A
// query drops out because it did not run in the window or because Query Store no
// longer holds it; silently showing four rows for five tracked queries reads as a
// report bug.
func qsMissingTrackedRows(stats []*gosmo.QSQueryStat, opts gosmo.QueryStoreReportOptions) []qsResultRow {
	var rows []qsResultRow
	for _, id := range opts.QueryIDs {
		if slices.ContainsFunc(stats, func(s *gosmo.QSQueryStat) bool { return s.QueryID == id }) {
			continue
		}
		cells := make([]string, len(queryStoreQueryColumns(qsValueLabel(opts))))
		for i := range cells {
			cells[i] = "-"
		}
		cells[0] = strconv.FormatInt(id, 10)
		cells[len(cells)-1] = "Not in Query Store for this window"
		// queryID is set although there is nothing to read for it: Untrack Query acts on
		// it, and without one a query that has left the store stays pinned with no way to
		// unpin it (the row is the only place it still appears). The plan pane answers "0
		// plans", which is true.
		rows = append(rows, qsResultRow{cells: cells, queryID: id})
	}
	return rows
}

// qsNoTrackedQueriesResult is the view before anything is tracked; an empty grid
// there reads as a failed report.
func qsNoTrackedQueriesResult() qsResult {
	return qsResult{
		columns: propertyValueColumns,
		rows: []qsResultRow{
			{cells: []string{"Tracked queries", "None yet"}},
			{cells: []string{"To track one", "Select a query in any report and press Track Query"}},
		},
		note: "Tracked Queries — nothing is tracked yet",
	}
}

// queryStoreRegressionOptions compares the second half of opts' window against
// the first, not the whole window against the one before it.
//
// gosmo's default baseline is the equally long window immediately *before* From,
// right for a caller-chosen range, but here the report would need twice its window
// of Query Store history before showing a row: on a database with two minutes of
// history the pane stays empty, which reads as a broken report. Splitting keeps
// the requirement at the window the other six reports need.
func queryStoreRegressionOptions(opts gosmo.QueryStoreReportOptions) gosmo.QueryStoreReportOptions {
	if opts.To.IsZero() {
		opts.To = time.Now()
	}
	if opts.From.IsZero() {
		opts.From = opts.To.Add(-time.Hour)
	}
	mid := opts.From.Add(opts.To.Sub(opts.From) / 2)
	opts.BaselineFrom, opts.BaselineTo = opts.From, mid
	opts.From = mid
	return opts
}

// regressedQueriesReport reads the window it is given. The two-halves split is
// queryStoreRegressionOptions', applied by the caller through
// queryStoreReport.effectiveOptions; applying it here too would split the recent
// half twice.
func regressedQueriesReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	stats, err := d.QueryStoreRegressedQueries(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	res := qsResult{columns: []string{"Query ID", "Object", qsValueLabel(opts), "Baseline", "Regression",
		"Executions", "Baseline Execs", qsQueryColumn},
		chartLabel: "Regression in " + qsValueLabel(opts), valueLabel: qsValueLabel(opts)}
	for _, s := range stats {
		res.rows = append(res.rows, qsResultRow{
			cells: []string{
				strconv.FormatInt(s.QueryID, 10),
				s.ObjectName,
				formatQSValue(opts.Metric, s.Value),
				formatQSValue(opts.Metric, s.BaselineValue),
				formatQSValue(opts.Metric, s.Regression),
				core.FormatThousands(s.ExecCount),
				core.FormatThousands(s.BaselineExecCount),
				queryStoreOneLine(s.QueryText),
			},
			label: qsQueryBarLabel(s),
			// The regression, not the value: this report ranks by how much a query grew, and
			// plotting absolute cost would put the slowest query atop a chart about change.
			value:     s.Regression,
			queryID:   s.QueryID,
			queryText: s.QueryText,
		})
	}
	return res, nil
}

func highVariationQueriesReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	stats, err := d.QueryStoreHighVariationQueries(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	res := qsResult{columns: []string{"Query ID", "Object", "Variation", qsValueLabel(opts), "Executions",
		"Plans", "Forced Plan", qsQueryColumn},
		chartLabel: "Variation (stdev / avg)", valueLabel: qsValueLabel(opts)}
	for _, s := range stats {
		res.rows = append(res.rows, qsResultRow{
			cells: []string{
				strconv.FormatInt(s.QueryID, 10),
				s.ObjectName,
				fmt.Sprintf("%.2f", s.Variation),
				formatQSValue(opts.Metric, s.Value),
				core.FormatThousands(s.ExecCount),
				strconv.Itoa(s.PlanCount),
				planIDOrDash(s.ForcedPlanID),
				queryStoreOneLine(s.QueryText),
			},
			label:     qsQueryBarLabel(s),
			value:     s.Variation,
			queryID:   s.QueryID,
			queryText: s.QueryText,
		})
	}
	return res, nil
}

func overallConsumptionReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	intervals, err := d.QueryStoreOverallConsumption(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	res := qsResult{columns: []string{"Interval Start", "Interval End", "Executions", qsValueLabel(opts)},
		chartLabel: qsValueLabel(opts), valueLabel: qsValueLabel(opts)}
	for _, iv := range intervals {
		res.rows = append(res.rows, qsResultRow{
			cells: []string{
				formatSQLDate(iv.StartTime),
				formatSQLDate(iv.EndTime),
				core.FormatThousands(iv.ExecCount),
				formatQSValue(opts.Metric, iv.Value),
			},
			label: iv.StartTime.Format("01-02 15:04"),
			value: iv.Value,
		})
	}
	return res, nil
}

func queryWaitStatisticsReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	if !d.QueryStoreWaitStatsSupported() {
		return qsResult{
			columns: propertyValueColumns,
			rows:    []qsResultRow{{cells: []string{"Query wait statistics", "Requires SQL Server 2017 or later"}}},
			note:    "Query wait statistics require SQL Server 2017 or later",
		}, nil
	}
	// The metric selects a runtime-stats column; wait statistics have none, so the
	// value is always wait time. The statistic still applies and is passed through.
	waits, err := d.QueryStoreWaitCategories(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	// The value column, chart axis and status line all name wait time rather than the
	// metric, from one expression, so none can claim the metric selector reached this
	// report.
	waitLabel := string(qsStatistic(opts)) + " Wait Time"
	res := qsResult{columns: []string{"Wait Category", waitLabel, "Executions"},
		chartLabel: waitLabel + " (ms)", valueLabel: waitLabel}
	for _, w := range waits {
		res.rows = append(res.rows, qsResultRow{
			cells: []string{
				w.Category,
				fmt.Sprintf("%s ms", core.FormatThousands(int64(w.Value+0.5))),
				core.FormatThousands(w.ExecCount),
			},
			label: w.Category,
			value: w.Value,
		})
	}
	return res, nil
}

// -- formatting ----------------------------------------------------------------

// qsMetric and qsStatistic resolve what a report is ranked by, with the same
// defaults gosmo's resolve uses, so a column header never disagrees with the query
// that filled it.
func qsMetric(opts gosmo.QueryStoreReportOptions) gosmo.QSMetric {
	if opts.Metric == "" {
		return gosmo.QSMetricDuration
	}
	return opts.Metric
}

func qsStatistic(opts gosmo.QueryStoreReportOptions) gosmo.QSStatistic {
	if opts.Statistic == "" {
		return gosmo.QSStatAvg
	}
	return opts.Statistic
}

// qsValueLabel names the value column: the statistic then the metric, as
// SSMS labels the same axis ("Total Duration", "Avg CPU time").
func qsValueLabel(opts gosmo.QueryStoreReportOptions) string {
	return string(qsStatistic(opts)) + " " + string(qsMetric(opts))
}

// formatQSValue renders a metric's value in the unit gosmo measures it in. Every
// metric shares one float column, so the unit alone says whether 2500 is 2.5 ms
// or 20 MB of reads.
func formatQSValue(m gosmo.QSMetric, v float64) string {
	unit, ok := gosmo.QSMetricUnit(m)
	if !ok {
		return fmt.Sprintf("%.2f", v)
	}
	switch unit {
	case gosmo.QSUnitMicroseconds:
		return fmt.Sprintf("%.2f ms", v/1000)
	case gosmo.QSUnitMilliseconds:
		return fmt.Sprintf("%.2f ms", v)
	case gosmo.QSUnitPages:
		return fmt.Sprintf("%s KB", core.FormatThousands(int64(v*8+0.5)))
	case gosmo.QSUnitBytes:
		return fmt.Sprintf("%s KB", core.FormatThousands(int64(v/1024+0.5)))
	}
	return fmt.Sprintf("%.2f", v)
}

// qsQueryBarLabel labels a query's bar. The chart gutter is a few columns wide,
// so the id fits, and it is how the grid's first column and both plan actions
// address the query.
func qsQueryBarLabel(s *gosmo.QSQueryStat) string {
	return "Q" + strconv.FormatInt(s.QueryID, 10)
}

// planIDOrDash renders a forced plan id, or a dash where none is forced; a 0
// would read as a real plan whose id is zero.
func planIDOrDash(id int64) string {
	if id == 0 {
		return "-"
	}
	return strconv.FormatInt(id, 10)
}

// queryStoreOneLine flattens a query's text onto one grid line. Query Store keeps
// the statement as submitted, newlines and indentation included, and a raw
// newline in a grid cell breaks its row.
//
// The text is not cut short: DataGrid clamps the column width and truncates what
// it draws, so the cell keeps the whole statement for "Show Value".
func queryStoreOneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// sortByLastExecution orders stats most recently executed first.
func sortByLastExecution(stats []*gosmo.QSQueryStat) {
	slices.SortStableFunc(stats, func(a, b *gosmo.QSQueryStat) int {
		return b.LastExecutionTime.Compare(a.LastExecutionTime)
	})
}
