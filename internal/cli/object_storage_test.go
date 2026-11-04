package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
)

// storageFake is objectFake plus object dependencies' edges and object
// indexes'/stats' reads. The uses graph has a cycle — usp_Get → vCustomer →
// Customer → fScalar → vCustomer — and Dependents is the same graph reversed.
// reads records each dependency read as "uses:schema.name".
type storageFake struct {
	*objectFake
	heap         bool // TableIndexUsage also returns index 0
	usageHidden  bool // as without VIEW SERVER STATE
	fragModes    []gosmo.FragmentationMode
	reads        []string
	statsColumns map[string][]string
	depErr       error // Dependents fails with it
}

func newStorageFake() *storageFake {
	return &storageFake{objectFake: newObjectFake(), statsColumns: map[string][]string{
		"PK_Customer": {"id"}, "_WA_Sys_00000002": {"CustomerName"}, "st_multi": {"CustomerName", "pct"},
	}}
}

type fakeEdge struct {
	from, to   [2]string
	desc       string
	bound, cal bool
}

var fakeEdges = []fakeEdge{
	{[2]string{"Sales", "usp_Get"}, [2]string{"Sales", "vCustomer"}, "VIEW", false, false},
	{[2]string{"Sales", "usp_Get"}, [2]string{"dbo", "fScalar"}, "SQL_SCALAR_FUNCTION", false, true},
	{[2]string{"Sales", "vCustomer"}, [2]string{"Sales", "Customer"}, "USER_TABLE", true, false},
	{[2]string{"Sales", "Customer"}, [2]string{"dbo", "fScalar"}, "SQL_SCALAR_FUNCTION", false, false},
	// A filtered index's reference to its own table.
	{[2]string{"Sales", "Customer"}, [2]string{"Sales", "Customer"}, "USER_TABLE", true, false},
	{[2]string{"dbo", "fScalar"}, [2]string{"Sales", "vCustomer"}, "VIEW", false, false},
}

// descOf is the type_desc of the object a fake edge names.
func (c *storageFake) descOf(o [2]string) string {
	for _, obj := range c.objects {
		if obj.Schema == o[0] && obj.Name == o[1] {
			return obj.TypeDesc
		}
	}
	return "USER_TABLE"
}

func (c *storageFake) Dependencies(_ context.Context, _ *gosmo.Database, schema, name string) ([]*gosmo.Dependency, error) {
	c.reads = append(c.reads, "uses:"+schema+"."+name)
	var out []*gosmo.Dependency
	for _, e := range fakeEdges {
		if e.from == [2]string{schema, name} {
			out = append(out, &gosmo.Dependency{Schema: e.to[0], Name: e.to[1], TypeDesc: e.desc, IsSchemaBound: e.bound, IsCallerDependent: e.cal})
		}
	}
	return out, nil
}

func (c *storageFake) Dependents(_ context.Context, _ *gosmo.Database, schema, name string) ([]*gosmo.Dependency, error) {
	c.reads = append(c.reads, "used-by:"+schema+"."+name)
	if c.depErr != nil {
		return nil, c.depErr
	}
	var out []*gosmo.Dependency
	for _, e := range fakeEdges {
		if e.to == [2]string{schema, name} {
			out = append(out, &gosmo.Dependency{Schema: e.from[0], Name: e.from[1], TypeDesc: c.descOf(e.from), IsSchemaBound: e.bound, IsCallerDependent: e.cal})
		}
	}
	return out, nil
}

var usageAt = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

// TableIndexes is fakeIndexes with index ids, and a hypothetical index with
// no usage row.
func (c *storageFake) TableIndexes(context.Context, *gosmo.Table) ([]*gosmo.Index, error) {
	var out []*gosmo.Index
	for i, idx := range fakeIndexes {
		cp := *idx
		cp.IndexID = i + 1
		out = append(out, &cp)
	}
	return append(out, &gosmo.Index{Name: "_dta_index", IndexID: 9, Type: gosmo.IndexTypeNonClustered, IsHypothetical: true,
		KeyColumns: []gosmo.IndexColumn{{Name: "pct"}}}), nil
}

func (c *storageFake) ViewIndexes(context.Context, *gosmo.View) ([]*gosmo.Index, error) {
	return []*gosmo.Index{{Name: "CIX_vCustomer", IndexID: 1, Type: gosmo.IndexTypeClustered, IsUnique: true,
		KeyColumns: []gosmo.IndexColumn{{Name: "id"}}}}, nil
}

