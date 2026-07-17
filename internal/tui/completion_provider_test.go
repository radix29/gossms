package tui

import (
	"sort"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// linesAndCursor splits s (which must contain exactly one '|' cursor
// marker) into Editor-shaped [][]rune lines plus the (row, col) the marker
// occupied, so a completion test can be written as one readable string
// instead of assembling lines and coordinates by hand.
func linesAndCursor(t *testing.T, s string) (lines [][]rune, row, col int) {
	t.Helper()
	marker := strings.IndexByte(s, '|')
	if marker < 0 {
		t.Fatalf("test SQL %q has no | cursor marker", s)
	}
	s = s[:marker] + s[marker+1:]
	parts := strings.Split(s, "\n")
	lines = make([][]rune, len(parts))
	for i, p := range parts {
		lines[i] = []rune(p)
	}
	offset := marker
	for r, p := range parts {
		if offset <= len(p) {
			return lines, r, offset
		}
		offset -= len(p) + 1
	}
	return lines, len(parts) - 1, len(parts[len(parts)-1])
}

// newTestQueryPanelWithInventory builds a QueryPanel wired to a fake open
// connection and a hand-filled, already-loaded completionInventory — no
// database, no goroutine, matching every other pure-function test here.
func newTestQueryPanelWithInventory(t *testing.T, database string, objects []gosmo.CatalogObject) *QueryPanel {
	t.Helper()
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	sc := addTestConn(a, "testserver")
	qp.conn = sc
	qp.database = database

	cat := &gosmo.Catalog{Objects: objects}
	seen := map[string]bool{}
	for _, o := range objects {
		if !seen[o.Schema] {
			seen[o.Schema] = true
			cat.Schemas = append(cat.Schemas, o.Schema)
		}
	}
	sort.Strings(cat.Schemas)

	key := completionInventoryKey(sc.Opts, database)
	a.completionInventories = map[string]*completionInventory{key: newCompletionInventory(cat)}

	// Seed an already-loaded (empty) sys-schema inventory too — like the
	// per-database one above, this keeps ensureSysCompletionInventory from
	// finding its key absent and starting a real background load against
	// the fake connection's nil gosmo.Server, which would panic.
	sysKey := sysCompletionInventoryKey(sc.Opts)
	a.sysCompletionInventories = map[string]*completionInventory{sysKey: newCompletionInventory(&gosmo.Catalog{})}
	return qp
}

func testCustomersOrders() []gosmo.CatalogObject {
	return []gosmo.CatalogObject{
		{
			ObjectID: 1, Schema: "dbo", Name: "Customers", Type: gosmo.CatalogTable,
			Columns: []gosmo.CatalogColumn{
				{Name: "Id", DataType: "int", IsNullable: false},
				{Name: "Name", DataType: "nvarchar", MaxLength: 100, IsNullable: false},
				{Name: "Email", DataType: "varchar", MaxLength: 200, IsNullable: true},
			},
		},
		{
			ObjectID: 2, Schema: "dbo", Name: "Orders", Type: gosmo.CatalogTable,
			Columns: []gosmo.CatalogColumn{
				{Name: "Id", DataType: "int", IsNullable: false},
				{Name: "CustomerId", DataType: "int", IsNullable: false},
				{Name: "Total", DataType: "decimal", Precision: 18, Scale: 2, IsNullable: false},
			},
		},
		{
			ObjectID: 3, Schema: "dbo", Name: "vActiveCustomers", Type: gosmo.CatalogView,
			Columns: []gosmo.CatalogColumn{{Name: "Id", DataType: "int"}},
		},
		{
			ObjectID: 4, Schema: "sales", Name: "Region", Type: gosmo.CatalogTable,
			Columns: []gosmo.CatalogColumn{{Name: "Code", DataType: "char", MaxLength: 2}},
		},
	}
}

func labels(items []controls.CompletionItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Label
	}
	return out
}

func containsLabel(items []controls.CompletionItem, label string) bool {
	for _, it := range items {
		if it.Label == label {
			return true
		}
	}
	return false
}

func TestSQLCompletionAfterFromOffersSchemasTablesViews(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM |")

	items, from := qp.sqlCompletionCandidates(lines, row, col)
	if from != col {
		t.Errorf("replaceFrom = %d, want %d (nothing typed yet)", from, col)
	}
	for _, want := range []string{"dbo", "sales", "dbo.Customers", "dbo.Orders", "dbo.vActiveCustomers"} {
		if !containsLabel(items, want) {
			t.Errorf("items %v missing %q", labels(items), want)
		}
	}
}

