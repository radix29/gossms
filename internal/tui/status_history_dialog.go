package tui

import (
	"strings"
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
// controls.Editor (SetReadOnly(true), gutter visible by default) rather
// than a hand-rolled scrolling line list like KeyDiagnosticsDialog, so it's
// selectable/copyable via the Editor's own selection — matching the
// Results-To-Text convention (see query_panel.go's p.resultsText).
//
// Unlike KeyDiagnosticsDialog, Show() is not overridden to reset the log:
// history accumulates across the whole session, in memory only, and is
// gone on the next app start — there is deliberately no persistence.
type StatusHistoryDialog struct {
	dialogs.ModalDialog
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
// setStatus/logStatus call this for every status-bar message, so the
// editor's text (a full strings.Join + SetText of up to
// maxStatusHistoryLines lines) is only rebuilt immediately while the dialog
// is actually visible; otherwise the rebuild is deferred to the next Show()
// via the dirty flag, so a busy session doesn't pay that cost for messages
// nobody is looking at.
func (d *StatusHistoryDialog) Record(msg string) {
	line := time.Now().Format("15:04:05") + "  " + msg
	d.lines = append([]string{line}, d.lines...)
	if len(d.lines) > maxStatusHistoryLines {
		d.lines = d.lines[:maxStatusHistoryLines]
	}
	d.dirty = true
	if d.Visible() {
		d.syncEditorText()
	}
}

// syncEditorText rebuilds the editor's content from d.lines and clears the
// dirty flag. d.editor is nil for a zero-value StatusHistoryDialog{} — the
// construction style this dialog's own tests use — so this is a no-op
// without a real editor.
func (d *StatusHistoryDialog) syncEditorText() {
	if d.editor != nil {
		d.editor.SetText(strings.Join(d.lines, "\n"))
	}
	d.dirty = false
}

// Show rebuilds the editor's content first if it fell behind while the
// dialog was hidden (see Record), then displays it. Unlike
// KeyDiagnosticsDialog, the history itself is never reset here — it
// accumulates across the whole session.
func (d *StatusHistoryDialog) Show() {
	if d.dirty {
		d.syncEditorText()
	}
	d.ModalDialog.Show()
}

// Draw renders the dialog.
func (d *StatusHistoryDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	inner := d.InnerRect()
	dataH := inner.H - 2 // leave room for the Close button

	if len(d.lines) == 0 {
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
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if d.ButtonClicked(ev, []string{"Close"}) == 0 {
		d.Hide()
		return true
	}
	d.editor.HandleMouse(ev)
	return true
}
