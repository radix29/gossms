package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
)

// The Indexes folder carries what an index's menu decides on: whether it is
// disabled (its label says so, and only Rebuild is offered), its type (a
// clustered one's Disable takes the data offline) and whether it is
// hypothetical (nothing is offered).
func TestIndexesFolderCarriesMaintenanceState(t *testing.T) {
	disabled := idxListResp("PK_Orders", 1, indexFixture{typeDesc: "CLUSTERED", unique: true, primaryKey: true, compression: "NONE"})
	disabled.rows[0][6] = true // is_disabled
	plain := idxListResp("IX_Orders_CustomerID", 3, plainIndex())
	hypo := plainIndex()
	hypo.hypothetical = true
	hypothetical := idxListResp("_dta_index_Orders_5", 5, hypo)
	list := disabled
	list.rows = append(list.rows, plain.rows[0], hypothetical.rows[0])

	sc, _ := newFakeConn(t, dbByNameResp(idxDatabase, 5), idxTableResp(), list, idxColumnsResp(
		[]driver.Value{int64(1), "OrderID", false, false},
		[]driver.Value{int64(3), "CustomerID", false, false},
		[]driver.Value{int64(5), "OrderDate", false, false},
	))
	l := loaderCtx{ctx: context.Background(), sc: sc}
	children, err := loadIndexesChildren(l, &explorerNode{data: nodeData{
		Type: NodeIndexes, Schema: idxSchema, Name: idxTable, DBName: idxDatabase, conn: sc}})
	if err != nil {
		t.Fatalf("loadIndexesChildren: %v", err)
	}
	byName := map[string]*explorerNode{}
	for _, c := range children {
		byName[c.data.Name] = c
	}
	if n := byName["PK_Orders"]; n == nil || n.data.IsEnabled || !indexClustered(n.data.IndexType) ||
		n.label != "PK_Orders (Clustered, Unique, Disabled)" {
		t.Errorf("disabled clustered index: %+v", n)
	}
	if n := byName["IX_Orders_CustomerID"]; n == nil || !n.data.IsEnabled || indexClustered(n.data.IndexType) || n.data.IsHypothetical {
		t.Errorf("plain index: %+v", n)
	}
	if n := byName["_dta_index_Orders_5"]; n == nil || !n.data.IsHypothetical {
		t.Errorf("hypothetical index: %+v", n)
	}
}

// indexMenuLabels is node's menu, as labels, under a principal granted ALTER
// on Sales.Orders only (the probe fixture withholds ALTER on the Sales schema).
func indexMenuLabels(t *testing.T, data nodeData) map[string]bool {
	t.Helper()
	sc := objectProbedConn(t, "appdb", []string{"Sales.Orders"}, nil)
	data.Type, data.DBName, data.conn = NodeIndex, "appdb", sc
	out := map[string]bool{}
	for _, it := range (&App{}).nodeMenuItems(&explorerNode{data: data}) {
		if it.Label != "" {
			out[it.Label] = it.Enabled == nil || it.Enabled()
		}
	}
	return out
}

// An enabled index offers all three verbs; a disabled one only the Rebuild
// that enables it (REORGANIZE on it fails, Msg 1973); a hypothetical or hash
// index none. Each is gated on the parent table: ALTER INDEX checks ALTER on
// it, so a principal granted ALTER on one table keeps the items there and
// loses them on another's index, even one named after the granted table.
func TestIndexMenuOffersWhatItsStateAllows(t *testing.T) {
	verbs := []string{"Rebuild...", "Reorganize...", "Disable...", "Rebuild (enables it)..."}
	for _, c := range []struct {
		what string
		data nodeData
		want []string
	}{
		{"enabled", nodeData{Schema: "Sales", Name: "IX", TableName: "Orders", IsEnabled: true, IndexType: "NONCLUSTERED"}, verbs[:3]},
		{"disabled", nodeData{Schema: "Sales", Name: "IX", TableName: "Orders", IndexType: "NONCLUSTERED"}, verbs[3:]},
		{"hypothetical", nodeData{Schema: "Sales", Name: "IX", TableName: "Orders", IsEnabled: true, IsHypothetical: true}, nil},
		{"hash", nodeData{Schema: "Sales", Name: "IX", TableName: "Orders", IsEnabled: true, IndexType: "NONCLUSTERED HASH"}, nil},
	} {
		t.Run(c.what, func(t *testing.T) {
			got := indexMenuLabels(t, c.data)
			for _, v := range verbs {
				enabled, present := got[v]
				switch want := slices.Contains(c.want, v); {
				case want && !present:
					t.Errorf("%q missing; menu %v", v, got)
				case want && !enabled:
					t.Errorf("%q withheld on an index of the table the principal may alter", v)
				case !want && present:
					t.Errorf("%q offered; menu %v", v, got)
				}
			}
			if !got["Properties..."] {
				t.Error("Properties... missing")
			}
		})
	}
	other := indexMenuLabels(t, nodeData{Schema: "Sales", Name: "Orders", TableName: "Customers", IsEnabled: true, IndexType: "NONCLUSTERED"})
	for _, v := range verbs[:3] {
		if other[v] {
			t.Errorf("%q offered on another table's index — the gate asked about the index, not its table", v)
		}
	}
}

