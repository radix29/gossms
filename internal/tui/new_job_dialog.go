package tui

import (
	"context"
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// new_job_dialog.go is the New Job creation dialog (Object Explorer's SQL
// Server Agent > Jobs folder, "New Job..."). Four pages — General, Steps,
// Schedules, Notifications — page builders live in new_job_pages.go.
// Alerts, Targets, and History are left out: Alerts/Targets have nothing
// meaningful to configure before the job exists (alert-job linking lives
// on Job Properties' own Alerts page, matching new_alert_dialog.go's same
// call), and History is naturally empty for a job that hasn't run yet.

// njobPrefetch holds the one fetch every New Job page is built from.
type njobPrefetch struct {
	existingNames map[string]bool
	loginNames    []string
	categories    []string
	dbNames       []string
	scheduleNames []string
	schedules     []*gosmo.Schedule
	operatorNames []string
}

func fetchNewJobPrefetch(ctx context.Context, sc *db.ServerConn) (*njobPrefetch, error) {
	jobs, err := sc.Server.JobsContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(jobs))
	for _, j := range jobs {
		existing[strings.ToLower(j.Name)] = true
	}
	logins, err := sc.Server.LoginsContext(ctx)
	if err != nil {
		return nil, err
	}
	loginNames := make([]string, len(logins))
	for i, l := range logins {
		loginNames[i] = l.Name
	}
	cats, err := sc.Server.CategoriesContext(ctx, gosmo.CategoryClassJob)
	if err != nil {
		return nil, err
	}
	catNames := make([]string, len(cats))
	for i, c := range cats {
		catNames[i] = c.Name
	}
	dbs, err := sc.Server.DatabasesContext(ctx)
	if err != nil {
		return nil, err
	}
	dbNames := make([]string, len(dbs))
	for i, d := range dbs {
		dbNames[i] = d.Name()
	}
	scheds, err := sc.Server.SchedulesContext(ctx)
	if err != nil {
		return nil, err
	}
	schedNames := make([]string, len(scheds))
	for i, sch := range scheds {
		schedNames[i] = sch.Name
	}
	ops, err := sc.Server.OperatorsContext(ctx)
	if err != nil {
		return nil, err
	}
	opNames := make([]string, len(ops))
	for i, o := range ops {
		opNames[i] = o.Name
	}
	return &njobPrefetch{
		existingNames: existing, loginNames: loginNames, categories: catNames,
		dbNames: dbNames, scheduleNames: schedNames, schedules: scheds, operatorNames: opNames,
	}, nil
}

// NewJobDialog is the New Job creation dialog.
type NewJobDialog struct {
	*propsheet.PropertySheet

	app *App
	sc  *db.ServerConn

	ctx    context.Context
	cancel context.CancelFunc

	prefetch  *njobPrefetch
	forms     [4]*propsheet.Form
	applyFns  [4]propApply
	jobName   func() string
	enabled   func() bool
	stepCount func() int
	preflight func() error
}

// NewNewJobDialog creates the dialog and wires its callbacks.
func NewNewJobDialog(app *App) *NewJobDialog {
	d := &NewJobDialog{
		app:           app,
		PropertySheet: propsheet.NewPropertySheet(app.screen, "New Job"),
	}
	d.OnLoadPage = d.onLoadPage
	d.OnApply = func() { d.runApply(false) }
	d.OnOK = func() { d.runApply(true) }
	d.OnClose = d.onClose
	d.ConfirmDiscard = d.onConfirmDiscard
	d.OnScript = d.runScript
	return d
}

func (d *NewJobDialog) show(sc *db.ServerConn) {
	if d.cancel != nil {
		d.cancel()
	}
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.sc = sc
	d.prefetch = nil
	d.forms = [4]*propsheet.Form{}
	d.applyFns = [4]propApply{}
	d.jobName = nil
	d.enabled = nil
	d.stepCount = nil
	d.preflight = nil
	d.SetHeader("Instance: "+sc.Opts.Server, "Connected: yes")
	d.SetPages([]string{"General", "Steps", "Schedules", "Notifications"})
	d.Show()
}

func (d *NewJobDialog) onClose() {
	if d.cancel != nil {
		d.cancel()
	}
}

func (d *NewJobDialog) post(fn func()) {
	d.app.postEvent(fn)
	d.app.wakeEventLoop()
}

func (d *NewJobDialog) onLoadPage(page, seq int) {
	if d.prefetch != nil {
		d.SetPageForm(page, seq, d.forms[page])
		return
	}
	sc := d.sc
	sessionCtx := d.ctx
	go func() {
		ctx, cancel := context.WithTimeout(sessionCtx, propFetchTimeout)
		defer cancel()
		pf, err := fetchNewJobPrefetch(ctx, sc)
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

// buildPages constructs all four pages' forms/applies from pf, once.
func (d *NewJobDialog) buildPages(pf *njobPrefetch) {
	d.prefetch = pf
	sc := d.sc

	generalForm, generalApply, jobName, enabled := buildNewJobGeneralPage(sc, pf)
	stepsForm, stepsApply, stepCount := buildNewJobStepsPage(sc, pf, jobName)
	schedulesForm, schedulesApply := buildNewJobSchedulesPage(sc, pf, jobName)
	notificationsForm, notificationsApply := buildNewJobNotificationsPage(sc, pf, jobName)

	d.forms = [4]*propsheet.Form{generalForm, stepsForm, schedulesForm, notificationsForm}
	d.applyFns = [4]propApply{generalApply, stepsApply, schedulesApply, notificationsApply}
	d.jobName = jobName
	d.enabled = enabled
	d.stepCount = stepCount
	d.preflight = func() error {
		name := jobName()
		if name == "" {
			return fmt.Errorf("job name is required")
		}
		if pf.existingNames[strings.ToLower(name)] {
			return fmt.Errorf("a job named %q already exists", name)
		}
		if enabled() && stepCount() == 0 {
			return fmt.Errorf("at least one job step is required for an enabled job — add one on the Steps page, or clear Enabled on General")
		}
		return nil
	}
}

func (d *NewJobDialog) onConfirmDiscard(page int, proceed func()) {
	d.app.confirmDialog.ShowConfirm("Discard Changes",
		"This page has unsaved changes. Discard them and refresh from the server?",
		func(confirmed bool) {
			if confirmed {
				proceed()
			}
		})
}

// runPipeline validates, then runs every page's apply closure in a fixed
// order (General, Steps, Schedules, Notifications) — General's closure is
// what creates the job (and, per gosmo.CreateJobContext, enlists it on the
// local server) before the later pages' own closures can target it.
func (d *NewJobDialog) runPipeline(runCtx context.Context, onSuccess func()) {
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

func (d *NewJobDialog) runApply(hideOnSuccess bool) {
	d.runPipeline(d.ctx, func() {
		d.app.setStatus(fmt.Sprintf("Job %q created", d.jobName()))
		d.app.explorer.RefreshFolderByType(d.sc, NodeAgentUserJobs)
		if hideOnSuccess {
			d.Hide()
		}
	})
}

func (d *NewJobDialog) runScript() {
	scriptCtx, script := gosmo.WithScript(d.ctx)
	sc := d.sc
	d.runPipeline(scriptCtx, func() {
		d.app.openQueryWithText(sc, "msdb", strings.Join(script.Statements, "\n\n"))
	})
}
