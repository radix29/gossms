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

	// press distinguishes a fresh Button1 press from a continued hold over the
	// same row (as TreeView). Without it, tcell's all-motion mouse tracking resends
	// Button1 on every cursor motion while the button stays down, and each resend
	// would re-select, and pair with its own press in clicks as a double-click.
	press core.PressLatch

	// sbDragging is true while the user is dragging the scrollbar thumb —
	// see DataGrid's field of the same name and purpose for the rationale
	// on why this is a separate flag from press.
	sbDragging bool

	// clicks pairs fresh presses on one row into the double-click that fires
	// OnActivate. A click on the already-selected row only selects it: the list
	// often opens with row 0 selected, and a single click there must not run the
	// default action (one click on a connection-history list's first entry used
	// to connect).
	clicks core.ClickTracker[int]

	// OnSelect fires whenever the selected index changes (arrow keys,
	// click). OnActivate fires on Enter, or a double-click on a row.
	OnSelect   func(i int)
	OnActivate func(i int)
}

// NewListBox creates an empty ListBox.
func NewListBox() *ListBox {
	return new(ListBox{})
}

// SetBounds positions the list box. A change of bounds also re-clamps scroll
// against the new height: Show() selects the first page (and scrolls it into view)
// before the first Draw/Layout pass assigns real bounds, so ensureVisible's first
// call runs against a zero-value rect.H and can scroll one row too far; re-running
// it once real bounds are known self-corrects that, and keeps the selection in
// view across any later resize.
//
// Bounds that have not moved do nothing, deliberately. Callers lay out from Draw,
// so the same rect is re-asserted every frame, and re-running ensureVisible there
// would snap scroll back onto the selection between frames: the wheel could not
// scroll past the selected row at all.
func (l *ListBox) SetBounds(x, y, w, h int) {
	r := core.Rect{X: x, Y: y, W: w, H: h}
	if r == l.rect {
		return
	}
	l.rect = r
	l.ensureVisible()
}

// SetItems replaces the item list, clamping selection/scroll into range.
func (l *ListBox) SetItems(items []string) {
	l.items = items
	// A press on the old list's row 3 and one on the new list's are not a double-click.
	l.clicks.Reset()
	l.sel = core.Clamp(l.sel, 0, max(0, len(items)-1))
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

func (l *ListBox) Focused() bool { return l.focused }

// Bounds returns the rect SetBounds last gave the list.
func (l *ListBox) Bounds() core.Rect { return l.rect }

// Draw renders the list.
func (l *ListBox) Draw(s tcell.Screen) {
	p := theme.Active()
	base := theme.StyleDialog()
	core.FillRect(s, l.rect, ' ', base)

	// The scrollbar takes the last column, so text stops short of it rather
	// than running underneath (DropDown's list reserves it the same way).
	hasBar := len(l.items) > l.rect.H && l.rect.H > 0
	textW := l.rect.W
	if hasBar {
		textW--
	}

	for row := range l.rect.H {
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
				st = theme.On(p.DialogBg, p.DialogTextHighlight)
			}
			core.FillRect(s, core.Rect{X: l.rect.X, Y: y, W: l.rect.W, H: 1}, ' ', st)
		}
		core.DrawTextClipped(s, l.rect.X, y, textW, st, marker+l.items[idx])
	}

	if hasBar {
		sbStyle := theme.StyleDialogLine()
		sbThumb := theme.StyleScrollThumb()
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
		l.sel = max(0, l.sel-l.rect.H)
		l.ensureVisible()
		l.fireSelect()
	case tcell.KeyPgDn:
		l.sel = min(len(l.items)-1, l.sel+l.rect.H)
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
	if l.press.Release(ev) {
		l.sbDragging = false
	}
	mx, my := ev.Position()

	// Scrollbar drag/click takes priority over row hit-testing below — the
	// bar is drawn at rect.Right()-1 (see Draw), which without this check
	// first would otherwise be read as a click on whatever item sits in
	// that screen column. It runs ahead of the bounds check: a latched thumb
	// drag keeps control once the mouse leaves the list, so a fast drag past
	// the bottom still reaches the end.
	if core.HandleScrollbarDrag(ev, l.rect.Right()-1, l.rect.Y, l.rect.H, len(l.items), &l.sbDragging, &l.scroll) {
		return true
	}
	if !l.rect.Contains(mx, my) {
		// A press that began on the list owns its motion until release; letting
		// it fall through would hand the host a drag it never saw start.
		return l.press.Held() && ev.Buttons() == tcell.Button1
	}

	switch ev.Buttons() {
	case tcell.Button1:
		if !l.press.Fresh(ev) {
			// Still the same physical press — do not re-select or
			// re-activate on every resent motion event.
			return true
		}
		idx := l.scroll + (my - l.rect.Y)
		if idx >= 0 && idx < len(l.items) {
			double := l.clicks.Press(idx, ev.When())
			if idx != l.sel {
				l.sel = idx
				l.fireSelect()
			}
			if double && l.OnActivate != nil {
				l.OnActivate(l.sel)
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
	// Never leave blank rows below the last item: a list that grew taller, or
	// one whose items were replaced by fewer, keeps a scroll offset the new
	// extent no longer justifies.
	if maxScroll := len(l.items) - l.rect.H; l.scroll > maxScroll {
		l.scroll = maxScroll
	}
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