// Each verb confirms, then sends its ALTER INDEX naming the index and the
// table it is on; a declined confirmation sends nothing.
func TestIndexMaintenanceIssuesItsStatement(t *testing.T) {
	for _, c := range []struct {
		verb       indexVerb
		wantStmt   string
		wantStatus string
	}{
		{indexRebuild, "ALTER INDEX [IX_Orders_CustomerID] ON [sales].[Orders] REBUILD", "Index [IX_Orders_CustomerID] on [sales].[Orders] rebuilt"},
		{indexReorganize, "ALTER INDEX [IX_Orders_CustomerID] ON [sales].[Orders] REORGANIZE", "Index [IX_Orders_CustomerID] on [sales].[Orders] reorganized"},
		{indexDisable, "ALTER INDEX [IX_Orders_CustomerID] ON [sales].[Orders] DISABLE", "Index [IX_Orders_CustomerID] on [sales].[Orders] disabled"},
	} {
		for _, confirm := range []bool{true, false} {
			t.Run(c.wantStatus, func(t *testing.T) {
				a := newTestApp()
				sc, inst := newFakeConn(t,
					dbByNameResp(idxDatabase, 5),
					idxTableResp(),
					idxListResp("IX_Orders_CustomerID", 3, plainIndex()),
					idxColumnsResp([]driver.Value{int64(3), "CustomerID", false, false}),
				)
				node := opTestNode(sc, NodeIndex, idxSchema, "IX_Orders_CustomerID", idxTable)
				node.data.IsEnabled, node.data.IndexType = true, "NONCLUSTERED"

				a.maintainIndex(sc, node, c.verb)
				if !confirm {
					a.confirmDialog.HandleKey(esc())
					if a.confirmDialog.Visible() {
						t.Fatal("Escape left the confirmation open")
					}
					if stmts := inst.StatementsIn(idxDatabase); len(stmts) != 0 {
						t.Errorf("declined, yet sent %q", stmts)
					}
					return
				}
				answerConfirm(t, a, false)
				waitAndDrain(t, a)

				stmts := inst.StatementsIn(idxDatabase)
				if !slices.ContainsFunc(stmts, func(s string) bool { return strings.Contains(s, c.wantStmt) }) {
					t.Errorf("statements = %q, want one containing %q", stmts, c.wantStmt)
				}
				if a.statusText != c.wantStatus {
					t.Errorf("status = %q, want %q", a.statusText, c.wantStatus)
				}
			})
		}
	}
}

// Disabling a clustered index is the one verb that takes data offline, and
// what it takes differs by what the index is on; the confirmation has to say
// which. A rebuild of a clustered index blocks reads too, not only writes.
func TestIndexVerbPromptSaysWhatAClusteredIndexTakes(t *testing.T) {
	table := nodeData{Schema: "sales", Name: "PK", TableName: "Orders", IsEnabled: true, IndexType: "CLUSTERED"}
	view := table
	view.OnView, view.TableName = true, "vOrders"
	nonclustered := table
	nonclustered.IndexType = "NONCLUSTERED"

	for _, c := range []struct {
		what    string
		data    nodeData
		verb    indexVerb
		want    string
		mustNot string
	}{
		{"table clustered disable", table, indexDisable, "takes [sales].[Orders] offline", "materialized"},
		{"view clustered disable", view, indexDisable, "materialized data is deleted", "offline"},
		{"nonclustered disable", nonclustered, indexDisable, "queries stop using it", "offline"},
		{"clustered rebuild", table, indexRebuild, "every read and write of", "That enables it"},
		{"nonclustered rebuild", nonclustered, indexRebuild, "blocking writes to", "every read"},
	} {
		_, prompt := indexVerbPrompt(c.data, c.verb)
		if !strings.Contains(prompt, c.want) || strings.Contains(prompt, c.mustNot) {
			t.Errorf("%s: prompt %q, want %q and not %q", c.what, prompt, c.want, c.mustNot)
		}
	}
	disabled := nonclustered
	disabled.IsEnabled = false
	if _, prompt := indexVerbPrompt(disabled, indexRebuild); !strings.Contains(prompt, "That enables it") {
		t.Errorf("a disabled index's rebuild prompt %q does not say it enables the index", prompt)
	}
}
