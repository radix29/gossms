package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// jobStepOnActionItems is the on-success/on-failure action dropdown, in
// sp_add_jobstep/sp_update_jobstep's own @on_success_action/@on_fail_action
// encoding: index i maps to action code i+1 (1=quit success, 2=quit
// failure, 3=go to next step, 4=go to a specific step — see the "go to
// step" number field next to each dropdown).
var jobStepOnActionItems = []string{
	"Quit the job reporting success",
	"Quit the job reporting failure",
	"Go to the next step",
	"Go to step...",
}

// tsqlSubsystem is the only subsystem the Steps page can edit. Every other
// one (CMDEXEC, PowerShell, SSIS, replication) is listed read-only — see
// jobStepEdit.editable.
const tsqlSubsystem = "TSQL"

// unchangedDatabaseItem is the Database dropdown's leading sentinel,
// selected when the step's own database_name is one the list can't show:
// NULL (which every non-T-SQL step has, and StepsContext reads as ""), or a
// database since dropped or renamed. Without it, indexOf's not-found 0 put
// the alphabetically first database on the server in the box and
// commitCurrent wrote it straight back — an ALTER of the step's target
// database that the user never asked for. gosmo reads an empty Database as
// "leave it alone" (JobStep.UpdateContext), which is exactly what this
// sentinel maps back to.
const unchangedDatabaseItem = "(unchanged)"

// defaultDatabaseItem is the New Job Steps page's counterpart to
// unchangedDatabaseItem: a step that doesn't exist yet has no database to
// leave alone, so the sentinel there means "don't send @database_name at
// all" and lets sp_add_jobstep apply the server's default. Without it, a
// step the user never chose a database for was created against whichever
// database sorted first in the dropdown.
const defaultDatabaseItem = "(default)"

// jobStepEdit tracks one Steps-page row's pending state: an existing step
// whose definition changed, a brand-new step pending Add (isNew), or an
// existing step pending Delete. orig is nil for a brand-new step —
// Update/Delete both need a real *gosmo.JobStep to call.
type jobStepEdit struct {
	orig          *gosmo.JobStep
	isNew         bool
	pendingRemove bool

	stepID int // display only; 0 for a not-yet-saved new step

	// subsystem is the step's own, carried through Update unchanged. It is
	// not "TSQL" for every step: the page lists whatever sysjobsteps holds.
	subsystem string

	name            string
	database        string
	command         string
	onSuccessAction int
	onSuccessStepID int
	onFailAction    int
	onFailStepID    int
	retryAttempts   int
	retryInterval   int
	outputFileName  string

	origName            string
	origDatabase        string
	origCommand         string
	origOnSuccessAction int
	origOnSuccessStepID int
	origOnFailAction    int
	origOnFailStepID    int
	origRetryAttempts   int
	origRetryInterval   int
	origOutputFileName  string
}

func jobStepEditFromStep(s *gosmo.JobStep) *jobStepEdit {
	return &jobStepEdit{
		orig: s, stepID: s.StepID, subsystem: s.Subsystem,
		name: s.Name, database: s.Database, command: s.Command,
		onSuccessAction: s.OnSuccessAction, onSuccessStepID: s.OnSuccessStepID,
		onFailAction: s.OnFailAction, onFailStepID: s.OnFailStepID,
		retryAttempts: s.RetryAttempts, retryInterval: s.RetryInterval, outputFileName: s.OutputFileName,
		origName: s.Name, origDatabase: s.Database, origCommand: s.Command,
		origOnSuccessAction: s.OnSuccessAction, origOnSuccessStepID: s.OnSuccessStepID,
		origOnFailAction: s.OnFailAction, origOnFailStepID: s.OnFailStepID,
		origRetryAttempts: s.RetryAttempts, origRetryInterval: s.RetryInterval, origOutputFileName: s.OutputFileName,
	}
}

// editable reports whether this page may write the step back. Only T-SQL:
// the edit panel has no CmdExec/PowerShell/SSIS fields, and JobStepRequest
// carries a subsystem, so writing one of those back through this page's
// form would hand its old command text to the query processor as T-SQL.
func (e *jobStepEdit) editable() bool {
	return e.isNew || e.subsystem == tsqlSubsystem
}

func (e *jobStepEdit) changed() bool {
	return e.name != e.origName || e.database != e.origDatabase || e.command != e.origCommand ||
		e.onSuccessAction != e.origOnSuccessAction || e.onSuccessStepID != e.origOnSuccessStepID ||
		e.onFailAction != e.origOnFailAction || e.onFailStepID != e.origOnFailStepID ||
		e.retryAttempts != e.origRetryAttempts || e.retryInterval != e.origRetryInterval ||
		e.outputFileName != e.origOutputFileName
}

