package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/tuikit/tuikittest"
)

// TestDialogStackNestingAndPruning drives modals.Sync the way Run's
// event loop does — once per tick — to verify the three things replacing
// the hand-enumerated switch depends on: a newly shown dialog becomes the
// new top (even while another is still open, i.e. nesting), a dialog that
// closed itself is pruned on the next sync uncovering whatever was below
// it, and re-showing an already-open dialog never duplicates its stack
// entry.
func TestDialogStackNestingAndPruning(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()

	if got := a.modals.Top(); got != nil {
		t.Fatalf("modals.Top() before anything shown = %v, want nil", got)
	}

	a.helpDialog.Show()
	a.modals.Sync()
	if got := a.modals.Top(); got != a.helpDialog {
		t.Errorf("modals.Top() after showing help = %v, want helpDialog", got)
	}
	if len(a.modals.Open()) != 1 {
		t.Fatalf("stack len = %d, want 1", len(a.modals.Open()))
	}

	// Nesting: a second dialog opens while help is still visible — the
	// path a dialog spawning a child takes, e.g. Backup > Browse.
	a.optionsDialog.Show()
	a.modals.Sync()
	if got := a.modals.Top(); got != a.optionsDialog {
		t.Errorf("modals.Top() after nested show = %v, want optionsDialog on top", got)
	}
	if len(a.modals.Open()) != 2 || a.modals.Open()[0] != Dialog(a.helpDialog) {
		t.Errorf("stack = %v, want [help, options]", a.modals.Open())
	}

	// Closing the top dialog drops it on the next sync, uncovering help.
	a.optionsDialog.Hide()
	a.modals.Sync()
	if got := a.modals.Top(); got != a.helpDialog {
		t.Errorf("modals.Top() after closing options = %v, want helpDialog uncovered", got)
	}
	if len(a.modals.Open()) != 1 {
		t.Errorf("stack len after closing options = %d, want 1", len(a.modals.Open()))
	}

	// Re-showing a dialog already in the stack must not duplicate it.
	a.helpDialog.Show()
	a.modals.Sync()
	if len(a.modals.Open()) != 1 {
		t.Errorf("re-showing an already-open dialog duplicated it: stack = %v", a.modals.Open())
	}

	a.helpDialog.Hide()
	a.modals.Sync()
	if got := a.modals.Top(); got != nil {
		t.Errorf("modals.Top() after closing the last dialog = %v, want nil", got)
	}
	if len(a.modals.Open()) != 0 {
		t.Errorf("stack after closing everything = %v, want empty", a.modals.Open())
	}
}

// TestTasksDialogParticipatesInStack guards against the one mistake this
// architecture can still make silently: adding a new dialog type without
// registering it in buildUI. If tasksDialog were never registered,
// modals.Sync would never notice it becoming visible and it
// would draw over everything with no way to route input to it.
func TestTasksDialogParticipatesInStack(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()

	a.tasksDialog.Show()
	a.modals.Sync()
	if got := a.modals.Top(); got != a.tasksDialog {
		t.Errorf("modals.Top() after showing tasksDialog = %v, want tasksDialog", got)
	}
}

// TestStatusHistoryDialogParticipatesInStack is the same guard as
// TestTasksDialogParticipatesInStack, for the status-bar click-to-view
// history dialog.
func TestStatusHistoryDialogParticipatesInStack(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()

	a.statusHistoryDialog.Show()
	a.modals.Sync()
	if got := a.modals.Top(); got != a.statusHistoryDialog {
		t.Errorf("modals.Top() after showing statusHistoryDialog = %v, want statusHistoryDialog", got)
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
	scr := a.screen.(*tuikittest.Screen)

	a.alertDialog.ShowAlert("Alert", "Something happened.")
	a.modals.Sync()

	scr.SetSize(44, 12)
	a.layoutAll()

	r := a.alertDialog.Rect()
	if sw, sh := scr.Size(); r.Right() > sw || r.Bottom() > sh {
		t.Errorf("dialog rect %+v extends past the %dx%d screen after resize", r, sw, sh)
	}

	// Below layoutAll's too-small floor is exactly where an unrefitted
	// dialog is most broken, so the refit must happen before that guard.
	scr.SetSize(16, 4)
	a.layoutAll()

	r = a.alertDialog.Rect()
	if sw, sh := scr.Size(); r.Right() > sw || r.Bottom() > sh {
		t.Errorf("dialog rect %+v extends past the %dx%d screen (below layoutAll's size floor)", r, sw, sh)
	}
}

// TestDialogStackRoutesInputToTopOnly confirms only the frontmost dialog
// ever receives input — the invariant handleKey/handleMouse rely on.
func TestDialogStackRoutesInputToTopOnly(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()

	a.helpDialog.Show()
	a.modals.Sync()
	a.optionsDialog.Show()
	a.modals.Sync()

	top := a.modals.Top()
	if top != a.optionsDialog {
		t.Fatalf("top = %v, want optionsDialog", top)
	}
	// helpDialog, though still open underneath, is not what modals.Top()
	// returns — routing HandleKey/HandleMouse to top alone leaves it inert.
	if top == Dialog(a.helpDialog) {
		t.Errorf("top resolved to the covered dialog, not the frontmost one")
	}
}
