package tui

import (
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/db"
)

// The Tables folder's four sub-folders — System Tables, FileTables, External
// Tables and Graph Tables — and the exclusions that keep a table in exactly
// one of them.
//
// These sub-folders change existing, well-covered behaviour rather than
// adding beside it: the same loader that listed every user table now lists
// only the plain ones, and a mistake there shows up as a table listed twice
// or not at all.

// folderName names a folder node type for a test message. nodeTypeName
// answers "Object" for a folder — it is written for leaves — so the four
// under test are spelled out here.
func folderName(t NodeType) string {
	switch t {
	case NodeTables:
		return "Tables"
	case NodeSystemTables:
		return "System Tables"
	case NodeFileTables:
		return "FileTables"
	case NodeExternalTables:
		return "External Tables"
	case NodeGraphTables:
		return "Graph Tables"
	}
	return nodeTypeName(t)
}

// tablesPresenceResp answers Database.TableKindsPresentContext. It is matched
// on the aggregate's own text and must be scripted *before* the listing
// answer: responses match by substring in order, and both queries read
// "FROM   sys.tables t".
func tablesPresenceResp(system, fileTable, external, graph bool) fakeResponse {
	return fakeResponse{
		match: "MAX(CASE WHEN t.is_ms_shipped = 1",
		cols:  4,
		rows:  [][]driver.Value{{system, fileTable, external, graph}},
	}
}

// tablesListResp answers a table listing of any kind with the rows given.
// Each row is a whole sys.tables row in tableSelect's order.
func tablesListResp(rows ...[]driver.Value) fakeResponse {
	return fakeResponse{match: "FROM   sys.tables t", cols: 12, rows: rows}
}

// tableRow builds one sys.tables row. flags are, in order, is_ms_shipped,
// is_filetable, is_external, is_node, is_edge.
func tableRow(id int, schema, name string, flags ...bool) []driver.Value {
	f := make([]driver.Value, 5)
	for i := range f {
		f[i] = i < len(flags) && flags[i]
	}
	created := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	return append([]driver.Value{
		int64(id), schema, name, created, created, false, false,
	}, f...)
}

// newTablesConn is newFamilyConn with a presence answer ahead of the listing.
func newTablesConn(t *testing.T, presence fakeResponse, responses ...fakeResponse) *db.ServerConn {
	t.Helper()
	return newFamilyConn(t, append([]fakeResponse{presence}, responses...)...)
}

// The sub-folders come before the user tables, the way System Databases
// precedes the user databases.
func TestTablesFolderListsItsSubFoldersFirst(t *testing.T) {
	sc := newTablesConn(t, tablesPresenceResp(true, true, true, true),
		tablesListResp(tableRow(1, "dbo", "Orders"), tableRow(2, "sales", "Invoice")))

	children := loadFamily(t, sc, NodeTables)
	got := labelsOfNodes(children)
	want := []string{"System Tables", "FileTables", "External Tables", "Graph Tables",
		"dbo.Orders", "sales.Invoice"}
	if !slices.Equal(got, want) {
		t.Fatalf("Tables = %v, want %v", got, want)
	}
	for i, typ := range []NodeType{NodeSystemTables, NodeFileTables, NodeExternalTables, NodeGraphTables} {
		if children[i].data.Type != typ {
			t.Errorf("%q is typed %v, want %v", children[i].label, children[i].data.Type, typ)
		}
		if children[i].data.DBName != "appdb" {
			t.Errorf("%q lost the database name", children[i].label)
		}
	}
	assertCarriesDatabase(t, children[4:], NodeTable)
}

// External Tables is PolyBase-specific and empty on every instance without
// it. SSMS lists it only where the database has one, and that is what the
// presence read is for — the folder must follow the answer, both ways.
func TestExternalTablesFolderFollowsThePresenceRead(t *testing.T) {
	for _, present := range []bool{true, false} {
		sc := newTablesConn(t, tablesPresenceResp(false, false, present, false), tablesListResp())
		labels := labelsOfNodes(loadFamily(t, sc, NodeTables))
		if got := slices.Contains(labels, "External Tables"); got != present {
			t.Errorf("with external tables present = %v, Tables = %v", present, labels)
		}
		// System Tables and FileTables are listed whatever the answer —
		// SSMS's own call, and both are families any database can grow.
		for _, always := range []string{"System Tables", "FileTables"} {
			if !slices.Contains(labels, always) {
				t.Errorf("Tables = %v, with no %s", labels, always)
			}
		}
	}
}

// sys.tables has no is_node/is_edge before 2017, so gosmo refuses the graph
// listing there. A folder whose only possible answer is that refusal is worse
// than no folder — the same call External Libraries gets.
func TestGraphTablesFolderIsAbsentBefore2017(t *testing.T) {
	load := func(version string) []string {
		sc, _ := newFakeConnAtVersion(t, version, treeFamilyDatabaseRow(),
			tablesPresenceResp(false, false, false, false), tablesListResp())
		return labelsOfNodes(loadFamily(t, sc, NodeTables))
	}
	if labels := load("13.0.6300.2"); slices.Contains(labels, "Graph Tables") {
		t.Errorf("Tables on major 13 = %v — the folder can only error", labels)
	}
	if labels := load("14.0.3465.1"); !slices.Contains(labels, "Graph Tables") {
		t.Errorf("Tables on major 14 = %v, with no Graph Tables", labels)
	}
}

