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

// query_store_reports.go builds the Query Store folder's seven report leaves
// (the views SSMS shows under the same folder) and the rows behind each one.
// The queries themselves are gosmo's — see gosmo.Database.QueryStore*Context.
//
// One report layer serves two surfaces: the Detail Browser grid a leaf shows,
// and QueryStorePanel, which plots the same rows and lets the metric, the
// statistic and the window be chosen. Both go through queryStoreReports.

// queryStoreDetailWindow is how far back a report reads in the Detail
// Browser. SSMS's own views open on the last hour, but the Detail Browser has
// no time selector to widen it with, and an hour of a development instance is
// usually empty — which reads as "Query Store is broken" rather than "nothing
// ran". The Query Store panel makes this selectable.
const queryStoreDetailWindow = 24 * time.Hour

// qsQueryColumn is the report column holding a query's text. Named because
// "Show Value" on it opens the statement in its own query panel — see
// App.showSQLCellValue.
const qsQueryColumn = "Query"

// qsResultRow is one row of a report: the cells the grid draws, the bar the
// chart plots it as, and the query it is about.
//
// One row type rather than a cell table beside a bar table beside a query-id
// table: those would be free to fall out of step, and a chart plotting one
// query's cost under another's label — or Force Plan acting on the row above
// the selected one — is invisible in anything but a live run.
type qsResultRow struct {
	cells []string

	// label and value are the bar this row plots as. value is in the metric's
	// own unit, so a chart's bars are comparable only within one report.
	label string
	value float64

	// queryID is the query the row is about, or 0 where the report's rows are
	// not queries (Overall Resource Consumption's intervals, Query Wait
	// Statistics' categories). A zero disables the plan pane and both plan
	// actions, which have nothing to act on.
	queryID int64

	// queryText is the statement exactly as Query Store holds it, newlines and
	// all — what "Show Value" on the Query column opens in a query panel.
	// Kept beside the flattened cell rather than read back out of it:
	// queryStoreOneLine collapses the statement onto one line, which turns a
	// trailing `-- comment` into one that swallows every line after it, so the
	// cell is a display rendering and never a statement to run. Empty where the
	// report's rows are not queries.
	queryText string
}

