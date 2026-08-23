package controls

import (
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// MenuBar
// ---------------------------------------------------------------------------

// MenuBar is the horizontal application menu bar with drop-down menus.
type MenuBar struct {
	rect         core.Rect
	menus        []Menu
	openMenu     int // -1 = closed
	hoverMenu    int
	selectedItem int // index within menus[openMenu].Items currently highlighted

	// mouseDragging distinguishes a fresh Button1 press, which toggles the
	// header, from a continued hold over it — DataGrid and Editor have the same
	// field. Without it, all-motion tracking resends Button1 on every motion
	// while the button is down, so a click that twitches re-toggles the header
	// it just opened right back closed.
	mouseDragging bool

	// cascade holds any open submenu levels of the open dropdown (see
	// menuCascade), cleared whenever the dropdown closes or switches menus.
	cascade menuCascade

	// scrollTop is the index of the first item drawn, for a dropdown taller than
	// the rows below the bar. A dropdown hangs off its own header and can't be
	// shifted up the way a ContextMenu can, so once it doesn't fit the overflow
	// has nowhere to go but out of view — Edit's 21 items need 23 rows, and on a
	// 20-row terminal its last three paint past the bottom edge, invisible and
	// unreachable. Capping the height alone hides them behind a tidy border,
	// which is worse. See drawDropdown.
	scrollTop int

	// drawnRect caches the box Draw last clamped the dropdown to, as ContextMenu
	// caches drawnX/drawnY: only Draw sees a tcell.Screen, so only Draw knows the
	// screen height the clamp needs, and HandleMouse must hit-test what was
	// painted.
	drawnRect core.Rect
}

// NewMenuBar creates a MenuBar.
func NewMenuBar() *MenuBar {
	return new(MenuBar{openMenu: -1, hoverMenu: -1})
}

// SetBounds positions the menu bar.
func (mb *MenuBar) SetBounds(x, y, w int) {
	mb.rect = core.Rect{X: x, Y: y, W: w, H: 1}
}

// SetMenus replaces all menus.
func (mb *MenuBar) SetMenus(menus []Menu) { mb.menus = menus }

// IsOpen reports whether a dropdown is currently open.
func (mb *MenuBar) IsOpen() bool { return mb.openMenu >= 0 }

// Close closes any open dropdown and every submenu under it.
func (mb *MenuBar) Close() {
	mb.openMenu = -1
	mb.cascade.reset()
	mb.scrollTop = 0
	mb.drawnRect = core.Rect{}
}

// Open opens the first menu without a mouse click — the F10 convention. Does
// nothing if there are no menus, or one is already open.
func (mb *MenuBar) Open() {
	if mb.openMenu < 0 && len(mb.menus) > 0 {
		mb.openMenu = 0
		mb.hoverMenu = 0
		mb.selectedItem = firstSelectableItem(mb.menus[0].Items)
		mb.cascade.reset()
		mb.scrollTop = 0
	}
}

// Draw renders just the menu bar row. Call DrawOverlay afterwards, once all
// other content has been drawn, to render any open dropdown on top: it extends
// below the bar into rows other panels draw into.
func (mb *MenuBar) Draw(s tcell.Screen) {
	p := theme.Active()
	barStyle := theme.StyleMenuBar()
	core.FillRect(s, mb.rect, ' ', barStyle)

	col := mb.rect.X + 1
	for i, m := range mb.menus {
		label := " " + m.Label + " "
		st := barStyle
		if i == mb.openMenu || i == mb.hoverMenu {
			st = tcell.StyleDefault.Background(p.MenuSelected).Foreground(color.White)
		}
		core.DrawText(s, col, mb.rect.Y, st, label)
		col += core.DisplayWidth(label)
	}
}

// DrawOverlay renders the open dropdown, if any. Must be called after every
// other panel has drawn, so the dropdown isn't painted over.
func (mb *MenuBar) DrawOverlay(s tcell.Screen) {
	if mb.openMenu >= 0 && mb.openMenu < len(mb.menus) {
		mb.drawDropdown(s, mb.openMenu)
	}
}

// menuHeaderOffset returns the column where the dropdown for menu index idx
// should begin, measured by display width of the preceding menu headers.
func (mb *MenuBar) menuHeaderOffset(idx int) int {
	col := mb.rect.X + 1
	for i := 0; i < idx; i++ {
		col += core.DisplayWidth(" " + mb.menus[i].Label + " ")
	}
	return col
}

func (mb *MenuBar) drawDropdown(s tcell.Screen, idx int) {
	p := theme.Active()
	menu := mb.menus[idx]

	r := mb.dropdownGeometry(s, idx)
	mb.drawnRect = r
	rows := r.H - 2
	mb.scrollTop = clampMenuScroll(mb.scrollTop, mb.selectedItem, len(menu.Items), rows)

	ddStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
	borderStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Border)
	core.DrawBox(s, r, borderStyle)
	core.FillRect(s, r.Inner(1), ' ', ddStyle)

	for i := 0; i < rows; i++ {
		item := mb.scrollTop + i
		if item >= len(menu.Items) {
			break
		}
		drawMenuRow(s, r.X, r.Y+1+i, r.W, menu.Items[item], item == mb.selectedItem, borderStyle)
	}
	drawMenuScrollMarks(s, r, mb.scrollTop, len(menu.Items), rows, borderStyle)
	mb.cascade.draw(s, menu.Items, r, mb.scrollTop)
}

