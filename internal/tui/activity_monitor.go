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
// dashboard too but runs its own collector, so it is deliberately not one of
// these.
func (t amTab) dashboardTab() bool { return t == amTabHistory || t == amTabSample }

// canvasTab reports whether t draws a scrolling dashboard canvas rather
// than a result grid. It gates every scrolling key and gesture, and the
// toolbar's rate/Pause arm — the three canvas tabs are exactly the tabs
// with a feed.
func (t amTab) canvasTab() bool { return t.dashboardTab() || t == amTabTempDB }

// amRates are the refresh intervals the rate selector offers, matching the
// work order's 2/3/5/10 seconds.
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
// returned without reporting why — the cancelled-context exit. An exit that
// did report goes through applyError and keeps that message instead.
const collectionStoppedStatus = "Collection stopped."

// noSamplesStatus is what a feed reports before its first tick lands.
const noSamplesStatus = "No samples collected yet."

// amTempDBRates are the TempDB tab's own intervals. They are an order of
// magnitude longer than the activity rates because tempdb space is a level
// that moves over minutes, and because the object enumeration each tick
// performs reads tempdb's own metadata — the very thing a contended tempdb
// has too little of.
var amTempDBRates = []time.Duration{
	10 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

var amTempDBRateLabels = []string{"10 s", "30 s", "60 s"}

// defaultTempDBRateIdx is 30 seconds: fast enough to watch a version store
// grow, slow enough that the metadata read is never the problem.
const defaultTempDBRateIdx = 1

// ActivityMonitor is the Activity Monitor panel: a five-tab view of one
// server's live activity, hosted by layout.PanelManager like any other
// panel. History, Sample and TempDB render dashboards from
// internal/tui/dashboard into an off-screen canvas of fixed size, which the
// panel scrolls a viewport over; Sessions and Block are result grids over
// sp_WhoIsActive and the blocking procedure.
//
// Unlike the work order's two independent collectors, one collector feeds
// both dashboards — Sample is the newest sample in History's store — so
// there is a single refresh rate and a single Pause/Continue governing both
// (see docs/plan-activity-monitor.md, deviation 1). TempDB is the exception
// and keeps its own collector, store and rate.
type ActivityMonitor struct {
	app  *App
	conn *db.ServerConn // the server being watched; owned by App, not by this panel

	rect   core.Rect
	active bool

	tab amTab

	// scrollX/scrollY are per tab, so switching tabs and coming back
	// returns to where the user was rather than to the top-left.
	scrollX [amTabCount]int
	scrollY [amTabCount]int

	// act and td are the collector-facing state of the two dashboards: the
	// activity feed behind History and Sample, and the TempDB tab's own. Every
	// tab-dependent read of a rate, a pause, a status or a sample time goes
	// through feed(), so neither has to be spelled twice.
	act amFeed
	td  amFeed

	// store holds every sample collected, and collector is the goroutine
	// filling it. One collector feeds both dashboards: History plots the
	// store, Sample draws its newest entry.
	store     activity.Store
	collector *activity.Collector

	history dashboard.HistoryView
	sample  dashboard.SampleView

	// The TempDB tab keeps its own store and collector as well as its own
	// feed: it ticks in tens of seconds against a store that retains hours,
	// and nothing about it is shared with the two activity dashboards.
	tdStore     activity.TempDBStore
	tdCollector *activity.TempDBCollector
	tempdb      dashboard.TempDBView

	// blk and sess are the two procedure-backed tabs: a result grid over one
	// run of sp_block and of sp_WhoIsActive. Each opens its own connection the
	// first time its tab is shown, and neither ever refreshes on a timer.
	blk  *amProcTab
	sess *amProcTab

	// feedConn is the connection both dashboard collectors run on — the one
	// the panel dialled for itself, also held in owned. Kept under its own
	// name so a collector whose Run has returned can be started again on it
	// without reopening the panel.
	feedConn *db.ServerConn

	// owned are connections this panel opened for itself and must close on
	// teardown — the per-tab connections of the work order's connection
	// model, opened lazily as each tab gains real queries. Empty until then.
	owned []*db.ServerConn

	tabRect     core.Rect
	toolRect    core.Rect
	contentRect core.Rect // everything below the toolbar, scrollbars included
	viewRect    core.Rect // the dashboard viewport, scrollbars excluded

	tools      []amTool
	toolPrefix string
	// toolsEnd is the column just past the last laid-out control, so the
	// right-aligned collector state can be fitted into what's left rather
	// than drawn under the buttons.
	toolsEnd int

	// hits is where each History chart's plot landed on the canvas, recorded
	// by the last draw. Canvas coordinates, not screen ones — the viewport
	// scrolls over them.
	hits []dashboard.ChartHit

	// canvas is the last rendered dashboard, kept between frames, and
	// canvasKey is the state it was rendered from. Draw runs on every event
	// the application handles, and re-rendering all eleven charts each time
	// costs milliseconds; the view models only move when a sample lands.
	canvas    *charts.Canvas
	canvasKey amCanvasKey

	// viewGen counts rebuilds of the view models. It is what tells the
	// cached canvas it is stale: the stores' own Len stops changing once
	// retention starts pruning, while their contents keep scrolling.
	viewGen uint64

	// tooltip is the readout pinned by the last click, nil when none is
	// showing. It survives redraws, so a paused dashboard keeps the numbers
	// the user clicked on.
	tooltip *amTooltip

	dragZone  amDragZone
	vDragging bool
	hDragging bool
}

// amFeed is one dashboard's collector-facing state: its rate list and
// selection, its Pause, whether its collector is running, and what it last
// reported. The activity feed (History and Sample, which share one collector
// — see the panel's doc comment) and the TempDB feed differ in their rates
// and in which collector they drive, and in nothing else, so this is held
// twice rather than written twice.
//
// The collectors are separate types, so reaching one is a closure rather
// than a field: applyRate/applyPaused are re-pointed at each new collector
// by the panel's start methods, and are nil until one has been started.
type amFeed struct {
	prefix     string // the toolbar's label for this feed's rate selector
	rates      []time.Duration
	rateLabels []string

	rateIdx int
	paused  bool

	// started is true from the moment a collector was first created for this
	// feed. The toolbar gate is started && !collecting — never !collecting
	// alone, since paused is a preference the panel carries into the start
	// and a panel still connecting has to keep its controls live.
	started bool

	// collecting is true while the collector goroutine is running. It is
	// what the toolbar reports, so the panel never claims to be sampling a
	// server it isn't.
	collecting bool

	// status is the collector's last non-fatal message, shown in the
	// dashboard header rather than replacing the dashboard.
	status string

	// sampleTime is the clock time of the sample currently on screen,
	// empty until the first one is collected.
	sampleTime string

	applyRate   func(time.Duration)
	applyPaused func(bool)
	// restart starts a new collector for this feed on the connection the
	// panel already owns — what the Retry control calls.
	restart func()
}

// rate is the interval this feed's collector ticks at.
func (f *amFeed) rate() time.Duration { return f.rates[f.rateIdx] }

// setRate selects an interval by index into the feed's rates, and reports
// whether anything changed — an index off either end is a no-op, not a
// wrap, so the toolbar and the +/- keys agree about the ends of the list.
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

// stopped reports a collector that started and has since returned — the
// state in which Pause can no longer do anything, because a stopped
// collector drops every send.
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

// amTooltip is a pinned readout of one sample: every series of one chart at
// the bucket that was clicked, in the chart's own colours.
type amTooltip struct {
	time string
	rows []amTooltipRow
	// anchor is where the click landed, in screen coordinates. The box is
	// placed beside it and flipped when it would fall off the viewport.
	anchor core.Rect
}

type amTooltipRow struct {
	label string
	value string
	color tcell.Color
}

// amTool is one clickable toolbar control. Rate buttons render selected;
// the rest are plain.
//
// disabled draws the control dimmed and makes a click on it do nothing —
// what the rate controls become once their collector has *started and then
// stopped* (amFeed.stopped). Not before it starts: Pause is a preference the
// panel carries into startActivityCollector, so a panel still connecting
// keeps its controls live.
type amTool struct {
	label    string
	selected bool
	disabled bool
	action   func()
	rect     core.Rect
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

// Close releases everything the panel owns: the collector goroutine, its
// per-tab connections, and the collected samples. Called from
// App.closePanelAt — nothing here is persisted.
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
	// The tempdb copies of the two procedures are deliberately left where they
	// are: they cost nothing, survive until the next SQL Server restart, and
	// dropping them would make every reopening of this panel reinstall them.
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
	// Dropped outright rather than left for refreshTooltip: the views are
	// now empty, so it would resolve to nothing on the next draw anyway, and
	// a closed panel keeping a box of numbers pinned is not a state worth
	// carrying to a reopening.
	am.tooltip = nil
	am.canvas = nil
}

// startCollector takes ownership of the panel's own connection and starts
// both dashboard collectors against it. Called once, from the connect
// callback; each collector can be started again on its own from there (see
// restartCollector).
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
	am.app.safego("collecting server activity", func() { am.runCollector(collector, ctx, rate) })
}

