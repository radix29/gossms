package tui

import (
	"slices"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/controls"
)

func opNode(t NodeType, schema, name, table string) *explorerNode {
	n := &explorerNode{label: name}
	n.data.Type = t
	n.data.Schema = schema
	n.data.Name = name
	n.data.TableName = table
	return n
}

// Delete and Rename are reachable only from the context menu, so a node type
// missing from the objectOps table makes both unreachable for it.
func TestObjectOpsMenuItemsPerNodeType(t *testing.T) {
	tests := []struct {
		name           string
		node           *explorerNode
		rename, delete bool
	}{
		{"table", opNode(NodeTable, "dbo", "Orders", ""), true, true},
		{"view", opNode(NodeView, "dbo", "vOrders", ""), true, true},
		{"procedure", opNode(NodeStoredProcedure, "dbo", "pDoWork", ""), true, true},
		{"function", opNode(NodeFunction, "dbo", "fnAge", ""), true, true},
		{"index", opNode(NodeIndex, "dbo", "IX_Orders", "Orders"), true, true},
		{"login", opNode(NodeLogin, "", "app_login", ""), true, true},
		{"database", opNode(NodeDatabase, "", "AppDB", ""), true, true},
		// A schema has no rename in SQL Server at all.
		{"schema", opNode(NodeSchema, "Sales", "Sales", ""), false, true},
		// Agent objects keep their own Delete, with per-type wording.
		{"agent job", opNode(NodeAgentJob, "", "nightly", ""), true, false},
		// Folders and columns are not objects these actions apply to.
		{"tables folder", opNode(NodeTables, "", "", ""), false, false},
		{"column", opNode(NodeColumn, "dbo", "OrderID", ""), false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &App{}
			var labels []string
			for _, it := range a.objectOpsMenuItems(tt.node) {
				labels = append(labels, it.Label)
			}
			if got := slices.Contains(labels, "Rename..."); got != tt.rename {
				t.Errorf("Rename present = %v, want %v (menu %v)", got, tt.rename, labels)
			}
			if got := slices.Contains(labels, "Delete..."); got != tt.delete {
				t.Errorf("Delete present = %v, want %v (menu %v)", got, tt.delete, labels)
			}
		})
	}
}

// Every entry must be usable: a noun for the dialogs, and at least one of
// the two actions — an entry with neither only adds an empty group.
func TestObjectOpsTableIsComplete(t *testing.T) {
	for nodeType, op := range objectOps {
		if op.noun == "" {
			t.Errorf("objectOps[%v] has no noun", nodeType)
		}
		if op.drop == nil && op.rename == nil {
			t.Errorf("objectOps[%v] offers neither drop nor rename", nodeType)
		}
		if op.typed && op.drop == nil {
			t.Errorf("objectOps[%v] is typed-confirm but has no drop", nodeType)
		}
	}
}

// SSMS puts Rename/Delete and the Filter items above Refresh, and leaves
// Refresh and Properties... last.
func TestContextMenuPlacesSharedGroupsAboveRefresh(t *testing.T) {
	labels := menuLabels(t, opNode(NodeTable, "dbo", "Orders", ""))
	refresh := slices.Index(labels, "Refresh")
	if refresh < 0 {
		t.Fatalf("table menu has no Refresh: %v", labels)
	}
	for _, want := range []string{"Rename...", "Delete..."} {
		i := slices.Index(labels, want)
		if i < 0 {
			t.Fatalf("table menu = %v, want a %q item", labels, want)
		}
		if i > refresh {
			t.Errorf("%q is at %d, below Refresh at %d: %v", want, i, refresh, labels)
		}
	}
	if last := labels[len(labels)-1]; last != "Properties..." {
		t.Errorf("last item = %q, want Properties...", last)
	}

	// A filterable folder: the Filter pair also lands above Refresh.
	folder := opNode(NodeTables, "", "", "")
	labels = menuLabels(t, folder)
	refresh = slices.Index(labels, "Refresh")
	if i := slices.Index(labels, "Filter Settings..."); i < 0 || i > refresh {
		t.Errorf("Filter Settings... at %d, Refresh at %d: %v", i, refresh, labels)
	}
}

// insertBeforeRefresh must not corrupt the menu it splices into: the shared
// append-to-a-shared-backing-array bug would otherwise overwrite Refresh
// itself.
func TestInsertBeforeRefresh(t *testing.T) {
	items := []controls.MenuItem{{Label: "New Query"}, {Divider: true}, {Label: "Refresh"}, {Label: "Properties..."}}
	got := insertBeforeRefresh(items, []controls.MenuItem{{Label: "Delete..."}})
	// The menu's existing divider is reused above the group; the one below
	// it separates the group from Refresh.
	want := []string{"New Query", "", "Delete...", "", "Refresh", "Properties..."}
	if labels := labelsOf(got); !slices.Equal(labels, want) {
		t.Errorf("menu = %v, want %v", labels, want)
	}

	// Nothing to insert leaves the menu untouched.
	if got := insertBeforeRefresh(items, nil); len(got) != len(items) {
		t.Errorf("empty extra changed the menu: %v", labelsOf(got))
	}
	// No Refresh anchor: the group is appended rather than dropped.
	noRefresh := []controls.MenuItem{{Label: "New Query"}}
	if got := labelsOf(insertBeforeRefresh(noRefresh, []controls.MenuItem{{Label: "Delete..."}})); !slices.Equal(got, []string{"New Query", "", "Delete..."}) {
		t.Errorf("menu without Refresh = %v", got)
	}
}

func labelsOf(items []controls.MenuItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Label)
	}
	return out
}

// The dialogs name the object the way the user knows it — schema-qualified
// where it has a schema — not by node.label, which carries decoration such
// as an index's "(Nonclustered, Unique)".
func TestObjectDisplayName(t *testing.T) {
	tests := []struct {
		node *explorerNode
		want string
	}{
		{opNode(NodeTable, "Sales", "Customer", ""), "Sales.Customer"},
		{opNode(NodeLogin, "", "sa", ""), "sa"},
		// A schema node carries its own name in both fields; "Sales.Sales"
		// would be nonsense.
		{opNode(NodeSchema, "Sales", "Sales", ""), "Sales"},
		// ...but a table whose name happens to equal its schema keeps the
		// qualifier. Testing Schema == Name instead of the node type dropped it.
		{opNode(NodeTable, "Sales", "Sales", ""), "Sales.Sales"},
	}
	for _, tt := range tests {
		if got := objectDisplayName(tt.node); got != tt.want {
			t.Errorf("objectDisplayName(%v) = %q, want %q", tt.node.data.Type, got, tt.want)
		}
	}
}
