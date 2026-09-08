package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Database Audit Specification Properties > General, driven through
// fakedb_test.go. Every case acts on "HIPAA_dbspec", the *second*
// specification in the list, so a page that ignored the name it was opened
// with would still pass on the first.

var dbSpecCreated = time.Date(2026, 3, 4, 9, 0, 0, 0, time.UTC)

// dbSpecRows is the list read. The rows carry no details — those come from
// dbSpecDetailRows, which is a second query rather than an aggregate, because
// an action row is four resolved names and not one.
func dbSpecRows() fakeResponse {
	return fakeResponse{
		match: "FROM   sys.database_audit_specifications",
		cols:  7,
		rows: [][]driver.Value{
			{int64(65536), "AppDBSpec", "11111111-1111-1111-1111-111111111111", "AppLogAudit",
				true, dbSpecCreated, dbSpecCreated},
			{int64(65537), "HIPAA_dbspec", "22222222-2222-2222-2222-222222222222", "HIPAA",
				false, dbSpecCreated, dbSpecCreated},
			// An orphan: SQL Server allows the audit to be dropped out from
			// under a specification, which leaves the join returning NULL.
			{int64(65538), "OrphanDB", "44444444-4444-4444-4444-444444444444", nil,
				false, dbSpecCreated, dbSpecCreated},
		},
	}
}

func dbSpecByName(name string) fakeResponse {
	return fakeResponse{match: "FROM   sys.database_audit_specifications", arg: name, cols: 7,
		rows: rowsNamed(dbSpecRows().rows, name, 1)}
}

// dbSpecDetailRows answers the details read for every specification at once —
// the id in the first column is what groups them.
func dbSpecDetailRows() fakeResponse {
	return fakeResponse{
		match: "FROM   sys.database_audit_specification_details",
		cols:  8,
		rows: [][]driver.Value{
			{int64(65536), "BACKUP_RESTORE_GROUP", "DATABASE", true, nil, nil, "appdb", nil},
			{int64(65537), "SCHEMA_OBJECT_ACCESS_GROUP", "DATABASE", true, nil, nil, "appdb", nil},
			// A group the pick list below no longer offers: still recorded, so
			// it must still appear on the page.
			{int64(65537), "DATABASE_ROLE_MEMBER_CHANGE_GROUP", "DATABASE", true, nil, nil, "appdb", nil},
			{int64(65537), "SELECT", "OBJECT", false, "SUCCESS AND FAILURE", "dbo", "Patient", "public"},
			{int64(65537), "INSERT", "SCHEMA", false, "SUCCESS AND FAILURE", nil, "dbo", "clinic_rw"},
		},
	}
}

// dbAuditActionRows scripts the two pick lists. The group list deliberately
// omits DATABASE_ROLE_MEMBER_CHANGE_GROUP, which HIPAA_dbspec records: a group
// the instance no longer defines must still appear, or a stray Apply drops
// something the user never saw.
func dbAuditActionRows() []fakeResponse {
	return []fakeResponse{
		{match: "class_desc = 'DATABASE' AND configuration_level = 'Group'", cols: 1,
			rows: [][]driver.Value{
				{"DATABASE_OBJECT_ACCESS_GROUP"}, {"SCHEMA_OBJECT_ACCESS_GROUP"},
			}},
		{match: "class_desc IN ('DATABASE', 'SCHEMA', 'OBJECT')", cols: 1,
			rows: [][]driver.Value{{"DELETE"}, {"EXECUTE"}, {"INSERT"}, {"SELECT"}, {"UPDATE"}}},
	}
}

func dbSpecEnabled(name string, on bool) fakeResponse {
	return fakeResponse{match: "SELECT is_state_enabled FROM sys.database_audit_specifications", arg: name,
		cols: 1, rows: [][]driver.Value{{on}}}
}

func dbSpecDatabaseRow() fakeResponse {
	return fakeResponse{match: "compatibility_level, collation_name", cols: 8, rows: [][]driver.Value{{
		"appdb", int64(7), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, dbSpecCreated,
	}}}
}

func dbSpecPageConn(t *testing.T, name string, extra ...fakeResponse) (*db.ServerConn, *fakeInstance) {
	t.Helper()
	responses := append([]fakeResponse{
		dbSpecDatabaseRow(), dbSpecByName(name), dbSpecRows(), dbSpecDetailRows(), auditRows(),
	}, dbAuditActionRows()...)
	return newFakeConn(t, append(responses, extra...)...)
}

func dbSpecPage(sc *db.ServerConn, name string) propPage {
	return pageDatabaseAuditSpecificationGeneral(sc, "appdb", name)
}

