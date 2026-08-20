package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// serverPermissionsResponse scripts sys.server_permissions with one explicit
// entry for appuser — a GRANT of ALTER ANY LOGIN — so both the "already has
// a state" and "has none" paths are on the page at once.
func serverPermissionsResponse() fakeResponse {
	return fakeResponse{match: "sp.class_desc = 'SERVER'", cols: 5, rows: [][]driver.Value{
		{"appuser", "SQL_LOGIN", "sa", "ALTER ANY LOGIN", "GRANT"},
		{"otheruser", "SQL_LOGIN", "sa", "CONTROL SERVER", "GRANT"},
	}}
}

// gridRowFor finds the page's grid row, and permRow the grid row index whose
// first column is perm — by name, because the page maps grid rows onto its
// edit slice through a filtered `visible` slice, and an index agreed on by
// both the test and the page would agree about a misalignment too.
func permGrid(t *testing.T, f *propsheet.Form) *propsheet.GridRow {
	t.Helper()
	for _, r := range f.Rows() {
		if gr, ok := r.(*propsheet.GridRow); ok {
			return gr
		}
	}
	t.Fatal("no grid row on this page")
	return nil
}

func permRowIndex(t *testing.T, gr *propsheet.GridRow, perm string) int {
	t.Helper()
	for i := 0; ; i++ {
		row := gr.Grid.Row(i)
		if row == nil {
			t.Fatalf("no grid row for permission %q", perm)
		}
		if row[0] == perm {
			return i
		}
	}
}

// cyclePermTo activates the State cell until it reads want, the way the user
// cycles it with Space. Activating is capped so a cycle that never reaches
// want fails here rather than spinning.
func cyclePermTo(t *testing.T, gr *propsheet.GridRow, row int, want string) {
	t.Helper()
	for range 5 {
		if gr.Grid.Row(row)[1] == want {
			return
		}
		gr.Grid.OnActivateCell(row, 1)
	}
	t.Fatalf("cycling row %d never reached %q; it stopped at %q", row, want, gr.Grid.Row(row)[1])
}

// TestServerPermissionsWriteTheVerbTheCellShows is the matrix's central
// claim. A cell carries three meanings — Grant, Deny, and the absence of
// either — and they are three different statements against the same
// permission. A wrong one is close to undetectable from the UI: the grid
// re-renders from the page's own edit state, not from the server, so a DENY
// written where a GRANT was asked for shows as Grant until the page is
// reopened.
func TestServerPermissionsWriteTheVerbTheCellShows(t *testing.T) {
	for _, tc := range []struct{ show, verb string }{
		{"Grant", "GRANT"},
		{"Deny", "DENY"},
		{"Grant With Grant", "GRANT"},
	} {
		t.Run(tc.show, func(t *testing.T) {
			sc, inst := newFakeConn(t, serverPermissionsResponse())
			form, apply := loadPage(t, pagePrincipalServerPermissions(sc, "appuser"), inst)

			gr := permGrid(t, form)
			cyclePermTo(t, gr, permRowIndex(t, gr, "VIEW SERVER STATE"), tc.show)

			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			stmts := inst.Statements()
			if len(stmts) != 1 {
				t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
			}
			if !strings.Contains(stmts[0], tc.verb+" VIEW SERVER STATE TO [appuser]") {
				t.Errorf("%q wrote:\n%s", tc.show, stmts[0])
			}
			// Grant With Grant is a GRANT plus WITH GRANT OPTION, and the
			// option is the whole difference between the two states — a
			// transition that dropped it would look identical in the grid.
			hasOption := strings.Contains(stmts[0], "WITH GRANT OPTION")
			if want := tc.show == "Grant With Grant"; hasOption != want {
				t.Errorf("%q: WITH GRANT OPTION present=%v, want %v:\n%s", tc.show, hasOption, want, stmts[0])
			}
		})
	}
}

