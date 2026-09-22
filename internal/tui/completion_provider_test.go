package tui

import (
	"slices"
	"sort"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tui/sqlparse"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// linesAndCursor splits s (which must contain exactly one '|' cursor marker)
// into Editor-shaped [][]rune lines plus the (row, col) the marker occupied,
// so a completion test can be written as one readable string.
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

	// Seed an already-loaded (empty) sys-schema inventory too: like the
	// per-database one above, this keeps ensureSysCompletionInventory from
	// finding its key absent and starting a real background load against the
	// fake connection's nil gosmo.Server, which would panic.
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

// completionReq packages a test's (lines, row, col) the way Editor packages a
// real one. Text is left zero: these tests drive the provider directly, so
// there is no document to identify, and a zero TextRevision is the "cannot
// justify a resume" case — the provider answers from a full scan, which is
// what every expectation here is written against.
func completionReq(lines [][]rune, row, col int) controls.CompletionRequest {
	return controls.CompletionRequest{Lines: lines, Row: row, Col: col}
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

	items, from := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, from := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "Name") {
		t.Errorf("items %v missing Name (bare alias without AS)", labels(items))
	}
}

func TestSQLCompletionUnqualifiedTableNameAsAlias(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.Customers WHERE Customers.|")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "Email") {
		t.Errorf("items %v missing Email (table's own name used as qualifier)", labels(items))
	}
}

func TestSQLCompletionSuppressedInsideStringLiteral(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.Customers WHERE Name = 'foo|bar'")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if len(items) != 0 {
		t.Errorf("items = %v, want none inside a string literal", labels(items))
	}
}

func TestSQLCompletionSuppressedInsideLineComment(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.Customers -- note foo|")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if len(items) != 0 {
		t.Errorf("items = %v, want none inside a line comment", labels(items))
	}
}

func TestSQLCompletionSuppressedInsideBlockComment(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT * FROM dbo.Customers /* foo|bar */")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if len(items) != 0 {
		t.Errorf("items = %v, want none inside a block comment", labels(items))
	}
}

func TestSQLCompletionColumnContextFallsBackToObjectListWhenNothingInScope(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT |")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "dbo.Customers") {
		t.Errorf("items %v want the object list when no FROM has been typed yet", labels(items))
	}
}

func TestSQLCompletionJoinUnionsColumnsAndOffersQualifiers(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT * FROM dbo.Customers c JOIN dbo.Orders o ON c.Id = o.CustomerId WHERE |")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if len(items) != 0 {
		t.Errorf("items = %v, want none for an unresolvable qualifier", labels(items))
	}
}

func TestSQLCompletionStatementScopedBySemicolon(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT * FROM dbo.Orders AS o WHERE o.Id = 1;\nSELECT o.|")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if len(items) != 0 {
		t.Errorf("items = %v, want none — alias %q from an earlier ';'-separated statement must not leak forward", labels(items), "o")
	}
}

func TestSQLCompletionStatementScopedByGoBatchSeparator(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT * FROM dbo.Orders AS o\nGO\nSELECT o.|")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if len(items) != 0 {
		t.Errorf("items = %v, want none — alias %q from an earlier GO-separated batch must not leak forward", labels(items), "o")
	}
}

// ---------------------------------------------------------------------------
// DML-keyword statement boundaries: statements stacked with no ';' between
// them (SSMS never requires one) must not bleed FROM-scope into each other,
// while multi-clause constructs sharing one real statement (UNION, CTEs,
// INSERT...SELECT) must not be split apart.
// ---------------------------------------------------------------------------

func TestSQLCompletionNoSemicolonBetweenStatementsDoesNotLeak(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT * FROM dbo.Customers\nSELECT * FROM dbo.Orders\nSELECT |")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "Id") {
		t.Errorf("items %v missing Id — a UNION'd SELECT is the same statement, not a new one", labels(items))
	}
}

