package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/tuikit/propsheet"
)

// newRoleTestDialog builds the New Database Role pages the way show() would,
// minus the connection, from the principals newRolePrefetchFrom sorts. The
// schemas are read through gosmo so Owned Schemas writes into a real database.
func newRoleTestDialog(t *testing.T, responses ...fakeResponse) *NewRoleDialog {
	t.Helper()
	responses = append(responses, fakeResponse{match: "FROM   sys.schemas s", cols: 3, rows: [][]driver.Value{
		{"dbo", int64(1), "dbo"}, {"INFORMATION_SCHEMA", int64(3), "INFORMATION_SCHEMA"},
		{"sales", int64(5), "dbo"}, {"sys", int64(4), "sys"},
	}})
	sc, _ := newGConn(t, responses...)
	schemas, err := sc.Server.DatabaseRef("appdb").Schemas(context.Background())
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	d := &NewRoleDialog{}
	d.dbName = "appdb"
	d.sc = sc
	d.pages = []string{"General", "Owned Schemas", "Members", "Securables", "Extended Properties"}
	d.forms = make([]*propsheet.Form, len(d.pages))
	d.applyFns = make([]propApply, len(d.pages))
	pf := newRolePrefetchFrom("", nroleTestUsers(), nroleTestRoles(), nroleTestAppRoles(), schemas)
	pf.securables = []securable{{Type: gosmo.SecurableTable, Schema: "sales", Name: "Orders"}}
	d.buildPages(pf)
	return d
}

func nroleTestUsers() []*gosmo.User {
	var us []*gosmo.User
	for _, n := range []string{"dbo", "guest", "INFORMATION_SCHEMA", "sys", "app]user"} {
		us = append(us, &gosmo.User{Name: n, UserType: gosmo.PrincipalSQLUser})
	}
	return us
}

func nroleTestRoles() []*gosmo.DatabaseRole {
	return []*gosmo.DatabaseRole{
		{Name: "public"}, {Name: "db_owner", IsFixedRole: true},
		{Name: "db_datareader", IsFixedRole: true}, {Name: "readers"},
	}
}

// nroleTestAppRoles is one application role: it takes a name in the
// principal namespace but is offered by no picker.
func nroleTestAppRoles() []*gosmo.ApplicationRole {
	return []*gosmo.ApplicationRole{{Name: "payroll_app"}}
}

// runNewRole runs every page under Script Changes, in order.
func runNewRole(t *testing.T, d *NewRoleDialog) string {
	t.Helper()
	for _, f := range d.forms {
		f.Commit()
	}
	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	ctx, script := gosmo.WithScript(context.Background())
	for i, apply := range d.applyFns {
		if err := apply(ctx); err != nil {
			t.Fatalf("apply page %d: %v", i, err)
		}
	}
	return strings.Join(script.Statements(), "\n")
}

const nroleUse = "USE [appdb];\nGO\n"

