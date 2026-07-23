package tui

import (
	"context"
	"fmt"

	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// agent_menu.go builds the Object Explorer context menu for SQL Server
// Agent nodes and the actions those items run — Start/Stop/Enable/Disable/
// Delete/View History, everything already backed by gosmo as of Phase 1,
// plus the New Job/Schedule/Alert/Operator entry points (Phase 4; dialogs
// themselves live in new_job_dialog.go/new_schedule_dialog.go/
// new_alert_dialog.go/new_operator_dialog.go).

// showNewJobDialog opens New Job for a known connection — the Object
// Explorer context menu's entry point for SQL Server Agent > Jobs > User
// Jobs (mirrors showNewLoginDialog).
func (a *App) showNewJobDialog(sc *db.ServerConn) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.newJobDialog.show(sc)
}

// showNewScheduleDialog opens New Schedule for a known connection — the
// Object Explorer context menu's entry point for SQL Server Agent >
// Schedules.
func (a *App) showNewScheduleDialog(sc *db.ServerConn) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.newScheduleDialog.show(sc)
}

// showNewAlertDialog opens New Alert for a known connection — the Object
// Explorer context menu's entry point for SQL Server Agent > Alerts > SQL
// Server Event Alerts.
func (a *App) showNewAlertDialog(sc *db.ServerConn) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.newAlertDialog.show(sc)
}

// showNewOperatorDialog opens New Operator for a known connection — the
// Object Explorer context menu's entry point for SQL Server Agent >
// Operators.
func (a *App) showNewOperatorDialog(sc *db.ServerConn) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.newOperatorDialog.show(sc)
}

// agentJobMenuItems builds the context menu for a NodeAgentJob leaf.
func agentJobMenuItems(a *App, sc *db.ServerConn, node *explorerNode, refresh controls.MenuItem) []controls.MenuItem {
	enableLabel := "Disable Job"
	if !node.data.IsEnabled {
		enableLabel = "Enable Job"
	}
	return []controls.MenuItem{
		{Label: "Start Job", Action: func() { a.startAgentJob(sc, node) }},
		{Label: "Stop Job", Action: func() { a.stopAgentJob(sc, node) }},
		{Divider: true},
		{Label: enableLabel, Action: func() { a.setAgentJobEnabled(sc, node, !node.data.IsEnabled) }},
		{Divider: true},
		{Label: "View History", Action: func() { a.showAgentJobHistory(sc, node.data.Name) }},
		{Divider: true},
		refresh,
		{Label: "Delete Job...", Action: func() { a.deleteAgentJob(sc, node) }},
		{Divider: true},
		{Label: "Properties...", Action: func() { a.showJobPropertiesFor(sc, node.data.Name) }},
	}
}

// agentScheduleMenuItems builds the context menu for a NodeAgentSchedule leaf.
func agentScheduleMenuItems(a *App, sc *db.ServerConn, node *explorerNode, refresh controls.MenuItem) []controls.MenuItem {
	enableLabel := "Disable Schedule"
	if !node.data.IsEnabled {
		enableLabel = "Enable Schedule"
	}
	return []controls.MenuItem{
		{Label: enableLabel, Action: func() { a.setAgentScheduleEnabled(sc, node, !node.data.IsEnabled) }},
		{Divider: true},
		refresh,
		{Label: "Delete Schedule...", Action: func() { a.deleteAgentSchedule(sc, node) }},
		{Divider: true},
		{Label: "Properties...", Action: func() { a.showScheduleProperties(sc, node.data.Name) }},
	}
}

// agentAlertMenuItems builds the context menu for a NodeAgentAlert leaf.
func agentAlertMenuItems(a *App, sc *db.ServerConn, node *explorerNode, refresh controls.MenuItem) []controls.MenuItem {
	enableLabel := "Disable Alert"
	if !node.data.IsEnabled {
		enableLabel = "Enable Alert"
	}
	return []controls.MenuItem{
		{Label: enableLabel, Action: func() { a.setAgentAlertEnabled(sc, node, !node.data.IsEnabled) }},
		{Divider: true},
		refresh,
		{Label: "Delete Alert...", Action: func() { a.deleteAgentAlert(sc, node) }},
		{Divider: true},
		{Label: "Properties...", Action: func() { a.showAlertProperties(sc, node.data.Name) }},
	}
}

