package cli

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/cmd"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// objectServer is the object group's test server: real gosmo over canned
// rows (gosmotest), answering the reads object list, search, describe,
// definition, dependencies, indexes and stats make, for database app on a
// SQL Server 2022. Its database is the one objectFake describes:
// Sales.Customer (a table with every child describe shows), a schema-bound
// view, procedures (one encrypted, one CLR), a scalar and an inline function,
// a trigger, a synonym, a sequence, three types, and a few shipped objects.
//
// Each rule answers from the fields at the time of the query, so a test sets
// a knob before it runs the command. The rows are the server's, column for
// column in the order gosmo's query selects them (the *Row encoders below):
// a gosmo change to a query's shape or its scanning fails these tests.
type objectServer struct {
	*gosmotest.Fake
	objects []*gosmo.ObjectInfo

	// hidden reads every computed, default and check definition, and
	// IX_Customer_Name's and st_multi's filters, as the server shows them to
	// a login without VIEW DEFINITION: NULL. CustomerName's type reads as one
	// the login cannot see (no sys.types row).
	hidden bool
	// heap makes the usage read answer index 0, a heap, first.
	heap bool
	// usageHidden answers the usage read as without VIEW SERVER STATE.
	usageHidden bool
	// depErr fails the dependents read.
	depErr error
	// statsColumns is each statistic's columns, by name.
	statsColumns map[string][]string

	mu    sync.Mutex
	reads []string // each dependency read, as "uses:schema.name" or "used-by:schema.name"
	modes []string // each fragmentation read's mode
}

var objectsAt = time.Date(2026, 3, 14, 9, 12, 44, 0, time.UTC)

func newObjectServer(t *testing.T) *objectServer {
	obj := func(id int, schema, name, typ, desc string) *gosmo.ObjectInfo {
		return &gosmo.ObjectInfo{ObjectID: id, Schema: schema, Name: name, Type: typ, TypeDesc: desc, Created: objectsAt, Modified: objectsAt}
	}
	trigger := obj(9, "Sales", "trCustomer", "TR", "SQL_TRIGGER")
	trigger.ParentSchema, trigger.ParentName = "Sales", "Customer"
	s := &objectServer{Fake: gosmotest.New(t), objects: []*gosmo.ObjectInfo{
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
	}, statsColumns: map[string][]string{
		"PK_Customer": {"id"}, "_WA_Sys_00000002": {"CustomerName"}, "st_multi": {"CustomerName", "pct"},
	}}
	s.connectRules()
	s.listRules()
	s.describeRules()
	s.storageRules()
	return s
}

// objectDeps is profileDeps (dev) dialling s, with gosmo's own catalog.
func objectDeps(t *testing.T, s *objectServer) cmd.Deps {
	d := profileDeps(t, func(ctx context.Context, _ config.Connection, _ db.Role) (*db.ServerConn, error) {
		srv, err := s.Connect(ctx)
		if err != nil {
			return nil, err
		}
		return &db.ServerConn{Server: srv}, nil
	})
	d.Catalog = cmd.ServerCatalog
	return d
}

// has is whether q's text contains every one of subs.
func has(q gosmotest.Query, subs ...string) bool {
	for _, s := range subs {
		if !strings.Contains(q.Text, s) {
			return false
		}
	}
	return true
}

// on answers the queries containing every one of subs with fn's rows.
func (s *objectServer) on(fn func(gosmotest.Query) [][]any, subs ...string) {
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, subs...) },
		func(q gosmotest.Query) gosmotest.Result {
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: fn(q)}}}
		})
}

func str(v any) string { s, _ := v.(string); return s }

// splitName undoes gosmo.QualifiedName: "[Sales].[Customer]" is Sales, Customer.
func splitName(qualified string) (schema, name string) {
	schema, name, _ = strings.Cut(qualified, "].[")
	return gosmo.UnquoteName(schema + "]"), gosmo.UnquoteName("[" + name)
}

func (s *objectServer) object(schema, name string) *gosmo.ObjectInfo {
	for _, o := range s.objects {
		if o.Schema == schema && o.Name == name {
			return o
		}
	}
	return nil
}

