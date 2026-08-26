package tui

import (
	"context"
	"fmt"
	"strconv"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/layout"
)

// query_store_panel.go is SSMS's Query Store views as one panel per database:
// a report selector over a chart, the report's rows, and the plans of whichever
// query is selected — the pane Force Plan and Unforce Plan act from. Drawing is
// in query_store_panel_draw.go and input in query_store_panel_input.go; the
// reports themselves are in query_store_reports.go, shared with the Detail
// Browser's own grids.

// qsReadTimeout bounds one report or one plan read. A Query Store aggregate
// over a month of a busy instance is a large scan, so this is generous — but a
// panel that never comes back is worse than one that says it gave up.
const qsReadTimeout = 120 * time.Second

// qsWindow is one entry of the Window selector: how far back the report reads.
type qsWindow struct {
	label string
	back  time.Duration
}

// qsWindows are the ranges the Window selector offers, shortest first. One
// table rather than a label list beside a duration list — a "24 h" that read
// seven days is invisible in every unit test.
var qsWindows = []qsWindow{
	// The two short ranges are there for the workload you just ran: Regressed
	// Queries compares the two halves of its own window, so on an hour it can
	// only see a change that straddles the half-hour mark — and a development
	// database's whole history is usually minutes old.
	{"5 m", 5 * time.Minute},
	{"15 m", 15 * time.Minute},
	{"1 h", time.Hour},
	{"4 h", 4 * time.Hour},
	{"12 h", 12 * time.Hour},
	{"24 h", 24 * time.Hour},
	{"7 d", 7 * 24 * time.Hour},
	{"30 d", 30 * 24 * time.Hour},
}

// qsDefaultWindowIdx is 24 hours, the same range the Detail Browser's grids
// read — so opening the panel from a report leaf shows the rows that leaf did.
// TestThePanelOpensOnTheWindowTheDetailBrowserRead pins the two together.
const qsDefaultWindowIdx = 5

// qsTopCounts are the row caps the Top selector offers.
var qsTopCounts = []int{10, 25, 50, 100}

// qsDefaultTopIdx is 25, gosmo's own QSDefaultTop.
const qsDefaultTopIdx = 1

// qsFocus names which grid has the keyboard.
type qsFocus int

const (
	qsFocusReport qsFocus = iota
	qsFocusPlans
)

// QueryStorePanel is one database's Query Store: the seven views SSMS shows,
// with the metric, statistic, window and row cap selectable, and the selected
// query's plans beside them.
//
// Reads run on the panel's host connection rather than one of its own — each
// is a one-shot query bounded by qsReadTimeout with nothing on a timer, so no
// background traffic queues behind the shared connection.
type QueryStorePanel struct {
	app    *App
	conn   *db.ServerConn
	dbName string

	rect   core.Rect
	active bool

	// What the toolbar selects. Metric and statistic are kept across a report
	// change: a user who switched to CPU time meant it for the next view too.
	reportIdx int
	metric    gosmo.QSMetric
	stat      gosmo.QSStatistic
	windowIdx int
	topIdx    int

	// res is the report on screen and plans are the selected query's, both
	// kept in typed form so the chart, the plan actions and Show Plan address
	// the same rows the grids draw.
	res   qsResult
	plans []*gosmo.QSPlan

	grid      *controls.DataGrid
	plansGrid *controls.DataGrid
	// chartSplit divides the chart from the grids below it; planSplit divides
	// the report grid from the plan grid below that. Both are horizontal: three
	// stacked panes, because the report grid's eight columns and the plan
	// grid's ten have no room to sit side by side on an 80-column terminal.
	chartSplit *layout.Splitter
	planSplit  *layout.Splitter

	sel  []toolButton // the five selectors and Refresh
	acts []toolButton // the plan actions

	selRect   core.Rect
	actRect   core.Rect
	chartRect core.Rect

	focus qsFocus

	// busy latches the whole toolbar while a report read or a plan write is in
	// flight. Released by the callback the goroutine posts, which is why every
	// launch here goes through safegoRepair.
	busy bool
	// seq and planSeq discard a superseded read that lands after a newer one —
	// the two run independently, so they count separately.
	seq     int
	planSeq int
	cancel  context.CancelFunc

	// barBuf is the chart's bar slice, kept across draws so plotting a report
	// every frame does not allocate one per frame. Rebuilt each time, never
	// read outside drawChart.
	barBuf []charts.Bar

	// queryID is the query the plan pane is showing, so a report reload that
	// lands on the same query does not blank the plans under the cursor.
	queryID int64

	dragZone qsDragZone
}

