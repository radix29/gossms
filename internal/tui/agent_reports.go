package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// agent_reports.go builds the SQL-only administration folder's reports (see
// agentReportTitles) and the SQL behind a job's "View History".

// agentReportDetail dispatches a NodeAgentReport title to its builder.
func agentReportDetail(ctx context.Context, sc *db.ServerConn, title string) ([]string, [][]string, error) {
	switch title {
	case "Agent Metadata Summary":
		return agentMetadataSummaryReport(ctx, sc)
	case "Job Execution Summary":
		return jobExecutionSummaryReport(ctx, sc)
	case "Failed Job Runs":
		return failedJobRunsReport(ctx, sc)
	case "Disabled Jobs":
		return disabledJobsReport(ctx, sc)
	case "Jobs Without Schedules":
		return jobsWithoutSchedulesReport(ctx, sc)
	case "Jobs Without Notifications":
		return jobsWithoutNotificationsReport(ctx, sc)
	case "Recently Modified Jobs":
		return recentlyModifiedJobsReport(ctx, sc)
	default:
		return nil, nil, fmt.Errorf("unknown SQL Server Agent report %q", title)
	}
}

func agentMetadataSummaryReport(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.JobsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	schedules, schErr := sc.Server.SchedulesContext(ctx)
	alerts, aErr := sc.Server.AlertsContext(ctx)
	eventAlerts, eaErr := sc.Server.EventAlertsContext(ctx)
	operators, oErr := sc.Server.OperatorsContext(ctx)
	jobCats, jcErr := sc.Server.CategoriesContext(ctx, gosmo.CategoryClassJob)
	alertCats, acErr := sc.Server.CategoriesContext(ctx, gosmo.CategoryClassAlert)

	enabled, disabled := 0, 0
	for _, j := range jobs {
		if j.IsEnabled {
			enabled++
		} else {
			disabled++
		}
	}

	statusText := "Unknown"
	if status, err := sc.Server.AgentInfoContext(ctx); err == nil {
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
	return propertyValueColumns, rows, nil
}

func jobExecutionSummaryReport(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.JobsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(jobs))
	for _, j := range jobs {
		rows = append(rows, []string{
			j.Name, boolStr(j.IsEnabled), j.Category,
			formatJobOutcome(j.LastRunOutcome), dashIfZero(j.LastRunDate), dashIfZero(j.NextRunDate),
		})
	}
	return []string{"Job Name", "Enabled", "Category", "Last Outcome", "Last Run", "Next Run"}, rows, nil
}

func failedJobRunsReport(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.JobsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0)
	for _, j := range jobs {
		if j.LastRunOutcome != gosmo.JobOutcomeFailed {
			continue
		}
		rows = append(rows, []string{j.Name, j.Category, dashIfZero(j.LastRunDate), formatHMS(j.LastRunDuration)})
	}
	return []string{"Job Name", "Category", "Last Run", "Duration"}, rows, nil
}

func disabledJobsReport(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.JobsContext(ctx)
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

// jobsWithoutSchedulesReport fetches each job's schedules (one round trip per
// job; fine for typical job counts).
//
// A job whose read fails is listed with Schedules "Unknown", not dropped:
// "couldn't check" is exactly what this report surfaces, and silence reads as
// fine (as countOrDash does).
func jobsWithoutSchedulesReport(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.JobsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0)
	for _, j := range jobs {
		scheds, err := j.SchedulesContext(ctx)
		// A cancelled context fails every remaining job; report the
		// cancellation instead.
		if err != nil && ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if err == nil && len(scheds) > 0 {
			continue
		}
		schedules := "None"
		if err != nil {
			schedules = "Unknown"
		}
		rows = append(rows, []string{j.Name, j.Category, boolStr(j.IsEnabled), schedules})
	}
	return []string{"Job Name", "Category", "Enabled", "Schedules"}, rows, nil
}

func jobsWithoutNotificationsReport(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.JobsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0)
	for _, j := range jobs {
		if j.NotifyLevelEmail != gosmo.NotifyNever {
			continue
		}
		rows = append(rows, []string{j.Name, j.Category, boolStr(j.IsEnabled)})
	}
	return []string{"Job Name", "Category", "Enabled"}, rows, nil
}

func recentlyModifiedJobsReport(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.JobsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	sorted := slices.Clone(jobs)
	slices.SortStableFunc(sorted, func(a, b *gosmo.Job) int { return b.DateModified.Compare(a.DateModified) })
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

// agentJobHistoryQuery builds the read-only T-SQL for a job's "View History",
// run in a new query window (like backupHistoryQuery).
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
