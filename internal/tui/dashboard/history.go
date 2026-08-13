package dashboard

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// Fixed scales for the History panels. Live data varies by orders of
// magnitude between servers, so most panels auto-scale; these are the ones
// whose readings are only meaningful against a known range — a cache hit
// ratio is a percentage, and a latency chart that rescales to its own worst
// sample hides the difference between a fast server and a slow one.
var (
	cacheRatioScale = charts.Scale{Min: 0, Max: 100}
	cpuPercentScale = charts.Scale{Min: 0, Max: 100}
	latencyScale    = charts.Scale{Min: 0, Max: 250} // milliseconds
)

// DrawHistory renders the History dashboard into r, top to bottom:
// activity, waits, memory, database I/O. Each section is a title bar plus a
// row of panels; the waits section is full width because wait patterns need
// horizontal history to read.
//
// r is normally a canvas of HistoryCanvasW × HistoryCanvasH. A shorter or
// narrower rect simply loses the sections and panels that don't fit, which
// is why the caller scrolls a viewport rather than shrinking the rect.
// The returned hits describe where each chart's plot area landed and what
// it plotted, so a caller can turn a click into the sample under it. They
// are in r's coordinates — a caller drawing into an off-screen canvas has
// to translate them the same way it translates the pixels.
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
	inner := drawPanelTitle(s, panel, title)
	addHit(hits, title, c.Draw(s, inner), c.TimeRow(inner), c.Series, false)
}

// drawStackedChart draws one stacked history panel and records where its
// plot landed.
func drawStackedChart(s tcell.Screen, panel core.Rect, title string, c charts.StackedHistoryChart, hits *[]ChartHit) {
	inner := drawPanelTitle(s, panel, title)
	addHit(hits, title, c.Draw(s, inner), c.TimeRow(inner), c.Series, false)
}

// section draws one section's bar and returns the body rect under it plus
// the row the next section starts on. A body that would fall past the
// bottom of r comes back zero-sized, and every panel drawn into it clips to
// nothing.
func section(s tcell.Screen, r core.Rect, y, bodyH int, title string, kpis []charts.KPI) (core.Rect, int) {
	if y < r.Bottom() {
		// Guarded rather than clipped by the drawing helpers: a section bar
		// is a filled row, so one starting past the bottom of r would paint
		// a stripe across whatever sits below the dashboard.
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

	// Stacked against a fixed 0-100: the three parts are one machine's CPU
	// split, so the column is always full height and only the mix moves.
	// Dropped entirely when the body is too narrow to split, since waits is
	// the panel this section is named for.
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

	// Overlaid rather than stacked: these are total and target server
	// memory, and target is a ceiling the total sits under — stacking them
	// would draw a combined height that means nothing.
	drawChart(s, cols[0], "SQL SERVER MEMORY", charts.HistoryChart{
		Series:    v.Memory,
		TimeLabel: v.Header.SampleTime,
		Interval:  v.Interval,
	}, hits)

	if len(cols) > 1 {
		// Overlaid, not stacked: two hit ratios are two readings of the same
		// 0-100 scale, and stacking them draws a 200% column that pins the
		// panel to its ceiling and hides both.
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
