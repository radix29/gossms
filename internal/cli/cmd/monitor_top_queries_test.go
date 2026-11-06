package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/showplan"
)

var cachedAt = time.Date(2026, 10, 30, 8, 0, 0, 0, time.UTC)

func cachedQuery() *gosmo.CachedQueryStat {
	return &gosmo.CachedQueryStat{
		Database: "Sales", QueryHash: []byte{0xAB, 0xCD, 0, 0, 0, 0, 0, 1}, PlanHandle: []byte{0x06, 0, 0x05, 0xFF},
		StatementStart: 112, StatementEnd: -1, Text: "SELECT * FROM t WHERE id = @1",
		ExecutionCount: 4, TotalCPUMicros: 10_001, TotalDurationMicros: 20_000,
		TotalLogicalReads: 9, TotalLogicalWrites: 0,
		LastExecutionTime: cachedAt.Add(time.Hour), CreationTime: cachedAt,
	}
}

// Microseconds come out as milliseconds, each total beside its average; the
// handles as T-SQL binary literals plan cached reads back; a statement whose
// batch left the cache has NULL text and database. --plan-database keeps the
// plans compiled in it, by its id.
func TestMonitorTopQueries(t *testing.T) {
	gone := &gosmo.CachedQueryStat{PlanHandle: []byte{1}, StatementEnd: -1, LastExecutionTime: cachedAt, CreationTime: cachedAt}
	srv := newMonitorServer(t)
	srv.queries = []*gosmo.CachedQueryStat{gone, cachedQuery()}
	res, err := runMonitor(t, srv, "top-queries", "--by", "reads", "--avg", "--top", "5")
	if err != nil {
		t.Fatal(err)
	}
	if q := lastQuery(srv, topRead); q.Arg(0) != int64(5) || has(q, "@p2") ||
		!has(q, "ORDER  BY CAST(qs.total_logical_reads AS float) / NULLIF(qs.execution_count, 0) DESC") {
		t.Errorf("the ranking's query:\n%s", q)
	}
	if field(res, "rankedBy") != "reads" || field(res, "ranking") != "average" {
		t.Errorf("fields = %+v", res.Fields)
	}
	sales := "1 Sales 4 10.001 2.500 20.000 5.000 9 2.25 0 0.00 2026-10-30 09:00:00.000 2026-10-30 08:00:00.000 " +
		"0xABCD000000000001 0x060005FF 112 -1 SELECT * FROM t WHERE id = @1"
	want := []string{sales,
		"2 NULL 0 0.000 0.000 0.000 0.000 0 0.00 0 0.00 2026-10-30 08:00:00.000 2026-10-30 08:00:00.000 NULL 0x01 0 -1 NULL",
	}
	if res.Sets[0].Name != "queries" || !slices.Equal(cells(res.Sets[0]), want) {
		t.Errorf("queries =\n%q\nwant\n%q", cells(res.Sets[0]), want)
	}
	if len(res.Sets[0].Columns) != len(res.Sets[0].Rows[0]) {
		t.Errorf("%d columns, %d cells", len(res.Sets[0].Columns), len(res.Sets[0].Rows[0]))
	}

	res, err = runMonitor(t, srv, "top-queries", "--by", "reads", "--avg", "--top", "5", "--plan-database", "sales")
	if err != nil {
		t.Fatal(err)
	}
	if q := lastQuery(srv, topRead); q.Arg(1) != int64(5) {
		t.Errorf("--plan-database sales: the ranking's query:\n%s", q)
	}
	if got := cells(res.Sets[0]); !slices.Equal(got, []string{sales}) {
		t.Errorf("--plan-database sales: queries = %q", got)
	}
}

// lastQuery is the last query s was sent containing sub.
func lastQuery(s *monitorServer, sub string) gosmotest.Query {
	var last gosmotest.Query
	for _, q := range s.Queries() {
		if has(q, sub) {
			last = q
		}
	}
	return last
}

func TestMonitorTopQueriesDefaults(t *testing.T) {
	srv := newMonitorServer(t)
	res, err := runMonitor(t, srv, "top-queries")
	if err != nil {
		t.Fatal(err)
	}
	if q := lastQuery(srv, topRead); q.Arg(0) != int64(20) || !has(q, "ORDER  BY qs.total_worker_time DESC") {
		t.Errorf("the ranking's query:\n%s", q)
	}
	if field(res, "ranking") != "total" || len(res.Sets[0].Rows) != 0 {
		t.Errorf("fields %+v, rows %q", res.Fields, cells(res.Sets[0]))
	}
}

