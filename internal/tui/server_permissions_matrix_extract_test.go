package tui

import (
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// The three database-scope Permissions pages built their principal list and
// (for two of them) their entry list inline, in blocks that were identical
// character for character. These pin what the extracted helpers produce, since
// the pages themselves can only be exercised against a live server.

func TestDatabasePermPrincipalsListsUsersThenRoles(t *testing.T) {
	users := []*gosmo.User{{Name: "app", UserType: "SQL_USER"}, {Name: "dbo", UserType: "SQL_USER"}}
	roles := []*gosmo.DatabaseRole{{Name: "db_owner"}, {Name: "reader"}}

	got := databasePermPrincipals(users, roles)
	want := []permPrincipal{
		{Name: "app", Type: "SQL_USER"},
		{Name: "dbo", Type: "SQL_USER"},
		{Name: "db_owner", Type: "DATABASE_ROLE"},
		{Name: "reader", Type: "DATABASE_ROLE"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d principals, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("principal %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// Users come first and roles second, and a role's type is filled in rather
// than read off the role: the grid groups by Type, so a swapped order or a
// blank type silently reshapes the page.
func TestDatabasePermPrincipalsWithNoRoles(t *testing.T) {
	if got := databasePermPrincipals(nil, nil); len(got) != 0 {
		t.Errorf("got %+v, want an empty list", got)
	}
}

func TestObjectPermEntriesConvertsTheNamedStringTypes(t *testing.T) {
	perms := []*gosmo.PermissionEntry{{
		Principal: "app", PrincipalType: "SQL_USER", Grantor: "dbo",
		Permission: gosmo.ObjectPermission("SELECT"), State: gosmo.PermissionState("GRANT"),
	}}
	got := objectPermEntries(perms)
	want := permEntry{Principal: "app", PrincipalType: "SQL_USER", Grantor: "dbo", Permission: "SELECT", State: "GRANT"}
	if len(got) != 1 || got[0] != want {
		t.Errorf("got %+v, want [%+v]", got, want)
	}
}
