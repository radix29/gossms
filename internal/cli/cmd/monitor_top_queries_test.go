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
// batch left the cache has NULL text and database.
func TestMonitorTopQueries(t *testing.T) {
	gone := &gosmo.CachedQueryStat{PlanHandle: []byte{1}, StatementEnd: -1, LastExecutionTime: cachedAt, CreationTime: cachedAt}
	cat := &monitorCatalog{queries: []*gosmo.CachedQueryStat{cachedQuery(), gone}}
	res, err := runMonitor(t, cat, "top-queries", "--by", "reads", "--avg", "--top", "5", "--plan-database", "Sales")
	if err != nil {
		t.Fatal(err)
	}
	if want := (gosmo.TopQueriesOptions{By: gosmo.TopByLogicalReads, Average: true, Top: 5, Database: "Sales"}); *cat.gotTopOps != want {
		t.Errorf("options = %+v, want %+v", *cat.gotTopOps, want)
	}
	if field(res, "rankedBy") != "reads" || field(res, "ranking") != "average" {
		t.Errorf("fields = %+v", res.Fields)
	}
	want := []string{
		"1 Sales 4 10.001 2.500 20.000 5.000 9 2.25 0 0.00 2026-10-30 09:00:00.000 2026-10-30 08:00:00.000 " +
			"0xABCD000000000001 0x060005FF 112 -1 SELECT * FROM t WHERE id = @1",
		"2 NULL 0 0.000 0.000 0.000 0.000 0 0.00 0 0.00 2026-10-30 08:00:00.000 2026-10-30 08:00:00.000 NULL 0x01 0 -1 NULL",
	}
	if res.Sets[0].Name != "queries" || !slices.Equal(cells(res.Sets[0]), want) {
		t.Errorf("queries =\n%q\nwant\n%q", cells(res.Sets[0]), want)
	}
	if len(res.Sets[0].Columns) != len(res.Sets[0].Rows[0]) {
		t.Errorf("%d columns, %d cells", len(res.Sets[0].Columns), len(res.Sets[0].Rows[0]))
	}
}

func TestMonitorTopQueriesDefaults(t *testing.T) {
	cat := &monitorCatalog{}
	res, err := runMonitor(t, cat, "top-queries")
	if err != nil {
		t.Fatal(err)
	}
	if want := (gosmo.TopQueriesOptions{By: gosmo.TopByCPU, Top: 20}); *cat.gotTopOps != want {
		t.Errorf("options = %+v, want %+v", *cat.gotTopOps, want)
	}
	if field(res, "ranking") != "total" || len(res.Sets[0].Rows) != 0 {
		t.Errorf("fields %+v, rows %q", res.Fields, cells(res.Sets[0]))
	}
}

func TestMonitorTopQueriesUsage(t *testing.T) {
	for _, argv := range [][]string{{"--by", "executions", "--avg"}, {"--top", "0"}, {"--top", "-3"}} {
		cat := &monitorCatalog{}
		_, err := runMonitor(t, cat, append([]string{"top-queries"}, argv...)...)
		if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUsageError {
			t.Errorf("%q: err = %v, want a usage error", argv, err)
		}
		if cat.gotTopOps != nil {
			t.Errorf("%q: read before refusing", argv)
		}
	}
}

// An unknown --plan-database is gosmo's ErrNotFound: exit 8.
func TestMonitorTopQueriesUnknownDatabase(t *testing.T) {
	cat := &monitorCatalog{topErr: errors.Join(gosmo.ErrNotFound, errors.New(`database "Nope" not found`))}
	_, err := runMonitor(t, cat, "top-queries", "--plan-database", "Nope")
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeNotFound {
		t.Errorf("err = %v, want NOT_FOUND", err)
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

// plan cached hands the handle and offsets to CachedPlan and renders the
// plan as plan estimated does; --plan-file gets the document.
func TestPlanCached(t *testing.T) {
	doc := estimatedPlanDoc(t)
	file := filepath.Join(t.TempDir(), "q.sqlplan")
	cat := &monitorCatalog{plan: doc}
	res, err := runGroup(t, cat, "plan", "cached", "0x060005ff", "--statement-start", "112", "--statement-end", "418",
		"--plan-file", file)
	if err != nil {
		t.Fatal(err)
	}
	if cat.gotPlan != "0x060005FF 112 418" {
		t.Errorf("CachedPlan got %s", cat.gotPlan)
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
	cat = &monitorCatalog{plan: doc}
	if _, err := runGroup(t, cat, "plan", "cached", "060005FF", "--plan-format", "xml"); err != nil {
		t.Fatal(err)
	}
	if cat.gotPlan != "0x060005FF 0 -1" {
		t.Errorf("CachedPlan got %s", cat.gotPlan)
	}
}

// A plan no longer cached is exit 8 with a hint to rank again, and
// --plan-file is not written.
func TestPlanCachedGone(t *testing.T) {
	file := filepath.Join(t.TempDir(), "q.sqlplan")
	cat := &monitorCatalog{planErr: errors.Join(gosmo.ErrNotFound, errors.New("no plan in cache"))}
	res, err := runGroup(t, cat, "plan", "cached", "0x0600", "--plan-file", file)
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
		cat := &monitorCatalog{}
		_, err := runGroup(t, cat, "plan", append([]string{"cached"}, argv...)...)
		if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodeUsageError {
			t.Errorf("%q: err = %v, want a usage error", argv, err)
		}
		if cat.gotPlan != "" {
			t.Errorf("%q: read before refusing", argv)
		}
	}
}

// Without VIEW SERVER STATE plan cached is exit 9 before reading the plan:
// the DMV would refuse with Msg 300 anyway, and the monitor reads answer so.
func TestPlanCachedNeedsViewServerState(t *testing.T) {
	cat := &monitorCatalog{noVSS: true}
	_, err := runGroup(t, cat, "plan", "cached", "0x0600")
	if oe, ok := errors.AsType[*output.Error](err); !ok || oe.Code != output.CodePermissionDenied {
		t.Errorf("err = %v, want PERMISSION_DENIED", err)
	}
	if cat.gotPlan != "" {
		t.Error("read the plan without the permission")
	}
}
