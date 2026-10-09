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

// confirmDialogMinW/confirmDialogBaseH are ConfirmDialog's floor size for
// fitMessage and its height with a one-line message.
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
	// ConfirmScript is a third way out of a question about a write: neither doing it
	// nor abandoning it, but asking for the statements it would have run. The
	// caller opens them for reading; the dialog closes as for any answer.
	ConfirmScript
)

// ConfirmDialog shows a question with Yes and No buttons, or — via
// ShowConfirmCancel — Yes, No and Cancel.
type ConfirmDialog struct {
	ModalDialog
	message  string
	msgLines []string
	btnFocus int

	// option is the optional checkbox a ShowConfirmOption showing carries: one extra
	// decision belonging to the question ("also drop the foreign keys that
	// reference it"); nil otherwise. optFocused puts the keyboard on it, ahead of
	// the buttons in the Tab cycle.
	option     *widgets.CheckBox
	optFocused bool

	// buttons is what the showing renders and hit-tests, so Draw and HandleMouse
	// can't disagree on the count; answers is what each means. They are parallel,
	// not the button index: a Script showing has three buttons and no Cancel, so
	// index 2 is ConfirmScript there and ConfirmCancel on a three-way prompt.
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

// Message returns the question the dialog asks.
func (d *ConfirmDialog) Message() string { return d.message }

// ShowConfirm shows a Yes/No question. Escape answers No, so use it only where
// No is the harmless answer (every current caller's is: "Discard changes?",
// "Take database offline?"); where No is itself destructive use
// ShowConfirmCancel. The dialog sizes as ShowAlert does (see fitMessage).
func (d *ConfirmDialog) ShowConfirm(title, message string, onConfirm func(bool)) {
	d.option = nil
	d.show(title, message, twoButtons, twoAnswers, ConfirmNo, func(a ConfirmAnswer) {
		onConfirm(a == ConfirmYes)
	})
}

// ShowConfirmDefaultNo is ShowConfirm opening with No focused, for a Yes the
// user should have to choose rather than Enter through. Escape still answers No.
func (d *ConfirmDialog) ShowConfirmDefaultNo(title, message string, onConfirm func(bool)) {
	d.ShowConfirm(title, message, onConfirm)
	d.btnFocus = 1
}

// ShowConfirmOption is ShowConfirm with one checkbox above the buttons, whose
// state is reported with the answer: for a question with a single modifier
// rather than a second question (SSMS's Delete Object dialog carries "close
// existing connections" the same way), kept where the consequence is described.
//
// The checkbox leads the Tab cycle and is reported as it stands for any answer,
// including No; a caller reads it only on yes.
func (d *ConfirmDialog) ShowConfirmOption(title, message, optionLabel string, initial bool, onConfirm func(confirmed, checked bool)) {
	box := widgets.NewCheckBox(optionLabel)
	box.SetChecked(initial)
	d.option = box
	d.setOptFocused(false)
	d.show(title, message, twoButtons, twoAnswers, ConfirmNo, func(a ConfirmAnswer) {
		onConfirm(a == ConfirmYes, box.Checked())
	})
}

// ShowConfirmCancel shows a Yes/No/Cancel question, Escape answering Cancel. For
// a question where both Yes and No commit ("Save before closing?", No discards
// work), Escape must pick neither and the user needs a way to back out.
func (d *ConfirmDialog) ShowConfirmCancel(title, message string, onAnswer func(ConfirmAnswer)) {
	d.option = nil
	d.show(title, message, threeButtons, threeAnswers, ConfirmCancel, onAnswer)
}

// ShowConfirmScript is ShowConfirm with a third button answering ConfirmScript,
// for a write the user may want to read as SQL instead of running. Escape still
// answers No: Script commits to opening a query window, so it must be asked for.
//
// optionLabel, when not empty, adds the ShowConfirmOption checkbox, and the
// answer carries its state: it changes the statements, so a script ignoring it
// would not be what Yes would have run.
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

// HandleKey handles keyboard events. Escape answers the showing's way out:
// Cancel on a three-way prompt, No otherwise.
func (d *ConfirmDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.visible {
		return false
	}
	n := len(d.buttons)
	// The checkbox gets the key first while focused, so Space and Enter toggle it:
	// an Enter that answered would commit the state the user was still setting.
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
	// Positions 0..n-1 are the buttons and n the checkbox, so the cycle is one
	// longer than the button set.
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

// setOptFocused moves the keyboard onto or off the checkbox. The widget's focus
// flag is set here, not left to Draw: CheckBox.HandleKey refuses every key while
// unfocused, so the first Space of a not-yet-drawn showing would be dropped.
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
	// (consumed below), or its mouseDragging latch survives and swallows the next
	// press. CheckBox returns false on ButtonNone, so this only resets the latch.
	if ev.Buttons() == tcell.ButtonNone && d.option != nil {
		d.option.HandleMouse(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	// The checkbox is offered the press before the buttons are hit-tested, and takes
	// focus with it so the keyboard is where the pointer just was.
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

// finish hides the dialog and reports answer. The handler is read and cleared
// first: it commonly opens another dialog (a Save As file dialog) that can route
// back here, and a stale handler would fire again on the next Escape.
func (d *ConfirmDialog) finish(answer ConfirmAnswer) {
	d.Hide()
	onAnswer := d.onAnswer
	d.onAnswer = nil
	if onAnswer != nil {
		onAnswer(answer)
	}
}