// CTE alias resolution itself ("FROM cte c" -> cte's columns) is out of scope
// (see the package doc comment) since "cte" isn't a real catalog object, so
// this checks sqlparse.DMLStatementStarts directly rather than round-tripping
// through sqlCompletionCandidates: WITH's main SELECT, after its parenthesized
// CTE body closes, must not be flagged as a second statement.
func TestDMLStatementStartsWithClauseMainSelectNotSplit(t *testing.T) {
	buf := []rune("WITH cte AS (SELECT Id FROM dbo.Customers) SELECT * FROM cte")
	tokens, _, _, _ := sqlparse.TokenizeRange(buf, 0, len(buf), false)
	starts := sqlparse.DMLStatementStarts(tokens)
	if len(starts) != 1 {
		t.Fatalf("sqlparse.DMLStatementStarts = %v, want exactly 1 (WITH itself; its own main SELECT is not a new statement)", starts)
	}
}

func TestDMLStatementStartsUnionChainIsOneStatement(t *testing.T) {
	buf := []rune("SELECT Id FROM A UNION ALL SELECT Id FROM B EXCEPT SELECT Id FROM C")
	tokens, _, _, _ := sqlparse.TokenizeRange(buf, 0, len(buf), false)
	starts := sqlparse.DMLStatementStarts(tokens)
	if len(starts) != 1 {
		t.Fatalf("sqlparse.DMLStatementStarts = %v, want exactly 1 — UNION ALL/EXCEPT chain a SELECT onto the same statement", starts)
	}
}

func TestDMLStatementStartsSubqueryNotCountedAsStatement(t *testing.T) {
	buf := []rune("SELECT * FROM A WHERE Id IN (SELECT Id FROM B)")
	tokens, _, _, _ := sqlparse.TokenizeRange(buf, 0, len(buf), false)
	starts := sqlparse.DMLStatementStarts(tokens)
	if len(starts) != 1 {
		t.Fatalf("sqlparse.DMLStatementStarts = %v, want exactly 1 — a parenthesized subquery's SELECT is not a new statement", starts)
	}
}

func TestDMLStatementStartsBackToBackWithoutSemicolon(t *testing.T) {
	buf := []rune("SELECT * FROM A SELECT * FROM B")
	tokens, _, _, _ := sqlparse.TokenizeRange(buf, 0, len(buf), false)
	starts := sqlparse.DMLStatementStarts(tokens)
	if len(starts) != 2 {
		t.Fatalf("sqlparse.DMLStatementStarts = %v, want exactly 2 — two SELECTs with no ';' and no UNION between them are two statements", starts)
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

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "SrcId") {
		t.Errorf("items %v missing SrcId — this SELECT's own alias must resolve", labels(items))
	}
}

// ---------------------------------------------------------------------------
// Whole-statement FROM-scope: a table/alias typed after the cursor ("SELECT |
// FROM Customers c", the usual order of writing a query) must resolve as well
// as one typed above the cursor.
// ---------------------------------------------------------------------------

func TestSQLCompletionColumnContextWhenFromTypedAfterCursor(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT |\nFROM dbo.Customers c")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "Name") {
		t.Errorf("items %v missing Name — alias %q defined later in the statement should still resolve", labels(items), "c")
	}
}

func TestSQLCompletionForwardScanStopsAtSemicolon(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT o.| FROM dbo.Customers o;\nSELECT * FROM dbo.Orders o")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if len(items) != 0 {
		t.Errorf("items = %v, want none — alias %q from a later GO-separated batch must not resolve either", labels(items), "o")
	}
}

// ---------------------------------------------------------------------------
// replaceFrom is a column on the cursor's row. The provider tokenizes a
// flattened whole-buffer copy, but the Editor contract replaces
// [replaceFrom, col) on the cursor's own row and anchors the popup there —
// returning buffer offsets makes a commit append, and the popup draw far right
// of the cursor, on any row after the first.
// ---------------------------------------------------------------------------

func TestSQLCompletionReplaceFromIsCursorRowColumnOnLaterRows(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "-- patients query\nSELECT * FROM Cus|")

	items, from := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, from := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, from := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, from := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, from := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, from := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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

	items, from := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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
	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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
	items, from := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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
	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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
	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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
	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if len(items) != 1 || !items[0].Placeholder {
		t.Fatalf("items = %+v, want a single Placeholder row while the sys inventory is still loading", items)
	}
}

