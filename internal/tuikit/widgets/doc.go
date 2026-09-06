// Package widgets provides stateful, self-contained input controls that
// render themselves onto a tcell.Screen.  Each widget follows a common
// pattern:
//
//   - Bounds are set with SetBounds(x, y); each widget's width comes from
//     its constructor.
//   - Keyboard input is handled with HandleKey(*tcell.EventKey) bool.
//   - Mouse input is handled with HandleMouse(*tcell.EventMouse) bool.
//   - Rendering is done with Draw(tcell.Screen).
//   - Focus is toggled with Focus(bool).
//
// Widgets are purely presentational; they hold their own value state but
// know nothing about the application.  The caller reads values via Value(),
// Checked(), Selected(), etc.
//
// spinner.go is the exception to the pattern above: a Spinner is a busy
// indicator, not an input, so it has no bounds, focus or event handling — it
// is a value that answers which frame shows for a given elapsed duration, and
// leaves the redraw clock to the host.
//
// One file per widget: input_field.go, dropdown.go, checkbox.go, button.go,
// radiobox.go, spinner.go. common.go holds small helpers shared across more
// than one of them.
package widgets
