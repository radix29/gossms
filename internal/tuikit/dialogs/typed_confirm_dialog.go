package dialogs

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// ---------------------------------------------------------------------------
// TypedConfirmDialog — retype-to-confirm
// ---------------------------------------------------------------------------

// typedConfirmFocus tracks which of the dialog's three focusable elements
// (the input, then the two buttons) currently has focus.
type typedConfirmFocus int

const (
	typedConfirmFocusInput typedConfirmFocus = iota
	typedConfirmFocusConfirm
	typedConfirmFocusCancel
)

const (
	typedConfirmW = 60
	typedConfirmH = 10
)

// TypedConfirmDialog gates an action behind retyping a short confirmation
// string, rather than a plain Yes/No (ConfirmDialog) — for actions serious
// enough that a single misclick shouldn't be enough to trigger them.
// Confirm only fires once the typed text matches Required, checked
// case-insensitively.
type TypedConfirmDialog struct {
	ModalDialog
	message  string
	msgLines []string
	required string
	status   string
	input    *widgets.InputField
	focus    typedConfirmFocus

	OnConfirm func(confirmed bool)
}

// NewTypedConfirmDialog creates a TypedConfirmDialog.
func NewTypedConfirmDialog(s tcell.Screen) *TypedConfirmDialog {
	d := &TypedConfirmDialog{}
	d.InitModal(s, "Confirm", typedConfirmW, typedConfirmH)
	return d
}

// ShowTypedConfirm shows the dialog: message explains the action, required
// is the exact text (matched case-insensitively, surrounding whitespace
// ignored) the user must type before Confirm proceeds. The dialog grows
// to show message on one line where that fits within 2/3 of the screen's
// width, or word-wraps onto more lines (growing taller, and pushing the
// required-text line/input down to make room) when it doesn't — see
// fitMessage.
func (d *TypedConfirmDialog) ShowTypedConfirm(title, message, required string, onConfirm func(bool)) {
	d.SetTitle(title)
	d.message = message
	d.required = required
	d.status = ""
	d.input = widgets.NewInputField("", core.Max(20, core.DisplayWidth(required)+16), false)
	d.focus = typedConfirmFocusInput
	d.syncFocus()
	d.OnConfirm = onConfirm
	w, h, lines := d.fitMessage(message, typedConfirmW, typedConfirmH)
	d.msgLines = lines
	d.SetSize(w, h)
	d.ModalDialog.Show()
}

func (d *TypedConfirmDialog) syncFocus() {
	d.input.Focus(d.focus == typedConfirmFocusInput)
}

func (d *TypedConfirmDialog) matched() bool {
	return d.required != "" && strings.EqualFold(strings.TrimSpace(d.input.Value()), d.required)
}

// finish resolves the dialog: a cancel always proceeds, but a confirm whose
// typed text doesn't match is refused in place, with a status message,
// rather than treated as a cancel — the user should have to either fix
// their input or explicitly back out.
func (d *TypedConfirmDialog) finish(confirmed bool) {
	if confirmed && !d.matched() {
		d.status = "Text doesn't match — action not confirmed."
		return
	}
	d.Hide()
	if d.OnConfirm != nil {
		d.OnConfirm(confirmed)
	}
}

// Draw renders the message, the required confirmation text, the input, and
// the Confirm/Cancel button row.
func (d *TypedConfirmDialog) Draw(s tcell.Screen) {
	if !d.visible {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	msgStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	inner := d.InnerRect()
	lx, w := inner.X+1, inner.W-2

	for i, line := range d.msgLines {
		x := lx + core.CenterOffset(w, core.DisplayWidth(line))
		core.DrawTextClipped(s, x, inner.Y+1+i, w, msgStyle, line)
	}
	extra := len(d.msgLines) - 1
	core.DrawTextClipped(s, lx, inner.Y+2+extra, w, msgStyle, "Type \""+d.required+"\" to confirm:")
	d.input.SetBounds(lx, inner.Y+3+extra)
	d.input.Draw(s)

	if d.status != "" {
		errStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Error)
		core.DrawTextClipped(s, lx, d.ButtonRowY()-2, w, errStyle, d.status)
	}

	d.DrawSeparator(s)
	activeIdx := -1
	switch d.focus {
	case typedConfirmFocusConfirm:
		activeIdx = 0
	case typedConfirmFocusCancel:
		activeIdx = 1
	}
	d.DrawButtons(s, []string{"Confirm", "Cancel"}, activeIdx)
}

// HandleKey routes keyboard events.
func (d *TypedConfirmDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.visible {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		d.finish(false)
		return true
	case tcell.KeyTab:
		d.focus = (d.focus + 1) % 3
		d.syncFocus()
		return true
	case tcell.KeyBacktab:
		d.focus = (d.focus + 3 - 1) % 3
		d.syncFocus()
		return true
	case tcell.KeyEnter:
		switch d.focus {
		case typedConfirmFocusCancel:
			d.finish(false)
		default:
			d.finish(true)
		}
		return true
	}
	if d.focus == typedConfirmFocusInput {
		d.input.HandleKey(ev)
	}
	return true
}

// HandleMouse routes mouse events.
func (d *TypedConfirmDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.visible {
		return false
	}
	// A release must reach d.input even when it lands outside the dialog
	// (consumed below) — otherwise its next press is swallowed as a
	// continuation of the stale drag. HandleMouse returns false on
	// ButtonNone, so this has no effect beyond resetting the latch.
	if ev.Buttons() == tcell.ButtonNone {
		d.input.HandleMouse(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, []string{"Confirm", "Cancel"}); i >= 0 {
		d.finish(i == 0)
		return true
	}
	if ev.Buttons() == tcell.ButtonNone {
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}
	mx, my := ev.Position()
	if d.input.HitTest(mx, my) {
		d.focus = typedConfirmFocusInput
		d.syncFocus()
		d.input.HandleMouse(ev)
	}
	return true
}
