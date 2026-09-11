package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// agent_schedule_props.go builds Schedule Properties: General (identity, owner,
// and the shared frequency form from agent_schedule_form.go) and a read-only
// Jobs page. Like agent_job_props.go, pages share &scheduleName, so General's
// rename (the run's last write, see propPage.renames) is seen by
// PropDialog.InvalidateAll's reload.

// findAgentSchedule wraps gosmo.Server.ScheduleByNameContext, like
// findAgentJob.
func findAgentSchedule(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.Schedule, error) {
	return sc.Server.ScheduleByNameContext(ctx, name)
}

// schedulePropPages builds the page set for Schedule Properties.
func schedulePropPages(sc *db.ServerConn, scheduleName string) []propPage {
	name := &scheduleName
	return []propPage{
		withRequires(pageScheduleGeneral(sc, name), "", agentWriteRights()...),
		pageScheduleJobs(sc, name),
	}
}

// showScheduleProperties opens Schedule Properties from Object Explorer's
// context menu. database is "msdb" so Script Changes' window opens there.
func (a *App) showScheduleProperties(sc *db.ServerConn, scheduleName string) {
	a.propDialog.show(sc, "msdb", "Schedule Properties", "Schedule: "+scheduleName, "Server: "+sc.Opts.Server,
		func() []propPage { return schedulePropPages(sc, scheduleName) })
}

func pageScheduleGeneral(sc *db.ServerConn, scheduleName *string) propPage {
	return propPage{
		title:   "General",
		renames: true,
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			sch, err := findAgentSchedule(ctx, sc, *scheduleName)
			if err != nil {
				return nil, nil, err
			}
			logins, err := sc.Server.LoginsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			loginNames := make([]string, len(logins))
			for i, l := range logins {
				loginNames[i] = l.Name
			}

			freqForm := newScheduleFreqForm()
			freqForm.populate(sch)
			ownerRow := selectPreserving("Owner", loginNames, sch.OwnerLoginName, unknownOwnerItem)

			f := propsheet.NewForm(
				propsheet.Section("Schedule identity"),
				freqForm.nameField, freqForm.enabledCheck, ownerRow,
			)
			f.Add(freqForm.rows()...)

			apply := func(ctx context.Context) error {
				sch, err := findAgentSchedule(ctx, sc, *scheduleName)
				if err != nil {
					return err
				}
				if freqForm.enabledCheck.Dirty() {
					if freqForm.enabled() {
						err = sch.EnableContext(ctx)
					} else {
						err = sch.DisableContext(ctx)
					}
					if err != nil {
						return err
					}
				}
				if freqForm.frequencyDirty() {
					if err := sch.SetFrequencyContext(ctx, freqForm.readFrequency()); err != nil {
						return err
					}
				}
				if freqForm.rangeDirty() {
					startDate, endDate, startTime, endTime := freqForm.readActiveRange()
					if err := sch.SetActiveRangeContext(ctx, startDate, endDate, startTime, endTime); err != nil {
						return err
					}
				}
				if owner, ok := changedTo(ownerRow, unknownOwnerItem); ok {
					if err := sch.SetOwnerContext(ctx, owner); err != nil {
						return err
					}
				}
				// Rename last so earlier writes use the server's current name
				// (see propPage.renames); commitRename then updates the shared
				// cell for the Jobs page and reloads.
				if freqForm.nameField.Dirty() {
					if err := sch.RenameContext(ctx, freqForm.name()); err != nil {
						return err
					}
					commitRename(ctx, scheduleName, freqForm.name())
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

// pageScheduleJobs lists the jobs using this shared schedule, read-only;
// editing the schedule affects them all.
func pageScheduleJobs(sc *db.ServerConn, scheduleName *string) propPage {
	return propPage{
		title: "Jobs",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			sch, err := findAgentSchedule(ctx, sc, *scheduleName)
			if err != nil {
				return nil, nil, err
			}
			jobs, err := sch.JobsContext(ctx)
			if err != nil {
				return nil, nil, err
			}
			cols := []string{"Job Name", "Enabled"}
			rows := make([][]string, len(jobs))
			for i, j := range jobs {
				rows[i] = []string{j.Name, boolStr(j.IsEnabled)}
			}
			grid := controls.NewDataGrid()
			grid.SetData(cols, rows)

			f := propsheet.NewForm(
				propsheet.Section("Jobs using this schedule"),
				propsheet.NewGridRow(grid, 10),
				propsheet.Note("This is a shared schedule — changes on the General page affect every job listed here. Edit a job's own settings from its own Job Properties dialog."),
			)
			return f, nil, nil
		},
	}
}
