package layout

import (
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// PanelManager
// ---------------------------------------------------------------------------

// tabCloseGlyph is the per-tab close button drawn after each tab's label.
const tabCloseGlyph = "[x]"

// PanelClosable reports whether p's tab should get a close button — true unless
// p implements Closable and returns false.
func PanelClosable(p Panel) bool {
	c, ok := p.(Closable)
	return !ok || c.Closable()
}

// tabLabelText returns a panel's tab-bar title: truncated to 20 columns, with a
// trailing "*" when the panel implements Dirty and reports unsaved changes.
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
	rect      core.Rect
	panels    []Panel
	active    int
	comboOpen bool

	// comboScroll is the drop-down's first visible row; the list is capped to the
	// rows that fit below the tab bar.
	comboScroll int

	// mouseDragging separates a fresh Button1 press on the combo arrow or tab row
	// from a resent hold (all-motion tracking), which would otherwise toggle the
	// combo or fire OnCloseTab twice.
	mouseDragging bool

	// comboSbDragging latches a drag of the drop-down's scrollbar. Not
	// mouseDragging: sharing it would let the press that opened the combo satisfy
	// HandleScrollbarDrag's "already dragging" test.
	comboSbDragging bool

	// OnCloseTab, if set, replaces RemovePanel on a tab's [x] click so the
	// application decides whether to close (e.g. prompt to save a Dirty panel).
	OnCloseTab func(i int)
}

// NewPanelManager creates an empty PanelManager.
func NewPanelManager() *PanelManager {
	return new(PanelManager{active: -1})
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
	pm.scrollComboToActive()
	return len(pm.panels) - 1
}

// RemovePanel removes the panel at index i.
func (pm *PanelManager) RemovePanel(i int) {
	if i < 0 || i >= len(pm.panels) {
		return
	}
	wasActive := i == pm.active
	pm.panels = append(pm.panels[:i], pm.panels[i+1:]...)
	// Removing a panel left of the active one shifts later indices down; adjust
	// pm.active to keep pointing at the same panel.
	if i < pm.active {
		pm.active--
	}
	pm.active = core.Clamp(pm.active, 0, len(pm.panels)-1)
	// If the removed panel was active, fire the new occupant's Activatable hook.
	if wasActive {
		if cur := pm.ActivePanel(); cur != nil {
			if a, ok := cur.(Activatable); ok {
				a.SetActive(true)
			}
		}
	}
	pm.scrollComboToActive()
	pm.relayout()
}

// SetActive switches the visible panel to index i.
func (pm *PanelManager) SetActive(i int) {
	if i >= 0 && i < len(pm.panels) {
		pm.setActiveIndex(i)
		pm.scrollComboToActive()
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

// PanelAt returns the panel at index i, or nil if out of range — for a caller
// inspecting every panel rather than only the active one.
func (pm *PanelManager) PanelAt(i int) Panel {
	if i < 0 || i >= len(pm.panels) {
		return nil
	}
	return pm.panels[i]
}

// FindIndex returns the index of the first panel for which predicate returns
// true, or -1 if none match — typically a type assertion, such as finding the
// single DetailBrowser panel.
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
		pm.scrollComboToActive()
	}
}

