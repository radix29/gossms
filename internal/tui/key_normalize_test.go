package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/dialogs"
)

// newKeyTestApp is newTestApp plus the dialogs handleKey touches.
func newKeyTestApp(t testing.TB) *App {
	t.Helper()
	a := newTestApp()
	installDialog(a, NewKeyDiagnosticsDialog(a))
	installDialog(a, NewConnectDialog(a))
	installDialog(a, dialogs.NewFileDialog())
	installDialog(a, NewHelpDialog(a))
	a.menuBar = controls.NewMenuBar()
	a.contextMenu = new(controls.ContextMenu{})
	return a
}

// F9 is the Connect binding that works on every terminal; Ctrl+Shift+O is
// the alias for terminals that can encode it, and only reaches the binding
// because handleKey normalizes it first. Plain Ctrl+O must still open a file.
func TestConnectDialogKeyBindings(t *testing.T) {
	tests := []struct {
		name        string
		ev          *tcell.EventKey
		wantConnect bool
	}{
		{"F9", tcell.NewEventKey(tcell.KeyF9, "", tcell.ModNone), true},
		{"kitty ctrl+shift+o", tcell.NewEventKey(tcell.KeyRune, "o", tcell.ModCtrl|tcell.ModShift), true},
		{"modifyOtherKeys ctrl+shift+O", tcell.NewEventKey(tcell.KeyRune, "O", tcell.ModCtrl|tcell.ModShift), true},
		{"plain ctrl+o opens a file", tcell.NewEventKey(tcell.KeyCtrlO, "", tcell.ModCtrl), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newKeyTestApp(t)
			if a.handleKey(tt.ev) {
				t.Fatal("handleKey signalled quit")
			}
			if got := a.connectDialog().Visible(); got != tt.wantConnect {
				t.Fatalf("connect dialog visible = %v, want %v", got, tt.wantConnect)
			}
			if !tt.wantConnect && !a.fileDialog().Visible() {
				t.Fatal("Ctrl+O did not open the file dialog")
			}
		})
	}
}
