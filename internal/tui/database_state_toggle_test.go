package tui

import (
	"slices"
	"testing"

	"github.com/gdamore/tcell/v3"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// stateToggleLabels is every label databaseStateToggle can produce.
var stateToggleLabels = []string{"Take Database Offline", "Bring Database Online", "Restore With Recovery"}

// K3 (review plan 2026-10-16): the online toggle follows the database's
// state. A RESTORING database was offered Bring Database Online, whose ALTER
// DATABASE ... SET ONLINE the server refuses on every restoring database
// (Msg 5052); it gets Restore With Recovery instead.
func TestDatabaseMenuStateToggleFollowsTheState(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	for state, want := range map[gosmo.DatabaseState]string{
		"ONLINE":            "Take Database Offline",
		"OFFLINE":           "Bring Database Online",
		"EMERGENCY":         "Bring Database Online",
		"RECOVERY_PENDING":  "Bring Database Online",
		"SUSPECT":           "Bring Database Online",
		"RESTORING":         "Restore With Recovery",
		"OFFLINE_SECONDARY": "",
		"RECOVERING":        "",
	} {
		node := &explorerNode{label: "appdb", data: nodeData{Type: NodeDatabase, DBName: "appdb", Name: "appdb",
			IsOffline: state != "ONLINE", DBState: state, conn: sc}}
		items := a.contextMenuItemsForNode(node)
		for _, label := range stateToggleLabels {
			if got := findMenuItem(items, label) != nil; got != (label == want) {
				t.Errorf("%s: %q offered = %v, want %v", state, label, got, label == want)
			}
		}
	}
}

// recoverTree is a connection whose tree holds Databases > {System Databases
// > master, appdb}, every folder loaded and collapsed so a reload starts no
// background load and leaves Loaded false.
func recoverTree(t *testing.T, a *App, sc *db.ServerConn) (dbs, sys, appdb, master *explorerNode) {
	t.Helper()
	a.connections = append(a.connections, sc)
	root := a.explorer.AddRoot("server-one", sc)
	dbs = &explorerNode{label: "Databases", data: nodeData{Type: NodeDatabases, conn: sc}}
	sys = &explorerNode{label: "System Databases", data: nodeData{Type: NodeSystemDatabases, conn: sc}}
	appdb = &explorerNode{label: "appdb", data: nodeData{Type: NodeDatabase, DBName: "appdb", Name: "appdb",
		IsOffline: true, DBState: "RESTORING", conn: sc}}
	master = &explorerNode{label: "master", data: nodeData{Type: NodeDatabase, DBName: "master", Name: "master",
		IsSystem: true, DBState: "ONLINE", conn: sc}}
	a.explorer.SetChildren(root, []*explorerNode{dbs})
	a.explorer.SetChildren(dbs, []*explorerNode{sys, appdb})
	a.explorer.SetChildren(sys, []*explorerNode{master})
	for _, n := range []*explorerNode{root, dbs, sys} {
		n.data.Loaded = true
	}
	return dbs, sys, appdb, master
}

// Restore With Recovery confirms, runs RESTORE DATABASE ... WITH RECOVERY,
// and re-reads the Databases folder rather than writing a state into the node.
func TestRestoreWithRecoveryRecoversAndReloadsTheFolder(t *testing.T) {
	a := newTestApp()
	sc, inst := newGConn(t)
	dbs, _, appdb, _ := recoverTree(t, a, sc)

	item := findMenuItem(a.contextMenuItemsForNode(appdb), "Restore With Recovery")
	if item == nil {
		t.Fatal("a RESTORING database offers no Restore With Recovery")
	}
	item.Action()
	if !a.confirmDialog.Visible() {
		t.Fatal("Restore With Recovery ran without asking")
	}
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone)) // Yes
	drainUntil(t, a, func() bool { return !a.progressDialog.Visible() }, "the recovery to finish")

	if want := "RESTORE DATABASE [appdb] WITH RECOVERY"; !slices.Contains(inst.Statements(), want) {
		t.Errorf("statements = %q, want %q", inst.Statements(), want)
	}
	if slices.ContainsFunc(inst.Statements(), func(s string) bool { return s == "ALTER DATABASE [appdb] SET ONLINE" }) {
		t.Error("Restore With Recovery sent SET ONLINE, which a restoring database refuses")
	}
	if dbs.data.Loaded || !appdb.retired {
		t.Errorf("the Databases folder was not re-read (Loaded=%v, node retired=%v)", dbs.data.Loaded, appdb.retired)
	}
	if appdb.data.DBState != "RESTORING" {
		t.Errorf("the old node's state was hand-written to %q", appdb.data.DBState)
	}
	if a.statusText != `Database "appdb" is now online` {
		t.Errorf("status = %q", a.statusText)
	}
}

// A system database's state change reloads System Databases, its own folder,
// not Databases above it: that would collapse System Databases.
func TestStateToggleOnASystemDatabaseReloadsSystemDatabases(t *testing.T) {
	a := newTestApp()
	sc, inst := newGConn(t)
	dbs, sys, _, master := recoverTree(t, a, sc)

	a.toggleDatabaseOffline(sc, master)
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone)) // Yes
	drainUntil(t, a, func() bool { return !a.progressDialog.Visible() }, "the toggle to finish")

	if want := "ALTER DATABASE [master] SET OFFLINE WITH ROLLBACK IMMEDIATE"; !slices.Contains(inst.Statements(), want) {
		t.Fatalf("statements = %q, want %q", inst.Statements(), want)
	}
	if sys.data.Loaded || !master.retired {
		t.Errorf("System Databases was not re-read (Loaded=%v, master retired=%v)", sys.data.Loaded, master.retired)
	}
	if !dbs.data.Loaded {
		t.Error("the Databases folder was reloaded for a system database, collapsing System Databases")
	}
}
