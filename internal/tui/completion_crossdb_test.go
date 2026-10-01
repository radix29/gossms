package tui

import (
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// billingObjects is the other database's catalog in these tests: a table that
// shares nothing with testCustomersOrders, so a column offered from it can
// only have come through the cross-database name.
func billingObjects() []gosmo.CatalogObject {
	return []gosmo.CatalogObject{
		{
			ObjectID: 10, Schema: "dbo", Name: "Invoices", Type: gosmo.CatalogTable,
			Columns: []gosmo.CatalogColumn{
				{Name: "InvoiceNo", DataType: "int"},
				{Name: "Amount", DataType: "money"},
			},
		},
		{
			ObjectID: 11, Schema: "audit", Name: "Trail", Type: gosmo.CatalogTable,
			Columns: []gosmo.CatalogColumn{{Name: "At", DataType: "datetime"}},
		},
	}
}

// newCrossDBPanel is a panel on testdb whose server also lists Billing (loaded),
// Pending (still loading), Locked (its gated load refused) and Offline (not
// ONLINE, so never loaded).
func newCrossDBPanel(t *testing.T) *QueryPanel {
	t.Helper()
	qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
	a, sc := qp.app, qp.conn
	cat := &gosmo.Catalog{Objects: billingObjects(), Schemas: []string{"audit", "dbo"}}
	billing := newCompletionInventory(cat)
	billing.gated = true
	a.completionInventories[completionInventoryKey(sc.Opts, "Billing")] = billing
	a.completionInventories[completionInventoryKey(sc.Opts, "Pending")] = &completionInventory{loading: true, gated: true}
	a.completionInventories[completionInventoryKey(sc.Opts, "Locked")] = &completionInventory{err: errNoDatabaseAccess, gated: true}
	dir := a.completionDirectories[sysCompletionInventoryKey(sc.Opts)]
	for _, e := range []directoryEntry{{"Billing", "ONLINE"}, {"Pending", "ONLINE"}, {"Locked", "ONLINE"}, {"Offline", "OFFLINE"}} {
		dir.byName[strings.ToLower(e.name)] = e
	}
	return qp
}

func crossDBLabels(t *testing.T, qp *QueryPanel, sql string) []string {
	t.Helper()
	lines, row, col := linesAndCursor(t, sql)
	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if len(items) == 1 && items[0].Placeholder {
		return []string{"<loading>"}
	}
	return labels(items)
}

func TestSQLCompletionCrossDatabase(t *testing.T) {
	cases := []struct {
		name, sql string
		want      []string
	}{
		{"three-part alias", "SELECT o.| FROM Billing.dbo.Invoices o", []string{"Amount", "InvoiceNo"}},
		{"three-part bare name", "SELECT Invoices.| FROM Billing.dbo.Invoices", []string{"Amount", "InvoiceNo"}},
		{"default schema", "SELECT o.| FROM Billing..Invoices o", []string{"Amount", "InvoiceNo"}},
		{"non-dbo schema", "SELECT x.| FROM billing.AUDIT.trail x", []string{"At"}},
		{"unqualified columns", "SELECT Amo| FROM Billing.dbo.Invoices o", []string{"Amount"}},
		{"joined with own table", "SELECT * FROM Billing.dbo.Invoices i JOIN dbo.Orders o ON i.|", []string{"Amount", "InvoiceNo"}},
		{"own database by name", "SELECT c.| FROM testdb.dbo.Customers c", []string{"Email", "Id", "Name"}},
		{"database dot", "SELECT * FROM Billing.|", []string{"audit", "dbo"}},
		{"database schema dot", "SELECT * FROM Billing.dbo.|", []string{"dbo.Invoices"}},
		{"database schema prefix", "SELECT * FROM Billing.audit.Tr|", []string{"audit.Trail"}},
		{"database double dot", "SELECT * FROM Billing..|", []string{"dbo.Invoices"}},
		{"four-part column", "SELECT Billing.dbo.Invoices.| FROM Billing.dbo.Invoices", []string{"Amount", "InvoiceNo"}},
		{"schema object dot stays local", "SELECT dbo.Orders.| FROM dbo.Orders", []string{"CustomerId", "Id", "Total"}},
		{"local schema wins over database", "SELECT * FROM sales.|", []string{"sales.Region"}},
		{"bracketed database", "SELECT * FROM [Billing].[dbo].|", []string{"dbo.Invoices"}},

		{"unknown database", "SELECT o.| FROM Nope.dbo.Invoices o", nil},
		{"unknown database dot", "SELECT * FROM Nope.dbo.|", nil},
		{"offline database", "SELECT * FROM Offline.dbo.|", nil},
		{"inaccessible database", "SELECT o.| FROM Locked.dbo.T o", nil},
		{"inaccessible database dot", "SELECT * FROM Locked.|", nil},
		{"wrong schema", "SELECT o.| FROM Billing.nope.Invoices o", nil},
		{"own table name in other database", "SELECT o.| FROM Billing.dbo.Orders o", nil},
		{"linked server", "SELECT o.| FROM srv.Billing.dbo.Invoices o", nil},
		{"linked server dot", "SELECT * FROM srv.Billing.dbo.|", nil},

		{"loading ref", "SELECT o.| FROM Pending.dbo.T o", []string{"<loading>"}},
		{"loading ref unqualified", "SELECT | FROM Pending.dbo.T o", []string{"<loading>"}},
		{"loading database dot", "SELECT * FROM Pending.|", []string{"<loading>"}},
		{"loading chain", "SELECT * FROM Pending.dbo.|", []string{"<loading>"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			qp := newCrossDBPanel(t)
			got := crossDBLabels(t, qp, c.sql)
			if !slices.Equal(got, c.want) {
				t.Errorf("labels = %q, want %q", got, c.want)
			}
		})
	}
}

// TestSQLCompletionCrossDatabaseNamesLoadNothingUnlisted pins the directory as
// the gate before any load: a name the server doesn't list as an ONLINE
// database — a typo, an alias that resolved to nothing — starts no inventory
// load, which against a real server would be a probe and a catalog read per
// mistyped qualifier.
func TestSQLCompletionCrossDatabaseNamesLoadNothingUnlisted(t *testing.T) {
	qp := newCrossDBPanel(t)
	before := len(qp.app.completionInventories)
	for _, sql := range []string{
		"SELECT * FROM Nope.|",
		"SELECT * FROM Nope.dbo.|",
		"SELECT x.| FROM Nope..T x",
		"SELECT * FROM Offline.dbo.|",
		"SELECT x.| FROM t",
	} {
		crossDBLabels(t, qp, sql)
	}
	if n := len(qp.app.completionInventories); n != before {
		t.Errorf("inventories = %d after unlisted names, want %d", n, before)
	}
}

// TestSQLCompletionCrossDatabaseWaitsForDirectory: until the database list
// lands, a name that might be a database shows the loading row rather than
// closing the popup on an answer that is about to change.
func TestSQLCompletionCrossDatabaseWaitsForDirectory(t *testing.T) {
	qp := newCrossDBPanel(t)
	qp.app.completionDirectories[sysCompletionInventoryKey(qp.conn.Opts)] = &completionDirectory{loading: true}
	if got := crossDBLabels(t, qp, "SELECT * FROM Billing.dbo.|"); !slices.Equal(got, []string{"<loading>"}) {
		t.Errorf("labels = %q, want the loading row", got)
	}
}

// TestSQLCompletionTempTableIgnoresDatabasePart: the server ignores the
// database part of a temp table's name, so "tempdb..#t" is the batch's #t.
func TestSQLCompletionTempTableIgnoresDatabasePart(t *testing.T) {
	qp := newCrossDBPanel(t)
	got := crossDBLabels(t, qp, "CREATE TABLE #t (a int, b int)\nSELECT x.| FROM tempdb..#t x")
	if !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("labels = %q, want the temp table's columns", got)
	}
}

