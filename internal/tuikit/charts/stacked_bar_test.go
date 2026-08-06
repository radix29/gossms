package charts

import (
	"strings"
	"testing"

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
	StackedBar{}.Draw(c, c.Rect())
	StackedBar{Series: []Series{{Label: "z", Color: colA, Values: []float64{0}}}}.Draw(c, c.Rect())

	if got := rowBlocks(c, 0); got != 0 {
		t.Errorf("empty composition drew %d block columns", got)
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