// qsDragZone names the sub-region that owns the in-progress mouse gesture —
// see QueryPanel.dragZone for why one is needed at all.
type qsDragZone int

const (
	qsZoneNone qsDragZone = iota
	qsZoneChartSplit
	qsZonePlanSplit
	qsZoneGrid
	qsZonePlans
	qsZoneToolbar
	// qsZoneUnclaimed is a press no sub-region wanted. It still owns the
	// gesture, so the repeats tcell sends while the button is held are
	// swallowed instead of landing on whatever the pointer drifts over.
	qsZoneUnclaimed
)

// NewQueryStorePanel creates the panel for one database, opened on the report
// title names (any unrecognised title opens the first). Nothing is read until
// Load runs.
func NewQueryStorePanel(app *App, sc *db.ServerConn, dbName, title string) *QueryStorePanel {
	idx := queryStoreReportIndex(title)
	p := new(QueryStorePanel{
		app:        app,
		conn:       sc,
		dbName:     dbName,
		reportIdx:  idx,
		metric:     gosmo.QSMetricDuration,
		stat:       queryStoreReports[idx].defaultStat,
		windowIdx:  qsDefaultWindowIdx,
		topIdx:     qsDefaultTopIdx,
		grid:       newQSGrid(app),
		plansGrid:  newQSGrid(app),
		chartSplit: layout.NewHorizontalSplitter("─── Report ─── (drag or Ctrl+Up/Down to resize)"),
		planSplit:  layout.NewHorizontalSplitter("─── Plans for the selected query ───"),
	})
	p.chartSplit.SetRatio(0.35)
	p.planSplit.SetRatio(0.6)
	// A row change reloads the plan pane. Only the *plan* grid is rebuilt from
	// here, never the report grid: SetData from inside a grid's own
	// OnSelectRow undoes the move that fired it — see the redrawGrid rule.
	p.grid.OnSelectRow = func(int) { p.selectedQueryChanged() }
	p.buildTools()
	return p
}

// newQSGrid builds one of the panel's two grids with the shared result-grid
// settings.
func newQSGrid(app *App) *controls.DataGrid {
	g := controls.NewDataGrid()
	g.SetCellCursor(true)
	g.SetStatusStyle(resultsStatusStyle)
	g.OnCopyRequest = app.copyWithStatus
	g.SetMaxCellWidth(app.cfg.MaxCellLength + 2)
	return g
}

// report is the view on screen.
func (p *QueryStorePanel) report() queryStoreReport { return queryStoreReports[p.reportIdx] }

// Title returns the panel's tab title (Panel interface).
func (p *QueryStorePanel) Title() string { return "Query Store — " + p.dbName }

// SetActive marks this panel focused (Activatable interface).
func (p *QueryStorePanel) SetActive(v bool) {
	p.active = v
	p.applyFocus()
	p.chartSplit.SetActive(v)
	p.planSplit.SetActive(v)
}

// applyFocus keeps both grids' focus flags in step so only one draws a cursor.
func (p *QueryStorePanel) applyFocus() {
	p.grid.Focus(p.active && p.focus == qsFocusReport)
	p.plansGrid.Focus(p.active && p.focus == qsFocusPlans)
}

// Close cancels any in-flight read. Called from App.closePanelAt; the
// connection belongs to App, so there is nothing else to release.
func (p *QueryStorePanel) Close() { p.cancelRead() }

// cancelRead aborts the in-flight read, on close and when a new read
// supersedes one. seq already discards a superseded result, but without
// cancelling the query runs on the shared host connection until qsReadTimeout.
func (p *QueryStorePanel) cancelRead() {
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
}

