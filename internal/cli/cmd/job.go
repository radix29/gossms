package cmd

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
)

// The job group (phase 8, D8): SQL Server Agent's jobs read from msdb, and
// started or stopped through its procedures. Every time a job read answers
// is the server's local wall clock, as msdb records it with no zone; gosmo
// carries it stamped UTC, and gridTime prints that wall clock as it is.

// The job commands' own options.
const (
	optEnabled     = "enabled"
	optFailedSince = "failed-since"
	optCategory    = "category"
	optFailed      = "failed"
	optStep        = "step"
	optWait        = "wait"
)

// defaultJobHistoryTop is job history --top's default.
const defaultJobHistoryTop = 20

// failedRunsRead caps job list --failed-since's history read: more failed
// runs than this in the window counts only the newest.
const failedRunsRead = 10000

// jobReadNeeds is the job reads' permission, said in every job command's
// help. gosmo reads msdb's job tables for a sysadmin and, for anyone else,
// sysjobs_view and the sp_help_job* procedures msdb grants its Agent roles,
// which apply msdb's visibility; a login in none of the roles is refused
// (Msg 229 on sp_help_jobactivity, 17).
const jobReadNeeds = "Reading the jobs takes sysadmin or a role in msdb: SQLAgentUserRole sees the jobs the login " +
	"owns, SQLAgentReaderRole and SQLAgentOperatorRole every job; another job is exit 8 (not found), and a login in " +
	"none of them is exit 9."

// agentTimeHelp is what --since and --failed-since take.
const agentTimeHelp = "YYYY-MM-DD[THH:MM[:SS]] in the server's local time (msdb records no zone), or with Z or an offset"

// JobCommands is the job group: its node and its commands. Its summary
// names only what exists.
func JobCommands(d Deps) []*spec.Command {
	return append([]*spec.Command{
		{
			Path:    []string{"job"},
			Summary: "List SQL Server Agent jobs, show one with its steps and schedules, read its run history; start and stop a job",
			Class:   spec.ClassRead,
		},
		{
			Path:    []string{"job", "list"},
			Summary: "List the Agent jobs: enabled, category, owner, state, last run and outcome, next run",
			Description: "One row per SQL Server Agent job (msdb.dbo.sysjobs), by name. state is Agent's own for a " +
				"sysadmin; for any other login, or with Agent stopped, only executing and idle can be told apart. " +
				"last_outcome is NULL for a job that never ran. Times are the server's local time, as msdb records " +
				"them. --enabled keeps the enabled jobs, --category one category (case-insensitive). --failed-since " +
				"keeps the jobs with a failed run that started at or after the time, read from the run history — so " +
				"a failure a later success hid is found — and adds failed_runs and last_failure. " + jobReadNeeds,
			Options: append(ConnectOptions(),
				spec.Option{Name: optEnabled, Type: spec.TypeBool, Help: "Only enabled jobs"},
				spec.Option{Name: optCategory, Type: spec.TypeString, Help: "Only jobs of this category (case-insensitive)"},
				spec.Option{Name: optFailedSince, Type: spec.TypeString,
					Help: "Only jobs with a failed run since this time: " + agentTimeHelp},
			),
			Class: spec.ClassRead,
			Run:   d.jobList,
			Examples: []string{
				"gossms job list -p prod-ro",
				"gossms job list --failed-since 2026-10-27T00:00:00Z -p prod-ro -o json",
				"gossms job list --enabled --category \"Database Maintenance\" -p prod-ro",
			},
		},
		{
			Path:    []string{"job", "show"},
			Summary: "Show one job: its properties and notifications, its steps and its schedules",
			Description: "The job's record — enabled, description, category, owner, state, last run and outcome, " +
				"next run, the step it starts at, its e-mail notification and automatic delete — then the set " +
				"steps (each step's subsystem, database, what happens on success and on failure, retries, last " +
				"run, command) and the set schedules (each attached schedule, its frequency as SSMS words it). " +
				"Times are the server's local time. A job that does not exist is exit 8. " + jobReadNeeds,
			Args:    []spec.Arg{{Name: "name", Help: "Job name", Required: true}},
			Options: ConnectOptions(),
			Class:   spec.ClassRead,
			Run:     d.jobShow,
			Examples: []string{
				"gossms job show \"Nightly ETL\" -p prod-ro",
				"gossms job show \"Nightly ETL\" -p prod-ro -o json",
			},
		},
		{
			Path:    []string{"job", "history"},
			Summary: "Read a job's run history, step rows and their messages included",
			Description: "The job's msdb.dbo.sysjobhistory rows, newest first: each run's outcome row (step_id 0) " +
				"and its step rows, with the message Agent recorded (a failed step's carries the server's error). " +
				"A step that started a second after its run can sort above that run's outcome row: group by time, " +
				"not by position. --top caps the rows (default 20), --since keeps rows that started at or after " +
				"the time, --failed only rows whose outcome is failed (the failed runs and the steps that failed). " +
				"Times are the server's local time. Agent purges old history (by default 1000 rows, 100 per job). " +
				"A job that does not exist is exit 8. " + jobReadNeeds,
			Args: []spec.Arg{{Name: "name", Help: "Job name", Required: true}},
			Options: append(ConnectOptions(),
				spec.Option{Name: optTop, Type: spec.TypeInt, Default: strconv.Itoa(defaultJobHistoryTop),
					Help: "Keep this many rows, the newest first"},
				spec.Option{Name: optSince, Type: spec.TypeString, Help: "Only rows since this time: " + agentTimeHelp},
				spec.Option{Name: optFailed, Type: spec.TypeBool, Help: "Only rows whose outcome is failed"},
			),
			Class: spec.ClassRead,
			Run:   d.jobHistory,
			Examples: []string{
				"gossms job history \"Nightly ETL\" --top 5 -p prod-ro",
				"gossms job history \"Nightly ETL\" --failed --since 2026-10-27 -p prod-ro -o json",
			},
		},
	}, jobWriteCommands(d)...)
}

