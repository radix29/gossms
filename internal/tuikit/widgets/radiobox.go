package widgets

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// RadioBox is a single-select group of mutually exclusive options, navigated
// as one focusable unit — Up/Down move the selection and a click on any
// option selects it directly.
type RadioBox struct {
	rect     core.Rect
	label    string
	options  []string
	selected int
	focused  bool
}

// NewRadioBox creates a RadioBox with the given options. Selection starts at
// index 0.
func NewRadioBox(label string, options []string) *RadioBox {
	return new(RadioBox{label: label, options: options})
}

func (r *RadioBox) SetBounds(x, y int) { r.rect.X, r.rect.Y = x, y }
func (r *RadioBox) Focus(v bool)       { r.focused = v }
func (r *RadioBox) Selected() int      { return r.selected }
func (r *RadioBox) SetSelected(i int) {
	if i >= 0 && i < len(r.options) {
		r.selected = i
	}
}

// Height returns the number of rows this widget occupies: the label row
// (if any) plus one row per option.
func (r *RadioBox) Height() int {
	h := len(r.options)
	if r.label != "" {
		h++
	}
	return h
}

// Width returns the display width of the widest option row.
func (r *RadioBox) Width() int {
	w := 0
	for _, opt := range r.options {
		if ow := core.DisplayWidth(opt) + 4; ow > w { // "(o) " + label
			w = ow
		}
	}
	return w
}

func (r *RadioBox) optionsY() int {
	if r.label != "" {
		return r.rect.Y + 1
	}
	return r.rect.Y
}

// Draw renders the group label (if any) and each option row.
func (r *RadioBox) Draw(s tcell.Screen) {
	p := theme.Active()
	if r.label != "" {
		drawLabel(s, r.rect.X, r.rect.Y, r.label, p)
	}
	base := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	activeStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.BorderActive)
	for i, opt := range r.options {
		st := base
		if r.focused && i == r.selected {
			st = activeStyle
		}
		mark := "( ) "
		if i == r.selected {
			mark = "(o) "
		}
		core.DrawText(s, r.rect.X, r.optionsY()+i, st, mark+opt)
	}
}

// HandleKey moves the selection with Up/Down. Returns false at either
// boundary instead of consuming the key as a no-op, so a caller like
// propsheet.Form can move focus to the next/previous row instead.
func (r *RadioBox) HandleKey(ev *tcell.EventKey) bool {
	if !r.focused {
		return false
	}
	switch ev.Key() {
	case tcell.KeyUp:
		if r.selected <= 0 {
			return false
		}
		r.selected--
		return true
	case tcell.KeyDown:
		if r.selected >= len(r.options)-1 {
			return false
		}
		r.selected++
		return true
	}
	return false
}

// HandleMouse selects the option clicked.
func (r *RadioBox) HandleMouse(ev *tcell.EventMouse) bool {
	if ev.Buttons() != tcell.Button1 {
		return false
	}
	mx, my := ev.Position()
	w := r.Width()
	for i := range r.options {
		y := r.optionsY() + i
		if my == y && mx >= r.rect.X && mx < r.rect.X+w {
			r.selected = i
			return true
		}
	}
	return false
}
