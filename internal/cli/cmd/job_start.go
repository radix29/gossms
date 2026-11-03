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

// job start / job stop (phase 8, D8): admin, as query run classes the msdb
// Agent procedures (D1); no --confirm (D3). Both are asynchronous on the
// server: sp_start_job returns once Agent has the request, sp_stop_job once
// it has been asked to stop.

// msgAgentRefused is SQL Server Agent's refusal of sp_start_job/sp_stop_job
// (HResult 0x5606): the job already running, not running, or Agent itself
// stopped. The message says which (captured on 17).
const msgAgentRefused = 22022

// msgJobIdentifier is msdb's "The specified @job_name / @step_name ('x')
// does not exist." — the job or step went between the read and the call.
const msgJobIdentifier = 14262

// maxJobWait bounds --wait: a day.
const maxJobWait = 86400

// jobPollInterval is how often --wait reads the job.
const jobPollInterval = 2 * time.Second

// jobNow is the client clock --wait measures its bound by; a test replaces
// it with the fake clock sleepCtx advances.
var jobNow = time.Now

// agentStoppedHint is the hint for a start or stop with Agent not running.
const agentStoppedHint = "Start SQL Server Agent first: the SQL Server Agent service on Windows; on Linux " +
	"`sudo /opt/mssql/bin/mssql-conf set sqlagent.enabled true` then restart mssql-server. Express has no Agent."

func jobWriteCommands(d Deps) []*spec.Command {
	return []*spec.Command{
		{
			Path:    []string{"job", "start"},
			Summary: "Start an Agent job now, at its first step or a given one; optionally wait for it to finish",
			Description: "EXEC msdb.dbo.sp_start_job, through gosmo. The job is read first: missing is exit 8; already " +
				"running is exit 7 and nothing is sent (Agent would refuse it). Agent not running is exit 11 — reads " +
				"still work. --step starts at a step given by its number or its name (job show lists them; missing: " +
				"exit 8). A disabled job starts too (a warning says so). Starting is asynchronous: without --wait the " +
				"command returns once Agent has the request (started), not when the job ends. --wait <sec> reads the " +
				"job every 2 s until that run ends, then reports outcome, lastRun and lastDurationS and the run's " +
				"history rows (set history, steps and messages); a failed run is still exit 0, outcome failed, with " +
				"a warning quoting the failed step's message. Still running after --wait seconds is exit 6 with the " +
				"job left running; Ctrl+C stops the waiting, never the job. Needs --allow admin. " + jobReadNeeds,
			Args: []spec.Arg{{Name: "name", Help: "Job name", Required: true}},
			Options: slices.Concat(ConnectOptions(), writeOptions(), []spec.Option{
				{Name: optStep, Type: spec.TypeString, Help: "Start at this step: its number or its name"},
				{Name: optWait, Type: spec.TypeInt, Default: "0",
					Help: fmt.Sprintf("Wait up to this many seconds (1–%d) for the run to end, and report its outcome", maxJobWait)},
			}),
			Class: spec.ClassAdmin,
			Run:   d.jobStart,
			Examples: []string{
				"gossms job start \"Nightly ETL\" -p dev --allow admin --dry-run",
				"gossms job start \"Nightly ETL\" --step 3 -p dev --allow admin",
				"gossms job start \"Nightly ETL\" --wait 600 -p dev --allow admin -o json",
			},
		},
		{
			Path:    []string{"job", "stop"},
			Summary: "Ask SQL Server Agent to stop a running job",
			Description: "EXEC msdb.dbo.sp_stop_job, through gosmo. The job is read first: missing is exit 8; Agent not " +
				"running is exit 11. A job that is not running is exit 0 with stopped false and a warning — the state " +
				"asked for holds — and nothing is sent when the read already says so. Stopping is asynchronous: the " +
				"running step is cancelled and the run recorded as cancelled once it does; state is the job as read " +
				"before, and the job read again the request (stateAfter). Needs --allow admin. " + jobReadNeeds,
			Args:    []spec.Arg{{Name: "name", Help: "Job name", Required: true}},
			Options: slices.Concat(ConnectOptions(), writeOptions()),
			Class:   spec.ClassAdmin,
			Run:     d.jobStop,
			Examples: []string{
				"gossms job stop \"Nightly ETL\" -p dev --allow admin --dry-run",
				"gossms job stop \"Nightly ETL\" -p dev --allow admin",
			},
		},
	}
}

