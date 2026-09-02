package tui

import (
	"database/sql/driver"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// Start Job and Stop Job read the job's state before acting, so a request the
// Agent would refuse is never sent and the user is told why in the app's own
// words rather than through "SQLServerAgent Error: Request to run job ...
// refused because the job is already running".
//
// The state is the 17th column of the job read (JobByNameContext's CASE:
// 1 — Executing — while a session is open, 4 — Idle — otherwise), which is why
// the fixture varies that field alone; everything else about the job is
// identical in both runs. The fake answers no xp_sqlagent_enum_jobs read, so
// this also covers the fallback gosmo takes when Agent's own state is
// unreachable.
func jobRowInState(state int64) []driver.Value {
	row := jobRow(agentJobName, "Database Maintenance", "sa", true, 0, 0, "")
	row[len(row)-1] = state
	return row
}

func agentJobTestNode(sc *db.ServerConn) *explorerNode {
	n := opNode(NodeAgentJob, "", agentJobName, "")
	n.data.conn = sc
	parent := &explorerNode{label: "Jobs"}
	n.parent = parent
	parent.children = []*explorerNode{n}
	return n
}

func TestStartAndStopJobRefuseTheStateTheyWouldNotChange(t *testing.T) {
	const (
		idle    = int64(4)
		running = int64(1)
	)
	for _, c := range []struct {
		name        string
		state       int64
		start       bool
		wantStmt    string
		wantStatus  string
		wantRefused bool
	}{
		{"start an idle job", idle, true, "sp_start_job", `Job "Nightly reindex" started`, false},
		{"start a running job", running, true, "", `Job "Nightly reindex" is already running`, true},
		{"stop a running job", running, false, "sp_stop_job", `Job "Nightly reindex" stopped`, false},
		{"stop an idle job", idle, false, "", `Job "Nightly reindex" is not running`, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			sc, inst := newFakeConn(t, agentJobResponses(jobRowInState(c.state))...)
			node := agentJobTestNode(sc)

			if c.start {
				a.startAgentJob(sc, node)
			} else {
				a.stopAgentJob(sc, node)
			}
			waitAndDrain(t, a)

			joined := strings.Join(inst.Statements(), "\n---\n")
			ran := strings.Contains(joined, "sp_start_job") || strings.Contains(joined, "sp_stop_job")
			if c.wantRefused && ran {
				t.Errorf("a request the Agent would refuse was sent anyway:\n%s", joined)
			}
			if !c.wantRefused && !strings.Contains(joined, c.wantStmt) {
				t.Errorf("statements = %s, want %s", joined, c.wantStmt)
			}
			if a.statusText != c.wantStatus {
				t.Errorf("status = %q, want %q", a.statusText, c.wantStatus)
			}
		})
	}
}

// The label and the state are pinned together by name: a shared table read
// two ways cannot catch a swapped pair, and every one of these values is
// SQL Server Agent's, not gosmo's to choose.
func TestFormatJobStateNamesEveryAgentState(t *testing.T) {
	for _, tc := range []struct {
		state gosmo.JobState
		code  int
		want  string
	}{
		{gosmo.JobStateExecuting, 1, "Running"},
		{gosmo.JobStateWaitingForWorker, 2, "Waiting for worker thread"},
		{gosmo.JobStateBetweenRetries, 3, "Between retries"},
		{gosmo.JobStateIdle, 4, "Idle"},
		{gosmo.JobStateSuspended, 5, "Suspended"},
		{gosmo.JobStateWaitingForStepToFinish, 6, "Waiting for step to finish"},
		{gosmo.JobStatePerformingCompletionActions, 7, "Performing completion actions"},
		{gosmo.JobStateUnknown, 0, "Unknown"},
	} {
		if int(tc.state) != tc.code {
			t.Errorf("%q is job_state %d, want %d", tc.want, int(tc.state), tc.code)
		}
		if got := formatJobState(tc.state); got != tc.want {
			t.Errorf("formatJobState(%d) = %q, want %q", int(tc.state), got, tc.want)
		}
	}
}

func TestJobStateRefusalOnlyRefusesAKnownState(t *testing.T) {
	const name = "nightly"
	for _, tc := range []struct {
		state       gosmo.JobState
		wantRunning bool
		want        string
	}{
		{gosmo.JobStateIdle, false, `Job "nightly" is not running`},
		{gosmo.JobStateIdle, true, ""},
		{gosmo.JobStateExecuting, true, `Job "nightly" is already running`},
		{gosmo.JobStateExecuting, false, ""},
		{gosmo.JobStateBetweenRetries, true, `Job "nightly" is already running`},
		{gosmo.JobStatePerformingCompletionActions, true, `Job "nightly" is already running`},
		// Neither of these says anything about a live session, so the
		// request has to reach the server rather than be refused here.
		{gosmo.JobStateUnknown, true, ""},
		{gosmo.JobStateUnknown, false, ""},
		{gosmo.JobStateSuspended, true, ""},
		{gosmo.JobStateSuspended, false, ""},
	} {
		got := jobStateRefusal(name, tc.state, tc.wantRunning)
		if got != tc.want {
			t.Errorf("jobStateRefusal(state %d, wantRunning %v) = %q, want %q",
				int(tc.state), tc.wantRunning, got, tc.want)
		}
	}
}