// --- shared -----------------------------------------------------------------

// jobStateName is a JobState as the job commands print it.
func jobStateName(s gosmo.JobState) string {
	switch s {
	case gosmo.JobStateExecuting:
		return "executing"
	case gosmo.JobStateWaitingForWorker:
		return "waiting_for_worker"
	case gosmo.JobStateBetweenRetries:
		return "between_retries"
	case gosmo.JobStateIdle:
		return "idle"
	case gosmo.JobStateSuspended:
		return "suspended"
	case gosmo.JobStateWaitingForStepToFinish:
		return "waiting_for_step"
	case gosmo.JobStatePerformingCompletionActions:
		return "performing_completion_actions"
	}
	return "unknown"
}

// jobOutcomeName is a run or step outcome as the job commands print it;
// sysjobhistory's run_status 4 is a run still in progress.
func jobOutcomeName(o gosmo.JobOutcome) string {
	switch o {
	case gosmo.JobOutcomeFailed:
		return "failed"
	case gosmo.JobOutcomeSucceeded:
		return "succeeded"
	case gosmo.JobOutcomeRetried:
		return "retry"
	case gosmo.JobOutcomeCancelled:
		return "cancelled"
	case 4:
		return "in_progress"
	}
	return "unknown"
}

// lastOutcomeCell is a job's or step's last outcome, NULL when it never ran.
func lastOutcomeCell(last time.Time, o gosmo.JobOutcome) *string {
	if last.IsZero() {
		return nil
	}
	return ptr(jobOutcomeName(o))
}

// notifyLevelName is a notification's or automatic delete's condition.
func notifyLevelName(n gosmo.NotifyLevel) string {
	switch n {
	case gosmo.NotifyNever:
		return "never"
	case gosmo.NotifyOnSuccess:
		return "on_success"
	case gosmo.NotifyOnFailure:
		return "on_failure"
	case gosmo.NotifyOnComplete:
		return "on_completion"
	}
	return "unknown"
}

// stepActionText is a step's on_success_action / on_fail_action.
func stepActionText(action, stepID int) string {
	switch action {
	case 1:
		return "quit with success"
	case 2:
		return "quit with failure"
	case 3:
		return "go to next step"
	case 4:
		return fmt.Sprintf("go to step %d", stepID)
	}
	return strconv.Itoa(action)
}

func secondsCell(d time.Duration) *string { return ptr(strconv.FormatInt(int64(d/time.Second), 10)) }

