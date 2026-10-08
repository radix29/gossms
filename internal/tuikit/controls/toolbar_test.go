package controls

import (
	"slices"
	"testing"

	"github.com/gdamore/tcell/v3"
)

func newTestToolbar(action func()) *Toolbar {
	tb := NewToolbar()
	tb.SetButtons([]ToolbarButton{{Icon: "Toggle", Tooltip: "Toggle", Action: action}})
	tb.SetBounds(0, 0, 8) // exactly one button's width, so it starts at column 0
	return tb
}

// TestToolbarClickFiresAction confirms a plain click still works — the
// baseline TestToolbarHeldButtonDoesNotRefire guards against regressing.
func TestToolbarClickFiresAction(t *testing.T) {
	calls := 0
	tb := newTestToolbar(func() { calls++ })

	tb.HandleMouse(tcell.NewEventMouse(1, 0, tcell.Button1, tcell.ModNone))

	if calls != 1 {
		t.Fatalf("Action calls = %d, want 1", calls)
	}
}

// TestToolbarHeldButtonDoesNotRefire covers tcell's all-motion mouse
// tracking resending Buttons()==Button1 on every motion event while the
// button stays down: without the mouseDragging latch, a toggle button (e.g.
// "Include Actual Execution Plan") flips back and forth whenever the mouse
// twitches during a click. TreeView's expander and MenuBar's header toggle
// carry the same latch.
func TestToolbarHeldButtonDoesNotRefire(t *testing.T) {
	calls := 0
	tb := newTestToolbar(func() { calls++ })

	// Press fires the action once.
	tb.HandleMouse(tcell.NewEventMouse(1, 0, tcell.Button1, tcell.ModNone))
	if calls != 1 {
		t.Fatalf("Action calls after press = %d, want 1", calls)
	}

	// The button is still down and the mouse merely shifted a column while
	// staying over the same button — must not refire.
	tb.HandleMouse(tcell.NewEventMouse(2, 0, tcell.Button1, tcell.ModNone))
	if calls != 1 {
		t.Fatalf("Action calls after held-button move = %d, want still 1", calls)
	}

	// Release, then a genuine new press, does fire again.
	tb.HandleMouse(tcell.NewEventMouse(2, 0, tcell.ButtonNone, tcell.ModNone))
	tb.HandleMouse(tcell.NewEventMouse(2, 0, tcell.Button1, tcell.ModNone))
	if calls != 2 {
		t.Fatalf("Action calls after release + fresh press = %d, want 2", calls)
	}
}

// TestToolbarDragOffAndBackDoesNotRefire confirms dragging off the button
// (still holding Button1) and back onto it — without ever releasing — is
// still treated as one continuous press, not a new one.
func TestToolbarDragOffAndBackDoesNotRefire(t *testing.T) {
	calls := 0
	tb := newTestToolbar(func() { calls++ })

	tb.HandleMouse(tcell.NewEventMouse(1, 0, tcell.Button1, tcell.ModNone))
	tb.HandleMouse(tcell.NewEventMouse(20, 0, tcell.Button1, tcell.ModNone)) // off the button, still held
	tb.HandleMouse(tcell.NewEventMouse(1, 0, tcell.Button1, tcell.ModNone))  // back onto it, still held

	if calls != 1 {
		t.Errorf("Action calls after drag off and back without release = %d, want 1", calls)
	}
}

// newTestDisabledToolbar mirrors newTestToolbar but the button is always
// disabled — the shared fixture for the Enabled-gating tests below.
func newTestDisabledToolbar(action func()) *Toolbar {
	tb := NewToolbar()
	tb.SetButtons([]ToolbarButton{{Icon: "Toggle", Tooltip: "Toggle", Action: action, Enabled: func() bool { return false }}})
	tb.SetBounds(0, 0, 8)
	return tb
}

func TestToolbarClickOnDisabledButtonDoesNotFire(t *testing.T) {
	calls := 0
	tb := newTestDisabledToolbar(func() { calls++ })

	tb.HandleMouse(tcell.NewEventMouse(1, 0, tcell.Button1, tcell.ModNone))

	if calls != 0 {
		t.Fatalf("Action calls = %d, want 0 for a disabled button", calls)
	}
}

// TestToolbarHoverOnDisabledButtonStillSetsHoverForTooltip pins down that a
// disabled button still shows its tooltip: hovering sets tb.hover (which
// DrawOverlay keys its tooltip render off of) even though the button won't
// fire on click, so it's still possible to see what the greyed-out icon
// is for.
func TestToolbarHoverOnDisabledButtonStillSetsHoverForTooltip(t *testing.T) {
	tb := newTestDisabledToolbar(nil)

	tb.HandleMouse(tcell.NewEventMouse(1, 0, tcell.ButtonNone, tcell.ModNone))

	if tb.hover != 0 {
		t.Fatalf("hover = %d, want 0 (disabled button still tracked for hover/tooltip)", tb.hover)
	}
}

