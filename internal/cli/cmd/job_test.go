package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/cli/output"
)

func setColumn(t *testing.T, s output.Set, col string) []string {
	t.Helper()
	i := slices.IndexFunc(s.Columns, func(c output.Column) bool { return c.Name == col })
	if i < 0 {
		t.Fatalf("set %s has no column %s", s.Name, col)
	}
	var out []string
	for _, r := range s.Rows {
		if r[i] == nil {
			out = append(out, "NULL")
		} else {
			out = append(out, *r[i])
		}
	}
	return out
}

func TestJobList(t *testing.T) {
	s := newJobServer(t)
	res, _, err := runJob(t, s, "list")
	if err != nil {
		t.Fatal(err)
	}
	set := res.Sets[0]
	if got := setColumn(t, set, "name"); !slices.Equal(got, []string{"Backup", "busy", "never_ran", "Nightly ETL"}) {
		t.Errorf("names %q", got)
	}
	if got := setColumn(t, set, "last_outcome"); !slices.Equal(got, []string{"failed", "succeeded", "NULL", "succeeded"}) {
		t.Errorf("outcomes %q", got)
	}
	if got := setColumn(t, set, "state"); got[1] != "executing" || got[2] != "idle" {
		t.Errorf("states %q", got)
	}
	if got := setColumn(t, set, "last_duration_s"); got[3] != "95" || got[2] != "NULL" {
		t.Errorf("durations %q", got)
	}
	if got := setColumn(t, set, "last_run"); got[3] != "2026-11-02 01:00:00.000" {
		t.Errorf("last_run %q: the server's wall clock as msdb records it", got)
	}
	if slices.ContainsFunc(set.Columns, func(c output.Column) bool { return c.Name == "failed_runs" }) || len(s.filters) != 0 {
		t.Errorf("failure columns or a history read without --failed-since: %v", s.filters)
	}

	res, _, err = runJob(t, newJobServer(t), "list", "--enabled", "--category", "data COLLECTOR")
	if err != nil {
		t.Fatal(err)
	}
	if got := setColumn(t, res.Sets[0], "name"); !slices.Equal(got, []string{"Nightly ETL"}) {
		t.Errorf("--enabled --category: %q", got)
	}
}

// --failed-since finds a failure a later success hid, from the history; a
// zoneless time is the server's wall clock, a zoned one read in its zone.
func TestJobListFailedSince(t *testing.T) {
	for _, tc := range []struct {
		since     string
		from      time.Time
		clockRead int
		names     []string
		runs      []string
	}{
		{"2026-10-30", agentDate("2026-10-30 00:00:00"), 0, []string{"Backup", "Nightly ETL"}, []string{"1", "1"}},
		{"2026-10-27T00:00:00Z", agentDate("2026-10-27 02:00:00"), 1, []string{"Backup", "Nightly ETL"}, []string{"1", "2"}},
		{"2026-11-01T01:00:00+01:00", agentDate("2026-11-01 02:00:00"), 1, []string{"Backup"}, []string{"1"}},
	} {
		s := newJobServer(t)
		res, _, err := runJob(t, s, "list", "--failed-since", tc.since)
		if err != nil {
			t.Fatalf("%s: %v", tc.since, err)
		}
		if len(s.filters) != 1 || !s.filters[0].Since.Equal(tc.from) || s.filters[0].Since.Location() != time.UTC ||
			!slices.Equal(s.filters[0].Outcomes, []gosmo.JobOutcome{gosmo.JobOutcomeFailed}) || s.filters[0].IncludeSteps {
			t.Errorf("%s: filter %+v, want since %v", tc.since, s.filters, tc.from)
		}
		if s.clockRead != tc.clockRead {
			t.Errorf("%s: %d clock reads", tc.since, s.clockRead)
		}
		set := res.Sets[0]
		if got := setColumn(t, set, "name"); !slices.Equal(got, tc.names) {
			t.Errorf("%s: names %q", tc.since, got)
		}
		if got := setColumn(t, set, "failed_runs"); !slices.Equal(got, tc.runs) {
			t.Errorf("%s: failed_runs %q", tc.since, got)
		}
		if got := setColumn(t, set, "last_failure"); got[0] != "2026-11-01 02:00:00.000" {
			t.Errorf("%s: last_failure %q", tc.since, got)
		}
	}
	_, dials, err := runJob(t, newJobServer(t), "list", "--failed-since", "yesterday")
	if errCode(err) != output.CodeUsageError || dials != 0 {
		t.Errorf("a bad time: %v after %d dials", err, dials)
	}
}

