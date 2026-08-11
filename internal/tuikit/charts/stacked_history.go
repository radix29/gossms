package charts

import (
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// StackedHistoryChart plots one column per time bucket with its series
// stacked, so a column's height is the bucket's total and each colour is
// one metric's contribution to it. This is the shape the waits and memory
// panels need: what matters there is the total and the mix, not each
// series against the baseline.
//
// The stack is composed as a single run per column rather than as one bar
// per series, which is what keeps it continuous — see composeStack. Series
// stack in slice order, first at the baseline.
type StackedHistoryChart struct {
	Series []Series

	// Scale fixes the value range. The zero Scale derives it from the
	// largest per-bucket total.
	Scale Scale

	// YLevels is how many labelled values the axis carries; 0 means five.
	YLevels int

	// TimeLabel is the timestamp of the newest bucket. With Interval set it
	// is right-aligned under that bucket and the older dividers are labelled
	// with their age; without one it is centred under the plot.
	TimeLabel string

	// Interval is how much time one bucket covers. Zero draws no time scale.
	Interval time.Duration

	// GridEvery is the column spacing of the dotted time dividers; 0 picks
	// a spacing from the plot width.
	GridEvery int

	// LegendRows is how many rows the legend may use; 0 means one row,
	// -1 suppresses it.
	LegendRows int
}

// Draw renders the chart into r and returns the plot rect it drew into —
// see HistoryChart.Draw.
func (h StackedHistoryChart) Draw(s tcell.Screen, r core.Rect) core.Rect {
	if r.W <= 0 || r.H <= 0 {
		return core.Rect{}
	}
	sc := h.Scale
	if sc.IsZero() {
		sc = AutoScale(maxStackTotal(h.Series))
	}
	frame := layoutPlot(r, axisGutter(sc, levelsFor(h.YLevels, r.H)), legendRowsFor(h.LegendRows, h.Series))
	if frame.plot.W <= 0 || frame.plot.H <= 0 {
		blankRect(s, r)
		return frame.plot
	}

	gridEvery := h.GridEvery
	if gridEvery == 0 {
		gridEvery = gridSpacing(frame.plot.W)
	}
	drawPlotBackground(s, frame.plot, gridEvery)
	h.drawColumns(s, frame.plot, sc)

	drawYAxis(s, frame.axis, frame.plot, sc, levelsFor(h.YLevels, frame.plot.H))
	drawTimeAxis(s, frame.timeRow, frame.plot, h.TimeLabel, h.Interval, gridEvery)
	DrawLegend(s, frame.legend, LegendItems(h.Series))
	return frame.plot
}

// Plot is the rect Draw would plot into for the same r — see
// HistoryChart.Plot.
func (h StackedHistoryChart) Plot(r core.Rect) core.Rect {
	sc := h.Scale
	if sc.IsZero() {
		sc = AutoScale(maxStackTotal(h.Series))
	}
	return layoutPlot(r, axisGutter(sc, levelsFor(h.YLevels, r.H)), legendRowsFor(h.LegendRows, h.Series)).plot
}

// TimeRow is the row Draw writes the time scale on for the same r — see
// HistoryChart.TimeRow.
func (h StackedHistoryChart) TimeRow(r core.Rect) core.Rect {
	sc := h.Scale
	if sc.IsZero() {
		sc = AutoScale(maxStackTotal(h.Series))
	}
	return layoutPlot(r, axisGutter(sc, levelsFor(h.YLevels, r.H)), legendRowsFor(h.LegendRows, h.Series)).timeRow
}

// drawColumns composes and draws one stacked run per visible bucket.
func (h StackedHistoryChart) drawColumns(s tcell.Screen, plot core.Rect, sc Scale) {
	bg := theme.Active().ChartPlotBg
	buckets := maxLen(h.Series)
	segs := make([]segment, 0, len(h.Series))

	for col := 0; col < plot.W; col++ {
		idx := bucketAt(col, plot.W, buckets)
		if idx < 0 {
			continue
		}
		// Segment heights come from each series' share of the column's
		// total, not from scaling each value on its own: rounding every
		// series independently would let the parts disagree with the total
		// the axis reports.
		segs = segs[:0]
		running := 0.0
		prevCells := 0.0
		for _, ser := range h.Series {
			v := ser.At(idx)
			if v <= 0 {
				continue
			}
			running += v
			cells := sc.Cells(running, plot.H)
			segs = append(segs, segment{color: ser.Color, cells: cells - prevCells})
			prevCells = cells
		}
		drawVRun(s, plot.X+col, plot.Bottom()-1, composeStack(plot.H, segs, bg))
	}
}
