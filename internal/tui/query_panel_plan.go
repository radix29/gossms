package tui

import (
	"context"
	"time"

	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/showplan"
)

// ShowEstimatedPlan fetches the estimated (compile-only) execution plan for
// the editor's selection, or the whole script if nothing is selected — the
// same selection-or-full-text rule as Execute.
func (p *QueryPanel) ShowEstimatedPlan() {
	if sel := p.editor.SelectedText(); sel != "" {
		p.runEstimatedPlan(sel)
		return
	}
	p.runEstimatedPlan(p.editor.Text())
}

// runEstimatedPlan is runQuery's plan-fetching counterpart: same guards,
// same p.executing/p.cancel single-flight fields (so Stop Execution and
// Cancel Executing Query also cancel an in-flight plan fetch for free), same
// postEvent/wakeEventLoop completion pattern. Uses query.ExecuteEstimatedPlan
// rather than talking to gosmo directly, so a script containing GO batch
// separators is split the same way Execute splits it — gosmo's own
// EstimatedPlanContext takes one statement at a time and would otherwise
// reject any multi-batch script with a syntax error on "GO" itself.
func (p *QueryPanel) runEstimatedPlan(queryText string) {
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
	// Snapshot now — read from the background goroutine below, not p.conn/
	// p.database, which could change while it's running.
	sc := p.conn
	database := p.database
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel
	p.resultsNotice = ""
	p.executing = true
	p.execStart = time.Now()
	p.app.setStatus("Fetching estimated execution plan...")

	done := make(chan struct{})
	go p.tickExecuting(done)

	go func() {
		res := query.ExecuteEstimatedPlan(ctx, sc.Server.DB(), database, queryText)
		// cancelled must be read before cancel() — calling cancel sets
		// ctx.Err() itself, which would make this always true otherwise.
		cancelled := ctx.Err() != nil
		cancel()
		close(done)
		p.app.postEvent(func() {
			p.executing = false
			p.cancel = nil
			if !p.app.panelHosted(p) {
				return
			}
			p.setEstimatedPlan(res, cancelled)
		})
		p.app.wakeEventLoop()
	}()
}

// setEstimatedPlan installs a finished plan fetch. On success, the plan
// replaces Results/Messages entirely — Estimated mode never runs the query
// for real, so p.result stays nil and there's nothing else to show (see
// planTabActive/resultTabs, which key off p.result == nil for this case).
// Like setResult's own res/cancelled split, a plan that did come back is
// still installed and shown even if cancelled happens to be true — the
// fetch can race a cancel signal and still succeed.
//
// On any failure (a SQL error, an empty or unparseable plan, or a genuine
// cancellation), res itself becomes p.result instead — with a Messages
// entry explaining why — which resultTabs naturally reduces to a single
// "Messages" tab, the same fallback a normal Execute failure gets from
// setResult. This also clears p.planView, so a previous run's plan can't
// stay browsable next to an unrelated new failure's Messages — the tab
// would otherwise still say "Execution Plan" and show stale (or, on the
// very first-ever failure, simply empty) content instead of nothing to
// show at all.
func (p *QueryPanel) setEstimatedPlan(res *query.Result, cancelled bool) {
	p.result = nil // mutual exclusion — see setResult's p.planView = nil

	fetchFailed := res.HasErrors()
	if !fetchFailed && len(res.PlanXML) == 0 {
		res.Messages = append(res.Messages, query.Message{Text: "No execution plan was returned.", IsError: true})
		fetchFailed = true
	}

	// showMessages installs res as the Messages-only fallback described
	// above — shared by the fetchFailed case and a parse failure below.
	showMessages := func() {
		if cancelled {
			res.Messages = []query.Message{{Text: "Query was cancelled by user.", IsError: true}}
		}
		p.planView = nil
		p.result = res
		p.setMessages(res.Messages)
		p.activeTab = p.messagesTabIndex()
	}

	var parseErr error
	switch {
	case fetchFailed:
		showMessages()
	default:
		plan, err := showplan.ParseAll(res.PlanXML)
		if err != nil {
			parseErr = err
			res.Messages = append(res.Messages, query.ErrorMessages(err)...)
			showMessages()
		} else {
			if p.planView == nil {
				p.planView = p.newPlanView()
			}
			p.planView.SetPlan(plan)
			p.setMessages(nil) // clear any stale Messages from an earlier failed run
			p.activeTab = 0
		}
	}
	p.layoutChildren()
	p.syncFocusVisuals()

	switch {
	case cancelled:
		p.app.setStatus("Query cancelled")
	case fetchFailed:
		p.app.setStatus("Could not display estimated execution plan — see Messages")
	case parseErr != nil:
		p.app.setStatus("Could not parse execution plan — see Messages")
	default:
		p.app.setStatus("Estimated execution plan displayed")
	}
}