func TestSQLCompletionAfterSchemaDotOffersSchemaMembers(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.|")

	items, from := qp.sqlCompletionCandidates(lines, row, col)
	wantFrom := len([]rune("SELECT * FROM dbo."))
	if from != wantFrom {
		t.Errorf("replaceFrom = %d, want %d", from, wantFrom)
	}
	for _, want := range []string{"dbo.Customers", "dbo.Orders", "dbo.vActiveCustomers"} {
		if !containsLabel(items, want) {
			t.Errorf("items %v missing %q", labels(items), want)
		}
	}
	if containsLabel(items, "sales.Region") {
		t.Errorf("items %v should not include a different schema's table", labels(items))
	}
}

func TestSQLCompletionAliasDotWithASOffersColumns(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.Customers AS c WHERE c.|")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	for _, want := range []string{"Id", "Name", "Email"} {
		if !containsLabel(items, want) {
			t.Errorf("items %v missing column %q", labels(items), want)
		}
	}
	if containsLabel(items, "CustomerId") {
		t.Errorf("items %v should not include Orders' column", labels(items))
	}
}

func TestSQLCompletionAliasDotWithoutASOffersColumns(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.Customers c WHERE c.|")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "Name") {
		t.Errorf("items %v missing Name (bare alias without AS)", labels(items))
	}
}

func TestSQLCompletionUnqualifiedTableNameAsAlias(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.Customers WHERE Customers.|")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "Email") {
		t.Errorf("items %v missing Email (table's own name used as qualifier)", labels(items))
	}
}

func TestSQLCompletionSuppressedInsideStringLiteral(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.Customers WHERE Name = 'foo|bar'")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if len(items) != 0 {
		t.Errorf("items = %v, want none inside a string literal", labels(items))
	}
}

func TestSQLCompletionSuppressedInsideLineComment(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.Customers -- note foo|")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if len(items) != 0 {
		t.Errorf("items = %v, want none inside a line comment", labels(items))
	}
}

func TestSQLCompletionSuppressedInsideBlockComment(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.Customers /* foo|bar */")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if len(items) != 0 {
		t.Errorf("items = %v, want none inside a block comment", labels(items))
	}
}

func TestSQLCompletionColumnContextFallsBackToObjectListWhenNothingInScope(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT |")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "dbo.Customers") {
		t.Errorf("items %v want the object list when no FROM has been typed yet", labels(items))
	}
}

func TestSQLCompletionJoinUnionsColumnsAndOffersQualifiers(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT * FROM dbo.Customers c JOIN dbo.Orders o ON c.Id = o.CustomerId WHERE |")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	for _, want := range []string{"c", "o", "Name", "CustomerId", "Total"} {
		if !containsLabel(items, want) {
			t.Errorf("items %v missing %q", labels(items), want)
		}
	}
	idCount := 0
	for _, it := range items {
		if it.Label == "Id" {
			idCount++
		}
	}
	if idCount != 1 {
		t.Errorf("Id appeared %d times, want 1 (deduplicated across joined tables)", idCount)
	}
}

func TestSQLCompletionUnresolvedQualifierReturnsNothing(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT zz.| FROM dbo.Customers c")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if len(items) != 0 {
		t.Errorf("items = %v, want none for an unresolvable qualifier", labels(items))
	}
}

func TestSQLCompletionStatementScopedBySemicolon(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT * FROM dbo.Orders AS o WHERE o.Id = 1;\nSELECT o.|")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if len(items) != 0 {
		t.Errorf("items = %v, want none — alias %q from an earlier ';'-separated statement must not leak forward", labels(items), "o")
	}
}

func TestSQLCompletionStatementScopedByGoBatchSeparator(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT * FROM dbo.Orders AS o\nGO\nSELECT o.|")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if len(items) != 0 {
		t.Errorf("items = %v, want none — alias %q from an earlier GO-separated batch must not leak forward", labels(items), "o")
	}
}

// ---------------------------------------------------------------------------
// DML-keyword statement boundaries: multiple statements stacked in the
// editor with no ';' between them (SSMS never requires one) must not bleed
// FROM-scope into each other, but legitimate multi-clause constructs that
// share one real statement (UNION, CTEs, INSERT...SELECT) must not be
// split apart either.
// ---------------------------------------------------------------------------

