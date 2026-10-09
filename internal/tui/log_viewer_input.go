package tui

import (
	"github.com/gdamore/tcell/v3"
)

// log_viewer_input.go holds the Log File Viewer's keyboard and mouse
// handling, including the gesture routing described in ARCHITECTURE.md
// § The mouseDragging idiom.

// HandleKey routes a key to the filter field or the grid, whichever holds
// focus, after the panel's own bindings. It returns false for anything it
// didn't act on: App only reaches its own bindings (Tab out, Escape, the menu
// keys) on a false, so the panel is otherwise a keyboard trap.
func (lv *LogViewer) HandleKey(ev *tcell.EventKey) bool {
	// The grid's value popup is drawn over everything, so it gets first refusal
	// (as in QueryPanel.HandleKey).
	if lv.grid.OverlayActive() {
		return lv.grid.HandleKey(ev)
	}
	// F5 is the Refresh cell's action through the same gate as the click path: a
	// key must not do what a dimmed button refuses.
	if ev.Key() == tcell.KeyF5 {
		lv.runTool(logToolRefresh)
		return true
	}
	// Tab walks grid -> filter -> out of the panel. The last step must return
	// false: App moves focus to Object Explorer only when the panel declines, so
	// a panel that always consumes Tab can be left only with the mouse. Returning
	// false also resets focus to the grid.
	if ev.Key() == tcell.KeyTab {
		if !lv.filterFocused && lv.filterVisible() {
			lv.setFilterFocused(true)
			return true
		}
		lv.setFilterFocused(false)
		return false
	}
	// Alt+Up/Down scrolls the details pane. Not PgUp/PgDn: those belong to the
	// grid, where the cursor is while the pane is read. Alt+Arrow reaches the app
	// on terminals where Shift+Arrow doesn't.
	if ev.Modifiers()&tcell.ModAlt != 0 {
		switch ev.Key() {
		case tcell.KeyDown:
			lv.scrollDetails(1)
			return true
		case tcell.KeyUp:
			lv.scrollDetails(-1)
			return true
		}
	}
	// Ctrl+Up/Down resizes the panes. Offered before the focused child so the
	// grid's own Ctrl+Arrow handling doesn't swallow it.
	if lv.splitter.HandleKey(ev) {
		lv.layoutChildren()
		return true
	}
	if lv.filterFocused {
		if ev.Key() == tcell.KeyEscape {
			// Escape with text in the box clears the filter; with an empty box it belongs
			// to App (close the panel / leave focus), so fall through rather than swallow.
			if lv.filter.Value() == "" {
				return false
			}
			lv.filter.SetValue("")
			lv.applyFilter()
			return true
		}
		before := lv.filter.Value()
		if !lv.filter.HandleKey(ev) {
			return false
		}
		if lv.filter.Value() != before {
			lv.applyFilter()
		}
		return true
	}
	// A row change moves the details pane to the top of the new entry: the old
	// scroll offset points into a message no longer on screen.
	beforeRow := lv.grid.SelectedRow()
	if !lv.grid.HandleKey(ev) {
		return false
	}
	if lv.grid.SelectedRow() != beforeRow {
		lv.detailScroll = 0
	}
	return true
}

// setFilterFocused moves keyboard focus between the filter field and the grid,
// keeping both widgets' focus flags in step so only one draws a cursor.
func (lv *LogViewer) setFilterFocused(v bool) {
	lv.filterFocused = v
	lv.filter.Focus(lv.active && v)
	lv.grid.Focus(lv.active && !v)
}

// scrollDetails moves the details pane by delta lines, clamped to the text it
// has. The count comes from the same detailLines call that draws, so the last
// line is always reachable and never overshot.
func (lv *LogViewer) scrollDetails(delta int) {
	row, ok := lv.selectedLogRow()
	if !ok || lv.detailRect.H <= 0 {
		return
	}
	limit := max(0, len(lv.detailLines(row, lv.detailRect.W-2))-lv.detailRect.H)
	lv.detailScroll = min(limit, max(0, lv.detailScroll+delta))
}

