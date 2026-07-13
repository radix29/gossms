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

	// Nesting: a second dialog opens while help is still visible (today
	// this can't happen through normal input, since the visible dialog
	// swallows all input — but a future dialog spawning a child, e.g.
	// Backup > Browse, would hit exactly this path).
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
// remembering to append it to allDialogs in buildUI. If tasksDialog were
// missing from that list, syncDialogStack would never notice it becoming
// visible and it would draw over everything with no way to route input to
// it — exactly the bug the dialog stack replaced hand-enumeration to avoid.
func TestTasksDialogParticipatesInStack(t *testing.T) {
	a := &App{cfg: &config.Config{}}
	a.buildUI()

	a.tasksDialog.Show()
	a.syncDialogStack()
	if got := a.topDialog(); got != a.tasksDialog {
		t.Errorf("topDialog after showing tasksDialog = %v, want tasksDialog", got)
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
