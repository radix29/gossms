package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Schedule Properties > General end to end. agent_schedule_form_test.go pins
// the form's round trip, which can't see a swapped table entry (Monday's box
// setting Tuesday's bit); only the statement sent shows the ticked day's bit.

func loadSchedulePage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form, *string) {
	t.Helper()
	responses := append(agentScheduleResponses(), loginListResponse())
	sc, inst := newFakeConn(t, responses...)
	name := agentScheduleName
	form, apply := loadPage(t, pageScheduleGeneral(sc, &name), inst)
	return inst, apply, form, &name
}

// The schedule is Daily, so weekdays start Mon-Fri; unticking three leaves
// Monday and Wednesday, 2|8. Any rotation of the table changes this.
func TestScheduleGeneralWeeklyWritesTheBitsOfTheDaysThatWereTicked(t *testing.T) {
	inst, apply, form, _ := loadSchedulePage(t)

	editSelect(t, form, "Occurs", "Weekly")
	tg := toggleGrid(t, form)
	for _, day := range []string{"Tuesday", "Thursday", "Friday"} {
		toggleByName(t, tg, day, 0)
	}

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst,
		"sp_update_schedule @schedule_id = 7, @freq_type = 8, @freq_interval = 10, "+
			"@freq_subday_type = 1, @freq_subday_interval = 1, "+
			"@freq_relative_interval = 0, @freq_recurrence_factor = 1")
}

// sp_update_schedule takes an id, so a page that lost its schedule would still
// emit a well-formed statement for schedule 0 or the first one.
func TestScheduleGeneralAddressesTheScheduleItLoaded(t *testing.T) {
	inst, apply, form, _ := loadSchedulePage(t)

	editText(t, form, "Start time (HH:MM:SS)", "02:30:00")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_schedule @schedule_id = 7, @active_start_date = 20260101, "+
		"@active_end_date = 99991231, @active_start_time = 23000, @active_end_time = 235959")
}

// 99991231 is msdb's "runs forever"; keeping it after the user set an end date
// never stops the schedule.
func TestScheduleGeneralGivingAnEndDateReplacesTheNoEndDateSentinel(t *testing.T) {
	inst, apply, form, _ := loadSchedulePage(t)

	editCheck(t, form, "No end date", false)
	editText(t, form, "End date (YYYY-MM-DD)", "2026-12-31")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@active_start_date = 20260101, @active_end_date = 20261231")
}

// Rename is last and the shared name cell follows it, or the reload looks for a
// vanished name.
func TestScheduleGeneralRenamesLastAndUnderTheLoadedID(t *testing.T) {
	inst, apply, form, name := loadSchedulePage(t)

	editSelect(t, form, "Owner", "otheruser")
	editText(t, form, "Name", "Hourly (business hours)")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 2 {
		t.Fatalf("want two statements, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], "@schedule_id = 7, @owner_login_name = N'otheruser'") {
		t.Errorf("first statement:\n%s", stmts[0])
	}
	if !strings.Contains(stmts[1], "@schedule_id = 7, @new_name = N'Hourly (business hours)'") {
		t.Errorf("the rename should run last:\n%s", stmts[1])
	}
	if *name != "Hourly (business hours)" {
		t.Errorf("the shared name cell is still %q after the rename", *name)
	}
}

// A shared schedule serves every attached job, so one phantom write moves them
// all.
func TestScheduleGeneralUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _ := loadSchedulePage(t)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
