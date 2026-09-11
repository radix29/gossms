package tui

import (
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The Steps page end to end. agent_job_props_steps_test.go pins
// planJobStepWrites; this checks the grid, panel and commit-on-selection wiring
// feed it the right steps, and the passes reach the server in order.

const stepNameCol = 1

// jobStepRow is one row of the 23-column sysjobsteps SELECT. The last six
// (proxy, additional parameters, CmdExec success code, server, run-as user, OS
// priority) are read because a reorder carries them back through
// sp_add_jobstep.
func jobStepRow(id int64, name, subsystem, command, database string) []driver.Value {
	return []driver.Value{
		id, name, subsystem, command, database,
		int64(3), int64(0), int64(2), int64(0),
		int64(1), int64(0), int64(0), int64(0),
		int64(0), int64(0), "", int64(0),
		"", "", int64(0), "", "", int64(0),
	}
}

// jobStepsResponse scripts four steps: two T-SQL, a PowerShell step (listed,
// never written), and a T-SQL step whose database isn't listed (the
// "(unchanged)" case).
func jobStepsResponse() fakeResponse {
	return fakeResponse{match: "FROM   msdb.dbo.sysjobsteps", cols: 23, rows: [][]driver.Value{
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

// sp_delete_jobstep renumbers later steps, so updates precede deletes, which
// run descending. Only the statements show the order.
func TestJobStepsRunsUpdatesThenDescendingDeletesThenAdds(t *testing.T) {
	inst, apply, form, grid := loadJobStepsPage(t)

	// Edit step 1, delete 2 and 4, add one.
	editEditor(t, form, "Command", "DBCC CHECKDB WITH NO_INFOMSGS")
	selectGridRow(t, grid, stepNameCol, "Rebuild indexes")
	clickButton(t, form, "Delete")
	selectGridRow(t, grid, stepNameCol, "Update stats")
	clickButton(t, form, "Delete")
	editText(t, form, "Step name", "Reorganize")
	editEditor(t, form, "Command", "EXEC dbo.usp_reorg")
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

// Moving the cursor files the panel to the row being left; committing to the
// new row would copy one step's command onto another.
func TestJobStepsCommitsTheStepTheGridMovedOffOf(t *testing.T) {
	inst, apply, form, grid := loadJobStepsPage(t)

	selectGridRow(t, grid, stepNameCol, "Rebuild indexes")
	editEditor(t, form, "Command", "EXEC dbo.usp_reindex_online")
	selectGridRow(t, grid, stepNameCol, "Check integrity")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@step_id = 2")
	if stmts := inst.Statements(); !strings.Contains(stmts[0], "@command = N'EXEC dbo.usp_reindex_online'") {
		t.Errorf("the edit landed with the wrong command:\n%s", stmts[0])
	}
}

// A PowerShell step written back through the T-SQL form would run its script as
// T-SQL. Two guards (commitCurrent and planJobStepWrites' editable check) each
// suffice; this test fails only if both go.
func TestJobStepsNeverWritesANonTSQLStep(t *testing.T) {
	inst, apply, form, grid := loadJobStepsPage(t)

	selectGridRow(t, grid, stepNameCol, "Notify ops")
	editEditor(t, form, "Command", "SELECT 1")
	selectGridRow(t, grid, stepNameCol, "Check integrity")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a read-only step was written back:\n%s", strings.Join(stmts, "\n"))
	}
}

// Omitting @database_name is the only way to leave it unchanged; an unlisted
// database must not be rewritten to the first one.
func TestJobStepsUnchangedDatabaseIsNotSent(t *testing.T) {
	inst, apply, form, grid := loadJobStepsPage(t)

	selectGridRow(t, grid, stepNameCol, "Update stats")
	if got := selectRow(t, form, "Database").Value(); got != unchangedDatabaseItem {
		t.Fatalf("a step on a database the list cannot show selects %q, want the sentinel", got)
	}
	editEditor(t, form, "Command", "EXEC dbo.usp_stats @full = 1")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "@step_id = 4")
	if stmts := inst.Statements(); strings.Contains(stmts[0], "@database_name") {
		t.Errorf("the step's own database was rewritten:\n%s", stmts[0])
	}
}

// New reads the same panel; a new step on the wrong database runs its T-SQL
// somewhere unchosen.
func TestJobStepsAddCarriesTheDatabaseThatWasPicked(t *testing.T) {
	inst, apply, form, _ := loadJobStepsPage(t)

	editText(t, form, "Step name", "Purge archive")
	editEditor(t, form, "Command", "EXEC dbo.usp_purge")
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

// A panel field that differs subtly from its load would rewrite every step on
// every OK.
func TestJobStepsUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _, _ := loadJobStepsPage(t)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// Move Up/Down reorder the page; the write is a fourth pass addressing msdb's
// future numbers, as a delete plus insert (msdb can't renumber in place).
//
// gosmo sends a reorder as one transactional batch (the definition exists only
// in memory in between; see gosmo's atomicBatch). This asserts
// delete-before-insert order within it.
func TestJobStepsMoveUpReordersOnTheServer(t *testing.T) {
	inst, apply, form, grid := loadJobStepsPage(t)

	// Rebuild indexes is step 2; up makes it 1.
	selectGridRow(t, grid, stepNameCol, "Rebuild indexes")
	clickButton(t, form, "Move Up")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want one batch carrying the delete and the insert, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	batch := stmts[0]
	if !strings.Contains(batch, "BEGIN TRANSACTION") || !strings.Contains(batch, "COMMIT TRANSACTION") {
		t.Errorf("the reorder was not sent as a transaction, so a failure between the "+
			"delete and the insert loses the step:\n%s", batch)
	}
	del := strings.Index(batch, "sp_delete_jobstep @job_name = N'Nightly reindex', @step_id = 2")
	if del < 0 {
		t.Errorf("the step is not removed from its old position:\n%s", batch)
	}
	add := strings.Index(batch, "sp_add_jobstep")
	if add < 0 || add < del {
		t.Errorf("the re-insert is missing or comes before the delete:\n%s", batch)
	}
	// The re-add must carry the full definition; omissions get defaulted.
	for _, want := range []string{"@step_id = 1", "@step_name = N'Rebuild indexes'", "@command = N'EXEC dbo.usp_reindex'", "@database_name = N'appdb'"} {
		if !strings.Contains(batch[add:], want) {
			t.Errorf("the re-inserted step lost %s:\n%s", want, batch)
		}
	}
}

// Moving a step and back writes nothing; orders are compared, not clicks.
func TestJobStepsMoveThereAndBackWritesNothing(t *testing.T) {
	inst, apply, form, grid := loadJobStepsPage(t)

	selectGridRow(t, grid, stepNameCol, "Rebuild indexes")
	clickButton(t, form, "Move Up")
	clickButton(t, form, "Move Down")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("statements = %v, want none", stmts)
	}
}

// reorderedStepIDs uses post-pass numbering: removed steps close gaps and new
// steps go last.
func TestReorderedStepIDsUsesThePostApplyNumbering(t *testing.T) {
	step := func(id int) *jobStepEdit {
		return &jobStepEdit{orig: &gosmo.JobStep{StepID: id}, stepID: id}
	}
	a, b, c := step(1), step(2), step(3)
	b.pendingRemove = true
	d := &jobStepEdit{isNew: true, name: "added"}

	// Page order c, a, d with b removed: a and c become 1 and 2, the new step
	// 3.
	got := reorderedStepIDs([]*jobStepEdit{c, a, b, d})
	want := []int{2, 1, 3}
	if !slices.Equal(got, want) {
		t.Errorf("reorderedStepIDs = %v, want %v", got, want)
	}

	// The same set in its own order isn't a reorder.
	if got := reorderedStepIDs([]*jobStepEdit{a, c, b, d}); got != nil {
		t.Errorf("reorderedStepIDs = %v for an unchanged order, want nil", got)
	}
}

// A non-T-SQL step's command refuses typing that would be discarded (and whose
// text a write-back would run).
func TestANonTSQLStepsCommandRefusesTyping(t *testing.T) {
	_, _, form, grid := loadJobStepsPage(t)
	row := editorRow(t, form, "Command")
	typeX := func() { row.HandleKey(tcell.NewEventKey(tcell.KeyRune, "X", tcell.ModNone)) }

	selectGridRow(t, grid, stepNameCol, "Notify ops")
	before := row.Value()
	typeX()
	if row.Value() != before {
		t.Errorf("a PowerShell step's command took typing: %q", row.Value())
	}

	// The gate lifts on a T-SQL step.
	selectGridRow(t, grid, stepNameCol, "Check integrity")
	before = row.Value()
	typeX()
	if row.Value() == before {
		t.Error("a T-SQL step's command refused typing")
	}
}

// The whole panel refuses typing for a non-T-SQL step (commitCurrent reads
// every row), and the gate lifts again on a T-SQL step.
func TestANonTSQLStepsWholeEditPanelRefusesTyping(t *testing.T) {
	inst, apply, form, grid := loadJobStepsPage(t)

	texts := [][2]string{
		{"Step name", "Renamed by the test"},
		{"On success go to step", "7"},
		{"On failure go to step", "9"},
		{"Retry attempts", "5"},
		{"Retry interval", "6"},
		{"Output file name", "/tmp/step.out"},
	}
	selects := [][2]string{
		{"Database", "salesdb"},
		{"On success action", jobStepOnActionItems[0]},
		{"On failure action", jobStepOnActionItems[2]},
	}

	selectGridRow(t, grid, stepNameCol, "Notify ops")
	for _, tc := range texts {
		row := textRow(t, form, tc[0])
		before := row.Value()
		row.Edit(tc[1])
		if row.Value() != before {
			t.Errorf("a PowerShell step's %q took an edit: %q", tc[0], row.Value())
		}
		if row.Dirty() {
			t.Errorf("a PowerShell step's %q went dirty", tc[0])
		}
	}
	for _, tc := range selects {
		row := selectRow(t, form, tc[0])
		before := row.Value()
		row.Edit(slices.Index(row.Items(), tc[1]))
		if row.Value() != before {
			t.Errorf("a PowerShell step's %q took an edit: %q", tc[0], row.Value())
		}
		if row.Dirty() {
			t.Errorf("a PowerShell step's %q went dirty", tc[0])
		}
	}

	// Move off the row so commitCurrent runs, then apply: nothing may reach the
	// step or server.
	selectGridRow(t, grid, stepNameCol, "Check integrity")
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a read-only step's panel reached the server:\n%s", strings.Join(stmts, "\n"))
	}

	// The gate lifts on "Check integrity", selected above.
	for _, tc := range texts {
		editText(t, form, tc[0], tc[1])
	}
	for _, tc := range selects {
		editSelect(t, form, tc[0], tc[1])
	}
}