func (c *storageFake) usage(ids ...int) []*gosmo.IndexUsage {
	var out []*gosmo.IndexUsage
	for _, id := range ids {
		u := &gosmo.IndexUsage{IndexID: id, UsageVisible: !c.usageHidden}
		if !c.usageHidden {
			u.UserSeeks, u.UserScans, u.UserUpdates = int64(10*id), int64(id), 7
			u.LastUserSeek, u.LastUserUpdate = usageAt, usageAt
		}
		out = append(out, u)
	}
	return out
}

func (c *storageFake) TableIndexUsage(context.Context, *gosmo.Table) ([]*gosmo.IndexUsage, error) {
	if c.heap {
		return c.usage(0, 2, 3), nil
	}
	return c.usage(1, 2, 3), nil
}

func (c *storageFake) ViewIndexUsage(context.Context, *gosmo.View) ([]*gosmo.IndexUsage, error) {
	return c.usage(1), nil
}

func (c *storageFake) fragmentation(mode gosmo.FragmentationMode, ids ...int) []*gosmo.IndexFragmentation {
	c.fragModes = append(c.fragModes, mode)
	var out []*gosmo.IndexFragmentation
	for _, id := range ids {
		out = append(out, &gosmo.IndexFragmentation{IndexID: id, AvgFragmentationPct: 12.346, PageCount: 100, FragmentCount: 4,
			AvgPageSpaceUsedPct: 88.5})
	}
	return out
}

func (c *storageFake) TableFragmentation(_ context.Context, _ *gosmo.Table, mode gosmo.FragmentationMode) ([]*gosmo.IndexFragmentation, error) {
	return c.fragmentation(mode, 1, 2), nil
}

func (c *storageFake) ViewFragmentation(_ context.Context, _ *gosmo.View, mode gosmo.FragmentationMode) ([]*gosmo.IndexFragmentation, error) {
	return c.fragmentation(mode, 1), nil
}

func (c *storageFake) TableStatistics(context.Context, *gosmo.Table) ([]*gosmo.Statistic, error) {
	return []*gosmo.Statistic{
		{Name: "PK_Customer", StatID: 1, LastUpdated: usageAt, TotalRows: 200, RowsSampled: 50, Steps: 120, ModificationCounter: 3},
		{Name: "_WA_Sys_00000002", StatID: 2, IsAutoCreated: true},
		{Name: "st_multi", StatID: 3, IsUserCreated: true, HasFilter: true, FilterDef: "([pct]>(0))", LastUpdated: usageAt,
			NoRecompute: true, ModificationCounter: 0},
	}, nil
}

func (c *storageFake) ViewStatistics(context.Context, *gosmo.View) ([]*gosmo.Statistic, error) {
	return []*gosmo.Statistic{{Name: "PK_Customer", StatID: 1, LastUpdated: usageAt, TotalRows: 10, RowsSampled: 10, Steps: 3}}, nil
}

func (c *storageFake) StatisticColumns(_ context.Context, st *gosmo.Statistic) ([]string, error) {
	return c.statsColumns[st.Name], nil
}

func runStorage(t *testing.T, cat *storageFake, argv ...string) (int, map[string]any) {
	t.Helper()
	r := runTree(t, context.Background(), newCommandTree(catalogDeps(t, cat)), append(append([]string{"object"}, argv...), "-p", "dev", "-o", "json")...)
	return r.exit, envelope(t, r.stdout)
}

