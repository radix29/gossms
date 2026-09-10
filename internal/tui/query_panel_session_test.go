package tui

import (
	"context"
	"database/sql/driver"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
)

// hostedSessionPanel adds a query panel connected the way connectForQueryPanel
// connects one — a pool plus a query.Session opened on it — over the scripted
// driver, with the session reporting tranCount open transactions. extra is
// appended to the script, for a COMMIT that should fail.
func hostedSessionPanel(t *testing.T, a *App, tranCount int64, extra ...fakeResponse) (*QueryPanel, *fakeInstance) {
	t.Helper()
	state := fakeResponse{match: "@@TRANCOUNT", cols: 3, rows: [][]driver.Value{{int64(61), "master", tranCount}}}
	sc, inst := newFakeConn(t, append([]fakeResponse{state}, extra...)...)
	sc.Opts = config.Connection{Server: "fake", User: "sa"}
	sess, st, err := query.Open(context.Background(), sc.Server.DB(), "")
	if err != nil {
		t.Fatalf("query.Open: %v", err)
	}
	qp := NewQueryPanel(a, "Query 1")
	qp.conn, qp.session, qp.tranCount, qp.database = sc, sess, st.TranCount, st.Database
	a.panels.AddPanel(qp)
	return qp, inst
}

func sentStatement(inst *fakeInstance, text string) bool {
	return slices.ContainsFunc(inst.Statements(), func(s string) bool { return strings.Contains(s, text) })
}

// BUG-1 step 5: closing a window whose session holds a transaction asks, as
// SSMS does, and Yes commits before the session ends — ending it silently
// would roll the work back.
func TestCloseWithOpenTransactionCommitsOnYes(t *testing.T) {
	a := newTestApp()
	qp, inst := hostedSessionPanel(t, a, 1)

	a.requestClosePanel(0)
	if !a.confirmDialog.Visible() {
		t.Fatal("closed a panel holding an open transaction without asking")
	}
	answer(a, 0)
	drainUntil(t, a, func() bool { return !a.panelHosted(qp) }, "the panel to close after the commit")
	if !sentStatement(inst, "COMMIT TRANSACTION") {
		t.Errorf("statements = %q, want a COMMIT before the close", inst.Statements())
	}
}

func TestCloseWithOpenTransactionRollsBackOnNo(t *testing.T) {
	a := newTestApp()
	qp, inst := hostedSessionPanel(t, a, 2)

	a.requestClosePanel(0)
	answer(a, 1)
	drainUntil(t, a, func() bool { return !a.panelHosted(qp) }, "the panel to close after the rollback")
	if !sentStatement(inst, "ROLLBACK TRANSACTION") || sentStatement(inst, "COMMIT") {
		t.Errorf("statements = %q, want a ROLLBACK and no COMMIT", inst.Statements())
	}
}

func TestCloseWithOpenTransactionCancelKeepsEverything(t *testing.T) {
	a := newTestApp()
	qp, inst := hostedSessionPanel(t, a, 1)

	a.requestClosePanel(0)
	answer(a, 2)
	if !a.panelHosted(qp) || !qp.connected() || qp.tranCount != 1 {
		t.Fatal("Cancel closed the panel or ended its session")
	}
	if sentStatement(inst, "TRANSACTION") {
		t.Errorf("statements = %q, want nothing sent on Cancel", inst.Statements())
	}
}

// A commit the server refuses must not be followed by the close: that ends
// the session and rolls back exactly the work the user asked to keep.
func TestFailedCommitKeepsThePanelOpen(t *testing.T) {
	a := newTestApp()
	a.alertDialog = dialogs.NewAlertDialog(a.screen)
	qp, _ := hostedSessionPanel(t, a, 1,
		fakeResponse{match: "COMMIT TRANSACTION", err: errors.New("commit refused")})

	a.requestClosePanel(0)
	answer(a, 0)
	drainUntil(t, a, func() bool { return !qp.executing }, "the commit to come back")
	if !a.panelHosted(qp) || !qp.connected() {
		t.Fatal("the panel was closed after its commit failed")
	}
	if qp.tranCount != 1 {
		t.Errorf("tranCount = %d after a failed commit, want it still 1", qp.tranCount)
	}
	if !a.alertDialog.Visible() {
		t.Error("a failed commit was not reported")
	}
}

// No open transaction, nothing unsaved: Ctrl+W closes at once, as before.
func TestCloseWithoutOpenTransactionDoesNotAsk(t *testing.T) {
	a := newTestApp()
	qp, _ := hostedSessionPanel(t, a, 0)
	qp.savedText = qp.editor.Text()

	a.requestClosePanel(0)
	if a.confirmDialog.Visible() || a.panelHosted(qp) {
		t.Fatal("asked about, or failed to close, a panel with nothing to lose")
	}
}

// Quit goes through the same prompt for every panel holding a transaction,
// and only quits once the commit is done.
func TestQuitAsksAboutOpenTransactions(t *testing.T) {
	a := newTestApp()
	qp, inst := hostedSessionPanel(t, a, 1)
	qp.savedText = qp.editor.Text()

	if a.requestQuit() {
		t.Fatal("quit without asking about an open transaction")
	}
	answer(a, 0)
	drainUntil(t, a, func() bool { return a.quitting }, "the quit to go ahead after the commit")
	if !sentStatement(inst, "COMMIT TRANSACTION") {
		t.Errorf("statements = %q, want the transaction committed before quitting", inst.Statements())
	}
}

// A run that lost its session leaves the panel disconnected, pointing at
// Reconnect, and says what went with it — never a silent redial.
func TestLostSessionDisconnectsThePanel(t *testing.T) {
	a := newTestApp()
	qp, _ := hostedSessionPanel(t, a, 1)

	res := &query.Result{SessionLost: true}
	if !qp.noteSessionState(res) {
		t.Fatal("noteSessionState did not report the loss")
	}
	if qp.connected() || qp.session != nil || qp.tranCount != 0 {
		t.Errorf("connected = %v, session = %v, tranCount = %d; want a disconnected panel",
			qp.connected(), qp.session, qp.tranCount)
	}
	if !strings.Contains(qp.notConnectedMessage(), "Reconnect") {
		t.Errorf("notConnectedMessage = %q, want it to point at Reconnect", qp.notConnectedMessage())
	}
	if n := len(res.Messages); n == 0 || !strings.Contains(res.Messages[n-1].Text, "temp tables") {
		t.Errorf("messages = %+v, want the lost session state explained", res.Messages)
	}
}

// BUG-1 step 6 and F-3: the connection bar carries the SPID, as SSMS's does,
// and calls out an open transaction.
func TestConnInfoShowsSPIDAndOpenTransactions(t *testing.T) {
	a := newTestApp()
	qp, _ := hostedSessionPanel(t, a, 0)
	if got, want := qp.connInfoText(), "fake | sa (61) | master"; got != want {
		t.Errorf("connInfoText() = %q, want %q", got, want)
	}
	qp.noteSessionState(&query.Result{State: &query.SessionState{Database: "master", TranCount: 2}})
	if got := qp.connInfoText(); !strings.HasSuffix(got, "| 2 open transactions") {
		t.Errorf("connInfoText() = %q, want the open transactions called out", got)
	}
}
