package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"
)

// filesPageResponses scripts a one-data-file, one-log-file database for the
// four reads the Files page's load closure makes: the by-name lookup, the
// options row, sys.database_files, and the filegroups.
//
// The data file starts with 64 MB autogrowth (growth is in 8 KB pages here,
// as sys.database_files reports it) so a test can switch it off.
func filesPageResponses() []fakeResponse {
	return []fakeResponse{
		{match: "compatibility_level, collation_name", cols: 8, rows: [][]driver.Value{{
			"appdb", int64(7), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now(),
		}}},
		{match: "page_verify_option_desc", cols: 25, rows: [][]driver.Value{
			append([]driver.Value{"sa", "CHECKSUM", "MULTI_USER", "NONE", false, "OFF"}, falses(19)...),
		}},
		{match: "df.file_id, df.name, df.physical_name", cols: 10, rows: [][]driver.Value{
			{int64(1), "appdb", `C:\data\appdb.mdf`, "ROWS", "PRIMARY", "ONLINE", int64(204800), int64(-1), int64(8192), false},
			{int64(2), "appdb_log", `C:\data\appdb_log.ldf`, "LOG", "", "ONLINE", int64(51200), int64(-1), int64(1280), false},
		}},
		{match: "fg.name, fg.is_default, fg.is_read_only", cols: 10, rows: [][]driver.Value{
			{"PRIMARY", true, false, "appdb", `C:\data\appdb.mdf`, int64(204800), int64(-1), int64(8192), false, true},
		}},
	}
}

// falses is n false values, for the run of boolean option flags the options
// row ends in — none of which the Files page reads.
func falses(n int) []driver.Value {
	out := make([]driver.Value, n)
	for i := range out {
		out[i] = false
	}
	return out
}

// TestFilesPageWritesTheAutogrowthItWasGiven drives the Files page's real
// load and apply closures end to end: the page reads its files from the fake
// instance, the test edits one field the way a user does, and the statement
// the page executes is read back.
//
// This is the test the autogrowth bug needed. Every encoder-level assertion
// around fileEdit.modify passed while the page as a whole did nothing:
// gosmo reads a zero growth as "leave FILEGROWTH alone", so the ALTER lost
// its only clause, buildAlterFileStatement returned "", AlterFileContext
// returned nil, and Apply reported success. Nothing short of running the
// apply closure and looking at what reached the server could see it.
func TestFilesPageWritesTheAutogrowthItWasGiven(t *testing.T) {
	sc, inst := newFakeConn(t, filesPageResponses()...)
	form, apply := loadPage(t, pageDatabaseFiles(sc, "appdb"), inst)

	// The grid opens on the first file, and the "Selected file" fields below
	// it are that file's live edit — so setting the growth spinner to 0 is
	// exactly the gesture a user makes to switch autogrowth off.
	editText(t, form, "Growth amount", "0")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "FILEGROWTH = 0") {
		t.Errorf("autogrowth was not switched off:\n%s", stmts[0])
	}
	// The size must not travel with it. MODIFY FILE reads SIZE as a grow-to
	// target and rejects anything at or below the file's current size, so a
	// page that resent the unchanged size would fail an edit the user never
	// made — on exactly the files that have grown since the page was opened.
	if strings.Contains(stmts[0], "SIZE") && !strings.Contains(stmts[0], "MAXSIZE") {
		t.Errorf("an unchanged size was sent with the growth edit:\n%s", stmts[0])
	}
}

// TestFilesPageWritesNothingWhenNothingChanged is the other half, and the
// one that would have caught the bug from the opposite side: a page that
// writes on every OK looks identical to a working one until you count the
// statements.
//
// Two guards have to fail together for this to fire, which is why mutating
// either one alone leaves it green: apply skips a file whose changed() is
// false, and gosmo builds no statement from a FileModify that carries only
// the identifying NAME. Defeating just the first costs a round trip and
// writes nothing; defeating just the second never gets past the first. Both
// gone, an untouched page resends every file its own current SIZE — which
// MODIFY FILE reads as a grow-to target and rejects outright.
func TestFilesPageWritesNothingWhenNothingChanged(t *testing.T) {
	sc, inst := newFakeConn(t, filesPageResponses()...)
	_, apply := loadPage(t, pageDatabaseFiles(sc, "appdb"), inst)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote %d statements: %q", len(stmts), stmts)
	}
}

