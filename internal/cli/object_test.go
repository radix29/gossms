package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/cmd"
)

// objectFake is fakeCatalog plus the object reads, over a small database:
// Sales.Customer (a table with every child describe shows), a schema-bound
// view, procedures (one encrypted, one CLR), a scalar and an inline function,
// a trigger, a synonym, a sequence, three types, and a few shipped objects.
// filters records each ObjectFilter a listing was given.
type objectFake struct {
	*fakeCatalog
	objects []*gosmo.ObjectInfo
	filters []gosmo.ObjectFilter
}

func newObjectFake() *objectFake {
	at := time.Date(2026, 3, 14, 9, 12, 44, 0, time.UTC)
	obj := func(id int, schema, name, typ, desc string) *gosmo.ObjectInfo {
		return &gosmo.ObjectInfo{ObjectID: id, Schema: schema, Name: name, Type: typ, TypeDesc: desc, Created: at, Modified: at}
	}
	trigger := obj(9, "Sales", "trCustomer", "TR", "SQL_TRIGGER")
	trigger.ParentSchema, trigger.ParentName = "Sales", "Customer"
	return &objectFake{fakeCatalog: newFakeCatalog(), objects: []*gosmo.ObjectInfo{
		obj(1, "Sales", "Customer", "U", "USER_TABLE"),
		obj(2, "dbo", "Customer", "U", "USER_TABLE"),
		obj(3, "Sales", "vCustomer", "V", "VIEW"),
		obj(4, "Sales", "usp_Get", "P", "SQL_STORED_PROCEDURE"),
		obj(5, "Sales", "p_secret", "P", "SQL_STORED_PROCEDURE"),
		obj(6, "Sales", "p_clr", "PC", "CLR_STORED_PROCEDURE"),
		obj(7, "dbo", "fScalar", "FN", "SQL_SCALAR_FUNCTION"),
		obj(8, "dbo", "fInline", "IF", "SQL_INLINE_TABLE_VALUED_FUNCTION"),
		trigger,
		obj(10, "dbo", "sn", "SN", "SYNONYM"),
		obj(11, "dbo", "seq", "SO", "SEQUENCE_OBJECT"),
		obj(12, "Sales", "PK_Customer", "PK", "PRIMARY_KEY_CONSTRAINT"),
	}}
}

// keep is gosmo's ObjectFilter rule for the fake's listings: schema equal,
// name containing, case-insensitive.
func (c *objectFake) keep(f gosmo.ObjectFilter, schema, name string) bool {
	for _, s := range f.Schema {
		if !strings.EqualFold(schema, s.Value) {
			return false
		}
	}
	for _, n := range f.Name {
		if !strings.Contains(strings.ToLower(name), strings.ToLower(n.Value)) {
			return false
		}
	}
	return true
}

// listed is the objects of type typ (system or not) f keeps, as built by mk.
func listed[T any](c *objectFake, f gosmo.ObjectFilter, types []string, mk func(*gosmo.ObjectInfo) T) []T {
	c.filters = append(c.filters, f)
	var out []T
	for _, o := range c.objects {
		if slices.Contains(types, o.Type) && c.keep(f, o.Schema, o.Name) {
			out = append(out, mk(o))
		}
	}
	return out
}

func (c *objectFake) ObjectByName(_ context.Context, _ *gosmo.Database, schema, name string) (*gosmo.ObjectInfo, error) {
	for _, o := range c.objects {
		if o.Schema == schema && o.Name == name {
			return o, nil
		}
	}
	return nil, fmt.Errorf("gosmo: object: %w", gosmo.ErrNotFound)
}

func (c *objectFake) ObjectsNamed(_ context.Context, _ *gosmo.Database, name string) ([]*gosmo.ObjectInfo, error) {
	var out []*gosmo.ObjectInfo
	for _, o := range c.objects {
		if o.Name == name {
			out = append(out, o)
		}
	}
	return out, nil
}

