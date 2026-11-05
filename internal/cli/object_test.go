package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// runObject runs object argv against s, as JSON.
func runObject(t *testing.T, s *objectServer, argv ...string) (int, map[string]any) {
	t.Helper()
	r := runTree(t, context.Background(), newCommandTree(objectDeps(t, s)), append(append([]string{"object"}, argv...), "-p", "dev", "-o", "json")...)
	return r.exit, envelope(t, r.stdout)
}

// setRows is result set i's rows as "a|b|c" lines, NULL as "NULL".
func setRows(t *testing.T, env map[string]any, i int) []string {
	t.Helper()
	sets := env["resultSets"].([]any)
	if i >= len(sets) {
		t.Fatalf("no result set %d in %v", i, env)
	}
	var out []string
	for _, r := range sets[i].(map[string]any)["rows"].([]any) {
		var cells []string
		for _, c := range r.([]any) {
			if c == nil {
				c = "NULL"
			}
			cells = append(cells, fmt.Sprint(c))
		}
		out = append(out, strings.Join(cells, "|"))
	}
	return out
}

func setNames(env map[string]any) []string {
	var out []string
	for _, s := range env["resultSets"].([]any) {
		out = append(out, fmt.Sprint(s.(map[string]any)["name"]))
	}
	return out
}

func TestObjectList(t *testing.T) {
	cat := newObjectServer(t)
	exit, env := runObject(t, cat, "list")
	if exit != 0 {
		t.Fatalf("exit %d: %v", exit, env)
	}
	// By kind in --type order, then schema and name; no shipped objects or
	// system schemas; a schema row has no schema.
	// Schema and name compare case-insensitively: dbo before Sales.
	want := []string{
		"dbo|Customer|table|false", "Sales|Customer|table|false",
		"Sales|vCustomer|view|false",
		"Sales|p_clr|procedure|false", "Sales|p_secret|procedure|false", "Sales|usp_Get|procedure|false",
		"dbo|fInline|function|false", "dbo|fScalar|function|false",
		"Sales|trCustomer|trigger|false", "dbo|sn|synonym|false", "dbo|seq|sequence|false",
		"dbo|Phone|type|false", "dbo|Point|type|false", "Sales|OrderLines|type|false",
		"NULL|dbo|schema|false", "NULL|Sales|schema|false",
	}
	if got := setRows(t, env, 0); !slices.Equal(got, want) {
		t.Errorf("rows\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestObjectListFilters(t *testing.T) {
	cat := newObjectServer(t)
	exit, env := runObject(t, cat, "list", "--type", "function,procedure", "--name", "S", "--schema", " sales ")
	if exit != 0 {
		t.Fatalf("exit %d: %v", exit, env)
	}
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"Sales|p_secret|procedure|false", "Sales|usp_Get|procedure|false"}) {
		t.Errorf("rows %q", got)
	}
	// The listings that take a filter were narrowed at the server, trimmed:
	// the name contained, the schema equal.
	var filtered []string
	for _, q := range cat.Queries() {
		if strings.Contains(q.Text, "LOWER(o.name) LIKE LOWER(@p1)") {
			filtered = append(filtered, fmt.Sprint(q.Args))
		}
	}
	if !slices.Equal(filtered, []string{"[%S% sales]", "[%S% sales]"}) {
		t.Errorf("filtered listings %q, want procedures and functions narrowed by %%S%% and sales", filtered)
	}

	// The families without a server filter apply the same rule in Go.
	_, env = runObject(t, newObjectServer(t), "list", "--type", "trigger,type,schema", "--schema", "SALES")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"Sales|trCustomer|trigger|false", "Sales|OrderLines|type|false", "NULL|Sales|schema|false"}) {
		t.Errorf("--schema SALES rows %q", got)
	}
	_, env = runObject(t, newObjectServer(t), "list", "--type", "type,synonym", "--name", "PHO")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"dbo|Phone|type|false"}) {
		t.Errorf("--name PHO rows %q", got)
	}

	if exit, env := runObject(t, newObjectServer(t), "list", "--type", "tabel"); exit != 2 ||
		!strings.Contains(firstError(t, env)["hint"].(string), "Did you mean table?") {
		t.Errorf("--type tabel: exit %d, %v", exit, env["errors"])
	}
}

