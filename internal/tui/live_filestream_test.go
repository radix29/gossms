//go:build livedb

// Live verification of Database Properties > Files against a real FILESTREAM
// database — the one thing its scripted tests cannot settle.
//
// Everything the page knows about a FILESTREAM file came from a hand-written
// sys.database_files until 2026-09-05: that a file's type_desc is FILESTREAM,
// that its physical name is a directory, that its size and growth are zero, and
// that FileGroupsContext lists the filegroup at all. All four are now measured
// here, and so is the statement the page builds for a new one — SIZE and
// FILEGROWTH on a FILESTREAM file are refused with Msg 5509, which no fake can
// discover.
//
//	go test -tags livedb ./internal/tui/ -run TestLiveFilestream -v \
//	  -livedb 'sqlserver://sa:PASS@host?TrustServerCertificate=true'
//
// Skipped without -livedb, and skipped with a reason on an instance where
// FILESTREAM is not enabled (it cannot be turned on from a SQL connection: the
// RsFx filter driver and the share are Configuration Manager's, and need an OS
// administrator). Creates and drops its own throwaway database.
package tui

import (
	"context"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// liveFilestreamDB creates a throwaway database with one FILESTREAM filegroup
// and one FILESTREAM file in it, and registers its drop.
//
// The paths go under the instance's own default data directory, which is the
// only directory this test can be sure the service account may write to. A
// FILESTREAM file's FILENAME is a directory SQL Server creates and DROP
// DATABASE removes, so nothing is left behind — but its *parent* must exist,
// which is why the default data path is read rather than assumed.
func liveFilestreamDB(t *testing.T, sc *db.ServerConn, ctx context.Context) string {
	t.Helper()

	var level int
	if err := sc.Server.DB().QueryRowContext(ctx,
		"SELECT CAST(SERVERPROPERTY('FilestreamEffectiveLevel') AS int)").Scan(&level); err != nil {
		t.Fatalf("reading FilestreamEffectiveLevel: %v", err)
	}
	if level == 0 {
		t.Skip("FILESTREAM is not enabled on this instance — enable it in SQL Server Configuration Manager and restart the service")
	}

	dataPath := sc.Server.Info().DefaultDataPath
	if dataPath == "" {
		t.Skip("the instance reports no default data path to put the test database in")
	}

	const name = "gossms_live_fs"
	drop := func() {
		_, _ = sc.Server.DB().ExecContext(ctx,
			"IF DB_ID('"+name+"') IS NOT NULL BEGIN "+
				"ALTER DATABASE "+name+" SET SINGLE_USER WITH ROLLBACK IMMEDIATE; DROP DATABASE "+name+"; END")
	}
	drop() // a previous run that died before its cleanup must not fail this one
	stmt := "CREATE DATABASE " + name + "\n" +
		"ON PRIMARY ( NAME = " + name + "_data, FILENAME = '" + dataPath + name + ".mdf', SIZE = 8MB ),\n" +
		"FILEGROUP fsdata CONTAINS FILESTREAM ( NAME = " + name + "_fs, FILENAME = '" + dataPath + name + "_fs' )\n" +
		"LOG ON ( NAME = " + name + "_log, FILENAME = '" + dataPath + name + "_log.ldf', SIZE = 8MB )"
	if _, err := sc.Server.DB().ExecContext(ctx, stmt); err != nil {
		t.Fatalf("creating the FILESTREAM test database:\n%s\n%v", stmt, err)
	}
	t.Cleanup(drop)
	return name
}

// TestLiveFilestreamFilesPage is the whole of item 10: what the page reads
// about a real FILESTREAM database, and what it writes for a new file in one.
func TestLiveFilestreamFilesPage(t *testing.T) {
	sc, ctx := livePropConn(t)
	dbName := liveFilestreamDB(t, sc, ctx)

	// -- what gosmo reports, which is what the page's pickers are built from --

	d, err := sc.Server.DatabaseByNameContext(ctx, dbName)
	if err != nil {
		t.Fatalf("DatabaseByName: %v", err)
	}
	fgs, err := d.FileGroupsContext(ctx)
	if err != nil {
		t.Fatalf("FileGroupsContext: %v", err)
	}
	var fsGroup *gosmo.FileGroup
	for _, fg := range fgs {
		if fg.Name == "fsdata" {
			fsGroup = fg
		}
	}
	// The open question this test was written for: the Filegroup picker is
	// built from these names, and a FILESTREAM filegroup missing from them
	// would leave the page unable to name where such a file lives.
	if fsGroup == nil {
		t.Fatalf("FileGroupsContext did not return the FILESTREAM filegroup; got %v", fgs)
	}
	if !fsGroup.IsFileStream() {
		t.Errorf("fsdata reports type %q, want %q", fsGroup.Type, gosmo.FileStreamFileGroup)
	}

	files, err := d.FilesContext(ctx)
	if err != nil {
		t.Fatalf("FilesContext: %v", err)
	}
	var fsFile *gosmo.DatabaseFileInfo
	for _, f := range files {
		if f.Type == filestreamFileType {
			fsFile = f
		}
	}
	if fsFile == nil {
		t.Fatalf("no FILESTREAM file reported; got %v", files)
	}
	if fsFile.FileGroup != "fsdata" {
		t.Errorf("the FILESTREAM file reports filegroup %q, want fsdata", fsFile.FileGroup)
	}
	// The shape the scripted tests fake. A non-zero size or growth here would
	// mean the page's greyed spinners are hiding a real value.
	if fsFile.SizeKB != 0 || fsFile.GrowthKB != 0 || fsFile.GrowthPercent != 0 {
		t.Errorf("FILESTREAM file reports size %d KB, growth %d KB / %d%%; want zeroes",
			fsFile.SizeKB, fsFile.GrowthKB, fsFile.GrowthPercent)
	}
	if strings.HasSuffix(fsFile.PhysicalName, ".ndf") || strings.HasSuffix(fsFile.PhysicalName, ".mdf") {
		t.Errorf("FILESTREAM file's physical name %q looks like a file; it is a directory", fsFile.PhysicalName)
	}

	// -- what the page writes ---------------------------------------------

	form, apply, err := pageDatabaseFiles(sc, dbName).load(ctx)
	if err != nil {
		t.Fatalf("Files page load: %v", err)
	}

	g := plainGrid(t, form)
	const nameCol, typeCol, fgCol = 0, 1, 2
	row := g.Row(gridRowIndex(t, g, nameCol, dbName+"_fs"))
	if row[typeCol] != filestreamFileType || row[fgCol] != "fsdata" {
		t.Errorf("the grid reports the real FILESTREAM file as %q/%q", row[typeCol], row[fgCol])
	}

	// Onto the data file first: FilesContext orders by type_desc, so the page
	// opens on the FILESTREAM file and its Filegroup picker already reads
	// fsdata — starting there would leave the picker undirtied and prove
	// nothing about the choice being what decides the file's type.
	selectGridRow(t, g, nameCol, dbName+"_data")

	editText(t, form, "Logical name", dbName+"_fs2")
	editSelect(t, form, "Filegroup", "fsdata")
	editText(t, form, "Path", sc.Server.Info().DefaultDataPath+dbName+"_fs2")
	clickButton(t, form, "Add")

	// The assertion the whole live run exists for: this apply used to build
	// SIZE and FILEGROWTH clauses and come back with Msg 5509.
	if err := apply(ctx); err != nil {
		t.Fatalf("adding a FILESTREAM file: %v", err)
	}

	after, err := d.FilesContext(ctx)
	if err != nil {
		t.Fatalf("FilesContext after the add: %v", err)
	}
	var added *gosmo.DatabaseFileInfo
	for _, f := range after {
		if f.Name == dbName+"_fs2" {
			added = f
		}
	}
	if added == nil {
		t.Fatalf("the added file is not in the database; got %v", after)
	}
	// It has to *be* a FILESTREAM file, not merely have been accepted: the same
	// ADD FILE aimed at a ROWS filegroup produces an ordinary data file, so
	// "no error" says nothing on its own.
	if added.Type != filestreamFileType {
		t.Errorf("the added file reports type %q, want %q", added.Type, filestreamFileType)
	}
	if added.FileGroup != "fsdata" {
		t.Errorf("the added file landed in filegroup %q, want fsdata", added.FileGroup)
	}
}