func TestJobShow(t *testing.T) {
	res, _, err := runJob(t, newJobServer(t), "show", "Nightly ETL")
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"name": "Nightly ETL", "enabled": "true", "category": "Data Collector", "owner": "sa",
		"state": "idle", "lastOutcome": "succeeded", "lastDurationS": "95", "lastRun": "2026-11-02 01:00:00.000",
		"nextRun": "2026-11-03 01:00:00.000", "notifyEmail": "on_failure", "notifyEmailOperator": "DBA",
		"deleteJob": "never", "startStep": "1",
	} {
		if got := fieldValue(res, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if len(res.Sets) != 2 || res.Sets[0].Name != "steps" || res.Sets[1].Name != "schedules" {
		t.Fatalf("sets %+v", res.Sets)
	}
	steps := res.Sets[0]
	if got := setColumn(t, steps, "on_success"); !slices.Equal(got, []string{"go to next step", "quit with success"}) {
		t.Errorf("on_success %q", got)
	}
	if got := setColumn(t, steps, "on_fail"); !slices.Equal(got, []string{"quit with failure", "go to step 1"}) {
		t.Errorf("on_fail %q", got)
	}
	if got := setColumn(t, steps, "last_outcome"); !slices.Equal(got, []string{"succeeded", "NULL"}) {
		t.Errorf("step outcomes %q", got)
	}
	if got := setColumn(t, steps, "last_duration_s"); got[0] != "60" {
		t.Errorf("step durations %q", got)
	}
	if got := setColumn(t, res.Sets[1], "start_date"); len(got) != 1 || got[0] != "2026-01-01 00:00:00.000" {
		t.Errorf("schedule start_date %q", got)
	}
	if got := setColumn(t, res.Sets[1], "frequency"); len(got) != 1 || !strings.Contains(got[0], "every day") {
		t.Errorf("frequency %q", got)
	}

	res, _, err = runJob(t, newJobServer(t), "show", "never_ran")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"lastRun", "lastOutcome", "lastDurationS", "description"} {
		if i := slices.IndexFunc(res.Fields, func(f output.Field) bool { return f.Key == key }); i < 0 || !res.Fields[i].Null {
			t.Errorf("a job that never ran: %s not null", key)
		}
	}

	_, _, err = runJob(t, newJobServer(t), "show", "Nope")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeNotFound || !strings.Contains(e.Hint, "job list") {
		t.Errorf("missing job: %v", err)
	}
}

func TestJobHistory(t *testing.T) {
	s := newJobServer(t)
	res, _, err := runJob(t, s, "history", "Nightly ETL")
	if err != nil {
		t.Fatal(err)
	}
	want := gosmo.JobHistoryFilter{JobName: "Nightly ETL", IncludeSteps: true, Max: defaultJobHistoryTop}
	if len(s.filters) != 1 || fmt.Sprint(s.filters[0]) != fmt.Sprint(want) {
		t.Errorf("filter %+v", s.filters)
	}
	if got := setColumn(t, res.Sets[0], "step_id"); !slices.Equal(got, []string{"0", "1", "0", "0"}) {
		t.Errorf("rows %q", got)
	}

	s = newJobServer(t)
	res, _, err = runJob(t, s, "history", "Backup", "--failed", "--since", "2026-11-01T00:00:00Z", "--top", "5")
	if err != nil {
		t.Fatal(err)
	}
	f := s.filters[0]
	if !f.Since.Equal(agentDate("2026-11-01 02:00:00")) || f.Max != 5 ||
		!slices.Equal(f.Outcomes, []gosmo.JobOutcome{gosmo.JobOutcomeFailed}) {
		t.Errorf("filter %+v", f)
	}
	if got := setColumn(t, res.Sets[0], "message"); !slices.Equal(got, []string{"The job failed.", "Msg 3201: Cannot open backup device"}) {
		t.Errorf("messages %q", got)
	}

	_, dials, err := runJob(t, newJobServer(t), "history", "Backup", "--top", "0")
	if errCode(err) != output.CodeUsageError || dials != 0 {
		t.Errorf("--top 0: %v after %d dials", err, dials)
	}
	_, _, err = runJob(t, newJobServer(t), "history", "Nope")
	if errCode(err) != output.CodeNotFound {
		t.Errorf("missing job: %v", err)
	}
}

