package tui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tui/dashboard"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// amTab identifies an Activity Monitor tab. Which tabs a connection offers is
// visibleTabs' concern.
type amTab int

const (
	amTabHistory amTab = iota
	amTabSample
	amTabTempDB
	amTabSessions
	amTabBlock
	amTabInstance
	amTabCount = 6
)

// amTabLabels are the tab-bar labels, indexed by amTab.
var amTabLabels = [amTabCount]string{"History", "Sample", "TempDB", "Sessions", "Block", "Instance"}

// amAllTabs is every tab in bar order. Per-tab arrays stay indexed by amTab and
// sized amTabCount (an unoffered tab just keeps an unreachable scroll
// position); only the bar is a slice.
var amAllTabs = []amTab{amTabHistory, amTabSample, amTabTempDB, amTabSessions, amTabBlock, amTabInstance}

// dashboardTab reports whether t is fed by the shared activity collector
// (History and Sample, one rate and Pause). TempDB and Instance have their own
// feeds.
func (t amTab) dashboardTab() bool { return t == amTabHistory || t == amTabSample }

// canvasTab reports whether t draws a scrolling canvas rather than a grid;
// gates scrolling input and the toolbar's rate/Pause arm.
func (t amTab) canvasTab() bool { return t.dashboardTab() || t == amTabTempDB || t == amTabInstance }

// azureOnly reports whether t needs an Azure engine edition. Instance reads
// sys.server_resource_stats, sys.dm_instance_resource_governance and
// sys.dm_os_job_object, which exist only there (gosmo refuses them elsewhere).
func (t amTab) azureOnly() bool { return t == amTabInstance }

// amRates are the refresh intervals the rate selector offers.
var amRates = []time.Duration{
	2 * time.Second,
	3 * time.Second,
	5 * time.Second,
	10 * time.Second,
}

// amRateLabels label the amRates entries.
var amRateLabels = []string{"2 s", "3 s", "5 s", "10 s"}

// defaultRateIdx is a new panel's interval.
const defaultRateIdx = 0

// collectionStoppedStatus is the header text when Run returned without a reason
// (cancelled context). Reported exits go through applyError.
const collectionStoppedStatus = "Collection stopped."

// noSamplesStatus is what a feed reports before its first tick lands.
const noSamplesStatus = "No samples collected yet."

