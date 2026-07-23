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
// was last connected with (whatever database it's currently in, not
// necessarily the connection's original default — see connectForQueryPanel),
// replacing whatever connection it currently holds. This is the query
// window's escape hatch for a connection silently dropped out from under it
// — an idle firewall/NAT timeout, the server killing the session, a
// failover — distinct from File > Disconnect, which the user only reaches
// via Object Explorer and which this panel doesn't share a connection with
// anyway (see connectForQueryPanel's own doc comment). A no-op if this
// panel was never connected to begin with, since there's no Opts to
// reconnect with.
//
// p.conn is deliberately left as the (now-closed) old connection rather than
// nilled out here: connectForQueryPanel only reads its Opts, and if the
// redial itself fails, leaving p.conn non-nil keeps this the exact same
// state as any other query window with a dropped connection — isConnected
// still (correctly) reports false, but the Reconnect menu item stays
// enabled and its Opts stay around for the user to simply try again,
// instead of the panel getting permanently stuck with no Opts to redial.
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

// runQuery is the shared execution path for Execute. The heavy lifting —
// GO batch splitting, the USE database switch, result sets, and the
// message stream — lives in internal/query.
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
	p.messages.SetText("") // clear stale messages from any previous run
	p.messageErrorLines = nil
	sc := p.conn
	// Results To File wants every row a query actually returns, not just
	// what the grid would show — captured now (like sc above) since
	// p.resultsMode can change via the Query menu while this goroutine runs.
	maxRows := p.app.cfg.MaxResultRows
	if p.resultsMode == ResultsModeFile {
		maxRows = 0
	}
	// Snapshot now, not read from the goroutine below — the "Include Actual
	// Execution Plan" toggle can change via the toolbar/Query menu while
	// this goroutine runs.
	capturePlan := p.app.actualPlanEnabled
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.resultsNotice = ""
	p.executing = true
	p.execStart = time.Now()
	p.app.setStatus("Executing query...")

	done := make(chan struct{})
	go p.tickExecuting(done)

	go func() {
		var res *query.Result
		if capturePlan {
			res = query.ExecuteWithPlan(ctx, sc.Server.DB(), p.database, queryText, maxRows)
		} else {
			res = query.Execute(ctx, sc.Server.DB(), p.database, queryText, maxRows)
		}
		// cancelled must be read before cancel() — calling cancel sets
		// ctx.Err() itself, which would make this always true otherwise.
		cancelled := ctx.Err() != nil
		cancel() // release ctx's resources now that the query is done, whether or not CancelExecution ever ran
		close(done)
		p.app.postEvent(func() {
			p.executing = false
			p.cancel = nil
			if !p.app.panelHosted(p) {
				// Panel was closed while the query was still running —
				// nothing left to update, and in Results To File mode
				// setResult would otherwise pop the save dialog for a panel
				// that no longer exists.
				return
			}
			p.setResult(res, cancelled)
		})
		p.app.wakeEventLoop()
	}()
}

// tickExecuting wakes the event loop once a second while a query runs, so
// updateResultsStatus's live elapsed-time counter visibly ticks instead of
// only updating once the query finishes. Exits as soon as done closes.
func (p *QueryPanel) tickExecuting(done chan struct{}) {
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
	p.result = res
	p.setResultPlan(res)
	p.activeTab = 0
	if len(res.Sets) == 0 || res.HasErrors() {
		p.activeTab = p.messagesTabIndex() // wherever it now sits
	}
	p.layoutChildren()

	if p.resultsMode == ResultsModeFile && len(res.Sets) > 0 {
		p.promptWriteResults(res)
	}
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
