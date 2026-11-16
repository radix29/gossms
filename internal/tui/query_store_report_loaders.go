package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/tuikit/core"
)

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
	stats, err := d.QueryStore().TopResourceQueries(ctx, opts)
	if err != nil {
		return qsResult{}, err
	}
	return queryStatResult(stats, opts), nil
}

func forcedPlanQueriesReport(ctx context.Context, d *gosmo.Database, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	stats, err := d.QueryStore().ForcedPlanQueries(ctx, opts)
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
	stats, err := d.QueryStore().TopResourceQueries(ctx, opts)
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
// right for a caller-chosen range, but here the report would need twice its
// window of history before showing a row: with two minutes of history the pane
// stays empty and reads as broken. Splitting keeps the requirement at the window
// the other six reports need.
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
	stats, err := d.QueryStore().RegressedQueries(ctx, opts)
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
	stats, err := d.QueryStore().HighVariationQueries(ctx, opts)
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
	intervals, err := d.QueryStore().OverallConsumption(ctx, opts)
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
	if !d.QueryStore().WaitStatsSupported() {
		return qsResult{
			columns: propertyValueColumns,
			rows:    []qsResultRow{{cells: []string{"Query wait statistics", "Requires SQL Server 2017 or later"}}},
			note:    "Query wait statistics require SQL Server 2017 or later",
		}, nil
	}
	// The metric selects a runtime-stats column; wait statistics have none, so the
	// value is always wait time. The statistic still applies and is passed through.
	waits, err := d.QueryStore().WaitCategories(ctx, opts)
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
				string(w.Category),
				formatWaitMs(w.Value),
				core.FormatThousands(w.ExecCount),
			},
			label: string(w.Category),
			value: w.Value,
			drill: &qsDrill{category: w.Category, label: string(w.Category)},
		})
	}
	return res, nil
}

// waitingQueriesReport is Query Wait Statistics' drill-down: the queries that
// waited in one category (or in all of them), ranked by that wait time. The
// shared per-query columns, so the plan pane, Track Query and Plot History work
// on its rows as on Top Resource Consuming Queries'. Values are milliseconds of
// wait whatever the Metric selector says, as on the category list.
func waitingQueriesReport(ctx context.Context, d *gosmo.Database, drill qsDrill, opts gosmo.QueryStoreReportOptions) (qsResult, error) {
	stats, err := d.QueryStore().WaitingQueries(ctx, drill.category, opts)
	if err != nil {
		return qsResult{}, err
	}
	label := string(qsStatistic(opts)) + " " + drill.label + " Wait Time"
	if drill.all() {
		label = string(qsStatistic(opts)) + " Wait Time"
	}
	res := qsResult{columns: queryStoreQueryColumns(label), chartLabel: label + " (ms)", valueLabel: label}
	for _, s := range stats {
		res.rows = append(res.rows, qsResultRow{
			cells:     queryStoreQueryRow(s, formatWaitMs(s.Value)),
			label:     qsQueryBarLabel(s),
			value:     s.Value,
			queryID:   s.QueryID,
			queryText: s.QueryText,
		})
	}
	return res, nil
}

// formatWaitMs renders a wait time, which gosmo gives in milliseconds.
func formatWaitMs(v float64) string {
	return fmt.Sprintf("%s ms", core.FormatThousands(int64(v+0.5)))
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
