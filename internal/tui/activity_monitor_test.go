package tui

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/charts"
)

// newTestActivityMonitor builds a bound panel over a never-opened connection; a
// bare ServerConn is enough for every control.
func newTestActivityMonitor(w, h int) *ActivityMonitor {
	sc := &db.ServerConn{Opts: config.Connection{Server: "SQLDEMO01"}}
	am := NewActivityMonitor(newTestApp(), sc)
	am.SetBounds(0, 0, w, h)
	return am
}

func amRender(am *ActivityMonitor, w, h int) []string {
	c := charts.NewCanvas(w, h)
	am.Draw(c)
	return c.Rows()
}

func amRowsContain(rows []string, want string) bool {
	for _, r := range rows {
		if strings.Contains(r, want) {
			return true
		}
	}
	return false
}

func amKey(am *ActivityMonitor, k tcell.Key) bool {
	return am.HandleKey(tcell.NewEventKey(k, "", tcell.ModNone))
}

func amRune(am *ActivityMonitor, r rune) bool {
	return am.HandleKey(tcell.NewEventKey(tcell.KeyRune, string(r), tcell.ModNone))
}

func TestActivityMonitorDrawsTabsAndDashboard(t *testing.T) {
	am := newTestActivityMonitor(100, 30)
	rows := amRender(am, 100, 30)

	for _, want := range []string{"History", "Sample", "TempDB", "Sessions", "Block"} {
		if !strings.Contains(rows[0], want) {
			t.Errorf("tab row = %q, missing the %s tab", rows[0], want)
		}
	}
	if !amRowsContain(rows, "SQL SERVER ACTIVITY") {
		t.Error("the History tab drew no dashboard")
	}
	if !amRowsContain(rows, "SQLDEMO01") {
		t.Error("the dashboard header doesn't name the instance")
	}
}

// The toolbar carries only the active tab's controls.
func TestActivityMonitorToolbarFollowsTheActiveTab(t *testing.T) {
	am := newTestActivityMonitor(100, 30)
	rows := amRender(am, 100, 30)
	for _, want := range []string{"Refresh rate:", "2 s", "10 s", "Pause"} {
		if !strings.Contains(rows[1], want) {
			t.Errorf("History toolbar = %q, missing %q", rows[1], want)
		}
	}

	am.setTab(amTabSessions)
	rows = amRender(am, 100, 30)
	if !strings.Contains(rows[1], "Refresh") {
		t.Errorf("Sessions toolbar = %q, want a manual Refresh button", rows[1])
	}
	if strings.Contains(rows[1], "Pause") || strings.Contains(rows[1], "Refresh rate:") {
		t.Errorf("Sessions toolbar = %q, want no auto-refresh controls", rows[1])
	}
}

func TestActivityMonitorPauseAndRate(t *testing.T) {
	am := newTestActivityMonitor(100, 30)

	if !amRune(am, 'p') {
		t.Fatal("Pause was not handled on the History tab")
	}
	rows := amRender(am, 100, 30)
	if !strings.Contains(rows[1], "Continue") {
		t.Errorf("toolbar = %q, want the button to read Continue while paused", rows[1])
	}
	if !amRowsContain(rows, "PAUSED") {
		t.Error("a paused collector isn't marked in the dashboard header")
	}

	// Pause is shared by History and Sample.
	am.setTab(amTabSample)
	if !amRowsContain(amRender(am, 100, 30), "PAUSED") {
		t.Error("the Sample tab doesn't show the collector as paused")
	}

	if !amRune(am, '-') {
		t.Fatal("the rate selector didn't accept a slower interval")
	}
	if got := am.act.rate().Seconds(); got != 3 {
		t.Errorf("rate = %vs, want 3s after one step slower", got)
	}
	if !amRowsContain(amRender(am, 100, 30), "3 sec") {
		t.Error("the header doesn't report the selected resolution")
	}
}

// The dashboard header scrolls away on a narrow terminal, so the toolbar
// reports the collector's real state on a fixed row.
func TestActivityMonitorToolbarReportsCollectorState(t *testing.T) {
	am := newTestActivityMonitor(100, 30)

	full := am.collectionState()[0]
	if !strings.Contains(full, "not collecting") {
		t.Errorf("state = %q, want it to say nothing is being collected", full)
	}
	if !strings.Contains(full, am.act.status) {
		t.Errorf("state = %q, want it to carry the collector's status %q", full, am.act.status)
	}

	am.act.collecting = true
	if got := am.collectionState()[0]; strings.Contains(got, "not collecting") {
		t.Errorf("state = %q, want it to say collection is running", got)
	}

	// Every fallback still says whether collection stopped.
	am.setPaused(true)
	for _, got := range am.collectionState() {
		if !strings.Contains(got, "PAUSED") {
			t.Errorf("state variant %q doesn't mark the collector paused", got)
		}
	}
	if !strings.Contains(amRender(am, 100, 30)[1], "PAUSED") {
		t.Error("the toolbar row doesn't show the collector as paused")
	}
}