func TestSQLCompletionNoSemicolonBetweenStatementsDoesNotLeak(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT * FROM dbo.Customers\nSELECT * FROM dbo.Orders\nSELECT |")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if containsLabel(items, "Name") || containsLabel(items, "Total") {
		t.Errorf("items %v should not pick up columns from unrelated statements stacked above with no ';'", labels(items))
	}
	// No FROM of its own -> falls back to the object list, same as
	// TestSQLCompletionColumnContextFallsBackToObjectListWhenNothingInScope.
	if !containsLabel(items, "dbo.Customers") {
		t.Errorf("items %v should fall back to the object list", labels(items))
	}
}

func TestSQLCompletionNoSemicolonBetweenStatementsBackward(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT * FROM dbo.Customers c\nSELECT o.|\nFROM dbo.Orders o")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "Total") {
		t.Errorf("items %v missing Orders.Total — this statement's own alias must resolve", labels(items))
	}
	if containsLabel(items, "Name") {
		t.Errorf("items %v should not pick up Customers' Name from the unrelated statement above with no ';'", labels(items))
	}
}

func TestSQLCompletionUnionedSelectsShareOneStatement(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT Id FROM dbo.Customers\nUNION\nSELECT |\nFROM dbo.Orders")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "Id") {
		t.Errorf("items %v missing Id — a UNION'd SELECT is the same statement, not a new one", labels(items))
	}
}

// CTE alias resolution itself ("FROM cte c" -> cte's columns) is out of
// scope (see the package doc comment) since "cte" isn't a real catalog
// object, so this checks dmlStatementStarts directly instead of round-
// tripping through sqlCompletionCandidates: WITH's own main SELECT
// (after its parenthesized CTE body closes) must not itself be flagged as
// a second, separate statement.
func TestDMLStatementStartsWithClauseMainSelectNotSplit(t *testing.T) {
	buf := []rune("WITH cte AS (SELECT Id FROM dbo.Customers) SELECT * FROM cte")
	tokens, _, _, _ := tokenizeSQLPrefix(buf, len(buf))
	starts := dmlStatementStarts(tokens)
	if len(starts) != 1 {
		t.Fatalf("dmlStatementStarts = %v, want exactly 1 (WITH itself; its own main SELECT is not a new statement)", starts)
	}
}

func TestDMLStatementStartsUnionChainIsOneStatement(t *testing.T) {
	buf := []rune("SELECT Id FROM A UNION ALL SELECT Id FROM B EXCEPT SELECT Id FROM C")
	tokens, _, _, _ := tokenizeSQLPrefix(buf, len(buf))
	starts := dmlStatementStarts(tokens)
	if len(starts) != 1 {
		t.Fatalf("dmlStatementStarts = %v, want exactly 1 — UNION ALL/EXCEPT chain a SELECT onto the same statement", starts)
	}
}

func TestDMLStatementStartsSubqueryNotCountedAsStatement(t *testing.T) {
	buf := []rune("SELECT * FROM A WHERE Id IN (SELECT Id FROM B)")
	tokens, _, _, _ := tokenizeSQLPrefix(buf, len(buf))
	starts := dmlStatementStarts(tokens)
	if len(starts) != 1 {
		t.Fatalf("dmlStatementStarts = %v, want exactly 1 — a parenthesized subquery's SELECT is not a new statement", starts)
	}
}

func TestDMLStatementStartsBackToBackWithoutSemicolon(t *testing.T) {
	buf := []rune("SELECT * FROM A SELECT * FROM B")
	tokens, _, _, _ := tokenizeSQLPrefix(buf, len(buf))
	starts := dmlStatementStarts(tokens)
	if len(starts) != 2 {
		t.Fatalf("dmlStatementStarts = %v, want exactly 2 — two SELECTs with no ';' and no UNION between them are two statements", starts)
	}
}

