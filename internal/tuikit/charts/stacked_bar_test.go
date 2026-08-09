package charts

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

func memoryFixture() []Series {
	return []Series{
		{Label: "Buffer", Color: colA, Values: []float64{50}},
		{Label: "Stolen", Color: colB, Values: []float64{30}},
		{Label: "Free", Color: colC, Values: []float64{20}},
	}
}

func TestStackedBarFillsWidthWithoutGaps(t *testing.T) {
	c := NewCanvas(20, 2)
	StackedBar{Series: memoryFixture(), LegendRows: -1}.Draw(c, c.Rect())

	row := []rune(c.Row(0))
	for x, r := range row {
		if !isBlock(r) {
			t.Fatalf("column %d of the composition bar is empty: %q", x, string(row))
		}
	}
}

func TestStackedBarSegmentsAppearInOrder(t *testing.T) {
	c := NewCanvas(20, 1)
	StackedBar{Series: memoryFixture(), LegendRows: -1}.Draw(c, c.Rect())

	// 50/30/20 of 100 across 20 columns: colA to column 9, colB to 15.
	checks := []struct {
		x    int
		want string
	}{{x: 0, want: "A"}, {x: 12, want: "B"}, {x: 18, want: "C"}}
	colors := map[string]any{"A": colA, "B": colB, "C": colC}
	for _, ch := range checks {
		_, style, _ := c.Get(ch.x, 0)
		if style.GetForeground() != colors[ch.want] {
			t.Errorf("column %d colour = %v, want segment %s", ch.x, style.GetForeground(), ch.want)
		}
	}
}

func TestStackedBarDrawsLegendAndTotal(t *testing.T) {
	c := NewCanvas(30, 2)
	StackedBar{Series: memoryFixture(), ShowTotal: true}.Draw(c, c.Rect())

	if !strings.Contains(c.Row(0), "100") {
		t.Errorf("row 0 = %q, want the total after the bar", c.Row(0))
	}
	if !strings.Contains(c.Row(1), "Buffer") {
		t.Errorf("row 1 = %q, want the legend", c.Row(1))
	}
}

func TestStackedBarHonoursExplicitScale(t *testing.T) {
	c := NewCanvas(20, 1)
	// Total 100 against a fixed maximum of 200: the bar fills half.
	StackedBar{Series: memoryFixture(), Scale: Scale{Min: 0, Max: 200}, LegendRows: -1}.Draw(c, c.Rect())

	if got := rowBlocks(c, 0); got != 10 {
		t.Errorf("bar = %d columns, want 10 of 20 against a doubled scale: %q", got, c.Row(0))
	}
}

func TestStackedBarEmptyInput(t *testing.T) {
	c := NewCanvas(12, 2)
	empty := StackedBar{}.Draw(c, c.Rect())
	zeroes := StackedBar{Series: []Series{{Label: "z", Color: colA, Values: []float64{0}}}}.Draw(c, c.Rect())

	if got := rowBlocks(c, 0); got != 0 {
		t.Errorf("empty composition drew %d block columns", got)
	}
	// A bar with no segments has nothing for a click to report, so it must
	// hand back the zero Rect rather than a plot rectangle over blank
	// background — the caller hit-tests against whatever it is given.
	if empty != (core.Rect{}) {
		t.Errorf("no series returned %+v, want the zero Rect", empty)
	}
	if zeroes != (core.Rect{}) {
		t.Errorf("all-zero series returned %+v, want the zero Rect", zeroes)
	}
}

