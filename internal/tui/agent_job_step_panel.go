package tui

import (
	"strconv"

	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// agent_job_step_panel.go is the "Selected step" edit panel, shared by the job
// Properties Steps page (agent_job_props_steps.go) and New Job's Steps page
// (new_job_pages.go).
//
// One type rather than a copy per page, because both pages read the same ten
// rows back into a jobStepEdit field by field and a mapping that is right in
// one copy and wrong in the other writes a step with somebody else's settings:
// the action dropdowns are off-by-one against sp_add_jobstep's encoding on
// purpose (index i is action i+1), the Database dropdown's index 0 is a
// sentinel and not a database, and the four numeric rows are read through
// IntValue and silently keep their old value when it fails to parse. The pages
// differ in three things, all of them parameters or the caller's business:
// which sentinel leads the Database dropdown, whether the panel is ever gated
// read-only, and what the grid beside it shows.

// jobStepPanel is the ten rows of a job step's definition plus the command
// editor, with the mapping to and from jobStepEdit.
type jobStepPanel struct {
	// sentinel is the Database dropdown's leading item — unchangedDatabaseItem
	// on the Properties page, defaultDatabaseItem on New Job. Both mean "send
	// no @database_name", which JobStepRequest spells as the empty string.
	sentinel string
	dbNames  []string

	// sqlHighlight is kept so the Properties page can put it back after
	// clearing it for a non-T-SQL step.
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

// newJobStepPanel builds the panel. sentinel leads the Database dropdown and
// dbNames follows it, so a database's index in the dropdown is its index here
// plus one.
func newJobStepPanel(sentinel string, dbNames []string) *jobStepPanel {
	// The command is a whole T-SQL script, not a field: it gets the query
	// editor control, with SQL highlighting and the line-number gutter, so a
	// syntax error reported as "line 12" can be found.
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

// rows returns the panel's rows in the order both pages lay them out.
func (p *jobStepPanel) rows() []propsheet.Row {
	return []propsheet.Row{
		p.nameField, p.databaseSelect, p.commandField,
		p.onSuccessSelect, p.onSuccessStep, p.onFailSelect, p.onFailStep,
		p.retryAttempts, p.retryInterval, p.outputFile,
	}
}

// database maps the dropdown back to what gosmo is sent: the sentinel means
// "leave it to the server", which JobStepRequest spells as the empty string.
func (p *jobStepPanel) database() string {
	if v := p.databaseSelect.Value(); v != p.sentinel {
		return v
	}
	return ""
}

// setReadOnly gates the whole panel as one. Every row here is written back by
// read, so a row left editable under a step the page cannot write takes typing
// that is silently discarded — the command was gated first and the rest
// followed.
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

// clear empties the panel, back to the defaults a new step starts from.
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

// read writes the panel's current contents into e.
//
// A step this page can't edit is never written back. Returning before the first
// assignment keeps it out of changed() and so out of apply — otherwise
// sp_update_jobstep turns a PowerShell step into a T-SQL one carrying its old
// script.
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

// write loads e into the panel, or clears it when e is nil.
func (p *jobStepPanel) write(e *jobStepEdit) {
	if e == nil {
		p.clear()
		return
	}
	p.nameField.SetValue(e.name)
	// indexOfOK, not indexOf: the dropdown is a closed set with a sentinel at
	// 0, so a database it can't show selects the sentinel rather than the first
	// real database.
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

// newStep builds a new T-SQL step from what is in the panel, name included —
// the caller has already checked that name against the list.
func (p *jobStepPanel) newStep() *jobStepEdit {
	e := &jobStepEdit{isNew: true, subsystem: tsqlSubsystem}
	p.read(e)
	return e
}

// addStep is the New button's body on both Steps pages: seed a step from the
// panel as it stands, refuse a name already in the list, and leave the new row
// selected with the panel showing it.
//
// Shared for the reason the panel itself is. The duplicate-name branch is the
// part that matters: it selects the existing row and re-syncs rather than
// returning silently, so the button never looks broken, and a copy that only
// set the hint would look correct in review.
//
// It deliberately does not read the panel into the *current* step first — the
// name row doubles as that step's live edit and this step's seed name, so
// committing here would misfile a freshly typed name as a rename of the row
// the user just left. Callers with a precondition (the Properties page's
// read-only step) check it before calling.
func (p *jobStepPanel) addStep(grid *controls.DataGrid, hint *propsheet.HintRow,
	cols []string, edits *[]*jobStepEdit, rowsFor func() [][]string, sync func()) {

	name := p.nameField.Value()
	if name == "" {
		hint.Set("Type a step name first.")
		return
	}
	for i, e := range visibleSteps(*edits) {
		if e.name == name {
			// Already present — say so and select it, rather than leaving the
			// button looking broken.
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

// visibleSteps is the edit list minus the rows pending removal, in order.
func visibleSteps(edits []*jobStepEdit) []*jobStepEdit {
	out := make([]*jobStepEdit, 0, len(edits))
	for _, e := range edits {
		if !e.pendingRemove {
			out = append(out, e)
		}
	}
	return out
}