func (c *objectFake) Search(_ context.Context, _ *gosmo.Database, pattern string) ([]*gosmo.SearchResult, error) {
	var out []*gosmo.SearchResult
	for _, o := range c.objects {
		if strings.Contains(strings.ToLower(o.Name), strings.ToLower(pattern)) && o.Type != "PK" {
			out = append(out, &gosmo.SearchResult{Schema: o.Schema, Name: o.Name, TypeDesc: o.TypeDesc})
		}
	}
	return out, nil
}

func (c *objectFake) Tables(_ context.Context, _ *gosmo.Database, q gosmo.TableQuery) ([]*gosmo.Table, error) {
	if q.Kind == gosmo.TableKindSystem {
		c.filters = append(c.filters, q.Filter)
		if c.keep(q.Filter, "dbo", "sysdiagrams") {
			return []*gosmo.Table{{Schema: "dbo", Name: "sysdiagrams", IsSystem: true}}, nil
		}
		return nil, nil
	}
	return listed(c, q.Filter, []string{"U"}, func(o *gosmo.ObjectInfo) *gosmo.Table {
		return &gosmo.Table{ObjectID: o.ObjectID, Schema: o.Schema, Name: o.Name}
	}), nil
}

func (c *objectFake) Views(_ context.Context, _ *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.View, error) {
	return listed(c, f, []string{"V"}, func(o *gosmo.ObjectInfo) *gosmo.View { return &gosmo.View{Schema: o.Schema, Name: o.Name} }), nil
}

func (c *objectFake) SystemViews(_ context.Context, _ *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.View, error) {
	c.filters = append(c.filters, f)
	if c.keep(f, "sys", "objects") {
		return []*gosmo.View{{Schema: "sys", Name: "objects"}}, nil
	}
	return nil, nil
}

func (c *objectFake) StoredProcedures(_ context.Context, _ *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.StoredProcedure, error) {
	return listed(c, f, []string{"P", "PC"}, func(o *gosmo.ObjectInfo) *gosmo.StoredProcedure {
		return &gosmo.StoredProcedure{Schema: o.Schema, Name: o.Name}
	}), nil
}

func (c *objectFake) SystemStoredProcedures(_ context.Context, _ *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.StoredProcedure, error) {
	c.filters = append(c.filters, f)
	if c.keep(f, "sys", "sp_who") {
		return []*gosmo.StoredProcedure{{Schema: "sys", Name: "sp_who"}}, nil
	}
	return nil, nil
}

func (c *objectFake) UserDefinedFunctions(_ context.Context, _ *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.UserDefinedFunction, error) {
	return listed(c, f, []string{"FN", "IF"}, func(o *gosmo.ObjectInfo) *gosmo.UserDefinedFunction {
		return &gosmo.UserDefinedFunction{Schema: o.Schema, Name: o.Name}
	}), nil
}

func (c *objectFake) SystemFunctions(_ context.Context, _ *gosmo.Database, f gosmo.ObjectFilter) ([]*gosmo.UserDefinedFunction, error) {
	c.filters = append(c.filters, f)
	return nil, nil
}

var fakeTrigger = &gosmo.Trigger{Schema: "Sales", Name: "trCustomer", TableName: "Customer", IsEnabled: true,
	Events: []string{"INSERT", "UPDATE"}}

func (c *objectFake) Triggers(context.Context, *gosmo.Database) ([]*gosmo.Trigger, error) {
	return []*gosmo.Trigger{fakeTrigger}, nil
}

func (c *objectFake) Synonyms(context.Context, *gosmo.Database) ([]*gosmo.Synonym, error) {
	return []*gosmo.Synonym{{Schema: "dbo", Name: "sn", BaseObject: "[other].[dbo].[t]"}}, nil
}

func (c *objectFake) Sequences(context.Context, *gosmo.Database) ([]*gosmo.Sequence, error) {
	return []*gosmo.Sequence{{Schema: "dbo", Name: "seq"}}, nil
}

func (c *objectFake) UserDefinedDataTypes(context.Context, *gosmo.Database) ([]*gosmo.UserDefinedDataType, error) {
	return []*gosmo.UserDefinedDataType{{Schema: "dbo", Name: "Phone"}}, nil
}