// dropdownRect returns the open dropdown's box as Draw last clamped it — the
// anchor the cascade's first submenu level hangs off, and what HandleMouse
// hit-tests. Reads the cache rather than recomputing, since the clamp needs a
// screen height only Draw has.
//
// Falls back to the unclamped box when nothing has been drawn, so a MenuBar
// driven without a Draw still hit-tests where it would have painted. The running
// application never takes that path.
func (mb *MenuBar) dropdownRect() core.Rect {
	if mb.openMenu < 0 {
		return core.Rect{}
	}
	if mb.drawnRect.W > 0 {
		return mb.drawnRect
	}
	col, w := mb.dropdownColumn(mb.openMenu)
	return core.Rect{X: col, Y: mb.rect.Y + 1, W: w, H: len(mb.menus[mb.openMenu].Items) + 2}
}

// HandleKey processes keyboard when a dropdown is open.
func (mb *MenuBar) HandleKey(ev *tcell.EventKey) bool {
	if mb.openMenu < 0 {
		return false
	}
	if run, handled := mb.cascade.handleKey(ev, mb.menus[mb.openMenu].Items); handled {
		if run != nil {
			mb.Close()
			run()
		}
		return true
	}
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyF10:
		mb.Close()
	case tcell.KeyLeft:
		mb.openMenu = (mb.openMenu - 1 + len(mb.menus)) % len(mb.menus)
		mb.hoverMenu = mb.openMenu
		mb.selectedItem = firstSelectableItem(mb.menus[mb.openMenu].Items)
		mb.scrollTop = 0
	case tcell.KeyRight:
		// Right opens the selected item's submenu when it has one, and otherwise
		// moves to the next menu header.
		if mb.cascade.openAt(mb.menus[mb.openMenu].Items, 0, mb.selectedItem) {
			return true
		}
		mb.openMenu = (mb.openMenu + 1) % len(mb.menus)
		mb.hoverMenu = mb.openMenu
		mb.selectedItem = firstSelectableItem(mb.menus[mb.openMenu].Items)
		mb.scrollTop = 0
	case tcell.KeyUp:
		mb.selectedItem = stepSelectableItem(mb.menus[mb.openMenu].Items, mb.selectedItem, -1)
	case tcell.KeyDown:
		mb.selectedItem = stepSelectableItem(mb.menus[mb.openMenu].Items, mb.selectedItem, 1)
	case tcell.KeyEnter:
		items := mb.menus[mb.openMenu].Items
		if mb.cascade.openAt(items, 0, mb.selectedItem) {
			return true
		}
		mb.Close()
		if mb.selectedItem >= 0 && mb.selectedItem < len(items) {
			item := items[mb.selectedItem]
			if !item.Divider && item.Action != nil && item.enabled() {
				item.Action()
			}
		}
	}
	return true
}

// HandleMouse processes mouse events for the bar and any open dropdown. While a
// dropdown is open every event is swallowed, so nothing underneath can react or
// take focus; a hover outside never closes it, only a click does.
func (mb *MenuBar) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	wasOpen := mb.openMenu >= 0

	if ev.Buttons() == tcell.ButtonNone {
		mb.mouseDragging = false
	}

	if my == mb.rect.Y {
		col := mb.rect.X + 1
		mb.hoverMenu = -1
		for i, m := range mb.menus {
			label := " " + m.Label + " "
			labelW := core.DisplayWidth(label)
			if mx >= col && mx < col+labelW {
				mb.hoverMenu = i
				if ev.Buttons() == tcell.Button1 && !mb.mouseDragging {
					mb.mouseDragging = true
					if mb.openMenu == i {
						mb.Close()
					} else {
						mb.Close()
						mb.openMenu = i
						mb.selectedItem = firstSelectableItem(m.Items)
					}
				}
				return true
			}
			col += labelW
		}
		// On the bar row but off every label, over the toolbar say: a click
		// still dismisses an open menu, and the event is swallowed either
		// way.
		if wasOpen && ev.Buttons() == tcell.Button1 {
			mb.Close()
		}
		return wasOpen
	}

	if wasOpen {
		root := mb.menus[mb.openMenu].Items
		if mb.handleDropdownWheel(ev, len(root)) {
			return true
		}
		level, row, inside := mb.cascade.hit(mb.dropdownRect(), mx, my)
		// The cascade reports level 0 as a row within the box it was handed,
		// which on a scrolled dropdown is not the item's index.
		if level == 0 && inside && row >= 0 {
			row += mb.scrollTop
		}
		if !inside {
			if ev.Buttons() == tcell.Button1 {
				mb.Close()
			}
			return true
		}
		items := mb.cascade.levelItems(root, level)
		if row < 0 || row >= len(items) {
			return true
		}
		item := items[row]
		if item.Divider || !item.enabled() {
			// A click anywhere inside the dropdown dismisses it, even on a row
			// that can't act — a disabled item would otherwise leave it stuck
			// open.
			if ev.Buttons() == tcell.Button1 && !mb.mouseDragging {
				mb.mouseDragging = true
				mb.Close()
			}
			return true
		}
		// Track hover so Up/Down and the mouse stay in sync, and let hovering a
		// cascade row open its submenu; moving to a sibling closes whatever that
		// level had open.
		if level == 0 {
			mb.selectedItem = row
		} else {
			mb.cascade.setHover(level, row)
		}
		if !mb.cascade.openAt(root, level, row) {
			mb.cascade.popTo(level)
		}
		if ev.Buttons() == tcell.Button1 && !mb.mouseDragging {
			mb.mouseDragging = true
			if len(item.Sub) == 0 {
				mb.Close()
				if item.Action != nil {
					item.Action()
				}
			}
		}
		return true
	}
	return false
}

