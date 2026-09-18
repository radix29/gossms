package tui

import (
	"strconv"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// optionsZone is which control on the Options dialog currently has
// keyboard focus.
type optionsZone int

const (
	zoneIconStyle optionsZone = iota
	zoneMaxCellLen
	zoneIndentWidth
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
	fIndentWidth   *widgets.InputField
	cbIntelliSense *widgets.CheckBox

	// drag owns the text-selection gesture a press in one of the two input
	// fields starts — see dialogs.FieldGesture for why the three calls it
	// drives have to sit where they do in HandleMouse. One value suffices for
	// both fields: the gesture is per-target, holding whichever field claimed
	// the press.
	drag dialogs.FieldGesture

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

	d.fMaxCellLen = widgets.NewInputField("Max default cell length:", 5, false)
	d.fIndentWidth = widgets.NewInputField("Indent size (spaces):", 5, false)
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
	d.fIndentWidth.SetValue(strconv.Itoa(d.app.cfg.IndentWidth))
	d.cbIntelliSense.SetChecked(!d.app.cfg.IntelliSenseDisabled)
	d.setZone(zoneIconStyle)
	d.ModalDialog.Show()
	// A latch must not survive into the next showing: a dialog dismissed
	// mid-drag would reopen still routing every click to that field.
	d.drag.Clear()
}

func (d *OptionsDialog) setZone(z optionsZone) {
	d.zone = z
	d.rbIconStyle.Focus(z == zoneIconStyle)
	d.fMaxCellLen.Focus(z == zoneMaxCellLen)
	d.fIndentWidth.Focus(z == zoneIndentWidth)
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

	d.fIndentWidth.SetBounds(inner.X+1, inner.Y+8)
	d.fIndentWidth.Draw(s)

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
			d.setZone(zoneIndentWidth)
		case tcell.KeyBacktab, tcell.KeyUp:
			d.setZone(zoneIconStyle)
		case tcell.KeyEnter:
			d.doButton()
		default:
			return false
		}
		return true
	case zoneIndentWidth:
		if d.fIndentWidth.HandleKey(ev) {
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
			d.setZone(zoneIndentWidth)
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
	// A release that lands outside the dialog is consumed by
	// ConsumeOutsideClick below before rbIconStyle/cbIntelliSense's own
	// HandleMouse calls further down ever see it, leaving their
	// mouseDragging latch set and swallowing the next press. Reset it here
	// first; each returns false on ButtonNone so this has no other effect.
	// drag.Release does the same for the input fields, and must likewise come
	// before ConsumeOutsideClick.
	if ev.Buttons() == tcell.ButtonNone {
		d.rbIconStyle.HandleMouse(ev)
		d.cbIntelliSense.HandleMouse(ev)
		d.drag.Release(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	// The gesture belongs to the field that claimed its press, so motion is
	// replayed there without hit-testing — ahead of ButtonClicked below,
	// which would otherwise fire OK the moment a selection drag wandered
	// down onto the button row.
	if d.drag.Replay(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, []string{"OK", "Cancel"}); i >= 0 {
		d.setZone(zoneOptButtons)
		d.btnFocus = i
		d.doButton()
		return true
	}
	// Hit-tested here rather than left to InputField.HandleMouse's own
	// bounds check, which a latch left over from a previous showing skips.
	if mx, my := ev.Position(); ev.Buttons() == tcell.Button1 && d.fMaxCellLen.HitTest(mx, my) {
		d.setZone(zoneMaxCellLen)
		d.drag.Claim(d.fMaxCellLen, ev)
		return true
	}
	if mx, my := ev.Position(); ev.Buttons() == tcell.Button1 && d.fIndentWidth.HitTest(mx, my) {
		d.setZone(zoneIndentWidth)
		d.drag.Claim(d.fIndentWidth, ev)
		return true
	}
	if d.rbIconStyle.HandleMouse(ev) {
		d.setZone(zoneIconStyle)
		return true
	}
	if d.cbIntelliSense.HandleMouse(ev) {
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

// apply commits all four settings — icon style, max cell length, indent size
// and whether IntelliSense is enabled — to the config, persists it, and
// rebuilds the Object Explorer so the icon change is visible immediately.
//
// The indent size needs one step the others don't: MaxCellLength and
// IntelliSenseDisabled are read from cfg where they are used, but the width
// lives in each Editor, so it has to be pushed into the open query panels (and
// into the default new editors are seeded from) here.
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
	n := config.DefaultIndentWidth
	if v, err := strconv.Atoi(d.fIndentWidth.Value()); err == nil && v >= 1 && v <= config.MaxIndentWidth {
		n = v
	}
	d.app.cfg.IndentWidth = n
	controls.SetDefaultIndentWidth(n)
	for i := 0; i < d.app.panels.Count(); i++ {
		if qp, ok := d.app.panels.PanelAt(i).(*QueryPanel); ok {
			qp.editor.SetIndentWidth(n)
		}
	}
	d.app.cfg.IntelliSenseDisabled = !d.cbIntelliSense.Checked()
	if err := d.app.cfg.Save(); err != nil {
		d.app.logStatus("save config: %v", err)
	}
	d.app.explorer.rebuild()
}

// FocusedClipboardTarget implements core.ClipboardHost: whichever of the two
// input fields has focus. The radio box, the checkbox and the button row
// answer nil.
func (d *OptionsDialog) FocusedClipboardTarget() core.ClipboardTarget {
	switch d.zone {
	case zoneMaxCellLen:
		return d.fMaxCellLen
	case zoneIndentWidth:
		return d.fIndentWidth
	}
	return nil
}