// -- connecting and resolving ----------------------------------------------

func (s *objectServer) connectRules() {
	s.on(func(gosmotest.Query) [][]any { return [][]any{{"app"}} }, "SELECT DB_NAME()")
	s.on(func(q gosmotest.Query) [][]any {
		if !strings.EqualFold(str(q.Arg(0)), "app") {
			return nil
		}
		return [][]any{{"app", 5, "ONLINE", "FULL", 160, "Latin1_General_CI_AS", "Latin1_General_CI_AS", false, objectsAt, 0, false}}
	}, "FROM sys.databases", "WHERE name = @p1")
	// ObjectByName and ObjectsNamed: the catalog compares names exactly.
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, o := range s.objects {
			if has(q, "@p2") && o.Schema == q.Arg(0) && o.Name == q.Arg(1) || !has(q, "@p2") && o.Name == q.Arg(0) {
				rows = append(rows, objectInfoRow(o))
			}
		}
		return rows
	}, "OBJECT_SCHEMA_NAME(o.parent_object_id)", "FROM   sys.objects o")
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, o := range s.objects {
			if slices.Contains([]string{"U", "V", "P", "FN", "IF", "TF", "TR"}, o.Type) && likes(o.Name, str(q.Arg(0))) {
				rows = append(rows, []any{o.Schema, o.Name, o.TypeDesc})
			}
		}
		return rows
	}, "LOWER(o.name) LIKE '%' + LOWER(@p1) + '%'")
}

// likes is LIKE '%pattern%' for a pattern with no wildcards of its own.
func likes(s, pattern string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(pattern))
}

func objectInfoRow(o *gosmo.ObjectInfo) []any {
	return []any{o.ObjectID, o.Schema, o.Name, o.Type, o.TypeDesc, o.ParentSchema, o.ParentName, o.Created, o.Modified, o.IsMSShipped}
}

// -- object list and search ------------------------------------------------

var (
	filterName   = regexp.MustCompile(`LOWER\([a-z]\.name\) LIKE LOWER\(@p(\d)\)`)
	filterSchema = regexp.MustCompile(`LOWER\(SCHEMA_NAME\([a-z]\.schema_id\) COLLATE DATABASE_DEFAULT\) = LOWER\(@p(\d)\)`)
)

// keeps is the WHERE an ObjectFilter clause adds, applied as the server
// would: the name LIKE '%x%' and the schema equal, case-insensitively.
func keeps(q gosmotest.Query, schema, name string) bool {
	arg := func(m []string) string { return str(q.Arg(int(m[1][0] - '1'))) }
	if m := filterName.FindStringSubmatch(q.Text); m != nil && !likes(name, strings.Trim(arg(m), "%")) {
		return false
	}
	if m := filterSchema.FindStringSubmatch(q.Text); m != nil && !strings.EqualFold(schema, arg(m)) {
		return false
	}
	return true
}

// modules is the rows of s's objects of types the listing keeps, each
// moduleColumns then extra(o).
func (s *objectServer) modules(q gosmotest.Query, types []string, extra func(*gosmo.ObjectInfo) []any) [][]any {
	var rows [][]any
	for _, o := range s.objects {
		if slices.Contains(types, o.Type) && keeps(q, o.Schema, o.Name) {
			rows = append(rows, append([]any{o.ObjectID, o.Schema, o.Name, o.Created, o.Modified}, extra(o)...))
		}
	}
	return rows
}

func none(*gosmo.ObjectInfo) []any { return nil }

// sysObject is one object SQL Server ships, in the sys schema unless named.
func sysObject(q gosmotest.Query, schema, name string, extra ...any) [][]any {
	if !keeps(q, schema, name) {
		return nil
	}
	return [][]any{append([]any{-100, schema, name, objectsAt, objectsAt}, extra...)}
}

