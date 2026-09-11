package tui

import (
	"strconv"

	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// agent_job_step_panel.go is the "Selected step" edit panel shared by Job
// Properties' Steps page and New Job's.
//
// Shared because both map the same ten rows to jobStepEdit, and a mapping wrong
// in one copy writes wrong settings: action dropdowns are off by one against
// sp_add_jobstep (index i is action i+1), the Database dropdown's index 0 is a
// sentinel, and the numeric rows keep their old value on parse failure. The
// pages differ only in the Database sentinel, whether the panel is gated
// read-only, and the adjacent grid.

// jobStepPanel is a job step's ten rows plus the command editor, with the
// mapping to and from jobStepEdit.
type jobStepPanel struct {
	// sentinel leads the Database dropdown (unchangedDatabaseItem or
	// defaultDatabaseItem); both mean "send no @database_name" (empty in
	// JobStepRequest).
	sentinel string
	dbNames  []string

	// sqlHighlight is kept so the Properties page can restore it after a
	// non-T-SQL step.
	sqlHighlight controls.Highlighter

	nameField       *propsheet.TextRow
	databaseSelect  *propsheet.SelectRow
	commandEditor   *controls.Editor
	commandField    *propsheet.EditorRow
	onSuccessSelect *propsheet.SelectRow
	onSuccessStep   *propsheet.TextRow
	onFailSelect    *propsheet.SelectRow
	onFailStep      *propsheet.TextRow
	retryAttempts   *propsheet.TextRow
	retryInterval   *propsheet.TextRow
	outputFile      *propsheet.TextRow
}

// newJobStepPanel builds the panel. The sentinel leads the Database dropdown,
// so a database's dropdown index is its dbNames index plus one.
func newJobStepPanel(sentinel string, dbNames []string) *jobStepPanel {
	// The command gets the query editor (highlighting, line numbers) so "line
	// 12" errors can be found.
	sqlHighlight := controls.SQLHighlighter(theme.Active())
	editor := controls.NewEditor(sqlHighlight)
	p := &jobStepPanel{
		sentinel:        sentinel,
		dbNames:         dbNames,
		sqlHighlight:    sqlHighlight,
		nameField:       propsheet.Text("Step name", "", 30),
		databaseSelect:  propsheet.Select("Database", append([]string{sentinel}, dbNames...), 0),
		commandEditor:   editor,
		commandField:    propsheet.NewEditorRow("Command", editor, 12),
		onSuccessSelect: propsheet.Select("On success action", jobStepOnActionItems, 2),
		onSuccessStep:   propsheet.Int("On success go to step", 0, 0, 999, ""),
		onFailSelect:    propsheet.Select("On failure action", jobStepOnActionItems, 1),
		onFailStep:      propsheet.Int("On failure go to step", 0, 0, 999, ""),
		retryAttempts:   propsheet.Int("Retry attempts", 0, 0, 999, ""),
		retryInterval:   propsheet.Int("Retry interval", 0, 0, 999, "minutes"),
		outputFile:      propsheet.Text("Output file name", "", 40),
	}
	return p
}

// rows returns the rows in both pages' order.
func (p *jobStepPanel) rows() []propsheet.Row {
	return []propsheet.Row{
		p.nameField, p.databaseSelect, p.commandField,
		p.onSuccessSelect, p.onSuccessStep, p.onFailSelect, p.onFailStep,
		p.retryAttempts, p.retryInterval, p.outputFile,
	}
}

// database maps the dropdown to what gosmo is sent; the sentinel becomes "".
func (p *jobStepPanel) database() string {
	if v := p.databaseSelect.Value(); v != p.sentinel {
		return v
	}
	return ""
}

// setReadOnly gates the whole panel; any editable row under an unwritable step
// would take typing that's discarded.
func (p *jobStepPanel) setReadOnly(ro bool) {
	p.nameField.SetReadOnly(ro)
	p.databaseSelect.SetReadOnly(ro)
	p.commandField.SetReadOnly(ro)
	p.onSuccessSelect.SetReadOnly(ro)
	p.onSuccessStep.SetReadOnly(ro)
	p.onFailSelect.SetReadOnly(ro)
	p.onFailStep.SetReadOnly(ro)
	p.retryAttempts.SetReadOnly(ro)
	p.retryInterval.SetReadOnly(ro)
	p.outputFile.SetReadOnly(ro)
}

// clear resets the panel to a new step's defaults.
func (p *jobStepPanel) clear() {
	p.nameField.SetValue("")
	p.databaseSelect.SetSelected(0)
	p.commandField.SetValue("")
	p.onSuccessSelect.SetSelected(2)
	p.onSuccessStep.SetValue("0")
	p.onFailSelect.SetSelected(1)
	p.onFailStep.SetValue("0")
	p.retryAttempts.SetValue("0")
	p.retryInterval.SetValue("0")
	p.outputFile.SetValue("")
}

// read writes the panel into e. Steps this page can't edit return before any
// assignment, keeping them out of changed() and apply (sp_update_jobstep would
// turn a PowerShell step into T-SQL).
func (p *jobStepPanel) read(e *jobStepEdit) {
	if e == nil || !e.editable() {
		return
	}
	e.name = p.nameField.Value()
	e.database = p.database()
	e.command = p.commandField.Value()
	e.onSuccessAction = p.onSuccessSelect.Selected() + 1
	e.onFailAction = p.onFailSelect.Selected() + 1
	if n, err := p.onSuccessStep.IntValue(); err == nil {
		e.onSuccessStepID = int(n)
	}
	if n, err := p.onFailStep.IntValue(); err == nil {
		e.onFailStepID = int(n)
	}
	if n, err := p.retryAttempts.IntValue(); err == nil {
		e.retryAttempts = int(n)
	}
	if n, err := p.retryInterval.IntValue(); err == nil {
		e.retryInterval = int(n)
	}
	e.outputFileName = p.outputFile.Value()
}

// write loads e into the panel, or clears it for nil.
func (p *jobStepPanel) write(e *jobStepEdit) {
	if e == nil {
		p.clear()
		return
	}
	p.nameField.SetValue(e.name)
	// indexOfOK, not indexOf: an unlisted database selects the sentinel, not
	// the first database.
	if i, ok := indexOfOK(p.dbNames, e.database); ok {
		p.databaseSelect.SetSelected(i + 1)
	} else {
		p.databaseSelect.SetSelected(0)
	}
	p.commandField.SetValue(e.command)
	p.onSuccessSelect.SetSelected(e.onSuccessAction - 1)
	p.onSuccessStep.SetValue(strconv.Itoa(e.onSuccessStepID))
	p.onFailSelect.SetSelected(e.onFailAction - 1)
	p.onFailStep.SetValue(strconv.Itoa(e.onFailStepID))
	p.retryAttempts.SetValue(strconv.Itoa(e.retryAttempts))
	p.retryInterval.SetValue(strconv.Itoa(e.retryInterval))
	p.outputFile.SetValue(e.outputFileName)
}

// newStep builds a T-SQL step from the panel; the caller has checked the name.
func (p *jobStepPanel) newStep() *jobStepEdit {
	e := &jobStepEdit{isNew: true, subsystem: tsqlSubsystem}
	p.read(e)
	return e
}

// addStep is both Steps pages' New button: seed a step from the panel, refuse a
// duplicate name, and select the new row.
//
// A duplicate selects the existing row and re-syncs rather than failing
// silently, so the button never looks broken.
//
// It doesn't read the panel into the current step first (the name row is also
// the seed, so that would misfile a typed name as a rename). Callers check
// preconditions (e.g. a read-only step) first.
func (p *jobStepPanel) addStep(grid *controls.DataGrid, hint *propsheet.HintRow,
	cols []string, edits *[]*jobStepEdit, rowsFor func() [][]string, sync func()) {

	name := p.nameField.Value()
	if name == "" {
		hint.Set("Type a step name first.")
		return
	}
	for i, e := range visibleSteps(*edits) {
		if e.name == name {
			// Already present: say so and select it.
			hint.Set("A step named " + name + " is already listed — its row is selected below.")
			grid.SetSelectedRow(i)
			sync()
			return
		}
	}
	hint.Clear()
	*edits = append(*edits, p.newStep())
	resetGrid(grid, cols, rowsFor(), len(visibleSteps(*edits))-1)
	sync()
}

// visibleSteps is the edits minus pending removals, in order.
func visibleSteps(edits []*jobStepEdit) []*jobStepEdit {
	out := make([]*jobStepEdit, 0, len(edits))
	for _, e := range edits {
		if !e.pendingRemove {
			out = append(out, e)
		}
	}
	return out
}
