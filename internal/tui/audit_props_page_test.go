package tui

import (
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
)

// Audit Properties > General, driven end to end through fakedb_test.go.
//
// Every case acts on "HIPAA", which is the *second* audit in the list — a page
// that ignored the name it was opened with would still pass on the first.

// TestAuditFailureLabelsAndValuesArePaired is the round-trip trap CLAUDE.md
// names: the label list and the value list are parallel, so swapping two
// entries in one leaves load and apply agreeing with each other while the page
// writes SHUTDOWN for "Fail operation". Pinning by name is the only thing that
// catches it.
func TestAuditFailureLabelsAndValuesArePaired(t *testing.T) {
	if len(auditFailureItems) != len(auditFailureValues) {
		t.Fatalf("%d labels against %d values", len(auditFailureItems), len(auditFailureValues))
	}
	for label, want := range map[string]string{
		"Continue":         gosmo.AuditFailureContinue,
		"Shut down server": gosmo.AuditFailureShutdown,
		"Fail operation":   gosmo.AuditFailureFailOp,
	} {
		i := slices.Index(auditFailureItems, label)
		if i < 0 {
			t.Fatalf("no %q item", label)
		}
		if auditFailureValues[i] != want {
			t.Errorf("%q writes %q, want %q", label, auditFailureValues[i], want)
		}
	}
}

func TestAuditGeneralLoadsTheNamedAudit(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("HIPAA"), auditRows())
	form, _ := loadPage(t, pageAuditGeneral(sc, ptr("HIPAA")), inst)

	if got := textRow(t, form, "Audit name").Value(); got != "HIPAA" {
		t.Errorf("Audit name = %q", got)
	}
	if got := textRow(t, form, "Queue delay").Value(); got != "2000" {
		t.Errorf("Queue delay = %q", got)
	}
	if got := selectRow(t, form, "On audit log failure").Value(); got != "Shut down server" {
		t.Errorf("On audit log failure = %q", got)
	}
	if got := textRow(t, form, "Filter predicate").Value(); !strings.Contains(got, "server_principal_name") {
		t.Errorf("Filter predicate = %q", got)
	}
	if got := selectRow(t, form, "File count limit").Value(); got != "Maximum files" {
		t.Errorf("File count limit = %q — max_rollover_files stays at its UNLIMITED sentinel", got)
	}
	if got := textRow(t, form, "Number of files").Value(); got != "7" {
		t.Errorf("Number of files = %q", got)
	}
	if !checkRow(t, form, "Reserve disk space").Checked() {
		t.Error("Reserve disk space did not load")
	}
}

// A Windows-log audit has no file settings at all, and offering an empty file
// path would build an ALTER naming a directory nobody chose.
func TestAuditGeneralHidesFileRowsForALogAudit(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("AppLogAudit"), auditRows())
	form, _ := loadPage(t, pageAuditGeneral(sc, ptr("AppLogAudit")), inst)
	for _, r := range form.Rows() {
		if tr, ok := r.(interface{ Label() string }); ok && tr.Label() == sheetLabel("File path") {
			t.Fatal("an application-log audit shows a File path row")
		}
	}
}

// Nothing edited writes nothing — the case that fails silently the other way
// round, since an ALTER SERVER AUDIT with no change still disables and
// re-enables the audit.
func TestAuditGeneralWritesNothingWhenUntouched(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("HIPAA"), auditRows())
	_, apply := loadPage(t, pageAuditGeneral(sc, ptr("HIPAA")), inst)
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote %v", stmts)
	}
}

// The audit is disabled here, so no state dance is expected — the ALTER is the
// only statement, and it carries every setting, because ALTER SERVER AUDIT
// replaces the whole block and a value left out is a value cleared.
func TestAuditGeneralAppliesEverySettingTogether(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("HIPAA"), auditRows(), auditEnabled("HIPAA", false))
	form, apply := loadPage(t, pageAuditGeneral(sc, ptr("HIPAA")), inst)

	editText(t, form, "Queue delay", "5000")
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1: %v", len(stmts), stmts)
	}
	for _, want := range []string{
		"ALTER SERVER AUDIT [HIPAA]", "QUEUE_DELAY = 5000",
		"ON_FAILURE = SHUTDOWN", // untouched, and still written
		"MAX_FILES = 7",         // ditto, and not turned into rollover
		"MAXSIZE = 20 MB",
		`FILEPATH = N'C:\audit\'`,
		"RESERVE_DISK_SPACE = ON",
		"WHERE ([server_principal_name]<>N'sa')",
	} {
		if !strings.Contains(stmts[0], want) {
			t.Errorf("missing %q in:\n%s", want, stmts[0])
		}
	}
}