// amTempDBRates are the TempDB tab's intervals, far longer than activity rates:
// tempdb space moves over minutes, and each tick reads tempdb metadata, which a
// contended tempdb lacks.
var amTempDBRates = []time.Duration{
	10 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

var amTempDBRateLabels = []string{"10 s", "30 s", "60 s"}

// defaultTempDBRateIdx is 30 seconds: enough to watch a version store grow
// without the metadata read becoming the problem.
const defaultTempDBRateIdx = 1

// ActivityMonitor is a multi-tab view of one server's live activity, hosted by
// layout.PanelManager. History, Sample and TempDB render internal/tui/dashboard
// into a fixed-size off-screen canvas scrolled by a viewport; Sessions and
// Block are grids over sp_WhoIsActive and sp_block.
//
// One collector feeds History and Sample (Sample draws History's newest
// sample), so one rate and Pause govern both. TempDB has its own collector,
// store and rate.
type ActivityMonitor struct {
	app  *App
	conn *db.ServerConn // the server being watched; owned by App, not by this panel

	rect   core.Rect
	active bool

	tab amTab

	// scrollX/scrollY are per tab, so returning to a tab restores its position.
	scrollX [amTabCount]int
	scrollY [amTabCount]int

	// act, td and inst are the collector state of the three dashboard feeds
	// (History/Sample, TempDB, Instance). Tab-dependent reads go through
	// feed().
	act  amFeed
	td   amFeed
	inst amInstanceFeed

	// store holds collected samples; collector fills it. History plots the
	// store, Sample draws its newest entry.
	store     activity.Store
	collector *activity.Collector

	history dashboard.HistoryView
	sample  dashboard.SampleView

	// The TempDB tab's own store and collector (slow ticks, hours retained).
	tdStore     activity.TempDBStore
	tdCollector *activity.TempDBCollector
	tempdb      dashboard.TempDBView

	// The Instance tab's poller and view. No store: the server keeps the
	// history and one tick reads it whole (see activity_monitor_instance.go).
	instPoller *activity.Poller[amInstanceSample]
	instance   dashboard.InstanceView

	// blk and sess are grids over one run of sp_block and sp_WhoIsActive. Each
	// opens its own connection when first shown; neither auto-refreshes.
	blk  *amProcTab
	sess *amProcTab

	// feedConn is the dashboard collectors' connection (also in owned), kept
	// separately so a stopped collector can restart without reopening the
	// panel.
	feedConn *db.ServerConn

	// owned are connections this panel opened and must close on teardown.
	owned []*db.ServerConn

	tabRect     core.Rect
	toolRect    core.Rect
	contentRect core.Rect // everything below the toolbar, scrollbars included
	viewRect    core.Rect // the dashboard viewport, scrollbars excluded

	// tools are the panel's toolbar cells (panel_toolbar.go, not App's icon
	// strip). Rate controls disable only once their collector has started and
	// stopped (amFeed.stopped): Pause is a preference carried into
	// startActivityCollector, so a connecting panel keeps them live.
	tools      []toolButton
	toolPrefix string
	// toolsEnd is the column past the last control (More included); the
	// collector state fits into what's left.
	toolsEnd int

	// more is the "More ▾" cell holding controls the row is too narrow for
	// (indexes in hidden). A dashboard row needs 47 columns and the pane gets
	// 70% of the terminal, so Pause (no key binding) would otherwise vanish
	// below 68 columns.
	more   toolButton
	hidden []int

	// hits records where each History chart's plot landed, in canvas
	// coordinates, on the last draw.
	hits []dashboard.ChartHit

	// canvas is the last rendered dashboard and canvasKey its inputs. Draw runs
	// every event and a full render costs milliseconds, while views change only
	// per sample.
	canvas    *charts.Canvas
	canvasKey amCanvasKey

	// viewGen counts view-model rebuilds and marks the canvas stale: store Len
	// stops changing once pruning starts, but contents keep moving.
	viewGen uint64

	// tooltip is the readout pinned by the last click, nil if none. Survives
	// redraws, so a paused dashboard keeps it.
	tooltip *amTooltip

	dragZone  amDragZone
	vDragging bool
	hDragging bool
}

// amFeed is one dashboard's collector state: rates, Pause, running, last
// report. Activity and TempDB feeds differ only in rates and collector.
//
// Collectors are different types, so applyRate/applyPaused are closures
// re-pointed by each start method; nil until started.
type amFeed struct {
	prefix     string // the toolbar's label for this feed's rate selector
	rates      []time.Duration
	rateLabels []string

	rateIdx int
	paused  bool

	// started is true once a collector exists. The toolbar gate is started &&
	// !collecting: paused carries into the start, so a connecting panel keeps
	// controls live.
	started bool

	// collecting is true while the collector goroutine runs.
	collecting bool

	// status is the collector's last non-fatal message, shown in the dashboard
	// header.
	status string

	// sampleTime is the on-screen sample's clock time, empty until the first.
	sampleTime string

	applyRate   func(time.Duration)
	applyPaused func(bool)
	// restart starts a new collector on the panel's connection (the Retry
	// control).
	restart func()
}

// rate is this feed's tick interval.
func (f *amFeed) rate() time.Duration { return f.rates[f.rateIdx] }

// setRate selects an interval by index and reports whether it changed.
// Out-of-range is a no-op, not a wrap.
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

// stopped reports a collector that started and has returned; Pause then does
// nothing.
func (f *amFeed) stopped() bool { return f.started && !f.collecting }

// amInstanceFeed is the Instance tab's feed plus its single reading. No
// activity.Store: sys.server_resource_stats is the history and each tick
// replaces it.
type amInstanceFeed struct {
	amFeed
	sample amInstanceSample
}

// amCanvasKey is everything a rendered canvas depends on; any change forces a
// redraw.
type amCanvasKey struct {
	tab      amTab
	w, h     int
	gen      uint64
	header   dashboard.Header
	interval time.Duration
}

// amTooltip is a pinned readout of one sample: every series of a chart at the
// clicked bucket, in chart colours.
//
// It pins the sample (chart and time), not the screen spot; its position is
// re-derived each draw, since new samples push history columns left.
type amTooltip struct {
	chart string // ChartHit.Title of the chart the pin lives on
	time  string // the pinned bucket's clock time — its identity and caption
	rows  []tooltipRow

	// snapshot marks a pin on a current-sample chart: one bucket, no time axis,
	// so nothing drifts.
	snapshot bool

	// col and row are the pinned point in canvas coordinates (bucket column,
	// click row within the plot), translated at draw time.
	col, row int

	// plot and timeRow are the pinned chart's canvas rects, re-read each draw:
	// the pin drops outside plot, and the time callout sits on timeRow.
	plot, timeRow core.Rect
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
		inst: amInstanceFeed{amFeed: amFeed{
			prefix:     "Instance rate:",
			rates:      amInstanceRates,
			rateLabels: amInstanceRateLabels,
			rateIdx:    defaultInstanceRateIdx,
			status:     noSamplesStatus,
		}},
	})
	am.act.restart = am.startActivityCollector
	am.td.restart = am.startTempDBCollector
	am.inst.restart = am.startInstancePoller
	am.blk = am.newProcTab(activity.BlockProc, "")
	am.sess = am.newProcTab(activity.WhoIsActiveProc, whoIsActiveCredit())
	am.buildTools()
	return am
}

