package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// query_store_panel_input.go holds the Query Store panel's keyboard and mouse
// handling, including the gesture routing described in ARCHITECTURE.md
// § The mouseDragging idiom.

// HandleKey routes a key to whichever grid holds focus, after the panel's own
// bindings get a look. It returns false for anything it didn't act on — the
// panel is a keyboard trap otherwise, since App only reaches its own bindings
// (Tab out, Escape, the menu keys) on a false.
func (p *QueryStorePanel) HandleKey(ev *tcell.EventKey) bool {
	// A grid's value popup is drawn over everything the panel has, so it gets
	// first refusal — same rule as QueryPanel.HandleKey.
	if g := p.overlayGrid(); g != nil {
		return g.HandleKey(ev)
	}
	// F5 is the Refresh cell's action, run through the same gate the click
	// path uses — the key must not do what a dimmed button refuses to.
	if ev.Key() == tcell.KeyF5 {
		p.runSel(qsToolRefresh)
		return true
	}
	// Tab walks the report grid → the plan grid → out of the panel. The last
	// step must return false: App only moves focus to Object Explorer when the
	// panel declines the key, so a panel that always consumes Tab can only be
	// left with the mouse. Leaving on a false also resets focus to the report
	// grid, so coming back starts where the rows are.
	if ev.Key() == tcell.KeyTab {
		if p.focus == qsFocusReport {
			p.setFocus(qsFocusPlans)
			return true
		}
		p.setFocus(qsFocusReport)
		return false
	}
	// Ctrl+Up/Down resizes the panes. Offered before the focused grid so its
	// own Ctrl+Arrow handling doesn't swallow it. The chart splitter goes
	// first: it is the one a user reaches for, and both answer the same key.
	if p.chartSplit.HandleKey(ev) || p.planSplit.HandleKey(ev) {
		p.layoutChildren()
		return true
	}
	return p.focusedGrid().HandleKey(ev)
}

// overlayGrid is whichever grid has a value popup open, nil when neither has.
// Only one can: a popup opens on the focused grid and closes when focus leaves.
func (p *QueryStorePanel) overlayGrid() *controls.DataGrid {
	switch {
	case p.grid.OverlayActive():
		return p.grid
	case p.plansGrid.OverlayActive():
		return p.plansGrid
	}
	return nil
}

// focusedGrid is the grid the keyboard is on.
func (p *QueryStorePanel) focusedGrid() *controls.DataGrid {
	if p.focus == qsFocusPlans {
		return p.plansGrid
	}
	return p.grid
}

// setFocus moves the keyboard between the two grids.
func (p *QueryStorePanel) setFocus(f qsFocus) {
	p.focus = f
	p.applyFocus()
}

// HandleMouse routes a mouse event to whichever sub-region owns it. The
// gesture rules it implements are the five in ARCHITECTURE.md § The
// mouseDragging idiom: a press claims the gesture until its release
// (dragZone), and a release is forwarded to every latch-bearing child
// regardless of where the pointer ended up.
func (p *QueryStorePanel) HandleMouse(ev *tcell.EventMouse) bool {
	// A grid's value popup can be drawn over any part of the panel, so it gets
	// every event — including the release ending a drag inside it — before any
	// positional routing below.
	if g := p.overlayGrid(); g != nil {
		return g.HandleMouse(ev)
	}
	mx, my := ev.Position()

	// Release: forwarded to both splitters and both grids wherever the pointer
	// is, so a drag that ended outside the panel still clears their latches.
	// Without this a grid treats the next click as a continuation of the last
	// drag's anchor.
	if ev.Buttons() == tcell.ButtonNone {
		handled := false
		if p.chartSplit.HandleMouse(ev) || p.planSplit.HandleMouse(ev) {
			p.layoutChildren()
			handled = true
		}
		if p.grid.HandleMouse(ev) {
			handled = true
		}
		if p.plansGrid.HandleMouse(ev) {
			handled = true
		}
		p.dragZone = qsZoneNone
		return handled
	}
	// Everything from the press that armed dragZone through to its release
	// belongs to the sub-region that claimed it, wherever the pointer has
	// drifted since — which is why this outranks the bounds check. A wheel tick
	// arriving mid-gesture is swallowed rather than routed: it is not part of
	// the gesture.
	if p.dragZone != qsZoneNone {
		if ev.Buttons() == tcell.Button1 {
			return p.routeDrag(ev)
		}
		return true
	}
	if mx < p.rect.X || mx >= p.rect.X+p.rect.W {
		return false
	}
	if p.chartSplit.HandleMouse(ev) {
		p.layoutChildren()
		p.armDrag(ev, qsZoneChartSplit)
		return true
	}
	if p.planSplit.HandleMouse(ev) {
		p.layoutChildren()
		p.armDrag(ev, qsZonePlanSplit)
		return true
	}
	if ev.Buttons() == tcell.Button1 && p.handleToolbarPress(mx, my) {
		p.armDrag(ev, qsZoneToolbar)
		return true
	}
	if ev.Buttons() == tcell.Button1 || ev.Buttons() == tcell.Button2 {
		if p.plansGrid.HandleMouse(ev) {
			p.armDrag(ev, qsZonePlans)
			p.setFocus(qsFocusPlans)
			return true
		}
		if p.grid.HandleMouse(ev) {
			p.armDrag(ev, qsZoneGrid)
			p.setFocus(qsFocusReport)
			return true
		}
		p.armDrag(ev, qsZoneUnclaimed)
		return false
	}
	if p.plansGrid.HandleMouse(ev) {
		return true
	}
	return p.grid.HandleMouse(ev)
}

