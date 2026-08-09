package charts

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// StackedBar is a single horizontal bar composed of several coloured
// contributions — the memory-composition bar, where the whole is one
// quantity (server memory) and each segment is a component of it.
//
// It shares composeStack with the stacked history chart, so a segment
// boundary falling inside a cell is drawn the same way in both and the bar
// has no internal gaps.
type StackedBar struct {
	Series []Series

	// Index selects which value of each series the bar shows. Snapshot
	// callers leave it 0.
	Index int

	// Scale fixes the bar's full-width value. The zero Scale sizes the bar
	// so its segments exactly fill the available width, which is what a
	// composition bar wants — the parts are read against each other, not
	// against an absolute maximum.
	Scale Scale

	// Rows is how many rows tall the bar itself is; 0 means one.
	Rows int

	// LegendRows is how many rows the legend may use; 0 means one row,
	// -1 suppresses it.
	LegendRows int

	// ShowTotal writes the formatted total after the bar.
	ShowTotal bool
}

// Draw renders the bar into r and returns the rectangle the bar itself
// covers — the segments only, without the total or the legend — so a caller
// can hit-test a click against it. The zero Rect means nothing was drawn.
func (b StackedBar) Draw(s tcell.Screen, r core.Rect) core.Rect {
	if r.W <= 0 || r.H <= 0 {
		return core.Rect{}
	}
	blankRect(s, r)

	total := 0.0
	for _, ser := range b.Series {
		if v := ser.At(b.Index); v > 0 {
			total += v
		}
	}

	// Clamped to leave the legend its rows, and never past r itself: r.H > 0
	// above, so both arms land in 1..r.H. That is what bounds the drawing
	// loop below, and what lets the returned rect be stated as barRows rather
	// than counted — the two must not be able to disagree.
	//
	// legendRows is then re-derived rather than assumed: the bar keeps a row
	// even when that leaves the legend none, so on a rect too short for both
	// the original figure would place the legend below r and DrawLegend, which
	// fills whatever rect it is handed, would paint over the panel beneath.
	legendRows := legendRowsFor(b.LegendRows, b.Series)
	barRows := core.Max(b.Rows, 1)
	if barRows+legendRows > r.H {
		barRows = core.Max(r.H-legendRows, 1)
		legendRows = core.Max(r.H-barRows, 0)
	}

	valueW := 0
	if b.ShowTotal {
		valueW = core.DisplayWidth(FormatValue(total)) + 1
	}
	barW := r.W - valueW
	if barW <= 0 {
		return core.Rect{}
	}

	sc := b.Scale
	if sc.IsZero() {
		// Fill the width: the composition is the message, so an empty tail
		// would only invite reading the bar as a fraction of some maximum
		// that isn't shown anywhere.
		sc = Scale{Min: 0, Max: total}
	}

	bg := theme.Active().ChartPlotBg
	segs := make([]segment, 0, len(b.Series))
	running, prevCells := 0.0, 0.0
	for _, ser := range b.Series {
		v := ser.At(b.Index)
		if v <= 0 {
			continue
		}
		running += v
		cells := sc.Cells(running, barW)
		segs = append(segs, segment{color: ser.Color, cells: cells - prevCells})
		prevCells = cells
	}
	cells := composeStack(barW, segs, bg)

	for row := range barRows {
		y := r.Y + row
		core.FillRect(s, core.Rect{X: r.X, Y: y, W: barW, H: 1}, ' ', theme.StyleChartPlot())
		drawHRun(s, r.X, y, cells)
		if valueW > 0 && row == 0 {
			core.DrawTextRight(s, r.Right()-valueW, y, valueW, theme.StyleChartAxis(), FormatValue(total))
		}
	}
	if legendRows > 0 {
		DrawLegend(s, core.Rect{X: r.X, Y: r.Y + barRows, W: r.W, H: legendRows}, LegendItems(b.Series))
	}
	if len(segs) == 0 {
		// Every series was zero or negative, so the plot is blank background
		// and there is nothing for a click to report. Saying so here is what
		// keeps "the zero Rect means nothing was drawn" true for the caller.
		return core.Rect{}
	}
	return core.Rect{X: r.X, Y: r.Y, W: barW, H: barRows}
}
