package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tui/planview"
)

// Execute runs the query against the connected server: the selected text if the
// editor has a selection, otherwise the whole content. Query > Execute and F5
// both call it.
func (p *QueryPanel) Execute() {
	if sel := p.editor.SelectedText(); sel != "" {
		p.runQuery(sel)
		return
	}
	p.runQuery(p.editor.Text())
}

// ExecuteSelection runs only the editor's selected text, setting a status
// message and nothing else when there is no selection — the toolbar's "Execute
// Selection" button, unlike Execute, which falls back to the whole script.
func (p *QueryPanel) ExecuteSelection() {
	if sel := p.editor.SelectedText(); sel != "" {
		p.runQuery(sel)
		return
	}
	p.app.setStatus("No selection to execute")
}

// CancelExecution cancels the in-flight query, if one is running.
func (p *QueryPanel) CancelExecution() {
	if p.executing && p.cancel != nil {
		p.cancel()
		p.app.setStatus("Cancelling query...")
	} else {
		p.app.setStatus("No query is currently executing")
	}
}

// Reconnect re-dials this panel's connection with the same server and login, in
// whatever database it is currently in (see connectForQueryPanel) — the escape
// hatch for a connection dropped out from under the panel by an idle timeout, a
// killed session or a failover. A no-op if the panel was never connected, since
// there are no Opts to redial with.
//
// The new connection brings a new session, so everything the old one held —
// temp tables, SET options, an open transaction — is gone, and the status bar
// says so. An open transaction is offered for commit first, as closing the
// panel does; nothing is ever re-run on the new session.
//
// p.conn is left as the now-closed old connection rather than nilled:
// connectForQueryPanel reads only its Opts, and keeping it non-nil after a
// failed redial leaves the panel like any other with a dropped connection —
// isConnected false, Reconnect enabled, Opts still there to try again.
func (p *QueryPanel) Reconnect() {
	if p.conn == nil {
		p.app.setStatus("Nothing to reconnect — this query window was never connected")
		return
	}
	if p.executing {
		p.app.setStatus("Cannot reconnect while a query is executing")
		return
	}
	if p.connectingTo != "" {
		p.app.setStatus(p.notConnectedMessage())
		return
	}
	p.app.confirmOpenTransactions(p, "reconnecting", func() {
		old := p.conn
		p.closeConnection()
		p.app.connectForQueryPanel(p, old, p.database, func() {
			p.app.setStatus(fmt.Sprintf("Reconnected to %s — new session (SPID %d); the previous session's temp tables, SET options and transactions are gone",
				old.Opts.Server, p.session.SPID()))
		})
	})
}

// connected reports whether the panel can run anything: an open connection
// and the session taken from it. connectForQueryPanel sets both and
// closeConnection clears both, so either answers alone — but only the pair is
// what a run needs.
func (p *QueryPanel) connected() bool {
	return p.app.isConnected(p.conn) && p.session != nil
}

// closeConnection ends the panel's session and closes its connection, for a
// panel closing, reconnecting or finding its session lost. The session is
// closed off the UI goroutine: query.Session.Close waits for a run still
// unwinding from a cancel. Closing p.conn cancels that run's context, which
// derives from it.
func (p *QueryPanel) closeConnection() {
	if s := p.session; s != nil {
		p.session = nil
		p.app.safego("closing a query session", s.Close)
	}
	if p.conn != nil {
		p.conn.Close()
	}
	p.tranCount = 0
}

// endTransactionsTimeout bounds the COMMIT or ROLLBACK a close/reconnect/quit
// prompt issues.
const endTransactionsTimeout = 30 * time.Second

