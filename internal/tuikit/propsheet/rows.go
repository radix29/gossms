package propsheet

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// LabelWidth is the fixed display-column width reserved for a row's label
// before its value/control begins. Every value-carrying row lines up on
// this column so a page reads as an aligned two-column form, matching the
// mockups. Checkbox rows are the one exception — they follow
// widgets.CheckBox's own "[x] Label" order, not "Label [x]".
const LabelWidth = 30

const selectControlWidth = 22

// ---------------------------------------------------------------------------
// Section — non-focusable heading with an underline
// ---------------------------------------------------------------------------

// SectionRow is a non-focusable heading with an underline. Most callers
// only need Section's returned Row; SetTitle is for the rare page whose
// heading must reflect a later selection (e.g. "Explicit permissions for
// <principal>" once a principal is picked in a grid above it).
type SectionRow struct {
	title   string
	x, y, w int
}

// Section returns a non-focusable, non-editable heading row.
func Section(title string) *SectionRow { return &SectionRow{title: title} }

// SetTitle changes the heading text in place.
func (r *SectionRow) SetTitle(title string) { r.title = title }

func (r *SectionRow) Height(w int) int   { return 2 }
func (r *SectionRow) Layout(x, y, w int) { r.x, r.y, r.w = x, y, w }
func (r *SectionRow) Focusable() bool    { return false }
func (r *SectionRow) Draw(s tcell.Screen, focused bool) {
	p := theme.Active()
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text).Bold(true)
	core.DrawText(s, r.x, r.y, st, r.title)
	sep := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
	core.DrawHLine(s, r.x, r.y+1, r.w, sep)
}

// ---------------------------------------------------------------------------
// Note — non-focusable, word-wrapped dim text
// ---------------------------------------------------------------------------

type noteRow struct {
	text    string
	lines   []string
	x, y, w int
}

// Note returns a non-focusable row of word-wrapped, dimmed text.
func Note(text string) Row { return &noteRow{text: text} }

func (r *noteRow) Height(w int) int { return len(wrapText(r.text, w)) }
func (r *noteRow) Layout(x, y, w int) {
	r.x, r.y, r.w = x, y, w
	r.lines = wrapText(r.text, w)
}
func (r *noteRow) Focusable() bool { return false }
func (r *noteRow) Draw(s tcell.Screen, focused bool) {
	p := theme.Active()
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	for i, line := range r.lines {
		core.DrawText(s, r.x, r.y+i, st, line)
	}
}

