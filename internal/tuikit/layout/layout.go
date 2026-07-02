// Package layout provides Panel interface, PanelManager (tab/combo switcher),
// and Splitter (draggable/keyboard-resizable divider between two regions).
// These types are pure layout infrastructure; they have no dependency on any
// application-level code.
package layout

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// Panel interface
// ---------------------------------------------------------------------------

// Panel is the contract that every right-hand side panel must satisfy.
// SetBounds is called by the layout manager whenever the available space
// changes.  Title is shown in the tab bar.
type Panel interface {
	SetBounds(x, y, w, h int)
	Draw(s tcell.Screen)
	HandleKey(ev *tcell.EventKey) bool
	HandleMouse(ev *tcell.EventMouse) bool
	Title() string
}

// Activatable is an optional interface a Panel may implement to be notified
// when it becomes the visible/focused panel (true) or loses focus (false).
// PanelManager calls SetActive automatically on every panel that implements
// it whenever the active panel changes — panels that don't need an "active"
// concept (e.g. a static read-only view) can simply not implement it.
type Activatable interface {
	SetActive(active bool)
}

// ---------------------------------------------------------------------------
// PanelManager
// ---------------------------------------------------------------------------

// PanelManager manages a stack of overlapping Panels displayed one at a time.
// A tab bar at the top lets the user switch between panels; a drop-down combo
// arrow shows when there are more tabs than fit.
type PanelManager struct {
	rect       core.Rect
	panels     []Panel
	active     int
	comboOpen  bool
	comboHover int
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

// Draw renders the tab bar and the active panel.
func (pm *PanelManager) Draw(s tcell.Screen) {
	p := theme.Active()
	barStyle := theme.StyleMenuBar()
	core.FillRect(s, core.Rect{X: pm.rect.X, Y: pm.rect.Y, W: pm.rect.W, H: 1}, ' ', barStyle)

	if len(pm.panels) == 0 {
		core.DrawText(s, pm.rect.X+1, pm.rect.Y, barStyle, "(no panels open — Ctrl+N for a new query)")
	} else {
		col := pm.rect.X + 1
		for i, panel := range pm.panels {
			tabStyle := barStyle
			if i == pm.active {
				tabStyle = tcell.StyleDefault.Background(p.BorderActive).Foreground(tcell.ColorWhite).Bold(true)
			}
			label := " " + core.Truncate(panel.Title(), 20) + " "
			labelW := core.DisplayWidth(label)
			if col+labelW > pm.rect.X+pm.rect.W-5 {
				break
			}
			core.DrawText(s, col, pm.rect.Y, tabStyle, label)
			col += labelW + 1
		}
		// Combo arrow
		arrowStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.TextDim)
		core.DrawText(s, pm.rect.X+pm.rect.W-4, pm.rect.Y, arrowStyle, " [v]")
	}

	// Drop-down list
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

	if panel := pm.ActivePanel(); panel != nil {
		panel.Draw(s)
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
			pm.comboOpen = !pm.comboOpen
			return true
		}
	}

	// Tab row click
	if my == pm.rect.Y && ev.Buttons() == tcell.Button1 {
		col := pm.rect.X + 1
		for i, panel := range pm.panels {
			label := " " + core.Truncate(panel.Title(), 20) + " "
			labelW := core.DisplayWidth(label)
			if mx >= col && mx < col+labelW {
				pm.setActiveIndex(i)
				pm.comboOpen = false
				return true
			}
			col += labelW + 1
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

// ---------------------------------------------------------------------------
// Splitter
// ---------------------------------------------------------------------------

// SplitterDir describes the orientation of a Splitter.
type SplitterDir int

const (
	SplitterVertical   SplitterDir = iota // column divider (left/right panes)
	SplitterHorizontal                    // row divider (top/bottom panes)
)

// Splitter is a draggable divider between two panel regions.
// It owns the geometry calculation; the caller owns the two child panels.
type Splitter struct {
	rect     core.Rect
	dir      SplitterDir
	ratio    float64 // fraction of the rect dimension given to the first pane
	minRatio float64
	maxRatio float64
	dragging bool
	dragBase int // screen coordinate where drag started
	ratioBase float64
	label    string // optional label drawn on the splitter bar
}

// NewHorizontalSplitter creates a top/bottom splitter (editor over results).
func NewHorizontalSplitter(label string) *Splitter {
	return new(Splitter{
		dir:      SplitterHorizontal,
		ratio:    0.5,
		minRatio: 0.1,
		maxRatio: 0.9,
		label:    label,
	})
}

// NewVerticalSplitter creates a left/right splitter (explorer | panels).
func NewVerticalSplitter() *Splitter {
	return new(Splitter{
		dir:      SplitterVertical,
		ratio:    0.3,
		minRatio: 0.1,
		maxRatio: 0.8,
	})
}

// SetBounds positions the splitter in the available rect.
func (sp *Splitter) SetBounds(x, y, w, h int) {
	sp.rect = core.Rect{X: x, Y: y, W: w, H: h}
}

// Ratio returns the current split ratio.
func (sp *Splitter) Ratio() float64 { return sp.ratio }

// SetRatio explicitly sets the split ratio.
func (sp *Splitter) SetRatio(r float64) {
	sp.ratio = core.ClampF(r, sp.minRatio, sp.maxRatio)
}

// SplitPos returns the absolute screen coordinate of the divider line.
func (sp *Splitter) SplitPos() int {
	if sp.dir == SplitterHorizontal {
		return sp.rect.Y + int(float64(sp.rect.H)*sp.ratio)
	}
	return sp.rect.X + int(float64(sp.rect.W)*sp.ratio)
}

// FirstRect returns the rect for the first (top or left) pane.
func (sp *Splitter) FirstRect() core.Rect {
	pos := sp.SplitPos()
	if sp.dir == SplitterHorizontal {
		return core.Rect{X: sp.rect.X, Y: sp.rect.Y, W: sp.rect.W, H: pos - sp.rect.Y}
	}
	return core.Rect{X: sp.rect.X, Y: sp.rect.Y, W: pos - sp.rect.X, H: sp.rect.H}
}

// SecondRect returns the rect for the second (bottom or right) pane.
func (sp *Splitter) SecondRect() core.Rect {
	pos := sp.SplitPos()
	if sp.dir == SplitterHorizontal {
		afterBar := pos + 1
		return core.Rect{X: sp.rect.X, Y: afterBar, W: sp.rect.W, H: sp.rect.Y + sp.rect.H - afterBar}
	}
	afterBar := pos + 1
	return core.Rect{X: afterBar, Y: sp.rect.Y, W: sp.rect.X + sp.rect.W - afterBar, H: sp.rect.H}
}

// Draw renders the divider line.
func (sp *Splitter) Draw(s tcell.Screen) {
	p := theme.Active()
	barColor := p.Splitter
	if sp.dragging {
		barColor = p.SplitterHover
	}
	style := tcell.StyleDefault.Background(barColor).Foreground(p.TextDim)
	pos := sp.SplitPos()

	if sp.dir == SplitterHorizontal {
		core.FillRect(s, core.Rect{X: sp.rect.X, Y: pos, W: sp.rect.W, H: 1}, '─', style)
		if sp.label != "" {
			core.DrawTextClipped(s, sp.rect.X+2, pos, sp.rect.W-4, style, sp.label)
		}
	} else {
		core.FillRect(s, core.Rect{X: pos, Y: sp.rect.Y, W: 1, H: sp.rect.H}, '│', style)
	}
}

// HandleKey adjusts the ratio with Ctrl+Arrow.
func (sp *Splitter) HandleKey(ev *tcell.EventKey) bool {
	const step = 0.05
	if sp.dir == SplitterHorizontal {
		if ev.Key() == tcell.KeyUp && ev.Modifiers()&tcell.ModCtrl != 0 {
			sp.ratio = core.ClampF(sp.ratio-step, sp.minRatio, sp.maxRatio)
			return true
		}
		if ev.Key() == tcell.KeyDown && ev.Modifiers()&tcell.ModCtrl != 0 {
			sp.ratio = core.ClampF(sp.ratio+step, sp.minRatio, sp.maxRatio)
			return true
		}
	} else {
		if ev.Key() == tcell.KeyLeft && ev.Modifiers()&tcell.ModCtrl != 0 {
			sp.ratio = core.ClampF(sp.ratio-step, sp.minRatio, sp.maxRatio)
			return true
		}
		if ev.Key() == tcell.KeyRight && ev.Modifiers()&tcell.ModCtrl != 0 {
			sp.ratio = core.ClampF(sp.ratio+step, sp.minRatio, sp.maxRatio)
			return true
		}
	}
	return false
}

// HandleMouse handles drag events on the splitter bar.
// Returns true when the event was consumed (including during active drag).
func (sp *Splitter) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	pos := sp.SplitPos()

	onBar := false
	if sp.dir == SplitterHorizontal {
		onBar = my == pos && mx >= sp.rect.X && mx < sp.rect.X+sp.rect.W
	} else {
		onBar = mx == pos && my >= sp.rect.Y && my < sp.rect.Y+sp.rect.H
	}

	if !sp.dragging && ev.Buttons() == tcell.Button1 && onBar {
		sp.dragging = true
		if sp.dir == SplitterHorizontal {
			sp.dragBase = my
		} else {
			sp.dragBase = mx
		}
		sp.ratioBase = sp.ratio
		return true
	}
	if sp.dragging {
		if ev.Buttons() == tcell.ButtonNone {
			sp.dragging = false
			return true
		}
		if sp.dir == SplitterHorizontal {
			delta := float64(my-sp.dragBase) / float64(sp.rect.H)
			sp.ratio = core.ClampF(sp.ratioBase+delta, sp.minRatio, sp.maxRatio)
		} else {
			delta := float64(mx-sp.dragBase) / float64(sp.rect.W)
			sp.ratio = core.ClampF(sp.ratioBase+delta, sp.minRatio, sp.maxRatio)
		}
		return true
	}
	return false
}