func TestSQLCompletionInsertSelectSharesOneStatement(t *testing.T) {
	objects := []gosmo.CatalogObject{
		{ObjectID: 1, Schema: "dbo", Name: "Archive", Type: gosmo.CatalogTable,
			Columns: []gosmo.CatalogColumn{{Name: "Id", DataType: "int"}}},
		{ObjectID: 2, Schema: "dbo", Name: "Source", Type: gosmo.CatalogTable,
			Columns: []gosmo.CatalogColumn{{Name: "SrcId", DataType: "int"}}},
	}
	qp := newTestQueryPanelWithInventory(t, "testdb", objects)
	lines, row, col := linesAndCursor(t, "INSERT INTO dbo.Archive\nSELECT |\nFROM dbo.Source")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "SrcId") {
		t.Errorf("items %v missing SrcId — INSERT...SELECT is one statement, not two", labels(items))
	}
}

func TestSQLCompletionInsertValuesThenNewStatement(t *testing.T) {
	objects := []gosmo.CatalogObject{
		{ObjectID: 1, Schema: "dbo", Name: "Archive", Type: gosmo.CatalogTable,
			Columns: []gosmo.CatalogColumn{{Name: "Id", DataType: "int"}}},
		{ObjectID: 2, Schema: "dbo", Name: "Source", Type: gosmo.CatalogTable,
			Columns: []gosmo.CatalogColumn{{Name: "SrcId", DataType: "int"}}},
	}
	qp := newTestQueryPanelWithInventory(t, "testdb", objects)
	lines, row, col := linesAndCursor(t,
		"INSERT INTO dbo.Archive VALUES (1)\nSELECT s.|\nFROM dbo.Source s")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "SrcId") {
		t.Errorf("items %v missing SrcId — this SELECT's own alias must resolve", labels(items))
	}
}

// ---------------------------------------------------------------------------
// Whole-statement FROM-scope: a table/alias typed after the cursor (e.g.
// "SELECT | FROM Customers c", the common order of writing a query in SSMS)
// must resolve just as well as one already typed above the cursor.
// ---------------------------------------------------------------------------

func TestSQLCompletionColumnContextWhenFromTypedAfterCursor(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT |\nFROM dbo.Customers c")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	for _, want := range []string{"Id", "Name", "Email", "c"} {
		if !containsLabel(items, want) {
			t.Errorf("items %v missing %q", labels(items), want)
		}
	}
	if containsLabel(items, "dbo.Customers") {
		t.Errorf("items %v should show column context, not the object list, once FROM is typed later in the statement", labels(items))
	}
}

func TestSQLCompletionAliasDotResolvesWhenFromTypedAfterCursor(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT c.|\nFROM dbo.Customers c")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "Name") {
		t.Errorf("items %v missing Name — alias %q defined later in the statement should still resolve", labels(items), "c")
	}
}

func TestSQLCompletionForwardScanStopsAtSemicolon(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT o.| FROM dbo.Customers o;\nSELECT * FROM dbo.Orders o")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "Email") {
		t.Errorf("items %v missing Customers' Email — alias %q should resolve within this statement", labels(items), "o")
	}
	if containsLabel(items, "CustomerId") {
		t.Errorf("items %v should not pick up alias %q from the next ';'-separated statement's Orders FROM", labels(items), "o")
	}
}

func TestSQLCompletionForwardScanStopsAtGoBatchSeparator(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT o.|\nGO\nSELECT * FROM dbo.Orders o")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if len(items) != 0 {
		t.Errorf("items = %v, want none — alias %q from a later GO-separated batch must not resolve either", labels(items), "o")
	}
}

// ---------------------------------------------------------------------------
// replaceFrom is a column on the cursor's row. The provider tokenizes a
// flattened whole-buffer copy, but the Editor contract replaces
// [replaceFrom, col) on the cursor's own row and anchors the popup at it.
// Regression: buffer offsets were returned instead, so on any row after the
// first a commit appended ("from paPatients") and the popup drew far right
// of the cursor.
// ---------------------------------------------------------------------------

func TestSQLCompletionReplaceFromIsCursorRowColumnOnLaterRows(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "-- patients query\nSELECT * FROM Cus|")

	items, from := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "dbo.Customers") {
		t.Fatalf("items %v missing dbo.Customers", labels(items))
	}
	if want := col - len("Cus"); from != want {
		t.Errorf("replaceFrom = %d, want %d (a column on the cursor's row, not a buffer offset)", from, want)
	}
}

func TestSQLCompletionAliasDotColumnOnLaterRow(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT *\nFROM dbo.Customers c\nWHERE c.Na|")

	items, from := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "Name") {
		t.Fatalf("items %v missing Name", labels(items))
	}
	if want := col - len("Na"); from != want {
		t.Errorf("replaceFrom = %d, want %d", from, want)
	}
}

