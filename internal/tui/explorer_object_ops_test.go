package tui

import (
	"slices"
	"testing"

	gosmo "github.com/radix29/gosmo"
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
		name                 string
		node                 *explorerNode
		rename, delete, move bool
	}{
		{"table", opNode(NodeTable, "dbo", "Orders", ""), true, true, true},
		{"view", opNode(NodeView, "dbo", "vOrders", ""), true, true, true},
		{"procedure", opNode(NodeStoredProcedure, "dbo", "pDoWork", ""), true, true, true},
		{"function", opNode(NodeFunction, "dbo", "fnAge", ""), true, true, true},
		// A trigger belongs to its table and moves with it; ALTER SCHEMA
		// TRANSFER refuses one, so the item must not be offered.
		{"trigger", opNode(NodeTrigger, "dbo", "trAudit", "Orders"), true, true, false},
		{"index", opNode(NodeIndex, "dbo", "IX_Orders", "Orders"), true, true, false},
		{"login", opNode(NodeLogin, "", "app_login", ""), true, true, false},
		{"database", opNode(NodeDatabase, "", "AppDB", ""), true, true, false},
		// A schema has no rename in SQL Server at all.
		{"schema", opNode(NodeSchema, "Sales", "Sales", ""), false, true, false},
		// Agent objects keep their own Delete, with per-type wording.
		{"agent job", opNode(NodeAgentJob, "", "nightly", ""), true, false, false},
		// A folder is not an object these actions apply to.
		{"tables folder", opNode(NodeTables, "", "", ""), false, false, false},
		// A column has Delete (ALTER TABLE ... DROP COLUMN) and Rename
		// (sp_rename's COLUMN class, behind a warning), but cannot change
		// schema: it belongs to its table.
		{"column", opNode(NodeColumn, "dbo", "OrderID", "Orders"), true, true, false},
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
			if got := slices.Contains(labels, "Move to Schema..."); got != tt.move {
				t.Errorf("Move to Schema present = %v, want %v (menu %v)", got, tt.move, labels)
			}
		})
	}
}

// A system object offers neither action, whatever its type says. The System *
// folders emit the same node types as the user ones, so before nodeData.IsSystem
// existed the type alone put Delete and Rename on master, on sys.objects and on
// the SQL-Server-created Agent jobs — and Rename on a system database runs
// SET SINGLE_USER WITH ROLLBACK IMMEDIATE before the server refuses it.
func TestObjectOpsMenuItemsRefusesSystemObjects(t *testing.T) {
	system := []struct {
		name string
		node *explorerNode
	}{
		{"system database", opNode(NodeDatabase, "", "master", "")},
		{"system view", opNode(NodeView, "sys", "objects", "")},
		{"system procedure", opNode(NodeStoredProcedure, "sys", "sp_who", "")},
		{"system function", opNode(NodeFunction, "sys", "fn_my_permissions", "")},
		{"system agent job", opNode(NodeAgentJob, "", "syspolicy_purge_history", "")},
		// Move to Schema is gated by the same flag: transferring sys.objects
		// is refused by the server, but only after the question was asked.
		{"system table", opNode(NodeTable, "sys", "objects", "")},
	}
	for _, tt := range system {
		t.Run(tt.name, func(t *testing.T) {
			tt.node.data.IsSystem = true
			a := &App{}
			if items := a.objectOpsMenuItems(tt.node); len(items) != 0 {
				t.Errorf("a system object offered %d menu items, want none: %v", len(items), items)
			}
		})
	}
}

// The loaders behind the System * folders are what set the flag the gate reads,
// so the two have to stay in step: a loader that stops setting it puts Delete
// back on master with nothing to catch it. Asserted on the node builders that
// take no connection.
func TestSystemAgentJobNodesAreMarkedSystem(t *testing.T) {
	l := loaderCtx{}
	sys := agentJobNode(l, &gosmo.Job{Name: "syspolicy_purge_history"})
	if !sys.data.IsSystem {
		t.Error("a SQL-Server-created Agent job is not marked IsSystem")
	}
	user := agentJobNode(l, &gosmo.Job{Name: "Nightly Reindex"})
	if user.data.IsSystem {
		t.Error("a user Agent job is marked IsSystem")
	}
}

// Every entry must be usable: a noun for the dialogs, and at least one of
// the two actions — an entry with neither only adds an empty group.
func TestObjectOpsTableIsComplete(t *testing.T) {
	for nodeType, op := range objectOps {
		if op.noun == "" {
			t.Errorf("objectOps[%v] has no noun", nodeType)
		}
		if op.drop == nil && op.dropWithOption == nil && op.rename == nil && op.transfer == nil {
			t.Errorf("objectOps[%v] offers no action at all", nodeType)
		}
		if op.typed && op.drop == nil {
			t.Errorf("objectOps[%v] is typed-confirm but has no drop", nodeType)
		}
		// The option and the drop it modifies are one feature: a label with
		// no drop behind it draws a checkbox that changes nothing, and a
		// dropWithOption with no label is a hidden argument nobody can set.
		if (op.dropOption == "") != (op.dropWithOption == nil) {
			t.Errorf("objectOps[%v] has dropOption %q and dropWithOption != nil = %v; set both or neither",
				nodeType, op.dropOption, op.dropWithOption != nil)
		}
		// Exactly one drop path, or deleteObject silently ignores one of them.
		if op.drop != nil && op.dropWithOption != nil {
			t.Errorf("objectOps[%v] has both drop and dropWithOption", nodeType)
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
		// A column's Schema/Name are the table's schema and the column's own
		// name, so the qualifier that means anything is the table's.
		{opNode(NodeColumn, "Sales", "CustomerID", "Customer"), "Sales.Customer.CustomerID"},
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
