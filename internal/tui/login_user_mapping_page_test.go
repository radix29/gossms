package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// User Mapping is the most destructive page in Login Properties: unticking Map
// drops a database user, which takes that user's owned schemas and every
// permission granted to it with them, and the gesture is one keypress on a
// grid row. Everything on the page is also addressed by an index — the row's
// position in the grid, the role's position in a parallel name/checked pair,
// and the "selected row" the schema box and role toggles are committed back
// into — so every failure here is a write landing on the wrong database or the
// wrong role, with the UI showing the right one.

// mappingResponses scripts three ONLINE databases where the login is mapped in
// appdb only, and gives appdb and salesdb different role lists so a test cannot
// pass by reading the wrong database's roles.
func mappingResponses() []fakeResponse {
	return append(loginResponses(),
		// The by-name read apply makes before touching a database, first
		// because responses are tried in order: the query contains
		// "FROM sys.databases" like the list read below, so behind it the
		// list answer serves it and every database resolves to master.
		dbByNameResp("appdb", int64(5)),
		dbByNameResp("salesdb", int64(6)),
		dbByNameResp("master", int64(1)),

		fakeResponse{match: "FROM sys.databases", cols: 8, rows: [][]driver.Value{
			{"master", int64(1), "ONLINE", "SIMPLE", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
			{"appdb", int64(5), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
			{"salesdb", int64(6), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
		}},

		// The login's mapping, per database. appdb answers with a user;
		// everything else answers empty, which is what "not mapped" is.
		fakeResponse{match: "dp.sid = @p1", db: "appdb", cols: 3, rows: [][]driver.Value{
			{"appuser", "sales", "db_datareader"},
		}},
		fakeResponse{match: "dp.sid = @p1", cols: 3, rows: nil},

		// Database roles, per database. public is present in both because the
		// page has to exclude it — ALTER ROLE public ADD MEMBER is a syntax
		// error — and the two lists differ so a role toggle proves which
		// database's list it was read from.
		fakeResponse{match: "r.type = 'R'", db: "appdb", cols: 5, rows: [][]driver.Value{
			{"public", int64(0), false, "dbo", nil},
			{"db_datareader", int64(1), true, "dbo", "appuser"},
			{"db_owner", int64(2), true, "dbo", nil},
		}},
		fakeResponse{match: "r.type = 'R'", cols: 5, rows: [][]driver.Value{
			{"public", int64(0), false, "dbo", nil},
			{"db_denydatawriter", int64(3), true, "dbo", nil},
			{"db_owner", int64(2), true, "dbo", nil},
		}},

		// The by-name user read apply makes before a schema change.
		fakeResponse{match: "sp.sid = dp.sid", cols: 9, rows: [][]driver.Value{
			{int64(7), "SQL_USER", "sales", time.Now(), time.Now(), "INSTANCE", []byte{0x01, 0x02}, "appuser", false},
		}},
	)
}

// dbByNameResp answers a by-name sys.databases read for one database.
func dbByNameResp(name string, id int64) fakeResponse {
	return fakeResponse{match: "FROM sys.databases", arg: name, cols: 8, rows: [][]driver.Value{
		{name, id, "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
	}}
}

const mapDatabaseCol = 1 // the grid's Database column
const mapCheckCol = 0    // the grid's Map column

func loadMappingPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	name := "appuser"
	sc, inst := newFakeConn(t, mappingResponses()...)
	form, apply := loadPage(t, pageLoginUserMapping(sc, &name), inst)
	// eachDatabase drops a database whose per-database read fails rather than
	// failing the page (db_scan.go), and gosmo's mapping scan skips one the
	// same way. A half-scripted fake therefore produces an empty grid and a
	// passing test, so every test here starts by insisting the page loaded.
	if got := plainGrid(t, form).Row(2); got == nil {
		t.Fatal("the mapping grid has fewer than three rows — the fake is under-scripted, not the page wrong")
	}
	return inst, apply, form
}

// TestUserMappingMapsTheDatabaseTheRowIsOn ticks Map on the third row and
// insists the user is created in that database and nowhere else.
func TestUserMappingMapsTheDatabaseTheRowIsOn(t *testing.T) {
	inst, apply, form := loadMappingPage(t)
	activateGridCell(t, plainGrid(t, form), mapDatabaseCol, "salesdb", mapCheckCol)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, "salesdb", "CREATE USER")
	assertNoStatementsIn(t, inst, "appdb", "master")
}

// TestUserMappingUnmapsTheDatabaseTheRowIsOn is the same gesture in the
// direction that destroys something. DROP USER takes the user's owned schemas
// and every permission granted to it; done in the wrong database it is not
// recoverable from this dialog.
func TestUserMappingUnmapsTheDatabaseTheRowIsOn(t *testing.T) {
	inst, apply, form := loadMappingPage(t)
	activateGridCell(t, plainGrid(t, form), mapDatabaseCol, "appdb", mapCheckCol)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, "appdb", "DROP USER")
	assertNoStatementsIn(t, inst, "salesdb", "master")
}

// TestUserMappingLoadsTheMappedStateOntoTheRightRow is the read half. The grid
// is built by walking the database list and looking each name up in a map, so
// a row can show another database's mapping without anything else going wrong.
func TestUserMappingLoadsTheMappedStateOntoTheRightRow(t *testing.T) {
	_, _, form := loadMappingPage(t)
	g := plainGrid(t, form)
	for _, tc := range []struct{ db, want string }{
		{"master", "[ ]"},
		{"appdb", "[x]"},
		{"salesdb", "[ ]"},
	} {
		row := g.Row(gridRowIndex(t, g, mapDatabaseCol, tc.db))
		if row[mapCheckCol] != tc.want {
			t.Errorf("%s shows Map %s, want %s", tc.db, row[mapCheckCol], tc.want)
		}
	}
	// The mapped row shows the user and schema the server reported, not the
	// unmapped default of the login's own name and dbo.
	row := g.Row(gridRowIndex(t, g, mapDatabaseCol, "appdb"))
	if row[3] != "sales" {
		t.Errorf("appdb shows schema %q, want %q", row[3], "sales")
	}
}

// TestUserMappingRoleTogglesActOnTheSelectedDatabase pins the second index
// pair on the page: the role toggle grid is parallel to the selected row's own
// roleNames slice, and it is reloaded from a different database's role list
// every time the selection moves. appdb and salesdb are scripted with
// different roles so a toggle read from the wrong list cannot name db_owner in
// both.
func TestUserMappingRoleTogglesActOnTheSelectedDatabase(t *testing.T) {
	inst, apply, form := loadMappingPage(t)
	selectGridRow(t, plainGrid(t, form), mapDatabaseCol, "appdb")
	toggleByName(t, toggleGrid(t, form), "db_owner", 0)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, "appdb", "ALTER ROLE [db_owner] ADD MEMBER [appuser]")
	assertNoStatementsIn(t, inst, "salesdb", "master")
}

