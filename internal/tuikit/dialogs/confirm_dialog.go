package dialogs

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// ---------------------------------------------------------------------------
// ConfirmDialog — two-button yes/no
// ---------------------------------------------------------------------------

// confirmDialogMinW/confirmDialogBaseH are ConfirmDialog's original fixed
// size — now the floor fitMessage never shrinks below, and the height
// with the message on a single line.
const (
	confirmDialogMinW  = 78
	confirmDialogBaseH = 9
)

// ConfirmAnswer is how a prompt with more than a Yes and a No was answered.
type ConfirmAnswer int

const (
	ConfirmYes ConfirmAnswer = iota
	ConfirmNo
	ConfirmCancel
	// ConfirmScript is a third way out of a question about a write: neither
	// doing it nor abandoning it, but asking for the statements it would have
	// run. The caller opens them somewhere the user can read them, and the
	// question is over — the dialog closes as it does for any other answer.
	ConfirmScript
)

// ConfirmDialog shows a question with Yes and No buttons, or — via
// ShowConfirmCancel — Yes, No and Cancel.
type ConfirmDialog struct {
	ModalDialog
	message  string
	msgLines []string
	btnFocus int

	// option is the optional checkbox a ShowConfirmOption showing carries —
	// one extra decision that belongs to the question rather than to a dialog
	// of its own ("also drop the foreign keys that reference it"). nil for
	// every other showing. optFocused puts the keyboard on it, ahead of the
	// buttons in the Tab cycle.
	option     *widgets.CheckBox
	optFocused bool

	// buttons is what the current showing renders and hit-tests, so Draw
	// and HandleMouse can't disagree about how many there are, and answers is
	// what each of them means. The two are parallel rather than the answer
	// being the button's index: a showing with a Script button has three
	// buttons and no Cancel, so index 2 is ConfirmScript there and
	// ConfirmCancel on a three-way prompt.
	buttons  []string
	answers  []ConfirmAnswer
	escape   ConfirmAnswer
	onAnswer func(ConfirmAnswer)
}

// The button sets a showing can use, each with the answers its buttons mean.
var (
	twoButtons    = []string{"Yes", "No"}
	threeButtons  = []string{"Yes", "No", "Cancel"}
	scriptButtons = []string{"Yes", "No", "Script"}

	twoAnswers    = []ConfirmAnswer{ConfirmYes, ConfirmNo}
	threeAnswers  = []ConfirmAnswer{ConfirmYes, ConfirmNo, ConfirmCancel}
	scriptAnswers = []ConfirmAnswer{ConfirmYes, ConfirmNo, ConfirmScript}
)

// NewConfirmDialog creates a ConfirmDialog.
func NewConfirmDialog(s tcell.Screen) *ConfirmDialog {
	d := new(ConfirmDialog{})
	d.InitModal(s, "Confirm", confirmDialogMinW, confirmDialogBaseH)
	return d
}

// ShowConfirm shows a Yes/No question. Escape answers No, so this is only
// safe for a question whose No is the harmless answer — every current
// caller's is ("Discard changes?", "Take database offline?", …). A question
// where No is itself destructive wants ShowConfirmCancel instead.
//
// The dialog grows to show message on one line where that fits within 2/3
// of the screen's width, or word-wraps onto more lines (growing taller
// instead) when it doesn't — see fitMessage.
func (d *ConfirmDialog) ShowConfirm(title, message string, onConfirm func(bool)) {
	d.option = nil
	d.show(title, message, twoButtons, twoAnswers, ConfirmNo, func(a ConfirmAnswer) {
		onConfirm(a == ConfirmYes)
	})
}

// ShowConfirmOption is ShowConfirm with one checkbox above the buttons, whose
// state is reported alongside the answer. It exists for a question that has a
// single modifier rather than a second question — SSMS's own Delete Object
// dialog carries its "close existing connections" the same way — and keeps
// that modifier where the consequence is described instead of behind another
// prompt.
//
// The checkbox leads the Tab cycle and is reported as it stands when an
// answer is given, including for a No: a caller reads it only when the answer
// is yes.
func (d *ConfirmDialog) ShowConfirmOption(title, message, optionLabel string, initial bool, onConfirm func(confirmed, checked bool)) {
	box := widgets.NewCheckBox(optionLabel)
	box.SetChecked(initial)
	d.option = box
	d.setOptFocused(false)
	d.show(title, message, twoButtons, twoAnswers, ConfirmNo, func(a ConfirmAnswer) {
		onConfirm(a == ConfirmYes, box.Checked())
	})
}

// ShowConfirmCancel shows a Yes/No/Cancel question, with Escape answering
// Cancel. For a question where both Yes and No commit to something — "Save
// before closing?", where No discards unsaved work — Escape must not pick
// either, and the user needs a way to back out of having asked at all.
func (d *ConfirmDialog) ShowConfirmCancel(title, message string, onAnswer func(ConfirmAnswer)) {
	d.option = nil
	d.show(title, message, threeButtons, threeAnswers, ConfirmCancel, onAnswer)
}

// ShowConfirmScript is ShowConfirm with a third button that answers
// ConfirmScript — for a question about a write the user may want to read as SQL
// instead of running. Escape still answers No: Script commits to opening a query
// window, so it must be asked for.
//
// optionLabel adds the ShowConfirmOption checkbox when it isn't empty, and the
// answer carries its state for the same reason the option form does — the
// checkbox changes the statements, so a script that ignored it would not be the
// script the Yes would have run.
func (d *ConfirmDialog) ShowConfirmScript(title, message, optionLabel string, initial bool, onAnswer func(a ConfirmAnswer, checked bool)) {
	d.option = nil
	checked := func() bool { return false }
	if optionLabel != "" {
		box := widgets.NewCheckBox(optionLabel)
		box.SetChecked(initial)
		d.option = box
		checked = box.Checked
	}
	d.setOptFocused(false)
	d.show(title, message, scriptButtons, scriptAnswers, ConfirmNo, func(a ConfirmAnswer) {
		onAnswer(a, checked())
	})
}

