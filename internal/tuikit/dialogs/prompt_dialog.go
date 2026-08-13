package dialogs

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// ---------------------------------------------------------------------------
// PromptDialog — one-line text input
// ---------------------------------------------------------------------------

// promptFocus tracks which of the dialog's three focusable elements (the
// input, then the two buttons) has focus.
type promptFocus int

const (
	promptFocusInput promptFocus = iota
	promptFocusOK
	promptFocusCancel
)

const (
	promptW = 54
	promptH = 10
)

// PromptDialog asks for a single line of text — a new name, a label — and
// hands it back through OnAccept. It is ConfirmDialog's shape with an input
// instead of a yes/no: a message, one field, OK/Cancel.
//
// An empty value is refused in place rather than accepted or treated as a
// cancel, and a Validate hook lets the host reject more (a duplicate name, a
// bad identifier) without the dialog knowing what the value means.
type PromptDialog struct {
	ModalDialog
	message  string
	msgLines []string
	label    string
	status   string
	input    *widgets.InputField
	focus    promptFocus

	// OnAccept receives the trimmed value once it passes Validate. Not
	// called on cancel — a dialog the user backed out of reports nothing.
	OnAccept func(value string)

	// Validate rejects a value with the error shown in the dialog, which
	// stays open. nil accepts anything non-empty.
	Validate func(value string) error
}

// NewPromptDialog creates a PromptDialog.
func NewPromptDialog(s tcell.Screen) *PromptDialog {
	d := &PromptDialog{}
	d.InitModal(s, "Prompt", promptW, promptH)
	return d
}

// ShowPrompt opens the dialog: message explains what is being asked, label
// names the field, and initial pre-fills it selected, so typing replaces it
// (the rename case — the current name is what the user is editing away
// from). Validate is cleared on every showing; set it after this call.
func (d *PromptDialog) ShowPrompt(title, message, label, initial string, onAccept func(string)) {
	d.SetTitle(title)
	d.message = message
	d.label = label
	d.status = ""
	d.OnAccept = onAccept
	d.Validate = nil
	d.input = widgets.NewInputField(label, max(24, promptW-core.DisplayWidth(label)-8), false)
	d.input.SetValue(initial)
	d.input.SelectAll()
	d.focus = promptFocusInput
	d.syncFocus()
	w, h, lines := d.fitMessage(message, promptW, promptH)
	d.msgLines = lines
	d.SetSize(w, h)
	d.ModalDialog.Show()
}

// Value is what the input currently holds, trimmed.
func (d *PromptDialog) Value() string { return strings.TrimSpace(d.input.Value()) }

func (d *PromptDialog) syncFocus() { d.input.Focus(d.focus == promptFocusInput) }

// accept validates and resolves the dialog. A rejected value leaves the
// dialog open with the reason shown, rather than closing and silently doing
// nothing.
func (d *PromptDialog) accept() {
	v := d.Value()
	if v == "" {
		d.status = "Enter a value."
		return
	}
	if d.Validate != nil {
		if err := d.Validate(v); err != nil {
			d.status = err.Error()
			return
		}
	}
	d.Hide()
	if d.OnAccept != nil {
		d.OnAccept(v)
	}
}

func (d *PromptDialog) cancel() { d.Hide() }

func (d *PromptDialog) buttons() []string { return []string{"OK", "Cancel"} }

// Draw renders the message, the input, and the button row.
func (d *PromptDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	msgStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	inner := d.InnerRect()
	lx, w := inner.X+1, inner.W-2

	for i, line := range d.msgLines {
		core.DrawTextClipped(s, lx, inner.Y+1+i, w, msgStyle, line)
	}
	d.input.SetBounds(lx, inner.Y+2+len(d.msgLines)-1)
	d.input.Draw(s)

	if d.status != "" {
		errStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Error)
		core.DrawTextClipped(s, lx, d.ButtonRowY()-2, w, errStyle, d.status)
	}

	d.DrawSeparator(s)
	activeIdx := -1
	switch d.focus {
	case promptFocusOK:
		activeIdx = 0
	case promptFocusCancel:
		activeIdx = 1
	}
	d.DrawButtons(s, d.buttons(), activeIdx)
}

// HandleKey routes keyboard events. Enter accepts from the input as well as
// from OK — the field is the only thing to fill in, so typing and pressing
// Enter is the whole interaction.
func (d *PromptDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		d.cancel()
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
		if d.focus == promptFocusCancel {
			d.cancel()
			return true
		}
		d.accept()
		return true
	}
	if d.focus == promptFocusInput {
		d.input.HandleKey(ev)
	}
	return true
}

// HandleMouse routes mouse events.
func (d *PromptDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	// A release must reach d.input even when it lands outside the dialog
	// (consumed below) — otherwise its next press is swallowed as a
	// continuation of the stale drag. HandleMouse returns false on
	// ButtonNone, so this only resets the latch.
	if ev.Buttons() == tcell.ButtonNone {
		d.input.HandleMouse(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, d.buttons()); i >= 0 {
		if i == 0 {
			d.accept()
		} else {
			d.cancel()
		}
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
		d.focus = promptFocusInput
		d.syncFocus()
		d.input.HandleMouse(ev)
	}
	return true
}
