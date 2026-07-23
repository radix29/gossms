package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_schedule_dialog.go is the New Schedule creation dialog (Object
// Explorer's SQL Server Agent > Schedules folder, "New Schedule..."). The
// General page is built from agent_schedule_form.go's shared
// scheduleFreqForm — the same frequency-field set Schedule Properties
// (agent_schedule_props.go) edits on an existing schedule. Only the second
// page is specific to creation: Jobs to attach at creation time.

type nschedulePrefetch struct {
	existingNames map[string]bool
	jobNames      []string
}

func fetchNewSchedulePrefetch(ctx context.Context, sc *db.ServerConn) (*nschedulePrefetch, error) {
	scheds, err := sc.Server.SchedulesContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(scheds))
	for _, sch := range scheds {
		existing[strings.ToLower(sch.Name)] = true
	}
	jobs, err := sc.Server.JobsContext(ctx)
	if err != nil {
		return nil, err
	}
	jobNames := make([]string, len(jobs))
	for i, j := range jobs {
		jobNames[i] = j.Name
	}
	return &nschedulePrefetch{existingNames: existing, jobNames: jobNames}, nil
}

// NewScheduleDialog is the New Schedule creation dialog.
type NewScheduleDialog struct {
	*propsheet.PropertySheet

	app *App
	sc  *db.ServerConn

	ctx    context.Context
	cancel context.CancelFunc

	prefetch     *nschedulePrefetch
	forms        [2]*propsheet.Form
	applyFns     [2]propApply
	scheduleName func() string
	preflight    func() error
}

// NewNewScheduleDialog creates the dialog and wires its callbacks.
func NewNewScheduleDialog(app *App) *NewScheduleDialog {
	d := &NewScheduleDialog{
		app:           app,
		PropertySheet: propsheet.NewPropertySheet(app.screen, "New Schedule"),
	}
	d.OnLoadPage = d.onLoadPage
	d.OnApply = func() { d.runApply(false) }
	d.OnOK = func() { d.runApply(true) }
	d.OnClose = d.onClose
	d.ConfirmDiscard = d.onConfirmDiscard
	d.OnScript = d.runScript
	return d
}

func (d *NewScheduleDialog) show(sc *db.ServerConn) {
	if d.cancel != nil {
		d.cancel()
	}
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.sc = sc
	d.prefetch = nil
	d.forms = [2]*propsheet.Form{}
	d.applyFns = [2]propApply{}
	d.scheduleName = nil
	d.preflight = nil
	d.SetHeader("Instance: "+sc.Opts.Server, "Connected: yes")
	d.SetPages([]string{"General", "Jobs"})
	d.Show()
}

func (d *NewScheduleDialog) onClose() {
	if d.cancel != nil {
		d.cancel()
	}
}

func (d *NewScheduleDialog) post(fn func()) {
	d.app.postEvent(fn)
	d.app.wakeEventLoop()
}

func (d *NewScheduleDialog) onLoadPage(page, seq int) {
	if d.prefetch != nil {
		d.SetPageForm(page, seq, d.forms[page])
		return
	}
	sc := d.sc
	sessionCtx := d.ctx
	go func() {
		ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
		defer cancel()
		pf, err := fetchNewSchedulePrefetch(ctx, sc)
		d.post(func() {
			if err != nil {
				d.SetPageError(page, seq, err)
				return
			}
			d.buildPages(pf)
			d.SetPageForm(page, seq, d.forms[page])
		})
	}()
}

