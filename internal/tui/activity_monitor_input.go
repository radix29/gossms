package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/core"
)

// amDragZone is the sub-region that claimed the held Button1 press. tcell
// resends Button1 on every motion while held, so without an owner a twitching
// click re-fires its tab, and a scrollbar drag crossing the tab row switches
// tabs. Mirrors QueryPanel.dragZone; released on ButtonNone.
type amDragZone int

const (
	amZoneTabs amDragZone = iota
	amZoneTools
	amZoneBars
	amZonePlot
	amZoneProcGrid
)

// hScrollStep is one horizontal scroll step; one column is too slow across a
// 150-column canvas.
const hScrollStep = 4

// pageStep is the PgUp/PgDn step when the viewport height is unknown.
const pageStep = 10

// HandleKey routes keys for the active tab. Unhandled keys return false,
// including a scroll key at its boundary, so the keyboard can always leave.
func (am *ActivityMonitor) HandleKey(ev *tcell.EventKey) bool {
	// A procedure tab's grid gets keys before the panel's tab switching and
	// scrolling. An open grid overlay (context menu, value viewer) gets first
	// refusal of everything, Tab included, so Tab can't switch tabs under a
	// popup.
	if pt := am.procTab(); pt != nil && pt.grid != nil {
		if pt.grid.OverlayActive() {
			return pt.grid.HandleKey(ev)
		}
		switch ev.Key() {
		case tcell.KeyTab, tcell.KeyBacktab:
			// The panel's own tab cycling.
		default:
			if pt.grid.HandleKey(ev) {
				return true
			}
		}
	}
	switch ev.Key() {
	case tcell.KeyTab:
		// Plain Tab only: App.handleKey takes Ctrl+Tab first.
		if ev.Modifiers() != 0 {
			return false
		}
		am.stepTab(1)
		return true
	case tcell.KeyBacktab:
		if ev.Modifiers()&tcell.ModCtrl != 0 {
			return false
		}
		am.stepTab(-1)
		return true
	case tcell.KeyDown:
		return am.scrollBy(0, 1)
	case tcell.KeyUp:
		return am.scrollBy(0, -1)
	case tcell.KeyRight:
		return am.scrollBy(hScrollStep, 0)
	case tcell.KeyLeft:
		return am.scrollBy(-hScrollStep, 0)
	case tcell.KeyPgDn:
		return am.scrollBy(0, am.pageHeight())
	case tcell.KeyPgUp:
		return am.scrollBy(0, -am.pageHeight())
	case tcell.KeyHome:
		return am.scrollTo(0, 0)
	case tcell.KeyEnd:
		maxX, maxY := am.scrollLimits()
		return am.scrollTo(maxX, maxY)
	case tcell.KeyRune:
		if ev.Modifiers()&tcell.ModCtrl != 0 || ev.Modifiers()&tcell.ModAlt != 0 {
			return false
		}
		return am.handleRune(core.EvRune(ev))
	}
	return false
}

// pageHeight is one PgUp/PgDn step: a viewport less one row of overlap.
func (am *ActivityMonitor) pageHeight() int {
	if am.viewRect.H <= 1 {
		return pageStep
	}
	return am.viewRect.H - 1
}

// handleRune runs letter shortcuts gated by tab: Pause/Continue and rate on
// dashboards, Refresh and the option switches on procedure tabs.
func (am *ActivityMonitor) handleRune(r rune) bool {
	if pt := am.procTab(); pt != nil {
		if i := pt.optionByKey(r); i >= 0 {
			pt.toggleOption(i)
			return true
		}
	}
	switch r {
	case 'p', 'P':
		if !am.tab.canvasTab() {
			return false
		}
		am.setPaused(!am.feed().paused)
		return true
	case 'r', 'R':
		pt := am.procTab()
		if pt == nil {
			return false
		}
		pt.refresh()
		return true
	case '+', '=':
		// Faster: a shorter interval, earlier in the list.
		return am.tab.canvasTab() && am.setRate(am.feed().rateIdx-1)
	case '-', '_':
		return am.tab.canvasTab() && am.setRate(am.feed().rateIdx+1)
	}
	return false
}

