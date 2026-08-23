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
	old := p.conn
	old.Close()
	p.app.connectForQueryPanel(p, old, p.database, nil)
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

// runQuery is the shared execution path for Execute. The heavy lifting — GO
// batch splitting, the USE switch, result sets, the message stream — lives in
// internal/query.
//
// In Results To File mode it asks for the destination first, then runs through
// query.ExecuteToSink, streaming rows to the file as they are scanned.
func (p *QueryPanel) runQuery(queryText string) {
	if queryText == "" {
		p.resultsNotice = "No query to execute"
		return
	}
	if !p.app.isConnected(p.conn) {
		p.resultsNotice = p.notConnectedMessage()
		p.results.SetData([]string{"Message"}, [][]string{{"No active connection"}})
		return
	}
	if p.executing {
		p.app.setStatus("A query is already executing in this panel")
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
			// opening and confirming would take startRun into sc.Server.DB() on
			// a dead connection.
			if !p.app.isConnected(p.conn) {
				p.app.setStatus(p.notConnectedMessage())
				return
			}
			p.startRun(queryText, path)
		})
		return
	}
	p.startRun(queryText, "")
}

// startRun executes queryText, clearing the results area first. exportPath is
// non-empty only for a Results To File run, which streams every row there
// instead of retaining it — bounded by the file rather than by memory.
func (p *QueryPanel) startRun(queryText, exportPath string) {
	p.clearResults()
	sc := p.conn
	// Snapshotted for the same reason as sc: setResult writes p.database back
	// from the connection once the script finishes, and connectForQueryPanel
	// writes it on the UI goroutine too, so neither may be read from the
	// goroutine below.
	database := p.database
	// Snapshot now, not read from the goroutine below: the "Include Actual
	// Execution Plan" toggle can change while this goroutine runs.
	capturePlan := p.app.actualPlanEnabled
	ctx, cancel := context.WithCancel(sc.Context())
	p.cancel = cancel
	p.resultsNotice = ""
	p.executing = true
	p.execStart = time.Now()
	p.app.setStatus("Executing query...")

	done := make(chan struct{})
	p.execDone = done
	go p.tickExecuting(done)

	p.app.safegoRepair("query execution", p.execPanicked, func() {
		// Both on every exit, not just the normal one: a panic past them leaks
		// ctx and leaves tickExecuting waking the event loop once a second for
		// the life of the process.
		defer cancel()
		defer close(done)

		var res *query.Result
		var sink *csvSink
		var exportErr error
		switch {
		case exportPath != "":
			sink, exportErr = newCSVSink(exportPath)
			if exportErr != nil {
				res = &query.Result{Messages: query.ErrorMessages(exportErr)}
				break
			}
			res = query.ExecuteToSink(ctx, sc.Server.DB(), database, queryText, sink)
			// Close after the run either way: the file has partial content and
			// the handle must not leak.
			if cerr := sink.Close(); cerr != nil && exportErr == nil {
				exportErr = cerr
			}
		case capturePlan:
			res = query.ExecuteWithPlan(ctx, sc.Server.DB(), database, queryText)
		default:
			res = query.Execute(ctx, sc.Server.DB(), database, queryText)
		}
		// cancelled must be read while ctx is still live: the deferred cancel()
		// sets ctx.Err() itself, so reading it later is always true.
		cancelled := ctx.Err() != nil
		p.app.postAndWake(func() {
			p.executing = false
			p.cancel = nil
			if !p.app.panelHosted(p) {
				// Panel was closed while the query was running, so there is
				// nothing to update — but the status bar still has to be told:
				// setResult is the only thing that normally replaces "Executing
				// query...". A file export already under way has been written
				// and closed regardless.
				p.app.setStatus(closedPanelResultStatus(p.Title(), cancelled))
				return
			}
			p.setResult(res, cancelled)
			if exportPath != "" {
				p.reportExport(res, exportPath, res.RowsWritten, exportErr)
			}
		})
	})
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
