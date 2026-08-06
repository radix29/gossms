package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/activity"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/charts"
)

// newTestActivityMonitor builds a bound panel over a connection that was
// never opened — nothing in the shell queries, so a bare ServerConn is
// enough to exercise every control.
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

// The toolbar carries the active tab's controls and only those: a rate
// selector the placeholder tabs have no timer for would be a control that
// does nothing.
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

func TestActivityMonitorStubTabsRenderPlaceholders(t *testing.T) {
	am := newTestActivityMonitor(100, 30)

	am.setTab(amTabSessions)
	if rows := amRender(am, 100, 30); !amRowsContain(rows, "Sessions view is not implemented yet.") {
		t.Error("the Sessions tab drew no placeholder")
	}
	am.setTab(amTabBlock)
	if rows := amRender(am, 100, 30); !amRowsContain(rows, "Blocking view is not implemented yet.") {
		t.Error("the Block tab drew no placeholder")
	}
}

// A Refresh that appears to do nothing is exactly what the context-gating
// rule forbids: the placeholder tabs have nothing to load, so the button
// has to say so.
func TestActivityMonitorStubRefreshAcknowledges(t *testing.T) {
	am := newTestActivityMonitor(100, 30)
	am.setTab(amTabBlock)

	if !amRune(am, 'r') {
		t.Fatal("Refresh on the Block tab was not handled")
	}
	if !amRowsContain(amRender(am, 100, 30), "Refreshed") {
		t.Error("Refresh left no sign it ran")
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

	// Pause is shared: one collector feeds both dashboards, so the Sample
	// tab must not come up looking live while History is frozen.
	am.setTab(amTabSample)
	if !amRowsContain(amRender(am, 100, 30), "PAUSED") {
		t.Error("the Sample tab doesn't show the collector as paused")
	}

	if !amRune(am, '-') {
		t.Fatal("the rate selector didn't accept a slower interval")
	}
	if got := am.rate().Seconds(); got != 3 {
		t.Errorf("rate = %vs, want 3s after one step slower", got)
	}
	if !amRowsContain(amRender(am, 100, 30), "3 sec") {
		t.Error("the header doesn't report the selected resolution")
	}
}

// The dashboard header lives on a canvas that scrolls out from under a
// narrow terminal, so the toolbar has to carry the collector's state on a
// row that never moves — and it must report what is actually happening, not
// what the panel would like to be happening.
func TestActivityMonitorToolbarReportsCollectorState(t *testing.T) {
	am := newTestActivityMonitor(100, 30)

	full := am.collectionState()[0]
	if !strings.Contains(full, "not collecting") {
		t.Errorf("state = %q, want it to say nothing is being collected", full)
	}
	if !strings.Contains(full, am.status) {
		t.Errorf("state = %q, want it to carry the collector's status %q", full, am.status)
	}

	am.collecting = true
	if got := am.collectionState()[0]; strings.Contains(got, "not collecting") {
		t.Errorf("state = %q, want it to say collection is running", got)
	}

	// Every fallback the toolbar can fall back to still says whether
	// collection is stopped; that is the fact a narrow panel must not lose.
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

// A control's label must never be broken up by the state text beside it:
// they share one row, and the state is drawn into whatever the controls
// leave rather than over the top of them.
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

// Every control is gated on the tab it belongs to.
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

// A scroll key that can't move must come back false, or the panel becomes
// somewhere the keyboard can't leave.
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

	// A placeholder tab scrolls nowhere at all.
	am.setTab(amTabBlock)
	if amKey(am, tcell.KeyDown) || amKey(am, tcell.KeyPgDn) {
		t.Error("a placeholder tab claimed a scroll key")
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

// Scrolling changes what's visible, not how the dashboard is laid out.
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

// tcell resends Button1 on every motion event while the button is held, so
// a click that twitches must not re-run the control it landed on — and a
// gesture that started on the toolbar must not switch tabs when it wanders
// onto the tab row.
func TestActivityMonitorHeldClickFiresOnce(t *testing.T) {
	am := newTestActivityMonitor(100, 30)
	pause := am.tools[len(am.tools)-1].rect

	am.HandleMouse(tcell.NewEventMouse(pause.X+1, pause.Y, tcell.Button1, tcell.ModNone))
	if !am.paused {
		t.Fatal("clicking Pause didn't pause the collector")
	}
	am.HandleMouse(tcell.NewEventMouse(pause.X+2, pause.Y, tcell.Button1, tcell.ModNone))
	if !am.paused {
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

// The panel owns whatever connections it opened for itself, and closing it
// has to release them — a leaked connection is a live SQL Server session.
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

// Opening Activity Monitor twice for the same server raises the existing
// panel rather than starting a second collector against that instance.
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

// A panel too small for its chrome must still draw what fits.
func TestActivityMonitorTinyBounds(t *testing.T) {
	am := newTestActivityMonitor(20, 3)
	rows := amRender(am, 20, 3)
	if !strings.Contains(rows[0], "History") {
		t.Errorf("tab row = %q, want it to survive a 20×3 panel", rows[0])
	}
}

// amClick sends a press and its release at one spot, which is what the
// panel's gesture owner expects — a press with no release leaves dragZone
// armed and the next click routed to the old zone.
func amClick(am *ActivityMonitor, x, y int) bool {
	claimed := am.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	am.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
	return claimed
}

// amWithSamples fills the store so the History charts have something to
// plot and the tooltip has a sample to name.
func amWithSamples(am *ActivityMonitor, n int) {
	base := time.Date(2026, 8, 6, 15, 32, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		am.store.Append(activity.Sample{
			At:              base.Add(time.Duration(i) * 2 * time.Second),
			BatchesSec:      float64(100 + i),
			TransactionsSec: float64(50 + i),
		})
	}
	am.sampleTime = "15:32:45"
	am.rebuild()
}

// A click on a chart pins a readout of that chart's series at the clicked
// column, and the next click anywhere dismisses it.
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

// The tooltip is anchored to a spot in the viewport, so it cannot outlive a
// scroll that moves the canvas under it.
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

// Dragging a scrollbar moves the canvas under the tooltip exactly as a
// scrolling key does, so it has to drop the tooltip too. The two bars are
// the one scrolling path that doesn't go through scrollBy, which is where
// the tooltip is cleared.
func TestActivityMonitorScrollbarDragDismissesTheTooltip(t *testing.T) {
	am := newTestActivityMonitor(150, 40)
	amWithSamples(am, 30)
	amRender(am, 150, 40)

	hit := am.hits[0]
	amClick(am, am.viewRect.X+hit.Plot.Right()-1-am.scrollX[am.tab], am.viewRect.Y+hit.Plot.Y)
	if am.tooltip == nil {
		t.Fatal("no tooltip to test with")
	}

	// Press on the vertical bar's own column, at the bottom of its track, so
	// the drag lands on an offset well away from the current one.
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

// A tooltip reports the sample under the pointer, not the newest one.
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

// amWithTempDBSamples fills the tempdb store so the TempDB tab has something
// to draw and to resolve a click against.
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
	am.tdSampleTime = "20:05:00"
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

// The TempDB tab runs its own collector, so its rate selector and its Pause
// must move its own state and leave the activity collector's alone.
func TestActivityMonitorTempDBHasItsOwnRateAndPause(t *testing.T) {
	am := newTestActivityMonitor(160, 45)
	am.setTab(amTabTempDB)

	if am.tdRate() != 30*time.Second {
		t.Errorf("TempDB opens at %v, want the 30s default", am.tdRate())
	}
	if !amRune(am, '-') || am.tdRate() != 60*time.Second {
		t.Errorf("'-' left the TempDB rate at %v, want 60s", am.tdRate())
	}
	if am.rate() != amRates[defaultRateIdx] {
		t.Errorf("changing the TempDB rate moved the activity rate to %v", am.rate())
	}
	if !amRune(am, 'p') || !am.tdPaused {
		t.Error("'p' did not pause the TempDB collector")
	}
	if am.paused {
		t.Error("pausing TempDB also paused the activity collector")
	}
	// Past the end of the list is a no-op, not a wrap: the toolbar shows the
	// slowest rate selected and the key has to agree with it.
	if amRune(am, '-') {
		t.Error("'-' at the slowest rate was accepted")
	}
}

// The tab is a real dashboard, not a placeholder: it scrolls, and its charts
// answer a click with the sample under it.
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