func TestSQLCompletionNoConnectionReturnsNothing(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	qp.conn = nil
	lines, row, col := linesAndCursor(t, "SELECT * FROM |")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if len(items) != 0 {
		t.Errorf("items = %v, want none without a connection", labels(items))
	}
}

func TestSQLCompletionDisabledReturnsNothing(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	qp.app.cfg.IntelliSenseDisabled = true
	lines, row, col := linesAndCursor(t, "SELECT * FROM |")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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
	// Seed a still-loading entry directly: ensureCompletionInventory only
	// starts a background load when the key is absent, and the fake
	// connection's nil gosmo.Server would panic a real load goroutine.
	key := completionInventoryKey(sc.Opts, qp.database)
	a.completionInventories = map[string]*completionInventory{key: {loading: true}}
	lines, row, col := linesAndCursor(t, "SELECT * FROM |")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
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
		{"untyped synthetic column", gosmo.CatalogColumn{Name: "id"}, "column"},
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
	_, state, _, quoteStart := sqlparse.TokenizeRange(buf, 0, len(buf), false)
	if state != sqlparse.LexBracket {
		t.Errorf("state = %v, want sqlparse.LexBracket for an unterminated [ ident", state)
	}
	if want := len([]rune("SELECT * FROM ")); quoteStart != want {
		t.Errorf("quoteStart = %d, want %d (the '[' offset)", quoteStart, want)
	}
}

// EXCEPT/INTERSECT are in sqlKeywords and UNION resets
// sqlparse.ParseFromScope's expectRef, so "UNION SELECT Id FROM B" doesn't
// mis-parse the second SELECT's column ("Id") as a table reference.
func TestParseFromScopeResetsAfterUnion(t *testing.T) {
	buf := []rune("FROM A UNION SELECT Id FROM B")
	tokens, _, _, _ := sqlparse.TokenizeRange(buf, 0, len(buf), false)
	refs := sqlparse.ParseFromScope(tokens)
	for _, r := range refs {
		if r.Name == "Id" {
			t.Fatalf("sqlparse.ParseFromScope refs = %+v — \"Id\" is the second SELECT's column, not a table reference", refs)
		}
	}
}

func TestParseFromScopeHandlesMultipleJoinsAndCommas(t *testing.T) {
	buf := []rune("FROM dbo.Customers c, dbo.Orders AS o JOIN sales.Region r ON 1=1")
	tokens, _, _, _ := sqlparse.TokenizeRange(buf, 0, len(buf), false)
	refs := sqlparse.ParseFromScope(tokens)
	if len(refs) != 3 {
		t.Fatalf("sqlparse.ParseFromScope returned %d refs, want 3: %+v", len(refs), refs)
	}
	want := []sqlparse.FromRef{
		{Schema: "dbo", Name: "Customers", Alias: "c"},
		{Schema: "dbo", Name: "Orders", Alias: "o"},
		{Schema: "sales", Name: "Region", Alias: "r"},
	}
	for i, w := range want {
		if refs[i] != w {
			t.Errorf("refs[%d] = %+v, want %+v", i, refs[i], w)
		}
	}
}

// ---------------------------------------------------------------------------
// CTEs, derived tables and subqueries (sqlparse.ScopeAt + completion_relations)
// ---------------------------------------------------------------------------

// itemDetail returns the detail text of the item labelled label, or "" when
// no item carries that label.
func itemDetail(items []controls.CompletionItem, label string) string {
	for _, it := range items {
		if it.Label == label {
			return it.Detail
		}
	}
	return ""
}

func TestSQLCompletionCTEColumnsInMainQuery(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "WITH t1 AS (SELECT * FROM dbo.Customers)\nSELECT | FROM t1")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	for _, want := range []string{"Id", "Name", "Email"} {
		if !containsLabel(items, want) {
			t.Errorf("items %v missing %q", labels(items), want)
		}
	}
	if got, want := itemDetail(items, "Name"), "nvarchar(50), not null — t1"; got != want {
		t.Errorf("detail for Name = %q, want %q", got, want)
	}
}

