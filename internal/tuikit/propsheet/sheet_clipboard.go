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

// HasSelection reports whether Ctrl+C has something to copy: a real text
// selection within the focused field, or — lacking one — any non-empty
// copyable value on the focused row (a static row, a checkbox's state, a
// grid's selected row/cell, or a text field's whole value).
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

// Cut removes and returns the focused field's real selection; every other
// row kind has nothing to remove, so it degrades to Copy — cutSelection()
// (internal/tui/clipboard.go) only calls Cut() when HasSelection() was
// already true, so this still only fires when there's something to copy.
func (p *PropertySheet) Cut() string {
	if cr, ok := p.focusedClipboardRow(); ok && cr.HasSelection() {
		return cr.Cut()
	}
	return p.SelectedText()
}

// Paste inserts text into the focused field, if it's an editable one.
func (p *PropertySheet) Paste(text string) {
	if cr, ok := p.focusedClipboardRow(); ok {
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
// target: every method above already resolves through the focused row, and
// answers harmlessly when focus is on the page list or the button row instead.
// Declaring it here is what makes every dialog embedding a PropertySheet — the
// Properties dialog and all ten New-<object> dialogs — a clipboard host
// without each having to say so.
func (p *PropertySheet) FocusedClipboardTarget() core.ClipboardTarget { return p }

// ClipboardTargetToken implements core.ClipboardTargetTokener: the focused
// row, as the identity behind the sheet-as-target above.
//
// Returning the sheet from FocusedClipboardTarget is what makes every dialog
// embedding a sheet a clipboard host for free, and it costs the application's
// "is this still the target?" paste guard its resolution — the sheet is the
// answer whichever row has focus, so the guard cannot tell one row from
// another. The row itself can.
//
// nil is a real answer, for focus on the page list or the button row.
func (p *PropertySheet) ClipboardTargetToken() any {
	cr, ok := p.focusedClipboardRow()
	if !ok {
		return nil
	}
	return cr
}
