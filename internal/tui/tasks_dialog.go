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
}

// NewTasksDialog creates the dialog.
func NewTasksDialog(app *App) *TasksDialog {
	d := &TasksDialog{app: app}
	d.InitModal(app.screen, "Background Tasks", 64, 16)
	return d
}

// Show resets selection/scroll to the top and displays the dialog.
func (d *TasksDialog) Show() {
	d.sel, d.scroll = 0, 0
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
	dataH := inner.H - 2 // leave room for the button row

	tasks := d.app.tasks
	if len(tasks) == 0 {
		msgStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		core.DrawText(s, inner.X+1, inner.Y+1, msgStyle, "(no background tasks)")
	}

	for row := 0; row < dataH; row++ {
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
		sbStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		core.DrawScrollbar(s, d.Rect().Right()-1, inner.Y+1, dataH, len(tasks), dataH, d.scroll, sbStyle, sbThumb)
	}

	d.DrawButtons(s, []string{"Cancel Task", "Close"}, 0)
}

// HandleKey processes keyboard events.
func (d *TasksDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	dataH := d.InnerRect().H - 2
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
		d.cancelSelected()
	}
	return true
}

// HandleMouse processes mouse events.
func (d *TasksDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, []string{"Cancel Task", "Close"}); i >= 0 {
		if i == 0 {
			d.cancelSelected()
		} else {
			d.Hide()
		}
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return true
	}
	mx, my := ev.Position()
	inner := d.InnerRect()
	dataH := inner.H - 2
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

func (d *TasksDialog) ensureVisible(dataH int) {
	if d.sel < d.scroll {
		d.scroll = d.sel
	}
	if d.sel >= d.scroll+dataH {
		d.scroll = d.sel - dataH + 1
	}
}

// cancelSelected cancels the selected task, if any and still running —
// Task.Cancel is a safe no-op on one that's already finished.
func (d *TasksDialog) cancelSelected() {
	if d.sel < 0 || d.sel >= len(d.app.tasks) {
		return
	}
	d.app.tasks[d.sel].Cancel()
}
