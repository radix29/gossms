package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// buildNewJobGeneralPage builds New Job's General page: identity, owner,
// category, enabled, description. Mirrors pageJobGeneral's field shape
// (agent_job_props.go) minus the read-only "current execution summary" —
// none of that exists yet for a job that hasn't been created.
func buildNewJobGeneralPage(sc *db.ServerConn, pf *njobPrefetch) (*propsheet.Form, propApply, func() string, func() bool) {
	nameField := propsheet.Text("Name", "", 30)
	ownerRow := propsheet.Select("Owner", pf.loginNames, 0)
	catItems := append([]string{"[Uncategorized (Local)]"}, pf.categories...)
	categoryRow := propsheet.Select("Category", catItems, 0)
	enabledRow := propsheet.Check("Enabled", true)
	descRow := propsheet.Text("Description", "", 50)

	f := propsheet.NewForm(
		propsheet.Section("Job identity"),
		nameField, ownerRow, categoryRow, enabledRow, descRow,
		propsheet.Section("SQL-only backing objects"),
		propsheet.Note("msdb.dbo.sysjobs, syscategories, sysjobactivity, sysjobhistory"),
	)

	jobName := func() string { return strings.TrimSpace(nameField.Value()) }
	enabled := func() bool { return enabledRow.Checked() }
	apply := func(ctx context.Context) error {
		req := gosmo.CreateJobRequest{
			Name: jobName(), Description: descRow.Value(), Enabled: enabled(),
		}
		if ownerRow.Selected() >= 0 && ownerRow.Selected() < len(pf.loginNames) {
			req.OwnerLogin = pf.loginNames[ownerRow.Selected()]
		}
		if categoryRow.Selected() != 0 {
			req.Category = categoryRow.Value()
		}
		_, err := sc.Server.CreateJobContext(ctx, req)
		return err
	}
	return f, apply, jobName, enabled
}

// buildNewJobStepsPage builds New Job's Steps page: the same grid + inline
// edit panel as pageJobSteps (agent_job_props_steps.go), reusing its
// jobStepEdit/jobStepOnActionItems/stepNumberText directly — every row
// here is simply isNew:true from the start, since the job doesn't exist
// yet. "Start at Step" is dropped (nothing to start yet).
func buildNewJobStepsPage(sc *db.ServerConn, pf *njobPrefetch, jobName func() string) (*propsheet.Form, propApply, func() int) {
	var edits []*jobStepEdit

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
			rows[i] = []string{"New", e.name, e.database}
		}
		return rows
	}

	grid := controls.NewDataGrid()
	grid.SetData(cols, rowsFor())

	nameField := propsheet.Text("Step name", "", 30)
	databaseSelect := propsheet.Select("Database", pf.dbNames, 0)
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
		databaseSelect.SetSelected(indexOf(pf.dbNames, current.database))
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

	var newBtn, deleteBtn *widgets.Button
	newBtn = widgets.NewButton("New", func() {
		name := nameField.Value()
		if name == "" {
			return
		}
		for _, e := range visible() {
			if e.name == name {
				return
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

	gridRow := propsheet.NewGridRow(grid, 10)
	gridRow.DirtyFn = func() bool { return len(visible()) > 0 }
	gridRow.RevertFn = func() {
		edits = nil
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
		propsheet.Buttons(newBtn, deleteBtn),
		propsheet.Note("Only T-SQL steps are supported. \"Go to step\" fields only take effect when the matching action above is set to \"Go to step...\"."),
	)

	apply := func(ctx context.Context) error {
		commitCurrent()
		j, err := sc.Server.JobByNameContext(ctx, jobName())
		if err != nil {
			return err
		}
		for _, e := range visible() {
			if err := j.AddStepContext(ctx, e.request()); err != nil {
				return err
			}
		}
		return nil
	}
	stepCount := func() int { return len(visible()) }
	return f, apply, stepCount
}

// buildNewJobSchedulesPage builds New Job's Schedules page: attach
// existing shared schedules at creation time (a toggle grid, the same
// idiom new_schedule_dialog.go's own Jobs page uses in reverse). Creating
// a brand-new schedule inline here would duplicate that dialog's General
// page for no real benefit — create the schedule first, then attach it,
// either here or from the job's own Schedules page afterward.
func buildNewJobSchedulesPage(sc *db.ServerConn, pf *njobPrefetch, jobName func() string) (*propsheet.Form, propApply) {
	grid := propsheet.NewToggleGrid([]string{"Attach", "Schedule"}, []int{0}, 12)
	text := make([][]string, len(pf.scheduleNames))
	vals := make([][]bool, len(pf.scheduleNames))
	for i, name := range pf.scheduleNames {
		text[i] = []string{name}
		vals[i] = []bool{false}
	}
	grid.SetRows(text, vals)

	f := propsheet.NewForm(
		propsheet.Section("Attach existing schedules"),
		grid,
		propsheet.Note("Optional — attach more, or create a new schedule, from the job's own Schedules page after it's created."),
	)
	apply := func(ctx context.Context) error {
		j, err := sc.Server.JobByNameContext(ctx, jobName())
		if err != nil {
			return err
		}
		for i, v := range grid.Values() {
			if !v[0] {
				continue
			}
			if err := j.AttachScheduleContext(ctx, pf.scheduleNames[i]); err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply
}

// buildNewJobNotificationsPage builds New Job's Notifications page — same
// fields and excluded-feature notes as pageJobNotifications
// (agent_job_props_alerts.go), operating on a job that doesn't exist yet.
func buildNewJobNotificationsPage(sc *db.ServerConn, pf *njobPrefetch, jobName func() string) (*propsheet.Form, propApply) {
	emailCheck := propsheet.Check("E-mail", false)
	operatorSelect := propsheet.Select("Operator", pf.operatorNames, 0)
	conditionSelect := propsheet.Select("When to e-mail", notifyConditionItems, 1)
	deleteCheck := propsheet.Check("Delete job", false)
	deleteConditionSelect := propsheet.Select("When to delete", notifyConditionItems, 1)

	f := propsheet.NewForm(
		propsheet.Section("E-mail operator"),
		emailCheck, operatorSelect, conditionSelect,
		propsheet.Note("E-mail notification is metadata configuration only — actual mail delivery depends on Database Mail being configured, outside this dialog."),
		propsheet.Section("Net send operator"),
		propsheet.Note("<excluded — SQL-only scope>"),
		propsheet.Section("Pager operator"),
		propsheet.Note("<excluded — SQL-only scope>"),
		propsheet.Section("Write to Windows application event log"),
		propsheet.Note("<excluded — SQL-only scope>"),
		propsheet.Section("Automatically delete job"),
		deleteCheck, deleteConditionSelect,
	)

	apply := func(ctx context.Context) error {
		if !emailCheck.Checked() && !deleteCheck.Checked() {
			return nil
		}
		j, err := sc.Server.JobByNameContext(ctx, jobName())
		if err != nil {
			return err
		}
		if emailCheck.Checked() {
			if len(pf.operatorNames) == 0 {
				return fmt.Errorf("no operators exist to notify — create one first, or clear E-mail")
			}
			opName := pf.operatorNames[operatorSelect.Selected()]
			if err := j.SetEmailNotifyContext(ctx, opName, notifyConditionLevels[conditionSelect.Selected()]); err != nil {
				return err
			}
		}
		if deleteCheck.Checked() {
			if err := j.SetDeleteLevelContext(ctx, notifyConditionLevels[deleteConditionSelect.Selected()]); err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply
}