// endTransactions commits or rolls back the session's open transactions in
// the background, then runs then on the UI goroutine — unless a commit
// failed, which leaves the panel open with the transaction and an alert
// saying why, since then would close the session and roll back the very work
// the user asked to keep. A rollback that fails goes on to then regardless:
// ending the session rolls the transaction back anyway.
//
// The run latch is held throughout, so nothing else reaches the session.
func (p *QueryPanel) endTransactions(commit bool, then func()) {
	sess := p.session
	ctx, cancel := context.WithTimeout(p.conn.Context(), endTransactionsTimeout)
	verb := "Rolling back"
	if commit {
		verb = "Committing"
	}
	p.executing = true
	p.execStart = time.Now()
	p.app.setStatus(fmt.Sprintf("%s open transactions in %s...", verb, p.Title()))
	p.app.safegoRepair("ending a query window's transactions", p.execPanicked, func() {
		defer cancel()
		err := sess.EndTransactions(ctx, commit)
		p.app.postAndWake(func() {
			p.executing = false
			if err != nil && commit {
				p.app.setStatus("Commit failed — " + p.Title() + " was left open")
				p.app.alertDialog.ShowAlert("Commit Failed",
					fmt.Sprintf("The open transactions in %s could not be committed: %v\n\nThe window was left open; the transaction is still there to deal with.", p.Title(), err))
				return
			}
			p.tranCount = 0
			done := "Rolled back"
			if commit {
				done = "Committed"
			}
			p.app.setStatus(fmt.Sprintf("%s the open transactions in %s", done, p.Title()))
			then()
		})
	})
}

// clearResults empties the results area before a new run, so the previous run's
// grid, tabs, messages and plan don't sit there looking current. setResult
// repopulates it when the run finishes.
func (p *QueryPanel) clearResults() {
	p.result = nil
	p.planView = nil
	p.activeTab = 0
	p.results.SetData(nil, nil)
	p.resultsText.SetText("")
	p.messages.SetText("")
	p.messageErrorLines = nil
	p.layoutChildren() // the tab bar's row goes back to the results area
}

// runQuery is the shared execution path for Execute and Execute Selection. The
// heavy lifting — GO batch splitting, result sets, the message stream — lives
// in internal/query.
//
// In Results To File mode it asks for the destination first, then runs through
// query.Session.ExecuteToSink, streaming rows to the file as they are scanned.
func (p *QueryPanel) runQuery(queryText string) {
	if p.runRefused(queryText) {
		return
	}
	// Snapshotted here rather than read inside the closures below, since the
	// Query menu can switch modes while the save dialog is open or the query is
	// running. See QueryPanel.runMode.
	p.runMode = p.resultsMode

	if p.runMode == ResultsModeFile {
		// The destination must exist before the first row is scanned, so the
		// prompt comes first and the run starts from its callback. Cancelling
		// runs nothing, and the panel keeps its previous results.
		p.promptResultsFile(func(path string) {
			if p.executing {
				p.app.setStatus("A query is already executing in this panel")
				return
			}
			// Re-checked rather than carried over: the save dialog is modal but
			// the connection isn't frozen behind it, and a disconnect between
			// opening and confirming would start a run on a session that is
			// already gone.
			if !p.connected() {
				p.app.setStatus(p.notConnectedMessage())
				return
			}
			p.startRun(queryText, path)
		})
		return
	}
	p.startRun(queryText, "")
}

// runRefused applies the checks every run entry point opens with — something
// to run, a connection to run it on, no run already in flight — reporting the
// first that fails and whether one did.
func (p *QueryPanel) runRefused(queryText string) bool {
	switch {
	case queryText == "":
		p.resultsNotice = "No query to execute"
	case !p.connected():
		p.resultsNotice = p.notConnectedMessage()
		if p.connectingTo == "" {
			p.results.SetData([]string{"Message"}, [][]string{{"No active connection"}})
		}
	case p.executing:
		p.app.setStatus("A query is already executing in this panel")
	default:
		return false
	}
	return true
}

// startRun executes queryText, clearing the results area first. exportPath is
// non-empty only for a Results To File run, which streams every row there
// instead of retaining it — bounded by the file rather than by memory.
func (p *QueryPanel) startRun(queryText, exportPath string) {
	p.clearResults()
	// Snapshot now, not read from the goroutine below: the "Include Actual
	// Execution Plan" toggle can change while this goroutine runs.
	capturePlan := p.app.actualPlanEnabled
	prog := &query.Progress{}

	// Written by run on the background goroutine, read by the completion on
	// the UI goroutine; launch's postAndWake orders the two.
	var exportErr error
	run := func(ctx context.Context, sess *query.Session) *query.Result {
		switch {
		case exportPath != "":
			sink, err := newCSVSink(exportPath)
			if err != nil {
				exportErr = err
				return &query.Result{Messages: query.ErrorMessages(err)}
			}
			res := sess.ExecuteToSink(ctx, queryText, sink, query.WithProgress(prog))
			// Close after the run either way: the file has partial content and
			// the handle must not leak.
			exportErr = sink.Close()
			return res
		case capturePlan:
			return sess.ExecuteWithPlan(ctx, queryText, query.WithProgress(prog))
		default:
			return sess.Execute(ctx, queryText, query.WithProgress(prog))
		}
	}
	p.launch("query execution", "Executing query...", prog, run, func(res *query.Result, cancelled bool) {
		p.setResult(res, cancelled)
		if exportPath != "" {
			p.reportExport(res, exportPath, res.RowsWritten, exportErr)
		}
	})
}

