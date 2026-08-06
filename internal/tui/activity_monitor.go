package tui

import (
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

// canvasTab reports whether t draws a scrolling canvas rather than a
// placeholder. It gates every scrolling key and gesture.
func (t amTab) canvasTab() bool { return t.dashboardTab() || t == amTabTempDB }

// amRates are the refresh intervals the rate selector offers, matching the
// work order's 2/3/5/10 seconds.
var amRates = [...]time.Duration{
	2 * time.Second,
	3 * time.Second,
	5 * time.Second,
	10 * time.Second,
}

// amRateLabels label the amRates entries.
var amRateLabels = [...]string{"2 s", "3 s", "5 s", "10 s"}

// defaultRateIdx is the interval a freshly opened panel collects at.
const defaultRateIdx = 0

// amTempDBRates are the TempDB tab's own intervals. They are an order of
// magnitude longer than the activity rates because tempdb space is a level
// that moves over minutes, and because the object enumeration each tick
// performs reads tempdb's own metadata — the very thing a contended tempdb
// has too little of.
var amTempDBRates = [...]time.Duration{
	10 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

var amTempDBRateLabels = [...]string{"10 s", "30 s", "60 s"}

// defaultTempDBRateIdx is 30 seconds: fast enough to watch a version store
// grow, slow enough that the metadata read is never the problem.
const defaultTempDBRateIdx = 1

// ActivityMonitor is the Activity Monitor panel: a five-tab view of one
// server's live activity, hosted by layout.PanelManager like any other
// panel. History, Sample and TempDB render dashboards from
// internal/tui/dashboard into an off-screen canvas of fixed size, which the
// panel scrolls a viewport over; Sessions and Block are placeholders until
// increment 2.
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

	rateIdx int
	paused  bool

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

	// stubStatus is the Refresh button's acknowledgement on a placeholder
	// tab, per tab. Sessions and Block have nothing to fetch yet, so their
	// Refresh has to show that it ran without claiming it loaded anything.
	stubStatus [amTabCount]string

	// store holds every sample collected, and collector is the goroutine
	// filling it. One collector feeds both dashboards: History plots the
	// store, Sample draws its newest entry.
	store     activity.Store
	collector *activity.Collector

	history dashboard.HistoryView
	sample  dashboard.SampleView

	// The TempDB tab keeps its own store, collector, rate, and paused state:
	// it ticks in tens of seconds against a store that retains hours, and
	// nothing about it is shared with the two activity dashboards.
	tdStore      activity.TempDBStore
	tdCollector  *activity.TempDBCollector
	tdRateIdx    int
	tdPaused     bool
	tdCollecting bool
	tdStatus     string
	tdSampleTime string
	tempdb       dashboard.TempDBView

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
type amTool struct {
	label    string
	selected bool
	action   func()
	rect     core.Rect
}

// NewActivityMonitor creates the panel for one server connection.
func NewActivityMonitor(app *App, sc *db.ServerConn) *ActivityMonitor {
	am := new(ActivityMonitor{
		app:       app,
		conn:      sc,
		rateIdx:   defaultRateIdx,
		tdRateIdx: defaultTempDBRateIdx,
		status:    "No samples collected yet.",
		tdStatus:  "No samples collected yet.",
	})
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
	am.collecting = false
	am.tdCollecting = false
	for _, sc := range am.owned {
		sc.Close()
	}
	am.owned = nil
	am.store.Reset()
	am.tdStore.Reset()
	am.history = dashboard.HistoryView{}
	am.sample = dashboard.SampleView{}
	am.tempdb = dashboard.TempDBView{}
	am.viewGen++
	am.canvas = nil
}

// startCollector takes ownership of the panel's own connection and starts
// collecting against it. Called once, from the connect callback.
func (am *ActivityMonitor) startCollector(conn *db.ServerConn) {
	am.adopt(conn)
	am.collector = activity.NewCollector(conn.Server.DB(),
		func(s activity.Sample) { am.app.postAndWake(func() { am.applySample(s) }) },
		func(err error) { am.app.postAndWake(func() { am.applyError(err) }) })
	am.collecting = true
	am.status = ""
	if am.paused {
		am.collector.SetPaused(true)
	}

	collector, ctx, rate := am.collector, conn.Context(), am.rate()
	am.app.safego("collecting server activity", func() { collector.Run(ctx, rate) })

	am.startTempDBCollector(conn)
}

// startTempDBCollector starts the TempDB tab's own collector on the same
// connection's pool. A second goroutine on the same *sql.DB gets its own
// physical connection, which is what keeps a slow tempdb tick from delaying
// an activity tick.
func (am *ActivityMonitor) startTempDBCollector(conn *db.ServerConn) {
	am.tdCollector = activity.NewTempDBCollector(conn.Server.DB(),
		func(s activity.TempDBSample) { am.app.postAndWake(func() { am.applyTempDBSample(s) }) },
		func(err error) { am.app.postAndWake(func() { am.applyTempDBError(err) }) })
	am.tdCollecting = true
	am.tdStatus = ""
	if am.tdPaused {
		am.tdCollector.SetPaused(true)
	}

	collector, ctx, rate := am.tdCollector, conn.Context(), am.tdRate()
	am.app.safego("collecting tempdb activity", func() { collector.Run(ctx, rate) })
}

// applyTempDBSample stores a tempdb tick and rebuilds that tab. Runs on the
// UI goroutine, via postAndWake.
func (am *ActivityMonitor) applyTempDBSample(s activity.TempDBSample) {
	if !am.app.panelHosted(am) {
		return
	}
	am.tdStore.Append(s)
	am.tdSampleTime = s.At.Format("15:04:05")
	am.tdStatus = ""
	am.tempdb = am.buildTempDBView()
	am.viewGen++
}

// applyTempDBError reports a failed tempdb tick without clearing what has
// already been collected — see applyError.
func (am *ActivityMonitor) applyTempDBError(err error) {
	if !am.app.panelHosted(am) {
		return
	}
	am.tdStatus = err.Error()
	if errors.Is(err, activity.ErrNoPermission) {
		am.tdCollecting = false
	}
}

// applySample stores a tick's result and rebuilds both dashboards from it.
// Runs on the UI goroutine, via postAndWake.
func (am *ActivityMonitor) applySample(s activity.Sample) {
	if !am.app.panelHosted(am) {
		return
	}
	am.store.Append(s)
	am.sampleTime = s.At.Format("15:04:05")
	am.status = ""
	am.rebuild()
}

// rebuild refreshes the view models the two dashboards draw.
func (am *ActivityMonitor) rebuild() {
	am.history = am.buildHistoryView()
	am.sample = am.buildSampleView()
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
	am.status = err.Error()
	if errors.Is(err, activity.ErrNoPermission) {
		am.collecting = false
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
// axis. Both are zero on a placeholder tab, which is what makes every
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
}

// setRate selects a refresh interval by index into amRates.
func (am *ActivityMonitor) setRate(i int) bool {
	if i < 0 || i >= len(amRates) || i == am.rateIdx {
		return false
	}
	am.rateIdx = i
	if am.collector != nil {
		am.collector.SetRate(am.rate())
	}
	am.buildTools()
	return true
}

// rate is the interval the collector ticks at.
func (am *ActivityMonitor) rate() time.Duration { return amRates[am.rateIdx] }

// setTempDBRate selects the TempDB tab's interval by index into
// amTempDBRates.
func (am *ActivityMonitor) setTempDBRate(i int) bool {
	if i < 0 || i >= len(amTempDBRates) || i == am.tdRateIdx {
		return false
	}
	am.tdRateIdx = i
	if am.tdCollector != nil {
		am.tdCollector.SetRate(am.tdRate())
	}
	am.buildTools()
	return true
}

// tdRate is the interval the tempdb collector ticks at.
func (am *ActivityMonitor) tdRate() time.Duration { return amTempDBRates[am.tdRateIdx] }

// setTempDBPaused pauses or resumes tempdb collection. Separate from
// setPaused: the two collectors are independent, and pausing one while
// reading the other is a normal thing to want.
func (am *ActivityMonitor) setTempDBPaused(v bool) {
	am.tdPaused = v
	if am.tdCollector != nil {
		am.tdCollector.SetPaused(v)
	}
	am.buildTools()
}

// setPaused pauses or resumes collection. One collector feeds both
// dashboards, so this governs History and Sample together.
func (am *ActivityMonitor) setPaused(v bool) {
	am.paused = v
	if am.collector != nil {
		am.collector.SetPaused(v)
	}
	am.buildTools()
}

// refreshStub is the Refresh button on Sessions and Block. Those tabs have
// no query yet, so it acknowledges the click on the tab itself rather than
// appearing to do nothing.
func (am *ActivityMonitor) refreshStub() {
	am.stubStatus[am.tab] = "Refreshed " + time.Now().Format("15:04:05") + " — nothing to load yet."
}

// buildTools rebuilds the toolbar for the active tab: the rate selector and
// Pause/Continue on the dashboards, a manual Refresh on the placeholders.
// Positions come from toolRect, so SetBounds calls this too.
func (am *ActivityMonitor) buildTools() {
	am.tools = am.tools[:0]
	switch {
	case am.tab.dashboardTab():
		am.toolPrefix = "Refresh rate:"
		for i, label := range amRateLabels {
			am.tools = append(am.tools, amTool{
				label:    label,
				selected: i == am.rateIdx,
				action:   func() { am.setRate(i) },
			})
		}
		label := "Pause"
		if am.paused {
			label = "Continue"
		}
		am.tools = append(am.tools, amTool{label: label, action: func() { am.setPaused(!am.paused) }})
	case am.tab == amTabTempDB:
		am.toolPrefix = "TempDB rate:"
		for i, label := range amTempDBRateLabels {
			am.tools = append(am.tools, amTool{
				label:    label,
				selected: i == am.tdRateIdx,
				action:   func() { am.setTempDBRate(i) },
			})
		}
		label := "Pause"
		if am.tdPaused {
			label = "Continue"
		}
		am.tools = append(am.tools, amTool{label: label, action: func() { am.setTempDBPaused(!am.tdPaused) }})
	default:
		am.toolPrefix = ""
		am.tools = append(am.tools, amTool{label: "Refresh", action: am.refreshStub})
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
// dashboards show it. TempDB has its own collector, so on that tab this has
// to report that collector's rate or the header would credit tempdb columns
// with a resolution they don't have.
func (am *ActivityMonitor) resolution() string {
	d := am.rate()
	if am.tab == amTabTempDB {
		d = am.tdRate()
	}
	return fmt.Sprintf("%d sec", int(d.Seconds()))
}

// collectionState is the one-line summary the toolbar repeats on the right:
// whether collection is running, which sample is on screen, and how far
// apart samples are. It comes back longest first — the caller draws the
// first variant that fits beside the toolbar's controls, so a narrow panel
// loses the collector's message before it loses the fact that collection is
// stopped.
func (am *ActivityMonitor) collectionState() []string {
	paused, collecting, sampleTime, status := am.paused, am.collecting, am.sampleTime, am.status
	if am.tab == amTabTempDB {
		paused, collecting, sampleTime, status = am.tdPaused, am.tdCollecting, am.tdSampleTime, am.tdStatus
	}
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
	h := dashboard.Header{
		Instance:   am.conn.Opts.Server,
		Resolution: am.resolution(),
		SampleTime: am.sampleTime,
		Status:     am.status,
		Paused:     am.paused,
	}
	if am.tab == amTabTempDB {
		h.SampleTime, h.Status, h.Paused = am.tdSampleTime, am.tdStatus, am.tdPaused
	}
	if am.conn.Server != nil && am.conn.Server.Info() != nil {
		info := am.conn.Server.Info()
		h.Version = "SQL Server " + info.ProductVersion
		h.Host = info.Edition
	}
	return h
}
