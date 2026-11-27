package tui

import (
	"slices"
	"testing"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

func TestDatabaseIconsDistinguishFabricWarehouse(t *testing.T) {
	sqlServer, _ := newGConn(t)
	azure, _ := newGConnOnAzure(t, int(gosmo.EngineAzureSQLDatabase))
	managedInstance, _ := newGConnOnAzure(t, int(gosmo.EngineAzureManagedInst))
	fabricSQL, _ := newGConnOnAzure(t, int(gosmo.EngineFabricSQLDatabase))
	for _, tt := range []struct {
		name string
		conn *db.ServerConn
		want rune
	}{
		{"unknown", nil, '⬢'},
		{"unconnected", &db.ServerConn{}, '⬢'},
		{"SQL Server", sqlServer, '⬢'},
		{"Azure SQL", azure, '⬢'},
		{"Managed Instance", managedInstance, '⬢'},
		{"Fabric SQL database", fabricSQL, '⬢'},
		{"Fabric Warehouse", fabricConn(t), '⬟'},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp()
			for _, style := range config.AllIconStyles() {
				a.cfg.IconStyle = style
				for _, offline := range []bool{false, true} {
					n := &explorerNode{data: nodeData{Type: NodeDatabase, conn: tt.conn, IsOffline: offline}}
					want := tt.want
					if style == config.IconStyleEmoji {
						want = '🛢'
					} else if style == config.IconStyleNone {
						want = 0
					}
					row := a.explorer.row(n, 1)
					if row.Icon != want || row.Dim != offline {
						t.Errorf("style %v, offline %v: icon %q, dim %v; want %q, %v",
							style, offline, row.Icon, row.Dim, want, offline)
					}
				}
			}
		})
	}
	if got := nodeIcon(nodeData{Type: NodeAvailabilityDatabase}, config.IconStyleSymbols, false); got != '⬢' {
		t.Errorf("availability database icon = %q, want ⬢", got)
	}
}

func TestDatabaseCompletionIconsDistinguishFabricWarehouse(t *testing.T) {
	for _, fabric := range []bool{false, true} {
		qp := newTestQueryPanelWithInventory(t, "testdb", testCustomersOrders())
		qp.app.cfg.IconStyle = config.IconStyleSymbols
		want := '⬢'
		if fabric {
			qp.conn.Server = fabricConn(t).Server
			want = '⬟'
		}
		items := qp.databaseItems("test")
		if len(items) != 1 || items[0].Icon != want {
			t.Errorf("Fabric %v: database completions %v, want one with icon %q", fabric, items, want)
		}
		linked := qp.linkedDatabaseItems(&linkedServer{name: "remote", databases: map[string]string{"linkeddb": "linkeddb"}}, "")
		if len(linked) != 1 || linked[0].Icon != '⬢' {
			t.Errorf("Fabric %v: linked database completions %v, want the ordinary database icon", fabric, linked)
		}
	}
}

func TestFunctionIconsDistinguishExternalFunctions(t *testing.T) {
	for _, style := range []config.IconStyle{config.IconStyleSymbols, config.IconStylePortable, config.IconStyleEmoji, config.IconStyleNone} {
		for _, kind := range []gosmo.FunctionType{gosmo.FunctionTypeScalar, gosmo.FunctionTypeExternal} {
			want := 'ƒ'
			if style == config.IconStyleNone {
				want = 0
			} else if kind == gosmo.FunctionTypeExternal && style != config.IconStyleEmoji {
				want = 'λ'
			}
			if got := nodeIcon(nodeData{Type: NodeFunction, FuncType: kind}, style, false); got != want {
				t.Errorf("function %s, style %v: got %q, want %q", kind, style, got, want)
			}
		}
	}
}

