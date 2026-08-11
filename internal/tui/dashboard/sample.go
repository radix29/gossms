package dashboard

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// DrawSample renders the Sample dashboard into r: the same four sections as
// History, but showing the current sample rather than its history. Numbers
// that read better as figures than as bars sit in the section bars as KPIs.
//
// r is normally a canvas of SampleCanvasW × SampleCanvasH.
//
// The returned hits describe the panels a click can be resolved against —
// the memory composition bar, whose segments are otherwise named only by a
// legend that has no room for their values.
func DrawSample(s tcell.Screen, r core.Rect, v SampleView) []ChartHit {
	if r.W <= 0 || r.H <= 0 {
		return nil
	}
	drawHeader(s, core.Rect{X: r.X, Y: r.Y, W: r.W, H: sampleHeaderH}, v.Header)

	hits := make([]ChartHit, 0, 1)
	y := r.Y + sampleHeaderH
	y = sampleActivity(s, r, y, v)
	y = sampleWaits(s, r, y, v)
	y = sampleMemory(s, r, y, v, &hits)
	sampleDatabaseIO(s, r, y, v)
	return hits
}

func sampleActivity(s tcell.Screen, r core.Rect, y int, v SampleView) int {
	body, next := section(s, r, y, sampleActivityBodyH, "SQL SERVER ACTIVITY", []charts.KPI{
		kpi("User Connections", v.UserConnections),
		kpi("Blocked Processes", v.BlockedProcesses),
	})
	cols := splitColumns(body, 3)

	drawBarPanel(s, cols[0], "SQL SERVER ACTIVITY", v.Activity)
	if len(cols) > 1 {
		drawBarPanel(s, cols[1], "Key lookups / Forwarded recs", v.Lookups)
	}
	if len(cols) > 2 {
		drawBarPanel(s, cols[2], "BACKUP THROUGHPUT", v.Backup)
	}
	return next
}

func sampleWaits(s tcell.Screen, r core.Rect, y int, v SampleView) int {
	body, next := section(s, r, y, sampleWaitsBodyH, "SQL SERVER WAITS", []charts.KPI{
		kpi("CPU % of Total Waits", v.CPUPctOfWaits),
	})
	// The load factor panel takes only the width its cores need, so the
	// waits chart keeps everything left over rather than being halved on a
	// four-core box.
	waitsBody := body
	if w := loadFactorWidth(len(v.LoadFactor.Bars), body.W); w > 0 {
		lf := core.Rect{X: body.X, Y: body.Y, W: w, H: body.H}
		charts.VBarChart{Bars: v.LoadFactor.Bars, Scale: v.LoadFactor.Scale, ShowLabels: true}.
			Draw(s, drawPanelTitle(s, lf, "Load Factor"))
		waitsBody = core.Rect{X: lf.Right() + panelGutter, Y: body.Y,
			W: body.Right() - lf.Right() - panelGutter, H: body.H}
	}

	// One bar per wait category, each split into its resource and signal
	// parts, so the categories stay comparable and the split stays visible.
	plot := waitsBody
	if len(v.WaitLegend) > 0 && plot.H > 1 {
		plot.H--
		charts.DrawLegend(s, core.Rect{X: waitsBody.X + 1, Y: plot.Bottom(), W: waitsBody.W - 2, H: 1}, v.WaitLegend)
	}
	charts.VBarChart{Bars: v.Waits.Bars, Scale: v.Waits.Scale, ShowLabels: true}.Draw(s, core.Rect{
		X: plot.X + 1, Y: plot.Y, W: plot.W - 2, H: plot.H,
	})
	return next
}

// Load Factor panel geometry: one slot per core, wide enough for a two-
// column bar with a gap and the cpu_id under it, plus the panel's own
// padding and the Y-axis gutter.
const (
	loadFactorSlotW   = 4
	loadFactorChromeW = 7
)

