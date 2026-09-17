package tui

import (
	"context"
	"fmt"

	"github.com/radix29/gossms/internal/showplan"
)

// query_store_panel_plans.go is what the plan pane's selection can do: force
// and unforce, show one plan, compare two, and script the force. The plan
// pane's own read is in query_store_panel_load.go.

// setPlanForced forces or unforces the selected plan, after confirming.
// Forcing a plan changes what every future execution of that query does, on a
// live database — which is why it asks, and why the question names both the
// query and the plan rather than "the selected row".
func (p *QueryStorePanel) setPlanForced(force bool) {
	plan := p.selectedPlan()
	if plan == nil || !p.app.requireConn(p.conn) {
		return
	}
	verb, doing := "Unforce", "Unforcing"
	if force {
		verb, doing = "Force", "Forcing"
	}
	sc, dbName, queryID, planID := p.conn, p.dbName, plan.QueryID, plan.PlanID
	// Latched before the question, not in the answer: busy is what stops a
	// read starting underneath the write, and the confirm dialog doesn't stop
	// F5 reaching the panel — a Load begun while the question was up would
	// clear busy from under the write it knows nothing about.
	p.busy = true
	p.app.confirmDialog.ShowConfirm(verb+" Plan", qsForceMessage(force, dbName, queryID, planID), func(confirmed bool) {
		if !confirmed {
			p.busy = false
			return
		}
		p.setStatus(fmt.Sprintf("%s plan %d for query %d...", doing, planID, queryID))
		// The job's repair for the same reason Load uses safegoRepair: busy is
		// cleared in the completion, which a panic never reaches.
		p.app.runWithProgress(progressJob{
			title:   verb + " Plan",
			message: fmt.Sprintf("%s plan %d for query %d in %q...", doing, planID, queryID, dbName),
			what:    "forcing a Query Store plan",
			sc:      sc,
			timeout: qsReadTimeout,
			repair:  func() { p.forcePanicked(verb) },
		}, func(ctx context.Context, _ progressReport) error {
			d := sc.Server.Database(dbName)
			if force {
				return d.QueryStoreForcePlanContext(ctx, queryID, planID)
			}
			return d.QueryStoreUnforcePlanContext(ctx, queryID, planID)
		}, func(err error, cancelled bool) {
			p.busy = false
			switch {
			case cancelled:
				// The app's status line, not the grid's: Refresh below
				// rewrites the grid's.
				p.app.setStatus(fmt.Sprintf("%s plan %d for query %d cancelled", doing, planID, queryID))
			case err != nil:
				p.setStatus(fmt.Sprintf("%s failed: %v", verb, withPermissionAdvice(err)))
				return
			default:
				p.app.setStatus(fmt.Sprintf("Plan %d %sd for query %d", planID, verb, queryID))
			}
			// Both panes are stale: the plan's own IsForced changed, and the
			// report's Forced Plan column with it — or may have, after a
			// cancel. Through Refresh, so the user is left on the query they
			// just acted on rather than back at the top of the report.
			p.Refresh()
		})
	})
}

// forcePanicked releases the busy latch after a panic on the write goroutine —
// setPlanForced's safegoRepair step. No seq guard, unlike readPanicked: busy
// was held across the whole write, so nothing else can have started.
func (p *QueryStorePanel) forcePanicked(verb string) {
	p.busy = false
	p.setStatus(verb + " stopped unexpectedly — see the log for details")
}

// qsForceMessage is the confirmation question. Forcing names what it costs:
// the plan stops being re-chosen, and a plan that can no longer be produced
// falls back silently rather than failing.
func qsForceMessage(force bool, dbName string, queryID, planID int64) string {
	if !force {
		return fmt.Sprintf("Stop forcing plan %d for query %d in %s?\n\n"+
			"The optimizer chooses a plan for this query again on its next execution.",
			planID, queryID, dbName)
	}
	return fmt.Sprintf("Force plan %d for query %d in %s?\n\n"+
		"Every future execution of this query uses this plan instead of one the "+
		"optimizer chooses. If the plan can no longer be produced, SQL Server "+
		"compiles normally and counts a forcing failure rather than failing the query.",
		planID, queryID, dbName)
}

