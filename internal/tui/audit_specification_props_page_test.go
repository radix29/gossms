package tui

import (
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
)

// Server Audit Specification Properties > General, driven through
// fakedb_test.go. Every case acts on "HIPAA_spec", the *second* specification
// in the list, so a page that ignored the name it was opened with would still
// pass on the first.

// actionGroupRows scripts the pick list sys.dm_audit_actions answers with. It
// deliberately does not contain "LOGIN_CHANGE_PASSWORD_GROUP", which
// HIPAA_spec already records: a group the instance no longer defines must
// still appear on the page, or a stray Apply drops something the user never
// saw.
func actionGroupRows() fakeResponse {
	return fakeResponse{
		match: "FROM   sys.dm_audit_actions",
		cols:  1,
		rows: [][]driver.Value{
			{"BACKUP_RESTORE_GROUP"}, {"DATABASE_CHANGE_GROUP"}, {"SERVER_ROLE_MEMBER_CHANGE_GROUP"},
		},
	}
}

func specPageConn(t *testing.T, name string) (*db.ServerConn, *fakeInstance) {
	t.Helper()
	sc, inst := newFakeConn(t, specByName(name), specRows(), auditRows(), actionGroupRows())
	return sc, inst
}

func TestSpecificationGeneralLoadsTheNamedSpecification(t *testing.T) {
	sc, inst := specPageConn(t, "HIPAA_spec")
	form, _ := loadPage(t, pageServerAuditSpecificationGeneral(sc, "HIPAA_spec"), inst)

	if got := selectRow(t, form, "Audit").Value(); got != "HIPAA" {
		t.Errorf("Audit = %q, want HIPAA", got)
	}
	grid := toggleGrid(t, form)
	checked := map[string]bool{}
	for i, row := range grid.Text() {
		checked[row[0]] = grid.Values()[i][0]
	}
	if !checked["DATABASE_CHANGE_GROUP"] || !checked["LOGIN_CHANGE_PASSWORD_GROUP"] {
		t.Errorf("the recorded groups are not ticked: %v", checked)
	}
	if checked["BACKUP_RESTORE_GROUP"] {
		t.Error("a group this specification does not record is ticked")
	}
	// The group the pick list no longer offers must still be on the page.
	if !slices.ContainsFunc(grid.Text(), func(r []string) bool { return r[0] == "LOGIN_CHANGE_PASSWORD_GROUP" }) {
		t.Error("a recorded group missing from the server's pick list vanished from the page")
	}
}

func TestSpecificationGeneralWritesNothingWhenUntouched(t *testing.T) {
	sc, inst := specPageConn(t, "HIPAA_spec")
	_, apply := loadPage(t, pageServerAuditSpecificationGeneral(sc, "HIPAA_spec"), inst)
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote %v", stmts)
	}
}

// Two edits at once, one on and one off, because the add and drop lists only
// diverge after the first — and the grid is read back index-parallel against
// the list it was built from, so the group named in the statement is the whole
// assertion.
func TestSpecificationGeneralAddsAndDropsTheTickedGroups(t *testing.T) {
	sc, inst := newFakeConn(t, specByName("HIPAA_spec"), specRows(), auditRows(),
		actionGroupRows(), specEnabled("HIPAA_spec", false))
	form, apply := loadPage(t, pageServerAuditSpecificationGeneral(sc, "HIPAA_spec"), inst)

	grid := toggleGrid(t, form)
	toggleByName(t, grid, "BACKUP_RESTORE_GROUP", 0)  // on
	toggleByName(t, grid, "DATABASE_CHANGE_GROUP", 0) // off
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("got %d statements, want a drop and an add: %v", len(stmts), stmts)
	}
	// Dropped first: a specification allows no duplicate group.
	if !strings.Contains(stmts[0], "DROP (DATABASE_CHANGE_GROUP)") {
		t.Errorf("first statement is %q", stmts[0])
	}
	if !strings.Contains(stmts[1], "ADD (BACKUP_RESTORE_GROUP)") {
		t.Errorf("second statement is %q", stmts[1])
	}
	for _, s := range stmts {
		if strings.Contains(s, "LOGIN_CHANGE_PASSWORD_GROUP") {
			t.Errorf("an untouched group was rewritten: %q", s)
		}
	}
}

// An enabled specification refuses every change, so each write has to be
// bracketed by the state toggle.
func TestSpecificationGeneralApplyOnAnEnabledSpecTurnsItOffAndBackOn(t *testing.T) {
	sc, inst := newFakeConn(t, specByName("AppSpec"), specRows(), auditRows(),
		actionGroupRows(), specEnabled("AppSpec", true))
	form, apply := loadPage(t, pageServerAuditSpecificationGeneral(sc, "AppSpec"), inst)

	toggleByName(t, toggleGrid(t, form), "DATABASE_CHANGE_GROUP", 0)
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 3 {
		t.Fatalf("got %d statements, want 3: %v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "STATE = OFF") || !strings.Contains(stmts[2], "STATE = ON") {
		t.Errorf("the change is not bracketed by the state toggle: %v", stmts)
	}
}

func TestSpecificationGeneralRebindsTheAudit(t *testing.T) {
	sc, inst := newFakeConn(t, specByName("HIPAA_spec"), specRows(), auditRows(),
		actionGroupRows(), specEnabled("HIPAA_spec", false))
	form, apply := loadPage(t, pageServerAuditSpecificationGeneral(sc, "HIPAA_spec"), inst)

	editSelect(t, form, "Audit", "Rollover")
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 || !strings.Contains(stmts[0], "FOR SERVER AUDIT [Rollover]") {
		t.Errorf("got %v, want one reparenting statement", stmts)
	}
}

// An orphaned specification's audit is in no list. Preselecting the first real
// audit would let a stray Apply rebind it silently, so the missing name is
// added to the dropdown and selected instead.
func TestSpecificationGeneralDoesNotSilentlyRebindAnOrphan(t *testing.T) {
	sc, inst := specPageConn(t, "Orphan")
	form, apply := loadPage(t, pageServerAuditSpecificationGeneral(sc, "Orphan"), inst)

	if got := selectRow(t, form, "Audit").Value(); got != missingAuditItem {
		t.Errorf("the orphan's Audit dropdown shows %q", got)
	}
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an orphaned specification was rebound by opening the page: %v", stmts)
	}
}
