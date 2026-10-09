package propsheet

import "github.com/gdamore/tcell/v3"

// Row is the minimal contract every property-sheet row implements, whether a
// section header, static value or editable field. Form drives layout and drawing
// through this interface plus the optional capability interfaces below, so new
// row kinds never require a change to Form.
type Row interface {
	// Height returns how many terminal rows this row occupies at width w (most
	// ignore w; Note wraps text and depends on it).
	Height(w int) int
	// Layout assigns the row's on-screen position ahead of Draw, called every frame
	// with the row's top-left and available width.
	Layout(x, y, w int)
	// Draw renders the row. focused is true only for the one row holding form focus.
	Draw(s tcell.Screen, focused bool)
	// Focusable reports whether this row can receive keyboard focus (false for
	// Section/Note/Static).
	Focusable() bool
}

// KeyHandler is implemented by rows that consume key events while focused (all
// editable rows). Form forwards keys to the focused row first; on false it falls
// back to its own navigation keys.
type KeyHandler interface {
	HandleKey(ev *tcell.EventKey) bool
}

// MouseHandler is implemented by rows that consume mouse events themselves
// (grids, buttons, dropdowns, checkboxes). Form finds the row under the cursor
// by the band each row occupied on the last Draw (not by asking the row), then
// forwards the event; the row must still ignore events outside its bounds and
// return false, as every tuikit control's HandleMouse does.
type MouseHandler interface {
	HandleMouse(ev *tcell.EventMouse) bool
}

// Copyable is implemented by rows with a sensible "copy to clipboard" value:
// every row kind except Section/Note.
type Copyable interface {
	CopyText() string
}

// Editable is implemented by every row whose value can change from its
// as-loaded baseline: Dirty reports whether it differs, Revert restores the
// baseline, Validate returns an error if the current value can't be applied.
type Editable interface {
	Dirty() bool
	Revert()
	Validate() error
}

// ClipboardRow is implemented by rows that support the full cut/paste/select-all
// cycle, not just a copyable value: TextRow, EditorRow and GridRow (the last only
// while its cell viewer is open). It is the row-level analogue of the
// clipboardTarget contract of widgets.InputField and controls.Editor;
// PropertySheet forwards to it from its own HasSelection/SelectedText/Cut/Paste/
// SelectAll so the sheet satisfies that contract.
type ClipboardRow interface {
	Copyable
	HasSelection() bool
	SelectedText() string
	Cut() string
	Paste(text string)
	SelectAll()
}

// Shrinkable is implemented by rows that can render in fewer lines than
// Height(w) reports: a GridRow (its DataGrid scrolls within whatever space it
// gets) and a wrapped Note. Form clamps such a row to the space left above the
// bottom edge, and skips it once less than MinDrawHeight lines remain, except
// the first row on screen, which always draws so the page can't come up blank.
type Shrinkable interface {
	// MinDrawHeight is the fewest lines the row still renders usefully in.
	MinDrawHeight() int
	// SetDrawHeight sets the height the next Layout/Draw pair must use.
	SetDrawHeight(h int)
}

// OverlayDrawer is implemented by rows that render a popup which must draw after
// every other row, so rows below don't paint over it (a dropdown's open list).
type OverlayDrawer interface {
	DrawOverlay(s tcell.Screen)
}

// OverlayActiver is implemented by rows whose popup can currently be open
// (SelectRow's dropdown list, GridRow's full-cell-content popup) — see
// Form.OverlayActive.
type OverlayActiver interface {
	OverlayActive() bool
}

// ReadOnlyDrawer is implemented by a row that can render itself as flat,
// uneditable text (no input box, brackets or dropdown arrow). Form switches every
// implementer whenever SetReadOnly changes.
//
// Behaviour and appearance are separate halves of read-only and both are needed:
// Form.SetReadOnly already makes a gated page impossible to edit, but a row that
// still draws its control reads as a field the terminal refuses to type into
// rather than as a value.
type ReadOnlyDrawer interface {
	SetDrawReadOnly(v bool)
}

// Browsable is implemented by a row that may still take focus on a read-only
// form, to be looked at rather than edited: GridRow and ToggleGridRow, whose
// selection drives the detail rows beside them. Form.SetReadOnly leaves every
// other row unfocusable and unclickable; a Browsable row keeps both and is
// trusted to have made itself inert through ReadOnlyDrawer (a grid goes
// browse-only: navigates, selects and copies, activates no cell), so the form
// still can't become dirty.
type Browsable interface {
	Browsable() bool
}

// Revealer is implemented by a row that can ask, from a handler, to be scrolled
// into view (a HintRow a page's button sets). TakeReveal reports whether it has
// asked since the last call and clears the request; Form.Draw calls it every
// frame. Without it a hint set below the fold (Accounts' "is deleted on Apply",
// under the focused Remove button) changes nothing the user can see.
type Revealer interface {
	TakeReveal() bool
}