// HandleMouse routes a mouse event to the sub-region that owns it. The gesture
// rules are the five invariants in docs/ui-rules.md § Mouse, overlays, and
// async UI (reasoning in ARCHITECTURE.md § The mouseDragging idiom): a press
// claims the gesture until its release (dragZone), and a release is forwarded
// to every latch-bearing child wherever the pointer ended up.
func (lv *LogViewer) HandleMouse(ev *tcell.EventMouse) bool {
	// The grid's value popup can be drawn over any part of the panel, so it gets
	// every event, including the release ending a drag inside it, before any
	// positional routing.
	if lv.grid.OverlayActive() {
		lv.setFilterFocused(false)
		return lv.grid.HandleMouse(ev)
	}
	mx, my := ev.Position()

	// Release: forwarded to the splitter, filter field and grid wherever the
	// pointer is, so a drag ending outside the panel still clears their latches.
	// Otherwise the grid treats the next click as a continuation of the last drag.
	if ev.Buttons() == tcell.ButtonNone {
		handled := false
		if lv.splitter.HandleMouse(ev) {
			lv.layoutChildren()
			handled = true
		}
		if lv.filter.HandleMouse(ev) {
			handled = true
		}
		if lv.grid.HandleMouse(ev) {
			handled = true
		}
		lv.dragZone = lZoneNone
		return handled
	}
	// Everything from the press that armed dragZone to its release belongs to the
	// sub-region that claimed it, wherever the pointer drifted, hence this
	// outranks the bounds check. A wheel tick mid-gesture is swallowed, not routed.
	if lv.dragZone != lZoneNone {
		if ev.Buttons() == tcell.Button1 {
			return lv.routeDrag(ev)
		}
		return true
	}
	if mx < lv.rect.X || mx >= lv.rect.X+lv.rect.W {
		return false
	}
	// The wheel over the details pane scrolls it; over the grid it goes to the
	// grid, below.
	if lv.detailRect.Contains(mx, my) {
		switch ev.Buttons() {
		case tcell.WheelDown:
			lv.scrollDetails(1)
			return true
		case tcell.WheelUp:
			lv.scrollDetails(-1)
			return true
		}
	}
	if lv.splitter.HandleMouse(ev) {
		lv.layoutChildren()
		lv.armDrag(ev, lZoneSplitter)
		return true
	}
	if lv.toolRect.H == 1 && my == lv.toolRect.Y && ev.Buttons() == tcell.Button1 {
		lv.armDrag(ev, lZoneToolbar)
		if lv.filterVisible() && lv.filter.HitTest(mx, my) {
			lv.setFilterFocused(true)
			lv.filter.HandleMouse(ev)
			lv.dragZone = lZoneFilter
			return true
		}
		// The action runs on the press, so tcell's repeats while the button is down
		// must not reach it again (lZoneToolbar swallows them in routeDrag). runTool
		// applies the gate drawToolbar dims on, so a dimmed cell is inert, not merely
		// grey.
		if i := lv.tools.CellAt(mx, my); i >= 0 {
			lv.runTool(i)
			return true
		}
		if lv.tools.More.Rect.Contains(mx, my) {
			lv.showOverflowMenu()
			return true
		}
		return true
	}
	if ev.Buttons() == tcell.Button1 || ev.Buttons() == tcell.Button2 {
		beforeRow := lv.grid.SelectedRow()
		if lv.grid.HandleMouse(ev) {
			lv.armDrag(ev, lZoneGrid)
			lv.setFilterFocused(false)
			if lv.grid.SelectedRow() != beforeRow {
				lv.detailScroll = 0
			}
			return true
		}
		lv.armDrag(ev, lZoneUnclaimed)
		return false
	}
	return lv.grid.HandleMouse(ev)
}

// armDrag records that zone consumed a Button1 press, so further events until
// the release go back to it (dragZone).
func (lv *LogViewer) armDrag(ev *tcell.EventMouse, zone logDragZone) {
	if ev.Buttons() == tcell.Button1 {
		lv.dragZone = zone
	}
}

// routeDrag delivers a held-Button1 event to the sub-region that armed the
// gesture. lZoneToolbar and lZoneUnclaimed swallow it: the action already ran
// on the press, and owning the gesture only keeps other regions from seeing
// the repeats.
func (lv *LogViewer) routeDrag(ev *tcell.EventMouse) bool {
	switch lv.dragZone {
	case lZoneSplitter:
		if lv.splitter.HandleMouse(ev) {
			lv.layoutChildren()
		}
	case lZoneGrid:
		lv.grid.HandleMouse(ev)
	case lZoneFilter:
		lv.filter.HandleMouse(ev)
	}
	return true
}
