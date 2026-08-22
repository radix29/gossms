package tui

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The Steps page driven end to end. agent_job_props_steps_test.go pins
// planJobStepWrites in isolation; what only a page test can show is that the
// grid, the edit panel and the commit-on-selection wiring in between feed that
// plan the steps the user actually touched — and that the three passes reach
// the server in the order the plan puts them in.

const stepNameCol = 1

// jobStepRow is one row of the 17-column sysjobsteps SELECT.
func jobStepRow(id int64, name, subsystem, command, database string) []driver.Value {
	return []driver.Value{
		id, name, subsystem, command, database,
		int64(3), int64(0), int64(2), int64(0),
		int64(1), int64(0), int64(0), int64(0),
		int64(0), int64(0), "", int64(0),
	}
}

// jobStepsResponse scripts four steps: two ordinary T-SQL ones, a PowerShell
// step the page may list but never write, and a T-SQL step whose database is
// not in the server's list — the case the "(unchanged)" sentinel exists for.
func jobStepsResponse() fakeResponse {
	return fakeResponse{match: "FROM   msdb.dbo.sysjobsteps", cols: 17, rows: [][]driver.Value{
		jobStepRow(1, "Check integrity", tsqlSubsystem, "DBCC CHECKDB", "appdb"),
		jobStepRow(2, "Rebuild indexes", tsqlSubsystem, "EXEC dbo.usp_reindex", "appdb"),
		jobStepRow(3, "Notify ops", "PowerShell", "Send-MailMessage", ""),
		jobStepRow(4, "Update stats", tsqlSubsystem, "EXEC dbo.usp_stats", "archivedb"),
	}}
}

func loadJobStepsPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form, *controls.DataGrid) {
	t.Helper()
	job := jobRow(agentJobName, "Database Maintenance", "appuser", true, 0, 0, "")
	responses := append(agentJobResponses(job), jobStepsResponse(), agentDatabaseListResponse())
	sc, inst := newFakeConn(t, responses...)
	dialog, _ := newFakeDialog(t)
	name := agentJobName
	form, apply := loadPage(t, pageJobSteps(dialog, sc, &name), inst)
	grid := plainGrid(t, form)
	if grid.Row(3) == nil {
		t.Fatal("the step grid has fewer than four rows — the fake is under-scripted, not the page wrong")
	}
	return inst, apply, form, grid
}