// qsResult is one report's output: the grid's columns, its rows, and what the
// chart's bars measure.
type qsResult struct {
	columns []string
	rows    []qsResultRow

	// chartLabel names the quantity qsResultRow.value carries, which is not
	// always the value column: the two ranking reports plot the regression and
	// the variation rather than the metric itself. Set by the loader that
	// filled value, so the axis can never disagree with the bars.
	chartLabel string

	// valueLabel names what the *value column* carries, for the status line
	// above the rows. Set by the loader from the same string it gave the column
	// header, for the reason chartLabel is: the panel's own metric and statistic
	// are not the authority. Query Wait Statistics is the case that proves it —
	// Query Store records wait time and nothing else per category, so the metric
	// selector does not reach it, and a status line built from p.metric read
	// "Avg CPU time" over a grid of milliseconds. Empty where the rows are not
	// a measurement.
	valueLabel string

	// note replaces the whole status line where the rows are an explanation
	// rather than a report — Query Store switched off, nothing tracked yet, a
	// server too old for wait statistics. Those grids counted their own
	// explanation as rows and claimed a metric and a window for a query that
	// never ran.
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

// bars renders the result as chart bars, dropping rows with nothing to plot.
// A zero-valued bar is not drawn but still consumes a row of the chart, so a
// report whose tail is all zeroes would push its real bars off the top.
// buf is reused across draws — bars is called once per frame, and the result
// is handed straight to BarChart.Draw and not kept. Pass nil for a fresh slice.
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

// qsFilters is the set of toolbar controls one report honours — see
// queryStoreReport.filters.
//
// "Honours" means the control changes what the report returns, not merely that
// the option reaches gosmo: Tracked Queries carries a TOP like every per-query
// report, but its row count is the size of the pinned set either way, so the
// Top selector is dead there and is gated off.
type qsFilters uint8

const (
	// qsFilterExecs is gosmo's MinExecCount, the HAVING the ranking reports
	// carry. qsFilterRegression is MinRegressionPct, which needs the two
	// windows only Regressed Queries reads.
	qsFilterExecs qsFilters = 1 << iota
	qsFilterRegression
	// qsFilterTracked marks the one report whose rows are the queries the user
	// pinned rather than a ranking — it is read with Options.QueryIDs, and the
	// caller has to supply them.
	qsFilterTracked
	// qsFilterTop is Options.Top. Overall Resource Consumption ignores it
	// outright — it was asked for a time range, and dropping intervals out of
	// the middle of one would misdraw the chart — and Tracked Queries cannot be
	// capped below the size of the pinned set.
	qsFilterTop
	// qsFilterMetric is Options.Metric. Query Wait Statistics is the one report
	// it does not reach: Query Store records wait time per category and nothing
	// else, so there is no runtime-stats column for the metric to select.
	qsFilterMetric
)

// honours reports whether this report's query carries filter f.
func (r queryStoreReport) honours(f qsFilters) bool { return r.filters&f != 0 }

// effectiveOptions is the window this report's query really reads, which is not
// always the one the caller asked for: Regressed Queries compares the two
// halves of it — see queryStoreRegressionOptions.
//
// Applied by the caller, exactly once, rather than inside the loader. The plan
// pane beside the rows and the status line above them both have to know the
// range the rows actually cover, and reproducing the split at each of those
// would be three copies of one rule free to disagree. It is not idempotent —
// a second application splits the recent half again — so a caller running a
// report applies it and the loader takes the window as given.
func (r queryStoreReport) effectiveOptions(opts gosmo.QueryStoreReportOptions) gosmo.QueryStoreReportOptions {
	if r.honours(qsFilterRegression) {
		return queryStoreRegressionOptions(opts)
	}
	return opts
}

// queryStoreReport is one of the seven views: its title, the sentence the
// folder's own grid describes it with, the statistic it is read with where
// nothing chooses one, and the loader behind it.
//
// One table rather than a title list beside a description list beside a
// dispatch switch: those three would be free to disagree, and a report
// showing under another's title is invisible until someone reads the SQL.
type queryStoreReport struct {
	Title       string
	Description string

	// filters are the toolbar controls that change what this report returns.
	// A report is listed here rather than the panel switching on its title,
	// because the answer is a property of the query behind it: Overall
	// Resource Consumption groups by interval and Query Wait Statistics by
	// wait category, so neither has an execution count per query to floor.
	//
	// The panel dims every control a report does not honour and says why —
	// a selector that changes a number the next read ignores is the silent
	// wrong-thing the context-gating rule exists to prevent.
	filters qsFilters

	// defaultStat is what the report means by default — Total for the three
	// read as accumulated cost (Overall Resource Consumption, Top Resource
	// Consuming Queries, Query Wait Statistics), Avg for the other four, which
	// are about cost per execution. The Detail Browser always uses it; the
	// panel opens on it and then follows the toolbar.
	defaultStat gosmo.QSStatistic

	load func(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error)
}

// queryStoreReports is every Query Store view, in the order SSMS lists them
// under the folder.
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
	// No floor and no cap: this view shows the queries you pinned, and both
	// would silently drop one. The Statistic and Window selectors still apply.
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

// queryStoreReportIndex is the position of the report title names, or 0 — the
// report a panel opened from an unrecognised title lands on.
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
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(queryStoreReports)+3)
	if info, err := d.QueryStoreContext(ctx); err == nil {
		rows = append(rows,
			[]string{"State", queryStoreStateText(info)},
			[]string{"Storage used", fmt.Sprintf("%s MB of %s MB",
				core.FormatThousands(info.CurrentStorageMB), core.FormatThousands(info.MaxStorageMB))},
			[]string{"Capture mode", info.CaptureMode},
		)
	}
	for _, r := range queryStoreReports {
		rows = append(rows, []string{r.Title, r.Description})
	}
	return []string{"Report", "Description"}, rows, nil
}

