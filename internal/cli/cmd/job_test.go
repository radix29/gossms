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
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// jobCatalog serves the job reads. JobByName answers from jobs on the first
// read of a name, then from polls in turn (the last one repeating), so a
// test sees a job start and end. history is filtered as gosmo's
// SearchJobHistory filters it, and every filter asked is kept.
type jobCatalog struct {
	Catalog
	jobs      []*gosmo.Job
	steps     map[string][]*gosmo.JobStep
	schedules map[string][]*gosmo.Schedule
	history   []*gosmo.JobHistoryEntry
	agent     *gosmo.AgentStatus
	agentErr  error
	readErr   error
	clock     time.Time
	polls     []*gosmo.Job
	reads     map[string]int
	filters   []gosmo.JobHistoryFilter
	clockRead int
}

func (c *jobCatalog) Jobs(context.Context) ([]*gosmo.Job, error) { return c.jobs, c.readErr }

func (c *jobCatalog) JobByName(_ context.Context, name string) (*gosmo.Job, error) {
	if c.readErr != nil {
		return nil, c.readErr
	}
	if c.reads == nil {
		c.reads = map[string]int{}
	}
	c.reads[name]++
	if n := c.reads[name]; n > 1 && len(c.polls) > 0 {
		return c.polls[min(n-2, len(c.polls)-1)], nil
	}
	for _, j := range c.jobs {
		if j.Name == name {
			return j, nil
		}
	}
	return nil, fmt.Errorf("gosmo: agent job %q not found: %w", name, gosmo.ErrNotFound)
}

func (c *jobCatalog) JobSteps(_ context.Context, j *gosmo.Job) ([]*gosmo.JobStep, error) {
	return c.steps[j.Name], nil
}

func (c *jobCatalog) JobSchedules(_ context.Context, j *gosmo.Job) ([]*gosmo.Schedule, error) {
	return c.schedules[j.Name], nil
}

func (c *jobCatalog) SearchJobHistory(_ context.Context, f gosmo.JobHistoryFilter) ([]*gosmo.JobHistoryEntry, error) {
	c.filters = append(c.filters, f)
	var out []*gosmo.JobHistoryEntry
	for _, h := range c.history {
		if (f.JobName != "" && h.JobName != f.JobName) || (!f.IncludeSteps && h.StepID != 0) ||
			(!f.Since.IsZero() && h.RunDate.Before(f.Since)) ||
			(len(f.Outcomes) > 0 && !slices.Contains(f.Outcomes, h.Outcome)) {
			continue
		}
		out = append(out, h)
	}
	if f.Max > 0 && len(out) > f.Max {
		out = out[:f.Max]
	}
	return out, nil
}

func (c *jobCatalog) AgentInfo(context.Context) (*gosmo.AgentStatus, error) {
	if c.agent == nil {
		return &gosmo.AgentStatus{Running: true, StatusText: "Running"}, c.agentErr
	}
	return c.agent, c.agentErr
}

func (c *jobCatalog) CurrentTime(context.Context) (time.Time, error) {
	c.clockRead++
	return c.clock, nil
}

// jobWriter records the starts and stops, failing them with err. Under
// WithScript it is the real serverWriter, so a dry run captures gosmo's
// statement.
type jobWriter struct {
	Writer
	started [][2]string
	stopped []string
	err     error
}

func (w *jobWriter) StartJob(ctx context.Context, job, step string) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{&gosmo.Server{}}.StartJob(ctx, job, step)
	}
	w.started = append(w.started, [2]string{job, step})
	return w.err
}

func (w *jobWriter) StopJob(ctx context.Context, job string) error {
	if gosmo.Scripting(ctx) {
		return serverWriter{&gosmo.Server{}}.StopJob(ctx, job)
	}
	w.stopped = append(w.stopped, job)
	return w.err
}

// The server's clock in the tests: UTC+2, so a zoned time is two hours from
// its server-local wall clock.
var jobServerZone = time.FixedZone("UTC+2", 2*3600)

