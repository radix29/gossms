package tui

import (
	"strconv"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/format"
	"github.com/radix29/tuikit/controls/editor"
	"github.com/radix29/tuikit/core"
	"github.com/radix29/tuikit/dialogs"
	"github.com/radix29/tuikit/theme"
	"github.com/radix29/tuikit/widgets"
)

// OptionsDialog is the application's Options/Settings dialog, reachable
// from Tools > Options.
type OptionsDialog struct {
	dialogs.ModalDialog
	app *App

	rbIconStyle    *widgets.RadioBox
	ddTheme        *widgets.DropDown
	ddSQLStyle     *widgets.DropDown // format.Styles(LangSQL), in registry order
	fMaxCellLen    *widgets.InputField
	fMaxTextLen    *widgets.InputField
	fIndentWidth   *widgets.InputField
	fXECapacity    *widgets.InputField
	fMaxFetchRows  *widgets.InputField
	cbIntelliSense *widgets.CheckBox

	// focusable is the Tab order, top to bottom; Tab/Backtab, hit-testing and the
	// clipboard target derive from it, so a new control is one entry here plus its
	// Draw position. ring is the focus ring over it and optionsButtons; its
	// default button is OK (0), so Enter from a field applies.
	focusable []focusable
	ring      dialogs.FieldRing
}

// NewOptionsDialog creates the Options dialog.
func NewOptionsDialog(app *App) *OptionsDialog {
	d := &OptionsDialog{app: app}
	d.InitModal("Options", 60, 23)

	styles := config.AllIconStyles()
	labels := make([]string, len(styles))
	for i, st := range styles {
		labels[i] = config.IconStyleName(st)
	}
	d.rbIconStyle = widgets.NewRadioBox("Object Explorer Icons:", labels)

	themes := config.AllColorThemes()
	themeLabels := make([]string, len(themes))
	for i, t := range themes {
		themeLabels[i] = config.ColorThemeName(t)
	}
	d.ddTheme = widgets.NewDropDown("", themeLabels, 20)

	sqlStyles := format.Styles(format.LangSQL)
	sqlStyleLabels := make([]string, len(sqlStyles))
	for i, st := range sqlStyles {
		sqlStyleLabels[i] = st.Name
	}
	d.ddSQLStyle = widgets.NewDropDown("", sqlStyleLabels, 20)

	d.fMaxCellLen = widgets.NewInputField("Max default cell length:", 5, false)
	d.fMaxTextLen = widgets.NewInputField("Max characters per column (text):", 5, false)
	d.fIndentWidth = widgets.NewInputField("Indent size (spaces):", 5, false)
	d.fXECapacity = widgets.NewInputField("Extended Events viewer holds (events):", 9, false)
	d.fMaxFetchRows = widgets.NewInputField("Max fetch rows (0 = unlimited):", 9, false)
	d.cbIntelliSense = widgets.NewCheckBox("Enable IntelliSense (autocomplete) in Query editor")
	d.focusable = []focusable{d.rbIconStyle, d.ddTheme, d.ddSQLStyle, d.fMaxCellLen, d.fMaxTextLen, d.fIndentWidth, d.fXECapacity, d.fMaxFetchRows, d.cbIntelliSense}
	d.ring = dialogs.FieldRing{
		Fields:  func() []focusable { return d.focusable },
		Buttons: func() []string { return optionsButtons },
		Press:   d.doButton,
		Dismiss: d.Hide,
	}
	return d
}

