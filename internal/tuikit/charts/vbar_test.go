package charts

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/core"
)

func TestVBarChartHeightsTrackValues(t *testing.T) {
	c := NewCanvas(20, 9) // plot is 8 rows once the label row is reserved
	VBarChart{
		Bars: []Bar{
			{Label: "Read", Value: 8, Color: colA},
			{Label: "Write", Value: 4, Color: colB},
		},
		Scale:      Scale{Min: 0, Max: 8},
		YLevels:    2,
		ShowLabels: true,
	}.Draw(c, c.Rect())

	bottom := plotBottom(9)
	// The first bar starts right after the axis gutter ("8.0" plus a space).
	tall := column(c, 5, bottom, bottom+1)
	filled := 0
	for _, r := range tall {
		if isBlock(r) {
			filled++
		}
	}
	if filled != 8 {
		t.Errorf("full-scale bar = %d rows, want 8: %q", filled, string(tall))
	}
}

func TestVBarChartDrawsLabelsUnderBars(t *testing.T) {
	c := NewCanvas(24, 6)
	VBarChart{
		Bars: []Bar{
			{Label: "Read", Value: 5, Color: colA},
			{Label: "Write", Value: 3, Color: colB},
		},
		Scale:      Scale{Min: 0, Max: 10},
		YLevels:    2,
		ShowLabels: true,
	}.Draw(c, c.Rect())

	labelRow := c.Row(5)
	if !strings.Contains(labelRow, "Read") || !strings.Contains(labelRow, "Write") {
		t.Errorf("label row = %q, want both bar labels", labelRow)
	}
}

func TestVBarChartFallsBackToLegend(t *testing.T) {
	c := NewCanvas(24, 7)
	VBarChart{
		Bars: []Bar{
			{Label: "Read", Value: 5, Color: colA},
			{Label: "Write", Value: 3, Color: colB},
		},
		Scale:   Scale{Min: 0, Max: 10},
		YLevels: 2,
	}.Draw(c, c.Rect())

	legendRow := c.Row(6)
	if !strings.Contains(legendRow, string(LegendSquare)) {
		t.Errorf("legend row = %q, want legend squares when labels are off", legendRow)
	}
}

func TestVBarChartSmallValueStaysVisible(t *testing.T) {
	c := NewCanvas(16, 7)
	VBarChart{
		Bars:       []Bar{{Label: "tiny", Value: 0.05, Color: colA}},
		Scale:      Scale{Min: 0, Max: 100},
		YLevels:    2,
		ShowLabels: true,
	}.Draw(c, c.Rect())

	bottom := plotBottom(7)
	found := false
	for x := 0; x < 16; x++ {
		if isBlock(column(c, x, bottom, 1)[0]) {
			found = true
		}
	}
	if !found {
		t.Error("a value 0.05% of the scale vanished; it should clamp to a sliver")
	}
}

func TestVBarChartMoreBarsThanColumns(t *testing.T) {
	bars := make([]Bar, 40)
	for i := range bars {
		bars[i] = Bar{Label: "b", Value: float64(i + 1), Color: colA}
	}
	c := NewCanvas(12, 6)
	VBarChart{Bars: bars, YLevels: 2, ShowLabels: true}.Draw(c, c.Rect())
	// Survival plus a drawn plot is the assertion: with one column per slot
	// there is no room for all 40, and the chart must clip rather than
	// write outside its rect.
	if rowBlocks(c, plotBottom(6)) == 0 {
		t.Error("chart drew nothing when bars outnumber columns")
	}
}

func TestVBarChartDegenerateInput(t *testing.T) {
	c := NewCanvas(10, 5)
	VBarChart{}.Draw(c, c.Rect())
	VBarChart{Bars: []Bar{{Label: "a", Value: 1, Color: colA}}}.Draw(c, core.Rect{X: 0, Y: 0, W: 0, H: 5})
	VBarChart{Bars: []Bar{{Label: "a", Value: 1, Color: colA}}}.Draw(c, core.Rect{X: 0, Y: 0, W: 10, H: 0})

	for y := range c.Rows() {
		if rowBlocks(c, y) != 0 {
			t.Errorf("row %d drew bars for empty input", y)
		}
	}
}
