package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/db"
)

// Database Snapshots: the server-level folder, the exclusion that keeps a
// snapshot out of the user-database list, and the read-only Properties and
// detail views behind it.
//
// The exclusion is the part with teeth. A snapshot is an ordinary row in
// sys.databases, so a listing that does not filter it shows every snapshot
// twice — once as a user database and once under its own folder — and the two
// entries are the same database.

var snapshotCreated = time.Date(2026, 9, 1, 14, 15, 0, 0, time.UTC)

// databasesListResp answers Server.Databases. The last column is
// source_database_id, which is what makes a row a snapshot.
func databasesListResp(rows ...[]driver.Value) fakeResponse {
	return fakeResponse{match: "compatibility_level, collation_name", cols: 11, rows: rows}
}

func databaseRow(id int, name string, sourceID int) []driver.Value {
	return []driver.Value{
		name, int64(id), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", "SQL_Latin1_General_CP1_CI_AS", false,
		snapshotCreated, int64(sourceID), false,
	}
}

// distributionRow is databaseRow for a Distributor's distribution database
// (sys.databases.is_distributor = 1, the last column).
func distributionRow(id int, name string) []driver.Value {
	r := databaseRow(id, name, 0)
	r[len(r)-1] = true
	return r
}

// snapshotListResp answers Server.DatabaseSnapshots.
func snapshotListResp(rows ...[]driver.Value) fakeResponse {
	return fakeResponse{match: "d.source_database_id IS NOT NULL", cols: 6, rows: rows}
}

func snapshotRow(id int, name, source string, sourceID int) []driver.Value {
	return []driver.Value{name, int64(id), source, int64(sourceID), "ONLINE", snapshotCreated}
}

// loadServerFolder expands a server-level folder (one with no database name).
func loadServerFolder(t *testing.T, sc *db.ServerConn, folder NodeType) []*explorerNode {
	t.Helper()
	loader, ok := childLoaders[folder]
	if !ok {
		t.Fatalf("%v has no childLoaders entry", folder)
	}
	children, err := loader(loaderCtx{ctx: context.Background(), sc: sc},
		&explorerNode{data: nodeData{Type: folder, conn: sc}})
	if err != nil {
		t.Fatalf("loader for %v: %v", folder, err)
	}
	return children
}

// A snapshot must appear once, under its own folder — never also in the user
// list the Databases node builds.
func TestDatabasesFolderExcludesSnapshots(t *testing.T) {
	sc, _ := newGConn(t, databasesListResp(
		databaseRow(1, "master", 0),
		databaseRow(7, "appdb", 0),
		databaseRow(9, "appdb_snapshot", 7),
	))
	labels := labelsOfNodes(loadServerFolder(t, sc, NodeDatabases))
	want := []string{"System Databases", "Database Snapshots", "appdb"}
	if !slices.Equal(labels, want) {
		t.Fatalf("Databases = %v, want %v", labels, want)
	}
}

// The folder is listed on a server with no snapshots too: it is where New
// Snapshot lives, so an empty one is exactly the case that needs it.
func TestDatabaseSnapshotsFolderIsListedWithNoSnapshots(t *testing.T) {
	sc, _ := newGConn(t, databasesListResp(databaseRow(7, "appdb", 0)))
	children := loadServerFolder(t, sc, NodeDatabases)
	labels := labelsOfNodes(children)
	if !slices.Contains(labels, "Database Snapshots") {
		t.Fatalf("Databases = %v, with no Database Snapshots folder", labels)
	}
	i := slices.Index(labels, "Database Snapshots")
	if children[i].data.Type != NodeDatabaseSnapshots {
		t.Errorf("the folder is typed %v, want NodeDatabaseSnapshots", children[i].data.Type)
	}
}

