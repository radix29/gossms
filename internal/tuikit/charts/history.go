package charts

import (
	"cmp"
	"slices"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// HistoryChart plots one column per time bucket with its series overlaid —
// each series drawn from the baseline in its own colour, tallest first, so
// a shorter series stays visible in front of a taller one. Use it where the
// series are independent quantities (pages read against pages written);
// StackedHistoryChart is for series that compose a total.
//
// The newest bucket is always the rightmost column. A chart wider than the
// data leaves its left end empty rather than stretching the data across it,
// so a partially filled buffer grows from the right as samples arrive
// instead of rescaling on every tick.
type HistoryChart struct {
	Series []Series

	// Scale fixes the value range. The zero Scale derives it from the data,
	// rounded up to a readable maximum.
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

	// LegendRows is how many rows the legend may use; 0 means one row when
	// there are series to name, and -1 suppresses the legend entirely.
	LegendRows int
}

// spec is this chart as the shared history machinery sees it: an auto-scale
// over each series' own largest value, and columns drawn independently.
func (h HistoryChart) spec() historySpec {
	return historySpec{
		series:     h.Series,
		scale:      h.Scale,
		autoMax:    func() float64 { return maxValue(h.Series) },
		yLevels:    h.YLevels,
		legendRows: h.LegendRows,
		gridEvery:  h.GridEvery,
		timeLabel:  h.TimeLabel,
		interval:   h.Interval,
		drawCols:   h.drawColumns,
	}
}

// Draw renders the chart into r and returns the plot rect it drew into —
// the same rect Plot reports for the same r, handed back so a caller that
// hit-tests what it just drew doesn't repeat the scale and layout pass.
func (h HistoryChart) Draw(s tcell.Screen, r core.Rect) core.Rect {
	plot, _ := h.spec().drawFrame(s, r)
	return plot
}

// DrawFrame is Draw reporting the time row as well, for a caller that needs
// both — Draw followed by TimeRow lays the chart out twice, and the layout
// pass runs the auto-scale walk over every bucket of every series.
func (h HistoryChart) DrawFrame(s tcell.Screen, r core.Rect) (plot, timeRow core.Rect) {
	return h.spec().drawFrame(s, r)
}

// Plot is the rect Draw would plot into for the same r — the data area,
// without the axis gutter, time row, or legend. A caller that hit-tests a
// drawn chart has to ask for this rather than recompute it, or the two
// disagree the moment either chart's chrome changes.
func (h HistoryChart) Plot(r core.Rect) core.Rect { return h.spec().plotRect(r) }

// TimeRow is the row Draw writes the time scale on for the same r, zero-sized
// when the chart was too short to carry one. A caller that marks a column on
// that scale has to ask for this for the same reason Plot exists: laying the
// chrome out a second time by hand puts the mark on the wrong row as soon as
// either layout changes.
func (h HistoryChart) TimeRow(r core.Rect) core.Rect { return h.spec().timeRowRect(r) }

// drawColumns plots every visible bucket, tallest series first within each
// column so shorter ones overwrite the taller and stay readable.
func (h HistoryChart) drawColumns(s tcell.Screen, plot core.Rect, sc Scale) {
	bg := theme.Active().ChartPlotBg
	buckets := maxLen(h.Series)
	order := make([]int, 0, len(h.Series))

	for col := 0; col < plot.W; col++ {
		idx := bucketAt(col, plot.W, buckets)
		if idx < 0 {
			continue
		}
		order = order[:0]
		for i := range h.Series {
			order = append(order, i)
		}
		slices.SortStableFunc(order, func(a, b int) int {
			return cmp.Compare(h.Series[b].At(idx), h.Series[a].At(idx))
		})
		for _, si := range order {
			v := h.Series[si].At(idx)
			if v <= 0 {
				continue
			}
			cells := composeStack(plot.H, []segment{{
				color: h.Series[si].Color,
				cells: sc.Cells(v, plot.H),
			}}, bg)
			drawVRun(s, plot.X+col, plot.Bottom()-1, cells)
		}
	}
}

// bucketAt maps a plot column to a bucket index, anchoring the newest
// bucket at the rightmost column. Returns -1 for a column that predates the
// data.
func bucketAt(col, plotW, buckets int) int {
	idx := buckets - 1 - (plotW - 1 - col)
	if idx < 0 || idx >= buckets {
		return -1
	}
	return idx
}

// BucketAt maps a screen column inside plot to the index of the bucket
// drawn there, or -1 for a column that predates the data or falls outside
// the plot. buckets is BucketCount of the series being plotted.
func BucketAt(plot core.Rect, x, buckets int) int {
	if x < plot.X || x >= plot.Right() || plot.W <= 0 {
		return -1
	}
	return bucketAt(x-plot.X, plot.W, buckets)
}

// ColumnAt is the screen column bucket idx is drawn in — the inverse of
// BucketAt, for a caller that has hold of a bucket and needs to know where
// it is now. Returns -1 for a bucket outside the data or one that has
// scrolled off the left edge of the plot, which is what a caller tracking a
// bucket across ticks watches for: newer samples push older ones left, and
// past the plot's width they are no longer drawn at all.
func ColumnAt(plot core.Rect, idx, buckets int) int {
	if plot.W <= 0 || idx < 0 || idx >= buckets {
		return -1
	}
	x := plot.Right() - 1 - (buckets - 1 - idx)
	if x < plot.X || x >= plot.Right() {
		return -1
	}
	return x
}

// BucketCount is how many time buckets a set of series plots — the length
// of its longest member, since a metric still filling its buffer reads as
// zero rather than shortening the chart.
func BucketCount(series []Series) int { return maxLen(series) }

// legendRowsFor resolves a chart's LegendRows field: negative suppresses
// the legend, zero asks for one row when there is anything to name.
func legendRowsFor(requested int, series []Series) int {
	switch {
	case requested < 0 || len(series) == 0:
		return 0
	case requested == 0:
		return 1
	}
	return requested
}
