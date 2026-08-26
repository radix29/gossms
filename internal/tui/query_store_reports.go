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

// queryStoreQueryTextWidth is how much of a query's text a grid cell shows.
// The full text of a generated statement runs to kilobytes and would push
// every other column off the pane.
const queryStoreQueryTextWidth = 80

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

	// defaultStat is what the report means by default — Total for the two
	// that rank by accumulated cost, Avg for the five that rank by cost per
	// execution. The Detail Browser always uses it; the panel opens on it and
	// then follows the toolbar.
	defaultStat gosmo.QSStatistic

	load func(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error)
}

// queryStoreReports is every Query Store view, in the order SSMS lists them
// under the folder.
var queryStoreReports = []queryStoreReport{
	{"Regressed Queries",
		"Queries whose average duration grew across the reported window",
		gosmo.QSStatAvg, regressedQueriesReport},
	{"Overall Resource Consumption",
		"Total duration and executions per Query Store interval",
		gosmo.QSStatTotal, overallConsumptionReport},
	{"Top Resource Consuming Queries",
		"Queries ranked by total duration",
		gosmo.QSStatTotal, topResourceQueriesReport},
	{"Queries With Forced Plans",
		"Queries pinned to one plan, and which plan",
		gosmo.QSStatAvg, forcedPlanQueriesReport},
	{"Queries With High Variation",
		"Queries whose duration is least predictable, by coefficient of variation",
		gosmo.QSStatAvg, highVariationQueriesReport},
	{"Query Wait Statistics",
		"Wait time by category, for queries Query Store captured",
		gosmo.QSStatTotal, queryWaitStatisticsReport},
	{"Tracked Queries",
		"The costliest captured queries and their plans, most recently executed first",
		gosmo.QSStatAvg, trackedQueriesReport},
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
	res, err := report.load(ctx, d, gosmo.QueryStoreReportOptions{
		Metric:    gosmo.QSMetricDuration,
		Statistic: report.defaultStat,
		From:      to.Add(-queryStoreDetailWindow),
		To:        to,
	})
	if err != nil {
		return nil, nil, err
	}
	return res.columns, res.cells(), nil
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
	return []string{"Query ID", "Object", value, "Executions", "Plans", "Forced Plan", "Last Execution", "Query"}
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
	res := qsResult{columns: queryStoreQueryColumns(qsValueLabel(opts)), chartLabel: qsValueLabel(opts)}
	for _, s := range stats {
		res.rows = append(res.rows, qsResultRow{
			cells:   queryStoreQueryRow(s, formatQSValue(opts.Metric, s.Value)),
			label:   qsQueryBarLabel(s),
			value:   s.Value,
			queryID: s.QueryID,
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

func trackedQueriesReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	stats, err := d.QueryStoreTopResourceQueriesContext(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	// Most recently executed first: this is the list a user picks a query to
	// track *from*, so recency orders it better than cost does.
	sortByLastExecution(stats)
	return queryStatResult(stats, opts), nil
}

// queryStoreRegressionOptions compares the second half of opts' window
// against the first, rather than the whole window against the one before it.
//
// gosmo's default baseline is the equally long window immediately *before*
// From, which is right for a caller that chose its own range — but here it
// would mean the report needed twice its window of Query Store history before
// it could ever show a row. Verified on a database with two minutes of
// history: the default left the pane permanently empty, which reads as a
// broken report rather than a young database. Splitting keeps the requirement
// at the window the other six reports already need.
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

func regressedQueriesReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	opts = queryStoreRegressionOptions(opts)
	stats, err := d.QueryStoreRegressedQueriesContext(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	res := qsResult{columns: []string{"Query ID", "Object", qsValueLabel(opts), "Baseline", "Regression",
		"Executions", "Baseline Execs", "Query"}, chartLabel: "Regression in " + qsValueLabel(opts)}
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
			value:   s.Regression,
			queryID: s.QueryID,
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
		"Plans", "Forced Plan", "Query"}, chartLabel: "Variation (stdev / avg)"}
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
			label:   qsQueryBarLabel(s),
			value:   s.Variation,
			queryID: s.QueryID,
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
		chartLabel: qsValueLabel(opts)}
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
		}, nil
	}
	// The metric selects a runtime-stats column, and wait statistics have
	// none of them — the value is always wait time. The statistic still
	// applies, and is passed through.
	waits, err := d.QueryStoreWaitCategoriesContext(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	res := qsResult{columns: []string{"Wait Category", string(qsStatistic(opts)) + " Wait Time", "Executions"},
		chartLabel: string(qsStatistic(opts)) + " wait time (ms)"}
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
func queryStoreOneLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	return core.Truncate(text, queryStoreQueryTextWidth)
}

// sortByLastExecution orders stats most recently executed first.
func sortByLastExecution(stats []*gosmo.QSQueryStat) {
	slices.SortStableFunc(stats, func(a, b *gosmo.QSQueryStat) int {
		return b.LastExecutionTime.Compare(a.LastExecutionTime)
	})
}
