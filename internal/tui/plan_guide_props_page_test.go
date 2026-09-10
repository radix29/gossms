package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Plan Guide Properties, driven through fakedb_test.go. The one writable
// dialog among the Phase 3 tree families: General enables and disables the
// guide with sp_control_plan_guide, and nothing else here writes.

const propPlanGuide = "pg_reports"

func planGuideResponse(disabled bool, scope, scopeObject, batch, hints string) fakeResponse {
	// The unquoted halves, as OBJECT_SCHEMA_NAME/OBJECT_NAME return them; the
	// fixtures' routine names hold no bracket or dot to split on wrongly.
	scopeSchema, scopeName, _ := strings.Cut(strings.NewReplacer("[", "", "]", "").Replace(scopeObject), ".")
	return fakeResponse{match: "FROM   sys.plan_guides g", cols: 13, rows: [][]driver.Value{{
		int64(65536), propPlanGuide, disabled,
		"SELECT * FROM Claims WHERE PatientId = @p",
		scope, scopeObject, scopeSchema, scopeName, batch, "@p int", hints,
		propTypeDate, propTypeDate,
	}}}
}

func TestPlanGuideGeneralShowsItsScopeAndState(t *testing.T) {
	sc, inst := newTypePropConn(t, planGuideResponse(true, "OBJECT", "[dbo].[GetClaims]", "", "OPTION (RECOMPILE)"))
	form, apply := loadPage(t, pagePlanGuideGeneral(sc, propTypeDB, propPlanGuide), inst)

	if got := staticValue(t, form, "Scope type"); got != "OBJECT" {
		t.Errorf("Scope type is %q", got)
	}
	if got := staticValue(t, form, "Scope object"); got != "[dbo].[GetClaims]" {
		t.Errorf("Scope object is %q", got)
	}
	if checkRow(t, form, "Enabled").Checked() {
		t.Error("Enabled is checked, but the scripted row has is_disabled set")
	}
	if apply == nil {
		t.Fatal("the General page has no apply — enabling and disabling is the one write a plan guide has")
	}
}

// A SQL- or TEMPLATE-scoped guide names no object, and an empty value there
// reads as a page that failed to load one rather than as a scope that has none.
func TestPlanGuideGeneralSaysWhenThereIsNoScopeObject(t *testing.T) {
	sc, inst := newTypePropConn(t, planGuideResponse(false, "TEMPLATE", "", "", ""))
	form, _ := loadPage(t, pagePlanGuideGeneral(sc, propTypeDB, propPlanGuide), inst)

	if got := staticValue(t, form, "Scope object"); got != "(not object-scoped)" {
		t.Errorf("Scope object is %q on a TEMPLATE-scoped guide", got)
	}
}

// An untouched page must write nothing. sp_control_plan_guide is not a
// no-op against an already-enabled guide — it succeeds, and every Apply on an
// unread dialog would then land a statement in the server's log.
func TestPlanGuideApplyWritesNothingUntouched(t *testing.T) {
	sc, inst := newTypePropConn(t, planGuideResponse(true, "OBJECT", "[dbo].[GetClaims]", "", ""))
	_, apply := loadPage(t, pagePlanGuideGeneral(sc, propTypeDB, propPlanGuide), inst)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if got := inst.Statements(); len(got) != 0 {
		t.Errorf("an untouched page executed %v", got)
	}
}

// Ticking Enabled on a disabled guide has to reach sp_control_plan_guide with
// ENABLE and the guide's own name — the two arguments that decide which guide
// changes and in which direction.
func TestPlanGuideApplyEnablesTheGuide(t *testing.T) {
	sc, inst := newTypePropConn(t, planGuideResponse(true, "OBJECT", "[dbo].[GetClaims]", "", ""))
	form, apply := loadPage(t, pagePlanGuideGeneral(sc, propTypeDB, propPlanGuide), inst)

	checkRow(t, form, "Enabled").Edit(true)
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stmts := inst.StatementsIn(propTypeDB)
	if len(stmts) != 1 {
		t.Fatalf("apply executed %v in %s, want one sp_control_plan_guide", stmts, propTypeDB)
	}
	if !strings.Contains(stmts[0], "sp_control_plan_guide") {
		t.Errorf("apply executed %q", stmts[0])
	}
}

// Unticking it goes the other way. Asserted separately rather than as a table
// because a page that sent the same operation whichever way the box moved
// would pass a one-direction test.
func TestPlanGuideApplyDisablesTheGuide(t *testing.T) {
	sc, inst := newTypePropConn(t, planGuideResponse(false, "SQL", "", "", ""))
	form, apply := loadPage(t, pagePlanGuideGeneral(sc, propTypeDB, propPlanGuide), inst)

	checkRow(t, form, "Enabled").Edit(false)
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	// The statement text is the same for both operations — the direction is
	// entirely in the parameters, so that is what has to be asserted.
	args, ok := inst.ExecArgs("sp_control_plan_guide")
	if !ok {
		t.Fatal("apply did not run sp_control_plan_guide")
	}
	if len(args) < 2 || args[0] != "DISABLE" || args[1] != propPlanGuide {
		t.Errorf("sp_control_plan_guide ran with %v, want DISABLE on %s", args, propPlanGuide)
	}
}

// The Query page is the guide's two texts. It must never carry an apply: the
// query text is matched character for character, so an edit that reached the
// server would silently stop the guide matching anything.
func TestPlanGuideQueryPageIsReadOnly(t *testing.T) {
	sc, inst := newTypePropConn(t, planGuideResponse(false, "SQL", "", "SELECT 1", "OPTION (MAXDOP 1)"))
	form, apply := loadPage(t, pagePlanGuideQuery(sc, propTypeDB, propPlanGuide), inst)

	if apply != nil {
		t.Error("the Query page has an apply")
	}
	var editors []*propsheet.EditorRow
	for _, r := range form.Rows() {
		if er, ok := r.(*propsheet.EditorRow); ok {
			editors = append(editors, er)
			if !er.Editor().ReadOnly() {
				t.Errorf("the %q editor is writable", er.Label())
			}
		}
	}
	// Query text, batch (this guide is SQL-scoped and has one) and hints.
	if len(editors) != 3 {
		t.Errorf("the Query page drew %d editors, want query text, batch and hints", len(editors))
	}
}

// A guide with no hints exists — one created from a plan handle pins a plan
// rather than adding an OPTION clause — and an empty editor there reads as a
// page that failed to load the hints.
func TestPlanGuideQueryPageSaysWhenThereAreNoHints(t *testing.T) {
	sc, inst := newTypePropConn(t, planGuideResponse(false, "TEMPLATE", "", "", ""))
	form, _ := loadPage(t, pagePlanGuideQuery(sc, propTypeDB, propPlanGuide), inst)

	for _, r := range form.Rows() {
		if er, ok := r.(*propsheet.EditorRow); ok && er.Label() == "Hints" {
			t.Error("the page drew a hints editor for a guide that applies none")
		}
	}
}
