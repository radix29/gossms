package charts

import (
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// bareHistory is a chart stripped of chrome so a test reads the plot at
// known coordinates: no legend, no time label, and a fixed scale.
func bareHistory(series []Series, max float64) HistoryChart {
	return HistoryChart{
		Series:     series,
		Scale:      Scale{Min: 0, Max: max},
		YLevels:    2,
		LegendRows: -1,
		GridEvery:  -1,
	}
}

// plotBottom is the canvas row a chart's baseline lands on: layoutPlot
// always reserves the last row for the time label when there is height for
// it, whether or not a label was supplied.
func plotBottom(canvasH int) int {
	if canvasH > 1 {
		return canvasH - 2
	}
	return canvasH - 1
}

func TestHistoryChartAnchorsNewestBucketAtRightEdge(t *testing.T) {
	c := NewCanvas(20, 6)
	h := bareHistory([]Series{{Label: "A", Color: colA, Values: []float64{4, 0, 0, 0, 4}}}, 4)
	h.Draw(c, c.Rect())

	bottom := plotBottom(6)
	right := column(c, 19, bottom, bottom+1)
	if right[0] != '█' {
		t.Errorf("rightmost column = %q, want the newest bucket's full-height bar", string(right))
	}
	// Buckets 1..3 are zero, so the three columns before it carry no data.
	for x := 16; x <= 18; x++ {
		col := column(c, x, bottom, bottom+1)
		if isBlock(col[0]) {
			t.Errorf("column %d = %q, want no bar for a zero bucket", x, string(col))
		}
	}
}

func TestHistoryChartLeavesLeftEndEmptyForPartialBuffer(t *testing.T) {
	c := NewCanvas(20, 6)
	h := bareHistory([]Series{{Label: "A", Color: colA, Values: []float64{4, 4}}}, 4)
	h.Draw(c, c.Rect())

	bottom := plotBottom(6)
	if col := column(c, 19, bottom, bottom+1); !isBlock(col[0]) {
		t.Errorf("newest bucket missing at the right edge: %q", string(col))
	}
	if col := column(c, 18, bottom, bottom+1); !isBlock(col[0]) {
		t.Errorf("second bucket missing: %q", string(col))
	}
	// Only two buckets exist, so everything further left predates the data.
	for x := 5; x <= 17; x++ {
		if col := column(c, x, bottom, bottom+1); isBlock(col[0]) {
			t.Fatalf("column %d drew data the series doesn't have: %q", x, string(col))
		}
	}
}

// A shorter series must stay visible in front of a taller one; drawing in
// slice order would bury it.
func TestHistoryChartDrawsShorterSeriesInFront(t *testing.T) {
	c := NewCanvas(12, 5)
	h := bareHistory([]Series{
		{Label: "tall", Color: colA, Values: []float64{4}},
		{Label: "short", Color: colB, Values: []float64{1}},
	}, 4)
	h.Draw(c, c.Rect())

	bottom := plotBottom(5)
	_, style, _ := c.Get(11, bottom) // baseline of the newest column
	if fg := style.GetForeground(); fg != colB {
		t.Errorf("bottom cell foreground = %v, want the shorter series (%v)", fg, colB)
	}
	_, topStyle, _ := c.Get(11, 0) // top of the taller series
	if fg := topStyle.GetForeground(); fg != colA {
		t.Errorf("top cell foreground = %v, want the taller series (%v)", fg, colA)
	}
}

func TestHistoryChartPartialBlockForSmallValue(t *testing.T) {
	c := NewCanvas(12, 5)
	h := bareHistory([]Series{{Label: "A", Color: colA, Values: []float64{0.1}}}, 4)
	h.Draw(c, c.Rect())

	bottom := plotBottom(5)
	col := column(c, 11, bottom, bottom+1)
	if !isBlock(col[0]) {
		t.Fatalf("small value vanished: %q", string(col))
	}
	if col[0] == '█' {
		t.Errorf("small value rendered as a full cell (%q); a partial block is what keeps it honest", col[0])
	}
}

func TestHistoryChartDrawsAxisLabelsAndLegend(t *testing.T) {
	c := NewCanvas(24, 8)
	HistoryChart{
		Series:    []Series{{Label: "Batches", Short: "Btch", Color: colA, Values: []float64{5}}},
		Scale:     Scale{Min: 0, Max: 10},
		YLevels:   2,
		TimeLabel: "11:56:23",
	}.Draw(c, c.Rect())

	rows := c.Rows()
	if !contains(rows[0], "10") {
		t.Errorf("top axis row = %q, want the scale maximum", rows[0])
	}
	if !contains(rows[len(rows)-1], "Batches") {
		t.Errorf("legend row = %q, want it to name the series", rows[len(rows)-1])
	}
	if !contains(rows[len(rows)-2], "11:56:23") {
		t.Errorf("time row = %q, want the sample time", rows[len(rows)-2])
	}
}

func TestHistoryChartEmptyRectDrawsNothing(t *testing.T) {
	series := []Series{{Label: "A", Color: colA, Values: []float64{1, 2}}}
	for _, r := range []core.Rect{
		{X: 0, Y: 0, W: 0, H: 4},
		{X: 0, Y: 0, W: 10, H: 0},
		{X: 0, Y: 0, W: -2, H: -2},
	} {
		c := NewCanvas(10, 4)
		bareHistory(series, 2).Draw(c, r)
		for y, row := range c.Rows() {
			if row != "          " {
				t.Errorf("rect %+v wrote row %d: %q", r, y, row)
			}
		}
	}
}

// A rect with room for data but none for chrome still plots — losing the
// labels is the intended degradation, losing the data is not.
func TestHistoryChartTinyRectStillPlots(t *testing.T) {
	series := []Series{{Label: "A", Color: colA, Values: []float64{2}}}
	for _, r := range []core.Rect{
		{X: 0, Y: 0, W: 1, H: 1},
		{X: 0, Y: 0, W: 3, H: 2},
		{X: 6, Y: 2, W: 4, H: 2},
	} {
		c := NewCanvas(10, 4)
		bareHistory(series, 2).Draw(c, r)
		drew := false
		for _, row := range c.Rows() {
			for _, ch := range row {
				if isBlock(ch) {
					drew = true
				}
			}
		}
		if !drew {
			t.Errorf("rect %+v drew no data:\n%q", r, c.Rows())
		}
	}
}

func TestHistoryChartEmptySeries(t *testing.T) {
	c := NewCanvas(16, 5)
	HistoryChart{Series: nil, YLevels: 2, LegendRows: -1}.Draw(c, c.Rect())

	for y, row := range c.Rows() {
		for _, r := range row {
			if isBlock(r) {
				t.Fatalf("row %d drew data for an empty chart: %q", y, row)
			}
		}
	}
	if got := c.Row(0); !contains(got, "1.0") {
		t.Errorf("empty chart axis = %q, want the 0..1 fallback scale", got)
	}
}

func TestHistoryChartAllZeroSeries(t *testing.T) {
	c := NewCanvas(16, 5)
	bareHistory([]Series{{Label: "A", Color: colA, Values: []float64{0, 0, 0}}}, 0).Draw(c, c.Rect())

	for x := 0; x < 16; x++ {
		if col := column(c, x, 4, 5); isBlock(col[0]) {
			t.Fatalf("column %d drew a bar for an all-zero series", x)
		}
	}
}

// With an interval the time row becomes a scale: the newest sample's time
// sits under the newest column, and every divider to its left is labelled
// with how far back it is.
func TestHistoryChartDrawsATimeScale(t *testing.T) {
	c := NewCanvas(60, 10)
	HistoryChart{
		Series:     []Series{{Label: "A", Color: colA, Values: []float64{5}}},
		Scale:      Scale{Min: 0, Max: 10},
		YLevels:    2,
		TimeLabel:  "11:56:23",
		Interval:   5 * time.Second,
		GridEvery:  10,
		LegendRows: -1,
	}.Draw(c, c.Rect())

	row := c.Rows()[9]
	// Dividers every 10 columns at 5 seconds a column.
	for _, want := range []string{"11:56:23", "-0:50", "-1:40"} {
		if !contains(row, want) {
			t.Errorf("time row = %q, want it to carry %q", row, want)
		}
	}
	if !strings.HasSuffix(strings.TrimRight(row, " "), "11:56:23") {
		t.Errorf("time row = %q, want the sample time at the right edge", row)
	}
}

// Without an interval the row keeps its old meaning — a centred caption —
// so a chart whose caller has no sample spacing to report doesn't grow a
// scale that would be wrong.
func TestHistoryChartWithoutIntervalCentresTheTimeLabel(t *testing.T) {
	c := NewCanvas(40, 8)
	HistoryChart{
		Series:     []Series{{Label: "A", Color: colA, Values: []float64{5}}},
		Scale:      Scale{Min: 0, Max: 10},
		YLevels:    2,
		TimeLabel:  "11:56:23",
		LegendRows: -1,
	}.Draw(c, c.Rect())

	row := c.Rows()[7]
	if contains(row, "-") {
		t.Errorf("time row = %q, want no age labels without an interval", row)
	}
	if strings.HasSuffix(row, "11:56:23") {
		t.Errorf("time row = %q, want the label centred rather than right-aligned", row)
	}
}

// A label that would collide with its neighbour is dropped whole: half of
// "-100s" reads as a different number.
func TestTimeScaleDropsCollidingLabels(t *testing.T) {
	c := NewCanvas(30, 8)
	HistoryChart{
		Series:     []Series{{Label: "A", Color: colA, Values: []float64{5}}},
		Scale:      Scale{Min: 0, Max: 10},
		YLevels:    2,
		TimeLabel:  "11:56:23",
		Interval:   time.Minute,
		GridEvery:  2,
		LegendRows: -1,
	}.Draw(c, c.Rect())

	row := c.Rows()[7]
	for _, partial := range []string{"-2:00-", "0:00-4"} {
		if contains(row, partial) {
			t.Errorf("time row = %q, want no labels packed against each other", row)
		}
	}
}

// One axis must not mix units: the span of the whole chart picks the unit,
// and every label on it uses that one.
func TestFormatAgeUsesOneUnitPerAxis(t *testing.T) {
	short := 45 * time.Second
	for d, want := range map[time.Duration]string{
		5 * time.Second:  "-5s",
		45 * time.Second: "-45s",
	} {
		if got := formatAge(d, short); got != want {
			t.Errorf("formatAge(%v, %v) = %q, want %q", d, short, got, want)
		}
	}

	long := 20 * time.Minute
	for d, want := range map[time.Duration]string{
		50 * time.Second:  "-0:50",
		100 * time.Second: "-1:40",
		150 * time.Second: "-2:30",
		20 * time.Minute:  "-20:00",
	} {
		if got := formatAge(d, long); got != want {
			t.Errorf("formatAge(%v, %v) = %q, want %q", d, long, got, want)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