// SetBounds positions the panel: the two toolbar rows, then the chart and the
// two grids on either side of the splitters.
func (p *QueryStorePanel) SetBounds(x, y, w, h int) {
	p.rect = core.Rect{X: x, Y: y, W: w, H: h}
	p.selRect, p.actRect = core.Rect{}, core.Rect{}
	if h >= 1 {
		p.selRect = core.Rect{X: x, Y: y, W: w, H: 1}
	}
	if h >= 2 {
		p.actRect = core.Rect{X: x, Y: y + 1, W: w, H: 1}
	}
	layoutToolButtons(p.sel, p.selRect, "")
	layoutToolButtons(p.acts, p.actRect, "")
	p.chartSplit.SetBounds(x, y+2, w, h-2)
	p.layoutChildren()
}

// layoutChildren gives the chart and the two grids their shares of the area
// below the toolbars, on every resize and after every splitter drag.
func (p *QueryStorePanel) layoutChildren() {
	p.chartRect = p.chartSplit.FirstRect()
	lower := p.chartSplit.SecondRect()
	p.planSplit.SetBounds(lower.X, lower.Y, lower.W, lower.H)
	g, pl := p.planSplit.FirstRect(), p.planSplit.SecondRect()
	p.grid.SetBounds(g.X, g.Y, g.W, g.H)
	p.plansGrid.SetBounds(pl.X, pl.Y, pl.W, pl.H)
}

// -- toolbar -------------------------------------------------------------------

// Selector-row cell indexes, in buildTools' layout order. Cells are addressed
// by index: popMenu anchors a selector's list under its own cell, and HandleKey
// runs the Refresh cell's action for F5.
const (
	qsToolReport = iota
	qsToolMetric
	qsToolStatistic
	qsToolWindow
	qsToolTop
	qsToolRefresh
)

// Action-row cell indexes.
const (
	qsActForce = iota
	qsActUnforce
	qsActShowPlan
	qsActScript
)

// buildTools defines the two toolbar rows in the order the qsTool*/qsAct*
// constants name. refreshToolLabels rebuilds the selector labels on every
// draw, since each shows what it points at.
func (p *QueryStorePanel) buildTools() {
	p.sel = []toolButton{
		{action: p.showReportMenu},
		{action: p.showMetricMenu},
		{action: p.showStatisticMenu},
		{action: p.showWindowMenu},
		{action: p.showTopMenu},
		{label: "Refresh", action: p.Refresh},
	}
	p.acts = []toolButton{
		{label: "Force Plan", action: func() { p.setPlanForced(true) }},
		{label: "Unforce Plan", action: func() { p.setPlanForced(false) }},
		{label: "Show Plan", action: p.showPlan},
		{label: "Script", action: p.scriptPlanForce},
	}
	p.refreshToolLabels()
}

// refreshToolLabels updates the five selectors from the current selection.
func (p *QueryStorePanel) refreshToolLabels() {
	p.sel[qsToolReport].label = "Report: " + p.report().Title + " ▾"
	p.sel[qsToolMetric].label = "Metric: " + string(p.metric) + " ▾"
	p.sel[qsToolStatistic].label = "Statistic: " + string(p.stat) + " ▾"
	p.sel[qsToolWindow].label = "Window: " + qsWindows[p.windowIdx].label + " ▾"
	p.sel[qsToolTop].label = "Top: " + strconv.Itoa(qsTopCounts[p.topIdx]) + " ▾"
}

// selDisabled reports whether selector cell i is inert right now. The whole
// row is, while a read or a write is in flight.
func (p *QueryStorePanel) selDisabled(int) bool { return p.busy }

