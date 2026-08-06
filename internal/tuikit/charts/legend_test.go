package charts

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/core"
)

func legendFixture() []LegendItem {
	return []LegendItem{
		{Label: "Transactions", Short: "Trans", Color: colA},
		{Label: "Compiles", Short: "Comp", Color: colB},
		{Label: "Recompiles", Short: "Recomp", Color: colC},
	}
}

func TestDrawLegendUsesFullLabelsWhenTheyFit(t *testing.T) {
	c := NewCanvas(48, 1)
	DrawLegend(c, c.Rect(), legendFixture())

	row := c.Row(0)
	for _, want := range []string{"■ Transactions", "■ Compiles", "■ Recompiles"} {
		if !strings.Contains(row, want) {
			t.Errorf("legend row %q is missing %q", row, want)
		}
	}
}

// Short labels come before clipping: "Trans" says more than "Transac".
func TestDrawLegendFallsBackToShortLabels(t *testing.T) {
	c := NewCanvas(30, 1)
	DrawLegend(c, c.Rect(), legendFixture())

	row := c.Row(0)
	if !strings.Contains(row, "■ Trans") || !strings.Contains(row, "■ Comp") {
		t.Errorf("legend row %q, want the short labels", row)
	}
	if strings.Contains(row, "Transactions") {
		t.Errorf("legend row %q kept a full label that doesn't fit", row)
	}
}

func TestDrawLegendWrapsAcrossAvailableRows(t *testing.T) {
	c := NewCanvas(20, 3)
	DrawLegend(c, c.Rect(), legendFixture())

	rows := c.Rows()
	used := 0
	for _, r := range rows {
		if strings.Contains(r, string(LegendSquare)) {
			used++
		}
	}
	if used < 2 {
		t.Errorf("legend used %d rows of 3 available; entries that don't fit one row should wrap:\n%q", used, rows)
	}
	for i, r := range rows {
		if core.DisplayWidth(strings.TrimRight(r, " ")) > 20 {
			t.Errorf("row %d overflows the legend width: %q", i, r)
		}
	}
}

// Squares must survive even when there is no room for any label text — a
// dropped square leaves a plotted colour with nothing naming it.
func TestDrawLegendKeepsSquaresWhenClipping(t *testing.T) {
	c := NewCanvas(8, 1)
	DrawLegend(c, c.Rect(), legendFixture())

	row := c.Row(0)
	if !strings.Contains(row, string(LegendSquare)) {
		t.Errorf("clipped legend %q dropped its squares entirely", row)
	}
}

func TestDrawLegendSquareCarriesSeriesColour(t *testing.T) {
	c := NewCanvas(40, 1)
	DrawLegend(c, c.Rect(), legendFixture())

	str, style, _ := c.Get(0, 0)
	if str != string(LegendSquare) {
		t.Fatalf("first cell = %q, want the legend square", str)
	}
	if fg := style.GetForeground(); fg != colA {
		t.Errorf("first square colour = %v, want the first series (%v)", fg, colA)
	}
}

func TestDrawLegendDegenerateInput(t *testing.T) {
	c := NewCanvas(10, 2)
	DrawLegend(c, c.Rect(), nil)
	DrawLegend(c, core.Rect{X: 0, Y: 0, W: 0, H: 2}, legendFixture())
	DrawLegend(c, core.Rect{X: 0, Y: 0, W: 10, H: 0}, legendFixture())

	for y, row := range c.Rows() {
		if strings.Contains(row, string(LegendSquare)) {
			t.Errorf("row %d drew a legend for empty input: %q", y, row)
		}
	}
}

func TestLegendItemsPreservesOrderAndColour(t *testing.T) {
	items := LegendItems([]Series{
		{Label: "one", Short: "1", Color: colA},
		{Label: "two", Short: "2", Color: colB},
	})
	if len(items) != 2 {
		t.Fatalf("LegendItems returned %d items, want 2", len(items))
	}
	if items[0].Label != "one" || items[0].Color != colA || items[1].Color != colB {
		t.Errorf("LegendItems = %+v, want the series order and colours preserved", items)
	}
}
