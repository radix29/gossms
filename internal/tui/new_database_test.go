package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// New Database driven through its real prefetch and all three applies.
//
// This dialog's risk is not the CREATE statement — gosmo builds and tests that
// — but the three things only this layer decides:
//
//   - A blank file field means "let the server choose", and a partially filled
//     file section means "derive the rest". Sending a zero-valued file spec
//     instead creates a database with a 0 MB file at an empty path.
//   - Every row on General and Options is applied only when it diverges from
//     *model*, because CREATE DATABASE inherits from model. A page that applied
//     unconditionally would emit an ALTER for all twenty-one options on every
//     new database.
//   - Options and Filegroups act on a database named on the *General* page,
//     through a closure. If that name is read from the wrong place the writes
//     land on whatever database that resolves to — the one failure here that
//     touches an existing database.

const newDatabaseName = "AppDB"

// newDatabaseResponses scripts the four reads the prefetch makes. model's
// by-name answer is scoped with arg: and placed ahead of the list read, or
// DatabaseByName("model") resolves to whichever database the list answer sorts
// first and the whole Options page is baselined against the wrong database.
//
// Every option comes back OFF and the recovery model SIMPLE, so a test that
// switches one knows which direction it moved.
func newDatabaseResponses() []fakeResponse {
	return []fakeResponse{
		{match: "compatibility_level, collation_name", arg: "model", cols: 8, rows: [][]driver.Value{
			{"model", int64(3), "ONLINE", "SIMPLE", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
		}},
		{match: "compatibility_level, collation_name", cols: 8, rows: [][]driver.Value{
			{"master", int64(1), "ONLINE", "SIMPLE", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
			{"model", int64(3), "ONLINE", "SIMPLE", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
			{"salesdb", int64(6), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
		}},
		loginListResponse(),
		{match: "page_verify_option_desc", cols: 25, rows: [][]driver.Value{
			append([]driver.Value{"sa", "CHECKSUM", "MULTI_USER", "NONE", false, "OFF"}, falses(19)...),
		}},
	}
}

func newDatabaseDialog(t *testing.T) (*NewDatabaseDialog, *fakeInstance) {
	t.Helper()
	a := newTestApp()
	d := NewNewDatabaseDialog(a)
	sc, inst := newFakeConn(t, newDatabaseResponses()...)
	d.show(sc)
	waitAndDrain(t, a)
	for i, f := range d.forms {
		if f == nil {
			t.Fatalf("the prefetch did not build page %q", d.pages[i])
		}
	}
	return d, inst
}

// ndbPage addresses a page by name rather than by index.
func (d *NewDatabaseDialog) page(t *testing.T, name string) (*propsheet.Form, propApply) {
	t.Helper()
	for i, p := range d.pages {
		if p == name {
			return d.forms[i], d.applyFns[i]
		}
	}
	t.Fatalf("this dialog has pages %v, not %q", d.pages, name)
	return nil, nil
}

// sectionTextRow finds a text row by its label *within* a section. General
// carries "Logical name", "Path", "Initial size" and "Growth" twice — once
// under "Data file" and once under "Log file" — and textRow would hand back
// the data file's row for both, so a test written with it would drive the data
// file twice and still report the log file covered.
func sectionTextRow(t *testing.T, f *propsheet.Form, section, label string) *propsheet.TextRow {
	t.Helper()
	in := false
	for _, r := range f.Rows() {
		if s, ok := r.(*propsheet.SectionRow); ok {
			in = s.Title() == section
			continue
		}
		if !in {
			continue
		}
		if tr, ok := r.(*propsheet.TextRow); ok && tr.Label() == sheetLabel(label) {
			return tr
		}
	}
	t.Fatalf("no text row labelled %q under section %q", label, section)
	return nil
}

// editFileField is editText for a row addressed through sectionTextRow.
func editFileField(t *testing.T, f *propsheet.Form, section, label, value string) {
	t.Helper()
	row := sectionTextRow(t, f, section, label)
	row.Edit(value)
	if !row.Dirty() {
		t.Fatalf("row %q under %q is not dirty after setting it to %q", label, section, value)
	}
}

func TestNewDatabaseWithNoFileFieldsCreatesTheBareDatabase(t *testing.T) {
	d, inst := newDatabaseDialog(t)
	form, apply := d.page(t, "General")

	editText(t, form, "Database name", newDatabaseName)

	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Exactly one statement, and no file clauses in it: the server's own
	// defaults are what a blank file section means.
	assertOneStatement(t, inst, "CREATE DATABASE [AppDB]")
	assertStatementLacks(t, inst.Statements()[0], "ON PRIMARY", "LOG ON", "COLLATE")
}

func TestNewDatabaseCarriesTheFileFieldsThatWereFilledIn(t *testing.T) {
	d, inst := newDatabaseDialog(t)
	form, apply := d.page(t, "General")

	editText(t, form, "Database name", newDatabaseName)
	editFileField(t, form, "Data file", "Logical name", "AppDB_data")
	editFileField(t, form, "Data file", "Path", `D:\Data\AppDB.mdf`)
	editFileField(t, form, "Data file", "Initial size", "512")
	editFileField(t, form, "Data file", "Growth", "64")
	editFileField(t, form, "Log file", "Logical name", "AppDB_translog")
	editFileField(t, form, "Log file", "Path", `E:\Log\AppDB.ldf`)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmt := inst.Statements()[0]
	// The two sections must not cross: the log file's name and path here are
	// deliberately unlike the data file's, so a page that read one section
	// twice cannot produce this statement.
	assertStatementHas(t, stmt,
		"ON PRIMARY",
		"NAME = [AppDB_data]",
		`D:\Data\AppDB.mdf`,
		"LOG ON",
		"NAME = [AppDB_translog]",
		`E:\Log\AppDB.ldf`,
	)
}

// A file section with one field filled in is still a file the user asked for,
// and the rest is derived from the database name — the same identity SQL
// Server would have chosen. Treating a blank name as "no file" here would drop
// the size the user typed.
func TestNewDatabaseDerivesTheRestOfAPartlyFilledFileSection(t *testing.T) {
	d, inst := newDatabaseDialog(t)
	form, apply := d.page(t, "General")

	editText(t, form, "Database name", newDatabaseName)
	editFileField(t, form, "Data file", "Initial size", "512")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmt := inst.Statements()[0]
	assertStatementHas(t, stmt, "ON PRIMARY", "NAME = [AppDB]", "AppDB.mdf", "SIZE = 524288KB")
	// The log section was left entirely blank, so it stays the server's.
	assertStatementLacks(t, stmt, "LOG ON")
}

// Recovery model, compatibility level and owner are each applied only when the
// user moved them off what the new database inherits from model. Applying them
// unconditionally is not harmless: it puts three no-op statements into Script
// Changes for every new database, and an ALTER DATABASE ... SET RECOVERY on a
// value that did not change is indistinguishable in a script from one that did.
func TestNewDatabaseAppliesOnlyTheMaintenanceRowsThatMoved(t *testing.T) {
	t.Run("untouched", func(t *testing.T) {
		d, inst := newDatabaseDialog(t)
		form, apply := d.page(t, "General")
		editText(t, form, "Database name", newDatabaseName)

		if err := apply(context.Background()); err != nil {
			t.Fatalf("apply: %v", err)
		}
		if stmts := inst.Statements(); len(stmts) != 1 {
			t.Errorf("only the CREATE should have run, got:\n%s", strings.Join(stmts, "\n"))
		}
	})

	t.Run("changed", func(t *testing.T) {
		d, inst := newDatabaseDialog(t)
		form, apply := d.page(t, "General")
		editText(t, form, "Database name", newDatabaseName)
		// model is SIMPLE at 160 in the fixture, so both of these moved.
		editSelect(t, form, "Recovery model", "BULK_LOGGED")
		editSelect(t, form, "Compatibility level", "150")
		// Neither is the first login in the list.
		editSelect(t, form, "Owner", "otheruser")

		if err := apply(context.Background()); err != nil {
			t.Fatalf("apply: %v", err)
		}
		assertStatementHas(t, onlyStatementWith(t, inst, "SET RECOVERY"), "ALTER DATABASE [AppDB] SET RECOVERY BULK_LOGGED")
		assertStatementHas(t, onlyStatementWith(t, inst, "COMPATIBILITY_LEVEL"), "SET COMPATIBILITY_LEVEL = 150")
		assertStatementHas(t, onlyStatementWith(t, inst, "ALTER AUTHORIZATION"), "ON DATABASE::[AppDB] TO [otheruser]")
	})
}

// The Options page's apply targets the name typed on General, through a
// closure across the two pages. A page that resolved the database any other
// way would write these options to an existing database.
func TestNewDatabaseOptionsWriteOnlyWhatDivergesFromModel(t *testing.T) {
	d, inst := newDatabaseDialog(t)
	general, _ := d.page(t, "General")
	editText(t, general, "Database name", newDatabaseName)

	form, apply := d.page(t, "Options")
	// Every option in the fixture is OFF, so this is the one that moved.
	editSelect(t, form, "Auto shrink", "ON")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("one option moved, but the page wrote %d statements:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if got, want := stmts[0], "ALTER DATABASE [AppDB] SET AUTO_SHRINK ON"; got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

// Restrict access is the one dropdown on this page that is not in the tracked
// option table, for the same reason as on Database Properties: it must go
// through SetUserAccess and carry WITH ROLLBACK IMMEDIATE. Moving it into the
// table would look like tidying and would produce a SET SINGLE_USER that
// blocks until every other connection leaves.
func TestNewDatabaseRestrictAccessGoesThroughSetUserAccess(t *testing.T) {
	d, inst := newDatabaseDialog(t)
	general, _ := d.page(t, "General")
	editText(t, general, "Database name", newDatabaseName)

	form, apply := d.page(t, "Options")
	editSelect(t, form, "Restrict access", "SINGLE_USER")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "WITH ROLLBACK IMMEDIATE")
}

// The Filegroups page is a pending-add list with a Remove that acts on the
// grid's selection, so the list the grid shows and the list apply walks
// diverge the moment anything is removed. Three added and the *middle* one
// removed is the smallest case that catches an off-by-one there — with two,
// removing either leaves an answer that a shifted index also produces.
func TestNewDatabaseFilegroupsAddOnlyTheOnesStillListed(t *testing.T) {
	d, inst := newDatabaseDialog(t)
	general, _ := d.page(t, "General")
	editText(t, general, "Database name", newDatabaseName)

	form, apply := d.page(t, "Filegroups")
	for _, name := range []string{"FG_Archive", "FG_Scratch", "FG_Current"} {
		editText(t, form, "New filegroup name", name)
		clickButton(t, form, "Add")
	}

	fg := toggleGrid(t, form)
	selectGridRow(t, fg.Grid, 0, "FG_Scratch")
	clickButton(t, form, "Remove")

	// Read-only and Default are the grid's two toggle columns, ticked on
	// different rows so a page that read one column for the other cannot pass.
	toggleByName(t, fg, "FG_Archive", 0)
	toggleByName(t, fg, "FG_Current", 1)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertStatementHas(t, onlyStatementWith(t, inst, "ADD FILEGROUP [FG_Archive]"), "ALTER DATABASE [AppDB]")
	assertStatementHas(t, onlyStatementWith(t, inst, "ADD FILEGROUP [FG_Current]"), "ALTER DATABASE [AppDB]")
	for _, s := range inst.Statements() {
		if strings.Contains(s, "FG_Scratch") {
			t.Errorf("FG_Scratch was removed before OK, but the page wrote:\n%s", s)
		}
	}
	assertStatementHas(t, onlyStatementWith(t, inst, "READ_ONLY"), "MODIFY FILEGROUP [FG_Archive] READ_ONLY")
	assertStatementHas(t, onlyStatementWith(t, inst, "DEFAULT"), "MODIFY FILEGROUP [FG_Current] DEFAULT")
}

// A filegroup's optional first file is added into that filegroup, not into
// PRIMARY — the FILEGROUP clause is the whole point of adding it here rather
// than on the General page.
func TestNewDatabaseFilegroupFirstFileLandsInItsOwnFilegroup(t *testing.T) {
	d, inst := newDatabaseDialog(t)
	general, _ := d.page(t, "General")
	editText(t, general, "Database name", newDatabaseName)

	form, apply := d.page(t, "Filegroups")
	editText(t, form, "New filegroup name", "FG_Archive")
	editText(t, form, "First file logical name", "AppDB_archive")
	editText(t, form, "First file path", `D:\Data\AppDB_archive.ndf`)
	editText(t, form, "First file initial size", "256")
	clickButton(t, form, "Add")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertStatementHas(t, onlyStatementWith(t, inst, "ADD FILE ("),
		"ALTER DATABASE [AppDB]",
		"NAME = [AppDB_archive]",
		`D:\Data\AppDB_archive.ndf`,
		"SIZE = 262144KB",
		"TO FILEGROUP [FG_Archive]",
	)
}

func TestNewDatabasePreflightRejectsANameThatExists(t *testing.T) {
	d, inst := newDatabaseDialog(t)
	general, _ := d.page(t, "General")

	// Not the first database in the fetched list, and the check is
	// case-insensitive.
	editText(t, general, "Database name", "SalesDB")

	if err := d.preflight(); err == nil {
		t.Fatal("preflight accepted a database name that already exists")
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("nothing should have been written:\n%s", strings.Join(stmts, "\n"))
	}
}
