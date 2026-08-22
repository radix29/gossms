package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The four "General" pages that re-own or rename a principal: Database Role,
// Server Role, Database User and Schema Properties.
//
// Two failures are worth the fixtures. An owner transfer landing on the wrong
// principal hands a role or schema to somebody who should not have it, and it
// looks right on the page that did it. And each page decides from what it
// loaded whether the object is writable at all — a built-in gets Static rows
// and no apply, because ALTER ROLE public and ALTER AUTHORIZATION ON
// ROLE::public are syntax errors, not permission failures.

// -- Database Role > General -------------------------------------------------

func roleGeneralResponses() []fakeResponse {
	responses := []fakeResponse{dbByNameResp(principalDatabase, 5)}
	responses = append(responses, databaseRoleResponses()...)
	responses = append(responses, databaseUsersResponse())
	responses = append(responses, databaseSchemaResponses()...)
	return append(responses, principalPermissionResponses()...)
}

func loadRoleGeneralPage(t *testing.T, roleName string) (*fakeInstance, propApply, *propsheet.Form, *string) {
	t.Helper()
	sc, inst := newFakeConn(t, roleGeneralResponses()...)
	name := roleName
	form, apply := loadPage(t, pageRoleGeneral(sc, principalDatabase, &name), inst)
	return inst, apply, form, &name
}

// TestRoleGeneralRenamesLastAndUnderTheOldName. ALTER AUTHORIZATION names the
// role, so the rename has to be the last write of the run — and the shared
// name cell has to follow it, or every other page reloads under a name the
// database no longer knows.
func TestRoleGeneralRenamesLastAndUnderTheOldName(t *testing.T) {
	inst, apply, form, name := loadRoleGeneralPage(t, principalRole)

	editSelect(t, form, "Owner", "reporting")
	editText(t, form, "Role name", "app_admins")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn(principalDatabase)
	if len(stmts) != 2 {
		t.Fatalf("want two statements in %s, got %d:\n%s", principalDatabase, len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], "ALTER AUTHORIZATION ON ROLE::[app_admin] TO [reporting]") {
		t.Errorf("owner statement:\n%s", stmts[0])
	}
	if !strings.Contains(stmts[1], "ALTER ROLE [app_admin] WITH NAME = [app_admins]") {
		t.Errorf("the rename should run last, under the old name:\n%s", stmts[1])
	}
	if *name != "app_admins" {
		t.Errorf("the shared name cell is still %q after the rename", *name)
	}
}

// TestRoleGeneralOffersEveryPrincipalAsAnOwner. The owner list is users and
// roles together; a page that offered only one kind silently narrows what the
// user may transfer to.
func TestRoleGeneralOffersEveryPrincipalAsAnOwner(t *testing.T) {
	_, _, form, _ := loadRoleGeneralPage(t, principalRole)
	items := selectRow(t, form, "Owner").Items()

	for _, want := range []string{principalUser, "dbo", "reporting", "audit_reader", "public"} {
		if !containsItem(items, want) {
			t.Errorf("the Owner list %q does not offer %q", items, want)
		}
	}
}

// TestRoleGeneralBuiltInRoleHasNoApply. A fixed role's name and owner are
// Static rows and the page returns no apply at all — the page is what stops
// the statement being built, since the server answers with a syntax error.
func TestRoleGeneralBuiltInRoleHasNoApply(t *testing.T) {
	_, apply, form, _ := loadRoleGeneralPage(t, "db_owner")

	if apply != nil {
		t.Error("a fixed database role's General page has an apply closure")
	}
	for _, label := range []string{"Role name", "Owner"} {
		if hasEditableRow(form, label) {
			t.Errorf("a fixed database role's %q is editable", label)
		}
	}
}

// TestRoleGeneralUntouchedPageWritesNothing.
func TestRoleGeneralUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _ := loadRoleGeneralPage(t, principalRole)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.StatementsIn(principalDatabase); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// -- Server Role > General ---------------------------------------------------

func loadServerRoleGeneralPage(t *testing.T, roleName string) (*fakeInstance, propApply, *propsheet.Form, *string) {
	t.Helper()
	responses := append(serverRoleResponses(), loginListResponse(), serverRolePermissionsResponse())
	sc, inst := newFakeConn(t, responses...)
	name := roleName
	form, apply := loadPage(t, pageServerRoleGeneral(sc, &name), inst)
	return inst, apply, form, &name
}

