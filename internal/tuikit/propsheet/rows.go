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

// LabelWidth is the display-column width reserved for a row's label before its
// value begins, so a page reads as an aligned two-column form. Checkbox rows
// are the exception, following widgets.CheckBox's "[x] Label" order.
const LabelWidth = 30

const selectControlWidth = 22

// ---------------------------------------------------------------------------
// Section — non-focusable heading with an underline
// ---------------------------------------------------------------------------

// SectionRow is a non-focusable heading with an underline. Most callers need
// only Section's returned Row; SetTitle is for a heading that reflects a later
// selection, like "Explicit permissions for <principal>".
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

// Text returns the note's text. Notes are the only rows on a sheet with no
// label to address them by, so this is how a caller reads one back.
func (r *noteRow) Text() string { return r.text }

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

// HintRow is a single line a page's button handlers write to, telling the user
// why an action did nothing: an empty name, a duplicate entry, nothing selected
// in the grid. Blank and invisible until something sets it.
//
// A page is built by a plain function with no App or dialog in scope, so
// without this an Add that hit a duplicate could only `return`, leaving a
// button that looks broken. The row also sits next to the control the user just
// used, unlike the status bar behind the dialog.
//
// Not focusable, so it takes no Tab stop, and it reserves its line whether or
// not it has text — an appearing hint must not reflow the rows around it.
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

// Clear blanks the row. Call it from a handler that succeeded, so a stale
// complaint doesn't outlive its cause.
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

// StaticRow displays a read-only label/value pair. Still focusable — Up/Down
// and Tab land on it and Ctrl+C copies its value — only editing is
// unavailable.
type StaticRow struct {
	label, value string
	x, y, w      int
}

// Static returns a read-only label/value row.
func Static(label, value string) *StaticRow { return &StaticRow{label: label, value: value} }

// Label returns the row's label, the way TextRow.Label does — what identifies a
// read-only row to anything working with a Form it did not build.
func (r *StaticRow) Label() string { return strings.TrimRight(r.label, " ") }

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
	drawFlatValue(s, r.x, r.y, r.w, r.label, r.value, lst, vst)
}

// drawFlatValue renders a label/value pair as plain text — what StaticRow draws
// and what every editable row switches to under Form.SetReadOnly. Shared on
// purpose: a gated page mixes both kinds of row, and two spellings of "a value
// you cannot change" would read as two kinds of field.
//
// The value never starts on LabelWidth: a label may be exactly that wide (the
// limit TestNoPropertySheetLabelIsTruncated enforces), and drawn flush the two
// run together — "Maximum concurrent connections0", live.
func drawFlatValue(s tcell.Screen, x, y, w int, label, value string, lst, vst tcell.Style) {
	core.DrawTextClipped(s, x, y, LabelWidth, lst, label)
	valX := flatValueX(x)
	core.DrawTextClipped(s, valX, y, max(0, x+w-valX), vst, value)
}

// flatValueX is the column a flat value starts in: where the same row's text
// sits when it is editable, so a row switching between the two does not jog its
// value sideways by a column. An editable row pads its label to LabelWidth and
// draws '[' after it, with the text one further —
// TestFlatValueStartsWhereAnEditableValueDoes pins the pair.
func flatValueX(x int) int { return x + LabelWidth + 2 }

// drawFlatReadOnly is drawFlatValue in the styles a read-only row uses.
func drawFlatReadOnly(s tcell.Screen, x, y, w int, label, value string) {
	p := theme.Active()
	lst := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
	vst := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	drawFlatValue(s, x, y, w, label, value, lst, vst)
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
	// drawReadOnly renders the row flat instead of as an input box — see
	// SetDrawReadOnly. pageReadOnly is the page's own, independent gate — see
	// SetReadOnly.
	drawReadOnly bool
	pageReadOnly bool
	x, y, w      int
}

// Text returns a plain editable text row, width columns wide.
func Text(label, value string, width int) *TextRow {
	f := widgets.NewInputField(core.PadRight(label, LabelWidth), width, false)
	f.SetValue(value)
	return &TextRow{field: f, orig: value, enabled: true}
}

// Password returns a masked password row. An empty value means "leave
// unchanged": Dirty()==false is "no change requested", which a SetValue("")
// baseline gives for free.
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

// Label returns the row's label, trimmed of the padding Text/Int applied to
// align it in the sheet — what identifies a row to anything working with a Form
// it did not build, such as a test driving a page.
func (r *TextRow) Label() string { return strings.TrimRight(r.field.Label(), " ") }

