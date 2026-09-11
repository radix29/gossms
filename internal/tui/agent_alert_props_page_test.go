package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Alert Properties write pages. The scripted alert fires on severity 20 in
// appdb and already responds to the job, so tests act on real state and a page
// that dropped its load can't pass by matching blanks.

func loadAlertPage(t *testing.T, extra []fakeResponse, build func(sc *db.ServerConn, name *string) propPage) (*fakeInstance, propApply, *propsheet.Form, *string) {
	t.Helper()
	responses := append(agentAlertResponses(), extra...)
	sc, inst := newFakeConn(t, responses...)
	name := agentAlertName
	form, apply := loadPage(t, build(sc, &name), inst)
	return inst, apply, form, &name
}

func alertGeneralResponses() []fakeResponse {
	return []fakeResponse{agentDatabaseListResponse(), agentCategoryResponse()}
}

func pageAlertGeneralFor(sc *db.ServerConn, n *string) propPage { return pageAlertGeneral(sc, n) }

// Triggers are mutually exclusive and sent together, so the unused one must go
// as 0; with both set the alert fires on the replaced severity.
func TestAlertGeneralSwitchingToAnErrorNumberClearsTheSeverity(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertGeneralResponses(), pageAlertGeneralFor)

	editRadio(t, form, "Trigger", "SQL Server error number")
	editText(t, form, "Error number", "9002")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_alert @name = N'Sev 20 errors', @message_id = 9002, @severity = 0")
}

// "<all databases>" is a sentinel mapping to "".
func TestAlertGeneralWideningTheScopeSendsAnEmptyDatabase(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertGeneralResponses(), pageAlertGeneralFor)

	editSelect(t, form, "Database", allDatabasesItem)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_alert @name = N'Sev 20 errors', @database_name = N''")
}

// Narrowing to a database that's neither the sentinel nor the first entry.
func TestAlertGeneralNarrowingTheScopePicksTheNamedDatabase(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertGeneralResponses(), pageAlertGeneralFor)

	editSelect(t, form, "Database", "salesdb")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@database_name = N'salesdb'")
}

// Rename is last, and earlier writes use the old name.
func TestAlertGeneralRenamesLastAndUnderTheOldName(t *testing.T) {
	inst, apply, form, name := loadAlertPage(t, alertGeneralResponses(), pageAlertGeneralFor)

	editText(t, form, "Notification message", "Page the on-call DBA")
	editText(t, form, "Name", "Severity 20 errors")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want two statements, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], "@name = N'Sev 20 errors', @notification_message = N'Page the on-call DBA'") {
		t.Errorf("first statement:\n%s", stmts[0])
	}
	if !strings.Contains(stmts[1], "@name = N'Sev 20 errors', @new_name = N'Severity 20 errors'") {
		t.Errorf("the rename should run last, under the old name:\n%s", stmts[1])
	}
	if *name != "Severity 20 errors" {
		t.Errorf("the shared name cell is still %q after the rename", *name)
	}
}

// An untouched page writes nothing; the delay loads and saves in seconds, and a
// unit mismatch would rewrite the alert on every OK.
func TestAlertGeneralUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _ := loadAlertPage(t, alertGeneralResponses(), pageAlertGeneralFor)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// -- Response ----------------------------------------------------------------

func pageAlertResponseFor(sc *db.ServerConn, n *string) propPage { return pageAlertResponse(sc, n) }

// The Response page reads the operator list it ticks and the job list for the
// response job.
func alertResponseResponses() []fakeResponse {
	job := jobRow(agentJobName, "Database Maintenance", "appuser", true, 0, 0, "")
	return append(agentOperatorResponses(), agentJobResponses(job)...)
}

// Ticks the second of three operators; the grid maps index-parallel to the
// list, and a mismatch pages the wrong person.
func TestAlertResponseNotifiesTheOperatorThatWasTicked(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertResponseResponses(), pageAlertResponseFor)

	toggleByName(t, toggleGrid(t, form), agentOperatorName, 0)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_add_notification @alert_name = N'Sev 20 errors', @operator_name = N'reporting'")
}

// Unticking removes the notification for the one operator actually notified.
func TestAlertResponseRemovesTheNotificationThatWasUnticked(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertResponseResponses(), pageAlertResponseFor)

	toggleByName(t, toggleGrid(t, form), "dba-oncall", 0)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_delete_notification @alert_name = N'Sev 20 errors', @operator_name = N'dba-oncall'")
}

// Tick marks come from a different query than the operator list and show which
// way a toggle goes.
func TestAlertResponseShowsWhichOperatorsAreAlreadyNotified(t *testing.T) {
	_, _, form, _ := loadAlertPage(t, alertResponseResponses(), pageAlertResponseFor)
	tg := toggleGrid(t, form)

	want := map[string]bool{"dba-oncall": true, agentOperatorName: false, "weekend-cover": false}
	for i, row := range tg.Text() {
		if got := tg.Values()[i][0]; got != want[row[0]] {
			t.Errorf("operator %q shows notified=%v, want %v", row[0], got, want[row[0]])
		}
	}
}

// Picks the second of two jobs; the wrong response job runs the wrong workload
// unattended.
func TestAlertResponseSetsTheResponseJobThatWasPicked(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertResponseResponses(), pageAlertResponseFor)

	editSelect(t, form, "Response job", "Backup log")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_alert @name = N'Sev 20 errors', @job_name = N'Backup log'")
}

// noneItem at index 0 means no job.
func TestAlertResponseClearingTheJobSendsTheEmptySentinel(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertResponseResponses(), pageAlertResponseFor)

	editSelect(t, form, "Response job", noneItem)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_alert @name = N'Sev 20 errors', @job_name = N''")
}
