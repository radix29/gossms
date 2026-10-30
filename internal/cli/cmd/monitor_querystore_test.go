package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
)

// qsFake is monitorCatalog's Query Store: database Sales, its state, the
// server's clock, the metrics the instance has, and each report's rows (or
// readErr). Every report read records the options it was given and which
// report it was.
type qsFake struct {
	state   gosmo.QueryStoreState
	now     time.Time
	metrics []gosmo.QSMetric // nil: every metric
	rows    []*gosmo.QSQueryStat
	waits   []*gosmo.QSWaitStat
	readErr error

	got         []string // the reports read, in order
	gotOpts     gosmo.QueryStoreReportOptions
	gotCategory gosmo.QSWaitCategory
}

// serverZone is the fake server's: not the client's, nor UTC, so a window
// read in the wrong zone shows.
var serverZone = time.FixedZone("", 5*3600)

func newQSCatalog() *monitorCatalog {
	return &monitorCatalog{qs: &qsFake{state: gosmo.QueryStoreReadWrite,
		now: time.Date(2026, 10, 30, 12, 0, 0, 0, serverZone)}}
}

func (c *monitorCatalog) CurrentDatabase(context.Context) (string, error) { return "Sales", nil }

func (c *monitorCatalog) DatabaseByName(_ context.Context, name string) (*gosmo.Database, error) {
	return &gosmo.Database{Name: name}, nil
}

func (c *monitorCatalog) QueryStoreOptions(context.Context, *gosmo.Database) (*gosmo.QueryStoreInfo, error) {
	if c.qs.state == "" {
		return nil, fmt.Errorf("gosmo: query store options: %w", gosmo.ErrNotFound)
	}
	return &gosmo.QueryStoreInfo{ActualState: c.qs.state, DesiredState: c.qs.state}, nil
}

func (c *monitorCatalog) CurrentTime(context.Context) (time.Time, error) { return c.qs.now, nil }

func (c *monitorCatalog) QueryStoreMetrics(*gosmo.Database) []gosmo.QSMetric {
	if c.qs.metrics == nil {
		var all []gosmo.QSMetric
		for _, m := range qsMetricNames {
			all = append(all, m.Metric)
		}
		return all
	}
	return c.qs.metrics
}

func (q *qsFake) read(report string, opts gosmo.QueryStoreReportOptions) ([]*gosmo.QSQueryStat, error) {
	q.got = append(q.got, report)
	q.gotOpts = opts
	return q.rows, q.readErr
}

func (c *monitorCatalog) QSTopResourceQueries(_ context.Context, _ *gosmo.Database, o gosmo.QueryStoreReportOptions) ([]*gosmo.QSQueryStat, error) {
	return c.qs.read("top", o)
}

func (c *monitorCatalog) QSRegressedQueries(_ context.Context, _ *gosmo.Database, o gosmo.QueryStoreReportOptions) ([]*gosmo.QSQueryStat, error) {
	return c.qs.read("regressed", o)
}

func (c *monitorCatalog) QSHighVariationQueries(_ context.Context, _ *gosmo.Database, o gosmo.QueryStoreReportOptions) ([]*gosmo.QSQueryStat, error) {
	return c.qs.read("variation", o)
}

func (c *monitorCatalog) QSForcedPlanQueries(_ context.Context, _ *gosmo.Database, o gosmo.QueryStoreReportOptions) ([]*gosmo.QSQueryStat, error) {
	return c.qs.read("forced", o)
}

func (c *monitorCatalog) QSWaitCategories(_ context.Context, _ *gosmo.Database, o gosmo.QueryStoreReportOptions) ([]*gosmo.QSWaitStat, error) {
	c.qs.got = append(c.qs.got, "categories")
	c.qs.gotOpts = o
	return c.qs.waits, c.qs.readErr
}

