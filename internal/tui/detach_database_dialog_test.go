package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// sp_detach_db's flags are inverted twice over — @skipchecks is the negation
// of "update statistics" and @keepfulltextindexfile the negation of "drop the
// full-text files" — and both are nvarchar 'true'/'false' rather than bits.
// These tests pin the statement that reaches the server for both settings of
// each box, which is the only place a flipped sense shows up: the dialog and
// gosmo agree with each other either way.

func detachTestDialog(t *testing.T, sc *db.ServerConn, pf *detachPrefetch) *DetachDatabaseDialog {
	t.Helper()
	d := &DetachDatabaseDialog{dbName: "AppDB"}
	d.sc = sc
	d.pages = []string{"General"}
	d.forms = make([]*propsheet.Form, 1)
	d.applyFns = make([]propApply, 1)
	d.buildPages(pf)
	return d
}

func detachTestPrefetch() *detachPrefetch {
	return &detachPrefetch{
		state:    "ONLINE",
		sessions: 0,
		files: []*gosmo.DatabaseFileInfo{
			{FileID: 1, Name: "AppDB", Type: "ROWS", PhysicalName: `C:\Data\AppDB.mdf`},
			{FileID: 3, Name: "AppDB_2", Type: "ROWS", PhysicalName: `C:\Data\AppDB_2.ndf`},
			{FileID: 2, Name: "AppDB_log", Type: "LOG", PhysicalName: `C:\Logs\AppDB_log.ldf`},
		},
	}
}

func TestDetachWithEveryOptionOff(t *testing.T) {
	sc, inst := newFakeConn(t)
	d := detachTestDialog(t, sc, detachTestPrefetch())

	if err := d.preflight(); err != nil {
		t.Fatalf("preflight refused a database nothing is connected to: %v", err)
	}
	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// One statement, not two: an unticked Drop connections must not put the
	// database into SINGLE_USER on its way out.
	assertOneStatement(t, inst, "sp_detach_db")
	stmt := inst.Statements()[0]
	for _, want := range []string{"@skipchecks = 'true'", "@keepfulltextindexfile = 'true'"} {
		if !strings.Contains(stmt, want) {
			t.Errorf("detached with:\n%s\nwant it to contain %s", stmt, want)
		}
	}
}

func TestDetachWithEveryOptionOn(t *testing.T) {
	sc, inst := newFakeConn(t)
	d := detachTestDialog(t, sc, detachTestPrefetch())
	f := d.forms[0]

	editCheck(t, f, "Drop connections", true)
	editCheck(t, f, "Update statistics", true)
	editCheck(t, f, "Drop full-text index files", true)

	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want the SINGLE_USER alter and the detach, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], "SET SINGLE_USER WITH ROLLBACK IMMEDIATE") {
		t.Errorf("Drop connections did not close the sessions first, it ran:\n%s", stmts[0])
	}
	// Both flags inverted relative to the box the user ticked.
	for _, want := range []string{"@skipchecks = 'false'", "@keepfulltextindexfile = 'false'"} {
		if !strings.Contains(stmts[1], want) {
			t.Errorf("detached with:\n%s\nwant it to contain %s", stmts[1], want)
		}
	}
}

// A detach with other sessions in the database fails on the server with an
// error that names neither the sessions nor the checkbox that deals with
// them. Refusing here is what turns that into an instruction.
func TestDetachRefusesWhileSessionsAreConnected(t *testing.T) {
	sc, inst := newFakeConn(t)
	pf := detachTestPrefetch()
	pf.sessions = 3
	d := detachTestDialog(t, sc, pf)

	err := d.preflight()
	if err == nil {
		t.Fatal("preflight accepted a detach that would fail on the server")
	}
	if !strings.Contains(err.Error(), "Drop connections") {
		t.Errorf("refusal %q does not name the option that fixes it", err)
	}
	if n := len(inst.Statements()); n != 0 {
		t.Errorf("a refused detach executed %d statements", n)
	}

	editCheck(t, d.forms[0], "Drop connections", true)
	if err := d.preflight(); err != nil {
		t.Errorf("still refused with Drop connections ticked: %v", err)
	}
}

// An unmeasured session count (no VIEW SERVER STATE) must not withhold the
// detach from a db_owner who can perform it.
func TestDetachIsOfferedWhenTheSessionCountIsUnknown(t *testing.T) {
	sc, _ := newFakeConn(t)
	pf := detachTestPrefetch()
	pf.sessions = -1
	d := detachTestDialog(t, sc, pf)

	if err := d.preflight(); err != nil {
		t.Errorf("preflight refused over an unreadable session count: %v", err)
	}
}

// The grid's first row is what the user needs to attach the database again,
// and gosmo orders the file list by type_desc — which sorts LOG ahead of
// ROWS. A grid built straight from that hands out the log file's path.
func TestDetachFileGridStartsAtThePrimaryDataFile(t *testing.T) {
	sc, _ := newFakeConn(t)
	pf := detachTestPrefetch()
	// gosmo's own order, log first.
	pf.files = []*gosmo.DatabaseFileInfo{pf.files[2], pf.files[0], pf.files[1]}
	pf.files = sortDatabaseFiles(pf.files)
	d := detachTestDialog(t, sc, pf)

	g := plainGrid(t, d.forms[0])
	want := [][]string{
		{"AppDB", "ROWS", `C:\Data\AppDB.mdf`},
		{"AppDB_2", "ROWS", `C:\Data\AppDB_2.ndf`},
		{"AppDB_log", "LOG", `C:\Logs\AppDB_log.ldf`},
	}
	for i := range want {
		row := g.Row(i)
		if row == nil {
			t.Fatalf("grid has %d rows, want %d", i, len(want))
		}
		for c := range want[i] {
			if row[c] != want[i][c] {
				t.Errorf("row %d col %d = %q, want %q", i, c, row[c], want[i][c])
			}
		}
	}
	if g.Row(len(want)) != nil {
		t.Errorf("grid has more than the %d files it was given", len(want))
	}
}

