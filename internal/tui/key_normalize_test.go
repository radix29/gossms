package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
)

// Ctrl+Shift+<letter> never reaches the app as KeyCtrlX: tcell folds a
// Ctrl-modified rune into KeyCtrlA..KeyCtrlZ only when Ctrl is the sole
// modifier. The two Ctrl+Shift+O forms below are what Kitty (CSI 111;6u,
// base-layout rune) and xterm modifyOtherKeys (CSI 27;6;79~, shifted rune)
// actually deliver — both verified against a running binary.
func TestNormalizeCtrlRune(t *testing.T) {
	tests := []struct {
		name    string
		in      *tcell.EventKey
		wantKey tcell.Key
		wantMod tcell.ModMask
	}{
		{"kitty ctrl+shift+o", tcell.NewEventKey(tcell.KeyRune, "o", tcell.ModCtrl|tcell.ModShift),
			tcell.KeyCtrlO, tcell.ModCtrl | tcell.ModShift},
		{"modifyOtherKeys ctrl+shift+O", tcell.NewEventKey(tcell.KeyRune, "O", tcell.ModCtrl|tcell.ModShift),
			tcell.KeyCtrlO, tcell.ModCtrl | tcell.ModShift},
		{"ctrl+shift+u", tcell.NewEventKey(tcell.KeyRune, "u", tcell.ModCtrl|tcell.ModShift),
			tcell.KeyCtrlU, tcell.ModCtrl | tcell.ModShift},
		{"already folded ctrl+o", tcell.NewEventKey(tcell.KeyCtrlO, "", tcell.ModCtrl),
			tcell.KeyCtrlO, tcell.ModCtrl},
		// AltGr arrives as Ctrl+Alt on many layouts and produces text.
		{"altgr letter stays a rune", tcell.NewEventKey(tcell.KeyRune, "o", tcell.ModCtrl|tcell.ModAlt),
			tcell.KeyRune, tcell.ModCtrl | tcell.ModAlt},
		{"ctrl+shift+digit stays a rune", tcell.NewEventKey(tcell.KeyRune, "1", tcell.ModCtrl|tcell.ModShift),
			tcell.KeyRune, tcell.ModCtrl | tcell.ModShift},
		{"plain typing stays a rune", tcell.NewEventKey(tcell.KeyRune, "o", tcell.ModNone),
			tcell.KeyRune, tcell.ModNone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeCtrlRune(tt.in)
			if got.Key() != tt.wantKey || got.Modifiers() != tt.wantMod {
				t.Fatalf("normalizeCtrlRune = Key=%d Mod=%d, want Key=%d Mod=%d",
					got.Key(), got.Modifiers(), tt.wantKey, tt.wantMod)
			}
		})
	}
}

// newKeyTestApp is newTestApp plus the dialogs handleKey touches.
func newKeyTestApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp()
	a.keyDiagDialog = NewKeyDiagnosticsDialog(a)
	a.connectDialog = NewConnectDialog(a)
	a.fileDialog = dialogs.NewFileDialog(nil)
	a.helpDialog = NewHelpDialog(a)
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
			if got := a.connectDialog.Visible(); got != tt.wantConnect {
				t.Fatalf("connect dialog visible = %v, want %v", got, tt.wantConnect)
			}
			if !tt.wantConnect && !a.fileDialog.Visible() {
				t.Fatal("Ctrl+O did not open the file dialog")
			}
		})
	}
}
