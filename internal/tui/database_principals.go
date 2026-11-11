package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
)

// databasePrincipals is a database's users, database roles and application
// roles, read in one round trip (gosmo's Database.Principals) instead of
// Users, Roles and ApplicationRoles' three. Every grantee and owner picker
// draws from it, so which kinds each offers is decided here, once:
//
//   - grantees, for a database-scope Permissions page: all three.
//   - owners, for an AUTHORIZATION or ALTER AUTHORIZATION picker: all three,
//     minus a dialog's exclusions.
//
// The families stay reachable (Users, Roles, ApplicationRoles) for the pages
// that sort them into more than one picker — New Database Role's members,
// New User's role list.
type databasePrincipals struct {
	*gosmo.DatabasePrincipals
}

// loadDatabasePrincipals reads every user, role and application role of d.
func loadDatabasePrincipals(ctx context.Context, d *gosmo.Database) (databasePrincipals, error) {
	p, err := d.Principals(ctx)
	if err != nil {
		return databasePrincipals{}, err
	}
	return databasePrincipals{p}, nil
}

// grantees is the principal list a database-scope Permissions page offers.
func (p databasePrincipals) grantees() []permPrincipal {
	return databasePermPrincipals(p.Users, p.Roles, p.ApplicationRoles)
}

// names is every principal's name, users then roles then application roles:
// the one namespace they share (Msg 15023).
func (p databasePrincipals) names() []string {
	return principalNames(p.Users, p.Roles, p.ApplicationRoles)
}

// owners is names less those exclude holds (nil excludes nothing).
func (p databasePrincipals) owners(exclude *nameSet) []string {
	var out []string
	for _, n := range p.names() {
		if !exclude.Has(n) {
			out = append(out, n)
		}
	}
	return out
}