func TestObjectDependencies(t *testing.T) {
	cat := newStorageFake()
	exit, env := runStorage(t, cat, "dependencies", "usp_Get", "--direction", "uses")
	if exit != 0 {
		t.Fatalf("exit %d: %v", exit, env)
	}
	if data := env["data"].(map[string]any); data["schema"] != "Sales" || data["direction"] != "uses" || data["depth"] != "1" {
		t.Errorf("data %v", data)
	}
	if got := setRows(t, env, 0); !slices.Equal(got, []string{
		"uses|1|[Sales].[usp_Get]|Sales|vCustomer|view|false|false",
		"uses|1|[Sales].[usp_Get]|dbo|fScalar|function|false|true",
	}) {
		t.Errorf("depth 1\n%s", strings.Join(got, "\n"))
	}
	if !slices.Equal(cat.reads, []string{"uses:Sales.usp_Get"}) {
		t.Errorf("reads %q", cat.reads)
	}

	// Breadth first; the edges back to vCustomer and fScalar are listed but
	// not followed again, so every object is read once and the walk ends
	// before depth 5.
	cat = newStorageFake()
	_, env = runStorage(t, cat, "dependencies", "Sales.usp_Get", "--direction", "uses", "--depth", "5")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{
		"uses|1|[Sales].[usp_Get]|Sales|vCustomer|view|false|false",
		"uses|1|[Sales].[usp_Get]|dbo|fScalar|function|false|true",
		"uses|2|[Sales].[vCustomer]|Sales|Customer|table|true|false",
		"uses|2|[dbo].[fScalar]|Sales|vCustomer|view|false|false",
		"uses|3|[Sales].[Customer]|dbo|fScalar|function|false|false",
	}) {
		t.Errorf("depth 5\n%s", strings.Join(got, "\n"))
	}
	if !slices.Equal(cat.reads, []string{"uses:Sales.usp_Get", "uses:Sales.vCustomer", "uses:dbo.fScalar", "uses:Sales.Customer"}) {
		t.Errorf("reads %q, want each object once", cat.reads)
	}

	// Default both: uses first, then used-by, each with its own walk.
	_, env = runStorage(t, newStorageFake(), "dependencies", "Sales.Customer", "--depth", "2")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{
		"uses|1|[Sales].[Customer]|dbo|fScalar|function|false|false",
		"uses|2|[dbo].[fScalar]|Sales|vCustomer|view|false|false",
		"used-by|1|[Sales].[Customer]|Sales|vCustomer|view|true|false",
		"used-by|2|[Sales].[vCustomer]|Sales|usp_Get|procedure|false|false",
		"used-by|2|[Sales].[vCustomer]|dbo|fScalar|function|false|false",
	}) {
		t.Errorf("both, depth 2\n%s", strings.Join(got, "\n"))
	}

	for _, depth := range []string{"0", "6"} {
		if exit, env := runStorage(t, newStorageFake(), "dependencies", "Sales.Customer", "--depth", depth); exit != 2 ||
			!strings.Contains(firstError(t, env)["message"].(string), "--depth") {
			t.Errorf("--depth %s: exit %d, %v", depth, exit, env["errors"])
		}
	}
	// Denied: exit 9, with the permission the read takes as the hint.
	cat = newStorageFake()
	cat.depErr = fmt.Errorf("gosmo: dependencies: %w", mssql.Error{Number: 229, Class: 14, Message: "SELECT denied"})
	if exit, env := runStorage(t, cat, "dependencies", "Sales.Customer"); exit != 9 ||
		!strings.Contains(fmt.Sprint(firstError(t, env)["hint"]), "sys.sql_expression_dependencies") {
		t.Errorf("denied: exit %d, %v", exit, env["errors"])
	}
	if exit, _ := runStorage(t, newStorageFake(), "dependencies", "Sales.Nope"); exit != 8 {
		t.Errorf("missing object: exit %d, want 8", exit)
	}
}

