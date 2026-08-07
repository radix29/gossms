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

// Draw renders the panel: tab row, toolbar row, then the active tab.
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

// tabSegments computes each tab's on-screen extent. Drawing and hit-testing
// both build their column math from this one call so a click always lands
// on the tab it looks like it landed on.
func (am *ActivityMonitor) tabSegments() [][]controls.TabSegment {
	widths := make([][]int, len(amTabLabels))
	for i, label := range amTabLabels {
		widths[i] = []int{controls.TabLabelWidth(label)}
	}
	return controls.TabStripSegments(am.tabRect.X+1, widths, am.tabRect.Right())
}

// drawTabBar renders the five tabs, styled like QueryPanel's result tabs.
func (am *ActivityMonitor) drawTabBar(s tcell.Screen) {
	if am.tabRect.H != 1 {
		return
	}
	pal := theme.Active()
	// The same background as a dashboard's section bars: the tab row and the
	// section bars are the two strips that divide this panel, and they read
	// as one system only if they share a colour.
	barStyle := tcell.StyleDefault.Background(pal.ChartSectionBg).Foreground(pal.Text)
	core.FillRect(s, am.tabRect, ' ', barStyle)
	for i, seg := range am.tabSegments() {
		style := barStyle
		if amTab(i) == am.tab {
			style = tcell.StyleDefault.Background(pal.BorderActive).Foreground(color.White).Bold(true)
		}
		core.DrawText(s, seg[0].X, am.tabRect.Y, style, " "+amTabLabels[i]+" ")
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
	if am.toolPrefix != "" {
		core.DrawTextClipped(s, am.toolRect.X+1, am.toolRect.Y, am.toolRect.W-2,
			barStyle.Foreground(pal.TextDim), am.toolPrefix)
	}
	// The dashboard header carries the same facts, but it lives on a canvas
	// that scrolls: on a terminal narrower than the canvas its right-hand
	// end — the sample time and the PAUSED marker — is off screen. A frozen
	// dashboard whose paused marker has scrolled away is the one way this
	// display can actively mislead, so the toolbar repeats it on a row that
	// never scrolls.
	// Every tab with a feed, TempDB included: its dashboard scrolls the same
	// way, and collectionState() has always reported that feed's own state —
	// it was just never drawn there.
	if am.tab.canvasTab() {
		// Fitted into what the controls leave rather than right-aligned over
		// the whole row: a longer message than the gap would otherwise be
		// drawn first and then partly overpainted by the buttons, leaving
		// stray letters between them.
		avail := am.toolRect.Right() - am.toolsEnd - 1
		for _, text := range am.collectionState() {
			if core.DisplayWidth(text) <= avail {
				core.DrawTextRight(s, am.toolsEnd, am.toolRect.Y, avail,
					barStyle.Foreground(pal.TextDim), text)
				break
			}
		}
	}

	// The buttons wear the tooltip's scheme: they are the panel's raised
	// surfaces, and so is the box a chart click produces.
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
}

// drawDashboard blits the visible window of the active dashboard's
// off-screen canvas, then draws the two scrollbars. The canvas is always
// its full fixed size, which is what keeps the layout stable: panel
// proportions never depend on the viewport, only on what's visible through
// it.
func (am *ActivityMonitor) drawDashboard(s tcell.Screen) {
	if am.viewRect.W <= 0 || am.viewRect.H <= 0 {
		return
	}
	cw, ch := am.canvasSize()
	c := am.dashboardCanvas(cw, ch)

	// Clamped here as well as in scrollTo: a resize can shrink the canvas
	// (its width follows the viewport) under a scroll offset set when the
	// panel was wider.
	am.scrollTo(am.scrollX[am.tab], am.scrollY[am.tab])
	sx, sy := am.scrollX[am.tab], am.scrollY[am.tab]
	c.Blit(s, core.Rect{X: sx, Y: sy, W: am.viewRect.W, H: am.viewRect.H}, am.viewRect)

	pal := theme.Active()
	track := tcell.StyleDefault.Background(pal.GridHeader).Foreground(pal.Border)
	thumb := tcell.StyleDefault.Background(pal.BorderActive).Foreground(pal.BorderActive)
	core.DrawScrollbar(s, am.viewRect.Right(), am.viewRect.Y, am.viewRect.H, ch, am.viewRect.H, sy, track, thumb)
	core.DrawScrollbarH(s, am.viewRect.X, am.viewRect.Bottom(), am.viewRect.W, cw, am.viewRect.W, sx, track, thumb)

	// Last, over everything: the tooltip is pinned to a spot in the viewport
	// and has to sit on top of the data it reports on.
	am.drawTooltip(s)
}

// dashboardCanvas returns the active tab's rendered canvas, re-rendering it
// only when something it is drawn from has changed. Draw runs on every event
// the application handles — every keystroke, every mouse motion during a
// drag — and a full render is all eleven charts over a 150x61 canvas, so
// re-rendering per frame is milliseconds of work for an identical picture.
//
// The cache holds a whole frame's output, so anything the render reads has
// to be in the key or the panel silently shows a stale dashboard.
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
		dashboard.DrawSample(c, c.Rect(), v)
	case amTabTempDB:
		v := am.tempdb
		v.Header = key.header
		v.Interval = key.interval
		am.hits = dashboard.DrawTempDB(c, c.Rect(), v)
	default:
		v := am.history
		v.Header = key.header
		v.Interval = key.interval
		am.hits = dashboard.DrawHistory(c, c.Rect(), v)
	}
	am.canvas, am.canvasKey = c, key
	return c
}

// drawInterval is the sampling interval the active tab's charts scale their
// time axis by. Read at draw time rather than stored with the samples: a
// rate change takes effect from the next tick, and the scale describes the
// columns arriving now.
func (am *ActivityMonitor) drawInterval() time.Duration { return am.feed().rate() }
