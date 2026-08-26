package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// attachTestFiles deliberately does not list the primary first. DBCC
// CHECKPRIMARYFILE's row order is undocumented, and every one of these tests
// passes on a dialog that assumes the first data file is the primary.
func attachTestFiles() []*gosmo.DetachedFile {
	return []*gosmo.DetachedFile{
		{FileID: 3, Name: "AppDB_2", PhysicalName: `C:\Old\AppDB_2.ndf`},
		{FileID: 2, Name: "AppDB_log", PhysicalName: `C:\Old\AppDB_log.ldf`, IsLog: true},
		{FileID: 1, Name: "AppDB", PhysicalName: `C:\Old\AppDB.mdf`},
	}
}

// attachFileNamed addresses a file by its logical name — never by index, so a
// reordered list shows up as the wrong path rather than the wrong row.
func attachFileNamed(t *testing.T, files []*gosmo.DetachedFile, name string) *gosmo.DetachedFile {
	t.Helper()
	for _, f := range files {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no file named %q in %v", name, files)
	return nil
}

func attachTestDialog(t *testing.T, sc *db.ServerConn, files []*gosmo.DetachedFile) *AttachDatabaseDialog {
	t.Helper()
	d := &AttachDatabaseDialog{files: files}
	d.sc = sc
	d.pages = []string{"General"}
	d.forms = make([]*propsheet.Form, 1)
	d.applyFns = make([]propApply, 1)
	d.buildPages(&attachPrefetch{
		dataPath: `C:\Data\`,
		existing: map[string]bool{"master": true, "healthclinic": true},
	})
	return d
}

// The whole reason the dialog reads the file list is that the recorded paths
// are stale the moment the files are moved — which is the case an attach
// exists for. Following the recorded path for the very file the user just
// browsed to somewhere else sends the attach back to the old location.
func TestAttachEditableFilesTakesThePathTheUserPointedAt(t *testing.T) {
	info := &gosmo.DetachedDatabase{Name: "AppDB", Files: attachTestFiles()}
	out := attachEditableFiles(info, `D:\Moved\AppDB.mdf`)

	if got := attachFileNamed(t, out, "AppDB").PhysicalName; got != `D:\Moved\AppDB.mdf` {
		t.Errorf("primary data file = %q, want the browsed path", got)
	}
	// Only the primary — and the primary is file 1, not whichever data file
	// DBCC happened to list first. Rewriting the secondary instead leaves both
	// data files pointed somewhere the attach will not find them.
	if got := attachFileNamed(t, out, "AppDB_2").PhysicalName; got != `C:\Old\AppDB_2.ndf` {
		t.Errorf("the secondary data file was rewritten to %q", got)
	}
	if got := attachFileNamed(t, out, "AppDB_log").PhysicalName; got != `C:\Old\AppDB_log.ldf` {
		t.Errorf("the log file was rewritten to %q", got)
	}
	// The copy must not write through to the list gosmo handed back.
	if got := attachFileNamed(t, info.Files, "AppDB").PhysicalName; got != `C:\Old\AppDB.mdf` {
		t.Errorf("the read result was mutated: %q", got)
	}
}

func TestAttachFilePaths(t *testing.T) {
	files := attachTestFiles()
	tests := []struct {
		name       string
		files      []*gosmo.DetachedFile
		primary    string
		rebuildLog bool
		want       []string
	}{
		// A DBCC that was refused leaves no list, and FOR ATTACH still works
		// from the primary file alone when nothing has moved.
		{"no list read", nil, `D:\Moved\AppDB.mdf`, false, []string{`D:\Moved\AppDB.mdf`}},
		{"every file", files, `D:\Moved\AppDB.mdf`, false,
			[]string{`C:\Old\AppDB_2.ndf`, `C:\Old\AppDB_log.ldf`, `C:\Old\AppDB.mdf`}},
		// ATTACH_REBUILD_LOG builds a new log and rejects a statement naming
		// the old one, so the log has to come out of the list.
		{"rebuilding the log", files, `D:\Moved\AppDB.mdf`, true,
			[]string{`C:\Old\AppDB_2.ndf`, `C:\Old\AppDB.mdf`}},
		{"nothing at all", nil, "", false, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := attachFilePaths(tt.files, tt.primary, tt.rebuildLog)
			if !slices.Equal(got, tt.want) {
				t.Errorf("attachFilePaths = %v, want %v", got, tt.want)
			}
		})
	}
}

// The statement that reaches the server, for the ordinary case: a file list
// read back, one path corrected by hand, and an owner.
func TestAttachWritesTheCorrectedFileList(t *testing.T) {
	sc, inst := newFakeConn(t)
	files := attachEditableFiles(&gosmo.DetachedDatabase{Name: "AppDB", Files: attachTestFiles()},
		`D:\Moved\AppDB.mdf`)
	d := attachTestDialog(t, sc, files)
	f := d.forms[0]

	editText(t, f, "Primary data file", `D:\Moved\AppDB.mdf`)
	editText(t, f, "Attach as", "AppDB_Restored")
	editText(t, f, "Owner", "sa")
	// The secondary file moved too. Selecting its row loads it into the path
	// editor; apply commits whatever is in there.
	g := plainGrid(t, f)
	selectGridRow(t, g, 0, "AppDB_2")
	editText(t, f, "Path of selected file", `D:\Moved\AppDB_2.ndf`)

	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want the CREATE DATABASE and the ownership change, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	for _, want := range []string{
		"CREATE DATABASE [AppDB_Restored]",
		`N'D:\Moved\AppDB.mdf'`,
		`N'D:\Moved\AppDB_2.ndf'`,
		`N'C:\Old\AppDB_log.ldf'`,
		"FOR ATTACH",
	} {
		if !strings.Contains(stmts[0], want) {
			t.Errorf("attached with:\n%s\nwant it to contain %s", stmts[0], want)
		}
	}
	if strings.Contains(stmts[0], `C:\Old\AppDB.mdf`) || strings.Contains(stmts[0], `C:\Old\AppDB_2.ndf`) {
		t.Errorf("the statement still names a path the user corrected:\n%s", stmts[0])
	}
	if !strings.Contains(stmts[1], "ALTER AUTHORIZATION ON DATABASE::[AppDB_Restored] TO [sa]") {
		t.Errorf("owner was not set, it ran:\n%s", stmts[1])
	}
}

// Rebuilding the log has to reach the statement in both halves — the keyword
// and the missing log file. Naming the log alongside ATTACH_REBUILD_LOG is
// rejected by SQL Server.
func TestAttachRebuildLogDropsTheLogFile(t *testing.T) {
	sc, inst := newFakeConn(t)
	d := attachTestDialog(t, sc, attachTestFiles())
	f := d.forms[0]

	editText(t, f, "Primary data file", `C:\Old\AppDB.mdf`)
	editText(t, f, "Attach as", "AppDB")
	editCheck(t, f, "Build a new log file", true)

	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmt := inst.Statements()[0]
	if !strings.Contains(stmt, "FOR ATTACH_REBUILD_LOG") {
		t.Errorf("attached with:\n%s\nwant FOR ATTACH_REBUILD_LOG", stmt)
	}
	if strings.Contains(stmt, "AppDB_log.ldf") {
		t.Errorf("the log file is still named alongside ATTACH_REBUILD_LOG:\n%s", stmt)
	}
}

// A read that was refused (no DBCC rights) must still leave an attach that can
// be made: only the ability to correct a path is lost.
func TestAttachWithNoFileListUsesThePrimaryFileAlone(t *testing.T) {
	sc, inst := newFakeConn(t)
	d := attachTestDialog(t, sc, nil)
	f := d.forms[0]

	editText(t, f, "Primary data file", `C:\Data\AppDB.mdf`)
	editText(t, f, "Attach as", "AppDB")

	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, `(FILENAME = N'C:\Data\AppDB.mdf')`)
}

func TestAttachPreflight(t *testing.T) {
	sc, _ := newFakeConn(t)
	tests := []struct {
		name, mdf, as, want string
	}{
		{"no primary file", "", "AppDB", "primary data file is required"},
		{"no name", `C:\Data\AppDB.mdf`, "", "name to attach the database as is required"},
		{"name already taken", `C:\Data\AppDB.mdf`, "HealthClinic", "already has a database"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := attachTestDialog(t, sc, attachTestFiles())
			f := d.forms[0]
			if tt.mdf != "" {
				editText(t, f, "Primary data file", tt.mdf)
			}
			if tt.as != "" {
				editText(t, f, "Attach as", tt.as)
			}
			err := d.preflight()
			if err == nil {
				t.Fatal("preflight accepted it")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

// attachExistsResponse scripts xp_fileexist for one path. Scoped with arg:
// every probe runs the identical statement, so without it the first answer
// serves every file and a missing one reads as present.
func attachExistsResponse(path string, exists bool) fakeResponse {
	n := int64(0)
	if exists {
		n = 1
	}
	return fakeResponse{match: "xp_fileexist", arg: path, cols: 3,
		rows: [][]driver.Value{{n, int64(0), int64(1)}}}
}

// An attach left with an uncorrected secondary or log path fails on the
// server with a sentence built around the full path, which the dialog's
// one-line message clips mid-path. Refusing here names the files instead.
func TestAttachRefusesFilesThatAreNotOnTheServer(t *testing.T) {
	sc, inst := newFakeConn(t,
		attachExistsResponse(`D:\Moved\AppDB.mdf`, true),
		attachExistsResponse(`C:\Old\AppDB_2.ndf`, false),
		attachExistsResponse(`C:\Old\AppDB_log.ldf`, false),
	)
	files := attachEditableFiles(&gosmo.DetachedDatabase{Name: "AppDB", Files: attachTestFiles()},
		`D:\Moved\AppDB.mdf`)
	d := attachTestDialog(t, sc, files)
	editText(t, d.forms[0], "Primary data file", `D:\Moved\AppDB.mdf`)
	editText(t, d.forms[0], "Attach as", "AppDB")

	err := d.applyFns[0](context.Background())
	if err == nil {
		t.Fatal("the attach was sent with two files that are not on the server")
	}
	for _, want := range []string{"AppDB_2.ndf", "AppDB_log.ldf"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %s", err, want)
		}
	}
	if strings.Contains(err.Error(), "AppDB.mdf") {
		t.Errorf("refusal %q names the file that is there", err)
	}
	if n := len(inst.Statements()); n != 0 {
		t.Errorf("a refused attach executed %d statements", n)
	}
}

// The check must not stand between a complete request and the server.
func TestAttachProceedsWhenEveryFileIsThere(t *testing.T) {
	sc, inst := newFakeConn(t,
		attachExistsResponse(`D:\Moved\AppDB.mdf`, true),
		attachExistsResponse(`C:\Old\AppDB_2.ndf`, true),
		attachExistsResponse(`C:\Old\AppDB_log.ldf`, true),
	)
	files := attachEditableFiles(&gosmo.DetachedDatabase{Name: "AppDB", Files: attachTestFiles()},
		`D:\Moved\AppDB.mdf`)
	d := attachTestDialog(t, sc, files)
	editText(t, d.forms[0], "Primary data file", `D:\Moved\AppDB.mdf`)
	editText(t, d.forms[0], "Attach as", "AppDB")

	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "CREATE DATABASE [AppDB]")
}

// xp_fileexist can be denied where the attach itself is not — nothing
// scripted here, so the probe fails the way a denied one does. An unmeasured
// answer is not a refusal.
func TestAttachIsSentWhenTheFileCheckCannotRun(t *testing.T) {
	sc, inst := newFakeConn(t)
	files := attachEditableFiles(&gosmo.DetachedDatabase{Name: "AppDB", Files: attachTestFiles()},
		`D:\Moved\AppDB.mdf`)
	d := attachTestDialog(t, sc, files)
	editText(t, d.forms[0], "Primary data file", `D:\Moved\AppDB.mdf`)
	editText(t, d.forms[0], "Attach as", "AppDB")

	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "CREATE DATABASE [AppDB]")
}

// Script Changes has to produce the statement whatever is on disk: scripting
// the attach and copying the files afterwards is a legitimate order.
func TestAttachScriptingDoesNotCheckTheFiles(t *testing.T) {
	sc, inst := newFakeConn(t,
		attachExistsResponse(`D:\Moved\AppDB.mdf`, false),
		attachExistsResponse(`C:\Old\AppDB_2.ndf`, false),
		attachExistsResponse(`C:\Old\AppDB_log.ldf`, false),
	)
	files := attachEditableFiles(&gosmo.DetachedDatabase{Name: "AppDB", Files: attachTestFiles()},
		`D:\Moved\AppDB.mdf`)
	d := attachTestDialog(t, sc, files)
	editText(t, d.forms[0], "Primary data file", `D:\Moved\AppDB.mdf`)
	editText(t, d.forms[0], "Attach as", "AppDB")

	before := inst.QueryCount()
	ctx, script := gosmo.WithScript(context.Background())
	if err := d.applyFns[0](ctx); err != nil {
		t.Fatalf("script: %v", err)
	}
	if len(script.Statements) == 0 || !strings.Contains(script.Statements[0], "CREATE DATABASE [AppDB]") {
		t.Fatalf("scripted %v, want the CREATE DATABASE ... FOR ATTACH", script.Statements)
	}
	if n := inst.QueryCount() - before; n != 0 {
		t.Errorf("scripting ran %d queries, want none — the files were not probed", n)
	}
}
