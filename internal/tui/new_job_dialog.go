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
// Alerts, Targets, and History are left out: alert-job linking lives on Job
// Properties' own Alerts page, Targets has nothing to configure before the
// job exists, and History is empty for a job that hasn't run.

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
	dbNames, err := databaseNames(ctx, sc)
	if err != nil {
		return nil, err
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
	newObjectDialog[njobPrefetch]
}

// NewNewJobDialog creates the dialog and wires its callbacks.
func NewNewJobDialog(app *App) *NewJobDialog {
	d := &NewJobDialog{}
	d.init(app, newObjectConfig[njobPrefetch]{
		title:          "New Job",
		noun:           "Job",
		pages:          []string{"General", "Steps", "Schedules", "Notifications"},
		scriptDatabase: "msdb",
		fetch:          fetchNewJobPrefetch,
		build:          d.buildPages,
		refresh:        func(sc *db.ServerConn) { d.app.explorer.RefreshFolderByType(sc, NodeAgentUserJobs) },
	})
	return d
}

func (d *NewJobDialog) buildPages(pf *njobPrefetch) {
	sc := d.sc

	generalForm, generalApply, jobName, enabled := buildNewJobGeneralPage(sc, pf)
	stepsForm, stepsApply, stepCount := buildNewJobStepsPage(sc, pf, jobName)
	schedulesForm, schedulesApply := buildNewJobSchedulesPage(sc, pf, jobName)
	notificationsForm, notificationsApply := buildNewJobNotificationsPage(sc, pf, jobName)

	d.forms = []*propsheet.Form{generalForm, stepsForm, schedulesForm, notificationsForm}
	d.applyFns = []propApply{generalApply, stepsApply, schedulesApply, notificationsApply}
	d.objectName = jobName
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
