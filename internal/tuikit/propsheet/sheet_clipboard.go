package propsheet

import "github.com/radix29/gossms/internal/tuikit/core"

// ---------------------------------------------------------------------------
// core.ClipboardTarget / core.ClipboardHost — see internal/tui/clipboard.go
// ---------------------------------------------------------------------------

func (p *PropertySheet) currentCopyText() (string, bool) {
	if p.zone != zoneForm {
		return "", false
	}
	f := p.PageForm(p.current)
	if f == nil {
		return "", false
	}
	return f.CopyText()
}

func (p *PropertySheet) focusedClipboardRow() (ClipboardRow, bool) {
	if p.zone != zoneForm {
		return nil, false
	}
	f := p.PageForm(p.current)
	if f == nil {
		return nil, false
	}
	cr, ok := f.Focused().(ClipboardRow)
	return cr, ok
}

// HasSelection reports whether Ctrl+C has something to copy: a real selection
// in the focused field, or else any non-empty copyable value on the focused row
// (a static row, a checkbox's state, a grid's selected row/cell, a text field's
// whole value).
func (p *PropertySheet) HasSelection() bool {
	if cr, ok := p.focusedClipboardRow(); ok && cr.HasSelection() {
		return true
	}
	txt, ok := p.currentCopyText()
	return ok && txt != ""
}

// SelectedText returns what Ctrl+C would copy — see HasSelection.
func (p *PropertySheet) SelectedText() string {
	if cr, ok := p.focusedClipboardRow(); ok && cr.HasSelection() {
		return cr.SelectedText()
	}
	txt, _ := p.currentCopyText()
	return txt
}

// Cut removes and returns the focused field's real selection; other row kinds
// have nothing to remove, so it degrades to Copy. cutSelection()
// (internal/tui/clipboard.go) calls Cut() only when HasSelection() was true.
func (p *PropertySheet) Cut() string {
	if cr, ok := p.focusedClipboardRow(); ok && cr.HasSelection() && !p.formLocked() {
		return cr.Cut()
	}
	return p.SelectedText()
}

// Paste inserts text into the focused field if editable and the form is not
// locked for an Apply (see HandleKey).
func (p *PropertySheet) Paste(text string) {
	if cr, ok := p.focusedClipboardRow(); ok && !p.formLocked() {
		cr.Paste(text)
	}
}

// SelectAll selects the focused field's entire contents, if editable.
func (p *PropertySheet) SelectAll() {
	if cr, ok := p.focusedClipboardRow(); ok {
		cr.SelectAll()
	}
}

// FocusedClipboardTarget implements core.ClipboardHost. The sheet is its own
// target: every method above resolves through the focused row and answers
// harmlessly when focus is on the page list or button row. This makes every
// dialog embedding a PropertySheet (Properties and every New-<object> dialog) a
// clipboard host without each saying so.
func (p *PropertySheet) FocusedClipboardTarget() core.ClipboardTarget { return p }

// ClipboardTargetToken implements core.ClipboardTargetTokener: the focused row,
// as the identity behind the sheet-as-target above.
//
// Returning the sheet from FocusedClipboardTarget costs the application's "is
// this still the target?" paste guard its resolution: the sheet is the answer
// whichever row has focus. The row itself can tell. nil is a real answer, for
// focus on the page list or button row.
func (p *PropertySheet) ClipboardTargetToken() any {
	cr, ok := p.focusedClipboardRow()
	if !ok {
		return nil
	}
	return cr
}
