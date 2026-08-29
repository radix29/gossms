package tui

import (
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// maxStatusHistoryLines caps the retained history at the last 256 messages;
// oldest entries are dropped first.
const maxStatusHistoryLines = 256

// StatusHistoryDialog is a read-only modal listing every status-bar/log
// message recorded this session, newest first, each prefixed with the
// timestamp it was recorded at. Content lives in a read-only, line-numbered
// controls.Editor (SetReadOnly(true), gutter visible by default), so it's
// selectable/copyable via the Editor's own selection — matching the
// Results-To-Text convention (see query_panel.go's p.resultsText).
// KeyDiagnosticsDialog is built the same way, for the same reason.
//
// Unlike KeyDiagnosticsDialog, Show() is not overridden to reset the log:
// history accumulates across the whole session, in memory only, and is
// gone on the next app start — there is deliberately no persistence.
//
// Record is called from background goroutines — App.logStatus reaches it from
// the Object Explorer's loader goroutine — so mu guards lines and dirty, and
// the editor is only ever touched from the UI goroutine (Show and Draw).
type StatusHistoryDialog struct {
	dialogs.ModalDialog
	mu     sync.Mutex // guards lines and dirty
	lines  []string
	editor *controls.Editor
	dirty  bool // lines changed since the editor's text was last rebuilt
}

// NewStatusHistoryDialog creates the status history dialog.
func NewStatusHistoryDialog(app *App) *StatusHistoryDialog {
	d := &StatusHistoryDialog{}
	d.InitModal(app.screen, "Status History", 90, 26)
	d.editor = controls.NewEditor(nil)
	d.editor.SetReadOnly(true)
	d.editor.SetActive(true)
	return d
}

// Record prepends a timestamped line to the history. Newest-first:
// controls.Editor.SetText always resets scroll/cursor to (0,0) and has no
// "scroll to end" API, so newest-first is what makes the most recent
// message visible on open without any extra plumbing.
//
// Safe to call from any goroutine, and it deliberately does no more than
// record: rebuilding the editor's text is the UI goroutine's job, done by
// Show and Draw when dirty. A background goroutine that called
// Editor.SetText here would be writing the widget the UI goroutine is
// drawing — App.logStatus reaches Record from the Object Explorer's loader
// goroutine.
//
// Deferring the rebuild also keeps it off the hot path: it is a full
// strings.Join and SetText of up to maxStatusHistoryLines lines, and a busy
// session pays it once per frame the dialog is actually open rather than
// once per message nobody is looking at.
func (d *StatusHistoryDialog) Record(msg string) {
	line := time.Now().Format("15:04:05") + "  " + msg

	d.mu.Lock()
	defer d.mu.Unlock()
	d.lines = append([]string{line}, d.lines...)
	if len(d.lines) > maxStatusHistoryLines {
		d.lines = d.lines[:maxStatusHistoryLines]
	}
	d.dirty = true
}

// lineCount reports how many messages are held.
func (d *StatusHistoryDialog) lineCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.lines)
}

// syncIfDirty rebuilds the editor's content from d.lines if Record has run
// since the last rebuild. UI goroutine only.
func (d *StatusHistoryDialog) syncIfDirty() {
	d.mu.Lock()
	if !d.dirty {
		d.mu.Unlock()
		return
	}
	text := strings.Join(d.lines, "\n")
	d.dirty = false
	d.mu.Unlock()

	// Outside the lock: SetText is the expensive half, and Record must not
	// block behind it on a background goroutine.
	if d.editor != nil {
		d.editor.SetText(text)
	}
}

// Show rebuilds the editor's content first if it fell behind while the
// dialog was hidden (see Record), then displays it. Unlike
// KeyDiagnosticsDialog, the history itself is never reset here — it
// accumulates across the whole session.
func (d *StatusHistoryDialog) Show() {
	d.syncIfDirty()
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
		dimStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
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

// FocusedClipboardTarget implements core.ClipboardHost. The history editor is
// read-only, so Copy works and Cut/Paste are no-ops on it — which is the point:
// without this the dialog fell through to the query editor underneath, and
// Ctrl+C there copied the user's SQL while they were looking at the log.
func (d *StatusHistoryDialog) FocusedClipboardTarget() core.ClipboardTarget {
	return d.editor
}
