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

// jobStepOnActionItems is the on-success/on-failure dropdown in
// sp_add_jobstep's encoding: index i is action i+1 (1 quit success, 2 quit
// failure, 3 next step, 4 go to step N, from the adjacent number field).
var jobStepOnActionItems = []string{
	"Quit the job reporting success",
	"Quit the job reporting failure",
	"Go to the next step",
	"Go to step...",
}

// tsqlSubsystem is the only subsystem the Steps page can edit; others are
// read-only (see jobStepEdit.editable).
const tsqlSubsystem = "TSQL"

// unchangedDatabaseItem is the Database dropdown's leading sentinel, selected
// when database_name isn't listable: NULL (every non-T-SQL step) or a
// dropped/renamed database. Without it, indexOf's 0 would select the first
// database and write it back. Maps to "", which gosmo reads as "leave it".
const unchangedDatabaseItem = "(unchanged)"

// defaultDatabaseItem is New Job's counterpart: a new step has nothing to leave
// alone, so it means "omit @database_name" and let sp_add_jobstep default.
const defaultDatabaseItem = "(default)"

// jobStepEdit tracks one Steps row's pending state: a changed existing step, a
// new step (isNew), or an existing step pending Delete. orig is nil for new
// steps; Update and Delete need a real *gosmo.JobStep.
type jobStepEdit struct {
	orig          *gosmo.JobStep
	isNew         bool
	pendingRemove bool

	stepID int // display only; 0 for a not-yet-saved new step

	// subsystem is the step's own, carried through Update unchanged.
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

// editable reports whether the page may write the step back: T-SQL only. The
// panel has no CmdExec/PowerShell/SSIS fields and JobStepRequest carries a
// subsystem, so writing another type back would run its command text as T-SQL.
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

// jobStepWritePlan is the pending writes, split into apply's three passes.
type jobStepWritePlan struct {
	updates []*jobStepEdit
	deletes []*jobStepEdit
	adds    []*jobStepEdit
}

// planJobStepWrites splits edits into updates, deletes, adds, because
// sp_delete_jobstep renumbers every later step down by one.
//
// Updates first, while loaded step_ids are valid. Deletes in descending step_id
// order, so each renumbers only steps already handled; ascending makes the
// second delete hit a shifted id ("not found" or the wrong step). Adds last,
// since msdb numbers new steps from the remaining count.
//
// editable() is implied by changed() (the panel won't copy onto another
// subsystem's step) but checked again, since this pass would rewrite that
// step's subsystem if that ever changed.
func planJobStepWrites(edits []*jobStepEdit) jobStepWritePlan {
	var plan jobStepWritePlan
	for _, e := range edits {
		switch {
		case e.pendingRemove:
			// Added and removed in one sitting: never on the server.
			if !e.isNew {
				plan.deletes = append(plan.deletes, e)
			}
		case e.isNew:
			plan.adds = append(plan.adds, e)
		case e.editable() && e.changed():
			plan.updates = append(plan.updates, e)
		}
	}
	slices.SortFunc(plan.deletes, func(a, b *jobStepEdit) int {
		return b.orig.StepID - a.orig.StepID
	})
	return plan
}

// reorderedStepIDs is the order ReorderSteps needs after the three passes: the
// step ids the page's steps will then have, in page order.
//
// After the passes, surviving steps hold 1..k in their original order (deletes
// close gaps) and new steps follow in add order. A different page order is the
// reorder; display numbers don't matter.
//
// Returns nil when already in order.
func reorderedStepIDs(edits []*jobStepEdit) []int {
	var surviving, added []*jobStepEdit
	for _, e := range edits {
		switch {
		case e.pendingRemove:
		case e.isNew:
			added = append(added, e)
		default:
			surviving = append(surviving, e)
		}
	}
	byStepID := slices.Clone(surviving)
	slices.SortFunc(byStepID, func(a, b *jobStepEdit) int { return a.orig.StepID - b.orig.StepID })

	final := make(map[*jobStepEdit]int, len(byStepID)+len(added))
	for i, e := range byStepID {
		final[e] = i + 1
	}
	for i, e := range added {
		final[e] = len(byStepID) + i + 1
	}

	ids := make([]int, 0, len(final))
	identity := true
	for _, e := range edits {
		if e.pendingRemove {
			continue
		}
		id := final[e]
		if id != len(ids)+1 {
			identity = false
		}
		ids = append(ids, id)
	}
	if identity {
		return nil
	}
	return ids
}

func stepNumberText(e *jobStepEdit) string {
	if e.isNew {
		return "New"
	}
	return strconv.Itoa(e.stepID)
}

// pageJobSteps is the Steps page: a grid of the job's steps with an inline edit
// panel (database_props_files.go's Add/Remove idiom) plus Move Up/Down.
//
// Reordering is a fourth pass, since it names ids the other three leave behind
// (see reorderedStepIDs).
//
// Editing is T-SQL only, but listing isn't: every step is shown with a Type
// column. Other subsystems are read-only (commitCurrent refuses to copy onto
// them, keeping them out of apply). New steps are T-SQL.
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
			dbNames, err := databaseNames(ctx, sc)
			if err != nil {
				return nil, nil, err
			}

			edits := make([]*jobStepEdit, len(steps))
			for i, s := range steps {
				edits[i] = jobStepEditFromStep(s)
			}

			visible := func() []*jobStepEdit { return visibleSteps(edits) }
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

			// Sentinel first, so index 0 is the "can't show" fallback (see
			// unchangedDatabaseItem).
			panel := newJobStepPanel(unchangedDatabaseItem, dbNames)

			hint := propsheet.Hint()

			selected := func() *jobStepEdit {
				vis := visible()
				i := grid.SelectedRow()
				if i < 0 || i >= len(vis) {
					return nil
				}
				return vis[i]
			}
			var current *jobStepEdit
			syncFieldsFromSelection := func() {
				current = selected()
				panel.write(current)
				if current == nil {
					panel.setReadOnly(false)
					return
				}
				// Explained on selection, when the gate applies. Highlighting
				// is dropped too; highlighting PowerShell as T-SQL would claim
				// it is T-SQL.
				if !current.editable() {
					panel.setReadOnly(true)
					panel.commandEditor.SetHighlighter(nil)
					hint.Set(current.subsystem + " steps are shown read-only — this page edits T-SQL steps only.")
				} else {
					panel.setReadOnly(false)
					panel.commandEditor.SetHighlighter(panel.sqlHighlight)
					hint.Clear()
				}
			}
			grid.OnSelectRow = func(row int) {
				panel.read(current)
				syncFieldsFromSelection()
			}
			syncFieldsFromSelection()

			var newBtn, deleteBtn *widgets.Button
			newBtn = widgets.NewButton("New", func() {
				// Don't read the panel into current first: the name row is both
				// the old step's live edit and the new step's seed, so
				// committing would misfile a typed name as a rename. A
				// read-only step's panel can't seed a new one (its rows refuse
				// typing), so clear and unlock it rather than disabling New.
				if current != nil && !current.editable() {
					current = nil
					panel.clear()
					panel.setReadOnly(false)
					panel.commandEditor.SetHighlighter(panel.sqlHighlight)
					hint.Set("Type a name for the new step, then press New again.")
					return
				}
				panel.addStep(grid, hint, cols, &edits, rowsFor, syncFieldsFromSelection)
			})
			// moveSelected moves the selected step up (-1) or down (+1),
			// swapping in edits rather than the visible slice, which skips
			// pending removals.
			moveSelected := func(delta int) {
				panel.read(current)
				vis := visible()
				i := grid.SelectedRow()
				if i < 0 || i >= len(vis) {
					hint.Set("Select a step in the grid above to move it.")
					return
				}
				if i+delta < 0 || i+delta >= len(vis) {
					hint.Set("Step " + vis[i].name + " is already " + map[int]string{-1: "first", 1: "last"}[delta] + ".")
					return
				}
				hint.Clear()
				a := slices.Index(edits, vis[i])
				b := slices.Index(edits, vis[i+delta])
				edits[a], edits[b] = edits[b], edits[a]
				resetGrid(grid, cols, rowsFor(), i+delta)
				syncFieldsFromSelection()
			}
			moveUpBtn := widgets.NewButton("Move Up", func() { moveSelected(-1) })
			moveDownBtn := widgets.NewButton("Move Down", func() { moveSelected(1) })

			deleteBtn = widgets.NewButton("Delete", func() {
				e := selected()
				if e == nil {
					hint.Set("Select a step in the grid above to delete it.")
					return
				}
				hint.Clear()
				e.pendingRemove = true
				current = nil
				resetGrid(grid, cols, rowsFor(), 0)
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
				if reorderedStepIDs(edits) != nil {
					return true
				}
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
				resetGrid(grid, cols, rowsFor(), 0)
				syncFieldsFromSelection()
			}

			rows := []propsheet.Row{propsheet.Section("Job steps"), gridRow, propsheet.Section("Selected step")}
			rows = append(rows, panel.rows()...)
			rows = append(rows,
				propsheet.Buttons(newBtn, deleteBtn, moveUpBtn, moveDownBtn, startBtn),
				hint,
				statusRow,
				propsheet.Note("Steps of other subsystems are listed but read-only; only T-SQL steps can be edited or created here. Database \"(unchanged)\" leaves the step's own database alone. \"Go to step\" fields only take effect when the matching action above is set to \"Go to step...\"."),
			)
			f := propsheet.NewForm(rows...)

			apply := func(ctx context.Context) error {
				panel.read(current)
				j, err := findAgentJob(ctx, sc, *jobName)
				if err != nil {
					return err
				}
				// Existing steps need a fresh *gosmo.JobStep fetched under j's
				// current name: Update/DeleteContext capture the job name at
				// load, which a same-Apply rename makes stale. Fetched lazily,
				// once.
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
				// Passes and order are planJobStepWrites'.
				plan := planJobStepWrites(edits)
				for _, e := range plan.updates {
					step, err := freshStep(e.orig.StepID)
					if err != nil {
						return err
					}
					if err := step.UpdateContext(ctx, e.request()); err != nil {
						return err
					}
				}
				for _, e := range plan.deletes {
					step, err := freshStep(e.orig.StepID)
					if err != nil {
						return err
					}
					if err := step.DeleteContext(ctx); err != nil {
						return err
					}
				}
				for _, e := range plan.adds {
					if err := j.AddStepContext(ctx, e.request()); err != nil {
						return err
					}
				}
				// Fourth pass, last: its ids are the post-pass ones. gosmo
				// repairs "go to step N" references; sp_delete_jobstep doesn't
				// (see MoveStepContext).
				if ids := reorderedStepIDs(edits); ids != nil {
					if err := j.ReorderStepsContext(ctx, func(int) []int { return ids }); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}