// agentTime is a --since or --failed-since as gosmo compares msdb's times: a
// server-local wall clock stamped UTC. A zoneless value is one already; a
// zoned one is read in the server's zone, which costs a clock read.
func agentTime(ctx context.Context, srv *gosmo.Server, q qsTime) (time.Time, error) {
	if !q.zoned {
		return q.t, nil
	}
	now, err := srv.CurrentTime(ctx)
	if err != nil {
		return time.Time{}, readError(err)
	}
	return serverWall(q.t.In(now.Location())), nil
}

// serverWall is t's wall clock stamped UTC: how gosmo carries msdb's times.
func serverWall(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), 0, time.UTC)
}

// jobReadError is readError for a read of msdb's jobs: a refusal is the
// msdb Agent roles a login needs.
func jobReadError(err error) *output.Error {
	e := readError(err)
	if e.Code == output.CodePermissionDenied && e.SQL != nil {
		e.Hint = "Reading the jobs needs sysadmin or a role in msdb: SQLAgentUserRole (the jobs the login owns), " +
			"SQLAgentReaderRole or SQLAgentOperatorRole (every job). Use a login that has one, or have it granted."
	}
	return e
}

// findJob is the job name names, NOT_FOUND hinting job list when there is
// none the login can see.
func findJob(ctx context.Context, srv *gosmo.Server, name string) (*gosmo.Job, error) {
	j, err := srv.JobByName(ctx, name)
	if errors.Is(err, gosmo.ErrNotFound) {
		return nil, output.NewError(output.CodeNotFound, fmt.Sprintf("no Agent job %s", gosmo.QuoteName(name)),
			"Check the name: gossms job list lists the jobs.")
	}
	if err != nil {
		return nil, jobReadError(err)
	}
	return j, nil
}

// readAgentTime is the option opt parsed, the zero qsTime when not given.
func readAgentTime(inv *spec.Invocation, opt string) (qsTime, error) {
	s, _ := inv.Values[opt].(string)
	if !inv.Given[opt] {
		return qsTime{}, nil
	}
	if s == "" {
		return qsTime{}, usageError("--"+opt+" is empty", "Give a time ("+agentTimeHelp+"), or leave --"+opt+" out.")
	}
	return parseQSTime(opt, s)
}

// --- job list ---------------------------------------------------------------

// jobFailures is one job's failed runs in job list --failed-since's window.
type jobFailures struct {
	runs int
	last time.Time
}

