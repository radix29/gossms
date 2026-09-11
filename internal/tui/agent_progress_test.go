package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/db"
)

// Every unconfirmed Agent write (Enable/Disable on four entity types,
// Start/Stop Job) runs behind the progress dialog: up while in flight, Cancel
// stops it, status says cancelled — so an sp_update_job blocked on a lock can
// be stopped.
func TestAgentWritesRunBehindTheProgressDialog(t *testing.T) {
	for _, c := range []struct {
		name      string
		responses []fakeResponse
		label     string
		write     string
		act       func(a *App, sc *db.ServerConn, node *explorerNode)
	}{
		{"disable a job", agentJobResponses(jobRowInState(4)), agentJobName, "sp_update_job",
			func(a *App, sc *db.ServerConn, n *explorerNode) { a.setAgentJobEnabled(sc, n, false) }},
		{"enable a schedule", agentScheduleResponses(), agentScheduleName, "sp_update_schedule",
			func(a *App, sc *db.ServerConn, n *explorerNode) { a.setAgentScheduleEnabled(sc, n, true) }},
		{"disable an alert", agentAlertResponses(), agentAlertName, "sp_update_alert",
			func(a *App, sc *db.ServerConn, n *explorerNode) { a.setAgentAlertEnabled(sc, n, false) }},
		{"enable an operator", agentOperatorResponses(), agentOperatorName, "sp_update_operator",
			func(a *App, sc *db.ServerConn, n *explorerNode) { a.setAgentOperatorEnabled(sc, n, true) }},
		{"start a job", agentJobResponses(jobRowInState(4)), agentJobName, "sp_start_job",
			func(a *App, sc *db.ServerConn, n *explorerNode) { a.startAgentJob(sc, n) }},
		{"stop a job", agentJobResponses(jobRowInState(1)), agentJobName, "sp_stop_job",
			func(a *App, sc *db.ServerConn, n *explorerNode) { a.stopAgentJob(sc, n) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			hold := make(chan struct{})
			defer close(hold)
			responses := append([]fakeResponse{{match: c.write, blockExec: hold}}, c.responses...)
			sc, inst := newFakeConn(t, responses...)
			node := agentJobTestNode(sc)
			node.label, node.data.Name = c.label, c.label

			c.act(a, sc, node)
			if !a.progressDialog.Visible() || !a.progressBusy {
				t.Fatalf("dialog visible = %v, busy = %v while the write runs, want both true",
					a.progressDialog.Visible(), a.progressBusy)
			}
			waitForStatement(t, inst, c.write)

			a.progressDialog.HandleKey(escapeKey())
			waitAndDrain(t, a)

			if a.progressDialog.Visible() || a.progressBusy {
				t.Errorf("dialog visible = %v, busy = %v after the write returned, want both false",
					a.progressDialog.Visible(), a.progressBusy)
			}
			if !strings.Contains(a.statusText, "cancelled") || !strings.Contains(a.statusText, c.label) {
				t.Errorf("status = %q, want it to say the action on %q was cancelled", a.statusText, c.label)
			}
		})
	}
}

// A successful toggle reports it, replacing any stale status such as an earlier
// cancelled attempt.
func TestAgentToggleReportsItsSuccess(t *testing.T) {
	a := newTestApp()
	sc, inst := newFakeConn(t, agentJobResponses(jobRowInState(4))...)
	node := agentJobTestNode(sc)
	node.data.IsEnabled = true
	a.setStatus(`Disabling "Nightly reindex" cancelled`)

	a.setAgentJobEnabled(sc, node, false)
	waitAndDrain(t, a)

	if want := `Job "Nightly reindex" is now disabled`; a.statusText != want {
		t.Errorf("status = %q, want %q", a.statusText, want)
	}
	if node.data.IsEnabled {
		t.Error("node still enabled after a successful disable")
	}
	if joined := strings.Join(inst.Statements(), "\n"); !strings.Contains(joined, "@enabled = 0") {
		t.Errorf("no sp_update_job @enabled = 0 sent:\n%s", joined)
	}
}
