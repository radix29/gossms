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
// value is indexed by rune and everything on screen is measured in terminal
// columns, which are not the same count: a CJK ideograph or emoji takes two
// columns, a combining mark none. cursor and selAnchor are rune indices;
// scroll is a column offset. core.ColumnOfRune and core.RuneIndexAtColumn
// convert between them, and every conversion here goes through one of those
// — treating a rune index as a column is what put the caret one column left
// of the character it was on in any field holding wide text.
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
	// enabled, so every existing construction site stays as it was.
	disabled bool
}

// NewInputField creates an InputField with an optional inline label.
// w is the visible width of the input area (excluding label and brackets).
func NewInputField(label string, w int, password bool) *InputField {
	return new(InputField{label: label, rect: core.Rect{W: w}, password: password})
}

// SetBounds positions the widget. The label is drawn at (x,y); the input
// box starts immediately after the label.
func (f *InputField) SetBounds(x, y int) { f.rect.X, f.rect.Y = x, y }

// RectX and RectY return the field's (label-start) position, for callers
// that need to position related UI relative to the field — e.g. an
// autocomplete list drawn directly beneath it. Mirrors CheckBox's
// existing RectX/RectY.
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
// greyed out and refuses keys and clicks — both halves matter: one that only
// stopped accepting input would look exactly like a live field that ignores
// the user, which is the "click does nothing" failure a page is supposed to
// make impossible.
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

// Cut returns the currently selected text (like SelectedText) and removes
// it — the combined "copy then delete" operation Ctrl+X performs. Returns
// "" if there is no selection, in which case nothing is deleted.
func (f *InputField) Cut() string {
	if !f.HasSelection() {
		return ""
	}
	text := f.SelectedText()
	f.deleteSelection()
	return text
}

// Paste inserts text at the cursor, replacing the current selection if
// there is one. Since InputField is single-line, only the first line of
// text is used — embedded newlines are not meaningful here.
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

	// Walk runes accumulating display width rather than assuming a column
	// each: a wide rune shifts everything after it one column right, and
	// counting runes drew the tail of the field one column left of where the
	// terminal actually put it.
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
	// A wide rune straddling the left edge shows only its right-hand cell,
	// which is not a glyph — blank it rather than emit half a character.
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

// displayRunes is what Draw and the click-to-position math both measure:
// the value itself, or one '*' per rune in password mode. The masked form
// keeps the same rune count as the value, so a rune index means the same
// thing in both and no mapping between them is needed.
func (f *InputField) displayRunes() []rune {
	if f.password {
		return []rune(strings.Repeat("*", len(f.value)))
	}
	return f.value
}

// HandleKey processes keyboard input. Returns true if the event was
// consumed — only for keys InputField actually acts on. Everything else
// (Up/Down, Tab/Backtab, Esc, Enter, plain modifier keys, …) returns false
// so a caller like propsheet.Form can fall through to focus-cycling
// instead of the field silently swallowing the key.
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
	// dropSelection decides, after the switch below runs, whether to clear
	// the selection — starts true and is flipped to false by any case
	// that manages the selection's lifecycle itself (SelectAll).
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
	// Refused before the release branch: a disabled field never latched a
	// press, so it has no drag to end.
	if f.disabled {
		return false
	}
	if ev.Buttons() == tcell.ButtonNone {
		wasDragging := f.mouseDragging
		f.mouseDragging = false
		return wasDragging
	}
	// Once the press is latched the gesture is this field's until the
	// release, so motion is consumed wherever the pointer went. Hit-testing
	// it instead froze the selection the moment the pointer left the box —
	// dragging past the end of a term stopped extending it. Only reachable
	// when the host forwards off-rect motion here; one that routes purely by
	// position never produces the event.
	if !f.mouseDragging && !f.HitTest(ev.Position()) {
		return false
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}
	mx, _ := ev.Position()
	ix := f.inputX()
	// mx-ix-1 is a terminal-column offset into the box; the cursor is a rune
	// index. Converting is what keeps a click landing on the character it
	// was aimed at once the field holds a wide rune.
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

// adjustScroll keeps the caret inside the visible box. It works in display
// columns — the caret's column, not its rune index — so a field of wide
// characters scrolls twice as far per caret step, which is what the eye
// expects and what keeps the caret on screen at all.
func (f *InputField) adjustScroll() {
	runes := f.displayRunes()
	col := core.ColumnOfRune(runes, f.cursor)
	if col < f.scroll {
		f.scroll = col
	}
	if col >= f.scroll+f.rect.W {
		f.scroll = col - f.rect.W + 1
	}
	// Keeping the caret visible is not enough on its own: replacing a long
	// value with a short one (SetValue) leaves the caret at the new, shorter
	// end, and the rule above happily scrolls the window to start exactly
	// there — past every character in the field, which then draws blank over
	// a value that is really set. Shipped as "Browse returns C:\temp\aaa.bak
	// and the Destination box goes empty, but Script emits the right path".
	if last := core.ColumnOfRune(runes, len(runes)) - f.rect.W + 1; f.scroll > last {
		f.scroll = last
	}
	if f.scroll < 0 {
		f.scroll = 0
	}
}
