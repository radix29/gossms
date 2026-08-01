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
	newObjectDialog[nschedulePrefetch]
}

// NewNewScheduleDialog creates the dialog and wires its callbacks.
func NewNewScheduleDialog(app *App) *NewScheduleDialog {
	d := &NewScheduleDialog{}
	d.init(app, newObjectConfig[nschedulePrefetch]{
		title:          "New Schedule",
		noun:           "Schedule",
		pages:          []string{"General", "Jobs"},
		scriptDatabase: "msdb",
		fetch:          fetchNewSchedulePrefetch,
		build:          d.buildPages,
		refresh:        func(sc *db.ServerConn) { d.app.explorer.RefreshFolderByType(sc, NodeAgentSchedules) },
	})
	return d
}

func (d *NewScheduleDialog) buildPages(pf *nschedulePrefetch) {
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
			j, err := scriptSafeJob(ctx, sc, pf.jobNames[i])
			if err != nil {
				return err
			}
			if err := j.AttachScheduleContext(ctx, name); err != nil {
				return err
			}
		}
		return nil
	}

	d.forms = []*propsheet.Form{generalForm, jobsForm}
	d.applyFns = []propApply{generalApply, jobsApply}
	d.objectName = scheduleName
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
