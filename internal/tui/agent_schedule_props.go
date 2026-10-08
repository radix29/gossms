package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// agent_schedule_props.go builds Schedule Properties: General (identity, owner,
// and the shared frequency form from agent_schedule_form.go) and a read-only
// Jobs page. Like agent_job_props.go, pages share &scheduleName, so General's
// rename (the run's last write, see propPage.renames) is seen by
// PropDialog.InvalidateAll's reload.

// findAgentSchedule reads a schedule by its schedule_id, never its name:
// schedule names are not unique (see nodeData.AgentScheduleID).
func findAgentSchedule(ctx context.Context, sc *db.ServerConn, id int) (*gosmo.Schedule, error) {
	return sc.Server.ScheduleByID(ctx, id)
}

// schedulePropPages builds the page set for Schedule Properties of schedule
// id, currently named scheduleName.
func schedulePropPages(sc *db.ServerConn, id int, scheduleName string) []propPage {
	name := &scheduleName
	return []propPage{
		withRequires(pageScheduleGeneral(sc, id, name), "", gate.AgentWriteRights()...),
		pageScheduleJobs(sc, id),
	}
}

// showScheduleProperties opens Schedule Properties from Object Explorer's
// context menu. database is "msdb" so Script Changes' window opens there.
func (a *App) showScheduleProperties(sc *db.ServerConn, id int, scheduleName string) {
	a.propDialog.showReloading(sc, "msdb", "Schedule Properties", "Schedule: "+scheduleName, "Server: "+sc.Opts.Server,
		func() []propPage { return schedulePropPages(sc, id, scheduleName) }, folderOf("", NodeAgentSchedules))
}

func pageScheduleGeneral(sc *db.ServerConn, id int, scheduleName *string) propPage {
	return propPage{
		title:   "General",
		renames: scheduleName,
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			sch, err := findAgentSchedule(ctx, sc, id)
			if err != nil {
				return nil, nil, err
			}
			logins, err := sc.Server.Logins(ctx)
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
				sch, err := findAgentSchedule(ctx, sc, id)
				if err != nil {
					return err
				}
				// One sp_update_schedule for the whole page — see the same
				// batching in pageJobGeneral. A schedule is keyed by
				// @schedule_id, which the rename does not disturb, so it
				// rides along rather than going last (see propPage.renames);
				// commitRename still updates the shared cell the Jobs page
				// reads.
				var ch gosmo.ScheduleChanges
				if freqForm.enabledCheck.Dirty() {
					ch.Enabled = new(freqForm.enabled())
				}
				if freqForm.frequencyDirty() {
					f := freqForm.readFrequency()
					ch.Frequency = &f
				}
				if freqForm.rangeDirty() {
					startDate, endDate, startTime, endTime := freqForm.readActiveRange()
					ch.Range = &gosmo.ScheduleActiveRange{
						StartDate: startDate, EndDate: endDate,
						StartTime: startTime, EndTime: endTime,
					}
				}
				if owner, ok := changedTo(ownerRow, unknownOwnerItem); ok {
					ch.OwnerLogin = new(owner)
				}
				if freqForm.nameField.Dirty() {
					ch.Name = new(freqForm.name())
				}
				if err := sch.Alter(ctx, ch); err != nil {
					return err
				}
				if freqForm.nameField.Dirty() {
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
func pageScheduleJobs(sc *db.ServerConn, id int) propPage {
	return propPage{
		title: "Jobs",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			sch, err := findAgentSchedule(ctx, sc, id)
			if err != nil {
				return nil, nil, err
			}
			jobs, err := sch.Jobs(ctx)
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
