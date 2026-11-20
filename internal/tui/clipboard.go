package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/clipboard"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/core"
)

// clipboardTarget is any widget that can take part in Copy/Cut/Paste: an alias
// for core.ClipboardTarget, which lives there so tuikit's dialogs can hand one
// back (see core.ClipboardHost). widgets.InputField, editor.Editor,
// controls.DataGrid and propsheet.PropertySheet satisfy it structurally.
type clipboardTarget = core.ClipboardTarget

// clip is the App's clipboard.Dispatcher, built on first use (on the UI
// goroutine, as every caller is) so an App a test builds as a bare literal has
// one too. It moves text between activeClipboardTarget and the system
// clipboard: newest copy wins, OSC 52 fallback, a paste aimed once and dropped
// if its target changed before the text arrived, bracketed paste as one edit
// (see tuikit's clipboard.Dispatcher for the bugs each part names).
func (a *App) clip() *clipboard.Dispatcher {
	if a.clipDispatcher == nil {
		a.clipDispatcher = &clipboard.Dispatcher{
			Screen: func() tcell.Screen { return a.screen },
			// The explicit nil: activeClipboardTarget's nil interface converts to a
			// nil clipboard.Target, which Paste and Deliver test for.
			Resolve: func() clipboard.Target {
				if t := a.activeClipboardTarget(); t != nil {
					return t
				}
				return nil
			},
			Token: func() any { return clipboardTargetToken(a.modals.Top()) },
			Post:  a.postAndWake,
			Go:    a.safego,
			// Every Target comes from Resolve, so it is a clipboardTarget.
			Pasted: func(t clipboard.Target) { a.notifyClipboardEdit(t.(clipboardTarget)) },
			// OSC 52 has no acknowledgement, so the caller's "Copied to clipboard"
			// may be false: VTE (xfce4-terminal, GNOME Terminal) ignores OSC 52
			// writes. Overwrites the caller's status, which it set synchronously,
			// before this ran.
			Unconfirmed: func() { a.setStatus("Copied via terminal (may be unsupported)") },
			NativeWrite: func(text string) bool { return clipboardWriter(text) },
		}
	}
	return a.clipDispatcher
}

// activeClipboardTarget resolves which widget Copy/Cut/Paste acts on now: the
// focused field in the frontmost open dialog; the active query panel's editor,
// or whichever results view has the keyboard (or its results grid while its
// "Show Value" viewer is open); the Always On dashboard's focused grid; or what
// any other panel names as a core.ClipboardHost. nil when nothing focused can
// participate.
func (a *App) activeClipboardTarget() clipboardTarget {
	// An open dialog owns the clipboard outright: its focused field, or nothing.
	// Never fall past it to a panel underneath, or Ctrl+X with the Find dialog open
	// would cut the query editor's selection behind it, reported as "Cut to
	// clipboard". A dialog that isn't a ClipboardHost has no text, so nil.
	if target, owned := a.modals.ClipboardTarget(); owned {
		return target
	}
	if qp := a.activeQueryPanel(); qp != nil {
		// Every view below shares the results half of the panel, so which one shows
		// decides the target only when that half holds keyboard focus. Without the
		// resultsHasFocus() gate, an execution leaving the pane on Messages, the plan
		// or Results To Text redirects Ctrl+V from the SQL editor to a read-only view
		// whose Paste is a no-op.
		if qp.resultsHasFocus() {
			// The Messages tab's read-only text view, while showing, takes priority; see
			// QueryPanel.onMessagesTab.
			if qp.onMessagesTab() {
				return qp.messages
			}
			// Then the Execution Plan view, while showing; see QueryPanel.planTabActive.
			if qp.planTabActive() {
				return qp.planView
			}
			// Then Results To Text's read-only view, while showing; see
			// QueryPanel.textTabActive.
			if qp.textTabActive() {
				return qp.resultsText
			}
			return qp.results
		}
		// The results grid's "Show Value" viewer is a modal overlay: it keeps its own
		// selection while open whichever half is focused, so it wins over the SQL
		// editor. controls.DataGrid.HasSelection is true only while it shows.
		if qp.results.HasSelection() {
			return qp.results
		}
		return qp.editor
	}
	switch p := a.panels.ActivePanel().(type) {
	case *AGDashboard:
		// The Always On dashboard is two grids and nothing else, so whichever has the
		// keyboard is the target; copySelection's DataGrid branch then copies the cell
		// under the cursor even with no viewer open.
		return p.grid()
	case core.ClipboardHost:
		// A panel names its focused copyable widget, or an explicit nil: the Log
		// Viewer's filter box, Plan and Live Plan's PlanView, the focused grid of
		// Plan Compare, Query Store and Replication Monitor, and the Detail
		// Browser's grid while its viewer is open. Without this the panels'
		// clipboard was never reached and Ctrl+C there did nothing.
		return p.FocusedClipboardTarget()
	}
	return nil
}

// gridClipboardTarget is g as a clipboard target, nil for a nil grid. The
// explicit nil is load-bearing: a nil *DataGrid in the interface is not a nil
// interface, and activeClipboardTarget's callers test against nil before
// calling through it.
func gridClipboardTarget(g *controls.DataGrid) clipboardTarget {
	if g == nil {
		return nil
	}
	return g
}

