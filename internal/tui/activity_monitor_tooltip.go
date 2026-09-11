package tui

import (
	"slices"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tui/dashboard"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// pinTooltip builds the readout for a click at (mx, my), or nil if it missed a
// plot area or hit a column without a sample.
func (am *ActivityMonitor) pinTooltip(mx, my int) *amTooltip {
	if !am.chartTab() || !am.viewRect.Contains(mx, my) {
		return nil
	}
	// Hits are in canvas coordinates; translate the click through the viewport.
	cx, cy := am.canvasPos(mx, my)

	for _, hit := range am.hits {
		if !hit.Plot.Contains(cx, cy) {
			continue
		}
		idx := hit.Bucket(cx)
		if idx < 0 {
			return nil
		}
		tip := &amTooltip{chart: hit.Title, snapshot: hit.Snapshot, col: cx, row: cy}
		am.readTooltip(tip, hit, idx)
		return tip
	}
	return nil
}

// readTooltip fills a pin from one bucket of one chart: its time, current
// column, chart geometry, and one row per series.
func (am *ActivityMonitor) readTooltip(t *amTooltip, hit dashboard.ChartHit, idx int) {
	t.time = am.bucketTime(idx)
	t.plot, t.timeRow = hit.Plot, hit.TimeRow
	if !t.snapshot {
		t.col = hit.Column(idx)
	}
	t.rows = t.rows[:0]
	for _, ser := range hit.Series {
		t.rows = append(t.rows, tooltipRow{
			label: ser.Label,
			value: charts.FormatValue(ser.At(idx)),
			color: ser.Color,
		})
	}
}

// refreshTooltip re-resolves the pin from its sample so the box tracks its
// column as new samples push it left.
//
// Must run after the canvas render (which rebuilds am.hits) and before
// drawTooltip, as drawDashboard does; earlier would use last frame's geometry.
//
// The pin drops when its chart is gone, its sample is pruned, or it has aged
// past the plot's left edge.
func (am *ActivityMonitor) refreshTooltip() {
	t := am.tooltip
	if t == nil {
		return
	}
	hit, ok := am.chartHit(t.chart)
	if !ok {
		am.tooltip = nil
		return
	}
	idx := 0
	if !t.snapshot {
		idx = am.bucketIndex(t.time)
	}
	if idx < 0 {
		am.tooltip = nil
		return
	}
	am.readTooltip(t, hit, idx)

	mx, my := am.screenPos(t.col, t.row)
	if t.col < 0 || !hit.Plot.Contains(t.col, t.row) || !am.viewRect.Contains(mx, my) {
		am.tooltip = nil
	}
}

// chartHit is the last draw's record for one chart, by title.
func (am *ActivityMonitor) chartHit(title string) (dashboard.ChartHit, bool) {
	for _, hit := range am.hits {
		if hit.Title == title {
			return hit, true
		}
	}
	return dashboard.ChartHit{}, false
}

// canvasPos translates a screen position to canvas coordinates; screenPos
// translates back.
func (am *ActivityMonitor) canvasPos(x, y int) (int, int) {
	return x - am.viewRect.X + am.scrollX[am.tab], y - am.viewRect.Y + am.scrollY[am.tab]
}

func (am *ActivityMonitor) screenPos(cx, cy int) (int, int) {
	return cx - am.scrollX[am.tab] + am.viewRect.X, cy - am.scrollY[am.tab] + am.viewRect.Y
}

// chartTab reports whether the active tab's charts resolve clicks. Includes
// Sample, whose memory composition legend lacks megabytes.
func (am *ActivityMonitor) chartTab() bool { return am.tab.canvasTab() }

// bucketTimes are the active tab's plotted bucket times, oldest first, aligned
// with its series.
func (am *ActivityMonitor) bucketTimes() []string {
	if am.tab == amTabTempDB {
		return am.tempdb.Times
	}
	return am.history.Times
}

// bucketTime is one bucket's clock time, or the newest sample's when the view
// has no per-bucket times.
func (am *ActivityMonitor) bucketTime(idx int) string {
	newest := am.act.sampleTime
	switch am.tab {
	case amTabTempDB:
		newest = am.td.sampleTime
	case amTabSample:
		// Sample plots one instant: index 0 is the newest sample.
		return am.act.sampleTime
	}
	if times := am.bucketTimes(); idx >= 0 && idx < len(times) {
		return times[idx]
	}
	return newest
}

// bucketIndex is the bucket a pinned time sits at now, or -1 once pruned. The
// time is the pin's identity because indexes shift with each sample. Searched
// newest first, so samples sharing a second resolve consistently.
func (am *ActivityMonitor) bucketIndex(at string) int {
	times := am.bucketTimes()
	for i, ts := range slices.Backward(times) {
		if ts == at {
			return i
		}
	}
	return -1
}

// place positions the box beside the pinned point, flipping above it rather
// than covering the time callout at keepOut (half-hidden, it would read as an
// age label).
func (t *amTooltip) place(ax, ay int, view core.Rect, keepOut int) core.Rect {
	w, h := tooltipBoxSize(t.time, t.rows)
	return placeTooltipBox(w, h, ax, ay, view, keepOut)
}

// drawTooltip renders the pinned readout: the column's time on the chart's time
// axis, then the box with the sample time and one line per series in its
// colour.
//
// c is the canvas just blitted, which the callout reads to clear axis labels;
// see drawTimeCallout.
func (am *ActivityMonitor) drawTooltip(s tcell.Screen, c *charts.Canvas) {
	if am.tooltip == nil || am.viewRect.W <= 0 || am.viewRect.H <= 0 {
		return
	}
	callout := am.drawCallout(s, c)

	ax, ay := am.screenPos(am.tooltip.col, am.tooltip.row)
	r := am.tooltip.place(ax, ay, am.viewRect, callout)
	if r.W > am.viewRect.W || r.H > am.viewRect.H {
		return // no room to show it honestly
	}
	drawTooltipBox(s, r, am.tooltip.time, am.tooltip.rows)
}

// drawCallout names the pinned bucket's moment on its chart's time axis and
// returns the screen row used, or -1, so the box can avoid it.
func (am *ActivityMonitor) drawCallout(s tcell.Screen, c *charts.Canvas) int {
	t := am.tooltip
	if c == nil || t.plot.W <= 0 {
		return -1
	}
	x, _ := am.screenPos(t.col, 0)
	if x < am.viewRect.X || x >= am.viewRect.Right() {
		return -1
	}
	return am.drawTimeCallout(s, c, x)
}

// drawTimeCallout writes the pinned bucket's time on the time-axis row, centred
// under column x and kept within the row and viewport (the row's labels are
// ages; a clipped callout would read as one).
//
// Any label it overlaps is cleared whole, or a tail survives as a bogus number
// ("-0:20" + callout → "-0:211:34:44"). c is the canvas the row was rendered
// on.
//
// Returns the row, or -1 when there's no room.
func (am *ActivityMonitor) drawTimeCallout(s tcell.Screen, c *charts.Canvas, x int) int {
	t := am.tooltip
	if t.timeRow.W <= 0 || t.timeRow.H <= 0 || t.time == "" {
		return -1
	}
	rowX, y := am.screenPos(t.timeRow.X, t.timeRow.Y)
	if y < am.viewRect.Y || y >= am.viewRect.Bottom() {
		return -1
	}
	w := core.DisplayWidth(t.time)
	left := max(rowX, am.viewRect.X)
	right := min(rowX+t.timeRow.W, am.viewRect.Right())
	if right-left < w {
		return -1
	}
	x = core.Clamp(x-w/2, left, right-w)

	clearFrom, clearTo := am.labelRun(c, t.timeRow, x, x+w)
	core.FillRect(s, core.Rect{X: clearFrom, Y: y, W: clearTo - clearFrom, H: 1}, ' ', theme.StyleChartAxis())
	core.DrawTextClipped(s, x, y, w, theme.StyleTooltip(), t.time)
	return y
}

// labelRun widens [from, to) on row to cover every label it touches, stopping
// at blank columns and staying within the row and viewport.
func (am *ActivityMonitor) labelRun(c *charts.Canvas, row core.Rect, from, to int) (int, int) {
	rowX, _ := am.screenPos(row.X, row.Y)
	lo := max(max(rowX, am.viewRect.X), 0)
	hi := min(rowX+row.W, am.viewRect.Right())

	blank := func(x int) bool {
		cx, _ := am.canvasPos(x, 0)
		str, _, _ := c.Get(cx, row.Y)
		return str == "" || str == " "
	}
	for from > lo && !blank(from-1) {
		from--
	}
	for to < hi && !blank(to) {
		to++
	}
	return from, to
}
