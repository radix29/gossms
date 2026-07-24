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
	x, y    int // requested position, from Show() — unclamped
	items   []MenuItem
	hover   int
	visible bool

	// drawnX, drawnY cache the on-screen column/row Draw last clamped the
	// menu to (see geometry) — HandleMouse hit-tests against these instead
	// of the raw x,y above, since HandleMouse has no tcell.Screen to
	// recompute the clamp itself. This stays correct because Draw always
	// runs once per event-loop iteration right after the event that called
	// Show(), and before the next input event is dispatched (see App.Run),
	// so by the time a click can land on the menu, the cache reflects
	// exactly what was last painted. Without this, Show()ing the menu near
	// the screen's right or bottom edge — routine for a tall Object
	// Explorer tree, or a DataGrid results pane — draws it shifted
	// on-screen while hit-testing stays anchored to the original,
	// off-screen request, so clicks land on the wrong item or miss the
	// menu entirely.
	drawnX, drawnY int
}

// Show displays the menu at (x,y) with the given items.
func (cm *ContextMenu) Show(x, y int, items []MenuItem) {
	cm.x, cm.y = x, y
	cm.drawnX, cm.drawnY = x, y
	cm.items = items
	cm.hover = -1
	cm.visible = true
}

// Hide dismisses the menu.
func (cm *ContextMenu) Hide() { cm.visible = false }

// Visible reports whether the menu is shown.
func (cm *ContextMenu) Visible() bool { return cm.visible }

// geometry returns the clamped column, row, width, and height the menu
// should draw at for screen s — shifted left/up from the requested (x,y) if
// it would otherwise run off the right or bottom edge.
func (cm *ContextMenu) geometry(s tcell.Screen) (x, y, w, h int) {
	sw, sh := s.Size()
	w = menuContentWidth(cm.items, 20)
	h = len(cm.items) + 2
	x, y = cm.x, cm.y
	if x+w > sw {
		x = sw - w
	}
	if y+h > sh {
		y = sh - h
	}
	return x, y, w, h
}

// Draw renders the context menu.
func (cm *ContextMenu) Draw(s tcell.Screen) {
	if !cm.visible {
		return
	}
	x, y, w, h := cm.geometry(s)
	cm.drawnX, cm.drawnY = x, y

	p := theme.Active()
	itemStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
	borderStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Border)
	r := core.Rect{X: x, Y: y, W: w, H: h}
	core.FillRect(s, r, ' ', itemStyle)
	core.DrawBox(s, r, borderStyle)

	for i, item := range cm.items {
		iy := y + 1 + i
		drawMenuRow(s, x, iy, w, item, i == cm.hover, borderStyle)
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
			if !item.Divider && item.Action != nil && item.enabled() {
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
	w := menuContentWidth(cm.items, 20)
	h := len(cm.items) + 2
	x, y := cm.drawnX, cm.drawnY

	if mx < x || mx >= x+w || my < y || my >= y+h {
		if ev.Buttons() == tcell.Button1 {
			cm.Hide()
		}
		return false
	}

	itemIdx := my - y - 1
	if itemIdx >= 0 && itemIdx < len(cm.items) {
		if it := cm.items[itemIdx]; !it.Divider && it.enabled() {
			cm.hover = itemIdx
		}
	}
	if ev.Buttons() == tcell.Button1 && itemIdx >= 0 && itemIdx < len(cm.items) {
		item := cm.items[itemIdx]
		cm.Hide()
		if !item.Divider && item.Action != nil && item.enabled() {
			item.Action()
		}
		return true
	}
	return true
}
