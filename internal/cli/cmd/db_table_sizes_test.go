package cmd

import (
	"errors"
	"github.com/radix29/gossms/internal/cli/args"
	"slices"
	"strings"
	"testing"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/clitest"
	"github.com/radix29/gossms/internal/cli/output"
)

// tablesServer is storageServer answering db table-sizes' read inside
// each database.
type tablesServer struct {
	*storageServer
	tables map[string][]*gosmo.TableSize
	err    error // every TableSizes read's error, when set
}

// tablesRead is the databases TableSizes was read in.
func (s *tablesServer) tablesRead() []string { return s.readIn("WITH alloc AS") }

func newTablesServer(t *testing.T) *tablesServer {
	s := &tablesServer{
		storageServer: newStorageServer(t),
		tables: map[string][]*gosmo.TableSize{
			"app": {
				{Schema: "dbo", Name: "Big", Rows: 100, ReservedKB: 10240, DataKB: 8000, IndexKB: 2000, UnusedKB: 240},
				{Schema: "dbo", Name: "Empty", Rows: 0},
				{Schema: "dbo", Name: "Hot", Rows: 5000, IsMemoryOptimized: true},
				{Schema: "Sales", Name: "Detail", Rows: 9000, ReservedKB: 4096, DataKB: 1024, IndexKB: 2896, UnusedKB: 176},
				{Schema: "Sales", Name: "Header", Rows: 900, ReservedKB: 4096, DataKB: 3000, IndexKB: 1000, UnusedKB: 96},
			},
			"app_snap": {
				{Schema: "dbo", Name: "Big", Rows: 90, ReservedKB: 9216, DataKB: 7000, IndexKB: 2000, UnusedKB: 216},
			},
		},
	}
	// In gosmo's schema-and-name order: the handler sorts and cuts.
	answer(s.Fake, func(q gosmotest.Query) ([][]any, error) {
		if s.err != nil {
			return nil, s.err
		}
		var rows [][]any
		for _, ts := range s.tables[q.Database] {
			rows = append(rows, clitest.TableSizeRow(ts))
		}
		return rows, nil
	}, "WITH alloc AS")
	return s
}

// runDBTableSizes runs db table-sizes over s with argv after "db
// table-sizes".
func runDBTableSizes(t *testing.T, s *tablesServer, argv string) (*output.Result, error) {
	t.Helper()
	return runStorage(t, s.Fake, "table-sizes", argv)
}

// tableNames is set's schema.table cells.
func tableNames(set output.Set) []string {
	schemas, tables := column(set, "schema"), column(set, "table")
	out := make([]string, len(tables))
	for i := range tables {
		out[i] = schemas[i] + "." + tables[i]
	}
	return out
}

func TestDBTableSizesDefault(t *testing.T) {
	res, err := runDBTableSizes(t, newTablesServer(t), "app")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	var names []string
	for _, c := range set.Columns {
		names = append(names, c.Name)
	}
	if want := []string{"schema", "table", "rows", "reserved_mb", "data_mb", "index_mb", "unused_mb"}; !slices.Equal(names, want) {
		t.Errorf("columns %q, want %q", names, want)
	}
	// reserved, largest first; Detail and Header tie on 4096 and go by name;
	// the memory-optimized table, with no sizes, last.
	if got, want := tableNames(set), []string{"dbo.Big", "Sales.Detail", "Sales.Header", "dbo.Empty", "dbo.Hot"}; !slices.Equal(got, want) {
		t.Errorf("order %q, want %q", got, want)
	}
	for col, want := range map[string][]string{
		"rows":        {"100", "9000", "900", "0", "5000"},
		"reserved_mb": {"10.00", "4.00", "4.00", "0.00", "NULL"},
		"data_mb":     {"7.81", "1.00", "2.93", "0.00", "NULL"},
		"index_mb":    {"1.95", "2.83", "0.98", "0.00", "NULL"},
		"unused_mb":   {"0.23", "0.17", "0.09", "0.00", "NULL"},
	} {
		if got := column(set, col); !slices.Equal(got, want) {
			t.Errorf("%s %q, want %q", col, got, want)
		}
	}
	if res.Database != "app" || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "[dbo].[Hot]: a memory-optimized table") {
		t.Errorf("database %q, warnings %q", res.Database, res.Warnings)
	}
}

