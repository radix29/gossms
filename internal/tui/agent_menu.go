package tui

import (
	"context"
	"fmt"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// agent_menu.go builds Object Explorer context menus for SQL Server Agent nodes
// and their actions (Start/Stop/Enable/Disable/Delete/View History), plus New
// Job/Schedule/Alert/Operator entry points (dialogs in
// new_{job,schedule,alert,operator}_dialog.go).

// showNewJobDialog opens New Job, from Agent > Jobs > User Jobs (mirrors
// showNewLoginDialog).
func (a *App) showNewJobDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.newJobDialog.show(sc)
}

// showNewScheduleDialog opens New Schedule, from Agent > Schedules.
func (a *App) showNewScheduleDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.newScheduleDialog.show(sc)
}

// showNewAlertDialog opens New Alert, from Agent > Alerts > SQL Server Event
// Alerts.
func (a *App) showNewAlertDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.newAlertDialog.show(sc)
}

// showNewOperatorDialog opens New Operator, from Agent > Operators.
func (a *App) showNewOperatorDialog(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	a.newOperatorDialog.show(sc)
}

// agentGate gates an Agent write (folders' New items; leaves' Start/Stop,
// Enable/Disable, Delete) on the msdb rights Rename gets via
// serverScopedOpRights. Each is an msdb sp_* call refused to logins in no
// SQLAgent* role.
func agentGate(item controls.MenuItem, sc *db.ServerConn) controls.MenuItem {
	return gate(item, sc, "msdb", agentWriteRights()...)
}

// agentJobMenuItems builds the context menu for a NodeAgentJob leaf.
func agentJobMenuItems(a *App, sc *db.ServerConn, node *explorerNode, _, refresh controls.MenuItem) []controls.MenuItem {
	enableLabel := "Disable Job"
	if !node.data.IsEnabled {
		enableLabel = "Enable Job"
	}
	return []controls.MenuItem{
		agentGate(controls.MenuItem{Label: "Start Job", Action: func() { a.startAgentJob(sc, node) }}, sc),
		agentGate(controls.MenuItem{Label: "Stop Job", Action: func() { a.stopAgentJob(sc, node) }}, sc),
		{Divider: true},
		agentGate(controls.MenuItem{Label: enableLabel, Action: func() { a.setAgentJobEnabled(sc, node, !node.data.IsEnabled) }}, sc),
		{Divider: true},
		{Label: "View History", Action: func() { a.showAgentJobHistory(sc, node.data.Name) }},
		{Divider: true},
		refresh,
		agentGate(controls.MenuItem{Label: "Delete Job...", Action: func() { a.deleteAgentJob(sc, node) }}, sc),
		{Divider: true},
		{Label: "Properties...", Action: func() { a.showJobPropertiesFor(sc, node.data.Name) }},
	}
}

// agentScheduleMenuItems builds the context menu for a NodeAgentSchedule leaf.
func agentScheduleMenuItems(a *App, sc *db.ServerConn, node *explorerNode, _, refresh controls.MenuItem) []controls.MenuItem {
	enableLabel := "Disable Schedule"
	if !node.data.IsEnabled {
		enableLabel = "Enable Schedule"
	}
	return []controls.MenuItem{
		agentGate(controls.MenuItem{Label: enableLabel, Action: func() { a.setAgentScheduleEnabled(sc, node, !node.data.IsEnabled) }}, sc),
		{Divider: true},
		refresh,
		agentGate(controls.MenuItem{Label: "Delete Schedule...", Action: func() { a.deleteAgentSchedule(sc, node) }}, sc),
		{Divider: true},
		{Label: "Properties...", Action: func() { a.showScheduleProperties(sc, node.data.Name) }},
	}
}

// agentAlertMenuItems builds the context menu for a NodeAgentAlert leaf.
func agentAlertMenuItems(a *App, sc *db.ServerConn, node *explorerNode, _, refresh controls.MenuItem) []controls.MenuItem {
	enableLabel := "Disable Alert"
	if !node.data.IsEnabled {
		enableLabel = "Enable Alert"
	}
	return []controls.MenuItem{
		agentGate(controls.MenuItem{Label: enableLabel, Action: func() { a.setAgentAlertEnabled(sc, node, !node.data.IsEnabled) }}, sc),
		{Divider: true},
		refresh,
		agentGate(controls.MenuItem{Label: "Delete Alert...", Action: func() { a.deleteAgentAlert(sc, node) }}, sc),
		{Divider: true},
		{Label: "Properties...", Action: func() { a.showAlertProperties(sc, node.data.Name) }},
	}
}

