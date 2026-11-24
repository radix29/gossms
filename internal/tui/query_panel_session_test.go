package tui

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/tuikit/dialogs"
)

func TestUseDatabasePreservesQuerySessionAndResults(t *testing.T) {
	for _, fabric := range []bool{false, true} {
		t.Run(fmt.Sprintf("fabric=%v", fabric), func(t *testing.T) {
			a := newTestApp()
			qp, inst := hostedSessionPanelOn(t, a, fabric, 2)
			inst.Rescript([]fakeResponse{
				{match: "USE [Sa]]les]"},
				{match: "@@TRANCOUNT", cols: 3, rows: [][]driver.Value{{int64(61), "Sa]les", int64(2)}}},
			})
			sc := &db.ServerConn{Opts: qp.conn.Opts}
			node := &explorerNode{data: nodeData{Type: NodeDatabase, DBName: "Sa]les", conn: sc}}
			oldSession := qp.session
			qp.editor.SetText("SELECT * FROM #kept")
			qp.setResult(&query.Result{Database: "master", Messages: []query.Message{{Text: "previous results"}}}, false)
			oldResult, oldMessages := qp.result, qp.messages.Text()
			qp.resultsNotice = "previous notice"
			qp.resultsShown = false
			item := a.contextMenuItemsForNode(node)[0]
			if item.Label != "Use Database" || !item.Enabled() {
				t.Fatalf("first menu item = %+v, want enabled Use Database", item)
			}
			item.Action()
			if !qp.executing || item.Enabled() || qp.resultsShown {
				t.Fatal("switch did not hold the run latch or exposed the results pane")
			}
			drainUntil(t, a, func() bool { return !qp.executing }, "database change")
			if len(inst.Reads("USE [Sa]]les]")) != 1 || qp.database != "Sa]les" {
				t.Fatalf("database=%q, queries=%v", qp.database, inst.Queries())
			}
			if qp.session != oldSession || qp.session.SPID() != 61 || qp.tranCount != 2 {
				t.Fatal("changing database replaced the session or its transaction state")
			}
			if qp.result != oldResult || qp.messages.Text() != oldMessages || qp.editor.Text() != "SELECT * FROM #kept" || qp.resultsNotice != "previous notice" {
				t.Fatal("changing database changed the query text or previous results")
			}
			if item.Enabled() || !item.Checked() {
				t.Fatal("the current database was not checked and disabled")
			}
		})
	}
}

func TestUseDatabaseFailureKeepsContextAndResults(t *testing.T) {
	a := newTestApp()
	installDialog(a, dialogs.NewAlertDialog())
	qp, inst := hostedSessionPanel(t, a, 1,
		fakeResponse{match: "USE [denied]", err: errors.New("database access denied")})
	qp.setResult(&query.Result{Database: "master"}, false)
	oldResult := qp.result
	sc := &db.ServerConn{Opts: qp.conn.Opts}
	node := &explorerNode{data: nodeData{Type: NodeDatabase, DBName: "denied", conn: sc}}
	a.contextMenuItemsForNode(node)[0].Action()
	drainUntil(t, a, func() bool { return !qp.executing }, "failed database change")
	if len(inst.Reads("USE [denied]")) != 1 || qp.database != "master" || qp.result != oldResult || qp.tranCount != 1 {
		t.Fatal("failed change lost the old context, results or transaction state")
	}
	if !a.alertDialog().Visible() {
		t.Fatal("database access error was not reported")
	}
}

func TestUseDatabaseCancellationKeepsContextAndResults(t *testing.T) {
	a := newTestApp()
	installDialog(a, dialogs.NewAlertDialog())
	block := make(chan struct{})
	qp, inst := hostedSessionPanel(t, a, 1,
		fakeResponse{match: "USE [Sales]", block: block})
	t.Cleanup(func() { close(block) })
	qp.setResult(&query.Result{Database: "master"}, false)
	oldResult := qp.result
	sc := &db.ServerConn{Opts: qp.conn.Opts}
	node := &explorerNode{data: nodeData{Type: NodeDatabase, DBName: "Sales", conn: sc}}
	item := a.contextMenuItemsForNode(node)[0]
	item.Action()
	drainUntil(t, a, func() bool { return len(inst.Reads("USE [Sales]")) != 0 }, "USE to start")
	qp.CancelExecution()
	drainUntil(t, a, func() bool { return !qp.executing }, "cancelled database change")
	if qp.database != "master" || qp.result != oldResult || qp.tranCount != 1 || !qp.connected() {
		t.Fatal("cancellation lost the query's context, results or session")
	}
	if a.alertDialog().Visible() {
		t.Fatal("a user cancellation was reported as a database-access error")
	}
}

