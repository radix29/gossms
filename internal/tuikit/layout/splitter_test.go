package layout

import (
	"math"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/tuikittest"
)

// TestSplitterDragOnlyStartsFromPressOnBar pins down that a resize begins
// only on a press that lands on the bar. A selection drag in the pane above
// that crosses the bar on its way down would otherwise grab it and resize
// the panes mid-selection — see the press field.
func TestSplitterDragOnlyStartsFromPressOnBar(t *testing.T) {
	sp := NewSplitter(SplitterHorizontal, 0.5)
	sp.SetBounds(0, 0, 80, 24)
	before := sp.Ratio()
	bar := sp.SplitPos()

	// Press well above the bar, then drag down across and past it.
	if sp.HandleMouse(tuikittest.Press(10, 2)) {
		t.Fatalf("press above the bar consumed by the splitter")
	}
	for _, y := range []int{bar - 2, bar, bar + 1, bar + 4} {
		if sp.HandleMouse(tuikittest.Press(10, y)) {
			t.Fatalf("motion at y=%d consumed by the splitter mid-gesture", y)
		}
	}
	sp.HandleMouse(tuikittest.Release(10, bar+4))

	if got := sp.Ratio(); got != before {
		t.Errorf("ratio = %v after a drag crossing the bar, want %v unchanged", got, before)
	}
}

// TestSplitterDragFromBarStillResizes is the other half: a gesture that
// really does begin on the bar must still resize, and must keep receiving
// events after the pointer leaves the bar's own row.
func TestSplitterDragFromBarStillResizes(t *testing.T) {
	sp := NewSplitter(SplitterHorizontal, 0.5)
	sp.SetBounds(0, 0, 80, 24)
	before := sp.Ratio()
	bar := sp.SplitPos()

	if !sp.HandleMouse(tuikittest.Press(10, bar)) {
		t.Fatalf("press on the bar not consumed")
	}
	if !sp.HandleMouse(tuikittest.Press(10, bar+3)) {
		t.Fatalf("motion off the bar not consumed while dragging")
	}
	if got := sp.Ratio(); got <= before {
		t.Fatalf("ratio = %v after dragging down, want greater than %v", got, before)
	}
	sp.HandleMouse(tuikittest.Release(10, bar+3))

	// The release cleared the latch, so the next press on the bar is a
	// fresh gesture rather than a continuation of the last one.
	moved := sp.Ratio()
	if !sp.HandleMouse(tuikittest.Press(10, sp.SplitPos())) {
		t.Fatalf("press on the bar after a completed drag not consumed")
	}
	sp.HandleMouse(tuikittest.Press(10, sp.SplitPos()-3))
	if got := sp.Ratio(); got >= moved {
		t.Errorf("ratio = %v after a second drag upward, want less than %v", got, moved)
	}
}

// A corrupt saved ratio must not reach the layout: core.Clamp passes NaN
// through, and a NaN ratio puts the bar anywhere.
func TestSplitterSetRatioIgnoresNaNAndInf(t *testing.T) {
	sp := NewSplitter(SplitterHorizontal, 0.5)
	sp.SetBounds(0, 0, 80, 24)
	before := sp.Ratio()
	for _, r := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		sp.SetRatio(r)
		if got := sp.Ratio(); got != before {
			t.Errorf("SetRatio(%v) left ratio %v, want %v unchanged", r, got, before)
		}
	}
}

// A resize to zero mid-drag must not divide by zero into a NaN ratio.
func TestSplitterDragSurvivesZeroSize(t *testing.T) {
	for _, sp := range []*Splitter{NewSplitter(SplitterHorizontal, 0.5), NewSplitter(SplitterVertical, 0.3)} {
		sp.SetBounds(0, 0, 80, 24)
		before := sp.Ratio()
		pos := sp.SplitPos()
		x, y := pos, 5
		if sp.dir == SplitterHorizontal {
			x, y = 10, pos
		}
		if !sp.HandleMouse(tuikittest.Press(x, y)) {
			t.Fatalf("dir %v: press on the bar not consumed", sp.dir)
		}
		sp.SetBounds(0, 0, 0, 0)
		sp.HandleMouse(tuikittest.Press(x, y)) // 0/0 before the fix
		if got := sp.Ratio(); math.IsNaN(got) || got != before {
			t.Errorf("dir %v: ratio = %v after a drag at zero size, want %v", sp.dir, got, before)
		}
		sp.HandleMouse(tuikittest.Release(x, y))
	}
}

// TestSplitterLimits pins C11 of the 2026-11-12 review: the limits are the
// host's, SetLimits re-clamps the ratio, and nonsense limits are ignored.
func TestSplitterLimits(t *testing.T) {
	sp := NewSplitter(SplitterVertical, 0.95)
	if got := sp.Ratio(); got != defaultMaxRatio {
		t.Fatalf("ratio = %v, want the default max %v", got, defaultMaxRatio)
	}
	sp.SetLimits(0.2, 0.6)
	if got := sp.Ratio(); got != 0.6 {
		t.Fatalf("ratio after SetLimits(0.2, 0.6) = %v, want 0.6", got)
	}
	sp.SetRatio(0.1)
	if got := sp.Ratio(); got != 0.2 {
		t.Fatalf("SetRatio(0.1) = %v, want the new min 0.2", got)
	}
	for _, lim := range [][2]float64{{0.7, 0.3}, {-0.1, 0.5}, {0.5, 1.1}, {math.NaN(), 0.5}} {
		sp.SetLimits(lim[0], lim[1])
		if sp.minRatio != 0.2 || sp.maxRatio != 0.6 {
			t.Errorf("SetLimits(%v, %v) changed the limits to %v–%v", lim[0], lim[1], sp.minRatio, sp.maxRatio)
		}
	}
}