// agentOperatorMenuItems builds the context menu for a NodeAgentOperator leaf.
func agentOperatorMenuItems(a *App, sc *db.ServerConn, node *explorerNode, _, refresh controls.MenuItem) []controls.MenuItem {
	enableLabel := "Disable Operator"
	if !node.data.IsEnabled {
		enableLabel = "Enable Operator"
	}
	return []controls.MenuItem{
		agentGate(controls.MenuItem{Label: enableLabel, Action: func() { a.setAgentOperatorEnabled(sc, node, !node.data.IsEnabled) }}, sc),
		{Divider: true},
		refresh,
		agentGate(controls.MenuItem{Label: "Delete Operator...", Action: func() { a.deleteAgentOperator(sc, node) }}, sc),
		{Divider: true},
		{Label: "Properties...", Action: func() { a.showOperatorProperties(sc, node.data.Name) }},
	}
}

// agentUserJobsMenuItems builds the User Jobs folder menu; Schedules, Event
// Alerts and Operators follow.
func agentUserJobsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		agentGate(controls.MenuItem{Label: "New Job...", Action: func() { a.showNewJobDialog(sc) }}, sc),
		{Divider: true},
		refresh,
	}
}

func agentSchedulesMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		agentGate(controls.MenuItem{Label: "New Schedule...", Action: func() { a.showNewScheduleDialog(sc) }}, sc),
		{Divider: true},
		refresh,
	}
}

func agentEventAlertsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		agentGate(controls.MenuItem{Label: "New Alert...", Action: func() { a.showNewAlertDialog(sc) }}, sc),
		{Divider: true},
		refresh,
	}
}

func agentOperatorsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		agentGate(controls.MenuItem{Label: "New Operator...", Action: func() { a.showNewOperatorDialog(sc) }}, sc),
		{Divider: true},
		refresh,
	}
}

// ---- Jobs: Start / Stop / View History ----

// jobIsRunning reports whether sp_start_job would be refused and sp_stop_job
// accepted, and whether the state knows.
//
// Only states with a certain live session answer. JobStateUnknown (a job Agent
// doesn't run, or gosmo's fallback when xp_sqlagent_enum_jobs is unreachable)
// and JobStateSuspended (sp_help_job groups it with idle, yet it holds a
// session) return known=false, so the server decides.
func jobIsRunning(state gosmo.JobState) (running, known bool) {
	switch state {
	case gosmo.JobStateExecuting, gosmo.JobStateWaitingForWorker,
		gosmo.JobStateBetweenRetries, gosmo.JobStateWaitingForStepToFinish,
		gosmo.JobStatePerformingCompletionActions:
		return true, true
	case gosmo.JobStateIdle:
		return false, true
	}
	return false, false
}

// jobStateRefusal is the message for a Start/Stop on a job already in the
// target state, or "".
//
// Checked at action time rather than gating the menu: the node's cached state
// is as old as the folder load, and greying would hide a valid Stop. The node
// is refreshed either way.
func jobStateRefusal(name string, state gosmo.JobState, wantRunning bool) string {
	running, known := jobIsRunning(state)
	switch {
	case !known:
		return ""
	case running && wantRunning:
		return fmt.Sprintf("Job %q is already running", name)
	case !running && !wantRunning:
		return fmt.Sprintf("Job %q is not running", name)
	}
	return ""
}

// runAgentJobStateAction is Start and Stop Job. Both run behind the progress
// dialog, unconfirmed as in SSMS; the state read and sp_start_job/sp_stop_job
// are one job, so Cancel stops whichever is running.
func (a *App) runAgentJobStateAction(sc *db.ServerConn, node *explorerNode, start bool) {
	if !a.requireConn(sc) {
		return
	}
	name := node.data.Name
	verb, doneVerb, doing, title, what := "stop", "stopped", "Stopping", "Stop Job", "stopping an Agent job"
	if start {
		verb, doneVerb, doing, title, what = "start", "started", "Starting", "Start Job", "starting an Agent job"
	}
	// Written by the work goroutine before returning; read only in done, posted
	// after.
	var refusal string
	a.runWithProgress(progressJob{
		title:   title,
		message: fmt.Sprintf("%s job %q...", doing, name),
		what:    what,
		sc:      sc,
	}, func(ctx context.Context, _ progressReport) error {
		j, err := sc.Server.JobByNameContext(ctx, name)
		if err != nil {
			return err
		}
		if refusal = jobStateRefusal(name, j.CurrentState, start); refusal != "" {
			return nil
		}
		if start {
			return j.StartContext(ctx, "")
		}
		return j.StopContext(ctx)
	}, func(err error, cancelled bool) {
		switch {
		case cancelled:
			// Invalidated anyway: Agent may have accepted before the cancel
			// arrived.
			a.setStatus(fmt.Sprintf("%s job %q cancelled", doing, name))
		case err != nil:
			a.setStatus(fmt.Sprintf("Failed to %s job %q: %v", verb, name, err))
			return
		case refusal != "":
			a.setStatus(refusal)
		default:
			a.setStatus(fmt.Sprintf("Job %q %s", name, doneVerb))
		}
		a.detailBrowser.Invalidate(a, node)
	})
}