// agentOperatorMenuItems builds the context menu for a NodeAgentOperator leaf.
func agentOperatorMenuItems(a *App, sc *db.ServerConn, node *explorerNode, refresh controls.MenuItem) []controls.MenuItem {
	enableLabel := "Disable Operator"
	if !node.data.IsEnabled {
		enableLabel = "Enable Operator"
	}
	return []controls.MenuItem{
		{Label: enableLabel, Action: func() { a.setAgentOperatorEnabled(sc, node, !node.data.IsEnabled) }},
		{Divider: true},
		refresh,
		{Label: "Delete Operator...", Action: func() { a.deleteAgentOperator(sc, node) }},
		{Divider: true},
		{Label: "Properties...", Action: func() { a.showOperatorProperties(sc, node.data.Name) }},
	}
}

// ---- Jobs: Start / Stop / View History ----

func (a *App) startAgentJob(sc *db.ServerConn, node *explorerNode) {
	name := node.data.Name
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
		defer cancel()
		j, err := sc.Server.JobByNameContext(ctx, name)
		if err == nil {
			err = j.StartContext(ctx, "")
		}
		a.postEvent(func() {
			if err != nil {
				a.setStatus(fmt.Sprintf("Failed to start job %q: %v", name, err))
				return
			}
			a.setStatus(fmt.Sprintf("Job %q started", name))
			a.detailBrowser.Invalidate(a, node)
		})
		a.wakeEventLoop()
	}()
}

func (a *App) stopAgentJob(sc *db.ServerConn, node *explorerNode) {
	name := node.data.Name
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), childFetchTimeout)
		defer cancel()
		j, err := sc.Server.JobByNameContext(ctx, name)
		if err == nil {
			err = j.StopContext(ctx)
		}
		a.postEvent(func() {
			if err != nil {
				a.setStatus(fmt.Sprintf("Failed to stop job %q: %v", name, err))
				return
			}
			a.setStatus(fmt.Sprintf("Job %q stopped", name))
			a.detailBrowser.Invalidate(a, node)
		})
		a.wakeEventLoop()
	}()
}

// showAgentJobHistory opens a new query window against msdb, pre-filled
// with agentJobHistoryQuery(jobName) and running it immediately — mirrors
// showBackupHistoryFor's identical pattern for a database's backup history.
func (a *App) showAgentJobHistory(sc *db.ServerConn, jobName string) {
	if !a.isConnected(sc) {
		a.setStatus("Not connected — use File > Connect")
		return
	}
	a.openQueryWithTextAndExecute(sc, "msdb", agentJobHistoryQuery(jobName))
}

// ---- Enable / Disable ----

func (a *App) setAgentJobEnabled(sc *db.ServerConn, node *explorerNode, enable bool) {
	name := node.data.Name
	a.setAgentEnabled(node, enable, func(ctx context.Context) error {
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
	a.setAgentEnabled(node, enable, func(ctx context.Context) error {
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
	a.setAgentEnabled(node, enable, func(ctx context.Context) error {
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
	a.setAgentEnabled(node, enable, func(ctx context.Context) error {
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
	a.deleteAgentEntity(node, "Delete Job",
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
	a.deleteAgentEntity(node, "Delete Schedule",
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
	a.deleteAgentEntity(node, "Delete Alert",
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
	a.deleteAgentEntity(node, "Delete Operator",
		fmt.Sprintf("Delete operator %q? This cannot be undone.", name),
		func(ctx context.Context) error {
			o, err := sc.Server.OperatorByNameContext(ctx, name)
			if err != nil {
				return err
			}
			return o.DropContext(ctx)
		})
}
