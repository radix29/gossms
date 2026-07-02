// Package widgets provides stateful, self-contained input controls that
// render themselves onto a tcell.Screen.  Each widget follows a common
// pattern:
//
//   - Bounds are set with SetBounds(x, y) or SetRect(core.Rect).
//   - Keyboard input is handled with HandleKey(*tcell.EventKey) bool.
//   - Mouse input is handled with HandleMouse(*tcell.EventMouse) bool.
//   - Rendering is done with Draw(tcell.Screen).
//   - Focus is toggled with Focus(bool).
//
// Widgets are purely presentational; they hold their own value state but
// know nothing about the application.  The caller reads values via Value(),
// Checked(), Selected(), etc.
package widgets

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// InputField
// ---------------------------------------------------------------------------

// InputField is a single-line text input control.
type InputField struct {
	rect     core.Rect
	value    []rune
	cursor   int
	scroll   int
	focused  bool
	password bool   // mask characters with *
	label    string // optional inline label drawn to the left
}

// NewInputField creates an InputField with an optional inline label.
// w is the visible width of the input area (excluding label and brackets).
func NewInputField(label string, w int, password bool) *InputField {
	return new(InputField{label: label, rect: core.Rect{W: w}, password: password})
}

// SetBounds positions the widget. The label is drawn at (x,y); the input
// box starts immediately after the label.
func (f *InputField) SetBounds(x, y int) { f.rect.X, f.rect.Y = x, y }

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

// InputX returns the x coordinate of the input box (after label).
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
		labelStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
		core.DrawText(s, f.rect.X, f.rect.Y, labelStyle, f.label)
	}
	ix := f.inputX()
	borderColor := p.InputBorder
	if f.focused {
		borderColor = p.InputFocused
	}
	borderStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(borderColor)
	s.SetContent(ix, f.rect.Y, '[', nil, borderStyle)
	s.SetContent(ix+f.rect.W+1, f.rect.Y, ']', nil, borderStyle)

	display := string(f.value)
	if f.password {
		display = strings.Repeat("*", len(f.value))
	}
	runes := []rune(display)
	inputStyle := theme.StyleInput()
	for col := 0; col < f.rect.W; col++ {
		ch := ' '
		if f.scroll+col < len(runes) {
			ch = runes[f.scroll+col]
		}
		cellStyle := inputStyle
		if f.focused && f.scroll+col == f.cursor {
			cellStyle = tcell.StyleDefault.Background(p.BorderActive).Foreground(tcell.ColorWhite)
		}
		s.SetContent(ix+1+col, f.rect.Y, ch, nil, cellStyle)
	}
}

// HandleKey processes keyboard input. Returns true if the event was consumed.
func (f *InputField) HandleKey(ev *tcell.EventKey) bool {
	if !f.focused {
		return false
	}
	switch ev.Key() {
	case tcell.KeyLeft:
		if f.cursor > 0 {
			f.cursor--
		}
	case tcell.KeyRight:
		if f.cursor < len(f.value) {
			f.cursor++
		}
	case tcell.KeyHome, tcell.KeyCtrlA:
		f.cursor = 0
	case tcell.KeyEnd:
		f.cursor = len(f.value)
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if f.cursor > 0 {
			f.value = append(f.value[:f.cursor-1], f.value[f.cursor:]...)
			f.cursor--
		}
	case tcell.KeyDelete:
		if f.cursor < len(f.value) {
			f.value = append(f.value[:f.cursor], f.value[f.cursor+1:]...)
		}
	case tcell.KeyCtrlU:
		f.value = nil
		f.cursor = 0
	default:
		r := core.EvRune(ev)
		if r != 0 {
			newVal := make([]rune, len(f.value)+1)
			copy(newVal, f.value[:f.cursor])
			newVal[f.cursor] = r
			copy(newVal[f.cursor+1:], f.value[f.cursor:])
			f.value = newVal
			f.cursor++
		}
	}
	f.adjustScroll()
	return true
}

