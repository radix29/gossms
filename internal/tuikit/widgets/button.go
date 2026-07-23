package widgets

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// Button is a clickable, focusable button.
type Button struct {
	rect    core.Rect
	label   string
	focused bool
	OnClick func()

	// mouseDragging distinguishes a fresh Button1 press from a continued
	// hold over the button — mirrors Toolbar's/TreeView's/MenuBar's field
	// of the same name and purpose. Without it, tcell's all-motion mouse
	// tracking resends Buttons()==Button1 on every motion event while the
	// button stays down, so a click that so much as twitches before
	// release fires OnClick again on every resent event instead of once
	// per physical click.
	mouseDragging bool
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
	if ev.Buttons() == tcell.ButtonNone {
		b.mouseDragging = false
		return false
	}
	if ev.Buttons() != tcell.Button1 {
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
