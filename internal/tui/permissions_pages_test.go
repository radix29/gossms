package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Four Properties pages hand buildPermissionsMatrix a different principal
// list, permission catalog and apply func, and are otherwise the same editor:
// Server Properties > Permissions, Database Properties > Permissions, and the
// Permissions page of Schema and Table Properties. The matrix itself is
// covered by server_permissions_page_test.go, which drives the single-principal
// form; what is untested is each page's own wiring, and that is where the
// damage is. All four write GRANT/DENY/REVOKE, and the scope is carried by the
// apply func alone — a page wired to the wrong one grants on the whole database
// what the user granted on one table, and the grid shows the change it asked
// for either way.

const permDatabase = "appdb"

// permPrincipalResponses are the users and roles every database-scoped page
// here lists. Two of each, so the principal acted on is never the first row and
// never the only one of its kind: a page that ignored the principal grid
// entirely would pass against a one-principal fake.
func permPrincipalResponses() []fakeResponse {
	return []fakeResponse{
		{match: "WHERE  type IN ('S','U','G')", db: permDatabase, cols: 7, rows: [][]driver.Value{
			{"appreader", int64(5), "SQL_USER", "dbo", time.Time{}, time.Time{}, "INSTANCE"},
			{"appwriter", int64(6), "SQL_USER", "dbo", time.Time{}, time.Time{}, "INSTANCE"},
		}},
		{match: "WHERE  r.type = 'R'", db: permDatabase, cols: 5, rows: [][]driver.Value{
			{"db_datareader", int64(7), true, "dbo", nil},
			{"reporting", int64(8), false, "dbo", nil},
		}},
	}
}

// permEntryResponse answers whichever sys.database_permissions read the page
// under test runs — all three share a SELECT list and differ only in their
// WHERE, so one response covers each page in turn.
func permEntryResponse(match string, rows ...[]driver.Value) fakeResponse {
	return fakeResponse{match: match, db: permDatabase, cols: 5, rows: rows}
}

// matrixGrids returns the page's two grids in layout order: principals on top,
// that principal's permissions below.
func matrixGrids(t *testing.T, f *propsheet.Form) (*controls.DataGrid, *propsheet.GridRow) {
	t.Helper()
	var grids []*propsheet.GridRow
	for _, r := range f.Rows() {
		if gr, ok := r.(*propsheet.GridRow); ok {
			grids = append(grids, gr)
		}
	}
	if len(grids) != 2 {
		t.Fatalf("this page has %d grids, want the principal grid and the permission grid", len(grids))
	}
	return grids[0].Grid, grids[1]
}

// permScope is one of the four pages, named by what its writes are scoped to.
type permScope struct {
	name string
	// page builds the page under test.
	page func(sc *db.ServerConn) propPage
	// extra is the permission read the page runs, on top of the shared
	// principal lists.
	extra fakeResponse
	// permission is a name from this scope's catalog, and on is the ON clause
	// its statements must carry — empty for a scope that has none.
	permission string
	on         string
}

func permScopes() []permScope {
	return []permScope{{
		name:       "database",
		page:       func(sc *db.ServerConn) propPage { return pageDatabasePermissions(sc, permDatabase) },
		extra:      permEntryResponse("dp.class_desc = 'DATABASE'"),
		permission: "ALTER ANY SCHEMA",
	}, {
		name:       "schema",
		page:       func(sc *db.ServerConn) propPage { return pageSchemaPermissions(sc, permDatabase, "sales") },
		extra:      permEntryResponse("dp.class_desc = 'SCHEMA'"),
		permission: "EXECUTE",
		on:         "ON SCHEMA::[sales]",
	}, {
		name:       "table",
		page:       func(sc *db.ServerConn) propPage { return pageTablePermissions(sc, permDatabase, "sales", "Orders") },
		extra:      permEntryResponse("dp.major_id = OBJECT_ID(@p1)"),
		permission: "UPDATE",
		on:         "ON [sales].[Orders]",
	}}
}