func (d *NewScheduleDialog) buildPages(pf *nschedulePrefetch) {
	d.prefetch = pf
	sc := d.sc

	freqForm := newScheduleFreqForm()
	generalForm := propsheet.NewForm(
		propsheet.Section("Schedule identity"),
		freqForm.nameField, freqForm.enabledCheck,
	)
	generalForm.Add(freqForm.rows()...)

	scheduleName := freqForm.name

	generalApply := func(ctx context.Context) error {
		freq := freqForm.readFrequency()
		req := gosmo.CreateScheduleRequest{
			Name: freqForm.name(), Enabled: freqForm.enabled(),
			FreqType: freq.FreqType, FreqInterval: freq.FreqInterval,
			FreqSubdayType: freq.FreqSubdayType, FreqSubdayInterval: freq.FreqSubdayInterval,
			FreqRelativeInterval: freq.FreqRelativeInterval, FreqRecurrenceFactor: freq.FreqRecurrenceFactor,
		}
		req.ActiveStartDate, req.ActiveEndDate, req.ActiveStartTime, req.ActiveEndTime = freqForm.readActiveRange()
		_, err := sc.Server.CreateScheduleContext(ctx, req)
		return err
	}

	jobsGrid := propsheet.NewToggleGrid([]string{"Attach", "Job"}, []int{0}, 12)
	jobText := make([][]string, len(pf.jobNames))
	jobVals := make([][]bool, len(pf.jobNames))
	for i, name := range pf.jobNames {
		jobText[i] = []string{name}
		jobVals[i] = []bool{false}
	}
	jobsGrid.SetRows(jobText, jobVals)

	jobsForm := propsheet.NewForm(
		propsheet.Section("Attach to jobs"),
		jobsGrid,
		propsheet.Note("Optional — a schedule doesn't need to be attached to any job yet. Attach more later from a job's own Schedules page."),
	)
	jobsApply := func(ctx context.Context) error {
		name := scheduleName()
		for i, v := range jobsGrid.Values() {
			if !v[0] {
				continue
			}
			j, err := sc.Server.JobByNameContext(ctx, pf.jobNames[i])
			if err != nil {
				return err
			}
			if err := j.AttachScheduleContext(ctx, name); err != nil {
				return err
			}
		}
		return nil
	}

	d.forms = [2]*propsheet.Form{generalForm, jobsForm}
	d.applyFns = [2]propApply{generalApply, jobsApply}
	d.scheduleName = scheduleName
	d.preflight = func() error {
		name := scheduleName()
		if name == "" {
			return fmt.Errorf("schedule name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("a schedule named %q already exists", name)
		}
		return nil
	}
}

func (d *NewScheduleDialog) onConfirmDiscard(page int, proceed func()) {
	d.app.confirmDialog.ShowConfirm("Discard Changes",
		"This page has unsaved changes. Discard them and refresh from the server?",
		func(confirmed bool) {
			if confirmed {
				proceed()
			}
		})
}

func (d *NewScheduleDialog) runPipeline(runCtx context.Context, onSuccess func()) {
	if d.prefetch == nil {
		d.SetMessage("Still loading — try again in a moment.", true)
		return
	}
	if err := d.preflight(); err != nil {
		d.SelectPage(0)
		d.SetMessage(err.Error(), true)
		return
	}
	if page, err := d.Validate(); err != nil {
		d.SelectPage(page)
		d.SetMessage(err.Error(), true)
		return
	}

	fns := d.applyFns
	d.SetApplying(true)
	d.SetMessage("", false)

	go func() {
		var runErr error
		for _, fn := range fns {
			if runErr = fn(runCtx); runErr != nil {
				break
			}
		}
		d.post(func() {
			d.SetApplying(false)
			if runErr != nil {
				d.SetMessage(runErr.Error(), true)
				return
			}
			onSuccess()
		})
	}()
}

func (d *NewScheduleDialog) runApply(hideOnSuccess bool) {
	d.runPipeline(d.ctx, func() {
		d.app.setStatus(fmt.Sprintf("Schedule %q created", d.scheduleName()))
		d.app.explorer.RefreshFolderByType(d.sc, NodeAgentSchedules)
		if hideOnSuccess {
			d.Hide()
		}
	})
}

func (d *NewScheduleDialog) runScript() {
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc := d.sc
	d.runPipeline(scriptCtx, func() {
		d.app.openQueryWithText(sc, "msdb", strings.Join(script.Statements, "\n\n"))
	})
}
