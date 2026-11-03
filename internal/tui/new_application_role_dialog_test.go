package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// newApplicationRoleTestDialog builds the New Application Role pages the way
// show() would, minus the connection. The schemas are read through gosmo so
// Owned Schemas writes into a real database.
func newApplicationRoleTestDialog(t *testing.T) *NewApplicationRoleDialog {
	t.Helper()
	sc, _ := newFakeConn(t, dbByNameResp("appdb", 5),
		fakeResponse{match: "FROM   sys.schemas s", cols: 3, rows: [][]driver.Value{
			{"dbo", int64(1), "dbo"}, {"INFORMATION_SCHEMA", int64(3), "INFORMATION_SCHEMA"},
			{"sales", int64(5), "dbo"}, {"sys", int64(4), "sys"},
		}})
	schemas, err := sc.Server.DatabaseRef("appdb").Schemas(context.Background())
	if err != nil {
		t.Fatalf("Schemas: %v", err)
	}
	d := &NewApplicationRoleDialog{}
	d.dbName = "appdb"
	d.sc = sc
	d.pages = []string{"General", "Owned Schemas", "Securables", "Extended Properties"}
	d.forms = make([]*propsheet.Form, len(d.pages))
	d.applyFns = make([]propApply, len(d.pages))
	pf := newApplicationRolePrefetchFrom("", nroleTestUsers(), nroleTestRoles(), nroleTestAppRoles(), schemas)
	pf.securables = []securable{{Type: gosmo.SecurableTable, Schema: "sales", Name: "Orders"}}
	d.buildPages(pf)
	return d
}

// runNewApplicationRole runs every page under Script Changes, in order.
func runNewApplicationRole(t *testing.T, d *NewApplicationRoleDialog) string {
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

// Left at (Default) the DEFAULT_SCHEMA clause is omitted, and untouched pages
// add nothing. The typed password reaches gosmo, which scripts a placeholder.
func TestNewApplicationRoleScriptsTheSchemaOnlyWhenPicked(t *testing.T) {
	d := newApplicationRoleTestDialog(t)
	editText(t, d.forms[0], "Role name", "  payroll  ")
	editText(t, d.forms[0], "Password", "S3cret-pw")
	editText(t, d.forms[0], "Confirm password", "S3cret-pw")
	want := nroleUse + "CREATE APPLICATION ROLE [payroll] WITH PASSWORD = N'" + gosmo.PasswordPlaceholder + "'"
	if got := runNewApplicationRole(t, d); got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	d = newApplicationRoleTestDialog(t)
	editText(t, d.forms[0], "Role name", "payroll")
	editText(t, d.forms[0], "Password", "S3cret-pw")
	editText(t, d.forms[0], "Confirm password", "S3cret-pw")
	editSelect(t, d.forms[0], "Default schema", "sales")
	got := runNewApplicationRole(t, d)
	if want := want + ", DEFAULT_SCHEMA = [sales]"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.Contains(got, "S3cret-pw") {
		t.Errorf("the password reached the script in clear:\n%s", got)
	}
}

// Owned Schemas, Securables and Extended Properties run after the CREATE and
// name the role as typed — read when apply runs, not when the pages were built.
func TestNewApplicationRoleLaterPagesNameTheNewRole(t *testing.T) {
	d := newApplicationRoleTestDialog(t)
	toggleByName(t, toggleGrid(t, d.forms[1]), "sales", 0)
	editSelectUntracked(t, d.forms[2], "Add securable", "[sales].[Orders]")
	clickButton(t, d.forms[2], "Add")
	_, perms, _ := securableGrids(t, d.forms[2])
	cyclePermTo(t, perms, permRowIndex(t, perms, "SELECT"), "Grant")
	editText(t, d.forms[3], "Name", "MS_Description")
	editText(t, d.forms[3], "Value", "Payroll app")
	clickButton(t, d.forms[3], "Add")
	editText(t, d.forms[0], "Password", "S3cret-pw")
	editText(t, d.forms[0], "Confirm password", "S3cret-pw")
	editText(t, d.forms[0], "Role name", "pay]roll")

	got := runNewApplicationRole(t, d)
	var last int
	for _, want := range []string{
		"CREATE APPLICATION ROLE [pay]]roll]",
		"ALTER AUTHORIZATION ON SCHEMA::[sales] TO [pay]]roll]",
		"GRANT SELECT ON [sales].[Orders] TO [pay]]roll]",
		"@level0type = N'USER', @level0name = N'pay]roll'",
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

// A blank name, one any principal already holds (Msg 15023), no password and a
// confirmation that differs are refused before the round trip.
func TestNewApplicationRolePreflight(t *testing.T) {
	d := newApplicationRoleTestDialog(t)
	editText(t, d.forms[0], "Password", "pw")
	editText(t, d.forms[0], "Confirm password", "pw")
	editText(t, d.forms[0], "Role name", "   ")
	if err := d.preflight(); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("a blank name: %v", err)
	}
	for _, taken := range []string{"readers", "guest", "PAYROLL_APP"} {
		editText(t, d.forms[0], "Role name", taken)
		if err := d.preflight(); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Errorf("%s passed the preflight: %v", taken, err)
		}
	}
	editText(t, d.forms[0], "Role name", "fresh")
	if err := d.preflight(); err != nil {
		t.Errorf("a free name and matching password: %v", err)
	}
	editText(t, d.forms[0], "Confirm password", "px")
	if err := d.preflight(); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Errorf("a mismatched confirmation: %v", err)
	}
	d = newApplicationRoleTestDialog(t)
	editText(t, d.forms[0], "Role name", "fresh")
	if err := d.preflight(); err == nil || !strings.Contains(err.Error(), "needs a password") {
		t.Errorf("no password: %v", err)
	}
}