func (s *objectServer) listRules() {
	s.on(func(q gosmotest.Query) [][]any {
		if has(q, "t.is_ms_shipped = 1") {
			if !keeps(q, "dbo", "sysdiagrams") {
				return nil
			}
			return [][]any{tableRow(&gosmo.Table{ObjectID: 13, Schema: "dbo", Name: "sysdiagrams", IsSystem: true})}
		}
		var rows [][]any
		for _, o := range s.objects {
			if o.Type == "U" && keeps(q, o.Schema, o.Name) {
				rows = append(rows, tableRow(&gosmo.Table{ObjectID: o.ObjectID, Schema: o.Schema, Name: o.Name}))
			}
		}
		return rows
	}, "FROM   sys.tables t", "ORDER  BY SCHEMA_NAME(t.schema_id), t.name")
	s.on(func(q gosmotest.Query) [][]any {
		return s.modules(q, []string{"V"}, func(*gosmo.ObjectInfo) []any { return []any{true} })
	}, "FROM   sys.views o", "ORDER  BY")
	s.on(func(q gosmotest.Query) [][]any { return sysObject(q, "sys", "objects", false) },
		"FROM   sys.all_objects o", "o.type = 'V' AND o.is_ms_shipped = 1")
	s.on(func(q gosmotest.Query) [][]any { return s.modules(q, []string{"P", "PC"}, none) }, "FROM   sys.procedures o")
	s.on(func(q gosmotest.Query) [][]any { return sysObject(q, "sys", "sp_who") },
		"FROM   sys.all_objects o", "o.type IN ('P','PC') AND o.is_ms_shipped = 1")
	s.on(func(q gosmotest.Query) [][]any {
		return s.modules(q, []string{"FN", "IF", "TF", "FS", "FT"}, func(o *gosmo.ObjectInfo) []any { return []any{o.Type} })
	}, "FROM   sys.objects o", "o.type IN ('FN','TF','IF','FS','FT') AND o.is_ms_shipped = 0")
	s.on(func(gosmotest.Query) [][]any { return nil },
		"FROM   sys.all_objects o", "o.type IN ('FN','TF','IF') AND o.is_ms_shipped = 1")
	s.on(func(gosmotest.Query) [][]any { return [][]any{{"sn", "dbo", 10, "[other].[dbo].[t]"}} },
		"FROM   sys.synonyms")
	s.on(func(gosmotest.Query) [][]any {
		return [][]any{{"seq", "dbo", 11, "decimal", "sys", 12, 0, "1", "5", "1", "999", false, true, 50, "11", "11", false}}
	}, "FROM   sys.sequences s")
	s.on(func(gosmotest.Query) [][]any {
		return [][]any{{"Phone", "dbo", 257, "varchar", 20, 0, 0, "SQL_Latin1_General_CP1_CI_AS", true, "", "", "", ""}}
	}, "WHERE  t.is_user_defined = 1 AND t.is_table_type = 0")
	s.on(func(gosmotest.Query) [][]any { return [][]any{{"OrderLines", "Sales", 258, 14, false}} },
		"FROM   sys.table_types tt")
	s.on(func(gosmotest.Query) [][]any {
		return [][]any{
			{"Point", "dbo", 259, 8000, 0, 0, true, "Geo", "Geo.Point"},
			{"geography", "sys", 130, -1, 0, 0, true, "Microsoft.SqlServer.Types", "Microsoft.SqlServer.Types.SqlGeography"},
		}
	}, "WHERE  t.is_assembly_type = 1")
	s.on(func(gosmotest.Query) [][]any {
		return [][]any{{"db_owner", 16384, "db_owner"}, {"dbo", 1, "dbo"}, {"guest", 2, "guest"}, {"Sales", 5, "dbo"}, {"sys", 4, "sys"}}
	}, "FROM   sys.schemas s", "ORDER  BY s.name")
	s.on(func(q gosmotest.Query) [][]any {
		if q.Arg(0) != "GETDATE()" {
			return nil
		}
		return [][]any{{"Sales", "usp_Get", "SQL_STORED_PROCEDURE"}}
	}, "LOWER(m.definition) LIKE")
	s.on(func(gosmotest.Query) [][]any {
		return [][]any{{"Sales", "Customer", "USER_TABLE", "CustomerName", "nvarchar", 100, 0, 0, "sys", false, false}}
	}, "LOWER(c.name) LIKE")
}