// TestServerRoleGeneralRenamesLastAndUnderTheOldName. Same shape as the
// database role, one scope up — and a server role owns other server roles, so
// the transfer is a privilege-administration change.
func TestServerRoleGeneralRenamesLastAndUnderTheOldName(t *testing.T) {
	inst, apply, form, name := loadServerRoleGeneralPage(t, principalSrvRole)

	editSelect(t, form, "Owner", "otheruser")
	editText(t, form, "Role name", "app_ops")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want two statements, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], "ALTER AUTHORIZATION ON SERVER ROLE::[app_operators] TO [otheruser]") {
		t.Errorf("owner statement:\n%s", stmts[0])
	}
	if !strings.Contains(stmts[1], "ALTER SERVER ROLE [app_operators] WITH NAME = [app_ops]") {
		t.Errorf("the rename should run last, under the old name:\n%s", stmts[1])
	}
	if *name != "app_ops" {
		t.Errorf("the shared name cell is still %q after the rename", *name)
	}
}

// TestServerRoleGeneralBuiltInRoleHasNoApply — sysadmin, where getting this
// wrong is at its worst.
func TestServerRoleGeneralBuiltInRoleHasNoApply(t *testing.T) {
	_, apply, form, _ := loadServerRoleGeneralPage(t, "sysadmin")

	if apply != nil {
		t.Error("a fixed server role's General page has an apply closure")
	}
	for _, label := range []string{"Role name", "Owner"} {
		if hasEditableRow(form, label) {
			t.Errorf("a fixed server role's %q is editable", label)
		}
	}
}

// -- Database User > General -------------------------------------------------

func userGeneralResponses() []fakeResponse {
	responses := []fakeResponse{
		dbByNameResp(principalDatabase, 5),
		userByNameResponse(principalUser, "dbo", "appuser"),
		userByNameResponse("dbo", "dbo", ""),
	}
	responses = append(responses, databaseSchemaResponses()...)
	responses = append(responses, databaseRoleResponses()...)
	responses = append(responses, principalPermissionResponses()...)
	return append(responses, loginListResponse())
}

func loadUserGeneralPage(t *testing.T, userName string) (*fakeInstance, propApply, *propsheet.Form, *string) {
	t.Helper()
	sc, inst := newFakeConn(t, userGeneralResponses()...)
	name := userName
	form, apply := loadPage(t, pageUserGeneral(sc, principalDatabase, &name), inst)
	return inst, apply, form, &name
}

// TestUserGeneralSetsTheDefaultSchemaThatWasPicked. The schema list is four
// long and the one chosen is neither first nor the user's current one.
func TestUserGeneralSetsTheDefaultSchemaThatWasPicked(t *testing.T) {
	inst, apply, form, _ := loadUserGeneralPage(t, principalUser)

	editSelect(t, form, "Default schema", principalSchema)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, principalDatabase, "ALTER USER [appuser] WITH DEFAULT_SCHEMA = [sales]")
}

// TestUserGeneralRemapsTheLoginThatWasPicked. Remapping a user to the wrong
// login hands that login everything the user can do.
func TestUserGeneralRemapsTheLoginThatWasPicked(t *testing.T) {
	inst, apply, form, _ := loadUserGeneralPage(t, principalUser)

	editSelect(t, form, "Login name", "otheruser")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, principalDatabase, "ALTER USER [appuser] WITH LOGIN = [otheruser]")
}

// TestUserGeneralChoosingNoLoginWritesNothing. "(None)" is the leading
// sentinel; ALTER USER ... WITH LOGIN = [(None)] would fail, and there is no
// statement that unmaps a user from its login — the page's job is to not try.
func TestUserGeneralChoosingNoLoginWritesNothing(t *testing.T) {
	inst, apply, form, _ := loadUserGeneralPage(t, principalUser)

	editSelect(t, form, "Login name", noneItem)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.StatementsIn(principalDatabase); len(stmts) != 0 {
		t.Errorf("picking %q wrote:\n%s", noneItem, strings.Join(stmts, "\n"))
	}
}

