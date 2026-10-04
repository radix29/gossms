package dialogs

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// FieldGesture is the drag latch a dialog needs for the text-selection gesture
// a click inside an InputField starts. Embed one in any dialog with a text
// field and drive it from HandleMouse; it is the dialog-level half of the
// per-widget mouseDragging latch (ARCHITECTURE.md § The mouseDragging idiom).
//
// It is a type rather than a two-line idiom per dialog because the idiom is
// correct only in one *order*, which is not local to any of the three calls: it
// depends on what ModalDialog.ConsumeOutsideClick and a dialog's mode switch do
// to the events the latch depends on. See Release and Replay for the two
// placements that matter.
//
// The zero value is a gesture nobody holds, the correct initial state.
type FieldGesture struct {
	field *widgets.InputField
}

// Release ends the gesture, forwarding the release to whichever field claimed
// the press, wherever the pointer is by then.
//
// Call it at the very top of HandleMouse, on ButtonNone, **before**
// ConsumeOutsideClick and before any early return for a dialog mode. Both
// return without looking at the latch, and a release outside the dialog (or
// arriving while the dialog shows a progress view) is the event that strands
// it: the field stays latched and its next press is swallowed as a continuation
// of the finished drag.
//
// A no-op unless ev is a release and a gesture is held, so it is safe to call
// unconditionally.
func (g *FieldGesture) Release(ev *tcell.EventMouse) {
	if ev.Buttons() != tcell.ButtonNone || g.field == nil {
		return
	}
	g.field.HandleMouse(ev)
	g.field = nil
}

// Replay reports whether a gesture is held, forwarding ev to its owner when it
// is. A true answer means the event is spoken for and HandleMouse must return
// without looking further.
//
// Call it after ConsumeOutsideClick and before any hit-testing. Hit-testing a
// motion event would end the selection the moment the pointer left the field's
// rect, and letting it reach ButtonClicked would fire a button the moment a
// selection drag wandered over the button row.
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

// Clear drops a held gesture without forwarding anything, and drops the field's
// own mouseDragging latch with it. Call it from Show: a latch must not survive
// into the dialog's next showing, or the first press of the new session is read
// as the continuation of the last one's drag.
//
// Both halves, because a dialog dismissed mid-drag (Escape with the button
// still down) never sees the release, so the field stays latched along with the
// gesture, and dialogs that build their fields once (Connect, Options,
// Find/Replace, Log Search) hand the same field back next showing. With the
// field's latch still set, the first press after reopening took
// InputField.HandleMouse's continued-drag branch, placing the cursor without
// arming a new anchor, so the drag selected from wherever the old cursor was.
// Invariant 4 in docs/ui-rules.md wants both latches gone, and this is the only
// call that can reach the field's. Backup, Restore and Filter rebuild their
// fields in show, so the stale one is discarded there.
func (g *FieldGesture) Clear() {
	if g.field != nil {
		g.field.CancelMouseDrag()
	}
	g.field = nil
}

// Field is the field holding the gesture, or nil when none is. It answers
// *which* field for a caller — or a test — that needs more than "a gesture is
// in progress".
func (g *FieldGesture) Field() *widgets.InputField { return g.field }
