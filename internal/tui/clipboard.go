package tui

import (
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// clipboardTarget is implemented by any widget that can participate in
// Copy/Cut/Paste. Both widgets.InputField and controls.Editor satisfy this
// structurally (Go interfaces don't require the implementing type to know
// about the interface), which is what lets a single set of App-level
// methods work uniformly across every dialog field and the query editor
// without tuikit itself needing any notion of "clipboard".
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
	case a.pathPrompt.Visible():
		return a.pathPrompt.field
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
		// The Messages tab's read-only text view, while showing, takes
		// priority next — see QueryPanel.onMessagesTab.
		if qp.onMessagesTab() {
			return qp.messages
		}
		// The results grid's built-in "Show Value" content viewer, while
		// open, takes priority over the SQL editor — see
		// controls.DataGrid.HasSelection, which is only true while that
		// popup is showing.
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
	if target == nil || !target.HasSelection() {
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
	go func() {
		if osClipboardWrite(text) {
			return
		}
		a.postEvent(func() { a.screen.SetClipboard([]byte(text)) })
		a.wakeEventLoop()
	}()
}

// pasteFromClipboard runs Paste (Ctrl+V / Edit > Paste). The native OS
// clipboard read happens in a background goroutine (again, so a stalled
// tool can't freeze the event loop — reading an X11 selection whose owner
// is unresponsive is a classic hang), then the paste is applied on the UI
// thread. When no native tool is available it falls back to requesting the
// terminal clipboard; that response arrives asynchronously as an
// *tcell.EventClipboard, handled in Run(), which re-resolves
// activeClipboardTarget() and calls its Paste method.
func (a *App) pasteFromClipboard() {
	if a.activeClipboardTarget() == nil {
		return
	}
	go func() {
		text, ok := osClipboardRead()
		a.postEvent(func() {
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
		a.wakeEventLoop()
	}()
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