// gridAt finds the nth toggle grid on a page — this one has two, so the
// shared toggleGrid helper refuses it by design.
func gridAt(t *testing.T, f *propsheet.Form, n int) *propsheet.ToggleGridRow {
	t.Helper()
	seen := 0
	for _, r := range f.Rows() {
		if tg, ok := r.(*propsheet.ToggleGridRow); ok {
			if seen == n {
				return tg
			}
			seen++
		}
	}
	t.Fatalf("this page has no toggle grid at index %d", n)
	return nil
}

func TestDBSpecificationGeneralLoadsTheNamedSpecification(t *testing.T) {
	sc, inst := dbSpecPageConn(t, "HIPAA_dbspec")
	form, _ := loadPage(t, dbSpecPage(sc, "HIPAA_dbspec"), inst)

	if got := selectRow(t, form, "Audit").Value(); got != "HIPAA" {
		t.Errorf("Audit = %q, want HIPAA", got)
	}
	groups := gridAt(t, form, 0)
	checked := map[string]bool{}
	for i, row := range groups.Text() {
		checked[row[0]] = groups.Values()[i][0]
	}
	if !checked["SCHEMA_OBJECT_ACCESS_GROUP"] {
		t.Errorf("a recorded group is not ticked: %v", checked)
	}
	// A group the server's pick list no longer offers is still recorded.
	if !checked["DATABASE_ROLE_MEMBER_CHANGE_GROUP"] {
		t.Errorf("a recorded group missing from the pick list vanished from the page: %v", checked)
	}
	if checked["DATABASE_OBJECT_ACCESS_GROUP"] {
		t.Error("a group this specification does not record is ticked")
	}
	// The other specification's group must not be here — the details read
	// answers for every specification at once and is grouped by id.
	if _, ok := checked["BACKUP_RESTORE_GROUP"]; ok {
		t.Error("another specification's action group leaked onto the page")
	}

	actions := gridAt(t, form, 1)
	var listed []string
	for _, row := range actions.Text() {
		listed = append(listed, row[0])
	}
	want := []string{"SELECT ON OBJECT::[dbo].[Patient] BY public", "INSERT ON SCHEMA::[dbo] BY clinic_rw"}
	for _, w := range want {
		if !containsString(listed, w) {
			t.Errorf("missing %q in the audited actions: %v", w, listed)
		}
	}
}

func containsString(all []string, want string) bool {
	for _, s := range all {
		if s == want {
			return true
		}
	}
	return false
}

// SQL Server allows one specification per audit per database, so an audit
// another specification already holds is not offered — while this
// specification's own stays, or the page would open showing something other
// than what it is bound to.
func TestDBSpecificationGeneralOmitsAnAuditAnotherSpecificationHolds(t *testing.T) {
	sc, inst := dbSpecPageConn(t, "HIPAA_dbspec")
	form, _ := loadPage(t, dbSpecPage(sc, "HIPAA_dbspec"), inst)

	items := selectRow(t, form, "Audit").Items()
	if containsString(items, "AppLogAudit") {
		t.Errorf("an audit AppDBSpec already holds is offered: %v", items)
	}
	if !containsString(items, "HIPAA") {
		t.Errorf("the specification's own audit is missing: %v", items)
	}
	if !containsString(items, "Rollover") {
		t.Errorf("a free audit is missing: %v", items)
	}
}

func TestDBSpecificationGeneralWritesNothingWhenUntouched(t *testing.T) {
	sc, inst := dbSpecPageConn(t, "HIPAA_dbspec")
	_, apply := loadPage(t, dbSpecPage(sc, "HIPAA_dbspec"), inst)
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote %v", stmts)
	}
}

// Two edits at once, one on and one off, because the add and drop lists only
// diverge after the first.
func TestDBSpecificationGeneralAddsAndDropsTheTickedGroups(t *testing.T) {
	sc, inst := dbSpecPageConn(t, "HIPAA_dbspec", dbSpecEnabled("HIPAA_dbspec", false))
	form, apply := loadPage(t, dbSpecPage(sc, "HIPAA_dbspec"), inst)

	groups := gridAt(t, form, 0)
	toggleByName(t, groups, "DATABASE_OBJECT_ACCESS_GROUP", 0) // on
	toggleByName(t, groups, "SCHEMA_OBJECT_ACCESS_GROUP", 0)   // off
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("got %d statements, want a drop and an add: %v", len(stmts), stmts)
	}
	// Dropped first: a specification allows no duplicate group.
	if !strings.Contains(stmts[0], "DROP (SCHEMA_OBJECT_ACCESS_GROUP)") {
		t.Errorf("first statement is %q", stmts[0])
	}
	if !strings.Contains(stmts[1], "ADD (DATABASE_OBJECT_ACCESS_GROUP)") {
		t.Errorf("second statement is %q", stmts[1])
	}
	for _, s := range stmts {
		if strings.Contains(s, "DATABASE_ROLE_MEMBER_CHANGE_GROUP") {
			t.Errorf("an untouched group was rewritten: %q", s)
		}
	}
}

