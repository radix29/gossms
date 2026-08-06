package charts

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// maxAutoBarWidth caps how wide an auto-sized vertical bar gets.
const maxAutoBarWidth = 6

// VBarChart draws one vertical bar per value against a labelled Y axis,
// with eighth-block smoothing on each bar's top edge.
//
// It plots categories, not time: the snapshot dashboard's pages read/write
// and per-database I/O panels, where the X positions are named things
// rather than moments. HistoryChart is the time-series counterpart.
type VBarChart struct {
	Bars []Bar

	// Scale fixes the value range; the zero Scale derives it from the
	// largest bar.
	Scale Scale

	// YLevels is how many labelled values the axis carries; 0 means five.
	YLevels int

	// BarWidth is each bar's width in columns; 0 sizes them from the plot,
	// leaving a gap between neighbours.
	BarWidth int

	// ShowLabels writes each bar's label under it instead of relying on a
	// legend.
	ShowLabels bool

	// LegendRows is how many rows the legend may use; 0 means one row,
	// -1 suppresses it.
	LegendRows int
}

// Draw renders the chart into r.
func (v VBarChart) Draw(s tcell.Screen, r core.Rect) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	sc := v.Scale
	if sc.IsZero() {
		sc = AutoScale(barsMax(v.Bars))
	}

	legendRows := 0
	if !v.ShowLabels {
		legendRows = legendRowsFor(v.LegendRows, barSeries(v.Bars))
	}
	frame := layoutPlot(r, axisGutter(sc, levelsFor(v.YLevels, r.H)), legendRows)
	if frame.plot.W <= 0 || frame.plot.H <= 0 || len(v.Bars) == 0 {
		blankRect(s, r)
		return
	}

	drawPlotBackground(s, frame.plot, 0)
	v.drawBars(s, frame.plot, sc)

	drawYAxis(s, frame.axis, frame.plot, sc, levelsFor(v.YLevels, frame.plot.H))
	if v.ShowLabels {
		v.drawLabels(s, frame.plot, frame.timeRow)
	} else {
		blankRect(s, frame.timeRow)
		DrawLegend(s, frame.legend, LegendItems(barSeries(v.Bars)))
	}
}

// drawBars places each bar at its slot, drawing the leftover columns of a
// non-dividing width as spacing rather than widening some bars and not
// others.
func (v VBarChart) drawBars(s tcell.Screen, plot core.Rect, sc Scale) {
	bg := theme.Active().ChartPlotBg
	slot := plot.W / len(v.Bars)
	if slot < 1 {
		slot = 1
	}
	barW := v.BarWidth
	if barW <= 0 {
		// Capped rather than filling the slot: two bars in a wide panel
		// would otherwise become two 20-column slabs, which reads as a
		// colour field rather than as a pair of measured values.
		barW = core.Clamp(slot-1, 1, maxAutoBarWidth)
	}
	barW = core.Min(barW, slot)

	for i, bar := range v.Bars {
		// Centre the bar in its slot so the gaps either side are even and
		// the label under it lines up with the bar rather than the slot.
		x := plot.X + i*slot + (slot-barW)/2
		if x >= plot.Right() {
			return
		}
		if bar.total() <= 0 {
			continue
		}
		cells := composeStack(plot.H, bar.segments(sc, plot.H), bg)
		for c := 0; c < barW && x+c < plot.Right(); c++ {
			drawVRun(s, x+c, plot.Bottom()-1, cells)
		}
	}
}

// drawLabels writes one label per bar, centred on its slot and clipped to
// it so neighbouring labels can't run together.
func (v VBarChart) drawLabels(s tcell.Screen, plot, row core.Rect) {
	if row.W <= 0 || row.H <= 0 {
		return
	}
	blankRect(s, row)
	style := theme.StyleChartAxis()
	slot := plot.W / len(v.Bars)
	if slot < 1 {
		return
	}
	for i, bar := range v.Bars {
		label := bar.labelFor(slot - 1)
		x := plot.X + i*slot + (slot-core.DisplayWidth(label))/2
		if x < plot.X+i*slot {
			x = plot.X + i*slot
		}
		core.DrawTextClipped(s, x, row.Y, plot.Right()-x, style, label)
	}
}

// barSeries adapts bars to the Series shape the legend renderer takes.
func barSeries(bars []Bar) []Series {
	out := make([]Series, 0, len(bars))
	for _, b := range bars {
		out = append(out, Series{Label: b.Label, Short: b.Short, Color: b.Color, Values: []float64{b.total()}})
	}
	return out
}
