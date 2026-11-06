package tui

import (
	"context"
	"database/sql/driver"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// R4: every role the Server Roles pages list used to fall back to "Low" when it
// had no entry — the SQL Server 2022 ##MS_*## roles (##MS_LoginManager## can
// alter any login) and every user-defined role, even one holding CONTROL SERVER.

// The ##MS_*## roles as SQL Server 17 lists them. Each needs a description and
// an impact of its own; a missing entry is the bug this pins.
var msFixedServerRoles = []string{
	"##MS_DatabaseConnector##", "##MS_DatabaseManager##", "##MS_DefinitionReader##",
	"##MS_LoginManager##", "##MS_PerformanceDefinitionReader##", "##MS_SecurityDefinitionReader##",
	"##MS_ServerPerformanceStateReader##", "##MS_ServerSecurityStateReader##",
	"##MS_ServerStateManager##", "##MS_ServerStateReader##",
}

func TestEveryMSFixedServerRoleIsRated(t *testing.T) {
	var roles []*gosmo.ServerRole
	for _, n := range msFixedServerRoles {
		roles = append(roles, &gosmo.ServerRole{Name: n, IsFixedRole: true})
	}
	ratings := serverRoleRatings(roles, nil)
	for _, n := range msFixedServerRoles {
		if ratings[n].impact == "" || ratings[n].description == "" {
			t.Errorf("%s has impact %q, description %q; want both", n, ratings[n].impact, ratings[n].description)
		}
	}
	// Pinned by name: the two that change who can do what on the instance.
	for name, want := range map[string]string{"##MS_LoginManager##": impactHigh, "##MS_DatabaseManager##": impactHigh} {
		if got := ratings[name].impact; got != want {
			t.Errorf("%s impact = %q, want %q", name, got, want)
		}
	}
}

func TestUserDefinedServerRolesAreRatedFromTheirGrants(t *testing.T) {
	roles := []*gosmo.ServerRole{
		{Name: "sysadmin", IsFixedRole: true, Members: []string{"r_in_sysadmin"}},
		{Name: "bulkadmin", IsFixedRole: true},
		{Name: "public"}, // is_fixed_role = 0 on the server
		{Name: "r_control", Members: []string{"r_nested"}},
		{Name: "r_nested"},
		{Name: "r_in_sysadmin"},
		{Name: "r_view", Members: []string{"r_cycle"}},
		{Name: "r_cycle", Members: []string{"r_view"}},
		{Name: "r_denied"},
		{Name: "r_nothing"},
		{Name: "r_unknown_perm"},
		{Name: "##MS_Future##", IsFixedRole: true},
	}
	perm := func(principal, permission string, state gosmo.PermissionState) *gosmo.ServerPermissionEntry {
		return &gosmo.ServerPermissionEntry{Principal: principal, PrincipalType: "SERVER_ROLE",
			Permission: gosmo.ServerPermission(permission), State: state}
	}
	perms := []*gosmo.ServerPermissionEntry{
		perm("r_control", "CONNECT SQL", "GRANT"),
		perm("r_control", "CONTROL SERVER", "GRANT"),
		perm("r_view", "VIEW SERVER STATE", "GRANT_WITH_GRANT_OPTION"),
		perm("r_denied", "CONTROL SERVER", "DENY"),
		perm("r_unknown_perm", "SOMETHING NEW", "GRANT"),
		perm("public", "VIEW ANY DATABASE", "GRANT"),
		// A login with the same name as a role is not the role.
		{Principal: "r_nothing", PrincipalType: "SQL_LOGIN", Permission: "CONTROL SERVER", State: "GRANT"},
	}
	ratings := serverRoleRatings(roles, perms)
	for _, tc := range []struct{ role, impact, description string }{
		{"r_control", impactCritical, "User-defined role; holds CONTROL SERVER"},
		{"r_nested", impactCritical, "User-defined role; member of r_control"},
		{"r_in_sysadmin", impactCritical, "User-defined role; member of sysadmin"},
		{"r_view", impactMedium, "User-defined role; holds VIEW SERVER STATE"},
		{"r_cycle", impactMedium, "User-defined role; member of r_view"},
		{"r_denied", "", "User-defined role"},
		{"r_nothing", "", "User-defined role"},
		{"r_unknown_perm", impactMedium, "User-defined role; holds SOMETHING NEW"},
		{"public", impactLow, "Every login is a member"},
		{"bulkadmin", impactMedium, "Run BULK INSERT statements"},
		{"##MS_Future##", "", ""},
	} {
		if got := ratings[tc.role]; got.impact != tc.impact || got.description != tc.description {
			t.Errorf("%s = {%q, %q}, want {%q, %q}", tc.role, got.impact, got.description, tc.impact, tc.description)
		}
	}
}

// With the grants unreadable a user-defined role is unrated, not Low.
func TestUserDefinedServerRoleIsUnratedWithoutItsGrants(t *testing.T) {
	ratings := serverRoleRatings([]*gosmo.ServerRole{{Name: "r_control"}}, nil)
	if got := ratings["r_control"].impact; got != "" {
		t.Errorf("impact = %q with no grants read, want \"\"", got)
	}
}

// customServerRoleResponses adds a user-defined role holding CONTROL SERVER to
// the fixed roles, and the grant that says so.
func customServerRoleResponses() []fakeResponse {
	roles := serverRolesResponse()
	roles.rows = append(roles.rows,
		[]driver.Value{"##MS_LoginManager##", int64(11), true, "sa", nil},
		[]driver.Value{"r_control", int64(300), false, "sa", nil})
	return []fakeResponse{roles, {match: "FROM   sys.server_permissions sp", cols: 5, rows: [][]driver.Value{
		{"r_control", "SERVER_ROLE", "sa", "CONTROL SERVER", "GRANT"},
	}}}
}

// Both Server Roles pages show the rating in the Impact cell of the row named
// for the role.
func TestServerRolesPagesShowTheRatedImpact(t *testing.T) {
	want := map[string]string{"r_control": impactCritical, "##MS_LoginManager##": impactHigh, "sysadmin": impactCritical}
	check := func(t *testing.T, text [][]string) {
		t.Helper()
		seen := 0
		for _, row := range text {
			if w, ok := want[row[0]]; ok {
				seen++
				if row[1] != w {
					t.Errorf("%s shows Impact %q, want %q", row[0], row[1], w)
				}
			}
		}
		if seen != len(want) {
			t.Fatalf("found %d of the %d roles checked in %q", seen, len(want), text)
		}
	}
	t.Run("Login Properties", func(t *testing.T) {
		name := "appuser"
		sc, inst := newGConn(t, append(loginResponses(), customServerRoleResponses()...)...)
		form, _ := loadPageG(t, pageLoginServerRoles(sc, &name), inst)
		check(t, toggleGrid(t, form).Text())
	})
	t.Run("New Login", func(t *testing.T) {
		// Through the real prefetch, so a dialog that never reads the grants fails here.
		sc, _ := newGConn(t, append(customServerRoleResponses(),
			fakeResponse{match: "FROM sys.server_principals sp", cols: 10},
			fakeResponse{match: "FROM sys.databases", cols: 11},
			fakeResponse{match: "c.certificate_id", db: "master"},
			fakeResponse{match: "k.asymmetric_key_id", db: "master"},
			fakeResponse{match: "FROM sys.syslanguages", cols: 3, rows: [][]driver.Value{{int64(0), "us_english", "English"}}},
		)...)
		pf, err := fetchNewLoginPrefetch(context.Background(), sc)
		if err != nil {
			t.Fatalf("prefetch: %v", err)
		}
		form, _ := buildNewLoginServerRolesPage(sc, pf, func() string { return "newlogin" })
		check(t, toggleGrid(t, form).Text())
	})
}
