package cmd

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The monitor querystore options (D5). The database is the connection's -d
// (--database). --since is server log's name, --top db table-sizes'.
const (
	optReport           = "report"
	optRegressed        = "regressed"
	optMetric           = "metric"
	optStat             = "stat"
	optUntil            = "until"
	optMinExec          = "min-exec"
	optMinRegressionPct = "min-regression-pct"
	optWaitCategory     = "wait-category"
)

// qsDefaultWindow is the window with no --since: the TUI Query Store
// panel's default, so the two answer alike.
const qsDefaultWindow = 24 * time.Hour

// qsReport is one --report value: gosmo's report and the statistic it is
// read with when --stat is not given — the TUI panel's: Total for the
// reports read as accumulated cost, Avg for those about cost per execution.
type qsReport struct {
	Name        string
	DefaultStat gosmo.QSStatistic
	read        func(Catalog, context.Context, *gosmo.Database, gosmo.QueryStoreReportOptions) ([]*gosmo.QSQueryStat, error)
}

// qsReports is --report's values, the default first. waits has no read: it
// is two reads (qsWaitsReport).
var qsReports = []qsReport{
	{"top", gosmo.QSStatTotal, Catalog.QSTopResourceQueries},
	{"regressed", gosmo.QSStatAvg, Catalog.QSRegressedQueries},
	{"variation", gosmo.QSStatAvg, Catalog.QSHighVariationQueries},
	{"forced", gosmo.QSStatAvg, Catalog.QSForcedPlanQueries},
	{"waits", gosmo.QSStatTotal, nil},
}

func qsReportNamed(name string) qsReport {
	i := slices.IndexFunc(qsReports, func(r qsReport) bool { return r.Name == name })
	return qsReports[i]
}

// qsMetricName is one --metric value and the gosmo metric it names, in
// gosmo's (SSMS's) order; the first is the default.
type qsMetricName struct {
	Name   string
	Metric gosmo.QSMetric
}

var qsMetricNames = []qsMetricName{
	{"duration", gosmo.QSMetricDuration},
	{"cpu", gosmo.QSMetricCPUTime},
	{"logical-reads", gosmo.QSMetricLogicalReads},
	{"logical-writes", gosmo.QSMetricLogicalWrites},
	{"physical-reads", gosmo.QSMetricPhysicalReads},
	{"clr", gosmo.QSMetricCLRTime},
	{"dop", gosmo.QSMetricDOP},
	{"memory", gosmo.QSMetricMemory},
	{"rows", gosmo.QSMetricRowCount},
	{"log-memory", gosmo.QSMetricLogMemory},
	{"tempdb", gosmo.QSMetricTempDBMemory},
}

// qsStatNames is --stat's values and the gosmo statistic each names.
var qsStatNames = map[string]gosmo.QSStatistic{
	"avg": gosmo.QSStatAvg, "max": gosmo.QSStatMax, "min": gosmo.QSStatMin,
	"stdev": gosmo.QSStatStdDev, "total": gosmo.QSStatTotal,
}

var qsStatOrder = []string{"avg", "max", "min", "stdev", "total"}

func names[T any](xs []T, name func(T) string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = name(x)
	}
	return out
}

