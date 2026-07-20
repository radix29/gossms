package controls

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// MenuItem / Menu — shared by MenuBar and ContextMenu
// ---------------------------------------------------------------------------

// MenuItem is a single entry in a menu.
type MenuItem struct {
	Label    string
	Shortcut string
	Divider  bool        // renders as a ──── separator
	Action   func()      // called when the item is activated
	Enabled  func() bool // nil means always enabled
}

// enabled reports whether it can be selected or activated right now.
func (it MenuItem) enabled() bool {
	return it.Enabled == nil || it.Enabled()
}

// Menu is a top-level menu header with its items.
type Menu struct {
	Label string
	Items []MenuItem
}

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

	// mouseDragging distinguishes a fresh Button1 press (toggle the header)
	// from a continued hold over the same header — mirrors DataGrid's and
	// Editor's field of the same name and purpose. Without it, the mouse
	// tracking mode gossms enables (core.NewScreen's EnableMouse()) resends
	// Buttons()==Button1 on every motion while the button stays down, so a
	// click that so much as twitches before release re-toggles the header
	// it just opened, right back closed — a visible open/close flicker.
	mouseDragging bool
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

// Close closes any open dropdown.
func (mb *MenuBar) Close() { mb.openMenu = -1 }

// Open opens the first menu without requiring a mouse click — used for
// keyboard-only activation (e.g. the F10 convention from Turbo
// Vision/Midnight-Commander-style TUIs). Does nothing if there are no
// menus, or if a menu is already open.
func (mb *MenuBar) Open() {
	if mb.openMenu < 0 && len(mb.menus) > 0 {
		mb.openMenu = 0
		mb.hoverMenu = 0
		mb.selectedItem = firstSelectableItem(mb.menus[0].Items)
	}
}

// menuItemSkippable reports whether an item must be skipped by keyboard/
// mouse selection — a divider, or one whose Enabled predicate says no.
func menuItemSkippable(it MenuItem) bool {
	return it.Divider || !it.enabled()
}

// firstSelectableItem returns the index of the first selectable item, or
// -1 if the menu has none.
func firstSelectableItem(items []MenuItem) int {
	for i, it := range items {
		if !menuItemSkippable(it) {
			return i
		}
	}
	return -1
}

// stepSelectableItem returns the next selectable item index starting from
// `from`, moving by `dir` (+1 or -1) and wrapping around. Returns -1 if the
// menu has no selectable items at all.
func stepSelectableItem(items []MenuItem, from, dir int) int {
	n := len(items)
	if n == 0 {
		return -1
	}
	i := from
	for range n {
		i = (i + dir + n) % n
		if !menuItemSkippable(items[i]) {
			return i
		}
	}
	return -1
}

// Draw renders just the menu bar row. Call DrawOverlay afterward, once all
// other content has been drawn, to render any open dropdown on top — the
// dropdown extends below the bar into rows other panels also draw into, so
// it must be painted last or it gets overwritten before Show().
func (mb *MenuBar) Draw(s tcell.Screen) {
	p := theme.Active()
	barStyle := theme.StyleMenuBar()
	core.FillRect(s, mb.rect, ' ', barStyle)

	col := mb.rect.X + 1
	for i, m := range mb.menus {
		label := " " + m.Label + " "
		st := barStyle
		if i == mb.openMenu || i == mb.hoverMenu {
			st = tcell.StyleDefault.Background(p.MenuSelected).Foreground(tcell.ColorWhite)
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

	col, w, h := mb.dropdownGeometry(idx)

	ddStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
	borderStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Border)
	r := core.Rect{X: col, Y: mb.rect.Y + 1, W: w, H: h}
	core.DrawBox(s, r, borderStyle)
	core.FillRect(s, r.Inner(1), ' ', ddStyle)

	for i, item := range menu.Items {
		y := mb.rect.Y + 2 + i
		if item.Divider {
			for x := col + 1; x < col+w-1; x++ {
				s.SetContent(x, y, '─', nil, borderStyle)
			}
			s.SetContent(col, y, '├', nil, borderStyle)
			s.SetContent(col+w-1, y, '┤', nil, borderStyle)
			continue
		}
		itemStyle := ddStyle
		shortcutStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.TextDim)
		switch {
		case !item.enabled():
			itemStyle = theme.StyleDisabled()
			shortcutStyle = itemStyle
		case i == mb.selectedItem:
			itemStyle = theme.StyleSelected()
			shortcutStyle = tcell.StyleDefault.Background(p.TreeSelected).Foreground(p.TextHighlight)
			core.FillRect(s, core.Rect{X: col + 1, Y: y, W: w - 2, H: 1}, ' ', itemStyle)
		}
		core.DrawTextClipped(s, col+2, y, w-4, itemStyle, item.Label)
		if item.Shortcut != "" {
			sx := col + w - 1 - core.DisplayWidth(item.Shortcut) - 1
			core.DrawText(s, sx, y, shortcutStyle, item.Shortcut)
		}
	}
}