// runCollector is the collector goroutine's whole body: collect, then tell
// the panel it has stopped.
//
// The second half is what keeps am.collecting honest. Run reports only one
// of its three exits through onError (ErrNoPermission); a failed permission
// prologue and a cancelled context both return silently, and without this
// the toolbar went on claiming to be collecting, with a live Pause whose
// every send the stopped collector dropped.
func (am *ActivityMonitor) runCollector(c *activity.Collector, ctx context.Context, rate time.Duration) {
	c.Run(ctx, rate)
	am.app.postAndWake(func() { am.collectorStopped(c) })
}

// startTempDBCollector starts the TempDB tab's own collector on the same
// connection's pool. A second goroutine on the same *sql.DB gets its own
// physical connection, which is what keeps a slow tempdb tick from delaying
// an activity tick.
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
	am.app.safego("collecting tempdb activity", func() { am.runTempDBCollector(collector, ctx, rate) })
}

// runTempDBCollector is runCollector for the TempDB tab — same two halves,
// same reason.
func (am *ActivityMonitor) runTempDBCollector(c *activity.TempDBCollector, ctx context.Context, rate time.Duration) {
	c.Run(ctx, rate)
	am.app.postAndWake(func() { am.tempDBCollectorStopped(c) })
}

// collectorStopped records that the activity collector's Run has returned.
// It is checked against the current collector because a Retry starts a new
// one: the old goroutine's callback arrives after the new collector is
// already running, and must not report that one as stopped.
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
// The connection is still the panel's own and still open — what died is the
// goroutine, so a failed permission prologue or a dropped tick no longer
// costs the whole panel.
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

