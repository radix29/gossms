package dashboard

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// Fixed scales for the History panels. Most panels auto-scale since data varies
// by orders of magnitude between servers; these are meaningful only against a
// known range (a hit ratio is a percentage; a latency chart rescaling to its
// worst sample hides the difference between fast and slow servers).
var (
	cacheRatioScale = charts.Scale{Min: 0, Max: 100}
	cpuPercentScale = charts.Scale{Min: 0, Max: 100}
	latencyScale    = charts.Scale{Min: 0, Max: 250} // milliseconds
)

// DrawHistory renders the History dashboard into r, top to bottom: activity,
// waits, memory, database I/O. Each section is a title bar plus a row of
// panels; waits is full width since wait patterns need horizontal history.
//
// r is normally a canvas of HistoryCanvasW × HistoryCanvasH. A smaller rect
// loses the sections and panels that don't fit, which is why the caller
// scrolls a viewport rather than shrinking the rect. The returned hits give
// each chart's plot area and what it plotted, so a click maps to a sample. They
// are in r's coordinates; a caller drawing off-screen must translate them as it
// does the pixels.
func DrawHistory(s tcell.Screen, r core.Rect, v HistoryView) []ChartHit {
	if r.W <= 0 || r.H <= 0 {
		return nil
	}
	drawHeader(s, core.Rect{X: r.X, Y: r.Y, W: r.W, H: historyHeaderH}, v.Header)

	hits := make([]ChartHit, 0, 10)
	y := r.Y + historyHeaderH
	y = historyActivity(s, r, y, v, &hits)
	y = historyWaits(s, r, y, v, &hits)
	y = historyMemory(s, r, y, v, &hits)
	historyDatabaseIO(s, r, y, v, &hits)
	return hits
}

// drawChart draws one overlaid history panel and records where its plot
// landed.
func drawChart(s tcell.Screen, panel core.Rect, title string, c charts.HistoryChart, hits *[]ChartHit) {
	inner := charts.DrawPanelTitle(s, panel, title)
	plot, timeRow := c.DrawFrame(s, inner)
	addHit(hits, title, plot, timeRow, c.Series, false)
}

// drawStackedChart draws one stacked history panel and records where its
// plot landed.
func drawStackedChart(s tcell.Screen, panel core.Rect, title string, c charts.StackedHistoryChart, hits *[]ChartHit) {
	inner := charts.DrawPanelTitle(s, panel, title)
	plot, timeRow := c.DrawFrame(s, inner)
	addHit(hits, title, plot, timeRow, c.Series, false)
}

// section draws one section's bar and returns the body rect under it plus
// the row the next section starts on. A body past the bottom of r comes back
// zero-sized, so panels drawn into it clip to nothing.
func section(s tcell.Screen, r core.Rect, y, bodyH int, title string, kpis []charts.KPI) (core.Rect, int) {
	if y < r.Bottom() {
		// Guarded, not left to the helpers' clipping: a section bar is a filled
		// row, so one past the bottom of r would stripe whatever lies below.
		drawSectionBar(s, core.Rect{X: r.X, Y: y, W: r.W, H: sectionBarH}, title, kpis)
	}
	body := core.Rect{X: r.X, Y: y + sectionBarH, W: r.W, H: bodyH}
	if body.Bottom() > r.Bottom() {
		body.H = max(r.Bottom()-body.Y, 0)
	}
	return body, y + sectionBarH + bodyH
}

func historyActivity(s tcell.Screen, r core.Rect, y int, v HistoryView, hits *[]ChartHit) int {
	body, next := section(s, r, y, historyBodyH, "SQL SERVER ACTIVITY", v.ActivityKPIs)
	cols := splitColumns(body, 3)

	drawStackedChart(s, cols[0], "SQL SERVER ACTIVITY", charts.StackedHistoryChart{
		Series:    v.Activity,
		TimeLabel: v.Header.SampleTime,
		Interval:  v.Interval,
	}, hits)

	if len(cols) > 1 {
		drawStackedChart(s, cols[1], "Key lookups / Forwarded recs", charts.StackedHistoryChart{
			Series:    v.Lookups,
			TimeLabel: v.Header.SampleTime,
			Interval:  v.Interval,
		}, hits)
	}
	if len(cols) > 2 {
		drawChart(s, cols[2], "BACKUP THROUGHPUT", charts.HistoryChart{
			Series:    v.Backup,
			TimeLabel: v.Header.SampleTime,
			Interval:  v.Interval,
		}, hits)
	}
	return next
}

