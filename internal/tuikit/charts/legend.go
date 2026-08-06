package charts

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// legendGap is the blank width between two legend entries.
const legendGap = 2

// LegendItem is one entry: a coloured square followed by a label.
type LegendItem struct {
	Label string
	Short string
	Color tcell.Color
}

// LegendItems builds legend entries from the series a chart drew, in the
// same order, so square colours match plotted colours by construction.
func LegendItems(series []Series) []LegendItem {
	out := make([]LegendItem, 0, len(series))
	for _, s := range series {
		out = append(out, LegendItem{Label: s.Label, Short: s.Short, Color: s.Color})
	}
	return out
}

// DrawLegend renders items into r, wrapping across r's rows.
//
// Degradation is ordered: full labels if they fit, short labels if those
// fit, and clipping only once neither does — a legend loses text detail
// before it loses entries, since an entry dropped entirely leaves a plotted
// colour unexplained.
func DrawLegend(s tcell.Screen, r core.Rect, items []LegendItem) {
	if r.W <= 0 || r.H <= 0 || len(items) == 0 {
		return
	}
	style := theme.StyleChartAxis()
	core.FillRect(s, r, ' ', style)

	labels := legendLabels(items, r.W, r.H)
	x, y := r.X, r.Y
	for i, label := range labels {
		entryW := 2 + core.DisplayWidth(label) // square + space + label
		if x > r.X && x+entryW > r.Right() {
			// Doesn't fit on this row: wrap if there's another row, else
			// stop — a half-drawn entry reads worse than a short legend.
			if y+1 >= r.Bottom() {
				return
			}
			x, y = r.X, y+1
		}
		s.SetContent(x, y, LegendSquare, nil, style.Foreground(items[i].Color))
		core.DrawTextClipped(s, x+2, y, r.Right()-(x+2), style, label)
		x += entryW + legendGap
	}
}

// legendLabels picks the label text for every entry: full labels when the
// whole legend fits the available cells, short labels otherwise, each then
// truncated to what one row can hold.
func legendLabels(items []LegendItem, w, h int) []string {
	full := make([]string, len(items))
	short := make([]string, len(items))
	for i, it := range items {
		full[i] = it.Label
		short[i] = it.Short
		if short[i] == "" {
			short[i] = it.Label
		}
	}
	if legendWidth(full) <= w*h {
		return truncateAll(full, w-2)
	}
	return truncateAll(short, w-2)
}

// legendWidth is the total width every entry needs laid end to end.
func legendWidth(labels []string) int {
	total := 0
	for i, l := range labels {
		if i > 0 {
			total += legendGap
		}
		total += 2 + core.DisplayWidth(l)
	}
	return total
}

func truncateAll(labels []string, maxW int) []string {
	if maxW < 1 {
		maxW = 1
	}
	out := make([]string, len(labels))
	for i, l := range labels {
		out[i] = core.Truncate(l, maxW)
	}
	return out
}
