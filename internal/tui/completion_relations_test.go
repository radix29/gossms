package tui

import (
	"fmt"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tui/sqlparse"
)

// resolveTestSQL parses sql — which must hold exactly one '|' cursor marker —
// and resolves the cursor's own query's FROM refs against a catalog built from
// objects, the same steps the provider makes.
//
// The whole script is one batch here, so declarations are scanned over all of
// it while the query tree sees only the cursor's own statement — the split the
// provider makes with NarrowToDMLStatement, and the one that keeps a
// declaration above the cursor out of its FROM scope.
func resolveTestSQL(t *testing.T, sql string, objects []gosmo.CatalogObject) []relation {
	t.Helper()
	return resolveTestSQLWith(t, sql, &gosmo.Catalog{Objects: objects}, nil)
}

// resolveTestSQLWith is resolveTestSQL over a whole catalog (its Schemas
// derived from Objects), with configure, when set, adjusting the resolve
// context — another database's inventory, the sys one.
func resolveTestSQLWith(t *testing.T, sql string, cat *gosmo.Catalog, configure func(*resolveCtx)) []relation {
	t.Helper()
	cursor := strings.Index(sql, "|")
	if cursor < 0 || strings.Count(sql, "|") != 1 {
		t.Fatalf("sql must hold exactly one '|' cursor marker: %q", sql)
	}
	buf := []rune(strings.Replace(sql, "|", "", 1))
	upTo := len([]rune(sql[:cursor]))
	tokens, _, _, _ := sqlparse.TokenizeRange(buf, 0, len(buf), false)
	start, end := sqlparse.NarrowToDMLStatement(tokens, 0, len(buf), upTo)
	stmtTokens, _, _, _ := sqlparse.TokenizeRange(buf, start, end, false)
	scope := sqlparse.ScopeAt(stmtTokens, upTo)
	if scope.Query == nil {
		t.Fatalf("no query in scope for %q", sql)
	}
	seen := map[string]bool{}
	for _, o := range cat.Objects {
		if !seen[o.Schema] {
			seen[o.Schema] = true
			cat.Schemas = append(cat.Schemas, o.Schema)
		}
	}
	rc := newResolveCtx(newCompletionInventory(cat), nil, scope.CTEs, sqlparse.ScanBindings(tokens))
	if configure != nil {
		configure(&rc)
	}
	return resolveRefs(rc, scope.Query.From)
}

// columnSpecs renders one relation's columns as "name:type", with an empty type
// for a synthetic column whose type couldn't be worked out.
func columnSpecs(r relation) string {
	parts := make([]string, 0, len(r.columns()))
	for _, c := range r.columns() {
		parts = append(parts, fmt.Sprintf("%s:%s", c.Name, c.DataType))
	}
	return strings.Join(parts, " ")
}

// oneRelation asserts exactly one relation came back, with the given name.
func oneRelation(t *testing.T, rels []relation, name string) relation {
	t.Helper()
	if len(rels) != 1 {
		t.Fatalf("got %d relations, want 1", len(rels))
	}
	if rels[0].name != name {
		t.Errorf("relation name = %q, want %q", rels[0].name, name)
	}
	return rels[0]
}

