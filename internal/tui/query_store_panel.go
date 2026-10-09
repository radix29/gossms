package tui

import (
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/showplan"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/layout"
)

// query_store_panel.go is SSMS's Query Store views as one panel per database: a
// report selector over a chart, the report's rows, and the plans of the selected
// query (the pane Force Plan and Unforce Plan act from). This file is state,
// construction and layout; see query_store_panel_toolbar.go, _load.go (reads),
// _plans.go (plan actions), _draw.go and _input.go; the reports are in
// query_store_reports.go, shared with the Detail Browser's grids.

// qsReadTimeout bounds one report or one plan read. A Query Store aggregate over
// a month of a busy instance is a large scan, so it is generous.
const qsReadTimeout = 120 * time.Second

// qsWindow is one entry of the Window selector: how far back the report reads.
type qsWindow struct {
	label string
	back  time.Duration
}

// qsWindows are the ranges the Window selector offers, shortest first. One table
// rather than a label list beside a duration list: a "24 h" that read seven days
// is invisible in every unit test.
var qsWindows = []qsWindow{
	// The two short ranges are for the workload you just ran: a development
	// database's whole history is often minutes old, and Regressed Queries compares
	// the two halves of its own window.
	{"5 m", 5 * time.Minute},
	{"15 m", 15 * time.Minute},
	{"1 h", time.Hour},
	{"4 h", 4 * time.Hour},
	{"12 h", 12 * time.Hour},
	{"24 h", 24 * time.Hour},
	{"7 d", 7 * 24 * time.Hour},
	{"30 d", 30 * 24 * time.Hour},
}

// qsDefaultWindowIdx is 24 hours, the range the Detail Browser's grids read, so
// opening the panel from a report leaf shows the rows that leaf did.
// TestThePanelOpensOnTheWindowTheDetailBrowserRead pins the two together.
const qsDefaultWindowIdx = 5

// qsTopCounts are the row caps the Top selector offers.
var qsTopCounts = []int{10, 25, 50, 100}

// qsDefaultTopIdx is 25, gosmo's own QSDefaultTop.
const qsDefaultTopIdx = 1

// qsMinExecCounts are the execution floors the Min Execs selector offers, the
// first meaning no floor. A query that ran twice has an average, variation and
// regression that mean nothing; the floor stops a report being topped by the
// query that ran once during a backup.
var qsMinExecCounts = []int64{0, 2, 5, 10, 100, 1000}

// qsRegressionPcts are the thresholds the Regression selector offers, as a
// percentage of the baseline; the first means none. A percentage because the same
// report is read under eleven metrics in four units (gosmo's MinRegressionPct).
var qsRegressionPcts = []float64{0, 10, 25, 50, 100}

// qsDefaultFilterIdx is index 0 of both: the panel opens unfiltered, as every
// report it can be opened from was read.
const qsDefaultFilterIdx = 0

// qsFocus names which grid has the keyboard.
type qsFocus int

const (
	qsFocusReport qsFocus = iota
	qsFocusPlans
)