// checkAgentRunning refuses a start or stop with Agent known to be stopped.
// An unreadable or Unknown status (no VIEW SERVER STATE, no service row)
// leaves it to the server, whose refusal agentError maps alike.
func checkAgentRunning(ctx context.Context, cat Catalog, res *output.Result) error {
	st, err := cat.AgentInfo(ctx)
	switch {
	case err != nil:
		res.Warnings = append(res.Warnings, "SQL Server Agent's status could not be read: "+err.Error())
		return nil
	case st.Running || strings.EqualFold(st.StatusText, "Unknown"):
		return nil
	}
	return output.NewError(output.CodeUnsupported, "SQL Server Agent is not running (status: "+st.StatusText+")",
		agentStoppedHint)
}

// resolveStep is --step: a whole number is a step id, anything else a step
// name — exact, else the one step whose name matches ignoring case.
// NOT_FOUND lists the job's steps.
func resolveStep(ctx context.Context, cat Catalog, j *gosmo.Job, step string) (*gosmo.JobStep, error) {
	steps, err := cat.JobSteps(ctx, j)
	if err != nil {
		return nil, jobReadError(err)
	}
	var match func(*gosmo.JobStep) bool
	if id, err := strconv.Atoi(step); err == nil {
		match = func(s *gosmo.JobStep) bool { return s.StepID == id }
	} else {
		match = func(s *gosmo.JobStep) bool { return s.Name == step }
	}
	if i := slices.IndexFunc(steps, match); i >= 0 {
		return steps[i], nil
	}
	var fold []*gosmo.JobStep
	for _, s := range steps {
		if strings.EqualFold(s.Name, step) {
			fold = append(fold, s)
		}
	}
	if len(fold) == 1 {
		return fold[0], nil
	}
	list := make([]string, len(steps))
	for i, s := range steps {
		list[i] = fmt.Sprintf("%d %s", s.StepID, s.Name)
	}
	hint := "The job has no steps."
	if len(list) > 0 {
		hint = "Its steps: " + strings.Join(list, ", ") + "."
	}
	return nil, output.NewError(output.CodeNotFound,
		fmt.Sprintf("job %s has no step %q", gosmo.QuoteName(j.Name), step), hint)
}

// agentError is writeError for sp_start_job/sp_stop_job, with Agent's
// refusals in the job commands' codes. notRunning reports a stop of a job
// that was not running, which the caller answers as a success.
func agentError(err error, job string) (e error, notRunning bool) {
	oe, ok := errors.AsType[*output.Error](err)
	if !ok || oe.SQL == nil {
		return err, false
	}
	switch oe.SQL.Number {
	case msgAgentRefused:
		msg := strings.ToLower(oe.Message)
		switch {
		case strings.Contains(msg, "not currently running so it cannot be notified"):
			oe.Code, oe.Hint = output.CodeUnsupported, agentStoppedHint
		case strings.Contains(msg, "already running"):
			oe.Code, oe.Hint = output.CodeBlocked, fmt.Sprintf("It started after it was read: gossms job stop %q ends it.", job)
		case strings.Contains(msg, "not currently running"):
			return nil, true
		}
	case msgJobIdentifier:
		oe.Code, oe.Hint = output.CodeNotFound, "It went after it was read: gossms job show lists the job's steps."
	}
	if oe.Code == output.CodePermissionDenied {
		oe.Hint = "Starting or stopping a job needs sysadmin, SQLAgentOperatorRole, or SQLAgentUserRole for a job " +
			"the login owns. Use a login that has one, or have it granted."
	}
	return oe, false
}

// --- job start --------------------------------------------------------------

