package widgets

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// InputField is a single-line text input control.
//
// value is indexed by rune but drawn in terminal columns (a CJK ideograph or
// emoji takes two, a combining mark none). cursor and selAnchor are rune indices,
// scroll is a column offset, and conversions go through core.ColumnOfRune or
// core.RuneIndexAtColumn: treating a rune index as a column puts the caret one
// column left of its character.
type InputField struct {
	rect     core.Rect
	value    []rune
	cursor   int // rune index
	scroll   int // terminal columns scrolled off to the left
	focused  bool
	password bool   // mask characters with *
	label    string // optional inline label drawn to the left

	// Selection (Shift+movement or mouse-drag), and Copy/Paste support.
	selecting     bool
	selAnchor     int
	mouseDragging bool

	// disabled fields refuse input and draw greyed out. The zero value is
	// enabled.
	disabled bool

	// OnChange fires after a key, Cut or Paste changes the value, with the new
	// value. SetValue never fires it: a host setting the value already knows.
	OnChange func(value string)
}

// NewInputField creates an InputField with an optional inline label.
// w is the visible width of the input area (excluding label and brackets).
func NewInputField(label string, w int, password bool) *InputField {
	return new(InputField{label: label, rect: core.Rect{W: w}, password: password})
}

// Label returns the inline label the field was created with, padded as the
// caller padded it. Fixed at construction.
func (f *InputField) Label() string { return f.label }

// SetBounds positions the widget. The label is drawn at (x,y); the input
// box starts immediately after the label.
func (f *InputField) SetBounds(x, y int) { f.rect.X, f.rect.Y = x, y }

// Bounds returns the drawn field: one row, label through the closing bracket.
// A caller positions related UI from it (an autocomplete list beneath the
// field); InputX is where the editable box starts.
func (f *InputField) Bounds() core.Rect {
	return core.Rect{X: f.rect.X, Y: f.rect.Y, W: f.inputX() - f.rect.X + f.rect.W + 2, H: 1}
}

// InputX returns the x coordinate of the input box itself (after the
// label), for positioning an overlay directly under the editable area
// rather than under the label.
func (f *InputField) InputX() int { return f.inputX() }

// Width returns the input box's visible width (excluding label and
// brackets), as passed to NewInputField or SetWidth.
func (f *InputField) Width() int { return f.rect.W }

// SetWidth changes the input box's visible width. It doesn't chase the caret
// as an edit does: a layout pass calling it per frame would undo ShowFromStart.
// A widened box only stops scrolling past the value's end.
func (f *InputField) SetWidth(w int) {
	if w = max(1, w); w == f.rect.W {
		return
	}
	f.rect.W = w
	runes := f.displayRunes()
	if last := core.ColumnOfRune(runes, len(runes)) - f.rect.W + 1; f.scroll > last {
		f.scroll = max(0, last)
	}
}

// HitTest reports whether (mx,my) falls within the input box (including
// brackets), useful for click-to-focus handling.
func (f *InputField) HitTest(mx, my int) bool {
	ix := f.inputX()
	return my == f.rect.Y && mx >= ix && mx <= ix+f.rect.W+1
}

// Value returns the current text content.
func (f *InputField) Value() string { return string(f.value) }

// SetValue sets the text and moves the cursor to the end. Any selection is
// dropped: a stale anchor past the end of a shorter new value paints the blanks
// beyond it as selected.
func (f *InputField) SetValue(v string) {
	f.value = []rune(v)
	f.cursor = len(f.value)
	f.selecting = false
	f.selAnchor = f.cursor
	f.adjustScroll()
}

// ShowFromStart scrolls the view to the field's first column, leaving value and
// caret. A caller pre-filling a field uses it: SetValue leaves the view on the
// tail, right for a Destination path (the file name matters) but wrong for the
// server-name field, where users pick by the first few characters.
// The caret is off screen on a focused field until the first key
// (adjustScroll); the focus border still shows where input goes.
func (f *InputField) ShowFromStart() { f.scroll = 0 }

// Focus sets the focused state.
func (f *InputField) Focus(v bool) { f.focused = v }

func (f *InputField) Focused() bool { return f.focused }

// SetEnabled toggles whether the field accepts input. A disabled field draws
// greyed and refuses keys and clicks (one that only stopped accepting input
// would look like a live field ignoring the user).
func (f *InputField) SetEnabled(v bool) { f.disabled = !v }

// Enabled reports whether the field accepts input.
func (f *InputField) Enabled() bool { return !f.disabled }

// HasSelection reports whether there is a non-empty active selection.
func (f *InputField) HasSelection() bool {
	return f.selecting && f.selAnchor != f.cursor
}