func TestSQLCompletionKeywordCollidingPrefixStillReplaces(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM OR|")

	items, from := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "dbo.Orders") {
		t.Fatalf("items %v missing dbo.Orders for prefix \"OR\"", labels(items))
	}
	if want := col - len("OR"); from != want {
		t.Errorf("replaceFrom = %d, want %d — \"OR\" lexes as a keyword but is still the word being typed", from, want)
	}
}

// ---------------------------------------------------------------------------
// Open bracket identifiers: "[Cus|" completes with the whole "[..." span as
// the replaced prefix instead of suppressing like a string literal.
// ---------------------------------------------------------------------------

func TestSQLCompletionInsideOpenBracketIdentifier(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM [Cus|")

	items, from := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "dbo.Customers") {
		t.Fatalf("items %v missing dbo.Customers for open-bracket prefix", labels(items))
	}
	if want := col - len("[Cus"); from != want {
		t.Errorf("replaceFrom = %d, want %d — the whole \"[Cus\" span gets replaced", from, want)
	}
}

func TestSQLCompletionQualifiedOpenBracket(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.[Ord|")

	items, from := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "dbo.Orders") {
		t.Fatalf("items %v missing dbo.Orders after \"dbo.[Ord\"", labels(items))
	}
	if want := col - len("[Ord"); from != want {
		t.Errorf("replaceFrom = %d, want %d", from, want)
	}
}

func TestSQLCompletionOpenBracketNameWithSpace(t *testing.T) {
	objects := []gosmo.CatalogObject{{
		ObjectID: 1, Schema: "dbo", Name: "Order Details", Type: gosmo.CatalogTable,
		Columns: []gosmo.CatalogColumn{{Name: "OrderId", DataType: "int"}},
	}}
	qp := newTestQueryPanelWithInventory(t, "testdb", objects)
	lines, row, col := linesAndCursor(t, "SELECT * FROM [Order De|")

	items, from := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "dbo.Order Details") {
		t.Fatalf("items %v missing dbo.Order Details", labels(items))
	}
	for _, it := range items {
		if it.Label == "dbo.Order Details" && it.Text != "[Order Details]" {
			t.Errorf("Text = %q, want %q (needs re-quoting)", it.Text, "[Order Details]")
		}
	}
	if want := col - len("[Order De"); from != want {
		t.Errorf("replaceFrom = %d, want %d", from, want)
	}
}

func TestSQLCompletionOpenBracketColumnContextScansForwardPastBracket(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT [N|] FROM dbo.Customers c")

	items, from := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "Name") {
		t.Fatalf("items %v missing Name — FROM after the open bracket must still resolve", labels(items))
	}
	if want := col - len("[N"); from != want {
		t.Errorf("replaceFrom = %d, want %d", from, want)
	}
}

// ---------------------------------------------------------------------------
// sys-schema inventory: server-level, shared across every database.
// ---------------------------------------------------------------------------

func sysCatalogFixture() *gosmo.Catalog {
	return &gosmo.Catalog{
		Schemas: []string{"sys"},
		Objects: []gosmo.CatalogObject{
			{
				ObjectID: 100, Schema: "sys", Name: "objects", Type: gosmo.CatalogView,
				Columns: []gosmo.CatalogColumn{
					{Name: "object_id", DataType: "int"},
					{Name: "name", DataType: "sysname"},
				},
			},
			{
				ObjectID: 101, Schema: "sys", Name: "columns", Type: gosmo.CatalogView,
				Columns: []gosmo.CatalogColumn{{Name: "column_id", DataType: "int"}},
			},
		},
	}
}

func TestSQLCompletionSysSchemaDotOffersSystemCatalogViews(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	sysKey := sysCompletionInventoryKey(qp.conn.Opts)
	qp.app.sysCompletionInventories[sysKey] = newCompletionInventory(sysCatalogFixture())

	lines, row, col := linesAndCursor(t, "SELECT * FROM sys.|")
	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	for _, want := range []string{"sys.objects", "sys.columns"} {
		if !containsLabel(items, want) {
			t.Errorf("items %v missing %q", labels(items), want)
		}
	}
}

