package charts

import "testing"

func bareStacked(series []Series, max float64) StackedHistoryChart {
	return StackedHistoryChart{
		Series:     series,
		Scale:      Scale{Min: 0, Max: max},
		YLevels:    2,
		LegendRows: -1,
		GridEvery:  -1,
	}
}

// The defect this chart type exists to avoid: a column with several series
// must be one continuous run of blocks, with the segment boundaries drawn
// inside cells rather than as gaps between them.
func TestStackedHistoryColumnsHaveNoInternalGaps(t *testing.T) {
	c := NewCanvas(14, 9)
	bareStacked([]Series{
		{Label: "a", Color: colA, Values: []float64{1.3}},
		{Label: "b", Color: colB, Values: []float64{0.4}},
		{Label: "c", Color: colC, Values: []float64{2.1}},
	}, 8).Draw(c, c.Rect())

	bottom := plotBottom(9)
	assertNoHoles(t, column(c, 13, bottom, bottom+1), "stacked column")
}

func TestStackedHistoryStackHeightMatchesTotal(t *testing.T) {
	c := NewCanvas(14, 9)
	// Total 4 over a scale of 8 on an 8-row plot: exactly four filled rows.
	bareStacked([]Series{
		{Label: "a", Color: colA, Values: []float64{2}},
		{Label: "b", Color: colB, Values: []float64{2}},
	}, 8).Draw(c, c.Rect())

	bottom := plotBottom(9)
	col := column(c, 13, bottom, bottom+1)
	filled := 0
	for _, r := range col {
		if isBlock(r) {
			filled++
		}
	}
	if filled != 4 {
		t.Errorf("stack height = %d rows, want 4 — %q", filled, string(col))
	}
}

func TestStackedHistorySegmentColoursStackInOrder(t *testing.T) {
	c := NewCanvas(14, 9)
	bareStacked([]Series{
		{Label: "a", Color: colA, Values: []float64{2}},
		{Label: "b", Color: colB, Values: []float64{2}},
	}, 8).Draw(c, c.Rect())

	bottom := plotBottom(9)
	_, baseStyle, _ := c.Get(13, bottom)
	if fg := baseStyle.GetForeground(); fg != colA {
		t.Errorf("baseline cell = %v, want the first series (%v)", fg, colA)
	}
	_, topStyle, _ := c.Get(13, bottom-3)
	if fg := topStyle.GetForeground(); fg != colB {
		t.Errorf("top cell = %v, want the second series (%v)", fg, colB)
	}
}

// A series whose values are all zero contributes nothing and must not
// displace the series above it in the stack.
func TestStackedHistorySkipsZeroSeries(t *testing.T) {
	c := NewCanvas(14, 9)
	bareStacked([]Series{
		{Label: "zero", Color: colA, Values: []float64{0}},
		{Label: "real", Color: colB, Values: []float64{2}},
	}, 8).Draw(c, c.Rect())

	bottom := plotBottom(9)
	_, style, _ := c.Get(13, bottom)
	if fg := style.GetForeground(); fg != colB {
		t.Errorf("baseline cell = %v, want the non-zero series (%v)", fg, colB)
	}
}

func TestStackedHistoryAutoScalesToLargestTotal(t *testing.T) {
	c := NewCanvas(16, 8)
	StackedHistoryChart{
		Series: []Series{
			{Label: "a", Color: colA, Values: []float64{3000, 1000}},
			{Label: "b", Color: colB, Values: []float64{2000, 1000}},
		},
		YLevels:    2,
		LegendRows: -1,
	}.Draw(c, c.Rect())

	// Largest bucket total is 5000, which rounds up to 5K.
	if got := c.Row(0); !contains(got, "5.0K") {
		t.Errorf("top axis row = %q, want the 5K auto scale", got)
	}
}

func TestStackedHistoryEmptyAndZeroInput(t *testing.T) {
	c := NewCanvas(12, 6)
	bareStacked(nil, 0).Draw(c, c.Rect())
	bareStacked([]Series{{Label: "a", Color: colA, Values: []float64{0, 0}}}, 0).Draw(c, c.Rect())

	for y, row := range c.Rows() {
		for _, r := range row {
			if isBlock(r) {
				t.Fatalf("row %d drew a stack with no data: %q", y, row)
			}
		}
	}
}
