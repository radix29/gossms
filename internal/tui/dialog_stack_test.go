package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// TestDialogStackNestingAndPruning drives syncDialogStack the way Run's
// event loop does — once per tick — to verify the three things replacing
// the hand-enumerated switch depends on: a newly shown dialog becomes the
// new top (even while another is still open, i.e. nesting), a dialog that
// closed itself is pruned on the next sync uncovering whatever was below
// it, and re-showing an already-open dialog never duplicates its stack
// entry.
func TestDialogStackNestingAndPruning(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()

	if got := a.topDialog(); got != nil {
		t.Fatalf("topDialog before anything shown = %v, want nil", got)
	}

	a.helpDialog.Show()
	a.syncDialogStack()
	if got := a.topDialog(); got != a.helpDialog {
		t.Errorf("topDialog after showing help = %v, want helpDialog", got)
	}
	if len(a.dialogStack) != 1 {
		t.Fatalf("stack len = %d, want 1", len(a.dialogStack))
	}

	// Nesting: a second dialog opens while help is still visible — the
	// path a dialog spawning a child takes, e.g. Backup > Browse.
	a.optionsDialog.Show()
	a.syncDialogStack()
	if got := a.topDialog(); got != a.optionsDialog {
		t.Errorf("topDialog after nested show = %v, want optionsDialog on top", got)
	}
	if len(a.dialogStack) != 2 || a.dialogStack[0] != Dialog(a.helpDialog) {
		t.Errorf("stack = %v, want [help, options]", a.dialogStack)
	}

	// Closing the top dialog drops it on the next sync, uncovering help.
	a.optionsDialog.Hide()
	a.syncDialogStack()
	if got := a.topDialog(); got != a.helpDialog {
		t.Errorf("topDialog after closing options = %v, want helpDialog uncovered", got)
	}
	if len(a.dialogStack) != 1 {
		t.Errorf("stack len after closing options = %d, want 1", len(a.dialogStack))
	}

	// Re-showing a dialog already in the stack must not duplicate it.
	a.helpDialog.Show()
	a.syncDialogStack()
	if len(a.dialogStack) != 1 {
		t.Errorf("re-showing an already-open dialog duplicated it: stack = %v", a.dialogStack)
	}

	a.helpDialog.Hide()
	a.syncDialogStack()
	if got := a.topDialog(); got != nil {
		t.Errorf("topDialog after closing the last dialog = %v, want nil", got)
	}
	if len(a.dialogStack) != 0 {
		t.Errorf("stack after closing everything = %v, want empty", a.dialogStack)
	}
}

// TestTasksDialogParticipatesInStack guards against the one mistake this
// architecture can still make silently: adding a new dialog type without
// appending it to allDialogs in buildUI. If tasksDialog were missing from
// that list, syncDialogStack would never notice it becoming visible and it
// would draw over everything with no way to route input to it.
func TestTasksDialogParticipatesInStack(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()

	a.tasksDialog.Show()
	a.syncDialogStack()
	if got := a.topDialog(); got != a.tasksDialog {
		t.Errorf("topDialog after showing tasksDialog = %v, want tasksDialog", got)
	}
}

// TestStatusHistoryDialogParticipatesInStack is the same guard as
// TestTasksDialogParticipatesInStack, for the status-bar click-to-view
// history dialog.
func TestStatusHistoryDialogParticipatesInStack(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()

	a.statusHistoryDialog.Show()
	a.syncDialogStack()
	if got := a.topDialog(); got != a.statusHistoryDialog {
		t.Errorf("topDialog after showing statusHistoryDialog = %v, want statusHistoryDialog", got)
	}
}

// A dialog is not part of the layout tree — it centres itself on the screen
// — so layoutAll's SetBounds calls reach none of them, and an open dialog
// used to keep the rect it was centred into before the resize: its right
// border and its entire button row landed off-screen while it went on
// swallowing every key. On a small enough terminal it drew nothing at all
// and the app looked hung.
func TestLayoutAllRefitsOpenDialogsToTheNewScreenSize(t *testing.T) {
	a := newLatchTestApp()
	scr := a.screen.(*fakeSizedScreen)

	a.alertDialog.ShowAlert("Alert", "Something happened.")
	a.syncDialogStack()

	scr.w, scr.h = 44, 12
	a.layoutAll()

	r := a.alertDialog.Rect()
	if r.Right() > scr.w || r.Bottom() > scr.h {
		t.Errorf("dialog rect %+v extends past the %dx%d screen after resize", r, scr.w, scr.h)
	}

	// Below layoutAll's too-small floor is exactly where an unrefitted
	// dialog is most broken, so the refit must happen before that guard.
	scr.w, scr.h = 16, 4
	a.layoutAll()

	r = a.alertDialog.Rect()
	if r.Right() > scr.w || r.Bottom() > scr.h {
		t.Errorf("dialog rect %+v extends past the %dx%d screen (below layoutAll's size floor)", r, scr.w, scr.h)
	}
}

// TestDialogStackRoutesInputToTopOnly confirms only the frontmost dialog
// ever receives input — the invariant handleKey/handleMouse rely on.
func TestDialogStackRoutesInputToTopOnly(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()

	a.helpDialog.Show()
	a.syncDialogStack()
	a.optionsDialog.Show()
	a.syncDialogStack()

	top := a.topDialog()
	if top != a.optionsDialog {
		t.Fatalf("top = %v, want optionsDialog", top)
	}
	// helpDialog, though still open underneath, is not what topDialog
	// returns — routing HandleKey/HandleMouse to top alone leaves it inert.
	if top == Dialog(a.helpDialog) {
		t.Errorf("top resolved to the covered dialog, not the frontmost one")
	}
}
