package dialogs

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
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

// ConfirmAnswer is how a three-way ShowConfirmCancel prompt was answered.
type ConfirmAnswer int

const (
	ConfirmYes ConfirmAnswer = iota
	ConfirmNo
	ConfirmCancel
)

// ConfirmDialog shows a question with Yes and No buttons, or — via
// ShowConfirmCancel — Yes, No and Cancel.
type ConfirmDialog struct {
	ModalDialog
	message  string
	msgLines []string
	btnFocus int

	// buttons is what the current showing renders and hit-tests, so Draw
	// and HandleMouse can't disagree about how many there are.
	buttons  []string
	onAnswer func(ConfirmAnswer)
}

// twoButtons and threeButtons are the two button sets a showing can use.
var (
	twoButtons   = []string{"Yes", "No"}
	threeButtons = []string{"Yes", "No", "Cancel"}
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
	d.show(title, message, twoButtons, func(a ConfirmAnswer) {
		onConfirm(a == ConfirmYes)
	})
}

// ShowConfirmCancel shows a Yes/No/Cancel question, with Escape answering
// Cancel. For a question where both Yes and No commit to something — "Save
// before closing?", where No discards unsaved work — Escape must not pick
// either, and the user needs a way to back out of having asked at all.
func (d *ConfirmDialog) ShowConfirmCancel(title, message string, onAnswer func(ConfirmAnswer)) {
	d.show(title, message, threeButtons, onAnswer)
}

func (d *ConfirmDialog) show(title, message string, buttons []string, onAnswer func(ConfirmAnswer)) {
	d.SetTitle(title)
	d.message = message
	d.btnFocus = 0
	d.buttons = buttons
	d.onAnswer = onAnswer
	w, h, lines := d.fitMessage(message, confirmDialogMinW, confirmDialogBaseH)
	d.msgLines = lines
	d.SetSize(w, h)
	d.ModalDialog.Show()
}

// Relayout re-wraps the message for the new screen width, then recentres.
func (d *ConfirmDialog) Relayout() {
	w, h, lines := d.fitMessage(d.message, confirmDialogMinW, confirmDialogBaseH)
	d.msgLines = lines
	d.SetSize(w, h)
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
	d.DrawSeparator(s)
	d.DrawButtons(s, d.buttons, d.btnFocus)
}

// HandleKey handles keyboard events. Escape answers Cancel on a three-way
// prompt and No on a two-button one — the closest thing to "I didn't mean
// to ask" each button set has.
func (d *ConfirmDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.visible {
		return false
	}
	n := len(d.buttons)
	switch ev.Key() {
	case tcell.KeyEscape:
		d.finish(ConfirmAnswer(n - 1))
	case tcell.KeyEnter:
		d.finish(ConfirmAnswer(d.btnFocus))
	case tcell.KeyTab, tcell.KeyRight:
		d.btnFocus = (d.btnFocus + 1) % n
	case tcell.KeyLeft:
		d.btnFocus = (d.btnFocus - 1 + n) % n
	}
	return true
}

// HandleMouse handles mouse events.
func (d *ConfirmDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.visible {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, d.buttons); i >= 0 {
		d.finish(ConfirmAnswer(i))
	}
	return true
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