// actDisabled reports whether action cell i is inert: the panel is busy, or
// the action has no plan to act on, or the login may not force one.
//
// Asked on demand rather than latched into toolButton.disabled: the toolbar is
// built once in NewQueryStorePanel, where the capability probe may not have
// run, and whether a cached flag had been refreshed by the time of a click
// would depend on a draw having happened first. Same reasoning as
// LogViewer.recycleDenied.
func (p *QueryStorePanel) actDisabled(i int) bool {
	if p.busy {
		return true
	}
	plan := p.selectedPlan()
	if plan == nil {
		return true
	}
	switch i {
	case qsActForce:
		return plan.IsForced || p.forceDenied()
	case qsActUnforce:
		return !plan.IsForced || p.forceDenied()
	case qsActScript:
		return p.forceDenied()
	}
	return false
}

// forceDenied reports whether the connected login may not force a plan.
// sp_query_store_force_plan is an ALTER-shaped write on the database, gated
// the same way every Database Properties page's writes are.
func (p *QueryStorePanel) forceDenied() bool {
	return !allowsAction(p.conn, p.dbName, databaseWriteRights()...)
}

// actReason is what to tell the user who clicks a dimmed action cell. A
// disabled button swallows its click, and swallowing it silently is the thing
// the context-gating rule exists to prevent.
func (p *QueryStorePanel) actReason(i int) string {
	switch {
	case p.busy:
		return "" // the whole row is grey while a read is out; that speaks for itself
	case p.selectedPlan() == nil:
		return "Select a plan in the plan pane first"
	case p.forceDenied():
		return requiresText(databaseWriteRights()...)
	}
	switch i {
	case qsActForce:
		return "That plan is already forced"
	case qsActUnforce:
		return "That plan is not forced"
	}
	return ""
}

// runSel invokes selector cell i's action if the row is live.
func (p *QueryStorePanel) runSel(i int) {
	if p.selDisabled(i) {
		return
	}
	p.sel[i].action()
}

// runAct invokes action cell i's action, or says why it did not.
func (p *QueryStorePanel) runAct(i int) {
	if p.actDisabled(i) {
		if reason := p.actReason(i); reason != "" {
			p.setStatus(reason)
		}
		return
	}
	p.acts[i].action()
}

// popMenu shows items under selector cell i, or at the panel's top-left if
// that cell didn't fit on the row.
func (p *QueryStorePanel) popMenu(i int, items []controls.MenuItem) {
	r := p.sel[i].rect
	if r.IsZero() {
		r = core.Rect{X: p.rect.X, Y: p.rect.Y}
	}
	p.app.contextMenu.Show(r.X, r.Y+1, items)
}

// qsMenuItems builds a selector's list, marking the entry in force. The
// bullet is what tells a user which of six windows they are looking at
// without reading the button they just covered with the menu.
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
	// The metric list comes from the connected instance, not from the whole
	// enum: log_bytes_used and tempdb_space_used arrived in 2017, and offering
	// them against 2016 would produce "Invalid column name" rather than rows.
	p.popMenu(qsToolMetric, qsMenuItems(p.availableMetrics(), p.metric,
		func(m gosmo.QSMetric) string { return string(m) },
		func(m gosmo.QSMetric) { p.metric = m; p.Load() }))
}

func (p *QueryStorePanel) showStatisticMenu() {
	p.popMenu(qsToolStatistic, qsMenuItems(gosmo.QSStatistics(), p.stat,
		func(s gosmo.QSStatistic) string { return string(s) },
		func(s gosmo.QSStatistic) { p.stat = s; p.Load() }))
}

func (p *QueryStorePanel) showWindowMenu() {
	idxs := make([]int, len(qsWindows))
	for i := range qsWindows {
		idxs[i] = i
	}
	p.popMenu(qsToolWindow, qsMenuItems(idxs, p.windowIdx,
		func(i int) string { return qsWindows[i].label },
		func(i int) { p.windowIdx = i; p.Load() }))
}

func (p *QueryStorePanel) showTopMenu() {
	idxs := make([]int, len(qsTopCounts))
	for i := range qsTopCounts {
		idxs[i] = i
	}
	p.popMenu(qsToolTop, qsMenuItems(idxs, p.topIdx,
		func(i int) string { return strconv.Itoa(qsTopCounts[i]) },
		func(i int) { p.topIdx = i; p.Load() }))
}

