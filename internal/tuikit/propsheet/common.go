package propsheet

import "github.com/gdamore/tcell/v3"

// Row is the minimal contract every property-sheet row implements —
// whether it's a section header, a static value, or an editable field.
// Form drives layout and drawing entirely through this interface plus the
// optional capability interfaces below, so new row kinds never require a
// change to Form itself.
type Row interface {
	// Height returns how many terminal rows this row occupies when laid
	// out at width w (most rows ignore w and return a constant; Note
	// wraps text and so depends on it).
	Height(w int) int
	// Layout assigns the row's on-screen position ahead of Draw — called
	// every frame with the row's top-left and available width.
	Layout(x, y, w int)
	// Draw renders the row. focused is true only for the single row
	// currently holding form focus.
	Draw(s tcell.Screen, focused bool)
	// Focusable reports whether this row can receive keyboard focus
	// (false for Section/Note/Static).
	Focusable() bool
}

// KeyHandler is implemented by rows that consume key events while
// focused (all editable rows). Form forwards keys to the focused row
// first; if it returns false, Form falls back to its own navigation keys.
type KeyHandler interface {
	HandleKey(ev *tcell.EventKey) bool
}

// MouseHandler is implemented by rows that consume mouse events
// themselves (grids, buttons, dropdowns, checkboxes). Form locates the row
// under the cursor by the on-screen band each row occupied on the last
// Draw (not by asking the row), then forwards the event; the row is still
// expected to ignore events outside its own bounds and return false, the
// same contract every other tuikit control's HandleMouse follows.
type MouseHandler interface {
	HandleMouse(ev *tcell.EventMouse) bool
}

// Copyable is implemented by rows that have a sensible "copy to clipboard"
// value — every row kind except Section/Note.
type Copyable interface {
	CopyText() string
}

// Editable is implemented by every row whose value can change from its
// as-loaded baseline: Dirty reports whether it currently differs, Revert
// restores the baseline, and Validate returns a non-nil error if the
// current value can't be applied (e.g. out of range).
type Editable interface {
	Dirty() bool
	Revert()
	Validate() error
}

// ClipboardRow is implemented by rows that support the full cut/paste/
// select-all cycle, not just a copyable value — today only TextRow. It's
// the row-level analogue of the clipboardTarget contract every other
// tuikit-backed dialog field in internal/tui satisfies structurally
// (widgets.InputField, controls.Editor); PropertySheet forwards to it from
// its own HasSelection/SelectedText/Cut/Paste/SelectAll so the sheet as a
// whole can satisfy that same contract.
type ClipboardRow interface {
	Copyable
	HasSelection() bool
	SelectedText() string
	Cut() string
	Paste(text string)
	SelectAll()
}

// OverlayDrawer is implemented by rows that render a popup which must be
// drawn after every other row in the form, so it isn't painted over by
// rows below it (a dropdown's open list).
type OverlayDrawer interface {
	DrawOverlay(s tcell.Screen)
}

// OverlayActiver is implemented by rows whose popup can currently be open
// (SelectRow's dropdown list, GridRow's full-cell-content popup) — see
// Form.OverlayActive.
type OverlayActiver interface {
	OverlayActive() bool
}
