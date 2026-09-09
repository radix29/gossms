package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// A pinned chart readout — the box a click on a chart opens, naming what the
// colours under the pointer are worth. Two panels pin one: Activity Monitor
// over its dashboards (activity_monitor_tooltip.go) and the Object Explorer
// Details disk-usage strip (detail_browser_charts.go). They differ in what
// identifies a pin — a sample in time there, a chart panel here — and share
// the box itself, so the two read as the same thing.

// tooltipPad is the space between a tooltip's frame and its text.
const tooltipPad = 1

// tooltipRow is one line of a readout: a series' label in that series' own
// colour, so a line in the box and a band in the chart are matched by eye,
// and its value right-aligned against it.
type tooltipRow struct {
	label string
	value string
	color tcell.Color
}

// tooltipBoxSize is the box caption and rows need, frame and padding
// included.
func tooltipBoxSize(caption string, rows []tooltipRow) (w, h int) {
	w = core.DisplayWidth(caption)
	for _, row := range rows {
		if lw := core.DisplayWidth(row.label) + core.DisplayWidth(row.value) + 3; lw > w {
			w = lw
		}
	}
	return w + 2 + tooltipPad*2, len(rows) + 3
}

// placeTooltipBox positions a w×h box next to the pinned point (ax, ay),
// flipped to whichever side of it fits and clamped into view. A tooltip that
// hangs off the viewport is worse than no tooltip: the numbers it exists to
// show are the ones that get clipped.
//
// keepOut is a screen row the box must not cover — Activity Monitor's time
// callout, which names the moment every number in the box belongs to — or -1
// for none.
func placeTooltipBox(w, h, ax, ay int, view core.Rect, keepOut int) core.Rect {
	x := ax + 2
	if x+w > view.Right() {
		x = ax - w - 1
	}
	covers := func(top int) bool { return keepOut >= top && keepOut < top+h }
	y, above := ay+1, ay-h
	if y+h > view.Bottom() || (covers(y) && above >= view.Y && !covers(above)) {
		y = above
	}
	x = core.Clamp(x, view.X, max(view.Right()-w, view.X))
	y = core.Clamp(y, view.Y, max(view.Bottom()-h, view.Y))
	return core.Rect{X: x, Y: y, W: w, H: h}
}

// drawTooltipBox renders the readout at r: the caption, then one line per
// row. r comes from placeTooltipBox at the size tooltipBoxSize asked for;
// anything that doesn't fit is clipped rather than wrapped.
func drawTooltipBox(s tcell.Screen, r core.Rect, caption string, rows []tooltipRow) {
	body := theme.StyleTooltip()
	core.FillRect(s, r, ' ', body)
	core.DrawBox(s, r, theme.StyleTooltipBorder())

	x := r.X + 1 + tooltipPad
	textW := r.W - 2 - tooltipPad*2
	core.DrawTextClipped(s, x, r.Y+1, textW, body, caption)
	for i, row := range rows {
		y := r.Y + 2 + i
		if y >= r.Bottom()-1 {
			break
		}
		core.DrawTextClipped(s, x, y, textW, body.Foreground(row.color), row.label)
		core.DrawTextRight(s, x, y, textW, body, row.value)
	}
}
