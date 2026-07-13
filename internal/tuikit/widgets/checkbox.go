package widgets

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

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