func monitorQueryStoreCommand(d Deps) *spec.Command {
	return &spec.Command{
		Path:    []string{"monitor", "querystore"},
		Summary: "Rank the database's queries from Query Store: top, regressed, high variation, forced plans, waits",
		Description: "Query Store's history of the connection's database (-d): the reports of SSMS's Query Store " +
			"views, read from sys.query_store_*. --report top (default) ranks by the metric; regressed by how " +
			"much it grew from the first half of the window to the second (the TUI's Regressed Queries; a query " +
			"with no executions in the first half is new, not regressed, and is left out); variation by its " +
			"coefficient of variation (stdev / avg); forced lists the queries with a forced plan; waits is the " +
			"wait time per category (set categories) and the queries that waited most (set queries), SQL " +
			"Server 2017 on (exit 11 before it). --regressed is --report regressed. --metric (default " +
			"duration; log-memory and tempdb from SQL Server 2017, exit 11 before it) and --stat (default total " +
			"for top and waits, avg for the others; stdev pooled across intervals) choose the value; waits " +
			"measures wait time whatever the metric, so --metric is refused with it, and across every category " +
			"only avg and total are defined (exit 2), --wait-category naming one (as the server spells it: CPU, " +
			"Lock, Buffer IO, ...) lifting that. The window is --since to --until (default: the 24 hours to now, " +
			"the TUI panel's), YYYY-MM-DD[THH:MM[:SS]] in the server's local time, or with Z or an offset that " +
			"instant; Query Store keeps one row per interval (60 minutes by default), and every interval that " +
			"overlaps the window counts whole. Fields from, to (and for regressed baselineFrom, baselineTo: the " +
			"first half) in server local time; metric, statistic, unit (ms for times — Query Store keeps " +
			"microseconds —, pages of 8 KB, bytes, or count). Set queries, highest first, --top rows (default " +
			"25): value the statistic of the metric, baseline_value, baseline_executions, regression (value - " +
			"baseline_value) and regression_pct for regressed, variation for variation; object the module the " +
			"query is in, NULL for an ad hoc batch; forced_plan_id NULL when no plan is forced; last_execution " +
			"in server local time. --min-exec drops queries that ran fewer times in the window (not with waits); " +
			"--min-regression-pct (regressed only) those whose value grew by less. Internal queries (statistics " +
			"updates and the like) are left out, as SSMS leaves them. Query Store OFF or in ERROR, or a database that cannot have one (master, tempdb): exit 8. " +
			"Needs VIEW DATABASE STATE: without it, exit 9.",
		Options: append(ConnectOptions(),
			spec.Option{Name: optReport, Type: spec.TypeEnum, Enum: names(qsReports, func(r qsReport) string { return r.Name }),
				Default: qsReports[0].Name, Help: "The report to read"},
			spec.Option{Name: optRegressed, Type: spec.TypeBool, Help: "--report regressed"},
			spec.Option{Name: optMetric, Type: spec.TypeEnum, Enum: names(qsMetricNames, func(m qsMetricName) string { return m.Name }),
				Default: qsMetricNames[0].Name, Help: "The metric to rank by (not with --report waits)"},
			spec.Option{Name: optStat, Type: spec.TypeEnum, Enum: qsStatOrder,
				Help: "How the metric is aggregated over the window; default total for top and waits, avg otherwise"},
			spec.Option{Name: optSince, Type: spec.TypeString,
				Help: "The window's start: YYYY-MM-DD[THH:MM[:SS]], server local time, or with Z or an offset; default 24 hours before --until"},
			spec.Option{Name: optUntil, Type: spec.TypeString,
				Help: "The window's end, as --since; default now"},
			spec.Option{Name: optTop, Type: spec.TypeInt, Default: strconv.Itoa(gosmo.QSDefaultTop),
				Help: "Keep this many rows, the highest first"},
			spec.Option{Name: optMinExec, Type: spec.TypeInt, Default: "0",
				Help: "Leave out queries that ran fewer times than this in the window"},
			spec.Option{Name: optMinRegressionPct, Type: spec.TypeInt, Default: "0",
				Help: "With --report regressed: leave out queries whose value grew by less than this percentage"},
			spec.Option{Name: optWaitCategory, Type: spec.TypeString,
				Help: "With --report waits: the queries waiting in this category only"},
		),
		Class: spec.ClassRead,
		Run:   d.monitorQueryStore,
		Examples: []string{
			"gossms monitor querystore -d Sales -p prod-ro",
			"gossms monitor querystore -d Sales --regressed --since 2026-10-29 -p prod-ro -o json",
			"gossms monitor querystore -d Sales --report waits --wait-category Lock --top 10 -p prod-ro",
			"gossms monitor querystore -d Sales --report variation --metric cpu --min-exec 10 -p prod-ro",
		},
	}
}

// qsRequest is monitor querystore's options, checked before connecting.
type qsRequest struct {
	Report       qsReport
	Metric       qsMetricName
	Stat         gosmo.QSStatistic
	Since, Until qsTime
	Opts         gosmo.QueryStoreReportOptions
	WaitCategory gosmo.QSWaitCategory
}

