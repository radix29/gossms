package widgets

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// CheckBox is a boolean toggle control.
type CheckBox struct {
	rect     core.Rect
	label    string
	checked  bool
	focused  bool
	disabled bool

	// mouseDragging distinguishes a fresh Button1 press from a continued
	// hold over the box — mirrors Toolbar's/TreeView's/MenuBar's field of
	// the same name and purpose. Without it, tcell's all-motion mouse
	// tracking resends Buttons()==Button1 on every motion event while the
	// button stays down, so a click that so much as twitches before
	// release toggles again on every resent event instead of once per
	// physical click.
	mouseDragging bool
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

// SetEnabled toggles whether the box can be changed. A disabled box draws
// greyed out *and* refuses keys and clicks: one that only stopped accepting
// input would look like a live box ignoring the user. It keeps its place in
// the caller's focus ring — mirroring InputField.SetEnabled, which every
// property page already relies on — so Tab order does not shift under the
// user when a page switches a control off.
func (c *CheckBox) SetEnabled(v bool) { c.disabled = !v }

// Enabled reports whether the box can be changed.
func (c *CheckBox) Enabled() bool { return !c.disabled }

// Width returns the display width of the box glyph plus label — the
// clickable region HandleMouse hit-tests against.
func (c *CheckBox) Width() int { return core.DisplayWidth(c.label) + 4 }

// Draw renders the check box.
func (c *CheckBox) Draw(s tcell.Screen) {
	p := theme.Active()
	st := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	switch {
	case c.disabled:
		st = theme.StyleControlDisabled()
	case c.focused:
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
	if !c.focused || c.disabled {
		return false
	}
	if ev.Key() == tcell.KeyEnter || core.EvRune(ev) == ' ' {
		c.checked = !c.checked
		return true
	}
	return false
}

// HandleMouse toggles the checkbox on a fresh Button1 press over its box
// or label.
func (c *CheckBox) HandleMouse(ev *tcell.EventMouse) bool {
	if ev.Buttons() == tcell.ButtonNone {
		c.mouseDragging = false
		return false
	}
	if c.disabled || ev.Buttons() != tcell.Button1 {
		return false
	}
	mx, my := ev.Position()
	if my != c.rect.Y || mx < c.rect.X || mx >= c.rect.X+c.Width() {
		return false
	}
	if c.mouseDragging {
		return true
	}
	c.mouseDragging = true
	c.checked = !c.checked
	return true
}
