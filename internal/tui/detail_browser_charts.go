package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// detailChart is one composition bar shown under the detail grid: a title
// and the series it stacks. A node type whose loader produces none — every
// one but NodeDatabase today — leaves the grid the whole panel.
type detailChart struct {
	Title  string
	Series []charts.Series

	// Format renders one segment's value for the tooltip. The bar's legend
	// has room for the names only, so the megabytes exist nowhere else on
	// screen. nil formats with charts.FormatValue.
	Format func(float64) string
}

// detailTooltip is the readout a click on a chart pins: every segment of one
// bar, named, coloured and valued.
//
// Nothing moves under it — the strip is drawn at a fixed place from data that
// only a refetch replaces — so the rows are read once, at the click, and the
// pin is dropped outright by anything that could stale them (setCharts) or
// move them (layout).
type detailTooltip struct {
	title string
	rows  []tooltipRow
	// x and y are the screen position of the click, which the box is placed
	// against.
	x, y int
}

// Chart-strip geometry. The strip is a section bar like a dashboard's, then
// one row of panels: a heading, the bar itself, and two rows of legend,
// which is what a four-segment legend needs at the width half a detail
// panel gives it.
const (
	detailChartSectionH = 1
	detailChartTitleH   = 1
	detailChartBarH     = 2
	detailChartLegendH  = 2
	detailChartsH       = detailChartSectionH + detailChartTitleH + detailChartBarH + detailChartLegendH

	// detailChartMinGridH is how many rows the grid keeps for itself — a
	// header and a few rows. Below that the strip is dropped entirely: the
	// properties are what the panel is for, and a chart that squeezes them
	// to one row costs more than it shows.
	detailChartMinGridH = 6

	// detailChartMinPanelW is the narrowest a single chart panel is worth
	// drawing at. Two of them below this share the strip's width only by
	// making both unreadable, so the strip is dropped instead.
	detailChartMinPanelW = 24

	detailChartGutter = 1
)

// chartsRect is the bottom strip of the panel the charts occupy, or the zero
// Rect when there are none or the panel is too small to give them room
// without crowding the grid out. layout() is the only caller: the grid's
// bounds and this rect are two halves of one split and must not be able to
// disagree.
func (db *DetailBrowser) chartsRect() core.Rect {
	if len(db.charts) == 0 {
		return core.Rect{}
	}
	body := core.Rect{X: db.rect.X, Y: db.rect.Y + 1, W: db.rect.W, H: db.rect.H - 1}
	if body.H < detailChartsH+detailChartMinGridH || body.W < detailChartMinPanelW {
		return core.Rect{}
	}
	return core.Rect{X: body.X, Y: body.Bottom() - detailChartsH, W: body.W, H: detailChartsH}
}

// layout splits the panel between the title bar, the grid and the chart
// strip. Called by SetBounds and by every path that changes db.charts, since
// whether there is a strip at all is what decides the grid's height.
func (db *DetailBrowser) layout() {
	// The pin is anchored to the spot clicked, and both callers move the
	// charts out from under it: a resize, and setCharts replacing the
	// numbers it reports.
	db.tooltip = nil
	body := core.Rect{X: db.rect.X, Y: db.rect.Y + 1, W: db.rect.W, H: db.rect.H - 1}
	if strip := db.chartsRect(); !strip.IsZero() {
		body.H -= strip.H
	}
	db.grid.SetBounds(body.X, body.Y, body.W, body.H)
}

// pinChartTooltip is the readout for a click at (mx, my), or nil when the
// click missed every chart panel. The whole panel answers, not just the bar:
// the segments of a composition bar are as small as one column each, and a
// click that lands on the wrong side of a boundary would open nothing at
// all.
func (db *DetailBrowser) pinChartTooltip(mx, my int) *detailTooltip {
	r := db.chartsRect()
	if r.IsZero() {
		return nil
	}
	body := core.Rect{X: r.X, Y: r.Y + detailChartSectionH, W: r.W, H: r.H - detailChartSectionH}
	for i, panel := range detailChartPanels(body, len(db.charts)) {
		if !panel.Contains(mx, my) {
			continue
		}
		return &detailTooltip{title: db.charts[i].Title, rows: db.charts[i].tooltipRows(), x: mx, y: my}
	}
	return nil
}