// Draw's return value is the caller's hit-test target: internal/tui records
// it as a ChartHit and resolves clicks against it. It has to be the bar
// itself and nothing else, because the total and the legend are drawn inside
// the same r but are not part of what a click reports on.
func TestStackedBarDrawReturnsTheBarRectOnly(t *testing.T) {
	c := NewCanvas(30, 4)
	got := StackedBar{Series: memoryFixture(), Rows: 2, LegendRows: 1, ShowTotal: true}.Draw(c, c.Rect())

	if got.Y != 0 || got.H != 2 {
		t.Errorf("rect covers rows %d..%d, want the 2 bar rows only", got.Y, got.Bottom())
	}
	if got.X != 0 || got.W >= 30 {
		t.Errorf("rect spans columns %d..%d, want the total's column excluded from 0..30", got.X, got.Right())
	}
	// Everything inside the rect is bar.
	for y := got.Y; y < got.Bottom(); y++ {
		row := []rune(c.Row(y))
		for x := got.X; x < got.Right(); x++ {
			if !isBlock(row[x]) {
				t.Fatalf("(%d,%d) is inside the returned rect but is not bar: %q", x, y, c.Row(y))
			}
		}
	}
	// The total is to the right of it, and the legend below it.
	if tail := c.Row(0)[got.Right():]; !strings.Contains(tail, "100") {
		t.Errorf("columns after the rect = %q, want the total outside it", tail)
	}
	if legend := c.Row(got.Bottom()); !strings.Contains(legend, "Buffer") {
		t.Errorf("row below the rect = %q, want the legend outside it", legend)
	}
}

// The rect must be stated in the coordinates it was drawn at, not the
// canvas's — a composition bar is drawn into a panel well inside the
// dashboard, and a rect rooted at 0,0 would resolve every click to the
// wrong chart.
func TestStackedBarDrawReturnsRectAtDrawOrigin(t *testing.T) {
	c := NewCanvas(30, 6)
	at := core.Rect{X: 4, Y: 2, W: 20, H: 3}
	got := StackedBar{Series: memoryFixture(), LegendRows: 1}.Draw(c, at)

	if got.X != at.X || got.Y != at.Y {
		t.Errorf("rect origin = (%d,%d), want the draw origin (%d,%d)", got.X, got.Y, at.X, at.Y)
	}
	if got.W != at.W || got.Bottom() > at.Bottom() {
		t.Errorf("rect = %+v, want it within %+v at full width", got, at)
	}
}

// A rect too short for both the bar and its legend keeps the bar and drops
// the legend rows that no longer fit. The bar is what floors at one row, so
// the legend's share has to be re-derived from what is left — otherwise it
// is placed below r, and DrawLegend fills whatever rect it is handed, so it
// paints over whichever panel is drawn beneath this one.
func TestStackedBarLegendNeverDrawsBelowItsRect(t *testing.T) {
	for _, h := range []int{1, 2, 3} {
		c := NewCanvas(20, 6)
		// Row h onward is sentinel text belonging to the panel below.
		for y := h; y < 6; y++ {
			core.DrawText(c, 0, y, tcell.StyleDefault, "SENTINEL")
		}
		StackedBar{Series: memoryFixture(), Rows: 2, LegendRows: 2}.
			Draw(c, core.Rect{X: 0, Y: 0, W: 20, H: h})

		for y := h; y < 6; y++ {
			if got := c.Row(y); !strings.HasPrefix(got, "SENTINEL") {
				t.Errorf("H=%d: row %d below the rect = %q, want it untouched", h, y, got)
			}
		}
	}
}

// A rect too narrow to hold both the total and any bar draws no bar, so it
// must report none either.
func TestStackedBarDrawReturnsZeroRectWhenNoRoom(t *testing.T) {
	c := NewCanvas(12, 2)
	for _, at := range []core.Rect{
		{X: 0, Y: 0, W: 0, H: 2},  // no width
		{X: 0, Y: 0, W: 12, H: 0}, // no height
		{X: 0, Y: 0, W: 2, H: 2},  // the total alone is wider than this
	} {
		if got := (StackedBar{Series: memoryFixture(), ShowTotal: true}).Draw(c, at); got != (core.Rect{}) {
			t.Errorf("Draw into %+v returned %+v, want the zero Rect", at, got)
		}
	}
}

func TestStackedBarDegenerateRects(t *testing.T) {
	c := NewCanvas(12, 2)
	StackedBar{Series: memoryFixture()}.Draw(c, core.Rect{X: 0, Y: 0, W: 0, H: 2})
	StackedBar{Series: memoryFixture()}.Draw(c, core.Rect{X: 0, Y: 0, W: 12, H: 0})

	if got := rowBlocks(c, 0); got != 0 {
		t.Errorf("degenerate rect drew %d block columns", got)
	}
}
