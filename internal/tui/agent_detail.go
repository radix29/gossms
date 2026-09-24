package tui

import (
	"context"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// agent_detail.go builds Object Explorer Details grids for SQL Server Agent
// nodes, dispatched from detail_browser.go's fetchNodeDetails (same (cols,
// rows, err) shape).

// countOrDash renders a count, or an em dash if its fetch failed, so one
// secondary count doesn't fail a summary.
func countOrDash(n int, err error) string {
	if err != nil {
		return "—"
	}
	return strconv.Itoa(n)
}

// agentServerDetail builds the Agent root's detail: run status and a census of
// its collections.
func agentServerDetail(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	statusText := "Unknown"
	lastStartup := ""
	if status, err := sc.Server.AgentInfo(ctx); err == nil {
		statusText = status.StatusText
		if !status.LastStartupTime.IsZero() {
			lastStartup = formatSQLDate(status.LastStartupTime)
		}
	}

	// One aggregate read, not four full listings (plus Jobs' Agent state
	// call) to take len() of. A failure dashes all four and keeps the status.
	counts, cErr := sc.Server.AgentCounts(ctx)
	if counts == nil {
		counts = &gosmo.AgentCounts{}
	}

	rows := [][]string{
		{"Status", statusText},
		{"Last startup", lastStartup},
		{"Jobs", countOrDash(counts.Jobs, cErr)},
		{"Schedules", countOrDash(counts.Schedules, cErr)},
		{"Event alerts", countOrDash(counts.EventAlerts, cErr)},
		{"Operators", countOrDash(counts.Operators, cErr)},
	}
	return propertyValueColumns, rows, nil
}

// agentJobDetail builds a job's detail view.
func agentJobDetail(ctx context.Context, sc *db.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	j, err := sc.Server.JobByName(ctx, node.data.Name)
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
		{"Enabled", boolStr(j.IsEnabled)},
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
	return propertyValueColumns, rows, nil
}

// agentScheduleDetail builds a schedule's detail view.
func agentScheduleDetail(ctx context.Context, sc *db.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	sch, err := sc.Server.ScheduleByName(ctx, node.data.Name)
	if err != nil {
		return nil, nil, err
	}
	jobs, jErr := sch.Jobs(ctx)

	endDate := formatAgentDate(sch.ActiveEndDate)
	if endDate == "" {
		endDate = "No end date"
	}
	rows := [][]string{
		{"Schedule name", sch.Name},
		{"Enabled", boolStr(sch.Enabled)},
		{"Owner", sch.OwnerLoginName},
		{"Start date", formatAgentDate(sch.ActiveStartDate)},
		{"End date", endDate},
		{"Used by jobs", countOrDash(len(jobs), jErr)},
		{"Description", sch.Description()},
	}
	return propertyValueColumns, rows, nil
}

// agentAlertDetail builds an alert's detail view.
func agentAlertDetail(ctx context.Context, sc *db.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	al, err := sc.Server.AlertByName(ctx, node.data.Name)
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
		{"Enabled", boolStr(al.Enabled)},
		{"Type", "SQL Server event alert"},
		{"Database", dbScope},
		{"Error number", errorNumber},
		{"Severity", severity},
		{"Delay between responses", al.DelayBetweenResponses.String()},
		{"Category", al.Category},
		{"Last occurrence", lastOccurrence},
	}
	if notifs, err := al.Notifications(ctx); err == nil {
		for _, n := range notifs {
			rows = append(rows, []string{"Notify operator", n.OperatorName + " (" + n.Method.String() + ")"})
		}
	}
	if al.JobName != "" {
		rows = append(rows, []string{"Response job", al.JobName})
	}
	return propertyValueColumns, rows, nil
}

// agentOperatorDetail builds an operator's detail view.
func agentOperatorDetail(ctx context.Context, sc *db.ServerConn, node *explorerNode) ([]string, [][]string, error) {
	o, err := sc.Server.OperatorByName(ctx, node.data.Name)
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
		{"Enabled", boolStr(o.Enabled)},
		{"Email address", o.EmailAddress},
		{"Pager address", pager},
		{"Net send address", netSend},
		{"Category", o.Category},
		{"Last email", lastEmail},
	}
	if alerts, err := o.NotifyingAlerts(ctx); err == nil {
		for _, n := range alerts {
			rows = append(rows, []string{"Notifies (alert)", n.AlertName + " — " + n.Method.String()})
		}
	}
	if jobs, err := o.NotifyingJobs(ctx); err == nil {
		for _, n := range jobs {
			rows = append(rows, []string{"Notifies (job)", n.JobName + " — " + formatNotifyLevel(n.Level)})
		}
	}
	return propertyValueColumns, rows, nil
}

// agentJobActivityDetail builds the "Job Activity" leaf's detail view.
func agentJobActivityDetail(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	jobs, err := sc.Server.Jobs(ctx)
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

// agentJobHistoryDetail builds the Job History leaf: the latest job-level
// outcome of every job.
func agentJobHistoryDetail(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	history, err := sc.Server.JobHistory(ctx, 100)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(history))
	for _, h := range history {
		rows = append(rows, []string{
			h.JobName, formatSQLDate(h.RunDate), formatJobOutcome(h.Outcome), formatHMS(h.Duration), h.Message,
		})
	}
	return []string{"Job Name", "Run Date", "Outcome", "Duration", "Message"}, rows, nil
}

// agentCategoriesDetail lists every category of a class, for Job and Alert
// Categories.
func agentCategoriesDetail(ctx context.Context, sc *db.ServerConn, class gosmo.CategoryClass) ([]string, [][]string, error) {
	cats, err := sc.Server.Categories(ctx, class)
	if err != nil {
		return nil, nil, err
	}
	rows := make([][]string, 0, len(cats))
	for _, c := range cats {
		rows = append(rows, []string{c.Name})
	}
	return []string{"Name"}, rows, nil
}

// agentJobCategoriesDetail is agentCategoriesDetail for job categories, so
// detail_browser.go needn't import gosmo.
func agentJobCategoriesDetail(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	return agentCategoriesDetail(ctx, sc, gosmo.CategoryClassJob)
}

// agentAlertCategoriesDetail is agentCategoriesDetail for alert categories.
func agentAlertCategoriesDetail(ctx context.Context, sc *db.ServerConn) ([]string, [][]string, error) {
	return agentCategoriesDetail(ctx, sc, gosmo.CategoryClassAlert)
}