// Value returns the field's current text.
func (r *TextRow) Value() string { return r.field.Value() }

// IntValue parses the field's current text as an integer (see Int).
func (r *TextRow) IntValue() (int64, error) {
	return strconv.ParseInt(strings.TrimSpace(r.field.Value()), 10, 64)
}

// SetValue replaces the field's text and resets the dirty baseline — for after
// a successful load or Apply, not while the user is editing.
func (r *TextRow) SetValue(v string) {
	r.field.SetValue(v)
	r.orig = v
}

// Edit sets the value the way a keystroke does: the value changes, the row goes
// dirty, and OnChange fires. SetValue is the counterpart, the post-load setter
// that moves the baseline with the value, so a row set that way reports itself
// clean.
//
// The distinction is the whole propsheet contract: every apply closure gates
// its write on Dirty(), so "set the value" and "the user changed the value" are
// different operations and neither can stand in for the other.
func (r *TextRow) Edit(v string) {
	if !r.enabled || r.pageReadOnly {
		return
	}
	before := r.field.Value()
	r.field.SetValue(v)
	r.notifyChanged(before)
}

// SetEnabled toggles whether the row can be focused or edited; a disabled row
// is skipped by Form's focus cycling and drawn dim.
func (r *TextRow) SetEnabled(v bool) {
	r.enabled = v
	r.field.SetEnabled(v)
}

// SetValidate installs a custom validator, replacing Int's numeric one or
// adding one to a plain Text row.
func (r *TextRow) SetValidate(fn func(string) error) { r.validate = fn }

func (r *TextRow) Height(w int) int { return 1 }
func (r *TextRow) Layout(x, y, w int) {
	r.x, r.y, r.w = x, y, w
	r.field.SetBounds(x, y)
}
func (r *TextRow) Focusable() bool { return r.enabled && !r.pageReadOnly }

// SetReadOnly is the page's own gate on the row, independent of the form's:
// the value is shown, the row cannot be focused, typed into or Edit'ed.
// EditorRow.SetReadOnly's counterpart, and set together with it by a page
// gating a whole panel — the Steps page's non-T-SQL step is the case.
//
// The two gates are separate fields rather than one flag because whichever is
// set last must not cancel the other out: lifting the form's permission gate
// must not make a non-T-SQL step editable.
func (r *TextRow) SetReadOnly(v bool) { r.pageReadOnly = v }

// ReadOnly reports the page's own gate, not the form's.
func (r *TextRow) ReadOnly() bool { return r.pageReadOnly }

// SetDrawReadOnly implements ReadOnlyDrawer: the row draws as a flat
// label/value pair, with no input box. A Password row has nothing to show —
// its value is empty by construction, "" meaning "leave unchanged".
func (r *TextRow) SetDrawReadOnly(v bool) { r.drawReadOnly = v }

func (r *TextRow) Draw(s tcell.Screen, focused bool) {
	if r.drawReadOnly || r.pageReadOnly {
		v := r.field.Value()
		if v != "" && r.unit != "" {
			v += " " + r.unit
		}
		drawFlatReadOnly(s, r.x, r.y, r.w, r.Label(), v)
		return
	}
	r.field.Focus(focused && r.enabled)
	r.field.Draw(s)
	if r.unit != "" {
		p := theme.Active()
		st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		ux := r.field.InputX() + r.field.Width() + 3
		core.DrawText(s, ux, r.y, st, r.unit)
	}
}

// SetOnChange installs a callback fired whenever an edit changes the field's
// text — for a row driving something else on the page, such as a filter box
// narrowing a grid. It fires on the edit, not on focus loss, so the effect
// keeps up with typing.
func (r *TextRow) SetOnChange(fn func(string)) { r.onChange = fn }

// notifyChanged fires onChange if the wrapped field's text changed. Every edit
// path goes through it: comparing values rather than guessing which keys mutate
// keeps a new InputField binding from silently skipping the callback.
func (r *TextRow) notifyChanged(before string) {
	if r.onChange != nil && r.field.Value() != before {
		r.onChange(r.field.Value())
	}
}