// loadPermScope loads one scope's page and checks it actually listed its
// principals — an under-scripted fake yields an empty grid, on which every
// assertion below passes for the wrong reason.
func loadPermScope(t *testing.T, s permScope) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	responses := append([]fakeResponse{dbByNameResp(permDatabase, 5), s.extra}, permPrincipalResponses()...)
	sc, inst := newFakeConn(t, responses...)
	form, apply := loadPage(t, s.page(sc), inst)
	principals, _ := matrixGrids(t, form)
	if principals.Row(3) == nil {
		t.Fatal("the principal grid has fewer than four rows — the fake is under-scripted, not the page wrong")
	}
	return inst, apply, form
}

// TestPermissionsMatrixWritesAtItsOwnScope is the claim each of these pages
// makes and none of them could previously be caught getting wrong. The verb
// and permission are the matrix's, already covered; the ON clause is the
// page's, and it is the difference between granting UPDATE on one table and
// granting it on every table in the database.
func TestPermissionsMatrixWritesAtItsOwnScope(t *testing.T) {
	for _, s := range permScopes() {
		t.Run(s.name, func(t *testing.T) {
			inst, apply, form := loadPermScope(t, s)

			principals, perms := matrixGrids(t, form)
			selectGridRow(t, principals, 0, "reporting")
			cyclePermTo(t, perms, permRowIndex(t, perms, s.permission), "Grant")

			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			want := "GRANT " + s.permission + " TO [reporting]"
			if s.on != "" {
				want = "GRANT " + s.permission + " " + s.on + " TO [reporting]"
			}
			assertOneStatementIn(t, inst, permDatabase, want)
		})
	}
}

