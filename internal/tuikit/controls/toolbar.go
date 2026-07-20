package controls

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ToolbarButton is a single icon-only button in a Toolbar. Unlike MenuItem
// it carries no text label — Icon is all that's drawn — and Tooltip is
// shown in a floating hint on hover instead of inline text.
type ToolbarButton struct {
	Icon    string
	Tooltip string
	Action  func()      // nil still renders and hovers normally, but clicking is a no-op
	Divider bool        // renders Icon as static text (e.g. "|"); never hovers, tooltips, or clicks
	Enabled func() bool // nil means always enabled
}

// enabled reports whether b can be activated right now.
func (b ToolbarButton) enabled() bool {
	return b.Enabled == nil || b.Enabled()
}

// toolbarButtonPad is the number of blank columns either side of a
// button's icon, for visual breathing room and a comfortable mouse target.
const toolbarButtonPad = 1

// Toolbar is a horizontal row of icon-only buttons, right-aligned within
// its bounds — designed to sit flush against the right edge of MenuBar's
// row, sharing the same line. Hovering a button shows a borderless
// tooltip styled like SSMS's yellow query-execution status bar
// (theme.StyleGridStatus). Call DrawOverlay after every other panel has
// drawn, same as MenuBar's own dropdown, since the tooltip extends into
// the row below the bar.
type Toolbar struct {
	rect    core.Rect
	buttons []ToolbarButton
	starts  []int // starts[i] is the column button i begins at
	widths  []int // widths[i] is button i's total width (icon + padding)
	hover   int   // index of the hovered button, -1 = none

	// mouseDragging distinguishes a fresh Button1 press (fire the button's
	// Action) from a continued hold over the same button — mirrors
	// MenuBar's and TreeView's field of the same name and purpose. Without
	// it, the mouse tracking mode gossms enables (core.NewScreen's
	// EnableMouse()) resends Buttons()==Button1 on every motion event while
	// the button stays down, so a click that so much as twitches before
	// release fires Action again — visibly flickering a toggle button
	// (e.g. Include Actual Execution Plan) back and forth instead of
	// flipping it once.
	mouseDragging bool
}

// NewToolbar creates an empty Toolbar.
func NewToolbar() *Toolbar {
	return &Toolbar{hover: -1}
}

// SetButtons replaces all buttons.
func (tb *Toolbar) SetButtons(buttons []ToolbarButton) {
	tb.buttons = buttons
	tb.hover = -1
}

// SetBounds positions the toolbar flush against the right edge of the row
// [x, x+w), at height y — the same row MenuBar occupies.
func (tb *Toolbar) SetBounds(x, y, w int) {
	widths := make([]int, len(tb.buttons))
	total := 0
	for i, b := range tb.buttons {
		bw := core.DisplayWidth(b.Icon) + toolbarButtonPad*2
		widths[i] = bw
		total += bw
	}
	starts := make([]int, len(tb.buttons))
	col := x + w - total
	for i, bw := range widths {
		starts[i] = col
		col += bw
	}
	tb.rect = core.Rect{X: x + w - total, Y: y, W: total, H: 1}
	tb.widths = widths
	tb.starts = starts
}

// buttonAt returns the index of the button containing column mx, or -1 —
// dividers occupy a column range like any other entry but are never
// returned, so they never hover, tooltip, or click. A disabled (but
// non-divider) button IS returned, so it still hovers and shows its
// tooltip; HandleMouse is what actually withholds firing its Action.
func (tb *Toolbar) buttonAt(mx int) int {
	for i, start := range tb.starts {
		if tb.buttons[i].Divider {
			continue
		}
		if mx >= start && mx < start+tb.widths[i] {
			return i
		}
	}
	return -1
}

// Draw renders the button row.
func (tb *Toolbar) Draw(s tcell.Screen) {
	barStyle := theme.StyleMenuBar()
	hoverStyle := tcell.StyleDefault.Background(theme.Active().MenuSelected).Foreground(tcell.ColorWhite)
	disabledStyle := theme.StyleDisabled()
	for i, b := range tb.buttons {
		st := barStyle
		switch {
		case !b.Divider && !b.enabled():
			st = disabledStyle
		case i == tb.hover:
			st = hoverStyle
		}
		x := tb.starts[i]
		core.FillRect(s, core.Rect{X: x, Y: tb.rect.Y, W: tb.widths[i], H: 1}, ' ', st)
		core.DrawText(s, x+toolbarButtonPad, tb.rect.Y, st, b.Icon)
	}
}

// DrawOverlay renders the hovered button's tooltip, if any — a
// borderless, single-line hint styled like SSMS's yellow query-execution
// status bar.
func (tb *Toolbar) DrawOverlay(s tcell.Screen) {
	if tb.hover < 0 || tb.hover >= len(tb.buttons) {
		return
	}
	tip := tb.buttons[tb.hover].Tooltip
	if tip == "" {
		return
	}
	sw, _ := s.Size()
	w := core.DisplayWidth(tip) + 2
	x := tb.starts[tb.hover]
	if x+w > sw {
		x = sw - w
	}
	if x < 0 {
		x = 0
	}
	y := tb.rect.Y + 1
	st := theme.StyleGridStatus()
	core.FillRect(s, core.Rect{X: x, Y: y, W: w, H: 1}, ' ', st)
	core.DrawText(s, x+1, y, st, tip)
}

// HandleMouse updates hover state and fires a button's Action on click.
// Returns true if the event's position falls within the toolbar's own
// bounds (whether or not a button was actually hit), so the caller can
// tell its region apart from MenuBar's, which occupies the rest of the
// same row.
func (tb *Toolbar) HandleMouse(ev *tcell.EventMouse) bool {
	if ev.Buttons() == tcell.ButtonNone {
		tb.mouseDragging = false
	}
	mx, my := ev.Position()
	if !tb.rect.Contains(mx, my) {
		tb.hover = -1
		return false
	}
	idx := tb.buttonAt(mx)
	tb.hover = idx
	if idx >= 0 && ev.Buttons() == tcell.Button1 && !tb.mouseDragging {
		tb.mouseDragging = true
		if b := tb.buttons[idx]; b.Action != nil && b.enabled() {
			b.Action()
		}
	}
	return true
}
