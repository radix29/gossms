package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/dialogs"
)

// maxKeyDiagLines caps the diagnostics log so a long session doesn't grow
// it without bound; oldest entries are dropped first.
const maxKeyDiagLines = 200

// KeyDiagnosticsDialog is a small modal that shows what tcell decoded for every
// key event while it's open: the raw Key/Modifiers/rune values plus ev.Name()'s
// decode (e.g. "Shift+Left"). It turns "a shortcut doesn't seem to work" into a
// 10-second check of whether the terminal delivers the modifier bits goSSMS
// expects. Mouse events are logged the same way, since a terminal can keep
// Shift+click for itself. See tuikit's controls/editor/editor.go and
// widgets/input_field.go for the Shift+Arrow selection handling this most often
// debugs.
//
// Newest entries are prepended, so the most recent key press is the first line.
//
// The log is tuikit's dialogs.LogDialog (as StatusHistoryDialog's is): a
// read-only editor rather than a hand-rolled line list, so it can be selected
// and copied — what the dialog shows is the text pasted into a bug report. Its
// prepend-only sync matters doubly here: the dialog records the very keys used
// to copy from it (Ctrl+A is itself recorded), so a SetText rebuild on the next
// frame would drop the selection and the following Ctrl+C would copy nothing.
// Each Show starts a fresh session (ResetOnShow).
type KeyDiagnosticsDialog struct {
	*dialogs.LogDialog
	app *App

	// lastMouse is the buttons/modifiers of the last mouse event recorded, so
	// RecordMouse can drop the resends tcell sends throughout a drag. -1 means
	// nothing recorded yet, which no real event matches.
	lastMouse int
}

// keyDiagButtons are the dialog's buttons; Close (the last) is the default.
var keyDiagButtons = []string{"Copy", "Close"}

// NewKeyDiagnosticsDialog creates the key diagnostics dialog.
func NewKeyDiagnosticsDialog(app *App) *KeyDiagnosticsDialog {
	d := &KeyDiagnosticsDialog{app: app, lastMouse: -1}
	d.LogDialog = dialogs.NewLogDialog(dialogs.LogDialogSpec{
		Title: "Key Diagnostics", W: 70, H: 24,
		Max:   maxKeyDiagLines,
		Empty: "Press any key to see how it was decoded...",
		// No gutter: line numbers say nothing about a key log, and every column
		// they take comes off lines that are already 60-odd wide.
		Gutter:      false,
		ResetOnShow: true,
		Buttons:     keyDiagButtons,
		OnButton:    func(int) { d.copyAll() }, // Copy is the only non-Close button
	})
	return d
}

// RecordKey appends a formatted description of ev to the log. Call only while
// the dialog is visible (see app_events.go's handleKey); recording unseen keys
// is wasted work.
func (d *KeyDiagnosticsDialog) RecordKey(ev *tcell.EventKey) {
	d.Record(fmt.Sprintf("%-20s  Key=%-4d Mod=%-3d Str=%-8q Repeat=%d",
		ev.Name(), ev.Key(), ev.Modifiers(), ev.Str(), ev.Repeat()))
}

// RecordMouse appends a description of a mouse event: the buttons, the modifiers,
// and where it landed. Same call rule as RecordKey.
//
// A mouse gesture is not one event. tcell resends the held button for every pixel
// of a drag, and a hundred identical lines would bury the press that started it,
// so only a change of buttons or modifiers is recorded (a press, a release, a
// wheel tick). The position is wherever that change happened.
//
// A modifier the terminal keeps for itself is invisible from inside the app:
// xfce4-terminal (VTE) handles Shift+click as its own text selection while mouse
// reporting is on and never forwards it, which reads like a broken Shift+click.
// One line in this log, or none, tells the two apart.
func (d *KeyDiagnosticsDialog) RecordMouse(ev *tcell.EventMouse) {
	state := int(ev.Buttons())<<8 | int(ev.Modifiers())
	if state == d.lastMouse {
		return
	}
	d.lastMouse = state
	x, y := ev.Position()
	d.Record(fmt.Sprintf("%-20s  Btn=%-4d Mod=%-3d At=%d,%d",
		core.MouseEventName(ev), ev.Buttons(), ev.Modifiers(), x, y))
}

// Show starts a fresh session: LogDialog empties the log (ResetOnShow), and the
// mouse collapse forgets the previous showing's last event.
func (d *KeyDiagnosticsDialog) Show() {
	d.lastMouse = -1
	d.LogDialog.Show()
}

// copyAll selects the whole log and copies it, for the Copy button: the keyboard
// route (Ctrl+A then Ctrl+C) records two more lines before it completes, and on a
// narrow terminal the selection is fiddly.
//
// The selection is dropped as soon as the text has been read: a read-only Editor
// ignores every plain rune key without clearing it, so a leftover select-all
// would sit over the log while the keys this dialog exists to decode arrive
// above it, and the next Ctrl+C would copy the stale whole-log selection.
// copySelection reads SelectedText() synchronously, so clearing right after is
// safe.
func (d *KeyDiagnosticsDialog) copyAll() {
	if d.Len() == 0 {
		return
	}
	e := d.Editor()
	e.SelectAll()
	d.app.copySelection()
	e.ClearSelection()
}
