package dialogs

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// AlertDialog — single-button info message
// ---------------------------------------------------------------------------

// AlertDialog shows a message with a single OK button.
type AlertDialog struct {
	ModalDialog
	message string
}

// NewAlertDialog creates an AlertDialog.
func NewAlertDialog(s tcell.Screen) *AlertDialog {
	d := new(AlertDialog{})
	d.InitModal(s, "Alert", 44, 9)
	return d
}

// ShowAlert shows a message.
func (d *AlertDialog) ShowAlert(title, message string) {
	d.SetTitle(title)
	d.message = message
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
	core.DrawTextClipped(s, inner.X+1, inner.Y+2, inner.W-2, msgStyle, d.message)
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
