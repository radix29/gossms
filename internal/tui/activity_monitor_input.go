package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// amDragZone is the sub-region that claimed the Button1 press currently
// being held. tcell resends Button1 on every motion event while the button
// is down, so without an owner a click on a tab that so much as twitches
// re-fires that tab on every motion event, and a drag started on a
// scrollbar keeps switching tabs when it crosses the tab row. Mirrors
// QueryPanel.dragZone; cleared on the release.
type amDragZone int

const (
	amZoneNone amDragZone = iota
	amZoneTabs
	amZoneTools
	amZoneVBar
	amZoneHBar
	amZonePlot
)

// hScrollStep is how far one horizontal scroll key moves the viewport —
// a single column is uselessly slow across a 150-column canvas.
const hScrollStep = 4

// pageStep is how far PgUp/PgDn moves when the viewport height is unknown
// or degenerate.
const pageStep = 10

// HandleKey routes keys for the active tab. Everything not acted on comes
// back false, including a scroll key already at its boundary — this panel
// must never become a place the keyboard can't leave.
func (am *ActivityMonitor) HandleKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyTab:
		// Plain Tab only: App.handleKey routes Ctrl+Tab to its own focus
		// cycle before the panel ever sees it.
		if ev.Modifiers() != 0 {
			return false
		}
		am.setTab((am.tab + 1) % amTabCount)
		return true
	case tcell.KeyBacktab:
		if ev.Modifiers()&tcell.ModCtrl != 0 {
			return false
		}
		am.setTab((am.tab + amTabCount - 1) % amTabCount)
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

// pageHeight is one PgUp/PgDn step: a viewport's worth, less one row of
// overlap so the reader keeps a landmark across the jump.
func (am *ActivityMonitor) pageHeight() int {
	if am.viewRect.H <= 1 {
		return pageStep
	}
	return am.viewRect.H - 1
}

// handleRune runs the panel's letter shortcuts, each gated on the tab it
// applies to: Pause/Continue and the rate selector belong to the
// dashboards, Refresh to the placeholders.
func (am *ActivityMonitor) handleRune(r rune) bool {
	switch r {
	case 'p', 'P':
		switch {
		case am.tab == amTabTempDB:
			am.setTempDBPaused(!am.tdPaused)
		case am.tab.dashboardTab():
			am.setPaused(!am.paused)
		default:
			return false
		}
		return true
	case 'r', 'R':
		if am.tab.canvasTab() {
			return false
		}
		am.refreshStub()
		return true
	case '+', '=':
		// Faster: a shorter interval, i.e. earlier in the rate list.
		if am.tab == amTabTempDB {
			return am.setTempDBRate(am.tdRateIdx - 1)
		}
		return am.tab.dashboardTab() && am.setRate(am.rateIdx-1)
	case '-', '_':
		if am.tab == amTabTempDB {
			return am.setTempDBRate(am.tdRateIdx + 1)
		}
		return am.tab.dashboardTab() && am.setRate(am.rateIdx+1)
	}
	return false
}

// HandleMouse routes clicks, wheel scrolling, and scrollbar drags.
func (am *ActivityMonitor) HandleMouse(ev *tcell.EventMouse) bool {
	if ev.Buttons() == tcell.ButtonNone {
		// The release ends the gesture wherever the pointer happens to be —
		// including outside the panel, which is why App forwards ButtonNone
		// even to a panel it would otherwise skip.
		am.dragZone = amZoneNone
		am.vDragging = false
		am.hDragging = false
		return false
	}

	if am.dragZone != amZoneNone {
		return am.routeDrag(ev)
	}

	switch ev.Buttons() {
	case tcell.WheelUp:
		return am.wheel(ev, 0, -1)
	case tcell.WheelDown:
		return am.wheel(ev, 0, 1)
	case tcell.WheelLeft:
		return am.wheel(ev, -hScrollStep, 0)
	case tcell.WheelRight:
		return am.wheel(ev, hScrollStep, 0)
	case tcell.Button1:
		return am.press(ev)
	}
	return false
}

// wheel scrolls the dashboard under the pointer.
func (am *ActivityMonitor) wheel(ev *tcell.EventMouse, dx, dy int) bool {
	mx, my := ev.Position()
	if !am.viewRect.Contains(mx, my) {
		return false
	}
	return am.scrollBy(dx, dy)
}

// press claims a fresh Button1 press for whichever sub-region it landed in
// and acts on it once. Every branch that claims the press arms dragZone, so
// the rest of the gesture is routed rather than re-hit-tested.
func (am *ActivityMonitor) press(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()

	if am.tabRect.Contains(mx, my) {
		am.dragZone = amZoneTabs
		for i, seg := range am.tabSegments() {
			if mx >= seg[0].X && mx < seg[0].X+seg[0].W {
				am.setTab(amTab(i))
				return true
			}
		}
		return true
	}
	if am.toolRect.Contains(mx, my) {
		am.dragZone = amZoneTools
		for _, t := range am.tools {
			if !t.rect.IsZero() && t.rect.Contains(mx, my) {
				if t.action != nil {
					t.action()
				}
				return true
			}
		}
		return true
	}
	if am.tab.canvasTab() {
		if am.scrollbarDrag(ev) {
			return true
		}
		if am.viewRect.Contains(mx, my) {
			// Claimed so a drag that starts here and wanders onto the tab row
			// doesn't switch tabs partway through.
			am.dragZone = amZonePlot
			// A showing tooltip is dismissed by the next click wherever it
			// lands, so one click never both closes a box and opens another —
			// the user would see only the second and think the first never
			// closed.
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

// routeDrag replays a held Button1 to whichever zone claimed the press.
// Only the scrollbars do anything with the rest of a gesture; the tab bar
// and toolbar fired once on the press and stay quiet until the release,
// which is what stops a twitching click from re-firing a control.
func (am *ActivityMonitor) routeDrag(ev *tcell.EventMouse) bool {
	if am.dragZone == amZoneVBar || am.dragZone == amZoneHBar {
		return am.scrollbarDrag(ev)
	}
	return true
}

// scrollbarDrag hands the event to whichever scrollbar it belongs to. The
// latch inside core.HandleScrollbarDrag keeps the drag under control once
// the pointer wanders off the bar's own column or row.
//
// The new offset goes through scrollTo rather than into scrollX/scrollY
// directly: a drag moves the canvas under a pinned tooltip exactly as the
// wheel and the scrolling keys do, and only scrollTo drops the tooltip. Set
// here by hand, the box stays up naming a column it is no longer over. The
// return is true either way — the gesture belongs to the bar whether or not
// the offset it computed differs from the current one.
func (am *ActivityMonitor) scrollbarDrag(ev *tcell.EventMouse) bool {
	cw, ch := am.canvasSize()
	sy := am.scrollY[am.tab]
	if core.HandleScrollbarDrag(ev, am.viewRect.Right(), am.viewRect.Y, am.viewRect.H, ch, &am.vDragging, &sy) {
		am.dragZone = amZoneVBar
		am.scrollTo(am.scrollX[am.tab], sy)
		return true
	}
	sx := am.scrollX[am.tab]
	if core.HandleScrollbarDragH(ev, am.viewRect.X, am.viewRect.Bottom(), am.viewRect.W, cw, &am.hDragging, &sx) {
		am.dragZone = amZoneHBar
		am.scrollTo(sx, am.scrollY[am.tab])
		return true
	}
	return false
}