// agentDate is a server-local wall clock as gosmo carries msdb's: stamped UTC.
func agentDate(s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.UTC)
	if err != nil {
		panic(err)
	}
	return t
}

func newJobCatalog() *jobCatalog {
	etl := &gosmo.Job{JobID: "6f1c", Name: "Nightly ETL", IsEnabled: true, Category: "Data Collector",
		OwnerLoginName: "sa", Description: "Loads the warehouse", StartStepID: 1,
		LastRunDate: agentDate("2026-11-02 01:00:00"), LastRunOutcome: gosmo.JobOutcomeSucceeded,
		LastRunDuration: 95 * time.Second, NextRunDate: agentDate("2026-11-03 01:00:00"),
		CurrentState: gosmo.JobStateIdle, NotifyLevelEmail: gosmo.NotifyOnFailure, NotifyEmailOperatorName: "DBA",
		DateCreated: agentDate("2026-01-01 00:00:00"), DateModified: agentDate("2026-10-01 00:00:00")}
	backup := &gosmo.Job{Name: "Backup", IsEnabled: false, Category: "Database Maintenance",
		OwnerLoginName: "sa", LastRunDate: agentDate("2026-11-01 02:00:00"), LastRunOutcome: gosmo.JobOutcomeFailed,
		CurrentState: gosmo.JobStateIdle}
	fresh := &gosmo.Job{Name: "never_ran", IsEnabled: true, Category: "[Uncategorized (Local)]",
		LastRunOutcome: gosmo.JobOutcomeUnknown, CurrentState: gosmo.JobStateIdle}
	busy := &gosmo.Job{Name: "busy", IsEnabled: true, Category: "[Uncategorized (Local)]",
		LastRunDate: agentDate("2026-11-02 03:00:00"), LastRunOutcome: gosmo.JobOutcomeSucceeded,
		CurrentState: gosmo.JobStateExecuting}
	return &jobCatalog{
		jobs: []*gosmo.Job{backup, busy, etl, fresh},
		steps: map[string][]*gosmo.JobStep{"Backup": {{StepID: 1, Name: "backup", Subsystem: "TSQL"}}, "Nightly ETL": {
			{StepID: 1, Name: "extract", Subsystem: "TSQL", Database: "Stage", Command: "EXEC dbo.Extract",
				OnSuccessAction: 3, OnFailAction: 2, LastRunDate: agentDate("2026-11-02 01:00:00"),
				LastRunOutcome: gosmo.JobOutcomeSucceeded, LastRunElapsed: 60 * time.Second},
			{StepID: 2, Name: "Load", Subsystem: "TSQL", Database: "DW", Command: "EXEC dbo.Load",
				OnSuccessAction: 1, OnFailAction: 4, OnFailStepID: 1, RetryAttempts: 2, RetryInterval: 5},
		}},
		schedules: map[string][]*gosmo.Schedule{"Nightly ETL": {
			{ID: 7, Name: "nightly", Enabled: true, FreqType: gosmo.FreqDaily, FreqInterval: 1,
				ActiveStartDate: agentDate("2026-01-01 00:00:00"), ActiveStartTime: 10000},
		}},
		history: []*gosmo.JobHistoryEntry{
			{JobName: "Nightly ETL", RunDate: agentDate("2026-11-02 01:00:00"), Outcome: gosmo.JobOutcomeSucceeded, StepID: 0, StepName: "(Job outcome)", Duration: 95 * time.Second},
			{JobName: "Nightly ETL", RunDate: agentDate("2026-11-02 01:00:00"), Outcome: gosmo.JobOutcomeSucceeded, StepID: 1, StepName: "extract"},
			{JobName: "Backup", RunDate: agentDate("2026-11-01 02:00:00"), Outcome: gosmo.JobOutcomeFailed, StepID: 0, StepName: "(Job outcome)", Message: "The job failed."},
			{JobName: "Backup", RunDate: agentDate("2026-11-01 02:00:00"), Outcome: gosmo.JobOutcomeFailed, StepID: 1, StepName: "backup", Message: "Msg 3201: Cannot open backup device"},
			{JobName: "Nightly ETL", RunDate: agentDate("2026-10-31 01:00:00"), Outcome: gosmo.JobOutcomeFailed, StepID: 0, StepName: "(Job outcome)"},
			{JobName: "Nightly ETL", RunDate: agentDate("2026-10-28 01:00:00"), Outcome: gosmo.JobOutcomeFailed, StepID: 0, StepName: "(Job outcome)"},
		},
		clock: time.Date(2026, 11, 3, 10, 0, 0, 0, jobServerZone),
	}
}