// State text is drawn into the space controls leave, never over their labels.
func TestActivityMonitorToolbarStateDoesNotOverlapControls(t *testing.T) {
	for _, w := range []int{60, 80, 100, 150} {
		am := newTestActivityMonitor(w, 30)
		row := amRender(am, w, 30)[1]
		for _, label := range []string{"2 s", "10 s", "Pause"} {
			if !strings.Contains(row, " "+label+" ") {
				t.Errorf("at width %d the toolbar row %q lost %q", w, row, label)
			}
		}
		if controls := string([]rune(row)[:am.toolsEnd]); strings.Contains(controls, "collecting") {
			t.Errorf("at width %d the state text was drawn among the controls: %q", w, controls)
		}
	}
}

// Every control is gated on its tab.
func TestActivityMonitorControlsAreTabGated(t *testing.T) {
	am := newTestActivityMonitor(100, 30)
	am.setTab(amTabSessions)

	if amRune(am, 'p') {
		t.Error("Pause was accepted on a tab with no timer")
	}
	if amRune(am, '-') {
		t.Error("the rate selector was accepted on a tab with no timer")
	}

	am.setTab(amTabHistory)
	if amRune(am, 'r') {
		t.Error("the placeholder Refresh was accepted on the History tab")
	}
}

// A scroll key that can't move returns false, so the keyboard can leave.
func TestActivityMonitorScrollingReportsWhatItDid(t *testing.T) {
	am := newTestActivityMonitor(100, 30)

	if amKey(am, tcell.KeyUp) {
		t.Error("scrolling up at the top claimed the key")
	}
	if !amKey(am, tcell.KeyDown) {
		t.Error("scrolling down inside a canvas taller than the viewport did nothing")
	}
	if !amKey(am, tcell.KeyEnd) {
		t.Error("End didn't move the viewport to the far corner")
	}
	if amKey(am, tcell.KeyDown) || amKey(am, tcell.KeyRight) {
		t.Error("scrolling past the far corner claimed the key")
	}
	if !amKey(am, tcell.KeyHome) {
		t.Error("Home didn't return the viewport to the top-left")
	}

	// Procedure tabs have no canvas; scrolling keys go to the grid.
	am.setTab(amTabSessions)
	if maxX, maxY := am.scrollLimits(); maxX != 0 || maxY != 0 {
		t.Errorf("the Sessions tab reported scroll limits %d,%d, want 0,0", maxX, maxY)
	}
	if am.scrollBy(0, 1) {
		t.Error("the panel scrolled a tab that has no canvas")
	}
}

func TestActivityMonitorScrollIsPerTab(t *testing.T) {
	am := newTestActivityMonitor(100, 30)
	amKey(am, tcell.KeyPgDn)
	historyY := am.scrollY[amTabHistory]
	if historyY == 0 {
		t.Fatal("PgDn on the History tab didn't scroll")
	}

	am.setTab(amTabSample)
	if am.scrollY[amTabSample] != 0 {
		t.Error("switching tabs carried the History scroll position over")
	}
	am.setTab(amTabHistory)
	if am.scrollY[amTabHistory] != historyY {
		t.Error("returning to History lost its scroll position")
	}
}

// Scrolling moves the viewport, not the layout.
func TestActivityMonitorScrollMovesTheViewport(t *testing.T) {
	am := newTestActivityMonitor(100, 30)
	before := amRender(am, 100, 30)
	am.scrollTo(0, 20)
	after := amRender(am, 100, 30)

	if before[5] == after[5] {
		t.Error("scrolling down left the same content on screen")
	}
	if before[0] != after[0] {
		t.Error("the tab row scrolled with the dashboard")
	}
}

func TestActivityMonitorTabClickAndKeyboardSwitching(t *testing.T) {
	am := newTestActivityMonitor(100, 30)

	if !amKey(am, tcell.KeyTab) || am.tab != amTabSample {
		t.Fatalf("Tab left the panel on tab %d, want Sample", am.tab)
	}
	if !am.HandleKey(tcell.NewEventKey(tcell.KeyBacktab, "", tcell.ModNone)) || am.tab != amTabHistory {
		t.Fatalf("Shift+Tab left the panel on tab %d, want History", am.tab)
	}

	segs := am.tabSegments()
	x := segs[2][0].X + 1
	am.HandleMouse(tcell.NewEventMouse(x, am.tabRect.Y, tcell.Button1, tcell.ModNone))
	if am.tab != amTabTempDB {
		t.Errorf("clicking the third tab selected tab %d, want TempDB", am.tab)
	}
}