// mainToolbarShape mirrors the app's main toolbar: icon widths, dividers and
// DropRanks as buildToolbar sets them.
func mainToolbarShape() []ToolbarButton {
	return []ToolbarButton{
		{Icon: "✚", Tooltip: "New Query", DropRank: 2},
		{Divider: true, Icon: "|"},
		{Icon: "▶", Tooltip: "Execute"},
		{Icon: "▷", Tooltip: "Execute Selection", DropRank: 3},
		{Icon: "■", Tooltip: "Stop Execution", DropRank: 1},
		{Divider: true, Icon: "|"},
		{Icon: "Est.Plan", Tooltip: "Est", DropRank: 5},
		{Icon: "Act.Plan[-OFF]", Tooltip: "Act", DropRank: 6},
		{Icon: "Live[-OFF]", Tooltip: "Live", DropRank: 7},
		{Icon: "Meta[-OFF]", Tooltip: "Meta", DropRank: 8},
		{Divider: true, Icon: "|"},
		{Icon: "📈", Tooltip: "Activity Monitor", DropRank: 4},
	}
}

// TestToolbarNeverCrossesLeftLimit covers B19: SetBounds right-aligned with no
// left limit, so on a narrow terminal the buttons painted over the menu
// labels and Tools and Help vanished. No shown button may start left of x,
// and the shown buttons stay flush right.
func TestToolbarNeverCrossesLeftLimit(t *testing.T) {
	const limit = 52 // roughly where the main menu labels end
	for _, screenW := range []int{40, 60, 80, 100, 112, 140} {
		tb := NewToolbar()
		tb.SetButtons(mainToolbarShape())
		tb.SetBounds(limit, 0, screenW-limit)
		end := limit
		for i := range tb.buttons {
			if tb.widths[i] == 0 {
				continue
			}
			if tb.starts[i] < limit {
				t.Errorf("width %d: button %q starts at %d, left of limit %d", screenW, tb.buttons[i].Icon, tb.starts[i], limit)
			}
			end = tb.starts[i] + tb.widths[i]
		}
		if tb.rect.W > 0 && end != screenW {
			t.Errorf("width %d: last shown button ends at %d, want flush at %d", screenW, end, screenW)
		}
	}
}

// TestToolbarDropsByRank pins the drop order: highest DropRank first, unranked
// last, and a divider left leading, trailing or doubled is hidden too.
func TestToolbarDropsByRank(t *testing.T) {
	shownIcons := func(w int) []string {
		tb := NewToolbar()
		tb.SetButtons(mainToolbarShape())
		tb.SetBounds(0, 0, w)
		var icons []string
		for i, b := range tb.buttons {
			if tb.widths[i] > 0 {
				icons = append(icons, b.Icon)
			}
		}
		return icons
	}
	cases := []struct {
		w    int
		want []string
	}{
		{100, []string{"✚", "|", "▶", "▷", "■", "|", "Est.Plan", "Act.Plan[-OFF]", "Live[-OFF]", "Meta[-OFF]", "|", "📈"}},
		// Widths are icon + 2: 75 in all. Meta, Live, then Act.Plan drop first.
		{74, []string{"✚", "|", "▶", "▷", "■", "|", "Est.Plan", "Act.Plan[-OFF]", "Live[-OFF]", "|", "📈"}},
		{50, []string{"✚", "|", "▶", "▷", "■", "|", "Est.Plan", "|", "📈"}},
		// Est.Plan gone: the divider before it would double up with 📈's.
		{24, []string{"✚", "|", "▶", "▷", "■", "|", "📈"}},
		// Activity Monitor gone: its divider would trail.
		{21, []string{"✚", "|", "▶", "▷", "■"}},
		{14, []string{"✚", "|", "▶", "■"}},
		{9, []string{"▶", "■"}},
		{5, []string{"▶"}},
		{2, nil},
	}
	for _, c := range cases {
		if got := shownIcons(c.w); !slices.Equal(got, c.want) {
			t.Errorf("width %d: shown = %q, want %q", c.w, got, c.want)
		}
	}
}

// TestToolbarHiddenButtonIsNotClickable confirms a dropped button neither
// hovers nor fires — its column belongs to the menu labels now.
func TestToolbarHiddenButtonIsNotClickable(t *testing.T) {
	calls := 0
	tb := NewToolbar()
	tb.SetButtons([]ToolbarButton{
		{Icon: "Drop", Tooltip: "Drop", DropRank: 1, Action: func() { calls++ }},
		{Icon: "Keep", Tooltip: "Keep"},
	})
	tb.SetBounds(0, 0, 6)
	for x := range 6 {
		tb.HandleMouse(tcell.NewEventMouse(x, 0, tcell.ButtonNone, tcell.ModNone))
		tb.HandleMouse(tcell.NewEventMouse(x, 0, tcell.Button1, tcell.ModNone))
	}
	if calls != 0 {
		t.Errorf("hidden button fired %d times", calls)
	}
}
