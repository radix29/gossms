package layout

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// press/motion/release build the three mouse events one drag gesture is
// made of. motion is the resend tcell's all-motion tracking sends on every
// pointer move while Button1 stays down — indistinguishable from press
// except for what came before it, which is the whole point.
func press(x, y int) *tcell.EventMouse {
	return tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone)
}
func motion(x, y int) *tcell.EventMouse {
	return tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone)
}
func release(x, y int) *tcell.EventMouse {
	return tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone)
}

// TestSplitterDragOnlyStartsFromPressOnBar pins down that a resize begins
// only on a press that lands on the bar. A selection drag in the pane above
// that crosses the bar on its way down would otherwise grab it and resize
// the panes mid-selection — see the mouseDragging field.
func TestSplitterDragOnlyStartsFromPressOnBar(t *testing.T) {
	sp := NewHorizontalSplitter("bar")
	sp.SetBounds(0, 0, 80, 24)
	before := sp.Ratio()
	bar := sp.SplitPos()

	// Press well above the bar, then drag down across and past it.
	if sp.HandleMouse(press(10, 2)) {
		t.Fatalf("press above the bar consumed by the splitter")
	}
	for _, y := range []int{bar - 2, bar, bar + 1, bar + 4} {
		if sp.HandleMouse(motion(10, y)) {
			t.Fatalf("motion at y=%d consumed by the splitter mid-gesture", y)
		}
	}
	sp.HandleMouse(release(10, bar+4))

	if got := sp.Ratio(); got != before {
		t.Errorf("ratio = %v after a drag crossing the bar, want %v unchanged", got, before)
	}
}

// TestSplitterDragFromBarStillResizes is the other half: a gesture that
// really does begin on the bar must still resize, and must keep receiving
// events after the pointer leaves the bar's own row.
func TestSplitterDragFromBarStillResizes(t *testing.T) {
	sp := NewHorizontalSplitter("bar")
	sp.SetBounds(0, 0, 80, 24)
	before := sp.Ratio()
	bar := sp.SplitPos()

	if !sp.HandleMouse(press(10, bar)) {
		t.Fatalf("press on the bar not consumed")
	}
	if !sp.HandleMouse(motion(10, bar+3)) {
		t.Fatalf("motion off the bar not consumed while dragging")
	}
	if got := sp.Ratio(); got <= before {
		t.Fatalf("ratio = %v after dragging down, want greater than %v", got, before)
	}
	sp.HandleMouse(release(10, bar+3))

	// The release cleared the latch, so the next press on the bar is a
	// fresh gesture rather than a continuation of the last one.
	moved := sp.Ratio()
	if !sp.HandleMouse(press(10, sp.SplitPos())) {
		t.Fatalf("press on the bar after a completed drag not consumed")
	}
	sp.HandleMouse(motion(10, sp.SplitPos()-3))
	if got := sp.Ratio(); got >= moved {
		t.Errorf("ratio = %v after a second drag upward, want less than %v", got, moved)
	}
}
