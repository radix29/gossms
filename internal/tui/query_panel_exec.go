package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tui/planview"
)

// Execute runs the query against the connected server. If the editor has
// an active text selection, only the selected text is run; otherwise the
// full editor content is run. This is what both the Query > Execute menu
// item and F5 call.
func (p *QueryPanel) Execute() {
	if sel := p.editor.SelectedText(); sel != "" {
		p.runQuery(sel)
		return
	}
	p.runQuery(p.editor.Text())
}

// ExecuteSelection runs only the editor's selected text, doing nothing but
// setting a status message if there is no active selection — the
// toolbar's dedicated "Execute Selection" button, as distinct from
// Execute, which falls back to running the whole script.
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

// Reconnect re-dials this panel's connection using the same server/login it
// was last connected with (in whatever database it's currently in, not
// necessarily the connection's original default — see connectForQueryPanel),
// replacing whatever connection it holds. This is the query window's escape
// hatch for a connection dropped out from under it: an idle firewall/NAT
// timeout, the server killing the session, a failover. A no-op if the panel
// was never connected, since there's no Opts to redial with.
//
// p.conn is deliberately left as the now-closed old connection rather than
// nilled out: connectForQueryPanel only reads its Opts, and if the redial
// fails, keeping p.conn non-nil leaves the panel in the same state as any
// other with a dropped connection — isConnected still reports false, but
// Reconnect stays enabled and its Opts stay around to try again.
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

// clearResults empties the results area before a new run starts, so the
// previous run's grid, tabs, messages and plan don't sit there looking
// current for however long the query takes. setResult repopulates all of
// it when the run finishes.
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

