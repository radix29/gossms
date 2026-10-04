package tui

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/db"
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
	form, apply := loadPage(t, pageScheduleGeneral(sc, agentScheduleID, &name), inst)
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

// The page applies as one sp_update_schedule, addressed by the schedule_id it
// loaded, and the shared name cell follows the rename — or the reload looks
// for a vanished name.
func TestScheduleGeneralAppliesEveryEditInOneStatementUnderTheLoadedID(t *testing.T) {
	inst, apply, form, name := loadSchedulePage(t)

	editSelect(t, form, "Owner", "otheruser")
	editText(t, form, "Name", "Hourly (business hours)")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst,
		"@schedule_id = 7, @new_name = N'Hourly (business hours)', @owner_login_name = N'otheruser'")
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

// twinScheduleResponses scripts two schedules both named Daily — msdb allows
// it, and SSMS's New Job ▸ Schedules makes one per job — with ids 7 and 8.
// The by-name answer is deliberately the first, so an action that resolved
// the node by name would hit schedule 7.
func twinScheduleResponses() []fakeResponse {
	twins := [][]driver.Value{
		scheduleRow(7, "Daily", 4, 1, 0, "appuser"),
		scheduleRow(8, "Daily", 4, 1, 0, "appuser"),
	}
	return append(scheduleByIDResponses(twins),
		fakeResponse{match: "WHERE sch.name = @p1", cols: 16, rows: twins},
		fakeResponse{match: "FROM   msdb.dbo.sysschedules sch", cols: 16, rows: twins},
		fakeResponse{match: "sysjobschedules js ON js.schedule_id", cols: 3},
	)
}

// twinScheduleNode is Object Explorer's node for the second Daily, id 8.
func twinScheduleNode(sc *db.ServerConn) *explorerNode {
	n := opNode(NodeAgentSchedule, "", "Daily", "")
	n.data.conn = sc
	n.data.AgentScheduleID = 8
	parent := &explorerNode{label: "Schedules"}
	n.parent = parent
	parent.children = []*explorerNode{n}
	return n
}

// Two nodes labelled Daily stand for two schedules. Each action on the
// second must reach schedule 8 and nothing else: by name, Disable and Delete
// would read whichever row came back first and write to schedule 7.
func TestScheduleActionsOnATwinNameHitTheNodesOwnID(t *testing.T) {
	for _, c := range []struct {
		name, want string
		act        func(a *App, sc *db.ServerConn, n *explorerNode)
	}{
		{"disable", "sp_update_schedule @schedule_id = 8, @enabled = 0",
			func(a *App, sc *db.ServerConn, n *explorerNode) { a.setAgentScheduleEnabled(sc, n, false) }},
		{"delete", "sp_delete_schedule @schedule_id = 8",
			func(a *App, sc *db.ServerConn, n *explorerNode) {
				a.deleteAgentSchedule(sc, n)
				a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone)) // Yes
			}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			sc, inst := newFakeConn(t, twinScheduleResponses()...)
			c.act(a, sc, twinScheduleNode(sc))
			waitAndDrain(t, a)
			assertOneStatement(t, inst, c.want)
			if a.statusText != "" && strings.Contains(a.statusText, "failed") {
				t.Errorf("status = %q", a.statusText)
			}
		})
	}
}

// The detail pane and Schedule Properties read the node's own schedule; the
// Properties apply writes it by id.
func TestScheduleReadsOnATwinNameUseTheNodesOwnID(t *testing.T) {
	sc, inst := newFakeConn(t, append(twinScheduleResponses(), loginListResponse())...)
	if _, _, err := agentScheduleDetail(t.Context(), sc, twinScheduleNode(sc)); err != nil {
		t.Fatalf("detail pane: %v", err)
	}
	if args, ok := inst.ReadArgs("WHERE sch.schedule_id = @p1"); !ok || len(args) != 1 || args[0].Value != int64(8) {
		t.Errorf("detail pane read schedule %v, want id 8", args)
	}
	if reads := inst.Reads("WHERE sch.name = @p1"); len(reads) != 0 {
		t.Errorf("a schedule was read by name: %v", reads)
	}

	name := "Daily"
	form, apply := loadPage(t, pageScheduleGeneral(sc, 8, &name), inst)
	editCheck(t, form, "Enabled", false)
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_update_schedule @schedule_id = 8, @enabled = 0")
}