// TestCrossDatabaseLoadIsGatedOnAccess drives the real loader over the fake
// pool: a database HAS_DBACCESS refuses gets errNoDatabaseAccess and no
// catalog read — its USE would only fail — while an accessible one goes on
// to read its catalog.
func TestCrossDatabaseLoadIsGatedOnAccess(t *testing.T) {
	for _, accessible := range []bool{false, true} {
		sc, inst := newFakeConn(t, capabilityResponses(accessible, nil, nil, nil, nil)...)
		a := newTestApp()
		before := inst.QueryCount()

		inv := a.ensureCrossDatabaseInventory(sc, "Other")
		drainUntil(t, a, func() bool { return !inv.loading }, "the cross-database load")

		probes := len(inst.Reads("HAS_DBACCESS"))
		if probes != 1 {
			t.Errorf("accessible=%v: HAS_DBACCESS asked %d times, want 1", accessible, probes)
		}
		if !accessible {
			if inv.err != errNoDatabaseAccess {
				t.Errorf("inaccessible: err = %v, want errNoDatabaseAccess", inv.err)
			}
			if n := inst.QueryCount() - before; n != 1 {
				t.Errorf("inaccessible: %d queries, want only the access probe", n)
			}
			continue
		}
		if inv.err == errNoDatabaseAccess {
			t.Error("accessible: the load was refused")
		}
		if n := inst.QueryCount() - before; n <= 3 {
			t.Errorf("accessible: %d queries, want the catalog read after the probe", n)
		}
	}
}

// TestSQLCompletionTableListOffersOnlineDatabases: the object list names the
// server's ONLINE databases, so typing one keeps the popup open for the '.'
// that follows — a '.' never opens it from closed.
func TestSQLCompletionTableListOffersOnlineDatabases(t *testing.T) {
	qp := newCrossDBPanel(t)
	lines, row, col := linesAndCursor(t, "SELECT * FROM |")
	items, _ := qp.sqlCompletionCandidates(completionReq(lines, row, col))
	if d := itemDetail(items, "Billing"); d != "database" {
		t.Errorf("Billing detail = %q, want database; labels %q", d, labels(items))
	}
	if containsLabel(items, "Offline") {
		t.Error("an OFFLINE database is offered")
	}
}