func readQSRequest(inv *spec.Invocation) (qsRequest, error) {
	r := qsRequest{Report: qsReportNamed(inv.Values[optReport].(string))}
	if inv.Values[optRegressed].(bool) {
		if inv.Given[optReport] && r.Report.Name != "regressed" {
			return r, usageError(fmt.Sprintf("--regressed and --report %s ask for two reports", r.Report.Name),
				"--regressed is --report regressed: give one of them.")
		}
		r.Report = qsReportNamed("regressed")
	}
	waits := r.Report.Name == "waits"
	onlyWith := func(opt, report string) error {
		return usageError(fmt.Sprintf("--%s applies to --report %s only", opt, report),
			fmt.Sprintf("Leave --%s out, or read --report %s.", opt, report))
	}
	if waits && inv.Given[optMetric] {
		return r, usageError("--metric: --report waits measures wait time",
			"Leave --metric out: the waits report's value is wait time in ms.")
	}
	if waits && inv.Given[optMinExec] {
		return r, onlyWith(optMinExec, "top, regressed, variation or forced")
	}
	if r.Report.Name != "regressed" && inv.Given[optMinRegressionPct] {
		return r, onlyWith(optMinRegressionPct, "regressed")
	}
	if !waits && inv.Given[optWaitCategory] {
		return r, onlyWith(optWaitCategory, "waits")
	}
	name := inv.Values[optMetric].(string)
	r.Metric = qsMetricNames[slices.IndexFunc(qsMetricNames, func(m qsMetricName) bool { return m.Name == name })]
	r.Stat = r.Report.DefaultStat
	if s, _ := inv.Values[optStat].(string); s != "" {
		r.Stat = qsStatNames[s]
	}
	if c, _ := inv.Values[optWaitCategory].(string); inv.Given[optWaitCategory] {
		if c == "" {
			return r, usageError("--wait-category is empty", "Name a category, or leave --wait-category out for every one.")
		}
		r.WaitCategory = gosmo.QSWaitCategory(c)
	}
	if waits && r.WaitCategory == gosmo.QSWaitAllCategories && r.Stat != gosmo.QSStatAvg && r.Stat != gosmo.QSStatTotal {
		return r, usageError(fmt.Sprintf("--stat %s: across every wait category only avg and total are defined", inv.Values[optStat]),
			"Use --stat avg or total, or name a category with --wait-category.")
	}
	top := inv.Values[optTop].(int)
	if top < 1 {
		return r, usageError(fmt.Sprintf("--top: %d is not a number of rows", top), "Give the number of rows to keep, 1 or more.")
	}
	minExec := inv.Values[optMinExec].(int)
	if minExec < 0 {
		return r, usageError(fmt.Sprintf("--min-exec: %d is negative", minExec), "Give the fewest executions a query must have, or 0.")
	}
	minPct := inv.Values[optMinRegressionPct].(int)
	if minPct < 0 {
		return r, usageError(fmt.Sprintf("--min-regression-pct: %d is negative", minPct), "Give the least growth in percent, or 0.")
	}
	var err error
	if s, _ := inv.Values[optSince].(string); s != "" {
		if r.Since, err = parseQSTime(optSince, s); err != nil {
			return r, err
		}
	}
	if s, _ := inv.Values[optUntil].(string); s != "" {
		if r.Until, err = parseQSTime(optUntil, s); err != nil {
			return r, err
		}
	}
	r.Opts = gosmo.QueryStoreReportOptions{Metric: r.Metric.Metric, Statistic: r.Stat, Top: top,
		MinExecCount: int64(minExec), MinRegressionPct: float64(minPct)}
	return r, nil
}

// qsTime is a --since or --until: an instant when it named a zone, else a
// server-local wall clock carried in UTC until the server's zone is known.
// The zero value is "not given".
type qsTime struct {
	t     time.Time
	zoned bool
}

// in is the instant t names, a zoneless wall clock read in loc.
func (q qsTime) in(loc *time.Location) time.Time {
	if q.zoned {
		return q.t
	}
	return time.Date(q.t.Year(), q.t.Month(), q.t.Day(), q.t.Hour(), q.t.Minute(), q.t.Second(), 0, loc)
}

// qsZonedLayouts are the forms with a zone --since and --until take
// besides server log's zoneless ones.
var qsZonedLayouts = []string{time.RFC3339, "2006-01-02T15:04Z07:00"}

func parseQSTime(opt, s string) (qsTime, error) {
	for _, layout := range sinceLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return qsTime{t: t}, nil
		}
	}
	for _, layout := range qsZonedLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return qsTime{t: t, zoned: true}, nil
		}
	}
	return qsTime{}, usageError(fmt.Sprintf("--%s %q is not a date", opt, s),
		"Give YYYY-MM-DD, YYYY-MM-DDTHH:MM or YYYY-MM-DDTHH:MM:SS in the server's local time, or add Z or an offset (+01:00).")
}

