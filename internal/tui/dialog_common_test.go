package tui

import (
	"context"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/dialogs"
	"github.com/radix29/tuikit/loop"
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
	var reg loop.Tasks
	task, ctx := reg.Start(context.Background(), "Backup")

	hidden := false
	runProgressButton(task, 1, func() { hidden = true })
	if ctx.Err() == nil {
		t.Error("button 1 on a running task did not cancel it")
	}
	if hidden {
		t.Error("button 1 hid the dialog; Cancel must leave the progress view up")
	}

	// Button 0 is Close, and closing a running task leaves it running — the
	// status bar and Tasks dialog keep tracking it.
	task, ctx = reg.Start(context.Background(), "Backup")
	hidden = false
	runProgressButton(task, 0, func() { hidden = true })
	if ctx.Err() != nil {
		t.Error("button 0 cancelled the task; Close must leave it running")
	}
	if !hidden {
		t.Error("button 0 did not hide the dialog")
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
	// Backtab from the first field: Tab past the last one would count the
	// greyed controls RingStep passes over.
	key(b, tcell.KeyBacktab)
	if !b.onButtons() || b.btnFocus != 0 {
		t.Fatalf("Backup: Backtab from the first field: onButtons=%v btnFocus=%d", b.onButtons(), b.btnFocus)
	}
	key(b, tcell.KeyRight)
	key(b, tcell.KeyRight)
	if b.btnFocus != 2 {
		t.Errorf("Backup: Right twice reached button %d, want Validate (2)", b.btnFocus)
	}
	key(b, tcell.KeyBacktab)
	if want := dialogs.RingStep(b.focusable, len(b.focusable), -1); b.focusIdx != want || b.btnFocus != 0 {
		t.Errorf("Backup: Backtab off the row: focusIdx=%d (want the last enabled field, %d) btnFocus=%d", b.focusIdx, want, b.btnFocus)
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
	if !r.onButtons() {
		t.Fatal("Restore: Backtab from the first field did not reach the button row")
	}
	if r.FocusedClipboardTarget() != nil {
		t.Error("Restore: a field still answers Copy/Paste while the buttons have focus")
	}
	// Files needs an analysed backup to open on; this is where it lands.
	r.enterFilesMode()
	if r.onFilesButtons() {
		t.Fatal("Restore: the Files view opened with focus on its button row")
	}
	// The Files view's own row, then Back to the form, into its fields.
	key(r, tcell.KeyBacktab)
	if !r.onFilesButtons() {
		t.Fatal("Restore Files: Backtab from the first field did not reach the button row")
	}
	key(r, tcell.KeyRight) // Back
	key(r, tcell.KeyEnter)
	if r.mode != restoreModeForm || r.onButtons() {
		t.Errorf("Restore: Back from the Files row: mode=%d onButtons=%v", r.mode, r.onButtons())
	}
}