// runJob runs job <argv> over cat and w, an ad-hoc connection; dials counts
// the connects.
func runJob(t *testing.T, cat *jobCatalog, w *jobWriter, argv ...string) (res *output.Result, dials int, err error) {
	t.Helper()
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(context.Context, config.Connection, db.Role) (*db.ServerConn, error) {
			dials++
			return &db.ServerConn{}, nil
		},
		Catalog: func(*db.ServerConn) Catalog { return cat },
		Writer:  func(*db.ServerConn) Writer { return w },
	}
	tree := &spec.Tree{Commands: JobCommands(d)}
	full := append([]string{"job"}, argv[0])
	full = append(full, "--server", "srv", "--auth", "windows")
	inv, perr := args.Parse(tree, append(full, argv[1:]...), args.Options{})
	if perr != nil {
		t.Fatalf("%q: %v", argv, perr)
	}
	res, err = inv.Command.Run(context.Background(), inv)
	return res, dials, err
}

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
	cat := newJobCatalog()
	res, _, err := runJob(t, cat, nil, "list")
	if err != nil {
		t.Fatal(err)
	}
	s := res.Sets[0]
	if got := setColumn(t, s, "name"); !slices.Equal(got, []string{"Backup", "busy", "Nightly ETL", "never_ran"}) {
		t.Errorf("names %q", got)
	}
	if got := setColumn(t, s, "last_outcome"); !slices.Equal(got, []string{"failed", "succeeded", "succeeded", "NULL"}) {
		t.Errorf("outcomes %q", got)
	}
	if got := setColumn(t, s, "state"); got[1] != "executing" || got[2] != "idle" {
		t.Errorf("states %q", got)
	}
	if got := setColumn(t, s, "last_duration_s"); got[2] != "95" || got[3] != "NULL" {
		t.Errorf("durations %q", got)
	}
	if got := setColumn(t, s, "last_run"); got[2] != "2026-11-02 01:00:00.000" {
		t.Errorf("last_run %q: the server's wall clock as msdb records it", got)
	}
	if slices.ContainsFunc(s.Columns, func(c output.Column) bool { return c.Name == "failed_runs" }) || len(cat.filters) != 0 {
		t.Errorf("failure columns or a history read without --failed-since: %v", cat.filters)
	}

	res, _, err = runJob(t, newJobCatalog(), nil, "list", "--enabled", "--category", "data COLLECTOR")
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
		cat := newJobCatalog()
		res, _, err := runJob(t, cat, nil, "list", "--failed-since", tc.since)
		if err != nil {
			t.Fatalf("%s: %v", tc.since, err)
		}
		if len(cat.filters) != 1 || !cat.filters[0].Since.Equal(tc.from) || cat.filters[0].Since.Location() != time.UTC ||
			!slices.Equal(cat.filters[0].Outcomes, []gosmo.JobOutcome{gosmo.JobOutcomeFailed}) || cat.filters[0].IncludeSteps {
			t.Errorf("%s: filter %+v, want since %v", tc.since, cat.filters, tc.from)
		}
		if cat.clockRead != tc.clockRead {
			t.Errorf("%s: %d clock reads", tc.since, cat.clockRead)
		}
		s := res.Sets[0]
		if got := setColumn(t, s, "name"); !slices.Equal(got, tc.names) {
			t.Errorf("%s: names %q", tc.since, got)
		}
		if got := setColumn(t, s, "failed_runs"); !slices.Equal(got, tc.runs) {
			t.Errorf("%s: failed_runs %q", tc.since, got)
		}
		if got := setColumn(t, s, "last_failure"); got[0] != "2026-11-01 02:00:00.000" {
			t.Errorf("%s: last_failure %q", tc.since, got)
		}
	}
	_, dials, err := runJob(t, newJobCatalog(), nil, "list", "--failed-since", "yesterday")
	if errCode(err) != output.CodeUsageError || dials != 0 {
		t.Errorf("a bad time: %v after %d dials", err, dials)
	}
}