func TestUseDatabaseWithoutPostRunState(t *testing.T) {
	a := newTestApp()
	qp, inst := hostedSessionPanel(t, a, 1)
	inst.Rescript([]fakeResponse{
		{match: "USE [Sales]"},
		{match: "@@TRANCOUNT", err: errors.New("state read unavailable")},
	})
	qp.setResult(&query.Result{Database: "master"}, false)
	oldResult, oldSession := qp.result, qp.session
	qp.changeDatabase("Sales")
	drainUntil(t, a, func() bool { return !qp.executing }, "database change without state read")
	if qp.database != "Sales" || qp.result != oldResult || qp.session != oldSession || qp.tranCount != 1 {
		t.Fatal("successful USE without a state read lost the new context or previous session state")
	}
}

func TestUseDatabaseCancellationUsesReportedContext(t *testing.T) {
	a := newTestApp()
	installDialog(a, dialogs.NewAlertDialog())
	qp, inst := hostedSessionPanel(t, a, 1)
	block := make(chan struct{})
	t.Cleanup(func() { close(block) })
	inst.Rescript([]fakeResponse{
		{match: "USE [Sales]", block: block},
		{match: "@@TRANCOUNT", cols: 3, rows: [][]driver.Value{{int64(61), "Sales", int64(1)}}},
	})
	qp.setResult(&query.Result{Database: "master"}, false)
	oldResult := qp.result
	qp.changeDatabase("Sales")
	drainUntil(t, a, func() bool { return len(inst.Reads("USE [Sales]")) != 0 }, "USE to start")
	qp.CancelExecution()
	drainUntil(t, a, func() bool { return !qp.executing }, "cancelled USE state refresh")
	if qp.database != "Sales" || qp.result != oldResult || qp.tranCount != 1 || !qp.connected() {
		t.Fatal("cancellation ignored the authoritative database or lost previous results/session state")
	}
	if a.alertDialog().Visible() {
		t.Fatal("cancellation with a changed database was reported as an access error")
	}
}

func TestUseDatabaseMenuGuards(t *testing.T) {
	for _, reason := range []string{"running", "connecting", "disconnected", "different server", "different login", "offline", "current", "Azure SQL", "Fabric SQL", "Synapse dedicated"} {
		t.Run(reason, func(t *testing.T) {
			a := newTestApp()
			qp, inst := hostedSessionPanel(t, a, 0)
			sc := &db.ServerConn{Opts: qp.conn.Opts, Server: qp.conn.Server}
			node := &explorerNode{data: nodeData{Type: NodeDatabase, DBName: "Sales", conn: sc}}
			item := a.contextMenuItemsForNode(node)[0]
			if !item.Enabled() {
				t.Fatal("the initial database switch was not enabled")
			}
			switch reason {
			case "running":
				qp.executing = true
			case "connecting":
				qp.connectingTo = "fake"
			case "disconnected":
				a.panels.AddPanel(NewQueryPanel(a, "Disconnected"))
				a.panels.SetActiveIndex(1)
			case "different server":
				sc.Opts.Server = "other"
			case "different login":
				sc.Opts.User = "other"
			case "offline":
				node.data.IsOffline = true
			case "current":
				node.data.DBName = "master"
			case "Azure SQL":
				qp.conn.Server.Info().EngineEdition = gosmo.EngineAzureSQLDatabase
			case "Fabric SQL":
				qp.conn.Server.Info().EngineEdition = gosmo.EngineFabricSQLDatabase
			case "Synapse dedicated":
				qp.conn.Server.Info().EngineEdition = gosmo.EngineAzureSynapse
			}
			active := a.activeQueryPanel()
			conn, session, database := active.conn, active.session, active.database
			connecting, executing := active.connectingTo, active.executing
			if item.Label != "Use Database" || item.Enabled() {
				t.Fatal("ineligible database switch was enabled")
			}
			if (reason == "Azure SQL" || reason == "Fabric SQL" || reason == "Synapse dedicated") && !item.NoteWhen() {
				t.Fatal("the connection-required explanation was hidden")
			}
			item.Action()
			if a.activeQueryPanel() != active || active.conn != conn || active.session != session ||
				active.database != database || active.connectingTo != connecting || active.executing != executing {
				t.Fatal("blocked database action changed or reconnected the active query")
			}
			if len(inst.Reads("USE [")) != 0 {
				t.Fatal("ineligible menu action sent USE")
			}
		})
	}
}