// tooltipRows is one row per segment, in the order the bar stacks them, each
// carrying the segment's own value and its share of the bar — the share is
// what the bar shows and the value is what it cannot.
//
// Segments the bar drops are dropped here too: StackedBar plots only
// positive values, and a row for one it never drew names a colour that is
// not in the picture.
func (c detailChart) tooltipRows() []tooltipRow {
	format := c.Format
	if format == nil {
		format = charts.FormatValue
	}
	total := 0.0
	for _, ser := range c.Series {
		if v := ser.At(0); v > 0 {
			total += v
		}
	}
	rows := make([]tooltipRow, 0, len(c.Series))
	for _, ser := range c.Series {
		v := ser.At(0)
		if v <= 0 {
			continue
		}
		value := format(v)
		if total > 0 {
			value = fmt.Sprintf("%s  %.1f%%", value, v/total*100)
		}
		rows = append(rows, tooltipRow{label: ser.Label, value: value, color: ser.Color})
	}
	return rows
}

// drawChartTooltip renders the pinned readout over the strip. The whole
// panel is the placement view, not the strip: the box is taller than the
// five rows the strip has and belongs over the grid above it.
func (db *DetailBrowser) drawChartTooltip(s tcell.Screen) {
	if db.tooltip == nil {
		return
	}
	w, h := tooltipBoxSize(db.tooltip.title, db.tooltip.rows)
	if w > db.rect.W || h > db.rect.H {
		return // no room to show it honestly
	}
	drawTooltipBox(s, placeTooltipBox(w, h, db.tooltip.x, db.tooltip.y, db.rect, -1),
		db.tooltip.title, db.tooltip.rows)
}

// drawCharts renders the chart strip. Two panels share the width side by
// side; a strip too narrow for both draws only the first, which is the data
// files — the half a database's size is normally read for.
func (db *DetailBrowser) drawCharts(s tcell.Screen) {
	r := db.chartsRect()
	if r.IsZero() {
		return
	}
	sec := core.Rect{X: r.X, Y: r.Y, W: r.W, H: detailChartSectionH}
	core.FillRect(s, sec, ' ', theme.StyleChartSection())
	core.DrawTextClipped(s, sec.X+1, sec.Y, sec.W-2, theme.StyleChartSection(), "DISK USAGE")

	body := core.Rect{X: r.X, Y: r.Y + detailChartSectionH, W: r.W, H: r.H - detailChartSectionH}
	for i, panel := range detailChartPanels(body, len(db.charts)) {
		c := db.charts[i]
		charts.StackedBar{
			Series:     c.Series,
			Rows:       detailChartBarH,
			LegendRows: detailChartLegendH,
			ShowTotal:  true,
		}.Draw(s, drawDetailChartTitle(s, panel, c.Title))
	}
}

// detailChartPanels divides body between n charts side by side, dropping
// the ones that would not fit rather than shrinking every panel below what
// its legend can be read at.
func detailChartPanels(body core.Rect, n int) []core.Rect {
	if n <= 0 || body.W <= 0 || body.H <= 0 {
		return nil
	}
	fit := min(n, (body.W+detailChartGutter)/(detailChartMinPanelW+detailChartGutter))
	if fit <= 0 {
		return nil
	}
	each := (body.W - (fit-1)*detailChartGutter) / fit
	out := make([]core.Rect, 0, fit)
	x := body.X
	for range fit {
		out = append(out, core.Rect{X: x, Y: body.Y, W: each, H: body.H})
		x += each + detailChartGutter
	}
	return out
}

// drawDetailChartTitle draws one panel's heading and returns what is left
// for the bar — dashboard.drawPanelTitle's job, which lives in a package
// that knows nothing about this one.
func drawDetailChartTitle(s tcell.Screen, r core.Rect, title string) core.Rect {
	if r.W <= 0 || r.H <= 0 {
		return core.Rect{}
	}
	style := theme.StyleChartTitle()
	core.FillRect(s, core.Rect{X: r.X, Y: r.Y, W: r.W, H: detailChartTitleH}, ' ', style)
	core.DrawTextClipped(s, r.X+1, r.Y, r.W-2, style, title)
	return core.Rect{X: r.X + 1, Y: r.Y + detailChartTitleH, W: r.W - 2, H: r.H - detailChartTitleH}
}