func (a *App) startAgentJob(sc *db.ServerConn, node *explorerNode) {
	a.runAgentJobStateAction(sc, node, true)
}

func (a *App) stopAgentJob(sc *db.ServerConn, node *explorerNode) {
	a.runAgentJobStateAction(sc, node, false)
}

// showAgentJobHistory opens a query window on msdb with
// agentJobHistoryQuery(jobName) and runs it (like showBackupHistoryFor).
func (a *App) showAgentJobHistory(sc *db.ServerConn, jobName string) {
	if !a.requireConn(sc) {
		return
	}
	a.openQueryWithTextAndExecute(sc, "msdb", agentJobHistoryQuery(jobName))
}

// ---- Enable / Disable ----

func (a *App) setAgentJobEnabled(sc *db.ServerConn, node *explorerNode, enable bool) {
	name := node.data.Name
	a.setAgentEnabled(sc, node, "job", enable, func(ctx context.Context) error {
		j, err := sc.Server.JobByNameContext(ctx, name)
		if err != nil {
			return err
		}
		if enable {
			return j.EnableContext(ctx)
		}
		return j.DisableContext(ctx)
	})
}

func (a *App) setAgentScheduleEnabled(sc *db.ServerConn, node *explorerNode, enable bool) {
	name := node.data.Name
	a.setAgentEnabled(sc, node, "schedule", enable, func(ctx context.Context) error {
		sch, err := sc.Server.ScheduleByNameContext(ctx, name)
		if err != nil {
			return err
		}
		if enable {
			return sch.EnableContext(ctx)
		}
		return sch.DisableContext(ctx)
	})
}

func (a *App) setAgentAlertEnabled(sc *db.ServerConn, node *explorerNode, enable bool) {
	name := node.data.Name
	a.setAgentEnabled(sc, node, "alert", enable, func(ctx context.Context) error {
		al, err := sc.Server.AlertByNameContext(ctx, name)
		if err != nil {
			return err
		}
		if enable {
			return al.EnableContext(ctx)
		}
		return al.DisableContext(ctx)
	})
}

func (a *App) setAgentOperatorEnabled(sc *db.ServerConn, node *explorerNode, enable bool) {
	name := node.data.Name
	a.setAgentEnabled(sc, node, "operator", enable, func(ctx context.Context) error {
		o, err := sc.Server.OperatorByNameContext(ctx, name)
		if err != nil {
			return err
		}
		if enable {
			return o.EnableContext(ctx)
		}
		return o.DisableContext(ctx)
	})
}

// ---- Delete ----

func (a *App) deleteAgentJob(sc *db.ServerConn, node *explorerNode) {
	name := node.data.Name
	a.deleteAgentEntity(sc, node, "Delete Job",
		fmt.Sprintf("Delete SQL Server Agent job %q? This cannot be undone.", name),
		func(ctx context.Context) error {
			j, err := sc.Server.JobByNameContext(ctx, name)
			if err != nil {
				return err
			}
			return j.DropContext(ctx)
		})
}

func (a *App) deleteAgentSchedule(sc *db.ServerConn, node *explorerNode) {
	name := node.data.Name
	a.deleteAgentEntity(sc, node, "Delete Schedule",
		fmt.Sprintf("Delete schedule %q? A schedule still attached to a job can't be deleted until it's detached.", name),
		func(ctx context.Context) error {
			sch, err := sc.Server.ScheduleByNameContext(ctx, name)
			if err != nil {
				return err
			}
			return sch.DropContext(ctx)
		})
}

func (a *App) deleteAgentAlert(sc *db.ServerConn, node *explorerNode) {
	name := node.data.Name
	a.deleteAgentEntity(sc, node, "Delete Alert",
		fmt.Sprintf("Delete alert %q? This cannot be undone.", name),
		func(ctx context.Context) error {
			al, err := sc.Server.AlertByNameContext(ctx, name)
			if err != nil {
				return err
			}
			return al.DropContext(ctx)
		})
}

func (a *App) deleteAgentOperator(sc *db.ServerConn, node *explorerNode) {
	name := node.data.Name
	a.deleteAgentEntity(sc, node, "Delete Operator",
		fmt.Sprintf("Delete operator %q? This cannot be undone.", name),
		func(ctx context.Context) error {
			o, err := sc.Server.OperatorByNameContext(ctx, name)
			if err != nil {
				return err
			}
			return o.DropContext(ctx)
		})
}
