package widgets

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// Button is a clickable, focusable button.
type Button struct {
	rect     core.Rect
	label    string
	focused  bool
	disabled bool
	OnClick  func()

	// mouseDragging separates a fresh Button1 press from a resent hold (same field
	// as Toolbar/TreeView/MenuBar): all-motion tracking resends Button1 on every
	// motion, so a twitchy click would fire OnClick repeatedly.
	mouseDragging bool
}

// NewButton creates a Button.
func NewButton(label string, onClick func()) *Button {
	return new(Button{label: label, OnClick: onClick})
}

func (b *Button) SetBounds(x, y int) { b.rect.X, b.rect.Y = x, y }
func (b *Button) Focus(v bool)       { b.focused = v }

// SetEnabled toggles whether the button fires. A disabled button draws greyed
// and refuses keys and clicks, keeping its place in the focus ring (see
// CheckBox.SetEnabled).
func (b *Button) SetEnabled(v bool) { b.disabled = !v }

// Enabled reports whether the button fires.
func (b *Button) Enabled() bool { return !b.disabled }

// Width returns the rendered width of this button.
func (b *Button) Width() int { return core.DisplayWidth(b.label) + 4 } // "[ label ]"

// Label returns the button's text.
func (b *Button) Label() string { return b.label }

// Draw renders the button.
func (b *Button) Draw(s tcell.Screen) {
	st := theme.StyleButton()
	switch {
	case b.disabled:
		st = theme.StyleButtonDisabled()
	case b.focused:
		st = theme.StyleButtonActive()
	}
	core.DrawText(s, b.rect.X, b.rect.Y, st, "[ "+b.label+" ]")
}

// HandleKey processes keyboard input.
func (b *Button) HandleKey(ev *tcell.EventKey) bool {
	if !b.focused || b.disabled {
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
	if ev.Buttons() == tcell.ButtonNone {
		b.mouseDragging = false
		return false
	}
	if b.disabled || ev.Buttons() != tcell.Button1 {
		return false
	}
	mx, my := ev.Position()
	if my == b.rect.Y && mx >= b.rect.X && mx < b.rect.X+b.Width() {
		if !b.mouseDragging {
			b.mouseDragging = true
			if b.OnClick != nil {
				b.OnClick()
			}
		}
		return true
	}
	return false
}
