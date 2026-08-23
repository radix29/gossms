package widgets

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// InputField is a single-line text input control.
//
// value is indexed by rune while everything on screen is measured in terminal
// columns, which are not the same count: a CJK ideograph or emoji takes two
// columns, a combining mark none. cursor and selAnchor are rune indices, scroll
// is a column offset, and every conversion goes through core.ColumnOfRune or
// core.RuneIndexAtColumn — treating a rune index as a column puts the caret one
// column left of the character it is on.
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

// RectX and RectY return the field's label-start position, for a caller
// positioning related UI relative to the field — an autocomplete list drawn
// beneath it, say.
func (f *InputField) RectX() int { return f.rect.X }
func (f *InputField) RectY() int { return f.rect.Y }

// InputX returns the x coordinate of the input box itself (after the
// label), for positioning an overlay directly under the editable area
// rather than under the label.
func (f *InputField) InputX() int { return f.inputX() }

// Width returns the input box's visible width (excluding label and
// brackets), as passed to NewInputField.
func (f *InputField) Width() int { return f.rect.W }

// HitTest reports whether (mx,my) falls within the input box (including
// brackets), useful for click-to-focus handling.
func (f *InputField) HitTest(mx, my int) bool {
	ix := f.inputX()
	return my == f.rect.Y && mx >= ix && mx <= ix+f.rect.W+1
}

// Value returns the current text content.
func (f *InputField) Value() string { return string(f.value) }

// SetValue sets the text and moves the cursor to the end.
func (f *InputField) SetValue(v string) {
	f.value = []rune(v)
	f.cursor = len(f.value)
	f.adjustScroll()
}

// Focus sets the focused state.
func (f *InputField) Focus(v bool) { f.focused = v }

// SetEnabled toggles whether the field accepts input. A disabled field draws
// greyed out *and* refuses keys and clicks: one that only stopped accepting
// input would look like a live field ignoring the user.
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