func (f *InputField) adjustScroll() {
	if f.cursor < f.scroll {
		f.scroll = f.cursor
	}
	if f.cursor >= f.scroll+f.rect.W {
		f.scroll = f.cursor - f.rect.W + 1
	}
}

// ---------------------------------------------------------------------------
// DropDown
// ---------------------------------------------------------------------------

// DropDown is a selector control that shows a pop-up list when open.
type DropDown struct {
	rect     core.Rect
	items    []string
	selected int
	focused  bool
	label    string
	open     bool
}

// NewDropDown creates a DropDown.
func NewDropDown(label string, items []string, w int) *DropDown {
	return new(DropDown{label: label, items: items, rect: core.Rect{W: w}})
}

func (d *DropDown) SetBounds(x, y int) { d.rect.X, d.rect.Y = x, y }
func (d *DropDown) Selected() int      { return d.selected }
func (d *DropDown) SetSelected(i int) {
	if i >= 0 && i < len(d.items) {
		d.selected = i
	}
}
func (d *DropDown) Focus(v bool) { d.focused = v; if !v { d.open = false } }
func (d *DropDown) Value() string {
	if d.selected >= 0 && d.selected < len(d.items) {
		return d.items[d.selected]
	}
	return ""
}
func (d *DropDown) IsOpen() bool { return d.open }

func (d *DropDown) inputX() int {
	if d.label != "" {
		return d.rect.X + core.DisplayWidth(d.label) + 1
	}
	return d.rect.X
}

// Draw renders the drop-down and its open list (if open).
// Draw renders the closed widget box (label, value, arrow). If the list is
// open, call DrawOverlay afterward — once every other widget in the same
// dialog has been drawn — so the open list isn't painted over by fields
// positioned below this one.
func (d *DropDown) Draw(s tcell.Screen) {
	p := theme.Active()
	if d.label != "" {
		drawLabel(s, d.rect.X, d.rect.Y, d.label, p)
	}
	ix := d.inputX()
	borderColor := p.InputBorder
	if d.focused {
		borderColor = p.InputFocused
	}
	borderStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(borderColor)
	s.SetContent(ix, d.rect.Y, '[', nil, borderStyle)
	s.SetContent(ix+d.rect.W+1, d.rect.Y, ']', nil, borderStyle)
	s.SetContent(ix+d.rect.W, d.rect.Y, 'v', nil, borderStyle)

	inputStyle := theme.StyleInput()
	core.FillRect(s, core.Rect{X: ix + 1, Y: d.rect.Y, W: d.rect.W - 1, H: 1}, ' ', inputStyle)
	core.DrawTextClipped(s, ix+1, d.rect.Y, d.rect.W-1, inputStyle, d.Value())
}

// DrawOverlay renders the open item list, if open. Must be called after
// every other widget in the same dialog has drawn, so nothing below this
// dropdown paints over the list.
func (d *DropDown) DrawOverlay(s tcell.Screen) {
	if !d.open {
		return
	}
	p := theme.Active()
	ix := d.inputX()
	listStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
	selStyle := theme.StyleSelected()
	for i, item := range d.items {
		st := listStyle
		if i == d.selected {
			st = selStyle
		}
		core.FillRect(s, core.Rect{X: ix + 1, Y: d.rect.Y + 1 + i, W: d.rect.W, H: 1}, ' ', st)
		core.DrawTextClipped(s, ix+1, d.rect.Y+1+i, d.rect.W, st, item)
	}
}

// HandleKey processes keyboard input.
func (d *DropDown) HandleKey(ev *tcell.EventKey) bool {
	if !d.focused {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEnter, tcell.KeyF4:
		d.open = !d.open
	case tcell.KeyUp:
		if d.open && d.selected > 0 {
			d.selected--
		}
	case tcell.KeyDown:
		if d.open && d.selected < len(d.items)-1 {
			d.selected++
		}
	case tcell.KeyEscape:
		d.open = false
	default:
		return false
	}
	return true
}