// Prev cycles to the previous panel.
func (pm *PanelManager) Prev() {
	if len(pm.panels) > 0 {
		pm.setActiveIndex((pm.active - 1 + len(pm.panels)) % len(pm.panels))
		pm.scrollComboToActive()
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

// tabMaxX returns the first column the tab row must not draw into, short of the
// combo arrow at the right edge.
func (pm *PanelManager) tabMaxX() int { return pm.rect.X + pm.rect.W - 5 }

// comboGeom returns the drop-down's origin column, width, and usable rows. Draw
// and HandleMouse share it so a click lands on the entry drawn under it. Height
// is what fits below the tab bar, never the panel count: one row per panel runs
// off-screen past ~20 panels, leaving the rest unreachable.
func (pm *PanelManager) comboGeom() (x, w, h int) {
	x = max(pm.rect.X, pm.rect.X+pm.rect.W-30)
	w = min(28, pm.rect.W)
	return x, w, max(0, min(len(pm.panels), pm.contentH()))
}

// comboScrollMax is the largest comboScroll that still fills the list.
func (pm *PanelManager) comboScrollMax(h int) int { return max(0, len(pm.panels)-h) }

// setComboOpen owns the drop-down's open/closed transition, so nothing a
// transition must do can be forgotten at one call site.
//
// It clears comboSbDragging: a close while the scrollbar is held (Escape or
// Enter mid-drag, a tab click) would leave the latch set, and the first Button1
// after the next open would jump the scroll. This is the "a latch must not
// survive into the widget's next showing" rule ModalDialog.Show enforces;
// PanelManager has no Show, so the transition carries it.
//
// mouseDragging is deliberately not cleared: it belongs to the press that
// claimed the gesture (tab row and combo arrow set it too), and clearing it on
// close would release a held press to the panel underneath.
func (pm *PanelManager) setComboOpen(v bool) {
	pm.comboOpen = v
	pm.comboSbDragging = false
	if v {
		pm.scrollComboToActive()
	}
}

// scrollComboToActive brings the active row into the drop-down's window by the
// least movement and clamps the offset. Called on open, after each Up/Down, and
// from every mutator that can move the active index or resize the list while
// open (SetActive, Next, Prev, AddPanel, RemovePanel): global keys that
// App.handleKey consumes reach those without the combo seeing them. A no-op
// while closed.
func (pm *PanelManager) scrollComboToActive() {
	if !pm.comboOpen {
		return
	}
	_, _, h := pm.comboGeom()
	if h <= 0 {
		pm.comboScroll = 0
		return
	}
	if pm.active < pm.comboScroll {
		pm.comboScroll = pm.active
	} else if pm.active >= pm.comboScroll+h {
		pm.comboScroll = pm.active - h + 1
	}
	pm.comboScroll = core.Clamp(pm.comboScroll, 0, pm.comboScrollMax(h))
}

// moveComboSelection moves the highlighted row by delta, clamped (Next/Prev
// wrap because Ctrl+Tab is a cycle; a visible list stops at its ends), and
// brings it into view. The move takes effect immediately: the drop-down
// previews the panel rather than waiting for Enter.
func (pm *PanelManager) moveComboSelection(delta int) {
	if len(pm.panels) == 0 {
		return
	}
	i := core.Clamp(pm.active+delta, 0, len(pm.panels)-1)
	if i == pm.active {
		return
	}
	pm.setActiveIndex(i)
	pm.scrollComboToActive()
}

// scrollCombo moves the visible window by delta rows without changing the
// active panel: the wheel scrolls the list, it doesn't pick from it.
func (pm *PanelManager) scrollCombo(delta int) {
	_, _, h := pm.comboGeom()
	pm.comboScroll = core.Clamp(pm.comboScroll+delta, 0, pm.comboScrollMax(h))
}

// tabSegments computes each panel's tab layout: segment 0 the label, segment 1
// the close glyph (zero-width if not closable). Draw and HandleMouse share it.
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
				tabStyle = tcell.StyleDefault.Background(p.BorderActive).Foreground(color.White).Bold(true)
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

	// Drop-down list — drawn after the active panel, whose content occupies the
	// same rows.
	if pm.comboOpen && len(pm.panels) > 0 {
		listX, listW, listH := pm.comboGeom()
		// A resize can leave the scroll offset out of range.
		pm.comboScroll = core.Clamp(pm.comboScroll, 0, pm.comboScrollMax(listH))
		listStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
		// The scrollbar takes the list's last column, so labels get one less.
		barW := 0
		if len(pm.panels) > listH {
			barW = 1
		}
		for row := range listH {
			i := row + pm.comboScroll
			y := pm.contentY() + row
			st := listStyle
			if i == pm.active {
				st = theme.StyleSelected()
			}
			core.FillRect(s, core.Rect{X: listX, Y: y, W: listW, H: 1}, ' ', st)
			core.DrawTextClipped(s, listX+1, y, listW-2-barW, st,
				core.Truncate(pm.panels[i].Title(), listW-3-barW))
		}
		if barW > 0 {
			core.DrawScrollbar(s, listX+listW-1, pm.contentY(), listH,
				len(pm.panels), listH, pm.comboScroll, listStyle, theme.StyleSelected())
		}
	}
}

// HandleKey routes keyboard events to the combo (if open) or active panel.
func (pm *PanelManager) HandleKey(ev *tcell.EventKey) bool {
	if pm.comboOpen {
		switch ev.Key() {
		case tcell.KeyEscape:
			pm.setComboOpen(false)
		case tcell.KeyUp:
			pm.moveComboSelection(-1)
		case tcell.KeyDown:
			pm.moveComboSelection(1)
		case tcell.KeyPgUp:
			_, _, h := pm.comboGeom()
			pm.moveComboSelection(-max(1, h))
		case tcell.KeyPgDn:
			_, _, h := pm.comboGeom()
			pm.moveComboSelection(max(1, h))
		case tcell.KeyHome:
			pm.moveComboSelection(-len(pm.panels))
		case tcell.KeyEnd:
			pm.moveComboSelection(len(pm.panels))
		case tcell.KeyEnter:
			pm.setComboOpen(false)
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

	// Releases can land outside our bounds (e.g. a splitter drag inside the active
	// panel), so always forward them to end drags cleanly.
	if ev.Buttons() == tcell.ButtonNone {
		pm.mouseDragging, pm.comboSbDragging = false, false
		if p := pm.ActivePanel(); p != nil {
			return p.HandleMouse(ev)
		}
	}

	// The drop-down's scrollbar goes ahead of the bounds check (a latched drag owns
	// the mouse until release) and of the list hit-test (the bar occupies the
	// list's last column). Writes nothing and returns false when no bar is needed.
	if pm.comboOpen {
		listX, listW, listH := pm.comboGeom()
		if core.HandleScrollbarDrag(ev, listX+listW-1, pm.contentY(), listH,
			len(pm.panels), &pm.comboSbDragging, &pm.comboScroll) {
			return true
		}
	}

	if mx < pm.rect.X || mx >= pm.rect.X+pm.rect.W {
		return false
	}

	// An open drop-down is an overlay and gets first refusal of the wheel, as
	// HandleKey does for keys; otherwise the wheel scrolls the editor under it.
	// Button1 outside the list falls through; see the dismiss branch below.
	if pm.comboOpen {
		switch ev.Buttons() {
		case tcell.WheelUp:
			pm.scrollCombo(-1)
			return true
		case tcell.WheelDown:
			pm.scrollCombo(1)
			return true
		}
	}

	// Combo toggle arrow
	if my == pm.rect.Y && mx >= pm.rect.X+pm.rect.W-4 {
		if ev.Buttons() == tcell.Button1 {
			if pm.mouseDragging {
				// Same physical press; don't re-toggle on resent motion.
				return true
			}
			pm.mouseDragging = true
			pm.setComboOpen(!pm.comboOpen)
			return true
		}
	}

	// Tab row click; segments come from tabSegments, as in Draw.
	if my == pm.rect.Y && ev.Buttons() == tcell.Button1 {
		if pm.mouseDragging {
			// Same physical press; don't re-fire OnCloseTab (it may prompt to save).
			return true
		}
		pm.mouseDragging = true
		for i, seg := range pm.tabSegments() {
			closeSeg := seg[1]
			if closeSeg.W > 0 && mx >= closeSeg.X && mx < closeSeg.X+closeSeg.W {
				if pm.OnCloseTab != nil {
					pm.OnCloseTab(i)
				}
				pm.setComboOpen(false)
				return true
			}
			labelSeg := seg[0]
			if mx >= labelSeg.X && mx < labelSeg.X+labelSeg.W {
				pm.setActiveIndex(i)
				pm.setComboOpen(false)
				return true
			}
		}
	}

	// Combo list click
	if pm.comboOpen {
		listX, listW, listH := pm.comboGeom()
		row := my - pm.contentY()
		if mx >= listX && mx < listX+listW && row >= 0 && row < listH &&
			ev.Buttons() == tcell.Button1 {
			if pm.mouseDragging {
				return true
			}
			pm.mouseDragging = true
			pm.setActiveIndex(row + pm.comboScroll)
			pm.setComboOpen(false)
			return true
		}
		// Close on a click outside the list, then fall through so the click also does
		// what it was aimed at (as widgets.DropDown does).
		if ev.Buttons() == tcell.Button1 {
			pm.setComboOpen(false)
		}
	}

	// A press claimed above owns the gesture until release, so held-Button1
	// resends must not reach the panel underneath. The branches above return early
	// only while their latch still matches: selecting from the drop-down closes it,
	// leaving nothing to match, and the held press would hit the newly active panel.
	if pm.mouseDragging && ev.Buttons() == tcell.Button1 {
		return true
	}

	if p := pm.ActivePanel(); p != nil && my >= pm.contentY() {
		return p.HandleMouse(ev)
	}
	return false
}
