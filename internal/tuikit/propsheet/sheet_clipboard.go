package propsheet

// ---------------------------------------------------------------------------
// clipboardTarget — see internal/tui/clipboard.go
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
