package charts

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/core"
)

func barFixture() []Bar {
	return []Bar{
		{Label: "Batches", Short: "Btch", Value: 100, Color: colA},
		{Label: "Transactions", Short: "Trans", Value: 50, Color: colB},
		{Label: "Compiles", Short: "Comp", Value: 0, Color: colC},
	}
}

// rowBlocks counts the block glyphs on a canvas row — a bar's drawn length.
func rowBlocks(c *Canvas, y int) int {
	n := 0
	for _, r := range c.Row(y) {
		if isBlock(r) {
			n++
		}
	}
	return n
}

func TestBarChartLengthsTrackValues(t *testing.T) {
	c := NewCanvas(30, 3)
	BarChart{Bars: barFixture(), Scale: Scale{Min: 0, Max: 100}, LabelWidth: -1}.Draw(c, c.Rect())

	full, half := rowBlocks(c, 0), rowBlocks(c, 1)
	if full != 30 {
		t.Errorf("full bar = %d columns, want the whole 30-column width", full)
	}
	if half != 15 {
		t.Errorf("half-value bar = %d columns, want 15", half)
	}
	if zero := rowBlocks(c, 2); zero != 0 {
		t.Errorf("zero bar drew %d columns, want none", zero)
	}
}

func TestBarChartPartialBlockForFractionalLength(t *testing.T) {
	c := NewCanvas(10, 1)
	// 55% of 10 columns = 5.5, so five full cells and a half one.
	BarChart{Bars: []Bar{{Value: 55, Color: colA}}, Scale: Scale{Min: 0, Max: 100}, LabelWidth: -1}.Draw(c, c.Rect())

	row := []rune(c.Row(0))
	if row[4] != '█' {
		t.Errorf("cell 4 = %q, want a full block", row[4])
	}
	if row[5] != '▌' {
		t.Errorf("cell 5 = %q, want a half block for the fractional remainder", row[5])
	}
}

func TestBarChartDrawsLabelsAndValues(t *testing.T) {
	c := NewCanvas(40, 2)
	BarChart{Bars: barFixture()[:2], Scale: Scale{Min: 0, Max: 100}, ShowValues: true}.Draw(c, c.Rect())

	rows := c.Rows()
	if !strings.HasPrefix(rows[0], "Batches") {
		t.Errorf("row 0 = %q, want it to start with the label", rows[0])
	}
	if !strings.Contains(rows[0], "100") {
		t.Errorf("row 0 = %q, want the formatted value", rows[0])
	}
}

// A long label must not crowd out the data: the auto gutter is capped at a
// third of the chart, and the label shortens to fit it.
func TestBarChartCapsAutoLabelGutter(t *testing.T) {
	c := NewCanvas(30, 1)
	BarChart{
		Bars:  []Bar{{Label: "An Extremely Long Metric Name", Short: "Short", Value: 100, Color: colA}},
		Scale: Scale{Min: 0, Max: 100},
	}.Draw(c, c.Rect())

	if blocks := rowBlocks(c, 0); blocks < 20 {
		t.Errorf("bar got %d columns; the label gutter should cap at a third of 30", blocks)
	}
	if !strings.HasPrefix(c.Row(0), "Short") {
		t.Errorf("row = %q, want the short label in the capped gutter", c.Row(0))
	}
}

func TestBarChartDropsBarsPastItsHeight(t *testing.T) {
	c := NewCanvas(20, 2)
	BarChart{Bars: barFixture(), Scale: Scale{Min: 0, Max: 100}, LabelWidth: -1}.Draw(c, c.Rect())

	if rowBlocks(c, 0) == 0 || rowBlocks(c, 1) == 0 {
		t.Error("the two bars that fit should both be drawn")
	}
}

func TestBarChartDegenerateInput(t *testing.T) {
	c := NewCanvas(10, 2)
	BarChart{}.Draw(c, c.Rect())
	BarChart{Bars: barFixture()}.Draw(c, core.Rect{X: 0, Y: 0, W: 0, H: 2})
	BarChart{Bars: barFixture()}.Draw(c, core.Rect{X: 0, Y: 0, W: 10, H: 0})

	for y := range c.Rows() {
		if rowBlocks(c, y) != 0 {
			t.Errorf("row %d drew bars for empty input", y)
		}
	}
}

// A chart too narrow for both labels and bars keeps the labels: a nameless
// bar of unknown scale says nothing at all.
func TestBarChartKeepsLabelsWhenTooNarrowForBars(t *testing.T) {
	c := NewCanvas(6, 1)
	BarChart{
		Bars:       []Bar{{Label: "Batches", Short: "Btch", Value: 100, Color: colA}},
		Scale:      Scale{Min: 0, Max: 100},
		LabelWidth: 6,
		ShowValues: true,
	}.Draw(c, c.Rect())

	if got := strings.TrimSpace(c.Row(0)); got == "" {
		t.Error("narrow chart drew nothing at all")
	}
}

func TestBarChartStackedPartsFillOneBar(t *testing.T) {
	c := NewCanvas(20, 1)
	BarChart{
		Bars: []Bar{{Label: "Locking", Parts: []BarPart{
			{Value: 75, Color: colA},
			{Value: 25, Color: colB},
		}}},
		Scale:      Scale{Min: 0, Max: 100},
		LabelWidth: -1,
	}.Draw(c, c.Rect())

	row := []rune(c.Row(0))
	for x, r := range row {
		if !isBlock(r) {
			t.Fatalf("column %d of a full-scale stacked bar is empty: %q", x, string(row))
		}
	}
	_, first, _ := c.Get(2, 0)
	if fg := first.GetForeground(); fg != colA {
		t.Errorf("left segment colour = %v, want %v", fg, colA)
	}
	_, last, _ := c.Get(18, 0)
	if fg := last.GetForeground(); fg != colB {
		t.Errorf("right segment colour = %v, want %v", fg, colB)
	}
}

// A stacked bar's reported value is the sum of its parts, which is also
// what auto-scaling has to size against.
func TestBarChartStackedTotalDrivesValueAndScale(t *testing.T) {
	c := NewCanvas(24, 2)
	BarChart{
		Bars: []Bar{
			{Label: "a", Parts: []BarPart{{Value: 60, Color: colA}, {Value: 40, Color: colB}}},
			{Label: "b", Value: 50, Color: colC},
		},
		LabelWidth: -1,
		ShowValues: true,
	}.Draw(c, c.Rect())

	if !strings.Contains(c.Row(0), "100") {
		t.Errorf("row 0 = %q, want the summed total", c.Row(0))
	}
	if blocks := rowBlocks(c, 1); blocks == 0 || blocks >= rowBlocks(c, 0) {
		t.Errorf("half-total bar = %d columns against %d; scale should follow the stacked total", blocks, rowBlocks(c, 0))
	}
}
