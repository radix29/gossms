package tui

import (
	"github.com/gdamore/tcell/v3"
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
	cx := mx - am.viewRect.X + am.scrollX[am.tab]
	cy := my - am.viewRect.Y + am.scrollY[am.tab]

	for _, hit := range am.hits {
		if !hit.Plot.Contains(cx, cy) {
			continue
		}
		idx := hit.Bucket(cx)
		if idx < 0 {
			return nil
		}
		tip := &amTooltip{
			time:   am.bucketTime(idx),
			anchor: core.Rect{X: mx, Y: my, W: 1, H: 1},
		}
		for _, ser := range hit.Series {
			tip.rows = append(tip.rows, amTooltipRow{
				label: ser.Label,
				value: charts.FormatValue(ser.At(idx)),
				color: ser.Color,
			})
		}
		return tip
	}
	return nil
}

// refreshTooltip re-resolves the pinned readout from its anchor, so a box on
// a live dashboard keeps reporting the column it points at instead of the
// numbers that happened to be there when it was pinned.
//
// It must run after the canvas render and before drawTooltip, which is
// exactly where drawDashboard calls it: am.hits is rebuilt by that render, so
// re-resolving any earlier reads the previous frame's series and reinstates
// the desync this exists to close.
//
// pinTooltip answering nil is the drop: a panel resized under the box, or a
// chart whose series went away, leaves the anchor over nothing, and a tooltip
// pointing at nothing is worse than none.
func (am *ActivityMonitor) refreshTooltip() {
	if am.tooltip == nil {
		return
	}
	a := am.tooltip.anchor
	am.tooltip = am.pinTooltip(a.X, a.Y)
}

// chartTab reports whether the active tab draws charts a click can be
// resolved against. Sample's bar panels carry their own value labels, but
// its memory composition bar names its segments in a legend with no room
// for their megabytes, so that tab reports hits too.
func (am *ActivityMonitor) chartTab() bool { return am.tab.canvasTab() }

// bucketTime is the clock time of one plotted bucket, falling back to the
// newest sample's time when the view carries no per-bucket times.
func (am *ActivityMonitor) bucketTime(idx int) string {
	times, newest := am.history.Times, am.act.sampleTime
	switch am.tab {
	case amTabTempDB:
		times, newest = am.tempdb.Times, am.td.sampleTime
	case amTabSample:
		// Sample plots one instant, not a series of buckets: index 0 is the
		// newest sample, not the oldest column of History's window.
		return am.act.sampleTime
	}
	if idx >= 0 && idx < len(times) {
		return times[idx]
	}
	return newest
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

// place positions the box next to its anchor, flipped to whichever side of
// the pointer it fits on and clamped into view. A tooltip that hangs off the
// viewport is worse than no tooltip: the numbers it exists to show are the
// ones that get clipped.
func (t *amTooltip) place(view core.Rect) core.Rect {
	w, h := t.size()
	x := t.anchor.X + 2
	if x+w > view.Right() {
		x = t.anchor.X - w - 1
	}
	y := t.anchor.Y + 1
	if y+h > view.Bottom() {
		y = t.anchor.Y - h
	}
	x = core.Clamp(x, view.X, core.Max(view.Right()-w, view.X))
	y = core.Clamp(y, view.Y, core.Max(view.Bottom()-h, view.Y))
	return core.Rect{X: x, Y: y, W: w, H: h}
}

// drawTooltip renders the pinned readout over the dashboard: the sample's
// time, then one line per series in that series' own colour, so a line in
// the box and a band in the chart are matched by eye.
func (am *ActivityMonitor) drawTooltip(s tcell.Screen) {
	if am.tooltip == nil || am.viewRect.W <= 0 || am.viewRect.H <= 0 {
		return
	}
	r := am.tooltip.place(am.viewRect)
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
