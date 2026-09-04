package tui

import (
	"errors"
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

// A Windows-log audit still carries the file rows — the destination can be
// changed to FILE here — but they are gated, so nothing invites input the
// ALTER would drop on the floor.
func TestAuditGeneralGatesFileRowsForALogAudit(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("AppLogAudit"), auditRows())
	form, _ := loadPage(t, pageAuditGeneral(sc, ptr("AppLogAudit")), inst)

	if got := selectRow(t, form, "Audit destination").Value(); got != "Application Log" {
		t.Errorf("Audit destination = %q", got)
	}
	if textRow(t, form, "File path").Focusable() {
		t.Error("File path is editable on an application-log audit")
	}
	if !selectRow(t, form, "File count limit").ReadOnly() {
		t.Error("File count limit is editable on an application-log audit")
	}
	if !checkRow(t, form, "Reserve disk space").ReadOnly() {
		t.Error("Reserve disk space is editable on an application-log audit")
	}
}

// Choosing File ungates the file rows: the gate follows the dropdown, not the
// destination the audit was loaded with.
func TestAuditGeneralUngatesFileRowsWhenFileIsChosen(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("AppLogAudit"), auditRows())
	form, _ := loadPage(t, pageAuditGeneral(sc, ptr("AppLogAudit")), inst)

	editSelect(t, form, "Audit destination", "File")
	if !textRow(t, form, "File path").Focusable() {
		t.Error("File path stayed gated after choosing File")
	}
	if checkRow(t, form, "Reserve disk space").ReadOnly() {
		t.Error("Reserve disk space stayed gated after choosing File")
	}
}

// Switching a file audit to a Windows log drops the whole file block: ALTER
// SERVER AUDIT replaces the target, and a file option under TO
// APPLICATION_LOG is a syntax error.
func TestAuditGeneralSwitchesAFileAuditToTheApplicationLog(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("HIPAA"), auditRows(), auditEnabled("HIPAA", false))
	form, apply := loadPage(t, pageAuditGeneral(sc, ptr("HIPAA")), inst)

	editSelect(t, form, "Audit destination", "Application Log")
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("got %d statements, want 1: %v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "TO APPLICATION_LOG") {
		t.Errorf("the destination was not written: %q", stmts[0])
	}
	for _, unwanted := range []string{"FILEPATH", "MAXSIZE", "MAX_FILES", "RESERVE_DISK_SPACE"} {
		if strings.Contains(stmts[0], unwanted) {
			t.Errorf("the file block survived the switch (%s):\n%s", unwanted, stmts[0])
		}
	}
}

// The other direction needs a path, and the catalog has none for a log audit.
// The server answers Msg 33072 "The audit log file path is invalid" — after
// disabling the audit — so the page refuses it before the window opens.
func TestAuditGeneralRefusesFileWithNoPath(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("AppLogAudit"), auditRows(), auditEnabled("AppLogAudit", false))
	form, apply := loadPage(t, pageAuditGeneral(sc, ptr("AppLogAudit")), inst)

	editSelect(t, form, "Audit destination", "File")
	if err := apply(t.Context()); err == nil {
		t.Fatal("a File audit with no path applied")
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("the refused apply still wrote %v", stmts)
	}
}