func TestJobStart(t *testing.T) {
	s := newJobServer(t)
	res, _, err := runJob(t, s, "start", "Nightly ETL", "--step", "2", "--allow", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.started, [][2]string{{"Nightly ETL", "Load"}}) {
		t.Errorf("started %q", s.started)
	}
	if fieldValue(res, "step") != "Load" || fieldValue(res, "stepId") != "2" || fieldValue(res, "started") != "true" ||
		fieldValue(res, "dryRun") != "false" || hasField(res, "tsql") || len(res.Warnings) != 0 {
		t.Errorf("fields %+v, warnings %q", res.Fields, res.Warnings)
	}

	// A step by name: exact, else the one match ignoring case.
	for _, step := range []string{"Load", "load", "EXTRACT"} {
		s := newJobServer(t)
		if _, _, err := runJob(t, s, "start", "Nightly ETL", "--step", step, "--allow", "admin"); err != nil {
			t.Fatalf("--step %s: %v", step, err)
		}
		if want := strings.ToLower(step); strings.ToLower(s.started[0][1]) != want {
			t.Errorf("--step %s started %q", step, s.started)
		}
	}

	s.started, s.stopped = nil, nil
	res, _, err = runJob(t, s, "start", "Backup", "--allow", "admin")
	if err != nil || len(s.started) != 1 || s.started[0][1] != "" {
		t.Fatalf("%v, started %q", err, s.started)
	}
	if !fieldIsNull(res, "step") || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "disabled") {
		t.Errorf("a disabled job: fields %+v, warnings %q", res.Fields, res.Warnings)
	}
}

func fieldIsNull(res *output.Result, key string) bool {
	i := slices.IndexFunc(res.Fields, func(f output.Field) bool { return f.Key == key })
	return i >= 0 && res.Fields[i].Null
}

func TestJobStartDryRun(t *testing.T) {
	s := newJobServer(t)
	res, _, err := runJob(t, s, "start", "Nightly ETL", "--step", "extract", "--wait", "60", "--allow", "admin", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.started) != 0 || s.clockRead != 0 || s.reads["Nightly ETL"] != 1 {
		t.Errorf("started %q, %d clock reads, %d job reads", s.started, s.clockRead, s.reads["Nightly ETL"])
	}
	want := "EXEC msdb.dbo.sp_start_job @job_name = N'Nightly ETL', @step_name = N'extract'\nGO"
	if got := strings.TrimSpace(fieldValue(res, "tsql")); got != want {
		t.Errorf("tsql %q, want %q", got, want)
	}
	if fieldValue(res, "dryRun") != "true" || hasField(res, "started") || hasField(res, "finished") {
		t.Errorf("fields %+v", res.Fields)
	}
}