func (d Deps) jobStart(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	wait := inv.Values[optWait].(int)
	if wait < 0 || wait > maxJobWait {
		return nil, usageError(fmt.Sprintf("--wait: %d is not a number of seconds from 1 to %d", wait, maxJobWait),
			"Leave --wait out to return once the job is started.")
	}
	step, _ := inv.Values[optStep].(string)
	if inv.Given[optStep] && step == "" {
		return nil, usageError("--step is empty", "Give a step number or name, or leave --step out to start at the job's first step.")
	}
	res := &output.Result{}
	sc, cat, w, err := d.connectWriter(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	j, err := findJob(ctx, cat, inv.Args[0])
	if err != nil {
		return res, err
	}
	if err := checkAgentRunning(ctx, cat, res); err != nil {
		return res, err
	}
	stepField := output.Field{Key: "step", Null: true}
	stepID := output.Field{Key: "stepId", Null: true}
	stepName := ""
	if step != "" {
		s, err := resolveStep(ctx, cat, j, step)
		if err != nil {
			return res, err
		}
		stepName = s.Name
		stepField = output.Field{Key: "step", Value: s.Name}
		stepID = output.Field{Key: "stepId", Value: strconv.Itoa(s.StepID)}
	}
	res.Fields = []output.Field{
		{Key: "job", Value: j.Name}, stepField, stepID,
		{Key: "enabled", Value: boolText(j.IsEnabled)},
		{Key: "state", Value: jobStateName(j.CurrentState)},
		{Key: "wait", Value: strconv.Itoa(wait)},
	}
	if running, known := jobIsRunning(j.CurrentState); running && known {
		return res, output.NewError(output.CodeBlocked,
			fmt.Sprintf("job %s is already running (%s): Agent would refuse to start it", gosmo.QuoteName(j.Name), jobStateName(j.CurrentState)),
			fmt.Sprintf("Wait for it to end, or stop it first: gossms job stop %q.", j.Name))
	}
	if !j.IsEnabled {
		res.Warnings = append(res.Warnings, "the job is disabled: it runs now, but not on its schedules")
	}

	// The server's clock before the start: the run's history rows start at
	// or after it.
	var begun time.Time
	if wait > 0 && !dryRun(inv) {
		now, err := cat.CurrentTime(ctx)
		if err != nil {
			return res, readError(err)
		}
		begun = serverWall(now.Add(-time.Second))
	}
	werr := applyWrite(ctx, inv, res, func(ctx context.Context) error { return w.StartJob(ctx, j.Name, stepName) })
	if werr != nil {
		e, _ := agentError(werr, j.Name)
		return res, e
	}
	if dryRun(inv) {
		return res, nil
	}
	res.Fields = append(res.Fields, output.Field{Key: "started", Value: "true"})
	if wait == 0 {
		return res, nil
	}
	return res, waitForJob(ctx, cat, res, j, begun, time.Duration(wait)*time.Second)
}

// waitForJob is job start --wait: it reads the job every jobPollInterval
// until a run newer than j's last one is recorded and the job is no longer
// running, then fills res with the outcome and the run's history rows. The
// bound passing is TIMEOUT, the job left running; a cancelled ctx (Ctrl+C)
// returns with a warning and the job left running.
func waitForJob(ctx context.Context, cat Catalog, res *output.Result, j *gosmo.Job, begun time.Time, bound time.Duration) error {
	start := jobNow()
	for {
		if err := sleepCtx(ctx, jobPollInterval); err != nil {
			res.Warnings = append(res.Warnings, "stopped waiting: the job keeps running")
			return nil
		}
		now, err := cat.JobByName(ctx, j.Name)
		if err != nil {
			if ctx.Err() != nil {
				res.Warnings = append(res.Warnings, "stopped waiting: the job keeps running")
				return nil
			}
			res.Warnings = append(res.Warnings, "the job started, but it could not be read while waiting: "+err.Error())
			return jobReadError(err)
		}
		running, _ := jobIsRunning(now.CurrentState)
		if !running && !now.LastRunDate.IsZero() && !now.LastRunDate.Equal(j.LastRunDate) {
			return jobFinished(ctx, cat, res, now, begun, jobNow().Sub(start))
		}
		if waited := jobNow().Sub(start); waited >= bound {
			res.Fields = append(res.Fields, output.Field{Key: "finished", Value: "false"},
				output.Field{Key: "waitedS", Value: strconv.FormatInt(int64(waited/time.Second), 10)})
			return output.NewError(output.CodeTimeout,
				fmt.Sprintf("job %s is still running after %d s; it was not stopped", gosmo.QuoteName(j.Name), int64(bound/time.Second)),
				fmt.Sprintf("gossms job history %q shows the run once it ends; gossms job stop %q ends it.", j.Name, j.Name))
		}
	}
}

// jobFinished is the outcome --wait reports: the job's last run as msdb
// recorded it, and that run's history rows. A failed run is a warning
// quoting the failed step's message.
func jobFinished(ctx context.Context, cat Catalog, res *output.Result, j *gosmo.Job, begun time.Time, waited time.Duration) error {
	outcome := jobOutcomeName(j.LastRunOutcome)
	res.Fields = append(res.Fields,
		output.Field{Key: "finished", Value: "true"},
		output.Field{Key: "outcome", Value: outcome},
		output.Field{Key: "lastRun", Value: gridTime(j.LastRunDate)},
		output.Field{Key: "lastDurationS", Value: *secondsCell(j.LastRunDuration)},
		output.Field{Key: "waitedS", Value: strconv.FormatInt(int64(waited/time.Second), 10)})
	rows, err := cat.SearchJobHistory(ctx, gosmo.JobHistoryFilter{JobName: j.Name, Since: begun, IncludeSteps: true})
	if err != nil {
		res.Warnings = append(res.Warnings, "the run's history could not be read: "+err.Error())
		return nil
	}
	res.Sets = []output.Set{jobHistorySet(rows)}
	if j.LastRunOutcome == gosmo.JobOutcomeSucceeded {
		return nil
	}
	msg := fmt.Sprintf("the job's run ended %s", outcome)
	for _, h := range rows {
		if h.StepID > 0 && h.Outcome == gosmo.JobOutcomeFailed {
			msg += fmt.Sprintf(" at step %d %s: %s", h.StepID, h.StepName, h.Message)
			break
		}
	}
	res.Warnings = append(res.Warnings, msg)
	return nil
}

// --- job stop ---------------------------------------------------------------

func (d Deps) jobStop(ctx context.Context, inv *spec.Invocation) (*output.Result, error) {
	res := &output.Result{}
	sc, cat, w, err := d.connectWriter(ctx, inv, res)
	if err != nil {
		return res, err
	}
	defer sc.Close()

	j, err := findJob(ctx, cat, inv.Args[0])
	if err != nil {
		return res, err
	}
	if err := checkAgentRunning(ctx, cat, res); err != nil {
		return res, err
	}
	res.Fields = []output.Field{{Key: "job", Value: j.Name}, {Key: "state", Value: jobStateName(j.CurrentState)}}
	notRunning := func() (*output.Result, error) {
		res.Fields = append(res.Fields, output.Field{Key: "stopped", Value: "false"})
		res.Warnings = append(res.Warnings, fmt.Sprintf("job %s is not running: there is nothing to stop", gosmo.QuoteName(j.Name)))
		return res, nil
	}
	if running, known := jobIsRunning(j.CurrentState); known && !running {
		res.Fields = append(res.Fields, output.Field{Key: "dryRun", Value: boolText(dryRun(inv))})
		return notRunning()
	}
	werr := applyWrite(ctx, inv, res, func(ctx context.Context) error { return w.StopJob(ctx, j.Name) })
	if werr != nil {
		e, idle := agentError(werr, j.Name)
		if idle {
			return notRunning()
		}
		return res, e
	}
	if dryRun(inv) {
		return res, nil
	}
	res.Fields = append(res.Fields, output.Field{Key: "stopped", Value: "true"})
	if after, err := cat.JobByName(ctx, j.Name); err == nil {
		res.Fields = append(res.Fields, output.Field{Key: "stateAfter", Value: jobStateName(after.CurrentState)})
	} else {
		res.Warnings = append(res.Warnings, "the stop was sent, but the job could not be read back: "+err.Error())
	}
	return res, nil
}
