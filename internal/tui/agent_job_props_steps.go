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

// jobStepEdit tracks one Steps-page row's pending state: an existing T-SQL
// step whose definition changed, a brand-new step pending Add (isNew), or
// an existing step pending Delete. orig is nil for a brand-new step —
// Update/Delete both need a real *gosmo.JobStep to call.
type jobStepEdit struct {
	orig          *gosmo.JobStep
	isNew         bool
	pendingRemove bool

	stepID int // display only; 0 for a not-yet-saved new step

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
		orig: s, stepID: s.StepID,
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

func (e *jobStepEdit) changed() bool {
	return e.name != e.origName || e.database != e.origDatabase || e.command != e.origCommand ||
		e.onSuccessAction != e.origOnSuccessAction || e.onSuccessStepID != e.origOnSuccessStepID ||
		e.onFailAction != e.origOnFailAction || e.onFailStepID != e.origOnFailStepID ||
		e.retryAttempts != e.origRetryAttempts || e.retryInterval != e.origRetryInterval ||
		e.outputFileName != e.origOutputFileName
}

func (e *jobStepEdit) request() gosmo.JobStepRequest {
	return gosmo.JobStepRequest{
		Name: e.name, Subsystem: "TSQL", Command: e.command, Database: e.database,
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

// pageJobSteps is the Steps page: a grid of the job's T-SQL steps (SQL-only
// scope — CmdExec, PowerShell, SSIS, and every other subsystem the mockup
// excludes stay excluded here too) plus an inline "selected step" edit
// panel, following the Add/Remove-button idiom database_props_files.go's
// Files page established. Step reordering (SSMS's Move Up/Move Down) is
// left out: there's no documented stored procedure for it, and the
// mockup's own step_id-renumbering caveat on Delete signals it's genuinely
// fiddly to get right — worth a dedicated look later rather than guessing.
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
			cols := []string{"Step", "Name", "Database"}
			rowsFor := func() [][]string {
				vis := visible()
				rows := make([][]string, len(vis))
				for i, e := range vis {
					rows[i] = []string{stepNumberText(e), e.name, e.database}
				}
				return rows
			}

			grid := controls.NewDataGrid()
			grid.SetData(cols, rowsFor())

			nameField := propsheet.Text("Step name", "", 30)
			databaseSelect := propsheet.Select("Database", dbNames, 0)
			commandField := propsheet.Text("Command", "", 60)
			onSuccessSelect := propsheet.Select("On success action", jobStepOnActionItems, 2)
			onSuccessStepField := propsheet.Int("On success go to step", 0, 0, 999, "")
			onFailSelect := propsheet.Select("On failure action", jobStepOnActionItems, 1)
			onFailStepField := propsheet.Int("On failure go to step", 0, 0, 999, "")
			retryAttemptsField := propsheet.Int("Retry attempts", 0, 0, 999, "")
			retryIntervalField := propsheet.Int("Retry interval", 0, 0, 999, "minutes")
			outputFileField := propsheet.Text("Output file name", "", 40)

			selected := func() *jobStepEdit {
				vis := visible()
				i := grid.SelectedRow()
				if i < 0 || i >= len(vis) {
					return nil
				}
				return vis[i]
			}
			var current *jobStepEdit
			commitCurrent := func() {
				if current == nil {
					return
				}
				current.name = nameField.Value()
				current.database = databaseSelect.Value()
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
				databaseSelect.SetSelected(indexOf(dbNames, current.database))
				commandField.SetValue(current.command)
				onSuccessSelect.SetSelected(current.onSuccessAction - 1)
				onSuccessStepField.SetValue(strconv.Itoa(current.onSuccessStepID))
				onFailSelect.SetSelected(current.onFailAction - 1)
				onFailStepField.SetValue(strconv.Itoa(current.onFailStepID))
				retryAttemptsField.SetValue(strconv.Itoa(current.retryAttempts))
				retryIntervalField.SetValue(strconv.Itoa(current.retryInterval))
				outputFileField.SetValue(current.outputFileName)
			}
			grid.OnSelectRow = func(row int) {
				commitCurrent()
				syncFieldsFromSelection()
			}
			syncFieldsFromSelection()

			var newBtn, deleteBtn *widgets.Button
			newBtn = widgets.NewButton("New", func() {
				// Deliberately doesn't call commitCurrent() first — see
				// database_props_files.go's addBtn comment: nameField
				// doubles as the previously-selected step's live edit and
				// the new step's seed name, and committing here would
				// misfile a freshly typed name as a rename of the wrong
				// step instead of a new one.
				name := nameField.Value()
				if name == "" {
					return
				}
				for _, e := range visible() {
					if e.name == name {
						return // already present — edit its row instead
					}
				}
				e := &jobStepEdit{isNew: true, name: name, database: databaseSelect.Value(), command: commandField.Value()}
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
				if e := selected(); e != nil {
					e.pendingRemove = true
					current = nil
					grid.SetData(cols, rowsFor())
					grid.SetSelectedRow(0)
					syncFieldsFromSelection()
				}
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
				statusRow,
				propsheet.Note("Only T-SQL steps are supported. \"Go to step\" fields only take effect when the matching action above is set to \"Go to step...\"."),
			)

			apply := func(ctx context.Context) error {
				commitCurrent()
				j, err := findAgentJob(ctx, sc, *jobName)
				if err != nil {
					return err
				}
				// An existing step needs a fresh *gosmo.JobStep fetched
				// under j (the job as it exists right now, under its
				// current name) rather than e.orig directly —
				// JobStep.UpdateContext/DeleteContext build their SQL
				// from an internal job-name reference captured back when
				// this page first loaded the step list, which goes stale
				// the moment a same-click General rename runs first in
				// this same Apply pipeline. Fetched lazily, once, only if
				// an existing step actually needs it.
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
				// step_id down by one (live-verified: deleting step 1 of
				// three leaves the survivors as steps 1 and 2). Interleaved
				// in grid order, a second delete (or an update of a step
				// after a deleted one) would target the pre-renumbering
				// step_id and hit the wrong step. Updates run first, while
				// every loaded step_id is still valid; deletes then run in
				// descending step_id order, so each delete only renumbers
				// steps already dealt with.
				for _, e := range edits {
					if e.isNew || e.pendingRemove || !e.changed() {
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
