package tui

import (
	"context"
	"fmt"
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
	ownerRow := propsheet.Select("Owner", pf.loginNames, connectedLoginIndex(sc, pf.loginNames))
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
		_, err := sc.Server.CreateJob(ctx, req)
		return err
	}
	return f, apply, jobName, enabled
}

// connectedLoginIndex is the index of the connected login (SUSER_NAME() as the
// connect read it) in names, or 0 when it is not listed. SSMS defaults a new
// job's owner to the caller; index 0 alone was whatever sorts first — on a
// 2017 instance ##MS_AgentSigningCertificate##, so an untouched OK created a
// job owned by a certificate login (B14). A Windows login can come back from
// SUSER_NAME() in a different case than sys.server_principals holds it, so the
// match is the server collation's, not an exact one.
func connectedLoginIndex(sc *db.ServerConn, names []string) int {
	if sc == nil || sc.Server == nil || sc.Server.Info() == nil || sc.Server.Info().Login == "" {
		return 0
	}
	collation, login := serverCollation(sc), sc.Server.Info().Login
	for i, n := range names {
		if gosmo.SameName(collation, n, login) {
			return i
		}
	}
	return 0
}

// buildNewJobStepsPage builds New Job's Steps page: the same grid + inline
// edit panel as pageJobSteps (agent_job_props_steps.go), reusing its
// jobStepEdit/jobStepOnActionItems/stepNumberText directly — every row
// here is simply isNew:true from the start, since the job doesn't exist
// yet. "Start at Step" is dropped (nothing to start yet).
func buildNewJobStepsPage(sc *db.ServerConn, pf *njobPrefetch, jobName func() string, indentWidth int) (*propsheet.Form, propApply, func() int) {
	var edits []*jobStepEdit

	visible := func() []*jobStepEdit { return visibleSteps(edits) }
	cols := []string{"Step", "Name", "Database"}
	rowsFor := func() [][]string {
		vis := visible()
		rows := make([][]string, len(vis))
		for i, e := range vis {
			rows[i] = []string{"New", e.name, orDefault(e.database, defaultDatabaseItem)}
		}
		return rows
	}

	grid := controls.NewDataGrid()
	grid.SetData(cols, rowsFor())

	// The sentinel goes first, so a step the user never picked a database
	// for is created against the server's own default rather than against
	// whichever database sorts first — see defaultDatabaseItem.
	panel := newJobStepPanel(defaultDatabaseItem, pf.dbNames, indentWidth)

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
	}
	grid.OnSelectRow = func(row int) {
		panel.read(current)
		syncFieldsFromSelection()
	}

	hint := propsheet.Hint()
	var newBtn, deleteBtn *widgets.Button
	newBtn = widgets.NewButton("New", func() {
		panel.addStep(serverCollation(sc), grid, hint, cols, &edits, rowsFor, syncFieldsFromSelection)
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
		resetGrid(grid, cols, rowsFor(), 0)
		syncFieldsFromSelection()
	})

	gridRow := propsheet.NewGridRow(grid, 10)
	gridRow.DirtyFn = func() bool { return len(visible()) > 0 }
	gridRow.ValidateFn = func() error { return jobStepNamesRefusal(serverCollation(sc), edits) }
	gridRow.RevertFn = func() {
		edits = nil
		resetGrid(grid, cols, rowsFor(), 0)
		syncFieldsFromSelection()
	}

	rows := []propsheet.Row{propsheet.Section("Job steps"), gridRow, propsheet.Section("Selected step")}
	rows = append(rows, panel.rows()...)
	rows = append(rows,
		propsheet.Buttons(newBtn, deleteBtn),
		hint,
		propsheet.Note("Only T-SQL steps are supported. Database \"(default)\" lets the server pick the step's database. \"Go to step\" fields only take effect when the matching action above is set to \"Go to step...\"."),
	)
	f := propsheet.NewForm(rows...)
	// The panel's fields reach the selected step here, on the UI goroutine
	// before the pipeline runs — never in apply (docs/ui-rules.md).
	f.SetCommit(func() { panel.read(current) })

	apply := func(ctx context.Context) error {
		j, err := scriptSafeJob(ctx, sc, jobName())
		if err != nil {
			return err
		}
		for _, e := range visible() {
			if _, err := j.AddStep(ctx, e.request()); err != nil {
				return err
			}
		}
		return nil
	}
	stepCount := func() int { return len(visible()) }
	return f, apply, stepCount
}

// buildNewJobSchedulesPage builds New Job's Schedules page: attach existing
// shared schedules at creation time (a toggle grid, the same idiom
// new_schedule_dialog.go's own Jobs page uses in reverse). A brand-new
// schedule can't be created inline — create it in New Schedule first, then
// attach it here or from the job's own Schedules page.
func buildNewJobSchedulesPage(sc *db.ServerConn, pf *njobPrefetch, jobName func() string) (*propsheet.Form, propApply) {
	// Frequency as Job Properties ▸ Schedules shows it: schedule names are
	// not unique, and two namesakes otherwise list as identical rows.
	grid := propsheet.NewToggleGrid([]string{"Attach", "Schedule", "Frequency"}, []int{0}, 12)
	text := make([][]string, len(pf.schedules))
	vals := make([][]bool, len(pf.schedules))
	for i, sch := range pf.schedules {
		text[i] = []string{sch.Name, sch.Description()}
		vals[i] = []bool{false}
	}
	grid.SetRows(text, vals)

	f := propsheet.NewForm(
		propsheet.Section("Attach existing schedules"),
		grid,
		propsheet.Note("Optional — attach more, or create a new schedule, from the job's own Schedules page after it's created."),
	)
	apply := func(ctx context.Context) error {
		j, err := scriptSafeJob(ctx, sc, jobName())
		if err != nil {
			return err
		}
		for i, v := range grid.Values() {
			if !v[0] {
				continue
			}
			// By the listed schedule, which carries its id: schedule names
			// are not unique.
			if err := j.AttachSchedule(ctx, pf.schedules[i]); err != nil {
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
		j, err := scriptSafeJob(ctx, sc, jobName())
		if err != nil {
			return err
		}
		if emailCheck.Checked() {
			if len(pf.operatorNames) == 0 {
				return fmt.Errorf("no operators exist to notify — create one first, or clear E-mail")
			}
			opName := pf.operatorNames[operatorSelect.Selected()]
			if err := j.SetEmailNotify(ctx, opName, notifyConditionLevels[conditionSelect.Selected()]); err != nil {
				return err
			}
		}
		if deleteCheck.Checked() {
			if err := j.SetDeleteLevel(ctx, notifyConditionLevels[deleteConditionSelect.Selected()]); err != nil {
				return err
			}
		}
		return nil
	}
	return f, apply
}
