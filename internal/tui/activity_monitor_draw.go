package tui

import (
	"slices"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tui/dashboard"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/theme"
)

// Draw renders the tab row, toolbar row, then the active tab.
func (am *ActivityMonitor) Draw(s tcell.Screen) {
	core.FillRect(s, am.rect, ' ', theme.StylePanel())
	am.drawTabBar(s)
	am.drawToolbar(s)
	if am.tab.canvasTab() {
		am.drawDashboard(s)
		return
	}
	am.procTab().draw(s)
}

// tabBar returns the offered tabs' bar, built fresh each call; drawing and
// hit-testing share it so clicks land where they look. Its indices are
// positions in visibleTabs, not amTab values.
func (am *ActivityMonitor) tabBar() *controls.TabBar {
	tabs := am.visibleTabs()
	labels := make([]string, len(tabs))
	for i, t := range tabs {
		labels[i] = amTabLabels[t]
	}
	pal := theme.Active()
	b := &controls.TabBar{
		Labels: labels,
		Active: slices.Index(tabs, am.tab),
		Inset:  1,
		// Same background as the dashboard section bars, so the dividing
		// strips read as one system.
		BarStyle: tcell.StyleDefault.Background(pal.ChartSectionBg).Foreground(pal.Text),
		OnSelect: func(i int) { am.setTab(tabs[i]) },
	}
	b.SetBounds(am.tabRect.X, am.tabRect.Y, am.tabRect.W)
	return b
}

// drawTabBar renders the offered tabs, styled like QueryPanel's result tabs.
func (am *ActivityMonitor) drawTabBar(s tcell.Screen) {
	if am.tabRect.H != 1 {
		return
	}
	am.tabBar().Draw(s)
}

// drawToolbar renders the active tab's refresh controls.
func (am *ActivityMonitor) drawToolbar(s tcell.Screen) {
	if am.toolRect.H != 1 {
		return
	}
	pal := theme.Active()
	barStyle := theme.StyleMenuBar()
	// Buttons wear the tooltip scheme: both are the panel's raised surfaces. Each
	// is dimmed from its Disabled field; More never is, its contents being gated
	// per control once opened. The prefix and collection state below go into
	// columns the cells leave free.
	am.tools.Draw(s, am.toolRect, nil, false)
	if am.toolPrefix != "" && am.prefixVisible() {
		core.DrawTextClipped(s, am.toolRect.X+1, am.toolRect.Y, am.toolRect.W-2,
			barStyle.Foreground(pal.TextDim), am.toolPrefix)
	}
	// The dashboard header has the same facts but scrolls: on a narrow terminal
	// its sample time and PAUSED marker can be off screen, and a frozen
	// dashboard with a hidden paused marker misleads. So the non-scrolling
	// toolbar repeats it, for every tab with a feed.
	if am.tab.canvasTab() {
		// Fit into the space the controls leave, rather than right-aligned over
		// the row and partly overpainted by buttons.
		avail := am.toolRect.Right() - am.toolsEnd - 1
		for _, text := range am.collectionState() {
			if core.DisplayWidth(text) <= avail {
				core.DrawTextRight(s, am.toolsEnd, am.toolRect.Y, avail,
					barStyle.Foreground(pal.TextDim), text)
				break
			}
		}
	}

}

// drawDashboard blits the visible window of the active dashboard's fixed-size
// off-screen canvas, then the scrollbars. Panel proportions never depend on the
// viewport.
func (am *ActivityMonitor) drawDashboard(s tcell.Screen) {
	if am.viewRect.W <= 0 || am.viewRect.H <= 0 {
		return
	}
	cw, ch := am.canvasSize()
	c := am.dashboardCanvas(cw, ch)

	// Clamped here too: a resize can shrink the canvas under an older scroll
	// offset.
	vp := am.viewport()
	am.panned(vp.Clamp())
	c.Blit(s, core.Rect{X: vp.X, Y: vp.Y, W: am.viewRect.W, H: am.viewRect.H}, am.viewRect)

	pal := theme.Active()
	track := tcell.StyleDefault.Background(pal.GridHeader).Foreground(pal.Border)
	thumb := tcell.StyleDefault.Background(pal.BorderActive).Foreground(pal.BorderActive)
	vp.DrawScrollbars(s, track, thumb)

	// Last, so the tooltip sits on top. Re-resolved against the freshly rebuilt
	// hit map, moving it onto its sample's current column.
	am.refreshTooltip()
	am.drawTooltip(s, c)
}

// dashboardCanvas returns the active tab's canvas, re-rendering only when an
// input changed. Draw runs on every event and a full render (eleven charts on
// 150x61) costs milliseconds.
//
// Everything the render reads must be in the key, or the panel shows a stale
// dashboard.
func (am *ActivityMonitor) dashboardCanvas(cw, ch int) *core.Buffer {
	key := amCanvasKey{
		tab:      am.tab,
		w:        cw,
		h:        ch,
		gen:      am.viewGen,
		header:   am.header(),
		interval: am.drawInterval(),
		palette:  *theme.Active(),
	}
	if am.canvas != nil && am.canvasKey == key {
		return am.canvas
	}

	c := core.NewBuffer(cw, ch)
	// The panel ground first: the renders leave column gutters and the corner
	// under each Y axis unpainted, and Blit copies those cells as they are. Left
	// colourless they showed the screen's default ground (a strip of a different
	// shade), and DimArea skips them, so they stayed bright behind a dialog.
	c.Fill(' ', theme.StylePanel())
	switch am.tab {
	case amTabSample:
		v := am.sample
		v.Header = key.header
		am.hits = dashboard.DrawSample(c, c.Rect(), v)
	case amTabTempDB:
		v := am.tempdb
		v.Header = key.header
		v.Interval = key.interval
		am.hits = dashboard.DrawTempDB(c, c.Rect(), v)
	case amTabInstance:
		v := am.instance
		v.Header = key.header
		v.Interval = key.interval
		am.hits = dashboard.DrawInstance(c, c.Rect(), v)
	default:
		v := am.history
		v.Header = key.header
		v.Interval = key.interval
		am.hits = dashboard.DrawHistory(c, c.Rect(), v)
	}
	am.canvas, am.canvasKey = c, key
	return c
}

// drawInterval is the time one plotted column covers on the active tab, read at
// draw time since a rate change applies from the next tick.
//
// For every tab but Instance it's the panel's collection rate. Instance plots
// server-aggregated 15-second windows, so its columns keep that resolution
// regardless of poll rate.
func (am *ActivityMonitor) drawInterval() time.Duration {
	if am.tab == amTabInstance {
		return instanceWindow
	}
	return am.feed().rate()
}