// availableMetrics is what this instance's Query Store can rank by. The panel
// asks gosmo rather than listing the enum, so a metric whose column the server
// does not have is never offered.
func (p *QueryStorePanel) availableMetrics() []gosmo.QSMetric {
	if d := p.database(); d != nil {
		if ms := d.QueryStoreMetrics(); len(ms) > 0 {
			return ms
		}
	}
	return []gosmo.QSMetric{gosmo.QSMetricDuration}
}

// database is the lightweight handle every read and write here goes through.
// Database, not DatabaseByName: this needs no metadata, and a by-name read on
// every menu open would put a query behind every click.
func (p *QueryStorePanel) database() *gosmo.Database {
	if p.conn == nil || p.conn.Server == nil {
		return nil
	}
	return p.conn.Server.Database(p.dbName)
}

// -- loading -------------------------------------------------------------------

// ShowReport points the panel at one of the seven views and reads it.
// Reopening from another tree node comes through here, so an already-open
// panel switches view instead of a second one being created.
func (p *QueryStorePanel) ShowReport(title string) {
	p.reportIdx = queryStoreReportIndex(title)
	p.Load()
}

// Refresh re-reads the current report (F5 or the toolbar), keeping the user
// where they were. Rewriting the same rows under the cursor is what
// redrawGrid exists for — and after a Force Plan it is the row just acted on
// that the user is looking at.
func (p *QueryStorePanel) Refresh() { p.load(true) }

// options are what the toolbar currently asks for.
func (p *QueryStorePanel) options() gosmo.QueryStoreReportOptions {
	to := time.Now()
	return gosmo.QueryStoreReportOptions{
		Metric:    p.metric,
		Statistic: p.stat,
		From:      to.Add(-qsWindows[p.windowIdx].back),
		To:        to,
		Top:       qsTopCounts[p.topIdx],
	}
}

// Load runs the current report in the background and applies the result on the
// UI goroutine.
func (p *QueryStorePanel) Load() { p.load(false) }

// load runs the current report. keepView carries the cursor and the scroll
// across the reload; a report, metric, statistic or window change does not,
// because the rows then mean something else and row 4 of the old ranking is
// not row 4 of the new one.
func (p *QueryStorePanel) load(keepView bool) {
	if !p.app.isConnected(p.conn) {
		p.applyResult(qsResult{}, false)
		p.setStatus("Not connected")
		return
	}
	p.cancelRead()
	p.seq++
	seq := p.seq
	p.busy = true
	p.setStatus("Running " + p.report().Title + "...")
	p.refreshToolLabels()

	report, opts, sc, dbName := p.report(), p.options(), p.conn, p.dbName
	ctx, cancel := context.WithCancel(sc.Context())
	p.cancel = cancel
	// safegoRepair, not safego: busy is cleared in the callback below, which a
	// panic on the read goroutine never reaches, and both toolbars are gated
	// on it — every selector and Refresh would sit inert until the panel was
	// closed.
	p.app.safegoRepair("running a Query Store report", func() { p.readPanicked(seq) }, func() {
		defer cancel()
		readCtx, readCancel := context.WithTimeout(ctx, qsReadTimeout)
		defer readCancel()
		d := sc.Server.Database(dbName)
		info, infoErr := d.QueryStoreContext(readCtx)
		var res qsResult
		var err error
		switch {
		case infoErr == nil && !queryStoreIsOn(info):
			// A database with Query Store off answers every report with no
			// rows, which reads identically to a database nothing ran in.
			res = qsOffResult(info)
		default:
			res, err = report.load(readCtx, d, opts)
		}
		p.app.postAndWake(func() {
			if seq != p.seq {
				return
			}
			p.busy = false
			p.cancel = nil
			if err != nil {
				p.res = qsResult{}
				p.grid.SetError(displayError(err))
				p.loadPlans(0)
				return
			}
			p.applyResult(res, keepView)
		})
	})
}

// qsOffResult is the explanatory grid a database with Query Store off gets,
// in place of seven reports that would each come back empty.
func qsOffResult(info *gosmo.QueryStoreInfo) qsResult {
	res := qsResult{columns: propertyValueColumns}
	for _, cells := range queryStoreOffRows(info) {
		res.rows = append(res.rows, qsResultRow{cells: cells})
	}
	return res
}

