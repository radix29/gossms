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

// typedConfirmFocus tracks which of the dialog's focusable elements has focus:
// the input, then one position per button — so focus-1 indexes d.buttons, and a
// showing with a Script button adds a position rather than renumbering these.
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

	// drag owns the text-selection gesture a press in input starts. See
	// FieldGesture for why its three calls sit where they do in HandleMouse.
	drag FieldGesture

	// buttons is what the current showing renders and hit-tests, and answers
	// what each of them means — parallel rather than the answer being the
	// button's index, for the reason ConfirmDialog's pair are.
	buttons []string
	answers []ConfirmAnswer

	// onAnswer is the showing's handler. OnConfirm is the two-button form's,
	// kept as the exported field it has always been.
	onAnswer  func(ConfirmAnswer)
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
	d.OnConfirm = onConfirm
	d.show(title, message, required, []string{"Confirm", "Cancel"},
		[]ConfirmAnswer{ConfirmYes, ConfirmNo}, func(a ConfirmAnswer) {
			if d.OnConfirm != nil {
				d.OnConfirm(a == ConfirmYes)
			}
		})
}

// ShowTypedConfirmScript is ShowTypedConfirm with a third button answering
// ConfirmScript — the retype-to-confirm counterpart of
// ConfirmDialog.ShowConfirmScript, for a delete serious enough to be typed out
// that the user may want to read as SQL first.
//
// Script is not gated on the typed text: it runs nothing, so there is nothing
// for the retyping to protect. Escape still answers No.
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
	// input is rebuilt above, so a gesture held from the last showing points
	// at a discarded widget — and would route every click there.
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

// finish resolves the dialog: every other answer proceeds, but a confirm whose
// typed text doesn't match is refused in place, with a status message,
// rather than treated as a cancel — the user should have to either fix
// their input or explicitly back out.
func (d *TypedConfirmDialog) finish(answer ConfirmAnswer) {
	if answer == ConfirmYes && !d.matched() {
		d.status = "Text doesn't match — action not confirmed."
		return
	}
	d.Hide()
	// Read and cleared before it runs: it commonly opens something else that can
	// route back here, and a stale handler would then fire a second time.
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
		// Enter with the keyboard still in the input answers the question the
		// dialog is about, as it did when the buttons were Confirm and Cancel.
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
	// A release must reach d.input even when it lands outside the dialog
	// (consumed below) — otherwise its next press is swallowed as a
	// continuation of the stale drag.
	if ev.Buttons() == tcell.ButtonNone {
		d.drag.Release(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	// The gesture belongs to the field that claimed its press, so motion is
	// replayed there without hit-testing — ahead of ButtonClicked below,
	// which would otherwise answer the confirmation the moment a selection
	// drag in the retype field wandered down onto the button row.
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