// With a path it goes through, and the file block is built from the rows the
// gate just released rather than from the audit's (empty) catalog row.
func TestAuditGeneralSwitchesALogAuditToFile(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("AppLogAudit"), auditRows(), auditEnabled("AppLogAudit", false))
	form, apply := loadPage(t, pageAuditGeneral(sc, ptr("AppLogAudit")), inst)

	editSelect(t, form, "Audit destination", "File")
	editText(t, form, "File path", `C:\newaudit\`)
	editSelect(t, form, "File count limit", "Rollover files")
	editText(t, form, "Number of files", "4")
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("got %d statements, want the ALTER and its REMOVE WHERE: %v", len(stmts), stmts)
	}
	for _, want := range []string{
		"TO FILE", `FILEPATH = N'C:\newaudit\'`, "MAX_ROLLOVER_FILES = 4",
		"MAXSIZE = UNLIMITED", "RESERVE_DISK_SPACE = OFF",
	} {
		if !strings.Contains(stmts[0], want) {
			t.Errorf("missing %q in:\n%s", want, stmts[0])
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

// auditNameReadMark is the by-name audit read auditApplyFailure repeats.
const auditNameReadMark = "WHERE  a.name = @p1"

// -- auditApplyFailure ---------------------------------------------------------

// A failed apply normally leaves the sheet alone, which is right when nothing
// landed and wrong when the disable window could not re-enable the audit: the
// State row then claims Enabled for an audit the server has stopped. The
// decision is a re-read, so it is pinned against a server that reports each
// answer.
func TestAuditApplyFailureMarksAnAuditLeftDisabled(t *testing.T) {
	// HIPAA's catalog row is is_state_enabled = 0 — the audit the window
	// switched off and could not switch back on.
	sc, inst := newFakeConn(t, auditByName("HIPAA"), auditRows())
	base := errors.New("Audit 'HIPAA' failed to start")
	before := len(inst.Reads(auditNameReadMark))

	err := auditApplyFailure(t.Context(), sc, "HIPAA", true, base)
	if _, ok := errors.AsType[committedApplyError](err); !ok {
		t.Fatalf("an audit left disabled is not marked committed: %v", err)
	}
	if !errors.Is(err, base) {
		t.Errorf("the original failure was lost: %v", err)
	}
	if !strings.Contains(err.Error(), "auditing is now stopped") {
		t.Errorf("the message does not say the audit is off: %v", err)
	}
	if len(inst.Reads(auditNameReadMark)) <= before {
		t.Error("the state was never re-read")
	}
}

// The ordinary failure — the ALTER refused, the window closed, the audit still
// running. Marking this one would reload the sheet and throw away edits the
// user has to retype.
func TestAuditApplyFailureLeavesAStillRunningAuditAlone(t *testing.T) {
	// Rollover's catalog row is is_state_enabled = 1.
	sc, _ := newFakeConn(t, auditByName("Rollover"), auditRows())
	base := errors.New("permission denied")

	err := auditApplyFailure(t.Context(), sc, "Rollover", true, base)
	if _, ok := errors.AsType[committedApplyError](err); ok {
		t.Fatalf("a still-running audit was marked committed: %v", err)
	}
	if err != base {
		t.Errorf("the error was not passed through unchanged: %v", err)
	}
}

// An audit that was already off cannot have been left off, so the re-read is
// not worth a round trip.
func TestAuditApplyFailureSkipsTheReadForADisabledAudit(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("HIPAA"), auditRows())
	base := errors.New("permission denied")
	// Against the count after connecting: gosmo.NewServer reads the instance.
	before := inst.QueryCount()

	if err := auditApplyFailure(t.Context(), sc, "HIPAA", false, base); err != base {
		t.Errorf("the error was not passed through unchanged: %v", err)
	}
	if inst.QueryCount() != before {
		t.Errorf("an audit that was already off was still re-read (%d queries)", inst.QueryCount()-before)
	}
}

// End to end: the re-enable fails after the ALTER has committed. The settings
// statement must still have gone out — that is what makes the failure a
// committed one — and the audit here is still reported running, so the error
// reaches the sheet unmarked and the edits survive.
func TestAuditGeneralApplyReportsAFailedReEnable(t *testing.T) {
	sc, inst := newFakeConn(t,
		auditByName("Rollover"), auditRows(), auditEnabled("Rollover", true),
		fakeResponse{match: "STATE = ON", err: errors.New("Audit 'Rollover' failed to start")})
	form, apply := loadPage(t, pageAuditGeneral(sc, ptr("Rollover")), inst)

	editSelect(t, form, "Audit destination", "Security Log")
	err := apply(t.Context())
	if err == nil {
		t.Fatal("a refused re-enable applied cleanly")
	}
	if !strings.Contains(err.Error(), "failed to start") {
		t.Errorf("the server's reason was lost: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 4 {
		t.Fatalf("got %d statements, want off, the ALTER, its REMOVE WHERE and the refused on: %v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[1], "TO SECURITY_LOG") {
		t.Errorf("the destination change did not go out before the failure: %q", stmts[1])
	}
}

// QUEUE_DELAY takes 0 (synchronous) or at least 1000 ms; 1..999 is refused by
// the engine, and on the properties page that rejection arrives only after
// WithDisabled has already stopped the audit. The row refuses it first.
func TestAuditQueueDelayRowRefusesSubSecondDelays(t *testing.T) {
	for _, tc := range []struct {
		value string
		ok    bool
	}{
		{"0", true},
		{"1", false},
		{"500", false},
		{"999", false},
		{"1000", true},
		{"60000", true},
		{"-1", false},
		{"nope", false},
	} {
		r := auditQueueDelayRow(2000)
		r.Edit(tc.value)
		err := r.Validate()
		if tc.ok && err != nil {
			t.Errorf("queue delay %q was refused: %v", tc.value, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("queue delay %q was accepted", tc.value)
		}
	}
	r := auditQueueDelayRow(2000)
	r.Edit("500")
	if err := r.Validate(); err == nil || !strings.Contains(err.Error(), "0 or at least 1000") {
		t.Errorf("the message does not say what is allowed: %v", err)
	}
}

// The page and the New Audit dialog must share the row, so the rule cannot
// drift between them: both go through auditQueueDelayRow.
func TestAuditGeneralQueueDelayRowIsTheValidatedOne(t *testing.T) {
	sc, inst := newFakeConn(t, auditByName("HIPAA"), auditRows())
	form, _ := loadPage(t, pageAuditGeneral(sc, ptr("HIPAA")), inst)

	row := textRow(t, form, "Queue delay")
	row.Edit("500")
	if err := row.Validate(); err == nil {
		t.Error("Audit Properties accepted a queue delay SQL Server refuses")
	}
}
