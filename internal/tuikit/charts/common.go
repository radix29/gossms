package charts

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// Series is one named, coloured metric. Values are ordered oldest first for
// a time-series chart, or left to right for a categorical one.
//
// Short is an abbreviated Label used when a legend can't fit the full text
// ("Trans" for "Transactions"). Empty Short means Label is already as short
// as it goes and clipping is the only remaining option.
type Series struct {
	Label  string
	Short  string
	Color  tcell.Color
	Values []float64
}

// At returns the value at index i, or 0 when i is outside the series —
// series in one chart may differ in length while a metric is still filling
// its buffer, and a missing sample reads as zero rather than shortening the
// whole chart.
func (s Series) At(i int) float64 {
	if i < 0 || i >= len(s.Values) {
		return 0
	}
	return s.Values[i]
}

// labelFor returns Label if it fits maxW display columns, else Short if
// that fits, else the better of the two truncated to maxW.
func (s Series) labelFor(maxW int) string {
	if core.DisplayWidth(s.Label) <= maxW {
		return s.Label
	}
	if s.Short != "" && core.DisplayWidth(s.Short) <= maxW {
		return s.Short
	}
	text := s.Label
	if s.Short != "" {
		text = s.Short
	}
	return core.Truncate(text, maxW)
}

// maxLen is the longest Values length across the series — the number of
// buckets a time-series chart has to plot.
func maxLen(series []Series) int {
	n := 0
	for _, s := range series {
		n = core.Max(n, len(s.Values))
	}
	return n
}

// maxValue is the largest single value across every series, for scaling a
// chart whose series are drawn independently.
func maxValue(series []Series) float64 {
	max := 0.0
	for _, s := range series {
		for _, v := range s.Values {
			if v > max {
				max = v
			}
		}
	}
	return max
}

// maxStackTotal is the largest per-bucket sum across every series, for
// scaling a chart whose series stack on top of one another. Negative values
// don't contribute: a stack has no meaning below its baseline, and the
// column renderer drops them too.
func maxStackTotal(series []Series) float64 {
	max := 0.0
	for i, n := 0, maxLen(series); i < n; i++ {
		total := 0.0
		for _, s := range series {
			if v := s.At(i); v > 0 {
				total += v
			}
		}
		if total > max {
			max = total
		}
	}
	return max
}

// segment is one coloured piece of a stacked column or bar, sized in cells.
type segment struct {
	color tcell.Color
	cells float64
}

// stackCell is one composed cell of a stacked run: the two colours it
// splits between and where the split falls, in eighths of the cell. split
// == 8 means one colour owns the whole cell; 0 means the cell is past the
// end of the stack.
//
// Orientation is applied at draw time, not here: the same composition
// serves a vertical column (lower colour at the bottom) and a horizontal
// bar (lower colour at the left).
type stackCell struct {
	lower, upper tcell.Color
	split        int
}

// vertical renders the cell as part of a bottom-up column.
func (c stackCell) vertical() (rune, tcell.Color, tcell.Color) {
	return c.glyph(VBlock)
}

// horizontal renders the cell as part of a left-to-right bar.
func (c stackCell) horizontal() (rune, tcell.Color, tcell.Color) {
	return c.glyph(HBlock)
}

func (c stackCell) glyph(block func(int) rune) (rune, tcell.Color, tcell.Color) {
	switch {
	case c.split >= 8:
		return FullBlock, c.lower, c.upper
	case c.split <= 0:
		return ' ', c.upper, c.upper
	}
	return block(c.split), c.lower, c.upper
}

// filled reports whether the cell carries any of the stack — cells past the
// end are skipped when drawing so the plot's grid dots stay visible behind
// and above the data.
func (c stackCell) filled() bool { return c.split > 0 }

// composeStack turns a run of segments into exactly length composed cells,
// ordered from the base of the stack outward.
//
// Every cell is filled — a cell spanning a segment boundary carries the
// lower segment as its foreground and the upper as its background, so the
// partial block glyph shows both and the stack has no internal hole. This
// is the whole reason stacked columns read as continuous: rendering each
// segment independently and rounding its own height leaves gaps and double
// counts at every boundary. Cells past the top of the stack are filled with
// bg.
//
// When more than one boundary falls inside a single cell (segments thinner
// than a cell), the cell keeps the segment covering most of its lower half
// and the one covering most of its upper half; the rest are dropped rather
// than drawn as a hole.
func composeStack(length int, segs []segment, bg tcell.Color) []stackCell {
	out := make([]stackCell, core.Max(length, 0))
	for i := range out {
		out[i] = stackCell{lower: bg, upper: bg}
	}
	if len(out) == 0 {
		return out
	}

	if len(segs) == 1 {
		// A single segment has no internal boundary to place, so the eighths
		// pass below buys nothing and its owner slice is pure allocation.
		// HistoryChart.drawColumns takes this path once per series per
		// column, which is most of the dashboard's stack composition.
		whole, rem := eighths(segs[0].cells)
		for i := range out {
			switch {
			case i < whole:
				out[i] = stackCell{lower: segs[0].color, upper: bg, split: 8}
			case i == whole && rem > 0:
				out[i] = stackCell{lower: segs[0].color, upper: bg, split: rem}
			}
		}
		return out
	}

	// Lay the segments out along one axis measured in eighths of a cell, so
	// a boundary can be placed inside a cell rather than snapped to one.
	limit := len(out) * 8
	owner := make([]int, limit) // eighth → segment index, -1 = past the top
	for i := range owner {
		owner[i] = -1
	}
	pos := 0
	for si, seg := range segs {
		if seg.cells <= 0 || pos >= limit {
			continue
		}
		whole, rem := eighths(seg.cells)
		n := whole*8 + rem
		for k := 0; k < n && pos < limit; k, pos = k+1, pos+1 {
			owner[pos] = si
		}
	}

	for c := range out {
		lower, upper, split := splitCell(owner[c*8:c*8+8], segs, bg)
		out[c] = stackCell{lower: lower, upper: upper, split: split}
	}
	return out
}

