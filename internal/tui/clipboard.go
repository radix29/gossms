package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// clipboardTarget is implemented by any widget that can participate in
// Copy/Cut/Paste. Both widgets.InputField and controls.Editor satisfy it
// structurally, which lets one set of App-level methods work across every
// dialog field and the query editor without tuikit itself needing any
// notion of "clipboard".
type clipboardTarget interface {
	HasSelection() bool
	SelectedText() string
	Cut() string
	Paste(text string)
	SelectAll()
}

// activeClipboardTarget resolves which widget Copy/Cut/Paste should act on
// right now: whichever InputField or Editor is focused in a visible
// dialog, the active query panel's editor (or its results grid, while that
// grid's "Show Value" content viewer is open), or a read-only panel's grid
// while its own content viewer is open. Returns nil if nothing focused
// right now can participate (e.g. plain Object Explorer focus).
func (a *App) activeClipboardTarget() clipboardTarget {
	switch {
	case a.fileDialog.Visible():
		if f := a.fileDialog.FocusedField(); f != nil {
			return f
		}
		return nil
	case a.propDialog.Visible():
		return a.propDialog.PropertySheet
	case a.connectDialog.Visible():
		if a.connectDialog.focusIdx < len(a.connectDialog.focusable) {
			switch f := a.connectDialog.focusable[a.connectDialog.focusIdx].(type) {
			case *widgets.InputField:
				return f
			case *controls.Editor:
				return f
			}
		}
		return nil
	}
	if qp := a.activeQueryPanel(); qp != nil {
		// Every view below shares the results half of the panel, so which
		// one is showing only decides the target when that half actually
		// holds keyboard focus. Without the resultsHasFocus() gate, any
		// execution that leaves the pane on the Messages tab (or the plan,
		// or Results To Text) silently redirects Ctrl+V from the SQL editor
		// the user is typing in to a read-only view whose Paste is a no-op
		// — the reported "paste does nothing in the query editor".
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
		// The results grid's built-in "Show Value" content viewer is a modal
		// overlay: it keeps its own selection while open regardless of which
		// half is marked focused, so it still wins over the SQL editor — see
		// controls.DataGrid.HasSelection, true only while that popup shows.
		if qp.results.HasSelection() {
			return qp.results
		}
		return qp.editor
	}
	if db, ok := a.panels.ActivePanel().(*DetailBrowser); ok && db.HasSelection() {
		return db
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
	// A focused results grid has no "selection" in the clipboardTarget
	// sense unless its content viewer is open (see
	// controls.DataGrid.HasSelection), but it always has a cell or block
	// under its cursor — copy that, matching its right-click "Copy".
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
	if a.connectDialog.Visible() {
		a.connectDialog.updateMatches()
	}
}

// writeClipboard sends text to the clipboard off the UI thread — the
// native OS clipboard (xclip/xsel/wl-copy/pbcopy/clip.exe, see
// os_clipboard.go) first, falling back to tcell's OSC 52 terminal
// clipboard when no native tool handled it (e.g. a bare SSH session). The
// shell-out to the native tool runs in a background goroutine so a slow or
// stalled clipboard tool can't freeze the single-threaded event loop; the
// OSC 52 fallback is marshalled back to the UI thread since SetClipboard
// writes to the terminal.
func (a *App) writeClipboard(text string) {
	a.safego("writing to the clipboard", func() {
		if osClipboardWrite(text) {
			return
		}
		a.postAndWake(func() { a.screen.SetClipboard([]byte(text)) })
	})
}

// copyWithStatus is writeClipboard plus the status-line acknowledgement,
// for the grid context menus whose "Copy" item is the whole interaction —
// nothing else on screen changes, so without the message there's no sign it
// worked. Wired into the results grid (NewQueryPanel) and the execution
// plan's operator summary (PlanView.OnCopyRequest, in both its hosts).
func (a *App) copyWithStatus(text string) {
	a.writeClipboard(text)
	a.setStatus("Copied to clipboard")
}

// pasteFromClipboard runs Paste (Ctrl+V / Edit > Paste). The native OS
// clipboard read happens in a background goroutine so a stalled tool can't
// freeze the event loop, then the paste is applied on the UI thread. When
// no native tool is available it falls back to requesting the terminal
// clipboard; that response arrives asynchronously as an
// *tcell.EventClipboard, handled in Run(), which re-resolves
// activeClipboardTarget() and calls its Paste method.
func (a *App) pasteFromClipboard() {
	if a.activeClipboardTarget() == nil {
		return
	}
	a.safego("reading the clipboard", func() {
		text, ok := osClipboardRead()
		a.postAndWake(func() {
			if ok {
				if t := a.activeClipboardTarget(); t != nil {
					t.Paste(text)
					if a.connectDialog.Visible() {
						a.connectDialog.updateMatches()
					}
				}
				return
			}
			a.screen.GetClipboard()
		})
	})
}

// ---------------------------------------------------------------------------
// Terminal bracketed paste
// ---------------------------------------------------------------------------

// beginBracketedPaste starts collecting pasted content. tcell's
// EnablePaste (see core.Init) only brackets the paste with EventPaste
// start/end markers — the content itself still arrives as ordinary
// EventKeys in between, which is why Run() buffers them here instead of
// handling them.
func (a *App) beginBracketedPaste() {
	a.pasting = true
	a.pasteBuf.Reset()
}

// bufferPastedKey appends one key of an in-progress bracketed paste to
// pasteBuf. Anything that isn't a character, newline, or tab (arrow keys,
// function keys — a terminal shouldn't send them inside a paste, but a
// stray escape sequence can decode as one) is dropped rather than acted
// on, so a paste can never trigger a command.
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

// endBracketedPaste applies the whole buffered paste to the current target
// as one edit. Going through clipboardTarget.Paste rather than replaying
// the keys is the point: replayed keys run the full typing path, where a
// pasted newline arrives as KeyEnter and the open IntelliSense popup
// commits its selected candidate instead — silently rewriting the pasted
// text. One Paste call is also one undo step.
func (a *App) endBracketedPaste() {
	a.pasting = false
	text := a.pasteBuf.String()
	a.pasteBuf.Reset()
	if text == "" {
		return
	}
	if t := a.activeClipboardTarget(); t != nil {
		t.Paste(text)
		if a.connectDialog.Visible() {
			a.connectDialog.updateMatches()
		}
	}
}

// selectAllInTarget runs Select All (Ctrl+A / Edit > Select All) on
// whichever widget Copy/Cut/Paste would currently act on.
func (a *App) selectAllInTarget() {
	if target := a.activeClipboardTarget(); target != nil {
		target.SelectAll()
	}
}

// showEditorContextMenu pops up a Cut/Copy/Paste/Select All menu at (x,y),
// wired to the same App-level clipboard actions the keyboard shortcuts and
// Edit menu use. Fired from a query editor's right-click (see
// controls.Editor.OnRightClick, wired in NewQueryPanel).
func (a *App) showEditorContextMenu(x, y int) {
	a.contextMenu.Show(x, y, []controls.MenuItem{
		{Label: "Cut", Shortcut: "Ctrl+X", Action: func() { a.cutSelection() }},
		{Label: "Copy", Shortcut: "Ctrl+C", Action: func() { a.copySelection() }},
		{Label: "Paste", Shortcut: "Ctrl+V", Action: func() { a.pasteFromClipboard() }},
		{Divider: true},
		{Label: "Select All", Shortcut: "Ctrl+A", Action: func() { a.selectAllInTarget() }},
	})
}
