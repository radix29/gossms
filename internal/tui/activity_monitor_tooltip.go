package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tui/dashboard"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// tooltipPad is the space between a tooltip's frame and its text.
const tooltipPad = 1

// pinTooltip builds the readout for a click at screen position (mx, my), or
// returns nil when the click didn't land on a chart's plot area or landed on
// a column with no sample behind it.
func (am *ActivityMonitor) pinTooltip(mx, my int) *amTooltip {
	if !am.chartTab() || !am.viewRect.Contains(mx, my) {
		return nil
	}
	// The hits were recorded against the canvas; the viewport scrolls over
	// it, so the click has to be translated before it can be matched.
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

// readTooltip fills a pin from one bucket of one chart: the bucket's time,
// where that bucket is drawn now, the chart's geometry, and one row per
// series.
func (am *ActivityMonitor) readTooltip(t *amTooltip, hit dashboard.ChartHit, idx int) {
	t.time = am.bucketTime(idx)
	t.plot, t.timeRow = hit.Plot, hit.TimeRow
	if !t.snapshot {
		t.col = hit.Column(idx)
	}
	t.rows = t.rows[:0]
	for _, ser := range hit.Series {
		t.rows = append(t.rows, amTooltipRow{
			label: ser.Label,
			value: charts.FormatValue(ser.At(idx)),
			color: ser.Color,
		})
	}
}

// refreshTooltip re-resolves the pinned readout from the sample it names, so
// the box tracks its own column as new samples push it left instead of
// staying put and reporting whatever has slid underneath.
//
// It must run after the canvas render and before drawTooltip, which is
// exactly where drawDashboard calls it: am.hits is rebuilt by that render, so
// re-resolving any earlier reads the previous frame's geometry and puts the
// box a column off.
//
// Three things drop the pin, and all three mean the sample it points at has
// nothing on screen left to point at: its chart is gone, the sample has been
// pruned out of the window, or it has aged past the left edge of the plot.
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

// canvasPos translates a screen position to the scrolling canvas underneath,
// and screenPos translates back.
func (am *ActivityMonitor) canvasPos(x, y int) (int, int) {
	return x - am.viewRect.X + am.scrollX[am.tab], y - am.viewRect.Y + am.scrollY[am.tab]
}

func (am *ActivityMonitor) screenPos(cx, cy int) (int, int) {
	return cx - am.scrollX[am.tab] + am.viewRect.X, cy - am.scrollY[am.tab] + am.viewRect.Y
}

// chartTab reports whether the active tab draws charts a click can be
// resolved against. Sample's bar panels carry their own value labels, but
// its memory composition bar names its segments in a legend with no room
// for their megabytes, so that tab reports hits too.
func (am *ActivityMonitor) chartTab() bool { return am.tab.canvasTab() }

// bucketTimes are the clock times of the active tab's plotted buckets,
// oldest first and index-aligned with every series it draws.
func (am *ActivityMonitor) bucketTimes() []string {
	if am.tab == amTabTempDB {
		return am.tempdb.Times
	}
	return am.history.Times
}

// bucketTime is the clock time of one plotted bucket, falling back to the
// newest sample's time when the view carries no per-bucket times.
func (am *ActivityMonitor) bucketTime(idx int) string {
	newest := am.act.sampleTime
	switch am.tab {
	case amTabTempDB:
		newest = am.td.sampleTime
	case amTabSample:
		// Sample plots one instant, not a series of buckets: index 0 is the
		// newest sample, not the oldest column of History's window.
		return am.act.sampleTime
	}
	if times := am.bucketTimes(); idx >= 0 && idx < len(times) {
		return times[idx]
	}
	return newest
}

// bucketIndex is the bucket a pinned time sits at now, or -1 once that
// sample has been pruned out of the window. The clock time is the pin's
// identity because the index isn't: every sample that lands renumbers the
// buckets under it. Searched newest first, so two samples sharing a second
// resolve to the same one on every refresh.
func (am *ActivityMonitor) bucketIndex(at string) int {
	times := am.bucketTimes()
	for i := len(times) - 1; i >= 0; i-- {
		if times[i] == at {
			return i
		}
	}
	return -1
}

// size is the box the tooltip needs, frame included.
func (t *amTooltip) size() (w, h int) {
	w = core.DisplayWidth(t.time)
	for _, row := range t.rows {
		if lw := core.DisplayWidth(row.label) + core.DisplayWidth(row.value) + 3; lw > w {
			w = lw
		}
	}
	return w + 2 + tooltipPad*2, len(t.rows) + 3
}

// place positions the box next to the pinned point, flipped to whichever
// side of it fits and clamped into view. A tooltip that hangs off the
// viewport is worse than no tooltip: the numbers it exists to show are the
// ones that get clipped.
//
// keepOut is the screen row of the time callout, or -1 for none. The box
// flips above the pinned point rather than cover it: the callout names the
// moment every number in the box belongs to, and half of it behind the box
// reads as one of the axis's own age labels.
func (t *amTooltip) place(ax, ay int, view core.Rect, keepOut int) core.Rect {
	w, h := t.size()
	x := ax + 2
	if x+w > view.Right() {
		x = ax - w - 1
	}
	covers := func(top int) bool { return keepOut >= top && keepOut < top+h }
	y, above := ay+1, ay-h
	if y+h > view.Bottom() || (covers(y) && above >= view.Y && !covers(above)) {
		y = above
	}
	x = core.Clamp(x, view.X, core.Max(view.Right()-w, view.X))
	y = core.Clamp(y, view.Y, core.Max(view.Bottom()-h, view.Y))
	return core.Rect{X: x, Y: y, W: w, H: h}
}

// drawTooltip renders the pinned readout over the dashboard: the reported
// column's time on the chart's own time axis, then the box — the sample's
// time and one line per series in that series' own colour, so a line in the
// box and a band in the chart are matched by eye.
//
// c is the canvas the viewport was just blitted from, which the callout reads
// to find the axis labels it has to clear; see drawTimeCallout.
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
	body := theme.StyleTooltip()
	core.FillRect(s, r, ' ', body)
	core.DrawBox(s, r, theme.StyleTooltipBorder())

	x := r.X + 1 + tooltipPad
	textW := r.W - 2 - tooltipPad*2
	core.DrawTextClipped(s, x, r.Y+1, textW, body, am.tooltip.time)
	for i, row := range am.tooltip.rows {
		y := r.Y + 2 + i
		if y >= r.Bottom()-1 {
			break
		}
		core.DrawTextClipped(s, x, y, textW, body.Foreground(row.color), row.label)
		core.DrawTextRight(s, x, y, textW, body, row.value)
	}
}