func TestObjectIndexes(t *testing.T) {
	exit, env := runStorage(t, newStorageFake(), "indexes", "Sales.Customer")
	if exit != 0 {
		t.Fatalf("exit %d: %v", exit, env)
	}
	if w := env["warnings"].([]any); len(w) != 0 {
		t.Errorf("warnings %v", env["warnings"])
	}
	at := "2026-10-01 08:00:00.000"
	if got := setRows(t, env, 0); !slices.Equal(got, []string{
		"PK_Customer|1|CLUSTERED|true|true|id|NULL|NULL|false|false|10|1|0|7|" + at + "|NULL|NULL|" + at,
		"IX_Customer_Name|2|NONCLUSTERED|false|false|CustomerName DESC|pct|([pct]>(0))|false|false|20|2|0|7|" + at + "|NULL|NULL|" + at,
		"UQ_Customer_Name|3|NONCLUSTERED|true|false|CustomerName|NULL|NULL|false|false|30|3|0|7|" + at + "|NULL|NULL|" + at,
		"_dta_index|9|NONCLUSTERED|false|false|pct|NULL|NULL|false|true|NULL|NULL|NULL|NULL|NULL|NULL|NULL|NULL",
	}) {
		t.Errorf("indexes\n%s", strings.Join(got, "\n"))
	}

	// A heap leads as index 0; usage the login may not read is NULL, warned.
	cat := newStorageFake()
	cat.heap, cat.usageHidden = true, true
	_, env = runStorage(t, cat, "indexes", "Sales.Customer")
	got := setRows(t, env, 0)
	if got[0] != "NULL|0|HEAP|false|false|NULL|NULL|NULL|false|false|NULL|NULL|NULL|NULL|NULL|NULL|NULL|NULL" ||
		!strings.HasSuffix(got[2], "|false|false|NULL|NULL|NULL|NULL|NULL|NULL|NULL|NULL") {
		t.Errorf("heap, hidden usage\n%s", strings.Join(got, "\n"))
	}
	if !strings.Contains(fmt.Sprint(env["warnings"]), "VIEW SERVER STATE") {
		t.Errorf("warnings %v", env["warnings"])
	}

	// Fragmentation only when asked, in the mode given; LIMITED has no page
	// fullness; an index the scan has no row for reads NULL.
	cat = newStorageFake()
	_, env = runStorage(t, cat, "indexes", "Sales.Customer")
	if len(cat.fragModes) != 0 || env["data"].(map[string]any)["fragmentationMode"] != nil ||
		len(strings.Split(setRows(t, env, 0)[0], "|")) != 18 {
		t.Errorf("fragmentation read without --fragmentation: %v", cat.fragModes)
	}
	_, env = runStorage(t, cat, "indexes", "Sales.Customer", "--fragmentation", "limited")
	got = setRows(t, env, 0)
	if !slices.Equal(cat.fragModes, []gosmo.FragmentationMode{gosmo.FragmentationLimited}) ||
		!strings.HasSuffix(got[0], "|12.35|100|4|NULL") || !strings.HasSuffix(got[2], at+"|NULL|NULL|NULL|NULL") ||
		env["data"].(map[string]any)["fragmentationMode"] != "LIMITED" {
		t.Errorf("limited: modes %v\n%s", cat.fragModes, strings.Join(got, "\n"))
	}
	_, env = runStorage(t, cat, "indexes", "Sales.vCustomer", "--fragmentation", "detailed")
	if got := setRows(t, env, 0); cat.fragModes[1] != gosmo.FragmentationDetailed ||
		!slices.Equal(got, []string{"CIX_vCustomer|1|CLUSTERED|true|false|id|NULL|NULL|false|false|10|1|0|7|" + at + "|NULL|NULL|" + at + "|12.35|100|4|88.50"}) {
		t.Errorf("view, detailed\n%s", strings.Join(got, "\n"))
	}

	if exit, env := runStorage(t, newStorageFake(), "indexes", "Sales.usp_Get"); exit != 2 ||
		!strings.Contains(firstError(t, env)["message"].(string), "is a procedure, which has no indexes") {
		t.Errorf("procedure: exit %d, %v", exit, env["errors"])
	}
}

func TestObjectStats(t *testing.T) {
	exit, env := runStorage(t, newStorageFake(), "stats", "Sales.Customer")
	if exit != 0 {
		t.Fatalf("exit %d: %v", exit, env)
	}
	at := "2026-10-01 08:00:00.000"
	// A statistic never computed has its properties NULL, not zero.
	if got := setRows(t, env, 0); !slices.Equal(got, []string{
		"PK_Customer|1|id|index|NULL|" + at + "|200|50|25.00|120|3|false|false",
		"_WA_Sys_00000002|2|CustomerName|auto|NULL|NULL|NULL|NULL|NULL|NULL|NULL|false|false",
		"st_multi|3|CustomerName, pct|user|([pct]>(0))|" + at + "|0|0|NULL|0|0|true|false",
	}) {
		t.Errorf("stats\n%s", strings.Join(got, "\n"))
	}
	if w := fmt.Sprint(env["warnings"]); !strings.Contains(w, "1 of 3 statistics show no properties") {
		t.Errorf("warnings %s", w)
	}
	_, env = runStorage(t, newStorageFake(), "stats", "vCustomer")
	if w := env["warnings"].([]any); len(w) != 0 {
		t.Errorf("view stats warnings %v", w)
	}
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"PK_Customer|1|id|index|NULL|" + at + "|10|10|100.00|3|0|false|false"}) {
		t.Errorf("view stats %q", got)
	}
	if exit, _ := runStorage(t, newStorageFake(), "stats", "dbo.sn"); exit != 2 {
		t.Errorf("synonym: exit %d, want 2", exit)
	}
}
