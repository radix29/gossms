package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// query_store_panel_draw.go renders the Query Store panel: the two toolbar
// rows, the chart, and the two grids on either side of the splitters.

// Draw renders the panel (Panel interface).
func (p *QueryStorePanel) Draw(s tcell.Screen) {
	// Every selector is labelled with what it points at, so the labels change
	// without a resize — and a rect laid out for the old label leaves the next
	// button overpainting the tail of this one. Relaying out here keeps the two
	// in step; it is a handful of width measurements.
	p.refreshToolLabels()
	p.layoutToolRows()
	p.drawToolRow(s, p.selRect, p.sel, &p.selMore, p.selDisabled)
	p.drawToolRow(s, p.actRect, p.acts, &p.actMore, p.actDisabled)
	p.drawChart(s)
	p.chartSplit.Draw(s)
	p.planSplit.Draw(s)
	p.grid.Draw(s)
	p.plansGrid.Draw(s)
	// Last, over both grids: a cell's context menu or value popup is drawn
	// outside the grid's own rect, and without this the menu a right-click
	// opens is invisible while still eating every key until Escape.
	p.grid.DrawOverlay(s)
	p.plansGrid.DrawOverlay(s)
}

// drawToolRow paints one toolbar row in the tooltip scheme Activity Monitor's
// buttons use, dimming each cell the panel would refuse a click on. The dimmed
// state and the refusal are the same predicate, so a cell that looks inert is.
func (p *QueryStorePanel) drawToolRow(s tcell.Screen, r core.Rect, tools []toolButton, more *toolButton, disabled func(int) bool) {
	if r.H != 1 {
		return
	}
	pal := theme.Active()
	core.FillRect(s, r, ' ', theme.StyleMenuBar())
	for i, t := range tools {
		if t.rect.IsZero() {
			continue
		}
		style := theme.StyleTooltip()
		if disabled(i) {
			style = style.Foreground(pal.TextDim)
		}
		core.FillRect(s, t.rect, ' ', style)
		core.DrawText(s, t.rect.X+1, t.rect.Y, style, t.label)
	}
	// The stand-in for whatever did not fit. Dimmed only while the whole row
	// is: what it holds is gated item by item once the menu is open.
	if more.rect.IsZero() {
		return
	}
	style := theme.StyleTooltip()
	if p.busy {
		style = style.Foreground(pal.TextDim)
	}
	core.FillRect(s, more.rect, ' ', style)
	core.DrawText(s, more.rect.X+1, more.rect.Y, style, more.label)
}

// drawChart plots the report's rows in the order the loader produced them:
// tallest first for the five that rank, chronologically for Overall Resource
// Consumption, and most recently executed first for Tracked Queries — which is
// a pinned list rather than a ranking, so its bars are deliberately unsorted.
// Bars beyond the pane's height are dropped by BarChart itself.
func (p *QueryStorePanel) drawChart(s tcell.Screen) {
	r := p.chartRect
	if r.W <= 0 || r.H <= 0 {
		return
	}
	pal := theme.Active()
	core.FillRect(s, r, ' ', theme.StyleDefault())
	p.barBuf = p.res.bars(p.barBuf, pal.ChartCyan)
	bars := p.barBuf
	if len(bars) == 0 {
		core.DrawTextClipped(s, r.X+1, r.Y, r.W-2, theme.StyleDefault().Foreground(pal.TextDim),
			"Nothing to plot for "+p.report().Title)
		return
	}
	// The value column is what the grid's own value column says, so a bar and
	// its row can be read against each other without converting units.
	core.DrawTextClipped(s, r.X+1, r.Y, r.W-2, theme.StyleChartAxis(), p.chartTitle())
	charts.BarChart{
		Bars:       bars,
		ShowValues: true,
	}.Draw(s, core.Rect{X: r.X + 1, Y: r.Y + 1, W: r.W - 2, H: r.H - 1})
}

// chartTitle names what the bars measure: the report, and the quantity the
// loader plotted — which is not always the value column, so it comes from the
// result rather than from the toolbar.
func (p *QueryStorePanel) chartTitle() string {
	if p.res.chartLabel == "" {
		return p.report().Title
	}
	return p.report().Title + " — " + p.res.chartLabel
}
