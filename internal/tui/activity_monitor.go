package tui

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/dashboard"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// amTab identifies one of the Activity Monitor's five tabs.
type amTab int

const (
	amTabHistory amTab = iota
	amTabSample
	amTabTempDB
	amTabSessions
	amTabBlock
	amTabCount = 5
)

// amTabLabels are the tab-bar labels, indexed by amTab.
var amTabLabels = [amTabCount]string{"History", "Sample", "TempDB", "Sessions", "Block"}

// dashboardTab reports whether t is fed by the shared activity collector —
// History and Sample, which share one rate and one Pause. TempDB draws a
// dashboard but runs its own collector, so it is excluded.
func (t amTab) dashboardTab() bool { return t == amTabHistory || t == amTabSample }

// canvasTab reports whether t draws a scrolling dashboard canvas rather than a
// result grid, gating every scrolling key and gesture and the toolbar's
// rate/Pause arm.
func (t amTab) canvasTab() bool { return t.dashboardTab() || t == amTabTempDB }

// amRates are the refresh intervals the rate selector offers.
var amRates = []time.Duration{
	2 * time.Second,
	3 * time.Second,
	5 * time.Second,
	10 * time.Second,
}

// amRateLabels label the amRates entries.
var amRateLabels = []string{"2 s", "3 s", "5 s", "10 s"}

// defaultRateIdx is the interval a freshly opened panel collects at.
const defaultRateIdx = 0

// collectionStoppedStatus is what the header says when a collector's Run
// returned without reporting why (the cancelled-context exit). An exit that did
// report goes through applyError and keeps its message.
const collectionStoppedStatus = "Collection stopped."

// noSamplesStatus is what a feed reports before its first tick lands.
const noSamplesStatus = "No samples collected yet."