// tcell resends Button1 on motion while held: a twitching click must not re-run
// its control, and a toolbar gesture must not switch tabs.
func TestActivityMonitorHeldClickFiresOnce(t *testing.T) {
	am := newTestActivityMonitor(100, 30)
	pause := am.tools[len(am.tools)-1].rect

	am.HandleMouse(tcell.NewEventMouse(pause.X+1, pause.Y, tcell.Button1, tcell.ModNone))
	if !am.act.paused {
		t.Fatal("clicking Pause didn't pause the collector")
	}
	am.HandleMouse(tcell.NewEventMouse(pause.X+2, pause.Y, tcell.Button1, tcell.ModNone))
	if !am.act.paused {
		t.Error("a held click re-fired Pause, toggling it back")
	}
	am.HandleMouse(tcell.NewEventMouse(4, am.tabRect.Y, tcell.Button1, tcell.ModNone))
	if am.tab != amTabHistory {
		t.Error("a gesture that started on the toolbar switched tabs when it crossed the tab row")
	}

	am.HandleMouse(tcell.NewEventMouse(4, am.tabRect.Y, tcell.ButtonNone, tcell.ModNone))
	if am.dragZone != amZoneNone {
		t.Error("the release didn't end the gesture")
	}
}

func TestActivityMonitorWheelScrolls(t *testing.T) {
	am := newTestActivityMonitor(100, 30)
	mx, my := am.viewRect.X+2, am.viewRect.Y+2

	if !am.HandleMouse(tcell.NewEventMouse(mx, my, tcell.WheelDown, tcell.ModNone)) {
		t.Fatal("the wheel didn't scroll the dashboard")
	}
	if am.scrollY[amTabHistory] == 0 {
		t.Error("the wheel claimed the event without scrolling")
	}
	if am.HandleMouse(tcell.NewEventMouse(mx, am.tabRect.Y, tcell.WheelDown, tcell.ModNone)) {
		t.Error("a wheel event over the tab row was treated as dashboard scrolling")
	}
}

// Closing releases every connection the panel opened; a leak is a live session.
func TestActivityMonitorCloseReleasesOwnedConnections(t *testing.T) {
	am := newTestActivityMonitor(100, 30)
	owned := &db.ServerConn{Opts: config.Connection{Server: "SQLDEMO01"}}
	am.adopt(owned)

	am.Close()
	if owned.IsOpen() {
		t.Error("closing the panel left an owned connection open")
	}
	if len(am.owned) != 0 {
		t.Error("Close didn't drop its references to the closed connections")
	}
}

// Opening Activity Monitor twice for one server raises the existing panel.
func TestShowActivityMonitorReusesThePanel(t *testing.T) {
	a := newTestApp()
	sc := &db.ServerConn{Opts: config.Connection{Server: "SQLDEMO01"}}

	a.showActivityMonitorFor(sc)
	first := a.panels.Count()
	a.showActivityMonitorFor(sc)

	if a.panels.Count() != first {
		t.Errorf("panel count went from %d to %d, want the existing panel reused", first, a.panels.Count())
	}
}

// A panel too small for its chrome still draws what fits.
func TestActivityMonitorTinyBounds(t *testing.T) {
	am := newTestActivityMonitor(20, 3)
	rows := amRender(am, 20, 3)
	if !strings.Contains(rows[0], "History") {
		t.Errorf("tab row = %q, want it to survive a 20×3 panel", rows[0])
	}
}

// amClick sends a press and release at one spot; a press without release leaves
// dragZone armed.
func amClick(am *ActivityMonitor, x, y int) bool {
	claimed := am.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	am.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	return claimed
}

// amWithSamples fills the store so charts and tooltips have samples.
func amWithSamples(am *ActivityMonitor, n int) {
	base := time.Date(2026, 8, 6, 15, 32, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		am.store.Append(activity.Sample{
			At:              base.Add(time.Duration(i) * 2 * time.Second),
			BatchesSec:      float64(100 + i),
			TransactionsSec: float64(50 + i),
		})
	}
	am.act.sampleTime = "15:32:45"
	am.rebuild()
}

// A chart click pins that chart's readout at the clicked column; the next click
// dismisses it.
func TestActivityMonitorChartClickPinsATooltip(t *testing.T) {
	am := newTestActivityMonitor(150, 40)
	amWithSamples(am, 30)
	amRender(am, 150, 40) // the hit map comes from a draw

	hit := am.hits[0]
	x := am.viewRect.X + hit.Plot.Right() - 1 - am.scrollX[am.tab]
	y := am.viewRect.Y + hit.Plot.Y - am.scrollY[am.tab]
	if !amClick(am, x, y) {
		t.Fatal("a click on the plot area wasn't claimed")
	}
	if am.tooltip == nil {
		t.Fatal("clicking a chart pinned no tooltip")
	}
	if am.tooltip.time == "" {
		t.Error("the tooltip names no sample time")
	}
	if len(am.tooltip.rows) != len(hit.Series) {
		t.Errorf("tooltip has %d rows, want one per series (%d)", len(am.tooltip.rows), len(hit.Series))
	}
	rows := amRender(am, 150, 40)
	if !amRowsContain(rows, am.tooltip.rows[0].label) {
		t.Errorf("the pinned tooltip wasn't drawn: %q", am.tooltip.rows[0].label)
	}

	amClick(am, x, y)
	if am.tooltip != nil {
		t.Error("the next click didn't dismiss the tooltip")
	}
}

