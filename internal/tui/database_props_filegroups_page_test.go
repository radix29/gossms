package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"
)

// filegroupsPageResponses scripts three filegroups — PRIMARY (default, one
// file), ARCHIVE (read-only, one file) and STAGING (one file) — so the page
// has a row in each toggle state and more than one row to get wrong.
//
// FileGroupsContext returns one row per file joined to its filegroup, ordered
// by filegroup name, and folds them into groups.
func filegroupsPageResponses() []fakeResponse {
	fg := func(name string, isDefault, readOnly bool, file string, primary bool) []driver.Value {
		return []driver.Value{name, isDefault, readOnly, file, `C:\data\` + file + ".ndf",
			int64(204800), int64(-1), int64(8192), false, primary}
	}
	return []fakeResponse{
		{match: "compatibility_level, collation_name", cols: 8, rows: [][]driver.Value{{
			"appdb", int64(7), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now(),
		}}},
		{match: "fg.name, fg.is_default, fg.is_read_only", cols: 10, rows: [][]driver.Value{
			fg("ARCHIVE", false, true, "appdb_archive", false),
			fg("PRIMARY", true, false, "appdb", true),
			fg("STAGING", false, false, "appdb_staging", false),
		}},
	}
}

// TestFilegroupTogglesActOnTheRowTheyAreOn is the alignment test for a grid
// with two toggle columns rather than one.
//
// Read-only and Default are adjacent checkboxes writing entirely different
// statements — MODIFY FILEGROUP ... READ_ONLY versus ... DEFAULT — and the
// page pulls them back out of the grid as v[0] and v[1] positionally. Ticking
// the wrong one on a large filegroup makes it read-only when the user meant to
// make it the default, which fails the next write into it rather than
// announcing itself here.
func TestFilegroupTogglesActOnTheRowTheyAreOn(t *testing.T) {
	for _, tc := range []struct {
		name, group, want string
		col               int
	}{
		{"read-only on STAGING", "STAGING", "ALTER DATABASE [appdb] MODIFY FILEGROUP [STAGING] READ_ONLY", 0},
		{"read-write on ARCHIVE", "ARCHIVE", "ALTER DATABASE [appdb] MODIFY FILEGROUP [ARCHIVE] READ_WRITE", 0},
		{"default onto STAGING", "STAGING", "ALTER DATABASE [appdb] MODIFY FILEGROUP [STAGING] DEFAULT", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sc, inst := newFakeConn(t, filegroupsPageResponses()...)
			form, apply := loadPage(t, pageDatabaseFilegroups(sc, "appdb"), inst)

			toggleByName(t, toggleGrid(t, form), tc.group, tc.col)

			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			stmts := inst.Statements()
			if len(stmts) != 1 {
				t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
			}
			if stmts[0] != tc.want {
				t.Errorf("got:  %s\nwant: %s", stmts[0], tc.want)
			}
		})
	}
}

// TestFilegroupTogglesLoadInTheStateTheServerReports covers the read half of
// the same two columns. A page that put Default in the Read-only column would
// pass every write test above and still show PRIMARY as read-only — and then
// write that state back the moment anything else changed.
func TestFilegroupTogglesLoadInTheStateTheServerReports(t *testing.T) {
	sc, inst := newFakeConn(t, filegroupsPageResponses()...)
	form, _ := loadPage(t, pageDatabaseFilegroups(sc, "appdb"), inst)

	grid := toggleGrid(t, form)
	want := map[string][2]bool{ // name -> {read-only, default}
		"ARCHIVE": {true, false},
		"PRIMARY": {false, true},
		"STAGING": {false, false},
	}
	for i, row := range grid.Text() {
		v := grid.Values()[i]
		if got := [2]bool{v[0], v[1]}; got != want[row[0]] {
			t.Errorf("%s: read-only/default = %v, want %v", row[0], got, want[row[0]])
		}
	}
	if grid.Dirty() {
		t.Error("the grid is dirty straight out of load: Apply would rewrite every filegroup")
	}
}

// TestRemovingAFilegroupRemovesTheOneSelected is the destructive path. Remove
// works off the grid's selected row against `visible`, a slice that shrinks as
// rows are marked — so the index the button reads and the filegroup the user
// pointed at stay in step only because rowsFor rebuilds the pairing. Getting
// it wrong drops a different filegroup than the one on screen.
func TestRemovingAFilegroupRemovesTheOneSelected(t *testing.T) {
	sc, inst := newFakeConn(t, filegroupsPageResponses()...)
	form, apply := loadPage(t, pageDatabaseFilegroups(sc, "appdb"), inst)

	grid := toggleGrid(t, form)
	grid.Grid.SetSelectedRow(2) // STAGING, last of the three
	if got := grid.Grid.Row(2)[0]; got != "STAGING" {
		t.Fatalf("row 2 is %q, not STAGING — the fixture changed", got)
	}
	clickButton(t, form, "Remove")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
	}
	if stmts[0] != "ALTER DATABASE [appdb] REMOVE FILEGROUP [STAGING]" {
		t.Errorf("Remove on the STAGING row wrote:\n%s", stmts[0])
	}
}

// TestATogglePastAPendingRemoveStillLandsOnItsOwnFilegroup is the specific
// index hazard the pendingRemove flag creates: the grid shows `visible`, which
// no longer contains the removed row, while `edits` still does. syncToggles
// walks the two together, so a row after the gap has different indices in each.
// Without that pairing, un-read-only-ing ARCHIVE after removing PRIMARY writes
// the toggle onto whichever filegroup happens to sit at the grid's index.
func TestATogglePastAPendingRemoveStillLandsOnItsOwnFilegroup(t *testing.T) {
	sc, inst := newFakeConn(t, filegroupsPageResponses()...)
	form, apply := loadPage(t, pageDatabaseFilegroups(sc, "appdb"), inst)

	grid := toggleGrid(t, form)
	grid.Grid.SetSelectedRow(0) // ARCHIVE, the first row
	clickButton(t, form, "Remove")

	// STAGING has moved up a row now that ARCHIVE is gone from the grid.
	toggleByName(t, grid, "STAGING", 0)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want two statements, got %d: %q", len(stmts), stmts)
	}
	joined := strings.Join(stmts, "\n")
	if !strings.Contains(joined, "REMOVE FILEGROUP [ARCHIVE]") {
		t.Errorf("ARCHIVE was not the filegroup removed:\n%s", joined)
	}
	if !strings.Contains(joined, "MODIFY FILEGROUP [STAGING] READ_ONLY") {
		t.Errorf("the read-only toggle did not land on STAGING:\n%s", joined)
	}
}

// TestAddingAFilegroupWritesItByName covers the Add button, and that the new
// row does not also pick up a toggle statement it was never given.
func TestAddingAFilegroupWritesItByName(t *testing.T) {
	sc, inst := newFakeConn(t, filegroupsPageResponses()...)
	form, apply := loadPage(t, pageDatabaseFilegroups(sc, "appdb"), inst)

	textRow(t, form, "New filegroup name").Edit("REPORTING")
	clickButton(t, form, "Add")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
	}
	if stmts[0] != "ALTER DATABASE [appdb] ADD FILEGROUP [REPORTING]" {
		t.Errorf("got: %s", stmts[0])
	}
}

// TestFilegroupsPageWritesNothingWhenUntouched. PRIMARY is already the default
// and ARCHIVE already read-only, so a page that wrote each row's current state
// rather than its changes would look successful and reissue both.
func TestFilegroupsPageWritesNothingWhenUntouched(t *testing.T) {
	sc, inst := newFakeConn(t, filegroupsPageResponses()...)
	_, apply := loadPage(t, pageDatabaseFilegroups(sc, "appdb"), inst)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched Filegroups page wrote %d statements: %q", len(stmts), stmts)
	}
}

// TestRemovingTwoFilegroupsRemovesBothTheOnesSelected is the case a single
// Remove cannot reach. With nothing pending, the grid's rows and the page's
// edit slice are the same list, so the button reads the right filegroup
// whichever of the two it indexes. After the first Remove they diverge, and
// only then does it matter that the second Remove resolves its row through
// `visible` — the list the user is actually looking at.
func TestRemovingTwoFilegroupsRemovesBothTheOnesSelected(t *testing.T) {
	sc, inst := newFakeConn(t, filegroupsPageResponses()...)
	form, apply := loadPage(t, pageDatabaseFilegroups(sc, "appdb"), inst)

	grid := toggleGrid(t, form)
	grid.Grid.SetSelectedRow(0) // ARCHIVE
	clickButton(t, form, "Remove")

	// PRIMARY has moved to row 0; STAGING is now row 1, and its index in the
	// page's own edit slice is still 2.
	if got := grid.Grid.Row(1)[0]; got != "STAGING" {
		t.Fatalf("after the first Remove, row 1 is %q, not STAGING", got)
	}
	grid.Grid.SetSelectedRow(1)
	clickButton(t, form, "Remove")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want two statements, got %d: %q", len(stmts), stmts)
	}
	joined := strings.Join(stmts, "\n")
	for _, fg := range []string{"[ARCHIVE]", "[STAGING]"} {
		if !strings.Contains(joined, "REMOVE FILEGROUP "+fg) {
			t.Errorf("%s was not removed:\n%s", fg, joined)
		}
	}
	if strings.Contains(joined, "[PRIMARY]") {
		t.Errorf("PRIMARY was removed — the second Remove resolved its row against the wrong list:\n%s", joined)
	}
}