// HandleKey processes keyboard when a dropdown is open.
func (mb *MenuBar) HandleKey(ev *tcell.EventKey) bool {
	if mb.openMenu < 0 {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyF10:
		mb.openMenu = -1
	case tcell.KeyLeft:
		mb.openMenu = (mb.openMenu - 1 + len(mb.menus)) % len(mb.menus)
		mb.hoverMenu = mb.openMenu
		mb.selectedItem = firstSelectableItem(mb.menus[mb.openMenu].Items)
	case tcell.KeyRight:
		mb.openMenu = (mb.openMenu + 1) % len(mb.menus)
		mb.hoverMenu = mb.openMenu
		mb.selectedItem = firstSelectableItem(mb.menus[mb.openMenu].Items)
	case tcell.KeyUp:
		mb.selectedItem = stepSelectableItem(mb.menus[mb.openMenu].Items, mb.selectedItem, -1)
	case tcell.KeyDown:
		mb.selectedItem = stepSelectableItem(mb.menus[mb.openMenu].Items, mb.selectedItem, 1)
	case tcell.KeyEnter:
		items := mb.menus[mb.openMenu].Items
		mb.openMenu = -1
		if mb.selectedItem >= 0 && mb.selectedItem < len(items) {
			item := items[mb.selectedItem]
			if !item.Divider && item.Action != nil && item.enabled() {
				item.Action()
			}
		}
	}
	return true
}

// HandleMouse processes mouse events for the bar and any open dropdown.
// While a dropdown is open, every mouse event is swallowed (return true) so
// nothing underneath can react or take focus; a hover outside the dropdown
// never closes it, only a click does.
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
						mb.openMenu = -1
					} else {
						mb.openMenu = i
						mb.selectedItem = firstSelectableItem(m.Items)
					}
				}
				return true
			}
			col += labelW
		}
		// On the bar row but off every label (e.g. over the toolbar): a
		// click still dismisses an open menu, but the event itself is
		// swallowed either way.
		if wasOpen && ev.Buttons() == tcell.Button1 {
			mb.openMenu = -1
		}
		return wasOpen
	}

	if wasOpen {
		if mb.dropdownContains(mx, my) {
			// Track hover so keyboard (Up/Down) and mouse stay in sync.
			if itemIdx := my - (mb.rect.Y + 2); itemIdx >= 0 && itemIdx < len(mb.menus[mb.openMenu].Items) {
				if it := mb.menus[mb.openMenu].Items[itemIdx]; !it.Divider && it.enabled() {
					mb.selectedItem = itemIdx
				}
			}
			if ev.Buttons() == tcell.Button1 {
				mb.handleDropdownClick(my)
			}
		} else if ev.Buttons() == tcell.Button1 {
			mb.openMenu = -1
		}
		return true
	}
	return false
}

// dropdownGeometry returns the column, width, and height of the open
// dropdown for menu index idx, using the same width calculation as
// drawDropdown so hit-testing always matches what was actually drawn.
func (mb *MenuBar) dropdownGeometry(idx int) (col, w, h int) {
	menu := mb.menus[idx]
	col = mb.menuHeaderOffset(idx)
	w = 28
	for _, item := range menu.Items {
		// +6, not +4: 2 columns of border/inset padding plus a guaranteed
		// 2-column gap between label and shortcut for whichever item ends
		// up defining w — without the extra margin, that widest item's own
		// label and shortcut land flush against each other with no gap.
		if n := core.DisplayWidth(item.Label) + core.DisplayWidth(item.Shortcut) + 6; n > w {
			w = n
		}
	}
	if col+w > mb.rect.X+mb.rect.W {
		col = mb.rect.X + mb.rect.W - w
	}
	h = len(menu.Items) + 2
	return col, w, h
}

func (mb *MenuBar) dropdownContains(mx, my int) bool {
	if mb.openMenu < 0 {
		return false
	}
	col, w, h := mb.dropdownGeometry(mb.openMenu)
	return mx >= col && mx < col+w && my >= mb.rect.Y+1 && my < mb.rect.Y+1+h
}

func (mb *MenuBar) handleDropdownClick(my int) {
	if mb.openMenu < 0 {
		return
	}
	itemIdx := my - (mb.rect.Y + 2)
	menu := mb.menus[mb.openMenu]
	mb.openMenu = -1
	if itemIdx >= 0 && itemIdx < len(menu.Items) {
		item := menu.Items[itemIdx]
		if !item.Divider && item.Action != nil && item.enabled() {
			item.Action()
		}
	}
}

