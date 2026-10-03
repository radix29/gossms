package tui

import (
	"slices"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// newLinkedPanel is newCrossDBPanel's panel whose server also has the linked
// servers Remote (databases Sales, loaded, and Later, loading), Down (its
// database list failed), Slow (still listing its databases) and Billing — a
// local database's name too, whose own reading must win.
func newLinkedPanel(t *testing.T) *QueryPanel {
	t.Helper()
	qp := newCrossDBPanel(t)
	sales := newCompletionInventory(&gosmo.Catalog{
		Schemas: []string{"audit", "dbo"},
		Objects: []gosmo.CatalogObject{
			{ObjectID: 1, Schema: "dbo", Name: "Orders", Type: gosmo.CatalogTable, Columns: []gosmo.CatalogColumn{
				{Name: "OrderNo", DataType: "int"}, {Name: "Placed", DataType: "datetime2", Scale: 3, IsNullable: true},
			}},
			{ObjectID: 2, Schema: "audit", Name: "Log", Type: gosmo.CatalogView, Columns: []gosmo.CatalogColumn{
				{Name: "At", DataType: "datetime"},
			}},
		},
	})
	d := qp.app.completion.linked[sysCompletionInventoryKey(qp.conn.Opts)]
	d.byName = map[string]*linkedServer{
		"remote": {
			name:      "Remote",
			databases: map[string]string{"sales": "Sales", "later": "Later"},
			inventories: map[string]*completionInventory{
				"sales": sales,
				"later": {loading: true},
			},
		},
		"down":    {name: "Down", err: errNoDatabaseAccess},
		"slow":    {name: "Slow", loading: true},
		"billing": {name: "Billing", databases: map[string]string{"dbo": "dbo"}},
	}
	return qp
}

func TestSQLCompletionLinkedServer(t *testing.T) {
	cases := []struct {
		name, sql string
		want      []string
	}{
		{"server dot", "SELECT * FROM Remote.|", []string{"Later", "Sales"}},
		{"server dot prefix", "SELECT * FROM remote.Sa|", []string{"Sales"}},
		{"database dot", "SELECT * FROM Remote.Sales.|", []string{"audit", "dbo"}},
		{"schema dot", "SELECT * FROM Remote.Sales.dbo.|", []string{"dbo.Orders"}},
		{"bracketed schema dot", "SELECT * FROM [Remote].[Sales].[audit].|", []string{"audit.Log"}},
		{"object dot", "SELECT Remote.Sales.dbo.Orders.| FROM Remote.Sales.dbo.Orders", []string{"OrderNo", "Placed"}},
		{"four-part alias", "SELECT o.| FROM Remote.Sales.dbo.Orders o", []string{"OrderNo", "Placed"}},
		{"four-part bare name", "SELECT Orders.| FROM REMOTE.sales.DBO.orders", []string{"OrderNo", "Placed"}},
		{"unqualified columns", "SELECT Pla| FROM Remote.Sales.dbo.Orders o", []string{"Placed"}},
		{"joined with own table", "SELECT * FROM dbo.Orders x JOIN Remote.Sales.audit.Log l ON l.|", []string{"At"}},
		{"in the object list", "SELECT * FROM Remo|", []string{"Remote"}},

		{"local database wins", "SELECT * FROM Billing.dbo.|", []string{"dbo.Invoices"}},
		{"unknown server", "SELECT * FROM Nope.Sales.dbo.|", nil},
		{"unknown remote database", "SELECT * FROM Remote.Nope.|", nil},
		{"unknown remote schema", "SELECT * FROM Remote.Sales.nope.|", nil},
		{"unknown remote object", "SELECT o.| FROM Remote.Sales.dbo.Nope o", nil},
		{"default database", "SELECT * FROM Remote..|", nil},
		{"default schema", "SELECT * FROM Remote.Sales..|", nil},
		{"default schema ref", "SELECT o.| FROM Remote.Sales..Orders o", nil},
		{"failed server", "SELECT * FROM Down.|", nil},
		{"failed server ref", "SELECT o.| FROM Down.Sales.dbo.Orders o", nil},

		{"listing databases", "SELECT * FROM Slow.|", []string{"<loading>"}},
		{"listing databases ref", "SELECT o.| FROM Slow.Sales.dbo.Orders o", []string{"<loading>"}},
		{"loading catalog", "SELECT * FROM Remote.Later.|", []string{"<loading>"}},
		{"loading catalog ref", "SELECT | FROM Remote.Later.dbo.T", []string{"<loading>"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			qp := newLinkedPanel(t)
			got := crossDBLabels(t, qp, c.sql)
			if c.name == "in the object list" {
				if !slices.Contains(got, "Remote") {
					t.Errorf("labels = %q, want Remote among them", got)
				}
				return
			}
			if !slices.Equal(got, c.want) {
				t.Errorf("labels = %q, want %q", got, c.want)
			}
		})
	}
}

// TestSQLCompletionLinkedServerListLoading: while the linked-server list is
// still loading, a name nothing local resolves waits on it rather than
// answering nothing.
func TestSQLCompletionLinkedServerListLoading(t *testing.T) {
	qp := newCrossDBPanel(t)
	qp.app.completion.linked[sysCompletionInventoryKey(qp.conn.Opts)] = &linkedDirectory{loading: true}
	for _, sql := range []string{"SELECT * FROM Remote.|", "SELECT * FROM Remote.Sales.dbo.|", "SELECT o.| FROM Remote.Sales.dbo.Orders o"} {
		if got := crossDBLabels(t, qp, sql); !slices.Equal(got, []string{"<loading>"}) {
			t.Errorf("%s: labels = %q, want the loading row", sql, got)
		}
	}
	// A local reading never waits on it.
	if got := crossDBLabels(t, qp, "SELECT * FROM Billing.dbo.|"); !slices.Equal(got, []string{"dbo.Invoices"}) {
		t.Errorf("local chain: labels = %q", got)
	}
}

// TestLinkedUnknownDatabaseReadsNothing: a database the linked server does
// not list never gets an inventory — no remote catalog read for a typo.
func TestLinkedUnknownDatabaseReadsNothing(t *testing.T) {
	qp := newLinkedPanel(t)
	crossDBLabels(t, qp, "SELECT * FROM Remote.Nope.dbo.|")
	ls := qp.app.completion.linked[sysCompletionInventoryKey(qp.conn.Opts)].byName["remote"]
	if _, ok := ls.inventories["nope"]; ok {
		t.Error("an inventory was created for a database the linked server does not list")
	}
}

// TestPurgeLinkedCompletionAbandonsEveryLoad: Ctrl+R and disconnect drop the
// whole linked cache, and a result still on its way from any of its three
// loads must fail its Done guard.
func TestPurgeLinkedCompletionAbandonsEveryLoad(t *testing.T) {
	qp := newLinkedPanel(t)
	a, key := qp.app, sysCompletionInventoryKey(qp.conn.Opts)
	d := a.completion.linked[key]
	ls := d.byName["remote"]
	inv := ls.inventories["later"]
	_, dirSeq := d.load.Begin(t.Context())
	_, lsSeq := ls.load.Begin(t.Context())
	_, invSeq := inv.load.Begin(t.Context())

	a.purgeLinkedCompletion(key)

	if _, ok := a.completion.linked[key]; ok {
		t.Error("the linked directory is still cached")
	}
	if d.load.Done(dirSeq) || ls.load.Done(lsSeq) || inv.load.Done(invSeq) {
		t.Error("a load begun before the purge still passes its Done guard")
	}
}