func (c *objectFake) UserDefinedTableTypes(context.Context, *gosmo.Database) ([]*gosmo.UserDefinedTableType, error) {
	return []*gosmo.UserDefinedTableType{{Schema: "Sales", Name: "OrderLines"}}, nil
}

func (c *objectFake) ClrTypes(context.Context, *gosmo.Database) ([]*gosmo.ClrType, error) {
	return []*gosmo.ClrType{{Schema: "dbo", Name: "Point"}, {Schema: "sys", Name: "geography"}}, nil
}

func (c *objectFake) Schemas(context.Context, *gosmo.Database) ([]*gosmo.Schema, error) {
	return []*gosmo.Schema{{ID: 1, Name: "dbo"}, {ID: 2, Name: "guest"}, {ID: 4, Name: "sys"},
		{ID: 5, Name: "Sales"}, {ID: 16384, Name: "db_owner"}}, nil
}

func (c *objectFake) SearchDefinitions(_ context.Context, _ *gosmo.Database, pattern string) ([]*gosmo.SearchResult, error) {
	if pattern != "GETDATE()" {
		return nil, nil
	}
	return []*gosmo.SearchResult{{Schema: "Sales", Name: "usp_Get", TypeDesc: "SQL_STORED_PROCEDURE"}}, nil
}

func (c *objectFake) SearchColumns(_ context.Context, _ *gosmo.Database, pattern string) ([]*gosmo.ColumnSearchResult, error) {
	return []*gosmo.ColumnSearchResult{{Schema: "Sales", Object: "Customer", TypeDesc: "USER_TABLE", Column: "CustomerName",
		DataType: "nvarchar(50)"}}, nil
}

func (c *objectFake) TableByName(_ context.Context, _ *gosmo.Database, schema, name string) (*gosmo.Table, error) {
	return &gosmo.Table{ObjectID: 1, Schema: schema, Name: name}, nil
}

func (c *objectFake) ViewByName(_ context.Context, _ *gosmo.Database, schema, name string) (*gosmo.View, error) {
	return &gosmo.View{ObjectID: 3, Schema: schema, Name: name, IsSchemaBound: true}, nil
}

func (c *objectFake) TriggerByName(context.Context, *gosmo.Database, string, string) (*gosmo.Trigger, error) {
	return fakeTrigger, nil
}

func (c *objectFake) SynonymByName(context.Context, *gosmo.Database, string, string) (*gosmo.Synonym, error) {
	return &gosmo.Synonym{Schema: "dbo", Name: "sn", BaseObject: "[other].[dbo].[t]"}, nil
}

func (c *objectFake) SequenceByName(context.Context, *gosmo.Database, string, string) (*gosmo.Sequence, error) {
	return &gosmo.Sequence{Schema: "dbo", Name: "seq", DataType: "decimal", DataTypeSchema: "sys", Precision: 12,
		StartValue: "1", Increment: "5", MinValue: "1", MaxValue: "999", IsCached: true, CacheSize: 50, CurrentValue: "11"}, nil
}

func (c *objectFake) TableColumns(context.Context, *gosmo.Table) ([]*gosmo.Column, error) {
	return []*gosmo.Column{
		{Name: "id", OrdinalPosition: 1, DataType: "int", IsIdentity: true, IdentitySeed: "1", IdentityIncrement: "1",
			IsPrimaryKey: true, TypeSchema: "sys"},
		{Name: "CustomerName", OrdinalPosition: 2, DataType: "nvarchar", MaxLength: 100, IsNullable: true,
			Collation: "Latin1_General_CI_AS", DefaultValue: &gosmo.ColumnDefault{Name: "DF_n", Definition: "(N'x')"}, TypeSchema: "sys"},
		{Name: "pct", OrdinalPosition: 3, DataType: "int", IsComputed: true, ComputedText: "([id]*(2))", IsPersisted: true,
			IsNullable: true, TypeSchema: "sys"},
	}, nil
}