// HandleMouse processes mouse events.
func (d *DropDown) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	ix := d.inputX()
	if ev.Buttons() != tcell.Button1 {
		return false
	}
	if my == d.rect.Y && mx >= ix && mx <= ix+d.rect.W+1 {
		d.open = !d.open
		return true
	}
	if d.open && my >= d.rect.Y+1 && my < d.rect.Y+1+len(d.items) &&
		mx >= ix+1 && mx <= ix+d.rect.W {
		d.selected = my - d.rect.Y - 1
		d.open = false
		return true
	}
	if d.open {
		d.open = false
	}
	return false
}

// ---------------------------------------------------------------------------
// CheckBox
// ---------------------------------------------------------------------------

// CheckBox is a boolean toggle control.
type CheckBox struct {
	rect    core.Rect
	label   string
	checked bool
	focused bool
}

// NewCheckBox creates a CheckBox.
func NewCheckBox(label string) *CheckBox {
	return new(CheckBox{label: label})
}

func (c *CheckBox) SetBounds(x, y int) { c.rect.X, c.rect.Y = x, y }
func (c *CheckBox) RectX() int         { return c.rect.X }
func (c *CheckBox) RectY() int         { return c.rect.Y }
func (c *CheckBox) Checked() bool      { return c.checked }
func (c *CheckBox) SetChecked(v bool)  { c.checked = v }
func (c *CheckBox) Focus(v bool)       { c.focused = v }

// Draw renders the check box.
func (c *CheckBox) Draw(s tcell.Screen) {
	p := theme.Active()
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	if c.focused {
		st = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.BorderActive)
	}
	box := "[ ] "
	if c.checked {
		box = "[x] "
	}
	core.DrawText(s, c.rect.X, c.rect.Y, st, box+c.label)
}

// HandleKey processes keyboard input.
func (c *CheckBox) HandleKey(ev *tcell.EventKey) bool {
	if !c.focused {
		return false
	}
	if ev.Key() == tcell.KeyEnter || core.EvRune(ev) == ' ' {
		c.checked = !c.checked
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Button
// ---------------------------------------------------------------------------

// Button is a clickable, focusable button.
type Button struct {
	rect    core.Rect
	label   string
	focused bool
	OnClick func()
}

// NewButton creates a Button.
func NewButton(label string, onClick func()) *Button {
	return new(Button{label: label, OnClick: onClick})
}

func (b *Button) SetBounds(x, y int) { b.rect.X, b.rect.Y = x, y }
func (b *Button) Focus(v bool)       { b.focused = v }

// Width returns the rendered width of this button.
func (b *Button) Width() int { return core.DisplayWidth(b.label) + 4 } // "[ label ]"

// Draw renders the button.
func (b *Button) Draw(s tcell.Screen) {
	st := theme.StyleButton()
	if b.focused {
		st = theme.StyleButtonActive()
	}
	core.DrawText(s, b.rect.X, b.rect.Y, st, "[ "+b.label+" ]")
}

// HandleKey processes keyboard input.
func (b *Button) HandleKey(ev *tcell.EventKey) bool {
	if !b.focused {
		return false
	}
	if ev.Key() == tcell.KeyEnter {
		if b.OnClick != nil {
			b.OnClick()
		}
		return true
	}
	return false
}

// HandleMouse processes mouse events.
func (b *Button) HandleMouse(ev *tcell.EventMouse) bool {
	if ev.Buttons() != tcell.Button1 {
		return false
	}
	mx, my := ev.Position()
	if my == b.rect.Y && mx >= b.rect.X && mx < b.rect.X+b.Width() {
		if b.OnClick != nil {
			b.OnClick()
		}
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

func drawLabel(s tcell.Screen, x, y int, label string, p *theme.Palette) {
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	core.DrawText(s, x, y, st, label)
}
