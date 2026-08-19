package controls

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// menuCascade — the open submenu chain hanging off a root menu
// ---------------------------------------------------------------------------

// menuCascade tracks which submenus of a root menu are currently open, for
// both MenuBar dropdowns and ContextMenu popups. "Level 0" is always the
// host's own menu, which the host draws and hit-tests itself; the cascade
// owns levels 1..n.
//
// path[i] is the index, within level i's item list, of the item whose Sub
// is shown as level i+1. hover[i] is the highlighted row within that
// submenu, and rects[i] is where it was last drawn — hit-testing reads the
// rect back rather than recomputing it, for the same reason ContextMenu
// caches drawnX/drawnY: only Draw sees a tcell.Screen, so only Draw knows
// where the edge clamp actually put the box.
type menuCascade struct {
	path  []int
	hover []int
	rects []core.Rect
}

// depth returns how many submenu levels are open (0 = none).
func (c *menuCascade) depth() int { return len(c.path) }

// reset closes every open submenu.
func (c *menuCascade) reset() { c.popTo(0) }

// popTo closes every level deeper than n.
func (c *menuCascade) popTo(n int) {
	if n < 0 {
		n = 0
	}
	if n >= len(c.path) {
		return
	}
	c.path, c.hover, c.rects = c.path[:n], c.hover[:n], c.rects[:n]
}

// levelItems returns the item list shown at the given level, walking down
// path from the root menu. Level 0 is root itself. An out-of-range level or
// a path that no longer matches its items yields nil.
func (c *menuCascade) levelItems(root []MenuItem, level int) []MenuItem {
	items := root
	for i := 0; i < level; i++ {
		if i >= len(c.path) || c.path[i] < 0 || c.path[i] >= len(items) {
			return nil
		}
		items = items[c.path[i]].Sub
	}
	return items
}

// levelRect returns where the given level was last drawn; level 0 is the
// host's own rect, which it passes in.
func (c *menuCascade) levelRect(rootRect core.Rect, level int) core.Rect {
	if level == 0 {
		return rootRect
	}
	if level-1 >= len(c.rects) {
		return core.Rect{}
	}
	return c.rects[level-1]
}

// hoverAt returns the highlighted row within an open submenu level.
func (c *menuCascade) hoverAt(level int) int {
	if level-1 < 0 || level-1 >= len(c.hover) {
		return -1
	}
	return c.hover[level-1]
}

// setHover highlights a row within an open submenu level.
func (c *menuCascade) setHover(level, row int) {
	if level-1 >= 0 && level-1 < len(c.hover) {
		c.hover[level-1] = row
	}
}

// openAt opens the submenu of item row at the given level, closing anything
// deeper first, and reports whether that item has one. Opening the submenu
// that is already open at that row is a no-op, so a mouse hover resent on
// every motion event doesn't reset its highlighted row.
func (c *menuCascade) openAt(root []MenuItem, level, row int) bool {
	items := c.levelItems(root, level)
	if row < 0 || row >= len(items) || len(items[row].Sub) == 0 {
		return false
	}
	if len(c.path) > level && c.path[level] == row {
		c.popTo(level + 1)
		return true
	}
	c.popTo(level)
	c.path = append(c.path, row)
	c.hover = append(c.hover, firstSelectableItem(items[row].Sub))
	c.rects = append(c.rects, core.Rect{})
	return true
}

// draw paints every open submenu level. rootRect is where the host drew its
// own menu, which anchors the first cascade level.
func (c *menuCascade) draw(s tcell.Screen, root []MenuItem, rootRect core.Rect) {
	parent, items := rootRect, root
	for i, row := range c.path {
		if row < 0 || row >= len(items) {
			return
		}
		sub := items[row].Sub
		r := submenuRect(s, parent, parent.Y+1+row, sub)
		drawMenuBox(s, r, sub, c.hover[i])
		c.rects[i] = r
		parent, items = r, sub
	}
}

// hit reports which open level, and which row within it, the point falls
// in. Deepest level first, so a submenu overlapping its parent isn't robbed
// of the click by the box underneath. Level 0 is the host's own menu; row
// is negative or past the item count on the box's border rows.
func (c *menuCascade) hit(rootRect core.Rect, mx, my int) (level, row int, ok bool) {
	for i := len(c.rects) - 1; i >= 0; i-- {
		if c.rects[i].Contains(mx, my) {
			return i + 1, my - c.rects[i].Y - 1, true
		}
	}
	if rootRect.Contains(mx, my) {
		return 0, my - rootRect.Y - 1, true
	}
	return 0, 0, false
}

// handleKey processes a key for the deepest open submenu, and reports
// whether it consumed it — false whenever no submenu is open, leaving the
// host's own level to handle its keys. A non-nil run is the action of an
// activated leaf item: the host calls it after closing the whole menu, so
// the action never fires with a stale menu still on screen.
func (c *menuCascade) handleKey(ev *tcell.EventKey, root []MenuItem) (run func(), handled bool) {
	d := c.depth()
	if d == 0 {
		return nil, false
	}
	items := c.levelItems(root, d)
	hov := c.hoverAt(d)
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyLeft:
		c.popTo(d - 1)
	case tcell.KeyUp:
		c.setHover(d, stepSelectableItem(items, hov, -1))
	case tcell.KeyDown:
		c.setHover(d, stepSelectableItem(items, hov, 1))
	case tcell.KeyRight:
		c.openAt(root, d, hov)
	case tcell.KeyEnter:
		if hov < 0 || hov >= len(items) {
			return nil, true
		}
		if c.openAt(root, d, hov) {
			return nil, true
		}
		item := items[hov]
		if !item.Divider && item.enabled() {
			return item.Action, true
		}
	default:
		return nil, false
	}
	return nil, true
}

// submenuRect returns where a submenu of the given items should be drawn:
// to the right of its parent box, first row aligned with the parent item
// that opened it, flipped to the parent's left when it wouldn't fit and
// pushed up when it would run off the bottom.
func submenuRect(s tcell.Screen, parent core.Rect, rowY int, items []MenuItem) core.Rect {
	sw, sh := s.Size()
	w := menuContentWidth(items, 20)
	h := len(items) + 2
	x := parent.X + parent.W
	if x+w > sw {
		x = parent.X - w
	}
	if x < 0 {
		x = 0
	}
	y := rowY - 1
	if y+h > sh {
		y = sh - h
	}
	if y < 0 {
		y = 0
	}
	return core.Rect{X: x, Y: y, W: w, H: h}
}

// drawMenuBox paints one menu box: background, border, and every item row
// with hover highlighted.
func drawMenuBox(s tcell.Screen, r core.Rect, items []MenuItem, hover int) {
	p := theme.Active()
	itemStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
	borderStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Border)
	core.FillRect(s, r, ' ', itemStyle)
	core.DrawBox(s, r, borderStyle)
	for i, item := range items {
		drawMenuRow(s, r.X, r.Y+1+i, r.W, item, i == hover, borderStyle)
	}
}
