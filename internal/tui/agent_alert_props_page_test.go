package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Alert Properties, both write pages. The alert scripted here triggers on
// severity 20 in appdb and already responds to the job — so every test acts on
// state that is real rather than zero, and a page that dropped what it loaded
// cannot pass by writing a value that happens to match a blank.

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

// TestAlertGeneralSwitchingToAnErrorNumberClearsTheSeverity. The two triggers
// are mutually exclusive and go out in one statement, so the field that is no
// longer in use has to be sent as 0 — an alert left with both set fires on the
// severity the user thought they had replaced.
func TestAlertGeneralSwitchingToAnErrorNumberClearsTheSeverity(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertGeneralResponses(), pageAlertGeneralFor)

	editRadio(t, form, "Trigger", "SQL Server error number")
	editText(t, form, "Error number", "9002")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_alert @name = N'Sev 20 errors', @message_id = 9002, @severity = 0")
}

// TestAlertGeneralWideningTheScopeSendsAnEmptyDatabase. "<all databases>" is
// the leading sentinel, and it maps to "" rather than to a database named
// after it.
func TestAlertGeneralWideningTheScopeSendsAnEmptyDatabase(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertGeneralResponses(), pageAlertGeneralFor)

	editSelect(t, form, "Database", allDatabasesItem)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_alert @name = N'Sev 20 errors', @database_name = N''")
}

// TestAlertGeneralNarrowingTheScopePicksTheNamedDatabase — the other
// direction, on a database that is neither the sentinel nor the first real
// entry.
func TestAlertGeneralNarrowingTheScopePicksTheNamedDatabase(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertGeneralResponses(), pageAlertGeneralFor)

	editSelect(t, form, "Database", "salesdb")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@database_name = N'salesdb'")
}

// TestAlertGeneralRenamesLastAndUnderTheOldName — as on every renaming page,
// the writes before it address the alert by the name the server still has.
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

// TestAlertGeneralUntouchedPageWritesNothing. Nine rows are seeded from the
// alert, including the delay, which is loaded in seconds and written back in
// seconds — a unit mismatch there rewrites the alert on every OK.
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

// The Response page reads the operator list it ticks against and the job list
// it picks a response job from.
func alertResponseResponses() []fakeResponse {
	job := jobRow(agentJobName, "Database Maintenance", "appuser", true, 0, 0, "")
	return append(agentOperatorResponses(), agentJobResponses(job)...)
}

// TestAlertResponseNotifiesTheOperatorThatWasTicked ticks the second of three
// operators. The grid is read back index-parallel against the operator list,
// so getting this wrong pages somebody who is not on call.
func TestAlertResponseNotifiesTheOperatorThatWasTicked(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertResponseResponses(), pageAlertResponseFor)

	toggleByName(t, toggleGrid(t, form), agentOperatorName, 0)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_add_notification @alert_name = N'Sev 20 errors', @operator_name = N'reporting'")
}

// TestAlertResponseRemovesTheNotificationThatWasUnticked — the destructive
// direction, on the one operator the alert actually notifies.
func TestAlertResponseRemovesTheNotificationThatWasUnticked(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertResponseResponses(), pageAlertResponseFor)

	toggleByName(t, toggleGrid(t, form), "dba-oncall", 0)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_delete_notification @alert_name = N'Sev 20 errors', @operator_name = N'dba-oncall'")
}

// TestAlertResponseShowsWhichOperatorsAreAlreadyNotified. The tick marks come
// from a different query than the operator list, and they are what tell the
// user which way a toggle will go.
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

// TestAlertResponseSetsTheResponseJobThatWasPicked picks the second of two
// jobs — a response job is what an alert actually *does*, so the wrong one is
// the wrong workload running unattended.
func TestAlertResponseSetsTheResponseJobThatWasPicked(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertResponseResponses(), pageAlertResponseFor)

	editSelect(t, form, "Response job", "Backup log")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_alert @name = N'Sev 20 errors', @job_name = N'Backup log'")
}

// TestAlertResponseClearingTheJobSendsTheEmptySentinel. noneItem sits at index
// 0 and means no job at all.
func TestAlertResponseClearingTheJobSendsTheEmptySentinel(t *testing.T) {
	inst, apply, form, _ := loadAlertPage(t, alertResponseResponses(), pageAlertResponseFor)

	editSelect(t, form, "Response job", noneItem)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_alert @name = N'Sev 20 errors', @job_name = N''")
}
