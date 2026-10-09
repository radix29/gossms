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

// typedConfirmFocus tracks which element has focus: the input, then one
// position per button (focus-1 indexes d.buttons), so a Script button adds a
// position rather than renumbering.
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
// string rather than a plain Yes/No, for actions where a misclick shouldn't
// suffice. Confirm fires only once the typed text matches Required,
// case-insensitively.
type TypedConfirmDialog struct {
	ModalDialog
	message  string
	msgLines []string
	required string
	status   string
	input    *widgets.InputField
	focus    typedConfirmFocus

	// drag owns the text-selection gesture a press in input starts; see
	// FieldGesture.
	drag FieldGesture

	// buttons is what the showing renders and hit-tests; answers is what each
	// means, parallel rather than the button index (as in ConfirmDialog).
	buttons []string
	answers []ConfirmAnswer

	// onAnswer is the showing's handler. OnConfirm is the two-button form's.
	onAnswer  func(ConfirmAnswer)
	OnConfirm func(confirmed bool)
}

// NewTypedConfirmDialog creates a TypedConfirmDialog.
func NewTypedConfirmDialog(s tcell.Screen) *TypedConfirmDialog {
	d := &TypedConfirmDialog{}
	d.InitModal(s, "Confirm", typedConfirmW, typedConfirmH)
	return d
}

// ShowTypedConfirm shows the dialog: message explains the action, required is
// the exact text (case-insensitive, surrounding whitespace ignored) the user
// must type before Confirm proceeds. Sizing follows fitMessage: one line where
// it fits within 2/3 of the screen width, else wrapped and taller, pushing the
// required-text line and input down.
func (d *TypedConfirmDialog) ShowTypedConfirm(title, message, required string, onConfirm func(bool)) {
	d.OnConfirm = onConfirm
	d.show(title, message, required, []string{"Confirm", "Cancel"},
		[]ConfirmAnswer{ConfirmYes, ConfirmNo}, func(a ConfirmAnswer) {
			if d.OnConfirm != nil {
				d.OnConfirm(a == ConfirmYes)
			}
		})
}

// ShowTypedConfirmScript is ShowTypedConfirm with a third button answering
// ConfirmScript, the counterpart of ConfirmDialog.ShowConfirmScript for a delete
// serious enough to be typed out that the user may want to read as SQL first.
// Script is not gated on the typed text: it runs nothing for the retyping to
// protect. Escape still answers No.
func (d *TypedConfirmDialog) ShowTypedConfirmScript(title, message, required string, onAnswer func(ConfirmAnswer)) {
	d.OnConfirm = nil
	d.show(title, message, required, []string{"Confirm", "Cancel", "Script"},
		[]ConfirmAnswer{ConfirmYes, ConfirmNo, ConfirmScript}, onAnswer)
}

func (d *TypedConfirmDialog) show(title, message, required string, buttons []string,
	answers []ConfirmAnswer, onAnswer func(ConfirmAnswer)) {
	d.SetTitle(title)
	d.message = message
	d.required = required
	d.status = ""
	d.input = widgets.NewInputField("", max(20, core.DisplayWidth(required)+16), false)
	d.focus = typedConfirmFocusInput
	d.syncFocus()
	// input is rebuilt above, so a gesture held from the last showing points at a
	// discarded widget and would route every click there.
	d.drag.Clear()
	d.buttons, d.answers = buttons, answers
	d.onAnswer = onAnswer
	w, h, lines := d.fitMessage(message, typedConfirmW, typedConfirmH)
	d.msgLines = lines
	d.SetSize(w, h)
	d.ModalDialog.Show()
}

// Relayout re-wraps the message for the new screen width, then recentres.
func (d *TypedConfirmDialog) Relayout() {
	w, h, lines := d.fitMessage(d.message, typedConfirmW, typedConfirmH)
	d.msgLines = lines
	d.SetSize(w, h)
}

func (d *TypedConfirmDialog) syncFocus() {
	d.input.Focus(d.focus == typedConfirmFocusInput)
}

func (d *TypedConfirmDialog) matched() bool {
	return d.required != "" && strings.EqualFold(strings.TrimSpace(d.input.Value()), d.required)
}

// finish resolves the dialog: every answer proceeds, but a confirm whose typed
// text doesn't match is refused in place with a status message rather than
// treated as a cancel, so the user must fix the input or explicitly back out.
func (d *TypedConfirmDialog) finish(answer ConfirmAnswer) {
	if answer == ConfirmYes && !d.matched() {
		d.status = "Text doesn't match — action not confirmed."
		return
	}
	d.Hide()
	// Read and cleared before it runs: it commonly opens something that can route
	// back here, and a stale handler would fire twice.
	onAnswer := d.onAnswer
	d.onAnswer = nil
	if onAnswer != nil {
		onAnswer(answer)
	}
}

// answerAt is what the button at index i answers.
func (d *TypedConfirmDialog) answerAt(i int) ConfirmAnswer {
	if i < 0 || i >= len(d.answers) {
		return ConfirmNo
	}
	return d.answers[i]
}

// Draw renders the message, the required confirmation text, the input, and
// the showing's button row.
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
	if d.focus != typedConfirmFocusInput {
		activeIdx = int(d.focus) - 1
	}
	d.DrawButtons(s, d.buttons, activeIdx)
}

// HandleKey routes keyboard events.
func (d *TypedConfirmDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.visible {
		return false
	}
	n := typedConfirmFocus(len(d.buttons) + 1)
	switch ev.Key() {
	case tcell.KeyEscape:
		d.finish(ConfirmNo)
		return true
	case tcell.KeyTab:
		d.focus = (d.focus + 1) % n
		d.syncFocus()
		return true
	case tcell.KeyBacktab:
		d.focus = (d.focus + n - 1) % n
		d.syncFocus()
		return true
	case tcell.KeyEnter:
		// Enter with the keyboard in the input answers the question the dialog is
		// about.
		if d.focus == typedConfirmFocusInput {
			d.finish(ConfirmYes)
		} else {
			d.finish(d.answerAt(int(d.focus) - 1))
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
	// A release must reach d.input even when it lands outside the dialog (consumed
	// below), or its next press is swallowed as a continuation of the stale drag.
	if ev.Buttons() == tcell.ButtonNone {
		d.drag.Release(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	// The gesture belongs to the field that claimed the press, so motion is
	// replayed there without hit-testing, ahead of ButtonClicked, which would answer
	// the confirmation when a selection drag wandered onto the button row.
	if d.drag.Replay(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, d.buttons); i >= 0 {
		d.finish(d.answerAt(i))
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
		d.drag.Claim(d.input, ev)
	}
	return true
}

// FocusedClipboardTarget implements core.ClipboardHost: the confirmation
// field while it has focus, nothing while a button does.
func (d *TypedConfirmDialog) FocusedClipboardTarget() core.ClipboardTarget {
	if d.focus == typedConfirmFocusInput {
		return d.input
	}
	return nil
}