// handleDropdownWheel scrolls a scrolled-back dropdown on a wheel event and
// reports whether it consumed one. Without it the hidden items are reachable by
// keyboard only.
func (mb *MenuBar) handleDropdownWheel(ev *tcell.EventMouse, n int) bool {
	rows := mb.drawnRect.H - 2
	if rows <= 0 || n <= rows {
		return false
	}
	switch ev.Buttons() {
	case tcell.WheelUp:
		mb.scrollTop = core.Clamp(mb.scrollTop-1, 0, n-rows)
	case tcell.WheelDown:
		mb.scrollTop = core.Clamp(mb.scrollTop+1, 0, n-rows)
	default:
		return false
	}
	// The highlight follows the window rather than staying put: drawDropdown
	// scrolls back to it, so leaving it behind makes the next draw undo the
	// scroll.
	mb.selectedItem = core.Clamp(mb.selectedItem, mb.scrollTop, mb.scrollTop+rows-1)
	return true
}

// dropdownGeometry returns the box the open dropdown for menu index idx is drawn
// in, clamped to the screen on all four sides.
//
// A dropdown is anchored under its own header, so unlike a ContextMenu it can't
// be shifted up to fit: the height is capped at the rows left below the bar and
// drawDropdown scrolls the items through it. The lower clamps matter as much as
// the upper ones — a menu wider or taller than the screen gives a negative
// column or height, and a negative height turns the box inside out.
func (mb *MenuBar) dropdownGeometry(s tcell.Screen, idx int) core.Rect {
	sw, sh := s.Size()
	col, w := mb.dropdownColumn(idx)
	col = core.Clamp(col, 0, max(sw-1, 0))
	w = core.Clamp(w, 1, max(sw-col, 1))

	y := mb.rect.Y + 1
	h := core.Clamp(len(mb.menus[idx].Items)+2, 3, max(sh-y, 3))
	return core.Rect{X: col, Y: y, W: w, H: h}
}

// dropdownColumn returns where the dropdown for menu idx starts and how wide it
// is, shifted left to stay inside the bar but not yet clamped to a screen. Split
// out so dropdownRect can answer before anything has been drawn.
func (mb *MenuBar) dropdownColumn(idx int) (col, w int) {
	col = mb.menuHeaderOffset(idx)
	w = menuContentWidth(mb.menus[idx].Items, 28)
	if col+w > mb.rect.X+mb.rect.W {
		col = mb.rect.X + mb.rect.W - w
	}
	return col, w
}

// clampMenuScroll returns the first-item index keeping sel visible in a window
// of rows items out of n, scrolling as little as possible. rows <= 0 yields 0
// rather than a negative index.
func clampMenuScroll(top, sel, n, rows int) int {
	if rows <= 0 || n <= rows {
		return 0
	}
	top = core.Clamp(top, 0, n-rows)
	if sel < 0 {
		return top
	}
	if sel < top {
		return sel
	}
	if sel >= top+rows {
		return sel - rows + 1
	}
	return top
}

// drawMenuScrollMarks paints the ▲/▼ saying a scrolled menu box has items past
// its top or bottom edge. Without them a partial menu is indistinguishable from
// a complete one.
func drawMenuScrollMarks(s tcell.Screen, r core.Rect, top, n, rows int, style tcell.Style) {
	if rows <= 0 || n <= rows {
		return
	}
	if top > 0 {
		core.DrawText(s, r.X+r.W-2, r.Y, style, "▲")
	}
	if top+rows < n {
		core.DrawText(s, r.X+r.W-2, r.Y+r.H-1, style, "▼")
	}
}
