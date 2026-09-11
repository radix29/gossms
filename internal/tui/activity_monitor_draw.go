package tui

import (
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/tui/dashboard"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
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

// tabSegments computes each tab's extent; drawing and hit-testing share it so
// clicks land where they look. Indexed by position in visibleTabs, not by
// amTab.
func (am *ActivityMonitor) tabSegments() [][]controls.TabSegment {
	tabs := am.visibleTabs()
	widths := make([][]int, len(tabs))
	for i, t := range tabs {
		widths[i] = []int{controls.TabLabelWidth(amTabLabels[t])}
	}
	return controls.TabStripSegments(am.tabRect.X+1, widths, am.tabRect.Right())
}

// drawTabBar renders the offered tabs, styled like QueryPanel's result tabs.
func (am *ActivityMonitor) drawTabBar(s tcell.Screen) {
	if am.tabRect.H != 1 {
		return
	}
	pal := theme.Active()
	// Same background as the dashboard section bars, so the dividing strips
	// read as one system.
	barStyle := tcell.StyleDefault.Background(pal.ChartSectionBg).Foreground(pal.Text)
	core.FillRect(s, am.tabRect, ' ', barStyle)
	tabs := am.visibleTabs()
	for i, seg := range am.tabSegments() {
		if i >= len(tabs) {
			break
		}
		style := barStyle
		if tabs[i] == am.tab {
			style = tcell.StyleDefault.Background(pal.BorderActive).Foreground(color.White).Bold(true)
		}
		core.DrawText(s, seg[0].X, am.tabRect.Y, style, " "+amTabLabels[tabs[i]]+" ")
	}
}

// drawToolbar renders the active tab's refresh controls.
func (am *ActivityMonitor) drawToolbar(s tcell.Screen) {
	if am.toolRect.H != 1 {
		return
	}
	pal := theme.Active()
	barStyle := theme.StyleMenuBar()
	core.FillRect(s, am.toolRect, ' ', barStyle)
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

	// Buttons wear the tooltip scheme: both are the panel's raised surfaces.
	for _, t := range am.tools {
		if t.rect.IsZero() {
			continue
		}
		style := theme.StyleTooltip()
		if t.disabled {
			style = style.Foreground(pal.TextDim)
		} else if t.selected {
			style = tcell.StyleDefault.Background(pal.MenuSelected).Foreground(color.White).Bold(true)
		}
		core.FillRect(s, t.rect, ' ', style)
		core.DrawText(s, t.rect.X+1, t.rect.Y, style, t.label)
	}
	// Never dimmed: its contents are gated per control once opened.
	if !am.more.rect.IsZero() {
		style := theme.StyleTooltip()
		core.FillRect(s, am.more.rect, ' ', style)
		core.DrawText(s, am.more.rect.X+1, am.more.rect.Y, style, am.more.label)
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
	am.scrollTo(am.scrollX[am.tab], am.scrollY[am.tab])
	sx, sy := am.scrollX[am.tab], am.scrollY[am.tab]
	c.Blit(s, core.Rect{X: sx, Y: sy, W: am.viewRect.W, H: am.viewRect.H}, am.viewRect)

	pal := theme.Active()
	track := tcell.StyleDefault.Background(pal.GridHeader).Foreground(pal.Border)
	thumb := tcell.StyleDefault.Background(pal.BorderActive).Foreground(pal.BorderActive)
	core.DrawScrollbar(s, am.viewRect.Right(), am.viewRect.Y, am.viewRect.H, ch, am.viewRect.H, sy, track, thumb)
	core.DrawScrollbarH(s, am.viewRect.X, am.viewRect.Bottom(), am.viewRect.W, cw, am.viewRect.W, sx, track, thumb)

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
func (am *ActivityMonitor) dashboardCanvas(cw, ch int) *charts.Canvas {
	key := amCanvasKey{
		tab:      am.tab,
		w:        cw,
		h:        ch,
		gen:      am.viewGen,
		header:   am.header(),
		interval: am.drawInterval(),
	}
	if am.canvas != nil && am.canvasKey == key {
		return am.canvas
	}

	c := charts.NewCanvas(cw, ch)
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