func TestJobStartRefusals(t *testing.T) {
	stopped := "Stopped"
	for _, tc := range []struct {
		name  string
		argv  []string
		setup func(*jobServer)
		code  output.Code
		dials int
		hint  string
	}{
		{"no --allow", []string{"Backup"}, nil, output.CodeBlocked, 0, ""},
		{"--wait negative", []string{"Backup", "--wait", "-1", "--allow", "admin"}, nil, output.CodeUsageError, 0, ""},
		{"--wait too long", []string{"Backup", "--wait", "86401", "--allow", "admin"}, nil, output.CodeUsageError, 0, ""},
		{"--step empty", []string{"Backup", "--step", "", "--allow", "admin"}, nil, output.CodeUsageError, 0, ""},
		{"missing job", []string{"Nope", "--allow", "admin"}, nil, output.CodeNotFound, 1, "job list"},
		{"missing step", []string{"Nightly ETL", "--step", "9", "--allow", "admin"}, nil, output.CodeNotFound, 1, "1 extract, 2 Load"},
		{"missing step name", []string{"Nightly ETL", "--step", "transform", "--allow", "admin"}, nil, output.CodeNotFound, 1, "1 extract"},
		{"already running", []string{"busy", "--allow", "admin"}, nil, output.CodeBlocked, 1, "job stop"},
		{"agent stopped", []string{"Backup", "--allow", "admin"}, func(c *jobServer) { c.agent = stopped }, output.CodeUnsupported, 1, "mssql-conf"},
		{"agent stopped, dry run", []string{"Backup", "--allow", "admin", "--dry-run"}, func(c *jobServer) { c.agent = stopped },
			output.CodeUnsupported, 1, ""},
		{"no msdb access", []string{"Backup", "--allow", "admin"}, func(c *jobServer) {
			c.readErr = mssql.Error{Number: 229, Class: 14, Message: "The SELECT permission was denied on the object 'sysjobs'"}
		}, output.CodePermissionDenied, 1, "sysadmin"},
	} {
		s := newJobServer(t)
		if tc.setup != nil {
			tc.setup(s)
		}
		_, dials, err := runJob(t, s, append([]string{"start"}, tc.argv...)...)
		e, ok := err.(*output.Error)
		if !ok || e.Code != tc.code || dials != tc.dials || !strings.Contains(e.Hint, tc.hint) {
			t.Errorf("%s: %v (hint %q) after %d dials", tc.name, err, hintOf(err), dials)
		}
		if len(s.started) != 0 {
			t.Errorf("%s: started %q", tc.name, s.started)
		}
	}

	// Unknown status (no service row, no VIEW SERVER STATE) and an unreadable
	// one leave it to the server.
	for _, setup := range []func(*jobServer){
		func(c *jobServer) { c.agent = "Unknown" },
		func(c *jobServer) { c.agentErr = fmt.Errorf("boom") },
	} {
		s := newJobServer(t)
		setup(s)
		if _, _, err := runJob(t, s, "start", "Backup", "--allow", "admin"); err != nil || len(s.started) != 1 {
			t.Errorf("an unknown Agent status refused the start: %v", err)
		}
	}
}

func hintOf(err error) string {
	if e, ok := err.(*output.Error); ok {
		return e.Hint
	}
	return ""
}

// agentRefusal is SQL Server Agent's refusal as go-mssqldb reports it.
func agentRefusal(msg string) error {
	return mssql.Error{Number: msgAgentRefused, Class: 16, Message: "SQLServerAgent Error: " + msg}
}

// The server's refusals, as captured on 17 (Agent's are Msg 22022 told
// apart by their text).
func TestJobStartServerRefusals(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code output.Code
	}{
		{agentRefusal("Request to run job Backup (from User sa) refused because the job is already running from a request by User sa."), output.CodeBlocked},
		{agentRefusal("Request to run job p8m_job (from User sa) refused because the job already has a pending request from User sa."), output.CodeBlocked},
		{agentRefusal("SQLServerAgent is not currently running so it cannot be notified of this action."), output.CodeUnsupported},
		{mssql.Error{Number: msgJobIdentifier, Class: 16, Message: "The specified @job_name ('Backup') does not exist."}, output.CodeNotFound},
		{mssql.Error{Number: 229, Class: 14, Message: "The EXECUTE permission was denied on the object 'sp_start_job'"}, output.CodePermissionDenied},
	} {
		s := newJobServer(t)
		s.err = tc.err
		_, _, err := runJob(t, s, "start", "Backup", "--allow", "admin")
		if errCode(err) != tc.code {
			t.Errorf("%v: %v, want %s", tc.err, err, tc.code)
		}
	}
}