// wrapText greedily word-wraps text to at most w display columns per line.
func wrapText(text string, w int) []string {
	if w <= 0 {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	lines := make([]string, 0, 4)
	cur := words[0]
	for _, word := range words[1:] {
		if core.DisplayWidth(cur+" "+word) > w {
			lines = append(lines, cur)
			cur = word
		} else {
			cur = cur + " " + word
		}
	}
	return append(lines, cur)
}

// ---------------------------------------------------------------------------
// StaticRow — focusable, read-only label/value pair
// ---------------------------------------------------------------------------

// StaticRow displays a read-only label/value pair. It's still focusable
// (Up/Down and Tab can land on it, Ctrl+C copies its value) — only text
// editing is unavailable.
type StaticRow struct {
	label, value string
	x, y, w      int
}

// Static returns a read-only label/value row.
func Static(label, value string) *StaticRow { return &StaticRow{label: label, value: value} }

// SetValue replaces the displayed value (e.g. after a refresh).
func (r *StaticRow) SetValue(v string) { r.value = v }

// Value returns the current displayed value.
func (r *StaticRow) Value() string { return r.value }

func (r *StaticRow) Height(w int) int   { return 1 }
func (r *StaticRow) Layout(x, y, w int) { r.x, r.y, r.w = x, y, w }
func (r *StaticRow) Focusable() bool    { return true }
func (r *StaticRow) CopyText() string   { return r.value }

func (r *StaticRow) Draw(s tcell.Screen, focused bool) {
	p := theme.Active()
	lst := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	vst := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	if focused {
		lst, vst = theme.StyleSelected(), theme.StyleSelected()
		core.FillRect(s, core.Rect{X: r.x, Y: r.y, W: r.w, H: 1}, ' ', vst)
	}
	core.DrawTextClipped(s, r.x, r.y, LabelWidth, lst, r.label)
	valX := r.x + LabelWidth
	core.DrawTextClipped(s, valX, r.y, core.Max(0, r.w-LabelWidth), vst, r.value)
}

// ---------------------------------------------------------------------------
// TextRow — editable single-line text, wraps widgets.InputField
// ---------------------------------------------------------------------------

// TextRow is an editable text/numeric/password row.
type TextRow struct {
	field    *widgets.InputField
	orig     string
	unit     string
	enabled  bool
	validate func(string) error
	y        int
}

// Text returns a plain editable text row, width columns wide.
func Text(label, value string, width int) *TextRow {
	f := widgets.NewInputField(core.PadRight(label, LabelWidth), width, false)
	f.SetValue(value)
	return &TextRow{field: f, orig: value, enabled: true}
}

// Password returns a masked password row. An empty value means "leave
// unchanged" — callers should treat Dirty()==false (i.e. still blank) as
// "no change requested", which SetValue("")-as-baseline gives for free.
func Password(label string, width int) *TextRow {
	f := widgets.NewInputField(core.PadRight(label, LabelWidth), width, true)
	return &TextRow{field: f, orig: "", enabled: true}
}

// Int returns an editable integer row constrained to [min, max], with an
// optional trailing unit label (e.g. "MB", "sec").
func Int(label string, value, min, max int64, unit string) *TextRow {
	f := widgets.NewInputField(core.PadRight(label, LabelWidth), 12, false)
	v := strconv.FormatInt(value, 10)
	f.SetValue(v)
	r := &TextRow{field: f, orig: v, unit: unit, enabled: true}
	r.validate = func(s string) error {
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return fmt.Errorf("must be a whole number")
		}
		if n < min || n > max {
			return fmt.Errorf("must be between %d and %d", min, max)
		}
		return nil
	}
	return r
}

// Value returns the field's current text.
func (r *TextRow) Value() string { return r.field.Value() }

// IntValue parses the field's current text as an integer (see Int).
func (r *TextRow) IntValue() (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(r.field.Value()), 10, 64)
}

// SetValue replaces the field's text and resets the dirty baseline —
// callers use this after a successful load or Apply, not while the user
// is editing.
func (r *TextRow) SetValue(v string) {
	r.field.SetValue(v)
	r.orig = v
}

// SetEnabled toggles whether the row can be focused/edited; a disabled
// row is skipped by Form's focus cycling and drawn dim.
func (r *TextRow) SetEnabled(v bool) { r.enabled = v }

// SetValidate installs a custom validator, replacing Int's numeric one
// (or adding one to a plain Text row).
func (r *TextRow) SetValidate(fn func(string) error) { r.validate = fn }

func (r *TextRow) Height(w int) int { return 1 }
func (r *TextRow) Layout(x, y, w int) {
	r.y = y
	r.field.SetBounds(x, y)
}
func (r *TextRow) Focusable() bool { return r.enabled }

func (r *TextRow) Draw(s tcell.Screen, focused bool) {
	r.field.Focus(focused && r.enabled)
	r.field.Draw(s)
	if r.unit != "" {
		p := theme.Active()
		st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		ux := r.field.InputX() + r.field.Width() + 3
		core.DrawText(s, ux, r.y, st, r.unit)
	}
}

func (r *TextRow) HandleKey(ev *tcell.EventKey) bool {
	if !r.enabled {
		return false
	}
	return r.field.HandleKey(ev)
}
func (r *TextRow) HandleMouse(ev *tcell.EventMouse) bool {
	if !r.enabled {
		return false
	}
	return r.field.HandleMouse(ev)
}
func (r *TextRow) CopyText() string { return r.field.Value() }

