package tui

import (
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/controls/editor"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/dialogs"
	"github.com/radix29/tuikit/theme"
)

// maxStatusHistoryLines caps the retained history at the last 256 messages;
// oldest entries are dropped first.
const maxStatusHistoryLines = 256

// StatusHistoryDialog is a read-only modal listing every status-bar/log message
// recorded this session, newest first, each prefixed with its timestamp.
// Content lives in a read-only, line-numbered editor.Editor, so it's
// selectable and copyable via the Editor's own selection, matching the
// Results-To-Text convention (see query_panel.go's p.resultsText).
// KeyDiagnosticsDialog is built the same way.
//
// Unlike KeyDiagnosticsDialog, Show() does not reset the log: history
// accumulates across the session, in memory only, with no persistence.
//
// Record is called from background goroutines (App.logStatus reaches it from
// the Object Explorer's loader goroutine), so mu guards lines, pending and
// dirty, and the editor is only touched from the UI goroutine (Show and Draw).
type StatusHistoryDialog struct {
	dialogs.ModalDialog
	mu     sync.Mutex // guards lines, pending and dirty
	lines  []string
	editor *editor.Editor
	dirty  bool // lines changed since the editor's text was last synced

	// pending holds the lines recorded since the last sync, newest first, so a
	// sync while the dialog is open can prepend just those (Editor.PrependLines)
	// instead of rebuilding with SetText, which resets cursor, scroll and
	// selection. A full rebuild dropped the selection on every message, the
	// "Copied to clipboard" of the user's own Ctrl+C included, so a second Copy
	// copied nothing, and snapped a scrolled-down view back to line 1 whenever
	// a background load recorded a status.
	pending []string
}

// NewStatusHistoryDialog creates the status history dialog.
func NewStatusHistoryDialog(app *App) *StatusHistoryDialog {
	d := &StatusHistoryDialog{}
	d.InitModal("Status History", 90, 26)
	d.editor = editor.NewEditor(nil)
	d.editor.SetReadOnly(true)
	d.editor.Focus(true)
	return d
}

// Record prepends a timestamped line to the history. Newest-first so the most
// recent message is the first line on open, and so a sync while open is a
// prepend that keeps a viewer at the top seeing the newest line.
//
// Safe from any goroutine, and it deliberately does no more than record:
// rebuilding the editor's text is the UI goroutine's job, done by Show and Draw
// when dirty. Calling Editor.SetText here would write the widget the UI
// goroutine is drawing.
//
// Deferring the sync also keeps it off the hot path: a busy session pays it
// once per frame the dialog is open rather than once per message nobody is
// looking at.
func (d *StatusHistoryDialog) Record(msg string) {
	line := time.Now().Format("15:04:05") + "  " + msg

	d.mu.Lock()
	defer d.mu.Unlock()
	d.lines = append([]string{line}, d.lines...)
	if len(d.lines) > maxStatusHistoryLines {
		d.lines = d.lines[:maxStatusHistoryLines]
	}
	// Bounded like lines: a closed dialog's pending is dropped by Show's full
	// rebuild, and more than maxStatusHistoryLines would be trimmed anyway.
	d.pending = append([]string{line}, d.pending...)
	if len(d.pending) > maxStatusHistoryLines {
		d.pending = d.pending[:maxStatusHistoryLines]
	}
	d.dirty = true
}

// lineCount reports how many messages are held.
func (d *StatusHistoryDialog) lineCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.lines)
}

// syncIfDirty prepends the lines Record added since the last sync, keeping the
// viewer's cursor, selection and scroll on the text they were on (see
// pending), and trims the editor to maxStatusHistoryLines. UI goroutine only.
func (d *StatusHistoryDialog) syncIfDirty() {
	d.mu.Lock()
	if !d.dirty {
		d.mu.Unlock()
		return
	}
	text := strings.Join(d.pending, "\n")
	d.pending = nil
	d.dirty = false
	d.mu.Unlock()

	// Outside the lock: the editor update is the expensive half, and Record
	// must not block behind it on a background goroutine.
	if d.editor != nil {
		d.editor.PrependLines(text, maxStatusHistoryLines)
	}
}

// rebuild replaces the editor's content with the whole history, resetting
// cursor, scroll and selection. UI goroutine only.
func (d *StatusHistoryDialog) rebuild() {
	d.mu.Lock()
	text := strings.Join(d.lines, "\n")
	d.pending = nil
	d.dirty = false
	d.mu.Unlock()

	if d.editor != nil {
		d.editor.SetText(text)
	}
}

// Show rebuilds the editor's content from the whole history, then displays
// it: a fresh open starts at the top on the newest message, as
// KeyDiagnosticsDialog's does. Unlike KeyDiagnosticsDialog, the history itself
// is never reset here — it accumulates across the whole session.
func (d *StatusHistoryDialog) Show() {
	d.rebuild()
	d.ModalDialog.Show()
}

// Draw renders the dialog.
func (d *StatusHistoryDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	// Messages recorded since the last frame are picked up here rather than
	// in Record, which may be on a background goroutine (see Record).
	d.syncIfDirty()

	d.DrawBase(s)
	inner := d.InnerRect()
	dataH := inner.H - 2 // leave room for the Close button

	if d.lineCount() == 0 || d.editor == nil {
		p := theme.Active()
		dimStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.DialogTextDim)
		core.DrawText(s, inner.X+1, inner.Y+1, dimStyle, "No messages recorded yet this session.")
	} else {
		d.editor.SetBounds(inner.X, inner.Y, inner.W, dataH)
		d.editor.Draw(s)
	}

	d.DrawButtons(s, []string{"Close"}, 0)
}

// HandleKey processes keyboard events. Delegates to the read-only editor
// first (arrow keys, PgUp/PgDn, Home/End, selection, Ctrl+A); Escape/Enter,
// which the editor rejects in read-only mode, close the dialog.
func (d *StatusHistoryDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	if d.editor.HandleKey(ev) {
		return true
	}
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyEnter:
		d.Hide()
	}
	return true
}

// HandleMouse handles mouse events.
func (d *StatusHistoryDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	// A release must reach d.editor even when it lands outside the dialog
	// (consumed below) — otherwise its next press is swallowed as a
	// continuation of the stale drag. Editor.HandleMouse returns false on
	// ButtonNone, so this has no effect beyond resetting the latch.
	if ev.Buttons() == tcell.ButtonNone && d.editor != nil {
		d.editor.HandleMouse(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if d.ButtonClicked(ev, []string{"Close"}) == 0 {
		d.Hide()
		return true
	}
	if ev.Buttons() == tcell.ButtonNone {
		return true
	}
	d.editor.HandleMouse(ev)
	return true
}

// FocusClipboardTargetAt implements core.ClipboardFocuser: the history editor is
// the only field, always focused, so a right-click on it is all it takes.
func (d *StatusHistoryDialog) FocusClipboardTargetAt(x, y int) bool {
	return d.editor != nil && d.editor.Bounds().Contains(x, y)
}

// FocusedClipboardTarget implements core.ClipboardHost. The history editor is
// read-only, so Copy works and Cut/Paste are no-ops on it. Without this the
// dialog fell through to the query editor underneath, and Ctrl+C there copied
// the user's SQL while they were looking at the log.
func (d *StatusHistoryDialog) FocusedClipboardTarget() core.ClipboardTarget {
	return d.editor
}