// TestUserGeneralRenamesLastAndUnderTheOldName.
func TestUserGeneralRenamesLastAndUnderTheOldName(t *testing.T) {
	inst, apply, form, name := loadUserGeneralPage(t, principalUser)

	editSelect(t, form, "Default schema", principalSchema)
	editText(t, form, "User name", "app_user")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn(principalDatabase)
	if len(stmts) != 2 {
		t.Fatalf("want two statements, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], "ALTER USER [appuser] WITH DEFAULT_SCHEMA = [sales]") {
		t.Errorf("schema statement:\n%s", stmts[0])
	}
	if !strings.Contains(stmts[1], "ALTER USER [appuser] WITH NAME = [app_user]") {
		t.Errorf("the rename should run last, under the old name:\n%s", stmts[1])
	}
	if *name != "app_user" {
		t.Errorf("the shared name cell is still %q after the rename", *name)
	}
}

// TestUserGeneralBuiltInUserHasNoApply. dbo, guest, sys and INFORMATION_SCHEMA
// reject ALTER USER outright.
func TestUserGeneralBuiltInUserHasNoApply(t *testing.T) {
	_, apply, form, _ := loadUserGeneralPage(t, "dbo")

	if apply != nil {
		t.Error("a built-in user's General page has an apply closure")
	}
	for _, label := range []string{"User name", "Login name", "Default schema"} {
		if hasEditableRow(form, label) {
			t.Errorf("built-in user dbo's %q is editable", label)
		}
	}
}

// -- Schema > General --------------------------------------------------------

func schemaGeneralResponses() []fakeResponse {
	responses := []fakeResponse{dbByNameResp(principalDatabase, 5)}
	responses = append(responses, databaseSchemaResponses()...)
	responses = append(responses, databaseUsersResponse())
	responses = append(responses, databaseRoleResponses()...)
	responses = append(responses, schemaObjectCountResponses()...)
	return append(responses, principalPermissionResponses()...)
}

func loadSchemaGeneralPage(t *testing.T, schemaName string) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t, schemaGeneralResponses()...)
	form, apply := loadPage(t, pageSchemaGeneral(sc, principalDatabase, schemaName), inst)
	return inst, apply, form
}

// TestSchemaGeneralTransfersToThePrincipalThatWasPicked. There is no RENAME
// SCHEMA, so ownership is the only thing this page writes — and changing it
// affects permission chaining for everything the schema contains.
func TestSchemaGeneralTransfersToThePrincipalThatWasPicked(t *testing.T) {
	inst, apply, form := loadSchemaGeneralPage(t, principalSchema)

	editSelect(t, form, "Owner", "reporting")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, principalDatabase, "ALTER AUTHORIZATION ON SCHEMA::[sales] TO [reporting]")
}

// TestSchemaGeneralSystemSchemaHasNoApply.
func TestSchemaGeneralSystemSchemaHasNoApply(t *testing.T) {
	_, apply, form := loadSchemaGeneralPage(t, "dbo")

	if apply != nil {
		t.Error("a system schema's General page has an apply closure")
	}
	if hasEditableRow(form, "Owner") {
		t.Error("system schema dbo's Owner is editable")
	}
}

// TestSchemaGeneralUntouchedPageWritesNothing.
func TestSchemaGeneralUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _ := loadSchemaGeneralPage(t, principalSchema)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.StatementsIn(principalDatabase); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// -- helpers -----------------------------------------------------------------

// hasEditableRow reports whether the form has a Text or Select row under this
// label. A built-in principal's page renders the same field as a StaticRow, so
// "the row is not editable" is the assertion, not "the row is absent".
func hasEditableRow(f *propsheet.Form, label string) bool {
	for _, r := range f.Rows() {
		switch row := r.(type) {
		case *propsheet.TextRow:
			if row.Label() == sheetLabel(label) {
				return true
			}
		case *propsheet.SelectRow:
			if row.Label() == sheetLabel(label) {
				return true
			}
		}
	}
	return false
}

func containsItem(items []string, want string) bool {
	for _, it := range items {
		if it == want {
			return true
		}
	}
	return false
}
