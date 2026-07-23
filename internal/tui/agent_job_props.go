package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// agent_job_props.go builds the Job Properties dialog — General and
// Targets pages here; Steps, Schedules, Alerts/Notifications, and History
// each get their own file (agent_job_props_*.go) since every one of them
// is a grid-backed page with its own pending-edit state, mirroring how
// database_props_files.go/database_props_filegroups.go split for the same
// reason.
//
// The mockup's eighth page, Extended Properties, is left out: SQL Server
// Agent jobs have no native extended-properties mechanism (the mockup's
// own notes say as much — it assumes an application-owned metadata table
// that doesn't exist here), so there's nothing real to back it with.

// jobPropPages builds the page set for Job Properties. d is threaded
// through for pages with immediate (non-Apply) actions — Steps' "Start at
// Step" and History's "View Full History" — mirroring indexPropPages'
// use of *PropDialog for Rebuild/Reorganize/Update Statistics.
func jobPropPages(d *PropDialog, sc *db.ServerConn, jobName string) []propPage {
	// name is a shared cell every page closes over, instead of each page
	// closing over its own frozen copy of jobName — pageJobGeneral's
	// apply updates *name the moment a rename succeeds, so any later
	// page's apply in the same Apply/OK click (and any page reload
	// thereafter, e.g. via PropDialog.InvalidateAll) sees the current
	// name instead of re-fetching a job that no longer exists under the
	// old one.
	name := &jobName
	return []propPage{
		pageJobGeneral(sc, name),
		pageJobSteps(d, sc, name),
		pageJobSchedules(sc, name),
		pageJobAlerts(sc, name),
		pageJobNotifications(sc, name),
		pageJobTargets(sc),
		pageJobHistory(d, sc, name),
	}
}

// showJobPropertiesFor opens Job Properties for a known connection and job
// name — the Object Explorer context menu's entry point (mirrors
// showLoginProperties). database is "msdb" so Script Changes' generated
// query window opens there, matching every other Agent action's query
// window (see showAgentJobHistory).
func (a *App) showJobPropertiesFor(sc *db.ServerConn, jobName string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.propDialog.show(sc, "msdb", "Job Properties", "Job: "+jobName, "Server: "+sc.Opts.Server,
		jobPropPages(a.propDialog, sc, jobName))
}

// findAgentJob is a thin wrapper over gosmo.Server.JobByNameContext, kept
// so every page's load/apply closure has one short name to call rather
// than reaching into sc.Server directly — mirrors login_props.go's
// findLogin.
func findAgentJob(ctx context.Context, sc *db.ServerConn, name string) (*gosmo.Job, error) {
	return sc.Server.JobByNameContext(ctx, name)
}

func pageJobGeneral(sc *db.ServerConn, jobName *string) propPage {
	return propPage{
		title: "General",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			j, err := findAgentJob(ctx, sc, *jobName)
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
			cats, err := sc.Server.CategoriesContext(ctx, gosmo.CategoryClassJob)
			if err != nil {
				return nil, nil, err
			}
			catNames := make([]string, len(cats))
			for i, c := range cats {
				catNames[i] = c.Name
			}

			nameRow := propsheet.Text("Name", j.Name, 30)
			ownerRow := propsheet.Select("Owner", loginNames, indexOf(loginNames, j.OwnerLoginName))
			categoryRow := propsheet.Select("Category", catNames, indexOf(catNames, j.Category))
			enabledRow := propsheet.Check("Enabled", j.IsEnabled)
			descRow := propsheet.Text("Description", j.Description, 50)

			f := propsheet.NewForm(
				propsheet.Section("Job identity"),
				nameRow, ownerRow, categoryRow, enabledRow, descRow,
				propsheet.Section("Current execution summary"),
				propsheet.Static("Status", formatJobState(j.CurrentState)),
				propsheet.Static("Last outcome", formatJobOutcome(j.LastRunOutcome)),
				propsheet.Static("Last run", dashIfZero(j.LastRunDate)),
				propsheet.Static("Last duration", formatHMS(j.LastRunDuration)),
				propsheet.Static("Next run", dashIfZero(j.NextRunDate)),
				propsheet.Section("SQL-only backing objects"),
				propsheet.Note("msdb.dbo.sysjobs, syscategories, sysjobactivity, sysjobhistory"),
			)

			apply := func(ctx context.Context) error {
				j, err := findAgentJob(ctx, sc, *jobName)
				if err != nil {
					return err
				}
				if nameRow.Dirty() {
					if err := j.RenameContext(ctx, nameRow.Value()); err != nil {
						return err
					}
					// Update the shared cell immediately so any later
					// page's apply in this same pipeline run (and any
					// page reload after a successful Apply/OK, via
					// PropDialog.InvalidateAll) re-fetches the job under
					// its new name instead of one that no longer exists.
					*jobName = nameRow.Value()
				}
				if descRow.Dirty() {
					if err := j.SetDescriptionContext(ctx, descRow.Value()); err != nil {
						return err
					}
				}
				if categoryRow.Dirty() {
					if err := j.SetCategoryContext(ctx, categoryRow.Value()); err != nil {
						return err
					}
				}
				if ownerRow.Dirty() {
					if err := j.SetOwnerContext(ctx, ownerRow.Value()); err != nil {
						return err
					}
				}
				if enabledRow.Dirty() {
					if enabledRow.Checked() {
						err = j.EnableContext(ctx)
					} else {
						err = j.DisableContext(ctx)
					}
					if err != nil {
						return err
					}
				}
				return nil
			}
			return f, apply, nil
		},
	}
}

// pageJobTargets is a read-only page mirroring the mockup's "Targets"
// page: this is a single-server (local) implementation, so there's no
// live target-server enlistment state to show beyond the connection
// itself, and multi-server administration (MSX/TSX) is out of scope.
func pageJobTargets(sc *db.ServerConn) propPage {
	return propPage{
		title: "Targets",
		load: func(ctx context.Context) (*propsheet.Form, propApply, error) {
			f := propsheet.NewForm(
				propsheet.Section("Target servers"),
				propsheet.Static("Target type", "Local server"),
				propsheet.Static("Target server", sc.Opts.Server),
				propsheet.Static("Enlisted", "Yes"),
				propsheet.Section("Multi-server administration"),
				propsheet.Note("Master/target server (MSX/TSX) operations aren't shown here — they depend on cross-server administration beyond local msdb CRUD."),
			)
			return f, nil, nil
		},
	}
}