// TestFilesPageRenamesByTheOldName pins the addressing rule: a rename goes
// out as NEWNAME on a MODIFY FILE that still names the file by the name the
// server currently knows it by. Addressing it by the new name targets a file
// that does not exist yet.
func TestFilesPageRenamesByTheOldName(t *testing.T) {
	sc, inst := newFakeConn(t, filesPageResponses()...)
	form, apply := loadPage(t, pageDatabaseFiles(sc, "appdb"), inst)

	editText(t, form, "Logical name", "appdb_data")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "NAME = [appdb]") {
		t.Errorf("the file is not addressed by its old name:\n%s", stmts[0])
	}
	if !strings.Contains(stmts[0], "NEWNAME = [appdb_data]") {
		t.Errorf("the rename did not reach the statement:\n%s", stmts[0])
	}
}

// filestreamFilesResponses is filesPageResponses plus a FILESTREAM file, the
// third type sys.database_files reports and the one the page's Type picker
// cannot offer — ALTER DATABASE ... ADD FILE from this page builds ROWS and
// LOG files only.
func filestreamFilesResponses() []fakeResponse {
	r := filesPageResponses()
	r[2].rows = append(r[2].rows, []driver.Value{
		int64(3), "appdb_fs", `C:\data\appdb_fs`, "FILESTREAM", "fsgroup", "ONLINE", int64(0), int64(0), int64(0), false,
	})
	r[3].rows = append(r[3].rows, []driver.Value{
		"fsgroup", false, false, "appdb_fs", `C:\data\appdb_fs`, int64(0), int64(0), int64(0), false, false,
	})
	return r
}

// TestFilesPageDoesNotRetypeAFilestreamFile pins the fact the page displays
// about a file it cannot create.
//
// The Type picker holds two items, ROWS and LOG. A FILESTREAM file's type is
// in neither, so an indexOf-shaped lookup left "ROWS" showing — a real option,
// not a sentinel — and commitCurrent, which runs on every row change, wrote
// that back onto the edit. Nothing wrong reached the server (changed() ignores
// the type), but the next grid rebuild reported the file as ROWS: a wrong fact
// about the database, in a properties dialog, which is what this page is for.
//
// Driven through the keys a user presses, because OnSelectRow is where the
// commit happens and SetSelectedRow deliberately doesn't fire it.
func TestFilesPageDoesNotRetypeAFilestreamFile(t *testing.T) {
	sc, inst := newFakeConn(t, filestreamFilesResponses()...)
	form, apply := loadPage(t, pageDatabaseFiles(sc, "appdb"), inst)

	const nameCol, typeCol, fgCol = 0, 1, 2
	g := plainGrid(t, form)

	selectGridRow(t, g, nameCol, "appdb_fs")
	if got := selectRow(t, form, "File type").Value(); got != "FILESTREAM" {
		t.Errorf("File type reads %q for a FILESTREAM file, want FILESTREAM", got)
	}
	if got := selectRow(t, form, "Filegroup").Value(); got != "fsgroup" {
		t.Errorf("Filegroup reads %q for a FILESTREAM file, want fsgroup", got)
	}

	// Selecting away commits the row just left; Remove then rebuilds the grid
	// from the edits, which is where a rewritten type becomes visible.
	selectGridRow(t, g, nameCol, "appdb")
	clickButton(t, form, "Remove")

	row := g.Row(gridRowIndex(t, g, nameCol, "appdb_fs"))
	if row[typeCol] != "FILESTREAM" || row[fgCol] != "fsgroup" {
		t.Errorf("the grid now reports the FILESTREAM file as %q/%q", row[typeCol], row[fgCol])
	}

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want exactly the one REMOVE FILE, got %d: %q", len(stmts), stmts)
	}
	if strings.Contains(stmts[0], "appdb_fs") {
		t.Errorf("the FILESTREAM file was written to:\n%s", stmts[0])
	}
}

// TestFilesPageWontAddAFileTypeItCannotBuild is the other side of the widened
// picker: showing FILESTREAM so an existing file reads correctly must not make
// it a choice for Add, whose spec builder handles ROWS and LOG only.
func TestFilesPageWontAddAFileTypeItCannotBuild(t *testing.T) {
	sc, inst := newFakeConn(t, filestreamFilesResponses()...)
	form, apply := loadPage(t, pageDatabaseFiles(sc, "appdb"), inst)

	g := plainGrid(t, form)
	selectGridRow(t, g, 0, "appdb_fs") // widens the picker to FILESTREAM
	editText(t, form, "Logical name", "appdb_fs2")
	clickButton(t, form, "Add")

	if h := hintText(t, form); h == "" {
		t.Error("Add accepted a FILESTREAM file type without saying anything")
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, s := range inst.Statements() {
		if strings.Contains(s, "ADD FILE") {
			t.Errorf("a file this page cannot build was added:\n%s", s)
		}
	}
}