// Title implements Panel.
func (am *ActivityMonitor) Title() string { return "Activity Monitor" }

// SetActive implements Activatable.
func (am *ActivityMonitor) SetActive(v bool) { am.active = v }

// Close releases the collector goroutines, per-tab connections and samples.
func (am *ActivityMonitor) Close() {
	if am.collector != nil {
		am.collector.Stop()
		am.collector = nil
	}
	if am.tdCollector != nil {
		am.tdCollector.Stop()
		am.tdCollector = nil
	}
	if am.instPoller != nil {
		am.instPoller.Stop()
		am.instPoller = nil
	}
	am.act.collecting = false
	am.td.collecting = false
	am.inst.collecting = false
	for _, sc := range am.owned {
		sc.Close()
	}
	am.owned = nil
	am.feedConn = nil
	// The tempdb procedure copies stay: harmless, gone at restart, and dropping
	// them means reinstalling on every reopen.
	for _, pt := range []*amProcTab{am.blk, am.sess} {
		pt.conn = nil
		pt.grid = nil
		pt.result = nil
	}
	am.store.Reset()
	am.tdStore.Reset()
	am.inst.sample = amInstanceSample{}
	am.history = dashboard.HistoryView{}
	am.sample = dashboard.SampleView{}
	am.tempdb = dashboard.TempDBView{}
	am.instance = dashboard.InstanceView{}
	am.invalidateView()
	// The views are empty now, so drop it outright.
	am.tooltip = nil
	am.canvas = nil
}

// startCollector takes ownership of the connection and starts both dashboard
// collectors. Called once from the connect callback; each restarts
// independently afterwards.
func (am *ActivityMonitor) startCollector(conn *db.ServerConn) {
	am.adopt(conn)
	am.feedConn = conn
	am.startActivityCollector()
	am.startTempDBCollector()
	am.startInstancePoller()
}

// startActivityCollector starts the History/Sample collector.
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
	// safegoRepair, not safego: am.act.collecting is cleared only by
	// runCollector's second half, which a panic skips; collectorStopped is
	// guarded on the collector.
	am.app.safegoRepair("collecting server activity",
		func() { am.collectorStopped(collector) },
		func() { am.runCollector(collector, ctx, rate) })
}

// runCollector runs the collector, then tells the panel it stopped. Run reports
// only ErrNoPermission via onError; a failed prologue or cancelled context
// return silently, and without this the toolbar would claim to collect with a
// dead Pause.
func (am *ActivityMonitor) runCollector(c *activity.Collector, ctx context.Context, rate time.Duration) {
	c.Run(ctx, rate)
	am.app.postAndWake(func() { am.collectorStopped(c) })
}

// startTempDBCollector starts the TempDB collector on the same pool; its own
// physical connection means slow tempdb ticks never delay activity ticks.
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
	// safegoRepair, as in startActivityCollector.
	am.app.safegoRepair("collecting tempdb activity",
		func() { am.tempDBCollectorStopped(collector) },
		func() { am.runTempDBCollector(collector, ctx, rate) })
}