// Show opens the dialog with the icon-style radio box focused, pre-filling every
// control from the current config.
func (d *OptionsDialog) Show() {
	for i, st := range config.AllIconStyles() {
		if st == d.app.cfg.IconStyle {
			d.rbIconStyle.SetSelected(i)
			break
		}
	}
	for i, t := range config.AllColorThemes() {
		if t == d.app.cfg.Theme {
			d.ddTheme.SetSelected(i)
			break
		}
	}
	// StyleByID resolves an empty or unknown ID to the default, so a stale value
	// shows the style Format Document will actually use.
	if cur, ok := format.StyleByID(format.LangSQL, d.app.cfg.SQLFormatStyle); ok {
		for i, st := range format.Styles(format.LangSQL) {
			if st.ID == cur.ID {
				d.ddSQLStyle.SetSelected(i)
				break
			}
		}
	}
	d.fMaxCellLen.SetValue(strconv.Itoa(d.app.cfg.MaxCellLength))
	d.fMaxTextLen.SetValue(strconv.Itoa(d.app.cfg.MaxTextColumnLength))
	d.fIndentWidth.SetValue(strconv.Itoa(d.app.cfg.IndentWidth))
	d.fXECapacity.SetValue(strconv.Itoa(config.ClampXEventStoreCapacity(d.app.cfg.XEventStoreCapacity)))
	d.fMaxFetchRows.SetValue(strconv.Itoa(max(d.app.cfg.MaxFetchRows, 0)))
	d.cbIntelliSense.SetChecked(!d.app.cfg.IntelliSenseDisabled)
	d.ring.Reset()
	d.ModalDialog.Show()
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
	core.DrawText(s, inner.X+31, inner.Y+1, theme.StyleDialog(), "Color Theme:")
	d.ddTheme.SetBounds(inner.X+31, inner.Y+2)
	d.ddTheme.Draw(s)
	core.DrawText(s, inner.X+31, inner.Y+4, theme.StyleDialog(), "SQL Format Style:")
	d.ddSQLStyle.SetBounds(inner.X+31, inner.Y+5)
	d.ddSQLStyle.Draw(s)

	d.fMaxCellLen.SetBounds(inner.X+1, inner.Y+6)
	d.fMaxCellLen.Draw(s)

	d.fMaxTextLen.SetBounds(inner.X+1, inner.Y+8)
	d.fMaxTextLen.Draw(s)

	d.fIndentWidth.SetBounds(inner.X+1, inner.Y+10)
	d.fIndentWidth.Draw(s)

	d.fXECapacity.SetBounds(inner.X+1, inner.Y+12)
	d.fXECapacity.Draw(s)

	d.fMaxFetchRows.SetBounds(inner.X+1, inner.Y+14)
	d.fMaxFetchRows.Draw(s)

	d.cbIntelliSense.SetBounds(inner.X+1, inner.Y+16)
	d.cbIntelliSense.Draw(s)

	d.DrawSeparator(s)
	activeIdx := -1
	if d.ring.OnButtons() {
		activeIdx = d.ring.BtnFocus
	}
	d.DrawButtons(s, optionsButtons, activeIdx)
	for _, dd := range d.dropDowns() {
		dd.DrawOverlay(s)
	}
}

// dropDowns are the dialog's dropdowns, top to bottom: their open lists overlap
// the controls below them, so Draw paints them last, and HandleKey (as the
// focused control) and dialogs.RingMouse offer them every event first.
func (d *OptionsDialog) dropDowns() []*widgets.DropDown {
	return []*widgets.DropDown{d.ddTheme, d.ddSQLStyle}
}

// openDropDown is the dropdown whose list is open, or nil.
func (d *OptionsDialog) openDropDown() *widgets.DropDown {
	dd, _ := core.OpenOverlay(d.dropDowns()...)
	return dd
}

// HandleKey processes keyboard events through the focus ring. An open
// dropdown is the focused control (DropDown.Focus(false) closes it), so Escape
// closes its list only and Up/Down move in it rather than between controls.
func (d *OptionsDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	return d.ring.Key(ev)
}

// optionsButtons is the button row, drawn and hit-tested alike.
var optionsButtons = []string{"OK", "Cancel"}

// HandleMouse is the focus ring's (dialogs.RingMouse).
func (d *OptionsDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	return d.ring.Mouse(ev, &d.ModalDialog)
}

func (d *OptionsDialog) doButton(btn int) {
	switch btn {
	case 0: // OK
		d.apply()
		d.Hide()
	case 1: // Cancel
		d.Hide()
	}
}

// editorIndentHost is a dialog holding Editors that Options' indent size has to
// reach while open: every propsheet.PropertySheet-based dialog, by promotion.
// Matched structurally rather than by naming the concrete dialogs, since which
// sheets host a writable EditorRow changes with the pages.
type editorIndentHost interface{ SetEditorIndentWidth(n int) }