// readPanicked releases the busy latch after a panic on the read goroutine —
// Load's safegoRepair step. Guarded by seq like the normal completion path: a
// newer Load set busy for itself, and clearing it here would re-enable a
// toolbar whose read is still out.
func (p *QueryStorePanel) readPanicked(seq int) {
	if seq != p.seq {
		return
	}
	p.busy = false
	p.cancel = nil
	p.setStatus("The report stopped unexpectedly — see the log for details")
}

// applyResult puts a finished report on screen and follows it with the plans
// of whatever query the cursor lands on.
//
// keepView picks between redrawGrid, which carries the cursor, the scroll and
// any dragged column width across the reload, and SetData, which resets all
// three — right only where the rows now mean something else. Neither is ever
// called from the grid's own OnSelectRow, which would undo the move that
// fired it.
func (p *QueryStorePanel) applyResult(res qsResult, keepView bool) {
	p.res = res
	if keepView {
		redrawGrid(p.grid, res.columns, res.cells())
	} else {
		p.grid.SetData(res.columns, res.cells())
	}
	p.setStatus(p.summary())
	p.loadPlans(p.selectedQueryID())
}

// summary is the status line under the report grid.
func (p *QueryStorePanel) summary() string {
	w := qsWindows[p.windowIdx]
	if len(p.res.rows) == 0 {
		return fmt.Sprintf("%s — no rows in the last %s", p.report().Title, w.label)
	}
	return fmt.Sprintf("%s — %d rows, %s %s over the last %s",
		p.report().Title, len(p.res.rows), p.stat, p.metric, w.label)
}

// selectedQueryID is the query the report grid's cursor is on, 0 for a report
// whose rows are not queries and for an empty grid.
func (p *QueryStorePanel) selectedQueryID() int64 {
	row := p.grid.SelectedRow()
	if row < 0 || row >= len(p.res.rows) {
		return 0
	}
	return p.res.rows[row].queryID
}

// selectedQueryChanged reloads the plan pane when the report grid's cursor
// moves to a different query. Guarded on the id, not the row: an arrow key
// through a wait-category report would otherwise start a plan read per row.
func (p *QueryStorePanel) selectedQueryChanged() {
	if id := p.selectedQueryID(); id != p.queryID {
		p.loadPlans(id)
	}
}

// loadPlans reads one query's plans into the plan pane, or empties it for a
// zero id. It has its own sequence rather than sharing the report's: the two
// reads are independent, and the plan pane must not be blanked by a report
// reload that has not landed yet.
func (p *QueryStorePanel) loadPlans(queryID int64) {
	p.queryID = queryID
	p.planSeq++
	seq := p.planSeq
	if queryID == 0 || !p.app.isConnected(p.conn) {
		p.plans = nil
		p.plansGrid.SetData(qsPlanColumns, nil)
		p.plansGrid.SetStatus("No query selected")
		return
	}
	p.plansGrid.SetStatus(fmt.Sprintf("Reading plans for query %d...", queryID))
	opts, sc, dbName := p.options(), p.conn, p.dbName
	// safegoRepair, not safego: the "Reading plans..." placeholder is replaced
	// by the callback below, which a panic on the read goroutine never reaches,
	// and nothing else writes the pane until another query is selected — so the
	// pane would claim to be reading a query it gave up on.
	p.app.safegoRepair("reading Query Store plans", func() { p.plansPanicked(seq) }, func() {
		ctx, cancel := context.WithTimeout(sc.Context(), qsReadTimeout)
		defer cancel()
		plans, err := sc.Server.Database(dbName).QueryStorePlansContext(ctx, queryID, opts)
		p.app.postAndWake(func() {
			if seq != p.planSeq {
				return
			}
			if err != nil {
				p.plans = nil
				p.plansGrid.SetError(displayError(err))
				return
			}
			p.plans = plans
			p.plansGrid.SetData(qsPlanColumns, qsPlanRows(plans, opts))
			p.plansGrid.SetStatus(fmt.Sprintf("Query %d — %d plans", queryID, len(plans)))
		})
	})
}

