package tui

import (
	"fmt"
	"sort"
	"strconv"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// agent_reports.go builds the "SQL-only administration" folder's report
// leaves (see agentReportTitles in agent_explorer.go) and the canned SQL
// behind Object Explorer's "View History" action on a job.

// agentReportDetail dispatches a NodeAgentReport leaf's title to its
// report builder.
func agentReportDetail(sc *db.ServerConn, title string) ([]string, [][]string, error) {
	switch title {
	case "Agent Metadata Summary":
		return agentMetadataSummaryReport(sc)
	case "Job Execution Summary":
		return jobExecutionSummaryReport(sc)
	case "Failed Job Runs":
		return failedJobRunsReport(sc)
	case "Disabled Jobs":
		return disabledJobsReport(sc)
	case "Jobs Without Schedules":
		return jobsWithoutSchedulesReport(sc)
	case "Jobs Without Notifications":
		return jobsWithoutNotificationsReport(sc)
	case "Recently Modified Jobs":
		return recentlyModifiedJobsReport(sc)
	default:
		return nil, nil, fmt.Errorf("unknown SQL Server Agent report %q", title)
	}
}

func agentMetadataSummaryReport(sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.Jobs()
	if err != nil {
		return nil, nil, err
	}
	schedules, schErr := sc.Server.Schedules()
	alerts, aErr := sc.Server.Alerts()
	eventAlerts, eaErr := sc.Server.EventAlerts()
	operators, oErr := sc.Server.Operators()
	jobCats, jcErr := sc.Server.Categories(gosmo.CategoryClassJob)
	alertCats, acErr := sc.Server.Categories(gosmo.CategoryClassAlert)

	enabled, disabled := 0, 0
	for _, j := range jobs {
		if j.IsEnabled {
			enabled++
		} else {
			disabled++
		}
	}

	statusText := "Unknown"
	if status, err := sc.Server.AgentInfo(); err == nil {
		statusText = status.StatusText
	}

	rows := [][]string{
		{"Agent status", statusText},
		{"Total jobs", strconv.Itoa(len(jobs))},
		{"Enabled jobs", strconv.Itoa(enabled)},
		{"Disabled jobs", strconv.Itoa(disabled)},
		{"Schedules", countOrDash(len(schedules), schErr)},
		{"Alerts (all)", countOrDash(len(alerts), aErr)},
		{"Alerts (SQL-only event alerts)", countOrDash(len(eventAlerts), eaErr)},
		{"Operators", countOrDash(len(operators), oErr)},
		{"Job categories", countOrDash(len(jobCats), jcErr)},
		{"Alert categories", countOrDash(len(alertCats), acErr)},
	}
	return []string{"Property", "Value"}, rows, nil
}

func jobExecutionSummaryReport(sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.Jobs()
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(jobs))
	for _, j := range jobs {
		rows = append(rows, []string{
			j.Name, fmt.Sprintf("%v", j.IsEnabled), j.Category,
			formatJobOutcome(j.LastRunOutcome), dashIfZero(j.LastRunDate), dashIfZero(j.NextRunDate),
		})
	}
	return []string{"Job Name", "Enabled", "Category", "Last Outcome", "Last Run", "Next Run"}, rows, nil
}

func failedJobRunsReport(sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.Jobs()
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0)
	for _, j := range jobs {
		if j.LastRunOutcome != gosmo.JobOutcomeFailed {
			continue
		}
		rows = append(rows, []string{j.Name, j.Category, dashIfZero(j.LastRunDate), j.LastRunDuration.String()})
	}
	return []string{"Job Name", "Category", "Last Run", "Duration"}, rows, nil
}

func disabledJobsReport(sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.Jobs()
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0)
	for _, j := range jobs {
		if j.IsEnabled {
			continue
		}
		rows = append(rows, []string{j.Name, j.Category, j.OwnerLoginName, formatSQLDate(j.DateModified)})
	}
	return []string{"Job Name", "Category", "Owner", "Last Modified"}, rows, nil
}

// jobsWithoutSchedulesReport fetches each job's attached schedules to
// determine whether it has any — one round trip per job, acceptable for
// the modest job counts a single SQL Server Agent instance typically has.
func jobsWithoutSchedulesReport(sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.Jobs()
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0)
	for _, j := range jobs {
		scheds, err := j.Schedules()
		if err != nil || len(scheds) > 0 {
			continue
		}
		rows = append(rows, []string{j.Name, j.Category, fmt.Sprintf("%v", j.IsEnabled)})
	}
	return []string{"Job Name", "Category", "Enabled"}, rows, nil
}

func jobsWithoutNotificationsReport(sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.Jobs()
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0)
	for _, j := range jobs {
		if j.NotifyLevelEmail != gosmo.NotifyNever {
			continue
		}
		rows = append(rows, []string{j.Name, j.Category, fmt.Sprintf("%v", j.IsEnabled)})
	}
	return []string{"Job Name", "Category", "Enabled"}, rows, nil
}

func recentlyModifiedJobsReport(sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.Jobs()
	if err != nil {
		return nil, nil, err
	}
	sorted := make([]*gosmo.Job, len(jobs))
	copy(sorted, jobs)
	sort.Slice(sorted, func(i, k int) bool { return sorted[i].DateModified.After(sorted[k].DateModified) })
	if len(sorted) > 20 {
		sorted = sorted[:20]
	}
	rows := make([][]string, 0, len(sorted))
	for _, j := range sorted {
		rows = append(rows, []string{j.Name, j.Category, formatSQLDate(j.DateModified)})
	}
	return []string{"Job Name", "Category", "Last Modified"}, rows, nil
}

// dashIfZero renders t, or "—" if it's the zero Time.
func dashIfZero(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return formatSQLDate(t)
}

// agentJobHistoryQuery builds the read-only T-SQL behind Object Explorer's
// "View History" action on a job — opened and run immediately in a new
// query window, mirroring backupHistoryQuery's pattern.
func agentJobHistoryQuery(jobName string) string {
	return fmt.Sprintf(`SELECT h.run_date                 AS [Run Date],
       h.run_time                 AS [Run Time],
       CASE h.run_status
           WHEN 0 THEN 'Failed'
           WHEN 1 THEN 'Succeeded'
           WHEN 2 THEN 'Retry'
           WHEN 3 THEN 'Cancelled'
           ELSE 'In Progress'
       END                        AS [Outcome],
       h.run_duration             AS [Duration],
       h.step_id                  AS [Step],
       h.step_name                AS [Step Name],
       h.message                  AS [Message]
FROM   msdb.dbo.sysjobhistory h
JOIN   msdb.dbo.sysjobs j ON j.job_id = h.job_id
WHERE  j.name = %s
ORDER  BY h.run_date DESC, h.run_time DESC;
`, sqlStringLiteral(jobName))
}