func TestObjectListIncludeSystem(t *testing.T) {
	_, env := runObject(t, newObjectServer(t), "list", "--type", "table,view,procedure,type,schema", "--include-system")
	got := setRows(t, env, 0)
	for _, w := range []string{"sys|geography|type|true", "dbo|sysdiagrams|table|true", "sys|objects|view|true", "sys|sp_who|procedure|true",
		"NULL|sys|schema|true", "NULL|guest|schema|true", "NULL|db_owner|schema|true", "NULL|dbo|schema|false"} {
		if !slices.Contains(got, w) {
			t.Errorf("missing %s in\n%s", w, strings.Join(got, "\n"))
		}
	}
}

func TestObjectSearch(t *testing.T) {
	_, env := runObject(t, newObjectServer(t), "search", "customer")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"Sales|Customer|table", "dbo|Customer|table",
		"Sales|vCustomer|view", "Sales|trCustomer|trigger"}) {
		t.Errorf("--in name rows %q", got)
	}
	_, env = runObject(t, newObjectServer(t), "search", "GETDATE()", "--in", "definition")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"Sales|usp_Get|procedure"}) {
		t.Errorf("--in definition rows %q", got)
	}
	_, env = runObject(t, newObjectServer(t), "search", "name", "--in", "column")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"Sales|Customer|table|CustomerName|nvarchar(50)"}) {
		t.Errorf("--in column rows %q", got)
	}
	if exit, _ := runObject(t, newObjectServer(t), "search", " "); exit != 2 {
		t.Errorf("empty text: exit %d, want 2", exit)
	}
}

func TestObjectDescribeTable(t *testing.T) {
	exit, env := runObject(t, newObjectServer(t), "describe", "Sales.Customer")
	if exit != 0 {
		t.Fatalf("exit %d: %v", exit, env)
	}
	data := env["data"].(map[string]any)
	for k, want := range map[string]string{"schema": "Sales", "name": "Customer", "type": "table", "typeDesc": "USER_TABLE",
		"objectId": "1", "created": "2026-03-14 09:12:44.000", "parent": "", "rowCount": "1234"} {
		if data[k] != want {
			t.Errorf("%s = %v, want %q", k, data[k], want)
		}
	}
	if got := setNames(env); !slices.Equal(got, []string{"columns", "keys", "indexes", "check_constraints", "triggers"}) {
		t.Errorf("sets %q", got)
	}
	if got := setRows(t, env, 0); !slices.Equal(got, []string{
		"1|id|int|false|IDENTITY(1,1)|NULL|NULL|NULL|true",
		"2|CustomerName|nvarchar(50)|true|NULL|NULL|(N'x')|Latin1_General_CI_AS|false",
		"3|pct|int|true|NULL|([id]*(2)) PERSISTED|NULL|NULL|false",
	}) {
		t.Errorf("columns\n%s", strings.Join(got, "\n"))
	}
	// The primary key and the unique constraint, not the plain index; then
	// the foreign key.
	if got := setRows(t, env, 1); !slices.Equal(got, []string{
		"PK_Customer|PRIMARY KEY|id|NULL|NULL|NULL|false|false",
		"UQ_Customer_Name|UNIQUE|CustomerName|NULL|NULL|NULL|false|false",
		"FK_Customer_Region|FOREIGN KEY|pct|[dbo].[Region] (id)|CASCADE|NO_ACTION|false|true",
	}) {
		t.Errorf("keys\n%s", strings.Join(got, "\n"))
	}
	if got := setRows(t, env, 2); got[1] != "IX_Customer_Name|NONCLUSTERED|false|false|CustomerName DESC|pct|([pct]>(0))|false" ||
		!strings.HasPrefix(got[0], "PK_Customer|CLUSTERED|true|true|id|NULL|NULL|") {
		t.Errorf("indexes\n%s", strings.Join(got, "\n"))
	}
	if got := setRows(t, env, 3); !slices.Equal(got, []string{"CK_pct|pct|([pct]>=(0))|false|false"}) {
		t.Errorf("checks %q", got)
	}
	if got := setRows(t, env, 4); !slices.Equal(got, []string{"trCustomer|true|INSERT, UPDATE"}) {
		t.Errorf("triggers %q", got)
	}
}