func TestSQLCompletionCTENameOfferedInFromPosition(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "WITH t1 AS (SELECT * FROM dbo.Customers) SELECT * FROM t|")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "t1") {
		t.Fatalf("items %v missing the CTE name t1", labels(items))
	}
	if got := itemDetail(items, "t1"); got != "CTE" {
		t.Errorf("detail for t1 = %q, want %q", got, "CTE")
	}
}

func TestSQLCompletionCTEQualifierResolvesColumns(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "WITH t1 AS (SELECT Id, Name FROM dbo.Customers) SELECT t1.| FROM t1")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if got, want := labels(items), []string{"Id", "Name"}; !slices.Equal(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
}

func TestSQLCompletionChainedCTEColumns(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"WITH a AS (SELECT Id x FROM dbo.Customers),\n     b AS (SELECT x FROM a)\nSELECT | FROM b")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "x") {
		t.Errorf("items %v missing the chained CTE column x", labels(items))
	}
	if got, want := itemDetail(items, "x"), "int, not null — b"; got != want {
		t.Errorf("detail for x = %q, want %q", got, want)
	}
}

func TestSQLCompletionDerivedTableQualifierResolvesColumns(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT * FROM (SELECT Name, Email FROM dbo.Customers) d WHERE d.|")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if got, want := labels(items), []string{"Email", "Name"}; !slices.Equal(got, want) {
		t.Errorf("items = %v, want %v", got, want)
	}
}

// Inside a CTE body the clause state is the body's own, not the outer
// statement's — a FROM there offers tables, not the enclosing SELECT's column
// context.
func TestSQLCompletionInsideCTEBodyUsesItsOwnClause(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "WITH t1 AS (SELECT * FROM |) SELECT * FROM t1")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	for _, want := range []string{"dbo", "dbo.Customers", "dbo.Orders"} {
		if !containsLabel(items, want) {
			t.Errorf("items %v missing %q", labels(items), want)
		}
	}
}

// A column context inside a CTE body resolves against that body's own FROM,
// not the main query's.
func TestSQLCompletionInsideCTEBodyScopesToItsOwnFrom(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "WITH t1 AS (SELECT | FROM dbo.Orders) SELECT * FROM dbo.Customers")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "CustomerId") {
		t.Errorf("items %v missing Orders' CustomerId", labels(items))
	}
	if containsLabel(items, "Email") {
		t.Errorf("items %v leaked Customers' Email into the CTE body", labels(items))
	}
}

// An expression column a CTE body only aliases has no type to report; the
// detail reads "column" rather than asserting one (see formatColumnType).
func TestSQLCompletionCTEExpressionColumnIsUntyped(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "WITH t1 AS (SELECT COUNT(*) n FROM dbo.Orders) SELECT | FROM t1")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if got, want := itemDetail(items, "n"), "column — t1"; got != want {
		t.Errorf("detail for n = %q, want %q", got, want)
	}
}

// "WITH (NOLOCK)" is a table hint, not a CTE clause — nothing may be bound.
func TestSQLCompletionTableHintWithIsNotACTE(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "SELECT | FROM dbo.Customers WITH (NOLOCK)")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if !containsLabel(items, "Email") {
		t.Errorf("items %v missing Customers' Email", labels(items))
	}
}

// ---------------------------------------------------------------------------
// Temp tables, table variables and PIVOT, end to end through the provider
// ---------------------------------------------------------------------------

func TestSQLCompletionOffersDeclaredTempTablesAndVariables(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"CREATE TABLE #staging (Id int)\nDECLARE @rows TABLE (Id int)\nSELECT * FROM |")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if got := itemDetail(items, "#staging"); got != "temp table" {
		t.Errorf("#staging detail = %q, want %q (items %v)", got, "temp table", labels(items))
	}
	if got := itemDetail(items, "@rows"); got != "table variable" {
		t.Errorf("@rows detail = %q, want %q (items %v)", got, "table variable", labels(items))
	}
}

