package charts

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// Bar is one labelled value — the unit both BarChart and VBarChart plot.
// Short is the abbreviated label used when the full one doesn't fit.
//
// A bar is either a single colour (Value plus Color) or a stack of
// contributions (Parts), which is what the snapshot dashboard's wait
// categories need: one bar per category, split into resource and signal
// time. Parts wins when both are set.
type Bar struct {
	Label string
	Short string
	Value float64
	Color tcell.Color
	Parts []BarPart
}

// BarPart is one contribution to a stacked bar, drawn in slice order from
// the bar's base.
type BarPart struct {
	Value float64
	Color tcell.Color
}

// total is the bar's full length in value terms: the sum of its parts, or
// its own Value when it has none.
func (b Bar) total() float64 {
	if len(b.Parts) == 0 {
		return b.Value
	}
	sum := 0.0
	for _, p := range b.Parts {
		if p.Value > 0 {
			sum += p.Value
		}
	}
	return sum
}

// segments converts the bar into stack segments sized in cells, so a
// single-colour bar and a stacked one render through the same path and get
// the same boundary treatment.
func (b Bar) segments(sc Scale, cells int) []segment {
	if len(b.Parts) == 0 {
		return []segment{{color: b.Color, cells: sc.Cells(b.Value, cells)}}
	}
	out := make([]segment, 0, len(b.Parts))
	running, prev := 0.0, 0.0
	for _, p := range b.Parts {
		if p.Value <= 0 {
			continue
		}
		running += p.Value
		at := sc.Cells(running, cells)
		out = append(out, segment{color: p.Color, cells: at - prev})
		prev = at
	}
	return out
}

// labelFor picks the widest of Label/Short that fits maxW, truncating only
// when neither does — the same degradation order as a legend.
func (b Bar) labelFor(maxW int) string {
	return Series{Label: b.Label, Short: b.Short}.labelFor(maxW)
}

// BarChart draws one horizontal bar per value, each on its own row, with
// eighth-block smoothing so a bar's length tracks its value to an eighth of
// a column instead of snapping to whole cells.
//
// Rows are the natural shape for a handful of named current values — the
// snapshot dashboard's activity and wait-category panels — where a
// time-series chart would have nothing to show along its X axis.
type BarChart struct {
	Bars []Bar

	// Scale fixes the value range. The zero Scale derives it from the
	// largest bar.
	Scale Scale

	// LabelWidth reserves a fixed left gutter for row labels. 0 sizes the
	// gutter from the labels themselves; -1 omits labels entirely, for a
	// chart whose rows are already named by a legend.
	LabelWidth int

	// ShowValues writes each bar's formatted value after its bar.
	ShowValues bool
}

// Draw renders the chart into r. Bars beyond r's height are dropped: a
// clipped-off bar is better than squeezing every bar below one row.
func (b BarChart) Draw(s tcell.Screen, r core.Rect) {
	if r.W <= 0 || r.H <= 0 || len(b.Bars) == 0 {
		blankRect(s, r)
		return
	}
	sc := b.Scale
	if sc.IsZero() {
		sc = AutoScale(barsMax(b.Bars))
	}
	blankRect(s, r)

	labelW := b.labelGutter(r.W)
	valueW := b.valueGutter()
	barW := r.W - labelW - valueW
	if barW <= 0 {
		// No room for bars once the labels are placed; the labels alone
		// still carry more than a blank panel would.
		barW, valueW = 0, 0
		labelW = r.W
	}

	bg := theme.Active().ChartPlotBg
	labelStyle := theme.StyleChartAxis()
	for i, bar := range b.Bars {
		if i >= r.H {
			return
		}
		y := r.Y + i
		if labelW > 0 {
			core.DrawTextClipped(s, r.X, y, labelW-1, labelStyle, bar.labelFor(labelW-1))
		}
		if barW > 0 {
			core.FillRect(s, core.Rect{X: r.X + labelW, Y: y, W: barW, H: 1}, ' ', theme.StyleChartPlot())
			drawHRun(s, r.X+labelW, y, composeStack(barW, bar.segments(sc, barW), bg))
		}
		if valueW > 0 {
			core.DrawTextRight(s, r.Right()-valueW, y, valueW, labelStyle, FormatValue(bar.total()))
		}
	}
}

// labelGutter resolves LabelWidth, capping an auto-sized gutter at a third
// of the chart so long labels can't crowd out the data.
func (b BarChart) labelGutter(w int) int {
	if b.LabelWidth < 0 {
		return 0
	}
	if b.LabelWidth > 0 {
		return core.Min(b.LabelWidth, w)
	}
	widest := 0
	for _, bar := range b.Bars {
		widest = core.Max(widest, core.DisplayWidth(bar.Label))
	}
	if widest == 0 {
		return 0
	}
	return core.Min(widest+1, core.Max(w/3, 1))
}

// valueGutter is the width the formatted values need, or 0 when they aren't
// shown.
func (b BarChart) valueGutter() int {
	if !b.ShowValues {
		return 0
	}
	widest := 0
	for _, bar := range b.Bars {
		widest = core.Max(widest, core.DisplayWidth(FormatValue(bar.total())))
	}
	return widest + 1
}

// barsMax is the largest bar value, for auto-scaling.
func barsMax(bars []Bar) float64 {
	max := 0.0
	for _, b := range bars {
		if t := b.total(); t > max {
			max = t
		}
	}
	return max
}