func tableRow(t *gosmo.Table) []any {
	return []any{t.ObjectID, t.Schema, t.Name, objectsAt, objectsAt, false, false, t.IsSystem, false, false, false, false}
}

// -- object describe and definition ----------------------------------------

// The table's and view's object ids, which their children are read by.
const (
	customerID  = 1
	vCustomerID = 3
)

func (s *objectServer) describeRules() {
	s.on(func(q gosmotest.Query) [][]any {
		o := s.object(str(q.Arg(0)), str(q.Arg(1)))
		if o == nil || o.Type != "U" {
			return nil
		}
		return [][]any{tableRow(&gosmo.Table{ObjectID: o.ObjectID, Schema: o.Schema, Name: o.Name})}
	}, "FROM   sys.tables t", "AND  t.name")
	s.on(func(q gosmotest.Query) [][]any {
		o := s.object(str(q.Arg(0)), str(q.Arg(1)))
		if o == nil || o.Type != "V" {
			return nil
		}
		return [][]any{{o.ObjectID, o.Schema, o.Name, o.Created, o.Modified, true}}
	}, "FROM   sys.views o", "AND  o.name")
	s.on(func(gosmotest.Query) [][]any { return [][]any{{int64(1234)}} }, "SELECT SUM(p.rows)")
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, c := range s.columns(q.Arg(0) == int64(vCustomerID)) {
			rows = append(rows, columnRow(c))
		}
		return rows
	}, "FROM   sys.columns c", "WHERE  c.object_id = @p1")
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, ix := range s.indexes(q.Arg(0) == int64(vCustomerID)) {
			rows = append(rows, indexRow(ix))
		}
		return rows
	}, "FROM   sys.indexes i", "WHERE  i.object_id = @p1 AND i.type > 0")
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, ix := range s.indexes(q.Arg(0) == int64(vCustomerID)) {
			for _, c := range ix.KeyColumns {
				rows = append(rows, []any{ix.IndexID, c.Name, c.Descending, false, 0})
			}
			for _, c := range ix.IncludedColumns {
				rows = append(rows, []any{ix.IndexID, c.Name, false, true, 0})
			}
		}
		return rows
	}, "FROM   sys.index_columns ic")
	s.on(func(gosmotest.Query) [][]any {
		return [][]any{{"FK_Customer_Region", false, true, false, "CASCADE", "NO_ACTION", "dbo", "Region", `[{"v":"pct"}]`, `[{"v":"id"}]`}}
	}, "FROM   sys.foreign_keys fk")
	s.on(func(gosmotest.Query) [][]any {
		def := "([pct]>=(0))"
		if s.hidden {
			def = ""
		}
		return [][]any{{"CK_pct", def, false, "pct", false, false}}
	}, "FROM   sys.check_constraints cc")
	trigger := []any{"trCustomer", "Customer", "Sales", false, `[{"v":"INSERT"},{"v":"UPDATE"}]`}
	s.on(func(q gosmotest.Query) [][]any {
		switch {
		case has(q, "tr.parent_id = OBJECT_ID(@p1)"): // a view's
			return nil
		case has(q, "tr.name = @p2") && (q.Arg(0) != "Sales" || q.Arg(1) != "trCustomer"):
			return nil
		}
		return [][]any{trigger}
	}, "FROM   sys.triggers tr")
	s.on(func(q gosmotest.Query) [][]any {
		if q.Arg(0) != "dbo" || q.Arg(1) != "sn" {
			return nil
		}
		return [][]any{{"sn", "dbo", 10, "[other].[dbo].[t]"}}
	}, "FROM   sys.synonyms", "@p2")
	s.on(func(q gosmotest.Query) [][]any {
		if has(q, "p.parameter_id = 0") {
			if q.Arg(1) != "fScalar" {
				return nil
			}
			return [][]any{parameterRow("", 0, "decimal", 9, 2, false, false)}
		}
		return [][]any{parameterRow("@id", 1, "int", 0, 0, false, false), parameterRow("@total", 2, "decimal", 9, 2, true, true)}
	}, "FROM   sys.parameters p")
	s.on(func(q gosmotest.Query) [][]any {
		schema, name := splitName(str(q.Arg(0)))
		switch {
		case name == "p_secret":
			return [][]any{{nil}} // encrypted: the object is there, its text NULL
		case has(q, "o.type = 'V'") && name == "vCustomer":
			return [][]any{{"CREATE VIEW Sales.vCustomer WITH SCHEMABINDING\r\nAS\r\nSELECT id FROM Sales.Customer\r\n"}}
		}
		return [][]any{{"CREATE PROCEDURE " + schema + "." + name + " AS SELECT 1"}}
	}, "SELECT OBJECT_DEFINITION(o.object_id)")
}