func (e *jobStepEdit) request() gosmo.JobStepRequest {
	sub := e.subsystem
	if sub == "" {
		sub = tsqlSubsystem
	}
	return gosmo.JobStepRequest{
		Name: e.name, Subsystem: sub, Command: e.command, Database: e.database,
		OnSuccessAction: e.onSuccessAction, OnSuccessStepID: e.onSuccessStepID,
		OnFailAction: e.onFailAction, OnFailStepID: e.onFailStepID,
		RetryAttempts: e.retryAttempts, RetryInterval: e.retryInterval, OutputFileName: e.outputFileName,
	}
}

func stepNumberText(e *jobStepEdit) string {
	if e.isNew {
		return "New"
	}
	return strconv.Itoa(e.stepID)
}

// pageJobSteps is the Steps page: a grid of every step the job has plus an
// inline "selected step" edit panel, following the Add/Remove-button idiom
// of database_props_files.go's Files page. There is no step reordering:
// msdb has no documented stored procedure for it.
//
// Editing is T-SQL-only, but *listing* is not — a job's steps are shown
// whole, with a Type column, so a mixed job doesn't look like it has fewer
// steps than it does. A step of any other subsystem is read-only here:
// commitCurrent refuses to copy the form back onto it, which is what keeps
// it out of changed() and so out of apply entirely. New steps this page
// creates are T-SQL.
func pageJobSteps(d *PropDialog, sc *db.ServerConn, jobName *string) propPage {
	return propPage{
		title: "Steps",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			j, err := findAgentJob(ctx, sc, *jobName)
			if err != nil {
				return nil, nil, err
			}
			steps, err := j.StepsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			dbs, err := sc.Server.DatabasesContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			dbNames := make([]string, len(dbs))
			for i, db := range dbs {
				dbNames[i] = db.Name()
			}

			edits := make([]*jobStepEdit, len(steps))
			for i, s := range steps {
				edits[i] = jobStepEditFromStep(s)
			}

			visible := func() []*jobStepEdit {
				out := make([]*jobStepEdit, 0, len(edits))
				for _, e := range edits {
					if !e.pendingRemove {
						out = append(out, e)
					}
				}
				return out
			}
			cols := []string{"Step", "Name", "Type", "Database"}
			rowsFor := func() [][]string {
				vis := visible()
				rows := make([][]string, len(vis))
				for i, e := range vis {
					rows[i] = []string{stepNumberText(e), e.name, e.subsystem, e.database}
				}
				return rows
			}

			grid := controls.NewDataGrid()
			grid.SetData(cols, rowsFor())

			// The sentinel goes first so index 0 is the "can't show what the
			// server reported" fallback rather than a real database — see
			// unchangedDatabaseItem and indexOf's own doc comment.
			dbItems := append([]string{unchangedDatabaseItem}, dbNames...)

			nameField := propsheet.Text("Step name", "", 30)
			databaseSelect := propsheet.Select("Database", dbItems, 0)
			commandField := propsheet.Text("Command", "", 60)
			onSuccessSelect := propsheet.Select("On success action", jobStepOnActionItems, 2)
			onSuccessStepField := propsheet.Int("On success go to step", 0, 0, 999, "")
			onFailSelect := propsheet.Select("On failure action", jobStepOnActionItems, 1)
			onFailStepField := propsheet.Int("On failure go to step", 0, 0, 999, "")
			retryAttemptsField := propsheet.Int("Retry attempts", 0, 0, 999, "")
			retryIntervalField := propsheet.Int("Retry interval", 0, 0, 999, "minutes")
			outputFileField := propsheet.Text("Output file name", "", 40)

			hint := propsheet.Hint()

			selected := func() *jobStepEdit {
				vis := visible()
				i := grid.SelectedRow()
				if i < 0 || i >= len(vis) {
					return nil
				}
				return vis[i]
			}
			// pickedDatabase maps the dropdown back to what gosmo should be
			// sent: the sentinel means "the server's value, whatever it is",
			// which JobStepRequest spells as the empty string.
			pickedDatabase := func() string {
				if v := databaseSelect.Value(); v != unchangedDatabaseItem {
					return v
				}
				return ""
			}
			var current *jobStepEdit
			commitCurrent := func() {
				if current == nil {
					return
				}
				// A step this page can't edit is never written back. Returning
				// before the first assignment is what keeps it out of
				// changed(), and so out of apply — the alternative was
				// sp_update_jobstep turning a PowerShell step into a T-SQL one
				// carrying its old script.
				if !current.editable() {
					return
				}
				current.name = nameField.Value()
				current.database = pickedDatabase()
				current.command = commandField.Value()
				current.onSuccessAction = onSuccessSelect.Selected() + 1
				current.onFailAction = onFailSelect.Selected() + 1
				if n, err := onSuccessStepField.IntValue(); err == nil {
					current.onSuccessStepID = int(n)
				}
				if n, err := onFailStepField.IntValue(); err == nil {
					current.onFailStepID = int(n)
				}
				if n, err := retryAttemptsField.IntValue(); err == nil {
					current.retryAttempts = int(n)
				}
				if n, err := retryIntervalField.IntValue(); err == nil {
					current.retryInterval = int(n)
				}
				current.outputFileName = outputFileField.Value()
			}
			syncFieldsFromSelection := func() {
				current = selected()
				if current == nil {
					nameField.SetValue("")
					databaseSelect.SetSelected(0)
					commandField.SetValue("")
					onSuccessSelect.SetSelected(2)
					onSuccessStepField.SetValue("0")
					onFailSelect.SetSelected(1)
					onFailStepField.SetValue("0")
					retryAttemptsField.SetValue("0")
					retryIntervalField.SetValue("0")
					outputFileField.SetValue("")
					return
				}
				nameField.SetValue(current.name)
				// indexOfOK, not indexOf: dbItems is a closed set with a
				// sentinel at 0, so a database the list can't show selects the
				// sentinel instead of the first real database.
				if i, ok := indexOfOK(dbNames, current.database); ok {
					databaseSelect.SetSelected(i + 1)
				} else {
					databaseSelect.SetSelected(0)
				}
				commandField.SetValue(current.command)
				onSuccessSelect.SetSelected(current.onSuccessAction - 1)
				onSuccessStepField.SetValue(strconv.Itoa(current.onSuccessStepID))
				onFailSelect.SetSelected(current.onFailAction - 1)
				onFailStepField.SetValue(strconv.Itoa(current.onFailStepID))
				retryAttemptsField.SetValue(strconv.Itoa(current.retryAttempts))
				retryIntervalField.SetValue(strconv.Itoa(current.retryInterval))
				outputFileField.SetValue(current.outputFileName)
				// Say so on selection rather than on OK: a read-only step's
				// fields still take typing (SelectRow has no disabled state to
				// match TextRow's), so the only honest moment to explain that
				// nothing will be saved is when the row is picked.
				if !current.editable() {
					hint.Set(current.subsystem + " steps are shown read-only — this page edits T-SQL steps only.")
				} else {
					hint.Clear()
				}
			}
			grid.OnSelectRow = func(row int) {
				commitCurrent()
				syncFieldsFromSelection()
			}
			syncFieldsFromSelection()

			var newBtn, deleteBtn *widgets.Button
			newBtn = widgets.NewButton("New", func() {
				// Deliberately doesn't call commitCurrent() first:
				// nameField doubles as the previously-selected step's
				// live edit and the new step's seed name, so committing
				// here would misfile a freshly typed name as a rename of
				// the wrong step.
				name := nameField.Value()
				if name == "" {
					hint.Set("Type a step name first.")
					return
				}
				for i, e := range visible() {
					if e.name == name {
						// Already present — say so and select it, rather than
						// leaving the button looking broken.
						hint.Set("A step named " + name + " is already listed — its row is selected below.")
						grid.SetSelectedRow(i)
						syncFieldsFromSelection()
						return
					}
				}
				hint.Clear()
				e := &jobStepEdit{
					isNew: true, subsystem: tsqlSubsystem,
					name: name, database: pickedDatabase(), command: commandField.Value(),
				}
				e.onSuccessAction = onSuccessSelect.Selected() + 1
				e.onFailAction = onFailSelect.Selected() + 1
				if n, err := onSuccessStepField.IntValue(); err == nil {
					e.onSuccessStepID = int(n)
				}
				if n, err := onFailStepField.IntValue(); err == nil {
					e.onFailStepID = int(n)
				}
				if n, err := retryAttemptsField.IntValue(); err == nil {
					e.retryAttempts = int(n)
				}
				if n, err := retryIntervalField.IntValue(); err == nil {
					e.retryInterval = int(n)
				}
				e.outputFileName = outputFileField.Value()
				edits = append(edits, e)
				grid.SetData(cols, rowsFor())
				grid.SetSelectedRow(len(visible()) - 1)
				syncFieldsFromSelection()
			})
			deleteBtn = widgets.NewButton("Delete", func() {
				e := selected()
				if e == nil {
					hint.Set("Select a step in the grid above to delete it.")
					return
				}
				hint.Clear()
				e.pendingRemove = true
				current = nil
				grid.SetData(cols, rowsFor())
				grid.SetSelectedRow(0)
				syncFieldsFromSelection()
			})

			statusRow := propsheet.Static("Last action", "")
			startBtn := d.asyncStatusButton("Start at Step", statusRow, "Starting...", func(ctx context.Context) (string, error) {
				e := selected()
				if e == nil || e.isNew {
					return "", fmt.Errorf("select an existing step first")
				}
				j, err := findAgentJob(ctx, sc, *jobName)
				if err != nil {
					return "", err
				}
				if err := j.StartContext(ctx, e.name); err != nil {
					return "", err
				}
				return "Job started at step " + e.name, nil
			})

			gridRow := propsheet.NewGridRow(grid, 10)
			gridRow.DirtyFn = func() bool {
				for _, e := range edits {
					if e.isNew || e.pendingRemove || e.changed() {
						return true
					}
				}
				return false
			}
			gridRow.RevertFn = func() {
				edits = edits[:0]
				for _, s := range steps {
					edits = append(edits, jobStepEditFromStep(s))
				}
				grid.SetData(cols, rowsFor())
				syncFieldsFromSelection()
			}

			f := propsheet.NewForm(
				propsheet.Section("Job steps"),
				gridRow,
				propsheet.Section("Selected step"),
				nameField, databaseSelect, commandField,
				onSuccessSelect, onSuccessStepField, onFailSelect, onFailStepField,
				retryAttemptsField, retryIntervalField, outputFileField,
				propsheet.Buttons(newBtn, deleteBtn, startBtn),
				hint,
				statusRow,
				propsheet.Note("Steps of other subsystems are listed but read-only; only T-SQL steps can be edited or created here. Database \"(unchanged)\" leaves the step's own database alone. \"Go to step\" fields only take effect when the matching action above is set to \"Go to step...\"."),
			)

			apply := func(ctx context.Context) error {
				commitCurrent()
				j, err := findAgentJob(ctx, sc, *jobName)
				if err != nil {
					return err
				}
				// An existing step needs a fresh *gosmo.JobStep fetched
				// under j, the job under its current name:
				// JobStep.UpdateContext/DeleteContext build their SQL
				// from a job-name reference captured when this page
				// loaded the step list, which a same-click General
				// rename makes stale. Fetched lazily, once, only if an
				// existing step needs it.
				var freshSteps []*gosmo.JobStep
				freshStep := func(stepID int) (*gosmo.JobStep, error) {
					if freshSteps == nil {
						var err error
						freshSteps, err = j.StepsContext(ctx)
						if err != nil {
							return nil, err
						}
					}
					for _, s := range freshSteps {
						if s.StepID == stepID {
							return s, nil
						}
					}
					return nil, fmt.Errorf("gosmo: step %d not found on job %q", stepID, j.Name)
				}
				// Three fixed passes — updates, then deletes, then adds —
				// because sp_delete_jobstep renumbers every later step's
				// step_id down by one. Updates run first, while every
				// loaded step_id is still valid; deletes then run in
				// descending step_id order, so each delete only renumbers
				// steps already dealt with.
				for _, e := range edits {
					// !editable is already implied by !changed() — commitCurrent
					// never writes to one — but stated here too, because this is
					// the loop that would rewrite the step's subsystem if it ever
					// stopped being implied.
					if e.isNew || e.pendingRemove || !e.editable() || !e.changed() {
						continue
					}
					step, err := freshStep(e.orig.StepID)
					if err != nil {
						return err
					}
					if err := step.UpdateContext(ctx, e.request()); err != nil {
						return err
					}
				}
				var removals []*jobStepEdit
				for _, e := range edits {
					if e.pendingRemove && !e.isNew {
						removals = append(removals, e)
					}
				}
				slices.SortFunc(removals, func(a, b *jobStepEdit) int {
					return b.orig.StepID - a.orig.StepID
				})
				for _, e := range removals {
					step, err := freshStep(e.orig.StepID)
					if err != nil {
						return err
					}
					if err := step.DeleteContext(ctx); err != nil {
						return err
					}
				}
				for _, e := range edits {
					if e.isNew && !e.pendingRemove {
						if err := j.AddStepContext(ctx, e.request()); err != nil {
							return err
						}
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
