package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"
)

// Three pages change who is in a role, and all three come down to one
// statement with two names in it: ALTER ROLE <role> ADD|DROP MEMBER <member>.
// Transposing the two is the classic mistake here and it is invisible in the
// UI, because the page only ever shows the member list. So is picking the
// wrong verb — an Add that drops takes an administrator out of sysadmin.
//
// The Database Role and Server Role pages share buildMembershipForm, whose own
// behaviour (the hint, the queue-and-revert) is already covered by
// membership_page_test.go against fake callbacks. What is untested there is
// the wiring underneath: which gosmo call each page hands it, and with the
// arguments in which order. That is what these drive.

func membershipResponses() []fakeResponse {
	return []fakeResponse{
		dbByNameResp("appdb", 5),

		// The database's roles, its role members, and its users (the addable
		// candidates). Roles first: the role list's own query embeds a
		// sys.database_role_members subquery, so behind the members answer it
		// is served two columns for a five-column scan.
		{match: "r.type = 'R'", db: "appdb", cols: 5, rows: [][]driver.Value{
			{"public", int64(0), false, "dbo", "alice"},
			{"db_owner", int64(1), true, "dbo", nil},
			{"salesrole", int64(2), false, "dbo", "alice, bob"},
			// A non-member role that is not the first row of the grid: a page
			// that ticked the *first* role whatever row was clicked would
			// still look right if every test used the first one.
			{"db_datawriter", int64(3), true, "dbo", nil},
		}},
		{match: "FROM   sys.database_role_members rm", db: "appdb", cols: 2, rows: [][]driver.Value{
			{"alice", "SQL_USER"}, {"bob", "SQL_USER"},
		}},
		{match: "name, principal_id, type_desc, default_schema_name", db: "appdb", cols: 7, rows: [][]driver.Value{
			{"alice", int64(5), "SQL_USER", "dbo", time.Now(), time.Now(), "INSTANCE"},
			{"bob", int64(6), "SQL_USER", "dbo", time.Now(), time.Now(), "INSTANCE"},
			{"carol", int64(7), "SQL_USER", "dbo", time.Now(), time.Now(), "INSTANCE"},
		}},

		// The server's roles, its role members, and its logins — same
		// ordering rule as the database half above.
		{match: "FROM sys.server_principals r", cols: 5, rows: [][]driver.Value{
			{"public", int64(0), true, "sa", nil},
			{"sysadmin", int64(1), true, "sa", "appuser, svcuser"},
			{"securityadmin", int64(2), true, "sa", nil},
		}},
		{match: "FROM   sys.server_role_members rm", cols: 2, rows: [][]driver.Value{
			{"appuser", "SQL_LOGIN"}, {"svcuser", "SQL_LOGIN"},
		}},
		{match: "name, sid, type_desc, is_disabled", cols: 7, rows: [][]driver.Value{
			{"appuser", []byte{1}, "SQL_LOGIN", false, "master", time.Now(), time.Now()},
			{"svcuser", []byte{2}, "SQL_LOGIN", false, "master", time.Now(), time.Now()},
			{"newlogin", []byte{3}, "SQL_LOGIN", false, "master", time.Now(), time.Now()},
		}},
	}
}

// TestDatabaseRoleMembersAddsTheNameSelected. The dropdown carries the
// principal and the page carries the role; a page that passed them the other
// way round builds a statement that is still valid T-SQL.
func TestDatabaseRoleMembersAddsTheNameSelected(t *testing.T) {
	role := "salesrole"
	sc, inst := newFakeConn(t, membershipResponses()...)
	form, apply := loadPage(t, pageRoleMembers(sc, "appdb", &role), inst)

	chooseSelect(t, form, "Add member", "carol")
	clickButton(t, form, "Add")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, "appdb", "ALTER ROLE [salesrole] ADD MEMBER [carol]")
}

// TestDatabaseRoleMembersRemovesTheRowSelected is the destructive direction,
// and the one addressed by grid position rather than by name: Remove reads the
// *visible* list, which is the edit list minus everything already pending
// removal.
func TestDatabaseRoleMembersRemovesTheRowSelected(t *testing.T) {
	role := "salesrole"
	sc, inst := newFakeConn(t, membershipResponses()...)
	form, apply := loadPage(t, pageRoleMembers(sc, "appdb", &role), inst)

	selectGridRow(t, plainGrid(t, form), 0, "bob")
	clickButton(t, form, "Remove")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, "appdb", "ALTER ROLE [salesrole] DROP MEMBER [bob]")
}