// drawVRun draws a composed stack as a column: cells[0] on row bottomY,
// growing upward. Cells past the end of the stack are left alone so the
// plot's grid shows through above the data.
func drawVRun(s tcell.Screen, x, bottomY int, cells []stackCell) {
	for i, c := range cells {
		if !c.filled() {
			continue
		}
		ch, fg, bg := c.vertical()
		s.SetContent(x, bottomY-i, ch, nil, tcell.StyleDefault.Foreground(fg).Background(bg))
	}
}

// drawHRun draws a composed stack as a bar: cells[0] at column startX,
// growing rightward.
func drawHRun(s tcell.Screen, startX, y int, cells []stackCell) {
	for i, c := range cells {
		if !c.filled() {
			continue
		}
		ch, fg, bg := c.horizontal()
		s.SetContent(startX+i, y, ch, nil, tcell.StyleDefault.Foreground(fg).Background(bg))
	}
}

// splitCell picks the two colours one cell of a stack shows and how many
// eighths the lower one gets. cell holds the segment index owning each
// eighth (-1 = past the top of the stack).
//
// A cell holding three or more segments keeps the lowest two: the third is
// thinner than an eighth of a cell, and absorbing it into its neighbour is
// the only representation that doesn't leave a gap.
func splitCell(cell []int, segs []segment, bg tcell.Color) (lower, upper tcell.Color, split int) {
	colorOf := func(i int) tcell.Color {
		if i >= 0 && i < len(segs) {
			return segs[i].color
		}
		return bg
	}
	first := cell[0]
	if first < 0 {
		return bg, bg, 0
	}
	split = 1
	for split < len(cell) && cell[split] == first {
		split++
	}
	if split == len(cell) {
		return colorOf(first), bg, len(cell)
	}
	return colorOf(first), colorOf(cell[split]), split
}

// plotFrame is a chart's rects once its chrome has been reserved: the
// Y-axis label gutter, the plot area itself, the time-label row under it,
// and the legend rows under that. Any of them may be zero-sized when the
// chart's rect is too small to hold it.
type plotFrame struct {
	axis    core.Rect
	plot    core.Rect
	timeRow core.Rect
	legend  core.Rect
}

// axisGutter is the width reserved for Y-axis labels, computed from the
// widest label the scale will actually produce plus a trailing space.
func axisGutter(sc Scale, levels int) int {
	w := 0
	for _, t := range sc.Ticks(levels) {
		w = core.Max(w, core.DisplayWidth(FormatValue(t)))
	}
	return w + 1
}

// layoutPlot divides r into the axis gutter, plot, time-label row, and
// legend rows. Chrome is dropped from the outside in when r is too small —
// legend first, then the time row — so a squeezed chart loses labelling
// before it loses data.
func layoutPlot(r core.Rect, gutter, legendRows int) plotFrame {
	var f plotFrame
	if r.W <= 0 || r.H <= 0 {
		return f
	}
	if gutter >= r.W {
		gutter = 0 // no room for labels; give the whole width to the plot
	}

	bottom := r.Bottom()
	if legendRows > 0 && r.H > legendRows+1 {
		f.legend = core.Rect{X: r.X, Y: bottom - legendRows, W: r.W, H: legendRows}
		bottom -= legendRows
	}
	if bottom-r.Y > 1 {
		f.timeRow = core.Rect{X: r.X + gutter, Y: bottom - 1, W: r.W - gutter, H: 1}
		bottom--
	}

	f.axis = core.Rect{X: r.X, Y: r.Y, W: gutter, H: bottom - r.Y}
	f.plot = core.Rect{X: r.X + gutter, Y: r.Y, W: r.W - gutter, H: bottom - r.Y}
	return f
}

// drawPlotBackground clears the plot area and lays the muted dot grid and
// dotted time dividers over it. gridEvery is the column spacing between
// dividers; zero or less draws none.
func drawPlotBackground(s tcell.Screen, plot core.Rect, gridEvery int) {
	if plot.W <= 0 || plot.H <= 0 {
		return
	}
	bgStyle := theme.StyleChartPlot()
	gridStyle := theme.StyleChartGrid()
	core.FillRect(s, plot, ' ', bgStyle)

	// Dots on every third column and every other row: dense enough to read
	// heights against, sparse enough not to compete with the data.
	for y := plot.Y; y < plot.Bottom(); y += 2 {
		for x := plot.X + 1; x < plot.Right(); x += 3 {
			s.SetContent(x, y, GridDot, nil, gridStyle)
		}
	}
	if gridEvery <= 0 {
		return
	}
	// Dividers are anchored to the right edge: the newest bucket is always
	// at the same place, so the rules stay put as data scrolls past.
	for x := plot.Right() - 1 - gridEvery; x > plot.X; x -= gridEvery {
		for y := plot.Y; y < plot.Bottom(); y++ {
			s.SetContent(x, y, GridDivider, nil, gridStyle)
		}
	}
}
