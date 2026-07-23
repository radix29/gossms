package tui

import (
	"context"
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// agent_schedule_props.go builds the Schedule Properties dialog — General
// (identity, owner, and the shared frequency form from
// agent_schedule_form.go) plus a read-only Jobs page listing which jobs
// use this schedule. Mirrors agent_job_props.go's shape, including its
// shared *string name cell: every page closes over &scheduleName instead
// of a frozen string, so a rename on General is visible to any later
// page's apply in the same pipeline run, and to PropDialog.InvalidateAll's
// post-Apply reload of the current page — see
// sql-agent-job-props-review-2026-07 memory (Bug 2), the same bug shape
// found and fixed in Job Properties, applied here from the start rather
// than shipping it and fixing it later.

// findAgentSchedule is a thin wrapper over gosmo.Server.ScheduleByNameContext,
// mirroring findAgentJob.
func findAgentSchedule(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.Schedule, error) {
	return sc.Server.ScheduleByNameContext(ctx, name)
}

// schedulePropPages builds the page set for Schedule Properties.
func schedulePropPages(sc *db.ServerConn, scheduleName string) []propPage {
	name := &scheduleName
	return []propPage{
		pageScheduleGeneral(sc, name),
		pageScheduleJobs(sc, name),
	}
}

// showScheduleProperties opens Schedule Properties for a known connection
// and schedule name — the Object Explorer context menu's entry point
// (mirrors showJobPropertiesFor). database is "msdb" so Script Changes'
// generated query window opens there, matching every other Agent action's
// query window.
func (a *App) showScheduleProperties(sc *db.ServerConn, scheduleName string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.propDialog.show(sc, "msdb", "Schedule Properties", "Schedule: "+scheduleName, "Server: "+sc.Opts.Server,
		schedulePropPages(sc, scheduleName))
}

func pageScheduleGeneral(sc *db.ServerConn, scheduleName *string) propPage {
	return propPage{
		title: "General",
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
			ownerRow := propsheet.Select("Owner", loginNames, indexOf(loginNames, sch.OwnerLoginName))

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
				if freqForm.nameField.Dirty() {
					if err := sch.RenameContext(ctx, freqForm.name()); err != nil {
						return err
					}
					// Update the shared cell immediately so the Jobs
					// page's own load (and any reload after a successful
					// Apply/OK, via PropDialog.InvalidateAll) re-fetches
					// under the new name instead of one that no longer
					// exists.
					*scheduleName = freqForm.name()
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
				if ownerRow.Dirty() {
					if err := sch.SetOwnerContext(ctx, ownerRow.Value()); err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

// pageScheduleJobs is a read-only page listing every job this shared
// schedule is attached to — editing a schedule here affects every job
// listed, so there's nothing page-local to apply.
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
				rows[i] = []string{j.Name, fmt.Sprintf("%v", j.IsEnabled)}
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