// loadFactorWidth is how many columns the Load Factor panel gets in a body
// of width bodyW, or 0 when there are no cores to draw or no room for the
// panel. It never takes more than half the section: the waits chart is what
// this section is named for, and a 64-core server would otherwise push it
// off the canvas entirely.
func loadFactorWidth(cores, bodyW int) int {
	if cores <= 0 || bodyW <= 0 {
		return 0
	}
	// Not held to minPanelW: this panel is deliberately narrow on a small
	// box, and widening it to a general panel's minimum would take room from
	// the waits chart for empty columns.
	w := core.Min(loadFactorChromeW+cores*loadFactorSlotW, bodyW/2)
	if w < loadFactorChromeW+loadFactorSlotW {
		return 0
	}
	return w
}

func sampleMemory(s tcell.Screen, r core.Rect, y int, v SampleView, hits *[]ChartHit) int {
	body, next := section(s, r, y, sampleBodyH, "SQL SERVER MEMORY", []charts.KPI{
		kpi("Page Life Expectancy", v.PageLifeExpectancy),
		kpi("Memory Grants Pending", v.MemoryGrantsPending),
	})
	cols := splitColumns(body, 3)

	// The composition bar is the section's centrepiece: it gets several
	// rows so its segments are legible, with its own legend beneath.
	bar := charts.StackedBar{
		Series:     v.Memory,
		Rows:       core.Max(cols[0].H/3, 1),
		LegendRows: 2,
		ShowTotal:  true,
	}.Draw(s, drawPanelTitle(s, cols[0], "MEMORY COMPOSITION"))
	addHit(hits, "MEMORY COMPOSITION", bar, core.Rect{}, v.Memory, true)

	if len(cols) > 1 {
		ratios := v.CacheRatios
		if ratios.Scale.IsZero() {
			ratios.Scale = cacheRatioScale
		}
		drawBarPanel(s, cols[1], "CACHE HIT RATIOS", ratios)
	}
	if len(cols) > 2 {
		charts.VBarChart{Bars: v.Pages.Bars, Scale: v.Pages.Scale, ShowLabels: true}.
			Draw(s, drawPanelTitle(s, cols[2], "PAGES READ / WRITE"))
	}
	return next
}

func sampleDatabaseIO(s tcell.Screen, r core.Rect, y int, v SampleView) int {
	body, next := section(s, r, y, sampleBodyH, "DATABASE IO", []charts.KPI{
		kpi("Log Flushes", v.LogFlushes),
		kpi("Checkpoint Pages", v.CheckpointPages),
		kpi("Lazy Writes", v.LazyWrites),
	})
	charts.VBarChart{Bars: v.DatabaseIO.Bars, Scale: v.DatabaseIO.Scale, ShowLabels: true}.
		Draw(s, drawPanelTitle(s, body, "ms/Read and ms/Write by file"))
	return next
}

// drawBarPanel draws one titled horizontal-bar panel with its legend.
func drawBarPanel(s tcell.Screen, panel core.Rect, title string, p BarPanel) {
	charts.BarChart{Bars: p.Bars, Scale: p.Scale, ShowValues: true, LabelWidth: -1}.
		Draw(s, withLegend(s, panel, title, p.Bars))
}

// withLegend draws the panel's title, reserves its last row for a legend
// naming the bars, and returns what's left for the chart itself. A bar
// chart drawn with LabelWidth -1 has no row labels, so this legend is what
// identifies its colours.
func withLegend(s tcell.Screen, panel core.Rect, title string, bars []charts.Bar) core.Rect {
	r := drawPanelTitle(s, panel, title)
	if r.H <= 1 || len(bars) == 0 {
		return r
	}
	items := make([]charts.LegendItem, 0, len(bars))
	for _, b := range bars {
		items = append(items, charts.LegendItem{Label: b.Label, Short: b.Short, Color: b.Color})
	}
	charts.DrawLegend(s, core.Rect{X: r.X, Y: r.Bottom() - 1, W: r.W, H: 1}, items)
	return core.Rect{X: r.X, Y: r.Y, W: r.W, H: r.H - 1}
}
