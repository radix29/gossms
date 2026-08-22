package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Schedule Properties > General, driven end to end.
//
// agent_schedule_form_test.go pins the frequency form's own round trip, and a
// round trip cannot see a fault in a table both halves read — swapping two
// entries in weekdayBits leaves the checkbox labelled Monday setting Tuesday's
// bit and populate/readFrequency still agree. What only a page test can show
// is the statement that reaches the server: the day the user ticked, by name,
// against the bit msdb stores.

func loadSchedulePage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form, *string) {
	t.Helper()
	responses := append(agentScheduleResponses(), loginListResponse())
	sc, inst := newFakeConn(t, responses...)
	name := agentScheduleName
	form, apply := loadPage(t, pageScheduleGeneral(sc, &name), inst)
	return inst, apply, form, &name
}

// TestScheduleGeneralWeeklyWritesTheBitsOfTheDaysThatWereTicked. The scripted
// schedule is Daily, so the weekday grid starts on the Mon-Fri default:
// unticking three of them leaves Monday and Wednesday, which msdb stores as
// 2|8. Any rotation of the weekday table changes this number.
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

// TestScheduleGeneralAddressesTheScheduleItLoaded. sp_update_schedule takes an
// id, not a name, so a page that lost the schedule it loaded would still emit
// a statement that looks entirely well-formed — pointed at schedule 0, or at
// whichever schedule sorts first.
func TestScheduleGeneralAddressesTheScheduleItLoaded(t *testing.T) {
	inst, apply, form, _ := loadSchedulePage(t)

	editText(t, form, "Start time (HH:MM:SS)", "02:30:00")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_schedule @schedule_id = 7, @active_start_date = 20260101, "+
		"@active_end_date = 99991231, @active_start_time = 23000, @active_end_time = 235959")
}

// TestScheduleGeneralGivingAnEndDateReplacesTheNoEndDateSentinel. 99991231 is
// msdb's own "runs forever"; a schedule that keeps it after the user set an
// end date never stops.
func TestScheduleGeneralGivingAnEndDateReplacesTheNoEndDateSentinel(t *testing.T) {
	inst, apply, form, _ := loadSchedulePage(t)

	editCheck(t, form, "No end date", false)
	editText(t, form, "End date (YYYY-MM-DD)", "2026-12-31")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@active_start_date = 20260101, @active_end_date = 20261231")
}

// TestScheduleGeneralRenamesLastAndUnderTheLoadedID. The rename is the last
// write of the run and the shared name cell has to follow it, or the reload
// after Apply looks for a schedule that no longer answers to that name.
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

// TestScheduleGeneralUntouchedPageWritesNothing. This page is the worst place
// for a row that loads dirty: a shared schedule is attached to every job that
// uses it, so one phantom write moves them all.
func TestScheduleGeneralUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _ := loadSchedulePage(t)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