// A scroll dismisses the tooltip.
func TestActivityMonitorScrollDismissesTheTooltip(t *testing.T) {
	am := newTestActivityMonitor(150, 40)
	amWithSamples(am, 30)
	amRender(am, 150, 40)

	hit := am.hits[0]
	amClick(am, am.viewRect.X+hit.Plot.Right()-1-am.scrollX[am.tab], am.viewRect.Y+hit.Plot.Y)
	if am.tooltip == nil {
		t.Fatal("no tooltip to test with")
	}
	if !amKey(am, tcell.KeyDown) {
		t.Fatal("the dashboard didn't scroll")
	}
	if am.tooltip != nil {
		t.Error("the tooltip survived a scroll; it now points at another column")
	}
}

// Scrollbar drags dismiss the tooltip too; they're the one scroll path not
// through scrollBy.
func TestActivityMonitorScrollbarDragDismissesTheTooltip(t *testing.T) {
	am := newTestActivityMonitor(150, 40)
	amWithSamples(am, 30)
	amRender(am, 150, 40)

	hit := am.hits[0]
	amClick(am, am.viewRect.X+hit.Plot.Right()-1-am.scrollX[am.tab], am.viewRect.Y+hit.Plot.Y)
	if am.tooltip == nil {
		t.Fatal("no tooltip to test with")
	}

	// Press at the bottom of the vertical bar so the offset changes noticeably.
	bar := am.viewRect.Right()
	am.HandleMouse(tcell.NewEventMouse(bar, am.viewRect.Bottom()-1, tcell.Button1, tcell.ModNone))
	am.HandleMouse(tcell.NewEventMouse(bar, am.viewRect.Bottom()-1, tcell.ButtonNone, tcell.ModNone))

	if am.scrollY[am.tab] == 0 {
		t.Fatal("the scrollbar drag didn't scroll")
	}
	if am.tooltip != nil {
		t.Error("the tooltip survived a scrollbar drag; it now points at another column")
	}
}

// A new sample pushes buckets left; the pin must keep its numbers and move with
// its column.
func TestActivityMonitorTooltipTracksItsSampleAcrossASample(t *testing.T) {
	am := newTestActivityMonitor(150, 40)
	am.app.panels.AddPanel(am) // applySample ignores an unhosted panel
	am.SetBounds(0, 0, 150, 40)
	amWithSamples(am, 30)
	amRender(am, 150, 40)

	// The rightmost column is newest; the next sample takes it and the box must
	// move.
	hit := am.hits[0]
	x := am.viewRect.X + hit.Plot.Right() - 1 - am.scrollX[am.tab]
	amClick(am, x, am.viewRect.Y+hit.Plot.Y)
	if am.tooltip == nil {
		t.Fatal("no tooltip to test with")
	}
	pinned, pinnedTime, col := am.tooltip.rows[0].value, am.tooltip.time, am.tooltip.col

	am.applySample(activity.Sample{
		At:              time.Date(2026, 8, 6, 15, 33, 0, 0, time.UTC),
		BatchesSec:      9999,
		TransactionsSec: 9999,
	})
	rows := amRender(am, 150, 40) // re-resolved here, not on the sample

	if am.tooltip == nil {
		t.Fatal("the tooltip was dropped by a new sample; its own sample is still on the chart")
	}
	if got := am.tooltip.time; got != pinnedTime {
		t.Errorf("the tooltip now names %q, want the sample it was pinned to (%q)", got, pinnedTime)
	}
	if got := am.tooltip.rows[0].value; got != pinned {
		t.Errorf("the tooltip reads %q, want its own sample's %q", got, pinned)
	}
	if got := am.tooltip.col; got != col-1 {
		t.Errorf("the pinned column is %d, want %d — one left, with the sample it names", got, col-1)
	}
	if !amRowsContain(rows, am.tooltip.rows[0].value) {
		t.Errorf("the moved tooltip wasn't drawn: %q", am.tooltip.rows[0].value)
	}
}

