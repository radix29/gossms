package tui

import "github.com/radix29/tuikit/dialogs"

// TasksDialog (Tools > Background Tasks) lists every task in the App's
// registry — running or finished — and lets the user cancel the selected
// one if it's still running. Unlike QueryListDialog, it doesn't snapshot
// its list on Show(): it reads a.tasks.All() directly on every Draw, so progress
// updates delivered by tasks.Progress while the dialog is open show up
// immediately without needing to be re-shown. A row click only selects:
// cancelling must be asked for.
type TasksDialog struct {
	*dialogs.ListDialog
	app *App
}

// NewTasksDialog creates the dialog.
func NewTasksDialog(app *App) *TasksDialog {
	d := &TasksDialog{app: app}
	d.ListDialog = dialogs.NewListDialog(dialogs.ListDialogSpec{
		Title: "Background Tasks", W: 64, H: 16,
		Buttons:  []string{"Cancel Task", "Close"},
		Empty:    "(no background tasks)",
		Count:    func() int { return len(app.tasks.All()) },
		Item:     func(i int) string { return taskStatusText(app.tasks.All()[i]) },
		OnButton: func(_, sel int) bool { d.cancel(sel); return false },
	})
	return d
}

// cancel cancels task sel, if any and still running — Task.Cancel is a safe
// no-op on one that's already finished.
func (d *TasksDialog) cancel(sel int) {
	if sel < 0 || sel >= len(d.app.tasks.All()) {
		return
	}
	d.app.tasks.All()[sel].Cancel()
}
