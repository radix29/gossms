package controls

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ListBox is a scrollable, single-column selectable list of strings.
type ListBox struct {
	rect    core.Rect
	items   []string
	sel     int
	scroll  int
	focused bool

	// OnSelect fires whenever the selected index changes (arrow keys,
	// click). OnActivate fires on Enter or a click that lands on an
	// already-selected row (double-activation semantics are the caller's
	// choice — ListBox itself fires on every click that changes nothing).
	OnSelect   func(i int)
	OnActivate func(i int)
}

// NewListBox creates an empty ListBox.
func NewListBox() *ListBox {
	return new(ListBox{})
}

// SetBounds positions the list box. Also re-clamps scroll against the new
// height: Show() selects the first page (and scrolls it into view) before
// the first Draw/Layout pass ever assigns real bounds, so ensureVisible's
// very first call runs against a zero-value rect.H and can scroll one row
// too far — re-running it here once real bounds are known self-corrects
// that, and keeps the selection in view across any later resize too.
func (l *ListBox) SetBounds(x, y, w, h int) {
	l.rect = core.Rect{X: x, Y: y, W: w, H: h}
	l.ensureVisible()
}

// SetItems replaces the item list, clamping selection/scroll into range.
func (l *ListBox) SetItems(items []string) {
	l.items = items
	l.sel = core.Clamp(l.sel, 0, core.Max(0, len(items)-1))
	l.ensureVisible()
}

// Selected returns the selected index, or -1 if the list is empty.
func (l *ListBox) Selected() int {
	if len(l.items) == 0 {
		return -1
	}
	return l.sel
}

// SetSelected sets the selected index (clamped) and scrolls it into view.
// Does not fire OnSelect — callers driving selection programmatically
// (e.g. a dialog restoring state) usually don't want their own callback
// re-entered.
func (l *ListBox) SetSelected(i int) {
	if len(l.items) == 0 {
		return
	}
	l.sel = core.Clamp(i, 0, len(l.items)-1)
	l.ensureVisible()
}

// Focus sets the focused state.
func (l *ListBox) Focus(v bool) { l.focused = v }

// Draw renders the list.
func (l *ListBox) Draw(s tcell.Screen) {
	p := theme.Active()
	base := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	core.FillRect(s, l.rect, ' ', base)

	for row := 0; row < l.rect.H; row++ {
		idx := l.scroll + row
		if idx >= len(l.items) {
			break
		}
		y := l.rect.Y + row
		st := base
		marker := "  "
		if idx == l.sel {
			marker = "▸ "
			if l.focused {
				st = theme.StyleSelected()
			} else {
				st = tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextHighlight)
			}
			core.FillRect(s, core.Rect{X: l.rect.X, Y: y, W: l.rect.W, H: 1}, ' ', st)
		}
		core.DrawTextClipped(s, l.rect.X, y, l.rect.W, st, marker+l.items[idx])
	}

	if len(l.items) > l.rect.H && l.rect.H > 0 {
		sbStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		core.DrawScrollbar(s, l.rect.Right()-1, l.rect.Y, l.rect.H, len(l.items), l.rect.H, l.scroll, sbStyle, sbThumb)
	}
}

// HandleKey processes keyboard navigation.
func (l *ListBox) HandleKey(ev *tcell.EventKey) bool {
	if !l.focused || len(l.items) == 0 {
		return false
	}
	switch ev.Key() {
	case tcell.KeyUp:
		if l.sel > 0 {
			l.sel--
			l.ensureVisible()
			l.fireSelect()
		}
	case tcell.KeyDown:
		if l.sel < len(l.items)-1 {
			l.sel++
			l.ensureVisible()
			l.fireSelect()
		}
	case tcell.KeyPgUp:
		l.sel = core.Max(0, l.sel-l.rect.H)
		l.ensureVisible()
		l.fireSelect()
	case tcell.KeyPgDn:
		l.sel = core.Min(len(l.items)-1, l.sel+l.rect.H)
		l.ensureVisible()
		l.fireSelect()
	case tcell.KeyHome:
		l.sel, l.scroll = 0, 0
		l.fireSelect()
	case tcell.KeyEnd:
		l.sel = len(l.items) - 1
		l.ensureVisible()
		l.fireSelect()
	case tcell.KeyEnter:
		if l.OnActivate != nil {
			l.OnActivate(l.sel)
		}
	default:
		return false
	}
	return true
}

// HandleMouse processes mouse clicks and wheel scroll.
func (l *ListBox) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	if !l.rect.Contains(mx, my) {
		return false
	}
	switch ev.Buttons() {
	case tcell.Button1:
		idx := l.scroll + (my - l.rect.Y)
		if idx >= 0 && idx < len(l.items) {
			same := idx == l.sel
			l.sel = idx
			if same && l.OnActivate != nil {
				l.OnActivate(l.sel)
			} else {
				l.fireSelect()
			}
		}
	case tcell.WheelUp:
		if l.scroll > 0 {
			l.scroll--
		}
	case tcell.WheelDown:
		if l.scroll < len(l.items)-l.rect.H {
			l.scroll++
		}
	}
	return true
}

func (l *ListBox) fireSelect() {
	if l.OnSelect != nil {
		l.OnSelect(l.sel)
	}
}

func (l *ListBox) ensureVisible() {
	if l.sel < l.scroll {
		l.scroll = l.sel
	}
	if l.sel >= l.scroll+l.rect.H {
		l.scroll = l.sel - l.rect.H + 1
	}
	if l.scroll < 0 {
		l.scroll = 0
	}
}