var fakeIndexes = []*gosmo.Index{
	{Name: "PK_Customer", Type: gosmo.IndexTypeClustered, IsUnique: true, IsPrimaryKey: true,
		KeyColumns: []gosmo.IndexColumn{{Name: "id"}}},
	{Name: "IX_Customer_Name", Type: gosmo.IndexTypeNonClustered, KeyColumns: []gosmo.IndexColumn{{Name: "CustomerName", Descending: true}},
		IncludedColumns: []gosmo.IndexColumn{{Name: "pct", IsIncluded: true}}, FilterDefinition: "([pct]>(0))"},
	{Name: "UQ_Customer_Name", Type: gosmo.IndexTypeNonClustered, IsUnique: true, IsUniqueConstraint: true,
		KeyColumns: []gosmo.IndexColumn{{Name: "CustomerName"}}},
}

func (c *objectFake) TableIndexes(context.Context, *gosmo.Table) ([]*gosmo.Index, error) {
	return fakeIndexes, nil
}

func (c *objectFake) ForeignKeys(context.Context, *gosmo.Table) ([]*gosmo.ForeignKey, error) {
	return []*gosmo.ForeignKey{{Name: "FK_Customer_Region", Columns: []string{"pct"}, ReferencedSchema: "dbo",
		ReferencedTable: "Region", ReferencedColumns: []string{"id"}, DeleteAction: "CASCADE", UpdateAction: "NO_ACTION",
		IsNotTrusted: true}}, nil
}

func (c *objectFake) CheckConstraints(context.Context, *gosmo.Table) ([]*gosmo.CheckConstraint, error) {
	return []*gosmo.CheckConstraint{{Name: "CK_pct", Column: "pct", Definition: "([pct]>=(0))"}}, nil
}

func (c *objectFake) TableTriggers(context.Context, *gosmo.Table) ([]*gosmo.Trigger, error) {
	return []*gosmo.Trigger{fakeTrigger}, nil
}

func (c *objectFake) RowCount(context.Context, *gosmo.Table) (int64, error) { return 1234, nil }

// ViewColumns is the table's first two, the identity without its seed, as
// sys.identity_columns has no row for a view's column.
func (c *objectFake) ViewColumns(ctx context.Context, _ *gosmo.View) ([]*gosmo.Column, error) {
	cols, _ := c.TableColumns(ctx, nil)
	id := *cols[0]
	id.IdentitySeed, id.IdentityIncrement, id.IsPrimaryKey = "", "", false
	return []*gosmo.Column{&id, cols[1]}, nil
}

func (c *objectFake) ViewIndexes(context.Context, *gosmo.View) ([]*gosmo.Index, error) {
	return fakeIndexes[:1], nil
}

func (c *objectFake) ViewTriggers(context.Context, *gosmo.View) ([]*gosmo.Trigger, error) {
	return nil, nil
}

func (c *objectFake) Parameters(_ context.Context, _ *gosmo.Database, _, name string) ([]*gosmo.Parameter, error) {
	return []*gosmo.Parameter{
		{Name: "@id", Ordinal: 1, DataType: "int", TypeSchema: "sys"},
		{Name: "@total", Ordinal: 2, DataType: "decimal", Precision: 9, Scale: 2, IsOutput: true, HasDefault: true, TypeSchema: "sys"},
	}, nil
}

func (c *objectFake) ReturnType(_ context.Context, _ *gosmo.Database, _, name string) (*gosmo.Parameter, error) {
	if name != "fScalar" {
		return nil, nil
	}
	return &gosmo.Parameter{DataType: "decimal", Precision: 9, Scale: 2, TypeSchema: "sys"}, nil
}

func (c *objectFake) ModuleDefinition(_ context.Context, _ *gosmo.Database, kind cmd.ModuleKind, schema, name string) (string, error) {
	switch {
	case name == "p_secret":
		return "", nil
	case kind == cmd.ModuleView && name == "vCustomer":
		return "CREATE VIEW Sales.vCustomer WITH SCHEMABINDING\r\nAS\r\nSELECT id FROM Sales.Customer\r\n", nil
	}
	return "CREATE PROCEDURE " + schema + "." + name + " AS SELECT 1", nil
}