// launch starts run on the panel's session in the background: the one
// run-start path Execute, Results To File and the estimated plan share. It
// holds the single-flight latch, the cancel func Stop Execution reaches and the
// elapsed-time ticker for the run's duration, repairs all three after a panic,
// and, once run returns, takes in what the run says about the session before
// finish installs the result on the UI goroutine.
//
// finish runs only for a panel still hosted. A closed one gets the status bar
// told instead — finish is what normally replaces the "Executing..." status.
// prog is the run's live row counter, nil for a run that scans no rows.
func (p *QueryPanel) launch(what, status string, prog *query.Progress,
	run func(context.Context, *query.Session) *query.Result,
	finish func(res *query.Result, cancelled bool)) {
	// Snapshotted: closeConnection and connectForQueryPanel replace both on
	// the UI goroutine while the run is in flight.
	sess := p.session
	ctx, cancel := context.WithCancel(p.conn.Context())
	p.cancel = cancel
	p.resultsNotice = ""
	p.executing = true
	p.execStart = time.Now()
	p.progress = prog
	p.app.setStatus(status)

	done := make(chan struct{})
	p.execDone = done
	go p.tickExecuting(done)

	p.app.safegoRepair(what, p.execPanicked, func() {
		// Both on every exit, not just the normal one: a panic past them leaks
		// ctx and leaves tickExecuting waking the event loop once a second for
		// the life of the process.
		defer cancel()
		defer close(done)

		res := run(ctx, sess)
		// cancelled must be read while ctx is still live: the deferred cancel()
		// sets ctx.Err() itself, so reading it later is always true.
		cancelled := ctx.Err() != nil
		p.app.postAndWake(func() {
			p.executing = false
			p.cancel = nil
			p.progress = nil
			if !p.app.panelHosted(p) {
				// A file export already under way has been written and closed
				// regardless.
				p.app.setStatus(closedPanelResultStatus(p.Title(), cancelled))
				return
			}
			lost := p.noteSessionState(res)
			finish(res, cancelled)
			if lost {
				p.app.setStatus("Connection lost — the session's state is gone; use Query > Reconnect")
			}
		})
	})
}

// sessionLostMessage closes the Messages pane of a run that lost its session.
const sessionLostMessage = "The connection to the server was lost. This session's temp tables, " +
	"SET options and any open transaction are gone; use Query > Reconnect to start a new session."

// noteSessionState takes in what a finished run says about the session,
// reporting whether the session was lost. A lost session takes the panel's
// connection with it, so the panel shows as disconnected and Query > Reconnect
// is the way on — never a silent re-dial, which would carry on as if the temp
// tables and the transaction were still there.
func (p *QueryPanel) noteSessionState(res *query.Result) (lost bool) {
	if res.SessionLost {
		p.closeConnection()
		res.Messages = append(res.Messages, query.Message{Text: sessionLostMessage, IsError: true})
		return true
	}
	if res.State != nil {
		p.tranCount = res.State.TranCount
	}
	return false
}

// closedPanelResultStatus is what the status bar says once a query whose panel
// was closed mid-flight returns. closePanelAt cancels the context on the way
// out, so this is normally the cancelled wording.
func closedPanelResultStatus(title string, cancelled bool) string {
	if cancelled {
		return fmt.Sprintf("%s was closed — its query was cancelled", title)
	}
	return fmt.Sprintf("%s was closed — its query finished, results discarded", title)
}