// The action half is the whole reason this page is not the server one: an
// unticked action leaves as a securable clause, not as a group.
func TestDBSpecificationGeneralDropsAnUntickedAction(t *testing.T) {
	sc, inst := dbSpecPageConn(t, "HIPAA_dbspec", dbSpecEnabled("HIPAA_dbspec", false))
	form, apply := loadPage(t, dbSpecPage(sc, "HIPAA_dbspec"), inst)

	toggleByName(t, gridAt(t, form, 1), "SELECT ON OBJECT::[dbo].[Patient] BY public", 0)
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want one drop: %v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "DROP (SELECT ON OBJECT::[dbo].[Patient] BY [public])") {
		t.Errorf("statement is %q", stmts[0])
	}
	if strings.Contains(stmts[0], "INSERT") {
		t.Errorf("an untouched action was rewritten: %q", stmts[0])
	}
}

// The add-an-action fields: the securable is an identifier and is
// bracket-quoted, the action and the class are keywords and are not.
func TestDBSpecificationGeneralAddsATypedAction(t *testing.T) {
	sc, inst := dbSpecPageConn(t, "HIPAA_dbspec", dbSpecEnabled("HIPAA_dbspec", false))
	form, apply := loadPage(t, dbSpecPage(sc, "HIPAA_dbspec"), inst)

	editSelect(t, form, "Action", "UPDATE")
	editText(t, form, "Securable", "dbo.Visit")
	editText(t, form, "Principal", "clinic_rw")
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want one add: %v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "ADD (UPDATE ON OBJECT::[dbo].[Visit] BY [clinic_rw])") {
		t.Errorf("statement is %q", stmts[0])
	}
}

// An action with no securable is refused on the page rather than sent as a
// clause with an empty name in it.
func TestDBSpecificationGeneralRefusesAnActionWithNoSecurable(t *testing.T) {
	sc, inst := dbSpecPageConn(t, "HIPAA_dbspec", dbSpecEnabled("HIPAA_dbspec", false))
	form, apply := loadPage(t, dbSpecPage(sc, "HIPAA_dbspec"), inst)

	editSelect(t, form, "Action", "DELETE")
	if err := apply(t.Context()); err == nil {
		t.Fatal("an action with no securable was accepted")
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a refused action still wrote %v", stmts)
	}
}

// An enabled specification refuses every change, so the whole apply has to be
// bracketed by the state toggle — once, not once per statement.
func TestDBSpecificationGeneralApplyOnAnEnabledSpecTurnsItOffAndBackOn(t *testing.T) {
	sc, inst := dbSpecPageConn(t, "AppDBSpec", dbSpecEnabled("AppDBSpec", true))
	form, apply := loadPage(t, dbSpecPage(sc, "AppDBSpec"), inst)

	toggleByName(t, gridAt(t, form, 0), "SCHEMA_OBJECT_ACCESS_GROUP", 0)
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

func TestDBSpecificationGeneralRebindsTheAudit(t *testing.T) {
	sc, inst := dbSpecPageConn(t, "HIPAA_dbspec", dbSpecEnabled("HIPAA_dbspec", false))
	form, apply := loadPage(t, dbSpecPage(sc, "HIPAA_dbspec"), inst)

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
// audit would let a stray Apply rebind it silently.
func TestDBSpecificationGeneralDoesNotSilentlyRebindAnOrphan(t *testing.T) {
	sc, inst := dbSpecPageConn(t, "OrphanDB")
	form, apply := loadPage(t, dbSpecPage(sc, "OrphanDB"), inst)

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

// The tree's own half: the folder labels a disabled specification, which is
// the only place its state shows.
func TestDatabaseAuditSpecificationsFolderLabelsDisabledOnes(t *testing.T) {
	sc, _ := newFakeConn(t, dbSpecDatabaseRow(), dbSpecRows(), dbSpecDetailRows())
	l := loaderCtx{ctx: t.Context(), sc: sc}
	children, err := loadDatabaseAuditSpecificationsChildren(l,
		&explorerNode{data: nodeData{Type: NodeDatabaseAuditSpecifications, DBName: "appdb", conn: sc}})
	if err != nil {
		t.Fatalf("loadDatabaseAuditSpecificationsChildren: %v", err)
	}
	if len(children) != 3 {
		t.Fatalf("got %d children, want 3", len(children))
	}
	if children[0].label != "AppDBSpec" || !children[0].data.IsEnabled {
		t.Errorf("the enabled specification is labelled %q / IsEnabled=%v",
			children[0].label, children[0].data.IsEnabled)
	}
	if children[1].label != "HIPAA_dbspec (Disabled)" {
		t.Errorf("the disabled specification is labelled %q", children[1].label)
	}
	// The node has to carry its database, or every action on it — Properties,
	// Delete, the state toggle — asks the wrong one.
	if children[1].data.DBName != "appdb" {
		t.Errorf("child DBName = %q, want appdb", children[1].data.DBName)
	}
}