func (d *ConfirmDialog) show(title, message string, buttons []string, answers []ConfirmAnswer,
	escape ConfirmAnswer, onAnswer func(ConfirmAnswer)) {
	d.SetTitle(title)
	d.message = message
	d.btnFocus = 0
	d.buttons = buttons
	d.answers = answers
	d.escape = escape
	d.onAnswer = onAnswer
	w, h, lines := d.fitMessage(message, confirmDialogMinW, confirmDialogBaseH)
	d.msgLines = lines
	d.SetSize(w, h+d.optionHeight())
	d.ModalDialog.Show()
}

// Relayout re-wraps the message for the new screen width, then recentres.
func (d *ConfirmDialog) Relayout() {
	w, h, lines := d.fitMessage(d.message, confirmDialogMinW, confirmDialogBaseH)
	d.msgLines = lines
	d.SetSize(w, h+d.optionHeight())
}

// optionHeight is the extra height a showing with a checkbox needs: a blank
// line and the box itself.
func (d *ConfirmDialog) optionHeight() int {
	if d.option == nil {
		return 0
	}
	return 2
}

// Draw renders the confirm dialog.
func (d *ConfirmDialog) Draw(s tcell.Screen) {
	if !d.visible {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	msgStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	inner := d.InnerRect()
	contentW := inner.W - 2
	for i, line := range d.msgLines {
		x := inner.X + 1 + core.CenterOffset(contentW, core.DisplayWidth(line))
		core.DrawTextClipped(s, x, inner.Y+2+i, contentW, msgStyle, line)
	}
	if d.option != nil {
		d.option.SetBounds(inner.X+1, inner.Y+2+len(d.msgLines)+1)
		d.option.Draw(s)
	}
	d.DrawSeparator(s)
	d.DrawButtons(s, d.buttons, d.btnFocus)
}

// HandleKey handles keyboard events. Escape answers whatever the showing named
// as its way out — Cancel on a three-way prompt, No everywhere else, the closest
// thing to "I didn't mean to ask" each button set has.
func (d *ConfirmDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.visible {
		return false
	}
	n := len(d.buttons)
	// The checkbox gets the key first while it has focus, so Space and Enter
	// toggle it rather than answering the question — an Enter that answered
	// while the box was focused would commit the state the user was still
	// setting.
	if d.optFocused && d.option != nil && d.option.HandleKey(ev) {
		return true
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		d.finish(d.escape)
	case tcell.KeyEnter:
		d.finish(d.answerAt(d.btnFocus))
	case tcell.KeyTab, tcell.KeyRight:
		d.focusNext(n, 1)
	case tcell.KeyLeft, tcell.KeyBacktab:
		d.focusNext(n, -1)
	}
	return true
}

// focusNext moves the keyboard one step through the checkbox (when there is
// one) and the buttons, in either direction.
func (d *ConfirmDialog) focusNext(n, step int) {
	if d.option == nil {
		d.btnFocus = (d.btnFocus + step + n) % n
		return
	}
	// Positions 0..n-1 are the buttons and n is the checkbox, so the cycle is
	// one longer than the button set.
	cur := d.btnFocus
	if d.optFocused {
		cur = n
	}
	cur = (cur + step + n + 1) % (n + 1)
	d.setOptFocused(cur == n)
	if !d.optFocused {
		d.btnFocus = cur
	}
}

// setOptFocused moves the keyboard onto or off the checkbox. The widget's own
// focus flag is set here rather than left to Draw: CheckBox.HandleKey refuses
// every key while it is unfocused, so a dialog that only told it at draw time
// would drop the first Space of a showing that had not been drawn yet.
func (d *ConfirmDialog) setOptFocused(v bool) {
	d.optFocused = v
	if d.option != nil {
		d.option.Focus(v)
	}
}

// HandleMouse handles mouse events.
func (d *ConfirmDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.visible {
		return false
	}
	// A release must reach the checkbox even when it lands outside the dialog
	// (consumed below) — otherwise its mouseDragging latch survives the
	// gesture and swallows the next press as a continuation of it. CheckBox
	// returns false on ButtonNone, so this only resets the latch.
	if ev.Buttons() == tcell.ButtonNone && d.option != nil {
		d.option.HandleMouse(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	// The checkbox is offered the press before the buttons are hit-tested,
	// and takes focus with it so the keyboard is where the pointer just was.
	if d.option != nil && d.option.HandleMouse(ev) {
		d.setOptFocused(true)
		return true
	}
	if i := d.ButtonClicked(ev, d.buttons); i >= 0 {
		d.finish(d.answerAt(i))
	}
	return true
}

// answerAt is what the button at index i answers.
func (d *ConfirmDialog) answerAt(i int) ConfirmAnswer {
	if i < 0 || i >= len(d.answers) {
		return d.escape
	}
	return d.answers[i]
}

// finish hides the dialog and reports answer. The handler is read and
// cleared before it runs: it commonly opens another dialog (a Save As file
// dialog, say) that can itself route back here, and a stale handler left
// installed would then be fired a second time by the next Escape.
func (d *ConfirmDialog) finish(answer ConfirmAnswer) {
	d.Hide()
	onAnswer := d.onAnswer
	d.onAnswer = nil
	if onAnswer != nil {
		onAnswer(answer)
	}
}