// TestAgentJobsIconIsStopwatchNotFolder checks NodeAgentJobs ("SQL Server
// Agent") gets its own distinct glyph rather than the generic folder icon
// every other container node type falls back to.
func TestAgentJobsIconIsStopwatchNotFolder(t *testing.T) {
	for _, style := range []config.IconStyle{config.IconStyleEmoji, config.IconStyleSymbols, config.IconStylePortable} {
		d := nodeData{Type: NodeAgentJobs}
		if got := nodeIcon(d, style, false); got != '⏱' {
			t.Errorf("nodeIcon(NodeAgentJobs, %v, false) = %q, want stopwatch ⏱", style, got)
		}
		if got := nodeIcon(d, style, true); got != '⏱' {
			t.Errorf("nodeIcon(NodeAgentJobs, %v, true) = %q, want stopwatch ⏱ regardless of expanded state", style, got)
		}
	}
	if got := nodeIcon(nodeData{Type: NodeAgentJobs}, config.IconStyleNone, false); got != 0 {
		t.Errorf("nodeIcon(NodeAgentJobs, IconStyleNone, false) = %q, want 0 (no icon)", got)
	}
}

// TestGroupedFolderTypes pins the families Group by Schema regroups: the
// schema-scoped folders whose listings carry a schema, with System Data Types
// and the other families left flat. Every NodeType is checked, so a type added
// to the set by accident fails here too.
func TestGroupedFolderTypes(t *testing.T) {
	want := map[NodeType]bool{
		NodeTables:                true,
		NodeSystemTables:          true,
		NodeFileTables:            true,
		NodeExternalTables:        true,
		NodeGraphTables:           true,
		NodeViews:                 true,
		NodeSystemViews:           true,
		NodeSynonyms:              true,
		NodeStoredProcedures:      true,
		NodeSystemProcedures:      true,
		NodeFunctions:             true,
		NodeSystemFunctions:       true,
		NodeExternalFunctions:     true,
		NodeSequences:             true,
		NodeUserDefinedDataTypes:  true,
		NodeUserDefinedTableTypes: true,
		NodeUserDefinedTypes:      true,
		NodeXMLSchemaCollections:  true,
	}
	for nt := range nodeTypeCount {
		if got := groupedFolderTypes[nt]; got != want[nt] {
			t.Errorf("groupedFolderTypes[%s] = %v, want %v", nodeTypeName(nt), got, want[nt])
		}
		if groupedFolderTypes[nt] && !isContainerNode(nt) {
			t.Errorf("%s is grouped but is not a folder", nodeTypeName(nt))
		}
	}
}

// TestSchemaGroupNode checks a NodeSchemaGroup is a folder drawn with the
// schema glyph, open or closed, and offers New Query and Refresh only.
func TestSchemaGroupNode(t *testing.T) {
	if !isContainerNode(NodeSchemaGroup) {
		t.Error("NodeSchemaGroup is not a folder")
	}
	if _, ok := childLoaders[NodeSchemaGroup]; ok {
		t.Error("NodeSchemaGroup has a childLoaders entry; its children come from regrouping")
	}
	for _, style := range []config.IconStyle{config.IconStyleEmoji, config.IconStyleSymbols, config.IconStylePortable} {
		want := objectIcon(NodeSchema, style)
		for _, expanded := range []bool{false, true} {
			if got := nodeIcon(nodeData{Type: NodeSchemaGroup}, style, expanded); got != want {
				t.Errorf("nodeIcon(NodeSchemaGroup, %v, %v) = %q, want the schema glyph %q", style, expanded, got, want)
			}
		}
	}

	sc, _ := newGConn(t)
	a := newTestApp()
	folder := &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, conn: sc, DBName: "db1"}}
	group := &explorerNode{label: "dbo", explorerItem: explorerItem{Parent: folder, Loaded: true}, data: nodeData{
		Type: NodeSchemaGroup, DBName: "db1", Schema: "dbo", GroupOf: NodeTables,
	}}
	folder.Children = []*explorerNode{group}
	folder.Loaded = true
	var labels []string
	var refresh func()
	for _, it := range a.nodeMenuItems(group) {
		if !it.Divider {
			labels = append(labels, it.Label)
		}
		if it.Label == refreshMenuLabel {
			refresh = it.Action
		}
	}
	if want := []string{"New Query", refreshMenuLabel}; !slices.Equal(labels, want) {
		t.Fatalf("NodeSchemaGroup menu = %v, want %v", labels, want)
	}
	// Refresh reloads the folder: the group has no loader, so reloading the
	// group itself would leave it empty.
	refresh()
	if folder.Loaded || !group.Retired() {
		t.Errorf("Refresh on a schema group: folder Loaded = %v, group retired = %v; want the folder reloaded",
			folder.Loaded, group.Retired())
	}
}