// runQuery is the shared execution path for Execute. The heavy lifting —
// GO batch splitting, the USE database switch, result sets, and the
// message stream — lives in internal/query.
//
// In Results To File mode it asks for the destination first and then runs
// through query.ExecuteToSink, streaming rows to the file as they are
// scanned; see startRun.
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
	// Query menu can switch modes while the save dialog is open or the query
	// is running. Every decision that has to agree with how this run was
	// executed reads the snapshot. See QueryPanel.runMode.
	p.runMode = p.resultsMode

	if p.runMode == ResultsModeFile {
		// The destination has to exist before the first row is scanned, so
		// the prompt comes first and the run starts from its callback.
		// Cancelling the dialog runs nothing — the panel keeps its previous
		// results rather than being cleared for a run that never happened.
		p.promptResultsFile(func(path string) {
			if p.executing {
				p.app.setStatus("A query is already executing in this panel")
				return
			}
			// Re-checked, not just carried over from above: the save dialog is
			// modal but the connection isn't frozen behind it — a server
			// disconnect (or Reconnect failing) between opening the dialog and
			// confirming it would otherwise take startRun into
			// sc.Server.DB() on a dead connection.
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
// instead of retaining it (see csvSink) — so that path is bounded by the file
// rather than by memory.
func (p *QueryPanel) startRun(queryText, exportPath string) {
	p.clearResults()
	sc := p.conn
	// Snapshotted for the same reason as sc: setResult writes p.database
	// back from the connection once the script finishes (a mid-script USE),
	// and connectForQueryPanel writes it on the UI goroutine too — neither
	// may be read from the goroutine below. Mirrors runEstimatedPlan.
	database := p.database
	// Snapshot now, not read from the goroutine below — the "Include Actual
	// Execution Plan" toggle can change via the toolbar/Query menu while
	// this goroutine runs.
	capturePlan := p.app.actualPlanEnabled
	ctx, cancel := context.WithCancel(sc.Context())
	p.cancel = cancel
	p.resultsNotice = ""
	p.executing = true
	p.execStart = time.Now()
	p.app.setStatus("Executing query...")

	done := make(chan struct{})
	go p.tickExecuting(done)

	go func() {
		defer p.app.recoverPanic("query execution")
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
			// Close after the run, whether or not it succeeded: the file has
			// partial content either way and the handle must not leak.
			if cerr := sink.Close(); cerr != nil && exportErr == nil {
				exportErr = cerr
			}
		case capturePlan:
			res = query.ExecuteWithPlan(ctx, sc.Server.DB(), database, queryText)
		default:
			res = query.Execute(ctx, sc.Server.DB(), database, queryText)
		}
		// cancelled must be read before cancel() — calling cancel sets
		// ctx.Err() itself, which would make this always true otherwise.
		cancelled := ctx.Err() != nil
		cancel() // release ctx's resources now that the query is done, whether or not CancelExecution ever ran
		close(done)
		p.app.postAndWake(func() {
			p.executing = false
			p.cancel = nil
			if !p.app.panelHosted(p) {
				// Panel was closed while the query was still running —
				// nothing left to update. The status bar still has to be told,
				// though: setResult is the only thing that normally replaces
				// "Executing query...", so returning without one left it
				// pinned there indefinitely. A file export that was already
				// under way has been written and closed regardless.
				p.app.setStatus(closedPanelResultStatus(p.Title(), cancelled))
				return
			}
			p.setResult(res, cancelled)
			if exportPath != "" {
				p.reportExport(res, exportPath, res.RowsWritten, exportErr)
			}
		})
	}()
}

// closedPanelResultStatus is what the status bar says once a query whose
// panel was closed mid-flight finally returns. closePanelAt cancels the
// context on the way out, so this is normally the cancelled wording; a
// query that had already finished by then reports plainly instead.
func closedPanelResultStatus(title string, cancelled bool) string {
	if cancelled {
		return fmt.Sprintf("%s was closed — its query was cancelled", title)
	}
	return fmt.Sprintf("%s was closed — its query finished, results discarded", title)
}

// tickExecuting wakes the event loop once a second while a query runs, so
// updateResultsStatus's live elapsed-time counter visibly ticks instead of
// only updating once the query finishes. Exits as soon as done closes.
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

// setResult installs a finished execution: picks the initial tab (first
// grid, or Messages when there are no grids or the run had errors — same
// as SSMS), makes room for the tab bar, and renders.
func (p *QueryPanel) setResult(res *query.Result, cancelled bool) {
	// A mid-script "USE otherdb" changes the session's database out from
	// under p.database — res.Database (read off the same connection right
	// after the script ran; see query.Execute) is the source of truth from
	// here on, so the connection-info bar and the next Execute's own USE
	// stay in sync with it instead of the stale value from before this run.
	if res.Database != "" {
		p.database = res.Database
	}
	// Folded into res.Messages once, here, rather than at render time: the
	// Messages tab is re-rendered on every tab switch (renderActiveTab), so
	// appending there would repeat the block on each visit.
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
// Execution Plan tab's "[ Expand ]" button — shared by setResultPlan
// (Actual mode) and setEstimatedPlan (Estimated mode) so both get the same
// behavior from one place.
func (p *QueryPanel) newPlanView() *planview.PlanView {
	v := planview.New()
	v.OnStatus = func(msg string) { p.app.setStatus(msg) }
	v.OnCopyRequest = p.app.copyWithStatus
	v.OnExpand = func() {
		if plan := v.Plan(); plan != nil {
			p.app.openPlanPanel("Execution Plan — "+p.Title(), plan)
		}
	}
	return v
}

// setResultPlan installs or clears the Execution Plan tab that rides
// alongside a normal Execute when the "Include Actual Execution Plan"
// toggle was on (see App.actualPlanEnabled and runQuery). Unlike
// setEstimatedPlan, which replaces Results/Messages entirely since it
// never runs the query for real, this tab sits alongside res's own
// Results tabs — resultTabs checks p.result and p.planView independently
// now, not exclusively.
//
// res.PlanXML holds one complete document per statement (SET STATISTICS
// XML ON appends a separate showplan result set after each statement,
// unlike SHOWPLAN_XML ON's single combined document — see Result.PlanXML),
// so they're merged with showplan.ParseAll into one Plan and handed to
// PlanView as a whole; PlanView's own statement selector ("Statement N/M")
// is what lets the user step through all of them, the same as it already
// does for a multi-statement estimated plan.
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
