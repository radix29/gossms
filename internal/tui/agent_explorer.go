package tui

import (
	"strings"

	gosmo "github.com/radix29/gosmo"
)

// agent_explorer.go loads the SQL Server Agent subtree — Jobs (User/System),
// Schedules, Alerts (SQL Server Event Alerts only — see
// gosmo.Server.EventAlerts), Operators, and a small set of SQL-only
// administration reports. Matches the SQL-only Object Explorer mockup:
// todo/mockups/sql_agent_object_explorer_sql_only_tui.txt.

// agentReportTitles lists the "SQL-only administration" folder's report
// leaves, in the order the mockup shows them. Each title doubles as the
// lookup key agentReportDetail dispatches on — see agent_reports.go.
var agentReportTitles = []string{
	"Agent Metadata Summary",
	"Job Execution Summary",
	"Failed Job Runs",
	"Disabled Jobs",
	"Jobs Without Schedules",
	"Jobs Without Notifications",
	"Recently Modified Jobs",
}

// loadAgentRootChildren returns the "SQL Server Agent" root's top-level
// folders.
func loadAgentRootChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("Jobs", NodeAgentJobsFolder, "", "", ""),
		l.node("Schedules", NodeAgentSchedules, "", "", ""),
		l.node("Alerts", NodeAgentAlerts, "", "", ""),
		l.node("Operators", NodeAgentOperators, "", "", ""),
		l.node("SQL-only administration", NodeAgentAdmin, "", "", ""),
	}, nil
}

// loadAgentJobsFolderChildren returns the "Jobs" folder's children: the
// User Jobs / System Jobs split, plus the Job Activity, Job History, and
// Job Categories report leaves.
func loadAgentJobsFolderChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return []*explorerNode{
		l.node("User Jobs", NodeAgentUserJobs, "", "", ""),
		l.node("System / Internal Jobs", NodeAgentSystemJobs, "", "", ""),
		l.node("Job Activity", NodeAgentJobActivity, "", "", ""),
		l.node("Job History", NodeAgentJobHistory, "", "", ""),
		l.node("Job Categories", NodeAgentJobCategories, "", "", ""),
	}, nil
}

// isSystemAgentJob reports whether j is a SQL-Server-created job rather
// than a user job — e.g. syspolicy_purge_history, created automatically
// when Policy-Based Management history retention is configured. msdb has
// no dedicated "is system job" flag, so this is a name-based heuristic
// (every built-in Agent job is named with the "syspolicy_" prefix).
func isSystemAgentJob(j *gosmo.Job) bool {
	return strings.HasPrefix(j.Name, "syspolicy_")
}

// loadAgentUserJobsChildren returns every job that isn't a SQL-Server-
// created system job (see isSystemAgentJob).
func loadAgentUserJobsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	jobs, err := l.sc.Server.JobsContext(l.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*explorerNode, 0, len(jobs))
	for _, j := range jobs {
		if isSystemAgentJob(j) {
			continue
		}
		out = append(out, agentJobNode(l, j))
	}
	return out, nil
}

// loadAgentSystemJobsChildren returns the SQL-Server-created system jobs
// (see isSystemAgentJob).
func loadAgentSystemJobsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	jobs, err := l.sc.Server.JobsContext(l.ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*explorerNode, 0)
	for _, j := range jobs {
		if isSystemAgentJob(j) {
			out = append(out, agentJobNode(l, j))
		}
	}
	return out, nil
}

// agentJobNode builds a NodeAgentJob leaf from a gosmo.Job.
func agentJobNode(l loaderCtx, j *gosmo.Job) *explorerNode {
	n := l.node(j.Name, NodeAgentJob, "", j.Name, "")
	n.data.IsEnabled = j.IsEnabled
	return n
}

// loadAgentSchedulesChildren returns every SQL Server Agent schedule.
func loadAgentSchedulesChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.Schedule, error) { return l.sc.Server.SchedulesContext(l.ctx) },
		func(sch *gosmo.Schedule) *explorerNode {
			n := l.node(sch.Name, NodeAgentSchedule, "", sch.Name, "")
			n.data.IsEnabled = sch.Enabled
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

// loadAgentEventAlertsChildren returns the SQL-only-implementable subset of
// alerts — see gosmo.Server.EventAlerts.
func loadAgentEventAlertsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.Alert, error) { return l.sc.Server.EventAlertsContext(l.ctx) },
		func(a *gosmo.Alert) *explorerNode {
			n := l.node(a.Name, NodeAgentAlert, "", a.Name, "")
			n.data.IsEnabled = a.Enabled
			return n
		})
}

// loadAgentOperatorsChildren returns every SQL Server Agent operator.
func loadAgentOperatorsChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	return listChildren(func() ([]*gosmo.Operator, error) { return l.sc.Server.OperatorsContext(l.ctx) },
		func(o *gosmo.Operator) *explorerNode {
			n := l.node(o.Name, NodeAgentOperator, "", o.Name, "")
			n.data.IsEnabled = o.Enabled
			return n
		})
}

// loadAgentAdminChildren returns the "SQL-only administration" folder's
// static report leaves — see agentReportTitles and agent_reports.go.
func loadAgentAdminChildren(l loaderCtx, node *explorerNode) ([]*explorerNode, error) {
	out := make([]*explorerNode, 0, len(agentReportTitles))
	for _, title := range agentReportTitles {
		out = append(out, l.node(title, NodeAgentReport, "", title, ""))
	}
	return out, nil
}