// copySelection runs Copy (Ctrl+C / Edit > Copy): if the resolved target
// has a selection, its text is sent to the clipboard.
func (a *App) copySelection() {
	target := a.activeClipboardTarget()
	if target == nil {
		return
	}
	// A focused results grid has no "selection" in the clipboardTarget sense unless
	// its content viewer is open, but always has a cell or block under its cursor:
	// copy that, matching its right-click "Copy".
	if g, ok := target.(*controls.DataGrid); ok && !g.HasSelection() {
		// A grid with no rows has no cell under its cursor: leave the clipboard alone.
		if g.SelectedRowCount() > 0 {
			a.copyWithStatus(g.SelectedCellsText())
		}
		return
	}
	if !target.HasSelection() {
		return
	}
	// A selection can still yield no text: a password field refuses to hand over
	// its plaintext. Leave the clipboard as it was rather than overwrite it with "".
	text := target.SelectedText()
	if text == "" {
		return
	}
	a.writeClipboard(text)
	a.setStatus("Copied to clipboard")
}

// cutSelection runs Cut (Ctrl+X / Edit > Cut): like copySelection, but
// also removes the selected text from the target.
func (a *App) cutSelection() {
	target := a.activeClipboardTarget()
	if target == nil || !target.HasSelection() {
		return
	}
	// "" means nothing was cut (a password field refuses Cut): keep the clipboard,
	// report nothing, and tell no dialog its field changed.
	text := target.Cut()
	if text == "" {
		return
	}
	a.writeClipboard(text)
	a.setStatus("Cut to clipboard")
	a.notifyClipboardEdit(target)
}

// notifyClipboardEdit tells the frontmost dialog that a Cut or Paste changed
// one of its fields, so it can follow up as after a keystroke (see
// core.ClipboardEditHandler). The dialog checks which target it was handed
// rather than assuming the edit landed in the field it watches.
func (a *App) notifyClipboardEdit(target clipboardTarget) {
	a.modals.ClipboardEdited(target)
}

// writeClipboard sends text to the clipboard: the native OS clipboard
// (xclip/xsel/wl-copy/pbcopy, the Win32 API on Windows) off the UI thread,
// falling back to OSC 52, the newest of several copies winning. See
// clipboard.Dispatcher.Copy.
func (a *App) writeClipboard(text string) { a.clip().Copy(text) }

// clipboardWriter is clipboard.Write, swapped by tests (read at each write, so
// a swap reaches a Dispatcher already built).
var clipboardWriter = clipboard.Write

// copyWithStatus is writeClipboard plus the status-line acknowledgement, for
// grid context menus whose "Copy" item is the whole interaction: nothing else
// on screen changes, so without the message there is no sign it worked.
func (a *App) copyWithStatus(text string) {
	a.writeClipboard(text)
	a.setStatus("Copied to clipboard")
}

// pasteFromClipboard runs Paste (Ctrl+V / Edit > Paste): the target is resolved
// once, now, and the text dropped unless that widget (and, through
// clipboardTargetToken, that field) is still the target when it arrives — a
// paste aimed at a closed dialog's field would otherwise land in the query
// editor behind it. See clipboard.Dispatcher.Paste and Deliver.
func (a *App) pasteFromClipboard() { a.clip().Paste() }

// clipboardTargetToken is the identity of the field a host has focus on, or nil
// from a host that doesn't distinguish its fields. Only propsheet.PropertySheet
// answers (returning itself from FocusedClipboardTarget, so nothing else can
// tell its rows apart). See core.ClipboardTargetTokener.
func clipboardTargetToken(top Dialog) any {
	if t, ok := top.(core.ClipboardTargetTokener); ok {
		return t.ClipboardTargetToken()
	}
	return nil
}

// selectAllInTarget runs Select All (Ctrl+A / Edit > Select All) on whichever
// widget Copy/Cut/Paste would act on.
func (a *App) selectAllInTarget() {
	if target := a.activeClipboardTarget(); target != nil {
		target.SelectAll()
	}
}

// showClipboardMenu pops up the standard right-click menu for a text field at
// (x,y) — Cut, Copy, Paste and Select All, or Copy and Select All on a read-only
// one (controls.ClipboardMenuItems) — wired to the same App-level actions as
// the shortcuts and the Edit menu, so each acts on activeClipboardTarget.
//
// target is the field right-clicked, passed rather than resolved: a query
// panel's editors fire OnRightClick from inside their HandleMouse, before the
// panel moves focus to them, so activeClipboardTarget still names the previous
// one then. The actions run later, once the focus move has landed.
func (a *App) showClipboardMenu(target clipboardTarget, x, y int) {
	if target == nil {
		return
	}
	a.contextMenu.Show(x, y, controls.ClipboardMenuItems(target, controls.ClipboardActions{
		Cut:       a.cutSelection,
		Copy:      a.copySelection,
		Paste:     a.pasteFromClipboard,
		SelectAll: a.selectAllInTarget,
	}))
}

// openDialogClipboardMenu is a right-click on a dialog's text field: the
// frontmost dialog focuses the field under the pointer (core.ClipboardFocuser)
// and the field's Cut/Copy/Paste menu opens there. One route for every dialog,
// so no field needs its own right-click handler. False when no dialog is open,
// it has no field there, or it isn't a focuser: the click then routes as usual
// (Connect's connection string preview opens its own menu that way).
func (a *App) openDialogClipboardMenu(x, y int) bool {
	f, ok := a.modals.Top().(core.ClipboardFocuser)
	if !ok || !f.FocusClipboardTargetAt(x, y) {
		return false
	}
	target, _ := a.modals.ClipboardTarget()
	a.showClipboardMenu(target, x, y)
	return true
}
