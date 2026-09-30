package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// xevent_viewer_input.go is the Extended Events viewer's keyboard and mouse
// handling, including the gesture routing described in ARCHITECTURE.md § The
// mouseDragging idiom.

// HandleKey gives the panel's own bindings a look, then the grid. It returns
// false for anything not acted on, so App's Tab and Escape still work.
func (v *XEventViewer) HandleKey(ev *tcell.EventKey) bool {
	if v.grid.OverlayActive() {
		return v.grid.HandleKey(ev)
	}
	switch ev.Key() {
	case tcell.KeyF5:
		// Always claimed: App runs the active query on an unclaimed F5.
		if v.live {
			if !v.feed.running {
				v.runToolByID(xeToolFeed)
			}
		} else {
			v.runToolByID(xeToolRefresh)
		}
		return true
	case tcell.KeyF6:
		return v.runToolByID(xeToolPause)
	case tcell.KeyF7:
		v.editFilter()
		return true
	case tcell.KeyF8:
		v.runToolByID(xeToolColumns)
		return true
	case tcell.KeyF2:
		// SSMS's bookmark keys: Ctrl+F2 toggles, F2 and Shift+F2 move.
		switch {
		case ev.Modifiers()&tcell.ModCtrl != 0:
			v.toggleBookmark()
		case ev.Modifiers()&tcell.ModShift != 0:
			v.nextBookmark(-1)
		default:
			v.nextBookmark(1)
		}
		return true
	}
	if v.handleGroupKey(ev) {
		return true
	}
	// Alt+Up/Down scrolls the details pane, as in the Log File Viewer.
	if ev.Modifiers()&tcell.ModAlt != 0 {
		switch ev.Key() {
		case tcell.KeyDown:
			v.scrollDetails(1)
			return true
		case tcell.KeyUp:
			v.scrollDetails(-1)
			return true
		}
	}
	if v.splitter.HandleKey(ev) {
		v.layoutChildren()
		return true
	}
	beforeRow := v.grid.SelectedRow()
	if !v.grid.HandleKey(ev) {
		return false
	}
	if v.grid.SelectedRow() != beforeRow {
		v.detailScroll = 0
	}
	return true
}

// scrollDetails moves the details pane by delta lines, clamped to its text.
func (v *XEventViewer) scrollDetails(delta int) {
	e, ok := v.selectedEvent()
	if !ok || v.detailRect.H <= 0 {
		return
	}
	limit := max(0, len(v.detailLines(e, v.detailRect.W-2))-v.detailRect.H)
	v.detailScroll = min(limit, max(0, v.detailScroll+delta))
}

// HandleMouse routes a mouse event to the sub-region owning it: a press
// claims the gesture until its release (dragZone), and a release reaches
// every latch-bearing child wherever the pointer is.
func (v *XEventViewer) HandleMouse(ev *tcell.EventMouse) bool {
	if v.grid.OverlayActive() {
		return v.grid.HandleMouse(ev)
	}
	mx, my := ev.Position()

	if ev.Buttons() == tcell.ButtonNone {
		handled := false
		if v.splitter.HandleMouse(ev) {
			v.layoutChildren()
			handled = true
		}
		if v.grid.HandleMouse(ev) {
			handled = true
		}
		v.dragZone = xZoneNone
		return handled
	}
	if v.dragZone != xZoneNone {
		if ev.Buttons() == tcell.Button1 {
			return v.routeDrag(ev)
		}
		return true
	}
	if !v.rect.Contains(mx, my) {
		return false
	}
	if v.detailRect.Contains(mx, my) {
		switch ev.Buttons() {
		case tcell.WheelDown:
			v.scrollDetails(1)
			return true
		case tcell.WheelUp:
			v.scrollDetails(-1)
			return true
		}
	}
	if v.splitter.HandleMouse(ev) {
		v.layoutChildren()
		v.armDrag(ev, xZoneSplitter)
		return true
	}
	if v.toolRect.H == 1 && my == v.toolRect.Y && ev.Buttons() == tcell.Button1 {
		// The action runs on the press; xZoneToolbar swallows the repeats.
		v.armDrag(ev, xZoneToolbar)
		if i := toolButtonAt(v.tools, mx, my); i >= 0 {
			v.runTool(i)
		} else if v.more.rect.Contains(mx, my) {
			v.showOverflowMenu()
		}
		return true
	}
	if ev.Buttons() == tcell.Button1 || ev.Buttons() == tcell.Button2 {
		beforeRow := v.grid.SelectedRow()
		if v.grid.HandleMouse(ev) {
			v.armDrag(ev, xZoneGrid)
			if v.grid.SelectedRow() != beforeRow {
				v.detailScroll = 0
			}
			if ev.Buttons() == tcell.Button1 {
				v.clickGroup(mx, beforeRow)
			}
			return true
		}
		v.armDrag(ev, xZoneUnclaimed)
		return false
	}
	return v.grid.HandleMouse(ev)
}

// armDrag records that zone took a Button1 press.
func (v *XEventViewer) armDrag(ev *tcell.EventMouse, zone xeDragZone) {
	if ev.Buttons() == tcell.Button1 {
		v.dragZone = zone
	}
}

// routeDrag delivers a held-Button1 event to the zone that armed the gesture.
func (v *XEventViewer) routeDrag(ev *tcell.EventMouse) bool {
	switch v.dragZone {
	case xZoneSplitter:
		if v.splitter.HandleMouse(ev) {
			v.layoutChildren()
		}
	case xZoneGrid:
		v.grid.HandleMouse(ev)
	}
	return true
}

// handleGroupKey is the keyboard on a group row: Enter or Space toggles it,
// Right opens and Left closes it, as on a tree node. Anywhere else, and for
// every other key, it answers false and the grid has the key.
func (v *XEventViewer) handleGroupKey(ev *tcell.EventKey) bool {
	g, ok := v.groupAt(v.grid.SelectedRow())
	if !ok || ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) != 0 {
		return false
	}
	switch {
	case ev.Key() == tcell.KeyEnter || core.EvRune(ev) == ' ':
		v.setExpanded(g, !v.expanded[g.Key])
	case ev.Key() == tcell.KeyRight && !v.expanded[g.Key]:
		v.setExpanded(g, true)
	case ev.Key() == tcell.KeyLeft && v.expanded[g.Key]:
		v.setExpanded(g, false)
	default:
		return false
	}
	return true
}

// clickGroup toggles the group row a Button1 press landed on when it hit the
// row's ▸/▾ glyph, or the row was already selected (a second click, which a
// double-click is too). A first click elsewhere on the row only selects it,
// as on a tree node.
func (v *XEventViewer) clickGroup(x, beforeRow int) {
	row := v.grid.SelectedRow()
	g, ok := v.groupAt(row)
	if !ok {
		return
	}
	glyph := v.gridRect.X + 1 + 2*g.Level
	if row == beforeRow || (x >= glyph && x < glyph+2) {
		v.setExpanded(g, !v.expanded[g.Key])
	}
}
