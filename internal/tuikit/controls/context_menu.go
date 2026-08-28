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
	// menu to (see geometry) — HandleMouse hit-tests against these rather
	// than the raw x,y above, having no tcell.Screen to recompute the clamp
	// itself. Draw runs once per event-loop iteration right after the event
	// that called Show() and before the next input event is dispatched (see
	// App.Run), so by the time a click can land, the cache reflects what was
	// last painted. Without it, a menu shown near the screen's right or
	// bottom edge draws shifted while hit-testing stays anchored to the
	// original off-screen request.
	drawnX, drawnY int

	// cascade holds any open submenu levels (see menuCascade). Cleared by
	// Show and Hide so a cascade never survives into the menu's next
	// showing — the same rule ModalDialog.Show follows for its drag latch.
	cascade menuCascade

	// mouseDragging distinguishes a fresh Button1 press from a continued
	// hold, needed once a click can leave the menu open: clicking a cascade
	// item keeps the popup up, and tcell's all-motion tracking resends
	// Buttons()==Button1 on every motion while the button stays down, so
	// without the latch a press that twitches over a leaf item would fire
	// its Action repeatedly.
	mouseDragging bool
}

// Show displays the menu at (x,y) with the given items.
func (cm *ContextMenu) Show(x, y int, items []MenuItem) {
	cm.x, cm.y = x, y
	cm.drawnX, cm.drawnY = x, y
	cm.items = items
	cm.hover = -1
	cm.visible = true
	cm.cascade.reset()
	cm.mouseDragging = false
}

// Hide dismisses the menu and every submenu open under it.
func (cm *ContextMenu) Hide() {
	cm.visible = false
	cm.cascade.reset()
}

// Visible reports whether the menu is shown.
func (cm *ContextMenu) Visible() bool { return cm.visible }

// Items returns the entries currently shown, for a caller that has to act on
// the menu without a screen to click on — the entry in force is bulleted by
// whoever built the list, so match on a substring rather than the whole label.
func (cm *ContextMenu) Items() []MenuItem { return cm.items }

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
	// Lower clamps for a menu bigger than the screen, where the shifts above
	// go negative and push the box off the opposite edge — the top rows, the
	// ones hover starts on, being the ones lost. No shipped context menu is
	// anywhere near that large (the longest is ten items), so this is
	// defensive; MenuBar's dropdown, which is, scrolls instead.
	x = max(x, 0)
	y = max(y, 0)
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
	cm.cascade.draw(s, cm.items, r, 0)
}

// drawnRect returns the box the menu was last painted in, using the same
// width/height maths as geometry so hit-testing matches what was drawn.
func (cm *ContextMenu) drawnRect() core.Rect {
	return core.Rect{X: cm.drawnX, Y: cm.drawnY, W: menuContentWidth(cm.items, 20), H: len(cm.items) + 2}
}

// HandleKey processes keyboard events.
func (cm *ContextMenu) HandleKey(ev *tcell.EventKey) bool {
	if !cm.visible {
		return false
	}
	if run, handled := cm.cascade.handleKey(ev, cm.items); handled {
		if run != nil {
			cm.Hide()
			run()
		}
		return true
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
	case tcell.KeyRight:
		cm.cascade.openAt(cm.items, 0, cm.hover)
	case tcell.KeyEnter:
		if cm.hover >= 0 && cm.hover < len(cm.items) {
			if cm.cascade.openAt(cm.items, 0, cm.hover) {
				return true
			}
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
	if ev.Buttons() == tcell.ButtonNone {
		cm.mouseDragging = false
	}

	level, row, inside := cm.cascade.hit(cm.drawnRect(), mx, my)
	if !inside {
		// A click outside dismisses the menu and stops there — it must not
		// also reach whatever it landed on, or one click would both close
		// the menu and activate the widget underneath (see
		// ModalDialog.ConsumeOutsideClick, which likewise consumes). Motion
		// with no button held still falls through, so hover tracking
		// elsewhere keeps working while the menu is open.
		if ev.Buttons() == tcell.Button1 {
			cm.Hide()
			return true
		}
		return false
	}

	items := cm.cascade.levelItems(cm.items, level)
	if row < 0 || row >= len(items) {
		return true
	}
	item := items[row]
	if item.Divider || !item.enabled() {
		// A click anywhere inside the menu dismisses it, even on a row that
		// can't act — otherwise a disabled item leaves the popup stuck open.
		if ev.Buttons() == tcell.Button1 && !cm.mouseDragging {
			cm.mouseDragging = true
			cm.Hide()
		}
		return true
	}

	// Hovering a row is what opens its submenu, and closes any submenu of a
	// sibling row that was open — the same level can only show one.
	if level == 0 {
		cm.hover = row
	} else {
		cm.cascade.setHover(level, row)
	}
	if !cm.cascade.openAt(cm.items, level, row) {
		cm.cascade.popTo(level)
	}

	if ev.Buttons() == tcell.Button1 && !cm.mouseDragging {
		cm.mouseDragging = true
		if len(item.Sub) == 0 {
			cm.Hide()
			if item.Action != nil {
				item.Action()
			}
		}
	}
	return true
}