func (c *monitorCatalog) QSWaitingQueries(_ context.Context, _ *gosmo.Database, cat gosmo.QSWaitCategory, o gosmo.QueryStoreReportOptions) ([]*gosmo.QSQueryStat, error) {
	c.qs.gotCategory = cat
	return c.qs.read("waiting", o)
}

func runQS(t *testing.T, cat *monitorCatalog, argv ...string) (*output.Result, error) {
	t.Helper()
	return runMonitor(t, cat, append([]string{"querystore"}, argv...)...)
}

func colNames(set output.Set) []string {
	return names(set.Columns, func(c output.Column) string { return c.Name })
}

// qsStat is a query whose figures are in Query Store's units: value 2 500
// microseconds.
func qsStat(id int64) *gosmo.QSQueryStat {
	return &gosmo.QSQueryStat{QueryID: id, QueryText: fmt.Sprint("SELECT ", id), ExecCount: 10, PlanCount: 2,
		LastExecutionTime: time.Date(2026, 10, 30, 6, 30, 0, 0, time.UTC), Value: 2500}
}

// The defaults are the TUI panel's: top by total duration over the 24 hours
// to the server's now. Microseconds come out as ms; times in the server's
// zone; an ad hoc batch's object, an unforced query's plan and a query
// never executed in the window are NULL.
func TestMonitorQueryStoreTop(t *testing.T) {
	cat := newQSCatalog()
	proc := qsStat(7)
	proc.ObjectName, proc.ForcedPlanID = "dbo.GetOrders", 31
	never := &gosmo.QSQueryStat{QueryID: 8, QueryText: "SELECT 8"}
	cat.qs.rows = []*gosmo.QSQueryStat{proc, qsStat(9), never}
	res, err := runQS(t, cat)
	if err != nil {
		t.Fatal(err)
	}
	want := gosmo.QueryStoreReportOptions{Metric: gosmo.QSMetricDuration, Statistic: gosmo.QSStatTotal, Top: 25,
		From: cat.qs.now.Add(-24 * time.Hour), To: cat.qs.now}
	if !slices.Equal(cat.qs.got, []string{"top"}) || !qsOptsEqual(cat.qs.gotOpts, want) {
		t.Errorf("read %q with %+v, want top with %+v", cat.qs.got, cat.qs.gotOpts, want)
	}
	for k, v := range map[string]string{"report": "top", "from": "2026-10-29 12:00:00.000", "to": "2026-10-30 12:00:00.000",
		"metric": "duration", "statistic": "total", "unit": "ms", "baselineFrom": "<missing>"} {
		if got := field(res, k); got != v {
			t.Errorf("field %s = %q, want %q", k, got, v)
		}
	}
	set := res.Sets[0]
	if cols := colNames(set); !slices.Equal(cols, []string{"n", "query_id", "object", "executions", "plans",
		"forced_plan_id", "last_execution", "value", "text"}) {
		t.Errorf("columns = %q", cols)
	}
	wantRows := []string{
		"1 7 dbo.GetOrders 10 2 31 2026-10-30 11:30:00.000 2.500 SELECT 7",
		"2 9 NULL 10 2 NULL 2026-10-30 11:30:00.000 2.500 SELECT 9",
		"3 8 NULL 0 0 NULL NULL 0.000 SELECT 8",
	}
	if set.Name != "queries" || !slices.Equal(cells(set), wantRows) {
		t.Errorf("queries =\n%q\nwant\n%q", cells(set), wantRows)
	}
}

func qsOptsEqual(a, b gosmo.QueryStoreReportOptions) bool {
	return a.Metric == b.Metric && a.Statistic == b.Statistic && a.Top == b.Top &&
		a.MinExecCount == b.MinExecCount && a.MinRegressionPct == b.MinRegressionPct &&
		a.From.Equal(b.From) && a.To.Equal(b.To) && a.BaselineFrom.Equal(b.BaselineFrom) && a.BaselineTo.Equal(b.BaselineTo)
}

