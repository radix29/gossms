package tui

import (
	"database/sql/driver"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The Extended Properties page, shared by every Properties dialog for an
// object inside a database.
//
// Three writers share one grid — sp_addextendedproperty, sp_updateextended-
// property and sp_dropextendedproperty — and which one runs is decided per row
// from state the page keeps alongside the grid, not from anything visible in
// it. The level naming the object is the other half: it is supplied as a
// closure so a rename on the General page moves it, and a property added under
// the wrong level lands on a different object entirely.

const extPropNameCol = 0

func extPropResponses() []fakeResponse {
	return []fakeResponse{
		dbByNameResp(principalDatabase, 5),
		{match: "fn_listextendedproperty", cols: 2, rows: [][]driver.Value{
			{"MS_Description", "Application administrators"},
			{"Owner_Team", "Platform"},
		}},
	}
}

// loadExtPropPage opens the page against a database role, whose level literals
// are USER + the role name — the same shape a user's page has.
func loadExtPropPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form, *controls.DataGrid, *string) {
	t.Helper()
	sc, inst := newFakeConn(t, extPropResponses()...)
	name := principalRole
	page := pageExtendedProperties(sc, principalDatabase, func() gosmo.ExtendedPropertyLevel {
		return gosmo.ExtendedPropertyLevel{Level0Type: "USER", Level0Name: name}
	})
	form, apply := loadPage(t, page, inst)
	grid := plainGrid(t, form)
	if grid.Row(1) == nil {
		t.Fatal("the property grid has fewer than two rows — the fake is under-scripted, not the page wrong")
	}
	return inst, apply, form, grid, &name
}

// TestExtendedPropertiesAddNamesTheObjectTheDialogIsFor. The level literals
// decide which object the property lands on; get them wrong and it is written
// against a different principal, with no error.
func TestExtendedPropertiesAddNamesTheObjectTheDialogIsFor(t *testing.T) {
	inst, apply, form, _, _ := loadExtPropPage(t)

	editText(t, form, "Name", "Review_Date")
	editText(t, form, "Value", "2026-12-01")
	clickButton(t, form, "Add")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, principalDatabase, "sp_addextendedproperty")
	stmt := inst.StatementsIn(principalDatabase)[0]
	for _, want := range []string{
		"@name = N'Review_Date'", "@value = N'2026-12-01'",
		"@level0type = N'USER'", "@level0name = N'app_admin'",
	} {
		if !strings.Contains(stmt, want) {
			t.Errorf("wrote:\n%s\nwant it to contain: %s", stmt, want)
		}
	}
}

// TestExtendedPropertiesEditingAValueUpdatesThatProperty. sp_updateextended-
// property, not sp_add — the two are not interchangeable: Add fails on a name
// that exists and Set fails on one that does not.
func TestExtendedPropertiesEditingAValueUpdatesThatProperty(t *testing.T) {
	inst, apply, form, grid, _ := loadExtPropPage(t)

	selectGridRow(t, grid, extPropNameCol, "Owner_Team")
	editText(t, form, "Value", "Data Platform")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, principalDatabase, "sp_updateextendedproperty")
	stmt := inst.StatementsIn(principalDatabase)[0]
	for _, want := range []string{"@name = N'Owner_Team'", "@value = N'Data Platform'"} {
		if !strings.Contains(stmt, want) {
			t.Errorf("wrote:\n%s\nwant it to contain: %s", stmt, want)
		}
	}
}

// TestExtendedPropertiesRemoveDropsTheSelectedProperty. Remove is the
// destructive one and it acts on the grid selection, so it is exercised on the
// second row: a page that always removed row 0 would still write a plausible
// statement.
func TestExtendedPropertiesRemoveDropsTheSelectedProperty(t *testing.T) {
	inst, apply, form, grid, _ := loadExtPropPage(t)

	selectGridRow(t, grid, extPropNameCol, "Owner_Team")
	clickButton(t, form, "Remove")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, principalDatabase, "sp_dropextendedproperty")
	if stmt := inst.StatementsIn(principalDatabase)[0]; !strings.Contains(stmt, "@name = N'Owner_Team'") {
		t.Errorf("dropped the wrong property:\n%s", stmt)
	}
}

// TestExtendedPropertiesAddedThenRemovedWritesNothing. A property added and
// removed in the same sitting was never on the server, so dropping it would
// fail — the same "was it ever there" question the Job Steps page answers for
// a new step.
func TestExtendedPropertiesAddedThenRemovedWritesNothing(t *testing.T) {
	inst, apply, form, _, _ := loadExtPropPage(t)

	editText(t, form, "Name", "Scratch")
	editText(t, form, "Value", "temporary")
	clickButton(t, form, "Add")
	clickButton(t, form, "Remove")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.StatementsIn(principalDatabase); len(stmts) != 0 {
		t.Errorf("a property added and removed in one sitting wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// TestExtendedPropertiesUntouchedPageWritesNothing. Selecting row 0 seeds the
// Value field from the property; if that round trip changed the text at all,
// every OK would rewrite the property.
func TestExtendedPropertiesUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _, _ := loadExtPropPage(t)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.StatementsIn(principalDatabase); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
