package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// TasksDialog (Tools > Background Tasks) lists every task in the App's
// registry — running or finished — and lets the user cancel the selected
// one if it's still running. Unlike QueryListDialog, it doesn't snapshot
// its list on Show(): it reads a.tasks directly on every Draw, so progress
// updates delivered by App.postProgress while the dialog is open show up
// immediately without needing to be re-shown.
type TasksDialog struct {
	dialogs.ModalDialog
	app    *App
	sel    int
	scroll int
	// btnFocus is the focused button, moved by Tab/Backtab and Left/Right
	// and pressed by Enter, as UpdateDialog's.
	btnFocus int
}

// tasksDialogButtons is the button row, in order; the first is the default.
var tasksDialogButtons = []string{"Cancel Task", "Close"}

// NewTasksDialog creates the dialog.
func NewTasksDialog(app *App) *TasksDialog {
	d := &TasksDialog{app: app}
	d.InitModal(app.screen, "Background Tasks", 64, 16)
	return d
}

// Show resets selection/scroll to the top and displays the dialog.
func (d *TasksDialog) Show() {
	d.sel, d.scroll, d.btnFocus = 0, 0, 0
	d.ModalDialog.Show()
}

// Draw renders the task list.
func (d *TasksDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	inner := d.InnerRect()
	dataH := d.dataH()

	tasks := d.app.tasks
	if len(tasks) == 0 {
		msgStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		core.DrawText(s, inner.X+1, inner.Y+1, msgStyle, "(no background tasks)")
	}

	for row := range dataH {
		idx := d.scroll + row
		if idx >= len(tasks) {
			break
		}
		y := inner.Y + 1 + row
		st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
		if idx == d.sel {
			st = theme.StyleSelected()
		}
		core.FillRect(s, core.Rect{X: inner.X, Y: y, W: inner.W, H: 1}, ' ', st)
		core.DrawTextClipped(s, inner.X+1, y, inner.W-2, st, tasks[idx].statusText())
	}

	if len(tasks) > dataH {
		d.DrawContentScrollbar(s, inner.Y+1, dataH, len(tasks), d.scroll)
	}

	d.DrawButtons(s, tasksDialogButtons, d.btnFocus)
}

// HandleKey processes keyboard events.
func (d *TasksDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	dataH := d.dataH()
	switch ev.Key() {
	case tcell.KeyEscape:
		d.Hide()
	case tcell.KeyUp:
		if d.sel > 0 {
			d.sel--
			d.ensureVisible(dataH)
		}
	case tcell.KeyDown:
		if d.sel < len(d.app.tasks)-1 {
			d.sel++
			d.ensureVisible(dataH)
		}
	case tcell.KeyEnter:
		d.press()
	case tcell.KeyTab, tcell.KeyRight:
		d.btnFocus = (d.btnFocus + 1) % len(tasksDialogButtons)
	case tcell.KeyBacktab, tcell.KeyLeft:
		d.btnFocus = (d.btnFocus - 1 + len(tasksDialogButtons)) % len(tasksDialogButtons)
	default:
		return false
	}
	return true
}

// press runs the focused button.
func (d *TasksDialog) press() {
	if d.btnFocus == 0 {
		d.cancelSelected()
	} else {
		d.Hide()
	}
}

// HandleMouse processes mouse events.
func (d *TasksDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, tasksDialogButtons); i >= 0 {
		d.btnFocus = i
		d.press()
		return true
	}
	inner := d.InnerRect()
	dataH := d.dataH()
	if d.ScrollbarDrag(ev, d.Rect().Right()-1, inner.Y+1, dataH, len(d.app.tasks), &d.scroll) {
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return true
	}
	mx, my := ev.Position()
	if mx >= inner.X && mx < inner.X+inner.W {
		row := my - (inner.Y + 1)
		if row >= 0 && row < dataH {
			if idx := d.scroll + row; idx >= 0 && idx < len(d.app.tasks) {
				d.sel = idx
			}
		}
	}
	return true
}

// dataH is the number of task rows drawn: those between the top padding row
// and the button row (ButtonRowY). It was InnerRect().H-2, one too many, so the
// button row overdrew the last row and the final task of a full list could
// never be scrolled into view (as HelpDialog.dataH).
func (d *TasksDialog) dataH() int { return d.ButtonRowY() - d.InnerRect().Y - 1 }

func (d *TasksDialog) ensureVisible(dataH int) {
	d.scroll = scrollToShow(d.sel, d.scroll, dataH)
}

// cancelSelected cancels the selected task, if any and still running —
// Task.Cancel is a safe no-op on one that's already finished.
func (d *TasksDialog) cancelSelected() {
	if d.sel < 0 || d.sel >= len(d.app.tasks) {
		return
	}
	d.app.tasks[d.sel].Cancel()
}