// TestJobStepsRunsUpdatesThenDescendingDeletesThenAdds. sp_delete_jobstep
// renumbers every later step down by one, so a delete that ran before an
// update addresses a step_id that has moved — and ascending deletes make the
// second one delete the wrong step outright. The order is only observable from
// the statements themselves.
func TestJobStepsRunsUpdatesThenDescendingDeletesThenAdds(t *testing.T) {
	inst, apply, form, grid := loadJobStepsPage(t)

	// Edit step 1, delete steps 2 and 4, then add one.
	editText(t, form, "Command", "DBCC CHECKDB WITH NO_INFOMSGS")
	selectGridRow(t, grid, stepNameCol, "Rebuild indexes")
	clickButton(t, form, "Delete")
	selectGridRow(t, grid, stepNameCol, "Update stats")
	clickButton(t, form, "Delete")
	editText(t, form, "Step name", "Reorganize")
	editText(t, form, "Command", "EXEC dbo.usp_reorg")
	clickButton(t, form, "New")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 4 {
		t.Fatalf("want four statements, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	want := []string{
		"sp_update_jobstep @job_name = N'Nightly reindex', @step_id = 1",
		"sp_delete_jobstep @job_name = N'Nightly reindex', @step_id = 4",
		"sp_delete_jobstep @job_name = N'Nightly reindex', @step_id = 2",
		"sp_add_jobstep @job_name = N'Nightly reindex', @step_name = N'Reorganize'",
	}
	for i, w := range want {
		if !strings.Contains(stmts[i], w) {
			t.Errorf("statement %d:\n%s\nwant it to contain: %s", i+1, stmts[i], w)
		}
	}
	if !strings.Contains(stmts[0], "@command = N'DBCC CHECKDB WITH NO_INFOMSGS'") {
		t.Errorf("the update carried the wrong command:\n%s", stmts[0])
	}
}

// TestJobStepsCommitsTheStepTheGridMovedOffOf. The edit panel belongs to
// whichever row was selected when the typing happened, and moving the cursor
// is what files it — a commit that ran against the newly selected row would
// copy one step's command onto another.
func TestJobStepsCommitsTheStepTheGridMovedOffOf(t *testing.T) {
	inst, apply, form, grid := loadJobStepsPage(t)

	selectGridRow(t, grid, stepNameCol, "Rebuild indexes")
	editText(t, form, "Command", "EXEC dbo.usp_reindex_online")
	selectGridRow(t, grid, stepNameCol, "Check integrity")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@step_id = 2")
	if stmts := inst.Statements(); !strings.Contains(stmts[0], "@command = N'EXEC dbo.usp_reindex_online'") {
		t.Errorf("the edit landed with the wrong command:\n%s", stmts[0])
	}
}

// TestJobStepsNeverWritesANonTSQLStep. JobStepRequest carries a subsystem, so
// writing a PowerShell step back through this page's T-SQL-only form would
// hand its script to the query processor.
//
// Two guards stop it — commitCurrent refuses to copy the form onto the step,
// and planJobStepWrites tests editable() again — and either one alone is
// enough, so removing just one leaves this test passing. That is the point of
// stating the guard twice (see planJobStepWrites); the test kills the pair.
func TestJobStepsNeverWritesANonTSQLStep(t *testing.T) {
	inst, apply, form, grid := loadJobStepsPage(t)

	selectGridRow(t, grid, stepNameCol, "Notify ops")
	editText(t, form, "Command", "SELECT 1")
	selectGridRow(t, grid, stepNameCol, "Check integrity")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a read-only step was written back:\n%s", strings.Join(stmts, "\n"))
	}
}

// TestJobStepsUnchangedDatabaseIsNotSent. sp_update_jobstep leaves a parameter
// it was not passed exactly as it was, so omitting @database_name is the only
// way to say "leave it alone". Before the sentinel, a step whose database the
// dropdown could not show was rewritten to whichever database sorted first.
func TestJobStepsUnchangedDatabaseIsNotSent(t *testing.T) {
	inst, apply, form, grid := loadJobStepsPage(t)

	selectGridRow(t, grid, stepNameCol, "Update stats")
	if got := selectRow(t, form, "Database").Value(); got != unchangedDatabaseItem {
		t.Fatalf("a step on a database the list cannot show selects %q, want the sentinel", got)
	}
	editText(t, form, "Command", "EXEC dbo.usp_stats @full = 1")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@step_id = 4")
	if stmts := inst.Statements(); strings.Contains(stmts[0], "@database_name") {
		t.Errorf("the step's own database was rewritten:\n%s", stmts[0])
	}
}

// TestJobStepsAddCarriesTheDatabaseThatWasPicked — the New button reads the
// same panel the edit path does, and a new step created against the wrong
// database runs its T-SQL somewhere the user never chose.
func TestJobStepsAddCarriesTheDatabaseThatWasPicked(t *testing.T) {
	inst, apply, form, _ := loadJobStepsPage(t)

	editText(t, form, "Step name", "Purge archive")
	editText(t, form, "Command", "EXEC dbo.usp_purge")
	editSelect(t, form, "Database", "salesdb")
	clickButton(t, form, "New")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "sp_add_jobstep @job_name = N'Nightly reindex', @step_name = N'Purge archive'")
	if stmts := inst.Statements(); !strings.Contains(stmts[0], "@database_name = N'salesdb'") {
		t.Errorf("the new step went to the wrong database:\n%s", stmts[0])
	}
}

// TestJobStepsUntouchedPageWritesNothing. Every row of the edit panel is
// seeded from the selected step, so a field that came back subtly different
// from what it was loaded with would rewrite every step on every OK.
func TestJobStepsUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _ := loadJobStepsPage(t)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
