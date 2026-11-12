package core

import "github.com/gdamore/tcell/v3"

// PressLatch tells a fresh Button1 press from its resends. tcell's all-motion
// tracking resends the held button on every motion event, so a widget acting
// on Button1 (a button, a menu label, a tree toggle) would act again on every
// twitch of a held click. The latch is set by the press the widget acts on and
// cleared by the matching ButtonNone.
//
// It guards only resends over the widget that armed it: a drag that began
// elsewhere and drifted onto the widget looks like a fresh press. Telling those
// apart is the router's job (Gesture), or, for a widget that sees every event
// of the gesture, calling Fresh on every Button1 rather than only on a hit
// (Splitter, ModalDialog's button row).
//
// The zero value holds nothing. In HandleMouse, call Release first, ahead of
// any bounds check or early return: a release landing outside the widget must
// still clear it, or the widget refuses its next press.
type PressLatch struct{ held bool }

// Release clears the latch when ev is a release (ButtonNone), and reports
// whether it was one, so a widget with nothing else to do on a release can
// return straight away.
func (l *PressLatch) Release(ev *tcell.EventMouse) bool {
	if ev.Buttons() != tcell.ButtonNone {
		return false
	}
	l.held = false
	return true
}

// Fresh reports whether ev is a Button1 press the latch has not yet claimed,
// and claims it. A resend of the held press, and any other button, reports
// false; any other button leaves the latch as it was.
func (l *PressLatch) Fresh(ev *tcell.EventMouse) bool {
	if ev.Buttons() != tcell.Button1 || l.held {
		return false
	}
	l.held = true
	return true
}

// Held reports whether a claimed press has not yet been released.
func (l *PressLatch) Held() bool { return l.held }

// Clear drops the latch without a release: for a widget shown again, or one
// whose gesture was cancelled, whose release will never arrive (see
// ModalDialog.Show).
func (l *PressLatch) Clear() { l.held = false }
