package tui

import (
	"slices"
	"testing"
)

// The Script cascade is the only way to script an object, so a node type
// missing from the scriptables table can't be scripted at all.
func TestScriptMenuItemsPerNodeType(t *testing.T) {
	tests := []struct {
		name  string
		node  *explorerNode
		item  string // "" = no Script item at all
		verbs []string
	}{
		{"table", opNode(NodeTable, "dbo", "Orders", ""), "Script Table as",
			[]string{"CREATE To", "DROP To", "DROP And CREATE To", "SELECT To", "INSERT To", "UPDATE To", "DELETE To"}},
		{"view", opNode(NodeView, "dbo", "vOrders", ""), "Script View as",
			[]string{"CREATE To", "ALTER To", "DROP To", "DROP And CREATE To", "SELECT To", "INSERT To", "UPDATE To", "DELETE To"}},
		{"procedure", opNode(NodeStoredProcedure, "dbo", "pDoWork", ""), "Script Stored Procedure as",
			[]string{"CREATE To", "ALTER To", "DROP To", "DROP And CREATE To", "EXECUTE To"}},
		{"function", opNode(NodeFunction, "dbo", "fnAge", ""), "Script Function as",
			[]string{"CREATE To", "ALTER To", "DROP To", "DROP And CREATE To", "SELECT To"}},
		// An index adds the three maintenance statements below its DDL ones.
		{"index", opNode(NodeIndex, "dbo", "IX_Orders", "Orders"), "Script Index as",
			[]string{"CREATE To", "DROP To", "DROP And CREATE To",
				"REBUILD To", "REORGANIZE To", "UPDATE STATISTICS To"}},
		// A key is an index too, but ALTER INDEX on a constraint-backing one
		// is Index Properties' business, not the constraint's menu.
		{"key", opNode(NodeKey, "dbo", "PK_Orders", "Orders"), "Script Key as",
			[]string{"CREATE To", "DROP To", "DROP And CREATE To"}},
		{"login", opNode(NodeLogin, "", "app_login", ""), "Script Login as",
			[]string{"CREATE To", "DROP To", "DROP And CREATE To"}},
		// A database has no DROP form — see scriptables.
		{"database", opNode(NodeDatabase, "", "AppDB", ""), "Script Database as", []string{"CREATE To"}},
		// Folders and columns are not objects that script.
		{"tables folder", opNode(NodeTables, "", "", ""), "", nil},
		{"column", opNode(NodeColumn, "dbo", "OrderID", ""), "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &App{}
			items := a.scriptMenuItems(tt.node)
			if tt.item == "" {
				if len(items) != 0 {
					t.Fatalf("scriptMenuItems = %v, want none", labelsOf(items))
				}
				return
			}
			if len(items) != 1 || items[0].Label != tt.item {
				t.Fatalf("scriptMenuItems = %v, want one %q", labelsOf(items), tt.item)
			}
			if got := labelsOf(items[0].Sub); !slices.Equal(got, tt.verbs) {
				t.Errorf("verbs = %v, want %v", got, tt.verbs)
			}
			// Every verb is a cascade of its own, never a leaf that fires
			// without the user saying where the script should go.
			for _, v := range items[0].Sub {
				if v.Action != nil {
					t.Errorf("verb %q fires an action directly", v.Label)
				}
				if got := labelsOf(v.Sub); !slices.Equal(got, []string{"New Query Editor Window", "File...", "Clipboard"}) {
					t.Errorf("verb %q destinations = %v", v.Label, got)
				}
			}
		})
	}
}

// The Script cascade is spliced in above Rename/Delete, matching SSMS's
// order, and every destination under it is reachable.
func TestScriptCascadeSitsAboveRenameInTheNodeMenu(t *testing.T) {
	a := &App{}
	node := opNode(NodeTable, "dbo", "Orders", "")
	labels := labelsOf(a.contextMenuItemsForNode(node))
	script := slices.Index(labels, "Script Table as")
	rename := slices.Index(labels, "Rename...")
	refresh := slices.Index(labels, refreshMenuLabel)
	if script < 0 || rename < 0 || refresh < 0 {
		t.Fatalf("menu %v is missing one of Script/Rename/Refresh", labels)
	}
	if !(script < rename && rename < refresh) {
		t.Errorf("order = Script %d, Rename %d, Refresh %d in %v", script, rename, refresh, labels)
	}
}

func TestScriptFileNameQualifiesBySchema(t *testing.T) {
	for _, tt := range []struct {
		n    nodeData
		want string
	}{
		{nodeData{Schema: "dbo", Name: "Orders"}, "dbo.Orders.sql"},
		{nodeData{Name: "app_login"}, "app_login.sql"},
	} {
		if got := scriptFileName(tt.n); got != tt.want {
			t.Errorf("scriptFileName(%+v) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// A system module can't be scripted at all — its definition is in the
// resource database — so it offers no Script item. A system database still
// does: its script is assembled from metadata, not from sys.sql_modules.
func TestScriptMenuItemsOnSystemObjects(t *testing.T) {
	view := opNode(NodeView, "sys", "objects", "")
	view.data.IsSystem = true
	if items := (&App{}).scriptMenuItems(view); len(items) != 0 {
		t.Errorf("system view offers %v", labelsOf(items))
	}
	master := opNode(NodeDatabase, "", "master", "")
	master.data.IsSystem = true
	if items := (&App{}).scriptMenuItems(master); len(items) != 1 {
		t.Errorf("system database offers %v, want the Script cascade", labelsOf(items))
	}
}
