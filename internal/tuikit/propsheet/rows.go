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
// this column so a page reads as an aligned two-column form. Checkbox rows
// are the one exception — they follow widgets.CheckBox's own "[x] Label"
// order, not "Label [x]".
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
	text       string
	lines      []string
	x, y, w    int
	drawHeight int
}

// Note returns a non-focusable row of word-wrapped, dimmed text.
func Note(text string) Row { return &noteRow{text: text, drawHeight: -1} }

func (r *noteRow) Height(w int) int { return len(core.WrapText(r.text, w)) }
func (r *noteRow) Layout(x, y, w int) {
	r.x, r.y, r.w = x, y, w
	r.lines = core.WrapText(r.text, w)
}
func (r *noteRow) Focusable() bool { return false }
func (r *noteRow) Draw(s tcell.Screen, focused bool) {
	p := theme.Active()
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	lines := r.lines
	if r.drawHeight >= 0 && r.drawHeight < len(lines) {
		lines = lines[:r.drawHeight]
	}
	for i, line := range lines {
		core.DrawText(s, r.x, r.y+i, st, line)
	}
}

// MinDrawHeight and SetDrawHeight implement Shrinkable: a note drops its
// trailing wrapped lines rather than running past the form's bottom edge.
func (r *noteRow) MinDrawHeight() int  { return 1 }
func (r *noteRow) SetDrawHeight(h int) { r.drawHeight = h }

// ---------------------------------------------------------------------------
// HintRow — non-focusable one-line message a handler sets at runtime
// ---------------------------------------------------------------------------

// HintRow is a single line of text a page's own button handlers write to, for
// telling the user why an action they just took did nothing: an empty name, a
// duplicate entry, nothing selected in the grid. Blank (and invisible) until
// something sets it.
//
// It exists because those handlers had no way to say anything. A page is built
// by a plain function with no App or dialog in scope, so an Add that hit a
// duplicate could only `return`, leaving a button that looks broken. A row is
// also the better place for the message than the status bar behind the dialog:
// it sits next to the control the user just used.
//
// Not focusable, so it never takes a Tab stop, and it reserves its line
// whether or not it has text — a hint appearing and disappearing must not
// reflow the rows around it.
type HintRow struct {
	text    string
	isError bool
	x, y, w int
}

// Hint returns an empty HintRow.
func Hint() *HintRow { return &HintRow{} }

// Set writes an advisory message (Warning colour).
func (r *HintRow) Set(text string) { r.text, r.isError = text, false }

// SetError writes a failure message (Error colour).
func (r *HintRow) SetError(text string) { r.text, r.isError = text, true }

// Clear blanks the row — call it from a handler that succeeded, so a stale
// complaint doesn't outlive the thing it was complaining about.
func (r *HintRow) Clear() { r.text, r.isError = "", false }

// Text returns the current message, "" when blank.
func (r *HintRow) Text() string { return r.text }

func (r *HintRow) Height(w int) int   { return 1 }
func (r *HintRow) Layout(x, y, w int) { r.x, r.y, r.w = x, y, w }
func (r *HintRow) Focusable() bool    { return false }
func (r *HintRow) Draw(s tcell.Screen, focused bool) {
	if r.text == "" {
		return
	}
	p := theme.Active()
	fg := p.Warning
	if r.isError {
		fg = p.Error
	}
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(fg)
	core.DrawTextClipped(s, r.x, r.y, r.w, st, r.text)
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
	core.DrawTextClipped(s, valX, r.y, max(0, r.w-LabelWidth), vst, r.value)
}

// ---------------------------------------------------------------------------
// TextRow — editable single-line text, wraps widgets.InputField
// ---------------------------------------------------------------------------

