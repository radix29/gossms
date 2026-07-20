package controls

import (
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

// TestToolbarHeldButtonDoesNotRefire pins the fix for a real bug: a toggle
// button (e.g. "Include Actual Execution Plan") flipped back and forth
// repeatedly when the mouse so much as twitched during a click, because
// tcell's all-motion mouse tracking resends Buttons()==Button1 on every
// motion event while the button stays down — the same bug shape already
// fixed for TreeView's expander and MenuBar's header toggle (see their own
// mouseDragging fields and tests).
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

// TestToolbarHoverOnDisabledButtonStillSetsHoverForTooltip pins down the
// resolved "does the tooltip still show for a disabled button" question:
// yes — hovering must still set tb.hover (which DrawOverlay keys its
// tooltip render off of) even though the button won't fire on click, so
// the user can still see what the greyed-out icon is for.
func TestToolbarHoverOnDisabledButtonStillSetsHoverForTooltip(t *testing.T) {
	tb := newTestDisabledToolbar(nil)

	tb.HandleMouse(tcell.NewEventMouse(1, 0, tcell.ButtonNone, tcell.ModNone))

	if tb.hover != 0 {
		t.Fatalf("hover = %d, want 0 (disabled button still tracked for hover/tooltip)", tb.hover)
	}
}