// columns is Sales.Customer's columns, or with view vCustomer's: the
// table's first two, the identity without its seed, as
// sys.identity_columns has no row for a view's column.
func (s *objectServer) columns(view bool) []*gosmo.Column {
	cols := []*gosmo.Column{
		{Name: "id", OrdinalPosition: 1, DataType: "int", MaxLength: 4, Precision: 10, IsIdentity: true, IdentitySeed: "1",
			IdentityIncrement: "1", IsPrimaryKey: true, TypeSchema: "sys"},
		{Name: "CustomerName", OrdinalPosition: 2, DataType: "nvarchar", MaxLength: 100, IsNullable: true,
			Collation: "Latin1_General_CI_AS", DefaultValue: &gosmo.ColumnDefault{Name: "DF_n", Definition: "(N'x')"}, TypeSchema: "sys"},
		{Name: "pct", OrdinalPosition: 3, DataType: "int", MaxLength: 4, Precision: 10, IsComputed: true, ComputedText: "([id]*(2))",
			IsPersisted: true, IsNullable: true, TypeSchema: "sys"},
	}
	if s.hidden {
		cols[1].DefaultValue.Definition, cols[2].ComputedText = "", ""
		cols[1].IsTypeHidden, cols[1].IsUserDefinedType, cols[1].TypeSchema = true, true, ""
	}
	if view {
		cols[0].IdentitySeed, cols[0].IdentityIncrement, cols[0].IsPrimaryKey = "", "", false
		cols = cols[:2]
	}
	return cols
}

// indexes is Sales.Customer's indexes — a hypothetical one among them — or
// with view vCustomer's.
func (s *objectServer) indexes(view bool) []*gosmo.Index {
	if view {
		return []*gosmo.Index{{Name: "CIX_vCustomer", IndexID: 1, Type: gosmo.IndexTypeClustered, IsUnique: true,
			KeyColumns: []gosmo.IndexColumn{{Name: "id"}}}}
	}
	ixs := []*gosmo.Index{
		{Name: "PK_Customer", IndexID: 1, Type: gosmo.IndexTypeClustered, IsUnique: true, IsPrimaryKey: true,
			KeyColumns: []gosmo.IndexColumn{{Name: "id"}}},
		{Name: "IX_Customer_Name", IndexID: 2, Type: gosmo.IndexTypeNonClustered,
			KeyColumns:      []gosmo.IndexColumn{{Name: "CustomerName", Descending: true}},
			IncludedColumns: []gosmo.IndexColumn{{Name: "pct", IsIncluded: true}}, FilterDefinition: "([pct]>(0))"},
		{Name: "UQ_Customer_Name", IndexID: 3, Type: gosmo.IndexTypeNonClustered, IsUnique: true, IsUniqueConstraint: true,
			KeyColumns: []gosmo.IndexColumn{{Name: "CustomerName"}}},
		{Name: "_dta_index", IndexID: 9, Type: gosmo.IndexTypeNonClustered, IsHypothetical: true,
			KeyColumns: []gosmo.IndexColumn{{Name: "pct"}}},
	}
	if s.hidden {
		ixs[1].FilterDefinition, ixs[1].IsFilterHidden = "", true
	}
	return ixs
}