func runObject(t *testing.T, cat *objectFake, argv ...string) (int, map[string]any) {
	t.Helper()
	r := runTree(t, context.Background(), newCommandTree(catalogDeps(t, cat)), append(append([]string{"object"}, argv...), "-p", "dev", "-o", "json")...)
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
	cat := newObjectFake()
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
	cat := newObjectFake()
	exit, env := runObject(t, cat, "list", "--type", "function,procedure", "--name", "S", "--schema", " sales ")
	if exit != 0 {
		t.Fatalf("exit %d: %v", exit, env)
	}
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"Sales|p_secret|procedure|false", "Sales|usp_Get|procedure|false"}) {
		t.Errorf("rows %q", got)
	}
	// The listings that take a filter were narrowed at the server, trimmed.
	for _, f := range cat.filters {
		if len(f.Name) != 1 || f.Name[0] != (gosmo.TextCriterion{Op: gosmo.TextContains, Value: "S"}) ||
			len(f.Schema) != 1 || f.Schema[0] != (gosmo.TextCriterion{Op: gosmo.TextEquals, Value: "sales"}) {
			t.Errorf("filter %+v", f)
		}
	}
	if len(cat.filters) != 2 {
		t.Errorf("%d filtered listings, want 2 (procedures, functions)", len(cat.filters))
	}

	// The families without a server filter apply the same rule in Go.
	_, env = runObject(t, newObjectFake(), "list", "--type", "trigger,type,schema", "--schema", "SALES")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"Sales|trCustomer|trigger|false", "Sales|OrderLines|type|false", "NULL|Sales|schema|false"}) {
		t.Errorf("--schema SALES rows %q", got)
	}
	_, env = runObject(t, newObjectFake(), "list", "--type", "type,synonym", "--name", "PHO")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"dbo|Phone|type|false"}) {
		t.Errorf("--name PHO rows %q", got)
	}

	if exit, env := runObject(t, newObjectFake(), "list", "--type", "tabel"); exit != 2 ||
		!strings.Contains(firstError(t, env)["hint"].(string), "Did you mean table?") {
		t.Errorf("--type tabel: exit %d, %v", exit, env["errors"])
	}
}

func TestObjectListIncludeSystem(t *testing.T) {
	_, env := runObject(t, newObjectFake(), "list", "--type", "table,view,procedure,type,schema", "--include-system")
	got := setRows(t, env, 0)
	for _, w := range []string{"sys|geography|type|true", "dbo|sysdiagrams|table|true", "sys|objects|view|true", "sys|sp_who|procedure|true",
		"NULL|sys|schema|true", "NULL|guest|schema|true", "NULL|db_owner|schema|true", "NULL|dbo|schema|false"} {
		if !slices.Contains(got, w) {
			t.Errorf("missing %s in\n%s", w, strings.Join(got, "\n"))
		}
	}
}

func TestObjectSearch(t *testing.T) {
	_, env := runObject(t, newObjectFake(), "search", "customer")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"Sales|Customer|table", "dbo|Customer|table",
		"Sales|vCustomer|view", "Sales|trCustomer|trigger"}) {
		t.Errorf("--in name rows %q", got)
	}
	_, env = runObject(t, newObjectFake(), "search", "GETDATE()", "--in", "definition")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"Sales|usp_Get|procedure"}) {
		t.Errorf("--in definition rows %q", got)
	}
	_, env = runObject(t, newObjectFake(), "search", "name", "--in", "column")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"Sales|Customer|table|CustomerName|nvarchar(50)"}) {
		t.Errorf("--in column rows %q", got)
	}
	if exit, _ := runObject(t, newObjectFake(), "search", " "); exit != 2 {
		t.Errorf("empty text: exit %d, want 2", exit)
	}
}

