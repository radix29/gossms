package tui

import (
	"fmt"
	"strconv"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tui/gate"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// query_store_panel_toolbar.go is the panel's two tool rows: what each cell
// says, when it is disabled and why, and the menu each selector pops. The
// panel itself is in query_store_panel.go.

// Selector-row cell indexes, in buildTools' layout order. Cells are addressed by
// index: popMenu anchors a selector's list under its own cell, and HandleKey
// runs the Refresh cell's action for F5.
const (
	qsToolReport = iota
	qsToolMetric
	qsToolStatistic
	qsToolWindow
	qsToolTop
	qsToolRefresh
)

// Action-row cell indexes. The two filters lead it rather than sitting with the
// other selectors: the selector row is already 130 columns wide with a long
// report title, and a cell that does not fit is not drawn, which took Refresh
// off the toolbar of every pane narrower than 150.
const (
	qsActMinExec = iota
	qsActRegression
	qsActForce
	qsActUnforce
	qsActShowPlan
	qsActScript
	qsActTrack
	qsActCompare
	qsActPlot
)

// buildTools defines the two toolbar rows in the order the qsTool*/qsAct*
// constants name. refreshToolLabels rebuilds the selector labels on every
// draw, since each shows what it points at.
func (p *QueryStorePanel) buildTools() {
	p.sel.Cells = []controls.ToolCell{
		{Action: p.showReportMenu},
		{Action: p.showMetricMenu},
		{Action: p.showStatisticMenu},
		{Action: p.showWindowMenu},
		{Action: p.showTopMenu},
		{Label: "Refresh", Action: p.Refresh},
	}
	p.acts.Cells = []controls.ToolCell{
		{Action: p.showMinExecMenu},
		{Action: p.showRegressionMenu},
		{Label: "Force Plan", Action: func() { p.setPlanForced(true) }},
		{Label: "Unforce Plan", Action: func() { p.setPlanForced(false) }},
		{Label: "Show Plan", Action: p.showPlan},
		{Label: "Script", Action: p.scriptPlanForce},
		{Label: "Track Query", Action: p.toggleTracked},
		{Label: "Compare Plans", Action: p.comparePlans},
		{Label: "Plot History", Action: p.toggleSeriesMode},
	}
	p.refreshToolLabels()
}

// refreshToolLabels updates the five selectors from the current selection.
func (p *QueryStorePanel) refreshToolLabels() {
	p.sel.Cells[qsToolReport].Label = "Report: " + p.report().Title + " ▾"
	p.sel.Cells[qsToolMetric].Label = "Metric: " + string(p.metric) + " ▾"
	p.sel.Cells[qsToolStatistic].Label = "Statistic: " + string(p.stat) + " ▾"
	p.sel.Cells[qsToolWindow].Label = "Window: " + qsWindows[p.windowIdx].label + " ▾"
	p.sel.Cells[qsToolTop].Label = "Top: " + strconv.Itoa(qsTopCounts[p.topIdx]) + " ▾"
	p.acts.Cells[qsActMinExec].Label = "Min execs: " + qsMinExecLabel(p.minExecIdx) + " ▾"
	p.acts.Cells[qsActRegression].Label = "Regression: " + qsRegressionLabel(p.regressIdx) + " ▾"
	// The action row's one stateful label. Refreshed on every draw with the
	// selectors because it follows the report grid's cursor rather than a click: a
	// button reading "Track Query" over an already-tracked query would untrack it.
	p.acts.Cells[qsActTrack].Label = "Track Query"
	if p.isTracked(p.selectedQueryID()) {
		p.acts.Cells[qsActTrack].Label = "Untrack Query"
	}
	// Script scripts whichever of Force/Unforce applies to the selected plan, so
	// the label says which. Same reason as Track Query's label.
	p.acts.Cells[qsActScript].Label = "Script"
	if plan := p.selectedPlan(); plan != nil {
		if plan.IsForced {
			p.acts.Cells[qsActScript].Label = "Script Unforce"
		} else {
			p.acts.Cells[qsActScript].Label = "Script Force"
		}
	}
	// The chart mode toggle says what the press does, not what is on screen: a
	// button reading "Plot History" while the history is plotted would put the
	// ranking back.
	p.acts.Cells[qsActPlot].Label = "Plot History"
	if p.seriesMode {
		p.acts.Cells[qsActPlot].Label = "Plot Report"
	}
	// Compare takes two presses, and the button says which one it is on.
	p.acts.Cells[qsActCompare].Label = "Compare Plans"
	if p.cmpPlanID != 0 {
		p.acts.Cells[qsActCompare].Label = fmt.Sprintf("Compare with %d", p.cmpPlanID)
	}
}

// qsMinExecLabel and qsRegressionLabel name one entry of each filter's table.
// Index 0 reads "off" rather than "0", which in a count-labelled selector would
// read as a floor that admits nothing.
func qsMinExecLabel(i int) string {
	if qsMinExecCounts[i] <= 0 {
		return "off"
	}
	return core.FormatThousands(qsMinExecCounts[i])
}

func qsRegressionLabel(i int) string {
	if qsRegressionPcts[i] <= 0 {
		return "off"
	}
	return fmt.Sprintf("≥%g%%", qsRegressionPcts[i])
}

// selDisabled reports whether selector cell i is inert now. The whole row is
// while a read or write is in flight; Metric and Top are also inert on a report
// whose query they do not reach, as the two action-row filters are.
func (p *QueryStorePanel) selDisabled(i int) bool {
	if p.busy {
		return true
	}
	switch i {
	case qsToolMetric:
		return !p.report().honours(qsFilterMetric)
	case qsToolTop:
		return !p.report().honours(qsFilterTop)
	}
	return false
}

// selReason is what to tell the user who clicks a dimmed selector. A disabled
// cell swallows its click, and swallowing it silently is what the context-gating
// rule exists to prevent.
func (p *QueryStorePanel) selReason(i int) string {
	switch {
	case p.busy:
		return "" // the whole row is grey while a read is out; that speaks for itself
	case i == qsToolMetric:
		return "Query Store records wait time and nothing else per category, so a metric does not apply to " +
			p.report().Title
	case i == qsToolTop && p.report().honours(qsFilterTracked):
		return "Tracked Queries shows every query you pinned, so there is no row cap to apply"
	case i == qsToolTop:
		return p.report().Title + " reports every interval in the window, so there is no row cap to apply"
	}
	return ""
}

// actDisabled reports whether action cell i is inert: the panel is busy, the
// action has no plan to act on, or the login may not force one.
//
// Asked on demand rather than latched into ToolCell.Disabled: the toolbar is
// built in NewQueryStorePanel, before the capability probe has necessarily run.
// Same as LogViewer.recycleDenied.
func (p *QueryStorePanel) actDisabled(i int) bool {
	if p.busy {
		return true
	}
	// The two filters are inert on a report whose query does not carry them; a
	// selector changing a number the next read ignores is the silent wrong-thing
	// the context-gating rule prevents.
	switch i {
	case qsActMinExec:
		return !p.report().honours(qsFilterExecs)
	case qsActRegression:
		return !p.report().honours(qsFilterRegression)
	}
	// Track acts on the report grid's query, not a plan: a tracked query with no
	// plan left in the window is exactly the case the view exists to show, so
	// requiring a plan would refuse it.
	if i == qsActTrack {
		return p.selectedQueryID() == 0
	}
	// Plot acts on the report grid's query too, and only on the way in: the mode
	// must always be switchable off, including from a row that could not have
	// turned it on (an arrow key can leave the cursor on one).
	if i == qsActPlot {
		return !p.seriesMode && p.selectedQueryID() == 0
	}
	plan := p.selectedPlan()
	if plan == nil {
		return true
	}
	switch i {
	case qsActCompare:
		// Nothing to compare a plan with no XML against, as the mark or the second half.
		return plan.QueryPlanXML == ""
	case qsActForce:
		return plan.IsForced || p.forceDenied()
	case qsActUnforce:
		return !plan.IsForced || p.forceDenied()
	}
	// Script is deliberately not gated on forceDenied. It writes nothing (it opens
	// the statement in a query panel), and Object Explorer's whole "Script <Noun>
	// as" cascade is ungated for the same reason: reading the T-SQL a write would
	// issue is how someone without the permission asks for it. Gating it withheld
	// the text from exactly the login that needed to send it to a DBA.
	return false
}

// forceDenied reports whether the connected login may not force a plan.
// sp_query_store_force_plan is an ALTER-shaped write on the database, gated like
// every Database Properties page's writes.
func (p *QueryStorePanel) forceDenied() bool {
	return !gate.Allows(p.conn, p.dbName, gate.DatabaseWriteRights()...)
}

// actReason is what to tell the user who clicks a dimmed action cell; silently
// swallowing the click is what the context-gating rule exists to prevent.
func (p *QueryStorePanel) actReason(i int) string {
	switch {
	case p.busy:
		return "" // the whole row is grey while a read is out; that speaks for itself
	case i == qsActMinExec:
		return "An execution floor applies to the reports that rank queries, not to " + p.report().Title
	case i == qsActRegression:
		return "A regression threshold applies to Regressed Queries only"
	case i == qsActTrack:
		return "Select a query in the report above first"
	case i == qsActPlot:
		return "Select a query in the report above first — this row is not a query, so it has no history to plot"
	case p.selectedPlan() == nil:
		return "Select a plan in the plan pane first"
	case p.forceDenied():
		return gate.RequiresText(gate.DatabaseWriteRights()...)
	}
	switch i {
	case qsActForce:
		return "That plan is already forced"
	case qsActUnforce:
		return "That plan is not forced"
	case qsActCompare:
		return fmt.Sprintf("Query Store holds no plan XML for plan %d", p.selectedPlan().PlanID)
	}
	return ""
}

// runSel invokes selector cell i's action, or says why it did not.
func (p *QueryStorePanel) runSel(i int) {
	if p.selDisabled(i) {
		if reason := p.selReason(i); reason != "" {
			p.setStatus(reason)
		}
		return
	}
	p.sel.Cells[i].Action()
}

// runAct invokes action cell i's action, or says why it did not.
func (p *QueryStorePanel) runAct(i int) {
	if p.actDisabled(i) {
		if reason := p.actReason(i); reason != "" {
			p.setStatus(reason)
		}
		return
	}
	p.acts.Cells[i].Action()
}

// popMenu shows items under selector cell i of the selector row.
func (p *QueryStorePanel) popMenu(i int, items []controls.MenuItem) {
	p.popMenuAt(p.sel.Cells[i].Rect, items)
}

// popMenuAt shows items under one toolbar cell, or at the panel's top-left if
// that cell didn't fit on its row.
func (p *QueryStorePanel) popMenuAt(r core.Rect, items []controls.MenuItem) {
	if r.IsZero() {
		r = core.Rect{X: p.rect.X, Y: p.rect.Y}
	}
	p.app.contextMenu.Show(r.X, r.Y+1, items)
}

// showOverflowMenu pops the buttons a row was too narrow to draw, under its
// "More ▾" cell — see ToolRow.OverflowItems for what each entry carries.
func (p *QueryStorePanel) showOverflowMenu(row *controls.ToolRow,
	disabled func(int) bool, reason func(int) string, run func(int)) {
	p.popMenuAt(row.More.Rect, row.OverflowItems(disabled, reason, run))
}

// qsMenuItems builds a selector's list, marking the entry in force. The bullet
// tells a user which of eight windows they are looking at without reading the
// button the menu covers.
func qsMenuItems[T comparable](values []T, current T, label func(T) string, choose func(T)) []controls.MenuItem {
	items := make([]controls.MenuItem, 0, len(values))
	for _, v := range values {
		text := label(v)
		if v == current {
			text = "• " + text
		}
		items = append(items, controls.MenuItem{Label: text, Action: func() { choose(v) }})
	}
	return items
}

func (p *QueryStorePanel) showReportMenu() {
	p.popMenu(qsToolReport, qsMenuItems(queryStoreReportTitles, p.report().Title,
		func(s string) string { return s }, p.ShowReport))
}

func (p *QueryStorePanel) showMetricMenu() {
	// The metric list comes from the connected instance, not the whole enum:
	// log_bytes_used and tempdb_space_used arrived in 2017, and offering them on
	// 2016 would produce "Invalid column name" rather than rows.
	p.popMenu(qsToolMetric, qsMenuItems(p.availableMetrics(), p.metric,
		func(m gosmo.QSMetric) string { return string(m) },
		func(m gosmo.QSMetric) { p.metric = m; p.Load() }))
}

func (p *QueryStorePanel) showStatisticMenu() {
	p.popMenu(qsToolStatistic, qsMenuItems(gosmo.QSStatistics(), p.stat,
		func(s gosmo.QSStatistic) string { return string(s) },
		func(s gosmo.QSStatistic) { p.stat, p.statChosen = s, true; p.Load() }))
}

func (p *QueryStorePanel) showWindowMenu() {
	p.popMenu(qsToolWindow, qsMenuItems(qsIndexes(len(qsWindows)), p.windowIdx,
		func(i int) string { return qsWindows[i].label },
		func(i int) { p.windowIdx = i; p.Load() }))
}

func (p *QueryStorePanel) showTopMenu() {
	p.popMenu(qsToolTop, qsMenuItems(qsIndexes(len(qsTopCounts)), p.topIdx,
		func(i int) string { return strconv.Itoa(qsTopCounts[i]) },
		func(i int) { p.topIdx = i; p.Load() }))
}

func (p *QueryStorePanel) showMinExecMenu() {
	p.popMenuAt(p.acts.Cells[qsActMinExec].Rect, qsMenuItems(qsIndexes(len(qsMinExecCounts)), p.minExecIdx,
		qsMinExecLabel, func(i int) { p.minExecIdx = i; p.Load() }))
}

func (p *QueryStorePanel) showRegressionMenu() {
	p.popMenuAt(p.acts.Cells[qsActRegression].Rect, qsMenuItems(qsIndexes(len(qsRegressionPcts)), p.regressIdx,
		qsRegressionLabel, func(i int) { p.regressIdx = i; p.Load() }))
}

// qsIndexes is 0..n-1, for a selector whose menu picks a position in a table
// rather than a value — the label and the value then come from the same entry.
func qsIndexes(n int) []int {
	idxs := make([]int, n)
	for i := range idxs {
		idxs[i] = i
	}
	return idxs
}

// availableMetrics is what this instance's Query Store can rank by. The panel
// asks gosmo rather than listing the enum, so a metric whose column the server
// lacks is never offered.
func (p *QueryStorePanel) availableMetrics() []gosmo.QSMetric {
	if d := p.database(); d != nil {
		if ms := d.QueryStoreMetrics(); len(ms) > 0 {
			return ms
		}
	}
	return []gosmo.QSMetric{gosmo.QSMetricDuration}
}

// database is the lightweight handle every read and write here goes through.
// DatabaseRef, not DatabaseByName: this needs no metadata, and a by-name read
// on every menu open would put a query behind every click.
func (p *QueryStorePanel) database() *gosmo.Database {
	if p.conn == nil || p.conn.Server == nil {
		return nil
	}
	return p.conn.Server.DatabaseRef(p.dbName)
}