// QueryStorePanel is one database's Query Store: the seven views SSMS shows, with
// the metric, statistic, window and row cap selectable, and the selected query's
// plans beside them.
//
// Reads run on the panel's host connection, not one of its own: each is a one-shot
// query bounded by qsReadTimeout, so no background traffic queues behind the
// shared connection.
type QueryStorePanel struct {
	app    *App
	conn   *db.ServerConn
	dbName string

	rect   core.Rect
	active bool

	// What the toolbar selects. The metric is kept across a report change: a user who
	// switched to CPU time meant it for the next view too.
	reportIdx int
	metric    gosmo.QSMetric
	stat      gosmo.QSStatistic
	windowIdx int
	topIdx    int

	// statChosen records that the user picked a statistic from the toolbar. Until
	// then the statistic follows each report's own defaultStat: Total for the three
	// read as accumulated cost, Avg for the four about cost per execution. Applying
	// the default at construction only would make one action give two answers: Total
	// on a panel opened for the report, whatever the last view left behind on one
	// already open.
	statChosen bool

	// minExecIdx and regressIdx index qsMinExecCounts and qsRegressionPcts: the two
	// filters the server applies, kept across a report change like the metric and
	// statistic.
	minExecIdx int
	regressIdx int

	// res is the report on screen and plans are the selected query's, both kept in
	// typed form so the chart, the plan actions and Show Plan address the same rows
	// the grids draw.
	res   qsResult
	plans []*gosmo.QSPlan

	grid      *controls.DataGrid
	plansGrid *controls.DataGrid
	// chartSplit divides the chart from the grids below it; planSplit divides the
	// report grid from the plan grid below that. Both horizontal: three stacked
	// panes, because the report grid's eight columns and the plan grid's ten don't
	// fit side by side on an 80-column terminal.
	chartSplit *layout.Splitter
	planSplit  *layout.Splitter

	// sel is the selector row (the five selectors and Refresh), acts the plan
	// actions. Each row's More cell matters: both are wider than the pane at ordinary
	// terminal sizes (the action row alone wants 119 columns of a pane that gets 70%
	// of the screen), so without it Track Query and Compare Plans were unreachable
	// below a 170-column terminal.
	sel  controls.ToolRow
	acts controls.ToolRow

	selRect   core.Rect
	actRect   core.Rect
	chartRect core.Rect

	focus qsFocus

	// busy latches the whole toolbar while a report read or a plan write is in
	// flight. Released by the callback the goroutine posts, which is why every launch
	// here goes through safegoRepair.
	busy bool
	// reportRead and planRead each discard a superseded read that lands after a newer
	// one and cancel the read it replaced. Two, not one: the report and plan panes
	// run independently, and a report reload must not kill the plan read beside it.
	// See latest.
	reportRead latest
	planRead   latest

	// barBuf is the chart's bar slice, kept across draws so plotting every frame does
	// not allocate. Rebuilt each time, never read outside drawChart.
	barBuf []charts.Bar

	// cmpPlan is the plan marked for comparison by the first press of Compare Plans,
	// parsed there and then: the plan grid is rebuilt by every report reload, and a
	// mark pointing into it would compare whatever row that index landed on after the
	// next Refresh. cmpQueryID is what it was a plan of; plans of different queries
	// are not a comparison.
	cmpPlan    *showplan.Plan
	cmpPlanID  int64
	cmpQueryID int64

	// queryID is the query the plan pane is showing, so a report reload that lands
	// on the same query does not blank the plans under the cursor.
	queryID int64

	// seriesMode swaps the report's bar chart for the selected query's per-plan
	// history, series holds what was read for it, and seriesNote is what the chart
	// says while there is nothing to plot (read in flight, failed, or no intervals).
	// seriesLabel names the quantity the lines carry, kept from the options the read
	// went out with rather than the toolbar, which can have moved on. See
	// query_store_series.go.
	seriesMode  bool
	series      qsSeriesData
	seriesNote  string
	seriesLabel string
	// seriesRead is a third latest, not a share of the plan pane's: a series read and
	// a plan read fire from the same cursor move and must not cancel or supersede one
	// another.
	seriesRead latest

	dragZone qsDragZone
}

// qsDragZone names the sub-region that owns the in-progress mouse gesture; see
// QueryPanel.dragZone.
type qsDragZone int

