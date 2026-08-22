package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Page-level tests for Job Properties — General, Notifications, Schedules and
// Alerts. Steps has its own file: agent_job_props_steps_test.go covers the
// write *plan* in isolation, agent_job_steps_page_test.go the page end to end.

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

// TestJobGeneralRenamesLastAndUnderTheOldName. Every other write on the page
// addresses the job by name, so a rename that ran first would leave them
// pointing at a job that no longer exists — and the shared name cell has to
// end up on the new name, or the reload after Apply re-fetches a job that is
// gone.
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

// TestJobGeneralWritesTheOwnerAndCategoryThatWerePicked names both, because
// each is a dropdown read back by index into a list the page built.
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

// TestJobGeneralDisablingSendsEnabledZero — the checkbox picks between two
// different gosmo calls rather than passing a value, so an inverted reading
// here is a job left running that the user switched off.
func TestJobGeneralDisablingSendsEnabledZero(t *testing.T) {
	inst, apply, form, _ := loadJobPage(t, jobGeneralResponses(), pageJobGeneralFor)

	editCheck(t, form, "Enabled", false)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@job_name = N'Nightly reindex', @enabled = 0")
}

// TestJobGeneralUntouchedPageWritesNothing. Every row here is seeded from the
// job, and a row that reported itself dirty on load would rewrite the job on
// every OK — including its owner, a security-relevant change nobody asked for.
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

// TestJobNotificationsDeleteConditionDoesNotRewriteTheEmailOperator is the
// page's own stated invariant: the two sections are gated on their own rows'
// dirtiness, because PropertySheet.DirtyPages only knows about pages.
func TestJobNotificationsDeleteConditionDoesNotRewriteTheEmailOperator(t *testing.T) {
	inst, apply, form, _ := loadJobPage(t, agentOperatorResponses(), pageJobNotificationsFor)

	editCheck(t, form, "Delete job", true)
	editSelect(t, form, "When to delete", "When the job succeeds")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@delete_level = 1")
}

// TestJobNotificationsEmailWithNoOperatorSendsNoOperatorName. sp_update_job
// has no value that clears an operator, so "" means "leave it alone" — and
// before the noneItem sentinel, ticking E-mail on a job with no operator sent
// whichever operator sorted first.
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

// TestJobNotificationsEmailsTheOperatorThatWasPicked picks the second of three
// operators: a page that ignored the selection would still name an operator.
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

// TestJobSchedulesAttachesTheScheduleTheRowIsOn toggles the *second* schedule.
// A page that read the grid back against the wrong slice attaches a schedule
// the user never picked, and the job then runs on somebody else's cadence.
func TestJobSchedulesAttachesTheScheduleTheRowIsOn(t *testing.T) {
	inst, apply, form := loadJobSchedulesPage(t)

	activateGridCell(t, plainGrid(t, form), schedNameCol, agentScheduleName, schedAttachCol)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_attach_schedule @job_name = N'Nightly reindex', @schedule_name = N'Hourly'")
}

// TestJobSchedulesDetachesTheOneItWasAttachedTo. Detaching is the destructive
// direction: the job silently stops running.
func TestJobSchedulesDetachesTheOneItWasAttachedTo(t *testing.T) {
	inst, apply, form := loadJobSchedulesPage(t)

	activateGridCell(t, plainGrid(t, form), schedNameCol, "Daily 01:00", schedAttachCol)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_detach_schedule @job_name = N'Nightly reindex', @schedule_name = N'Daily 01:00'")
}

// TestJobSchedulesShowsWhichAreAlreadyAttached. The Attached column is what
// tells the user which way a toggle will go, and it comes from a different
// query than the list itself.
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

// TestJobAlertsExcludesWMIAlerts. The page's edit slice is index-parallel with
// the *filtered* list, so if the filter and the grid ever disagreed every
// toggle past the WMI alert would write to its neighbour.
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

// TestJobAlertsLinkingReassignsTheAlertToThisJob links the alert that
// currently responds to a *different* job — the reassignment the page's own
// note warns about, and the one worth landing on the right alert.
func TestJobAlertsLinkingReassignsTheAlertToThisJob(t *testing.T) {
	inst, apply, form := loadJobAlertsPage(t)

	activateGridCell(t, plainGrid(t, form), alertNameCol, "Sev 17 errors", alertLinkCol)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_alert @name = N'Sev 17 errors', @job_name = N'Nightly reindex'")
}

// TestJobAlertsUnlinkingClearsTheJob. An empty @job_name is sp_update_alert's
// own "no job response" sentinel; anything else fails as a missing job.
func TestJobAlertsUnlinkingClearsTheJob(t *testing.T) {
	inst, apply, form := loadJobAlertsPage(t)

	activateGridCell(t, plainGrid(t, form), alertNameCol, agentAlertName, alertLinkCol)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_alert @name = N'Sev 20 errors', @job_name = N''")
}