// amTempDBRates are the TempDB tab's own intervals, an order of magnitude
// longer than the activity rates: tempdb space moves over minutes, and each
// tick reads tempdb metadata — the very thing a contended tempdb lacks.
var amTempDBRates = []time.Duration{
	10 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

var amTempDBRateLabels = []string{"10 s", "30 s", "60 s"}

// defaultTempDBRateIdx is 30 seconds — fast enough to watch a version store
// grow, slow enough that the metadata read is never the problem.
const defaultTempDBRateIdx = 1

// ActivityMonitor is a five-tab view of one server's live activity, hosted by
// layout.PanelManager. History, Sample and TempDB render internal/tui/dashboard
// into a fixed-size off-screen canvas the panel scrolls a viewport over;
// Sessions and Block are result grids over sp_WhoIsActive and sp_block.
//
// One collector feeds both dashboards — Sample is the newest sample in
// History's store — so one rate and one Pause govern both. TempDB keeps its own
// collector, store and rate.
type ActivityMonitor struct {
	app  *App
	conn *db.ServerConn // the server being watched; owned by App, not by this panel

	rect   core.Rect
	active bool

	tab amTab

	// scrollX/scrollY are per tab, so leaving a tab and coming back returns to
	// where the user was rather than to the top-left.
	scrollX [amTabCount]int
	scrollY [amTabCount]int

	// act and td are the collector-facing state of the two dashboards: the feed
	// behind History and Sample, and the TempDB tab's own. Every tab-dependent
	// read of rate, pause, status or sample time goes through feed().
	act amFeed
	td  amFeed

	// store holds every sample collected, and collector is the goroutine
	// filling it. History plots the store, Sample draws its newest entry.
	store     activity.Store
	collector *activity.Collector

	history dashboard.HistoryView
	sample  dashboard.SampleView

	// The TempDB tab's own store and collector: tens of seconds per tick
	// against a store retaining hours, sharing nothing with the activity
	// dashboards.
	tdStore     activity.TempDBStore
	tdCollector *activity.TempDBCollector
	tempdb      dashboard.TempDBView

	// blk and sess are the procedure-backed tabs: a result grid over one run of
	// sp_block and of sp_WhoIsActive. Each opens its own connection when first
	// shown; neither refreshes on a timer.
	blk  *amProcTab
	sess *amProcTab

	// feedConn is the connection both dashboard collectors run on, also in
	// owned. Named separately so a stopped collector can be restarted without
	// reopening the panel.
	feedConn *db.ServerConn

	// owned are connections this panel opened for itself and must close on
	// teardown, opened lazily as each tab gains real queries.
	owned []*db.ServerConn

	tabRect     core.Rect
	toolRect    core.Rect
	contentRect core.Rect // everything below the toolbar, scrollbars included
	viewRect    core.Rect // the dashboard viewport, scrollbars excluded

	// tools are the panel's toolbar cells (panel_toolbar.go, not App's icon
	// strip in toolbar.go). The rate controls disable once their collector has
	// *started and then stopped* (amFeed.stopped), never before it starts:
	// Pause is a preference carried into startActivityCollector, so a panel
	// still connecting keeps them live.
	tools      []toolButton
	toolPrefix string
	// toolsEnd is the column just past the last laid-out control, the More
	// cell included, so the right-aligned collector state fits into what is
	// left of the row.
	toolsEnd int

	// more is the "More ▾" cell standing in for the controls this row was too
	// narrow to draw, and hidden the indexes it holds. A dashboard row wants 47
	// columns and the pane gets 70% of the terminal, so Pause — which has no
	// key binding — went off the row on anything under a 68-column terminal.
	more   toolButton
	hidden []int

	// hits is where each History chart's plot landed on the canvas, in canvas
	// coordinates, recorded by the last draw.
	hits []dashboard.ChartHit

	// canvas is the last rendered dashboard and canvasKey the state it came
	// from. Draw runs on every event and re-rendering all eleven charts costs
	// milliseconds, while the view models only move when a sample lands.
	canvas    *charts.Canvas
	canvasKey amCanvasKey

	// viewGen counts view-model rebuilds and is what marks the cached canvas
	// stale: the stores' Len stops changing once retention prunes, while their
	// contents keep scrolling.
	viewGen uint64

	// tooltip is the readout pinned by the last click, nil when none shows.
	// It survives redraws, so a paused dashboard keeps the clicked numbers.
	tooltip *amTooltip

	dragZone  amDragZone
	vDragging bool
	hDragging bool
}

// amFeed is one dashboard's collector-facing state: rate list and selection,
// Pause, whether the collector runs, and what it last reported. The activity
// and TempDB feeds differ only in rates and in which collector they drive.
//
// The collectors are separate types, so reaching one is a closure rather than a
// field: applyRate/applyPaused are re-pointed at each new collector by the
// panel's start methods, and nil until one has started.
type amFeed struct {
	prefix     string // the toolbar's label for this feed's rate selector
	rates      []time.Duration
	rateLabels []string

	rateIdx int
	paused  bool

	// started is true once a collector has been created for this feed. The
	// toolbar gate is started && !collecting, never !collecting alone: paused
	// is a preference carried into the start, so a panel still connecting has
	// to keep its controls live.
	started bool

	// collecting is true while the collector goroutine runs; the toolbar
	// reports it.
	collecting bool

	// status is the collector's last non-fatal message, shown in the dashboard
	// header rather than in place of the dashboard.
	status string

	// sampleTime is the clock time of the sample on screen, empty until the
	// first is collected.
	sampleTime string

	applyRate   func(time.Duration)
	applyPaused func(bool)
	// restart starts a new collector on the connection the panel already
	// owns — what the Retry control calls.
	restart func()
}

// rate is the interval this feed's collector ticks at.
func (f *amFeed) rate() time.Duration { return f.rates[f.rateIdx] }

// setRate selects an interval by index into the feed's rates and reports
// whether anything changed. An index off either end is a no-op, not a wrap, so
// the toolbar and the +/- keys agree about the list's ends.
func (f *amFeed) setRate(i int) bool {
	if i < 0 || i >= len(f.rates) || i == f.rateIdx {
		return false
	}
	f.rateIdx = i
	if f.applyRate != nil {
		f.applyRate(f.rate())
	}
	return true
}

// setPaused pauses or resumes this feed's collector.
func (f *amFeed) setPaused(v bool) {
	f.paused = v
	if f.applyPaused != nil {
		f.applyPaused(v)
	}
}

// stopped reports a collector that started and has since returned — the state
// in which Pause does nothing, every send being dropped.
func (f *amFeed) stopped() bool { return f.started && !f.collecting }

// amCanvasKey is everything a rendered dashboard canvas depends on. Any
// difference from the cached key means the canvas has to be redrawn.
type amCanvasKey struct {
	tab      amTab
	w, h     int
	gen      uint64
	header   dashboard.Header
	interval time.Duration
}

// amTooltip is a pinned readout of one sample: every series of one chart at the
// clicked bucket, in the chart's own colours.
//
// The pin is the *sample*, not the spot clicked — chart and time identify it,
// and its position is re-derived each draw. A history plots its newest bucket
// at the right edge, so each new sample pushes the pinned one a column left;
// anchored to the screen, the box would sit still and quietly start reporting
// whichever sample slid under it.
type amTooltip struct {
	chart string // ChartHit.Title of the chart the pin lives on
	time  string // the pinned bucket's clock time — its identity and caption
	rows  []amTooltipRow

	// snapshot marks a pin on a current-sample chart. Those have one bucket
	// and no time axis, so nothing drifts and the column stays where clicked.
	snapshot bool

	// col and row are the pinned point in canvas coordinates: col the
	// bucket's column, row where the click landed within the plot. Both are
	// translated to the screen at draw time.
	col, row int

	// plot and timeRow are the pinned chart's rects in canvas coordinates,
	// re-read with the position each draw: the pin drops when it falls outside
	// the first, and the time callout sits on the second.
	plot, timeRow core.Rect
}

type amTooltipRow struct {
	label string
	value string
	color tcell.Color
}

// NewActivityMonitor creates the panel for one server connection.
func NewActivityMonitor(app *App, sc *db.ServerConn) *ActivityMonitor {
	am := new(ActivityMonitor{
		app:  app,
		conn: sc,
		act: amFeed{
			prefix:     "Refresh rate:",
			rates:      amRates,
			rateLabels: amRateLabels,
			rateIdx:    defaultRateIdx,
			status:     noSamplesStatus,
		},
		td: amFeed{
			prefix:     "TempDB rate:",
			rates:      amTempDBRates,
			rateLabels: amTempDBRateLabels,
			rateIdx:    defaultTempDBRateIdx,
			status:     noSamplesStatus,
		},
	})
	am.act.restart = am.startActivityCollector
	am.td.restart = am.startTempDBCollector
	am.blk = am.newProcTab(activity.BlockProc, "")
	am.sess = am.newProcTab(activity.WhoIsActiveProc, whoIsActiveCredit())
	am.buildTools()
	return am
}

// Title returns the panel title (Panel interface).
func (am *ActivityMonitor) Title() string { return "Activity Monitor" }

// SetActive marks this panel focused (Activatable interface).
func (am *ActivityMonitor) SetActive(v bool) { am.active = v }

// Close releases everything the panel owns: the collector goroutine, the
// per-tab connections and the collected samples. Nothing here is persisted.
func (am *ActivityMonitor) Close() {
	if am.collector != nil {
		am.collector.Stop()
		am.collector = nil
	}
	if am.tdCollector != nil {
		am.tdCollector.Stop()
		am.tdCollector = nil
	}
	am.act.collecting = false
	am.td.collecting = false
	for _, sc := range am.owned {
		sc.Close()
	}
	am.owned = nil
	am.feedConn = nil
	// The tempdb copies of the two procedures stay: they cost nothing, vanish
	// at the next restart, and dropping them would make every reopening of this
	// panel reinstall them.
	for _, pt := range []*amProcTab{am.blk, am.sess} {
		pt.conn = nil
		pt.grid = nil
		pt.result = nil
	}
	am.store.Reset()
	am.tdStore.Reset()
	am.history = dashboard.HistoryView{}
	am.sample = dashboard.SampleView{}
	am.tempdb = dashboard.TempDBView{}
	am.invalidateView()
	// Dropped outright rather than left for refreshTooltip: the views are empty
	// now, so it would resolve to nothing on the next draw anyway.
	am.tooltip = nil
	am.canvas = nil
}

// startCollector takes ownership of the panel's connection and starts both
// dashboard collectors against it. Called once from the connect callback;
// each can be restarted on its own afterwards.
func (am *ActivityMonitor) startCollector(conn *db.ServerConn) {
	am.adopt(conn)
	am.feedConn = conn
	am.startActivityCollector()
	am.startTempDBCollector()
}

// startActivityCollector starts the History/Sample collector on the panel's
// own connection.
func (am *ActivityMonitor) startActivityCollector() {
	conn := am.feedConn
	if conn == nil || conn.Server == nil {
		return
	}
	am.collector = activity.NewCollector(conn.Server.DB(),
		func(s activity.Sample) { am.app.postAndWake(func() { am.applySample(s) }) },
		func(err error) { am.app.postAndWake(func() { am.applyError(err) }) })
	am.act.applyRate = am.collector.SetRate
	am.act.applyPaused = am.collector.SetPaused
	am.act.started, am.act.collecting, am.act.status = true, true, ""
	if am.act.paused {
		am.collector.SetPaused(true)
	}

	am.buildTools() // the rate/Pause controls are gated on the feed's state

	collector, ctx, rate := am.collector, conn.Context(), am.act.rate()
	// safegoRepair, not safego: am.act.collecting was latched above and is
	// cleared only by runCollector's second half, which a panic skips.
	// collectorStopped is that half, already guarded on collector.
	am.app.safegoRepair("collecting server activity",
		func() { am.collectorStopped(collector) },
		func() { am.runCollector(collector, ctx, rate) })
}

// runCollector is the collector goroutine's whole body: collect, then tell the
// panel it stopped.
//
// The second half keeps am.collecting honest. Run reports only one of its three
// exits through onError (ErrNoPermission); a failed permission prologue and a
// cancelled context both return silently, and without this the toolbar goes on
// claiming to collect, with a live Pause whose sends are all dropped.
func (am *ActivityMonitor) runCollector(c *activity.Collector, ctx context.Context, rate time.Duration) {
	c.Run(ctx, rate)
	am.app.postAndWake(func() { am.collectorStopped(c) })
}

// startTempDBCollector starts the TempDB tab's own collector on the same
// connection's pool. A second goroutine on one *sql.DB gets its own physical
// connection, so a slow tempdb tick never delays an activity tick.
func (am *ActivityMonitor) startTempDBCollector() {
	conn := am.feedConn
	if conn == nil || conn.Server == nil {
		return
	}
	am.tdCollector = activity.NewTempDBCollector(conn.Server.DB(),
		func(s activity.TempDBSample) { am.app.postAndWake(func() { am.applyTempDBSample(s) }) },
		func(err error) { am.app.postAndWake(func() { am.applyTempDBError(err) }) })
	am.td.applyRate = am.tdCollector.SetRate
	am.td.applyPaused = am.tdCollector.SetPaused
	am.td.started, am.td.collecting, am.td.status = true, true, ""
	if am.td.paused {
		am.tdCollector.SetPaused(true)
	}
	am.buildTools()

	collector, ctx, rate := am.tdCollector, conn.Context(), am.td.rate()
	// safegoRepair for the same reason as startActivityCollector's.
	am.app.safegoRepair("collecting tempdb activity",
		func() { am.tempDBCollectorStopped(collector) },
		func() { am.runTempDBCollector(collector, ctx, rate) })
}

// runTempDBCollector is runCollector for the TempDB tab — same two halves,
// same reason.
func (am *ActivityMonitor) runTempDBCollector(c *activity.TempDBCollector, ctx context.Context, rate time.Duration) {
	c.Run(ctx, rate)
	am.app.postAndWake(func() { am.tempDBCollectorStopped(c) })
}

// collectorStopped records that the activity collector's Run returned. It
// checks c against the current collector because Retry starts a new one, and
// the old goroutine's callback must not report that one as stopped.
func (am *ActivityMonitor) collectorStopped(c *activity.Collector) {
	if !am.app.panelHosted(am) || am.collector != c {
		return
	}
	am.act.collecting = false
	if am.act.status == "" {
		am.act.status = collectionStoppedStatus
	}
	am.buildTools()
}

// tempDBCollectorStopped is collectorStopped for the TempDB tab.
func (am *ActivityMonitor) tempDBCollectorStopped(c *activity.TempDBCollector) {
	if !am.app.panelHosted(am) || am.tdCollector != c {
		return
	}
	am.td.collecting = false
	if am.td.status == "" {
		am.td.status = collectionStoppedStatus
	}
	am.buildTools()
}

// restartCollector starts the active tab's collector again after it stopped.
// The connection is still open — only the goroutine died — so a failed
// permission prologue or a dropped tick doesn't cost the whole panel.
func (am *ActivityMonitor) restartCollector() {
	f := am.feed()
	if f.collecting || f.restart == nil {
		return
	}
	f.status = ""
	f.restart()
}

// applyTempDBSample stores a tempdb tick and rebuilds that tab. Runs on the
// UI goroutine, via postAndWake.
func (am *ActivityMonitor) applyTempDBSample(s activity.TempDBSample) {
	if !am.app.panelHosted(am) {
		return
	}
	am.tdStore.Append(s)
	am.td.sampleTime = s.At.Format("15:04:05")
	am.td.status = ""
	am.tempdb = am.buildTempDBView()
	am.invalidateView()
}

// applyTempDBError reports a failed tempdb tick without clearing what has
// already been collected — see applyError.
func (am *ActivityMonitor) applyTempDBError(err error) {
	if !am.app.panelHosted(am) {
		return
	}
	am.td.status = err.Error()
	if errors.Is(err, activity.ErrNoPermission) {
		am.td.collecting = false
		am.buildTools()
	}
}

// applySample stores a tick's result and rebuilds both dashboards from it.
// Runs on the UI goroutine, via postAndWake.
func (am *ActivityMonitor) applySample(s activity.Sample) {
	if !am.app.panelHosted(am) {
		return
	}
	am.store.Append(s)
	am.act.sampleTime = s.At.Format("15:04:05")
	am.act.status = ""
	am.rebuild()
}

// rebuild refreshes the view models the two dashboards draw.
func (am *ActivityMonitor) rebuild() {
	am.history = am.buildHistoryView()
	am.sample = am.buildSampleView()
	am.invalidateView()
}

// invalidateView marks the cached canvas stale, so the next draw re-renders it
// from the replaced view models.
//
// A pinned tooltip is deliberately left alone. A new sample does shift every
// history chart one column left, but moving the box is refreshTooltip's job on
// the next draw, once the render has rebuilt the hit map it re-derives the
// column from. Clearing it here makes a pinned box vanish on the following
// tick, and lets a tempdb tick dismiss a box pinned on History.
func (am *ActivityMonitor) invalidateView() {
	am.viewGen++
}

// applyError shows a collection failure in the header without clearing what was
// already collected: a failed tick against a busy server is ordinary, and
// blanking the dashboard loses the history that explains it. A missing
// permission is different — the collector has stopped, so the panel says so
// rather than sitting there looking idle.
func (am *ActivityMonitor) applyError(err error) {
	if !am.app.panelHosted(am) {
		return
	}
	am.act.status = err.Error()
	if errors.Is(err, activity.ErrNoPermission) {
		am.act.collecting = false
		am.buildTools()
	}
}

// adopt records a connection this panel opened for itself, so Close will
// release it.
func (am *ActivityMonitor) adopt(sc *db.ServerConn) {
	am.owned = append(am.owned, sc)
}

// SetBounds positions the panel: a tab row, a toolbar row, and the rest for
// the active tab's content.
func (am *ActivityMonitor) SetBounds(x, y, w, h int) {
	am.rect = core.Rect{X: x, Y: y, W: w, H: h}
	am.tabRect = core.Rect{X: x, Y: y, W: w, H: 1}
	am.toolRect = core.Rect{X: x, Y: y + 1, W: w, H: 1}
	am.contentRect = core.Rect{X: x, Y: y + 2, W: w, H: max(h-2, 0)}

	// The scrollbars sit outside the viewport permanently rather than appearing
	// on overflow: a bar that comes and goes changes the viewport size, which
	// changes whether the canvas overflows — a layout that oscillates at one
	// specific terminal size.
	am.viewRect = core.Rect{
		X: am.contentRect.X,
		Y: am.contentRect.Y,
		W: max(am.contentRect.W-1, 0),
		H: max(am.contentRect.H-1, 0),
	}
	am.blk.layout()
	am.sess.layout()
	am.buildTools()
}

// canvasSize is the fixed dashboard size for the active tab. A viewport wider
// than the canvas widens the canvas instead of stretching it, buying more time
// buckets on the history charts.
func (am *ActivityMonitor) canvasSize() (int, int) {
	var cw, ch int
	switch am.tab {
	case amTabSample:
		cw, ch = dashboard.SampleCanvasW, dashboard.SampleCanvasH
	case amTabTempDB:
		cw, ch = dashboard.TempDBCanvasW, dashboard.TempDBCanvasH
	default:
		cw, ch = dashboard.HistoryCanvasW, dashboard.HistoryCanvasH
	}
	return max(cw, am.viewRect.W), ch
}

// scrollLimits is the largest scroll offset the active tab allows on each
// axis. Both are zero on a tab with no canvas, which is what makes every
// scrolling key return false there.
func (am *ActivityMonitor) scrollLimits() (maxX, maxY int) {
	if !am.tab.canvasTab() {
		return 0, 0
	}
	cw, ch := am.canvasSize()
	return max(cw-am.viewRect.W, 0), max(ch-am.viewRect.H, 0)
}

// scrollTo moves the active tab's viewport, clamped, and reports whether it
// moved. HandleKey returns that verbatim, so a scroll key at a boundary falls
// through to the App instead of being swallowed.
func (am *ActivityMonitor) scrollTo(x, y int) bool {
	maxX, maxY := am.scrollLimits()
	x = core.Clamp(x, 0, maxX)
	y = core.Clamp(y, 0, maxY)
	if x == am.scrollX[am.tab] && y == am.scrollY[am.tab] {
		return false
	}
	am.scrollX[am.tab], am.scrollY[am.tab] = x, y
	// A pinned tooltip is dropped on a pan: a box that follows is in the way of
	// whatever the user scrolled to see.
	am.tooltip = nil
	return true
}

// scrollBy is scrollTo relative to the current position.
func (am *ActivityMonitor) scrollBy(dx, dy int) bool {
	return am.scrollTo(am.scrollX[am.tab]+dx, am.scrollY[am.tab]+dy)
}

// setTab switches tabs, rebuilding the toolbar for the new tab's controls.
func (am *ActivityMonitor) setTab(t amTab) {
	if t < 0 || t >= amTabCount || t == am.tab {
		return
	}
	am.tab = t
	am.tooltip = nil
	am.buildTools()
	if pt := am.procTab(); pt != nil {
		pt.activate()
	}
}

// feed is the collector state the active tab reads and writes: the TempDB tab's
// own, or the activity feed behind History and Sample. The procedure-backed
// tabs have no feed and get the activity one, which their header and status
// line already show.
func (am *ActivityMonitor) feed() *amFeed {
	if am.tab == amTabTempDB {
		return &am.td
	}
	return &am.act
}

// setRate selects a refresh interval for the active tab's feed, by index
// into that feed's own rate list, and reports whether anything changed.
func (am *ActivityMonitor) setRate(i int) bool {
	if !am.feed().setRate(i) {
		return false
	}
	am.buildTools()
	return true
}

// setPaused pauses or resumes the active tab's feed. The two feeds are
// independent; on the dashboards one collector serves History and Sample
// together.
func (am *ActivityMonitor) setPaused(v bool) {
	am.feed().setPaused(v)
	am.buildTools()
}

// buildTools rebuilds the toolbar for the active tab: rate selector and
// Pause/Continue on the dashboards, a manual Refresh on the procedure-backed
// ones. Positions come from toolRect, so SetBounds calls this too.
func (am *ActivityMonitor) buildTools() {
	am.tools = am.tools[:0]
	switch {
	case am.tab.canvasTab():
		// One arm for both dashboards: the toolbar reads neither the rate list
		// nor the collector directly.
		f := am.feed()
		am.toolPrefix = f.prefix
		off := f.stopped()
		for i, label := range f.rateLabels {
			am.tools = append(am.tools, toolButton{
				label:    label,
				selected: i == f.rateIdx,
				disabled: off,
				action:   func() { am.setRate(i) },
			})
		}
		if off {
			// Pause has nothing left to pause once the collector stopped.
			// Retry is the one control that can still act; without it a single
			// failed prologue leaves the panel a static picture.
			am.tools = append(am.tools, toolButton{label: "Retry", action: am.restartCollector})
			break
		}
		label := "Pause"
		if f.paused {
			label = "Continue"
		}
		am.tools = append(am.tools, toolButton{label: label, action: func() { am.setPaused(!f.paused) }})
	default:
		pt := am.procTab()
		am.toolPrefix = ""
		am.tools = append(am.tools, toolButton{
			label:    "Refresh",
			disabled: pt.busy,
			action:   pt.refresh,
		})
		// Offered only while the procedure isn't already in master, where the
		// button would have nothing to do but write to a system database.
		if pt.loc != activity.ProcMaster {
			// Installing a procedure in master is a sysadmin act, and this
			// button used to offer it to anyone: clicking it as a db_owner
			// returned "Cannot alter the procedure 'sp_block', because it does
			// not exist or you do not have permission." CONTROL SERVER rather
			// than a role test, because HAS_PERMS_BY_NAME answers 1 for a
			// sysadmin while IS_SRVROLEMEMBER does not fold sysadmin into
			// anything else.
			denied := !allowsAction(pt.conn, "", rightControlServer)
			am.tools = append(am.tools, toolButton{
				label:    "Install in master",
				disabled: pt.busy || pt.conn == nil || denied,
				reason:   requiresText(rightControlServer),
				action:   pt.confirmInstallInMaster,
			})
		}
	}
	am.layoutTools()
}

// layoutTools assigns each control its screen rect, collapsing whatever does
// not fit into the "More ▾" menu — see layoutToolButtonsOverflow.
func (am *ActivityMonitor) layoutTools() {
	am.hidden, am.toolsEnd = layoutToolButtonsOverflow(am.tools, am.toolRect, am.toolPrefix, &am.more)
}

// prefixVisible reports whether the rate selector's label is drawn. The "More
// ▾" cell takes its place on a row too narrow for both — see
// layoutToolButtonsOverflow — and the two would otherwise overlap, leaving the
// tail of the label beside the cell that replaced it.
func (am *ActivityMonitor) prefixVisible() bool {
	return am.more.rect.IsZero() || am.more.rect.X > am.toolRect.X+1
}

// runTool invokes toolbar cell i's action, or says why it did not — the one
// gate behind the click path and the overflow menu, so a control withheld on
// the row is withheld in the menu for the same stated reason.
func (am *ActivityMonitor) runTool(i int) {
	switch t := am.tools[i]; {
	case t.disabled && t.reason != "":
		am.app.setStatus(t.reason)
	case t.action != nil && !t.disabled:
		t.action()
	}
}

// showOverflowMenu pops the controls the row was too narrow to draw, under the
// "More ▾" cell.
func (am *ActivityMonitor) showOverflowMenu() {
	r := am.more.rect
	if r.IsZero() {
		r = core.Rect{X: am.rect.X, Y: am.rect.Y}
	}
	am.app.contextMenu.Show(r.X, r.Y+1, toolOverflowItems(am.tools, am.hidden,
		func(i int) bool { return am.tools[i].disabled },
		func(i int) string { return am.tools[i].reason },
		am.runTool))
}

// resolution names the active tab's sampling interval as the dashboards show
// it, from that tab's own feed: reporting the activity rate on TempDB would
// credit its columns with a resolution they don't have.
func (am *ActivityMonitor) resolution() string {
	return fmt.Sprintf("%d sec", int(am.feed().rate().Seconds()))
}

// collectionState is the one-line summary the toolbar shows on the right:
// whether collection runs, which sample is on screen, and how far apart samples
// are. Longest variant first — the caller draws the first that fits, so a
// narrow panel loses the collector's message before the fact that collection
// has stopped.
func (am *ActivityMonitor) collectionState() []string {
	f := am.feed()
	paused, collecting, sampleTime, status := f.paused, f.collecting, f.sampleTime, f.status
	state := "not collecting"
	switch {
	case paused:
		state = "PAUSED"
	case collecting:
		state = "collecting"
	}
	if sampleTime != "" {
		state += "  " + sampleTime
	}
	short := state + "  (" + am.resolution() + ")"
	if status == "" {
		return []string{short, state}
	}
	return []string{status + "  \u2014  " + short, short, state}
}

// header is the identification strip both dashboards draw, built from the
// connection and the collector's current state.
func (am *ActivityMonitor) header() dashboard.Header {
	f := am.feed()
	h := dashboard.Header{
		Instance:   am.conn.Opts.Server,
		Resolution: am.resolution(),
		SampleTime: f.sampleTime,
		Status:     f.status,
		Paused:     f.paused,
	}
	if am.conn.Server != nil && am.conn.Server.Info() != nil {
		info := am.conn.Server.Info()
		h.Version = "SQL Server " + info.ProductVersion
		h.Host = info.Edition
	}
	return h
}