// A zoneless --since is the server's wall clock, not the client's or UTC; one
// with a zone is that instant.
func TestMonitorQueryStoreWindow(t *testing.T) {
	cat := newQSCatalog()
	res, err := runQS(t, cat, "--since", "2026-10-30T08:00", "--until", "2026-10-30T05:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	from, to := time.Date(2026, 10, 30, 8, 0, 0, 0, serverZone), time.Date(2026, 10, 30, 5, 0, 0, 0, time.UTC)
	if !cat.qs.gotOpts.From.Equal(from) || !cat.qs.gotOpts.To.Equal(to) {
		t.Errorf("window %v to %v, want %v to %v", cat.qs.gotOpts.From, cat.qs.gotOpts.To, from, to)
	}
	if field(res, "from") != "2026-10-30 08:00:00.000" || field(res, "to") != "2026-10-30 10:00:00.000" {
		t.Errorf("fields %+v", res.Fields)
	}

	// --since alone: the window runs to now.
	cat = newQSCatalog()
	if _, err := runQS(t, cat, "--since", "2026-10-30"); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 30, 0, 0, 0, 0, serverZone); !cat.qs.gotOpts.From.Equal(want) || !cat.qs.gotOpts.To.Equal(cat.qs.now) {
		t.Errorf("window %v to %v", cat.qs.gotOpts.From, cat.qs.gotOpts.To)
	}

	// --until alone: the 24 hours before it.
	cat = newQSCatalog()
	if _, err := runQS(t, cat, "--until", "2026-10-30T06:00"); err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 29, 6, 0, 0, 0, serverZone); !cat.qs.gotOpts.From.Equal(want) {
		t.Errorf("from %v, want %v", cat.qs.gotOpts.From, want)
	}

	// An empty window is refused before any report is read.
	cat = newQSCatalog()
	_, err = runQS(t, cat, "--since", "2026-10-30T09:00", "--until", "2026-10-30T09:00")
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUsageError || len(cat.qs.got) != 0 {
		t.Errorf("empty window: err = %v, read %q", err, cat.qs.got)
	}
}

// Regressed compares the window's second half against its first, as the TUI
// does, and shows the baseline beside the value; growth from a zero
// baseline has no percentage.
func TestMonitorQueryStoreRegressed(t *testing.T) {
	cat := newQSCatalog()
	grew := qsStat(3)
	grew.BaselineValue, grew.BaselineExecCount, grew.Regression = 1000, 4, 1500
	fromZero := qsStat(4)
	fromZero.Regression = 2500
	cat.qs.rows = []*gosmo.QSQueryStat{grew, fromZero}
	res, err := runQS(t, cat, "--regressed", "--since", "2026-10-30T08:00", "--until", "2026-10-30T12:00",
		"--metric", "cpu", "--min-exec", "3", "--min-regression-pct", "50")
	if err != nil {
		t.Fatal(err)
	}
	at := func(h int) time.Time { return time.Date(2026, 10, 30, h, 0, 0, 0, serverZone) }
	want := gosmo.QueryStoreReportOptions{Metric: gosmo.QSMetricCPUTime, Statistic: gosmo.QSStatAvg, Top: 25,
		MinExecCount: 3, MinRegressionPct: 50, From: at(10), To: at(12), BaselineFrom: at(8), BaselineTo: at(10)}
	if !slices.Equal(cat.qs.got, []string{"regressed"}) || !qsOptsEqual(cat.qs.gotOpts, want) {
		t.Errorf("read %q with %+v, want %+v", cat.qs.got, cat.qs.gotOpts, want)
	}
	if field(res, "from") != "2026-10-30 10:00:00.000" || field(res, "baselineFrom") != "2026-10-30 08:00:00.000" ||
		field(res, "baselineTo") != "2026-10-30 10:00:00.000" || field(res, "statistic") != "avg" || field(res, "metric") != "cpu" {
		t.Errorf("fields %+v", res.Fields)
	}
	set := res.Sets[0]
	if cols := colNames(set); !slices.Equal(cols[7:], []string{"value", "baseline_value", "baseline_executions",
		"regression", "regression_pct", "text"}) {
		t.Errorf("columns = %q", cols)
	}
	wantRows := []string{
		"1 3 NULL 10 2 NULL 2026-10-30 11:30:00.000 2.500 1.000 4 1.500 150.00 SELECT 3",
		"2 4 NULL 10 2 NULL 2026-10-30 11:30:00.000 2.500 0.000 0 2.500 NULL SELECT 4",
	}
	if !slices.Equal(cells(set), wantRows) {
		t.Errorf("queries =\n%q\nwant\n%q", cells(set), wantRows)
	}
	// --report regressed is the same read.
	cat = newQSCatalog()
	if _, err := runQS(t, cat, "--report", "regressed", "--regressed"); err != nil || !slices.Equal(cat.qs.got, []string{"regressed"}) {
		t.Errorf("--report regressed --regressed: err %v, read %q", err, cat.qs.got)
	}
}