func historyWaits(s tcell.Screen, r core.Rect, y int, v HistoryView, hits *[]ChartHit) int {
	body, next := section(s, r, y, historyBodyH, "SQL SERVER WAITS", v.WaitsKPIs)
	cols := splitColumns(body, 2)

	// Stacked on a fixed 0-100: the parts are one machine's CPU split, so the
	// column is always full height and only the mix moves. Dropped when the
	// body is too narrow to split; waits is the section's namesake.
	waits := cols[0]
	if len(cols) > 1 {
		drawStackedChart(s, cols[0], "CPU usage", charts.StackedHistoryChart{
			Series:     v.CPU,
			Scale:      cpuPercentScale,
			TimeLabel:  v.Header.SampleTime,
			Interval:   v.Interval,
			LegendRows: 1,
		}, hits)
		waits = cols[1]
	}
	drawStackedChart(s, waits, "SQL SERVER WAITS", charts.StackedHistoryChart{
		Series:     v.Waits,
		TimeLabel:  v.Header.SampleTime,
		Interval:   v.Interval,
		LegendRows: 1,
	}, hits)
	return next
}

func historyMemory(s tcell.Screen, r core.Rect, y int, v HistoryView, hits *[]ChartHit) int {
	body, next := section(s, r, y, historyBodyH, "SQL SERVER MEMORY", v.MemoryKPIs)
	cols := splitColumns(body, 3)

	// Overlaid, not stacked: total and target server memory, target being a
	// ceiling the total sits under; a combined height would mean nothing.
	drawChart(s, cols[0], "SQL SERVER MEMORY", charts.HistoryChart{
		Series:    v.Memory,
		TimeLabel: v.Header.SampleTime,
		Interval:  v.Interval,
	}, hits)

	if len(cols) > 1 {
		// Overlaid, not stacked: two readings of one 0-100 scale; stacking draws
		// a 200% column pinned to the ceiling that hides both.
		drawChart(s, cols[1], "CACHE HIT RATIOS / PLE", charts.HistoryChart{
			Series:    v.CacheRatios,
			Scale:     cacheRatioScale,
			TimeLabel: v.Header.SampleTime,
			Interval:  v.Interval,
		}, hits)
	}
	if len(cols) > 2 {
		drawChart(s, cols[2], "PAGES READ / WRITE", charts.HistoryChart{
			Series:    v.Pages,
			TimeLabel: v.Header.SampleTime,
			Interval:  v.Interval,
		}, hits)
	}
	return next
}

func historyDatabaseIO(s tcell.Screen, r core.Rect, y int, v HistoryView, hits *[]ChartHit) int {
	file := v.File
	if file == "" {
		file = "Total"
	}
	body, next := section(s, r, y, historyBodyH, "DATABASE IO", []charts.KPI{kpi("File", file)})
	cols := splitColumns(body, 3)

	drawChart(s, cols[0], "DATABASE IO", charts.HistoryChart{
		Series:    v.DatabaseIO,
		Scale:     latencyScale,
		TimeLabel: v.Header.SampleTime,
		Interval:  v.Interval,
	}, hits)

	if len(cols) > 1 {
		drawChart(s, cols[1], "LOG FLUSHES", charts.HistoryChart{
			Series:    v.LogFlushes,
			TimeLabel: v.Header.SampleTime,
			Interval:  v.Interval,
		}, hits)
	}
	if len(cols) > 2 {
		drawChart(s, cols[2], "CHECKPOINTS / LAZY WRITES", charts.HistoryChart{
			Series:    v.Checkpoints,
			TimeLabel: v.Header.SampleTime,
			Interval:  v.Interval,
		}, hits)
	}
	return next
}
