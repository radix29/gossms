package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Page tests for Job Properties General, Notifications, Schedules and Alerts.
// Steps: agent_job_props_steps_test.go (write plan) and
// agent_job_steps_page_test.go (page end to end).

func loadJobPage(t *testing.T, extra []fakeResponse, build func(sc *db.ServerConn, name *string) propPage) (*fakeInstance, propApply, *propsheet.Form, *string) {
	t.Helper()
	job := jobRow(agentJobName, "Database Maintenance", "appuser", true, 0, 0, "")
	responses := append(agentJobResponses(job), extra...)
	sc, inst := newFakeConn(t, responses...)
	name := agentJobName
	form, apply := loadPage(t, build(sc, &name), inst)
	return inst, apply, form, &name
}

// -- General -----------------------------------------------------------------

func jobGeneralResponses() []fakeResponse {
	return []fakeResponse{loginListResponse(), agentCategoryResponse()}
}

func pageJobGeneralFor(sc *db.ServerConn, n *string) propPage { return pageJobGeneral(sc, n) }

// Other writes address the job by name, so rename runs last, and the shared
// name cell must end on the new name for the post-Apply reload.
func TestJobGeneralRenamesLastAndUnderTheOldName(t *testing.T) {
	inst, apply, form, name := loadJobPage(t, jobGeneralResponses(), pageJobGeneralFor)

	editText(t, form, "Description", "Rebuilds every index nightly")
	editText(t, form, "Name", "Nightly maintenance")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want two statements, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], "@job_name = N'Nightly reindex', @description = N'Rebuilds every index nightly'") {
		t.Errorf("first statement should set the description under the old name:\n%s", stmts[0])
	}
	if !strings.Contains(stmts[1], "@job_name = N'Nightly reindex', @new_name = N'Nightly maintenance'") {
		t.Errorf("the rename should run last, under the old name:\n%s", stmts[1])
	}
	if *name != "Nightly maintenance" {
		t.Errorf("the shared name cell is still %q after the rename", *name)
	}
}

// Owner and category are dropdowns read back by index into lists the page
// built.
func TestJobGeneralWritesTheOwnerAndCategoryThatWerePicked(t *testing.T) {
	inst, apply, form, _ := loadJobPage(t, jobGeneralResponses(), pageJobGeneralFor)

	editSelect(t, form, "Owner", "otheruser")
	editSelect(t, form, "Category", "Replication")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want two statements, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], "@category_name = N'Replication'") {
		t.Errorf("category statement:\n%s", stmts[0])
	}
	if !strings.Contains(stmts[1], "@owner_login_name = N'otheruser'") {
		t.Errorf("owner statement:\n%s", stmts[1])
	}
}

// The checkbox picks between two gosmo calls; inverted, a switched-off job
// keeps running.
func TestJobGeneralDisablingSendsEnabledZero(t *testing.T) {
	inst, apply, form, _ := loadJobPage(t, jobGeneralResponses(), pageJobGeneralFor)

	editCheck(t, form, "Enabled", false)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@job_name = N'Nightly reindex', @enabled = 0")
}

// A row dirty on load would rewrite the job (including its owner) on every OK.
func TestJobGeneralUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _ := loadJobPage(t, jobGeneralResponses(), pageJobGeneralFor)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// -- Notifications -----------------------------------------------------------

func pageJobNotificationsFor(sc *db.ServerConn, n *string) propPage {
	return pageJobNotifications(sc, n)
}

// The two sections are gated on their own rows, since DirtyPages is page-level
// only.
func TestJobNotificationsDeleteConditionDoesNotRewriteTheEmailOperator(t *testing.T) {
	inst, apply, form, _ := loadJobPage(t, agentOperatorResponses(), pageJobNotificationsFor)

	editCheck(t, form, "Delete job", true)
	editSelect(t, form, "When to delete", "When the job succeeds")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@delete_level = 1")
}

// sp_update_job can't clear an operator, so "" means leave it; ticking E-mail
// with no operator must not send the first operator.
func TestJobNotificationsEmailWithNoOperatorSendsNoOperatorName(t *testing.T) {
	inst, apply, form, _ := loadJobPage(t, agentOperatorResponses(), pageJobNotificationsFor)

	editCheck(t, form, "E-mail", true)
	editSelect(t, form, "When to e-mail", "When the job completes")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@notify_level_email = 3")
	if stmts := inst.Statements(); strings.Contains(stmts[0], "@notify_email_operator_name") {
		t.Errorf("no operator was chosen, so none should be sent:\n%s", stmts[0])
	}
}