// Variation adds its coefficient; a metric counted in pages is not scaled.
func TestMonitorQueryStoreVariation(t *testing.T) {
	cat := newQSCatalog()
	q := qsStat(5)
	q.Variation = 1.23456
	cat.qs.rows = []*gosmo.QSQueryStat{q}
	res, err := runQS(t, cat, "--report", "variation", "--metric", "logical-reads", "--stat", "max", "--top", "3")
	if err != nil {
		t.Fatal(err)
	}
	if cat.qs.gotOpts.Metric != gosmo.QSMetricLogicalReads || cat.qs.gotOpts.Statistic != gosmo.QSStatMax || cat.qs.gotOpts.Top != 3 {
		t.Errorf("options %+v", cat.qs.gotOpts)
	}
	if field(res, "unit") != "pages" || field(res, "statistic") != "max" {
		t.Errorf("fields %+v", res.Fields)
	}
	if got := cells(res.Sets[0]); !slices.Equal(got, []string{"1 5 NULL 10 2 NULL 2026-10-30 11:30:00.000 2500.000 1.2346 SELECT 5"}) {
		t.Errorf("queries = %q", got)
	}
	cat = newQSCatalog()
	if _, err := runQS(t, cat, "--report", "forced"); err != nil || !slices.Equal(cat.qs.got, []string{"forced"}) {
		t.Errorf("forced: err %v, read %q", err, cat.qs.got)
	}
}

// Waits is two reads: the categories, then the queries waiting in every
// category or the one named. Values are wait ms, unscaled.
func TestMonitorQueryStoreWaits(t *testing.T) {
	cat := newQSCatalog()
	cat.qs.waits = []*gosmo.QSWaitStat{{Category: "Lock", ExecCount: 3, Value: 40}, {Category: "CPU", ExecCount: 9, Value: 120.5}}
	cat.qs.rows = []*gosmo.QSQueryStat{qsStat(1)}
	res, err := runQS(t, cat, "--report", "waits")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cat.qs.got, []string{"categories", "waiting"}) || cat.qs.gotCategory != gosmo.QSWaitAllCategories ||
		cat.qs.gotOpts.Statistic != gosmo.QSStatTotal {
		t.Errorf("read %q, category %q, options %+v", cat.qs.got, cat.qs.gotCategory, cat.qs.gotOpts)
	}
	if field(res, "metric") != "wait time" || field(res, "unit") != "ms" {
		t.Errorf("fields %+v", res.Fields)
	}
	if res.Sets[0].Name != "categories" || !slices.Equal(cells(res.Sets[0]), []string{"CPU 9 120.500", "Lock 3 40.000"}) {
		t.Errorf("categories = %q", cells(res.Sets[0]))
	}
	if got := cells(res.Sets[1]); !slices.Equal(got, []string{"1 1 NULL 10 2 NULL 2026-10-30 11:30:00.000 2500.000 SELECT 1"}) {
		t.Errorf("queries = %q", got)
	}

	// A named category lifts the avg/total restriction.
	cat = newQSCatalog()
	if _, err := runQS(t, cat, "--report", "waits", "--wait-category", "Lock", "--stat", "max"); err != nil {
		t.Fatal(err)
	}
	if cat.qs.gotCategory != "Lock" || cat.qs.gotOpts.Statistic != gosmo.QSStatMax {
		t.Errorf("category %q, options %+v", cat.qs.gotCategory, cat.qs.gotOpts)
	}
}