// Once the pinned sample leaves the plot's left edge, the box closes.
func TestActivityMonitorTooltipClosesWhenItsSampleLeavesThePlot(t *testing.T) {
	am := newTestActivityMonitor(150, 40)
	am.app.panels.AddPanel(am)
	am.SetBounds(0, 0, 150, 40)
	amWithSamples(am, 30)
	amRender(am, 150, 40)

	// Pinned on the oldest drawn bucket, which only leaves once the plot is
	// full.
	base := time.Date(2026, 8, 6, 16, 0, 0, 0, time.UTC)
	n := am.hits[0].Plot.W
	for i := 0; i < n; i++ {
		am.store.Append(activity.Sample{
			At:         base.Add(time.Duration(i) * 2 * time.Second),
			BatchesSec: float64(i),
		})
	}
	am.rebuild()
	amRender(am, 150, 40)

	hit := am.hits[0]
	amClick(am, am.viewRect.X+hit.Plot.X-am.scrollX[am.tab], am.viewRect.Y+hit.Plot.Y)
	if am.tooltip == nil {
		t.Fatal("no tooltip to test with")
	}

	// One more sample: the pinned one is still stored but no longer drawn.
	am.applySample(activity.Sample{
		At:         base.Add(time.Duration(n) * 2 * time.Second),
		BatchesSec: 1,
	})
	amRender(am, 150, 40)

	if am.tooltip != nil {
		t.Errorf("the tooltip survived its sample leaving the plot; it now names %q", am.tooltip.time)
	}
}

// A tempdb tick must not dismiss a box pinned on History.
func TestActivityMonitorATempDBTickLeavesAHistoryTooltipAlone(t *testing.T) {
	am := newTestActivityMonitor(150, 40)
	am.app.panels.AddPanel(am)
	am.SetBounds(0, 0, 150, 40)
	amWithSamples(am, 30)
	amRender(am, 150, 40)

	hit := am.hits[0]
	amClick(am, am.viewRect.X+hit.Plot.Right()-1-am.scrollX[am.tab], am.viewRect.Y+hit.Plot.Y)
	if am.tooltip == nil {
		t.Fatal("no tooltip to test with")
	}
	pinned := am.tooltip.rows[0].value

	am.applyTempDBSample(activity.TempDBSample{At: time.Date(2026, 8, 6, 15, 33, 0, 0, time.UTC)})
	amRender(am, 150, 40)

	if am.tooltip == nil {
		t.Fatal("a tempdb tick dismissed a tooltip pinned on History")
	}
	if got := am.tooltip.rows[0].value; got != pinned {
		t.Errorf("a tempdb tick moved a History tooltip from %q to %q", pinned, got)
	}
}

// Nor an activity tick one pinned on TempDB.
func TestActivityMonitorAnActivityTickLeavesATempDBTooltipAlone(t *testing.T) {
	am := newTestActivityMonitor(150, 40)
	am.app.panels.AddPanel(am)
	am.SetBounds(0, 0, 150, 40)
	amWithTempDBSamples(am, 6)
	am.setTab(amTabTempDB)
	amRender(am, 150, 40)

	if len(am.hits) == 0 {
		t.Fatal("the TempDB tab recorded no chart hits to click")
	}
	hit := am.hits[0]
	amClick(am, am.viewRect.X+hit.Plot.Right()-1-am.scrollX[am.tab], am.viewRect.Y+hit.Plot.Y-am.scrollY[am.tab])
	if am.tooltip == nil {
		t.Fatal("no tooltip to test with")
	}
	pinned := am.tooltip.rows[0].value

	am.applySample(activity.Sample{
		At:              time.Date(2026, 8, 6, 20, 5, 30, 0, time.UTC),
		BatchesSec:      9999,
		TransactionsSec: 9999,
	})
	amRender(am, 150, 40)

	if am.tooltip == nil {
		t.Fatal("an activity tick dismissed a tooltip pinned on TempDB")
	}
	if got := am.tooltip.rows[0].value; got != pinned {
		t.Errorf("an activity tick moved a TempDB tooltip from %q to %q", pinned, got)
	}
}

// A tooltip reports the sample under the pointer, not the newest.
func TestActivityMonitorTooltipNamesTheClickedSample(t *testing.T) {
	am := newTestActivityMonitor(150, 40)
	amWithSamples(am, 30)
	amRender(am, 150, 40)

	hit := am.hits[0]
	newest := am.pinTooltip(am.viewRect.X+hit.Plot.Right()-1-am.scrollX[am.tab], am.viewRect.Y+hit.Plot.Y)
	older := am.pinTooltip(am.viewRect.X+hit.Plot.Right()-6-am.scrollX[am.tab], am.viewRect.Y+hit.Plot.Y)
	if newest == nil || older == nil {
		t.Fatal("a column inside the plot produced no tooltip")
	}
	if newest.time == older.time {
		t.Errorf("both columns reported %q; the tooltip isn't reading the clicked bucket", newest.time)
	}
}