// startInstancePoller starts the Instance tab's poller on the same pool. A
// Poller, not a Collector, because the reading is already a history (see
// activity_monitor_instance.go).
//
// No-op off Azure: the views don't exist and visibleTabs withholds the tab.
func (am *ActivityMonitor) startInstancePoller() {
	conn := am.feedConn
	if conn == nil || conn.Server == nil || !serverIsAzure(conn) {
		return
	}
	// Captured outside the probe, which runs on the poller's goroutine and
	// mustn't touch am.
	srv := conn.Server
	am.instPoller = activity.NewPoller(srv.DB(),
		func(ctx context.Context) (*amInstanceSample, error) { return probeInstance(ctx, srv) },
		func(s amInstanceSample) { am.app.postAndWake(func() { am.applyInstanceSample(s) }) },
		func(err error) { am.app.postAndWake(func() { am.applyInstanceError(err) }) })
	am.inst.applyRate = am.instPoller.SetRate
	am.inst.applyPaused = am.instPoller.SetPaused
	am.inst.started, am.inst.collecting, am.inst.status = true, true, ""
	if am.inst.paused {
		am.instPoller.SetPaused(true)
	}
	am.buildTools()

	poller, ctx, rate := am.instPoller, conn.Context(), am.inst.rate()
	// safegoRepair, as in startActivityCollector.
	am.app.safegoRepair("reading Azure instance resources",
		func() { am.instancePollerStopped(poller) },
		func() { am.runInstancePoller(poller, ctx, rate) })
}

// runInstancePoller is runCollector for the Instance tab.
func (am *ActivityMonitor) runInstancePoller(p *activity.Poller[amInstanceSample], ctx context.Context, rate time.Duration) {
	p.Run(ctx, rate)
	am.app.postAndWake(func() { am.instancePollerStopped(p) })
}

// instancePollerStopped is collectorStopped for the Instance tab.
func (am *ActivityMonitor) instancePollerStopped(p *activity.Poller[amInstanceSample]) {
	if !am.app.panelHosted(am) || am.instPoller != p {
		return
	}
	am.inst.collecting = false
	if am.inst.status == "" {
		am.inst.status = collectionStoppedStatus
	}
	am.buildTools()
}

// applyInstanceSample stores an instance reading and rebuilds the tab. UI
// goroutine, via postAndWake.
func (am *ActivityMonitor) applyInstanceSample(s amInstanceSample) {
	if !am.app.panelHosted(am) {
		return
	}
	am.inst.sample = s
	// The tab's own clock, not the newest row's: rows carry the server's window
	// boundaries, which don't vouch for freshness. The time axis labels what
	// each bucket covers.
	am.inst.sampleTime = s.At.Format("15:04:05")
	am.inst.status = ""
	am.instance = am.buildInstanceView()
	am.invalidateView()
}

// applyInstanceError reports a failed tick, keeping what was read (see
// applyError).
func (am *ActivityMonitor) applyInstanceError(err error) {
	if !am.app.panelHosted(am) {
		return
	}
	am.inst.status = err.Error()
	if errors.Is(err, activity.ErrNoPermission) {
		am.inst.collecting = false
		am.buildTools()
	}
}

// runTempDBCollector is runCollector for the TempDB tab.
func (am *ActivityMonitor) runTempDBCollector(c *activity.TempDBCollector, ctx context.Context, rate time.Duration) {
	c.Run(ctx, rate)
	am.app.postAndWake(func() { am.tempDBCollectorStopped(c) })
}

// collectorStopped records that the activity collector's Run returned. Checks c
// is current, since Retry starts a new one.
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

// restartCollector restarts the active tab's collector. The connection is still
// open, so a failed prologue or dropped tick doesn't cost the panel.
func (am *ActivityMonitor) restartCollector() {
	f := am.feed()
	if f.collecting || f.restart == nil {
		return
	}
	f.status = ""
	f.restart()
}

// applyTempDBSample stores a tempdb tick and rebuilds the tab. UI goroutine,
// via postAndWake.
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

// applyTempDBError reports a failed tempdb tick, keeping what was collected
// (see applyError).
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

// applySample stores a tick and rebuilds both dashboards. UI goroutine, via
// postAndWake.
func (am *ActivityMonitor) applySample(s activity.Sample) {
	if !am.app.panelHosted(am) {
		return
	}
	am.store.Append(s)
	am.act.sampleTime = s.At.Format("15:04:05")
	am.act.status = ""
	am.rebuild()
}