// ClearSelection drops any active selection without affecting the cursor.
func (f *InputField) ClearSelection() { f.selecting = false }

// SelectAll selects the entire field contents.
func (f *InputField) SelectAll() {
	f.selecting = true
	f.selAnchor = 0
	f.cursor = len(f.value)
}

// selectionBounds returns the selection endpoints ordered start <= end.
func (f *InputField) selectionBounds() (start, end int) {
	if f.selAnchor <= f.cursor {
		return f.selAnchor, f.cursor
	}
	return f.cursor, f.selAnchor
}

// SelectedText returns the currently selected text, or "" if none. A password
// field always returns "": the mask covers the drawing only, and handing the
// plaintext to Copy would put it on the OS clipboard (or, over SSH, through
// OSC 52 into the terminal). Desktop password boxes refuse Copy the same way.
func (f *InputField) SelectedText() string {
	if !f.HasSelection() || f.password {
		return ""
	}
	start, end := f.selectionBounds()
	start = core.Clamp(start, 0, len(f.value))
	end = core.Clamp(end, 0, len(f.value))
	return string(f.value[start:end])
}

// deleteSelection removes the selected text (if any) and moves the cursor
// to where the selection started.
func (f *InputField) deleteSelection() {
	if !f.HasSelection() {
		return
	}
	start, end := f.selectionBounds()
	start = core.Clamp(start, 0, len(f.value))
	end = core.Clamp(end, 0, len(f.value))
	f.value = append(f.value[:start], f.value[end:]...)
	f.cursor = start
	f.selecting = false
}

// Cut returns the selected text and removes it — Ctrl+X's copy-then-delete.
// Returns "" with nothing deleted if there is no selection, or in a password
// field, which refuses Cut for the reason SelectedText refuses Copy (deleting
// text the clipboard never received would just lose it).
func (f *InputField) Cut() string {
	if !f.HasSelection() || f.password {
		return ""
	}
	text := f.SelectedText()
	f.deleteSelection()
	f.changed()
	return text
}

// Paste inserts text at the cursor, replacing any selection. InputField is
// single-line, so only the first line of text is used.
func (f *InputField) Paste(text string) {
	if nl := strings.IndexAny(text, "\r\n"); nl >= 0 {
		text = text[:nl]
	}
	if text == "" {
		return
	}
	if f.HasSelection() {
		f.deleteSelection()
	}
	pasted := []rune(text)
	newVal := make([]rune, 0, len(f.value)+len(pasted))
	newVal = append(newVal, f.value[:f.cursor]...)
	newVal = append(newVal, pasted...)
	newVal = append(newVal, f.value[f.cursor:]...)
	f.value = newVal
	f.cursor += len(pasted)
	f.adjustScroll()
	f.changed()
}

// changed fires OnChange for an edit that did change the value (Cut and Paste
// always do).
func (f *InputField) changed() {
	if f.OnChange != nil {
		f.OnChange(string(f.value))
	}
}

// inputX returns the x coordinate of the input box (after label).
func (f *InputField) inputX() int {
	if f.label != "" {
		return f.rect.X + core.DisplayWidth(f.label) + 1
	}
	return f.rect.X
}