// showPlan opens the selected plan in its own PlanPanel — the same detached
// window the Execution Plan tab's Expand button opens.
func (p *QueryStorePanel) showPlan() {
	plan := p.selectedPlan()
	if plan == nil {
		return
	}
	if plan.QueryPlanXML == "" {
		p.setStatus(fmt.Sprintf("Query Store holds no plan XML for plan %d", plan.PlanID))
		return
	}
	parsed, err := showplan.Parse([]byte(plan.QueryPlanXML))
	if err != nil {
		p.setStatus(fmt.Sprintf("Plan %d could not be read: %v", plan.PlanID, err))
		return
	}
	p.app.openPlanPanel(fmt.Sprintf("Plan %d — query %d (%s)", plan.PlanID, plan.QueryID, p.dbName), parsed)
}

// comparePlans marks a plan on the first press and compares the second against
// it — the two plans of one query a comparison is about are two rows of the
// same pane, and there is nowhere to select both at once.
//
// Pressing it again on the marked plan clears the mark, so a mark made by
// accident is undone the same way it was made.
func (p *QueryStorePanel) comparePlans() {
	plan := p.selectedPlan()
	if plan == nil {
		return
	}
	// Same check showPlan makes, and for the same reason: Query Store can hold
	// a plan row whose XML it no longer has, and Parse then reports a document
	// error where "there is no plan here" is what happened.
	if plan.QueryPlanXML == "" {
		p.setStatus(fmt.Sprintf("Query Store holds no plan XML for plan %d", plan.PlanID))
		return
	}
	parsed, err := showplan.Parse([]byte(plan.QueryPlanXML))
	if err != nil {
		p.setStatus(fmt.Sprintf("Plan %d could not be read: %v", plan.PlanID, err))
		return
	}
	if p.cmpPlanID == plan.PlanID {
		p.clearComparison()
		p.setStatus(fmt.Sprintf("Plan %d unmarked", plan.PlanID))
		return
	}
	if p.cmpPlan == nil {
		p.cmpPlan, p.cmpPlanID, p.cmpQueryID = parsed, plan.PlanID, plan.QueryID
		p.setStatus(fmt.Sprintf("Plan %d marked — select another plan and press Compare Plans", plan.PlanID))
		return
	}
	if p.cmpQueryID != plan.QueryID {
		// Two plans of different queries have no operators in common to pair,
		// and the row-by-row result would read as one plan replaced wholesale.
		p.setStatus(fmt.Sprintf("Plan %d is a plan of query %d, not query %d — comparison cleared",
			plan.PlanID, plan.QueryID, p.cmpQueryID))
		p.clearComparison()
		return
	}
	title := fmt.Sprintf("Compare plans %d and %d — query %d (%s)",
		p.cmpPlanID, plan.PlanID, plan.QueryID, p.dbName)
	p.app.openPlanComparePanel(title, p.cmpPlan, parsed)
	p.clearComparison()
}

// clearComparison drops the marked plan, releasing the parsed document with it.
func (p *QueryStorePanel) clearComparison() {
	p.cmpPlan, p.cmpPlanID, p.cmpQueryID = nil, 0, 0
}

// scriptPlanForce opens the statement that would force or unforce the selected
// plan in a query panel, rather than running it — the Script half of every
// write in this application.
func (p *QueryStorePanel) scriptPlanForce() {
	plan := p.selectedPlan()
	if plan == nil {
		return
	}
	d := p.database()
	if d == nil {
		return
	}
	force := !plan.IsForced
	// WithScript intercepts the exec, so this runs against the same lightweight
	// handle the real write uses and reaches the server no more than the
	// statement text needs it to.
	script, err := collectScript(p.conn.Context(), func(ctx context.Context) error {
		if force {
			return d.QueryStoreForcePlanContext(ctx, plan.QueryID, plan.PlanID)
		}
		return d.QueryStoreUnforcePlanContext(ctx, plan.QueryID, plan.PlanID)
	})
	if err != nil {
		p.setStatus(fmt.Sprintf("Script failed: %v", err))
		return
	}
	p.app.openQueryWithText(p.conn, p.dbName, script)
}