// plansPanicked replaces the plan pane's "Reading plans..." placeholder after a
// panic on the read goroutine — loadPlans' safegoRepair step. Guarded by
// planSeq like the normal completion path: a newer load owns the pane, and
// blanking it here would drop a result that is still on its way.
func (p *QueryStorePanel) plansPanicked(seq int) {
	if seq != p.planSeq {
		return
	}
	p.plans = nil
	p.plansGrid.SetData(qsPlanColumns, nil)
	p.plansGrid.SetStatus("Reading plans stopped unexpectedly — see the log for details")
}

// qsPlanColumns are the plan pane's columns.
var qsPlanColumns = []string{"Plan ID", "Forced", "Forcing Type", "Executions", "Value",
	"Parallel", "Trivial", "Compat", "Last Execution", "Force Failures"}

// qsPlanRows renders one query's plans under qsPlanColumns.
func qsPlanRows(plans []*gosmo.QSPlan, opts gosmo.QueryStoreReportOptions) [][]string {
	rows := make([][]string, 0, len(plans))
	for _, pl := range plans {
		rows = append(rows, []string{
			strconv.FormatInt(pl.PlanID, 10),
			yesNo(pl.IsForced),
			pl.ForcingType,
			core.FormatThousands(pl.ExecCount),
			formatQSValue(opts.Metric, pl.Value),
			yesNo(pl.IsParallelPlan),
			yesNo(pl.IsTrivialPlan),
			strconv.Itoa(pl.CompatibilityLevel),
			dashIfZero(pl.LastExecutionTime),
			core.FormatThousands(pl.ForceFailureCount),
		})
	}
	return rows
}

// selectedPlan is the plan the plan grid's cursor is on, nil when the pane is
// empty. Indexed against plans, which is what the grid was built from.
func (p *QueryStorePanel) selectedPlan() *gosmo.QSPlan {
	row := p.plansGrid.SelectedRow()
	if row < 0 || row >= len(p.plans) {
		return nil
	}
	return p.plans[row]
}

// setStatus writes the panel's one-line state into the report grid's own
// status bar, so it sits with the rows it describes.
func (p *QueryStorePanel) setStatus(s string) { p.grid.SetStatus(s) }

// -- the plan actions ----------------------------------------------------------

// setPlanForced forces or unforces the selected plan, after confirming.
// Forcing a plan changes what every future execution of that query does, on a
// live database — which is why it asks, and why the question names both the
// query and the plan rather than "the selected row".
func (p *QueryStorePanel) setPlanForced(force bool) {
	plan := p.selectedPlan()
	if plan == nil || !p.app.requireConn(p.conn) {
		return
	}
	verb := "Unforce"
	if force {
		verb = "Force"
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
		p.setStatus(fmt.Sprintf("%s plan %d for query %d...", verb+"ing", planID, queryID))
		// safegoRepair for the same reason Load uses it: busy is cleared in
		// the posted callback, which a panic never reaches.
		p.app.safegoRepair("forcing a Query Store plan", func() { p.forcePanicked(verb) }, func() {
			ctx, cancel := context.WithTimeout(sc.Context(), qsReadTimeout)
			defer cancel()
			d := sc.Server.Database(dbName)
			var err error
			if force {
				err = d.QueryStoreForcePlanContext(ctx, queryID, planID)
			} else {
				err = d.QueryStoreUnforcePlanContext(ctx, queryID, planID)
			}
			p.app.postAndWake(func() {
				p.busy = false
				if err != nil {
					p.setStatus(fmt.Sprintf("%s failed: %v", verb, withPermissionAdvice(err)))
					return
				}
				p.app.setStatus(fmt.Sprintf("Plan %d %sd for query %d", planID, verb, queryID))
				// Both panes are stale: the plan's own IsForced changed, and
				// the report's Forced Plan column with it. Through Refresh, so
				// the user is left on the query they just acted on rather than
				// back at the top of the report.
				p.Refresh()
			})
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