func TestJobShow(t *testing.T) {
	res, _, err := runJob(t, newJobCatalog(), nil, "show", "Nightly ETL")
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
	if got := setColumn(t, res.Sets[1], "frequency"); len(got) != 1 || !strings.Contains(got[0], "every day") {
		t.Errorf("frequency %q", got)
	}

	res, _, err = runJob(t, newJobCatalog(), nil, "show", "never_ran")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"lastRun", "lastOutcome", "lastDurationS", "description"} {
		if i := slices.IndexFunc(res.Fields, func(f output.Field) bool { return f.Key == key }); i < 0 || !res.Fields[i].Null {
			t.Errorf("a job that never ran: %s not null", key)
		}
	}

	_, _, err = runJob(t, newJobCatalog(), nil, "show", "Nope")
	if e, ok := err.(*output.Error); !ok || e.Code != output.CodeNotFound || !strings.Contains(e.Hint, "job list") {
		t.Errorf("missing job: %v", err)
	}
}

func TestJobHistory(t *testing.T) {
	cat := newJobCatalog()
	res, _, err := runJob(t, cat, nil, "history", "Nightly ETL")
	if err != nil {
		t.Fatal(err)
	}
	want := gosmo.JobHistoryFilter{JobName: "Nightly ETL", IncludeSteps: true, Max: defaultJobHistoryTop}
	if len(cat.filters) != 1 || fmt.Sprint(cat.filters[0]) != fmt.Sprint(want) {
		t.Errorf("filter %+v", cat.filters)
	}
	if got := setColumn(t, res.Sets[0], "step_id"); !slices.Equal(got, []string{"0", "1", "0", "0"}) {
		t.Errorf("rows %q", got)
	}

	cat = newJobCatalog()
	res, _, err = runJob(t, cat, nil, "history", "Backup", "--failed", "--since", "2026-11-01T00:00:00Z", "--top", "5")
	if err != nil {
		t.Fatal(err)
	}
	f := cat.filters[0]
	if !f.Since.Equal(agentDate("2026-11-01 02:00:00")) || f.Max != 5 ||
		!slices.Equal(f.Outcomes, []gosmo.JobOutcome{gosmo.JobOutcomeFailed}) {
		t.Errorf("filter %+v", f)
	}
	if got := setColumn(t, res.Sets[0], "message"); !slices.Equal(got, []string{"The job failed.", "Msg 3201: Cannot open backup device"}) {
		t.Errorf("messages %q", got)
	}

	_, dials, err := runJob(t, newJobCatalog(), nil, "history", "Backup", "--top", "0")
	if errCode(err) != output.CodeUsageError || dials != 0 {
		t.Errorf("--top 0: %v after %d dials", err, dials)
	}
	_, _, err = runJob(t, newJobCatalog(), nil, "history", "Nope")
	if errCode(err) != output.CodeNotFound {
		t.Errorf("missing job: %v", err)
	}
}

