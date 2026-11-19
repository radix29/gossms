package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/dialogs"
)

// dialog_common.go holds the goSSMS-specific behaviours shared by the
// hand-rolled dialogs — the ones that lay out widgets directly and drive focus
// with a flat slice, rather than delegating to propsheet.Form's rows: Connect,
// Backup, Restore, Find/Replace and Log Search. The generic operations over
// that focus ring (focus and scroll movement, the button-row keyboard, the
// clipboard hit test, release forwarding) are tuikit's, in
// dialogs/focus_ring.go; what stays here needs a *Task or the App — the
// Backup/Restore progress button and the discard-changes confirmation.

// focusable is a dialog's focus-ring entry: any tuikit control that takes
// keyboard focus. Ask a ring entry for more through core's other control
// interfaces (core.Enabler, core.KeyHandler), not a switch over widget types.
type focusable = core.Focusable

// runProgressButton is the Close/Cancel button pair on the Backup and Restore
// progress views. A finished or absent task leaves Close as the only outcome
// whichever button has focus, so a run that completed between the draw and the
// keypress cannot cancel anything.
func runProgressButton(task *Task, btnFocus int, hide func()) {
	if task == nil || task.Done {
		hide()
		return
	}
	switch btnFocus {
	case 0:
		hide()
	case 1:
		task.Cancel()
	}
}

// progressModeKey is the whole keyboard of the Backup and Restore progress views:
// Escape hides, Enter fires the focused button, Tab and Backtab rotate between
// them.
//
// btnFocus is clamped on Enter rather than on every rotation, because the button
// list shrinks under the view — Cancel disappears the moment the task finishes —
// and a focus index recorded against the longer list would index past the shorter.
//
// It always reports handled: a progress view is modal over its dialog, and a key
// falling through to the form underneath would edit a page the user cannot see.
func progressModeKey(ev *tcell.EventKey, btnFocus *int, buttons []string, hide, fire func()) bool {
	switch ev.Key() {
	case tcell.KeyEscape:
		hide()
	case tcell.KeyEnter:
		*btnFocus = min(*btnFocus, len(buttons)-1)
		fire()
	case tcell.KeyTab:
		*btnFocus = dialogs.NextFocus(*btnFocus, len(buttons))
	case tcell.KeyBacktab:
		*btnFocus = dialogs.PrevFocus(*btnFocus, len(buttons))
	}
	return true
}

// confirmDiscardChanges asks before throwing away a property page's unsaved
// edits, running proceed only if the user agrees. Shared by PropDialog and
// newObjectDialog, which pass it to propsheet as the same callback.
func (a *App) confirmDiscardChanges(proceed func()) {
	a.confirmDialog().ShowConfirm("Discard Changes",
		"This page has unsaved changes. Discard them and refresh from the server?",
		func(confirmed bool) {
			if confirmed {
				proceed()
			}
		})
}