// apply commits all nine settings (icon style, color theme, SQL format style, max cell length, max text column
// length, indent size, the Extended Events viewer's event limit, max fetch rows,
// IntelliSense enabled) to the config, persists it, and rebuilds the Object Explorer so the
// icon change shows immediately. The theme needs only the palette swapped: every
// style is read from it at draw time.
//
// The indent size needs one step the others don't: MaxCellLength,
// MaxTextColumnLength and IntelliSenseDisabled are read from cfg where used, but
// the width lives in each Editor, so it is pushed into the open query panels and
// property sheets (and the default new editors are seeded from) here.
func (d *OptionsDialog) apply() {
	styles := config.AllIconStyles()
	if i := d.rbIconStyle.Selected(); i >= 0 && i < len(styles) {
		d.app.cfg.IconStyle = styles[i]
	}
	if themes := config.AllColorThemes(); d.ddTheme.Selected() >= 0 && d.ddTheme.Selected() < len(themes) {
		d.app.cfg.Theme = themes[d.ddTheme.Selected()]
		applyColorTheme(d.app.screen, d.app.cfg.Theme)
	}
	// Read by Format Document when it runs; nothing open holds a copy.
	if sqlStyles := format.Styles(format.LangSQL); d.ddSQLStyle.Selected() >= 0 && d.ddSQLStyle.Selected() < len(sqlStyles) {
		d.app.cfg.SQLFormatStyle = sqlStyles[d.ddSQLStyle.Selected()].ID
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
	editor.SetDefaultIndentWidth(n)
	for i := range d.app.panels.Count() {
		if qp, ok := d.app.panels.PanelAt(i).(*QueryPanel); ok {
			qp.editor.SetIndentWidth(n)
		}
	}
	// The open property sheets too: the Agent job-step Command box is a writable
	// editor that is not a QueryPanel, so walking the panels alone leaves it on the
	// old width until the page is rebuilt. Every sheet-based dialog answers this, so
	// a future writable EditorRow is covered without another arm here.
	for _, dlg := range d.app.modals.Registered() {
		if h, ok := dlg.(editorIndentHost); ok {
			h.SetEditorIndentWidth(n)
		}
	}
	// A viewer takes the capacity when it opens; open ones keep theirs.
	xeCap, _ := strconv.Atoi(d.fXECapacity.Value())
	d.app.cfg.XEventStoreCapacity = config.ClampXEventStoreCapacity(xeCap)
	// A run reads it when it starts; one in flight keeps its cap. Anything but a
	// positive number (0, negative, not a number) is the default, unlimited.
	if n, err := strconv.Atoi(d.fMaxFetchRows.Value()); err == nil && n > 0 {
		d.app.cfg.MaxFetchRows = n
	} else {
		d.app.cfg.MaxFetchRows = 0
	}
	d.app.cfg.IntelliSenseDisabled = !d.cbIntelliSense.Checked()
	d.app.saveConfig(func(err error) {
		if err != nil {
			d.app.logStatus("save config: %v", err)
		}
	})
	d.app.explorer.rebuild()
}

// FocusClipboardTargetAt implements core.ClipboardFocuser: a right-click on a
// number field focuses it.
func (d *OptionsDialog) FocusClipboardTargetAt(x, y int) bool {
	return d.ring.FocusClipboardTargetAt(x, y)
}

// FocusedClipboardTarget implements core.ClipboardHost: whichever input field
// has focus. The radio box, checkbox and button row answer nil.
func (d *OptionsDialog) FocusedClipboardTarget() core.ClipboardTarget {
	return d.ring.FocusedClipboardTarget()
}

// applyColorTheme makes t's palette the active one, and s's default style its
// ground: a cell drawn without a background (a chart canvas's gaps) takes the
// screen style, which core.Init set from whatever palette was active then. The
// Sync repaints every cell: tcell resolves the screen style only when it emits a
// cell, and one whose content and style are unchanged is never re-emitted, so it
// would keep the old ground. s may be nil (an App from newTestApp).
func applyColorTheme(s tcell.Screen, t config.ColorTheme) {
	switch t {
	case config.ThemeLight:
		theme.SetPalette(theme.Light)
	case config.ThemeMC:
		theme.SetPalette(theme.MC)
	case config.ThemeMCRetro:
		theme.SetPalette(theme.MCRetro)
	default:
		theme.SetPalette(theme.Default)
	}
	if s != nil {
		s.SetStyle(theme.StyleDefault())
		s.Sync()
	}
}