// Clearing the predicate is REMOVE WHERE, not an omitted clause: an ALTER that
// simply leaves WHERE out keeps the old filter.
func TestAuditGeneralClearingTheFilterRemovesIt(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("HIPAA"), auditRows(), auditEnabled("HIPAA", false))
	form, apply := loadPage(t, pageAuditGeneral(sc, ptr("HIPAA")), inst)

	editText(t, form, "Filter predicate", "")
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Two statements, not one: REMOVE WHERE combined with WITH(...) is a
	// syntax error the server reports only at execution — it failed a live
	// Apply after every statement-shape test here passed.
	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("got %d statements, want the settings ALTER and a separate REMOVE WHERE: %v", len(stmts), stmts)
	}
	if strings.Contains(stmts[0], "REMOVE") {
		t.Errorf("REMOVE WHERE shares a statement with the settings: %q", stmts[0])
	}
	if stmts[1] != "ALTER SERVER AUDIT [HIPAA] REMOVE WHERE" {
		t.Errorf("the clearing statement is %q", stmts[1])
	}
}

// The on-failure dropdown is where a swapped label/value table would show up
// in the statement, which is what the pairing test above cannot prove on its
// own: this pins the value that actually reaches the server.
func TestAuditGeneralWritesTheChosenFailureAction(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("HIPAA"), auditRows(), auditEnabled("HIPAA", false))
	form, apply := loadPage(t, pageAuditGeneral(sc, ptr("HIPAA")), inst)

	editSelect(t, form, "On audit log failure", "Fail operation")
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 1 || !strings.Contains(stmts[0], "ON_FAILURE = FAIL_OPERATION") {
		t.Errorf("got %v, want ON_FAILURE = FAIL_OPERATION", stmts)
	}
}

// An enabled audit refuses every ALTER but the state toggle, so the page's
// apply has to come out as off, alter, on — in that order.
func TestAuditGeneralApplyOnAnEnabledAuditTurnsItOffAndBackOn(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("Rollover"), auditRows(), auditEnabled("Rollover", true))
	form, apply := loadPage(t, pageAuditGeneral(sc, ptr("Rollover")), inst)

	editText(t, form, "Queue delay", "3000")
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	// Four: off, the settings ALTER, its own REMOVE WHERE (this audit has no
	// predicate), and on.
	stmts := inst.Statements()
	if len(stmts) != 4 {
		t.Fatalf("got %d statements, want 4: %v", len(stmts), stmts)
	}
	last := len(stmts) - 1
	if !strings.Contains(stmts[0], "STATE = OFF") || !strings.Contains(stmts[last], "STATE = ON") {
		t.Errorf("the alter is not bracketed by the state toggle: %v", stmts)
	}
	if !strings.Contains(stmts[1], "QUEUE_DELAY = 3000") {
		t.Errorf("the settings statement is %q", stmts[1])
	}
}

// Renaming goes last, so the ALTER above it still addresses the name the
// server has.
func TestAuditGeneralRenamesLast(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("HIPAA"), auditRows(), auditEnabled("HIPAA", false))
	form, apply := loadPage(t, pageAuditGeneral(sc, ptr("HIPAA")), inst)

	editText(t, form, "Audit name", "HIPAA2")
	editText(t, form, "Queue delay", "4000")
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("got %d statements, want 2: %v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "ALTER SERVER AUDIT [HIPAA]") || strings.Contains(stmts[0], "MODIFY NAME") {
		t.Errorf("the settings ALTER is %q", stmts[0])
	}
	if !strings.Contains(stmts[1], "MODIFY NAME = [HIPAA2]") {
		t.Errorf("the rename is %q", stmts[1])
	}
}