// TestDatabaseRoleMembersRemovesBothOfTwo is the divergence case. The visible
// list and the edit list are identical until the first removal, so a Remove
// that indexed the wrong one still takes the right member out on its own —
// and takes the wrong one out on the second click.
func TestDatabaseRoleMembersRemovesBothOfTwo(t *testing.T) {
	role := "salesrole"
	sc, inst := newFakeConn(t, membershipResponses()...)
	form, apply := loadPage(t, pageRoleMembers(sc, "appdb", &role), inst)

	g := plainGrid(t, form)
	selectGridRow(t, g, 0, "alice")
	clickButton(t, form, "Remove")
	selectGridRow(t, g, 0, "bob")
	clickButton(t, form, "Remove")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 2 {
		t.Fatalf("want two DROP MEMBER statements, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	joined := strings.Join(stmts, "\n")
	for _, name := range []string{"[alice]", "[bob]"} {
		if !strings.Contains(joined, "DROP MEMBER "+name) {
			t.Errorf("wrote:\n%s\nwant a DROP MEMBER %s", joined, name)
		}
	}
}

// TestServerRoleMembersWriteTheServerScopedStatement. Same page shape, a
// different gosmo call: ALTER SERVER ROLE, not ALTER ROLE. sysadmin membership
// is the most powerful thing this application grants, so the verb and the
// scope both get pinned.
func TestServerRoleMembersWriteTheServerScopedStatement(t *testing.T) {
	role := "sysadmin"

	t.Run("Add", func(t *testing.T) {
		sc, inst := newFakeConn(t, membershipResponses()...)
		form, apply := loadPage(t, pageServerRoleMembers(sc, &role), inst)
		chooseSelect(t, form, "Add member", "newlogin")
		clickButton(t, form, "Add")
		if err := apply(context.Background()); err != nil {
			t.Fatalf("apply: %v", err)
		}
		assertOneStatement(t, inst, "ALTER SERVER ROLE [sysadmin] ADD MEMBER [newlogin]")
	})

	t.Run("Remove", func(t *testing.T) {
		sc, inst := newFakeConn(t, membershipResponses()...)
		form, apply := loadPage(t, pageServerRoleMembers(sc, &role), inst)
		selectGridRow(t, plainGrid(t, form), 0, "svcuser")
		clickButton(t, form, "Remove")
		if err := apply(context.Background()); err != nil {
			t.Fatalf("apply: %v", err)
		}
		assertOneStatement(t, inst, "ALTER SERVER ROLE [sysadmin] DROP MEMBER [svcuser]")
	})
}

// TestServerRoleMembersOffersNoCandidateWhoIsAlreadyOne. The candidate list is
// built by filtering the login list against the current membership, and it is
// the only thing stopping the page from issuing an ADD MEMBER that the server
// rejects.
func TestServerRoleMembersOffersNoCandidateWhoIsAlreadyOne(t *testing.T) {
	role := "sysadmin"
	sc, inst := newFakeConn(t, membershipResponses()...)
	form, _ := loadPage(t, pageServerRoleMembers(sc, &role), inst)

	items := selectRow(t, form, "Add member").Items()
	for _, already := range []string{"appuser", "svcuser"} {
		if slices.Contains(items, already) {
			t.Errorf("Add member offers %q, who is already a member: %q", already, items)
		}
	}
	// The role must not be offered itself, either.
	if slices.Contains(items, "sysadmin") {
		t.Errorf("Add member offers the role itself: %q", items)
	}
	if !slices.Contains(items, "newlogin") {
		t.Errorf("Add member does not offer %q, who is not a member: %q", "newlogin", items)
	}
}

// TestRoleMembershipPagesWriteNothingWhenUntouched.
func TestRoleMembershipPagesWriteNothingWhenUntouched(t *testing.T) {
	role := "salesrole"
	srole := "sysadmin"
	user := "alice"

	sc, inst := newFakeConn(t, membershipResponses()...)
	for _, p := range []propPage{
		pageRoleMembers(sc, "appdb", &role),
		pageServerRoleMembers(sc, &srole),
		pageUserMembership(sc, "appdb", &user),
	} {
		_, apply := loadPage(t, p, inst)
		if err := apply(context.Background()); err != nil {
			t.Fatalf("%s apply: %v", p.title, err)
		}
		if stmts := inst.Statements(); len(stmts) != 0 {
			t.Fatalf("%s wrote with nothing touched:\n%s", p.title, strings.Join(stmts, "\n"))
		}
	}
}

// TestUserMembershipTogglesTheRoleTheRowIsLabelled. This page is a toggle grid
// read back index-parallel against its own role slice, so a row offset by one
// grants db_owner from the checkbox labelled db_datareader. public is excluded
// from the grid because ALTER ROLE public ADD MEMBER is a syntax error, and
// that exclusion is what makes the two lists differ in length — which is
// exactly how an index-parallel read-back goes wrong.
func TestUserMembershipTogglesTheRoleTheRowIsLabelled(t *testing.T) {
	user := "alice"
	sc, inst := newFakeConn(t, membershipResponses()...)
	form, apply := loadPage(t, pageUserMembership(sc, "appdb", &user), inst)

	tg := toggleGrid(t, form)
	for _, row := range tg.Text() {
		if row[0] == "public" {
			t.Fatal("public is in the membership grid; ALTER ROLE public ADD MEMBER is a syntax error")
		}
	}
	toggleByName(t, tg, "db_datawriter", 0)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, "appdb", "ALTER ROLE [db_datawriter] ADD MEMBER [alice]")
}

// TestUserMembershipUntickingRemovesMembership is the same grid in the
// direction that takes access away. alice is scripted as a member of
// salesrole, so this is a real removal rather than a no-op.
func TestUserMembershipUntickingRemovesMembership(t *testing.T) {
	user := "alice"
	sc, inst := newFakeConn(t, membershipResponses()...)
	form, apply := loadPage(t, pageUserMembership(sc, "appdb", &user), inst)

	tg := toggleGrid(t, form)
	// The load half: the row alice is a member of is the one that shows
	// ticked, and no other.
	for i, row := range tg.Text() {
		want := row[0] == "salesrole"
		if got := tg.Values()[i][0]; got != want {
			t.Errorf("%s shows membership %v, want %v", row[0], got, want)
		}
	}
	toggleByName(t, tg, "salesrole", 0)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, "appdb", "ALTER ROLE [salesrole] DROP MEMBER [alice]")
}
