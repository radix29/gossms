package widgets

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

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

// HandleKey processes keyboard input. Returns false for a key it doesn't
// act on — in particular Up/Down/Escape while closed fall through instead
// of being swallowed, so a caller like propsheet.Form can move focus to
// the next row instead of a closed dropdown eating arrow navigation.
func (d *DropDown) HandleKey(ev *tcell.EventKey) bool {
	if !d.focused {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEnter, tcell.KeyF4:
		d.open = !d.open
	case tcell.KeyUp:
		if !d.open {
			return false
		}
		if d.selected > 0 {
			d.selected--
		}
	case tcell.KeyDown:
		if !d.open {
			return false
		}
		if d.selected < len(d.items)-1 {
			d.selected++
		}
	case tcell.KeyEscape:
		if !d.open {
			return false
		}
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
