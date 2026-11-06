package cmd

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/output"
)

// serverZone is the fake server's: not the client's, nor UTC, so a window
// read in the wrong zone shows.
var serverZone = time.FixedZone("", 5*3600)

// qsNow is the Query Store tests' server clock.
var qsNow = time.Date(2026, 10, 30, 12, 0, 0, 0, serverZone)

// qsMonitor is a monitorServer for the Query Store reports: database Sales,
// its Query Store READ_WRITE, the server's clock qsNow.
func qsMonitor(t *testing.T) *monitorServer {
	s := newMonitorServer(t)
	s.clock = qsNow
	return s
}

func runQS(t *testing.T, s *monitorServer, argv ...string) (*output.Result, error) {
	t.Helper()
	return runMonitor(t, s, append([]string{"querystore"}, argv...)...)
}

// valueIs reports whether q ranks by expr: its value column is expr.
func valueIs(q gosmotest.Query, expr string) bool {
	return regexp.MustCompile(regexp.QuoteMeta(expr) + `\s+AS value`).MatchString(q.Text)
}

// windowIs reports whether q's i-th interval window is [from, to).
func windowIs(q gosmotest.Query, i int, from, to time.Time) bool {
	w := qsWindows(q)
	return len(w) > i && w[i][0].Equal(from) && w[i][1].Equal(to)
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
// to the server's now, read in the connection's database. Microseconds come
// out as ms; times in the server's zone; an ad hoc batch's object, an
// unforced query's plan and a query never executed in the window are NULL.
func TestMonitorQueryStoreTop(t *testing.T) {
	srv := qsMonitor(t)
	proc := qsStat(7)
	proc.ObjectName, proc.ForcedPlanID = "dbo.GetOrders", 31
	never := &gosmo.QSQueryStat{QueryID: 8, QueryText: "SELECT 8"}
	srv.qs.rows = []*gosmo.QSQueryStat{proc, qsStat(9), never}
	res, err := runQS(t, srv)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(srv.qs.read(), []string{"top"}) {
		t.Fatalf("read %q, want top", srv.qs.read())
	}
	q := srv.qs.last()
	if q.Database != "Sales" || q.Arg(0) != int64(25) || !valueIs(q, "SUM(rs.avg_duration * rs.count_executions)") ||
		!windowIs(q, 0, qsNow.Add(-24*time.Hour), qsNow) || qsParam(q, qsMinExec) != nil {
		t.Errorf("the report's query, in %q:\n%s", q.Database, q)
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

// A zoneless --since is the server's wall clock, not the client's or UTC; one
// with a zone is that instant.
func TestMonitorQueryStoreWindow(t *testing.T) {
	srv := qsMonitor(t)
	res, err := runQS(t, srv, "--since", "2026-10-30T08:00", "--until", "2026-10-30T05:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	from, to := time.Date(2026, 10, 30, 8, 0, 0, 0, serverZone), time.Date(2026, 10, 30, 5, 0, 0, 0, time.UTC)
	if q := srv.qs.last(); !windowIs(q, 0, from, to) {
		t.Errorf("window %v, want %v to %v", qsWindows(q), from, to)
	}
	if field(res, "from") != "2026-10-30 08:00:00.000" || field(res, "to") != "2026-10-30 10:00:00.000" {
		t.Errorf("fields %+v", res.Fields)
	}

	// --since alone: the window runs to now.
	srv = qsMonitor(t)
	if _, err := runQS(t, srv, "--since", "2026-10-30"); err != nil {
		t.Fatal(err)
	}
	if q := srv.qs.last(); !windowIs(q, 0, time.Date(2026, 10, 30, 0, 0, 0, 0, serverZone), qsNow) {
		t.Errorf("window %v", qsWindows(q))
	}

	// --until alone: the 24 hours before it.
	srv = qsMonitor(t)
	if _, err := runQS(t, srv, "--until", "2026-10-30T06:00"); err != nil {
		t.Fatal(err)
	}
	until := time.Date(2026, 10, 30, 6, 0, 0, 0, serverZone)
	if q := srv.qs.last(); !windowIs(q, 0, until.Add(-24*time.Hour), until) {
		t.Errorf("window %v", qsWindows(q))
	}

	// An empty window is refused before any report is read.
	srv = qsMonitor(t)
	_, err = runQS(t, srv, "--since", "2026-10-30T09:00", "--until", "2026-10-30T09:00")
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUsageError || len(srv.qs.reads) != 0 {
		t.Errorf("empty window: err = %v, read %q", err, srv.qs.read())
	}
}

// Regressed compares the window's second half against its first, as the TUI
// does, and shows the baseline beside the value. A regression floor drops a
// query with no baseline, as the server's WHERE does; without one its growth
// from zero has no percentage.
func TestMonitorQueryStoreRegressed(t *testing.T) {
	grew := qsStat(3)
	grew.BaselineValue, grew.BaselineExecCount, grew.Regression = 1000, 4, 1500
	fromZero := qsStat(4)
	fromZero.Regression = 2500
	growRow := "2026-10-30 11:30:00.000 2.500 1.000 4 1.500 150.00 SELECT 3"

	srv := qsMonitor(t)
	srv.qs.rows = []*gosmo.QSQueryStat{grew, fromZero}
	res, err := runQS(t, srv, "--regressed", "--since", "2026-10-30T08:00", "--until", "2026-10-30T12:00",
		"--metric", "cpu", "--min-exec", "3", "--min-regression-pct", "50")
	if err != nil {
		t.Fatal(err)
	}
	at := func(h int) time.Time { return time.Date(2026, 10, 30, h, 0, 0, 0, serverZone) }
	q := srv.qs.last()
	if !slices.Equal(srv.qs.read(), []string{"regressed"}) || q.Arg(0) != int64(25) ||
		!valueIs(q, "SUM(rs.avg_cpu_time * rs.count_executions) / NULLIF(SUM(rs.count_executions), 0)") ||
		!windowIs(q, 0, at(10), at(12)) || !windowIs(q, 1, at(8), at(10)) ||
		qsParam(q, qsMinExec) != int64(3) || qsParam(q, qsRegression) != float64(50) {
		t.Errorf("read %q:\n%s", srv.qs.read(), q)
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
	if want := []string{"1 3 NULL 10 2 NULL " + growRow}; !slices.Equal(cells(set), want) {
		t.Errorf("queries =\n%q\nwant\n%q", cells(set), want)
	}

	// No floor: growth from zero ranks first, with no percentage. --report
	// regressed is the same read.
	srv = qsMonitor(t)
	srv.qs.rows = []*gosmo.QSQueryStat{grew, fromZero}
	res, err = runQS(t, srv, "--report", "regressed", "--regressed")
	if err != nil || !slices.Equal(srv.qs.read(), []string{"regressed"}) || qsParam(srv.qs.last(), qsRegression) != nil {
		t.Fatalf("--report regressed --regressed: err %v, read %q", err, srv.qs.read())
	}
	want := []string{
		"1 4 NULL 10 2 NULL 2026-10-30 11:30:00.000 2.500 0.000 0 2.500 NULL SELECT 4",
		"2 3 NULL 10 2 NULL " + growRow,
	}
	if !slices.Equal(cells(res.Sets[0]), want) {
		t.Errorf("queries =\n%q\nwant\n%q", cells(res.Sets[0]), want)
	}
}

// Variation adds its coefficient; a metric counted in pages is not scaled.
func TestMonitorQueryStoreVariation(t *testing.T) {
	srv := qsMonitor(t)
	q := qsStat(5)
	q.Variation = 1.23456
	srv.qs.rows = []*gosmo.QSQueryStat{q}
	res, err := runQS(t, srv, "--report", "variation", "--metric", "logical-reads", "--stat", "max", "--top", "3")
	if err != nil {
		t.Fatal(err)
	}
	if got := srv.qs.last(); !slices.Equal(srv.qs.read(), []string{"variation"}) || got.Arg(0) != int64(3) ||
		!valueIs(got, "CAST(MAX(rs.max_logical_io_reads) AS float)") {
		t.Errorf("read %q:\n%s", srv.qs.read(), got)
	}
	if field(res, "unit") != "pages" || field(res, "statistic") != "max" {
		t.Errorf("fields %+v", res.Fields)
	}
	if got := cells(res.Sets[0]); !slices.Equal(got, []string{"1 5 NULL 10 2 NULL 2026-10-30 11:30:00.000 2500.000 1.2346 SELECT 5"}) {
		t.Errorf("queries = %q", got)
	}
	srv = qsMonitor(t)
	if _, err := runQS(t, srv, "--report", "forced"); err != nil || !slices.Equal(srv.qs.read(), []string{"forced"}) {
		t.Errorf("forced: err %v, read %q", err, srv.qs.read())
	}
}

// Waits is two reads: the categories, then the queries waiting in every
// category or the one named. Values are wait ms, unscaled.
func TestMonitorQueryStoreWaits(t *testing.T) {
	srv := qsMonitor(t)
	srv.qs.waits = []*gosmo.QSWaitStat{{Category: "Lock", ExecCount: 3, Value: 40}, {Category: "CPU", ExecCount: 9, Value: 120.5}}
	srv.qs.rows = []*gosmo.QSQueryStat{qsStat(1)}
	res, err := runQS(t, srv, "--report", "waits")
	if err != nil {
		t.Fatal(err)
	}
	q := srv.qs.last()
	if !slices.Equal(srv.qs.read(), []string{"categories", "waiting"}) || qsParam(q, qsCategory) != nil ||
		!has(q, "GROUP BY plan_id, runtime_stats_interval_id, execution_type") ||
		!valueIs(q, "CAST(SUM(ws.total_query_wait_time_ms) AS float)") {
		t.Errorf("read %q:\n%s", srv.qs.read(), q)
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
	srv = qsMonitor(t)
	if _, err := runQS(t, srv, "--report", "waits", "--wait-category", "Lock", "--stat", "max"); err != nil {
		t.Fatal(err)
	}
	if q := srv.qs.last(); qsParam(q, qsCategory) != "Lock" || !valueIs(q, "CAST(MAX(ws.max_query_wait_time_ms) AS float)") {
		t.Errorf("waiting read:\n%s", q)
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
		srv := qsMonitor(t)
		_, err := runQS(t, srv, argv...)
		if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUsageError {
			t.Errorf("%q: err = %v, want a usage error", argv, err)
		}
		if len(srv.qs.reads) != 0 {
			t.Errorf("%q: read before refusing", argv)
		}
	}
}

// Query Store off is exit 8 naming the way to turn it on, and so is a
// database with none; a metric the instance does not record is exit 11;
// neither reads a report. A report's own failure maps as every read's does.
func TestMonitorQueryStoreRefusals(t *testing.T) {
	srv := qsMonitor(t)
	srv.qs.state = gosmo.QueryStoreOff
	_, err := runQS(t, srv)
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeNotFound || len(srv.qs.reads) != 0 ||
		oe.Message != "Query Store is OFF in [Sales]" {
		t.Errorf("off: err = %v, read %q", err, srv.qs.read())
	}
	srv = qsMonitor(t)
	srv.qs.state = "" // master: no row
	_, err = runQS(t, srv)
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeNotFound || len(srv.qs.reads) != 0 {
		t.Errorf("master: err = %v, read %q", err, srv.qs.read())
	}
	srv = qsMonitor(t)
	srv.qs.state = gosmo.QueryStoreReadOnly // full, still readable
	if _, err := runQS(t, srv); err != nil || len(srv.qs.reads) != 1 {
		t.Errorf("read-only: err = %v, read %q", err, srv.qs.read())
	}

	// SQL Server 2016 records neither tempdb nor waits.
	srv = qsMonitor(t)
	srv.Info.ProductVersion = "13.0.6300.2"
	_, err = runQS(t, srv, "--metric", "tempdb")
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUnsupported || len(srv.qs.reads) != 0 {
		t.Errorf("tempdb on 2016: err = %v, read %q", err, srv.qs.read())
	}
	srv = qsMonitor(t)
	srv.Info.ProductVersion = "13.0.6300.2"
	_, err = runQS(t, srv, "--report", "waits")
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUnsupported || len(srv.qs.reads) != 0 {
		t.Errorf("waits on 2016: err = %v, read %q", err, srv.qs.read())
	}

	srv = qsMonitor(t)
	srv.qs.readErr = mssql.Error{Number: 300, Class: 14, Message: "The user does not have permission to perform this action."}
	_, err = runQS(t, srv)
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodePermissionDenied ||
		!strings.Contains(oe.Hint, "VIEW DATABASE STATE") {
		t.Errorf("no VIEW DATABASE STATE: err = %v", err)
	}
}
