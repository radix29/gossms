package propsheet

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// ---------------------------------------------------------------------------
// CheckRow: editable boolean, wraps widgets.CheckBox
// ---------------------------------------------------------------------------

// CheckRow is a boolean toggle row.
type CheckRow struct {
	box   *widgets.CheckBox
	label string
	orig  bool

	// drawReadOnly renders the row as a tick or a cross instead of a checkbox (see
	// SetDrawReadOnly). pageReadOnly is the page's own, independent gate (see
	// TextRow.SetReadOnly).
	drawReadOnly bool
	pageReadOnly bool
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
// row goes dirty. SetChecked's counterpart; see TextRow.Edit.
func (r *CheckRow) Edit(v bool) {
	if r.pageReadOnly {
		return
	}
	r.box.SetChecked(v)
}

// Label returns the row's label. A checkbox draws its label inline at full
// width, so unlike Text/Select there is no padding to trim.
func (r *CheckRow) Label() string { return r.label }

func (r *CheckRow) Height(w int) int { return 1 }
func (r *CheckRow) Layout(x, y, w int) {
	r.x, r.y, r.w = x, y, w
	r.box.SetBounds(x, y)
}
func (r *CheckRow) Focusable() bool { return !r.pageReadOnly }

// SetReadOnly is the page's own gate on the row — see TextRow.SetReadOnly.
func (r *CheckRow) SetReadOnly(v bool) { r.pageReadOnly = v }

// ReadOnly reports the page's own gate, not the form's.
func (r *CheckRow) ReadOnly() bool { return r.pageReadOnly }

// SetDrawReadOnly implements ReadOnlyDrawer: the row draws its state as a
// leading tick or cross rather than a checkbox. Both states are marked, since
// an unmarked label in a column of them reads as a heading, not an option that
// is off.
func (r *CheckRow) SetDrawReadOnly(v bool) { r.drawReadOnly = v }

func (r *CheckRow) Draw(s tcell.Screen, focused bool) {
	if r.drawReadOnly || r.pageReadOnly {
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
func (r *CheckRow) HandleKey(ev *tcell.EventKey) bool {
	if r.pageReadOnly {
		return false
	}
	return r.box.HandleKey(ev)
}
func (r *CheckRow) HandleMouse(ev *tcell.EventMouse) bool {
	if r.pageReadOnly {
		return false
	}
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
// SelectRow: editable dropdown, wraps widgets.DropDown
// ---------------------------------------------------------------------------

// SelectRow is a dropdown-select row.
type SelectRow struct {
	dd        *widgets.DropDown
	orig      int
	untracked bool // see TextRow.SetDirtyTracked
	onChange  func(string)

	// drawReadOnly renders the row flat instead of as a dropdown (see
	// SetDrawReadOnly). pageReadOnly is the page's own, independent gate (see
	// TextRow.SetReadOnly).
	drawReadOnly bool
	pageReadOnly bool
	// fitItems widens the control to its widest item — see SetFitItems.
	fitItems bool
	x, y, w  int
}

// Select returns an editable dropdown row.
func Select(label string, items []string, selected int) *SelectRow {
	dd := widgets.NewDropDown(core.PadRight(label, LabelWidth), items, selectControlWidth)
	dd.SetSelected(selected)
	// orig is read back from the widget, not from selected: see SetSelected on why
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
// Selected() != orig with no way to reconcile, a permanently dirty row whose
// Apply writes what nobody asked for.
func (r *SelectRow) SetSelected(i int) {
	r.dd.SetSelected(i)
	r.orig = r.dd.Selected()
}

// Label returns the row's label, trimmed of the padding Select applied to align
// it in the sheet — TextRow.Label's counterpart.
func (r *SelectRow) Label() string { return strings.TrimRight(r.dd.Label(), " ") }

// Edit selects by index the way a keystroke does: the selection changes, the
// row goes dirty, and OnChange fires. SetSelected's counterpart (see
// TextRow.Edit). An out-of-range index is ignored, as DropDown ignores one, so
// a rejected index cannot leave the row dirty against a value it never took.
func (r *SelectRow) Edit(i int) {
	if r.pageReadOnly {
		return
	}
	before := r.dd.Value()
	r.dd.SetSelected(i)
	r.notifyChanged(before)
}

// SetDirtyTracked with false makes the row a view control rather than an edit —
// see TextRow.SetDirtyTracked.
func (r *SelectRow) SetDirtyTracked(v bool) { r.untracked = !v }

// SetItems replaces the row's choices and resets the dirty baseline with them,
// for a picker whose options depend on another control. The baseline must move
// too: the old orig indexed the old list.
func (r *SelectRow) SetItems(items []string) {
	r.dd.SetItems(items)
	r.orig = r.dd.Selected()
}

// SetFitItems makes the control as wide as its widest item, up to the row's
// width, instead of the fixed width every other select shares, for items too
// long to tell apart when cut (such as URLs differing only at the end). Never
// narrower than the fixed width, so short items still line up.
func (r *SelectRow) SetFitItems(v bool) { r.fitItems = v }

func (r *SelectRow) Height(w int) int { return 1 }
func (r *SelectRow) Layout(x, y, w int) {
	r.x, r.y, r.w = x, y, w
	r.dd.SetBounds(x, y)
	want := selectControlWidth
	if r.fitItems {
		for _, it := range r.dd.Items() {
			want = max(want, core.DisplayWidth(it)+1) // +1: the arrow's cell
		}
	}
	// The label, a space and the two brackets take the rest of the row. A row
	// narrower than the control narrows it, as TextRow.Layout does, rather than
	// drawing it over the form's right border (Database Mail Profiles' "Account to
	// add" at 80 columns); the floor keeps one value cell and the arrow.
	room := w - core.DisplayWidth(r.dd.Label()) - 3
	r.dd.SetWidth(max(2, min(want, room)))
}
func (r *SelectRow) Focusable() bool { return !r.pageReadOnly }

// SetReadOnly is the page's own gate on the row — see TextRow.SetReadOnly.
func (r *SelectRow) SetReadOnly(v bool) {
	r.pageReadOnly = v
	if v {
		// Focus(false) closes an open list: a gate applied while it is open would leave
		// the overlay drawn over a row nothing routes events to.
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
// selection (TextRow.SetOnChange's counterpart). Not fired by
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
// RadioRow: editable single-select group, wraps widgets.RadioBox
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
// reading the baseline back from the widget for the reason documented on
// SelectRow.SetSelected.
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
// selection, given the new index (SelectRow.SetOnChange's counterpart, for a
// group that drives other rows on the page). Not fired by SetSelected, which is
// programmatic and would re-enter whatever set it.
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
// label/value line naming the selected option, as SelectRow does. The
// unselected options are choices, which a page that cannot be written doesn't
// offer.
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

// Revert restores the dirty baseline and fires onChange, as SelectRow.Revert: a
// group driving other rows has to drive them back too, or Ctrl+Z leaves the
// page describing a source it no longer has selected.
func (r *RadioRow) Revert() {
	before := r.rb.Selected()
	r.rb.SetSelected(r.orig)
	r.notifyChanged(before)
}