// TestServerPermissionsReadTheServerStateOntoTheRightRow: the page builds one
// edit per name in gosmo's catalog and fills states from a map keyed by
// permission, so the one permission the scripted principal actually holds has
// to land on its own row and nowhere else.
func TestServerPermissionsReadTheServerStateOntoTheRightRow(t *testing.T) {
	sc, inst := newFakeConn(t, serverPermissionsResponse())
	form, _ := loadPage(t, pagePrincipalServerPermissions(sc, "appuser"), inst)

	gr := permGrid(t, form)
	for i := 0; ; i++ {
		row := gr.Grid.Row(i)
		if row == nil {
			break
		}
		want := "(none)"
		if row[0] == "ALTER ANY LOGIN" {
			want = "Grant"
		}
		if row[1] != want {
			t.Errorf("permission %q shows as %q, want %q", row[0], row[1], want)
		}
	}
	// CONTROL SERVER is granted, but to somebody else. A page that filled its
	// state map without checking the principal would show every permission any
	// login holds as this login's — and then leave them there.
	if got := gr.Grid.Row(permRowIndex(t, gr, "CONTROL SERVER"))[1]; got != "(none)" {
		t.Errorf("CONTROL SERVER shows as %q: another principal's grant was read onto this page", got)
	}
}

// TestServerPermissionsRevokeCarriesCascade covers the transition that
// existed only in permTransition's unit tests before: dropping a permission
// the server holds WITH GRANT OPTION has to REVOKE ... CASCADE, because SQL
// Server refuses a plain REVOKE against a grantable permission that has been
// passed on.
func TestServerPermissionsRevokeCarriesCascade(t *testing.T) {
	resp := serverPermissionsResponse()
	resp.rows[0][4] = "GRANT_WITH_GRANT_OPTION"
	sc, inst := newFakeConn(t, resp)
	form, apply := loadPage(t, pagePrincipalServerPermissions(sc, "appuser"), inst)

	gr := permGrid(t, form)
	cyclePermTo(t, gr, permRowIndex(t, gr, "ALTER ANY LOGIN"), "(none)")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "REVOKE") || !strings.Contains(stmts[0], "CASCADE") {
		t.Errorf("dropping a WITH GRANT OPTION permission wrote:\n%s", stmts[0])
	}
}

// TestServerPermissionsFilterDoesNotMisdirectAnEdit is the index hazard the
// filter box introduces. OnActivateCell indexes `visible`, a filtered subset,
// while the write loop walks the full catalog — so the two are only in step
// because rowsFor rebuilds `visible` as it builds the rows. Toggling a cell
// with a filter applied must still change the permission on that row.
func TestServerPermissionsFilterDoesNotMisdirectAnEdit(t *testing.T) {
	sc, inst := newFakeConn(t, serverPermissionsResponse())
	form, apply := loadPage(t, pagePrincipalServerPermissions(sc, "appuser"), inst)

	typeInto(t, form, "Filter permissions", "VIEW")
	gr := permGrid(t, form)
	// The filter has to have actually narrowed the grid, or this proves
	// nothing about the mapping.
	if gr.Grid.Row(0) == nil {
		t.Fatal("the filter emptied the grid")
	}
	for i := 0; ; i++ {
		row := gr.Grid.Row(i)
		if row == nil {
			break
		}
		if !strings.Contains(row[0], "VIEW") {
			t.Fatalf("the filter left %q in the grid", row[0])
		}
	}

	target := gr.Grid.Row(0)[0]
	cyclePermTo(t, gr, 0, "Deny")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "DENY "+target+" TO [appuser]") {
		t.Errorf("editing the first row under a filter denied the wrong permission.\nrow was %q, statement:\n%s", target, stmts[0])
	}
}

// TestServerPermissionsWriteNothingWhenUntouched: the catalog is every
// server permission SQL Server has, and opening the page must issue none of
// them. A page that wrote each row's current state on OK would rewrite the
// entire server permission set of whichever principal an admin looked at.
func TestServerPermissionsWriteNothingWhenUntouched(t *testing.T) {
	sc, inst := newFakeConn(t, serverPermissionsResponse())
	_, apply := loadPage(t, pagePrincipalServerPermissions(sc, "appuser"), inst)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched Securables page wrote %d of the %d catalog permissions: %q",
			len(stmts), len(gosmo.ServerPermissionNames()), stmts)
	}
}
