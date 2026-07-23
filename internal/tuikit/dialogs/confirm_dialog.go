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

// ConfirmDialog shows a question with Yes and No buttons.
type ConfirmDialog struct {
	ModalDialog
	message   string
	msgLines  []string
	btnFocus  int
	OnConfirm func(confirmed bool)
}

// NewConfirmDialog creates a ConfirmDialog.
func NewConfirmDialog(s tcell.Screen) *ConfirmDialog {
	d := new(ConfirmDialog{})
	d.InitModal(s, "Confirm", confirmDialogMinW, confirmDialogBaseH)
	return d
}

// ShowConfirm shows the dialog with the given title, message, and callback.
// The dialog grows to show message on one line where that fits within 2/3
// of the screen's width, or word-wraps onto more lines (growing taller
// instead) when it doesn't — see fitMessage.
func (d *ConfirmDialog) ShowConfirm(title, message string, onConfirm func(bool)) {
	d.SetTitle(title)
	d.message = message
	d.btnFocus = 0
	d.OnConfirm = onConfirm
	w, h, lines := d.fitMessage(message, confirmDialogMinW, confirmDialogBaseH)
	d.msgLines = lines
	d.SetSize(w, h)
	d.ModalDialog.Show()
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
	d.DrawButtons(s, []string{"Yes", "No"}, d.btnFocus)
}

// HandleKey handles keyboard events.
func (d *ConfirmDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.visible {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		d.finish(false)
	case tcell.KeyEnter:
		d.finish(d.btnFocus == 0)
	case tcell.KeyTab, tcell.KeyRight:
		d.btnFocus = (d.btnFocus + 1) % 2
	case tcell.KeyLeft:
		d.btnFocus = (d.btnFocus - 1 + 2) % 2
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
	if i := d.ButtonClicked(ev, []string{"Yes", "No"}); i >= 0 {
		d.finish(i == 0)
	}
	return true
}

func (d *ConfirmDialog) finish(confirmed bool) {
	d.Hide()
	if d.OnConfirm != nil {
		d.OnConfirm(confirmed)
	}
}
