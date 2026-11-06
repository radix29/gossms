package tui

import (
	"context"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/gate"
)

// agent_explorer.go loads the SQL Server Agent subtree: Jobs (User/System),
// Schedules, Alerts (SQL Server event alerts only; see
// gosmo.Server.EventAlerts), Operators, Error Logs, and SQL-only administration
// reports.

// agentReportTitles lists the administration folder's reports in display order;
// each title is agentReportDetail's dispatch key (agent_reports.go).
var agentReportTitles = []string{
	"Agent Metadata Summary",
	"Job Execution Summary",
	"Failed Job Runs",
	"Disabled Jobs",
	"Jobs Without Schedules",
	"Jobs Without Notifications",
	"Recently Modified Jobs",
}

// loadAgentRootChildren returns the Agent root's folders. Alerts is left out
// for a login that cannot read them (see agentAlertsReadable), as SSMS does,
// rather than shown and answering Access denied on expand.
func loadAgentRootChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	out := []*explorerNode{
		l.node("Jobs", NodeAgentJobsFolder, "", "", ""),
		l.node("Schedules", NodeAgentSchedules, "", "", ""),
	}
	if agentAlertsReadable(l.ctx, l.sc) {
		out = append(out, l.node("Alerts", NodeAgentAlerts, "", "", ""))
	}
	return append(out,
		l.node("Operators", NodeAgentOperators, "", "", ""),
		l.node("Error Logs", NodeAgentErrorLogs, "", "", ""),
		l.node("SQL-only administration", NodeAgentAdmin, "", "", ""),
	), nil
}

// agentAlertsReadable reports whether the login may read SQL Agent's alerts
// (gate.AgentAlertReadRights) — false for a SQLAgentReaderRole or
// SQLAgentUserRole member, whom msdb refuses sysalerts. It probes msdb, so it
// runs off the UI goroutine (a loader or a page load), and fails open like
// every gate: an unprobed or failed probe reads as readable.
func agentAlertsReadable(ctx context.Context, sc *db.ServerConn) bool {
	dbCaps := func(name string) *gosmo.DatabaseCapabilities { return sc.DatabaseCapabilities(ctx, name) }
	return gate.RightsAllow(sc.Capabilities(), dbCaps, "msdb", "", "", gate.AgentAlertReadRights()...)
}

// loadAgentJobsFolderChildren returns User Jobs, System Jobs, and the Job
// Activity, Job History and Job Categories leaves.
func loadAgentJobsFolderChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("User Jobs", NodeAgentUserJobs, "", "", ""),
		l.node("System / Internal Jobs", NodeAgentSystemJobs, "", "", ""),
		l.node("Job Activity", NodeAgentJobActivity, "", "", ""),
		l.node("Job History", NodeAgentJobHistory, "", "", ""),
		l.node("Job Categories", NodeAgentJobCategories, "", "", ""),
	}, nil
}

// loadAgentUserJobsChildren returns every non-system job (see
// gosmo.Job.IsSystem, which also gates Delete/Rename through data.IsSystem).
func loadAgentUserJobsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	jobs, err := l.sc.Server.Jobs(l.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*explorerNode, 0, len(jobs))
	for _, j := range jobs {
		if j.IsSystem() {
			continue
		}
		out = append(out, agentJobNode(l, j))
	}
	return out, nil
}

// loadAgentSystemJobsChildren returns the system jobs (see gosmo.Job.IsSystem).
func loadAgentSystemJobsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	jobs, err := l.sc.Server.Jobs(l.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*explorerNode, 0)
	for _, j := range jobs {
		if j.IsSystem() {
			out = append(out, agentJobNode(l, j))
		}
	}
	return out, nil
}

// agentJobNode builds a NodeAgentJob leaf from a gosmo.Job.
func agentJobNode(l loaderCtx, j *gosmo.Job) *explorerNode {
	n := l.node(j.Name, NodeAgentJob, "", j.Name, "")
	n.data.IsEnabled = j.IsEnabled
	n.data.IsSystem = j.IsSystem()
	return n
}

// loadAgentSchedulesChildren returns every SQL Server Agent schedule.
func loadAgentSchedulesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.Schedule, error) { return l.sc.Server.Schedules(l.ctx) },
		func(sch *gosmo.Schedule) *explorerNode {
			n := l.node(sch.Name, NodeAgentSchedule, "", sch.Name, "")
			n.data.IsEnabled = sch.Enabled
			n.data.AgentScheduleID = sch.ID
			n.data.AgentScheduleDesc = sch.Description()
			return n
		})
}

// loadAgentAlertsChildren returns the "Alerts" folder's children: the SQL
// Server Event Alerts folder and the Alert Categories report leaf.
func loadAgentAlertsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("SQL Server Event Alerts", NodeAgentEventAlerts, "", "", ""),
		l.node("Alert Categories", NodeAgentAlertCategories, "", "", ""),
	}, nil
}

// loadAgentEventAlertsChildren returns the SQL-only alert subset (see
// gosmo.Server.EventAlerts).
func loadAgentEventAlertsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.Alert, error) { return l.sc.Server.EventAlerts(l.ctx) },
		func(a *gosmo.Alert) *explorerNode {
			n := l.node(a.Name, NodeAgentAlert, "", a.Name, "")
			n.data.IsEnabled = a.Enabled
			return n
		})
}

// loadAgentOperatorsChildren returns every SQL Server Agent operator.
func loadAgentOperatorsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.Operator, error) { return l.sc.Server.Operators(l.ctx) },
		func(o *gosmo.Operator) *explorerNode {
			n := l.node(o.Name, NodeAgentOperator, "", o.Name, "")
			n.data.IsEnabled = o.Enabled
			return n
		})
}

// loadAgentAdminChildren returns the administration folder's report leaves (see
// agentReportTitles, agent_reports.go).
func loadAgentAdminChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	out := make([]*explorerNode, 0, len(agentReportTitles))
	for _, title := range agentReportTitles {
		out = append(out, l.node(title, NodeAgentReport, "", title, ""))
	}
	return out, nil
}

// isAgentNode reports whether t is an Agent node type. Agent nodes carry no
// DBName but their permissions live in msdb, so this tells
// primeDatabaseCapabilities which database to probe. The types are the
// contiguous NodeAgent* block in tree_node.go; add new ones inside it.
func isAgentNode(t NodeType) bool {
	return t >= NodeAgentJobs && t <= NodeAgentErrorLog
}