// A login with SELECT but not VIEW DEFINITION sees the columns and
// constraints with every definition NULL: those read NULL, with a warning,
// not as an empty default or a bare " PERSISTED".
func TestObjectDescribeHiddenDefinitions(t *testing.T) {
	cat := newObjectServer(t)
	cat.hidden = true
	exit, env := runObject(t, cat, "describe", "Sales.Customer")
	if exit != 0 {
		t.Fatalf("exit %d: %v", exit, env)
	}
	if got := setRows(t, env, 0); got[1] != "2|CustomerName|nvarchar(50)|true|NULL|NULL|NULL|Latin1_General_CI_AS|false" ||
		got[2] != "3|pct|int|true|NULL|NULL|NULL|NULL|false" {
		t.Errorf("columns\n%s", strings.Join(got, "\n"))
	}
	if got := setRows(t, env, 3); !slices.Equal(got, []string{"CK_pct|pct|NULL|false|false"}) {
		t.Errorf("checks %q", got)
	}
	// The hidden filter reads NULL, as an unfiltered index's does, so only
	// the warning naming it tells the two apart.
	if got := setRows(t, env, 2); !strings.HasPrefix(got[1], "IX_Customer_Name|NONCLUSTERED|false|false|CustomerName DESC|pct|NULL|") {
		t.Errorf("indexes\n%s", strings.Join(got, "\n"))
	}
	// CustomerName's hidden type reads as its storage type, warned of too.
	if w := env["warnings"].([]any); len(w) != 3 || !strings.HasPrefix(w[0].(string), "3 computed, default or check definitions read NULL") ||
		!strings.HasPrefix(w[1].(string), "The login cannot see 1 user-defined data type(s)") ||
		!strings.HasPrefix(w[2].(string), "The login cannot read the filter of 1 filtered index(es) (IX_Customer_Name)") {
		t.Errorf("warnings %v", w)
	}
	if _, env := runObject(t, newObjectServer(t), "describe", "Sales.Customer"); len(env["warnings"].([]any)) != 0 {
		t.Errorf("visible definitions warned: %v", env["warnings"])
	}
}

func TestObjectDescribeKinds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields map[string]any
		sets   []string
	}{
		{"Sales.vCustomer", map[string]any{"type": "view", "isSchemaBound": "true"}, []string{"columns", "indexes", "triggers"}},
		{"dbo.fScalar", map[string]any{"type": "function", "returns": "decimal(9,2)"}, []string{"parameters"}},
		{"dbo.fInline", map[string]any{"type": "function", "returns": "TABLE"}, []string{"parameters"}},
		{"usp_Get", map[string]any{"type": "procedure", "returns": nil}, []string{"parameters"}},
		{"Sales.trCustomer", map[string]any{"type": "trigger", "parent": "[Sales].[Customer]", "isEnabled": "true",
			"events": "INSERT, UPDATE"}, nil},
		{"dbo.sn", map[string]any{"type": "synonym", "baseObject": "[other].[dbo].[t]"}, nil},
		{"dbo.seq", map[string]any{"type": "sequence", "dataType": "decimal(12,0)", "increment": "5", "cache": "CACHE 50",
			"currentValue": "11"}, nil},
		{"Sales.PK_Customer", map[string]any{"type": "primary_key_constraint", "rowCount": nil}, nil},
	} {
		exit, env := runObject(t, newObjectServer(t), "describe", tc.name)
		if exit != 0 {
			t.Errorf("%s: exit %d: %v", tc.name, exit, env["errors"])
			continue
		}
		data := env["data"].(map[string]any)
		for k, want := range tc.fields {
			if data[k] != want {
				t.Errorf("%s: %s = %v, want %v", tc.name, k, data[k], want)
			}
		}
		if got := setNames(env); !slices.Equal(got, tc.sets) {
			t.Errorf("%s: sets %q, want %q", tc.name, got, tc.sets)
		}
	}
	_, env := runObject(t, newObjectServer(t), "describe", "usp_Get")
	// A T-SQL module's has_default is unknown to the catalog; a CLR one's is read.
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"1|@id|int|false|NULL", "2|@total|decimal(9,2)|true|NULL"}) {
		t.Errorf("parameters %q", got)
	}
	_, env = runObject(t, newObjectServer(t), "describe", "Sales.p_clr")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"1|@id|int|false|false", "2|@total|decimal(9,2)|true|true"}) {
		t.Errorf("CLR parameters %q", got)
	}
	_, env = runObject(t, newObjectServer(t), "describe", "Sales.vCustomer")
	if got := setRows(t, env, 0); !strings.HasPrefix(got[0], "1|id|int|false|IDENTITY|") {
		t.Errorf("view columns %q", got)
	}
}