// A sigil name is never bracketed on commit: "[@t]" is a column or object
// name, not the variable, and "[#t]" is not what was typed.
func TestSQLCompletionCommitsSigilNamesUnbracketed(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t, "DECLARE @rows TABLE (Id int)\nSELECT * FROM @|")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	for _, it := range items {
		if it.Label == "@rows" {
			if it.Text != "@rows" {
				t.Errorf("commit text = %q, want %q", it.Text, "@rows")
			}
			return
		}
	}
	t.Errorf("items %v missing @rows", labels(items))
}

func TestSQLCompletionTempTableColumnsInScope(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"CREATE TABLE #staging (Ref int NOT NULL, Note nvarchar(50))\nSELECT | FROM #staging")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	for _, want := range []string{"Ref", "Note"} {
		if !containsLabel(items, want) {
			t.Errorf("items %v missing %q", labels(items), want)
		}
	}
	if got := itemDetail(items, "Note"); !strings.HasPrefix(got, "nvarchar(50)") {
		t.Errorf("Note detail = %q, want it to start with %q", got, "nvarchar(50)")
	}
}

func TestSQLCompletionTempTableMemberLookup(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"DECLARE @rows TABLE (Ref int)\nSELECT @rows.| FROM @rows")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if got := labels(items); len(got) != 1 || got[0] != "Ref" {
		t.Errorf("items = %v, want [Ref]", got)
	}
}

// A declaration in an earlier GO batch is out of scope, and an undeclared
// name resolves to nothing rather than to the catalog table that shares it
// without the sigil.
func TestSQLCompletionTempTableScopedToItsBatch(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"CREATE TABLE #Orders (Ref int)\nGO\nSELECT | FROM #Orders")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if containsLabel(items, "Ref") {
		t.Errorf("items %v offer a column from the previous batch's declaration", labels(items))
	}
	if containsLabel(items, "CustomerId") {
		t.Errorf("items %v resolved #Orders to the catalog table Orders", labels(items))
	}
}

func TestSQLCompletionPivotOutputColumns(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	lines, row, col := linesAndCursor(t,
		"SELECT | FROM dbo.Orders PIVOT (SUM(Total) FOR CustomerId IN ([1], [2])) AS p")

	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	for _, want := range []string{"1", "2", "Id"} {
		if !containsLabel(items, want) {
			t.Errorf("items %v missing %q", labels(items), want)
		}
	}
	for _, unwanted := range []string{"Total", "CustomerId"} {
		if containsLabel(items, unwanted) {
			t.Errorf("items %v still offer %q, which the pivot consumed", labels(items), unwanted)
		}
	}
}

