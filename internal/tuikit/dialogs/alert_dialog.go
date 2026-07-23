package dialogs

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// AlertDialog — single-button info message
// ---------------------------------------------------------------------------

// alertDialogMinW/alertDialogBaseH are AlertDialog's original fixed size —
// now the floor fitMessage never shrinks below, and the height with the
// message on a single line.
const (
	alertDialogMinW  = 44
	alertDialogBaseH = 9
)

// AlertDialog shows a message with a single OK button.
type AlertDialog struct {
	ModalDialog
	message  string
	msgLines []string
}

// NewAlertDialog creates an AlertDialog.
func NewAlertDialog(s tcell.Screen) *AlertDialog {
	d := new(AlertDialog{})
	d.InitModal(s, "Alert", alertDialogMinW, alertDialogBaseH)
	return d
}

// ShowAlert shows a message. The dialog grows to show it on one line where
// that fits within 2/3 of the screen's width, or word-wraps onto more
// lines (growing taller instead) when it doesn't — see fitMessage.
func (d *AlertDialog) ShowAlert(title, message string) {
	d.SetTitle(title)
	d.message = message
	w, h, lines := d.fitMessage(message, alertDialogMinW, alertDialogBaseH)
	d.msgLines = lines
	d.SetSize(w, h)
	d.ModalDialog.Show()
}

// Draw renders the alert.
func (d *AlertDialog) Draw(s tcell.Screen) {
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
	d.DrawButtons(s, []string{"OK"}, 0)
}

// HandleKey handles keyboard events.
func (d *AlertDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.visible {
		return false
	}
	if ev.Key() == tcell.KeyEscape || ev.Key() == tcell.KeyEnter {
		d.Hide()
	}
	return true
}

// HandleMouse handles mouse events.
func (d *AlertDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.visible {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if d.ButtonClicked(ev, []string{"OK"}) == 0 {
		d.Hide()
	}
	return true
}