func (d Deps) jobList(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	since, err := readAgentTime(inv, optFailedSince)
	if err != nil {
		return nil, err
	}
	category, _ := inv.Values[optCategory].(string)
	if inv.Given[optCategory] && category == "" {
		return nil, usageError("--category is empty", "Name a category, or leave --category out for every one.")
	}
	res := &output.Result{}
	sc, err := d.connectServer(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	jobs, err := sc.Server.Jobs(ctx)
	if err != nil {
		return res, jobReadError(err)
	}
	onlyEnabled, _ := inv.Values[optEnabled].(bool)
	jobs = slices.DeleteFunc(jobs, func(j *gosmo.Job) bool {
		return (onlyEnabled && !j.IsEnabled) || (category != "" && !strings.EqualFold(j.Category, category))
	})

	failedOnly := inv.Given[optFailedSince]
	var failures map[string]*jobFailures
	if failedOnly {
		from, err := agentTime(ctx, sc.Server, since)
		if err != nil {
			return res, err
		}
		runs, err := sc.Server.SearchJobHistory(ctx, gosmo.JobHistoryFilter{Since: from,
			Outcomes: []gosmo.JobOutcome{gosmo.JobOutcomeFailed}, Max: failedRunsRead})
		if err != nil {
			return res, jobReadError(err)
		}
		if len(runs) == failedRunsRead {
			res.Warnings = append(res.Warnings, fmt.Sprintf("more than %d failed runs since %s: failed_runs counts the newest %d",
				failedRunsRead, gridTime(from), failedRunsRead))
		}
		failures = map[string]*jobFailures{}
		for _, h := range runs {
			f := failures[h.JobName]
			if f == nil {
				f = &jobFailures{}
				failures[h.JobName] = f
			}
			f.runs++
			if h.RunDate.After(f.last) {
				f.last = h.RunDate
			}
		}
		jobs = slices.DeleteFunc(jobs, func(j *gosmo.Job) bool { return failures[j.Name] == nil })
	}

	set := output.Set{Name: "jobs", Columns: []output.Column{
		{Name: "name", Type: "nvarchar(128)"}, {Name: "enabled", Type: "bit"},
		{Name: "category", Type: "nvarchar(128)"}, {Name: "owner", Type: "nvarchar(128)", Nullable: true},
		{Name: "state", Type: "nvarchar(30)"},
		{Name: "last_run", Type: "datetime", Nullable: true}, {Name: "last_outcome", Type: "nvarchar(20)", Nullable: true},
		{Name: "last_duration_s", Type: "bigint", Nullable: true},
		{Name: "next_run", Type: "datetime", Nullable: true},
	}}
	if failedOnly {
		set.Columns = append(set.Columns, output.Column{Name: "failed_runs", Type: "int"},
			output.Column{Name: "last_failure", Type: "datetime"})
	}
	for _, j := range jobs {
		var dur *string
		if !j.LastRunDate.IsZero() {
			dur = secondsCell(j.LastRunDuration)
		}
		row := []*string{
			ptr(j.Name), bitCell(j.IsEnabled), ptr(j.Category), textOrNull(j.OwnerLoginName),
			ptr(jobStateName(j.CurrentState)),
			cellTime(j.LastRunDate), lastOutcomeCell(j.LastRunDate, j.LastRunOutcome), dur,
			cellTime(j.NextRunDate),
		}
		if failedOnly {
			f := failures[j.Name]
			row = append(row, ptr(strconv.Itoa(f.runs)), ptr(gridTime(f.last)))
		}
		set.Rows = append(set.Rows, row)
	}
	set.TotalRowCount = len(set.Rows)
	res.Sets = []output.Set{set}
	return res, nil
}

// --- job show ---------------------------------------------------------------

func (d Deps) jobShow(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, err := d.connectServer(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	j, err := findJob(ctx, sc.Server, inv.Args[0])
	if err != nil {
		return res, err
	}
	steps, err := j.Steps(ctx)
	if err != nil {
		return res, jobReadError(err)
	}
	schedules, err := j.Schedules(ctx)
	if err != nil {
		return res, jobReadError(err)
	}
	res.Fields = jobFields(j)
	res.Sets = []output.Set{jobStepsSet(steps), jobSchedulesSet(schedules)}
	return res, nil
}

// jobFields is job show's record.
func jobFields(j *gosmo.Job) []output.Field {
	timeField := func(key string, t time.Time) output.Field {
		if t.IsZero() {
			return output.Field{Key: key, Null: true}
		}
		return output.Field{Key: key, Value: gridTime(t)}
	}
	text := func(key, v string) output.Field { return output.Field{Key: key, Value: v, Null: v == ""} }
	last := output.Field{Key: "lastOutcome", Null: true}
	dur := output.Field{Key: "lastDurationS", Null: true}
	if !j.LastRunDate.IsZero() {
		last.Value, last.Null = jobOutcomeName(j.LastRunOutcome), false
		dur.Value, dur.Null = *secondsCell(j.LastRunDuration), false
	}
	return []output.Field{
		{Key: "name", Value: j.Name},
		{Key: "jobId", Value: j.JobID},
		{Key: "enabled", Value: boolText(j.IsEnabled)},
		text("description", j.Description),
		{Key: "category", Value: j.Category},
		text("owner", j.OwnerLoginName),
		{Key: "state", Value: jobStateName(j.CurrentState)},
		{Key: "startStep", Value: strconv.Itoa(j.StartStepID)},
		timeField("lastRun", j.LastRunDate), last, dur,
		timeField("nextRun", j.NextRunDate),
		timeField("created", j.DateCreated), timeField("modified", j.DateModified),
		{Key: "notifyEmail", Value: notifyLevelName(j.NotifyLevelEmail)},
		text("notifyEmailOperator", j.NotifyEmailOperatorName),
		{Key: "deleteJob", Value: notifyLevelName(j.DeleteLevel)},
	}
}

func jobStepsSet(steps []*gosmo.JobStep) output.Set {
	set := output.Set{Name: "steps", Columns: []output.Column{
		{Name: "step_id", Type: "int"}, {Name: "name", Type: "nvarchar(128)"},
		{Name: "subsystem", Type: "nvarchar(40)"}, {Name: "database", Type: "nvarchar(128)", Nullable: true},
		{Name: "on_success", Type: "nvarchar(30)"}, {Name: "on_fail", Type: "nvarchar(30)"},
		{Name: "retry_attempts", Type: "int"}, {Name: "retry_interval_min", Type: "int"},
		{Name: "last_run", Type: "datetime", Nullable: true}, {Name: "last_outcome", Type: "nvarchar(20)", Nullable: true},
		{Name: "last_duration_s", Type: "bigint", Nullable: true},
		{Name: "proxy", Type: "nvarchar(128)", Nullable: true}, {Name: "output_file", Type: "nvarchar(200)", Nullable: true},
		{Name: "command", Type: "nvarchar(max)", Nullable: true},
	}}
	for _, s := range steps {
		var dur *string
		if !s.LastRunDate.IsZero() {
			dur = secondsCell(s.LastRunElapsed)
		}
		set.Rows = append(set.Rows, []*string{
			ptr(strconv.Itoa(s.StepID)), ptr(s.Name), ptr(s.Subsystem), textOrNull(s.Database),
			ptr(stepActionText(s.OnSuccessAction, s.OnSuccessStepID)), ptr(stepActionText(s.OnFailAction, s.OnFailStepID)),
			ptr(strconv.Itoa(s.RetryAttempts)), ptr(strconv.Itoa(s.RetryInterval)),
			cellTime(s.LastRunDate), lastOutcomeCell(s.LastRunDate, s.LastRunOutcome), dur,
			textOrNull(s.ProxyName), textOrNull(s.OutputFileName), textOrNull(s.Command),
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

func jobSchedulesSet(schedules []*gosmo.Schedule) output.Set {
	set := output.Set{Name: "schedules", Columns: []output.Column{
		{Name: "schedule_id", Type: "int"}, {Name: "name", Type: "nvarchar(128)"}, {Name: "enabled", Type: "bit"},
		{Name: "frequency", Type: "nvarchar(max)"},
		{Name: "start_date", Type: "datetime", Nullable: true}, {Name: "end_date", Type: "datetime", Nullable: true},
	}}
	for _, s := range schedules {
		set.Rows = append(set.Rows, []*string{
			ptr(strconv.Itoa(s.ID)), ptr(s.Name), bitCell(s.Enabled), ptr(s.Description()),
			cellTime(s.ActiveStartDate), cellTime(s.ActiveEndDate),
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}

// --- job history ------------------------------------------------------------

func (d Deps) jobHistory(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	top := inv.Values[optTop].(int)
	if top < 1 {
		return nil, usageError(fmt.Sprintf("--top: %d is not a number of rows", top), "Give the number of rows to keep, 1 or more.")
	}
	since, err := readAgentTime(inv, optSince)
	if err != nil {
		return nil, err
	}
	res := &output.Result{}
	sc, err := d.connectServer(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	j, err := findJob(ctx, sc.Server, inv.Args[0])
	if err != nil {
		return res, err
	}
	f := gosmo.JobHistoryFilter{JobName: j.Name, IncludeSteps: true, Max: top}
	if inv.Given[optSince] {
		if f.Since, err = agentTime(ctx, sc.Server, since); err != nil {
			return res, err
		}
	}
	if failed, _ := inv.Values[optFailed].(bool); failed {
		f.Outcomes = []gosmo.JobOutcome{gosmo.JobOutcomeFailed}
	}
	rows, err := sc.Server.SearchJobHistory(ctx, f)
	if err != nil {
		return res, jobReadError(err)
	}
	res.Fields = []output.Field{{Key: "job", Value: j.Name}}
	res.Sets = []output.Set{jobHistorySet(rows)}
	return res, nil
}

// jobHistorySet is the history rows as job history and job start --wait
// show them.
func jobHistorySet(rows []*gosmo.JobHistoryEntry) output.Set {
	set := output.Set{Name: "history", Columns: []output.Column{
		{Name: "run_date", Type: "datetime"}, {Name: "step_id", Type: "int"},
		{Name: "step_name", Type: "nvarchar(128)"}, {Name: "outcome", Type: "nvarchar(20)"},
		{Name: "duration_s", Type: "bigint"}, {Name: "message", Type: "nvarchar(4000)", Nullable: true},
	}}
	for _, h := range rows {
		set.Rows = append(set.Rows, []*string{
			ptr(gridTime(h.RunDate)), ptr(strconv.Itoa(h.StepID)), ptr(h.StepName),
			ptr(jobOutcomeName(h.Outcome)), secondsCell(h.Duration), textOrNull(h.Message),
		})
	}
	set.TotalRowCount = len(set.Rows)
	return set
}
