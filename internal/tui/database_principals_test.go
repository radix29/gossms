package tui

import (
	"context"
	"slices"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

func testDatabasePrincipals() databasePrincipals {
	return databasePrincipals{&gosmo.DatabasePrincipals{
		Users:            []*gosmo.User{{Name: "appuser", UserType: gosmo.PrincipalSQLUser}, {Name: "dbo", UserType: gosmo.PrincipalSQLUser}},
		Roles:            []*gosmo.DatabaseRole{{Name: "public"}, {Name: "readers"}},
		ApplicationRoles: []*gosmo.ApplicationRole{{Name: "payroll_app"}},
	}}
}

// TestDatabasePrincipalsOwnersExcludeOnlyWhatIsNamed: owners is every family,
// users then roles then application roles, less the exclusions — and the
// exclusion compares under the database's collation, as every name check does.
func TestDatabasePrincipalsOwnersExcludeOnlyWhatIsNamed(t *testing.T) {
	p := testDatabasePrincipals()
	if got, want := p.owners(nil), []string{"appuser", "dbo", "public", "readers", "payroll_app"}; !slices.Equal(got, want) {
		t.Errorf("owners(nil) = %v, want %v", got, want)
	}
	if got, want := p.names(), p.owners(nil); !slices.Equal(got, want) {
		t.Errorf("names() = %v, want owners(nil) = %v", got, want)
	}
	exclude := newNameSet("SQL_Latin1_General_CP1_CI_AS", "PUBLIC", "Payroll_App")
	if got, want := p.owners(exclude), []string{"appuser", "dbo", "readers"}; !slices.Equal(got, want) {
		t.Errorf("owners(exclude) = %v, want %v", got, want)
	}
}

// TestLoadDatabasePrincipalsIsOneRead: the families come back sorted into
// their own lists, each typed as the grantee grid shows it, from the one
// Principals read and none of the three family reads.
func TestLoadDatabasePrincipalsIsOneRead(t *testing.T) {
	sc, inst := newGConn(t, dbByNameResp(principalDatabase, 5), databasePrincipalsResponse())
	d, err := sc.Server.DatabaseByName(context.Background(), principalDatabase)
	if err != nil {
		t.Fatal(err)
	}
	p, err := loadDatabasePrincipals(context.Background(), d)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Users) != 3 || len(p.Roles) != 5 || len(p.ApplicationRoles) != 1 {
		t.Fatalf("got %d users, %d roles, %d application roles; want 3, 5, 1",
			len(p.Users), len(p.Roles), len(p.ApplicationRoles))
	}
	if p.ApplicationRoles[0].Name != principalAppRoleOwner {
		t.Errorf("application role = %q, want %q", p.ApplicationRoles[0].Name, principalAppRoleOwner)
	}
	// Every New dialog and Permissions page takes its grantee list from here,
	// so this is where an application role must not drop out (review A1).
	if !slices.Contains(p.grantees(), permPrincipal{Name: principalAppRoleOwner, Type: gosmo.PrincipalApplicationRole}) {
		t.Errorf("grantees() = %+v, want it to offer application role %s", p.grantees(), principalAppRoleOwner)
	}
	for _, g := range p.grantees() {
		if g.Name == principalRole && g.Type != gosmo.PrincipalDatabaseRole {
			t.Errorf("%s typed %q, want %q", g.Name, g.Type, gosmo.PrincipalDatabaseRole)
		}
		if g.Name == principalUser && g.Type != gosmo.PrincipalSQLUser {
			t.Errorf("%s typed %q, want %q", g.Name, g.Type, gosmo.PrincipalSQLUser)
		}
	}
	for _, family := range []string{usersListRead, "FROM   sys.database_principals r", "WHERE  type = 'A'"} {
		if reads := inst.Reads(family); len(reads) != 0 {
			t.Errorf("a family read ran beside Principals:\n%s", reads[0])
		}
	}
	if reads := inst.Reads(principalsListRead); len(reads) != 1 {
		t.Errorf("Principals ran %d times, want once", len(reads))
	}
}
