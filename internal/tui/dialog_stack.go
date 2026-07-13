package tui

import (
	"slices"

	"github.com/gdamore/tcell/v3"
)

// Dialog is the shape every modal dialog already has, by embedding
// tuikit/dialogs.ModalDialog (directly, or via dialogs.PropertiesDialog):
// Visible/Draw/HandleKey/HandleMouse. App tracks which dialogs are open as
// a z-ordered stack of these instead of hand-enumerating every dialog type
// at each place that needs to know "what's on top" — adding a new dialog
// costs one line in buildUI's allDialogs list; draw order and input
// routing then fall out of the stack automatically.
type Dialog interface {
	Visible() bool
	Draw(s tcell.Screen)
	HandleKey(ev *tcell.EventKey) bool
	HandleMouse(ev *tcell.EventMouse) bool
}

// syncDialogStack reconciles dialogStack with reality: every dialog closes
// itself (Escape, a Close button, ShowXxx swapping content on an already-
// open PropertiesDialog, ...) by flipping its own Visible() to false, with
// no way to tell the stack directly, so any stack entry that's no longer
// visible is dropped here. Conversely, any dialog that became visible
// since the last sync — which today is every Show()/Prompt()/ShowXxx()
// call, none of which know about the stack either — is appended, becoming
// the new top. Called twice per event-loop iteration (see Run): after
// draining posted callbacks so input routing sees their changes, and again
// after handling the event so a dialog it opened or closed is drawn that
// same frame rather than one event later.
func (a *App) syncDialogStack() {
	a.dialogStack = slices.DeleteFunc(a.dialogStack, func(d Dialog) bool { return !d.Visible() })
	for _, d := range a.allDialogs {
		if d.Visible() && !slices.Contains(a.dialogStack, d) {
			a.dialogStack = append(a.dialogStack, d)
		}
	}
}

// topDialog returns the frontmost open dialog, or nil if none is open.
// It alone gets keyboard/mouse input — a nested dialog (opened from
// within another, once one exists) leaves its parent inert underneath it.
func (a *App) topDialog() Dialog {
	if len(a.dialogStack) == 0 {
		return nil
	}
	return a.dialogStack[len(a.dialogStack)-1]
}

// drawDialogs renders every open dialog bottom-to-top, so a nested dialog
// paints over its parent rather than the reverse.
func (a *App) drawDialogs(s tcell.Screen) {
	for _, d := range a.dialogStack {
		d.Draw(s)
	}
}