func TestSQLCompletionSysAllKeywordPrefixResolvesMembers(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	sysKey := sysCompletionInventoryKey(qp.conn.Opts)
	qp.app.sysCompletionInventories[sysKey] = newCompletionInventory(&gosmo.Catalog{
		Schemas: []string{"sys"},
		Objects: []gosmo.CatalogObject{
			{ObjectID: 102, Schema: "sys", Name: "all_objects", Type: gosmo.CatalogView},
			{ObjectID: 103, Schema: "sys", Name: "all_columns", Type: gosmo.CatalogView},
		},
	})

	// "all" lexes as the T-SQL keyword ALL — it must still behave as the
	// member prefix being typed after "sys.".
	lines, row, col := linesAndCursor(t, "SELECT * FROM sys.all|")
	items, from := qp.sqlCompletionCandidates(lines, row, col)
	for _, want := range []string{"sys.all_objects", "sys.all_columns"} {
		if !containsLabel(items, want) {
			t.Errorf("items %v missing %q", labels(items), want)
		}
	}
	if want := col - len("all"); from != want {
		t.Errorf("replaceFrom = %d, want %d — committing must replace \"all\", not append after it", from, want)
	}
}

func TestRetrySysCompletionInventoryKeepsLoadedSnapshot(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	sysKey := sysCompletionInventoryKey(qp.conn.Opts)
	loaded := qp.app.sysCompletionInventories[sysKey]

	qp.app.retrySysCompletionInventory(qp.conn)
	if qp.app.sysCompletionInventories[sysKey] != loaded {
		t.Fatal("retrySysCompletionInventory must keep a successfully loaded sys snapshot (only a failed load reloads)")
	}
}

func TestSQLCompletionSysSchemaAliasColumnsResolve(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	sysKey := sysCompletionInventoryKey(qp.conn.Opts)
	qp.app.sysCompletionInventories[sysKey] = newCompletionInventory(sysCatalogFixture())

	lines, row, col := linesAndCursor(t, "SELECT o.| FROM sys.objects o")
	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	for _, want := range []string{"object_id", "name"} {
		if !containsLabel(items, want) {
			t.Errorf("items %v missing sys.objects column %q", labels(items), want)
		}
	}
}

func TestSQLCompletionSysSchemaListedButObjectsNotUnqualified(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	sysKey := sysCompletionInventoryKey(qp.conn.Opts)
	qp.app.sysCompletionInventories[sysKey] = newCompletionInventory(sysCatalogFixture())

	lines, row, col := linesAndCursor(t, "SELECT * FROM |")
	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if !containsLabel(items, "sys") {
		t.Errorf("items %v missing %q schema", labels(items), "sys")
	}
	if containsLabel(items, "sys.objects") {
		t.Errorf("items %v should not list sys.* objects unqualified (hundreds of them — too noisy)", labels(items))
	}
}

func TestSQLCompletionSysSchemaLoadingShowsPlaceholder(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	sysKey := sysCompletionInventoryKey(qp.conn.Opts)
	qp.app.sysCompletionInventories[sysKey] = &completionInventory{loading: true}

	lines, row, col := linesAndCursor(t, "SELECT * FROM sys.|")
	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if len(items) != 1 || !items[0].Placeholder {
		t.Fatalf("items = %+v, want a single Placeholder row while the sys inventory is still loading", items)
	}
}

func TestSQLCompletionNoConnectionReturnsNothing(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	qp.conn = nil
	lines, row, col := linesAndCursor(t, "SELECT * FROM |")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if len(items) != 0 {
		t.Errorf("items = %v, want none without a connection", labels(items))
	}
}

func TestSQLCompletionDisabledReturnsNothing(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	qp.app.cfg.IntelliSenseDisabled = true
	lines, row, col := linesAndCursor(t, "SELECT * FROM |")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if len(items) != 0 {
		t.Errorf("items = %v, want none while IntelliSense is disabled", labels(items))
	}
}

func TestSQLCompletionLoadingShowsPlaceholder(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	sc := addTestConn(a, "testserver")
	qp.conn = sc
	qp.database = "testdb"
	// Seed a still-loading entry directly — ensureCompletionInventory only
	// starts a background load when the key is absent, and the fake
	// connection's nil gosmo.Server would panic a real load goroutine.
	key := completionInventoryKey(sc.Opts, qp.database)
	a.completionInventories = map[string]*completionInventory{key: {loading: true}}
	lines, row, col := linesAndCursor(t, "SELECT * FROM |")

	items, _ := qp.sqlCompletionCandidates(lines, row, col)
	if len(items) != 1 || !items[0].Placeholder {
		t.Fatalf("items = %+v, want a single Placeholder row while loading", items)
	}
}

