package tui

import (
	"context"
	"slices"
	"testing"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

func TestExplorerRootSupportUsesEditionNotConnectionHostname(t *testing.T) {
	azure, azureFake := newGConnOnAzure(t, int(gosmo.EngineAzureSQLDatabase))
	fabricSQL, _ := newGConnOnAzure(t, int(gosmo.EngineFabricSQLDatabase))
	mi, _ := newGConnOnAzureMI(t)
	sql, _ := newGConn(t)
	fabric := fabricConn(t)
	azure.Opts.Server = "localhost"
	fabricSQL.Opts.Server = "custom-alias"
	sql.Opts.Server = "not-azure.database.windows.net"
	mi.Opts.Server = "managed.database.windows.net"

	full := []string{"Databases", "Security", "Server Objects", "Replication", "Management", alwaysOnRootLabel, agentRootLabel}
	reduced := []string{"Databases", "Security"}
	for _, tc := range []struct {
		name string
		sc   *db.ServerConn
		want []string
	}{
		{"azure-alias", azure, reduced},
		{"sql-azure-hostname", sql, full},
		{"fabric", fabric, reduced},
		{"managed-instance", mi, full},
		{"fabric-sql-database", fabricSQL, []string{"Databases", "Security", "Server Objects", "Management", alwaysOnRootLabel, agentRootLabel}},
		{"unknown", &db.ServerConn{}, full},
		{"nil", nil, full},
		{"azure-again", azure, reduced},
		{"sql-again", sql, full},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &explorerNode{data: nodeData{Type: NodeServer, conn: tc.sc}}
			children, err := loadServerChildren(loaderCtx{ctx: t.Context(), sc: tc.sc}, node)
			if err != nil {
				t.Fatal(err)
			}
			if got := labelsOfNodes(children); !slices.Equal(got, tc.want) {
				t.Errorf("root = %v, want %v", got, tc.want)
			}
			rows := childFolderRows(t.Context(), tc.sc, node)
			var labels []string
			for _, row := range rows {
				labels = append(labels, row[0])
			}
			if !slices.Equal(labels, tc.want) {
				t.Errorf("Details folders = %v, want %v", labels, tc.want)
			}
		})
	}
	before := len(azureFake.Reads(""))
	azure.Server.Info().IsHADREnabled = true
	if got := agLocalDatabaseStates(loaderCtx{ctx: t.Context(), sc: azure}); got != nil {
		t.Error("Azure SQL acquired instance availability-group state")
	}
	if got := len(azureFake.Reads("")); got != before {
		t.Errorf("unsupported availability-group probe issued %d reads", got-before)
	}
}

func TestAzureSQLExplorerFoldersAndDetailsKeepDatabaseFeatures(t *testing.T) {
	sc, inst := newGConnOnAzure(t, int(gosmo.EngineAzureSQLDatabase),
		capabilityResponses(true, nil, nil, nil, nil)...)
	for _, tc := range []struct {
		name string
		typ  NodeType
		want []string
	}{
		{"server objects", NodeServerObjects, nil},
		{"management", NodeManagement, nil},
		{"server security", NodeSecurity, []string{"Logins", "Server Roles"}},
		{"database", NodeDatabase, []string{"Tables", "Views", "External Resources", "Programmability",
			"Query Store", "Security", "Storage", "Extended Events"}},
		{"programmability", NodeProgrammability, []string{"Stored Procedures", "Functions", "Database Triggers",
			"Types", "Rules", "Defaults", "Plan Guides", "Sequences", "Synonyms"}},
		{"types", NodeTypes, []string{"System Data Types", "User-Defined Data Types", "User-Defined Table Types", "XML Schema Collections"}},
		{"external resources", NodeExternalResources, []string{"External Data Sources", "External File Formats"}},
		{"database security", NodeDatabaseSecurity, []string{"Users", "Roles", "Schemas", "Asymmetric Keys",
			"Certificates", "Symmetric Keys", "Database Scoped Credentials", "Security Policies", "Always Encrypted Keys"}},
		{"table", NodeTable, []string{"Columns", "Keys", "Constraints", "Triggers", "Indexes", "Statistics"}},
		{"view", NodeView, []string{"Columns", "Triggers", "Indexes", "Statistics"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &explorerNode{data: nodeData{Type: tc.typ, DBName: "appdb", conn: sc}}
			if tc.typ == NodeTable || tc.typ == NodeView {
				node.data.Schema, node.data.Name = "sales", "Orders"
			}
			before := len(inst.Reads(""))
			children, err := childLoaders[tc.typ](loaderCtx{ctx: t.Context(), sc: sc}, node)
			if err != nil {
				t.Fatal(err)
			}
			if got := labelsOfNodes(children); !slices.Equal(got, tc.want) {
				t.Errorf("folders = %v, want %v", got, tc.want)
			}
			for _, child := range children {
				if child.data.conn != sc {
					t.Errorf("%q lost its connection", child.label)
				}
				if tc.typ == NodeTable || tc.typ == NodeView {
					if child.data.Schema != "sales" || child.data.Name != "Orders" || child.data.DBName != "appdb" {
						t.Errorf("%q lost its parent object: %+v", child.label, child.data)
					}
					if tc.typ == NodeView && (child.data.Type == NodeIndexes || child.data.Type == NodeStatistics) && !child.data.OnView {
						t.Errorf("%q lost view scope", child.label)
					}
				}
			}
			rows := childFolderRows(t.Context(), sc, node)
			var labels []string
			for _, row := range rows {
				labels = append(labels, row[0])
			}
			if !slices.Equal(labels, tc.want) {
				t.Errorf("Details folders = %v, want %v", labels, tc.want)
			}
			if tc.typ != NodeDatabase && len(inst.Reads("")) != before {
				t.Errorf("static folders issued SQL reads: %v", inst.Reads("")[before:])
			}
		})
	}
}