// HasSelection, SelectedText, Cut, Paste, and SelectAll forward to the
// wrapped InputField's own implementations, making TextRow a ClipboardRow.
func (r *TextRow) HasSelection() bool   { return r.field.HasSelection() }
func (r *TextRow) SelectedText() string { return r.field.SelectedText() }
func (r *TextRow) Cut() string {
	if !r.enabled {
		return ""
	}
	return r.field.Cut()
}
func (r *TextRow) Paste(text string) {
	if r.enabled {
		r.field.Paste(text)
	}
}
func (r *TextRow) SelectAll() {
	if r.enabled {
		r.field.SelectAll()
	}
}

func (r *TextRow) Dirty() bool { return r.field.Value() != r.orig }
func (r *TextRow) Revert()     { r.field.SetValue(r.orig) }
func (r *TextRow) Validate() error {
	if r.validate == nil {
		return nil
	}
	return r.validate(r.field.Value())
}

// ---------------------------------------------------------------------------
// CheckRow — editable boolean, wraps widgets.CheckBox
// ---------------------------------------------------------------------------

// CheckRow is a boolean toggle row.
type CheckRow struct {
	box   *widgets.CheckBox
	label string
	orig  bool
	x, y  int
}

// Check returns an editable checkbox row.
func Check(label string, checked bool) *CheckRow {
	b := widgets.NewCheckBox(label)
	b.SetChecked(checked)
	return &CheckRow{box: b, label: label, orig: checked}
}

// Checked returns the checkbox's current state.
func (r *CheckRow) Checked() bool { return r.box.Checked() }

// SetChecked sets the state and resets the dirty baseline.
func (r *CheckRow) SetChecked(v bool) { r.box.SetChecked(v); r.orig = v }

func (r *CheckRow) Height(w int) int { return 1 }
func (r *CheckRow) Layout(x, y, w int) {
	r.x, r.y = x, y
	r.box.SetBounds(x, y)
}
func (r *CheckRow) Focusable() bool { return true }
func (r *CheckRow) Draw(s tcell.Screen, focused bool) {
	r.box.Focus(focused)
	r.box.Draw(s)
}
func (r *CheckRow) HandleKey(ev *tcell.EventKey) bool { return r.box.HandleKey(ev) }
func (r *CheckRow) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	if ev.Buttons() != tcell.Button1 || my != r.y || mx < r.x || mx >= r.x+core.DisplayWidth(r.label)+4 {
		return false
	}
	r.box.SetChecked(!r.box.Checked())
	return true
}
func (r *CheckRow) CopyText() string {
	if r.box.Checked() {
		return "true"
	}
	return "false"
}
func (r *CheckRow) Dirty() bool     { return r.box.Checked() != r.orig }
func (r *CheckRow) Revert()         { r.box.SetChecked(r.orig) }
func (r *CheckRow) Validate() error { return nil }

// ---------------------------------------------------------------------------
// SelectRow — editable dropdown, wraps widgets.DropDown
// ---------------------------------------------------------------------------

// SelectRow is a dropdown-select row.
type SelectRow struct {
	dd   *widgets.DropDown
	orig int
}

// Select returns an editable dropdown row.
func Select(label string, items []string, selected int) *SelectRow {
	dd := widgets.NewDropDown(core.PadRight(label, LabelWidth), items, selectControlWidth)
	dd.SetSelected(selected)
	return &SelectRow{dd: dd, orig: selected}
}

// Selected returns the selected item's index.
func (r *SelectRow) Selected() int { return r.dd.Selected() }

// Value returns the selected item's text.
func (r *SelectRow) Value() string { return r.dd.Value() }

// SetSelected sets the selection by index and resets the dirty baseline.
func (r *SelectRow) SetSelected(i int) { r.dd.SetSelected(i); r.orig = i }

func (r *SelectRow) Height(w int) int   { return 1 }
func (r *SelectRow) Layout(x, y, w int) { r.dd.SetBounds(x, y) }
func (r *SelectRow) Focusable() bool    { return true }
func (r *SelectRow) Draw(s tcell.Screen, focused bool) {
	r.dd.Focus(focused)
	r.dd.Draw(s)
}
func (r *SelectRow) DrawOverlay(s tcell.Screen)            { r.dd.DrawOverlay(s) }
func (r *SelectRow) HandleKey(ev *tcell.EventKey) bool     { return r.dd.HandleKey(ev) }
func (r *SelectRow) HandleMouse(ev *tcell.EventMouse) bool { return r.dd.HandleMouse(ev) }
func (r *SelectRow) CopyText() string                      { return r.dd.Value() }
func (r *SelectRow) Dirty() bool                           { return r.dd.Selected() != r.orig }
func (r *SelectRow) Revert()                               { r.dd.SetSelected(r.orig) }
func (r *SelectRow) Validate() error                       { return nil }