// hostedSessionPanel adds a query panel connected the way connectForQueryPanel
// connects one — a pool plus a query.Session opened on it — over the scripted
// driver, with the session reporting tranCount open transactions. extra is
// appended to the script, for a COMMIT that should fail.
func hostedSessionPanel(t *testing.T, a *App, tranCount int64, extra ...fakeResponse) (*QueryPanel, *gFake) {
	t.Helper()
	return hostedSessionPanelOn(t, a, false, tranCount, extra...)
}

// hostedSessionPanelOn is hostedSessionPanel on a Fabric Warehouse when fabric
// is set (newGConnFabric).
func hostedSessionPanelOn(t *testing.T, a *App, fabric bool, tranCount int64, extra ...fakeResponse) (*QueryPanel, *gFake) {
	t.Helper()
	state := fakeResponse{match: "@@TRANCOUNT", cols: 3, rows: [][]driver.Value{{int64(61), "master", tranCount}}}
	sc, inst := newGConnFabric(t, fabric, append(slices.Clone(extra), state)...) // extras first: a COMMIT statement may mention @@TRANCOUNT
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

func sentStatement(inst *gFake, text string) bool {
	return slices.ContainsFunc(inst.Statements(), func(s string) bool { return strings.Contains(s, text) })
}

// BUG-1 step 5: closing a window whose session holds a transaction asks, as
// SSMS does, and Yes commits before the session ends — ending it silently
// would roll the work back.
func TestCloseWithOpenTransactionCommitsOnYes(t *testing.T) {
	a := newTestApp()
	qp, inst := hostedSessionPanel(t, a, 1)

	a.requestClosePanel(0)
	if !a.confirmDialog().Visible() {
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
	installDialog(a, dialogs.NewAlertDialog())
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
	if !a.alertDialog().Visible() {
		t.Error("a failed commit was not reported")
	}
}

// No open transaction, nothing unsaved: Ctrl+W closes at once, as before.
func TestCloseWithoutOpenTransactionDoesNotAsk(t *testing.T) {
	a := newTestApp()
	qp, _ := hostedSessionPanel(t, a, 0)
	qp.markSaved()

	a.requestClosePanel(0)
	if a.confirmDialog().Visible() || a.panelHosted(qp) {
		t.Fatal("asked about, or failed to close, a panel with nothing to lose")
	}
}

// Quit goes through the same prompt for every panel holding a transaction,
// and only quits once the commit is done.
func TestQuitAsksAboutOpenTransactions(t *testing.T) {
	a := newTestApp()
	qp, inst := hostedSessionPanel(t, a, 1)
	qp.markSaved()

	if a.requestQuit() {
		t.Fatal("quit without asking about an open transaction")
	}
	answer(a, 0)
	drainUntil(t, a, func() bool { return a.wake.Closed() }, "the quit to go ahead after the commit")
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

// L5: a connect that fails or is cancelled keeps the panel's previous
// connection, so it must keep that connection's database too — Reconnect,
// IntelliSense and the next Connect prompt all pair the two.
func TestFailedQueryPanelDialKeepsTheDatabase(t *testing.T) {
	for _, wanted := range []bool{true, false} {
		a := newTestApp()
		installDialog(a, dialogs.NewAlertDialog())
		qp, _ := hostedSessionPanel(t, a, 0)
		qp.closeConnection()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // the dial fails at once, without touching the network
		answered := false
		a.dialQueryPanel(ctx, qp, config.Connection{Server: "elsewhere", Database: "OtherDB"}, "",
			nil, func(error) bool { answered = true; return wanted }, nil)
		if got, want := qp.connInfoText(), "Connecting to elsewhere | OtherDB..."; got != want {
			t.Errorf("connInfoText() while dialling = %q, want %q", got, want)
		}
		drainUntil(t, a, func() bool { return answered && qp.connectingTo == "" }, "the dial to come back")
		if qp.database != "master" {
			t.Errorf("wanted = %v: database = %q after a failed dial, want the previous %q", wanted, qp.database, "master")
		}
		if got := qp.connInfoText(); !strings.HasPrefix(got, "fake |") || !strings.HasSuffix(got, "| master (disconnected)") {
			t.Errorf("wanted = %v: connInfoText() = %q, want the previous server and database", wanted, got)
		}
	}
}

// L9: a COMMIT that failed because the connection dropped cannot be retried —
// the server rolled the transaction back — so the panel goes disconnected and
// the alert says so, instead of "the transaction is still there".
func TestCommitFailedOnALostSessionDisconnectsThePanel(t *testing.T) {
	a := newTestApp()
	installDialog(a, dialogs.NewAlertDialog())
	qp, _ := hostedSessionPanel(t, a, 1,
		fakeResponse{match: "COMMIT TRANSACTION", err: errors.New("connection reset"), dropsConn: true})

	a.requestClosePanel(0)
	answer(a, 0)
	drainUntil(t, a, func() bool { return !qp.executing }, "the commit to come back")
	if !a.panelHosted(qp) {
		t.Fatal("the panel was closed after its commit failed")
	}
	if qp.connected() || qp.session != nil || qp.tranCount != 0 {
		t.Errorf("connected = %v, session = %v, tranCount = %d; want a disconnected panel",
			qp.connected(), qp.session, qp.tranCount)
	}
	if !a.alertDialog().Visible() {
		t.Fatal("a failed commit was not reported")
	}
	msg := a.alertDialog().Message()
	if !strings.Contains(msg, "rolled the transactions back") || strings.Contains(msg, "still there") {
		t.Errorf("alert = %q, want it to say the server rolled the work back", msg)
	}
}

// B4 (review 2026-10-24): Stop while a close prompt's COMMIT or ROLLBACK runs
// says it cannot be stopped, not that nothing is executing, and cancels nothing.
func TestCancelDuringEndTransactionsSaysItCannotStop(t *testing.T) {
	for _, c := range []struct {
		commit bool
		want   string
	}{
		{true, "Committing open transactions — this cannot be stopped"},
		{false, "Rolling back open transactions — this cannot be stopped"},
	} {
		a := newTestApp()
		qp, _ := hostedSessionPanel(t, a, 1)
		done := false
		qp.endTransactions(c.commit, func() { done = true })
		if qp.cancellable() {
			t.Errorf("commit=%v: cancellable() = true while ending transactions; Cancel and Stop must be disabled", c.commit)
		}
		qp.CancelExecution()
		if a.statusText != c.want {
			t.Errorf("commit=%v: status = %q, want %q", c.commit, a.statusText, c.want)
		}
		drainUntil(t, a, func() bool { return done }, "the transactions to end")
		qp.CancelExecution()
		if a.statusText != "No query is currently executing" {
			t.Errorf("commit=%v: after the end, status = %q, want no query executing", c.commit, a.statusText)
		}
	}
}

// A panic on endTransactions' goroutine clears ending with the latch, so the next
// run's Cancel cancels it rather than saying a COMMIT cannot be stopped.
func TestExecPanickedClearsEnding(t *testing.T) {
	a := newTestApp()
	qp, _ := hostedSessionPanel(t, a, 1)
	qp.executing, qp.ending = true, "Committing"
	qp.execPanicked()

	cancelled := false
	qp.executing, qp.cancel = true, func() { cancelled = true }
	if !qp.cancellable() {
		t.Error("cancellable() = false for a run after the panic, want true")
	}
	qp.CancelExecution()
	if !cancelled || a.statusText != "Cancelling query..." {
		t.Errorf("cancelled = %v, status = %q; want the run cancelled", cancelled, a.statusText)
	}
}
