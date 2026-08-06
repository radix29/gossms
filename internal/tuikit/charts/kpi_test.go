package charts

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

func TestKPIRightAlignsValue(t *testing.T) {
	c := NewCanvas(30, 1)
	KPI{Label: "User Connections", Value: "1519"}.Draw(c, c.Rect())

	row := c.Row(0)
	if !strings.HasSuffix(row, " 1519 ") {
		t.Errorf("row = %q, want the value box flush against the right edge", row)
	}
	if !strings.Contains(row, "User Connections") {
		t.Errorf("row = %q, want the label", row)
	}
}

func TestKPIValueCarriesItsOwnBackground(t *testing.T) {
	bg := tcell.NewRGBColor(1, 2, 3)
	fg := tcell.NewRGBColor(4, 5, 6)
	c := NewCanvas(20, 1)
	KPI{Label: "PLE", Value: "5761", ValueBg: bg, ValueFg: fg}.Draw(c, c.Rect())

	_, style, _ := c.Get(19, 0)
	if style.GetBackground() != bg {
		t.Errorf("value background = %v, want %v", style.GetBackground(), bg)
	}
	_, valStyle, _ := c.Get(16, 0) // inside "5761"
	if valStyle.GetForeground() != fg {
		t.Errorf("value foreground = %v, want %v", valStyle.GetForeground(), fg)
	}
}

// Too narrow to show both, the number survives and the label goes: the
// section header already says what the number is.
func TestKPIDropsLabelBeforeValue(t *testing.T) {
	c := NewCanvas(7, 1)
	KPI{Label: "Blocked Processes", Value: "92"}.Draw(c, c.Rect())

	row := c.Row(0)
	if !strings.Contains(row, "92") {
		t.Errorf("row = %q, want the value kept", row)
	}
	if strings.Contains(row, "Blocked") {
		t.Errorf("row = %q, want the label dropped at this width", row)
	}
}

func TestKPIWidth(t *testing.T) {
	k := KPI{Label: "PLE", Value: "5761"}
	if got, want := k.Width(), 10; got != want {
		t.Errorf("Width() = %d, want %d (3 label + 4 value + 3 padding)", got, want)
	}
}

func TestKPITruncatesAnOversizedValue(t *testing.T) {
	c := NewCanvas(4, 1)
	KPI{Label: "x", Value: "1234567890"}.Draw(c, c.Rect())

	if got := c.Row(0); core.DisplayWidth(got) != 4 {
		t.Errorf("row = %q, want it clipped to the 4-column rect", got)
	}
}

func TestKPIDegenerateRects(t *testing.T) {
	c := NewCanvas(10, 1)
	KPI{Label: "a", Value: "1"}.Draw(c, core.Rect{X: 0, Y: 0, W: 0, H: 1})
	KPI{Label: "a", Value: "1"}.Draw(c, core.Rect{X: 0, Y: 0, W: 10, H: 0})

	if got := strings.TrimSpace(c.Row(0)); got != "" {
		t.Errorf("row = %q, want nothing drawn into a degenerate rect", got)
	}
}