// Draw renders the label and input box.
func (f *InputField) Draw(s tcell.Screen) {
	p := theme.Active()
	if f.label != "" {
		labelFg := p.DialogText
		if f.disabled {
			labelFg = p.DialogTextDim
		}
		labelStyle := theme.On(p.DialogBg, labelFg)
		core.DrawText(s, f.rect.X, f.rect.Y, labelStyle, f.label)
	}
	ix := f.inputX()
	borderColor := p.InputBorder
	if f.focused && !f.disabled {
		borderColor = p.InputFocused
	}
	borderStyle := theme.On(p.DialogBg, borderColor)
	core.PutRune(s, ix, f.rect.Y, '[', borderStyle)
	core.PutRune(s, ix+f.rect.W+1, f.rect.Y, ']', borderStyle)

	runes := f.displayRunes()
	inputStyle := theme.StyleInput()
	if f.disabled {
		inputStyle = theme.StyleControlDisabled()
	}
	selStyle := theme.StyleSelected()
	cursorStyle := theme.StyleActiveBlock()
	hasSel := f.HasSelection()
	selStart, selEnd := 0, 0
	if hasSel {
		selStart, selEnd = f.selectionBounds()
	}

	styleFor := func(i int) tcell.Style {
		if f.focused && !f.disabled && i == f.cursor {
			return cursorStyle
		}
		if hasSel && i >= selStart && i < selEnd {
			return selStyle
		}
		return inputStyle
	}

	// Walk grapheme clusters accumulating display width, not a column per rune: a
	// wide character shifts everything after it and a cluster's width isn't the sum
	// of its runes' ("❤️" is two runes, two columns). Dropping zero-width runes would
	// lose every combining mark. Each cluster is styled by its first rune.
	i, col := 0, 0
	for i < len(runes) {
		end, cw := core.GraphemeAt(runes, i)
		if col+cw > f.scroll {
			break
		}
		col += cw
		i = end
	}
	sx := 0
	// A wide cluster straddling the left edge shows only its right-hand cell, not a
	// glyph: blank it.
	if i < len(runes) && col < f.scroll {
		end, cw := core.GraphemeAt(runes, i)
		for c := f.scroll; c < col+cw && sx < f.rect.W; c++ {
			core.PutRune(s, ix+1+sx, f.rect.Y, ' ', styleFor(i))
			sx++
		}
		i = end
	}
	for sx < f.rect.W {
		st := styleFor(i)
		if i >= len(runes) {
			core.PutRune(s, ix+1+sx, f.rect.Y, ' ', st)
			sx++
			i++
			continue
		}
		j, cw := core.GraphemeAt(runes, i)
		if cw == 0 {
			// Combining marks with no base: no cell of their own.
			i = j
			continue
		}
		if sx+cw > f.rect.W {
			// Clipped by the right edge: blanks, never half a glyph (tcell owns both cells
			// of a double-width character).
			for ; sx < f.rect.W; sx++ {
				core.PutRune(s, ix+1+sx, f.rect.Y, ' ', st)
			}
			break
		}
		if j == i+1 {
			core.PutRune(s, ix+1+sx, f.rect.Y, runes[i], st)
		} else {
			// The whole cluster in one cell write, so the terminal joins it.
			s.Put(ix+1+sx, f.rect.Y, string(runes[i:j]), st)
		}
		sx += cw
		i = j
	}
}

// displayRunes is what Draw and click-to-position both measure: the value, or
// one '*' per rune in password mode (same rune count, so indices agree).
func (f *InputField) displayRunes() []rune {
	if f.password {
		return []rune(strings.Repeat("*", len(f.value)))
	}
	return f.value
}

// HandleKey processes keyboard input, returning true only for keys InputField
// acts on; Up/Down, Tab/Backtab, Esc, Enter and plain modifiers return false so
// a caller like propsheet.Form can cycle focus.
func (f *InputField) HandleKey(ev *tcell.EventKey) bool {
	if !f.focused || f.disabled {
		return false
	}
	hadSelection := f.HasSelection()
	// Compared after the key rather than flagged per edit, so a new binding
	// can't silently skip OnChange. Only paid for by a field that has one.
	var before string
	if f.OnChange != nil {
		before = string(f.value)
	}

	mods := ev.Modifiers()
	ctrlHeld := mods&tcell.ModCtrl != 0
	shiftHeld := mods&tcell.ModShift != 0
	altHeld := mods&tcell.ModAlt != 0

	isMovementKey := false
	switch ev.Key() {
	case tcell.KeyLeft, tcell.KeyRight, tcell.KeyHome, tcell.KeyEnd:
		isMovementKey = true
	}
	extending := isMovementKey && shiftHeld
	if extending && !f.selecting {
		f.selecting = true
		f.selAnchor = f.cursor
	}
	// dropSelection decides, after the switch, whether to clear the selection. True
	// by default; cases managing the selection themselves clear it.
	dropSelection := !extending
	consumed := true

	switch ev.Key() {
	case tcell.KeyLeft:
		if ctrlHeld {
			f.cursor = f.wordLeft()
		} else if f.cursor > 0 {
			f.cursor = core.PrevGrapheme(f.displayRunes(), f.cursor)
		}
	case tcell.KeyRight:
		if ctrlHeld {
			f.cursor = f.wordRight()
		} else if f.cursor < len(f.value) {
			f.cursor = core.NextGrapheme(f.displayRunes(), f.cursor)
		}
	case tcell.KeyHome:
		f.cursor = 0
	case tcell.KeyEnd:
		f.cursor = len(f.value)
	case tcell.KeyCtrlA:
		f.SelectAll()
		dropSelection = false
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		switch {
		case hadSelection:
			f.deleteSelection()
		case ctrlHeld:
			f.deleteWordLeft()
		case f.cursor > 0:
			// The whole cluster, as Left moves over it (see Editor.backspace).
			from := core.PrevGrapheme(f.displayRunes(), f.cursor)
			f.value = append(f.value[:from], f.value[f.cursor:]...)
			f.cursor = from
		}
	case tcell.KeyDelete:
		switch {
		case hadSelection:
			f.deleteSelection()
		case ctrlHeld:
			f.deleteWordRight()
		case f.cursor < len(f.value):
			to := min(core.NextGrapheme(f.displayRunes(), f.cursor), len(f.value))
			f.value = append(f.value[:f.cursor], f.value[to:]...)
		}
	case tcell.KeyCtrlU:
		f.value = nil
		f.cursor = 0
	default:
		r := core.EvRune(ev)
		if r != 0 && !ctrlHeld && !altHeld {
			if hadSelection {
				f.deleteSelection()
			}
			text := core.EvText(ev)
			newVal := make([]rune, 0, len(f.value)+len(text))
			newVal = append(newVal, f.value[:f.cursor]...)
			newVal = append(newVal, text...)
			f.value = append(newVal, f.value[f.cursor:]...)
			f.cursor += len(text)
		} else {
			consumed = false
		}
	}
	if !consumed {
		return false
	}
	if dropSelection {
		f.selecting = false
	}
	f.adjustScroll()
	if f.OnChange != nil && string(f.value) != before {
		f.changed()
	}
	return true
}