func TestMonitorTopQueriesUsage(t *testing.T) {
	for _, argv := range [][]string{{"--by", "executions", "--avg"}, {"--top", "0"}, {"--top", "-3"}} {
		srv := newMonitorServer(t)
		_, err := runMonitor(t, srv, append([]string{"top-queries"}, argv...)...)
		if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUsageError {
			t.Errorf("%q: err = %v, want a usage error", argv, err)
		}
		if srv.reads(topRead) != 0 {
			t.Errorf("%q: read before refusing", argv)
		}
	}
}

// An unknown --plan-database (DB_ID NULL) is exit 8, and nothing is ranked.
func TestMonitorTopQueriesUnknownDatabase(t *testing.T) {
	srv := newMonitorServer(t)
	_, err := runMonitor(t, srv, "top-queries", "--plan-database", "Nope")
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeNotFound || srv.reads(topRead) != 0 {
		t.Errorf("err = %v, %d rankings; want NOT_FOUND and none", err, srv.reads(topRead))
	}
}

func estimatedPlanDoc(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "showplan", "testdata", "estimated_plan.xml"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := showplan.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return p.XML
}

// plan cached reads the plan of the handle and offsets given and renders the
// plan as plan estimated does; --plan-file gets the document.
func TestPlanCached(t *testing.T) {
	doc := estimatedPlanDoc(t)
	file := filepath.Join(t.TempDir(), "q.sqlplan")
	srv := newMonitorServer(t)
	srv.plans["0x060005FF 112 418"] = doc
	res, err := runGroup(t, srv, "plan", "cached", "0x060005ff", "--statement-start", "112", "--statement-end", "418",
		"--plan-file", file)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"planType": "cached", "planHandle": "0x060005FF", "statementStart": "112",
		"statementEnd": "418", "planFile": file} {
		if got := field(res, k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	var names []string
	for _, s := range res.Sets {
		names = append(names, s.Name)
	}
	if !slices.Equal(names, []string{"statements", "operators", "missing_indexes", "warnings"}) || len(res.Sets[0].Rows) == 0 {
		t.Errorf("sets = %q", names)
	}
	if got, _ := os.ReadFile(file); string(got) != doc {
		t.Errorf("--plan-file holds %d bytes, want the plan's %d", len(got), len(doc))
	}

	// No 0x, and the default offsets: the whole batch.
	srv = newMonitorServer(t)
	srv.plans["0x060005FF 0 -1"] = doc
	if _, err := runGroup(t, srv, "plan", "cached", "060005FF", "--plan-format", "xml"); err != nil {
		t.Fatal(err)
	}
}

// A plan no longer cached is exit 8 with a hint to rank again, and
// --plan-file is not written.
func TestPlanCachedGone(t *testing.T) {
	file := filepath.Join(t.TempDir(), "q.sqlplan")
	res, err := runGroup(t, newMonitorServer(t), "plan", "cached", "0x0600", "--plan-file", file)
	oe, ok := errors.AsType[*output.Error](err)
	if !ok || oe.Code != output.CodeNotFound || !strings.Contains(oe.Hint, "monitor top-queries") {
		t.Errorf("err = %v, want NOT_FOUND hinting top-queries", err)
	}
	if _, serr := os.Stat(file); serr == nil {
		t.Error("--plan-file was written")
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "not written") {
		t.Errorf("warnings = %q", res.Warnings)
	}
}

func TestPlanCachedUsage(t *testing.T) {
	for _, argv := range [][]string{
		{"0xZZ"}, {"0x"}, {"0x" + strings.Repeat("00", 65)}, {"0x0600", "--statement-start", "-1"},
		{"0x0600", "--statement-end", "-2"}, {"0x0600", "--statement-start", "50", "--statement-end", "10"},
		{"0x0600", "--top", "-1"},
	} {
		srv := newMonitorServer(t)
		_, err := runGroup(t, srv, "plan", append([]string{"cached"}, argv...)...)
		if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUsageError {
			t.Errorf("%q: err = %v, want a usage error", argv, err)
		}
		if srv.reads(planRead) != 0 {
			t.Errorf("%q: read before refusing", argv)
		}
	}
}

// Without VIEW SERVER STATE plan cached is exit 9 before reading the plan:
// the DMV would refuse with Msg 300 anyway, and the monitor reads answer so.
func TestPlanCachedNeedsViewServerState(t *testing.T) {
	srv := newMonitorServer(t)
	srv.noVSS = true
	_, err := runGroup(t, srv, "plan", "cached", "0x0600")
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodePermissionDenied {
		t.Errorf("err = %v, want PERMISSION_DENIED", err)
	}
	if srv.reads(planRead) != 0 {
		t.Error("read the plan without the permission")
	}
}