// nullable is v, or NULL for the zero value.
func nullable[T comparable](v T) any {
	var zero T
	if v == zero {
		return nil
	}
	return v
}

// columnRow is c as one row of gosmo's column select.
func columnRow(c *gosmo.Column) []any {
	dcName, dcDef := "", ""
	if c.DefaultValue != nil {
		dcName, dcDef = c.DefaultValue.Name, c.DefaultValue.Definition
	}
	return []any{c.Name, c.OrdinalPosition, string(c.DataType),
		c.MaxLength, c.Precision, c.Scale,
		c.IsNullable, c.IsIdentity, c.IsComputed,
		c.ComputedText, dcName, dcDef,
		c.IsRowGUID, c.Collation,
		nullable(c.IdentitySeed), nullable(c.IdentityIncrement),
		c.IsPrimaryKey,
		nullable(c.TypeSchema), c.IsUserDefinedType,
		c.IsPersisted, c.IdentityNotForReplication,
		c.IsSparse, c.IsColumnSet,
		c.MaskingFunction,
		0, c.IsHidden, c.IsFileStream,
		0, false,
		"", "", "",
		"", "", false,
		0, "",
		"", "", "", "",
		c.IsTypeHidden}
}

// indexRow is ix as one row of gosmo's index list: a rowstore index on
// PRIMARY, with a filter NULL when it is hidden.
func indexRow(ix *gosmo.Index) []any {
	var filter any = ix.FilterDefinition
	if ix.IsFilterHidden {
		filter = nil
	}
	return []any{ix.Name, ix.IndexID, string(ix.Type),
		ix.IsUnique, ix.IsPrimaryKey, ix.IsUniqueConstraint,
		ix.IsDisabled, ix.IsHypothetical, 0, filter,
		false, false, true, true,
		nil,
		"PRIMARY", false, true, "",
		false, false, false,
		0, 0,
		false, "", "",
		false, "",
		"", nil, nil, nil, nil,
		"", "", "", "",
		0}
}

func parameterRow(name string, ordinal int, typ string, precision, scale int, output, hasDefault bool) []any {
	return []any{name, ordinal, typ, 0, precision, scale, output, hasDefault, "sys", false, "", "", false, 0, "", false}
}

// -- object dependencies, indexes and stats --------------------------------

// edge is one row of sys.sql_expression_dependencies, as object
// dependencies reads it.
type edge struct {
	from, to   [2]string
	bound, cal bool
}

// edges is the uses graph, with a cycle: usp_Get → vCustomer → Customer →
// fScalar → vCustomer. The dependents read is the same graph reversed.
var edges = []edge{
	{[2]string{"Sales", "usp_Get"}, [2]string{"Sales", "vCustomer"}, false, false},
	{[2]string{"Sales", "usp_Get"}, [2]string{"dbo", "fScalar"}, false, true},
	{[2]string{"Sales", "vCustomer"}, [2]string{"Sales", "Customer"}, true, false},
	{[2]string{"Sales", "Customer"}, [2]string{"dbo", "fScalar"}, false, false},
	// A filtered index's reference to its own table.
	{[2]string{"Sales", "Customer"}, [2]string{"Sales", "Customer"}, true, false},
	{[2]string{"dbo", "fScalar"}, [2]string{"Sales", "vCustomer"}, false, false},
}

var usageAt = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

func (s *objectServer) record(list *[]string, v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	*list = append(*list, v)
}

// recorded is a copy of list.
func (s *objectServer) recorded(list *[]string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(*list)
}

