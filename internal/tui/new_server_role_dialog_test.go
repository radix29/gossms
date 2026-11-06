package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// newServerRoleTestDialog builds the New Server Role pages the way show()
// would, minus the connection, from the principals newServerRolePrefetchFrom
// sorts.
func newServerRoleTestDialog(t *testing.T) *NewServerRoleDialog {
	t.Helper()
	sc, _ := newGConn(t)
	d := &NewServerRoleDialog{}
	d.sc = sc
	d.pages = []string{"General", "Members", "Securables"}
	d.forms = make([]*propsheet.Form, len(d.pages))
	d.applyFns = make([]propApply, len(d.pages))
	d.buildPages(newServerRolePrefetchFrom("", nsroleTestLogins(), nsroleTestRoles()))
	return d
}

// nsroleTestLogins includes a renamed sa: it is refused as a member by its SID.
func nsroleTestLogins() []*gosmo.Login {
	return []*gosmo.Login{
		{Name: "admin_renamed", SID: []byte{0x01}, LoginType: gosmo.PrincipalSQLLogin},
		{Name: `DOM\o]b`, SID: []byte{0x01, 0x05}, LoginType: gosmo.PrincipalWindowsLogin},
		{Name: "app_login", SID: []byte{0xAB}, LoginType: gosmo.PrincipalSQLLogin},
	}
}

func nsroleTestRoles() []*gosmo.ServerRole {
	return []*gosmo.ServerRole{
		{Name: "##MS_ServerStateReader##", IsFixedRole: true}, {Name: "public"},
		{Name: "sysadmin", IsFixedRole: true}, {Name: "auditors"},
	}
}

// runNewServerRole runs every page under Script Changes, in order.
func runNewServerRole(t *testing.T, d *NewServerRoleDialog) string {
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

// Left at (Default) the AUTHORIZATION clause is omitted, and untouched pages
// add nothing; a picked owner is named, quoted.
func TestNewServerRoleScriptsTheOwnerOnlyWhenPicked(t *testing.T) {
	d := newServerRoleTestDialog(t)
	editText(t, d.forms[0], "Role name", "  monitors  ")
	if got, want := runNewServerRole(t, d), "CREATE SERVER ROLE [monitors]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	d = newServerRoleTestDialog(t)
	editText(t, d.forms[0], "Role name", "monitors")
	editSelect(t, d.forms[0], "Owner", `DOM\o]b`)
	if got, want := runNewServerRole(t, d), `CREATE SERVER ROLE [monitors] AUTHORIZATION [DOM\o]]b]`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Members and Securables run after the CREATE and name the role as typed —
// read when apply runs, not when the pages were built.
func TestNewServerRoleLaterPagesNameTheNewRole(t *testing.T) {
	d := newServerRoleTestDialog(t)
	editSelect(t, d.forms[1], "Add member", "app_login")
	clickButton(t, d.forms[1], "Add")
	gr := permGrid(t, d.forms[2])
	cyclePermTo(t, gr, permRowIndex(t, gr, "VIEW SERVER STATE"), "Grant")
	editText(t, d.forms[0], "Role name", "mon]itors")

	got := runNewServerRole(t, d)
	var last int
	for _, want := range []string{
		"CREATE SERVER ROLE [mon]]itors]",
		"ALTER SERVER ROLE [mon]]itors] ADD MEMBER [app_login]",
		"GRANT VIEW SERVER STATE TO [mon]]itors]",
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

// The pickers leave out what SQL Server refuses (Msg 15405, major 17): public
// as owner; sa (by SID, so renamed too), public and every fixed role, ##MS_*##
// included, as a member. A fixed role is accepted as owner.
func TestNewServerRolePickersLeaveOutSpecialPrincipals(t *testing.T) {
	pf := newServerRolePrefetchFrom("", nsroleTestLogins(), nsroleTestRoles())
	if want := []string{"admin_renamed", `DOM\o]b`, "app_login", "##MS_ServerStateReader##", "sysadmin", "auditors"}; !slices.Equal(pf.owners, want) {
		t.Errorf("owners = %q, want %q", pf.owners, want)
	}
	if want := []string{`DOM\o]b`, "app_login", "auditors"}; !slices.Equal(pf.members, want) {
		t.Errorf("members = %q, want %q", pf.members, want)
	}
}

// A blank name and one any server principal already holds (a role cannot
// share a login's name, Msg 15025) are refused before the round trip.
func TestNewServerRolePreflight(t *testing.T) {
	d := newServerRoleTestDialog(t)
	editText(t, d.forms[0], "Role name", "   ")
	if err := d.preflight(); err == nil {
		t.Error("a blank name passed the preflight")
	}
	for _, taken := range []string{"auditors", "app_login", "public"} {
		editText(t, d.forms[0], "Role name", taken)
		if err := d.preflight(); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Errorf("%s passed the preflight: %v", taken, err)
		}
	}
}

// The Server Roles folder offers New Server Role between New Query and Refresh.
func TestServerRolesFolderMenu(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	folder := &explorerNode{data: nodeData{Type: NodeServerRoles, conn: sc}}
	if got, want := labelsOf(a.nodeMenuItems(folder)), []string{"New Query", "", "New Server Role...", "", refreshMenuLabel}; !slices.Equal(got, want) {
		t.Errorf("folder menu = %q, want %q", got, want)
	}
}

// TestNewServerRoleGateMatchesWhatTheServerAllowed is the probed CREATE SERVER
// ROLE column (majors 13 and 17): CREATE SERVER ROLE and ALTER ANY SERVER ROLE
// each permitted the CREATE; a login holding neither — securityadmin and ALTER
// ANY LOGIN included — reads both 0 and was refused Msg 15247.
func TestNewServerRoleGateMatchesWhatTheServerAllowed(t *testing.T) {
	all := []string{"CREATE SERVER ROLE", "ALTER ANY SERVER ROLE"}
	for _, tc := range []struct {
		name    string
		granted []string
		want    bool
	}{
		{"nothing, securityadmin or ALTER ANY LOGIN", nil, false},
		{"CREATE SERVER ROLE", []string{"CREATE SERVER ROLE"}, true},
		{"ALTER ANY SERVER ROLE", all, true},
	} {
		var denied []string
		for _, n := range all {
			if !slices.Contains(tc.granted, n) {
				denied = append(denied, n)
			}
		}
		sc, _ := newGConn(t, capabilityResponses(true, tc.granted, denied, nil, nil)...)
		sc.ProbeCapabilities(sc.Server.Context())
		folder := &explorerNode{data: nodeData{Type: NodeServerRoles, conn: sc}}
		var found bool
		for _, it := range (&App{}).nodeMenuItems(folder) {
			if it.Label != "New Server Role..." {
				continue
			}
			found = true
			if got := it.Enabled == nil || it.Enabled(); got != tc.want {
				t.Errorf("%s: New Server Role enabled = %v, want %v", tc.name, got, tc.want)
			}
		}
		if !found {
			t.Fatalf("%s: no New Server Role item", tc.name)
		}
	}
}
