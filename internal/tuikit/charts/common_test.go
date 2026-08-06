package charts

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// Test colours, distinct from every palette colour so a cell's origin is
// unambiguous.
var (
	colA = tcell.NewRGBColor(10, 10, 10)
	colB = tcell.NewRGBColor(20, 20, 20)
	colC = tcell.NewRGBColor(30, 30, 30)
)

func plotBg() tcell.Color { return theme.Active().ChartPlotBg }

// column reads canvas column x bottom-up, returning one rune per row.
func column(c *Canvas, x, bottomY, height int) []rune {
	out := make([]rune, 0, height)
	for i := 0; i < height; i++ {
		str, _, _ := c.Get(x, bottomY-i)
		runes := []rune(str)
		if len(runes) == 0 {
			out = append(out, ' ')
			continue
		}
		out = append(out, runes[0])
	}
	return out
}

// isBlock reports whether r is any part of a filled bar or stack.
func isBlock(r rune) bool {
	for _, b := range vBlocks[1:] {
		if r == b {
			return true
		}
	}
	for _, b := range hBlocks[1:] {
		if r == b {
			return true
		}
	}
	return false
}

// assertNoHoles fails when a run of block glyphs has a gap in it — the
// defect stacked composition exists to prevent.
func assertNoHoles(t *testing.T, cells []rune, what string) {
	t.Helper()
	seenGap := false
	for i, r := range cells {
		switch {
		case isBlock(r) && seenGap:
			t.Fatalf("%s: block at index %d after a gap — stack is not continuous: %q", what, i, string(cells))
		case !isBlock(r):
			seenGap = true
		}
	}
}

func TestComposeStackSingleSegment(t *testing.T) {
	bg := plotBg()
	cells := composeStack(4, []segment{{color: colA, cells: 2.5}}, bg)

	if cells[0].split != 8 || cells[0].lower != colA {
		t.Errorf("cell 0 = %+v, want a full cell of colA", cells[0])
	}
	if cells[1].split != 8 {
		t.Errorf("cell 1 = %+v, want a full cell", cells[1])
	}
	if cells[2].split != 4 || cells[2].lower != colA || cells[2].upper != bg {
		t.Errorf("cell 2 = %+v, want a half cell of colA over the background", cells[2])
	}
	if cells[3].filled() {
		t.Errorf("cell 3 = %+v, want empty", cells[3])
	}
}

func TestComposeStackBoundaryInsideOneCellKeepsBothColours(t *testing.T) {
	cells := composeStack(2, []segment{
		{color: colA, cells: 0.5},
		{color: colB, cells: 0.5},
	}, plotBg())

	if cells[0].split != 4 {
		t.Fatalf("cell 0 split = %d, want 4", cells[0].split)
	}
	if cells[0].lower != colA || cells[0].upper != colB {
		t.Errorf("cell 0 colours = %v/%v, want colA over colB", cells[0].lower, cells[0].upper)
	}
	ch, fg, bg := cells[0].vertical()
	if ch != '▄' || fg != colA || bg != colB {
		t.Errorf("vertical() = %q %v/%v, want '▄' colA/colB", ch, fg, bg)
	}
}

func TestComposeStackHasNoInternalHoles(t *testing.T) {
	// Deliberately awkward sizes: none of these land on a cell boundary.
	cells := composeStack(6, []segment{
		{color: colA, cells: 1.3},
		{color: colB, cells: 0.4},
		{color: colC, cells: 2.1},
	}, plotBg())

	runes := make([]rune, len(cells))
	for i, c := range cells {
		runes[i], _, _ = c.vertical()
	}
	assertNoHoles(t, runes, "composeStack")

	filled := 0
	for _, c := range cells {
		if c.filled() {
			filled++
		}
	}
	// 1.3 + 0.4 + 2.1 = 3.8 cells, so four cells carry data.
	if filled != 4 {
		t.Errorf("filled cells = %d, want 4 (3.8 cells of stack)", filled)
	}
}

// Three boundaries inside one cell can't all be drawn; the cell keeps the
// lowest two and stays filled rather than leaving a gap.
func TestComposeStackCrowdedCellStaysFilled(t *testing.T) {
	cells := composeStack(1, []segment{
		{color: colA, cells: 0.125},
		{color: colB, cells: 0.125},
		{color: colC, cells: 0.125},
	}, plotBg())

	if !cells[0].filled() {
		t.Fatal("crowded cell is empty; a hole is worse than a dropped segment")
	}
	if cells[0].lower != colA || cells[0].upper != colB {
		t.Errorf("crowded cell = %v/%v, want the lowest two segments", cells[0].lower, cells[0].upper)
	}
}

func TestComposeStackEmptyInputs(t *testing.T) {
	if got := composeStack(0, []segment{{color: colA, cells: 3}}, plotBg()); len(got) != 0 {
		t.Errorf("zero-length stack returned %d cells", len(got))
	}
	cells := composeStack(3, nil, plotBg())
	for i, c := range cells {
		if c.filled() {
			t.Errorf("cell %d of an empty stack is filled: %+v", i, c)
		}
	}
}

// A segment taller than the run is clipped to it rather than writing past
// the end of the slice.
func TestComposeStackClipsOverflow(t *testing.T) {
	cells := composeStack(2, []segment{{color: colA, cells: 99}}, plotBg())
	for i, c := range cells {
		if c.split != 8 || c.lower != colA {
			t.Errorf("cell %d = %+v, want a full cell of colA", i, c)
		}
	}
}

func TestLayoutPlotDropsChromeWhenCramped(t *testing.T) {
	full := layoutPlot(core.Rect{X: 0, Y: 0, W: 20, H: 10}, 5, 1)
	if full.legend.H != 1 || full.timeRow.H != 1 {
		t.Fatalf("roomy layout dropped chrome: %+v", full)
	}
	if full.plot.W != 15 || full.plot.X != 5 {
		t.Errorf("plot = %+v, want X=5 W=15 after a 5-column gutter", full.plot)
	}

	tight := layoutPlot(core.Rect{X: 0, Y: 0, W: 20, H: 1}, 5, 1)
	if tight.legend.H != 0 || tight.timeRow.H != 0 {
		t.Errorf("one-row layout kept chrome: %+v", tight)
	}
	if tight.plot.H != 1 {
		t.Errorf("one-row layout plot height = %d, want 1", tight.plot.H)
	}

	// A gutter wider than the whole rect is dropped, not applied — a chart
	// with no room for labels still shows its data.
	narrow := layoutPlot(core.Rect{X: 0, Y: 0, W: 4, H: 6}, 9, 1)
	if narrow.plot.W != 4 || narrow.plot.X != 0 {
		t.Errorf("narrow plot = %+v, want the full width", narrow.plot)
	}
}