// invalidateView marks the cached canvas stale, so the next draw re-renders
// it from the view models that have just been replaced.
//
// A pinned tooltip is deliberately left alone here. New view models do put
// different numbers under it — that desync is real — but it is the *box* that
// has to move on, not the pin: refreshTooltip re-resolves it from its anchor
// on the next draw, once the render has rebuilt the hit map. Clearing it here
// instead was the first fix, and it made a pinned box vanish on the following
// tick (two seconds at the default rate) and, because both collectors land
// here, let a tempdb tick dismiss a box pinned on History.
func (am *ActivityMonitor) invalidateView() {
	am.viewGen++
}

// applyError shows a collection failure in the header without clearing what
// has already been collected — a failed tick against a busy server is
// ordinary, and blanking the dashboard would lose the history that explains
// it. A missing permission is different: the collector has stopped, so the
// panel has to say so rather than sit there looking idle.
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
	am.contentRect = core.Rect{X: x, Y: y + 2, W: w, H: core.Max(h-2, 0)}

	// The scrollbars sit outside the viewport permanently rather than
	// appearing only when the canvas overflows: a bar that comes and goes
	// changes the viewport size, which changes whether the canvas
	// overflows, which is a layout that can oscillate at one specific
	// terminal size.
	am.viewRect = core.Rect{
		X: am.contentRect.X,
		Y: am.contentRect.Y,
		W: core.Max(am.contentRect.W-1, 0),
		H: core.Max(am.contentRect.H-1, 0),
	}
	am.blk.layout()
	am.sess.layout()
	am.buildTools()
}

// canvasSize is the fixed dashboard size for the active tab. A viewport
// wider than the canvas widens the canvas instead of stretching it, which
// buys more time buckets on the history charts.
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
	return core.Max(cw, am.viewRect.W), ch
}

// scrollLimits is the largest scroll offset the active tab allows on each
// axis. Both are zero on a tab with no canvas, which is what makes every
// scrolling key return false there.
func (am *ActivityMonitor) scrollLimits() (maxX, maxY int) {
	if !am.tab.canvasTab() {
		return 0, 0
	}
	cw, ch := am.canvasSize()
	return core.Max(cw-am.viewRect.W, 0), core.Max(ch-am.viewRect.H, 0)
}

