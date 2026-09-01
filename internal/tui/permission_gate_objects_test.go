package tui

import (
	"context"
	"database/sql/driver"
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

// deniedObjectConn returns a connection holding a database-wide ALTER and a
// schema-wide one — the shape of a db_owner — with an explicit DENY of ALTER
// recorded on the named objects. That is the case no wider answer can see: all
// three of the wider rights read 1, and SQL Server refuses the write anyway.
func deniedObjectConn(t *testing.T, dbName string, objDenied []string, sysadmin bool) *db.ServerConn {
	t.Helper()
	res := capabilityResponsesWithSchemas(true, nil, nil,
		[]string{"ALTER", "CONTROL", "ALTER ANY SCHEMA"}, nil, []string{"Sales"}, nil)
	for i, r := range res {
		switch r.match {
		case "IS_ROLEMEMBER":
			for _, n := range objDenied {
				r.rows = append(r.rows, []driver.Value{"O:ALTER", n, int64(0)})
			}
		case "IS_SRVROLEMEMBER":
			if sysadmin {
				r.rows = append(r.rows, []driver.Value{"R", "sysadmin", int64(1)})
			}
		}
		res[i] = r
	}
	sc, _ := newFakeConn(t, res...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), dbName)
	return sc
}

// TestADenyOnTheObjectWithholdsWhatEveryWiderRightPermits. SQL Server resolves
// an object-scope DENY over every wider grant: verified live 2026-09-01, a user
// with database-wide ALTER reads HAS_PERMS_BY_NAME 0 on a table denied ALTER
// and its rename fails Msg 297. Before this, the gate asked the wider rights,
// got 1 from all of them, and offered three items the server then refused.
func TestADenyOnTheObjectWithholdsWhatEveryWiderRightPermits(t *testing.T) {
	sc := deniedObjectConn(t, "appdb", []string{"Sales.Orders"}, false)
	rights := objectOpRights(NodeTable)

	if allowsActionOn(sc, "appdb", "Sales", "Orders", rights...) {
		t.Error("an object with an explicit DENY kept its object ops, though every wider right is overridden by it")
	}
	// The other half: the denial reaches that object and nothing else. A
	// blanket withhold would be just as wrong, and passes the check above.
	if !allowsActionOn(sc, "appdb", "Sales", "Customers", rights...) {
		t.Error("an object with no denial of its own lost its object ops")
	}
	if !allowsActionOn(sc, "appdb", "dbo", "Orders", rights...) {
		t.Error("an object of the same name in another schema lost its object ops")
	}
	// And an action that names no object still reads the wider rights only.
	if !allowsAction(sc, "appdb", databaseWriteRights()...) {
		t.Error("a database-scope action was withheld by an object's denial")
	}
}

// TestASysadminIsNotWithheldByAnObjectDeny. gosmo's object probe reads the
// catalog through every principal the login's permissions can arrive through,
// public included, so a DENY made to public is recorded for a sysadmin too —
// who bypasses the check entirely. Verified live 2026-09-01: with
// DENY ALTER ON OBJECT::dbo.t2 TO public in place, sa reads
// HAS_PERMS_BY_NAME 1 and the rename succeeds.
func TestASysadminIsNotWithheldByAnObjectDeny(t *testing.T) {
	sc := deniedObjectConn(t, "appdb", []string{"Sales.Orders"}, true)
	if !allowsActionOn(sc, "appdb", "Sales", "Orders", objectOpRights(NodeTable)...) {
		t.Error("a sysadmin lost the object ops to a DENY that does not apply to them")
	}
}

// TestTheObjectOpMenuItemsFollowTheObjectDeny drives the menu, not
// allowsActionOn: the denial is only worth having where the items are built,
// and it must not name a right in the note — the login holds ALTER already,
// and being sent to ask for it is the wrong instruction.
func TestTheObjectOpMenuItemsFollowTheObjectDeny(t *testing.T) {
	sc := deniedObjectConn(t, "appdb", []string{"Sales.Orders"}, false)
	app := &App{}
	items := func(name string) []controls.MenuItem {
		return app.objectOpsMenuItems(&explorerNode{data: nodeData{
			Type: NodeTable, Name: name, Schema: "Sales", DBName: "appdb", conn: sc,
		}})
	}
	denied, other := items("Orders"), items("Customers")
	if len(denied) == 0 {
		t.Fatal("the object-ops menu is empty; the test is addressing the wrong node type")
	}
	for i, it := range denied {
		if itemEnabled(it) {
			t.Errorf("%s was offered on an object the server denies ALTER on", it.Label)
		}
		if it.Note != "ALTER denied on this object" {
			t.Errorf("%s disabled note = %q, want it to name the denial rather than a right to ask for",
				it.Label, it.Note)
		}
		if it.NoteWhen == nil || !it.NoteWhen() {
			t.Errorf("%s did not show its note", it.Label)
		}
		if !itemEnabled(other[i]) {
			t.Errorf("%s was withheld on an object with no denial of its own", other[i].Label)
		}
	}
}

// TestAPageDeniedOnItsObjectSaysSoRatherThanNamingARight. The banner is the
// page half of the same rule, and the wording is the point: "Requires ALTER
// (db_owner)" on a page whose login is db_owner describes neither what is
// wrong nor what would fix it.
func TestAPageDeniedOnItsObjectSaysSoRatherThanNamingARight(t *testing.T) {
	sc := deniedObjectConn(t, "appdb", []string{"Sales.Orders"}, false)
	page := withRequiresOn(propPage{title: "General"}, "appdb", "Sales", "Orders", objectWriteRights()...)

	got := pageReadOnlyReason(context.Background(), sc, page)
	if want := readOnlyBannerPrefix + "ALTER is denied on this object."; got != want {
		t.Errorf("banner = %q, want %q", got, want)
	}
	ok := withRequiresOn(propPage{title: "General"}, "appdb", "Sales", "Customers", objectWriteRights()...)
	if got := pageReadOnlyReason(context.Background(), sc, ok); got != "" {
		t.Errorf("a page on an object with no denial was made read-only: %q", got)
	}
}