// TextRow is an editable text/numeric/password row.
type TextRow struct {
	field     *widgets.InputField
	orig      string
	unit      string
	enabled   bool
	validate  func(string) error
	onChange  func(string)
	untracked bool // see SetDirtyTracked
	y         int
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
func (r *TextRow) SetEnabled(v bool) {
	r.enabled = v
	r.field.SetEnabled(v)
}

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

// SetOnChange installs a callback fired whenever an edit changes the
// field's text — for a row whose value drives something else on the same
// page, a filter box narrowing a grid being the case it was added for.
// It fires on the edit, not on focus loss, so the effect keeps up with
// typing.
func (r *TextRow) SetOnChange(fn func(string)) { r.onChange = fn }

// notifyChanged fires onChange if the wrapped field's text differs from
// before. Every edit path (keys, mouse paste) goes through it — comparing
// values rather than guessing which keys mutate is what keeps a new
// InputField binding from silently skipping the callback.
func (r *TextRow) notifyChanged(before string) {
	if r.onChange != nil && r.field.Value() != before {
		r.onChange(r.field.Value())
	}
}

func (r *TextRow) HandleKey(ev *tcell.EventKey) bool {
	if !r.enabled {
		return false
	}
	before := r.field.Value()
	handled := r.field.HandleKey(ev)
	r.notifyChanged(before)
	return handled
}
func (r *TextRow) HandleMouse(ev *tcell.EventMouse) bool {
	if !r.enabled {
		return false
	}
	before := r.field.Value()
	handled := r.field.HandleMouse(ev)
	r.notifyChanged(before)
	return handled
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
	before := r.field.Value()
	cut := r.field.Cut()
	r.notifyChanged(before)
	return cut
}
func (r *TextRow) Paste(text string) {
	if r.enabled {
		before := r.field.Value()
		r.field.Paste(text)
		r.notifyChanged(before)
	}
}
func (r *TextRow) SelectAll() {
	if r.enabled {
		r.field.SelectAll()
	}
}

// SetDirtyTracked(false) makes the row a *view control* rather than an
// edit: it never reports dirty and Revert leaves it alone. For a row that
// steers what a read-only page displays — a filter box, a scope picker —
// rather than something Apply would write. Without it such a page reports
// unsaved changes it has no way to save, and Refresh prompts to discard
// them.
func (r *TextRow) SetDirtyTracked(v bool) { r.untracked = !v }

func (r *TextRow) Dirty() bool { return !r.untracked && r.field.Value() != r.orig }

// Revert restores the dirty baseline and fires onChange, so whatever the row
// drives follows the text back. An untracked row is left alone, as
// SetDirtyTracked promises: blanking a filter box without telling the grid it
// filters leaves the two disagreeing about what is being shown.
func (r *TextRow) Revert() {
	if r.untracked {
		return
	}
	before := r.field.Value()
	r.field.SetValue(r.orig)
	r.notifyChanged(before)
}
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
	r.box.SetBounds(x, y)
}
func (r *CheckRow) Focusable() bool { return true }
func (r *CheckRow) Draw(s tcell.Screen, focused bool) {
	r.box.Focus(focused)
	r.box.Draw(s)
}
func (r *CheckRow) HandleKey(ev *tcell.EventKey) bool { return r.box.HandleKey(ev) }
func (r *CheckRow) HandleMouse(ev *tcell.EventMouse) bool {
	return r.box.HandleMouse(ev)
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
	dd        *widgets.DropDown
	orig      int
	untracked bool // see TextRow.SetDirtyTracked
	onChange  func(string)
}

// Select returns an editable dropdown row.
func Select(label string, items []string, selected int) *SelectRow {
	dd := widgets.NewDropDown(core.PadRight(label, LabelWidth), items, selectControlWidth)
	dd.SetSelected(selected)
	// orig read back from the widget, not taken from selected — see
	// SetSelected on why an out-of-range index must not become the baseline.
	return &SelectRow{dd: dd, orig: dd.Selected()}
}

// Selected returns the selected item's index.
func (r *SelectRow) Selected() int { return r.dd.Selected() }

// Value returns the selected item's text.
func (r *SelectRow) Value() string { return r.dd.Value() }