func (s *objectServer) storageRules() {
	dependencyRows := func(dependents bool) func(q gosmotest.Query) [][]any {
		return func(q gosmotest.Query) [][]any {
			schema, name := splitName(str(q.Arg(0)))
			var rows [][]any
			for _, e := range edges {
				near, far := e.from, e.to
				if dependents {
					near, far = e.to, e.from
				}
				if near == [2]string{schema, name} {
					rows = append(rows, []any{far[0], far[1], s.object(far[0], far[1]).TypeDesc, e.bound, e.cal})
				}
			}
			return rows
		}
	}
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "sys.dm_sql_referencing_entities(@p1") },
		func(q gosmotest.Query) gosmotest.Result {
			schema, name := splitName(str(q.Arg(0)))
			s.record(&s.reads, "used-by:"+schema+"."+name)
			if s.depErr != nil {
				return gosmotest.Result{Err: s.depErr}
			}
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: dependencyRows(true)(q)}}}
		})
	s.on(func(q gosmotest.Query) [][]any {
		schema, name := splitName(str(q.Arg(0)))
		s.record(&s.reads, "uses:"+schema+"."+name)
		return dependencyRows(false)(q)
	}, "FROM   sys.sql_expression_dependencies sed", "sed.referencing_id = OBJECT_ID(@p1)")

	s.on(func(q gosmotest.Query) [][]any {
		view := q.Arg(0) == "[Sales].[vCustomer]"
		ids := []int{1, 2, 3}
		switch {
		case view:
			ids = []int{1}
		case s.heap:
			ids = []int{0, 2, 3}
		}
		var rows [][]any
		for _, id := range ids {
			name := ""
			for _, ix := range s.indexes(view) {
				if ix.IndexID == id {
					name = ix.Name
				}
			}
			if s.usageHidden {
				rows = append(rows, []any{name, id, false, 0, 0, 0, 0, nil, nil, nil, nil})
			} else {
				rows = append(rows, []any{name, id, true, 10 * id, id, 0, 7, usageAt, nil, nil, usageAt})
			}
		}
		return rows
	}, "sys.dm_db_index_usage_stats u")

	modeOf := regexp.MustCompile(`NULL, N'(\w+)'\)`)
	s.on(func(q gosmotest.Query) [][]any {
		s.record(&s.modes, modeOf.FindStringSubmatch(q.Text)[1])
		ids := []int{1, 2}
		if q.Arg(0) == "[Sales].[vCustomer]" {
			ids = []int{1}
		}
		var rows [][]any
		for _, id := range ids {
			rows = append(rows, []any{"ix", id, 12.346, 100, 4, 88.5})
		}
		return rows
	}, "sys.dm_db_index_physical_stats(DB_ID(), i.object_id")

	s.on(func(q gosmotest.Query) [][]any {
		if q.Arg(0) == int64(vCustomerID) {
			return [][]any{statisticRow("PK_Customer", 1, false, false, false, "", usageAt, 10, 10, 3, 0, false)}
		}
		filter := "([pct]>(0))"
		if s.hidden {
			filter = "" // NULL, read as ''
		}
		return [][]any{
			statisticRow("PK_Customer", 1, false, false, false, "", usageAt, 50, 200, 120, 3, false),
			statisticRow("_WA_Sys_00000002", 2, true, false, false, "", nil, nil, nil, nil, nil, false),
			statisticRow("st_multi", 3, false, true, true, filter, usageAt, 0, 0, 0, 0, true),
		}
	}, "OUTER  APPLY sys.dm_db_stats_properties(s.object_id, s.stats_id) sp")
	s.on(func(q gosmotest.Query) [][]any {
		var rows [][]any
		for _, c := range s.statsColumns[str(q.Arg(0))] {
			rows = append(rows, []any{c})
		}
		return rows
	}, "JOIN   sys.stats_columns sc")
}

// statisticRow is one row of gosmo's statistics select. The select reads a
// NULL filter as ”, so only hasFilter tells a hidden filter from none; the
// properties are NULL (nil) for a statistic never computed.
func statisticRow(name string, id int, auto, user, hasFilter bool, filter string,
	lastUpdated, sampled, rows, steps, modified any, noRecompute bool) []any {
	return []any{name, id, auto, user, hasFilter, filter, lastUpdated, sampled, rows, steps, nil, noRecompute, false, modified}
}

// deniedDependents is the server refusing the dependents read: Msg 229.
var deniedDependents = mssql.Error{Number: 229, Class: 14, Message: "The SELECT permission was denied on the object 'sql_expression_dependencies'"}
