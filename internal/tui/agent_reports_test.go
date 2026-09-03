package tui

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
)

// The "Jobs Without Schedules" report fetches each job's schedules
// individually, so one job's read can fail while the rest succeed — the arm
// that lists it as "Unknown" rather than dropping it. A live server has no way
// to produce that: it answers every job or none. fakeResponse.err scoped by
// arg is the seam.

// jobSchedulesResponses answers Job.SchedulesContext. failFor, if non-empty,
// is the job_id whose read fails; every other job is answered with schedules.
func jobSchedulesResponses(failFor string, withSchedule ...string) []fakeResponse {
	const match = "WHERE  js.job_id = @p1"
	var out []fakeResponse
	if failFor != "" {
		out = append(out, fakeResponse{match: match, arg: failFor, err: errors.New("msdb unavailable")})
	}
	for _, id := range withSchedule {
		out = append(out, fakeResponse{match: match, arg: id, cols: 16,
			rows: [][]driver.Value{scheduleRow(3, "Daily 01:00", 4, 1, 0, "appuser")}})
	}
	// Everything left over has no schedule at all.
	out = append(out, fakeResponse{match: match, cols: 16})
	return out
}

func reportRow(t *testing.T, rows [][]string, name string) []string {
	t.Helper()
	for _, r := range rows {
		if r[0] == name {
			return r
		}
	}
	t.Fatalf("no row for job %q in %v", name, rows)
	return nil
}

// TestJobsWithoutSchedulesListsAFailedJobAsUnknown drives the three outcomes
// the report distinguishes in one run: a job with a schedule is omitted, a job
// with none reads "None", and the job whose read failed reads "Unknown"
// instead of being dropped — dropping it is the failure the arm exists to
// prevent, and it is indistinguishable from "all fine".
func TestJobsWithoutSchedulesListsAFailedJobAsUnknown(t *testing.T) {
	responses := append([]fakeResponse{
		{match: "FROM   msdb.dbo.sysjobs j", cols: 17, rows: [][]driver.Value{
			jobRow("scheduled", "Database Maintenance", "sa", true, 0, 0, ""),
			jobRow("broken", "Database Maintenance", "sa", true, 0, 0, ""),
			jobRow("orphan", "Replication", "appuser", false, 0, 0, ""),
		}},
	}, jobSchedulesResponses("job-broken", "job-scheduled")...)
	sc, inst := newFakeConn(t, responses...)

	cols, rows, err := agentReportDetail(context.Background(), sc, "Jobs Without Schedules")
	if err != nil {
		t.Fatalf("agentReportDetail: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (unanswered: %v)", len(rows), inst.unmatched)
	}
	schedIdx := -1
	for i, c := range cols {
		if c == "Schedules" {
			schedIdx = i
		}
	}
	if schedIdx < 0 {
		t.Fatalf("no Schedules column in %v", cols)
	}
	if got := reportRow(t, rows, "broken")[schedIdx]; got != "Unknown" {
		t.Errorf("the job whose read failed reads %q, want %q", got, "Unknown")
	}
	if got := reportRow(t, rows, "orphan")[schedIdx]; got != "None" {
		t.Errorf("the job with no schedule reads %q, want %q", got, "None")
	}
	for _, r := range rows {
		if r[0] == "scheduled" {
			t.Errorf("the job that has a schedule is listed: %v", r)
		}
	}
}

// TestJobsWithoutSchedulesReportsCancellation is the other half of the same
// arm: when the context is gone, every remaining job's read fails, and a
// report claiming every job is unverifiable is worse than the cancellation
// itself.
func TestJobsWithoutSchedulesReportsCancellation(t *testing.T) {
	sc, _ := newFakeConn(t,
		fakeResponse{match: "FROM   msdb.dbo.sysjobs j", cols: 17, rows: [][]driver.Value{
			jobRow("orphan", "Replication", "appuser", false, 0, 0, ""),
		}},
		fakeResponse{match: "WHERE  js.job_id = @p1", err: errors.New("context canceled")},
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := agentReportDetail(ctx, sc, "Jobs Without Schedules"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