func TestObjectDescribeTable(t *testing.T) {
	exit, env := runObject(t, newObjectFake(), "describe", "Sales.Customer")
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
		exit, env := runObject(t, newObjectFake(), "describe", tc.name)
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
	_, env := runObject(t, newObjectFake(), "describe", "usp_Get")
	// A T-SQL module's has_default is unknown to the catalog; a CLR one's is read.
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"1|@id|int|false|NULL", "2|@total|decimal(9,2)|true|NULL"}) {
		t.Errorf("parameters %q", got)
	}
	_, env = runObject(t, newObjectFake(), "describe", "Sales.p_clr")
	if got := setRows(t, env, 0); !slices.Equal(got, []string{"1|@id|int|false|false", "2|@total|decimal(9,2)|true|true"}) {
		t.Errorf("CLR parameters %q", got)
	}
	_, env = runObject(t, newObjectFake(), "describe", "Sales.vCustomer")
	if got := setRows(t, env, 0); !strings.HasPrefix(got[0], "1|id|int|false|IDENTITY|") {
		t.Errorf("view columns %q", got)
	}
}

func TestObjectDescribeResolution(t *testing.T) {
	// D2: a one-part name in two schemas is a usage error listing both.
	exit, env := runObject(t, newObjectFake(), "describe", "Customer")
	if e := firstError(t, env); exit != 2 || !strings.Contains(e["message"].(string), "[Sales].[Customer], [dbo].[Customer]") {
		t.Errorf("ambiguous: exit %d, %v", exit, e)
	}
	exit, env = runObject(t, newObjectFake(), "describe", "Sales.Custom")
	if e := firstError(t, env); exit != 8 || e["code"] != "NOT_FOUND" || !strings.Contains(fmt.Sprint(e["hint"]), "Did you mean") {
		t.Errorf("not found: exit %d, %v", exit, e)
	}
	if exit, _ := runObject(t, newObjectFake(), "describe", "other.Sales.Customer"); exit != 2 {
		t.Errorf("another database's three-part name: exit %d, want 2", exit)
	}
	if exit, env := runObject(t, newObjectFake(), "describe", "app.Sales.Customer"); exit != 0 || env["database"] != "app" {
		t.Errorf("own database's three-part name: exit %d, database %v", exit, env["database"])
	}
}

func TestObjectDefinition(t *testing.T) {
	exit, env := runObject(t, newObjectFake(), "definition", "Sales.vCustomer")
	if exit != 0 {
		t.Fatalf("exit %d: %v", exit, env)
	}
	data := env["data"].(map[string]any)
	if data["type"] != "view" || data["definition"] != "CREATE VIEW Sales.vCustomer WITH SCHEMABINDING\r\nAS\r\nSELECT id FROM Sales.Customer\r\n" {
		t.Errorf("data %v", data)
	}

	// Text: the definition whole after its marker, \n line ends, unclipped.
	r := runTree(t, context.Background(), newCommandTree(catalogDeps(t, newObjectFake())),
		"object", "definition", "Sales.vCustomer", "-p", "dev", "--max-col-width", "5")
	want := "TYPE: view\n\n=== DEFINITION (3 lines) ===\nCREATE VIEW Sales.vCustomer WITH SCHEMABINDING\nAS\nSELECT id FROM Sales.Customer\n"
	if r.exit != 0 || !strings.Contains(r.stdout, want) || strings.Contains(r.stdout, "\r") {
		t.Errorf("text:\n%s", r.stdout)
	}

	for name, warning := range map[string]string{"Sales.p_secret": "encrypted", "Sales.p_clr": "CLR module"} {
		exit, env := runObject(t, newObjectFake(), "definition", name)
		data := env["data"].(map[string]any)
		if v, ok := data["definition"]; exit != 0 || !ok || v != nil || !strings.Contains(fmt.Sprint(env["warnings"]), warning) {
			t.Errorf("%s: exit %d, definition %v (present %v), warnings %v", name, exit, v, ok, env["warnings"])
		}
	}
	if exit, env := runObject(t, newObjectFake(), "definition", "Sales.Customer"); exit != 2 ||
		!strings.Contains(firstError(t, env)["message"].(string), "is a table") || env["data"] != nil {
		t.Errorf("table: exit %d, data %v, %v", exit, env["data"], env["errors"])
	}
}
