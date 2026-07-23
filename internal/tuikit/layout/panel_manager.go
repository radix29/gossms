package layout

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// PanelManager
// ---------------------------------------------------------------------------

// tabCloseGlyph is the per-tab close button drawn after each tab's label.
const tabCloseGlyph = "[x]"

// PanelClosable reports whether p's tab should get a close button — true
// unless p implements Closable and returns false (see that interface's doc
// comment).
func PanelClosable(p Panel) bool {
	c, ok := p.(Closable)
	return !ok || c.Closable()
}

// tabLabelText returns a panel's tab-bar title: truncated to 20 columns,
// with a trailing "*" when the panel implements Dirty and reports unsaved
// changes.
func tabLabelText(p Panel) string {
	title := core.Truncate(p.Title(), 20)
	if dp, ok := p.(Dirty); ok && dp.Dirty() {
		title += "*"
	}
	return title
}

// PanelManager manages a stack of overlapping Panels displayed one at a time.
// A tab bar at the top lets the user switch between panels; a drop-down combo
// arrow shows when there are more tabs than fit.
type PanelManager struct {
	rect       core.Rect
	panels     []Panel
	active     int
	comboOpen  bool
	comboHover int

	// mouseDragging distinguishes a fresh Button1 press on the combo arrow
	// or tab row from a continued hold over the same spot — mirrors
	// TreeView's/MenuBar's/Toolbar's field of the same name and purpose.
	// Without it, tcell's all-motion mouse tracking resends
	// Buttons()==Button1 on every cursor motion while the button stays
	// down, so a single click can toggle the combo open/closed twice (net
	// no-op flicker) or fire OnCloseTab twice for one physical click.
	mouseDragging bool

	// OnCloseTab, if set, is called instead of RemovePanel when the user
	// clicks a tab's [x] button — the application decides whether (and how)
	// to actually close it, e.g. prompting to save a Dirty panel first.
	OnCloseTab func(i int)
}

// NewPanelManager creates an empty PanelManager.
func NewPanelManager() *PanelManager {
	return new(PanelManager{active: -1, comboHover: -1})
}

// setActiveIndex changes the active panel index, notifying the old and new
// active panels via the Activatable interface if they implement it, then
// relaying out the newly active panel.
func (pm *PanelManager) setActiveIndex(i int) {
	if i == pm.active {
		pm.relayout()
		return
	}
	if old := pm.ActivePanel(); old != nil {
		if a, ok := old.(Activatable); ok {
			a.SetActive(false)
		}
	}
	pm.active = i
	if cur := pm.ActivePanel(); cur != nil {
		if a, ok := cur.(Activatable); ok {
			a.SetActive(true)
		}
	}
	pm.relayout()
}

// AddPanel appends a panel and returns its index.
func (pm *PanelManager) AddPanel(p Panel) int {
	pm.panels = append(pm.panels, p)
	if pm.active < 0 {
		pm.setActiveIndex(0)
	}
	return len(pm.panels) - 1
}

// RemovePanel removes the panel at index i.
func (pm *PanelManager) RemovePanel(i int) {
	if i < 0 || i >= len(pm.panels) {
		return
	}
	wasActive := i == pm.active
	pm.panels = append(pm.panels[:i], pm.panels[i+1:]...)
	// Removing a panel to the left of the active one shifts every later
	// index down by one — without this, pm.active would keep its old
	// numeric value and silently end up pointing at a different panel than
	// the one that was actually active a moment ago.
	if i < pm.active {
		pm.active--
	}
	pm.active = core.Clamp(pm.active, 0, len(pm.panels)-1)
	// If the removed panel was the active one, a different panel (or none)
	// now occupies the active slot — fire its Activatable hook.
	if wasActive {
		if cur := pm.ActivePanel(); cur != nil {
			if a, ok := cur.(Activatable); ok {
				a.SetActive(true)
			}
		}
	}
	pm.relayout()
}

// SetActive switches the visible panel to index i.
func (pm *PanelManager) SetActive(i int) {
	if i >= 0 && i < len(pm.panels) {
		pm.setActiveIndex(i)
	}
}

// ActiveIndex returns the index of the currently visible panel.
func (pm *PanelManager) ActiveIndex() int { return pm.active }