// The pin names its moment on the chart's time axis, whose other labels are
// ages.
func TestActivityMonitorTooltipCallsOutItsTimeOnTheAxis(t *testing.T) {
	am := newTestActivityMonitor(150, 40)
	amWithSamples(am, 200) // enough to fill the plot out to its left edge
	amRender(am, 150, 40)

	hit := am.hits[0]
	if hit.TimeRow.H == 0 {
		t.Fatal("the chart recorded no time-axis row to call out on")
	}
	// Near the plot's left, where the box sits clear of the callout.
	amClick(am, am.viewRect.X+hit.Plot.X+5-am.scrollX[am.tab], am.viewRect.Y+hit.Plot.Y)
	if am.tooltip == nil {
		t.Fatal("no tooltip to test with")
	}
	rows := amRender(am, 150, 40)

	_, y := am.screenPos(0, hit.TimeRow.Y)
	if y < 0 || y >= len(rows) {
		t.Fatalf("the time-axis row is off screen at y=%d", y)
	}
	if !strings.Contains(rows[y], am.tooltip.time) {
		t.Errorf("the time axis reads %q, want the pinned %q called out on it", rows[y], am.tooltip.time)
	}
}

// The memory composition bar answers clicks too (its legend lacks megabytes).
// It plots one instant, so every column reports the same components.
func TestActivityMonitorMemoryCompositionClickPinsATooltip(t *testing.T) {
	am := newTestActivityMonitor(150, 40)
	am.store.Append(activity.Sample{
		At:                  time.Date(2026, 8, 6, 15, 32, 45, 0, time.UTC),
		TotalServerMemoryMB: 4096,
		Detail: &activity.SampleDetail{Memory: []activity.MemoryComponent{
			{Name: "Buffer", MB: 3000},
			{Name: "Stolen Buffer", MB: 800},
			{Name: "Plan (SQL)", MB: 296},
		}},
	})
	am.act.sampleTime = "15:32:45"
	am.rebuild()
	am.setTab(amTabSample)
	amRender(am, 150, 40)

	if len(am.hits) != 1 {
		t.Fatalf("the Sample tab reported %d chart hits, want the composition bar", len(am.hits))
	}
	hit := am.hits[0]
	y := am.viewRect.Y + hit.Plot.Y - am.scrollY[amTabSample]
	for _, dx := range []int{0, hit.Plot.W / 2, hit.Plot.W - 1} {
		tip := am.pinTooltip(am.viewRect.X+hit.Plot.X+dx-am.scrollX[amTabSample], y)
		if tip == nil {
			t.Fatalf("column %d of the composition bar produced no tooltip", dx)
		}
		if tip.time != "15:32:45" {
			t.Errorf("tooltip names sample %q, want the newest sample", tip.time)
		}
		if len(tip.rows) != 3 || tip.rows[0].label != "Buffer" {
			t.Errorf("tooltip rows = %+v, want one per memory component", tip.rows)
		}
	}
}

// amWithTempDBSamples fills the tempdb store.
func amWithTempDBSamples(am *ActivityMonitor, n int) {
	start := time.Date(2026, 8, 6, 20, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		s := activity.TempDBSample{
			At: start.Add(time.Duration(i) * 30 * time.Second),
			Space: activity.TempDBSpace{
				VersionStoreMB: float64(i), UserObjectMB: 2, InternalObjectMB: 1,
				MixedExtentMB: 1, FreeMB: 100, TotalMB: float64(104 + i),
			},
			Files: []activity.TempDBFile{
				{FileID: 1, Name: "tempdev", Type: "ROWS", SizeMB: 72, UsedMB: 4},
				{FileID: 3, Name: "tempdev2", Type: "ROWS", SizeMB: 72, UsedMB: 1},
				{FileID: 2, Name: "templog", Type: "LOG", SizeMB: 8},
			},
			Sessions: []activity.TempDBSession{
				{SessionID: 57, Host: "APP01", Program: "loadtest", Login: "sa", UserMB: 3, InternalMB: 1, TotalMB: 4},
			},
			ActiveTempTables: float64(8 + i),
			Cores:            2,
		}
		s.Objects[activity.TempDBUserTemp] = activity.TempDBObjects{
			Kind: activity.TempDBUserTemp, Count: 8, ReservedMB: 0.5,
		}
		am.tdStore.Append(s)
	}
	am.td.sampleTime = "20:05:00"
	am.tempdb = am.buildTempDBView()
}

func TestActivityMonitorTempDBTabDrawsItsSections(t *testing.T) {
	am := newTestActivityMonitor(160, 45)
	amWithTempDBSamples(am, 6)
	am.setTab(amTabTempDB)
	rows := amRender(am, 160, 45)

	for _, want := range []string{"TEMPDB SPACE", "TEMPDB ACTIVITY", "Version store MB"} {
		if !amRowsContain(rows, want) {
			t.Errorf("the TempDB tab is missing %q", want)
		}
	}
}

