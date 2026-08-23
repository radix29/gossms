package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// clipboardTarget is any widget that can participate in Copy/Cut/Paste — an
// alias for core.ClipboardTarget, which lives there so tuikit's own dialogs can
// hand one back (see core.ClipboardHost). widgets.InputField, controls.Editor,
// controls.DataGrid and propsheet.PropertySheet all satisfy it structurally, so
// one set of App-level methods works across every dialog field and the
// editor.
type clipboardTarget = core.ClipboardTarget

// activeClipboardTarget resolves which widget Copy/Cut/Paste acts on now:
// whichever field is focused in the frontmost open dialog, the active query
// panel's editor (or its results grid while that grid's "Show Value" viewer is
// open), or a read-only panel's grid while its own viewer is open. nil when
// nothing focused can participate.
func (a *App) activeClipboardTarget() clipboardTarget {
	// An open dialog owns the clipboard outright: its focused field, or nothing.
	// Never fall past it to a panel underneath — a fall-through lets Ctrl+X with
	// the Find dialog open cut the query editor's selection behind it, reported
	// as "Cut to clipboard" with no sign of what happened. A dialog that isn't a
	// ClipboardHost has no text to act on, so nil is the answer.
	if top := a.topDialog(); top != nil {
		if h, ok := top.(core.ClipboardHost); ok {
			return h.FocusedClipboardTarget()
		}
		return nil
	}
	if qp := a.activeQueryPanel(); qp != nil {
		// Every view below shares the results half of the panel, so which one
		// shows decides the target only when that half holds keyboard focus.
		// Without the resultsHasFocus() gate, an execution leaving the pane on
		// Messages, the plan or Results To Text redirects Ctrl+V from the SQL
		// editor to a read-only view whose Paste is a no-op.
		if qp.resultsHasFocus() {
			// The Messages tab's read-only text view, while showing, takes
			// priority — see QueryPanel.onMessagesTab.
			if qp.onMessagesTab() {
				return qp.messages
			}
			// The Execution Plan view, while showing, takes priority next
			// — see QueryPanel.planTabActive.
			if qp.planTabActive() {
				return qp.planView
			}
			// Results To Text's read-only view, while showing, takes
			// priority next too — see QueryPanel.textTabActive.
			if qp.textTabActive() {
				return qp.resultsText
			}
			return qp.results
		}
		// The results grid's "Show Value" viewer is a modal overlay: it keeps its
		// own selection while open whichever half is focused, so it wins over the
		// SQL editor. controls.DataGrid.HasSelection is true only while it
		// shows.
		if qp.results.HasSelection() {
			return qp.results
		}
		return qp.editor
	}
	if db, ok := a.panels.ActivePanel().(*DetailBrowser); ok && db.HasSelection() {
		return db
	}
	// The Always On dashboard is two grids and nothing else, so whichever has the
	// keyboard is the target; copySelection's DataGrid branch then copies the
	// cell under the cursor even with no viewer open.
	if dash, ok := a.panels.ActivePanel().(*AGDashboard); ok {
		return dash.grid()
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
	// A focused results grid has no "selection" in the clipboardTarget sense
	// unless its content viewer is open, but it always has a cell or block under
	// its cursor — copy that, matching its right-click "Copy".
	if g, ok := target.(*controls.DataGrid); ok && !g.HasSelection() {
		a.copyWithStatus(g.SelectedCellsText())
		return
	}
	if !target.HasSelection() {
		return
	}
	a.writeClipboard(target.SelectedText())
	a.setStatus("Copied to clipboard")
}

// cutSelection runs Cut (Ctrl+X / Edit > Cut): like copySelection, but
// also removes the selected text from the target.
func (a *App) cutSelection() {
	target := a.activeClipboardTarget()
	if target == nil || !target.HasSelection() {
		return
	}
	a.writeClipboard(target.Cut())
	a.setStatus("Cut to clipboard")
	a.notifyClipboardEdit(target)
}

// notifyClipboardEdit tells the frontmost dialog that a Cut or Paste changed one
// of its fields, so it can follow up as it does after a keystroke — see
// core.ClipboardEditHandler. The dialog checks which target it was handed rather
// than assuming the edit landed in the field it watches.
func (a *App) notifyClipboardEdit(target clipboardTarget) {
	if h, ok := a.topDialog().(core.ClipboardEditHandler); ok {
		h.ClipboardEdited(target)
	}
}

// writeClipboard sends text to the clipboard off the UI thread: the native OS
// clipboard (xclip/xsel/wl-copy/pbcopy/clip.exe, see os_clipboard.go) first,
// falling back to tcell's OSC 52 terminal clipboard when no native tool handled
// it. The shell-out runs on a background goroutine so a stalled tool can't
// freeze the event loop; the OSC 52 fallback returns to the UI thread, since
// SetClipboard writes to the terminal.
func (a *App) writeClipboard(text string) {
	a.safego("writing to the clipboard", func() {
		if osClipboardWrite(text) {
			return
		}
		a.postAndWake(func() { a.screen.SetClipboard([]byte(text)) })
	})
}

// copyWithStatus is writeClipboard plus the status-line acknowledgement, for the
// grid context menus whose "Copy" item is the whole interaction: nothing else on
// screen changes, so without the message there is no sign it worked.
func (a *App) copyWithStatus(text string) {
	a.writeClipboard(text)
	a.setStatus("Copied to clipboard")
}

// pasteFromClipboard runs Paste (Ctrl+V / Edit > Paste). The native OS clipboard
// read runs on a background goroutine so a stalled tool can't freeze the event
// loop, and the paste is applied on the UI thread. With no native tool it falls
// back to requesting the terminal clipboard, whose reply arrives later as an
// *tcell.EventClipboard handled in Run().
//
// Both halves resolve the target once, now, and carry it: whichever way the text
// comes back, pasteInto drops it unless the widget it was aimed at is still the
// one the clipboard would act on.
func (a *App) pasteFromClipboard() {
	target := a.activeClipboardTarget()
	if target == nil {
		return
	}
	token := clipboardTargetToken(a.topDialog())
	a.safego("reading the clipboard", func() {
		text, ok := osClipboardRead()
		a.postAndWake(func() {
			if ok {
				a.pasteInto(target, token, text)
				return
			}
			a.pendingPaste, a.pendingPasteToken = target, token
			a.screen.GetClipboard()
		})
	})
}

// clipboardTargetToken is the identity of the field a host has focus on, or nil
// from a host that doesn't distinguish its fields. Only propsheet.PropertySheet
// answers: it returns itself from FocusedClipboardTarget, so nothing else can
// tell its rows apart. See core.ClipboardTargetTokener.
func clipboardTargetToken(top Dialog) any {
	if t, ok := top.(core.ClipboardTargetTokener); ok {
		return t.ClipboardTargetToken()
	}
	return nil
}

// pasteInto applies text to the widget the paste was aimed at, and only to that
// widget.
//
// Every clipboard read is asynchronous — a shell-out on a background goroutine,
// or the terminal's OSC 52 reply arriving as an event later still — so between
// Ctrl+V and the text arriving the user can have closed the dialog it was meant
// for. Re-resolving the target then is how a paste aimed at a dialog field lands
// in the query editor behind it. Dropping the paste is the right answer: the
// text is still on the clipboard, and the next Ctrl+V goes where the user is.
//
// token carries the same check one level deeper, for a host that answers with
// itself: a PropertySheet is the active target whichever row has focus, so the
// identity check above cannot see a move from one row to the next and a paste
// aimed at Name arrives in Description. See core.ClipboardTargetTokener.
func (a *App) pasteInto(target clipboardTarget, token any, text string) {
	if target == nil || a.activeClipboardTarget() != target {
		return
	}
	if clipboardTargetToken(a.topDialog()) != token {
		return
	}
	target.Paste(text)
	a.notifyClipboardEdit(target)
}

// ---------------------------------------------------------------------------
// Terminal bracketed paste
// ---------------------------------------------------------------------------

// beginBracketedPaste starts collecting pasted content. tcell's EnablePaste
// (see core.Init) only brackets the paste with EventPaste start/end markers —
// the content still arrives as ordinary EventKeys in between, which is why Run()
// buffers them here.
func (a *App) beginBracketedPaste() {
	a.pasting = true
	a.pasteBuf.Reset()
}

// bufferPastedKey appends one key of an in-progress bracketed paste to pasteBuf.
// Anything that isn't a character, newline or tab is dropped rather than acted
// on — a stray escape sequence can decode as a function key — so a paste can
// never trigger a command.
func (a *App) bufferPastedKey(ev *tcell.EventKey) {
	switch ev.Key() {
	case tcell.KeyRune:
		a.pasteBuf.WriteString(ev.Str())
	case tcell.KeyEnter:
		a.pasteBuf.WriteByte('\n')
	case tcell.KeyTab:
		a.pasteBuf.WriteByte('\t')
	}
}

// endBracketedPaste applies the whole buffered paste to the current target as
// one edit. Going through clipboardTarget.Paste rather than replaying the keys
// is the point: replayed keys run the typing path, where a pasted newline
// arrives as KeyEnter and the open IntelliSense popup commits its candidate
// instead, silently rewriting the text. One Paste call is also one undo step.
func (a *App) endBracketedPaste() {
	a.pasting = false
	text := a.pasteBuf.String()
	a.pasteBuf.Reset()
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

// showEditorContextMenu pops up a Cut/Copy/Paste/Select All menu at (x,y), wired
// to the same App-level clipboard actions the keyboard shortcuts and Edit menu
// use. Fired from a query editor's right-click.
func (a *App) showEditorContextMenu(x, y int) {
	a.contextMenu.Show(x, y, []controls.MenuItem{
		{Label: "Cut", Shortcut: "Ctrl+X", Action: func() { a.cutSelection() }},
		{Label: "Copy", Shortcut: "Ctrl+C", Action: func() { a.copySelection() }},
		{Label: "Paste", Shortcut: "Ctrl+V", Action: func() { a.pasteFromClipboard() }},
		{Divider: true},
		{Label: "Select All", Shortcut: "Ctrl+A", Action: func() { a.selectAllInTarget() }},
	})
}
