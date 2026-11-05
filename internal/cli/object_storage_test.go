package cli

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestObjectDependencies(t *testing.T) {
	cat := newObjectServer(t)
	exit, env := runObject(t, cat, "dependencies", "usp_Get", "--direction", "uses")
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
	if !slices.Equal(cat.recorded(&cat.reads), []string{"uses:Sales.usp_Get"}) {
		t.Errorf("reads %q", cat.recorded(&cat.reads))
	}

	// Breadth first; the edges back to vCustomer and fScalar are listed but
	// not followed again, so every object is read once and the walk ends
	// before depth 5.
	cat = newObjectServer(t)
	_, env = runObject(t, cat, "dependencies", "Sales.usp_Get", "--direction", "uses", "--depth", "5")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{
		"uses|1|[Sales].[usp_Get]|Sales|vCustomer|view|false|false",
		"uses|1|[Sales].[usp_Get]|dbo|fScalar|function|false|true",
		"uses|2|[Sales].[vCustomer]|Sales|Customer|table|true|false",
		"uses|2|[dbo].[fScalar]|Sales|vCustomer|view|false|false",
		"uses|3|[Sales].[Customer]|dbo|fScalar|function|false|false",
	}) {
		t.Errorf("depth 5\n%s", strings.Join(got, "\n"))
	}
	if !slices.Equal(cat.recorded(&cat.reads), []string{"uses:Sales.usp_Get", "uses:Sales.vCustomer", "uses:dbo.fScalar", "uses:Sales.Customer"}) {
		t.Errorf("reads %q, want each object once", cat.recorded(&cat.reads))
	}

	// Default both: uses first, then used-by, each with its own walk.
	_, env = runObject(t, newObjectServer(t), "dependencies", "Sales.Customer", "--depth", "2")
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
		if exit, env := runObject(t, newObjectServer(t), "dependencies", "Sales.Customer", "--depth", depth); exit != 2 ||
			!strings.Contains(firstError(t, env)["message"].(string), "--depth") {
			t.Errorf("--depth %s: exit %d, %v", depth, exit, env["errors"])
		}
	}
	// Denied: exit 9, with the permission the read takes as the hint.
	cat = newObjectServer(t)
	cat.depErr = deniedDependents
	if exit, env := runObject(t, cat, "dependencies", "Sales.Customer"); exit != 9 ||
		!strings.Contains(fmt.Sprint(firstError(t, env)["hint"]), "sys.sql_expression_dependencies") {
		t.Errorf("denied: exit %d, %v", exit, env["errors"])
	}
	if exit, _ := runObject(t, newObjectServer(t), "dependencies", "Sales.Nope"); exit != 8 {
		t.Errorf("missing object: exit %d, want 8", exit)
	}
}

func TestObjectIndexes(t *testing.T) {
	exit, env := runObject(t, newObjectServer(t), "indexes", "Sales.Customer")
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
	cat := newObjectServer(t)
	cat.heap, cat.usageHidden = true, true
	_, env = runObject(t, cat, "indexes", "Sales.Customer")
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
	cat = newObjectServer(t)
	_, env = runObject(t, cat, "indexes", "Sales.Customer")
	if len(cat.recorded(&cat.modes)) != 0 || env["data"].(map[string]any)["fragmentationMode"] != nil ||
		len(strings.Split(setRows(t, env, 0)[0], "|")) != 18 {
		t.Errorf("fragmentation read without --fragmentation: %v", cat.recorded(&cat.modes))
	}
	_, env = runObject(t, cat, "indexes", "Sales.Customer", "--fragmentation", "limited")
	got = setRows(t, env, 0)
	if !slices.Equal(cat.recorded(&cat.modes), []string{"LIMITED"}) ||
		!strings.HasSuffix(got[0], "|12.35|100|4|NULL") || !strings.HasSuffix(got[2], at+"|NULL|NULL|NULL|NULL") ||
		env["data"].(map[string]any)["fragmentationMode"] != "LIMITED" {
		t.Errorf("limited: modes %v\n%s", cat.recorded(&cat.modes), strings.Join(got, "\n"))
	}
	_, env = runObject(t, cat, "indexes", "Sales.vCustomer", "--fragmentation", "detailed")
	if got := setRows(t, env, 0); cat.recorded(&cat.modes)[1] != "DETAILED" ||
		!slices.Equal(got, []string{"CIX_vCustomer|1|CLUSTERED|true|false|id|NULL|NULL|false|false|10|1|0|7|" + at + "|NULL|NULL|" + at + "|12.35|100|4|88.50"}) {
		t.Errorf("view, detailed\n%s", strings.Join(got, "\n"))
	}

	// A filter the login cannot read reads NULL, as no filter does: the
	// warning names the index.
	cat = newObjectServer(t)
	cat.hidden = true
	_, env = runObject(t, cat, "indexes", "Sales.Customer")
	if got := setRows(t, env, 0); !strings.HasPrefix(got[1], "IX_Customer_Name|2|NONCLUSTERED|false|false|CustomerName DESC|pct|NULL|") ||
		!strings.Contains(fmt.Sprint(env["warnings"]), "filter of 1 filtered index(es) (IX_Customer_Name)") {
		t.Errorf("hidden filter %q, warnings %v", got[1], env["warnings"])
	}

	if exit, env := runObject(t, newObjectServer(t), "indexes", "Sales.usp_Get"); exit != 2 ||
		!strings.Contains(firstError(t, env)["message"].(string), "is a procedure, which has no indexes") {
		t.Errorf("procedure: exit %d, %v", exit, env["errors"])
	}
}

func TestObjectStats(t *testing.T) {
	exit, env := runObject(t, newObjectServer(t), "stats", "Sales.Customer")
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
	// A filter the login cannot read is NULL, not empty, and named.
	cat := newObjectServer(t)
	cat.hidden = true
	_, env = runObject(t, cat, "stats", "Sales.Customer")
	if got := setRows(t, env, 0); !strings.HasPrefix(got[2], "st_multi|3|CustomerName, pct|user|NULL|") ||
		!strings.Contains(fmt.Sprint(env["warnings"]), "filter of 1 filtered statistic(s) (st_multi)") {
		t.Errorf("hidden filter %q, warnings %v", got[2], env["warnings"])
	}
	_, env = runObject(t, newObjectServer(t), "stats", "vCustomer")
	if w := env["warnings"].([]any); len(w) != 0 {
		t.Errorf("view stats warnings %v", w)
	}
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"PK_Customer|1|id|index|NULL|" + at + "|10|10|100.00|3|0|false|false"}) {
		t.Errorf("view stats %q", got)
	}
	if exit, _ := runObject(t, newObjectServer(t), "stats", "dbo.sn"); exit != 2 {
		t.Errorf("synonym: exit %d, want 2", exit)
	}
}
