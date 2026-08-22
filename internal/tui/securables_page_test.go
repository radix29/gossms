package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The database Securables page is the widest write surface in the app: one
// grid of securables of three different kinds, each routed to a different
// gosmo call by applySecurable, plus a column-permission editor that writes at
// a fourth scope. The routing is the part worth pinning — a schema securable
// sent down the object path grants on a table named after the schema, and the
// grid shows the state the user asked for either way, because it redraws from
// the page's own edit state and not from the server.
//
// securables_search_test.go already covers the Add picker's search. This is
// the load-and-apply half, which needed both gosmo.NewServer and a PropDialog
// the column editor's button could run through.

const secDatabase = "appdb"

// securableResponses scripts a principal holding permissions on three
// securables of three kinds — the database, a schema, and a table — so no one
// row's routing can stand in for another's, and the table (the one most of
// these act on) is last.
func securableResponses() []fakeResponse {
	return []fakeResponse{
		dbByNameResp(secDatabase, 5),
		{match: "dp.class_desc IN ('DATABASE','SCHEMA','OBJECT_OR_COLUMN')", db: secDatabase, cols: 6, rows: [][]driver.Value{
			{"DATABASE", "CONNECT", "GRANT", "", "", ""},
			{"SCHEMA", "SELECT", "GRANT", "sales", "", ""},
			{"OBJECT_OR_COLUMN", "SELECT", "GRANT", "sales", "Orders", "USER_TABLE"},
		}},
		{match: "dp.minor_id > 0", db: secDatabase, cols: 9, rows: nil},
		{match: "x.qualified LIKE", db: secDatabase, cols: 4, rows: nil},
	}
}

// securableGrids returns the page's three grids in layout order: securables,
// that securable's permissions, and its columns.
func securableGrids(t *testing.T, f *propsheet.Form) (secs *controls.DataGrid, perms, cols *propsheet.GridRow) {
	t.Helper()
	var grids []*propsheet.GridRow
	for _, r := range f.Rows() {
		if gr, ok := r.(*propsheet.GridRow); ok {
			grids = append(grids, gr)
		}
	}
	if len(grids) != 3 {
		t.Fatalf("this page has %d grids, want securables, permissions and columns", len(grids))
	}
	return grids[0].Grid, grids[1], grids[2]
}

func loadSecurablesPage(t *testing.T) (*fakeInstance, *App, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t, securableResponses()...)
	d, app := newFakeDialog(t)
	principal := "appwriter"
	form, apply := loadPage(t, pageDatabasePrincipalSecurables(d, sc, secDatabase, &principal), inst)
	secs, _, _ := securableGrids(t, form)
	if secs.Row(2) == nil {
		t.Fatal("the securables grid has fewer than three rows — the fake is under-scripted, not the page wrong")
	}
	return inst, app, apply, form
}

// TestSecurablesRouteEachKindToItsOwnScope is applySecurable's switch, seen
// from the page. The three securables carry three different ON clauses, and
// picking the wrong one writes a valid statement about the wrong object.
func TestSecurablesRouteEachKindToItsOwnScope(t *testing.T) {
	for _, tc := range []struct{ row, permission, want string }{
		{"[sales].[Orders]", "UPDATE", "GRANT UPDATE ON [sales].[Orders] TO [appwriter]"},
		{"[sales]", "EXECUTE", "GRANT EXECUTE ON SCHEMA::[sales] TO [appwriter]"},
		{"(database)", "ALTER ANY SCHEMA", "GRANT ALTER ANY SCHEMA TO [appwriter]"},
	} {
		t.Run(tc.row, func(t *testing.T) {
			inst, _, apply, form := loadSecurablesPage(t)

			secs, perms, _ := securableGrids(t, form)
			selectGridRow(t, secs, 0, tc.row)
			cyclePermTo(t, perms, permRowIndex(t, perms, tc.permission), "Grant")

			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			assertOneStatementIn(t, inst, secDatabase, tc.want)
		})
	}
}

// TestSecurablesRevokeCarriesTheObjectItWasGrantedOn is the destructive
// direction: the page's own read said this principal holds SELECT on
// sales.Orders, and taking it away has to name that object. A revoke aimed at
// the schema instead removes access to every table in it.
func TestSecurablesRevokeCarriesTheObjectItWasGrantedOn(t *testing.T) {
	inst, _, apply, form := loadSecurablesPage(t)

	secs, perms, _ := securableGrids(t, form)
	selectGridRow(t, secs, 0, "[sales].[Orders]")
	cyclePermTo(t, perms, permRowIndex(t, perms, "SELECT"), "(none)")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, secDatabase, "REVOKE SELECT ON [sales].[Orders] FROM [appwriter]")
}

// TestSecurablesColumnGrantNamesTheColumnAndItsObject drives the column
// editor: Load Columns fetches on a background goroutine through the dialog,
// and each loaded row carries the securable and permission it was loaded for,
// so a cycled cell must write against those rather than against whatever is
// selected when apply runs.
func TestSecurablesColumnGrantNamesTheColumnAndItsObject(t *testing.T) {
	sc, inst := newFakeConn(t, append(securableResponses(),
		// ObjectColumns, not Table.Columns — the editor works on views too.
		fakeResponse{match: "OBJECT_ID(@p1)", db: secDatabase, cols: 17, rows: [][]driver.Value{
			{"OrderID", int64(1), "int", int64(4), int64(10), int64(0), false, false, false, "", "", "", false, "", int64(0), int64(0), false},
			{"CustomerID", int64(2), "int", int64(4), int64(10), int64(0), false, false, false, "", "", "", false, "", int64(0), int64(0), false},
			{"Total", int64(3), "money", int64(8), int64(19), int64(4), false, false, false, "", "", "", false, "", int64(0), int64(0), false},
		}},
	)...)
	d, app := newFakeDialog(t)
	principal := "appwriter"
	form, apply := loadPage(t, pageDatabasePrincipalSecurables(d, sc, secDatabase, &principal), inst)

	secs, _, cols := securableGrids(t, form)
	selectGridRow(t, secs, 0, "[sales].[Orders]")
	chooseSelect(t, form, "Column permission", "UPDATE")
	clickButton(t, form, "Load Columns")
	drainDialog(t, app, func() bool { return cols.Grid.Row(0) != nil }, "the column list to arrive")

	// The third column, not the first: the grid is read back index-parallel
	// against the column list it was built from.
	cyclePermTo(t, cols, permRowIndex(t, cols, "Total"), "Grant")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, secDatabase, "GRANT UPDATE ([Total]) ON [sales].[Orders] TO [appwriter]")
}

// TestSecurablesWriteNothingWhenUntouched. Browsing securables builds an edit
// slice for each one visited and apply walks every slice built, so a page
// comparing against the wrong baseline reissues a GRANT for every permission
// of every securable the user merely clicked on.
func TestSecurablesWriteNothingWhenUntouched(t *testing.T) {
	inst, _, apply, form := loadSecurablesPage(t)

	secs, _, _ := securableGrids(t, form)
	selectGridRow(t, secs, 0, "[sales]")
	selectGridRow(t, secs, 0, "[sales].[Orders]")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.StatementsIn(secDatabase); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
