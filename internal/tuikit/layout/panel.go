package layout

import "github.com/gdamore/tcell/v3"

// ---------------------------------------------------------------------------
// Panel interface
// ---------------------------------------------------------------------------

// Panel is the contract that every right-hand side panel must satisfy.
// SetBounds is called by the layout manager whenever the available space
// changes.  Title is shown in the tab bar.
type Panel interface {
	SetBounds(x, y, w, h int)
	Draw(s tcell.Screen)
	HandleKey(ev *tcell.EventKey) bool
	HandleMouse(ev *tcell.EventMouse) bool
	Title() string
}

// Activatable is an optional interface a Panel may implement to be notified
// when it becomes the visible/focused panel (true) or loses focus (false).
// PanelManager calls SetActive automatically on every panel that implements
// it whenever the active panel changes — panels that don't need an "active"
// concept (e.g. a static read-only view) can simply not implement it.
type Activatable interface {
	SetActive(active bool)
}

// Dirty is an optional interface a Panel may implement to report unsaved
// changes. PanelManager detects it the same way it detects Activatable and
// marks such a panel's tab with a trailing "*" — a panel with nothing worth
// losing (e.g. a static read-only view) can simply not implement it.
type Dirty interface {
	Dirty() bool
}

// Closable is an optional interface a Panel may implement to forbid the tab
// bar's [x] button — e.g. a fixed, always-present panel like Object
// Explorer Details. A panel that doesn't implement it is closable by
// default, matching Activatable/Dirty's "absence means the common case"
// convention.
type Closable interface {
	Closable() bool
}