// queryStoreReportDetail dispatches a NodeQueryStoreReport leaf's title to
// its loader, after establishing that there is anything to report at all.
func queryStoreReportDetail(ctx context.Context, sc *db.ServerConn, dbName, title string) ([]string, [][]string, error) {
	report, ok := queryStoreReportByTitle(title)
	if !ok {
		return nil, nil, fmt.Errorf("unknown Query Store report %q", title)
	}
	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		return nil, nil, err
	}
	// A database with Query Store off answers every report with no rows,
	// which is indistinguishable from a database nothing ran in. Say which.
	if info, err := d.QueryStoreContext(ctx); err == nil && !queryStoreIsOn(info) {
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

// qsQueryIDColumn is the report column holding a query's id. Named because the
// Detail Browser addresses a row by it to re-read that query's statement — see
// DetailBrowser.showQueryStoreValue.
const qsQueryIDColumn = "Query ID"

// queryStoreQueryText reads one query's statement as Query Store holds it.
// The Detail Browser's grid carries only the flattened cell, so "Show Value"
// there asks the server for the real text rather than opening a rendering that
// a `-- comment` has turned into a mostly commented-out batch.
func queryStoreQueryText(ctx context.Context, sc *db.ServerConn, dbName string, queryID int64) (string, error) {
	text, _, err := sc.Server.Database(dbName).QueryStoreQueryTextContext(ctx, queryID)
	return text, err
}

// trackedIDsFor is the tracked-query set a report reads with, empty for the six
// that rank the whole database. Read from the file-backed set rather than
// passed in, so the Detail Browser's grid and the panel — which hold different
// connections and never see each other — show the same list.
func trackedIDsFor(report queryStoreReport, sc *db.ServerConn, dbName string) []int64 {
	if !report.honours(qsFilterTracked) || sc == nil {
		return nil
	}
	return config.Tracked().IDs(sc.Opts.Server, dbName)
}

// trackedQueriesChanged is what a pin or unpin has to run: every view showing
// that database's tracked set on that server is now describing a set that no
// longer exists.
//
// The tree's Tracked Queries leaf is the reason it exists: its rows come from a
// Detail Browser fetch cached per node like every other one, so without this it
// goes on listing the old set until the user refreshes it, which reads as the
// pin not having worked. Any Query Store panel on the same database is stale for
// the same reason, including the one the toggle came from; they are found by
// server address rather than by connection, because that is what the set is
// keyed by and two connections to one instance share it.
func (a *App) trackedQueriesChanged(server, dbName string) {
	a.detailBrowser.InvalidateWhere(a, func(n *explorerNode) bool {
		return isTrackedQueriesLeaf(n, server, dbName)
	})
	for i := range a.panels.Count() {
		qs, ok := a.panels.PanelAt(i).(*QueryStorePanel)
		if !ok || qs.conn == nil || qs.dbName != dbName ||
			!config.SameServer(qs.conn.Opts.Server, server) {
			continue
		}
		// Only the view whose rows are the set: the other six reports read
		// the whole database and a pin changes nothing about them.
		if qs.report().honours(qsFilterTracked) {
			qs.Refresh()
		}
	}
}

// isTrackedQueriesLeaf reports whether a node is the Tracked Queries report
// leaf for one server and database. By the report's own qsFilterTracked flag
// rather than by its title, so the two cannot disagree about which view reads
// the pinned set.
func isTrackedQueriesLeaf(n *explorerNode, server, dbName string) bool {
	if n.data.Type != NodeQueryStoreReport || n.data.DBName != dbName {
		return false
	}
	r, ok := queryStoreReportByTitle(n.data.Name)
	if !ok || !r.honours(qsFilterTracked) {
		return false
	}
	sc := resolveConn(n)
	return sc != nil && config.SameServer(sc.Opts.Server, server)
}

// queryStoreOffRows explains a Query Store that is not collecting, and says
// where it is turned on. Shared by the Detail Browser and the panel.
func queryStoreOffRows(info *gosmo.QueryStoreInfo) [][]string {
	return [][]string{
		{"Query Store", queryStoreStateText(info)},
		{"To enable", "Database Properties > Query Store — set Operation mode to Read write"},
	}
}

// queryStoreIsOn reports whether Query Store is collecting or at least
// readable. READ_ONLY still has data worth reporting — it is what a Query
// Store that filled its quota degrades to.
func queryStoreIsOn(info *gosmo.QueryStoreInfo) bool {
	return info.ActualState == "READ_WRITE" || info.ActualState == "READ_ONLY"
}

// queryStoreStateText renders Query Store's state the way the Database
// Properties page does, naming the mismatch when the state it is in is not
// the state it was asked for — a Query Store that hit its storage quota reads
// READ_ONLY while still desiring READ_WRITE, and that gap is the whole
// explanation for a report that stopped growing.
func queryStoreStateText(info *gosmo.QueryStoreInfo) string {
	if info.ActualState == info.DesiredState || info.DesiredState == "" {
		return info.ActualState
	}
	return info.ActualState + " (requested " + info.DesiredState + ")"
}

// -- the seven reports ---------------------------------------------------------

// queryStoreQueryColumns is the column list every per-query report shares,
// with the value column named by the caller.
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

// queryStatResult renders a per-query report under the shared columns, each
// row plotting the value it was ranked by.
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
	stats, err := d.QueryStoreTopResourceQueriesContext(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	return queryStatResult(stats, opts), nil
}

func forcedPlanQueriesReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	stats, err := d.QueryStoreForcedPlanQueriesContext(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	return queryStatResult(stats, opts), nil
}

// trackedQueriesReport reports the queries the user pinned, and only those.
// The ids arrive in Options.QueryIDs — the caller holds the set, because the
// Detail Browser's grid and the panel must show the same list and neither owns
// the other.
func trackedQueriesReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	if len(opts.QueryIDs) == 0 {
		return qsNoTrackedQueriesResult(), nil
	}
	// Top would otherwise cap a set larger than the toolbar's row count and
	// drop tracked queries out of the one report that is not a ranking.
	if opts.Top < len(opts.QueryIDs) {
		opts.Top = len(opts.QueryIDs)
	}
	stats, err := d.QueryStoreTopResourceQueriesContext(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	// Most recently executed first: a tracked list is read to see what has
	// happened lately, not to rank cost — the cost column is right there.
	sortByLastExecution(stats)
	res := queryStatResult(stats, opts)
	res.rows = append(res.rows, qsMissingTrackedRows(stats, opts)...)
	return res, nil
}

// qsMissingTrackedRows accounts for every tracked id the report did not
// return. A query drops out for two very different reasons — it did not run in
// the window, or Query Store no longer holds it at all — and silently showing
// four rows for five tracked queries reads as a bug in the report.
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
		// queryID is set even though there is nothing to read for it: it is
		// what Untrack Query acts on, and a row without one leaves a query
		// that has left the store pinned with no way to unpin it — the row is
		// the only place it still appears. The plan pane answers "0 plans",
		// which is what is true.
		rows = append(rows, qsResultRow{cells: cells, queryID: id})
	}
	return rows
}