// handleToolbarPress runs the toolbar cell under the pointer, and reports
// whether the press belonged to a toolbar row at all. The action runs on the
// press, so the repeats tcell sends while the button stays down must not reach
// it again — qsZoneToolbar swallows them in routeDrag. runSel/runAct apply the
// same gate drawToolRow dims on, so a dimmed cell is inert rather than merely
// grey.
func (p *QueryStorePanel) handleToolbarPress(mx, my int) bool {
	switch {
	case p.selRect.H == 1 && my == p.selRect.Y:
		if p.selMore.rect.Contains(mx, my) {
			p.showOverflowMenu(p.selMore.rect, p.sel, p.hiddenSel, p.selDisabled, p.selReason, p.runSel)
			return true
		}
		if i := toolButtonAt(p.sel, mx, my); i >= 0 {
			p.runSel(i)
		}
		return true
	case p.actRect.H == 1 && my == p.actRect.Y:
		if p.actMore.rect.Contains(mx, my) {
			p.showOverflowMenu(p.actMore.rect, p.acts, p.hiddenActs, p.actDisabled, p.actReason, p.runAct)
			return true
		}
		if i := toolButtonAt(p.acts, mx, my); i >= 0 {
			p.runAct(i)
		}
		return true
	}
	return false
}

// armDrag records that zone consumed a Button1 press, so every further event
// until the release goes back to it — see the dragZone field.
func (p *QueryStorePanel) armDrag(ev *tcell.EventMouse, zone qsDragZone) {
	if ev.Buttons() == tcell.Button1 {
		p.dragZone = zone
	}
}

// routeDrag delivers a held-Button1 event to the sub-region that armed the
// gesture. qsZoneToolbar and qsZoneUnclaimed swallow it: the button's action
// already ran on the press, and the point of owning the gesture is only that
// no other sub-region sees the repeats.
func (p *QueryStorePanel) routeDrag(ev *tcell.EventMouse) bool {
	switch p.dragZone {
	case qsZoneChartSplit:
		if p.chartSplit.HandleMouse(ev) {
			p.layoutChildren()
		}
	case qsZonePlanSplit:
		if p.planSplit.HandleMouse(ev) {
			p.layoutChildren()
		}
	case qsZoneGrid:
		p.grid.HandleMouse(ev)
	case qsZonePlans:
		p.plansGrid.HandleMouse(ev)
	}
	return true
}

// HasSelection, SelectedText, Cut, Paste and SelectAll implement
// clipboardTarget by forwarding to the focused grid — the same forwarding
// DetailBrowser does to its one grid.
func (p *QueryStorePanel) HasSelection() bool   { return p.focusedGrid().HasSelection() }
func (p *QueryStorePanel) SelectedText() string { return p.focusedGrid().SelectedText() }
func (p *QueryStorePanel) Cut() string          { return p.focusedGrid().Cut() }
func (p *QueryStorePanel) Paste(text string)    { p.focusedGrid().Paste(text) }
func (p *QueryStorePanel) SelectAll()           { p.focusedGrid().SelectAll() }
