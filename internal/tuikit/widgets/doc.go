// Package widgets provides stateful, self-contained input controls that
// render themselves onto a tcell.Screen.  Each widget follows a common
// pattern:
//
//   - Bounds are set with SetBounds(x, y) or SetRect(core.Rect).
//   - Keyboard input is handled with HandleKey(*tcell.EventKey) bool.
//   - Mouse input is handled with HandleMouse(*tcell.EventMouse) bool.
//   - Rendering is done with Draw(tcell.Screen).
//   - Focus is toggled with Focus(bool).
//
// Widgets are purely presentational; they hold their own value state but
// know nothing about the application.  The caller reads values via Value(),
// Checked(), Selected(), etc.
//
// One file per widget: input_field.go, dropdown.go, checkbox.go, button.go,
// radiobox.go. common.go holds small helpers shared across more than one of
// them.
package widgets