// TestSQLCompletionPrefixCacheMatchesUncached pins the one call site that
// hands req.Text to the panel's sqlparse.PrefixCache. Every other test here
// passes a zero Text, the "cannot justify a resume" case, so without this one
// nothing in the package exercises a warm cache and a mis-plumbed field (a
// constant DirtyFrom, the wrong Version, a Doc shared across tabs) would go
// unnoticed until it served a completion scoped to the wrong batch.
//
// It types a script rune by rune through one panel carrying a real revision,
// and compares each answer against a fresh panel asked the same question with
// no identity — the uncached path the rest of the file treats as the oracle.
// The script is deliberately boundary-dense: the batches a stale boundary
// would leak between are the whole risk.
//
// Mutation-checked on the Version field, the one that can serve a stale
// answer. Dropping Doc or mis-setting DirtyFrom only *disables* the resume, so
// no correctness test can catch either; the benchmarks pin those.
func TestSQLCompletionPrefixCacheMatchesUncached(t *testing.T) {
	const script = "SELECT * FROM dbo.Customers;\n" +
		"GO\n" +
		"CREATE TABLE #t (Ref int);\n" +
		"SELECT c.Id FROM dbo.Customers AS c JOIN dbo.Orders AS o ON o.CustomerId = c.Id;\n" +
		"GO\n" +
		"SELECT t. FROM #t AS t"

	warm := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	doc := new(int) // stands in for the editor's *Document — compared, never read

	full := []rune(script)
	var version uint64
	for n := 1; n <= len(full); n++ {
		text := string(full[:n])
		parts := strings.Split(text, "\n")
		lines := make([][]rune, len(parts))
		for i, part := range parts {
			lines[i] = []rune(part)
		}
		row := len(lines) - 1
		col := len(lines[row])

		// Typing a rune is one mutation on the row it lands in, which is what
		// the editor reports; a newline dirties the row it opens.
		version++
		req := controls.CompletionRequest{Lines: lines, Row: row, Col: col}
		req.Text = controls.TextRevision{Doc: doc, Version: version, DirtyFrom: row}

		gotItems, gotFrom := warm.sqlCompletionCandidates(req)

		cold := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
		wantItems, wantFrom := cold.sqlCompletionCandidates(completionReq(lines, row, col))

		if gotFrom != wantFrom || !slices.Equal(labels(gotItems), labels(wantItems)) {
			t.Fatalf("after typing %d runes (%q):\n cached: from=%d %v\nuncached: from=%d %v",
				n, text, gotFrom, labels(gotItems), wantFrom, labels(wantItems))
		}
	}

	// Typing alone only appends, so every boundary the cache holds stays true
	// and a cache ignoring the revision would pass. These edit the text *above*
	// the cursor, the case the version and DirtyFrom checks exist for: each
	// moves the batch the last line belongs to, so serving it from the previous
	// scan's boundaries offers columns from the wrong batch.
	edits := []struct {
		name string
		text string
	}{
		{"last go commented out", strings.Replace(script, "c.Id;\nGO", "c.Id;\n-- GO", 1)},
		{"first go commented out", strings.Replace(script, "GO\nCREATE", "-- GO\nCREATE", 1)},
		{"block comment opened", strings.Replace(script, "CREATE TABLE", "/* CREATE TABLE", 1)},
		{"back to the original", script},
	}
	for _, e := range edits {
		parts := strings.Split(e.text, "\n")
		lines := make([][]rune, len(parts))
		for i, part := range parts {
			lines[i] = []rune(part)
		}
		row := len(lines) - 1
		col := len(lines[row]) - len(" FROM #t AS t")

		version++
		req := controls.CompletionRequest{Lines: lines, Row: row, Col: col}
		req.Text = controls.TextRevision{Doc: doc, Version: version, DirtyFrom: 1}

		gotItems, gotFrom := warm.sqlCompletionCandidates(req)

		cold := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
		wantItems, wantFrom := cold.sqlCompletionCandidates(completionReq(lines, row, col))

		if gotFrom != wantFrom || !slices.Equal(labels(gotItems), labels(wantItems)) {
			t.Errorf("%s:\n cached: from=%d %v\nuncached: from=%d %v",
				e.name, gotFrom, labels(gotItems), wantFrom, labels(wantItems))
		}
	}
}

// A fragment matches anywhere in a name, prefix matches listed first and the
// rest marked Partial — the table list and the column list alike.
func TestSQLCompletionMatchesSubstringPrefixFirst(t *testing.T) {
	qp := newTestQueryPanelWithInventory(t, "Shop", testCustomersOrders())

	lines, row, col := linesAndCursor(t, "SELECT * FROM cust|")
	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if got, want := labels(items), []string{"dbo.Customers", "dbo.vActiveCustomers"}; !slices.Equal(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	if items[0].Partial || !items[1].Partial {
		t.Fatalf("Partial = %v, %v; want false, true", items[0].Partial, items[1].Partial)
	}

	lines, row, col = linesAndCursor(t, "SELECT o.id| FROM Orders o")
	items, _ = qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if got, want := labels(items), []string{"Id", "CustomerId"}; !slices.Equal(got, want) {
		t.Fatalf("column labels = %v, want %v", got, want)
	}
}

// newCompletionInventory builds a fresh entry with the lookup indexes for
// a freshly loaded catalog.
func newCompletionInventory(cat *gosmo.Catalog) *completionInventory {
	inv := &completionInventory{}
	inv.applyCatalog(cat)
	return inv
}
