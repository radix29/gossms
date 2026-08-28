package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// itemNamed returns the menu item node offers under label, driving the real
// tree menu builder rather than allowsAction — a gate declared correctly but
// never attached to the item, or attached with the wrong database or schema,
// passes every test that asks the rights directly.
func itemNamed(t *testing.T, node *explorerNode, label string) controls.MenuItem {
	t.Helper()
	a := &App{}
	for _, it := range a.nodeMenuItems(node) {
		if it.Label == label {
			return it
		}
	}
	t.Fatalf("%v menu = %v, want an item %q", node.data.Type, labelsOf(a.nodeMenuItems(node)), label)
	return controls.MenuItem{}
}

// itemEnabled is nil-safe, so an item that lost its gate entirely reports
// "offered" rather than panicking on a nil Enabled — which is exactly the
// mutant these tests exist to kill.
func itemEnabled(it controls.MenuItem) bool {
	return it.Enabled == nil || it.Enabled()
}

func folderNode(t NodeType, sc *db.ServerConn) *explorerNode {
	return &explorerNode{data: nodeData{
		Type: t, Name: "Orders", Schema: "Sales", DBName: "appdb", conn: sc,
	}}
}

// TestTheDatabaseScopedWriteItemsFollowTheirOwnPermission. Each of these three
// has one permission behind it and no wider alternative that stands in — a
// database-wide ALTER answers 1 for the two key permissions but 0 for ALTER
// ANY SECURITY POLICY (live, 2026-08-27), so the item has to ask for the name
// it needs rather than for a right that merely usually accompanies it.
func TestTheDatabaseScopedWriteItemsFollowTheirOwnPermission(t *testing.T) {
	for _, tt := range []struct {
		node  NodeType
		label string
		right string
	}{
		{NodeColumnMasterKeys, "New Column Master Key...", "ALTER ANY COLUMN MASTER KEY"},
		{NodeColumnEncryptionKeys, "New Column Encryption Key...", "ALTER ANY COLUMN ENCRYPTION KEY"},
		{NodeSecurityPolicy, "Disable", "ALTER ANY SECURITY POLICY"},
	} {
		granted := probedConn(t, "appdb", nil, nil, []string{tt.right}, nil)
		node := folderNode(tt.node, granted)
		node.data.IsEnabled = true // the policy toggle reads "Disable" only when it is on
		if item := itemNamed(t, node, tt.label); !itemEnabled(item) {
			t.Errorf("%q was withheld from a login granted %s", tt.label, tt.right)
		}

		denied := probedConn(t, "appdb", nil, nil, nil, []string{tt.right})
		node = folderNode(tt.node, denied)
		node.data.IsEnabled = true
		item := itemNamed(t, node, tt.label)
		if itemEnabled(item) {
			t.Errorf("%q was offered though the server denied %s", tt.label, tt.right)
		}
		if item.NoteWhen == nil || !item.NoteWhen() || item.Note != "needs "+tt.right {
			t.Errorf("%q disabled note = %q (shown %v), want it to name %s",
				tt.label, item.Note, item.NoteWhen(), tt.right)
		}
	}
}

// TestNewIndexAndNewStatisticsFollowTheSchemaGrant. Both write to the table
// they are opened on, so they take the object-write rights the tree's
// Rename/Move/Delete take — including the schema-scoped ALTER, which is the
// only one a principal granted nothing database-wide can hold. The New Index
// cascade is gated on its parent: a disabled cascade never opens, so the items
// under it need no gate of their own.
func TestNewIndexAndNewStatisticsFollowTheSchemaGrant(t *testing.T) {
	sc := schemaProbedConn(t, "appdb", []string{"Sales"}, []string{"dbo"})

	for _, tt := range []struct {
		node  NodeType
		label string
	}{
		{NodeIndexes, "New Index"},
		{NodeStatistics, "New Statistics..."},
	} {
		node := folderNode(tt.node, sc)
		if item := itemNamed(t, node, tt.label); !itemEnabled(item) {
			t.Errorf("%q was withheld on a table in a schema the login has ALTER on", tt.label)
		}

		node = folderNode(tt.node, sc)
		node.data.Schema = "dbo"
		if item := itemNamed(t, node, tt.label); itemEnabled(item) {
			t.Errorf("%q was offered on a table in a schema the login has no ALTER on", tt.label)
		}
	}
}

// TestNewIndexAndNewStatisticsSurviveADatabaseWideGrant — the other half of
// the same set. A db_owner holds no schema-scoped grant at all, and gating on
// the schema right alone would take both items away from them.
func TestNewIndexAndNewStatisticsSurviveADatabaseWideGrant(t *testing.T) {
	sc := probedConn(t, "appdb", nil, nil, []string{"ALTER"}, nil)

	for _, tt := range []struct {
		node  NodeType
		label string
	}{
		{NodeIndexes, "New Index"},
		{NodeStatistics, "New Statistics..."},
	} {
		if item := itemNamed(t, folderNode(tt.node, sc), tt.label); !itemEnabled(item) {
			t.Errorf("%q was withheld from a login with a database-wide ALTER", tt.label)
		}
	}
}
