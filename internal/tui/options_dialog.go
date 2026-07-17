package tui

import (
	"strconv"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// optionsZone is which control on the Options dialog currently has
// keyboard focus.
type optionsZone int

const (
	zoneIconStyle optionsZone = iota
	zoneMaxCellLen
	zoneMaxResultRows
	zoneIntelliSense
	zoneOptButtons
)

// OptionsDialog is the application's Options/Settings dialog, reachable
// from Tools > Options.
type OptionsDialog struct {
	dialogs.ModalDialog
	app *App

	rbIconStyle    *widgets.RadioBox
	fMaxCellLen    *widgets.InputField
	fMaxResultRows *widgets.InputField
	cbIntelliSense *widgets.CheckBox

	zone     optionsZone
	btnFocus int // 0=OK 1=Cancel
}

// NewOptionsDialog creates the Options dialog.
func NewOptionsDialog(app *App) *OptionsDialog {
	d := &OptionsDialog{app: app}
	d.InitModal(app.screen, "Options", 60, 17)

	styles := config.AllIconStyles()
	labels := make([]string, len(styles))
	for i, st := range styles {
		labels[i] = config.IconStyleName(st)
	}
	d.rbIconStyle = widgets.NewRadioBox("Object Explorer Icons:", labels)

	d.fMaxCellLen = widgets.NewInputField("Max cell length (Query Results):", 5, false)
	d.fMaxResultRows = widgets.NewInputField("Max result rows (Query Results):", 8, false)
	d.cbIntelliSense = widgets.NewCheckBox("Enable IntelliSense (autocomplete) in Query editor")
	return d
}

// Show opens the dialog with the icon-style radio box focused, pre-filling
// every control from the current config.
func (d *OptionsDialog) Show() {
	d.btnFocus = 0
	for i, st := range config.AllIconStyles() {
		if st == d.app.cfg.IconStyle {
			d.rbIconStyle.SetSelected(i)
			break
		}
	}
	d.fMaxCellLen.SetValue(strconv.Itoa(d.app.cfg.MaxCellLength))
	d.fMaxResultRows.SetValue(strconv.Itoa(d.app.cfg.MaxResultRows))
	d.cbIntelliSense.SetChecked(!d.app.cfg.IntelliSenseDisabled)
	d.setZone(zoneIconStyle)
	d.ModalDialog.Show()
}

func (d *OptionsDialog) setZone(z optionsZone) {
	d.zone = z
	d.rbIconStyle.Focus(z == zoneIconStyle)
	d.fMaxCellLen.Focus(z == zoneMaxCellLen)
	d.fMaxResultRows.Focus(z == zoneMaxResultRows)
	d.cbIntelliSense.Focus(z == zoneIntelliSense)
}

// Draw renders the dialog.
func (d *OptionsDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	inner := d.InnerRect()
	d.rbIconStyle.SetBounds(inner.X+1, inner.Y+1)
	d.rbIconStyle.Draw(s)

	d.fMaxCellLen.SetBounds(inner.X+1, inner.Y+6)
	d.fMaxCellLen.Draw(s)

	d.fMaxResultRows.SetBounds(inner.X+1, inner.Y+8)
	d.fMaxResultRows.Draw(s)

	d.cbIntelliSense.SetBounds(inner.X+1, inner.Y+10)
	d.cbIntelliSense.Draw(s)

	d.DrawSeparator(s)
	activeIdx := -1
	if d.zone == zoneOptButtons {
		activeIdx = d.btnFocus
	}
	d.DrawButtons(s, []string{"OK", "Cancel"}, activeIdx)
}

// HandleKey processes keyboard events.
func (d *OptionsDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	if ev.Key() == tcell.KeyEscape {
		d.Hide()
		return true
	}
	switch d.zone {
	case zoneMaxCellLen:
		if d.fMaxCellLen.HandleKey(ev) {
			return true
		}
		switch ev.Key() {
		case tcell.KeyTab, tcell.KeyDown:
			d.setZone(zoneMaxResultRows)
		case tcell.KeyBacktab, tcell.KeyUp:
			d.setZone(zoneIconStyle)
		case tcell.KeyEnter:
			d.doButton()
		default:
			return false
		}
		return true
	case zoneMaxResultRows:
		if d.fMaxResultRows.HandleKey(ev) {
			return true
		}
		switch ev.Key() {
		case tcell.KeyTab, tcell.KeyDown:
			d.setZone(zoneIntelliSense)
		case tcell.KeyBacktab, tcell.KeyUp:
			d.setZone(zoneMaxCellLen)
		case tcell.KeyEnter:
			d.doButton()
		default:
			return false
		}
		return true
	case zoneIntelliSense:
		if d.cbIntelliSense.HandleKey(ev) {
			return true
		}
		switch ev.Key() {
		case tcell.KeyTab, tcell.KeyDown:
			d.setZone(zoneOptButtons)
		case tcell.KeyBacktab, tcell.KeyUp:
			d.setZone(zoneMaxResultRows)
		case tcell.KeyEnter:
			d.doButton()
		default:
			return false
		}
		return true
	case zoneOptButtons:
		switch ev.Key() {
		case tcell.KeyTab, tcell.KeyRight:
			d.btnFocus = (d.btnFocus + 1) % 2
		case tcell.KeyBacktab, tcell.KeyLeft:
			if d.btnFocus > 0 {
				d.btnFocus--
			} else {
				d.setZone(zoneIntelliSense)
			}
		case tcell.KeyUp:
			d.setZone(zoneIntelliSense)
		case tcell.KeyEnter:
			d.doButton()
		}
		return true
	default: // zoneIconStyle
		if d.rbIconStyle.HandleKey(ev) {
			return true
		}
		switch ev.Key() {
		case tcell.KeyTab, tcell.KeyDown:
			d.setZone(zoneMaxCellLen)
		case tcell.KeyEnter:
			d.doButton()
		}
		return true
	}
}

// HandleMouse processes mouse events.
func (d *OptionsDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, []string{"OK", "Cancel"}); i >= 0 {
		d.setZone(zoneOptButtons)
		d.btnFocus = i
		d.doButton()
		return true
	}
	if d.fMaxCellLen.HandleMouse(ev) {
		d.setZone(zoneMaxCellLen)
		return true
	}
	if d.fMaxResultRows.HandleMouse(ev) {
		d.setZone(zoneMaxResultRows)
		return true
	}
	if d.rbIconStyle.HandleMouse(ev) {
		d.setZone(zoneIconStyle)
		return true
	}
	if mx, my := ev.Position(); my == d.cbIntelliSense.RectY() && mx >= d.cbIntelliSense.RectX() && mx < d.cbIntelliSense.RectX()+3 {
		d.cbIntelliSense.SetChecked(!d.cbIntelliSense.Checked())
		d.setZone(zoneIntelliSense)
		return true
	}
	return true
}

