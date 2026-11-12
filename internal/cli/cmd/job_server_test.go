package cmd

import (
	"context"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gosmo/gosmotest"
	"github.com/radix29/gossms/internal/cli/args"
	"github.com/radix29/gossms/internal/cli/clitest"
	"github.com/radix29/gossms/internal/cli/output"
	"github.com/radix29/gossms/internal/cli/spec"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// jobServer is real gosmo over canned msdb rows for the job commands: a
// catalogServer with the server's jobs, their steps, schedules and run
// history, Agent's status and its clock. The reads answer as the server's
// would — history filtered by the statement's own conditions (parsed from
// its text and arguments, so gosmo's SQL is what the test exercises) — and
// the writes are recorded and failed with err.
//
// JobByName answers from jobs on the first read of a name, then from polls
// in turn (the last one repeating), so a test sees a job start and end.
type jobServer struct {
	*catalogServer
	jobs      []*gosmo.Job
	steps     map[string][]*gosmo.JobStep
	schedules map[string][]*gosmo.Schedule
	history   []*gosmo.JobHistoryEntry

	// agent is Agent's status text (empty: Running); agentErr fails the
	// read, readErr every read of the jobs (msdb refusing the login).
	agent    string
	agentErr error
	readErr  error
	clock    time.Time
	polls    []*gosmo.Job

	// What the tests judge by: job reads by name, the history filters asked,
	// the clock reads, and the starts and stops (job, step) the server took.
	reads     map[string]int
	filters   []gosmo.JobHistoryFilter
	clockRead int
	started   [][2]string
	stopped   []string
	err       error

	// states is Agent's live state per lower-cased job id, as of the job
	// rows last served.
	states map[string]gosmo.JobState
}

var (
	histTop   = regexp.MustCompile(`SELECT TOP (\d+)\n\s+j\.name`)
	histName  = regexp.MustCompile(`j\.name = @p(\d+)`)
	histSince = regexp.MustCompile(`h\.run_date > @p(\d+) OR \(h\.run_date = @p\d+ AND h\.run_time >= @p(\d+)\)`)
	histIn    = regexp.MustCompile(`h\.run_status IN \(([^)]*)\)`)
	argIndex  = regexp.MustCompile(`@p(\d+)`)
)

// argAt is the query's argument that parameter @pN names.
func argAt(q gosmotest.Query, n string) any {
	i, _ := strconv.Atoi(n)
	return q.Arg(i - 1)
}

func (s *jobServer) rules() {
	s.OnFunc(func(q gosmotest.Query) bool { return q.Exec }, s.applyWrite)
	read := func(rows func(q gosmotest.Query) [][]any, subs ...string) {
		s.OnFunc(func(q gosmotest.Query) bool { return has(q, subs...) }, func(q gosmotest.Query) gosmotest.Result {
			if s.readErr != nil {
				return gosmotest.Result{Err: s.readErr}
			}
			return gosmotest.Result{Sets: []gosmotest.Set{{Rows: rows(q)}}}
		})
	}
	// The jobs: all of them, or the one a WHERE j.name = @p1 names.
	read(s.jobRows, "FROM   msdb.dbo.sysjobs j\nLEFT   JOIN msdb.dbo.syscategories")
	s.on(func(gosmotest.Query) [][]any {
		var rows [][]any
		for id, st := range s.states {
			rows = append(rows, []any{id, int(st)})
		}
		return rows
	}, "xp_sqlagent_enum_jobs")
	jobOf := func(q gosmotest.Query) *gosmo.Job {
		id := argString(q, 0)
		for _, j := range s.jobs {
			if j.JobID == id {
				return j
			}
		}
		return nil
	}
	read(func(q gosmotest.Query) [][]any {
		var rows [][]any
		if j := jobOf(q); j != nil {
			for _, st := range s.steps[j.Name] {
				rows = append(rows, clitest.JobStepRow(st))
			}
		}
		return rows
	}, "FROM   msdb.dbo.sysjobsteps s", "WHERE  s.job_id = @p1")
	read(func(q gosmotest.Query) [][]any {
		var rows [][]any
		if j := jobOf(q); j != nil {
			for _, sch := range s.schedules[j.Name] {
				rows = append(rows, clitest.ScheduleRow(sch))
			}
		}
		return rows
	}, "JOIN   msdb.dbo.sysjobschedules js ON", "WHERE  js.job_id = @p1")
	read(s.historyRows, "FROM   msdb.dbo.sysjobhistory h")
	s.OnFunc(func(q gosmotest.Query) bool { return has(q, "FROM   sys.dm_server_services") }, func(gosmotest.Query) gosmotest.Result {
		if s.agentErr != nil {
			return gosmotest.Result{Err: s.agentErr}
		}
		status := s.agent
		if status == "" {
			status = "Running"
		}
		return gosmotest.Result{Sets: []gosmotest.Set{{Rows: [][]any{clitest.AgentStatusRow(status, time.Time{})}}}}
	})
	s.on(func(gosmotest.Query) [][]any {
		s.clockRead++
		return [][]any{{s.clock}}
	}, "SELECT SYSDATETIMEOFFSET()")
}

// jobRows serves the job read: every job, name order, or the named one —
// from polls after the first read of a name.
func (s *jobServer) jobRows(q gosmotest.Query) [][]any {
	var served []*gosmo.Job
	if has(q, "WHERE  j.name = @p1") {
		name := argString(q, 0)
		if s.reads == nil {
			s.reads = map[string]int{}
		}
		s.reads[name]++
		if n := s.reads[name]; n > 1 && len(s.polls) > 0 {
			served = []*gosmo.Job{s.polls[min(n-2, len(s.polls)-1)]}
		} else if i := slices.IndexFunc(s.jobs, func(j *gosmo.Job) bool { return j.Name == name }); i >= 0 {
			served = []*gosmo.Job{s.jobs[i]}
		}
	} else {
		served = slices.Clone(s.jobs)
		slices.SortFunc(served, func(a, b *gosmo.Job) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	}
	s.states = map[string]gosmo.JobState{}
	var rows [][]any
	for _, j := range served {
		s.states[strings.ToLower(j.JobID)] = j.CurrentState
		rows = append(rows, clitest.JobRow(j))
	}
	return rows
}

// historyRows is the history read as the server filters it: the statement's
// own conditions are parsed back into a gosmo.JobHistoryFilter (kept in
// filters), then applied newest first.
func (s *jobServer) historyRows(q gosmotest.Query) [][]any {
	f := gosmo.JobHistoryFilter{IncludeSteps: !has(q, "h.step_id = 0")}
	if m := histTop.FindStringSubmatch(q.Text); m != nil {
		f.Max, _ = strconv.Atoi(m[1])
	}
	if m := histName.FindStringSubmatch(q.Text); m != nil {
		f.JobName = argAt(q, m[1]).(string)
	}
	if m := histSince.FindStringSubmatch(q.Text); m != nil {
		d, c := argAt(q, m[1]).(int64), argAt(q, m[2]).(int64)
		f.Since = time.Date(int(d/10000), time.Month(d%10000/100), int(d%100), int(c/10000), int(c%10000/100), int(c%100), 0, time.UTC)
	}
	if m := histIn.FindStringSubmatch(q.Text); m != nil {
		for _, p := range argIndex.FindAllStringSubmatch(m[1], -1) {
			f.Outcomes = append(f.Outcomes, gosmo.JobOutcome(argAt(q, p[1]).(int64)))
		}
	}
	s.filters = append(s.filters, f)
	limit := f.Max
	if limit <= 0 {
		limit = 100
	}
	var rows [][]any
	for _, h := range s.history {
		if (f.JobName != "" && h.JobName != f.JobName) || (!f.IncludeSteps && h.StepID != 0) ||
			(!f.Since.IsZero() && h.RunDate.Before(f.Since)) ||
			(len(f.Outcomes) > 0 && !slices.Contains(f.Outcomes, h.Outcome)) {
			continue
		}
		rows = append(rows, clitest.JobHistoryRow(h))
		if len(rows) == limit {
			break
		}
	}
	return rows
}

var jobWrite = regexp.MustCompile(`^EXEC msdb\.dbo\.sp_(start|stop)_job @job_name = @p1(?:, @step_name = @p2)?$`)

// applyWrite is the server taking sp_start_job or sp_stop_job: recorded,
// then failed with err.
func (s *jobServer) applyWrite(q gosmotest.Query) gosmotest.Result {
	m := jobWrite.FindStringSubmatch(q.Text)
	if m == nil {
		s.t.Errorf("jobServer: a write it does not know: %s", q)
		return gosmotest.Result{}
	}
	job := argString(q, 0)
	if m[1] == "start" {
		s.started = append(s.started, [2]string{job, argString(q, 1)})
	} else {
		s.stopped = append(s.stopped, job)
	}
	return gosmotest.Result{Err: s.err}
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

func newJobServer(t *testing.T) *jobServer {
	etl := &gosmo.Job{JobID: "6F1C0000-0000-0000-0000-000000000001", Name: "Nightly ETL", IsEnabled: true, Category: "Data Collector",
		OwnerLoginName: "sa", Description: "Loads the warehouse", StartStepID: 1,
		LastRunDate: agentDate("2026-11-02 01:00:00"), LastRunOutcome: gosmo.JobOutcomeSucceeded,
		LastRunDuration: 95 * time.Second, NextRunDate: agentDate("2026-11-03 01:00:00"),
		CurrentState: gosmo.JobStateIdle, NotifyLevelEmail: gosmo.NotifyOnFailure, NotifyEmailOperatorName: "DBA",
		DateCreated: agentDate("2026-01-01 00:00:00"), DateModified: agentDate("2026-10-01 00:00:00")}
	backup := &gosmo.Job{JobID: "6F1C0000-0000-0000-0000-000000000002", Name: "Backup", IsEnabled: false, Category: "Database Maintenance",
		OwnerLoginName: "sa", LastRunDate: agentDate("2026-11-01 02:00:00"), LastRunOutcome: gosmo.JobOutcomeFailed,
		CurrentState: gosmo.JobStateIdle}
	fresh := &gosmo.Job{JobID: "6F1C0000-0000-0000-0000-000000000003", Name: "never_ran", IsEnabled: true, Category: "[Uncategorized (Local)]",
		LastRunOutcome: gosmo.JobOutcomeUnknown, CurrentState: gosmo.JobStateIdle}
	busy := &gosmo.Job{JobID: "6F1C0000-0000-0000-0000-000000000004", Name: "busy", IsEnabled: true, Category: "[Uncategorized (Local)]",
		LastRunDate: agentDate("2026-11-02 03:00:00"), LastRunOutcome: gosmo.JobOutcomeSucceeded,
		CurrentState: gosmo.JobStateExecuting}
	s := &jobServer{
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
	newCatalogServerWith(t, "master", func(c *catalogServer) { s.catalogServer = c; s.rules() })
	return s
}

// runJob runs job <argv> over s, an ad-hoc connection; dials counts the
// connects.
func runJob(t *testing.T, s *jobServer, argv ...string) (res *output.Result, dials int, err error) {
	t.Helper()
	d := Deps{
		LoadConfig: func() (*config.Config, error) { return &config.Config{}, nil },
		Connect: func(ctx context.Context, _ config.Connection, _ db.Role) (*db.ServerConn, error) {
			dials++
			return s.conn(ctx)
		},
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