// rebuild refreshes both dashboards' view models.
func (am *ActivityMonitor) rebuild() {
	am.history = am.buildHistoryView()
	am.sample = am.buildSampleView()
	am.invalidateView()
}

// invalidateView marks the canvas stale so the next draw re-renders.
//
// A pinned tooltip is left alone: refreshTooltip moves it on the next draw once
// the hit map is rebuilt. Clearing it here would dismiss a pin on every tick,
// including TempDB ticks dismissing a History pin.
func (am *ActivityMonitor) invalidateView() {
	am.viewGen++
}

// applyError shows a failure in the header, keeping collected data: a failed
// tick on a busy server is ordinary, and the history explains it. A missing
// permission stops the collector, so the panel says so.
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

// adopt records a connection this panel opened, for Close to release.
func (am *ActivityMonitor) adopt(sc *db.ServerConn) {
	am.owned = append(am.owned, sc)
}

// SetBounds lays out a tab row, a toolbar row, and the active tab's content.
func (am *ActivityMonitor) SetBounds(x, y, w, h int) {
	am.rect = core.Rect{X: x, Y: y, W: w, H: h}
	am.tabRect = core.Rect{X: x, Y: y, W: w, H: 1}
	am.toolRect = core.Rect{X: x, Y: y + 1, W: w, H: 1}
	am.contentRect = core.Rect{X: x, Y: y + 2, W: w, H: max(h-2, 0)}

	// Scrollbars are always present: bars that appear on overflow change the
	// viewport, which changes overflow — oscillating at certain sizes.
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

// canvasSize is the active tab's dashboard size. A wider viewport widens the
// canvas (more time buckets) instead of stretching it.
func (am *ActivityMonitor) canvasSize() (int, int) {
	var cw, ch int
	switch am.tab {
	case amTabSample:
		cw, ch = dashboard.SampleCanvasW, dashboard.SampleCanvasH
	case amTabTempDB:
		cw, ch = dashboard.TempDBCanvasW, dashboard.TempDBCanvasH
	case amTabInstance:
		cw, ch = dashboard.InstanceCanvasW, dashboard.InstanceCanvasH
	default:
		cw, ch = dashboard.HistoryCanvasW, dashboard.HistoryCanvasH
	}
	return max(cw, am.viewRect.W), ch
}

// scrollLimits is the active tab's max scroll offset per axis; zero on
// non-canvas tabs, making scroll keys return false.
func (am *ActivityMonitor) scrollLimits() (maxX, maxY int) {
	if !am.tab.canvasTab() {
		return 0, 0
	}
	cw, ch := am.canvasSize()
	return max(cw-am.viewRect.W, 0), max(ch-am.viewRect.H, 0)
}

// scrollTo moves the viewport, clamped, and reports whether it moved; HandleKey
// returns that so a key at a boundary falls through to App.
func (am *ActivityMonitor) scrollTo(x, y int) bool {
	maxX, maxY := am.scrollLimits()
	x = core.Clamp(x, 0, maxX)
	y = core.Clamp(y, 0, maxY)
	if x == am.scrollX[am.tab] && y == am.scrollY[am.tab] {
		return false
	}
	am.scrollX[am.tab], am.scrollY[am.tab] = x, y
	// A pan drops the pinned tooltip; it would cover what the user scrolled to.
	am.tooltip = nil
	return true
}

// scrollBy is scrollTo relative to the current position.
func (am *ActivityMonitor) scrollBy(dx, dy int) bool {
	return am.scrollTo(am.scrollX[am.tab]+dx, am.scrollY[am.tab]+dy)
}

// visibleTabs is this connection's tab bar, in order: every tab minus those the
// engine edition has no data for (Instance is Azure-only). Per-tab arrays stay
// indexed by amTab.
func (am *ActivityMonitor) visibleTabs() []amTab {
	azure := serverIsAzure(am.conn)
	out := make([]amTab, 0, len(amAllTabs))
	for _, t := range amAllTabs {
		if t.azureOnly() && !azure {
			continue
		}
		out = append(out, t)
	}
	return out
}

// tabVisible reports whether t is offered on this connection.
func (am *ActivityMonitor) tabVisible(t amTab) bool {
	return slices.Contains(am.visibleTabs(), t)
}

// setTab switches tabs and rebuilds the toolbar. An unoffered tab is refused.
func (am *ActivityMonitor) setTab(t amTab) {
	if t == am.tab || !am.tabVisible(t) {
		return
	}
	am.tab = t
	am.tooltip = nil
	am.buildTools()
	if pt := am.procTab(); pt != nil {
		pt.activate()
	}
}

// stepTab moves d places along visibleTabs, wrapping, so Tab never lands on an
// undrawn tab.
func (am *ActivityMonitor) stepTab(d int) {
	tabs := am.visibleTabs()
	if len(tabs) == 0 {
		return
	}
	i := slices.Index(tabs, am.tab)
	if i < 0 {
		// The active tab is no longer offered; land on the first visible one.
		am.setTab(tabs[0])
		return
	}
	am.setTab(tabs[((i+d)%len(tabs)+len(tabs))%len(tabs)])
}

// feed is the active tab's collector state: TempDB's own, or the activity feed.
// Procedure tabs get the activity feed, which their header shows.
func (am *ActivityMonitor) feed() *amFeed {
	switch am.tab {
	case amTabTempDB:
		return &am.td
	case amTabInstance:
		return &am.inst.amFeed
	}
	return &am.act
}

// setRate selects the active feed's interval by index and reports whether it
// changed.
func (am *ActivityMonitor) setRate(i int) bool {
	if !am.feed().setRate(i) {
		return false
	}
	am.buildTools()
	return true
}

// setPaused pauses or resumes the active tab's feed; the feeds are independent.
func (am *ActivityMonitor) setPaused(v bool) {
	am.feed().setPaused(v)
	am.buildTools()
}

// buildTools rebuilds the toolbar for the active tab: rate and Pause/Continue
// on dashboards, Refresh on procedure tabs. Positions come from toolRect, so
// SetBounds calls this too.
func (am *ActivityMonitor) buildTools() {
	am.tools = am.tools[:0]
	switch {
	case am.tab.canvasTab():
		// One arm for all dashboards, via feed().
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
			// A stopped collector leaves nothing to pause; Retry is the one
			// control that can act.
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
		// Offered only when the procedure isn't already in master.
		if pt.loc != activity.ProcMaster {
			// Installing into master needs sysadmin; a db_owner gets "Cannot
			// alter the procedure 'sp_block'...". CONTROL SERVER rather than a
			// role test, since HAS_PERMS_BY_NAME answers 1 for sysadmin while
			// IS_SRVROLEMEMBER doesn't fold it in.
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

// layoutTools assigns control rects, collapsing what doesn't fit into "More ▾"
// (see layoutToolButtonsOverflow).
func (am *ActivityMonitor) layoutTools() {
	am.hidden, am.toolsEnd = layoutToolButtonsOverflow(am.tools, am.toolRect, am.toolPrefix, &am.more)
}

// prefixVisible reports whether the rate selector's label is drawn; "More ▾"
// replaces it on a narrow row, and they'd otherwise overlap.
func (am *ActivityMonitor) prefixVisible() bool {
	return am.more.rect.IsZero() || am.more.rect.X > am.toolRect.X+1
}

// runTool invokes cell i's action or explains why not — the single gate for
// clicks and the overflow menu.
func (am *ActivityMonitor) runTool(i int) {
	switch t := am.tools[i]; {
	case t.disabled && t.reason != "":
		am.app.setStatus(t.reason)
	case t.action != nil && !t.disabled:
		t.action()
	}
}

// showOverflowMenu pops the hidden controls under "More ▾".
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

// resolution names what one plotted column covers. Follows drawInterval, not
// the feed rate: TempDB and Instance columns differ from the activity rate. The
// toolbar selector shows the poll rate.
func (am *ActivityMonitor) resolution() string {
	return fmt.Sprintf("%d sec", int(am.drawInterval().Seconds()))
}

// collectionState is the toolbar's right-hand summary: collecting or not, the
// sample shown, and sample spacing. Longest variant first; the caller draws the
// first that fits, so narrow panels drop the message before the stopped state.
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

// header is both dashboards' identification strip, from the connection and
// collector state.
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
