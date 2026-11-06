package tui

import (
	"context"
	"slices"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// A Distributor's distribution database (sys.databases.is_distributor) is
// listed under System Databases and offers what SSMS 21 offers on it
// (checked 2026-10-16): no Delete, Rename, Detach, Take Offline or Bring
// Online. It is removed with sp_dropdistributiondb.

func TestDistributionDatabaseIsListedUnderSystemDatabases(t *testing.T) {
	sc, _ := newGConn(t, databasesListResp(
		databaseRow(1, "master", 0),
		distributionRow(5, "distribution"),
		databaseRow(7, "appdb", 0),
	))
	if got, want := labelsOfNodes(loadServerFolder(t, sc, NodeDatabases)),
		[]string{"System Databases", "Database Snapshots", "appdb"}; !slices.Equal(got, want) {
		t.Errorf("Databases = %v, want %v", got, want)
	}

	sys := loadServerFolder(t, sc, NodeSystemDatabases)
	if got, want := labelsOfNodes(sys), []string{"master", "distribution"}; !slices.Equal(got, want) {
		t.Fatalf("System Databases = %v, want %v", got, want)
	}
	if d := sys[1].data; !d.IsDistributor || !d.IsSystem {
		t.Errorf("distribution node: IsDistributor=%v IsSystem=%v, want true/true", d.IsDistributor, d.IsSystem)
	}
	if d := sys[0].data; d.IsDistributor || !d.IsSystem {
		t.Errorf("master node: IsDistributor=%v IsSystem=%v, want false/true", d.IsDistributor, d.IsSystem)
	}
}

// A distribution database alone still gets the System Databases folder.
func TestDistributionDatabaseAloneListsSystemDatabases(t *testing.T) {
	sc, _ := newGConn(t, databasesListResp(distributionRow(5, "distribution")))
	if labels := labelsOfNodes(loadServerFolder(t, sc, NodeDatabases)); !slices.Contains(labels, "System Databases") {
		t.Errorf("Databases = %v, with no System Databases folder", labels)
	}
}

func TestDistributionDatabaseMenuMatchesSSMS(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	for _, state := range []gosmo.DatabaseState{gosmo.DatabaseOnline, gosmo.DatabaseOffline, gosmo.DatabaseRestoring} {
		node := &explorerNode{label: "distribution", data: nodeData{
			Type: NodeDatabase, DBName: "distribution", Name: "distribution",
			IsSystem: true, IsDistributor: true, IsOffline: state != "ONLINE", DBState: state, conn: sc}}
		items := a.contextMenuItemsForNode(node)
		for _, label := range []string{"Delete...", "Rename...", "Detach Database...",
			"Take Database Offline", "Bring Database Online", "Restore With Recovery", "New Snapshot..."} {
			if findMenuItem(items, label) != nil {
				t.Errorf("%s: %q is offered on a distribution database", state, label)
			}
		}
		for _, label := range []string{"Back Up Database...", "Restore Database...", "Properties..."} {
			if findMenuItem(items, label) == nil {
				t.Errorf("%s: %q is missing", state, label)
			}
		}
	}

	// master keeps its offline toggle: only the distribution database loses it.
	master := &explorerNode{label: "master", data: nodeData{
		Type: NodeDatabase, DBName: "master", Name: "master", IsSystem: true, DBState: "ONLINE", conn: sc}}
	if findMenuItem(a.contextMenuItemsForNode(master), "Take Database Offline") == nil {
		t.Error("Take Database Offline is gone from master too")
	}
}

// The actions re-check IsSystem themselves: Delete and Rename are reachable
// by key as well as from the menu.
func TestDistributionDatabaseDeleteAndRenameRefuse(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	n := nodeData{Type: NodeDatabase, DBName: "distribution", Name: "distribution", IsSystem: true, IsDistributor: true, conn: sc}
	called := false
	a.confirmDeleteObjects(sc, []nodeData{n}, func() { called = true })
	a.renameObject(&explorerNode{label: "distribution", data: n})
	if called || a.promptDialog.Visible() {
		t.Errorf("Delete or Rename went ahead on a distribution database (after=%v prompt=%v)", called, a.promptDialog.Visible())
	}
}

// The System Databases detail pane lists what the folder does.
func TestDistributionDatabaseIsInTheSystemDatabasesDetail(t *testing.T) {
	sc, _ := newGConn(t, databasesListResp(
		databaseRow(1, "master", 0),
		distributionRow(5, "distribution"),
		databaseRow(7, "appdb", 0),
	))
	var objs []nodeData
	_, rows, err := systemDatabasesDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeSystemDatabases, conn: sc}}, &objs)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, r := range rows {
		names = append(names, r[0])
	}
	if want := []string{"master", "distribution"}; !slices.Equal(names, want) {
		t.Errorf("System Databases detail = %v, want %v", names, want)
	}
}
