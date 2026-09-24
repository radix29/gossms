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

// OptionsDialog is the application's Options/Settings dialog, reachable
// from Tools > Options.
type OptionsDialog struct {
	dialogs.ModalDialog
	app *App

	rbIconStyle    *widgets.RadioBox
	fMaxCellLen    *widgets.InputField
	fMaxTextLen    *widgets.InputField
	fIndentWidth   *widgets.InputField
	cbIntelliSense *widgets.CheckBox

	// drag owns the text-selection gesture a press in one of the input
	// fields starts — see dialogs.FieldGesture for why the three calls it
	// drives have to sit where they do in HandleMouse. One value suffices for
	// every field: the gesture is per-target, holding whichever field claimed
	// the press.
	drag dialogs.FieldGesture

	// focusable is the Tab order, top to bottom; Tab/Backtab, hit-testing
	// and the clipboard target are all derived from it, so a new control is
	// one entry here plus its Draw position. focusIdx == len(focusable) is
	// the button row, which is not a widget.
	focusable []focusable
	focusIdx  int
	btnFocus  int // 0=OK 1=Cancel
}

// NewOptionsDialog creates the Options dialog.
func NewOptionsDialog(app *App) *OptionsDialog {
	d := &OptionsDialog{app: app}
	d.InitModal(app.screen, "Options", 60, 19)

	styles := config.AllIconStyles()
	labels := make([]string, len(styles))
	for i, st := range styles {
		labels[i] = config.IconStyleName(st)
	}
	d.rbIconStyle = widgets.NewRadioBox("Object Explorer Icons:", labels)

	d.fMaxCellLen = widgets.NewInputField("Max default cell length:", 5, false)
	d.fMaxTextLen = widgets.NewInputField("Max characters per column (text):", 5, false)
	d.fIndentWidth = widgets.NewInputField("Indent size (spaces):", 5, false)
	d.cbIntelliSense = widgets.NewCheckBox("Enable IntelliSense (autocomplete) in Query editor")
	d.focusable = []focusable{d.rbIconStyle, d.fMaxCellLen, d.fMaxTextLen, d.fIndentWidth, d.cbIntelliSense}
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
	d.fMaxTextLen.SetValue(strconv.Itoa(d.app.cfg.MaxTextColumnLength))
	d.fIndentWidth.SetValue(strconv.Itoa(d.app.cfg.IndentWidth))
	d.cbIntelliSense.SetChecked(!d.app.cfg.IntelliSenseDisabled)
	d.setFocus(0)
	d.ModalDialog.Show()
	// A latch must not survive into the next showing: a dialog dismissed
	// mid-drag would reopen still routing every click to that field.
	d.drag.Clear()
}

// setFocus focuses focusable[i], or the button row at len(focusable).
func (d *OptionsDialog) setFocus(i int) {
	for j, f := range d.focusable {
		f.Focus(j == i)
	}
	d.focusIdx = i
}

// onButtons reports whether the button row has focus.
func (d *OptionsDialog) onButtons() bool { return d.focusIdx == len(d.focusable) }

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

	d.fMaxTextLen.SetBounds(inner.X+1, inner.Y+8)
	d.fMaxTextLen.Draw(s)

	d.fIndentWidth.SetBounds(inner.X+1, inner.Y+10)
	d.fIndentWidth.Draw(s)

	d.cbIntelliSense.SetBounds(inner.X+1, inner.Y+12)
	d.cbIntelliSense.Draw(s)

	d.DrawSeparator(s)
	activeIdx := -1
	if d.onButtons() {
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
	if d.onButtons() {
		last := len(d.focusable) - 1
		switch ev.Key() {
		case tcell.KeyTab, tcell.KeyRight:
			d.btnFocus = (d.btnFocus + 1) % 2
		case tcell.KeyBacktab, tcell.KeyLeft:
			if d.btnFocus > 0 {
				d.btnFocus--
			} else {
				d.setFocus(last)
			}
		case tcell.KeyUp:
			d.setFocus(last)
		case tcell.KeyEnter:
			d.doButton()
		}
		return true
	}
	if h, ok := d.focusable[d.focusIdx].(interface {
		HandleKey(*tcell.EventKey) bool
	}); ok && h.HandleKey(ev) {
		return true
	}
	switch ev.Key() {
	case tcell.KeyTab, tcell.KeyDown:
		d.setFocus(d.focusIdx + 1)
	case tcell.KeyBacktab, tcell.KeyUp:
		// The first control has nowhere to go back to; Backtab does not wrap.
		if d.focusIdx > 0 {
			d.setFocus(d.focusIdx - 1)
		}
	case tcell.KeyEnter:
		d.doButton()
	default:
		// The icon-style radio box has always swallowed the keys it does not
		// use; the fields and the checkbox pass them on.
		return d.focusIdx == 0
	}
	return true
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
		for _, f := range d.focusable {
			if w, ok := optionsMouseTarget(f); ok {
				w.HandleMouse(ev)
			}
		}
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
		d.setFocus(len(d.focusable))
		d.btnFocus = i
		d.doButton()
		return true
	}
	// The fields first, hit-tested here rather than left to
	// InputField.HandleMouse's own bounds check, which a latch left over from
	// a previous showing skips.
	if mx, my := ev.Position(); ev.Buttons() == tcell.Button1 {
		if i, fld := d.fieldAt(mx, my); fld != nil {
			d.setFocus(i)
			d.drag.Claim(fld, ev)
			return true
		}
	}
	for i, f := range d.focusable {
		if w, ok := optionsMouseTarget(f); ok && w.HandleMouse(ev) {
			d.setFocus(i)
			return true
		}
	}
	return true
}

