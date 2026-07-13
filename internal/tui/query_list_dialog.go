package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// QueryListDialog lists every currently open query panel and lets the
// user jump straight to one (Tools > Query List).
type QueryListDialog struct {
	dialogs.ModalDialog
	app     *App
	indices []int // a.panels index for each listed row
	titles  []string
	sel     int
	scroll  int
}

// NewQueryListDialog creates the dialog.
func NewQueryListDialog(app *App) *QueryListDialog {
	d := &QueryListDialog{app: app}
	d.InitModal(app.screen, "Query List", 56, 16)
	return d
}

// Show rebuilds the list from the current panels and displays the dialog.
func (d *QueryListDialog) Show() {
	d.indices = d.indices[:0]
	d.titles = d.titles[:0]
	for i := 0; i < d.app.panels.Count(); i++ {
		qp, ok := d.app.panels.PanelAt(i).(*QueryPanel)
		if !ok {
			continue
		}
		label := qp.Title()
		if qp.conn != nil {
			label += "  [" + qp.conn.Opts.Server + "]"
		}
		if qp.filePath != "" {
			label += "  — " + qp.filePath
		}
		d.indices = append(d.indices, i)
		d.titles = append(d.titles, label)
	}
	d.sel = 0
	d.scroll = 0
	d.ModalDialog.Show()
}

// Draw renders the query list.
func (d *QueryListDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	inner := d.InnerRect()
	dataH := inner.H - 2 // leave room for the button row

	if len(d.titles) == 0 {
		msgStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		core.DrawText(s, inner.X+1, inner.Y+1, msgStyle, "(no open queries)")
	}

	for row := 0; row < dataH; row++ {
		idx := d.scroll + row
		if idx >= len(d.titles) {
			break
		}
		y := inner.Y + 1 + row
		st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
		if idx == d.sel {
			st = theme.StyleSelected()
		}
		core.FillRect(s, core.Rect{X: inner.X, Y: y, W: inner.W, H: 1}, ' ', st)
		core.DrawTextClipped(s, inner.X+1, y, inner.W-2, st, d.titles[idx])
	}

	if len(d.titles) > dataH {
		sbStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		core.DrawScrollbar(s, d.Rect().Right()-1, inner.Y+1, dataH, len(d.titles), dataH, d.scroll, sbStyle, sbThumb)
	}

	d.DrawButtons(s, []string{"Switch To", "Close"}, 0)
}

// HandleKey processes keyboard events.
func (d *QueryListDialog) HandleKey(ev *tcell.EventKey) bool {
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
		if d.sel < len(d.titles)-1 {
			d.sel++
			d.ensureVisible(dataH)
		}
	case tcell.KeyEnter:
		d.activate()
	}
	return true
}

// HandleMouse processes mouse events.
func (d *QueryListDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, []string{"Switch To", "Close"}); i >= 0 {
		if i == 0 {
			d.activate()
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
			idx := d.scroll + row
			if idx >= 0 && idx < len(d.titles) {
				d.sel = idx
				d.activate()
			}
		}
	}
	return true
}

func (d *QueryListDialog) ensureVisible(dataH int) {
	if d.sel < d.scroll {
		d.scroll = d.sel
	}
	if d.sel >= d.scroll+dataH {
		d.scroll = d.sel - dataH + 1
	}
}

func (d *QueryListDialog) activate() {
	if d.sel < 0 || d.sel >= len(d.indices) {
		return
	}
	d.app.panels.SetActive(d.indices[d.sel])
	d.app.focusPanels()
	d.Hide()
}