// The folder lists snapshots with their source carried on the node: Restore
// acts on the source, and the label is the snapshot's own name alone.
func TestDatabaseSnapshotsFolderLoader(t *testing.T) {
	sc, _ := newGConn(t, snapshotListResp(
		snapshotRow(9, "appdb_snapshot", "appdb", 7),
		snapshotRow(10, "orphan_snapshot", "", 0),
	))
	children := loadServerFolder(t, sc, NodeDatabaseSnapshots)
	if got := labelsOfNodes(children); !slices.Equal(got, []string{"appdb_snapshot", "orphan_snapshot"}) {
		t.Fatalf("Database Snapshots = %v", got)
	}
	for _, n := range children {
		if n.data.Type != NodeDatabaseSnapshot {
			t.Errorf("%q is typed %v", n.label, n.data.Type)
		}
		// DBName is the snapshot's own name: every read below it runs in the
		// snapshot, not in its source.
		if n.data.DBName != n.data.Name {
			t.Errorf("%q carries DBName %q, want its own name", n.label, n.data.DBName)
		}
	}
	if got := children[0].data.SourceDatabase; got != "appdb" {
		t.Errorf("source database = %q, want appdb", got)
	}
	if got := children[1].data.SourceDatabase; got != "" {
		t.Errorf("a snapshot whose source is gone reports %q, want empty", got)
	}
}

// A snapshot is read-only: Query Store, Storage and Security lead to pages
// that write, so a snapshot gets the object families and nothing else.
func TestSnapshotOffersOnlyTheObjectFolders(t *testing.T) {
	sc, _ := newGConn(t)
	loader := childLoaders[NodeDatabaseSnapshot]
	children, err := loader(loaderCtx{ctx: context.Background(), sc: sc},
		&explorerNode{data: nodeData{Type: NodeDatabaseSnapshot, DBName: "appdb_snapshot",
			Name: "appdb_snapshot", conn: sc}})
	if err != nil {
		t.Fatalf("loadDatabaseSnapshotChildren: %v", err)
	}
	got := labelsOfNodes(children)
	want := []string{"Tables", "Views", "Programmability"}
	if !slices.Equal(got, want) {
		t.Fatalf("a snapshot's folders = %v, want %v", got, want)
	}
	for _, n := range children {
		if n.data.DBName != "appdb_snapshot" {
			t.Errorf("%q reads in %q, want the snapshot itself", n.label, n.data.DBName)
		}
	}
}

// The Detail Browser's own folder listing.
func TestDatabaseSnapshotsFolderDetail(t *testing.T) {
	sc, _ := newGConn(t, snapshotListResp(snapshotRow(9, "appdb_snapshot", "appdb", 7)))
	var objs []nodeData
	cols, rows, err := databaseSnapshotsFolderDetail(context.Background(), sc,
		&explorerNode{data: nodeData{Type: NodeDatabaseSnapshots, conn: sc}}, &objs)
	if err != nil {
		t.Fatalf("databaseSnapshotsFolderDetail: %v", err)
	}
	if !slices.Equal(cols, []string{"Name", "Source", "State", "Created"}) {
		t.Fatalf("columns = %v", cols)
	}
	if len(rows) != 1 || rows[0][0] != "appdb_snapshot" || rows[0][1] != "appdb" {
		t.Fatalf("rows = %v", rows)
	}
	// Without the objs mapping the pane's own Delete is withheld silently.
	if len(objs) != 1 || objs[0].Type != NodeDatabaseSnapshot || objs[0].SourceDatabase != "appdb" {
		t.Fatalf("objs = %+v", objs)
	}
}

