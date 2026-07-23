package tui

import (
	"context"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/controls"
)

func TestFQN(t *testing.T) {
	tests := []struct {
		schema, name, want string
	}{
		{"dbo", "Orders", "[dbo].[Orders]"},
		{"", "Orders", "[Orders]"},
		{"my]schema", "a]b", "[my]]schema].[a]]b]"},
	}
	for _, tt := range tests {
		if got := fqn(tt.schema, tt.name); got != tt.want {
			t.Errorf("fqn(%q, %q) = %q, want %q", tt.schema, tt.name, got, tt.want)
		}
	}
}

// The registry's static loaders (folders whose children are fixed labels,
// not a database query) must propagate Type/DBName correctly to every
// child — that's the wiring most likely to break silently when a new
// NodeType is added, since there's no compiler check tying childLoaders
// to hasChildren.
func TestStaticLoadersPropagateDBName(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	dbNode := &explorerNode{label: "AdventureWorks", data: nodeData{Type: NodeDatabase, DBName: "AdventureWorks", conn: sc}}
	children, err := childLoaders[NodeDatabase](l, dbNode)
	if err != nil {
		t.Fatalf("loadDatabaseChildren: %v", err)
	}
	wantLabels := []string{"Tables", "Views", "Stored Procedures", "Functions", "Triggers", "Sequences", "Synonyms", "Security"}
	if len(children) != len(wantLabels) {
		t.Fatalf("got %d children, want %d", len(children), len(wantLabels))
	}
	for i, c := range children {
		if c.label != wantLabels[i] {
			t.Errorf("child[%d].label = %q, want %q", i, c.label, wantLabels[i])
		}
		if c.data.DBName != "AdventureWorks" {
			t.Errorf("child[%d].data.DBName = %q, want AdventureWorks", i, c.data.DBName)
		}
		if c.data.conn != sc {
			t.Errorf("child[%d].data.conn not propagated", i)
		}
	}

	secChildren, err := childLoaders[NodeDatabaseSecurity](l, dbNode)
	if err != nil {
		t.Fatalf("loadDatabaseSecurityChildren: %v", err)
	}
	for _, c := range secChildren {
		if c.data.DBName != "AdventureWorks" {
			t.Errorf("security child %q has DBName %q, want AdventureWorks", c.label, c.data.DBName)
		}
	}
}

// A Table's children are also a static loader (see comment above) — its six
// SSMS-style object-family folders, each needing the owning table's
// Schema/Name (not just DBName) propagated so its own loader knows which
// table to query.
func TestTableChildrenAreStaticFolders(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	tableNode := &explorerNode{
		label: "dbo.Orders",
		data:  nodeData{Type: NodeTable, Schema: "dbo", Name: "Orders", DBName: "AdventureWorks", conn: sc},
	}
	children, err := childLoaders[NodeTable](l, tableNode)
	if err != nil {
		t.Fatalf("loadTableChildren: %v", err)
	}
	wantFolders := []struct {
		label string
		typ   NodeType
	}{
		{"Columns", NodeColumns},
		{"Keys", NodeKeys},
		{"Constraints", NodeChecks},
		{"Triggers", NodeTriggers},
		{"Indexes", NodeIndexes},
		{"Statistics", NodeStatistics},
	}
	if len(children) != len(wantFolders) {
		t.Fatalf("got %d children, want %d", len(children), len(wantFolders))
	}
	for i, c := range children {
		if c.label != wantFolders[i].label {
			t.Errorf("child[%d].label = %q, want %q", i, c.label, wantFolders[i].label)
		}
		if c.data.Type != wantFolders[i].typ {
			t.Errorf("child[%d].data.Type = %v, want %v", i, c.data.Type, wantFolders[i].typ)
		}
		if c.data.Schema != "dbo" || c.data.Name != "Orders" || c.data.DBName != "AdventureWorks" {
			t.Errorf("child[%d] = %+v, want Schema=dbo Name=Orders DBName=AdventureWorks", i, c.data)
		}
		if c.data.conn != sc {
			t.Errorf("child[%d].data.conn not propagated", i)
		}
	}
}

