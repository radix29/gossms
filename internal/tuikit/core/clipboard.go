package core

// ClipboardTarget is implemented by any widget that can take part in
// Copy/Cut/Paste (widgets.InputField, controls.Editor, controls.DataGrid,
// propsheet.PropertySheet). It lives in core so a container can hand its focused
// widget back across a package boundary (see ClipboardHost); identical
// interfaces declared in two packages would not be interchangeable.
type ClipboardTarget interface {
	HasSelection() bool
	SelectedText() string
	Cut() string
	Paste(text string)
	SelectAll()
}

// ClipboardHost is implemented by a container (a dialog, a property sheet) that
// owns ClipboardTargets and knows which has keyboard focus.
//
// Returning nil is a real answer: "nothing here can take this key", and the
// application must then do nothing rather than look elsewhere. The application
// resolves Ctrl+C/X/V by asking the frontmost dialog, not by enumerating dialog
// types: that list fell behind, and Ctrl+X in the Find dialog cut the query
// editor's text invisibly behind it.
//
// A dialog with no text entry deliberately does not implement this, which makes
// the clipboard inert while it is open.
type ClipboardHost interface {
	FocusedClipboardTarget() ClipboardTarget
}

// ClipboardTargetTokener is an optional companion to ClipboardHost, for a host
// that answers FocusedClipboardTarget with itself and resolves the real field
// per call. The token is an opaque identity of the current field: compare with
// ==, never interpret.
//
// App.pasteInto guards an asynchronous clipboard read by checking the target is
// still the active one; against such a host that always passes, so a paste aimed
// at one row but delivered after focus moved lands in another. The token lets
// the guard see inside the host without exposing its rows. A host returning a
// distinct target per field needs none of this.
type ClipboardTargetTokener interface {
	ClipboardTargetToken() any
}

// ClipboardEditHandler is an optional companion to ClipboardHost, for a host
// that must follow up a clipboard edit of one of its fields (e.g. re-filtering
// an autocomplete list). Called after a Cut or Paste changed target, never a
// Copy. The edited target is passed because the application only knows
// "something changed", and acting on that unconditionally would react to a
// paste into an unrelated field as if the watched one were typed in.
type ClipboardEditHandler interface {
	ClipboardEdited(target ClipboardTarget)
}