func TestAzureSQLExplorerMenusHideOnlyUnsupportedActions(t *testing.T) {
	a := newTestApp()
	sc, inst := newGConnOnAzure(t, int(gosmo.EngineAzureSQLDatabase))
	sc.Opts.Server = "custom-alias"
	for _, tc := range []struct {
		typ     NodeType
		missing []string
		present []string
	}{
		{NodeServer, []string{"Activity Monitor", "View SQL Server Log"}, []string{"New Database...", "Disconnect", "Properties..."}},
		{NodeDatabases, []string{"Back Up Database...", "Restore Database..."}, []string{"New Database...", "Refresh"}},
		{NodeDatabase, []string{"Back Up Database...", "Restore Database...", "View Backup History"},
			[]string{"New Query", "Generate Scripts...", "Script Database Security", "Rename...", "Delete...", "Shrink", "Properties..."}},
		{NodeServerRoles, []string{"New Server Role..."}, []string{"New Query", "Refresh"}},
		{NodeUsers, nil, []string{"New User...", "Refresh"}},
		{NodeTable, nil, []string{"Rebuild All Indexes", "Full-Text index", "Rename...", "Delete...", "Properties..."}},
	} {
		t.Run(nodeTypeName(tc.typ), func(t *testing.T) {
			node := &explorerNode{data: nodeData{
				Type: tc.typ, DBName: "appdb", Schema: "dbo", Name: "Orders",
				DBState: gosmo.DatabaseOnline, conn: sc,
			}}
			before := len(inst.Reads(""))
			items := a.contextMenuItemsForNode(node)
			for _, label := range tc.missing {
				if findMenuItem(items, label) != nil {
					t.Errorf("unsupported %q is present", label)
				}
			}
			for _, label := range tc.present {
				if findMenuItem(items, label) == nil {
					t.Errorf("supported %q is missing", label)
				}
			}
			if len(items) == 0 || items[0].Divider || items[len(items)-1].Divider {
				t.Errorf("empty menu or edge divider: %v", menuLabelsWithDividers(items))
			}
			for i := 1; i < len(items); i++ {
				if items[i-1].Divider && items[i].Divider {
					t.Errorf("adjacent dividers: %v", menuLabelsWithDividers(items))
				}
			}
			if len(inst.Reads("")) != before {
				t.Error("building an Explorer menu issued SQL")
			}
		})
	}
}

func TestAzureSQLTablesOmitFileTablesButKeepOtherFamilies(t *testing.T) {
	sc, _ := newGConnOnAzure(t, int(gosmo.EngineAzureSQLDatabase), treeFamilyDatabaseRow(),
		tablesPresenceResp(true, true, true, true), tablesListResp(tableRow(1, "dbo", "Orders")))
	got := labelsOfNodes(loadFamily(t, sc, NodeTables))
	want := []string{"System Tables", "External Tables", "Graph Tables", "dbo.Orders"}
	if !slices.Equal(got, want) {
		t.Errorf("Tables = %v, want %v", got, want)
	}
}

func TestExplorerFeatureMenusKeepManagedInstanceAndFabricDistinct(t *testing.T) {
	a := newTestApp()
	mi, _ := newGConnOnAzureMI(t)
	fabricSQL, _ := newGConnOnAzure(t, int(gosmo.EngineFabricSQLDatabase))
	fabric := fabricConn(t)
	for _, tc := range []struct {
		name string
		sc   *db.ServerConn
		want bool
	}{
		{"managed instance", mi, true},
		{"fabric sql database", fabricSQL, true},
		{"fabric", fabric, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := &explorerNode{data: nodeData{Type: NodeDatabase, DBName: "appdb", DBState: gosmo.DatabaseOnline, conn: tc.sc}}
			items := a.contextMenuItemsForNode(node)
			for _, label := range []string{"Back Up Database...", "Restore Database...", "View Backup History", "Rename...", "Delete...", "Shrink"} {
				if got := findMenuItem(items, label) != nil; got != tc.want {
					t.Errorf("%q present = %v, want %v", label, got, tc.want)
				}
			}
			server := &explorerNode{data: nodeData{Type: NodeServer, conn: tc.sc}}
			for _, label := range []string{"Activity Monitor", "View SQL Server Log"} {
				if got := findMenuItem(a.contextMenuItemsForNode(server), label) != nil; got != tc.want {
					t.Errorf("server %q present = %v, want %v", label, got, tc.want)
				}
			}
			roles := &explorerNode{data: nodeData{Type: NodeServerRoles, conn: tc.sc}}
			if findMenuItem(a.contextMenuItemsForNode(roles), "New Server Role...") == nil {
				t.Error("existing New Server Role action was removed")
			}
		})
	}
}

func TestAzureSQLExplorerSupportDoesNotGrantPermissions(t *testing.T) {
	sc, _ := newGConnOnAzure(t, int(gosmo.EngineAzureSQLDatabase),
		capabilityResponses(false, nil, nil, nil, nil)...)
	sc.ProbeCapabilities(sc.Server.Context())
	sc.DatabaseCapabilities(context.Background(), "appdb")
	a := newTestApp()
	node := &explorerNode{data: nodeData{Type: NodeUsers, DBName: "appdb", conn: sc}}
	item := findMenuItem(a.contextMenuItemsForNode(node), "New User...")
	if item == nil || item.Enabled == nil || item.Enabled() {
		t.Fatal("supported New User must remain permission-gated")
	}
}