func TestObjectDescribeResolution(t *testing.T) {
	// D2: a one-part name in two schemas is a usage error listing both.
	exit, env := runObject(t, newObjectServer(t), "describe", "Customer")
	if e := firstError(t, env); exit != 2 || !strings.Contains(e["message"].(string), "[Sales].[Customer], [dbo].[Customer]") {
		t.Errorf("ambiguous: exit %d, %v", exit, e)
	}
	exit, env = runObject(t, newObjectServer(t), "describe", "Sales.Custom")
	if e := firstError(t, env); exit != 8 || e["code"] != "NOT_FOUND" || !strings.Contains(fmt.Sprint(e["hint"]), "Did you mean") {
		t.Errorf("not found: exit %d, %v", exit, e)
	}
	if exit, _ := runObject(t, newObjectServer(t), "describe", "other.Sales.Customer"); exit != 2 {
		t.Errorf("another database's three-part name: exit %d, want 2", exit)
	}
	if exit, env := runObject(t, newObjectServer(t), "describe", "app.Sales.Customer"); exit != 0 || env["database"] != "app" {
		t.Errorf("own database's three-part name: exit %d, database %v", exit, env["database"])
	}
}

func TestObjectDefinition(t *testing.T) {
	exit, env := runObject(t, newObjectServer(t), "definition", "Sales.vCustomer")
	if exit != 0 {
		t.Fatalf("exit %d: %v", exit, env)
	}
	data := env["data"].(map[string]any)
	if data["type"] != "view" || data["definition"] != "CREATE VIEW Sales.vCustomer WITH SCHEMABINDING\r\nAS\r\nSELECT id FROM Sales.Customer\r\n" {
		t.Errorf("data %v", data)
	}

	// Text: the definition whole after its marker, \n line ends, unclipped.
	r := runTree(t, context.Background(), newCommandTree(objectDeps(t, newObjectServer(t))),
		"object", "definition", "Sales.vCustomer", "-p", "dev", "--max-col-width", "5")
	want := "TYPE: view\n\n=== DEFINITION (3 lines) ===\nCREATE VIEW Sales.vCustomer WITH SCHEMABINDING\nAS\nSELECT id FROM Sales.Customer\n"
	if r.exit != 0 || !strings.Contains(r.stdout, want) || strings.Contains(r.stdout, "\r") {
		t.Errorf("text:\n%s", r.stdout)
	}

	for name, warning := range map[string]string{"Sales.p_secret": "encrypted", "Sales.p_clr": "CLR module"} {
		exit, env := runObject(t, newObjectServer(t), "definition", name)
		data := env["data"].(map[string]any)
		if v, ok := data["definition"]; exit != 0 || !ok || v != nil || !strings.Contains(fmt.Sprint(env["warnings"]), warning) {
			t.Errorf("%s: exit %d, definition %v (present %v), warnings %v", name, exit, v, ok, env["warnings"])
		}
	}
	if exit, env := runObject(t, newObjectServer(t), "definition", "Sales.Customer"); exit != 2 ||
		!strings.Contains(firstError(t, env)["message"].(string), "is a table") || env["data"] != nil {
		t.Errorf("table: exit %d, data %v, %v", exit, env["data"], env["errors"])
	}
}
