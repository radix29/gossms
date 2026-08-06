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

// Draw renders the bar into r.
func (b StackedBar) Draw(s tcell.Screen, r core.Rect) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	blankRect(s, r)

	total := 0.0
	for _, ser := range b.Series {
		if v := ser.At(b.Index); v > 0 {
			total += v
		}
	}

	legendRows := legendRowsFor(b.LegendRows, b.Series)
	barRows := core.Max(b.Rows, 1)
	if barRows+legendRows > r.H {
		barRows = core.Max(r.H-legendRows, 1)
	}

	valueW := 0
	if b.ShowTotal {
		valueW = core.DisplayWidth(FormatValue(total)) + 1
	}
	barW := r.W - valueW
	if barW <= 0 {
		return
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

	for row := 0; row < barRows && r.Y+row < r.Bottom(); row++ {
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
}
