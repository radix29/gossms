package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// maxKeyDiagLines caps the diagnostics log so a long session doesn't grow
// it without bound; oldest entries are dropped first.
const maxKeyDiagLines = 200

// KeyDiagnosticsDialog is a small modal that shows exactly what tcell
// decoded for every key event while it's open — the raw Key/Modifiers/rune
// values plus ev.Name()'s human-readable decode (e.g. "Shift+Left"). It
// exists to turn "a shortcut doesn't seem to work" from a guessing game
// into a 10-second check of whether the terminal is actually delivering
// the modifier bits goSSMS expects; see internal/tuikit/controls/editor.go
// and widgets/input_field.go for the Shift+Arrow selection handling this
// is most often used to debug.
//
// Newest entries are prepended, so the most recent key press is always the
// first line — no scrolling needed for the common case of pressing one key
// and immediately checking what was recorded.
//
// The log lives in a read-only controls.Editor rather than a hand-rolled
// scrolling line list, so it can be selected and copied: what this dialog
// shows is exactly the text someone pastes into a bug report, and without a
// widget to hand back there was no core.ClipboardTarget and Ctrl+C did
// nothing at all.
type KeyDiagnosticsDialog struct {
	dialogs.ModalDialog
	app    *App
	lines  []string
	editor *controls.Editor
	dirty  bool // lines changed since the editor's text was last rebuilt
}

// NewKeyDiagnosticsDialog creates the key diagnostics dialog.
func NewKeyDiagnosticsDialog(app *App) *KeyDiagnosticsDialog {
	d := &KeyDiagnosticsDialog{app: app}
	d.InitModal(app.screen, "Key Diagnostics", 70, 24)
	d.editor = controls.NewEditor(nil)
	d.editor.SetReadOnly(true)
	d.editor.SetActive(true)
	// No gutter: line numbers say nothing about a key log, and every column
	// they take comes off lines that are already 60-odd wide.
	d.editor.SetGutterVisible(false)
	return d
}

// RecordKey appends a formatted description of ev to the log. Call only
// while the dialog is visible (see app_events.go's handleKey) — recording
// keys the user can't currently see would just be wasted work.
//
// Unlike StatusHistoryDialog.Record this needs no mutex: handleKey runs on
// the UI goroutine, which is the only goroutine that reaches this.
func (d *KeyDiagnosticsDialog) RecordKey(ev *tcell.EventKey) {
	line := fmt.Sprintf("%-20s  Key=%-4d Mod=%-3d Str=%-8q Repeat=%d",
		ev.Name(), ev.Key(), ev.Modifiers(), ev.Str(), ev.Repeat())
	d.lines = append([]string{line}, d.lines...)
	if len(d.lines) > maxKeyDiagLines {
		d.lines = d.lines[:maxKeyDiagLines]
	}
	d.dirty = true
}

// syncIfDirty rebuilds the editor's content from d.lines if RecordKey has
// run since the last rebuild.
//
// It must not rebuild while the editor has a selection. This dialog records
// the very keys used to copy from it, and Editor.SetText resets cursor,
// scroll and selection: Ctrl+A is itself recorded, so a rebuild on the next
// frame would drop the selection the user just made and the Ctrl+C after it
// would copy nothing. Skipping leaves d.dirty set, so the deferred lines
// appear as soon as the selection is cleared.
func (d *KeyDiagnosticsDialog) syncIfDirty() {
	if !d.dirty || d.editor == nil || d.editor.HasSelection() {
		return
	}
	d.editor.SetText(strings.Join(d.lines, "\n"))
	d.dirty = false
}

// Show resets the log, then displays the dialog — each open starts a fresh
// diagnostic session rather than accumulating across unrelated opens.
//
// The editor is rebuilt unconditionally rather than through syncIfDirty,
// which deliberately refuses to while a selection is live: a selection left
// over from the last showing would otherwise pin the previous session's text
// on screen.
func (d *KeyDiagnosticsDialog) Show() {
	d.lines = nil
	d.dirty = false
	if d.editor != nil {
		d.editor.SetText("")
	}
	d.ModalDialog.Show()
}

// keyDiagButtons are the dialog's buttons; Close is the default.
var keyDiagButtons = []string{"Copy", "Close"}

// Draw renders the dialog.
func (d *KeyDiagnosticsDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.syncIfDirty()

	d.DrawBase(s)
	inner := d.InnerRect()
	dataH := inner.H - 2 // leave room for the button row

	if len(d.lines) == 0 || d.editor == nil {
		p := theme.Active()
		dimStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		core.DrawText(s, inner.X+1, inner.Y+1, dimStyle, "Press any key to see how it was decoded...")
	} else {
		d.editor.SetBounds(inner.X, inner.Y, inner.W, dataH)
		d.editor.Draw(s)
	}

	d.DrawButtons(s, keyDiagButtons, 1)
}

// copyAll selects the whole log and copies it, which is what the Copy button
// is for: the keyboard route (Ctrl+A then Ctrl+C) records two more lines
// before it completes, and on a narrow terminal the selection is fiddly.
//
// The selection is dropped again as soon as the text has been read.
// syncIfDirty refuses to rebuild the editor while one stands, and a read-only
// Editor ignores every plain rune key without clearing it — so a selection
// left behind here freezes the log on exactly the keys this dialog exists to
// decode, until the user happens to press an arrow. copySelection reads
// SelectedText() synchronously, so clearing immediately after is safe.
func (d *KeyDiagnosticsDialog) copyAll() {
	if d.editor == nil || len(d.lines) == 0 {
		return
	}
	d.editor.SelectAll()
	d.app.copySelection()
	d.editor.ClearSelection()
}

// HandleKey processes keyboard events. RecordKey (called from
// app_events.go before this) has already logged ev by the time this runs,
// so Escape/Enter closing the dialog shows up in the log too — as does the
// Ctrl+C that copies from it, which App.handleKey consumes centrally before
// any dialog sees it.
//
// The read-only editor gets first refusal (arrows, Home/End, PgUp/PgDn,
// Shift-selection, Ctrl+A); it rejects Escape and Enter in read-only mode,
// which is what leaves those free to close the dialog.
func (d *KeyDiagnosticsDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	if d.editor != nil && d.editor.HandleKey(ev) {
		return true
	}
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyEnter:
		d.Hide()
	}
	return true
}

// HandleMouse handles mouse events.
func (d *KeyDiagnosticsDialog) HandleMouse(ev *tcell.EventMouse) bool {
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
	switch d.ButtonClicked(ev, keyDiagButtons) {
	case 0:
		d.copyAll()
		return true
	case 1:
		d.Hide()
		return true
	}
	if ev.Buttons() == tcell.ButtonNone {
		return true
	}
	if d.editor != nil {
		d.editor.HandleMouse(ev)
	}
	return true
}

// FocusedClipboardTarget implements core.ClipboardHost. The log editor is
// read-only, so Copy works and Cut/Paste are no-ops on it.
func (d *KeyDiagnosticsDialog) FocusedClipboardTarget() core.ClipboardTarget {
	return d.editor
}
