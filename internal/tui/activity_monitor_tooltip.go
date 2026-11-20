package tui

import (
	"slices"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/charts"
	"github.com/radix29/tuikit/core"
)

// The pinned readout's life (what it is pinned to, how it follows its sample,
// when it closes) is tuikit's charts.Pin. This file supplies what a bucket's
// time means on each tab; the pan-drops-pin rule is panned's.

// pinTooltip pins the readout for a click at (mx, my), or nil if it missed a
// plot area or hit a column without a sample.
func (am *ActivityMonitor) pinTooltip(mx, my int) *charts.Pin {
	if !am.chartTab() {
		return nil
	}
	return charts.PinAt(am.hits, am.viewport(), mx, my, am.bucketTime)
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
	if am.tooltip != nil && !am.tooltip.Refresh(am.hits, am.viewport(), am.bucketIndex, am.bucketTime) {
		am.tooltip = nil
	}
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

// drawTooltip renders the pinned readout over the canvas just blitted: the
// column's time on the chart's time axis, then the box with one line per
// series in its colour. c is that canvas; see charts.DrawAxisCallout.
func (am *ActivityMonitor) drawTooltip(s tcell.Screen, c *core.Buffer) {
	if am.tooltip != nil {
		am.tooltip.Draw(s, c, am.viewport())
	}
}