// scrollTo moves the active tab's viewport, clamped, and reports whether it
// actually moved — HandleKey returns that verbatim, so a scroll key at a
// boundary falls through to the App instead of being silently swallowed.
func (am *ActivityMonitor) scrollTo(x, y int) bool {
	maxX, maxY := am.scrollLimits()
	x = core.Clamp(x, 0, maxX)
	y = core.Clamp(y, 0, maxY)
	if x == am.scrollX[am.tab] && y == am.scrollY[am.tab] {
		return false
	}
	am.scrollX[am.tab], am.scrollY[am.tab] = x, y
	// A pinned tooltip is anchored to a spot in the viewport, and the canvas
	// has just moved under it: left up, it would point at a column whose
	// numbers are not the ones in the box.
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

// feed is the collector state the active tab reads and writes: the TempDB
// tab's own, or the activity feed behind History and Sample. The
// procedure-backed tabs have no feed of their own and get the activity one,
// which is what the tab-independent header and status line already showed
// there.
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
// independent — pausing TempDB while watching History is a normal thing to
// want — and on the dashboards one collector serves History and Sample
// together.
func (am *ActivityMonitor) setPaused(v bool) {
	am.feed().setPaused(v)
	am.buildTools()
}

// buildTools rebuilds the toolbar for the active tab: the rate selector and
// Pause/Continue on the dashboards, a manual Refresh on the procedure-backed
// ones. Positions come from toolRect, so SetBounds calls this too.
func (am *ActivityMonitor) buildTools() {
	am.tools = am.tools[:0]
	switch {
	case am.tab.canvasTab():
		// One arm for both dashboards: the feeds differ in their rate lists
		// and in which collector they drive, and the toolbar reads neither
		// directly.
		f := am.feed()
		am.toolPrefix = f.prefix
		off := f.stopped()
		for i, label := range f.rateLabels {
			am.tools = append(am.tools, amTool{
				label:    label,
				selected: i == f.rateIdx,
				disabled: off,
				action:   func() { am.setRate(i) },
			})
		}
		if off {
			// Pause has nothing left to pause: its every send is dropped by a
			// stopped collector. Retry is the one control that can still do
			// something, and without it a single failed prologue left the
			// panel a static picture until it was closed and reopened.
			am.tools = append(am.tools, amTool{label: "Retry", action: am.restartCollector})
			break
		}
		label := "Pause"
		if f.paused {
			label = "Continue"
		}
		am.tools = append(am.tools, amTool{label: label, action: func() { am.setPaused(!f.paused) }})
	default:
		pt := am.procTab()
		am.toolPrefix = ""
		am.tools = append(am.tools, amTool{
			label:    "Refresh",
			disabled: pt.busy,
			action:   pt.refresh,
		})
		// Offered only while the procedure isn't already in master: with a
		// master copy in use there is nothing left for the button to do, and
		// leaving it there would invite a pointless write to a system
		// database.
		if pt.loc != activity.ProcMaster {
			am.tools = append(am.tools, amTool{
				label:    "Install in master",
				disabled: pt.busy || pt.conn == nil,
				action:   pt.confirmInstallInMaster,
			})
		}
	}
	am.layoutTools()
}

// toolGap is the blank column between two toolbar controls; a control's own
// label is drawn with one space of padding either side.
const toolGap = 1

// layoutTools assigns each control its screen rect, left to right after the
// prefix. A control that would run past the right edge gets a zero rect and
// is neither drawn nor clickable.
func (am *ActivityMonitor) layoutTools() {
	x := am.toolRect.X + 1
	defer func() { am.toolsEnd = x }()
	if am.toolPrefix != "" {
		x += core.DisplayWidth(am.toolPrefix) + 1
	}
	for i := range am.tools {
		w := core.DisplayWidth(am.tools[i].label) + 2
		if am.toolRect.W == 0 || x+w > am.toolRect.Right() {
			am.tools[i].rect = core.Rect{}
			continue
		}
		am.tools[i].rect = core.Rect{X: x, Y: am.toolRect.Y, W: w, H: 1}
		x += w + toolGap
	}
}

// resolution names the active tab's sampling interval the way the
// dashboards show it. It reads the active tab's own feed: TempDB has its own
// collector, and reporting the activity rate there would credit tempdb
// columns with a resolution they don't have.
func (am *ActivityMonitor) resolution() string {
	return fmt.Sprintf("%d sec", int(am.feed().rate().Seconds()))
}

// collectionState is the one-line summary the toolbar repeats on the right:
// whether collection is running, which sample is on screen, and how far
// apart samples are. It comes back longest first — the caller draws the
// first variant that fits beside the toolbar's controls, so a narrow panel
// loses the collector's message before it loses the fact that collection is
// stopped.
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