// qsWindow resolves the window against the server's clock now: --until
// defaulting to now, --since to qsDefaultWindow before --until.
func (r *qsRequest) qsWindow(now time.Time) error {
	loc := now.Location()
	r.Opts.To = now
	if r.Until != (qsTime{}) {
		r.Opts.To = r.Until.in(loc)
	}
	r.Opts.From = r.Opts.To.Add(-qsDefaultWindow)
	if r.Since != (qsTime{}) {
		r.Opts.From = r.Since.in(loc)
	}
	if !r.Opts.From.Before(r.Opts.To) {
		return usageError(fmt.Sprintf("the window %s to %s is empty", gridTime(r.Opts.From.In(loc)), gridTime(r.Opts.To.In(loc))),
			"Give a --since before --until (server local time).")
	}
	if r.Report.Name == "regressed" {
		// The TUI's split (queryStoreRegressionOptions): the second half
		// against the first, so the report needs only its own window of
		// history, and the two answer alike for the same window.
		mid := r.Opts.From.Add(r.Opts.To.Sub(r.Opts.From) / 2)
		r.Opts.BaselineFrom, r.Opts.BaselineTo, r.Opts.From = r.Opts.From, mid, mid
	}
	return nil
}

func (d Deps) monitorQueryStore(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	r, err := readQSRequest(inv)
	if err != nil {
		return nil, err
	}
	res := &output.Result{}
	sc, cat, dbase, err := d.connectDatabase(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	if r.Report.Name != "waits" && !slices.Contains(cat.QueryStoreMetrics(dbase), r.Metric.Metric) {
		return res, output.NewError(output.CodeUnsupported, fmt.Sprintf("--metric %s: the server's Query Store does not record it", r.Metric.Name),
			"log-memory and tempdb are recorded from SQL Server 2017 on; choose another metric.")
	}
	info, err := cat.QueryStoreOptions(ctx, dbase)
	if errors.Is(err, gosmo.ErrNotFound) {
		return res, output.NewError(output.CodeNotFound,
			fmt.Sprintf("%s has no Query Store: it cannot be enabled in master or tempdb", gosmo.QuoteName(dbase.Name)),
			"Give a user database with -d.")
	}
	if err != nil {
		return res, qsReadError(err)
	}
	if !info.IsReadable() {
		return res, output.NewError(output.CodeNotFound,
			fmt.Sprintf("Query Store is %s in %s", info.ActualState, gosmo.QuoteName(dbase.Name)),
			fmt.Sprintf("Turn it on with `gossms db set %s --option QUERY_STORE=ON --allow admin`; it then records from that moment on.",
				dbase.Name))
	}
	now, err := cat.CurrentTime(ctx)
	if err != nil {
		return res, readError(err)
	}
	if err := r.qsWindow(now); err != nil {
		return res, err
	}
	loc := now.Location()

	unit, scale := "ms", 1.0
	if r.Report.Name != "waits" {
		unit, scale = qsUnit(r.Metric.Metric)
	}
	res.Fields = []output.Field{
		{Key: "report", Value: r.Report.Name},
		{Key: "from", Value: gridTime(r.Opts.From.In(loc))},
		{Key: "to", Value: gridTime(r.Opts.To.In(loc))},
	}
	if r.Report.Name == "regressed" {
		res.Fields = append(res.Fields,
			output.Field{Key: "baselineFrom", Value: gridTime(r.Opts.BaselineFrom.In(loc))},
			output.Field{Key: "baselineTo", Value: gridTime(r.Opts.BaselineTo.In(loc))})
	}
	metric := r.Metric.Name
	if r.Report.Name == "waits" {
		metric = "wait time"
	}
	res.Fields = append(res.Fields,
		output.Field{Key: "metric", Value: metric},
		output.Field{Key: "statistic", Value: qsStatName(r.Stat)},
		output.Field{Key: "unit", Value: unit})

	if r.Report.Name == "waits" {
		cats, err := cat.QSWaitCategories(ctx, dbase, r.Opts)
		if err != nil {
			return res, qsReadError(err)
		}
		qs, err := cat.QSWaitingQueries(ctx, dbase, r.WaitCategory, r.Opts)
		if err != nil {
			return res, qsReadError(err)
		}
		res.Sets = []output.Set{qsCategoriesSet(cats), qsQueriesSet(qs, r.Report.Name, scale, loc)}
		return res, nil
	}
	qs, err := r.Report.read(cat, ctx, dbase, r.Opts)
	if err != nil {
		return res, qsReadError(err)
	}
	res.Sets = []output.Set{qsQueriesSet(qs, r.Report.Name, scale, loc)}
	return res, nil
}

// qsReadError is readError for a Query Store read: the server's refusal
// (Msg 262, 300) names neither the permission nor an object.
func qsReadError(err error) *output.Error {
	e := readError(err)
	if e.Code == output.CodePermissionDenied {
		e.Hint = "Query Store's views need VIEW DATABASE STATE in the database. Use a login that has it, or have it granted."
	}
	return e
}

// qsStatName is --stat's spelling of a gosmo statistic.
func qsStatName(s gosmo.QSStatistic) string {
	for _, n := range qsStatOrder {
		if qsStatNames[n] == s {
			return n
		}
	}
	return string(s)
}

// qsUnit is the unit a metric's values are shown in, and the factor from
// Query Store's: times as milliseconds, as monitor top-queries shows them.
func qsUnit(m gosmo.QSMetric) (string, float64) {
	u, _ := gosmo.QSMetricUnit(m)
	switch u {
	case gosmo.QSUnitMicroseconds:
		return "ms", 1.0 / 1000
	case gosmo.QSUnitPages:
		return "pages", 1
	case gosmo.QSUnitBytes:
		return "bytes", 1
	case gosmo.QSUnitMilliseconds:
		return "ms", 1
	}
	return "count", 1
}

// qsValue is a value in the shown unit, three decimals.
func qsValue(v, scale float64) *string { return ptr(strconv.FormatFloat(v*scale, 'f', 3, 64)) }

// qsQueriesSet is monitor querystore's queries set: the columns every report
// fills, and those only regressed or variation does.
func qsQueriesSet(qs []*gosmo.QSQueryStat, report string, scale float64, loc *time.Location) output.Set {
	cols := []output.Column{
		{Name: "n", Type: "int"}, {Name: "query_id", Type: "bigint"},
		{Name: "object", Type: "nvarchar(257)", Nullable: true},
		{Name: "executions", Type: "bigint"}, {Name: "plans", Type: "int"},
		{Name: "forced_plan_id", Type: "bigint", Nullable: true},
		{Name: "last_execution", Type: "datetime", Nullable: true},
		{Name: "value", Type: "decimal(19,3)"},
	}
	switch report {
	case "regressed":
		cols = append(cols, output.Column{Name: "baseline_value", Type: "decimal(19,3)"},
			output.Column{Name: "baseline_executions", Type: "bigint"},
			output.Column{Name: "regression", Type: "decimal(19,3)"},
			output.Column{Name: "regression_pct", Type: "decimal(19,2)", Nullable: true})
	case "variation":
		cols = append(cols, output.Column{Name: "variation", Type: "decimal(19,4)"})
	}
	cols = append(cols, output.Column{Name: "text", Type: "nvarchar(max)"})
	set := output.Set{Name: "queries", Columns: cols}
	for i, q := range qs {
		var forced, last *string
		if q.ForcedPlanID != 0 {
			forced = int64Cell(q.ForcedPlanID)
		}
		if !q.LastExecutionTime.IsZero() {
			last = ptr(gridTime(q.LastExecutionTime.In(loc)))
		}
		row := []*string{
			ptr(strconv.Itoa(i + 1)), int64Cell(q.QueryID), textOrNull(q.ObjectName),
			int64Cell(q.ExecCount), ptr(strconv.Itoa(q.PlanCount)), forced, last, qsValue(q.Value, scale),
		}
		switch report {
		case "regressed":
			var pct *string
			if q.BaselineValue != 0 {
				pct = ptr(mbf(q.Regression / q.BaselineValue * 100))
			}
			row = append(row, qsValue(q.BaselineValue, scale), int64Cell(q.BaselineExecCount), qsValue(q.Regression, scale), pct)
		case "variation":
			row = append(row, ptr(strconv.FormatFloat(q.Variation, 'f', 4, 64)))
		}
		set.Rows = append(set.Rows, append(row, ptr(q.QueryText)))
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// qsCategoriesSet is the waits report's categories set, most wait time
// first as the server ranked it, category breaking ties.
func qsCategoriesSet(ws []*gosmo.QSWaitStat) output.Set {
	ws = slices.Clone(ws)
	slices.SortStableFunc(ws, func(a, b *gosmo.QSWaitStat) int {
		return cmp.Or(cmp.Compare(b.Value, a.Value), cmp.Compare(a.Category, b.Category))
	})
	set := output.Set{Name: "categories", Columns: []output.Column{
		{Name: "category", Type: "nvarchar(60)"}, {Name: "executions", Type: "bigint"},
		{Name: "value", Type: "decimal(19,3)"},
	}}
	for _, w := range ws {
		set.Rows = append(set.Rows, []*string{ptr(string(w.Category)), int64Cell(w.ExecCount), qsValue(w.Value, 1)})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}