// HandleMouse routes clicks, wheel scrolling, and scrollbar drags.
func (am *ActivityMonitor) HandleMouse(ev *tcell.EventMouse) bool {
	if ev.Buttons() == tcell.ButtonNone {
		// The release ends the gesture wherever the pointer is, even outside
		// the panel (App forwards ButtonNone for this). Each grid has its own
		// drag latch, so both see the release even if hidden or uninvolved.
		for _, pt := range []*amProcTab{am.blk, am.sess} {
			if pt.grid != nil {
				pt.grid.HandleMouse(ev)
			}
		}
		am.dragZone.Release()
		for i := range am.vp {
			am.vp[i].Release()
		}
		return false
	}

	pt := am.procTab()

	// An open grid overlay covers the whole panel, so it takes the click first.
	if pt != nil && pt.grid != nil && pt.grid.OverlayActive() {
		return pt.grid.HandleMouse(ev)
	}

	// Every button goes to the owner, the wheel included, not only Button1 as
	// core.Gesture.Route would: a grid gesture hands the grid its wheel ticks.
	if zone, held := am.dragZone.Owner(); held {
		return am.routeDrag(ev, zone)
	}

	// The grid owns everything below the toolbar for every button (right-click
	// "Show Value", block-selection drag). The credit row isn't the grid's, so
	// hit-test the grid's own rect.
	if pt != nil && pt.grid != nil {
		if mx, my := ev.Position(); pt.gridRect.Contains(mx, my) {
			am.dragZone.Arm(ev, amZoneProcGrid)
			return pt.grid.HandleMouse(ev)
		}
	}

	if _, _, ok := core.WheelDelta(ev, hScrollStep, 1); ok {
		return am.wheel(ev)
	}
	if ev.Buttons() == tcell.Button1 {
		return am.press(ev)
	}
	return false
}

// wheel scrolls the dashboard under the pointer (Shift or a horizontal wheel
// sideways), reporting whether it moved.
func (am *ActivityMonitor) wheel(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	if !am.viewRect.Contains(mx, my) {
		return false
	}
	_, moved := am.viewport().Wheel(ev, hScrollStep, 1)
	return am.panned(moved)
}

// press claims a fresh Button1 press for the sub-region it landed in and acts
// once. Every claiming branch arms dragZone so the rest of the gesture is
// routed, not re-hit-tested.
func (am *ActivityMonitor) press(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()

	if am.tabRect.Contains(mx, my) {
		am.dragZone.Arm(ev, amZoneTabs)
		am.tabBar().HandleMouse(ev)
		return true
	}
	if am.toolRect.Contains(mx, my) {
		am.dragZone.Arm(ev, amZoneTools)
		if i := am.tools.CellAt(mx, my); i >= 0 {
			am.tools.RunCell(i)
		} else if am.tools.More.Rect.Contains(mx, my) {
			am.tools.OverflowMenu(am.app.contextMenu, am.rect)
		}
		// Claimed either way, so a disabled control or gap swallows the press.
		return true
	}
	if am.tab.canvasTab() {
		if am.scrollbarDrag(ev) {
			return true
		}
		if am.viewRect.Contains(mx, my) {
			// Claimed so a drag wandering onto the tab row doesn't switch tabs.
			am.dragZone.Arm(ev, amZonePlot)
			// A showing tooltip is dismissed by the next click, which does
			// nothing else, so one click never closes one box and opens
			// another.
			if am.tooltip != nil {
				am.tooltip = nil
				return true
			}
			am.tooltip = am.pinTooltip(mx, my)
			return true
		}
	}
	return false
}

// routeDrag replays a held Button1 to the zone that claimed the press. Only
// scrollbars (and the grid) use the rest of a gesture; tab bar and toolbar fire
// once on the press.
func (am *ActivityMonitor) routeDrag(ev *tcell.EventMouse, zone amDragZone) bool {
	if zone == amZoneBars {
		return am.scrollbarDrag(ev)
	}
	// The grid extends a block selection, so it needs every held event.
	if zone == amZoneProcGrid {
		if pt := am.procTab(); pt != nil && pt.grid != nil {
			return pt.grid.HandleMouse(ev)
		}
	}
	return true
}

// scrollbarDrag hands the event to the scrollbars; core.Viewport's latch keeps
// control once the pointer leaves the bar.
//
// A move drops a pinned tooltip as wheel and keys do. Returns true for any
// event the bars took, moved or not: the gesture belongs to the bar.
func (am *ActivityMonitor) scrollbarDrag(ev *tcell.EventMouse) bool {
	vp := am.viewport()
	x, y := vp.X, vp.Y
	if !vp.HandleScrollbars(ev) {
		return false
	}
	am.dragZone.Arm(ev, amZoneBars)
	am.panned(vp.X != x || vp.Y != y)
	return true
}