// The TempDB tab's rate and Pause move only its own collector.
func TestActivityMonitorTempDBHasItsOwnRateAndPause(t *testing.T) {
	am := newTestActivityMonitor(160, 45)
	am.setTab(amTabTempDB)

	if am.td.rate() != 30*time.Second {
		t.Errorf("TempDB opens at %v, want the 30s default", am.td.rate())
	}
	if !amRune(am, '-') || am.td.rate() != 60*time.Second {
		t.Errorf("'-' left the TempDB rate at %v, want 60s", am.td.rate())
	}
	if am.act.rate() != amRates[defaultRateIdx] {
		t.Errorf("changing the TempDB rate moved the activity rate to %v", am.act.rate())
	}
	if !amRune(am, 'p') || !am.td.paused {
		t.Error("'p' did not pause the TempDB collector")
	}
	if am.act.paused {
		t.Error("pausing TempDB also paused the activity collector")
	}
	// Past the end of the list is a no-op, not a wrap.
	if amRune(am, '-') {
		t.Error("'-' at the slowest rate was accepted")
	}
}

// The TempDB tab scrolls and pins tooltips.
func TestActivityMonitorTempDBScrollsAndPinsATooltip(t *testing.T) {
	am := newTestActivityMonitor(160, 45)
	amWithTempDBSamples(am, 6)
	am.setTab(amTabTempDB)
	amRender(am, 160, 45)

	if !amKey(am, tcell.KeyDown) || am.scrollY[amTabTempDB] == 0 {
		t.Fatal("the TempDB tab did not scroll")
	}
	am.scrollTo(0, 0)
	amRender(am, 160, 45)

	if len(am.hits) == 0 {
		t.Fatal("the TempDB tab reported no chart hits")
	}
	hit := am.hits[0]
	mx := hit.Plot.Right() - 1 + am.viewRect.X
	my := hit.Plot.Y + 1 + am.viewRect.Y
	amClick(am, mx, my)
	if am.tooltip == nil {
		t.Fatal("clicking a TempDB chart pinned no tooltip")
	}
	if am.tooltip.time != "20:02:30" {
		t.Errorf("tooltip names sample %q, want the newest tempdb sample", am.tooltip.time)
	}
}

// hostedActivityMonitor is a hosted panel, as panelHosted requires for
// collector callbacks.
func hostedActivityMonitor(t *testing.T) *ActivityMonitor {
	t.Helper()
	a := newTestApp()
	am := NewActivityMonitor(a, &db.ServerConn{Opts: config.Connection{Server: "SQLDEMO01"}})
	a.panels.AddPanel(am)
	am.SetBounds(0, 0, 100, 30)
	return am
}

// Run returns three ways but reports only ErrNoPermission via onError; the
// panel must still stop claiming to collect.
func TestActivityMonitorCollectorStoppedClearsCollecting(t *testing.T) {
	am := hostedActivityMonitor(t)
	c := activity.NewCollector(nil, nil, nil)
	am.collector, am.act.started, am.act.collecting, am.act.status = c, true, true, ""

	am.collectorStopped(c)

	if am.act.collecting {
		t.Fatal("the panel still reports collecting after Run returned")
	}
	if am.act.status != collectionStoppedStatus {
		t.Errorf("status = %q, want %q for a Run that returned without reporting why", am.act.status, collectionStoppedStatus)
	}
	if state := am.collectionState(); !strings.Contains(state[len(state)-1], "not collecting") {
		t.Errorf("collectionState() = %q, want it to say collection stopped", state)
	}
}

// A reported error is the better message; the silent exit mustn't overwrite it.
func TestActivityMonitorCollectorStoppedKeepsAReportedError(t *testing.T) {
	am := hostedActivityMonitor(t)
	c := activity.NewCollector(nil, nil, nil)
	am.collector, am.act.started, am.act.collecting = c, true, true
	am.applyError(errors.New("read DMVs: connection reset"))

	am.collectorStopped(c)

	if am.act.status != "read DMVs: connection reset" {
		t.Errorf("status = %q, want the error the collector reported", am.act.status)
	}
}

// After Retry, the old goroutine's stopped-callback must not mark the new
// collector stopped.
func TestActivityMonitorStaleStoppedCallbackIgnored(t *testing.T) {
	am := hostedActivityMonitor(t)
	old := activity.NewCollector(nil, nil, nil)
	am.collector, am.act.started, am.act.collecting = activity.NewCollector(nil, nil, nil), true, true

	am.collectorStopped(old)

	if !am.act.collecting {
		t.Error("the previous collector's stopped-callback stopped the current one")
	}
}

// A stopped collector's Pause does nothing, so the toolbar offers Retry.
func TestActivityMonitorOffersRetryWhenStopped(t *testing.T) {
	am := hostedActivityMonitor(t)
	c := activity.NewCollector(nil, nil, nil)
	am.collector, am.act.started, am.act.collecting = c, true, true
	am.buildTools()
	if amToolLabelled(am, "Retry") != nil {
		t.Fatal("a running collector offered Retry")
	}

	am.collectorStopped(c)

	retry := amToolLabelled(am, "Retry")
	if retry == nil {
		t.Fatal("a stopped collector offered no Retry")
	}
	if amToolLabelled(am, "Pause") != nil {
		t.Error("a stopped collector still offers Pause, whose sends it drops")
	}
	// feedConn is nil here, so restart is a no-op; this pins that the control
	// is live.
	retry.action()
}