// The whole point of the sub-folders: a table listed under one of them must
// not also be listed under Tables itself. The exclusion is gosmo's clause, so
// what is asserted here is that the loader asked for TableKindUser at all.
func TestTablesFolderAsksForPlainUserTablesOnly(t *testing.T) {
	sc, inst := newFakeConn(t, treeFamilyDatabaseRow(),
		tablesPresenceResp(false, false, false, false), tablesListResp())
	loadFamily(t, sc, NodeTables)

	listings := inst.Reads("ORDER  BY SCHEMA_NAME(t.schema_id)")
	if len(listings) != 1 {
		t.Fatalf("the Tables folder ran %d listings, want 1", len(listings))
	}
	for _, want := range []string{
		"t.is_ms_shipped = 0", "t.is_filetable = 0", "t.is_external = 0", "NOT (",
	} {
		if !strings.Contains(listings[0], want) {
			t.Errorf("the Tables listing does not exclude %s:\n%s", want, listings[0])
		}
	}
}

// Each sub-folder lists its own family, and a System Tables leaf is marked
// IsSystem — which is what keeps Delete and Rename off its menu (see
// objectOpsMenuItems). Every leaf is a NodeTable whatever folder it came
// from: the folder is where the families differ, not the object.
func TestTableSubFolderLoaders(t *testing.T) {
	for _, c := range []struct {
		folder   NodeType
		clause   string
		row      []driver.Value
		wantName string
		system   bool
	}{
		{NodeSystemTables, "t.is_ms_shipped = 1", tableRow(1, "dbo", "sysdiagrams", true), "dbo.sysdiagrams", true},
		{NodeFileTables, "t.is_filetable = 1", tableRow(2, "dbo", "Docs", false, true), "dbo.Docs", false},
		{NodeExternalTables, "t.is_external = 1", tableRow(3, "ext", "Remote", false, false, true), "ext.Remote", false},
		{NodeGraphTables, "t.is_node = 1", tableRow(4, "dbo", "Person", false, false, false, true), "dbo.Person (node)", false},
	} {
		t.Run(folderName(c.folder), func(t *testing.T) {
			sc, inst := newFakeConn(t, treeFamilyDatabaseRow(), tablesListResp(c.row))
			children := loadFamily(t, sc, c.folder)
			if got := labelsOfNodes(children); !slices.Equal(got, []string{c.wantName}) {
				t.Fatalf("children = %v, want %v", got, []string{c.wantName})
			}
			assertCarriesDatabase(t, children, NodeTable)
			if children[0].data.IsSystem != c.system {
				t.Errorf("IsSystem = %v, want %v", children[0].data.IsSystem, c.system)
			}
			listings := inst.Reads("ORDER  BY SCHEMA_NAME(t.schema_id)")
			if len(listings) != 1 || !strings.Contains(listings[0], c.clause) {
				t.Errorf("listing does not select %s:\n%v", c.clause, listings)
			}
		})
	}
}

// A graph table's label says which half of the graph it is: node and edge
// tables share one folder, and nothing else in the row distinguishes them.
func TestGraphTableLabelsSayNodeOrEdge(t *testing.T) {
	sc, _ := newFakeConn(t, treeFamilyDatabaseRow(), tablesListResp(
		tableRow(1, "dbo", "Person", false, false, false, true),
		tableRow(2, "dbo", "Knows", false, false, false, false, true),
	))
	got := labelsOfNodes(loadFamily(t, sc, NodeGraphTables))
	want := []string{"dbo.Person (node)", "dbo.Knows (edge)"}
	if !slices.Equal(got, want) {
		t.Errorf("Graph Tables = %v, want %v", got, want)
	}
}

// A folder with no filterProps entry offers a Filter menu that does nothing.
// The sub-folders are listings of sys.tables like the parent, so they offer
// what the parent offers.
func TestTableSubFoldersOfferTheSameFilterAsTables(t *testing.T) {
	want := filterProps(NodeTables)
	if len(want) == 0 {
		t.Fatal("the Tables folder itself offers no filter properties")
	}
	for _, folder := range []NodeType{NodeSystemTables, NodeFileTables, NodeExternalTables, NodeGraphTables} {
		got := filterProps(folder)
		if len(got) != len(want) {
			t.Errorf("%s offers %d filter properties, want %d", folderName(folder), len(got), len(want))
			continue
		}
		for i := range got {
			if got[i].id != want[i].id {
				t.Errorf("%s filter property %d = %v, want %v", folderName(folder), i, got[i].name, want[i].name)
			}
		}
	}
}

// The Detail Browser has to make the same split as the tree, or the two panes
// disagree about which folder a table belongs to.
func TestDetailBrowserAsksForTheFolderSOwnKind(t *testing.T) {
	for folder, want := range map[NodeType]string{
		NodeTables:         "user",
		NodeSystemTables:   "system",
		NodeFileTables:     "filetable",
		NodeExternalTables: "external",
		NodeGraphTables:    "graph",
	} {
		if got := tableKindForNode(folder).String(); got != want {
			t.Errorf("%s shows the %s tables, want %s", folderName(folder), got, want)
		}
	}
}