// ActivePanel returns the visible Panel, or nil.
func (pm *PanelManager) ActivePanel() Panel {
	if pm.active >= 0 && pm.active < len(pm.panels) {
		return pm.panels[pm.active]
	}
	return nil
}

// Count returns the number of managed panels.
func (pm *PanelManager) Count() int { return len(pm.panels) }

// PanelAt returns the panel at index i, or nil if out of range. Used by
// callers that need to inspect every panel (e.g. building a "Query List"
// picker) rather than only the currently active one.
func (pm *PanelManager) PanelAt(i int) Panel {
	if i < 0 || i >= len(pm.panels) {
		return nil
	}
	return pm.panels[i]
}

// FindIndex returns the index of the first panel for which predicate
// returns true, or -1 if none match. Typical use is a type assertion,
// e.g. finding the (singular) DetailBrowser panel to reactivate it from a
// View-menu command.
func (pm *PanelManager) FindIndex(predicate func(Panel) bool) int {
	for i, p := range pm.panels {
		if predicate(p) {
			return i
		}
	}
	return -1
}

// Next cycles to the next panel.
func (pm *PanelManager) Next() {
	if len(pm.panels) > 0 {
		pm.setActiveIndex((pm.active + 1) % len(pm.panels))
	}
}

// Prev cycles to the previous panel.
func (pm *PanelManager) Prev() {
	if len(pm.panels) > 0 {
		pm.setActiveIndex((pm.active - 1 + len(pm.panels)) % len(pm.panels))
	}
}

// SetBounds updates the layout geometry.
func (pm *PanelManager) SetBounds(x, y, w, h int) {
	pm.rect = core.Rect{X: x, Y: y, W: w, H: h}
	pm.relayout()
}

func (pm *PanelManager) contentY() int { return pm.rect.Y + 1 }
func (pm *PanelManager) contentH() int { return pm.rect.H - 1 }

func (pm *PanelManager) relayout() {
	if p := pm.ActivePanel(); p != nil {
		p.SetBounds(pm.rect.X, pm.contentY(), pm.rect.W, pm.contentH())
	}
}

// tabMaxX returns the first column the tab row must not draw into —
// truncated short of the combo arrow at the right edge.
func (pm *PanelManager) tabMaxX() int { return pm.rect.X + pm.rect.W - 5 }

// tabSegments computes each panel's tab-bar layout: segment 0 is the label,
// segment 1 the close-button glyph (zero-width when the panel isn't
// closable). Draw and HandleMouse both build their column math from this
// same call so hits line up with what's actually on screen.
func (pm *PanelManager) tabSegments() [][]controls.TabSegment {
	widths := make([][]int, len(pm.panels))
	for i, panel := range pm.panels {
		closeW := 0
		if PanelClosable(panel) {
			closeW = core.DisplayWidth(tabCloseGlyph)
		}
		widths[i] = []int{controls.TabLabelWidth(tabLabelText(panel)), closeW}
	}
	return controls.TabStripSegments(pm.rect.X+1, widths, pm.tabMaxX())
}

// Draw renders the tab bar and the active panel.
func (pm *PanelManager) Draw(s tcell.Screen) {
	p := theme.Active()
	barStyle := theme.StyleMenuBar()
	core.FillRect(s, core.Rect{X: pm.rect.X, Y: pm.rect.Y, W: pm.rect.W, H: 1}, ' ', barStyle)

	if len(pm.panels) == 0 {
		core.DrawText(s, pm.rect.X+1, pm.rect.Y, barStyle, "(no panels open — Ctrl+N for a new query)")
	} else {
		for i, seg := range pm.tabSegments() {
			panel := pm.panels[i]
			tabStyle := barStyle
			if i == pm.active {
				tabStyle = tcell.StyleDefault.Background(p.BorderActive).Foreground(tcell.ColorWhite).Bold(true)
			}
			label := " " + tabLabelText(panel) + " "
			core.DrawText(s, seg[0].X, pm.rect.Y, tabStyle, label)
			if seg[1].W > 0 {
				closeStyle := tabStyle
				if i != pm.active {
					closeStyle = tcell.StyleDefault.Background(p.MenuBar).Foreground(p.TextDim)
				}
				core.DrawText(s, seg[1].X, pm.rect.Y, closeStyle, tabCloseGlyph)
			}
		}
		// Combo arrow
		arrowStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.TextDim)
		core.DrawText(s, pm.rect.X+pm.rect.W-4, pm.rect.Y, arrowStyle, " [v]")
	}

	if panel := pm.ActivePanel(); panel != nil {
		panel.Draw(s)
	}

	// Drop-down list — drawn after the active panel so it isn't immediately
	// painted over by the panel's own content, which occupies the same rows.
	if pm.comboOpen && len(pm.panels) > 0 {
		listX := core.Max(pm.rect.X, pm.rect.X+pm.rect.W-30)
		listW := 28
		listStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
		for i, panel := range pm.panels {
			y := pm.contentY() + i
			st := listStyle
			if i == pm.active {
				st = theme.StyleSelected()
			}
			core.FillRect(s, core.Rect{X: listX, Y: y, W: listW, H: 1}, ' ', st)
			core.DrawTextClipped(s, listX+1, y, listW-2, st, core.Truncate(panel.Title(), listW-3))
		}
	}
}