// ---------------------------------------------------------------------------
// ContextMenu
// ---------------------------------------------------------------------------

// ContextMenu is a floating popup menu shown on right-click.
type ContextMenu struct {
	x, y    int
	items   []MenuItem
	hover   int
	visible bool
}

// Show displays the menu at (x,y) with the given items.
func (cm *ContextMenu) Show(x, y int, items []MenuItem) {
	cm.x, cm.y = x, y
	cm.items = items
	cm.hover = -1
	cm.visible = true
}

// Hide dismisses the menu.
func (cm *ContextMenu) Hide() { cm.visible = false }

// Visible reports whether the menu is shown.
func (cm *ContextMenu) Visible() bool { return cm.visible }

func (cm *ContextMenu) width() int {
	w := 20
	for _, item := range cm.items {
		// +6: see MenuBar.dropdownGeometry's identical formula and comment.
		if n := core.DisplayWidth(item.Label) + core.DisplayWidth(item.Shortcut) + 6; n > w {
			w = n
		}
	}
	return w
}

// Draw renders the context menu.
func (cm *ContextMenu) Draw(s tcell.Screen) {
	if !cm.visible {
		return
	}
	sw, sh := s.Size()
	w := cm.width()
	h := len(cm.items) + 2
	x, y := cm.x, cm.y
	if x+w > sw {
		x = sw - w
	}
	if y+h > sh {
		y = sh - h
	}
	p := theme.Active()
	itemStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
	hoverStyle := theme.StyleSelected()
	shortcutStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.TextDim)
	borderStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Border)
	r := core.Rect{X: x, Y: y, W: w, H: h}
	core.FillRect(s, r, ' ', itemStyle)
	core.DrawBox(s, r, borderStyle)

	for i, item := range cm.items {
		iy := y + 1 + i
		if item.Divider {
			for cx := x + 1; cx < x+w-1; cx++ {
				s.SetContent(cx, iy, '─', nil, borderStyle)
			}
			s.SetContent(x, iy, '├', nil, borderStyle)
			s.SetContent(x+w-1, iy, '┤', nil, borderStyle)
			continue
		}
		st := itemStyle
		scStyle := shortcutStyle
		if i == cm.hover {
			st = hoverStyle
			scStyle = hoverStyle
		}
		core.FillRect(s, core.Rect{X: x + 1, Y: iy, W: w - 2, H: 1}, ' ', st)
		core.DrawTextClipped(s, x+2, iy, w-4, st, item.Label)
		if item.Shortcut != "" {
			sx := x + w - 1 - core.DisplayWidth(item.Shortcut) - 1
			core.DrawText(s, sx, iy, scStyle, item.Shortcut)
		}
	}
}

// HandleKey processes keyboard events.
func (cm *ContextMenu) HandleKey(ev *tcell.EventKey) bool {
	if !cm.visible {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		cm.Hide()
	case tcell.KeyUp:
		if cm.hover < 0 {
			cm.hover = firstSelectableItem(cm.items)
		} else {
			cm.hover = stepSelectableItem(cm.items, cm.hover, -1)
		}
	case tcell.KeyDown:
		if cm.hover < 0 {
			cm.hover = firstSelectableItem(cm.items)
		} else {
			cm.hover = stepSelectableItem(cm.items, cm.hover, 1)
		}
	case tcell.KeyEnter:
		if cm.hover >= 0 && cm.hover < len(cm.items) {
			item := cm.items[cm.hover]
			cm.Hide()
			if !item.Divider && item.Action != nil {
				item.Action()
			}
		}
	}
	return true
}

// HandleMouse processes mouse events.
func (cm *ContextMenu) HandleMouse(ev *tcell.EventMouse) bool {
	if !cm.visible {
		return false
	}
	mx, my := ev.Position()
	w := cm.width()
	h := len(cm.items) + 2
	x, y := cm.x, cm.y

	if mx < x || mx >= x+w || my < y || my >= y+h {
		if ev.Buttons() == tcell.Button1 {
			cm.Hide()
		}
		return false
	}

	itemIdx := my - y - 1
	if itemIdx >= 0 && itemIdx < len(cm.items) {
		cm.hover = itemIdx
	}
	if ev.Buttons() == tcell.Button1 && itemIdx >= 0 && itemIdx < len(cm.items) {
		item := cm.items[itemIdx]
		cm.Hide()
		if !item.Divider && item.Action != nil {
			item.Action()
		}
		return true
	}
	return true
}
