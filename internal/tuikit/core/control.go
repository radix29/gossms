package core

import "github.com/gdamore/tcell/v3"

// The control contract, as small interfaces a host can type-assert on instead
// of switching over concrete types. Every interactive tuikit control implements
// the ones that fit it; control_test.go in each package pins which. They live
// in core, not in the packages declaring the controls, so a host (a focus ring,
// propsheet.Form, an application dialog) can hold a widgets.InputField and a
// controls.Editor in one slice: identical interfaces declared in two packages
// would not be interchangeable.

// KeyHandler takes a key. HandleKey returns true only for a key the control
// acted on; a host offers the key to its own navigation (Tab, arrows past an
// edge, Escape) on false.
type KeyHandler interface {
	HandleKey(ev *tcell.EventKey) bool
}

// MouseHandler takes a mouse event. HandleMouse returns false for an event
// outside the control's bounds that it did not latch (a drag it started keeps
// control off its edge), so a host can offer the event to whatever is beneath.
type MouseHandler interface {
	HandleMouse(ev *tcell.EventMouse) bool
}

// Focusable is a control that draws and behaves differently with keyboard
// focus. Focus(false) also dismisses anything the control opened that would
// otherwise linger while keys go elsewhere (a drop-down list, a completion
// popup). Focus is the host's statement, not the control's: no control
// focuses itself on a click, since only the host knows its focus ring.
type Focusable interface {
	Focus(v bool)
	Focused() bool
}

// Enabler is a control that can be switched off. A disabled control draws
// greyed, refuses keys and clicks, keeps its place in a focus ring (Tab order
// does not shift when a page switches a control off) and its own setters still
// work, so a host can pin a value the server requires and then disable it.
type Enabler interface {
	SetEnabled(v bool)
	Enabled() bool
}

// Bounded is a control placed on the screen. Bounds returns the rect it
// occupies, for a host hit-testing or positioning related UI (an autocomplete
// list under a field, a drop target). A one-row widget's rect covers its label
// and its box; its W is the drawn width, not the value area's (see each
// widget's Width).
type Bounded interface {
	Bounds() Rect
}

// Overlay is a control that can show something floating outside its own rect:
// a drop-down's list, a menu, a completion popup, a cell viewer.
//
// DrawOverlay draws it, and the host calls it after everything that shares the
// space has drawn, or what draws later paints over it. It draws nothing while
// no overlay is shown.
//
// OverlayActive reports whether such an overlay is open. While it is, the host
// gives the control first refusal of every key and mouse event: the overlay is
// positioned independently of the control's rect, so routing by position or
// focus would hand its input to whatever sits under it.
type Overlay interface {
	DrawOverlay(s tcell.Screen)
	OverlayActive() bool
}