// The Properties dialog is the snapshot's own, and every page on it is
// read-only — the Database Properties dialog would offer a recovery model and
// a file list to a database that refuses both.
func TestDatabaseSnapshotPropertiesAreReadOnly(t *testing.T) {
	sc, inst := newGConn(t,
		snapshotListResp(snapshotRow(9, "appdb_snapshot", "appdb", 7)),
		// Server.DatabaseFiles reads sys.master_files, not
		// sys.database_files: a snapshot's own files are recorded there and
		// the read needs no connection into the snapshot.
		// The catalog size (64 MB) is the sparse file's ceiling; the
		// footprint (128 KB) is the DMV's — the probe's figures.
		snapshotFilesResp(int64(128)),
	)
	pages := databaseSnapshotPropPages(sc, "appdb_snapshot")
	if len(pages) != 2 {
		t.Fatalf("the dialog has %d pages, want General and Files", len(pages))
	}
	for _, p := range pages {
		form, apply := loadPageG(t, p, inst)
		if apply != nil {
			t.Errorf("page %q has an apply — a snapshot cannot be written to", p.title)
		}
		if form == nil {
			t.Errorf("page %q built no form", p.title)
		}
	}

	general, _ := loadPageG(t, pages[0], inst)
	if got := staticValue(t, general, "Source database"); got != "appdb" {
		t.Errorf("Source database = %q, want appdb", got)
	}
	if got := staticValue(t, general, "Name"); got != "appdb_snapshot" {
		t.Errorf("Name = %q", got)
	}

	files, _ := loadPageG(t, pages[1], inst)
	grid := firstGrid(t, files)
	var fileRows [][]string
	for i := 0; grid.Row(i) != nil; i++ {
		fileRows = append(fileRows, grid.Row(i))
	}
	if len(fileRows) != 1 {
		t.Fatalf("the Files page shows %d files, want 1", len(fileRows))
	}
	if fileRows[0][0] != "appdb_data" || fileRows[0][4] != `C:\data\appdb_data_appdb_snapshot.ss` {
		t.Errorf("file row = %v", fileRows[0])
	}
	if grid.ColumnIndex("Size on disk") != 2 || grid.ColumnIndex("Maximum size") != 3 ||
		fileRows[0][2] != "128.0 KB" || fileRows[0][3] != "64.0 MB" {
		t.Errorf("row %v: want Size on disk 128.0 KB beside Maximum size 64.0 MB", fileRows[0])
	}
}

// snapshotFilesResp is one 64 MB-ceiling sparse file whose on-disk size is
// onDiskKB (nil: unknown, the reading without VIEW SERVER STATE).
func snapshotFilesResp(onDiskKB any) fakeResponse {
	return fakeResponse{match: "FROM   sys.master_files mf", cols: 10, rows: [][]driver.Value{
		{int64(1), "appdb_data", `C:\data\appdb_data_appdb_snapshot.ss`, "ROWS",
			"ONLINE", int64(65536), int64(-1), int64(1024), false, onDiskKB},
	}}
}

// The Detail Browser's view of one snapshot reports what the sparse files
// occupy, not the catalog size — which for a snapshot is only the ceiling,
// and once read as "Size on disk" showed a 128 KB snapshot as 64 MB.
func TestDatabaseSnapshotDetailShowsSizeOnDisk(t *testing.T) {
	for _, c := range []struct {
		name     string
		onDiskKB any
		want     string
	}{
		{"known", int64(128), "128.0 KB"},
		{"no VIEW SERVER STATE", nil, "N/A"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sc, _ := newGConn(t, snapshotListResp(snapshotRow(9, "appdb_snapshot", "appdb", 7)), snapshotFilesResp(c.onDiskKB))
			_, rows, err := databaseSnapshotDetail(context.Background(), sc,
				&explorerNode{data: nodeData{Type: NodeDatabaseSnapshot, Name: "appdb_snapshot", conn: sc}})
			if err != nil {
				t.Fatalf("databaseSnapshotDetail: %v", err)
			}
			got := map[string]string{}
			for _, r := range rows {
				got[r[0]] = r[1]
			}
			if got["Size on disk"] != c.want || got["Maximum size"] != "64.0 MB" {
				t.Errorf("Size on disk %q, Maximum size %q; want %q and 64.0 MB", got["Size on disk"], got["Maximum size"], c.want)
			}
		})
	}
}

