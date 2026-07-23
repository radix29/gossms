package tui

import (
	"context"
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// agent_detail.go builds Object Explorer Details grids for every SQL
// Server Agent node type — dispatched from detail_browser.go's
// fetchNodeDetails. Each function matches fetchNodeDetails' own shape
// (cols, rows, err) so it plugs into the existing async fetch/cache
// machinery with no extra plumbing.

// countOrDash renders a count, or an em dash if the fetch that would have
// produced it failed — used by summary rows that shouldn't fail their
// whole detail view over one secondary count.
func countOrDash(n int, err error) string {
	if err != nil {
		return "—"
	}
	return strconv.Itoa(n)
}

// agentServerDetail builds the "SQL Server Agent" root's detail view: its
// run status plus a quick census of every child collection.
func agentServerDetail(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	statusText := "Unknown"
	lastStartup := ""
	if status, err := sc.Server.AgentInfoContext(ctx); err == nil {
		statusText = status.StatusText
		if !status.LastStartupTime.IsZero() {
			lastStartup = formatSQLDate(status.LastStartupTime)
		}
	}

	jobs, jErr := sc.Server.JobsContext(ctx)
	schedules, schErr := sc.Server.SchedulesContext(ctx)
	alerts, aErr := sc.Server.EventAlertsContext(ctx)
	operators, oErr := sc.Server.OperatorsContext(ctx)

	rows := [][]string{
		{"Status", statusText},
		{"Last startup", lastStartup},
		{"Surface", "SQL-only"},
		{"Source", "msdb"},
		{"Jobs", countOrDash(len(jobs), jErr)},
		{"Schedules", countOrDash(len(schedules), schErr)},
		{"Event alerts", countOrDash(len(alerts), aErr)},
		{"Operators", countOrDash(len(operators), oErr)},
	}
	return []string{"Property", "Value"}, rows, nil
}

// agentJobDetail builds a job's detail view, matching the mockup's
// "SELECTED NODE MOCKUP — JOB".
func agentJobDetail(ctx context.Context, sc *db.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	j, err := sc.Server.JobByNameContext(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	notifyOperator := j.NotifyEmailOperatorName
	if notifyOperator == "" {
		notifyOperator = "(none)"
	}
	lastRun := "—"
	if !j.LastRunDate.IsZero() {
		lastRun = formatSQLDate(j.LastRunDate)
	}
	nextRun := "—"
	if !j.NextRunDate.IsZero() {
		nextRun = formatSQLDate(j.NextRunDate)
	}
	rows := [][]string{
		{"Job name", j.Name},
		{"Enabled", fmt.Sprintf("%v", j.IsEnabled)},
		{"Owner", j.OwnerLoginName},
		{"Category", j.Category},
		{"Description", j.Description},
		{"Created", formatSQLDate(j.DateCreated)},
		{"Last modified", formatSQLDate(j.DateModified)},
		{"Start step", strconv.Itoa(j.StartStepID)},
		{"Delete job", formatNotifyLevel(j.DeleteLevel)},
		{"Notify operator", notifyOperator},
		{"Notify condition", formatNotifyLevel(j.NotifyLevelEmail)},
		{"Status", formatJobState(j.CurrentState)},
		{"Last outcome", formatJobOutcome(j.LastRunOutcome)},
		{"Last run", lastRun},
		{"Next run", nextRun},
	}
	return []string{"Property", "Value"}, rows, nil
}

// agentScheduleDetail builds a schedule's detail view, matching the
// mockup's "SELECTED NODE MOCKUP — SCHEDULE".
func agentScheduleDetail(ctx context.Context, sc *db.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	sch, err := sc.Server.ScheduleByNameContext(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	jobs, jErr := sch.JobsContext(ctx)

	endDate := "No end date"
	if !sch.ActiveEndDate.IsZero() {
		endDate = sch.ActiveEndDate.Format("2006-01-02")
	}
	rows := [][]string{
		{"Schedule name", sch.Name},
		{"Enabled", fmt.Sprintf("%v", sch.Enabled)},
		{"Owner", sch.OwnerLoginName},
		{"Start date", sch.ActiveStartDate.Format("2006-01-02")},
		{"End date", endDate},
		{"Used by jobs", countOrDash(len(jobs), jErr)},
		{"Description", sch.Description()},
	}
	return []string{"Property", "Value"}, rows, nil
}

// agentAlertDetail builds an alert's detail view, matching the mockup's
// "SELECTED NODE MOCKUP — SQL SERVER EVENT ALERT".
func agentAlertDetail(ctx context.Context, sc *db.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	al, err := sc.Server.AlertByNameContext(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}

	dbScope := al.DatabaseName
	if dbScope == "" {
		dbScope = "<all databases>"
	}
	errorNumber := "<not used>"
	if al.ErrorNumber != 0 {
		errorNumber = strconv.Itoa(al.ErrorNumber)
	}
	severity := "<not used>"
	if al.Severity != 0 {
		severity = strconv.Itoa(al.Severity)
	}
	lastOccurrence := "<never>"
	if !al.LastOccurrence.IsZero() {
		lastOccurrence = formatSQLDate(al.LastOccurrence)
	}

	rows := [][]string{
		{"Alert name", al.Name},
		{"Enabled", fmt.Sprintf("%v", al.Enabled)},
		{"Type", "SQL Server event alert"},
		{"Database", dbScope},
		{"Error number", errorNumber},
		{"Severity", severity},
		{"Delay between responses", al.DelayBetweenResponses.String()},
		{"Category", al.Category},
		{"Last occurrence", lastOccurrence},
	}
	if notifs, err := al.NotificationsContext(ctx); err == nil {
		for _, n := range notifs {
			rows = append(rows, []string{"Notify operator", n.OperatorName + " (" + n.Method.String() + ")"})
		}
	}
	if al.JobName != "" {
		rows = append(rows, []string{"Response job", al.JobName})
	}
	return []string{"Property", "Value"}, rows, nil
}

// agentOperatorDetail builds an operator's detail view, matching the
// mockup's "SELECTED NODE MOCKUP — OPERATOR".
func agentOperatorDetail(ctx context.Context, sc *db.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	o, err := sc.Server.OperatorByNameContext(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}

	pager := o.PagerAddress
	if pager == "" {
		pager = "<not configured>"
	}
	netSend := o.NetSendAddress
	if netSend == "" {
		netSend = "<not configured>"
	}
	lastEmail := "<never>"
	if !o.LastEmailDate.IsZero() {
		lastEmail = formatSQLDate(o.LastEmailDate)
	}

	rows := [][]string{
		{"Operator name", o.Name},
		{"Enabled", fmt.Sprintf("%v", o.Enabled)},
		{"Email address", o.EmailAddress},
		{"Pager address", pager},
		{"Net send address", netSend},
		{"Category", o.Category},
		{"Last email", lastEmail},
	}
	if alerts, err := o.NotifyingAlertsContext(ctx); err == nil {
		for _, n := range alerts {
			rows = append(rows, []string{"Notifies (alert)", n.AlertName + " — " + n.Method.String()})
		}
	}
	if jobs, err := o.NotifyingJobsContext(ctx); err == nil {
		for _, n := range jobs {
			rows = append(rows, []string{"Notifies (job)", n.JobName + " — " + formatNotifyLevel(n.Level)})
		}
	}
	return []string{"Property", "Value"}, rows, nil
}

// agentJobActivityDetail builds the "Job Activity" leaf's detail view,
// matching the mockup's "SELECTED NODE MOCKUP — JOB ACTIVITY".
func agentJobActivityDetail(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.JobsContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(jobs))
	for _, j := range jobs {
		lastRun := "—"
		if !j.LastRunDate.IsZero() {
			lastRun = formatSQLDate(j.LastRunDate)
		}
		nextRun := "—"
		if !j.NextRunDate.IsZero() {
			nextRun = formatSQLDate(j.NextRunDate)
		}
		rows = append(rows, []string{
			j.Name, formatJobState(j.CurrentState), formatJobOutcome(j.LastRunOutcome), lastRun, nextRun,
		})
	}
	return []string{"Job Name", "Status", "Last Outcome", "Last Run", "Next Run"}, rows, nil
}

// agentJobHistoryDetail builds the "Job History" leaf's detail view: the
// most recent job-level outcome across every job.
func agentJobHistoryDetail(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	history, err := sc.Server.JobHistoryContext(ctx, 100)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(history))
	for _, h := range history {
		rows = append(rows, []string{
			h.JobName, formatSQLDate(h.RunDate), formatJobOutcome(h.Outcome), h.Duration.String(), h.Message,
		})
	}
	return []string{"Job Name", "Run Date", "Outcome", "Duration", "Message"}, rows, nil
}

// agentCategoriesDetail lists every category of the given class — shared
// by the "Job Categories" and "Alert Categories" leaves.
func agentCategoriesDetail(ctx context.Context, sc *db.ServerConn, class gosmo.CategoryClass) ([]string, [][]string, error) {
	cats, err := sc.Server.CategoriesContext(ctx, class)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(cats))
	for _, c := range cats {
		rows = append(rows, []string{c.Name})
	}
	return []string{"Name"}, rows, nil
}

// agentJobCategoriesDetail is agentCategoriesDetail for job categories —
// a thin wrapper so detail_browser.go's dispatch doesn't need to import
// gosmo just to name gosmo.CategoryClassJob.
func agentJobCategoriesDetail(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	return agentCategoriesDetail(ctx, sc, gosmo.CategoryClassJob)
}

// agentAlertCategoriesDetail is agentCategoriesDetail for alert categories.
func agentAlertCategoriesDetail(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	return agentCategoriesDetail(ctx, sc, gosmo.CategoryClassAlert)
}
