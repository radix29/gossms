package core

import "github.com/gdamore/tcell/v3"

// Gesture records which zone of a host widget claimed the Button1 press being
// held, so every event until the release goes back to that zone instead of
// being hit-tested again. tcell's all-motion tracking resends the held button
// on every motion, and positional routing hands a drag to whatever it drifts
// over: a text selection crossing a splitter resizes the panes, and a release
// over another child leaves the claiming one latched.
//
// The zero value holds no gesture. A host keeps one per router and calls, in
// HandleMouse:
//
//   - Release on ButtonNone, beside forwarding the release to every
//     latch-bearing child wherever the pointer is;
//   - Route ahead of any bounds check or hit-testing, so a drag that left the
//     host's rect still reaches its owner;
//   - Arm at every branch that consumed the press.
//
// Z is the host's own zone type; a zone that acted on the press (a toolbar
// cell, a tab) is still armed, and its route case does nothing, so no other
// zone sees the repeats.
type Gesture[Z comparable] struct {
	zone Z
	held bool
}

// Arm claims the gesture for z when ev is a Button1 press. Anything else (a
// right-click, a wheel tick) claims nothing, so the caller may arm on every
// branch that consumed an event without checking the button.
func (g *Gesture[Z]) Arm(ev *tcell.EventMouse, z Z) {
	if ev.Buttons() == tcell.Button1 {
		g.zone, g.held = z, true
	}
}

// Owner returns the zone that claimed the gesture, and whether one is held.
func (g *Gesture[Z]) Owner() (Z, bool) { return g.zone, g.held }

// Release ends the gesture.
func (g *Gesture[Z]) Release() { *g = Gesture[Z]{} }

// Route delivers a mid-gesture event: a held Button1 goes to route with the
// owning zone (a host passes its routeDrag method value), and anything else (a
// wheel tick) is swallowed, since it is not part of the gesture and routing it
// by position would scroll whatever the pointer drifted over. It reports
// whether a gesture is held, in which case the event is consumed; false means
// the caller routes it by position. The release must be handled (Release)
// before Route sees it.
func (g *Gesture[Z]) Route(ev *tcell.EventMouse, route func(*tcell.EventMouse, Z)) bool {
	if !g.held {
		return false
	}
	if ev.Buttons() == tcell.Button1 {
		route(ev, g.zone)
	}
	return true
}
