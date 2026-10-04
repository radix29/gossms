package tui

import (
	gosmo "github.com/radix29/gosmo"
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

// loadAgentRootChildren returns the Agent root's folders.
func loadAgentRootChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Jobs", NodeAgentJobsFolder, "", "", ""),
		l.node("Schedules", NodeAgentSchedules, "", "", ""),
		l.node("Alerts", NodeAgentAlerts, "", "", ""),
		l.node("Operators", NodeAgentOperators, "", "", ""),
		l.node("Error Logs", NodeAgentErrorLogs, "", "", ""),
		l.node("SQL-only administration", NodeAgentAdmin, "", "", ""),
	}, nil
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