// A snapshot whose source has been dropped can only be dropped itself. The
// General page says so rather than showing an empty value that reads as
// "not read yet".
func TestDatabaseSnapshotPropertiesSayWhenTheSourceIsGone(t *testing.T) {
	sc, inst := newGConn(t, snapshotListResp(snapshotRow(10, "orphan_snapshot", "", 0)))
	pages := databaseSnapshotPropPages(sc, "orphan_snapshot")
	general, _ := loadPageG(t, pages[0], inst)
	if got := staticValue(t, general, "Source database"); got == "" || got == "appdb" {
		t.Errorf("Source database = %q, want a sentence saying the source is gone", got)
	}
}

// Delete is spliced into every menu by contextMenuItemsForNode, above
// Refresh. A branch of nodeMenuItems that adds its own copy gets two, which
// is what the snapshot's arm did — found by opening the menu, not by a test.
func TestASnapshotMenuHasOneDeleteAndItsOwnCommands(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	node := &explorerNode{label: "appdb_snapshot", data: nodeData{
		Type: NodeDatabaseSnapshot, DBName: "appdb_snapshot", Name: "appdb_snapshot",
		SourceDatabase: "appdb", conn: sc}}

	var labels []string
	for _, it := range a.contextMenuItemsForNode(node) {
		if !it.Divider {
			labels = append(labels, it.Label)
		}
	}
	deletes := 0
	for _, l := range labels {
		if l == "Delete..." {
			deletes++
		}
	}
	if deletes != 1 {
		t.Errorf("the menu has %d Delete items: %v", deletes, labels)
	}
	for _, want := range []string{"Restore Database from Snapshot...", "Properties..."} {
		if !slices.Contains(labels, want) {
			t.Errorf("the menu = %v, with no %q", labels, want)
		}
	}
	// Rename is not offered: a snapshot cannot be renamed, and objectOps
	// gives it no rename for exactly that reason.
	if slices.Contains(labels, "Rename...") {
		t.Errorf("the menu offers Rename on a snapshot: %v", labels)
	}
}

// The New Snapshot item belongs on the folder as well as on a database — the
// folder is where a server with no snapshots yet has to reach it.
func TestNewSnapshotIsOfferedOnTheFolderAndOnADatabase(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	for _, node := range []*explorerNode{
		{label: "Database Snapshots", data: nodeData{Type: NodeDatabaseSnapshots, conn: sc}},
		{label: "appdb", data: nodeData{Type: NodeDatabase, DBName: "appdb", Name: "appdb", conn: sc}},
	} {
		var labels []string
		for _, it := range a.contextMenuItemsForNode(node) {
			labels = append(labels, it.Label)
		}
		if !slices.Contains(labels, "New Snapshot...") {
			t.Errorf("%s menu = %v, with no New Snapshot", node.label, labels)
		}
	}
	// A system database cannot be snapshotted; the server refuses it.
	sys := &explorerNode{label: "master", data: nodeData{
		Type: NodeDatabase, DBName: "master", Name: "master", IsSystem: true, conn: sc}}
	for _, it := range a.contextMenuItemsForNode(sys) {
		if it.Label == "New Snapshot..." {
			t.Error("New Snapshot is offered on a system database")
		}
	}
}

// The name offered for a new snapshot follows the source, and a name the user
// typed is never overwritten by changing the source.
func TestDefaultSnapshotName(t *testing.T) {
	if got := defaultSnapshotName("appdb"); got != "appdb_snapshot" {
		t.Errorf("defaultSnapshotName = %q", got)
	}
	if got := defaultSnapshotName(""); got != "" {
		t.Errorf("with no source, defaultSnapshotName = %q, want empty", got)
	}
	sources := []string{"appdb", "reporting"}
	if !isDefaultSnapshotName("reporting_snapshot", sources) {
		t.Error("a name still at one of the offered defaults is not recognised as one")
	}
	if isDefaultSnapshotName("before_the_upgrade", sources) {
		t.Error("a typed name is treated as a default and would be overwritten")
	}
}