// ---------------------------------------------------------------------------
// RadioRow — editable single-select group, wraps widgets.RadioBox
// ---------------------------------------------------------------------------

// RadioRow is a radio-button-group row.
type RadioRow struct {
	rb      *widgets.RadioBox
	options []string
	orig    int
}

// Radio returns an editable radio-group row.
func Radio(label string, options []string, selected int) *RadioRow {
	rb := widgets.NewRadioBox(label, options)
	rb.SetSelected(selected)
	return &RadioRow{rb: rb, options: options, orig: selected}
}

// Selected returns the selected option's index.
func (r *RadioRow) Selected() int { return r.rb.Selected() }

// SetSelected sets the selection by index and resets the dirty baseline.
func (r *RadioRow) SetSelected(i int) { r.rb.SetSelected(i); r.orig = i }

func (r *RadioRow) Height(w int) int   { return r.rb.Height() }
func (r *RadioRow) Layout(x, y, w int) { r.rb.SetBounds(x, y) }
func (r *RadioRow) Focusable() bool    { return true }
func (r *RadioRow) Draw(s tcell.Screen, focused bool) {
	r.rb.Focus(focused)
	r.rb.Draw(s)
}
func (r *RadioRow) HandleKey(ev *tcell.EventKey) bool     { return r.rb.HandleKey(ev) }
func (r *RadioRow) HandleMouse(ev *tcell.EventMouse) bool { return r.rb.HandleMouse(ev) }
func (r *RadioRow) CopyText() string {
	if i := r.rb.Selected(); i >= 0 && i < len(r.options) {
		return r.options[i]
	}
	return ""
}
func (r *RadioRow) Dirty() bool     { return r.rb.Selected() != r.orig }
func (r *RadioRow) Revert()         { r.rb.SetSelected(r.orig) }
func (r *RadioRow) Validate() error { return nil }

// ---------------------------------------------------------------------------
// ButtonsRow — a right-flowing row of push buttons (Add/Remove, …)
// ---------------------------------------------------------------------------

// ButtonsRow is a row of one or more push buttons, left-flowing from the
// row's start (unlike ModalDialog's own DrawButtons, which is right-
// aligned — page action buttons like "Add"/"Remove" read better flush
// with the rest of the form).
type ButtonsRow struct {
	buttons []*widgets.Button
	focus   int
}

// Buttons returns a row hosting the given buttons in order.
func Buttons(btns ...*widgets.Button) *ButtonsRow {
	return &ButtonsRow{buttons: btns}
}

func (r *ButtonsRow) Height(w int) int { return 1 }
func (r *ButtonsRow) Layout(x, y, w int) {
	col := x
	for _, b := range r.buttons {
		b.SetBounds(col, y)
		col += b.Width() + 2
	}
}
func (r *ButtonsRow) Focusable() bool { return len(r.buttons) > 0 }
func (r *ButtonsRow) Draw(s tcell.Screen, focused bool) {
	for i, b := range r.buttons {
		b.Focus(focused && i == r.focus)
		b.Draw(s)
	}
}
func (r *ButtonsRow) HandleKey(ev *tcell.EventKey) bool {
	if len(r.buttons) == 0 {
		return false
	}
	switch ev.Key() {
	case tcell.KeyLeft:
		if r.focus > 0 {
			r.focus--
			return true
		}
	case tcell.KeyRight:
		if r.focus < len(r.buttons)-1 {
			r.focus++
			return true
		}
	}
	return r.buttons[r.focus].HandleKey(ev)
}
func (r *ButtonsRow) HandleMouse(ev *tcell.EventMouse) bool {
	for i, b := range r.buttons {
		if b.HandleMouse(ev) {
			r.focus = i
			return true
		}
	}
	return false
}
