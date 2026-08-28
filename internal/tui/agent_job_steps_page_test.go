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

// The Steps page driven end to end. agent_job_props_steps_test.go pins
// planJobStepWrites in isolation; what only a page test can show is that the
// grid, the edit panel and the commit-on-selection wiring in between feed that
// plan the steps the user actually touched — and that the three passes reach
// the server in the order the plan puts them in.

const stepNameCol = 1

// jobStepRow is one row of the 23-column sysjobsteps SELECT. The last six —
// proxy, additional parameters, CmdExec success code, server, run-as user and
// OS run priority — are what a reorder has to carry back through
// sp_add_jobstep, which is why they are read at all.
func jobStepRow(id int64, name, subsystem, command, database string) []driver.Value {
	return []driver.Value{
		id, name, subsystem, command, database,
		int64(3), int64(0), int64(2), int64(0),
		int64(1), int64(0), int64(0), int64(0),
		int64(0), int64(0), "", int64(0),
		"", "", int64(0), "", "", int64(0),
	}
}

// jobStepsResponse scripts four steps: two ordinary T-SQL ones, a PowerShell
// step the page may list but never write, and a T-SQL step whose database is
// not in the server's list — the case the "(unchanged)" sentinel exists for.
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

// TestJobStepsRunsUpdatesThenDescendingDeletesThenAdds. sp_delete_jobstep
// renumbers every later step down by one, so a delete that ran before an
// update addresses a step_id that has moved — and ascending deletes make the
// second one delete the wrong step outright. The order is only observable from
// the statements themselves.
func TestJobStepsRunsUpdatesThenDescendingDeletesThenAdds(t *testing.T) {
	inst, apply, form, grid := loadJobStepsPage(t)

	// Edit step 1, delete steps 2 and 4, then add one.
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

// TestJobStepsCommitsTheStepTheGridMovedOffOf. The edit panel belongs to
// whichever row was selected when the typing happened, and moving the cursor
// is what files it — a commit that ran against the newly selected row would
// copy one step's command onto another.
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
	editEditor(t, form, "Command", "SELECT 1")
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
	editEditor(t, form, "Command", "EXEC dbo.usp_stats @full = 1")

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

// TestJobStepsMoveUpReordersOnTheServer. Move Up/Move Down only rearrange the
// page's own list; the write is a fourth pass, and it addresses steps by the
// numbers msdb will have — a delete and an insert, since msdb has no procedure
// that renumbers a step in place.
//
// Both arrive in one statement: gosmo sends a reorder as a single transactional
// batch, because between the delete and the insert the step's definition exists
// nowhere but in memory. See gosmo's atomicBatch. The order *within* the batch
// is what this asserts — the delete of the old position before the insert at
// the new one — since the two are not interchangeable.
func TestJobStepsMoveUpReordersOnTheServer(t *testing.T) {
	inst, apply, form, grid := loadJobStepsPage(t)

	// Rebuild indexes is step 2; moving it up makes it step 1.
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
	// The re-add has to carry the step's own definition: it is a new row, so
	// anything left out is defaulted away rather than kept.
	for _, want := range []string{"@step_id = 1", "@step_name = N'Rebuild indexes'", "@command = N'EXEC dbo.usp_reindex'", "@database_name = N'appdb'"} {
		if !strings.Contains(batch[add:], want) {
			t.Errorf("the re-inserted step lost %s:\n%s", want, batch)
		}
	}
}

// Moving a step and moving it back is not a change, and must write nothing —
// the page compares orders, not clicks.
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

// reorderedStepIDs is the part a server cannot check: the ids it names are the
// ones the other three passes leave behind, not the ones the page loaded. A
// removed step closes its gap and a new step lands at the end, so the page's
// display numbers say nothing about what the reorder has to send.
func TestReorderedStepIDsUsesThePostApplyNumbering(t *testing.T) {
	step := func(id int) *jobStepEdit {
		return &jobStepEdit{orig: &gosmo.JobStep{StepID: id}, stepID: id}
	}
	a, b, c := step(1), step(2), step(3)
	b.pendingRemove = true
	d := &jobStepEdit{isNew: true, name: "added"}

	// Page order: c, a, d — with b removed, a and c become steps 1 and 2 and
	// the new step is 3.
	got := reorderedStepIDs([]*jobStepEdit{c, a, b, d})
	want := []int{2, 1, 3}
	if !slices.Equal(got, want) {
		t.Errorf("reorderedStepIDs = %v, want %v", got, want)
	}

	// The same set left in its own order is not a reorder at all.
	if got := reorderedStepIDs([]*jobStepEdit{a, c, b, d}); got != nil {
		t.Errorf("reorderedStepIDs = %v for an unchanged order, want nil", got)
	}
}

// TestANonTSQLStepsCommandRefusesTyping. The page can write back a T-SQL step
// only, and it says so in the hint — but a box that takes typing it will throw
// away is the "a keypress that does nothing" case, and the command is the field
// whose text a write-back would hand to the query processor.
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

	// And the gate lifts again — a page that never re-enabled it would pass the
	// half above and make every T-SQL step read-only.
	selectGridRow(t, grid, stepNameCol, "Check integrity")
	before = row.Value()
	typeX()
	if row.Value() == before {
		t.Error("a T-SQL step's command refused typing")
	}
}

// TestANonTSQLStepsWholeEditPanelRefusesTyping. The command was gated first
// because its text is what a write-back hands to the query processor, but every
// other row on the panel is written by commitCurrent too — so a row still
// taking typing is typing the page throws away. This pins the whole panel, and
// the second half pins the gate lifting again: a page that never re-enabled the
// rows would pass the first half and make every T-SQL step read-only.
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

	// Move off the row so commitCurrent runs against it, then apply: a refused
	// edit must reach neither the step nor the server.
	selectGridRow(t, grid, stepNameCol, "Check integrity")
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a read-only step's panel reached the server:\n%s", strings.Join(stmts, "\n"))
	}

	// The gate lifts on a T-SQL step — "Check integrity" is selected above.
	for _, tc := range texts {
		editText(t, form, tc[0], tc[1])
	}
	for _, tc := range selects {
		editSelect(t, form, tc[0], tc[1])
	}
}