// fieldAt returns the input field under (x, y) and its focus index, or nil.
func (d *OptionsDialog) fieldAt(x, y int) (int, *widgets.InputField) {
	for i, f := range d.focusable {
		if fld, ok := f.(*widgets.InputField); ok && fld.HitTest(x, y) {
			return i, fld
		}
	}
	return -1, nil
}

// optionsMouseTarget answers the controls that take a press themselves — the
// radio box and the checkbox. An input field is excluded: its press goes
// through d.drag, and its own HandleMouse would honour a stale latch.
func optionsMouseTarget(f focusable) (interface{ HandleMouse(*tcell.EventMouse) bool }, bool) {
	if _, isField := f.(*widgets.InputField); isField {
		return nil, false
	}
	w, ok := f.(interface{ HandleMouse(*tcell.EventMouse) bool })
	return w, ok
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

// editorIndentHost is a dialog holding Editors that Options' indent size has to
// reach while it is open — every propsheet.PropertySheet-based dialog, by
// promotion. Matched structurally rather than by naming the two concrete
// dialogs, since which sheets host a writable EditorRow changes with the pages.
type editorIndentHost interface{ SetEditorIndentWidth(n int) }

// apply commits all five settings — icon style, max cell length, max text
// column length, indent size and whether IntelliSense is enabled — to the config, persists it, and
// rebuilds the Object Explorer so the icon change is visible immediately.
//
// The indent size needs one step the others don't: MaxCellLength,
// MaxTextColumnLength and IntelliSenseDisabled are read from cfg where they are used, but the width
// lives in each Editor, so it has to be pushed into the open query panels and
// property sheets (and into the default new editors are seeded from) here.
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
	if n, err := strconv.Atoi(d.fMaxTextLen.Value()); err == nil && n > 0 {
		d.app.cfg.MaxTextColumnLength = n
	} else {
		d.app.cfg.MaxTextColumnLength = config.DefaultMaxTextColumnLength
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
	// The open property sheets too: the Agent job-step Command box is a
	// writable editor that is not a QueryPanel, so walking the panels alone
	// leaves it on the old width until the page is rebuilt. Every sheet-based
	// dialog answers this, so a future writable EditorRow is covered without
	// another arm here.
	for _, dlg := range d.app.allDialogs {
		if h, ok := dlg.(editorIndentHost); ok {
			h.SetEditorIndentWidth(n)
		}
	}
	d.app.cfg.IntelliSenseDisabled = !d.cbIntelliSense.Checked()
	if err := d.app.cfg.Save(); err != nil {
		d.app.logStatus("save config: %v", err)
	}
	d.app.explorer.rebuild()
}

// FocusedClipboardTarget implements core.ClipboardHost: whichever of the
// input fields has focus. The radio box, the checkbox and the button row
// answer nil.
func (d *OptionsDialog) FocusedClipboardTarget() core.ClipboardTarget {
	return focusedClipboardTarget(d.focusable, d.focusIdx)
}