func TestDBTableSizesSortTopSchema(t *testing.T) {
	for argv, want := range map[string][]string{
		"app --sort-by rows":                      {"Sales.Detail", "dbo.Hot", "Sales.Header", "dbo.Big", "dbo.Empty"},
		"app --sort-by data":                      {"dbo.Big", "Sales.Header", "Sales.Detail", "dbo.Empty", "dbo.Hot"},
		"app --sort-by index":                     {"Sales.Detail", "dbo.Big", "Sales.Header", "dbo.Empty", "dbo.Hot"},
		"app --sort-by unused":                    {"dbo.Big", "Sales.Detail", "Sales.Header", "dbo.Empty", "dbo.Hot"},
		"app --top 2":                             {"dbo.Big", "Sales.Detail"},
		"app --top 0":                             {"dbo.Big", "Sales.Detail", "Sales.Header", "dbo.Empty", "dbo.Hot"},
		"app --schema sales":                      {"Sales.Detail", "Sales.Header"},
		"app --schema Sales --top 1":              {"Sales.Detail"},
		"app --schema nope":                       {},
		"app --sort-by rows --top 1 --schema dbo": {"dbo.Hot"},
	} {
		res, err := runDBTableSizes(t, newTablesServer(t), argv)
		if err != nil {
			t.Fatalf("%q: %v", argv, err)
		}
		set := res.Sets[0]
		if got := tableNames(set); !slices.Equal(got, want) {
			t.Errorf("%q: %q, want %q", argv, got, want)
		}
		if set.TotalRowCount != len(want) || set.Truncated {
			t.Errorf("%q: total %d, truncated %v", argv, set.TotalRowCount, set.Truncated)
		}
		// The memory-optimized warning only for a row shown.
		shown := slices.Contains(want, "dbo.Hot")
		if warned := strings.Contains(strings.Join(res.Warnings, "\n"), "[dbo].[Hot]"); warned != shown {
			t.Errorf("%q: Hot shown %v, warned %v: %q", argv, shown, warned, res.Warnings)
		}
	}
}

// --unit kb prints sp_spaceused's own KB figures.
func TestDBTableSizesUnitKB(t *testing.T) {
	res, err := runDBTableSizes(t, newTablesServer(t), "app --unit kb --top 1")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	for col, want := range map[string]string{"reserved_kb": "10240", "data_kb": "8000", "index_kb": "2000", "unused_kb": "240"} {
		if got := column(set, col); !slices.Equal(got, []string{want}) {
			t.Errorf("%s %q, want %q", col, got, want)
		}
	}
	if i := slices.IndexFunc(set.Columns, func(c output.Column) bool { return c.Name == "reserved_kb" }); set.Columns[i].Type != "bigint" {
		t.Errorf("reserved_kb type %q", set.Columns[i].Type)
	}
}

func TestDBTableSizesSnapshot(t *testing.T) {
	res, err := runDBTableSizes(t, newTablesServer(t), "app_snap")
	if err != nil {
		t.Fatal(err)
	}
	if got := tableNames(res.Sets[0]); !slices.Equal(got, []string{"dbo.Big"}) {
		t.Errorf("rows %q", got)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "[app_snap]: a database snapshot") {
		t.Errorf("warnings %q", res.Warnings)
	}
}

func TestDBTableSizesRefusals(t *testing.T) {
	for argv, want := range map[string]output.Code{
		"archive": output.CodeNotFound,
		"hr":      output.CodePermissionDenied,
		"nope":    output.CodeNotFound,
	} {
		s := newTablesServer(t)
		_, err := runDBTableSizes(t, s, argv)
		if e, ok := errors.AsType[*output.Error](err); !ok || e.Code != want {
			t.Errorf("%q: %v, want %s", argv, err, want)
		}
		if len(s.tablesRead()) != 0 {
			t.Errorf("%q: TableSizes read for %q", argv, s.tablesRead())
		}
	}
	s := newTablesServer(t)
	s.err = mssql.Error{Number: 229, Class: 14, Message: "The SELECT permission was denied on the object 'partitions', database 'mssqlsystemresource', schema 'sys'."}
	_, err := runDBTableSizes(t, s, "app")
	if e, ok := errors.AsType[*output.Error](err); !ok || e.Code != output.CodePermissionDenied {
		t.Errorf("refused TableSizes: %v, want %s", err, output.CodePermissionDenied)
	}
	s.err = errors.New("boom")
	if _, err := runDBTableSizes(t, s, "app"); err == nil {
		t.Error("a failed TableSizes succeeded")
	}
}

func TestDBTableSizesUsage(t *testing.T) {
	for _, argv := range []string{"", "app --all", "app --sort-by name", "app --top -1"} {
		_, err := runDBTableSizes(t, newTablesServer(t), argv)
		// The parser refuses the first three, the handler the last.
		_, parse := errors.AsType[*args.Error](err)
		e, ok := errors.AsType[*output.Error](err)
		if !parse && (!ok || e.Code != output.CodeUsageError) {
			t.Errorf("%q: %v, want a usage error", argv, err)
		}
	}
}