// TestUserMappingUntickingARoleRemovesMembership is the other direction, and
// the one that quietly takes access away. db_datareader is the role the
// scripted login is already in, so this is a real remove rather than a
// no-op.
func TestUserMappingUntickingARoleRemovesMembership(t *testing.T) {
	inst, apply, form := loadMappingPage(t)
	selectGridRow(t, plainGrid(t, form), mapDatabaseCol, "appdb")
	toggleByName(t, toggleGrid(t, form), "db_datareader", 0)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, "appdb", "ALTER ROLE [db_datareader] DROP MEMBER [appuser]")
}

// TestUserMappingSkipsRoleAndSchemaEditsForAnUnmappedDatabase pins the rule the
// page's own Note states. There is no user in master to alter, so an edit made
// while sitting on an unmapped row has to be dropped rather than sent.
func TestUserMappingSkipsRoleAndSchemaEditsForAnUnmappedDatabase(t *testing.T) {
	inst, apply, form := loadMappingPage(t)
	selectGridRow(t, plainGrid(t, form), mapDatabaseCol, "master")
	toggleByName(t, toggleGrid(t, form), "db_owner", 0)
	editText(t, form, "Default schema", "guest")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Fatalf("editing an unmapped database wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// TestUserMappingSchemaFollowsTheRowItWasTypedOn is the commit-on-move
// hazard, and the reason the page keeps a `selected` index of its own: the
// schema box holds one database's value at a time, and moving the grid has to
// commit it back to the row it was typed on before loading the next. Getting
// that ordering wrong writes appdb's new schema onto salesdb — silently, since
// both are valid statements.
func TestUserMappingSchemaFollowsTheRowItWasTypedOn(t *testing.T) {
	inst, apply, form := loadMappingPage(t)
	g := plainGrid(t, form)
	selectGridRow(t, g, mapDatabaseCol, "appdb")
	editText(t, form, "Default schema", "reporting")
	// Move away and back, which is what makes this different from typing and
	// pressing OK: the value has to survive a round trip through another
	// database's load.
	selectGridRow(t, g, mapDatabaseCol, "salesdb")
	selectGridRow(t, g, mapDatabaseCol, "appdb")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, "appdb", "DEFAULT_SCHEMA = [reporting]")
	assertNoStatementsIn(t, inst, "salesdb", "master")
}

// TestUserMappingWritesNothingWhenUntouched. Opening Properties on a login and
// pressing OK must not create, drop or re-grant anything — and on this page
// the load itself sets every control, so a page that dirtied a row on load
// would rewrite mappings nobody touched.
func TestUserMappingWritesNothingWhenUntouched(t *testing.T) {
	inst, apply, _ := loadMappingPage(t)
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Fatalf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
