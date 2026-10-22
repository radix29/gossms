package tui

import (
	"context"
	"slices"
	"testing"

	"github.com/radix29/gossms/internal/db"
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
	wantLabels := []string{"Tables", "Views", "External Resources", "Programmability",
		"Query Store", "Service Broker", "Security", "Storage"}
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
// nested under the Server Objects folder — matching SSMS's own
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

	mgmtChildren, err := childLoaders[NodeServerObjects](l, &explorerNode{data: nodeData{Type: NodeServerObjects, conn: sc}})
	if err != nil {
		t.Fatalf("loadServerObjectsChildren: %v", err)
	}
	for _, c := range mgmtChildren {
		if c.label == "SQL Server Agent" {
			t.Error(`Server Objects folder still contains "SQL Server Agent" — it should only be under the server root now`)
		}
	}
}

func TestFabricDWServerFoldersAreConnectionScoped(t *testing.T) {
	fabric := &db.ServerConn{IsFabricDW: true}
	sql := &db.ServerConn{}
	for _, sc := range []*db.ServerConn{fabric, sql, fabric, sql} {
		children, err := loadServerChildren(loaderCtx{ctx: context.Background(), sc: sc},
			&explorerNode{data: nodeData{Type: NodeServer, conn: sc}})
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"Databases", "Security"}
		if !sc.IsFabricDW {
			want = append(want, "Server Objects", "Replication", "Management", alwaysOnRootLabel, agentRootLabel)
		}
		if got := labelsOfNodes(children); !slices.Equal(got, want) {
			t.Errorf("IsFabricDW = %v: root folders = %v, want %v", sc.IsFabricDW, got, want)
		}
		for _, child := range children {
			if child.data.conn != sc {
				t.Errorf("%q lost its own connection", child.label)
			}
		}
	}
}

func TestFabricDWObjectFoldersAreConnectionScoped(t *testing.T) {
	for _, tc := range []struct {
		name     string
		typ      NodeType
		dbName   string
		fabric   []string
		standard []string
	}{
		{
			name: "server security", typ: NodeSecurity,
			fabric:   []string{"Logins", "Server Roles"},
			standard: []string{"Logins", "Server Roles", "Credentials", "Cryptographic Providers", "Audits", "Server Audit Specifications"},
		},
		{
			name: "database", typ: NodeDatabase, dbName: "appdb",
			fabric:   []string{"Tables", "Views", "External Data Sources", "Programmability", "Security"},
			standard: []string{"Tables", "Views", "External Resources", "Programmability", "Query Store", "Service Broker", "Security", "Storage"},
		},
		{
			name: "table", typ: NodeTable, dbName: "appdb",
			fabric:   []string{"Columns", "Keys", "Constraints", "Statistics"},
			standard: []string{"Columns", "Keys", "Constraints", "Triggers", "Indexes", "Statistics"},
		},
		{
			name: "programmability", typ: NodeProgrammability, dbName: "appdb",
			fabric:   []string{"Stored Procedures", "Functions", "External Functions"},
			standard: []string{"Stored Procedures", "Functions", "Database Triggers", "Assemblies", "Types", "Rules", "Defaults", "Plan Guides", "Sequences", "Synonyms"},
		},
		{
			name: "database security", typ: NodeDatabaseSecurity, dbName: "appdb",
			fabric:   []string{"Users", "Roles", "Schemas"},
			standard: []string{"Users", "Roles", "Schemas", "Asymmetric Keys", "Certificates", "Symmetric Keys", "Database Audit Specifications", "Database Scoped Credentials", "Security Policies", "Always Encrypted Keys"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fabric, _ := newFakeConn(t, capabilityResponses(true, nil, nil, nil, nil)...)
			fabric.IsFabricDW = true
			standard, _ := newFakeConn(t, capabilityResponses(true, nil, nil, nil, nil)...)
			for _, sc := range []*db.ServerConn{fabric, standard, fabric, standard} {
				node := &explorerNode{data: nodeData{Type: tc.typ, DBName: tc.dbName, conn: sc}}
				if tc.typ == NodeTable {
					node.data.Schema, node.data.Name = "sales", "Orders"
				}
				children, err := childLoaders[tc.typ](loaderCtx{ctx: context.Background(), sc: sc}, node)
				if err != nil {
					t.Fatal(err)
				}
				want := tc.standard
				if sc.IsFabricDW {
					want = tc.fabric
				}
				if got := labelsOfNodes(children); !slices.Equal(got, want) {
					t.Errorf("IsFabricDW = %v: folders = %v, want %v", sc.IsFabricDW, got, want)
				}
				for _, child := range children {
					if child.data.conn != sc || child.data.DBName != tc.dbName ||
						child.data.Schema != node.data.Schema || child.data.Name != node.data.Name {
						t.Errorf("IsFabricDW = %v: %q lost its owning connection/database/object: %+v",
							sc.IsFabricDW, child.label, child.data)
					}
				}
				if tc.typ == NodeDatabase {
					continue // A database has a property detail view, not a folder list.
				}
				var objs []nodeData
				_, rows, err := fetchNodeDetails(context.Background(), sc, node, &objs)
				if err != nil {
					t.Fatal(err)
				}
				var labels []string
				for _, row := range rows {
					labels = append(labels, row[0])
				}
				if !slices.Equal(labels, want) || len(objs) != len(children) {
					t.Fatalf("IsFabricDW = %v: detail folders = %v (%d objects), want %v",
						sc.IsFabricDW, labels, len(objs), want)
				}
				for i, obj := range objs {
					if obj != children[i].data {
						t.Errorf("detail folder %q lost its tree identity", labels[i])
					}
				}
			}
		})
	}
}