// An OFFLINE database is the case the server-scoped read exists for: its
// files cannot be read through a USE, and its paths are exactly what the
// detach is about to destroy. Nothing is scripted for sys.database_files
// here, so the database-scoped read fails the way a real one does.
func TestDetachOnAnOfflineDatabaseStillListsTheFiles(t *testing.T) {
	sc, _ := newFakeConn(t,
		fakeResponse{match: "FROM sys.databases", arg: "AppDB", cols: 8, rows: [][]driver.Value{
			{"AppDB", int64(7), "OFFLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
		}},
		fakeResponse{match: "sys.master_files", cols: 9, rows: [][]driver.Value{
			{int64(1), "AppDB", `C:\Data\AppDB.mdf`, "ROWS", "ONLINE", int64(8192), int64(-1), int64(65536), false},
			{int64(2), "AppDB_log", `C:\Logs\AppDB_log.ldf`, "LOG", "ONLINE", int64(8192), int64(-1), int64(65536), false},
			{int64(3), "AppDB_2", `C:\Data\AppDB_2.ndf`, "ROWS", "ONLINE", int64(8192), int64(-1), int64(65536), false},
		}},
	)

	d := &DetachDatabaseDialog{dbName: "AppDB"}
	pf, err := d.fetchPrefetch(context.Background(), sc)
	if err != nil {
		t.Fatalf("fetchPrefetch: %v", err)
	}
	if pf.state != "OFFLINE" {
		t.Fatalf("state = %q, want OFFLINE", pf.state)
	}
	if len(pf.files) != 3 {
		t.Fatalf("got %d files for an OFFLINE database, want 3 — the server catalog answers in any state", len(pf.files))
	}

	d.sc = sc
	d.pages = []string{"General"}
	d.forms = make([]*propsheet.Form, 1)
	d.applyFns = make([]propApply, 1)
	d.buildPages(pf)

	g := plainGrid(t, d.forms[0])
	want := [][]string{
		{"AppDB", "ROWS", `C:\Data\AppDB.mdf`},
		{"AppDB_2", "ROWS", `C:\Data\AppDB_2.ndf`},
		{"AppDB_log", "LOG", `C:\Logs\AppDB_log.ldf`},
	}
	for i := range want {
		row := g.Row(i)
		if row == nil {
			t.Fatalf("grid has %d rows, want %d", i, len(want))
		}
		for c := range want[i] {
			if row[c] != want[i][c] {
				t.Errorf("row %d col %d = %q, want %q", i, c, row[c], want[i][c])
			}
		}
	}
}

// An ONLINE database must not pay for the fallback: the database-scoped read
// answers, and the server catalog is not asked at all.
func TestDetachOnAnOnlineDatabaseDoesNotReadTheServerCatalog(t *testing.T) {
	sc, _ := newFakeConn(t,
		fakeResponse{match: "FROM sys.databases", arg: "AppDB", cols: 8, rows: [][]driver.Value{
			{"AppDB", int64(7), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
		}},
		fakeResponse{match: "sys.database_files", cols: 10, rows: [][]driver.Value{
			{int64(1), "AppDB", `C:\Data\AppDB.mdf`, "ROWS", "PRIMARY", "ONLINE", int64(8192), int64(-1), int64(65536), false},
		}},
		// Deliberately not scripted: if the fallback runs anyway, this
		// answer is wrong on purpose and the assertion below catches it.
		fakeResponse{match: "sys.master_files", cols: 9, rows: [][]driver.Value{
			{int64(9), "wrong", `C:\Nowhere\wrong.mdf`, "ROWS", "ONLINE", int64(8192), int64(-1), int64(65536), false},
		}},
	)

	d := &DetachDatabaseDialog{dbName: "AppDB"}
	pf, err := d.fetchPrefetch(context.Background(), sc)
	if err != nil {
		t.Fatalf("fetchPrefetch: %v", err)
	}
	if len(pf.files) != 1 || pf.files[0].Name != "AppDB" {
		t.Fatalf("files = %+v, want the one sys.database_files reported", pf.files)
	}
	if pf.files[0].FileGroup != "PRIMARY" {
		t.Errorf("filegroup = %q, want PRIMARY — the server catalog cannot answer that column", pf.files[0].FileGroup)
	}
}

// With neither catalog readable the page has to say so rather than show an
// empty grid: the paths are unrecoverable once the detach has run.
func TestDetachSaysWhyTheFilesAreMissingWhenNeitherCatalogAnswers(t *testing.T) {
	sc, _ := newFakeConn(t)
	d := detachTestDialog(t, sc, &detachPrefetch{state: "OFFLINE", sessions: 0})

	var notes []string
	for _, r := range d.forms[0].Rows() {
		if n, ok := r.(interface{ Text() string }); ok {
			notes = append(notes, n.Text())
		}
	}
	joined := strings.Join(notes, "\n")
	if !strings.Contains(joined, "OFFLINE") || !strings.Contains(joined, "VIEW ANY DEFINITION") {
		t.Errorf("the page does not explain the missing file list:\n%s", joined)
	}
}