// ---------------------------------------------------------------------------
// Tokenizer / context-resolution unit tests
// ---------------------------------------------------------------------------

func TestBracketIfNeeded(t *testing.T) {
	cases := []struct{ name, want string }{
		{"Customers", "Customers"},
		{"Order Details", "[Order Details]"},
		{"select", "[select]"}, // reserved word, even though it's a valid bare identifier shape
		{"1Customers", "[1Customers]"},
		{"a]b", "[a]]b]"},
	}
	for _, c := range cases {
		if got := bracketIfNeeded(c.name); got != c.want {
			t.Errorf("bracketIfNeeded(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFormatColumnType(t *testing.T) {
	cases := []struct {
		name string
		col  gosmo.CatalogColumn
		want string
	}{
		{"varchar with length", gosmo.CatalogColumn{DataType: "varchar", MaxLength: 50, IsNullable: true}, "varchar(50)"},
		{"varchar MAX", gosmo.CatalogColumn{DataType: "varchar", MaxLength: -1, IsNullable: true}, "varchar(MAX)"},
		{"nvarchar halves byte length", gosmo.CatalogColumn{DataType: "nvarchar", MaxLength: 100, IsNullable: true}, "nvarchar(50)"},
		{"decimal precision/scale", gosmo.CatalogColumn{DataType: "decimal", Precision: 18, Scale: 2, IsNullable: true}, "decimal(18,2)"},
		{"not null suffix", gosmo.CatalogColumn{DataType: "int", IsNullable: false}, "int, not null"},
		{"plain int nullable", gosmo.CatalogColumn{DataType: "int", IsNullable: true}, "int"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := formatColumnType(c.col); got != c.want {
				t.Errorf("formatColumnType(%+v) = %q, want %q", c.col, got, c.want)
			}
		})
	}
}

func TestTokenizeSQLPrefixReportsBracketStateAndStart(t *testing.T) {
	buf := []rune("SELECT * FROM [Order ")
	_, state, _, quoteStart := tokenizeSQLPrefix(buf, len(buf))
	if state != sqlLexBracket {
		t.Errorf("state = %v, want sqlLexBracket for an unterminated [ ident", state)
	}
	if want := len([]rune("SELECT * FROM ")); quoteStart != want {
		t.Errorf("quoteStart = %d, want %d (the '[' offset)", quoteStart, want)
	}
}

// Regression: before EXCEPT/INTERSECT were added to sqlKeywords and UNION
// was added to parseFromScope's expectRef-reset list, "UNION SELECT Id
// FROM B" mis-parsed the second SELECT's own column ("Id") as a table
// reference, since nothing between "FROM A" and "Id" ever reset
// expectRef back to false.
func TestParseFromScopeResetsAfterUnion(t *testing.T) {
	buf := []rune("FROM A UNION SELECT Id FROM B")
	tokens, _, _, _ := tokenizeSQLPrefix(buf, len(buf))
	refs := parseFromScope(tokens)
	for _, r := range refs {
		if r.name == "Id" {
			t.Fatalf("parseFromScope refs = %+v — \"Id\" is the second SELECT's column, not a table reference", refs)
		}
	}
}

func TestParseFromScopeHandlesMultipleJoinsAndCommas(t *testing.T) {
	buf := []rune("FROM dbo.Customers c, dbo.Orders AS o JOIN sales.Region r ON 1=1")
	tokens, _, _, _ := tokenizeSQLPrefix(buf, len(buf))
	refs := parseFromScope(tokens)
	if len(refs) != 3 {
		t.Fatalf("parseFromScope returned %d refs, want 3: %+v", len(refs), refs)
	}
	want := []fromRef{
		{schema: "dbo", name: "Customers", alias: "c"},
		{schema: "dbo", name: "Orders", alias: "o"},
		{schema: "sales", name: "Region", alias: "r"},
	}
	for i, w := range want {
		if refs[i] != w {
			t.Errorf("refs[%d] = %+v, want %+v", i, refs[i], w)
		}
	}
}