// HandleKey routes keyboard events to the combo (if open) or active panel.
func (pm *PanelManager) HandleKey(ev *tcell.EventKey) bool {
	if pm.comboOpen {
		switch ev.Key() {
		case tcell.KeyEscape:
			pm.comboOpen = false
		case tcell.KeyUp:
			if pm.active > 0 {
				pm.setActiveIndex(pm.active - 1)
			}
		case tcell.KeyDown:
			if pm.active < len(pm.panels)-1 {
				pm.setActiveIndex(pm.active + 1)
			}
		case tcell.KeyEnter:
			pm.comboOpen = false
		}
		return true
	}
	if p := pm.ActivePanel(); p != nil {
		return p.HandleKey(ev)
	}
	return false
}

// HandleMouse routes mouse events to the tab bar or active panel.
func (pm *PanelManager) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()

	// A button-release can arrive after the cursor has moved outside the
	// panel manager's bounds (e.g. while dragging a splitter inside the
	// active panel). Always forward release events to the active panel so
	// drags terminate cleanly instead of getting stuck.
	if ev.Buttons() == tcell.ButtonNone {
		pm.mouseDragging = false
		if p := pm.ActivePanel(); p != nil {
			return p.HandleMouse(ev)
		}
	}

	if mx < pm.rect.X || mx >= pm.rect.X+pm.rect.W {
		return false
	}

	// Combo toggle arrow
	if my == pm.rect.Y && mx >= pm.rect.X+pm.rect.W-4 {
		if ev.Buttons() == tcell.Button1 {
			if pm.mouseDragging {
				// Still the same physical press — do not re-toggle on
				// every resent motion event.
				return true
			}
			pm.mouseDragging = true
			pm.comboOpen = !pm.comboOpen
			return true
		}
	}

	// Tab row click. Segments come from the same tabSegments call Draw uses,
	// so hits line up with what's actually on screen.
	if my == pm.rect.Y && ev.Buttons() == tcell.Button1 {
		if pm.mouseDragging {
			// Still the same physical press — do not re-fire on every
			// resent motion event (in particular OnCloseTab, which may
			// prompt to save a Dirty panel).
			return true
		}
		pm.mouseDragging = true
		for i, seg := range pm.tabSegments() {
			closeSeg := seg[1]
			if closeSeg.W > 0 && mx >= closeSeg.X && mx < closeSeg.X+closeSeg.W {
				if pm.OnCloseTab != nil {
					pm.OnCloseTab(i)
				}
				pm.comboOpen = false
				return true
			}
			labelSeg := seg[0]
			if mx >= labelSeg.X && mx < labelSeg.X+labelSeg.W {
				pm.setActiveIndex(i)
				pm.comboOpen = false
				return true
			}
		}
	}

	// Combo list click
	if pm.comboOpen {
		listX := core.Max(pm.rect.X, pm.rect.X+pm.rect.W-30)
		listW := 28
		row := my - pm.contentY()
		if ev.Buttons() == tcell.Button1 && mx >= listX && mx < listX+listW &&
			row >= 0 && row < len(pm.panels) {
			pm.setActiveIndex(row)
			pm.comboOpen = false
			return true
		}
		if ev.Buttons() == tcell.Button1 {
			pm.comboOpen = false
		}
	}

	if p := pm.ActivePanel(); p != nil && my >= pm.contentY() {
		return p.HandleMouse(ev)
	}
	return false
}