// fakeJobClock makes sleepCtx advance a fake clock jobNow reads, so --wait
// polls without sleeping; cancelAt cancels the sleep at that poll (0: never).
func fakeJobClock(t *testing.T, cancelAt int) *int {
	t.Helper()
	oldSleep, oldNow := sleepCtx, jobNow
	t.Cleanup(func() { sleepCtx, jobNow = oldSleep, oldNow })
	var clock time.Time
	polls := new(int)
	sleepCtx = func(ctx context.Context, d time.Duration) error {
		*polls++
		if *polls == cancelAt {
			return context.Canceled
		}
		clock = clock.Add(d)
		return nil
	}
	jobNow = func() time.Time { return clock }
	return polls
}

// running and done are Nightly ETL while its started run goes, and after it
// ended with outcome.
func etlRun(t *testing.T, outcome gosmo.JobOutcome) (running, done *gosmo.Job) {
	base := newJobServer(t).jobs[2]
	r, d := *base, *base
	r.CurrentState = gosmo.JobStateExecuting
	d.LastRunDate, d.LastRunOutcome, d.LastRunDuration = agentDate("2026-11-03 10:00:00"), outcome, 6*time.Second
	return &r, &d
}

func TestJobStartWait(t *testing.T) {
	polls := fakeJobClock(t, 0)
	s := newJobServer(t)
	running, done := etlRun(t, gosmo.JobOutcomeSucceeded)
	// Still idle with the old run first: sp_start_job is asynchronous.
	s.polls = []*gosmo.Job{s.jobs[2], running, running, done}
	s.history = append([]*gosmo.JobHistoryEntry{
		{JobName: "Nightly ETL", RunDate: agentDate("2026-11-03 10:00:00"), Outcome: gosmo.JobOutcomeSucceeded, StepID: 0, Duration: 6 * time.Second},
		{JobName: "Nightly ETL", RunDate: agentDate("2026-11-03 10:00:00"), Outcome: gosmo.JobOutcomeSucceeded, StepID: 1, StepName: "extract"},
	}, s.history...)
	res, _, err := runJob(t, s, "start", "Nightly ETL", "--wait", "60", "--allow", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if *polls != 4 {
		t.Errorf("%d polls, want 4", *polls)
	}
	for key, want := range map[string]string{"finished": "true", "outcome": "succeeded", "lastDurationS": "6",
		"lastRun": "2026-11-03 10:00:00.000", "waitedS": "8"} {
		if got := fieldValue(res, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	// The history read starts a second before the server's clock at the start,
	// as a server-local wall clock.
	f := s.filters[len(s.filters)-1]
	if f.JobName != "Nightly ETL" || !f.IncludeSteps || !f.Since.Equal(agentDate("2026-11-03 09:59:59")) {
		t.Errorf("history filter %+v", f)
	}
	if len(res.Sets) != 1 || len(res.Sets[0].Rows) != 2 || len(res.Warnings) != 0 {
		t.Errorf("sets %+v, warnings %q", res.Sets, res.Warnings)
	}
}

func TestJobStartWaitFailed(t *testing.T) {
	fakeJobClock(t, 0)
	s := newJobServer(t)
	_, done := etlRun(t, gosmo.JobOutcomeFailed)
	s.polls = []*gosmo.Job{done}
	s.history = append([]*gosmo.JobHistoryEntry{
		{JobName: "Nightly ETL", RunDate: agentDate("2026-11-03 10:00:01"), Outcome: gosmo.JobOutcomeFailed, StepID: 2, StepName: "Load", Message: "Msg 50000: p8h failure"},
		{JobName: "Nightly ETL", RunDate: agentDate("2026-11-03 10:00:00"), Outcome: gosmo.JobOutcomeFailed, StepID: 0, Message: "The job failed."},
	}, s.history...)
	res, _, err := runJob(t, s, "start", "Nightly ETL", "--wait", "60", "--allow", "admin")
	if err != nil {
		t.Fatalf("a failed run is not the command's failure: %v", err)
	}
	if fieldValue(res, "outcome") != "failed" || len(res.Warnings) != 1 ||
		!strings.Contains(res.Warnings[0], "at step 2 Load: Msg 50000: p8h failure") {
		t.Errorf("outcome %q, warnings %q", fieldValue(res, "outcome"), res.Warnings)
	}
}

func TestJobStartWaitTimeout(t *testing.T) {
	polls := fakeJobClock(t, 0)
	s := newJobServer(t)
	running, _ := etlRun(t, gosmo.JobOutcomeSucceeded)
	s.polls = []*gosmo.Job{running}
	res, _, err := runJob(t, s, "start", "Nightly ETL", "--wait", "5", "--allow", "admin")
	if errCode(err) != output.CodeTimeout || !strings.Contains(hintOf(err), "job stop") {
		t.Fatalf("%v", err)
	}
	if *polls != 3 || fieldValue(res, "finished") != "false" || fieldValue(res, "waitedS") != "6" || len(s.stopped) != 0 {
		t.Errorf("%d polls, fields %+v, stopped %q", *polls, res.Fields, s.stopped)
	}
}

// Ctrl+C stops the waiting, never the job.
func TestJobStartWaitCancelled(t *testing.T) {
	fakeJobClock(t, 2)
	s := newJobServer(t)
	running, _ := etlRun(t, gosmo.JobOutcomeSucceeded)
	s.polls = []*gosmo.Job{running}
	res, _, err := runJob(t, s, "start", "Nightly ETL", "--wait", "60", "--allow", "admin")
	if err != nil || len(s.stopped) != 0 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "keeps running") {
		t.Errorf("%v, stopped %q, warnings %q", err, s.stopped, res.Warnings)
	}
}

func TestJobStop(t *testing.T) {
	s := newJobServer(t)
	idle := *s.jobs[1]
	idle.CurrentState = gosmo.JobStateIdle
	s.polls = []*gosmo.Job{&idle}
	res, _, err := runJob(t, s, "stop", "busy", "--allow", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(s.stopped, []string{"busy"}) || fieldValue(res, "stopped") != "true" ||
		fieldValue(res, "state") != "executing" || fieldValue(res, "stateAfter") != "idle" {
		t.Errorf("stopped %q, fields %+v", s.stopped, res.Fields)
	}

	// The read says idle: nothing is sent, and the wanted state holds.
	for _, extra := range [][]string{nil, {"--dry-run"}} {
		s.started, s.stopped = nil, nil
		res, _, err = runJob(t, s, append([]string{"stop", "Backup", "--allow", "admin"}, extra...)...)
		if err != nil || len(s.stopped) != 0 || fieldValue(res, "stopped") != "false" || hasField(res, "tsql") ||
			len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "not running") {
			t.Errorf("idle %q: %v, stopped %q, fields %+v, warnings %q", extra, err, s.stopped, res.Fields, res.Warnings)
		}
	}

	// The state is unknown, and the server says it is not running.
	s = newJobServer(t)
	s.jobs[0].CurrentState = gosmo.JobStateUnknown
	s.err = agentRefusal("Request to stop job Backup (from User sa) refused because the job is not currently running.")
	res, _, err = runJob(t, s, "stop", "Backup", "--allow", "admin")
	if err != nil || len(s.stopped) != 1 || fieldValue(res, "stopped") != "false" || len(res.Warnings) != 1 {
		t.Errorf("server's not running: %v, fields %+v, warnings %q", err, res.Fields, res.Warnings)
	}

	s = newJobServer(t)
	s.agent = "Stopped"
	if _, _, err := runJob(t, s, "stop", "busy", "--allow", "admin"); errCode(err) != output.CodeUnsupported {
		t.Errorf("Agent stopped: %v", err)
	}
	s = newJobServer(t)
	s.err = agentRefusal("SQLServerAgent is not currently running so it cannot be notified of this action.")
	if _, _, err := runJob(t, s, "stop", "busy", "--allow", "admin"); errCode(err) != output.CodeUnsupported {
		t.Errorf("server's Agent stopped: %v", err)
	}
}

func TestJobStopDryRun(t *testing.T) {
	s := newJobServer(t)
	res, _, err := runJob(t, s, "stop", "busy", "--allow", "admin", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.stopped) != 0 || s.reads["busy"] != 1 || hasField(res, "stopped") ||
		strings.TrimSpace(fieldValue(res, "tsql")) != "EXEC msdb.dbo.sp_stop_job @job_name = N'busy'\nGO" {
		t.Errorf("stopped %q, %d reads, fields %+v", s.stopped, s.reads["busy"], res.Fields)
	}
}
