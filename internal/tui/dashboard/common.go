package dashboard

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/charts"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// Canvas sizes. A dashboard is always laid out at these dimensions and the
// caller scrolls a viewport over the result, so panel proportions stay put
// instead of reflowing every time the terminal is resized.
//
// The widths are the narrowest that still fit three side-by-side panels at
// the mockups' density; the heights are what the sections add up to. Both
// mockups are wider than this (~198 columns) and the extra width only ever
// buys more time buckets, which is exactly what a caller with a wider
// terminal gets by passing a wider canvas.
const (
	HistoryCanvasW = 150
	HistoryCanvasH = 61
	SampleCanvasW  = 150
	SampleCanvasH  = 50
)

// Section geometry. A section is a one-row title bar plus a body of panels;
// History's bodies are taller because each of their panels carries a time
// axis and a legend under the plot.
const (
	sectionBarH     = 1
	historyBodyH    = 14
	historySectionH = sectionBarH + historyBodyH
	sampleBodyH     = 11
	sampleSectionH  = sectionBarH + sampleBodyH
	// Activity gives two rows to Waits: activity is four labelled bars that
	// read fine short, while the waits panel carries one bar per category
	// plus its legend and is the section that runs out of room first. The
	// two totals still add up to the same canvas.
	sampleActivityBodyH = sampleBodyH - 2
	sampleWaitsBodyH    = sampleBodyH + 2
	panelGutter         = 1
	historyHeaderH      = 1
	sampleHeaderH       = 2
	panelTitleHeight    = 1
)

// drawHeader draws the identification strip at the top of a dashboard:
// instance and version on the left, sample position on the right, and the
// collector's status in between when it has something to say.
func drawHeader(s tcell.Screen, r core.Rect, h Header) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	pal := theme.Active()
	style := tcell.StyleDefault.Background(pal.MenuBar).Foreground(pal.Text)
	core.FillRect(s, r, ' ', style)

	left := h.Instance
	if h.Version != "" {
		left += "  " + h.Version
	}
	core.DrawTextClipped(s, r.X+1, r.Y, r.W-2, style.Foreground(pal.ChartCyan), left)

	right := h.SampleTime
	switch {
	case h.Paused && right != "":
		// Say so on the same line as the timestamp: a frozen dashboard that
		// still shows a plausible time is the one way this display can
		// actively mislead.
		right = "PAUSED  " + right
	case h.Paused:
		right = "PAUSED"
	}
	if h.Resolution != "" {
		right += "  (" + h.Resolution + ")"
	}
	if right != "" {
		core.DrawTextRight(s, r.X, r.Y, r.W-1, style, right)
	}

	if r.H > 1 && h.Host != "" {
		core.DrawTextClipped(s, r.X+1, r.Y+1, r.W-2, style.Foreground(pal.TextDim), h.Host)
	}
	if h.Status != "" {
		row := r.Y
		if r.H > 1 {
			row = r.Y + 1
		}
		core.DrawTextRight(s, r.X, row, r.W-1, style.Foreground(pal.Warning), h.Status)
	}
}

// drawSectionBar draws a section's title strip, with any KPI readouts
// right-aligned along it.
func drawSectionBar(s tcell.Screen, r core.Rect, title string, kpis []charts.KPI) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	pal := theme.Active()
	style := theme.StyleChartSection()
	core.FillRect(s, r, ' ', style)
	core.DrawTextClipped(s, r.X+1, r.Y, r.W-2, style, title)

	right := r.Right()
	for i := len(kpis) - 1; i >= 0; i-- {
		w := kpis[i].Width()
		if right-w <= r.X+core.DisplayWidth(title)+2 {
			return
		}
		k := kpis[i]
		k.LabelBg = pal.ChartSectionBg
		k.Draw(s, core.Rect{X: right - w, Y: r.Y, W: w, H: 1})
		right -= w + 1
	}
}

// drawPanelTitle draws one chart panel's heading and returns the rect left
// for the chart itself.
func drawPanelTitle(s tcell.Screen, r core.Rect, title string) core.Rect {
	if r.W <= 0 || r.H <= 0 {
		return core.Rect{}
	}
	style := theme.StyleChartTitle()
	core.FillRect(s, core.Rect{X: r.X, Y: r.Y, W: r.W, H: panelTitleHeight}, ' ', style)
	core.DrawTextClipped(s, r.X+1, r.Y, r.W-2, style, title)
	return core.Rect{X: r.X + 1, Y: r.Y + panelTitleHeight, W: r.W - 2, H: r.H - panelTitleHeight}
}

// splitColumns divides r into n equal-width columns separated by a gutter.
// A rect too narrow to give every column at least one usable column of its
// own returns r whole as a single panel, which is what "degrade
// predictably" means here: fewer, readable panels beat n unreadable slivers.
func splitColumns(r core.Rect, n int) []core.Rect {
	if n <= 1 || r.W < n*minPanelW {
		return []core.Rect{r}
	}
	each := (r.W - (n-1)*panelGutter) / n
	out := make([]core.Rect, 0, n)
	x := r.X
	for i := 0; i < n; i++ {
		w := each
		if i == n-1 {
			w = r.Right() - x // the last panel absorbs the rounding
		}
		out = append(out, core.Rect{X: x, Y: r.Y, W: w, H: r.H})
		x += w + panelGutter
	}
	return out
}

// minPanelW is the narrowest a side-by-side chart panel may get before the
// section collapses to a single full-width panel: an axis gutter, a legend
// square, and enough plot to read a trend.
const minPanelW = 24

// kpi builds a section-bar readout, blank-tolerant: an empty value renders
// as a dash rather than an empty box, so a missing metric reads as missing
// instead of as zero.
func kpi(label, value string) charts.KPI {
	if value == "" {
		value = "—"
	}
	return charts.KPI{Label: label, Value: value}
}

// addHit records where one chart's plot landed so a click can be turned back
// into the sample under it. A zero-sized plot or a chart with no series is
// dropped rather than recorded: both draw nothing, and a hit over nothing
// would pin a tooltip with no numbers in it.
//
// snapshot distinguishes a current-sample chart, whose series carry one value
// each, from a history — see ChartHit.Snapshot for what it changes. timeRow
// is the chart's time-scale row, zero for a chart that draws none.
func addHit(hits *[]ChartHit, title string, plot, timeRow core.Rect, series []charts.Series, snapshot bool) {
	if hits == nil || plot.W <= 0 || plot.H <= 0 || len(series) == 0 {
		return
	}
	*hits = append(*hits, ChartHit{Title: title, Plot: plot, TimeRow: timeRow, Series: series, Snapshot: snapshot})
}