// TestStoredProceduresAndFunctionsAreLeaves pins down that these node types
// show no tree expand arrow: they have no childLoaders entry, so an arrow
// would expand to nothing.
//
// NodeView used to be here and is deliberately not any more: a view carries
// INSTEAD OF triggers, and since the database-wide Triggers roll-up was
// removed, its own Triggers folder is the only place they are listed.
func TestStoredProceduresAndFunctionsAreLeaves(t *testing.T) {
	for _, nt := range []NodeType{NodeStoredProcedure, NodeFunction} {
		if hasChildren(nt) {
			t.Errorf("hasChildren(%v) = true, want false (leaf, no childLoaders entry)", nt)
		}
		if _, ok := childLoaders[nt]; ok {
			t.Errorf("childLoaders has an entry for %v, but hasChildren says it's a leaf — inconsistent", nt)
		}
	}
}

// A view expands to SSMS's four folders (issue #2: its Details listed only
// Triggers, and its columns were nowhere), and each must carry the view's own
// schema and name, or its loader reads the wrong object.
func TestViewExpandsToItsFolders(t *testing.T) {
	if !hasChildren(NodeView) {
		t.Fatal("hasChildren(NodeView) = false — the view's folders would have no expand arrow")
	}
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	viewNode := &explorerNode{label: "dbo.vOrders",
		data: nodeData{Type: NodeView, Schema: "dbo", Name: "vOrders", DBName: "AdventureWorks", conn: sc}}
	children, err := childLoaders[NodeView](l, viewNode)
	if err != nil {
		t.Fatalf("loadViewChildren: %v", err)
	}
	want := []struct {
		label string
		typ   NodeType
	}{
		{"Columns", NodeViewColumns},
		{"Triggers", NodeTriggers},
		{"Indexes", NodeIndexes},
		{"Statistics", NodeStatistics},
	}
	if len(children) != len(want) {
		t.Fatalf("got %d view children, want %d", len(children), len(want))
	}
	for i, c := range children {
		got := c.data
		if c.label != want[i].label || got.Type != want[i].typ {
			t.Errorf("child[%d] = %q (%v), want %q (%v)", i, c.label, got.Type, want[i].label, want[i].typ)
		}
		if got.Schema != "dbo" || got.Name != "vOrders" || got.DBName != "AdventureWorks" {
			t.Errorf("%s folder data = %+v, want the view's own schema/name/database", c.label, got)
		}
		// Indexes and Statistics are the table's folder types, told apart by
		// OnView: it decides the New Index kinds and the extended-property level.
		if wantOnView := got.Type == NodeIndexes || got.Type == NodeStatistics; got.OnView != wantOnView {
			t.Errorf("%s folder OnView = %v, want %v", c.label, got.OnView, wantOnView)
		}
	}
}

// The Programmability folder is where the DDL trigger family lives, and it
// holds the module families the database node used to list flat.
func TestProgrammabilityFolderChildren(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	node := &explorerNode{label: "Programmability",
		data: nodeData{Type: NodeProgrammability, DBName: "AdventureWorks", conn: sc}}
	children, err := childLoaders[NodeProgrammability](l, node)
	if err != nil {
		t.Fatalf("loadProgrammabilityChildren: %v", err)
	}
	want := []string{"Stored Procedures", "Functions", "Database Triggers",
		"Assemblies", "Types", "Rules", "Defaults", "Plan Guides",
		"Sequences", "Synonyms"}
	if len(children) != len(want) {
		t.Fatalf("got %d children, want %d: %v", len(children), len(want), children)
	}
	for i, c := range children {
		if c.label != want[i] {
			t.Errorf("child[%d].label = %q, want %q", i, c.label, want[i])
		}
		if c.data.DBName != "AdventureWorks" {
			t.Errorf("child[%d] lost the database name", i)
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

// contextMenuItemsForNode must build the FQN from nodeData's Schema+Name,
// not from node.label: a table/view/proc's label is already "schema.name",
// so using it doubles the schema — "[dbo].[dbo.Orders]", which SQL Server
// rejects.
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
	if qp.connectingDB != "AdventureWorks" {
		t.Errorf("panel connecting to database %q, want AdventureWorks", qp.connectingDB)
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