func TestJobStart(t *testing.T) {
	cat, w := newJobCatalog(), &jobWriter{}
	res, _, err := runJob(t, cat, w, "start", "Nightly ETL", "--step", "2", "--allow", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.started, [][2]string{{"Nightly ETL", "Load"}}) {
		t.Errorf("started %q", w.started)
	}
	if fieldValue(res, "step") != "Load" || fieldValue(res, "stepId") != "2" || fieldValue(res, "started") != "true" ||
		fieldValue(res, "dryRun") != "false" || hasField(res, "tsql") || len(res.Warnings) != 0 {
		t.Errorf("fields %+v, warnings %q", res.Fields, res.Warnings)
	}

	// A step by name: exact, else the one match ignoring case.
	for _, step := range []string{"Load", "load", "EXTRACT"} {
		w := &jobWriter{}
		if _, _, err := runJob(t, newJobCatalog(), w, "start", "Nightly ETL", "--step", step, "--allow", "admin"); err != nil {
			t.Fatalf("--step %s: %v", step, err)
		}
		if want := strings.ToLower(step); strings.ToLower(w.started[0][1]) != want {
			t.Errorf("--step %s started %q", step, w.started)
		}
	}

	w = &jobWriter{}
	res, _, err = runJob(t, newJobCatalog(), w, "start", "Backup", "--allow", "admin")
	if err != nil || len(w.started) != 1 || w.started[0][1] != "" {
		t.Fatalf("%v, started %q", err, w.started)
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
	cat, w := newJobCatalog(), &jobWriter{}
	res, _, err := runJob(t, cat, w, "start", "Nightly ETL", "--step", "extract", "--wait", "60", "--allow", "admin", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.started) != 0 || cat.clockRead != 0 || cat.reads["Nightly ETL"] != 1 {
		t.Errorf("started %q, %d clock reads, %d job reads", w.started, cat.clockRead, cat.reads["Nightly ETL"])
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
	stopped := &gosmo.AgentStatus{StatusText: "Stopped"}
	for _, tc := range []struct {
		name  string
		argv  []string
		setup func(*jobCatalog)
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
		{"agent stopped", []string{"Backup", "--allow", "admin"}, func(c *jobCatalog) { c.agent = stopped }, output.CodeUnsupported, 1, "mssql-conf"},
		{"agent stopped, dry run", []string{"Backup", "--allow", "admin", "--dry-run"}, func(c *jobCatalog) { c.agent = stopped },
			output.CodeUnsupported, 1, ""},
		{"no msdb access", []string{"Backup", "--allow", "admin"}, func(c *jobCatalog) {
			c.readErr = mssql.Error{Number: 229, Class: 14, Message: "The SELECT permission was denied on the object 'sysjobs'"}
		}, output.CodePermissionDenied, 1, "sysadmin"},
	} {
		cat, w := newJobCatalog(), &jobWriter{}
		if tc.setup != nil {
			tc.setup(cat)
		}
		_, dials, err := runJob(t, cat, w, append([]string{"start"}, tc.argv...)...)
		e, ok := err.(*output.Error)
		if !ok || e.Code != tc.code || dials != tc.dials || !strings.Contains(e.Hint, tc.hint) {
			t.Errorf("%s: %v (hint %q) after %d dials", tc.name, err, hintOf(err), dials)
		}
		if len(w.started) != 0 {
			t.Errorf("%s: started %q", tc.name, w.started)
		}
	}

	// Unknown status (no service row, no VIEW SERVER STATE) and an unreadable
	// one leave it to the server.
	for _, setup := range []func(*jobCatalog){
		func(c *jobCatalog) { c.agent = &gosmo.AgentStatus{StatusText: "Unknown"} },
		func(c *jobCatalog) { c.agentErr = fmt.Errorf("boom") },
	} {
		cat, w := newJobCatalog(), &jobWriter{}
		setup(cat)
		if _, _, err := runJob(t, cat, w, "start", "Backup", "--allow", "admin"); err != nil || len(w.started) != 1 {
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
		{agentRefusal("SQLServerAgent is not currently running so it cannot be notified of this action."), output.CodeUnsupported},
		{mssql.Error{Number: msgJobIdentifier, Class: 16, Message: "The specified @job_name ('Backup') does not exist."}, output.CodeNotFound},
		{mssql.Error{Number: 229, Class: 14, Message: "The EXECUTE permission was denied on the object 'sp_start_job'"}, output.CodePermissionDenied},
	} {
		w := &jobWriter{err: tc.err}
		_, _, err := runJob(t, newJobCatalog(), w, "start", "Backup", "--allow", "admin")
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
func etlRun(outcome gosmo.JobOutcome) (running, done *gosmo.Job) {
	base := newJobCatalog().jobs[2]
	r, d := *base, *base
	r.CurrentState = gosmo.JobStateExecuting
	d.LastRunDate, d.LastRunOutcome, d.LastRunDuration = agentDate("2026-11-03 10:00:00"), outcome, 6*time.Second
	return &r, &d
}

func TestJobStartWait(t *testing.T) {
	polls := fakeJobClock(t, 0)
	cat, w := newJobCatalog(), &jobWriter{}
	running, done := etlRun(gosmo.JobOutcomeSucceeded)
	// Still idle with the old run first: sp_start_job is asynchronous.
	cat.polls = []*gosmo.Job{cat.jobs[2], running, running, done}
	cat.history = append([]*gosmo.JobHistoryEntry{
		{JobName: "Nightly ETL", RunDate: agentDate("2026-11-03 10:00:00"), Outcome: gosmo.JobOutcomeSucceeded, StepID: 0, Duration: 6 * time.Second},
		{JobName: "Nightly ETL", RunDate: agentDate("2026-11-03 10:00:00"), Outcome: gosmo.JobOutcomeSucceeded, StepID: 1, StepName: "extract"},
	}, cat.history...)
	res, _, err := runJob(t, cat, w, "start", "Nightly ETL", "--wait", "60", "--allow", "admin")
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
	f := cat.filters[len(cat.filters)-1]
	if f.JobName != "Nightly ETL" || !f.IncludeSteps || !f.Since.Equal(agentDate("2026-11-03 09:59:59")) {
		t.Errorf("history filter %+v", f)
	}
	if len(res.Sets) != 1 || len(res.Sets[0].Rows) != 2 || len(res.Warnings) != 0 {
		t.Errorf("sets %+v, warnings %q", res.Sets, res.Warnings)
	}
}

func TestJobStartWaitFailed(t *testing.T) {
	fakeJobClock(t, 0)
	cat, w := newJobCatalog(), &jobWriter{}
	_, done := etlRun(gosmo.JobOutcomeFailed)
	cat.polls = []*gosmo.Job{done}
	cat.history = append([]*gosmo.JobHistoryEntry{
		{JobName: "Nightly ETL", RunDate: agentDate("2026-11-03 10:00:01"), Outcome: gosmo.JobOutcomeFailed, StepID: 2, StepName: "Load", Message: "Msg 50000: p8h failure"},
		{JobName: "Nightly ETL", RunDate: agentDate("2026-11-03 10:00:00"), Outcome: gosmo.JobOutcomeFailed, StepID: 0, Message: "The job failed."},
	}, cat.history...)
	res, _, err := runJob(t, cat, w, "start", "Nightly ETL", "--wait", "60", "--allow", "admin")
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
	cat, w := newJobCatalog(), &jobWriter{}
	running, _ := etlRun(gosmo.JobOutcomeSucceeded)
	cat.polls = []*gosmo.Job{running}
	res, _, err := runJob(t, cat, w, "start", "Nightly ETL", "--wait", "5", "--allow", "admin")
	if errCode(err) != output.CodeTimeout || !strings.Contains(hintOf(err), "job stop") {
		t.Fatalf("%v", err)
	}
	if *polls != 3 || fieldValue(res, "finished") != "false" || fieldValue(res, "waitedS") != "6" || len(w.stopped) != 0 {
		t.Errorf("%d polls, fields %+v, stopped %q", *polls, res.Fields, w.stopped)
	}
}

// Ctrl+C stops the waiting, never the job.
func TestJobStartWaitCancelled(t *testing.T) {
	fakeJobClock(t, 2)
	cat, w := newJobCatalog(), &jobWriter{}
	running, _ := etlRun(gosmo.JobOutcomeSucceeded)
	cat.polls = []*gosmo.Job{running}
	res, _, err := runJob(t, cat, w, "start", "Nightly ETL", "--wait", "60", "--allow", "admin")
	if err != nil || len(w.stopped) != 0 || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "keeps running") {
		t.Errorf("%v, stopped %q, warnings %q", err, w.stopped, res.Warnings)
	}
}

func TestJobStop(t *testing.T) {
	cat, w := newJobCatalog(), &jobWriter{}
	idle := *cat.jobs[1]
	idle.CurrentState = gosmo.JobStateIdle
	cat.polls = []*gosmo.Job{&idle}
	res, _, err := runJob(t, cat, w, "stop", "busy", "--allow", "admin")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(w.stopped, []string{"busy"}) || fieldValue(res, "stopped") != "true" ||
		fieldValue(res, "state") != "executing" || fieldValue(res, "stateAfter") != "idle" {
		t.Errorf("stopped %q, fields %+v", w.stopped, res.Fields)
	}

	// The read says idle: nothing is sent, and the wanted state holds.
	for _, extra := range [][]string{nil, {"--dry-run"}} {
		w = &jobWriter{}
		res, _, err = runJob(t, newJobCatalog(), w, append([]string{"stop", "Backup", "--allow", "admin"}, extra...)...)
		if err != nil || len(w.stopped) != 0 || fieldValue(res, "stopped") != "false" || hasField(res, "tsql") ||
			len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "not running") {
			t.Errorf("idle %q: %v, stopped %q, fields %+v, warnings %q", extra, err, w.stopped, res.Fields, res.Warnings)
		}
	}

	// The state is unknown, and the server says it is not running.
	cat = newJobCatalog()
	cat.jobs[0].CurrentState = gosmo.JobStateUnknown
	w = &jobWriter{err: agentRefusal("Request to stop job Backup (from User sa) refused because the job is not currently running.")}
	res, _, err = runJob(t, cat, w, "stop", "Backup", "--allow", "admin")
	if err != nil || len(w.stopped) != 1 || fieldValue(res, "stopped") != "false" || len(res.Warnings) != 1 {
		t.Errorf("server's not running: %v, fields %+v, warnings %q", err, res.Fields, res.Warnings)
	}

	cat = newJobCatalog()
	cat.agent = &gosmo.AgentStatus{StatusText: "Stopped"}
	if _, _, err := runJob(t, cat, &jobWriter{}, "stop", "busy", "--allow", "admin"); errCode(err) != output.CodeUnsupported {
		t.Errorf("Agent stopped: %v", err)
	}
	w = &jobWriter{err: agentRefusal("SQLServerAgent is not currently running so it cannot be notified of this action.")}
	if _, _, err := runJob(t, newJobCatalog(), w, "stop", "busy", "--allow", "admin"); errCode(err) != output.CodeUnsupported {
		t.Errorf("server's Agent stopped: %v", err)
	}
}

func TestJobStopDryRun(t *testing.T) {
	cat, w := newJobCatalog(), &jobWriter{}
	res, _, err := runJob(t, cat, w, "stop", "busy", "--allow", "admin", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if len(w.stopped) != 0 || cat.reads["busy"] != 1 || hasField(res, "stopped") ||
		strings.TrimSpace(fieldValue(res, "tsql")) != "EXEC msdb.dbo.sp_stop_job @job_name = N'busy'\nGO" {
		t.Errorf("stopped %q, %d reads, fields %+v", w.stopped, cat.reads["busy"], res.Fields)
	}
}