func amToolLabelled(am *ActivityMonitor, label string) *toolButton {
	for i := range am.tools {
		if am.tools[i].label == label {
			return &am.tools[i]
		}
	}
	return nil
}

// deadConnector fails every dial, so the permission prologue errors and Run
// returns before its first tick.
type deadConnector struct{}

func (deadConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("tui_test: no connection")
}

func (deadConnector) Driver() driver.Driver { return nil }

// A returning Run must reach the panel: drives the real collector against a
// dead pool and drains postAndWake's queue.
func TestActivityMonitorLearnsARunReturned(t *testing.T) {
	am := hostedActivityMonitor(t)
	pool := sql.OpenDB(deadConnector{})
	defer pool.Close()

	c := activity.NewCollector(pool, nil,
		func(err error) { am.app.postAndWake(func() { am.applyError(err) }) })
	am.collector, am.act.started, am.act.collecting = c, true, true

	am.runCollector(c, context.Background(), time.Second)
	am.app.drainPending()

	if am.act.collecting {
		t.Fatal("the panel still reports collecting after the collector's Run returned")
	}
	if am.act.status == "" {
		t.Error("nothing was reported about why collection stopped")
	}
}

// Every control is drawn or in the More menu at every width; none has a key
// binding, so a zero-rect control would be unreachable.
func TestNoActivityMonitorToolbarControlIsUnreachableAtAnyWidth(t *testing.T) {
	sawOverflow := false
	// Every tab an Azure connection offers (a superset of on-premises);
	// Instance has its own rate selector.
	for _, tab := range newAzureActivityMonitor(t, 100, 30).visibleTabs() {
		for w := 20; w <= 120; w++ {
			am := newAzureActivityMonitor(t, w, 30)
			am.setTab(tab)

			inMenu := map[int]bool{}
			for _, i := range am.hidden {
				inMenu[i] = true
			}
			for i, tb := range am.tools {
				if tb.rect.IsZero() != inMenu[i] {
					t.Fatalf("tab %d width %d: control %d (%q) is neither drawn nor in the More menu",
						tab, w, i, tb.label)
				}
			}
			if len(am.hidden) == 0 {
				continue
			}
			sawOverflow = true
			if am.more.rect.IsZero() && w > 12 {
				t.Fatalf("tab %d width %d: the row hides %d controls with no More cell to reach them",
					tab, w, len(am.hidden))
			}
			if !am.more.rect.IsZero() && am.more.rect.Right() > am.rect.Right() {
				t.Fatalf("tab %d width %d: the More cell runs past the pane", tab, w)
			}
			// The hidden set must be the row's tail.
			want := len(am.tools) - len(am.hidden)
			for n, i := range am.hidden {
				if i != want+n {
					t.Fatalf("tab %d width %d: hidden controls %v are not the row's tail starting at %d",
						tab, w, am.hidden, want)
				}
			}
		}
	}
	if !sawOverflow {
		t.Fatal("nothing overflowed at any width, so this proves nothing")
	}
}

// End to end: Pause off the row, More on it, and the menu entry pauses the
// feed.
func TestTheActivityMonitorOverflowMenuRunsTheHiddenControl(t *testing.T) {
	am := newTestActivityMonitor(40, 30)
	pause := len(am.tools) - 1
	if !am.tools[pause].rect.IsZero() {
		t.Fatal("Pause still fits at 40 columns, so this proves nothing")
	}
	if am.more.rect.IsZero() {
		t.Fatal("no More cell to reach it through")
	}

	// Press the More cell.
	press := tcell.NewEventMouse(am.more.rect.X+1, am.more.rect.Y, tcell.Button1, 0)
	if !am.HandleMouse(press) {
		t.Fatal("the More cell did not take the press")
	}
	chooseMenuItem(t, am.app, "Pause")

	if !am.feed().paused {
		t.Error("choosing Pause from the More menu did not pause the feed")
	}
}

// The selected rate must stay marked inside the menu; it's the only indication
// of which rate is in force.
func TestTheActivityMonitorOverflowMenuMarksTheRateInForce(t *testing.T) {
	am := newTestActivityMonitor(30, 30)
	am.setRate(2) // "5 s"
	if len(am.hidden) == 0 {
		t.Fatal("nothing overflowed at 30 columns, so this proves nothing")
	}
	am.showOverflowMenu()

	var found bool
	for _, it := range am.app.contextMenu.Items() {
		if strings.Contains(it.Label, "5 s") {
			found = true
			if !strings.HasPrefix(it.Label, "• ") {
				t.Errorf("the rate in force reads %q in the More menu, want it bulleted", it.Label)
			}
		}
	}
	if !found {
		t.Fatalf("the rate in force is not in the More menu; it holds %v", am.app.contextMenu.Items())
	}
}