// Security > Roles holds the two folders SSMS shows, and Application Roles
// offers New Application Role between New Query and Refresh.
func TestRolesFolderHoldsDatabaseAndApplicationRoles(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	roles := &explorerNode{data: nodeData{Type: NodeRoles, DBName: "appdb", conn: sc}}
	kids, err := loadRolesChildren(loaderCtx{ctx: context.Background(), sc: sc}, roles)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, k := range kids {
		got = append(got, k.label)
		if k.data.DBName != "appdb" {
			t.Errorf("%s carries database %q", k.label, k.data.DBName)
		}
	}
	if want := []string{"Database Roles", "Application Roles"}; !slices.Equal(got, want) {
		t.Errorf("Roles children = %q, want %q", got, want)
	}
	if kids[0].data.Type != NodeDatabaseRoles || kids[1].data.Type != NodeApplicationRoles {
		t.Errorf("Roles children types = %v, %v", kids[0].data.Type, kids[1].data.Type)
	}

	folder := &explorerNode{data: nodeData{Type: NodeApplicationRoles, DBName: "appdb", conn: sc}}
	if got, want := labelsOf(a.nodeMenuItems(folder)), []string{"New Query", "", "New Application Role...", "", refreshMenuLabel}; !slices.Equal(got, want) {
		t.Errorf("folder menu = %q, want %q", got, want)
	}
}

// The Application Roles folder lists sys.database_principals type 'A' rows,
// each a leaf in its database.
func TestApplicationRolesFolderListsTheRoles(t *testing.T) {
	sc, _ := newFakeConn(t, dbByNameResp("appdb", 5),
		fakeResponse{match: "WHERE  type = 'A'", cols: 5, rows: [][]driver.Value{
			{"billing", "dbo", int64(7), principalEpoch, principalEpoch},
			{"payroll", nil, int64(8), principalEpoch, principalEpoch},
		}})
	folder := &explorerNode{data: nodeData{Type: NodeApplicationRoles, DBName: "appdb", conn: sc}}
	kids, err := loadApplicationRolesChildren(loaderCtx{ctx: context.Background(), sc: sc}, folder)
	if err != nil {
		t.Fatal(err)
	}
	if got := labelsOfNodes(kids); !slices.Equal(got, []string{"billing", "payroll"}) {
		t.Fatalf("children = %q", got)
	}
	for _, k := range kids {
		if k.data.Type != NodeApplicationRole || k.data.Name != k.label || k.data.DBName != "appdb" {
			t.Errorf("child %q = %+v", k.label, k.data)
		}
	}
}

// TestNewApplicationRoleGateMatchesWhatTheServerAllowed is the probed column
// (majors 13 and 17, gate.AlterAnyApplicationRole): ALTER ANY APPLICATION ROLE
// (alone or through db_securityadmin), ALTER and CONTROL on the database each
// create; a user holding none — ALTER ANY ROLE, db_accessadmin and db_ddladmin
// included — reads every one 0. The same set gates Delete and Rename on a role.
func TestNewApplicationRoleGateMatchesWhatTheServerAllowed(t *testing.T) {
	all := []string{"ALTER ANY APPLICATION ROLE", "ALTER", "CONTROL"}
	for _, tc := range []struct {
		name    string
		granted []string
		want    bool
	}{
		{"nothing, ALTER ANY ROLE, db_accessadmin or db_ddladmin", nil, false},
		{"ALTER ANY APPLICATION ROLE, or db_securityadmin", []string{"ALTER ANY APPLICATION ROLE"}, true},
		{"ALTER on the database", []string{"ALTER ANY APPLICATION ROLE", "ALTER"}, true},
		{"CONTROL on the database", all, true},
	} {
		var denied []string
		for _, n := range all {
			if !slices.Contains(tc.granted, n) {
				denied = append(denied, n)
			}
		}
		sc, _ := newFakeConn(t, capabilityResponses(true, nil, nil, tc.granted, denied)...)
		sc.ProbeCapabilities(sc.Server.Context())
		sc.DatabaseCapabilities(context.Background(), "appdb")
		folder := &explorerNode{data: nodeData{Type: NodeApplicationRoles, DBName: "appdb", conn: sc}}
		var found bool
		for _, it := range (&App{}).nodeMenuItems(folder) {
			if it.Label != "New Application Role..." {
				continue
			}
			found = true
			if got := it.Enabled == nil || it.Enabled(); got != tc.want {
				t.Errorf("%s: New Application Role enabled = %v, want %v", tc.name, got, tc.want)
			}
		}
		if !found {
			t.Fatalf("%s: no New Application Role item", tc.name)
		}
		if got := gate.AllowsOn(sc, "appdb", "", "payroll", objectOpRights(NodeApplicationRole)...); got != tc.want {
			t.Errorf("%s: Delete/Rename allowed = %v, want %v", tc.name, got, tc.want)
		}
	}
}
