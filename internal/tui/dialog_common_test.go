package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/tuikittest"
)

// A finished task leaves Close as the only outcome whichever button has focus.
// Cancel on a task that completed between the draw and the keypress would
// otherwise cancel nothing while looking like it did something.
func TestRunProgressButtonHidesOnAFinishedOrAbsentTask(t *testing.T) {
	for _, tt := range []struct {
		name string
		task *Task
	}{
		{"no task", nil},
		{"finished task", &Task{Done: true}},
	} {
		for _, btn := range []int{0, 1} {
			hidden := false
			runProgressButton(tt.task, btn, func() { hidden = true })
			if !hidden {
				t.Errorf("%s, button %d: did not hide", tt.name, btn)
			}
		}
	}
}

func TestRunProgressButtonCancelsARunningTask(t *testing.T) {
	cancelled := false
	task := &Task{cancel: func() { cancelled = true }}

	hidden := false
	runProgressButton(task, 1, func() { hidden = true })
	if !cancelled {
		t.Error("button 1 on a running task did not cancel it")
	}
	if hidden {
		t.Error("button 1 hid the dialog; Cancel must leave the progress view up")
	}

	// Button 0 is Close, and closing a running task leaves it running — the
	// status bar and Tasks dialog keep tracking it.
	cancelled, hidden = false, false
	runProgressButton(task, 0, func() { hidden = true })
	if cancelled {
		t.Error("button 0 cancelled the task; Close must leave it running")
	}
	if !hidden {
		t.Error("button 0 did not hide the dialog")
	}
}

// TestProgressModeKeyClampsFocusToTheShorterButtonList.
//
// The Backup and Restore progress views lose their Cancel button the moment
// the task finishes, so a btnFocus of 1 recorded while it was running indexes
// past a one-button list. Enter is where that lands: it is the only key that
// reads the focused button rather than just moving between them, and an
// unclamped index there panics on a run that completed between the draw and
// the keypress — the ordinary way of pressing Enter on a backup that just
// finished.
func TestProgressModeKeyClampsFocusToTheShorterButtonList(t *testing.T) {
	btnFocus := 1
	fired := 0
	progressModeKey(key(tcell.KeyEnter), &btnFocus, []string{"Close"}, func() {}, func() { fired++ })

	if btnFocus != 0 {
		t.Errorf("btnFocus = %d after Enter on a one-button view, want 0", btnFocus)
	}
	if fired != 1 {
		t.Errorf("the button fired %d times, want 1", fired)
	}
}

// TestProgressModeKeyRotatesAndHides pins the rest of the progress view's
// keyboard, including that it swallows everything: the view is modal over the
// dialog's form, and a key falling through would edit rows the user cannot see.
func TestProgressModeKeyRotatesAndHides(t *testing.T) {
	buttons := []string{"Hide", "Cancel"}
	btnFocus := 0
	hidden := false
	hide := func() { hidden = true }
	fire := func() { t.Error("a rotation key fired the button") }

	if !progressModeKey(key(tcell.KeyTab), &btnFocus, buttons, hide, fire) {
		t.Error("Tab was not handled")
	}
	if btnFocus != 1 {
		t.Errorf("Tab left btnFocus at %d, want 1", btnFocus)
	}

	// F1 is Help everywhere else, so it is not a second Tab here: swallowed,
	// like any unbound key, and the focus stays put.
	btnFocus = 0
	if !progressModeKey(key(tcell.KeyF1), &btnFocus, buttons, hide, fire) {
		t.Error("F1 fell through to the form under the progress view")
	}
	if btnFocus != 0 {
		t.Errorf("F1 moved btnFocus to %d; it is not a rotation key", btnFocus)
	}

	// Three buttons, not this view's two: with two, forward and backward from
	// the first land on the same index, so a Backtab wired to nextFocus would
	// pass. Nothing in the dialogs has three, but the wiring is what is under
	// test.
	btnFocus = 0
	progressModeKey(key(tcell.KeyBacktab), &btnFocus, []string{"a", "b", "c"}, hide, fire)
	if btnFocus != 2 {
		t.Errorf("Backtab from the first button left btnFocus at %d, want the last (2)", btnFocus)
	}

	if !progressModeKey(key(tcell.KeyEscape), &btnFocus, buttons, hide, fire) {
		t.Error("Escape was not handled")
	}
	if !hidden {
		t.Error("Escape did not hide the dialog")
	}

	// An unbound key is still swallowed.
	if !progressModeKey(key(tcell.KeyLeft), &btnFocus, buttons, hide, fire) {
		t.Error("an unbound key fell through to the form under the progress view")
	}
}