func TestResolveDerivedTableColumns(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT * FROM (SELECT Name, Email FROM dbo.Customers) d WHERE d.|",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "d")), "Name:nvarchar Email:varchar"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

func TestResolveDerivedTableStarExpandsSource(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT | FROM (SELECT * FROM dbo.Customers) d",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "d")), "Id:int Name:nvarchar Email:varchar"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

func TestResolveNestedDerivedTables(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT | FROM (SELECT Name FROM (SELECT * FROM dbo.Customers) inner1) outer1",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "outer1")), "Name:nvarchar"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

func TestResolveCTEColumns(t *testing.T) {
	rels := resolveTestSQL(t,
		"WITH t1 AS (SELECT * FROM dbo.Customers) SELECT | FROM t1",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "t1")), "Id:int Name:nvarchar Email:varchar"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

func TestResolveCTEExplicitColumnListTypedPositionally(t *testing.T) {
	rels := resolveTestSQL(t,
		"WITH t1 (a, b, c) AS (SELECT * FROM dbo.Customers) SELECT | FROM t1",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "t1")), "a:int b:nvarchar c:varchar"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

// A count mismatch means one side was misparsed, so the names stand but the
// types don't get paired up arbitrarily.
func TestResolveCTEColumnListCountMismatchIsUntyped(t *testing.T) {
	rels := resolveTestSQL(t,
		"WITH t1 (a, b) AS (SELECT * FROM dbo.Customers) SELECT | FROM t1",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "t1")), "a: b:"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

func TestResolveCTEReferencingEarlierCTE(t *testing.T) {
	rels := resolveTestSQL(t,
		"WITH a AS (SELECT Id x FROM dbo.Customers), b AS (SELECT x FROM a) SELECT | FROM b",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "b")), "x:int"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

// WITH r AS (SELECT * FROM r) is legal T-SQL. The cycle guard must stop the
// expansion rather than loop; no columns is the right answer here.
func TestResolveRecursiveCTETerminates(t *testing.T) {
	rels := resolveTestSQL(t,
		"WITH r AS (SELECT * FROM r) SELECT | FROM r",
		testCustomersOrders())
	if len(rels) != 0 {
		t.Fatalf("got %d relations (%q), want none", len(rels), columnSpecs(rels[0]))
	}
}

func TestResolveCTENameShadowedBySchemaQualifiedRef(t *testing.T) {
	rels := resolveTestSQL(t,
		"WITH Customers AS (SELECT Id FROM dbo.Orders) SELECT | FROM dbo.Customers",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "Customers")), "Id:int Name:nvarchar Email:varchar"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

func TestResolveSelectAliasNamesAndTypesColumn(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT | FROM (SELECT c.Name AS FullName FROM dbo.Customers c) d",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "d")), "FullName:nvarchar"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

// An unaliased expression names nothing and is dropped; an aliased one is kept
// with no type, which formatColumnType renders as the bare word "column".
func TestResolveExpressionItemsNamedOnlyByAlias(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT | FROM (SELECT COUNT(*), COUNT(*) n FROM dbo.Orders) d",
		testCustomersOrders())
	d := oneRelation(t, rels, "d")
	if got, want := columnSpecs(d), "n:"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
	if got := formatColumnType(d.columns()[0]); got != "column" {
		t.Errorf("formatColumnType = %q, want %q", got, "column")
	}
}

func TestResolveQualifiedStarPicksOneRelation(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT | FROM (SELECT o.* FROM dbo.Customers c JOIN dbo.Orders o ON o.CustomerId = c.Id) d",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "d")), "Id:int CustomerId:int Total:decimal"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

// A star over a join deduplicates by name — both tables have an Id.
func TestResolveStarOverJoinDeduplicates(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT | FROM (SELECT * FROM dbo.Customers c JOIN dbo.Orders o ON o.CustomerId = c.Id) d",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "d")), "Id:int Name:nvarchar Email:varchar CustomerId:int Total:decimal"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

func TestResolveUnknownTableResolvesToNothing(t *testing.T) {
	rels := resolveTestSQL(t, "SELECT | FROM dbo.NoSuchTable", testCustomersOrders())
	if len(rels) != 0 {
		t.Fatalf("got %d relations, want none", len(rels))
	}
}

// The depth cap has to stop a nesting deeper than maxRelationDepth without
// looping or panicking; no columns is the right answer.
func TestResolveDepthCapStopsDeepNesting(t *testing.T) {
	const depth = maxRelationDepth + 2
	sql := "SELECT * FROM dbo.Customers"
	for i := 0; i < depth; i++ {
		sql = fmt.Sprintf("SELECT * FROM (%s) d%d", sql, i)
	}
	rels := resolveTestSQL(t, strings.Replace(sql, "SELECT *", "SELECT |", 1), testCustomersOrders())
	if len(rels) != 0 {
		t.Fatalf("got %d relations (%q), want none past the depth cap", len(rels), columnSpecs(rels[0]))
	}
}

// ---------------------------------------------------------------------------
// Temp tables and table variables
// ---------------------------------------------------------------------------

func TestResolveTempTableFromCreateTable(t *testing.T) {
	rels := resolveTestSQL(t,
		"CREATE TABLE #t (Id int NOT NULL, Name nvarchar(50))\nSELECT | FROM #t",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "#t")), "Id:int Name:nvarchar"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

// A declared type has to render the way the catalog's own would — nvarchar's
// max_length is bytes there, and gosmo.TypeString halves it on the way out,
// so a declared character count that isn't doubled prints at half its size.
// A bare datetime2 is scale 7, and datetime2(0) must keep its (0).
func TestResolveTempTableColumnTypesRenderLikeTheCatalog(t *testing.T) {
	rels := resolveTestSQL(t,
		"CREATE TABLE #t (Name nvarchar(50), Note varchar(MAX), Amount decimal(18, 2) NOT NULL, Code char(2), At datetime2, Day datetime2(0))\nSELECT | FROM #t",
		testCustomersOrders())
	got := make([]string, 0, 6)
	for _, c := range oneRelation(t, rels, "#t").columns() {
		got = append(got, formatColumnType(c))
	}
	want := []string{"nvarchar(50)", "varchar(MAX)", "decimal(18,2), not null", "char(2)", "datetime2(7)", "datetime2(0)"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("types = %v, want %v", got, want)
	}
}

func TestResolveTableVariableFromDeclare(t *testing.T) {
	rels := resolveTestSQL(t,
		"DECLARE @t TABLE (Id int, Total decimal(18, 2))\nSELECT | FROM @t",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "@t")), "Id:int Total:decimal"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

func TestResolveTempTableFromSelectInto(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT c.Name, c.Email INTO #t FROM dbo.Customers c\nSELECT | FROM #t",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "#t")), "Name:nvarchar Email:varchar"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

func TestResolveTempTableAlias(t *testing.T) {
	rels := resolveTestSQL(t,
		"CREATE TABLE #t (Id int)\nSELECT | FROM #t AS x",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "x")), "Id:int"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

// The sigil is part of the name, so an undeclared "#Orders" is not the catalog
// table "Orders" — which is exactly what it resolved to before the tokenizer
// kept it.
func TestResolveUndeclaredTempTableIsNotACatalogTable(t *testing.T) {
	rels := resolveTestSQL(t, "SELECT | FROM #Orders", testCustomersOrders())
	if len(rels) != 0 {
		t.Fatalf("got %d relations (%q), want none", len(rels), columnSpecs(rels[0]))
	}
}

// A redeclared name means the later shape; the earlier one is history.
func TestResolveTempTableLastDeclarationWins(t *testing.T) {
	rels := resolveTestSQL(t,
		"CREATE TABLE #t (Old int)\nDROP TABLE #t\nCREATE TABLE #t (New int)\nSELECT | FROM #t",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "#t")), "New:int"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

// "SELECT * INTO #t FROM #t" is not legal T-SQL, but a half-typed script on
// the way to something else is what this runs on.
func TestResolveSelfReferencingTempTableTerminates(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT * INTO #t FROM #t\nSELECT | FROM #t",
		testCustomersOrders())
	if len(rels) != 0 {
		t.Fatalf("got %d relations (%q), want none", len(rels), columnSpecs(rels[0]))
	}
}

// ---------------------------------------------------------------------------
// PIVOT / UNPIVOT
// ---------------------------------------------------------------------------

// PIVOT drops the aggregated column and the one it spreads, and adds the
// IN-list names, typed by the aggregate. Before this parsed, the clause keyword was read as the alias
// and the source's own columns were offered under the name PIVOT.
func TestResolvePivotOutputColumns(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT | FROM dbo.Orders PIVOT (SUM(Total) FOR CustomerId IN ([1], [2])) AS p",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "p")), "Id:int 1:decimal 2:decimal"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

// COUNT(*) aggregates no column, so only the pivoted column goes; its
// outputs are int all the same.
func TestResolvePivotOverNoColumnKeepsTheRest(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT | FROM dbo.Orders PIVOT (COUNT(*) FOR CustomerId IN ([1])) AS p",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "p")), "Id:int Total:decimal 1:int"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

// UNPIVOT drops the IN-list columns and adds the value and name columns. The
// value column takes the type the unpivoted columns share.
func TestResolveUnpivotOutputColumns(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT | FROM dbo.Orders UNPIVOT (Amount FOR Kind IN (Id, Total)) AS u",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "u")), "CustomerId:int Amount:int Kind:"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

func TestResolvePivotOverDerivedTable(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT | FROM (SELECT CustomerId, Total FROM dbo.Orders) src PIVOT (SUM(Total) FOR CustomerId IN ([1])) AS p",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "p")), "1:decimal"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

func TestResolvePivotOverTempTable(t *testing.T) {
	rels := resolveTestSQL(t,
		"CREATE TABLE #t (Id int, Yr int, Amt money)\nSELECT | FROM #t PIVOT (SUM(Amt) FOR Yr IN ([2005], [2006])) AS p",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "p")), "Id:int 2005:money 2006:money"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

// TestPivotAggregateType pins each modelled aggregate to the type
// sys.dm_exec_describe_first_result_set reports for it (checked on SQL Server
// 2016 and 2025), and everything else to untyped — CHECKSUM_AGG and
// STRING_AGG among them, which PIVOT refuses.
func TestPivotAggregateType(t *testing.T) {
	col := func(dt string, p, s int) gosmo.CatalogColumn {
		return gosmo.CatalogColumn{Name: "x", DataType: gosmo.DataType(dt), Precision: p, Scale: s, MaxLength: 20}
	}
	cases := []struct {
		fn   string
		arg  gosmo.CatalogColumn
		want string
	}{
		{"COUNT", gosmo.CatalogColumn{}, "int"},
		{"count", col("varchar", 0, 0), "int"},
		{"COUNT_BIG", col("datetime2", 0, 3), "bigint"},
		{"MIN", col("varchar", 0, 0), "varchar(20)"},
		{"MAX", col("datetime2", 0, 3), "datetime2(3)"},
		{"MAX", col("smallmoney", 0, 0), "smallmoney"},
		{"Max", col("decimal", 10, 2), "decimal(10,2)"},
		{"SUM", col("tinyint", 0, 0), "int"},
		{"SUM", col("smallint", 0, 0), "int"},
		{"AVG", col("int", 0, 0), "int"},
		{"SUM", col("bigint", 0, 0), "bigint"},
		{"AVG", col("BIGINT", 0, 0), "bigint"},
		{"SUM", col("decimal", 10, 2), "decimal(38,2)"},
		{"AVG", col("decimal", 10, 2), "decimal(38,6)"},
		{"SUM", col("numeric", 9, 8), "numeric(38,8)"},
		{"AVG", col("numeric", 9, 8), "numeric(38,8)"},
		{"SUM", col("smallmoney", 0, 0), "money"},
		{"AVG", col("money", 0, 0), "money"},
		{"SUM", col("real", 0, 0), "float"},
		{"AVG", col("float", 0, 0), "float"},
		{"APPROX_COUNT_DISTINCT", col("nvarchar", 0, 0), "bigint"},
		{"approx_count_distinct", gosmo.CatalogColumn{}, "bigint"},
		{"STDEV", col("tinyint", 0, 0), "float"},
		{"STDEVP", col("int", 0, 0), "float"},
		{"VAR", col("bigint", 0, 0), "float"},
		{"VARP", col("decimal", 10, 2), "float"},
		{"stdev", col("NUMERIC", 9, 8), "float"},
		{"VAR", col("smallmoney", 0, 0), "float"},
		{"VARP", col("money", 0, 0), "float"},
		{"STDEVP", col("real", 0, 0), "float"},
		{"STDEV", col("float", 0, 0), "float"},

		{"SUM", col("varchar", 0, 0), "column"},
		{"AVG", col("datetime2", 0, 3), "column"},
		{"SUM", gosmo.CatalogColumn{}, "column"},
		{"MIN", gosmo.CatalogColumn{}, "column"},
		{"STDEV", col("bit", 0, 0), "column"},
		{"VAR", col("varchar", 0, 0), "column"},
		{"VARP", gosmo.CatalogColumn{}, "column"},
		{"CHECKSUM_AGG", col("int", 0, 0), "column"},
		{"STRING_AGG", col("varchar", 0, 0), "column"},
		{"", col("int", 0, 0), "column"},
	}
	for _, c := range cases {
		got := pivotAggregateType(c.fn, c.arg)
		if s := formatColumnType(got); s != c.want {
			t.Errorf("%s(%s) = %q, want %q", c.fn, formatColumnType(c.arg), s, c.want)
		}
		if got.Name != "" {
			t.Errorf("%s(%s) carries the argument's name %q", c.fn, formatColumnType(c.arg), got.Name)
		}
	}
}

// A qualified aggregate is user-defined whatever its name, so its outputs
// stay untyped rather than borrowing a built-in's rule.
func TestResolvePivotQualifiedAggregateIsUntyped(t *testing.T) {
	rels := resolveTestSQL(t,
		"SELECT | FROM dbo.Orders PIVOT (dbo.SUM(Total) FOR CustomerId IN ([1])) AS p",
		testCustomersOrders())
	if got, want := columnSpecs(oneRelation(t, rels, "p")), "Id:int 1:"; got != want {
		t.Errorf("columns = %q, want %q", got, want)
	}
}

// A user-defined aggregate's PIVOT columns take its declared return type —
// sys.dm_exec_describe_first_result_set reports exactly that, nullable, for a
// CLR aggregate (checked on SQL Server 2025) — looked up by schema in the
// connected database, the one a three-part call names, or the sys schema's.
// Anything it can't name exactly stays untyped, and a qualified call never
// borrows a built-in's rule.
func TestResolvePivotUserDefinedAggregate(t *testing.T) {
	agg := func(schema, name, dt string) gosmo.CatalogAggregate {
		return gosmo.CatalogAggregate{Schema: schema, Name: name, Returns: gosmo.CatalogColumn{DataType: gosmo.DataType(dt)}}
	}
	own := &gosmo.Catalog{Objects: testCustomersOrders(), Aggregates: []gosmo.CatalogAggregate{
		agg("dbo", "Concat", "nvarchar"),
		agg("dbo", "SUM", "bigint"),
		agg("stats", "Median", "float"),
	}}
	billing := newCompletionInventory(&gosmo.Catalog{Aggregates: []gosmo.CatalogAggregate{agg("dbo", "Spread", "real")}})
	sys := newCompletionInventory(&gosmo.Catalog{Aggregates: []gosmo.CatalogAggregate{agg("sys", "ORMask", "varbinary")}})
	configure := func(rc *resolveCtx) {
		rc.sysInv = sys
		rc.otherDB = func(name string) (*completionInventory, bool) {
			switch strings.ToLower(name) {
			case "billing":
				return billing, false
			case "testdb":
				return rc.inv, false
			}
			return nil, false
		}
	}
	cases := []struct{ name, call, want string }{
		{"own database", "dbo.Concat(Total)", "Id:int 1:nvarchar"},
		{"another schema", "stats.Median(Total)", "Id:int 1:float"},
		{"names fold in a CI database", "DBO.concat(Total)", "Id:int 1:nvarchar"},
		{"bracketed parts", "[dbo].[Concat](Total)", "Id:int 1:nvarchar"},
		{"shadows the built-in of that name", "dbo.SUM(Total)", "Id:int 1:bigint"},
		{"three-part, own database", "testdb.dbo.Concat(Total)", "Id:int 1:nvarchar"},
		{"three-part, another database", "Billing.dbo.Spread(Total)", "Id:int 1:real"},
		{"sys schema", "sys.ORMask(Total)", "Id:int 1:varbinary"},
		{"the built-in, unqualified", "SUM(Total)", "Id:int 1:decimal"},

		{"wrong schema", "stats.Concat(Total)", "Id:int 1:"},
		{"unknown aggregate", "dbo.Nope(Total)", "Id:int 1:"},
		{"omitted schema part", "Billing..Spread(Total)", "Id:int 1:"},
		{"another database's aggregate in this one", "dbo.Spread(Total)", "Id:int 1:"},
		{"unknown database", "Nope.dbo.Concat(Total)", "Id:int 1:"},
		{"four parts", "srv.Billing.dbo.Spread(Total)", "Id:int 1:"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rels := resolveTestSQLWith(t,
				"SELECT | FROM dbo.Orders PIVOT ("+c.call+" FOR CustomerId IN ([1])) AS p", own, configure)
			r := oneRelation(t, rels, "p")
			if got := columnSpecs(r); got != c.want {
				t.Errorf("columns = %q, want %q", got, c.want)
			}
			if cols := r.columns(); !cols[len(cols)-1].IsNullable {
				t.Errorf("pivoted column is NOT NULL; an IN value with no rows reads NULL")
			}
		})
	}
}
