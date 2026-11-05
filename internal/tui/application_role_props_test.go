package tui

import (
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

const principalAppRole = "payroll"

func loadApplicationRoleGeneralPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form, *string) {
	t.Helper()
	responses := []fakeResponse{
		dbByNameResp(principalDatabase, 5),
		{match: "WHERE  type = 'A'", arg: principalAppRole, cols: 5, rows: [][]driver.Value{
			{principalAppRole, "dbo", int64(9), principalEpoch, principalEpoch},
		}},
	}
	responses = append(responses, databaseSchemaResponses()...)
	responses = append(responses, principalPermissionResponses()...)
	sc, inst := newFakeConn(t, responses...)
	name := principalAppRole
	form, apply := loadPage(t, pageApplicationRoleGeneral(sc, principalDatabase, &name), inst)
	return inst, apply, form, &name
}

// TestApplicationRoleGeneralWritesInOrderAndRenamesLast. The schema and the
// password changes name the role, so the rename is the last write of the run,
// under the old name, and the shared name cell follows it. The typed password is
// what reaches the server; Script Changes' placeholder is gosmo's
// (secret_redaction_test.go there).
func TestApplicationRoleGeneralWritesInOrderAndRenamesLast(t *testing.T) {
	inst, apply, form, name := loadApplicationRoleGeneralPage(t)
	editSelect(t, form, "Default schema", principalSchema)
	editText(t, form, "Password", "N3w-pass")
	editText(t, form, "Confirm password", "N3w-pass")
	editText(t, form, "Role name", "pay]roll2")
	if err := form.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn(principalDatabase)
	want := []string{
		"ALTER APPLICATION ROLE [payroll] WITH DEFAULT_SCHEMA = [sales]",
		"ALTER APPLICATION ROLE [payroll] WITH PASSWORD = N'N3w-pass'",
		"ALTER APPLICATION ROLE [payroll] WITH NAME = [pay]]roll2]",
	}
	if len(stmts) != len(want) {
		t.Fatalf("want %d statements, got %d:\n%s", len(want), len(stmts), strings.Join(stmts, "\n"))
	}
	for i := range want {
		if !strings.Contains(stmts[i], want[i]) {
			t.Errorf("statement %d:\n%s\nwant it to contain: %s", i, stmts[i], want[i])
		}
	}
	if *name != "pay]roll2" {
		t.Errorf("the shared name cell is still %q after the rename", *name)
	}
}

// An untouched page writes nothing — blank password fields keep the password —
// and a password whose confirmation differs is refused before apply.
func TestApplicationRoleGeneralUntouchedAndMismatch(t *testing.T) {
	inst, apply, form, _ := loadApplicationRoleGeneralPage(t)
	if got := staticValue(t, form, "Explicit securables"); got != "2" {
		t.Errorf("Explicit securables = %q, want 2", got)
	}
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertNoStatementsIn(t, inst, principalDatabase)

	editText(t, form, "Password", "one")
	editText(t, form, "Confirm password", "two")
	if err := form.Validate(); err == nil || !strings.Contains(err.Error(), "do not match") {
		t.Errorf("Validate with a mismatched confirmation: %v", err)
	}
}

// An application role node offers Script Application Role as with CREATE,
// DROP and DROP And CREATE — no ALTER, which would only rename or reset the
// password — alongside Delete and Properties.
func TestApplicationRoleScriptMenu(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	leaf := &explorerNode{data: nodeData{Type: NodeApplicationRole, DBName: "appdb", Name: "payroll", conn: sc}}
	got := labelsOf(a.contextMenuItemsForNode(leaf))
	for _, want := range []string{"Script Application Role as", "Delete...", "Properties..."} {
		if !slices.Contains(got, want) {
			t.Errorf("leaf menu %q has no %q", got, want)
		}
	}
	items := (&App{}).scriptMenuItems(opNode(NodeApplicationRole, "", "payroll", "appdb"))
	if len(items) == 0 {
		t.Fatal("an application role offers no Script item")
	}
	if got, want := labelsOf(items[0].Sub), []string{"CREATE To", "DROP To", "DROP And CREATE To"}; !slices.Equal(got, want) {
		t.Errorf("script verbs = %v, want %v", got, want)
	}
}

// principalNames lists users, then roles, then application roles: an
// application role may own every securable an owner picker offers.
func TestPrincipalNamesOffersApplicationRoles(t *testing.T) {
	got := principalNames(nroleTestUsers()[:1], nroleTestRoles()[:1], nroleTestAppRoles())
	want := []string{nroleTestUsers()[0].Name, nroleTestRoles()[0].Name, "payroll_app"}
	if !slices.Equal(got, want) {
		t.Errorf("principalNames = %q, want %q", got, want)
	}
	if got := principalNames(nroleTestUsers()[:1], nil, nil); len(got) != 1 {
		t.Errorf("nil roles and application roles = %q, want the user alone", got)
	}
}