// TestSQLServerAgentIsSiblingOfDatabases pins down SQL Server Agent's tree
// position: a direct child of the server root, alongside Databases, not
// nested under the Server Objects (Management) folder — matching SSMS's own
// top-level placement.
func TestSQLServerAgentIsSiblingOfDatabases(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	serverChildren, err := childLoaders[NodeServer](l, &explorerNode{data: nodeData{Type: NodeServer, conn: sc}})
	if err != nil {
		t.Fatalf("loadServerChildren: %v", err)
	}
	var sawAgent bool
	for _, c := range serverChildren {
		if c.label == "SQL Server Agent" {
			if c.data.Type != NodeAgentJobs {
				t.Errorf("SQL Server Agent child has Type %v, want NodeAgentJobs", c.data.Type)
			}
			sawAgent = true
		}
	}
	if !sawAgent {
		t.Fatal(`loadServerChildren didn't include "SQL Server Agent"`)
	}

	mgmtChildren, err := childLoaders[NodeManagement](l, &explorerNode{data: nodeData{Type: NodeManagement, conn: sc}})
	if err != nil {
		t.Fatalf("loadManagementChildren: %v", err)
	}
	for _, c := range mgmtChildren {
		if c.label == "SQL Server Agent" {
			t.Error(`Server Objects (Management) folder still contains "SQL Server Agent" — it should only be under the server root now`)
		}
	}
}

// TestViewsStoredProceduresFunctionsAreLeaves pins down that these node
// types no longer show a tree expand arrow — they have no childLoaders
// entry, so an arrow that expands to nothing was misleading ("cannot be
// opened").
func TestViewsStoredProceduresFunctionsAreLeaves(t *testing.T) {
	for _, nt := range []NodeType{NodeView, NodeStoredProcedure, NodeFunction} {
		if hasChildren(nt) {
			t.Errorf("hasChildren(%v) = true, want false (leaf, no childLoaders entry)", nt)
		}
		if _, ok := childLoaders[nt]; ok {
			t.Errorf("childLoaders has an entry for %v, but hasChildren says it's a leaf — inconsistent", nt)
		}
	}
}

// findMenuItem returns the item with the given label, or nil.
func findMenuItem(items []controls.MenuItem, label string) *controls.MenuItem {
	for i := range items {
		if items[i].Label == label {
			return &items[i]
		}
	}
	return nil
}

// Regression test for a real bug: contextMenuItemsForNode used to build the
// FQN as fmt.Sprintf("[%s].[%s]", node.data.Schema, node.label) — but
// node.label for a table/view/proc is "schema.name" (set by the loader that
// creates it), so the schema ended up duplicated: "[dbo].[dbo.Orders]",
// which SQL Server rejects. Storing the bare name in nodeData.Name and
// building the FQN from Schema+Name fixes it; this pins the fix down.
func TestObjectContextMenuBuildsCorrectFQN(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")

	table := &explorerNode{
		label: "dbo.Orders",
		data:  nodeData{Type: NodeTable, Schema: "dbo", Name: "Orders", DBName: "AdventureWorks", conn: sc},
	}
	items := a.contextMenuItemsForNode(table)

	top1000 := findMenuItem(items, "Select Top 1000 Rows")
	if top1000 == nil {
		t.Fatal(`"Select Top 1000 Rows" not found in table context menu`)
	}
	top1000.Action()
	qp := lastQueryPanel(t, a)
	if want := "SELECT TOP 1000 *\nFROM [dbo].[Orders]"; qp.editor.Text() != want {
		t.Errorf("Select Top 1000 Rows SQL = %q, want %q", qp.editor.Text(), want)
	}
	if qp.database != "AdventureWorks" {
		t.Errorf("panel database = %q, want AdventureWorks", qp.database)
	}

	view := &explorerNode{
		label: "dbo.CustomerOrders",
		data:  nodeData{Type: NodeView, Schema: "dbo", Name: "CustomerOrders", DBName: "AdventureWorks", conn: sc},
	}
	viewItems := a.contextMenuItemsForNode(view)
	findMenuItem(viewItems, "Select Top 1000 Rows").Action()
	qp = lastQueryPanel(t, a)
	if want := "SELECT TOP 1000 *\nFROM [dbo].[CustomerOrders]"; qp.editor.Text() != want {
		t.Errorf("view Select Top 1000 Rows SQL = %q, want %q", qp.editor.Text(), want)
	}

	proc := &explorerNode{
		label: "dbo.usp_GetOrders",
		data:  nodeData{Type: NodeStoredProcedure, Schema: "dbo", Name: "usp_GetOrders", DBName: "AdventureWorks", conn: sc},
	}
	procItems := a.contextMenuItemsForNode(proc)
	findMenuItem(procItems, "Execute Stored Procedure").Action()
	qp = lastQueryPanel(t, a)
	if want := "EXEC [dbo].[usp_GetOrders]"; qp.editor.Text() != want {
		t.Errorf("Execute Stored Procedure SQL = %q, want %q", qp.editor.Text(), want)
	}
}

// lastQueryPanel returns the most recently added panel as a *QueryPanel,
// failing the test if the active panel isn't one.
func lastQueryPanel(t *testing.T, a *App) *QueryPanel {
	t.Helper()
	qp, ok := a.panels.PanelAt(a.panels.Count() - 1).(*QueryPanel)
	if !ok {
		t.Fatalf("last panel is not a *QueryPanel")
	}
	return qp
}
