package tui

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
)

// Start Job and Stop Job read the job's state before acting, so a request the
// Agent would refuse is never sent and the user is told why in the app's own
// words rather than through "SQLServerAgent Error: Request to run job ...
// refused because the job is already running".
//
// The state is the 17th column of the job read (JobByNameContext's CASE:
// 4 while a session is open, 1 when idle), which is why the fixture varies
// that field alone — everything else about the job is identical in both runs.
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
		idle    = int64(1)
		running = int64(4)
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