// Left at (Default) the AUTHORIZATION clause is omitted, and untouched pages
// add nothing; a picked owner is named, quoted.
func TestNewRoleScriptsTheOwnerOnlyWhenPicked(t *testing.T) {
	d := newRoleTestDialog(t, dbByNameResp("appdb", 5))
	editText(t, d.forms[0], "Role name", "  auditors  ")
	if got, want := runNewRole(t, d), nroleUse+"CREATE ROLE [auditors]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	d = newRoleTestDialog(t, dbByNameResp("appdb", 5))
	editText(t, d.forms[0], "Role name", "auditors")
	editSelect(t, d.forms[0], "Owner", "app]user")
	if got, want := runNewRole(t, d), nroleUse+"CREATE ROLE [auditors] AUTHORIZATION [app]]user]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Owned Schemas, Members, Securables and Extended Properties run after the
// CREATE and name the role as typed — read when apply runs, not when the pages
// were built.
func TestNewRoleLaterPagesNameTheNewRole(t *testing.T) {
	d := newRoleTestDialog(t, dbByNameResp("appdb", 5))
	toggleByName(t, toggleGrid(t, d.forms[1]), "sales", 0)
	editSelect(t, d.forms[2], "Add member", "readers")
	clickButton(t, d.forms[2], "Add")
	editSelectUntracked(t, d.forms[3], "Add securable", "[sales].[Orders]")
	clickButton(t, d.forms[3], "Add")
	_, perms, _ := securableGrids(t, d.forms[3])
	cyclePermTo(t, perms, permRowIndex(t, perms, "SELECT"), "Grant")
	editText(t, d.forms[4], "Name", "MS_Description")
	editText(t, d.forms[4], "Value", "Audit readers")
	clickButton(t, d.forms[4], "Add")
	editText(t, d.forms[0], "Role name", "aud]itors")

	got := runNewRole(t, d)
	var last int
	for _, want := range []string{
		"CREATE ROLE [aud]]itors]",
		"ALTER AUTHORIZATION ON SCHEMA::[sales] TO [aud]]itors]",
		"ALTER ROLE [aud]]itors] ADD MEMBER [readers]",
		"GRANT SELECT ON [sales].[Orders] TO [aud]]itors]",
		"@level0type = N'USER', @level0name = N'aud]itors'",
	} {
		i := strings.Index(got, want)
		if i < 0 {
			t.Errorf("script:\n%s\nwant it to contain: %s", got, want)
			continue
		}
		if i < last {
			t.Errorf("script:\n%s\n%s is out of page order", got, want)
		}
		last = i
	}
}

// The pickers leave out what SQL Server refuses (Msg 15405, major 17): sys,
// INFORMATION_SCHEMA and public as owner or member, and dbo and every fixed
// role as a member. guest is accepted as both. An application role may own
// the role (probed on 13 and 17) but is no member.
func TestNewRolePickersLeaveOutSpecialPrincipals(t *testing.T) {
	pf := newRolePrefetchFrom("", nroleTestUsers(), nroleTestRoles(), nroleTestAppRoles(), nil)
	if want := []string{"dbo", "guest", "app]user", "db_owner", "db_datareader", "readers", "payroll_app"}; !slices.Equal(pf.owners, want) {
		t.Errorf("owners = %q, want %q", pf.owners, want)
	}
	if want := []string{"guest", "app]user", "readers"}; !slices.Equal(pf.members, want) {
		t.Errorf("members = %q, want %q", pf.members, want)
	}
}

// A blank name and one any principal already holds (a role cannot share a
// user's or an application role's name, Msg 15023) are refused before the
// round trip.
func TestNewRolePreflight(t *testing.T) {
	d := newRoleTestDialog(t)
	editText(t, d.forms[0], "Role name", "   ")
	if err := d.preflight(); err == nil {
		t.Error("a blank name passed the preflight")
	}
	for _, taken := range []string{"readers", "guest", "payroll_app"} {
		editText(t, d.forms[0], "Role name", taken)
		if err := d.preflight(); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Errorf("%s passed the preflight: %v", taken, err)
		}
	}
}

// The Roles folder offers New Database Role between New Query and Refresh.
func TestDatabaseRolesFolderMenu(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	folder := &explorerNode{data: nodeData{Type: NodeDatabaseRoles, DBName: "appdb", conn: sc}}
	if got, want := labelsOf(a.nodeMenuItems(folder)), []string{"New Query", "", "New Database Role...", "", refreshMenuLabel}; !slices.Equal(got, want) {
		t.Errorf("folder menu = %q, want %q", got, want)
	}
}

// TestNewRoleGateMatchesWhatTheServerAllowed is the probed CREATE ROLE column
// (majors 13 and 17): each permitting right reads CREATE ROLE 1 through
// HAS_PERMS_BY_NAME, and a user holding none (db_ddladmin included) reads
// every one 0.
func TestNewRoleGateMatchesWhatTheServerAllowed(t *testing.T) {
	all := []string{"CREATE ROLE", "ALTER ANY ROLE", "ALTER", "CONTROL"}
	for _, tc := range []struct {
		name    string
		granted []string
		want    bool
	}{
		{"nothing, or db_ddladmin", nil, false},
		{"CREATE ROLE", []string{"CREATE ROLE"}, true},
		{"ALTER ANY ROLE, or db_securityadmin", []string{"CREATE ROLE", "ALTER ANY ROLE"}, true},
		{"ALTER on the database", []string{"CREATE ROLE", "ALTER ANY ROLE", "ALTER"}, true},
		{"CONTROL on the database", all, true},
	} {
		var denied []string
		for _, n := range all {
			if !slices.Contains(tc.granted, n) {
				denied = append(denied, n)
			}
		}
		sc, _ := newGConn(t, capabilityResponses(true, nil, nil, tc.granted, denied)...)
		sc.ProbeCapabilities(sc.Server.Context())
		sc.DatabaseCapabilities(context.Background(), "appdb")
		folder := &explorerNode{data: nodeData{Type: NodeDatabaseRoles, DBName: "appdb", conn: sc}}
		var found bool
		for _, it := range (&App{}).nodeMenuItems(folder) {
			if it.Label != "New Database Role..." {
				continue
			}
			found = true
			if got := it.Enabled == nil || it.Enabled(); got != tc.want {
				t.Errorf("%s: New Database Role enabled = %v, want %v", tc.name, got, tc.want)
			}
		}
		if !found {
			t.Fatalf("%s: no New Database Role item", tc.name)
		}
	}
}