// Items returns the row's current choices — for a caller that repopulated
// them with SetItems and needs to find an index within the new list.
func (r *SelectRow) Items() []string { return r.dd.Items() }

// SetSelected sets the selection by index and resets the dirty baseline.
//
// The baseline comes from reading the widget back, not from i: DropDown
// silently ignores an out-of-range index, and storing the rejected i would
// leave Selected() != orig with no way for the user to reconcile them — a
// permanently dirty row that reports unsaved changes forever and makes Apply
// issue a write nobody asked for.
func (r *SelectRow) SetSelected(i int) {
	r.dd.SetSelected(i)
	r.orig = r.dd.Selected()
}

// SetDirtyTracked(false) makes the row a view control rather than an edit —
// see TextRow.SetDirtyTracked.
func (r *SelectRow) SetDirtyTracked(v bool) { r.untracked = !v }

// SetItems replaces the row's choices and resets the dirty baseline along
// with them — for a picker whose options depend on another control. The
// baseline has to move too: the old orig indexed the old list, so leaving it
// would make the row read dirty (or clean) by comparing against an item that
// is no longer there.
func (r *SelectRow) SetItems(items []string) {
	r.dd.SetItems(items)
	r.orig = r.dd.Selected()
}

func (r *SelectRow) Height(w int) int   { return 1 }
func (r *SelectRow) Layout(x, y, w int) { r.dd.SetBounds(x, y) }
func (r *SelectRow) Focusable() bool    { return true }
func (r *SelectRow) Draw(s tcell.Screen, focused bool) {
	r.dd.Focus(focused)
	r.dd.Draw(s)
}
func (r *SelectRow) DrawOverlay(s tcell.Screen) { r.dd.DrawOverlay(s) }
func (r *SelectRow) OverlayActive() bool        { return r.dd.IsOpen() }
func (r *SelectRow) HandleKey(ev *tcell.EventKey) bool {
	before := r.dd.Value()
	handled := r.dd.HandleKey(ev)
	r.notifyChanged(before)
	return handled
}
func (r *SelectRow) HandleMouse(ev *tcell.EventMouse) bool {
	before := r.dd.Value()
	handled := r.dd.HandleMouse(ev)
	r.notifyChanged(before)
	return handled
}

// SetOnChange installs a callback fired whenever the user's interaction
// changes the selection — the SelectRow counterpart of
// TextRow.SetOnChange, for a row that drives something else on the page.
// Not fired by SetSelected/SetItems, which are the page's own programmatic
// updates and would otherwise re-enter whatever set them.
func (r *SelectRow) SetOnChange(fn func(string)) { r.onChange = fn }

func (r *SelectRow) notifyChanged(before string) {
	if r.onChange != nil && r.dd.Value() != before {
		r.onChange(r.dd.Value())
	}
}
func (r *SelectRow) CopyText() string { return r.dd.Value() }
func (r *SelectRow) Dirty() bool      { return !r.untracked && r.dd.Selected() != r.orig }
func (r *SelectRow) Validate() error  { return nil }

// Revert restores the dirty baseline and fires onChange — see
// TextRow.Revert, which this matches in both respects.
func (r *SelectRow) Revert() {
	if r.untracked {
		return
	}
	before := r.dd.Value()
	r.dd.SetSelected(r.orig)
	r.notifyChanged(before)
}

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
	return &RadioRow{rb: rb, options: options, orig: rb.Selected()}
}

// Selected returns the selected option's index.
func (r *RadioRow) Selected() int { return r.rb.Selected() }

// SetSelected sets the selection by index and resets the dirty baseline,
// reading the baseline back from the widget for the reason SelectRow's own
// SetSelected documents.
func (r *RadioRow) SetSelected(i int) {
	r.rb.SetSelected(i)
	r.orig = r.rb.Selected()
}

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

// Buttons returns the row's buttons, for a host that needs to reach one by
// label after the row is built.
func (r *ButtonsRow) Buttons() []*widgets.Button { return r.buttons }
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