func (d *OptionsDialog) doButton() {
	switch d.btnFocus {
	case 0: // OK
		d.apply()
		d.Hide()
	case 1: // Cancel
		d.Hide()
	}
}

// apply commits the selected icon style, max cell length, and max result
// rows to the config, persists it, and rebuilds the Object Explorer so the
// icon change is visible immediately.
func (d *OptionsDialog) apply() {
	styles := config.AllIconStyles()
	if i := d.rbIconStyle.Selected(); i >= 0 && i < len(styles) {
		d.app.cfg.IconStyle = styles[i]
	}
	if n, err := strconv.Atoi(d.fMaxCellLen.Value()); err == nil && n > 0 {
		d.app.cfg.MaxCellLength = n
	} else {
		d.app.cfg.MaxCellLength = config.DefaultMaxCellLength
	}
	if n, err := strconv.Atoi(d.fMaxResultRows.Value()); err == nil && n > 0 {
		d.app.cfg.MaxResultRows = n
	} else {
		d.app.cfg.MaxResultRows = config.DefaultMaxResultRows
	}
	d.app.cfg.IntelliSenseDisabled = !d.cbIntelliSense.Checked()
	if err := d.app.cfg.Save(); err != nil {
		d.app.logStatus("save config: %v", err)
	}
	d.app.explorer.rebuild()
}
