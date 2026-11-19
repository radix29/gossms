package tui

import (
	"github.com/radix29/tuikit/clipboard"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/core"
)

// clipboardTarget is any widget that can take part in Copy/Cut/Paste: an alias
// for core.ClipboardTarget, which lives there so tuikit's dialogs can hand one
// back (see core.ClipboardHost). widgets.InputField, editor.Editor,
// controls.DataGrid and propsheet.PropertySheet satisfy it structurally.
type clipboardTarget = core.ClipboardTarget

// appPaste is the state of a paste still arriving, by either route.
type appPaste struct {
	// Bracketed paste: Active between an *tcell.EventPaste start and its matching
	// end, during which every EventKey is pasted content (see Run).
	bracket clipboard.PasteBuffer

	// target is the widget a Ctrl+V was aimed at while the terminal's OSC 52
	// clipboard reply is outstanding (the fallback when no native tool answered).
	// The reply arrives as an *tcell.EventClipboard an unbounded time later;
	// App.pasteInto says why the target is remembered rather than re-resolved.
	target clipboardTarget

	// token pins which field of target the Ctrl+V was aimed at, for a host that
	// hands back itself rather than the field; see core.ClipboardTargetTokener and
	// App.pasteInto.
	token any
}

// activeClipboardTarget resolves which widget Copy/Cut/Paste acts on now: the
// focused field in the frontmost open dialog; the active query panel's editor,
// or whichever results view has the keyboard (or its results grid while its
// "Show Value" viewer is open); the Detail Browser's grid while its viewer is
// open; or the Always On dashboard's focused grid. nil when nothing focused can
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
	case *DetailBrowser:
		if p.HasSelection() {
			return p
		}
		return nil
	case *AGDashboard:
		// The Always On dashboard is two grids and nothing else, so whichever has the
		// keyboard is the target; copySelection's DataGrid branch then copies the cell
		// under the cursor even with no viewer open.
		return p.grid()
	case core.ClipboardHost:
		// A panel with a text field among other things (the Log Viewer's filter box)
		// names the focused one, or an explicit nil.
		return p.FocusedClipboardTarget()
	case clipboardTarget:
		// A panel that is its own target, forwarding to whatever it shows (Plan, Live
		// Plan, Plan Compare, Query Store, Replication Monitor). Without this arm their
		// clipboard methods were never reached and Ctrl+C there did nothing.
		return p
	}
	return nil
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

// writeClipboard sends text to the clipboard off the UI thread: the native OS
// clipboard (xclip/xsel/wl-copy/pbcopy, the Win32 API on Windows; see tuikit's
// clipboard.Write) first, falling back to tcell's OSC 52 terminal clipboard
// when no native tool handled it. The shell-out runs on a background goroutine
// so a stalled tool can't freeze the event loop; the OSC 52 fallback returns to the UI thread, since SetClipboard
// writes to the terminal.
//
// One write at a time, and only the newest: racing goroutines would let the
// earlier copy finish last and win. Under clipWriteMu a write a later copy has
// superseded is skipped, whichever goroutine gets the lock first, as is its OSC 52
// fallback if a newer copy came in meanwhile.
func (a *App) writeClipboard(text string) {
	seq := a.clipWriteSeq.Add(1)
	a.safego("writing to the clipboard", func() {
		a.clipWriteMu.Lock()
		defer a.clipWriteMu.Unlock()
		if a.clipWriteSeq.Load() != seq {
			return
		}
		if clipboardWriter(text) {
			return
		}
		a.postAndWake(func() {
			if a.clipWriteSeq.Load() == seq {
				a.screen.SetClipboard([]byte(text))
				// OSC 52 has no acknowledgement, so the caller's "Copied to
				// clipboard" may be false: VTE (xfce4-terminal, GNOME Terminal)
				// ignores OSC 52 writes. Overwrites the caller's status, which
				// it set synchronously, before this ran.
				if !clipboard.TerminalLikely(a.screen) {
					a.setStatus("Copied via terminal (may be unsupported)")
				}
			}
		})
	})
}

// clipboardWriter is clipboard.Write, swapped by tests.
var clipboardWriter = clipboard.Write

// copyWithStatus is writeClipboard plus the status-line acknowledgement, for
// grid context menus whose "Copy" item is the whole interaction: nothing else
// on screen changes, so without the message there is no sign it worked.
func (a *App) copyWithStatus(text string) {
	a.writeClipboard(text)
	a.setStatus("Copied to clipboard")
}

// pasteFromClipboard runs Paste (Ctrl+V / Edit > Paste). The native OS
// clipboard read runs on a background goroutine so a stalled tool can't freeze
// the event loop, and the paste is applied on the UI thread. With no native
// tool it requests the terminal clipboard, whose reply arrives later as an
// *tcell.EventClipboard handled in Run().
//
// Both halves resolve the target once, now, and carry it: pasteInto drops the
// text unless that widget is still the one the clipboard would act on.
func (a *App) pasteFromClipboard() {
	target := a.activeClipboardTarget()
	if target == nil {
		return
	}
	token := clipboardTargetToken(a.modals.Top())
	a.safego("reading the clipboard", func() {
		text, ok := clipboard.Read()
		a.postAndWake(func() {
			if ok {
				a.pasteInto(target, token, text)
				return
			}
			a.paste.target, a.paste.token = target, token
			a.screen.GetClipboard()
		})
	})
}

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

// pasteInto applies text to the widget the paste was aimed at, and only that.
//
// Every clipboard read is asynchronous (a shell-out on a goroutine, or the
// terminal's OSC 52 reply as a later event), so between Ctrl+V and the text
// arriving the user can have closed the dialog it was meant for. Re-resolving
// would land a paste aimed at a dialog field in the query editor behind it.
// Dropping the paste is right: the text is still on the clipboard.
//
// token carries the same check one level deeper, for a host that answers with
// itself: a PropertySheet is the active target whichever row has focus, so the
// identity check can't see a move from one row to the next and a paste aimed at
// Name would arrive in Description. See core.ClipboardTargetTokener.
func (a *App) pasteInto(target clipboardTarget, token any, text string) {
	if target == nil || a.activeClipboardTarget() != target {
		return
	}
	if clipboardTargetToken(a.modals.Top()) != token {
		return
	}
	target.Paste(clipboard.StripBOMs(text))
	a.notifyClipboardEdit(target)
}

// ---------------------------------------------------------------------------
// Terminal bracketed paste
// ---------------------------------------------------------------------------

// beginBracketedPaste starts collecting pasted content. tcell's EnablePaste
// (see core.Init) only brackets the paste with EventPaste start/end markers;
// the content still arrives as ordinary EventKeys, which Run() buffers in
// paste.bracket (clipboard.PasteBuffer: its Key says which keys survive and
// how a CR, LF or CRLF becomes one line break).
func (a *App) beginBracketedPaste() {
	a.paste.bracket.Begin()
}

// endBracketedPaste applies the whole buffered paste to the current target as
// one edit. Going through clipboardTarget.Paste rather than replaying keys is
// the point: replayed keys run the typing path, where a pasted newline arrives
// as KeyEnter and an open IntelliSense popup commits its candidate, silently
// rewriting the text. One Paste call is also one undo step.
func (a *App) endBracketedPaste() {
	text := a.paste.bracket.End() // BOMs already stripped
	if text == "" {
		return
	}
	if t := a.activeClipboardTarget(); t != nil {
		t.Paste(text)
		a.notifyClipboardEdit(t)
	}
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
