package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// replication_monitor_panel_input.go is Replication Monitor's keyboard and
// mouse handling, with the gesture routing of ARCHITECTURE.md § The
// mouseDragging idiom.

// HandleKey: F5 refreshes, Tab walks the four grids and then leaves (false,
// so App moves focus on), Ctrl+Up/Down resize the panes, and the rest goes to
// the focused grid.
func (p *ReplicationMonitorPanel) HandleKey(ev *tcell.EventKey) bool {
	if g := p.overlayGrid(); g != nil {
		return g.HandleKey(ev)
	}
	switch ev.Key() {
	case tcell.KeyF5:
		p.runTool(rmToolRefresh)
		return true
	case tcell.KeyTab:
		if p.focus < rmFocusCount-1 {
			p.setFocus(p.focus + 1)
			return true
		}
		p.setFocus(rmFocusPubs)
		return false
	case tcell.KeyBacktab:
		if p.focus > rmFocusPubs {
			p.setFocus(p.focus - 1)
			return true
		}
		return false
	}
	for _, sp := range p.splits {
		if sp.HandleKey(ev) {
			p.layoutChildren()
			return true
		}
	}
	return p.focusedGrid().HandleKey(ev)
}

// overlayGrid is the grid with a value popup or cell menu open, if any.
func (p *ReplicationMonitorPanel) overlayGrid() *controls.DataGrid {
	for _, g := range p.grids() {
		if g.OverlayActive() {
			return g
		}
	}
	return nil
}

// HandleMouse routes a mouse event to the sub-region that owns it: a press
// claims the gesture until its release, and a release goes to every
// latch-bearing child wherever the pointer ended.
func (p *ReplicationMonitorPanel) HandleMouse(ev *tcell.EventMouse) bool {
	if g := p.overlayGrid(); g != nil {
		return g.HandleMouse(ev)
	}
	mx, my := ev.Position()
	if ev.Buttons() == tcell.ButtonNone {
		handled := false
		for _, sp := range p.splits {
			if sp.HandleMouse(ev) {
				p.layoutChildren()
				handled = true
			}
		}
		for _, g := range p.grids() {
			if g.HandleMouse(ev) {
				handled = true
			}
		}
		p.dragZone, p.dragGrid = rmZoneNone, nil
		return handled
	}
	if p.dragZone != rmZoneNone {
		if ev.Buttons() == tcell.Button1 {
			return p.routeDrag(ev)
		}
		return true
	}
	if !p.rect.Contains(mx, my) {
		return false
	}
	for i, sp := range p.splits {
		if sp.HandleMouse(ev) {
			p.layoutChildren()
			p.armDrag(ev, rmZoneSplit)
			p.dragSplit = i
			return true
		}
	}
	if ev.Buttons() == tcell.Button1 && p.toolRect.H == 1 && my == p.toolRect.Y {
		if p.tools.More.Rect.Contains(mx, my) {
			p.popMenuAt(p.tools.More.Rect, p.tools.OverflowItems(p.toolDisabled, p.toolReason, p.runTool))
		} else if i := p.tools.CellAt(mx, my); i >= 0 {
			p.runTool(i)
		}
		p.armDrag(ev, rmZoneToolbar)
		return true
	}
	if ev.Buttons() == tcell.Button1 || ev.Buttons() == tcell.Button2 {
		for i, g := range p.grids() {
			if g.Bounds().Contains(mx, my) && g.HandleMouse(ev) {
				p.armDrag(ev, rmZoneGrid)
				p.dragGrid = g
				p.setFocus(rmFocus(i))
				return true
			}
		}
		p.armDrag(ev, rmZoneUnclaimed)
		return false
	}
	// The wheel: the grid under the pointer.
	for _, g := range p.grids() {
		if g.Bounds().Contains(mx, my) {
			return g.HandleMouse(ev)
		}
	}
	return false
}

func (p *ReplicationMonitorPanel) armDrag(ev *tcell.EventMouse, zone rmDragZone) {
	if ev.Buttons() == tcell.Button1 {
		p.dragZone = zone
	}
}

// routeDrag delivers a held-Button1 event to whatever claimed the gesture;
// the toolbar and an unclaimed press swallow it.
func (p *ReplicationMonitorPanel) routeDrag(ev *tcell.EventMouse) bool {
	switch p.dragZone {
	case rmZoneSplit:
		if p.splits[p.dragSplit].HandleMouse(ev) {
			p.layoutChildren()
		}
	case rmZoneGrid:
		if p.dragGrid != nil {
			p.dragGrid.HandleMouse(ev)
		}
	}
	return true
}

// HasSelection and the rest of clipboardTarget forward to the focused grid.
func (p *ReplicationMonitorPanel) HasSelection() bool   { return p.focusedGrid().HasSelection() }
func (p *ReplicationMonitorPanel) SelectedText() string { return p.focusedGrid().SelectedText() }
func (p *ReplicationMonitorPanel) Cut() string          { return p.focusedGrid().Cut() }
func (p *ReplicationMonitorPanel) Paste(text string)    { p.focusedGrid().Paste(text) }
func (p *ReplicationMonitorPanel) SelectAll()           { p.focusedGrid().SelectAll() }