const (
	qsZoneNone qsDragZone = iota
	qsZoneChartSplit
	qsZonePlanSplit
	qsZoneGrid
	qsZonePlans
	qsZoneToolbar
	// qsZoneUnclaimed is a press no sub-region wanted. It still owns the gesture, so
	// tcell's repeats while the button is held are swallowed rather than landing
	// wherever the pointer drifts.
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
		minExecIdx: qsDefaultFilterIdx,
		regressIdx: qsDefaultFilterIdx,
		grid:       newQSGrid(app),
		plansGrid:  newQSGrid(app),
		chartSplit: layout.NewHorizontalSplitter("─── Report ─── (drag or Ctrl+Up/Down to resize)"),
		planSplit:  layout.NewHorizontalSplitter("─── Plans for the selected query ───"),
	})
	p.chartSplit.SetRatio(0.35)
	p.planSplit.SetRatio(0.6)
	// A row change reloads the plan pane. Only the *plan* grid is rebuilt from here,
	// never the report grid: SetData from inside a grid's own OnSelectRow undoes the
	// move that fired it (the redrawGrid rule).
	p.grid.OnSelectRow = func(int) { p.selectedQueryChanged() }
	// The report grid's Query column is a flattened rendering, so "Show Value" on it
	// must not open the cell; see showValue.
	p.grid.OnShowValue = p.showValue
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
	g.OnShowValue = app.showSQLCellValue
	g.SetMaxCellWidth(app.cfg.MaxCellLength + 2)
	return g
}

// showValue is the report grid's "Show Value" hook. It opens the statement Query
// Store actually holds, not the cell: queryStoreOneLine collapses the statement
// onto one line for the grid, and a `-- comment` anywhere in it then swallows every
// line after it, leaving runnable SQL mostly commented out.
//
// The row comes from the grid rather than the hook, whose first parameter is the
// *column* index. DataGrid.openViewer reads the cell at selRow/selCol, so
// SelectedRow is the row whose value is being shown.
func (p *QueryStorePanel) showValue(col int, column, value string) bool {
	if column == qsQueryColumn {
		if row := p.grid.SelectedRow(); row >= 0 && row < len(p.res.rows) {
			// Empty on a report whose rows are not queries, and on a tracked row for a query
			// Query Store no longer holds; the cell is then the only text there is.
			if raw := p.res.rows[row].queryText; raw != "" {
				value = raw
			}
		}
	}
	return p.app.showSQLCellValue(col, column, value)
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

// Close cancels any in-flight read. Called from App.closePanelAt; the connection
// belongs to App.
func (p *QueryStorePanel) Close() {
	p.reportRead.Cancel()
	p.planRead.Cancel()
	p.seriesRead.Cancel()
}

// SetBounds positions the panel: the two toolbar rows, then the chart and the two
// grids either side of the splitters.
func (p *QueryStorePanel) SetBounds(x, y, w, h int) {
	p.rect = core.Rect{X: x, Y: y, W: w, H: h}
	p.selRect, p.actRect = core.Rect{}, core.Rect{}
	if h >= 1 {
		p.selRect = core.Rect{X: x, Y: y, W: w, H: 1}
	}
	if h >= 2 {
		p.actRect = core.Rect{X: x, Y: y + 1, W: w, H: 1}
	}
	p.layoutToolRows()
	p.chartSplit.SetBounds(x, y+2, w, h-2)
	p.layoutChildren()
}

// layoutToolRows places both toolbar rows, collapsing whatever does not fit
// into each row's own "More ▾" menu.
func (p *QueryStorePanel) layoutToolRows() {
	p.sel.Layout(p.selRect, "")
	p.acts.Layout(p.actRect, "")
}

// layoutChildren gives the chart and the two grids their shares of the area below
// the toolbars, on every resize and splitter drag.
func (p *QueryStorePanel) layoutChildren() {
	p.chartRect = p.chartSplit.FirstRect()
	lower := p.chartSplit.SecondRect()
	p.planSplit.SetBounds(lower.X, lower.Y, lower.W, lower.H)
	g, pl := p.planSplit.FirstRect(), p.planSplit.SecondRect()
	p.grid.SetBounds(g.X, g.Y, g.W, g.H)
	p.plansGrid.SetBounds(pl.X, pl.Y, pl.W, pl.H)
}
