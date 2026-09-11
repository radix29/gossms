package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Operator Properties > General. The operator is second of three with a
// category, so a lost selection or load can't pass by writing blanks.

func loadOperatorGeneralPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form, *string) {
	t.Helper()
	responses := append(agentOperatorResponses(), agentCategoryResponse())
	sc, inst := newFakeConn(t, responses...)
	name := agentOperatorName
	form, apply := loadPage(t, pageOperatorGeneral(sc, &name), inst)
	return inst, apply, form, &name
}

// sp_update_operator addresses by name, so the e-mail write precedes the
// rename.
func TestOperatorGeneralRenamesLastAndUnderTheOldName(t *testing.T) {
	inst, apply, form, name := loadOperatorGeneralPage(t)

	editText(t, form, "E-mail address", "reports@contoso.com")
	editText(t, form, "Name", "reporting-team")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want two statements, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], "@name = N'reporting', @email_address = N'reports@contoso.com'") {
		t.Errorf("first statement:\n%s", stmts[0])
	}
	if !strings.Contains(stmts[1], "@name = N'reporting', @new_name = N'reporting-team'") {
		t.Errorf("the rename should run last, under the old name:\n%s", stmts[1])
	}
	if *name != "reporting-team" {
		t.Errorf("the shared name cell is still %q after the rename", *name)
	}
}

// noneItem means no category, sent as [Uncategorized]; sp_update_operator
// rejects an empty name.
func TestOperatorGeneralClearingTheCategorySendsUncategorized(t *testing.T) {
	inst, apply, form, _ := loadOperatorGeneralPage(t)

	editSelect(t, form, "Category", noneItem)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@name = N'reporting', @category_name = N'[Uncategorized]'")
}

// Picks an entry that's neither the sentinel nor the first.
func TestOperatorGeneralWritesTheCategoryThatWasPicked(t *testing.T) {
	inst, apply, form, _ := loadOperatorGeneralPage(t)

	editSelect(t, form, "Category", "[Uncategorized (Local)]")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@category_name = N'[Uncategorized (Local)]'")
}

// A disabled operator gets no alert pages, so the checkbox must mean what it
// shows.
func TestOperatorGeneralDisablingSendsEnabledZero(t *testing.T) {
	inst, apply, form, _ := loadOperatorGeneralPage(t)

	editCheck(t, form, "Enabled", false)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@name = N'reporting', @enabled = 0")
}

// TestOperatorGeneralUntouchedPageWritesNothing.
func TestOperatorGeneralUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _ := loadOperatorGeneralPage(t)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
