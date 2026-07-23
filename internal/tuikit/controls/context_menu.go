package controls

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

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
