package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"
)

// loginResponses scripts an enabled SQL login with no CONNECT SQL permission
// entry of its own and no active sessions — the four reads the Status page
// makes, plus the server-role list the Server Roles page needs.
func loginResponses() []fakeResponse {
	return []fakeResponse{
		{match: "type IN ('S','U','G') AND name", cols: 7, rows: [][]driver.Value{{
			"appuser", []byte{0x01, 0x02}, "SQL_LOGIN", false, "master", time.Now(), time.Now(),
		}}},
		{match: "LOGINPROPERTY(@p1, 'IsLocked')", cols: 12, rows: [][]driver.Value{{
			int64(0), int64(0), int64(0), true, false,
			time.Now(), nil, int64(0), nil, "us_english", "", "",
		}}},
		{match: "sys.dm_exec_sessions s", cols: 14, rows: nil},
	}
}

// TestLoginStatusConnectPermissionWritesTheStateItShows is the three-way
// index-to-statement table on this page, and the reason it is worth pinning
// by name: Grant, Deny and Default are three *different* verbs on the same
// permission, the row records only an index, and apply switches on that index.
// Swapping two arms leaves the page working in the UI while a Deny grants.
//
// DENY on CONNECT SQL is also how a login is locked out without being
// disabled, so getting it backwards is the kind of mistake that is noticed by
// the person who can no longer connect.
func TestLoginStatusConnectPermissionWritesTheStateItShows(t *testing.T) {
	for _, tc := range []struct{ option, want string }{
		{"Grant", "GRANT CONNECT SQL TO [appuser]"},
		{"Deny", "DENY CONNECT SQL TO [appuser]"},
		// REVOKE takes FROM, not TO — a difference gosmo owns, restated here
		// only so the three arms are compared against real statements.
		{"Default", "REVOKE CONNECT SQL FROM [appuser]"},
	} {
		t.Run(tc.option, func(t *testing.T) {
			name := "appuser"
			sc, inst := newFakeConn(t, loginResponses()...)
			form, apply := loadPage(t, pageLoginStatus(sc, &name), inst)

			// The scripted login has no CONNECT SQL entry, so the page opens
			// on Default — move off it first for the case under test to be a
			// real edit in every direction.
			const connectLabel = "Permission to connect to database engine"
			if tc.option == "Default" {
				radioRow(t, form, connectLabel).SetSelected(0)
			}
			editRadio(t, form, connectLabel, tc.option)

			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			stmts := inst.Statements()
			if len(stmts) != 1 {
				t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
			}
			// gosmo prefixes every server-scoped grant with USE master, which
			// SQL Server requires; the verb is what this test is about.
			if !strings.Contains(stmts[0], tc.want) {
				t.Errorf("%q wrote:\n%s\nwant it to contain: %s", tc.option, stmts[0], tc.want)
			}
		})
	}
}

