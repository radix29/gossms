package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	mssql "github.com/microsoft/go-mssqldb"
	"github.com/radix29/gossms/internal/db"
)

// Enable, Disable and Delete on a job, alert or operator write through a Ref:
// the write addresses the object by name, so a ByName read first is a round
// trip that buys nothing. Each case sends exactly its write and reads nothing.
func TestAgentMenuWritesReadNothingFirst(t *testing.T) {
	yes := func(a *App) { a.confirmDialog().HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone)) }
	for _, c := range []struct {
		name, label, want string
		act               func(a *App, sc *db.ServerConn, n *explorerNode)
	}{
		{"disable a job", agentJobName, "sp_update_job @job_name = N'" + agentJobName + "'",
			func(a *App, sc *db.ServerConn, n *explorerNode) { a.setAgentJobEnabled(sc, n, false) }},
		{"delete a job", agentJobName, "sp_delete_job @job_name = N'" + agentJobName + "'",
			func(a *App, sc *db.ServerConn, n *explorerNode) { a.deleteAgentJob(sc, n); yes(a) }},
		{"enable an alert", agentAlertName, "sp_update_alert @name = N'" + agentAlertName + "'",
			func(a *App, sc *db.ServerConn, n *explorerNode) { a.setAgentAlertEnabled(sc, n, true) }},
		{"delete an alert", agentAlertName, "sp_delete_alert @name = N'" + agentAlertName + "'",
			func(a *App, sc *db.ServerConn, n *explorerNode) { a.deleteAgentAlert(sc, n); yes(a) }},
		{"disable an operator", agentOperatorName, "sp_update_operator @name = N'" + agentOperatorName + "'",
			func(a *App, sc *db.ServerConn, n *explorerNode) { a.setAgentOperatorEnabled(sc, n, false) }},
		{"delete an operator", agentOperatorName, "sp_delete_operator @name = N'" + agentOperatorName + "'",
			func(a *App, sc *db.ServerConn, n *explorerNode) { a.deleteAgentOperator(sc, n); yes(a) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			sc, inst := newGConn(t)
			node := agentJobTestNode(sc)
			node.label, node.data.Name = c.label, c.label
			before := inst.QueryCount()

			c.act(a, sc, node)
			waitAndDrain(t, a)

			// Delete reloads the folder afterwards; nothing may be read
			// before the write lands, and the write is the only statement.
			stmts := inst.Statements()
			if len(stmts) != 1 || !strings.Contains(stmts[0], c.want) {
				t.Fatalf("statements = %q, want one containing %q", stmts, c.want)
			}
			if strings.HasPrefix(c.name, "disable") || strings.HasPrefix(c.name, "enable") {
				if n := inst.QueryCount() - before; n != 0 {
					t.Errorf("%d reads around the write, want none", n)
				}
			}
			if strings.Contains(a.statusText, "ailed") {
				t.Errorf("status = %q", a.statusText)
			}
		})
	}
}

// A job deleted underneath the menu is no longer caught by a ByName read, so
// the failure is msdb's own; it must still name the job and say it is gone.
func TestAgentMenuReportsAJobDeletedUnderneath(t *testing.T) {
	gone := mssql.Error{Number: 14262, Class: 16,
		Message: "The specified @job_name ('" + agentJobName + "') does not exist."}
	for _, c := range []struct {
		name string
		act  func(a *App, sc *db.ServerConn, n *explorerNode)
	}{
		{"disable", func(a *App, sc *db.ServerConn, n *explorerNode) { a.setAgentJobEnabled(sc, n, false) }},
		{"delete", func(a *App, sc *db.ServerConn, n *explorerNode) {
			a.deleteAgentJob(sc, n)
			a.confirmDialog().HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := newTestApp()
			sc, _ := newGConn(t,
				fakeResponse{match: "sp_update_job", err: gone},
				fakeResponse{match: "sp_delete_job", err: gone})
			c.act(a, sc, agentJobTestNode(sc))
			waitAndDrain(t, a)
			for _, want := range []string{"ailed", agentJobName, "does not exist"} {
				if !strings.Contains(a.statusText, want) {
					t.Errorf("status = %q, want it to contain %q", a.statusText, want)
				}
			}
		})
	}
}

// A system job (syspolicy_purge_history) is offered Delete, as in SSMS, but
// asks for its name to be typed, as SQL Server's own XEvent sessions do; a user
// job keeps the one-Yes confirmation. Confirm with the name untyped does nothing.
func TestDeletingASystemJobNeedsItsNameTyped(t *testing.T) {
	const sysJob = "syspolicy_purge_history"
	a := newTestApp()
	sc, inst := newGConn(t)
	node := agentJobTestNode(sc)
	node.label, node.data.Name, node.data.IsSystem = sysJob, sysJob, true

	a.deleteAgentJob(sc, node)
	if a.confirmDialog().Visible() || !a.confirmTypedDialog().Visible() {
		t.Fatal("deleting a system job should ask for the name to be typed")
	}
	enter := tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone)
	a.confirmTypedDialog().HandleKey(enter)
	if !a.confirmTypedDialog().Visible() {
		t.Fatal("Confirm with the name untyped closed the dialog")
	}
	for _, r := range sysJob {
		a.confirmTypedDialog().HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
	}
	a.confirmTypedDialog().HandleKey(enter)
	waitAndDrain(t, a)
	if stmts := inst.Statements(); len(stmts) != 1 || !strings.Contains(stmts[0], "sp_delete_job @job_name = N'"+sysJob+"'") {
		t.Fatalf("statements = %q, want the job's sp_delete_job", stmts)
	}

	user := agentJobTestNode(sc)
	a.deleteAgentJob(sc, user)
	if a.confirmTypedDialog().Visible() || !a.confirmDialog().Visible() {
		t.Error("a user job should keep the one-Yes confirmation")
	}
}