// qsNoTrackedQueriesResult is what the view shows before anything is tracked —
// an empty grid there reads as a report that failed.
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

// queryStoreRegressionOptions compares the second half of opts' window
// against the first, rather than the whole window against the one before it.
//
// gosmo's default baseline is the equally long window immediately *before*
// From, which is right for a caller that chose its own range — but here it
// would mean the report needed twice its window of Query Store history before
// it could show a row: on a database with two minutes of history that leaves
// the pane permanently empty, which reads as a broken report rather than a
// young database. Splitting keeps the requirement at the window the other six
// reports already need.
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

// regressedQueriesReport reads the window it is given. The split into two
// halves is queryStoreRegressionOptions', applied by the caller through
// queryStoreReport.effectiveOptions — applying it here as well would split the
// recent half a second time.
func regressedQueriesReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	stats, err := d.QueryStoreRegressedQueriesContext(ctx, opts)
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
			// The regression, not the value: this report is ranked by how much
			// a query grew, and plotting the absolute cost would put the
			// slowest query at the top of a chart about change.
			value:     s.Regression,
			queryID:   s.QueryID,
			queryText: s.QueryText,
		})
	}
	return res, nil
}

func highVariationQueriesReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	stats, err := d.QueryStoreHighVariationQueriesContext(ctx, opts)
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
	intervals, err := d.QueryStoreOverallConsumptionContext(ctx, opts)
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
	// The metric selects a runtime-stats column, and wait statistics have
	// none of them — the value is always wait time. The statistic still
	// applies, and is passed through.
	waits, err := d.QueryStoreWaitCategoriesContext(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	// The value column, the chart axis and the status line all name wait time
	// rather than the metric — from one expression, so none of the three can
	// start claiming the metric selector reached this report.
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

// qsMetric and qsStatistic resolve what a report is ranked by, filling in the
// same defaults gosmo's own resolve does — so a column header never disagrees
// with the query that filled it.
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

// formatQSValue renders a metric's value in the unit gosmo measures it in.
// Every metric shares one float column, so the unit is the only thing saying
// whether 2500 is two and a half milliseconds or 20 MB of reads.
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

// qsQueryBarLabel labels a query's bar. The chart's gutter is a few columns
// wide, so the id is what fits — and it is what the grid's first column and
// both plan actions address the query by.
func qsQueryBarLabel(s *gosmo.QSQueryStat) string {
	return "Q" + strconv.FormatInt(s.QueryID, 10)
}

// planIDOrDash renders a forced plan id, or a dash where no plan is forced —
// a 0 in that column would read as a real plan whose id happens to be zero.
func planIDOrDash(id int64) string {
	if id == 0 {
		return "-"
	}
	return strconv.FormatInt(id, 10)
}

// queryStoreOneLine flattens a query's text onto one grid line. Query Store
// keeps the statement exactly as it was submitted, newlines and indentation
// included, and a raw newline in a grid cell breaks the row it is in.
//
// The text is not cut short: DataGrid clamps the column's width and truncates
// what it draws, so the cell keeps the whole statement for "Show Value" to
// open in a query panel.
func queryStoreOneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// sortByLastExecution orders stats most recently executed first.
func sortByLastExecution(stats []*gosmo.QSQueryStat) {
	slices.SortStableFunc(stats, func(a, b *gosmo.QSQueryStat) int {
		return b.LastExecutionTime.Compare(a.LastExecutionTime)
	})
}
