package charts

import (
	"fmt"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// defaultYLevels is how many labelled values a chart's Y axis carries when
// the caller doesn't say — the work order's "around five".
const defaultYLevels = 5

// levelsFor clamps a requested Y-level count to what the plot height can
// actually show: one label per row at most, and never fewer than two (the
// maximum and the baseline) as long as there are two rows to put them on.
func levelsFor(requested, plotH int) int {
	if requested <= 0 {
		requested = defaultYLevels
	}
	if plotH < 2 {
		return 1
	}
	return core.Clamp(requested, 2, plotH)
}

// drawYAxis writes the scale's tick labels into the gutter, right-aligned
// and each on the plot row its value falls on — top label on the plot's
// first row, baseline on its last.
func drawYAxis(s tcell.Screen, axis, plot core.Rect, sc Scale, levels int) {
	if axis.W <= 1 || plot.H <= 0 {
		return
	}
	style := theme.StyleChartAxis()
	core.FillRect(s, axis, ' ', style)

	ticks := sc.Ticks(levels)
	for i, v := range ticks {
		row := plot.Y
		if len(ticks) > 1 {
			row = plot.Y + i*(plot.H-1)/(len(ticks)-1)
		}
		if row >= plot.Bottom() {
			continue
		}
		// The gutter's last column is the gap between label and plot, so
		// labels right-align against that instead of touching the data.
		core.DrawTextRight(s, axis.X, row, axis.W-1, style, FormatValue(v))
	}
}

// drawTimeLabel centres text under a plot — the sample time a history chart
// ends at, or a category caption. Text wider than the row is clipped rather
// than shifted off its centre.
func drawTimeLabel(s tcell.Screen, row core.Rect, text string) {
	if row.W <= 0 || row.H <= 0 || text == "" {
		return
	}
	style := theme.StyleChartAxis()
	core.FillRect(s, row, ' ', style)
	x := row.X + (row.W-core.DisplayWidth(text))/2
	if x < row.X {
		x = row.X
	}
	core.DrawTextClipped(s, x, row.Y, row.Right()-x, style, text)
}

// drawTimeAxis writes the horizontal scale under a history plot: the newest
// sample's time under the rightmost column, and an age at each of the plot's
// time dividers — "-30s", "-1m" — so a column's distance from now can be
// read off the chart instead of counted.
//
// interval is how much time one column covers. Zero means the caller doesn't
// know, and the row falls back to the centred sample time alone.
//
// Labels are laid out right to left and each one is dropped rather than
// truncated when it would touch its neighbour, so the row never shows a
// half-written age that reads as a different number.
func drawTimeAxis(s tcell.Screen, row, plot core.Rect, now string, interval time.Duration, gridEvery int) {
	if row.W <= 0 || row.H <= 0 {
		return
	}
	if interval <= 0 || gridEvery <= 0 {
		drawTimeLabel(s, row, now)
		return
	}
	style := theme.StyleChartAxis()
	core.FillRect(s, row, ' ', style)

	// leftmost tracks the left edge of the last label drawn, so the next one
	// to its left knows where it has to stop.
	leftmost := row.Right()
	if now != "" {
		x := core.Max(row.Right()-core.DisplayWidth(now), row.X)
		core.DrawTextClipped(s, x, row.Y, row.Right()-x, style, now)
		leftmost = x
	}

	// The dividers this labels are the ones drawPlotBackground draws, at the
	// same spacing and anchored to the same right edge. The unit comes from
	// the oldest of them so one axis never mixes "-100s" with "-2.5m".
	oldest := time.Duration(plot.W-1) * interval
	for x := plot.Right() - 1 - gridEvery; x > plot.X; x -= gridEvery {
		text := formatAge(time.Duration(plot.Right()-1-x)*interval, oldest)
		start := x - core.DisplayWidth(text) + 1
		if start < row.X || x >= leftmost-1 {
			continue
		}
		core.DrawText(s, start, row.Y, style, text)
		leftmost = start
	}
}

// formatAge renders how far back a plot column sits. span is the age of the
// oldest column on the same axis and picks the unit for all of them: whole
// seconds while the chart covers less than a minute, minutes and seconds
// beyond that.
func formatAge(d, span time.Duration) string {
	if span < time.Minute {
		return fmt.Sprintf("-%ds", int(d.Seconds()))
	}
	secs := int(d.Seconds())
	return fmt.Sprintf("-%d:%02d", secs/60, secs%60)
}

// gridSpacing picks the column spacing for a plot's dotted time dividers
// when the caller hasn't chosen one: roughly a quarter of the plot's width,
// so a chart carries three interior rules regardless of how wide it is.
// Returns 0 for a plot too narrow for a divider to mean anything.
func gridSpacing(plotW int) int {
	if plotW < 12 {
		return 0
	}
	return plotW / 4
}

// blankRect clears r to the panel background — used where a chart has
// chrome space it isn't filling, so stale content underneath can't show
// through.
func blankRect(s tcell.Screen, r core.Rect) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	core.FillRect(s, r, ' ', theme.StyleChartAxis())
}