func TestMonitorQueryStoreUsage(t *testing.T) {
	for _, argv := range [][]string{
		{"--regressed", "--report", "top"},
		{"--report", "waits", "--metric", "cpu"},
		{"--report", "waits", "--stat", "max"},
		{"--report", "waits", "--min-exec", "2"},
		{"--report", "waits", "--wait-category", ""},
		{"--min-regression-pct", "10"},
		{"--wait-category", "Lock"},
		{"--top", "0"},
		{"--min-exec", "-1"},
		{"--regressed", "--min-regression-pct", "-5"},
		{"--since", "yesterday"},
		{"--until", "2026-10-30 10:00"},
	} {
		cat := newQSCatalog()
		_, err := runQS(t, cat, argv...)
		if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUsageError {
			t.Errorf("%q: err = %v, want a usage error", argv, err)
		}
		if len(cat.qs.got) != 0 {
			t.Errorf("%q: read before refusing", argv)
		}
	}
}

// Query Store off is exit 8 naming the way to turn it on, and so is a
// database with none; a metric the
// instance does not record is exit 11; neither reads a report. A report's
// own failure maps as every read's does.
func TestMonitorQueryStoreRefusals(t *testing.T) {
	cat := newQSCatalog()
	cat.qs.state = gosmo.QueryStoreOff
	_, err := runQS(t, cat)
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeNotFound || len(cat.qs.got) != 0 ||
		oe.Message != "Query Store is OFF in [Sales]" {
		t.Errorf("off: err = %v, read %q", err, cat.qs.got)
	}
	cat = newQSCatalog()
	cat.qs.state = "" // master: no row
	_, err = runQS(t, cat)
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeNotFound || len(cat.qs.got) != 0 {
		t.Errorf("master: err = %v, read %q", err, cat.qs.got)
	}
	cat = newQSCatalog()
	cat.qs.state = gosmo.QueryStoreReadOnly // full, still readable
	if _, err := runQS(t, cat); err != nil || len(cat.qs.got) != 1 {
		t.Errorf("read-only: err = %v, read %q", err, cat.qs.got)
	}

	cat = newQSCatalog()
	cat.qs.metrics = []gosmo.QSMetric{gosmo.QSMetricDuration, gosmo.QSMetricCPUTime}
	_, err = runQS(t, cat, "--metric", "tempdb")
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUnsupported || len(cat.qs.got) != 0 {
		t.Errorf("tempdb on 2016: err = %v, read %q", err, cat.qs.got)
	}

	cat = newQSCatalog()
	cat.qs.readErr = fmt.Errorf("gosmo: query store wait statistics in \"Sales\": %w", gosmo.ErrUnsupportedVersion)
	_, err = runQS(t, cat, "--report", "waits")
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUnsupported {
		t.Errorf("waits on 2016: err = %v", err)
	}

	cat = newQSCatalog()
	cat.qs.readErr = mssql.Error{Number: 300, Class: 14, Message: "The user does not have permission to perform this action."}
	_, err = runQS(t, cat)
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodePermissionDenied ||
		!strings.Contains(oe.Hint, "VIEW DATABASE STATE") {
		t.Errorf("no VIEW DATABASE STATE: err = %v", err)
	}
}
