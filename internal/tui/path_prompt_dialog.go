package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// PathPromptDialog is a small modal for entering a single file-system
// path, reused by File > Save/Save As and Query > Results To File.
type PathPromptDialog struct {
	dialogs.ModalDialog
	field     *widgets.InputField
	btnFocus  int
	OnConfirm func(path string)
}

// NewPathPromptDialog creates the dialog.
func NewPathPromptDialog(app *App) *PathPromptDialog {
	d := &PathPromptDialog{}
	d.InitModal(app.screen, "Save", 66, 9)
	d.field = widgets.NewInputField("Path:", 56, false)
	return d
}

// Prompt configures and shows the dialog. onConfirm is called with the
// entered path when the user presses OK/Enter; it is not called on Cancel.
func (d *PathPromptDialog) Prompt(title, initial string, onConfirm func(path string)) {
	d.SetTitle(title)
	d.field.SetValue(initial)
	d.field.Focus(true)
	d.btnFocus = 0
	d.OnConfirm = onConfirm
	d.ModalDialog.Show()
}

// Draw renders the dialog.
func (d *PathPromptDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	inner := d.InnerRect()
	d.field.SetBounds(inner.X+1, inner.Y+2)
	d.field.Draw(s)
	d.DrawSeparator(s)
	d.DrawButtons(s, []string{"OK", "Cancel"}, d.btnFocus)
}

// HandleKey processes keyboard events.
func (d *PathPromptDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		d.Hide()
		return true
	case tcell.KeyEnter:
		d.confirm()
		return true
	case tcell.KeyTab:
		d.btnFocus = (d.btnFocus + 1) % 2
		return true
	}
	d.field.HandleKey(ev)
	return true
}

// HandleMouse processes mouse events.
func (d *PathPromptDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, []string{"OK", "Cancel"}); i >= 0 {
		if i == 0 {
			d.confirm()
		} else {
			d.Hide()
		}
		return true
	}
	d.field.HandleMouse(ev)
	return true
}

func (d *PathPromptDialog) confirm() {
	path := d.field.Value()
	d.Hide()
	if path != "" && d.OnConfirm != nil {
		d.OnConfirm(path)
	}
}