// SelectedText returns the currently selected text, or "" if none.
func (f *InputField) SelectedText() string {
	if !f.HasSelection() {
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
// Returns "" with nothing deleted if there is no selection.
func (f *InputField) Cut() string {
	if !f.HasSelection() {
		return ""
	}
	text := f.SelectedText()
	f.deleteSelection()
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
		labelFg := p.Text
		if f.disabled {
			labelFg = p.TextDim
		}
		labelStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(labelFg)
		core.DrawText(s, f.rect.X, f.rect.Y, labelStyle, f.label)
	}
	ix := f.inputX()
	borderColor := p.InputBorder
	if f.focused && !f.disabled {
		borderColor = p.InputFocused
	}
	borderStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(borderColor)
	s.SetContent(ix, f.rect.Y, '[', nil, borderStyle)
	s.SetContent(ix+f.rect.W+1, f.rect.Y, ']', nil, borderStyle)

	runes := f.displayRunes()
	inputStyle := theme.StyleInput()
	if f.disabled {
		inputStyle = theme.StyleInputDisabled()
	}
	selStyle := theme.StyleSelected()
	cursorStyle := tcell.StyleDefault.Background(p.BorderActive).Foreground(color.White)
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

	// Walk runes accumulating display width rather than assuming a column each: a
	// wide rune shifts everything after it one column right, and counting runes
	// draws the tail of the field one column left of the terminal's.
	i, col := 0, 0
	for i < len(runes) {
		rw := core.RuneWidth(runes[i])
		if col+rw > f.scroll {
			break
		}
		col += rw
		i++
	}
	sx := 0
	// A wide rune straddling the left edge shows only its right-hand cell, which
	// is not a glyph — blank it rather than emit half a character.
	if i < len(runes) && col < f.scroll {
		for c := f.scroll; c < col+core.RuneWidth(runes[i]) && sx < f.rect.W; c++ {
			s.SetContent(ix+1+sx, f.rect.Y, ' ', nil, styleFor(i))
			sx++
		}
		i++
	}
	for sx < f.rect.W {
		st := styleFor(i)
		if i >= len(runes) {
			s.SetContent(ix+1+sx, f.rect.Y, ' ', nil, st)
			sx++
			i++
			continue
		}
		ch := runes[i]
		rw := core.RuneWidth(ch)
		if rw == 0 {
			// A combining mark shares its base rune's cell.
			i++
			continue
		}
		if sx+rw > f.rect.W {
			// Clipped by the right edge — blanks, never half a glyph, since
			// tcell owns both cells of a double-width character.
			for ; sx < f.rect.W; sx++ {
				s.SetContent(ix+1+sx, f.rect.Y, ' ', nil, st)
			}
			break
		}
		s.SetContent(ix+1+sx, f.rect.Y, ch, nil, st)
		sx += rw
		i++
	}
}

// displayRunes is what Draw and the click-to-position math both measure: the
// value itself, or one '*' per rune in password mode. The masked form keeps the
// value's rune count, so a rune index means the same thing in both.
func (f *InputField) displayRunes() []rune {
	if f.password {
		return []rune(strings.Repeat("*", len(f.value)))
	}
	return f.value
}

// HandleKey processes keyboard input, returning true only for keys InputField
// acts on. Everything else — Up/Down, Tab/Backtab, Esc, Enter, plain modifiers —
// returns false, so a caller like propsheet.Form can fall through to
// focus-cycling instead of the field swallowing the key.
func (f *InputField) HandleKey(ev *tcell.EventKey) bool {
	if !f.focused || f.disabled {
		return false
	}
	hadSelection := f.HasSelection()

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
	// dropSelection decides, after the switch below, whether to clear the
	// selection. True by default, flipped false by any case managing the
	// selection itself.
	dropSelection := !extending
	consumed := true

	switch ev.Key() {
	case tcell.KeyLeft:
		if ctrlHeld {
			f.cursor = core.WordBoundaryLeft(f.value, f.cursor)
		} else if f.cursor > 0 {
			f.cursor--
		}
	case tcell.KeyRight:
		if ctrlHeld {
			f.cursor = core.WordBoundaryRight(f.value, f.cursor)
		} else if f.cursor < len(f.value) {
			f.cursor++
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
			f.value = append(f.value[:f.cursor-1], f.value[f.cursor:]...)
			f.cursor--
		}
	case tcell.KeyDelete:
		switch {
		case hadSelection:
			f.deleteSelection()
		case ctrlHeld:
			f.deleteWordRight()
		case f.cursor < len(f.value):
			f.value = append(f.value[:f.cursor], f.value[f.cursor+1:]...)
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
			newVal := make([]rune, len(f.value)+1)
			copy(newVal, f.value[:f.cursor])
			newVal[f.cursor] = r
			copy(newVal[f.cursor+1:], f.value[f.cursor:])
			f.value = newVal
			f.cursor++
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
	return true
}

// deleteWordLeft removes the word to the left of the cursor (Ctrl+
// Backspace).
func (f *InputField) deleteWordLeft() {
	left := core.WordBoundaryLeft(f.value, f.cursor)
	f.value = append(f.value[:left], f.value[f.cursor:]...)
	f.cursor = left
}

// deleteWordRight removes the word to the right of the cursor (Ctrl+
// Delete).
func (f *InputField) deleteWordRight() {
	right := core.WordBoundaryRight(f.value, f.cursor)
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
	// Once the press is latched the gesture is this field's until the release, so
	// motion is consumed wherever the pointer went. Hit-testing instead freezes
	// the selection the moment the pointer leaves the box. Only reachable when
	// the host forwards off-rect motion here.
	if !f.mouseDragging && !f.HitTest(ev.Position()) {
		return false
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}
	mx, _ := ev.Position()
	ix := f.inputX()
	// mx-ix-1 is a terminal-column offset into the box while the cursor is a rune
	// index; converting is what keeps a click landing on the character it was
	// aimed at once the field holds a wide rune.
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

// adjustScroll keeps the caret inside the visible box, working in display
// columns rather than rune indices so a field of wide characters scrolls twice
// as far per caret step.
func (f *InputField) adjustScroll() {
	runes := f.displayRunes()
	col := core.ColumnOfRune(runes, f.cursor)
	if col < f.scroll {
		f.scroll = col
	}
	if col >= f.scroll+f.rect.W {
		f.scroll = col - f.rect.W + 1
	}
	// Keeping the caret visible is not enough on its own: replacing a long value
	// with a short one leaves the caret at the new, shorter end, and the rule
	// above scrolls the window to start exactly there — past every character in
	// the field, which then draws blank over a value that is really set.
	if last := core.ColumnOfRune(runes, len(runes)) - f.rect.W + 1; f.scroll > last {
		f.scroll = last
	}
	if f.scroll < 0 {
		f.scroll = 0
	}
}
