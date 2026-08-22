package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The three owner-transfer pages at page level: Owned Schemas (shared by
// Database Role and Database User Properties) and Owned Roles (Database Role
// and Server Role Properties).
//
// owner_transfer_page_test.go covers the shared widget — selection, commit,
// dirty/revert — against a stand-in type. What only a page test can show is
// which objects the page decided to *list*, and that the transfer reaches the
// right gosmo writer: the pages filter a full catalog down to what this
// principal owns, so an object that leaks into that list gets handed to
// somebody on a page the user thinks is about something else.

const ownedNameCol = 0

// transferTo moves onto the named row and picks a new owner, the gesture the
// page stages until Apply. The dropdown is not dirty-tracked — the page reads
// it back through its own commitCurrent — so chooseSelect, not editSelect.
func transferTo(t *testing.T, form *propsheet.Form, grid *controls.DataGrid, object, newOwner string) {
	t.Helper()
	selectGridRow(t, grid, ownedNameCol, object)
	chooseSelect(t, form, "Transfer owner to", newOwner)
}

// assertGridLists insists the grid holds exactly these objects, in any order —
// the page's filter is the assertion, not the catalog's order.
func assertGridLists(t *testing.T, grid *controls.DataGrid, want ...string) {
	t.Helper()
	var got []string
	for i := 0; ; i++ {
		row := grid.Row(i)
		if row == nil {
			break
		}
		got = append(got, row[ownedNameCol])
	}
	if len(got) != len(want) {
		t.Fatalf("the grid lists %v, want %v", got, want)
	}
	for _, w := range want {
		if !containsItem(got, w) {
			t.Errorf("the grid lists %v, missing %q", got, w)
		}
	}
}

// -- Owned Schemas -----------------------------------------------------------

func loadOwnedSchemasPage(t *testing.T, principal string) (*fakeInstance, propApply, *propsheet.Form, *controls.DataGrid) {
	t.Helper()
	responses := []fakeResponse{dbByNameResp(principalDatabase, 5)}
	responses = append(responses, databaseSchemaResponses()...)
	responses = append(responses, databaseUsersResponse())
	responses = append(responses, databaseRoleResponses()...)
	responses = append(responses, schemaObjectCountResponses()...)
	sc, inst := newFakeConn(t, responses...)
	name := principal
	form, apply := loadPage(t, pagePrincipalOwnedSchemas(sc, principalDatabase, &name, "role"), inst)
	return inst, apply, form, plainGrid(t, form)
}

// TestOwnedSchemasListsOnlyWhatThisPrincipalOwns. Four schemas exist and this
// role owns two of them; a page that listed all four would offer to transfer
// dbo, which is a system schema the server refuses, and staging, which belongs
// to somebody else.
func TestOwnedSchemasListsOnlyWhatThisPrincipalOwns(t *testing.T) {
	_, _, _, grid := loadOwnedSchemasPage(t, principalRole)
	assertGridLists(t, grid, "archive", principalSchema)
}

// TestOwnedSchemasTransfersTheSchemaTheRowIsOn acts on the second of the two,
// so a page that ignored the selection cannot pass.
func TestOwnedSchemasTransfersTheSchemaTheRowIsOn(t *testing.T) {
	inst, apply, form, grid := loadOwnedSchemasPage(t, principalRole)

	transferTo(t, form, grid, principalSchema, "reporting")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, principalDatabase, "ALTER AUTHORIZATION ON SCHEMA::[sales] TO [reporting]")
}

// TestOwnedSchemasTransfersBothOfTwo. The pending edits are held per row and
// only the row under the cursor is live, so a page that committed only the
// selected one would silently drop the first transfer.
func TestOwnedSchemasTransfersBothOfTwo(t *testing.T) {
	inst, apply, form, grid := loadOwnedSchemasPage(t, principalRole)

	transferTo(t, form, grid, "archive", "reporting")
	transferTo(t, form, grid, principalSchema, principalUser)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn(principalDatabase)
	if len(stmts) != 2 {
		t.Fatalf("want two statements, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	want := []string{
		"ALTER AUTHORIZATION ON SCHEMA::[archive] TO [reporting]",
		"ALTER AUTHORIZATION ON SCHEMA::[sales] TO [appuser]",
	}
	for i, w := range want {
		if !strings.Contains(stmts[i], w) {
			t.Errorf("statement %d:\n%s\nwant it to contain: %s", i+1, stmts[i], w)
		}
	}
}

// TestOwnedSchemasUntouchedPageWritesNothing. Merely visiting the page selects
// row 0 and fills the dropdown from it; if that counted as an edit, opening
// and OK-ing the dialog would transfer ownership.
func TestOwnedSchemasUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _ := loadOwnedSchemasPage(t, principalRole)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.StatementsIn(principalDatabase); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// -- Owned Roles, database ---------------------------------------------------

func loadOwnedRolesPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form, *controls.DataGrid) {
	t.Helper()
	responses := []fakeResponse{dbByNameResp(principalDatabase, 5)}
	responses = append(responses, databaseRoleResponses()...)
	responses = append(responses, databaseUsersResponse())
	sc, inst := newFakeConn(t, responses...)
	name := principalRole
	form, apply := loadPage(t, pageRoleOwnedRoles(sc, principalDatabase, &name), inst)
	return inst, apply, form, plainGrid(t, form)
}

// TestOwnedRolesListsOnlyTheRolesThisRoleOwns — five roles exist, this one
// owns two, and it must not list itself.
func TestOwnedRolesListsOnlyTheRolesThisRoleOwns(t *testing.T) {
	_, _, _, grid := loadOwnedRolesPage(t)
	assertGridLists(t, grid, "audit_reader", "report_reader")
}

// TestOwnedRolesTransfersTheRoleTheRowIsOn. Role ownership is a
// security-administration change — the new owner can add members to it.
func TestOwnedRolesTransfersTheRoleTheRowIsOn(t *testing.T) {
	inst, apply, form, grid := loadOwnedRolesPage(t)

	transferTo(t, form, grid, "report_reader", "reporting")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, principalDatabase, "ALTER AUTHORIZATION ON ROLE::[report_reader] TO [reporting]")
}

// -- Owned Roles, server -----------------------------------------------------

func loadServerOwnedRolesPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form, *controls.DataGrid) {
	t.Helper()
	responses := append(serverRoleResponses(), loginListResponse())
	sc, inst := newFakeConn(t, responses...)
	name := principalSrvRole
	form, apply := loadPage(t, pageServerRoleOwnedRoles(sc, &name), inst)
	return inst, apply, form, plainGrid(t, form)
}

// TestServerOwnedRolesListsOnlyTheRolesThisRoleOwns.
func TestServerOwnedRolesListsOnlyTheRolesThisRoleOwns(t *testing.T) {
	_, _, _, grid := loadServerOwnedRolesPage(t)
	assertGridLists(t, grid, "app_readers", "app_writers")
}

// TestServerOwnedRolesTransfersTheRoleTheRowIsOn — the same page one scope up,
// where the writer is ALTER AUTHORIZATION ON SERVER ROLE:: and the statement
// runs server-scoped rather than in a database.
func TestServerOwnedRolesTransfersTheRoleTheRowIsOn(t *testing.T) {
	inst, apply, form, grid := loadServerOwnedRolesPage(t)

	transferTo(t, form, grid, "app_writers", "otheruser")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "ALTER AUTHORIZATION ON SERVER ROLE::[app_writers] TO [otheruser]")
}