// execPanicked releases the single-flight latch after a panic on an execute or
// estimated-plan goroutine — the App.safegoRepair step for both. Without it
// p.executing stays set for the panel's lifetime and every later Execute is
// refused. No seq guard is needed, unlike LogViewer.readPanicked: p.executing is
// itself what stops a second run starting.
func (p *QueryPanel) execPanicked() {
	p.executing = false
	p.cancel = nil
	p.progress = nil
	p.resultsNotice = "Execution stopped unexpectedly — see the log for details."
}

// tickExecuting wakes the event loop once a second while a query runs, so
// updateResultsStatus's elapsed-time counter visibly ticks. Exits as soon as
// done closes.
func (p *QueryPanel) tickExecuting(done chan struct{}) {
	defer p.app.recoverPanic("the query elapsed-time timer")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			p.app.wakeEventLoop()
		}
	}
}

// setResult installs a finished execution: picks the initial tab — the first
// grid, or Messages when there are no grids or the run had errors, as SSMS does
// — makes room for the tab bar, and renders.
func (p *QueryPanel) setResult(res *query.Result, cancelled bool) {
	// A mid-script "USE otherdb" changes the session's database out from under
	// p.database. res.Database, read off the same connection right after the
	// script ran, is the source of truth from here on, so the connection-info bar
	// and the next Execute's own USE stay in sync with it.
	if res.Database != "" {
		p.database = res.Database
	}
	// Folded into res.Messages once here rather than at render time: the Messages
	// tab is re-rendered on every tab switch, so appending there would repeat the
	// block on each visit.
	if p.app.metaEnabled {
		if meta := columnMetaMessages(res.Sets); len(meta) > 0 {
			if len(res.Messages) > 0 {
				res.Messages = append(res.Messages, query.Message{Text: ""})
			}
			res.Messages = append(res.Messages, meta...)
		}
	}
	p.result = res
	p.setResultPlan(res)
	p.activeTab = 0
	if len(res.Sets) == 0 || res.HasErrors() {
		p.activeTab = p.messagesTabIndex() // wherever it now sits
	}
	p.layoutChildren()

	p.renderActiveTab()

	elapsed := res.Elapsed.Round(time.Millisecond)
	switch {
	case cancelled:
		p.app.setStatus("Query cancelled")
	case res.HasErrors():
		p.app.setStatus(fmt.Sprintf("Query completed with errors in %v — see Messages", elapsed))
	default:
		p.app.setStatus(fmt.Sprintf("Query completed in %v — %d row(s), %d message(s)",
			elapsed, res.TotalRows(), len(res.Messages)))
	}
}

// newPlanView builds a PlanView wired into this panel's status bar and its
// Execution Plan tab's "[ Expand ]" button, shared by setResultPlan and
// setEstimatedPlan.
func (p *QueryPanel) newPlanView() *planview.PlanView {
	v := planview.New()
	v.OnStatus = func(msg string) { p.app.setStatus(msg) }
	v.OnCopyRequest = p.app.copyWithStatus
	v.OnMissingIndex = func(script string) { p.app.openQueryWithText(p.conn, p.database, script) }
	v.OnExpand = func() {
		if plan := v.Plan(); plan != nil {
			p.app.openPlanPanel("Execution Plan — "+p.Title(), plan)
		}
	}
	return v
}

// setResultPlan installs or clears the Execution Plan tab that rides alongside a
// normal Execute when "Include Actual Execution Plan" was on. Unlike
// setEstimatedPlan, which replaces Results/Messages entirely because it never
// runs the query, this tab sits alongside res's own Results tabs.
//
// res.PlanXML holds one complete document per statement — SET STATISTICS XML ON
// appends a showplan result set after each statement, unlike SHOWPLAN_XML ON's
// single combined document — so they are merged with showplan.ParseAll into one
// Plan. PlanView's statement selector is what steps through them.
func (p *QueryPanel) setResultPlan(res *query.Result) {
	if len(res.PlanXML) == 0 {
		p.planView = nil
		return
	}
	plan, err := showplan.ParseAll(res.PlanXML)
	if err != nil {
		p.planView = nil
		res.Messages = append(res.Messages, query.ErrorMessages(err)...)
		return
	}
	if p.planView == nil {
		p.planView = p.newPlanView()
	}
	p.planView.SetPlan(plan)
}