// TestLoginStatusEnableAndDisable pins the other two-arm switch on the page.
// Disabling a login is the most direct thing this dialog can do to lock
// somebody out, and Enabled/Disabled is an index pair like every other.
func TestLoginStatusEnableAndDisable(t *testing.T) {
	for _, tc := range []struct{ option, want string }{
		{"Disabled", "ALTER LOGIN [appuser] DISABLE"},
		{"Enabled", "ALTER LOGIN [appuser] ENABLE"},
	} {
		t.Run(tc.option, func(t *testing.T) {
			name := "appuser"
			sc, inst := newFakeConn(t, loginResponses()...)
			form, apply := loadPage(t, pageLoginStatus(sc, &name), inst)

			// The scripted login is enabled, so selecting Enabled is not a
			// change — set the row the other way first, without dirtying it,
			// so the edit under test is a real one in both directions.
			if tc.option == "Enabled" {
				radioRow(t, form, "Login").SetSelected(1)
			}
			editRadio(t, form, "Login", tc.option)

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

// TestLoginStatusWritesNothingWhenUntouched: the page has two editable rows
// and opening it must not touch either. A page that revoked CONNECT SQL just
// for being opened would lock out every login an admin inspected.
func TestLoginStatusWritesNothingWhenUntouched(t *testing.T) {
	name := "appuser"
	sc, inst := newFakeConn(t, loginResponses()...)
	_, apply := loadPage(t, pageLoginStatus(sc, &name), inst)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched Status page wrote %d statements: %q", len(stmts), stmts)
	}
}

// TestLoginStatusReadsTheConnectStateBackAsTheServerReportsIt covers the load
// half of the same table. GRANT_WITH_GRANT_OPTION has to read as Grant: the
// server reports it for a login that can pass the permission on, and falling
// through to the Default arm would show a granted login as unset — and then
// write a REVOKE the moment anything else on the page changed.
func TestLoginStatusReadsTheConnectStateBackAsTheServerReportsIt(t *testing.T) {
	for _, tc := range []struct{ state, want string }{
		{"GRANT", "Grant"},
		{"GRANT_WITH_GRANT_OPTION", "Grant"},
		{"DENY", "Deny"},
		{"", "Default"},
	} {
		t.Run(tc.state+"/"+tc.want, func(t *testing.T) {
			resp := loginResponses()
			resp[1].rows[0][11] = tc.state
			name := "appuser"
			sc, inst := newFakeConn(t, resp...)
			form, _ := loadPage(t, pageLoginStatus(sc, &name), inst)

			row := radioRow(t, form, "Permission to connect to database engine")
			if got := row.Options()[row.Selected()]; got != tc.want {
				t.Errorf("server state %q showed as %q, want %q", tc.state, got, tc.want)
			}
			if row.Dirty() {
				t.Error("the row is dirty straight out of load: apply would write it unasked")
			}
		})
	}
}

// serverRolesResponse scripts the fixed server roles in the order
// ServerRolesContext returns them (ORDER BY name), with appuser already a
// member of dbcreator and of nothing else.
func serverRolesResponse() fakeResponse {
	member := func(names ...string) driver.Value {
		if len(names) == 0 {
			return nil
		}
		return strings.Join(names, ", ")
	}
	rows := [][]driver.Value{
		{"bulkadmin", int64(3), true, "sa", member()},
		{"dbcreator", int64(4), true, "sa", member("appuser", "otheruser")},
		{"diskadmin", int64(5), true, "sa", member()},
		{"processadmin", int64(6), true, "sa", member()},
		{"public", int64(2), true, "sa", member()},
		{"securityadmin", int64(7), true, "sa", member()},
		{"serveradmin", int64(8), true, "sa", member()},
		{"setupadmin", int64(9), true, "sa", member()},
		{"sysadmin", int64(10), true, "sa", member("sa")},
	}
	return fakeResponse{match: "WHERE r.type = 'R'", cols: 5, rows: rows}
}

// TestServerRolesGrantsTheRoleTheRowIsNamedFor is the index-alignment test.
//
// The page builds its grid from ServerRolesContext's slice and reads it back
// with `for i, v := range rolesGrid.Values()` against `roles[i]` — so the
// checkbox and the role it grants are related by nothing but position. Ticking
// the row labelled sysadmin has to grant *sysadmin*; if the two lists ever
// slip, this dialog hands out unrestricted control of the instance while
// showing the user a tick next to something else. That is why this toggles by
// name and asserts on the role in the statement.
func TestServerRolesGrantsTheRoleTheRowIsNamedFor(t *testing.T) {
	for _, role := range []string{"sysadmin", "securityadmin", "bulkadmin", "public"} {
		t.Run(role, func(t *testing.T) {
			name := "appuser"
			sc, inst := newFakeConn(t, append(loginResponses(), serverRolesResponse())...)
			form, apply := loadPage(t, pageLoginServerRoles(sc, &name), inst)

			toggleByName(t, toggleGrid(t, form), role, 0)

			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			stmts := inst.Statements()
			if len(stmts) != 1 {
				t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
			}
			if !strings.Contains(stmts[0], "["+role+"]") {
				t.Errorf("ticking %q wrote:\n%s", role, stmts[0])
			}
			if !strings.Contains(stmts[0], "ADD MEMBER") {
				t.Errorf("ticking a role did not add a member:\n%s", stmts[0])
			}
		})
	}
}

// TestServerRolesUntickingRemovesMembership covers the other direction, on the
// one role the scripted login is actually in. The page decides add-vs-remove
// by comparing against the membership it loaded, so a row that starts ticked
// is the only place that comparison is exercised.
func TestServerRolesUntickingRemovesMembership(t *testing.T) {
	name := "appuser"
	sc, inst := newFakeConn(t, append(loginResponses(), serverRolesResponse())...)
	form, apply := loadPage(t, pageLoginServerRoles(sc, &name), inst)

	grid := toggleGrid(t, form)
	// The membership read has to have landed on the right row for the untick
	// to mean anything — a page that showed every role unticked would "pass"
	// a remove test by doing an add.
	for i, row := range grid.Text() {
		if row[0] == "dbcreator" && !grid.Values()[i][0] {
			t.Fatal("dbcreator is not shown as a membership: the members column was not read back onto the row")
		}
	}
	toggleByName(t, grid, "dbcreator", 0)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "DROP MEMBER") || !strings.Contains(stmts[0], "[dbcreator]") {
		t.Errorf("unticking dbcreator wrote:\n%s", stmts[0])
	}
}

// TestServerRolesMembershipIsNotSubstringMatched: "otheruser" is a member of
// dbcreator and "appuser" is a substring of nothing here, but the members
// column arrives as one comma-joined string, so a membership test done by
// strings.Contains rather than by element would tick roles the login is not
// in — and then silently remove it from them on the next Apply.
func TestServerRolesMembershipIsNotSubstringMatched(t *testing.T) {
	name := "user"
	sc, inst := newFakeConn(t, append(loginResponses(), serverRolesResponse())...)
	form, _ := loadPage(t, pageLoginServerRoles(sc, &name), inst)

	grid := toggleGrid(t, form)
	for i, row := range grid.Text() {
		if grid.Values()[i][0] {
			t.Errorf("login %q is shown as a member of %q, but is not one", name, row[0])
		}
	}
}

// TestServerRolesWritesNothingWhenUntouched: nine rows, no edit, no write.
func TestServerRolesWritesNothingWhenUntouched(t *testing.T) {
	name := "appuser"
	sc, inst := newFakeConn(t, append(loginResponses(), serverRolesResponse())...)
	_, apply := loadPage(t, pageLoginServerRoles(sc, &name), inst)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched Server Roles page wrote %d statements: %q", len(stmts), stmts)
	}
}