func (r *TextRow) HandleKey(ev *tcell.EventKey) bool {
	if !r.enabled || r.pageReadOnly {
		return false
	}
	before := r.field.Value()
	handled := r.field.HandleKey(ev)
	r.notifyChanged(before)
	return handled
}
func (r *TextRow) HandleMouse(ev *tcell.EventMouse) bool {
	if !r.enabled || r.pageReadOnly {
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

// SetDirtyTracked(false) makes the row a *view control* rather than an edit: it
// never reports dirty and Revert leaves it alone. For a row steering what a
// read-only page displays — a filter box, a scope picker — rather than
// something Apply writes. Without it such a page reports unsaved changes it
// can't save, and Refresh prompts to discard them.
func (r *TextRow) SetDirtyTracked(v bool) { r.untracked = !v }

func (r *TextRow) Dirty() bool { return !r.untracked && r.field.Value() != r.orig }

// Revert restores the dirty baseline and fires onChange, so whatever the row
// drives follows the text back. An untracked row is left alone: blanking a
// filter box without telling the grid it filters leaves the two disagreeing
// about what is shown.
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

	// drawReadOnly renders the row as a tick or a cross instead of a
	// checkbox — see SetDrawReadOnly.
	drawReadOnly bool
	x, y, w      int
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

// Edit sets the state the way pressing Space does: the value changes and the
// row goes dirty. SetChecked's counterpart — see TextRow.Edit for why the two
// are different operations.
func (r *CheckRow) Edit(v bool) { r.box.SetChecked(v) }

// Label returns the row's label. A checkbox draws its label inline at full
// width, so unlike Text/Select there is no padding to trim.
func (r *CheckRow) Label() string { return r.label }

func (r *CheckRow) Height(w int) int { return 1 }
func (r *CheckRow) Layout(x, y, w int) {
	r.x, r.y, r.w = x, y, w
	r.box.SetBounds(x, y)
}
func (r *CheckRow) Focusable() bool { return true }

// SetDrawReadOnly implements ReadOnlyDrawer: the row draws its state as a
// leading tick or cross rather than as a checkbox. Both states are marked —
// an unmarked label sitting in a column of them reads as a heading, not as an
// option that is off.
func (r *CheckRow) SetDrawReadOnly(v bool) { r.drawReadOnly = v }

func (r *CheckRow) Draw(s tcell.Screen, focused bool) {
	if r.drawReadOnly {
		p := theme.Active()
		mark := "✗"
		if r.box.Checked() {
			mark = "✓"
		}
		mst := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
		lst := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		core.DrawText(s, r.x, r.y, mst, mark)
		core.DrawTextClipped(s, r.x+2, r.y, max(0, r.w-2), lst, r.label)
		return
	}
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

	// drawReadOnly renders the row flat instead of as a dropdown — see
	// SetDrawReadOnly. pageReadOnly is the page's own, independent gate — see
	// TextRow.SetReadOnly.
	drawReadOnly bool
	pageReadOnly bool
	x, y, w      int
}

// Select returns an editable dropdown row.
func Select(label string, items []string, selected int) *SelectRow {
	dd := widgets.NewDropDown(core.PadRight(label, LabelWidth), items, selectControlWidth)
	dd.SetSelected(selected)
	// orig read back from the widget, not from selected — see SetSelected on why
	// an out-of-range index must not become the baseline.
	return &SelectRow{dd: dd, orig: dd.Selected()}
}

// Selected returns the selected item's index.
func (r *SelectRow) Selected() int { return r.dd.Selected() }

// Value returns the selected item's text.
func (r *SelectRow) Value() string { return r.dd.Value() }

// Items returns the row's current choices, for a caller that repopulated them
// with SetItems and needs an index within the new list.
func (r *SelectRow) Items() []string { return r.dd.Items() }

// SetSelected sets the selection by index and resets the dirty baseline.
//
// The baseline is read back from the widget, not taken from i: DropDown
// silently ignores an out-of-range index, and storing the rejected i leaves
// Selected() != orig with no way to reconcile them — a permanently dirty row
// that reports unsaved changes forever and makes Apply write what nobody
// asked for.
func (r *SelectRow) SetSelected(i int) {
	r.dd.SetSelected(i)
	r.orig = r.dd.Selected()
}

// Label returns the row's label, trimmed of the padding Select applied to align
// it in the sheet — TextRow.Label's counterpart.
func (r *SelectRow) Label() string { return strings.TrimRight(r.dd.Label(), " ") }

// Edit selects by index the way a keystroke does: the selection changes, the
// row goes dirty, and OnChange fires. SetSelected's counterpart — see
// TextRow.Edit for why the two are different operations. An out-of-range index
// is ignored, as DropDown ignores one, so a rejected index cannot leave the row
// dirty against a value it never took.
func (r *SelectRow) Edit(i int) {
	if r.pageReadOnly {
		return
	}
	before := r.dd.Value()
	r.dd.SetSelected(i)
	r.notifyChanged(before)
}

// SetDirtyTracked(false) makes the row a view control rather than an edit —
// see TextRow.SetDirtyTracked.
func (r *SelectRow) SetDirtyTracked(v bool) { r.untracked = !v }

// SetItems replaces the row's choices and resets the dirty baseline with them,
// for a picker whose options depend on another control. The baseline must move
// too: the old orig indexed the old list, so leaving it compares against an
// item that is no longer there.
func (r *SelectRow) SetItems(items []string) {
	r.dd.SetItems(items)
	r.orig = r.dd.Selected()
}

func (r *SelectRow) Height(w int) int { return 1 }
func (r *SelectRow) Layout(x, y, w int) {
	r.x, r.y, r.w = x, y, w
	r.dd.SetBounds(x, y)
}
func (r *SelectRow) Focusable() bool { return !r.pageReadOnly }

// SetReadOnly is the page's own gate on the row — see TextRow.SetReadOnly.
func (r *SelectRow) SetReadOnly(v bool) {
	r.pageReadOnly = v
	if v {
		// Focus(false) closes an open list: a gate applied while it is open
		// would otherwise leave the overlay drawn over a row nothing routes
		// events to any more.
		r.dd.Focus(false)
	}
}

// ReadOnly reports the page's own gate, not the form's.
func (r *SelectRow) ReadOnly() bool { return r.pageReadOnly }

// SetDrawReadOnly implements ReadOnlyDrawer: the row draws the selected item
// as flat text, with no box and no arrow.
func (r *SelectRow) SetDrawReadOnly(v bool) { r.drawReadOnly = v }

func (r *SelectRow) Draw(s tcell.Screen, focused bool) {
	if r.drawReadOnly || r.pageReadOnly {
		drawFlatReadOnly(s, r.x, r.y, r.w, r.Label(), r.dd.Value())
		return
	}
	r.dd.Focus(focused)
	r.dd.Draw(s)
}
func (r *SelectRow) DrawOverlay(s tcell.Screen) { r.dd.DrawOverlay(s) }
func (r *SelectRow) OverlayActive() bool        { return r.dd.IsOpen() }
func (r *SelectRow) HandleKey(ev *tcell.EventKey) bool {
	if r.pageReadOnly {
		return false
	}
	before := r.dd.Value()
	handled := r.dd.HandleKey(ev)
	r.notifyChanged(before)
	return handled
}
func (r *SelectRow) HandleMouse(ev *tcell.EventMouse) bool {
	if r.pageReadOnly {
		return false
	}
	before := r.dd.Value()
	handled := r.dd.HandleMouse(ev)
	r.notifyChanged(before)
	return handled
}

// SetOnChange installs a callback fired whenever user interaction changes the
// selection — TextRow.SetOnChange's counterpart. Not fired by
// SetSelected/SetItems, which are programmatic and would re-enter whatever set
// them.
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
	rb       *widgets.RadioBox
	options  []string
	orig     int
	onChange func(int)

	// drawReadOnly collapses the group to a single label/value line — see
	// SetDrawReadOnly.
	drawReadOnly bool
	x, y, w      int
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
// reading the baseline back from the widget for the reason SelectRow.SetSelected
// documents.
func (r *RadioRow) SetSelected(i int) {
	r.rb.SetSelected(i)
	r.orig = r.rb.Selected()
}

// Edit selects by index the way a keystroke does: the row goes dirty and
// OnChange fires. SetSelected's counterpart, for the reason TextRow.Edit
// documents.
func (r *RadioRow) Edit(i int) {
	before := r.rb.Selected()
	r.rb.SetSelected(i)
	r.notifyChanged(before)
}

// SetOnChange installs a callback fired whenever user interaction changes the
// selection, given the newly selected index — SelectRow.SetOnChange's
// counterpart, for a group that drives other rows on the same page. Not fired
// by SetSelected, which is programmatic and would re-enter whatever set it.
func (r *RadioRow) SetOnChange(fn func(int)) { r.onChange = fn }

func (r *RadioRow) notifyChanged(before int) {
	if r.onChange != nil && r.rb.Selected() != before {
		r.onChange(r.rb.Selected())
	}
}

// Options returns the row's choices, and Label its caption — how a caller
// working with a Form it did not build identifies the row and its selection.
func (r *RadioRow) Options() []string { return r.options }
func (r *RadioRow) Label() string     { return r.rb.Label() }

func (r *RadioRow) Height(w int) int {
	if r.drawReadOnly {
		return 1
	}
	return r.rb.Height()
}
func (r *RadioRow) Layout(x, y, w int) {
	r.x, r.y, r.w = x, y, w
	r.rb.SetBounds(x, y)
}
func (r *RadioRow) Focusable() bool { return true }

// SetDrawReadOnly implements ReadOnlyDrawer: the group collapses to one
// label/value line naming the selected option, the same shape SelectRow takes
// — the unselected options are choices, and a page that cannot be written is
// not offering them.
func (r *RadioRow) SetDrawReadOnly(v bool) { r.drawReadOnly = v }

func (r *RadioRow) Draw(s tcell.Screen, focused bool) {
	if r.drawReadOnly {
		drawFlatReadOnly(s, r.x, r.y, r.w, r.Label(), r.CopyText())
		return
	}
	r.rb.Focus(focused)
	r.rb.Draw(s)
}
func (r *RadioRow) HandleKey(ev *tcell.EventKey) bool {
	before := r.rb.Selected()
	handled := r.rb.HandleKey(ev)
	r.notifyChanged(before)
	return handled
}
func (r *RadioRow) HandleMouse(ev *tcell.EventMouse) bool {
	before := r.rb.Selected()
	handled := r.rb.HandleMouse(ev)
	r.notifyChanged(before)
	return handled
}
func (r *RadioRow) CopyText() string {
	if i := r.rb.Selected(); i >= 0 && i < len(r.options) {
		return r.options[i]
	}
	return ""
}
func (r *RadioRow) Dirty() bool     { return r.rb.Selected() != r.orig }
func (r *RadioRow) Validate() error { return nil }

// Revert restores the dirty baseline and fires onChange — SelectRow.Revert,
// which this matches: a group driving other rows has to drive them back too,
// or Ctrl+Z leaves the page describing a source it no longer has selected.
func (r *RadioRow) Revert() {
	before := r.rb.Selected()
	r.rb.SetSelected(r.orig)
	r.notifyChanged(before)
}

// ---------------------------------------------------------------------------
// ButtonsRow — a right-flowing row of push buttons (Add/Remove, …)
// ---------------------------------------------------------------------------

// ButtonsRow is a row of push buttons flowing left from the row's start, unlike
// ModalDialog's right-aligned DrawButtons: page actions like Add/Remove read
// better flush with the rest of the form.
type ButtonsRow struct {
	buttons []*widgets.Button
	focus   int

	// drawReadOnly dims the buttons — see SetDrawReadOnly.
	drawReadOnly bool
	x, y         int
}

// Buttons returns a row hosting the given buttons in order.
func Buttons(btns ...*widgets.Button) *ButtonsRow {
	return &ButtonsRow{buttons: btns}
}

func (r *ButtonsRow) Height(w int) int { return 1 }
func (r *ButtonsRow) Layout(x, y, w int) {
	r.x, r.y = x, y
	col := x
	for _, b := range r.buttons {
		b.SetBounds(col, y)
		col += b.Width() + 2
	}
}
func (r *ButtonsRow) Focusable() bool { return len(r.buttons) > 0 }

// Buttons returns the row's buttons, for a host reaching one by label after the
// row is built.
func (r *ButtonsRow) Buttons() []*widgets.Button { return r.buttons }

// SetDrawReadOnly implements ReadOnlyDrawer: the buttons draw dimmed. They are
// already inert on a read-only form — Form refuses to route a press into a row
// — so what is left is not to look clickable. Drawn here rather than through a
// widgets.Button state, which no other caller needs.
func (r *ButtonsRow) SetDrawReadOnly(v bool) { r.drawReadOnly = v }

func (r *ButtonsRow) Draw(s tcell.Screen, focused bool) {
	if r.drawReadOnly {
		p := theme.Active()
		st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)
		col := r.x
		for _, b := range r.buttons {
			core.DrawText(s, col, r.y, st, "[ "+b.Label()+" ]")
			col += b.Width() + 2
		}
		return
	}
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