func key(k tcell.Key) *tcell.EventKey { return tcell.NewEventKey(k, "", tcell.ModNone) }

// Backup's and Restore's button rows are a Tab stop past either end of the
// form's ring, as Connect's is (TestConnectTabReachesTheButtonRow): F1 used to
// be the only keyboard way to reach Script, Validate or OK.
func TestBackupAndRestoreTabReachTheButtonRow(t *testing.T) {
	quietLog(t)
	key := func(h interface{ HandleKey(*tcell.EventKey) bool }, k tcell.Key) {
		h.HandleKey(tcell.NewEventKey(k, "", tcell.ModNone))
	}

	a := newTestApp()
	a.setScreen(tuikittest.NewScreen(100, 40))
	b := withScreen(NewBackupDialog(a), a.screen)
	a.setScreen(nil)
	b.show(&db.ServerConn{}, "testdb")
	for range b.focusable {
		key(b, tcell.KeyTab)
	}
	if !b.onButtons || b.btnFocus != 0 {
		t.Fatalf("Backup: Tab past the last field: onButtons=%v btnFocus=%d", b.onButtons, b.btnFocus)
	}
	key(b, tcell.KeyRight)
	key(b, tcell.KeyRight)
	if b.btnFocus != 2 {
		t.Errorf("Backup: Right twice reached button %d, want Validate (2)", b.btnFocus)
	}
	key(b, tcell.KeyBacktab)
	if b.onButtons || b.focusIdx != len(b.focusable)-1 || b.btnFocus != 0 {
		t.Errorf("Backup: Backtab off the row: onButtons=%v focusIdx=%d btnFocus=%d", b.onButtons, b.focusIdx, b.btnFocus)
	}
	key(b, tcell.KeyTab)
	key(b, tcell.KeyRight)
	key(b, tcell.KeyRight)
	key(b, tcell.KeyRight)
	key(b, tcell.KeyEnter) // Cancel
	if b.Visible() {
		t.Error("Backup: Enter on Cancel did not close the dialog")
	}

	a = newTestApp()
	a.setScreen(tuikittest.NewScreen(100, 40))
	r := withScreen(NewRestoreDialog(a), a.screen)
	a.setScreen(nil)
	r.show(&db.ServerConn{}, "testdb")
	key(r, tcell.KeyBacktab)
	if !r.onButtons {
		t.Fatal("Restore: Backtab from the first field did not reach the button row")
	}
	if r.FocusedClipboardTarget() != nil {
		t.Error("Restore: a field still answers Copy/Paste while the buttons have focus")
	}
	// Files needs an analysed backup to open on; this is where it lands.
	r.enterFilesMode()
	if r.onButtons {
		t.Fatal("Restore: the Files view opened with focus still on a button row")
	}
	// The Files view's own row, then Back to the form, into its fields.
	key(r, tcell.KeyBacktab)
	if !r.onButtons {
		t.Fatal("Restore Files: Backtab from the first field did not reach the button row")
	}
	key(r, tcell.KeyRight) // Back
	key(r, tcell.KeyEnter)
	if r.mode != restoreModeForm || r.onButtons {
		t.Errorf("Restore: Back from the Files row: mode=%d onButtons=%v", r.mode, r.onButtons)
	}
}