// wordLeft and wordRight are the Ctrl+Left/Right (and Ctrl+Backspace/Delete)
// targets. A password field is one word: stopping at the hidden value's spaces
// and punctuation would reveal where they are.
func (f *InputField) wordLeft() int {
	if f.password {
		return 0
	}
	return core.WordBoundaryLeft(f.value, f.cursor)
}

func (f *InputField) wordRight() int {
	if f.password {
		return len(f.value)
	}
	return core.WordBoundaryRight(f.value, f.cursor)
}

// deleteWordLeft removes the word to the left of the cursor (Ctrl+
// Backspace).
func (f *InputField) deleteWordLeft() {
	left := f.wordLeft()
	f.value = append(f.value[:left], f.value[f.cursor:]...)
	f.cursor = left
}

// deleteWordRight removes the word to the right of the cursor (Ctrl+
// Delete).
func (f *InputField) deleteWordRight() {
	right := f.wordRight()
	f.value = append(f.value[:f.cursor], f.value[right:]...)
}

// HandleMouse handles click-to-position and click-and-drag text selection.
func (f *InputField) HandleMouse(ev *tcell.EventMouse) bool {
	// Refused before the release branch: a disabled field never latched a press,
	// so it has no drag to end.
	if f.disabled {
		return false
	}
	if ev.Buttons() == tcell.ButtonNone {
		wasDragging := f.mouseDragging
		f.mouseDragging = false
		return wasDragging
	}
	// Once the press is latched the gesture is this field's until release, so
	// motion is consumed wherever the pointer went; hit-testing would freeze the
	// selection when it leaves the box. Reachable only when the host forwards
	// off-rect motion here.
	if !f.mouseDragging && !f.HitTest(ev.Position()) {
		return false
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}
	mx, _ := ev.Position()
	ix := f.inputX()
	// mx-ix-1 is a column offset while the cursor is a rune index; converting keeps
	// a click on its character once the field holds a wide rune.
	runes := f.displayRunes()
	col := core.Clamp(core.RuneIndexAtColumn(runes, f.scroll+(mx-ix-1)), 0, len(f.value))
	if !f.mouseDragging {
		f.mouseDragging = true
		f.cursor = col
		f.selecting = true
		f.selAnchor = col
	} else {
		f.cursor = col
	}
	f.adjustScroll()
	return true
}

// CancelMouseDrag drops the drag latch without moving the cursor or touching
// the selection, for a host taking the field out of a gesture rather than
// ending one (`dialogs.FieldGesture.Clear` on a reshow). A synthetic
// `ButtonNone` would misreport the pointer position and HandleMouse's answer
// isn't what the caller asks.
func (f *InputField) CancelMouseDrag() { f.mouseDragging = false }

// adjustScroll keeps the caret inside the visible box, in display columns so a
// field of wide characters scrolls twice as far per caret step.
func (f *InputField) adjustScroll() {
	runes := f.displayRunes()
	col := core.ColumnOfRune(runes, f.cursor)
	if col < f.scroll {
		f.scroll = col
	}
	if col >= f.scroll+f.rect.W {
		f.scroll = col - f.rect.W + 1
	}
	// Keeping the caret visible isn't enough: replacing a long value with a short
	// one leaves the caret at the new end, and the rule above scrolls the window to
	// start exactly there, past every character, drawing blank over a set value.
	if last := core.ColumnOfRune(runes, len(runes)) - f.rect.W + 1; f.scroll > last {
		f.scroll = last
	}
	if f.scroll < 0 {
		f.scroll = 0
	}
}
