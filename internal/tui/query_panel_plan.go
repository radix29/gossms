package tui

import (
	"context"

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

// runEstimatedPlan is runQuery's plan-fetching counterpart, through the same
// runRefused guards and launch — so Stop Execution and Cancel Executing Query
// cancel an in-flight plan fetch for free, and a panel closed mid-fetch still
// has its status line cleared. Uses query.Session.ExecuteEstimatedPlan rather
// than talking to gosmo directly, so a script containing GO batch separators
// is split the same way Execute splits it — gosmo's own EstimatedPlanContext
// takes one statement at a time and would otherwise reject any multi-batch
// script with a syntax error on "GO" itself.
func (p *QueryPanel) runEstimatedPlan(queryText string) {
	if p.runRefused(queryText) {
		return
	}
	// No rows are scanned by an estimated plan, so the status line's row
	// counter stays off for this run.
	p.launch("the estimated execution plan", "Fetching estimated execution plan...", nil,
		func(ctx context.Context, sess *query.Session) *query.Result {
			return sess.ExecuteEstimatedPlan(ctx, queryText)
		},
		p.setEstimatedPlan)
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
// entry explaining why — which resultTabs reduces to a single "Messages"
// tab, the same fallback setResult gives a normal Execute failure. It also
// clears p.planView, so a previous run's plan can't stay browsable next to
// an unrelated new failure's Messages.
func (p *QueryPanel) setEstimatedPlan(res *query.Result, cancelled bool) {
	p.result = nil // Estimated mode has no result; setResultPlan clears the other way

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
