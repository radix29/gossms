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
// objects, the same two steps the provider will make in step 5.
func resolveTestSQL(t *testing.T, sql string, objects []gosmo.CatalogObject) []relation {
	t.Helper()
	cursor := strings.Index(sql, "|")
	if cursor < 0 || strings.Count(sql, "|") != 1 {
		t.Fatalf("sql must hold exactly one '|' cursor marker: %q", sql)
	}
	buf := []rune(strings.Replace(sql, "|", "", 1))
	tokens, _, _, _ := sqlparse.TokenizeRange(buf, 0, len(buf), false)
	scope := sqlparse.ScopeAt(tokens, len([]rune(sql[:cursor])))
	if scope.Query == nil {
		t.Fatalf("no query in scope for %q", sql)
	}
	cat := &gosmo.Catalog{Objects: objects}
	seen := map[string]bool{}
	for _, o := range objects {
		if !seen[o.Schema] {
			seen[o.Schema] = true
			cat.Schemas = append(cat.Schemas, o.Schema)
		}
	}
	rc := newResolveCtx(newCompletionInventory(cat), nil, scope.CTEs)
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
