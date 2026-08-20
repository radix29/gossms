package core

// ClipboardTarget is implemented by any widget that can take part in
// Copy/Cut/Paste. widgets.InputField, controls.Editor, controls.DataGrid and
// propsheet.PropertySheet all satisfy it structurally.
//
// It lives in core, the one package every widget package and the application
// layer already import, so that a container can hand its focused widget back
// across a package boundary (see ClipboardHost). Two identical interfaces
// declared in two packages would not let it: a method returning one is not a
// method returning the other.
type ClipboardTarget interface {
	HasSelection() bool
	SelectedText() string
	Cut() string
	Paste(text string)
	SelectAll()
}

// ClipboardHost is implemented by a container — a dialog, a property sheet —
// that owns ClipboardTargets and knows which one has keyboard focus.
//
// Returning nil is a real answer, not a failure: it means "nothing here can
// take this key", and the application must then do nothing at all rather than
// look for a target elsewhere. That is the whole point of the interface. The
// application resolves Ctrl+C/X/V by asking the frontmost open dialog for its
// target instead of enumerating dialog types, because the enumeration fell
// behind: it knew three of thirty dialogs, and every other one fell through to
// the query editor underneath — Ctrl+X in the Find dialog cut the editor's
// text, invisibly, behind the dialog.
//
// A dialog with no text entry deliberately does not implement this, which
// makes the clipboard inert while it is open.
type ClipboardHost interface {
	FocusedClipboardTarget() ClipboardTarget
}

// ClipboardTargetTokener is an optional companion to ClipboardHost, for a
// host that answers FocusedClipboardTarget with *itself* and resolves the real
// field on each call. The token is an opaque identity for whichever field that
// is right now — compare two tokens with ==, never interpret one.
//
// It exists for the paste half. App.pasteInto guards an asynchronous clipboard
// read by checking that the target it was handed is still the active one, and
// against such a host that check always passes: the sheet is the target
// whichever of its rows has focus. A paste aimed at one row and delivered
// after focus moved to another therefore lands in the wrong row. The token
// makes the guard see inside the host without making the host expose its rows.
//
// A host that returns a distinct ClipboardTarget per field needs none of this;
// the identity check on the target already covers it.
type ClipboardTargetTokener interface {
	ClipboardTargetToken() any
}

// ClipboardEditHandler is an optional companion to ClipboardHost, for a host
// that has to follow up an edit the clipboard made in one of its fields — the
// way a dialog with an autocomplete list has to re-filter it. Called after a
// Cut or a Paste changed target, never after a Copy.
//
// The edited target is passed because a host usually cares about one field:
// the application knows only "something in this dialog changed", and a host
// that acts on that unconditionally reacts to a paste into an unrelated field
// as if the field it watches had been typed in.
type ClipboardEditHandler interface {
	ClipboardEdited(target ClipboardTarget)
}