// drawCallout names the pinned bucket's moment on its chart's time axis, so
// the box and the column it reports are read as one thing. It returns the
// screen row the callout went on, or -1 for none, so the box can be placed
// clear of it.
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

// drawTimeCallout writes the pinned bucket's time on the chart's time-axis
// row, centred under the pinned column at x and kept inside both the axis
// row and the viewport — the row's own labels are ages counted back from
// now, and a callout half off the end of it would read as one of them.
//
// Whatever the callout lands on is cleared whole: the row's labels are laid
// out so they never touch, and overwriting the middle of one leaves its tail
// standing as a number of its own ("-0:20" plus a callout read back as
// "-0:211:34:44"). c is the canvas the row was rendered on, which is where
// the run of characters to clear is measured.
//
// Returns the row it drew on, or -1 when there was no room for it.
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
	left := core.Max(rowX, am.viewRect.X)
	right := core.Min(rowX+t.timeRow.W, am.viewRect.Right())
	if right-left < w {
		return -1
	}
	x = core.Clamp(x-w/2, left, right-w)

	clearFrom, clearTo := am.labelRun(c, t.timeRow, x, x+w)
	core.FillRect(s, core.Rect{X: clearFrom, Y: y, W: clearTo - clearFrom, H: 1}, ' ', theme.StyleChartAxis())
	core.DrawTextClipped(s, x, y, w, theme.StyleTooltip(), t.time)
	return y
}

// labelRun widens the screen span [from, to) to cover every label it touches
// on row, stopping at the first blank column on either side and never leaving
// the row or the viewport.
func (am *ActivityMonitor) labelRun(c *charts.Canvas, row core.Rect, from, to int) (int, int) {
	rowX, _ := am.screenPos(row.X, row.Y)
	lo := core.Max(core.Max(rowX, am.viewRect.X), 0)
	hi := core.Min(rowX+row.W, am.viewRect.Right())

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
