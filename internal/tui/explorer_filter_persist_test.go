package tui

import (
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

func nameFilter(value string) *nodeFilter {
	return &nodeFilter{criteria: []filterCriterion{{prop: nameProp(), op: opContains, value: value}}}
}

// filterObjects is the Detail Browser's half of the filter: its loaders hold
// gosmo objects rather than *explorerNode, so filterChildren can't be reused.
// The pane used to list the whole folder while the tree beside it showed the
// filtered one.
func TestFilterObjectsDropsRejectedObjects(t *testing.T) {
	node := &explorerNode{data: nodeData{Type: NodeTables, Filter: nameFilter("cust")}}
	tables := []*gosmo.Table{
		{Schema: "Sales", Name: "Customer"},
		{Schema: "dbo", Name: "Orders"},
		{Schema: "dbo", Name: "CustomerArchive"},
	}
	key := func(tb *gosmo.Table) nodeData {
		return nodeData{Name: tb.Name, Schema: tb.Schema, CreateDate: tb.CreateDate, IsMemoryOptimized: tb.IsMemoryOptimized}
	}

	got := filterObjects(node.data.Filter, tables, key)
	want := []string{"Customer", "CustomerArchive"}
	if len(got) != len(want) {
		t.Fatalf("kept %d tables, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Name != w {
			t.Errorf("kept[%d] = %q, want %q", i, got[i].Name, w)
		}
	}

	node.data.Filter = nil
	if len(filterObjects(node.data.Filter, tables, key)) != len(tables) {
		t.Error("an unfiltered folder must keep every object")
	}
}

// Every criterion a folder offers has to reach filterObjects, not just Name:
// the Tables folder's Creation Date and Is Memory Optimized come off the
// gosmo object, and a key function that dropped either would filter the tree
// and the detail pane differently.
func TestFilterObjectsReadsEveryTableProperty(t *testing.T) {
	made := time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)
	tables := []*gosmo.Table{
		{Schema: "dbo", Name: "Hot", CreateDate: made, IsMemoryOptimized: true},
		{Schema: "dbo", Name: "Cold", CreateDate: made.AddDate(0, 0, 1)},
	}
	key := func(tb *gosmo.Table) nodeData {
		return nodeData{Name: tb.Name, Schema: tb.Schema, CreateDate: tb.CreateDate, IsMemoryOptimized: tb.IsMemoryOptimized}
	}
	for _, c := range []filterCriterion{
		{prop: dateProp(), op: opOn, value: "2026-03-04"},
		{prop: memProp(), op: opEquals, value: "True"},
		{prop: schemaProp(), op: opEquals, value: "dbo"},
	} {
		node := &explorerNode{data: nodeData{Type: NodeTables, Filter: &nodeFilter{criteria: []filterCriterion{c}}}}
		got := filterObjects(node.data.Filter, tables, key)
		if c.prop.id == fpSchema {
			if len(got) != 2 {
				t.Errorf("Schema equals dbo kept %d tables, want 2", len(got))
			}
			continue
		}
		if len(got) != 1 || got[0].Name != "Hot" {
			t.Errorf("%s %v %q kept %v, want just Hot", c.prop.name, c.op, c.value, got)
		}
	}
}

func filteredTablesNode(sc *db.ServerConn, dbName string) *explorerNode {
	return &explorerNode{
		label: "Tables",
		data:  nodeData{Type: NodeTables, DBName: dbName, conn: sc},
	}
}

// A filter must outlive the node holding it: disconnecting drops the whole
// subtree, and reconnecting builds fresh nodes that would otherwise come back
// unfiltered.
func TestSavedFilterSurvivesAReconnect(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	node := filteredTablesNode(sc, "HealthClinic")
	a.applyNodeFilter(node, nameFilter("cust"))

	// A fresh connection to the same server, and the fresh nodes its expand
	// would build — one Tables folder in the same database, one in another.
	sc2 := &db.ServerConn{Opts: config.Connection{Server: "server-one"}}
	same := filteredTablesNode(sc2, "HealthClinic")
	other := filteredTablesNode(sc2, "Northwind")
	a.restoreFilters(sc2, []*explorerNode{same, other})

	if !same.data.Filter.active() {
		t.Error("the same folder on a reconnect came back unfiltered")
	}
	if other.data.Filter.active() {
		t.Errorf("a different database's Tables folder picked up the filter: %s", other.data.Filter.summary())
	}

	// A different server must not inherit it either.
	elsewhere := filteredTablesNode(&db.ServerConn{Opts: config.Connection{Server: "server-two"}}, "HealthClinic")
	a.restoreFilters(elsewhere.data.conn, []*explorerNode{elsewhere})
	if elsewhere.data.Filter.active() {
		t.Error("another server's Tables folder picked up the filter")
	}
}

// Remove Filter has to forget the saved copy too, or the filter comes back
// on the next reconnect after the user cleared it.
func TestRemovingAFilterForgetsIt(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	node := filteredTablesNode(sc, "HealthClinic")

	a.applyNodeFilter(node, nameFilter("cust"))
	a.applyNodeFilter(node, nil)

	fresh := filteredTablesNode(sc, "HealthClinic")
	a.restoreFilters(sc, []*explorerNode{fresh})
	if fresh.data.Filter.active() {
		t.Errorf("a removed filter came back on reload: %s", fresh.data.Filter.summary())
	}
	if len(a.savedFilters) != 0 {
		t.Errorf("savedFilters holds %d entries after Remove Filter, want 0", len(a.savedFilters))
	}
}

// A table's own Triggers folder and its database's are the same NodeType in
// the same database — only Schema and TableName tell them apart, so both must
// be in the key.
func TestSavedFilterKeyDistinguishesTableScopedFolders(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	onTable := &explorerNode{
		data: nodeData{Type: NodeTriggers, DBName: "HealthClinic", Schema: "Sales", TableName: "Customer", conn: sc},
	}
	a.applyNodeFilter(onTable, nameFilter("audit"))

	onDatabase := &explorerNode{data: nodeData{Type: NodeTriggers, DBName: "HealthClinic", conn: sc}}
	otherTable := &explorerNode{
		data: nodeData{Type: NodeTriggers, DBName: "HealthClinic", Schema: "Sales", TableName: "Orders", conn: sc},
	}
	a.restoreFilters(sc, []*explorerNode{onDatabase, otherTable})

	if onDatabase.data.Filter.active() {
		t.Error("the database-scoped Triggers folder inherited a table's filter")
	}
	if otherTable.data.Filter.active() {
		t.Error("another table's Triggers folder inherited the filter")
	}
}
