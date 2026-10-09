package dialogs

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// FieldGesture is the drag latch a dialog needs for the text-selection gesture a
// click inside an InputField starts. Embed one in any dialog with a text field
// and drive it from HandleMouse; it is the dialog-level half of the per-widget
// mouseDragging latch (ARCHITECTURE.md § The mouseDragging idiom).
//
// It is a type, not a per-dialog idiom, because the idiom is correct only in one
// order, not local to any of the three calls: it depends on what
// ModalDialog.ConsumeOutsideClick and a dialog's mode switch do to the events
// the latch needs. See Release and Replay.
type FieldGesture struct {
	field *widgets.InputField
}

// Release ends the gesture, forwarding the release to whichever field claimed
// the press, wherever the pointer is by then.
//
// Call it at the very top of HandleMouse, on ButtonNone, before
// ConsumeOutsideClick and any early return for a dialog mode. Both return
// without looking at the latch, and a release outside the dialog (or during a
// progress view) is what strands it: the field stays latched and its next press
// is swallowed as a continuation of the finished drag.
//
// A no-op unless ev is a release and a gesture is held, so safe to call
// unconditionally.
func (g *FieldGesture) Release(ev *tcell.EventMouse) {
	if ev.Buttons() != tcell.ButtonNone || g.field == nil {
		return
	}
	g.field.HandleMouse(ev)
	g.field = nil
}

// Replay reports whether a gesture is held, forwarding ev to its owner when it
// is; true means the event is spoken for and HandleMouse must return.
//
// Call it after ConsumeOutsideClick and before any hit-testing: hit-testing a
// motion event would end the selection when the pointer left the field's rect,
// and letting it reach ButtonClicked would fire a button when a selection drag
// wandered over the button row.
func (g *FieldGesture) Replay(ev *tcell.EventMouse) bool {
	if g.field == nil || ev.Buttons() != tcell.Button1 {
		return false
	}
	g.field.HandleMouse(ev)
	return true
}

// Claim gives the gesture to f and forwards the press, so the click positions
// the cursor or starts a selection rather than only moving focus. Call it from
// the branch whose hit-test matched f.
func (g *FieldGesture) Claim(f *widgets.InputField, ev *tcell.EventMouse) {
	g.field = f
	f.HandleMouse(ev)
}

// Clear drops a held gesture without forwarding anything, and the field's own
// mouseDragging latch with it. Call it from Show: a latch must not survive into
// the dialog's next showing, or the first press of the new session reads as the
// last one's drag continuing.
//
// Both halves, because a dialog dismissed mid-drag (Escape with the button down)
// never sees the release, and dialogs that build their fields once (Connect,
// Options, Find/Replace, Log Search) hand the same field back next showing. With
// its latch still set, the first press after reopening took
// InputField.HandleMouse's continued-drag branch, placing the cursor without
// arming a new anchor. Invariant 4 in docs/ui-rules.md wants both latches gone,
// and this is the only call that can reach the field's. Backup, Restore and
// Filter rebuild their fields in show, discarding the stale one.
func (g *FieldGesture) Clear() {
	if g.field != nil {
		g.field.CancelMouseDrag()
	}
	g.field = nil
}

// Field is the field holding the gesture, or nil, for a caller or test that
// needs more than "a gesture is in progress".
func (g *FieldGesture) Field() *widgets.InputField { return g.field }