// TestPermissionsMatrixActsOnTheSelectedPrincipal. The page holds one edit
// slice per principal and applies all of them, so a page that wrote the grid's
// selection back onto the wrong principal grants a permission to somebody the
// user never picked — and the grid, which redraws from the page's own edit
// state, shows exactly what was asked for.
func TestPermissionsMatrixActsOnTheSelectedPrincipal(t *testing.T) {
	for _, s := range permScopes() {
		t.Run(s.name, func(t *testing.T) {
			inst, apply, form := loadPermScope(t, s)

			principals, perms := matrixGrids(t, form)
			// The second user, not the first, and a user rather than a role:
			// databasePermPrincipals concatenates the two lists, and an
			// off-by-one between them lands on the other kind.
			selectGridRow(t, principals, 0, "appwriter")
			cyclePermTo(t, perms, permRowIndex(t, perms, s.permission), "Deny")

			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			stmts := inst.StatementsIn(permDatabase)
			if len(stmts) != 1 {
				t.Fatalf("want exactly one statement, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
			}
			if !strings.Contains(stmts[0], "TO [appwriter]") {
				t.Errorf("wrote:\n%s\nwant it to name the selected principal", stmts[0])
			}
			if !strings.HasPrefix(stmts[0], "DENY ") {
				t.Errorf("wrote:\n%s\nwant a DENY", stmts[0])
			}
		})
	}
}

// TestPermissionsMatrixReadsStateOntoTheRightPrincipal. Every page fills the
// matrix from one flat list of entries keyed by principal *and* permission, so
// an entry attributed to the wrong principal shows another user's DENY as this
// one's — and Apply then leaves it there, because the page believes it is the
// server's own state.
func TestPermissionsMatrixReadsStateOntoTheRightPrincipal(t *testing.T) {
	s := permScopes()[0] // database scope; the fill is shared by all four
	s.extra = permEntryResponse("dp.class_desc = 'DATABASE'",
		[]driver.Value{"appreader", "SQL_USER", "dbo", s.permission, "DENY"},
	)
	_, _, form := loadPermScope(t, s)

	principals, perms := matrixGrids(t, form)
	selectGridRow(t, principals, 0, "appreader")
	if got := perms.Grid.Row(permRowIndex(t, perms, s.permission))[1]; got != "Deny" {
		t.Errorf("appreader's %s shows as %q, want Deny", s.permission, got)
	}

	selectGridRow(t, principals, 0, "appwriter")
	if got := perms.Grid.Row(permRowIndex(t, perms, s.permission))[1]; got != "(none)" {
		t.Errorf("appwriter's %s shows as %q: another principal's entry was read onto this row", s.permission, got)
	}
}

// TestPermissionsMatrixWritesNothingWhenUntouched. Browsing principals builds
// an edit slice for each one visited, and apply walks every slice it built — so
// a page that compared against the wrong baseline would reissue a GRANT for
// every permission of every principal the user merely looked at.
func TestPermissionsMatrixWritesNothingWhenUntouched(t *testing.T) {
	for _, s := range permScopes() {
		t.Run(s.name, func(t *testing.T) {
			inst, apply, form := loadPermScope(t, s)

			principals, _ := matrixGrids(t, form)
			selectGridRow(t, principals, 0, "reporting")
			selectGridRow(t, principals, 0, "appwriter")

			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			assertNoStatementsIn(t, inst, permDatabase)
		})
	}
}

// -- Server Properties > Permissions -----------------------------------------

// loginListResponse and serverRoleListResponse are the server page's two
// principal lists. Two of each again, and the login acted on is neither first
// nor last.
func loginListResponse() fakeResponse {
	return fakeResponse{match: "FROM sys.server_principals\n\tWHERE type IN ('S','U','G','E','X','C','K')", cols: 7, rows: [][]driver.Value{
		{"appuser", []byte{0x01}, "SQL_LOGIN", false, "master", time.Time{}, time.Time{}},
		{"otheruser", []byte{0x02}, "SQL_LOGIN", false, "master", time.Time{}, time.Time{}},
	}}
}

func serverRoleListResponse() fakeResponse {
	return fakeResponse{match: "WHERE r.type = 'R'", cols: 5, rows: [][]driver.Value{
		{"reportrole", int64(10), false, "sa", nil},
		{"sysadmin", int64(3), true, "sa", nil},
	}}
}

// The server page has no database to be pinned to and its own two principal
// lists, so it is scripted separately rather than bent into permScope.
func loadServerPermissionsPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t,
		fakeResponse{match: "sp.class_desc = 'SERVER'", cols: 5, rows: [][]driver.Value{
			{"appuser", "SQL_LOGIN", "sa", "ALTER ANY LOGIN", "GRANT"},
		}},
		loginListResponse(),
		serverRoleListResponse(),
	)
	form, apply := loadPage(t, pageServerPermissions(sc), inst)
	principals, _ := matrixGrids(t, form)
	if principals.Row(3) == nil {
		t.Fatal("the principal grid has fewer than four rows — the fake is under-scripted, not the page wrong")
	}
	return inst, apply, form
}

// TestServerPropertiesPermissionsGrantsToTheSelectedPrincipal. Server-scoped
// permissions are the ones worth getting wrong: CONTROL SERVER handed to the
// login below the one the user picked is a privilege escalation that looks
// right on the page that did it.
func TestServerPropertiesPermissionsGrantsToTheSelectedPrincipal(t *testing.T) {
	inst, apply, form := loadServerPermissionsPage(t)

	principals, perms := matrixGrids(t, form)
	selectGridRow(t, principals, 0, "reportrole")
	cyclePermTo(t, perms, permRowIndex(t, perms, "VIEW SERVER STATE"), "Grant")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "GRANT VIEW SERVER STATE TO [reportrole]")
}

// TestServerPropertiesPermissionsWritesNothingWhenUntouched.
func TestServerPropertiesPermissionsWritesNothingWhenUntouched(t *testing.T) {
	inst, apply, form := loadServerPermissionsPage(t)

	principals, _ := matrixGrids(t, form)
	selectGridRow(t, principals, 0, "reportrole")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
