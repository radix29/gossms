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
	// width is the field's width as asked for; Layout narrows the field to
	// what the row has room for, so a sheet on an 80-column terminal doesn't
	// draw it over the form's right border, and widens it back when it can.
	width   int
	x, y, w int
}

// Text returns a plain editable text row, width columns wide.
func Text(label, value string, width int) *TextRow {
	f := widgets.NewInputField(core.PadRight(label, LabelWidth), width, false)
	f.SetValue(value)
	return &TextRow{field: f, orig: value, enabled: true, width: width}
}

// Password returns a masked password row. An empty value means "leave
// unchanged": Dirty()==false is "no change requested", which a SetValue("")
// baseline gives for free.
func Password(label string, width int) *TextRow {
	f := widgets.NewInputField(core.PadRight(label, LabelWidth), width, true)
	return &TextRow{field: f, orig: "", enabled: true, width: width}
}

// Int returns an editable integer row constrained to [min, max], with an
// optional trailing unit label (e.g. "MB", "sec").
func Int(label string, value, min, max int64, unit string) *TextRow {
	f := widgets.NewInputField(core.PadRight(label, LabelWidth), 12, false)
	v := strconv.FormatInt(value, 10)
	f.SetValue(v)
	r := &TextRow{field: f, orig: v, unit: unit, enabled: true, width: 12}
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

// ShowFromStart scrolls the field back to its first column — for a value set
// programmatically that is read from its start, which SetValue otherwise
// shows by its tail (see widgets.InputField.ShowFromStart).
func (r *TextRow) ShowFromStart() { r.field.ShowFromStart() }

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
	// The label, a space and the two brackets take the rest of the row, and
	// a unit a space before it.
	room := w - core.DisplayWidth(r.field.Label()) - 3
	if r.unit != "" {
		room -= core.DisplayWidth(r.unit) + 1
	}
	r.field.SetWidth(min(r.width, room))
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

// HasSelection and the SelectedText, Cut, Paste and SelectAll beside it forward
// to the wrapped InputField's own implementations, making TextRow a
// ClipboardRow.
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

// SetDirtyTracked with false makes the row a *view control* rather than an
// edit: it never reports dirty and Revert leaves it alone. For a row steering
// what a read-only page displays — a filter box, a scope picker — rather than
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