// Picks the second of three operators; ignoring the selection would still name
// one.
func TestJobNotificationsEmailsTheOperatorThatWasPicked(t *testing.T) {
	inst, apply, form, _ := loadJobPage(t, agentOperatorResponses(), pageJobNotificationsFor)

	editCheck(t, form, "E-mail", true)
	editSelect(t, form, "Operator", agentOperatorName)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@notify_email_operator_name = N'reporting'")
}

// -- Schedules ---------------------------------------------------------------

const (
	schedAttachCol = 0
	schedNameCol   = 1
)

func loadJobSchedulesPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	inst, apply, form, _ := loadJobPage(t, agentScheduleResponses(),
		func(sc *db.ServerConn, n *string) propPage { return pageJobSchedules(sc, n) })
	if plainGrid(t, form).Row(2) == nil {
		t.Fatal("the schedule grid has fewer than three rows — the fake is under-scripted, not the page wrong")
	}
	return inst, apply, form
}

// Toggles the second schedule; reading the grid against the wrong slice
// attaches one the user never picked.
func TestJobSchedulesAttachesTheScheduleTheRowIsOn(t *testing.T) {
	inst, apply, form := loadJobSchedulesPage(t)

	activateGridCell(t, plainGrid(t, form), schedNameCol, agentScheduleName, schedAttachCol)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_attach_schedule @job_name = N'Nightly reindex', @schedule_name = N'Hourly'")
}

// Detaching silently stops the job running.
func TestJobSchedulesDetachesTheOneItWasAttachedTo(t *testing.T) {
	inst, apply, form := loadJobSchedulesPage(t)

	activateGridCell(t, plainGrid(t, form), schedNameCol, "Daily 01:00", schedAttachCol)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_detach_schedule @job_name = N'Nightly reindex', @schedule_name = N'Daily 01:00'")
}

// The Attached column comes from a different query and shows which way a toggle
// goes.
func TestJobSchedulesShowsWhichAreAlreadyAttached(t *testing.T) {
	_, _, form := loadJobSchedulesPage(t)
	grid := plainGrid(t, form)

	want := map[string]bool{"Daily 01:00": true, agentScheduleName: false, "Weekly Sunday": false}
	for name, attached := range want {
		row := grid.Row(gridRowIndex(t, grid, schedNameCol, name))
		if got := row[schedAttachCol] == mapCell(true); got != attached {
			t.Errorf("schedule %q shows attached=%v, want %v", name, got, attached)
		}
	}
}

// -- Alerts ------------------------------------------------------------------

const (
	alertLinkCol = 0
	alertNameCol = 1
)

func loadJobAlertsPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	inst, apply, form, _ := loadJobPage(t, agentAlertResponses(),
		func(sc *db.ServerConn, n *string) propPage { return pageJobAlerts(sc, n) })
	if plainGrid(t, form).Row(1) == nil {
		t.Fatal("the alert grid has fewer than two rows — the fake is under-scripted, not the page wrong")
	}
	return inst, apply, form
}

// The edit slice parallels the filtered list; a mismatch writes every toggle
// past the WMI alert to its neighbour.
func TestJobAlertsExcludesWMIAlerts(t *testing.T) {
	_, _, form := loadJobAlertsPage(t)
	grid := plainGrid(t, form)

	for i := 0; ; i++ {
		row := grid.Row(i)
		if row == nil {
			if i != 2 {
				t.Errorf("the grid has %d rows, want the two non-WMI alerts", i)
			}
			break
		}
		if row[alertNameCol] == "WMI deadlock" {
			t.Fatal("the WMI alert is listed on a page that cannot manage it")
		}
	}
}

// Links an alert currently responding to another job — the reassignment the
// page warns about.
func TestJobAlertsLinkingReassignsTheAlertToThisJob(t *testing.T) {
	inst, apply, form := loadJobAlertsPage(t)

	activateGridCell(t, plainGrid(t, form), alertNameCol, "Sev 17 errors", alertLinkCol)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_alert @name = N'Sev 17 errors', @job_name = N'Nightly reindex'")
}

// An empty @job_name is sp_update_alert's "no job response"; anything else
// fails as a missing job.
func TestJobAlertsUnlinkingClearsTheJob(t *testing.T) {
	inst, apply, form := loadJobAlertsPage(t)

	activateGridCell(t, plainGrid(t, form), alertNameCol, agentAlertName, alertLinkCol)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_alert @name = N'Sev 20 errors', @job_name = N''")
}
