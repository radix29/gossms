package planview

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/tuikit/core"
)

// HandleKey switches tabs (1/2/3), pages the statement selector ([/]), or
// forwards to the XML editor when it's the active tab. Returns false for
// anything else so the host can route focus-navigation keys elsewhere.
func (v *PlanView) HandleKey(ev *tcell.EventKey) bool {
	// An open summary popup outranks everything below, search included: its
	// keys would be read as tab digits, sort keys or search input, leaving no
	// way to dismiss it.
	if v.summaryOverlayActive() {
		return v.handleSummaryOverlayKey(ev)
	}
	// Search gets first refusal of every key while active (or idle but
	// eligible, for '/', 'n', 'N', 'w', 'p'), else a typed '1' would switch tabs.
	if v.handleSearchKey(ev) {
		return true
	}
	switch core.EvRune(ev) {
	case '1':
		v.setActiveTab(TabPlan)
		return true
	case '2':
		v.setActiveTab(TabTree)
		return true
	case '3':
		v.setActiveTab(TabXML)
		return true
	case '[':
		v.stepStatement(-1)
		return true
	case ']':
		v.stepStatement(1)
		return true
	case 'm':
		// Its own key, not Enter: Enter already toggles the Properties strip
		// in the Plan tab and collapses a subtree in the Tree tab.
		return v.openMissingIndexDetails()
	}
	switch {
	case v.activeTab == TabXML:
		return v.xml.HandleKey(ev)
	case v.activeTab == TabTree:
		return v.handleTreeTabKey(ev)
	default: // TabPlan
		return v.handleGraphTabKey(ev)
	}
}

// planZone names the part of the view that claimed a Button1 press; the
// gesture (PlanView.drag) routes every held resend back to it (routeDrag)
// instead of hit-testing it again. With all-motion tracking a held press
// resends Button1 on every motion, and positional routing read each as a new
// click: a press on a tile dragged onto a scrollbar grabbed the thumb, and one
// dragged across the graph selected every tile it passed.
type planZone int

const (
	pzNone       planZone = iota // an inert press: the repeats go nowhere
	pzChrome                     // tab row, banner, statement selector: act on the press only
	pzXML                        // the XML editor: a text selection drag
	pzGraphSplit                 // the Plan tab's canvas|Properties splitter
	pzGraphBar                   // the Plan tab's canvas scrollbars
	pzGraphTile                  // the Plan tab's canvas (a tile, or the space between)
	pzTreeSplit                  // the Tree tab's tree|details splitter
	pzTreeBar                    // the Tree tab's tree pane scrollbar
	pzTreeRow                    // a Tree tab row
	pzSummary                    // the Tree tab's operator summary grid: it extends its selection
)

// routeDrag delivers a held Button1 to the zone that claimed the press. Only
// the splitters, scrollbars, XML editor and summary grid use the rest of a
// gesture; the other zones act once on the press.
func (v *PlanView) routeDrag(ev *tcell.EventMouse, z planZone) {
	switch z {
	case pzXML:
		v.xml.HandleMouse(ev)
	case pzGraphSplit:
		if v.graphSplit.HandleMouse(ev) {
			v.layoutGraphTab()
		}
	case pzGraphBar:
		// The bar latched on the press keeps the drag (core.Viewport).
		v.graphView().HandleScrollbars(ev, false)
	case pzTreeSplit:
		if v.treeSplit.HandleMouse(ev) {
			v.layoutTree()
		}
	case pzTreeBar:
		v.dragTreeBar(ev)
	case pzSummary:
		v.handleSummaryMouse(ev)
	}
}

// routeToContent forwards ev to the active tab (XML editor, Tree, or Plan);
// shared by HandleMouse's release and fresh-press branches.
func (v *PlanView) routeToContent(ev *tcell.EventMouse) bool {
	switch {
	case v.activeTab == TabXML:
		v.drag.Arm(ev, pzXML)
		return v.xml.HandleMouse(ev)
	case v.activeTab == TabTree:
		return v.handleTreeTabMouse(ev)
	default: // TabPlan
		return v.handleGraphTabMouse(ev)
	}
}

// HandleMouse routes clicks to the tab bar, the "[ Expand ]" button, the
// statement selector's ◀/▶ arrows, or the XML editor.
func (v *PlanView) HandleMouse(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	// An open summary popup outranks the tab row, statement bar and content area:
	// it's centred on the whole screen, so its coordinates land inside all of them.
	//
	// Releases included. Routing one by position would never reach the grid (the
	// popup sits nowhere near the summary strip), and DataGrid hands a release to
	// the popup's editor, whose HandleMouse clears mouseDragging wherever it
	// landed. Withholding it strands that latch and the next press reads as more of
	// the same drag. PlanView's own latch comes down here too.
	if v.summaryOverlayActive() {
		if ev.Buttons() == tcell.ButtonNone {
			v.mouseDragging = false
			v.drag.Release()
		}
		return v.handleSummaryMouse(ev)
	}
	// Always forward releases to the content, so a text-selection drag, a
	// splitter or the summary grid's selection ends cleanly even if the
	// cursor left this control (as QueryPanel.HandleMouse).
	if ev.Buttons() == tcell.ButtonNone {
		v.mouseDragging = false
		v.drag.Release()
		return v.routeToContent(ev)
	}
	// Ahead of the bounds test: a held drag keeps its zone after leaving the
	// view, so a scrollbar thumb dragged past the end still reaches it.
	if v.drag.Route(ev, v.routeDrag) {
		return true
	}
	if !v.rect.Contains(mx, my) {
		return false
	}
	// From here on a Button1 is a fresh press: Route took every held one.
	if v.tabRect.H == 1 && my == v.tabRect.Y && ev.Buttons() == tcell.Button1 {
		v.drag.Arm(ev, pzChrome)
		if v.expandRect().Contains(mx, my) {
			v.OnExpand()
			return true
		}
		v.tabBar().HandleMouse(ev)
		return true
	}
	if v.bannerRect.H == 1 && my == v.bannerRect.Y {
		if ev.Buttons() != tcell.Button1 {
			return true
		}
		v.drag.Arm(ev, pzChrome)
		v.openMissingIndexDetails()
		return true
	}
	if v.stmtRect.H == 1 && my == v.stmtRect.Y && ev.Buttons() == tcell.Button1 {
		v.drag.Arm(ev, pzChrome)
		prev, next := v.arrowRects()
		switch {
		case prev.Contains(mx, my):
			v.stepStatement(-1)
		case next.Contains(mx, my):
			v.stepStatement(1)
		}
		return true
	}
	if ev.Buttons() == tcell.Button2 && v.OnContextMenu != nil && v.contextMenuAt(mx, my) {
		// Latched like Button1: a held right button resends Button2 on every
		// motion, each reopening the menu.
		if !v.mouseDragging {
			v.mouseDragging = true
			v.OnContextMenu(mx, my)
		}
		return true
	}
	handled := v.routeToContent(ev)
	// Every fresh press in the view is claimed, so one the tab's handler left
	// alone (a pane with nothing to click) still owns its repeats rather than
	// handing them to whatever the drag reaches.
	if _, held := v.drag.Owner(); !held {
		v.drag.Arm(ev, pzNone)
	}
	return handled
}

// contextMenuAt reports whether a right-click at (x, y) opens the host's plan
// menu: on the operators themselves, the graph canvas or the tree pane.
func (v *PlanView) contextMenuAt(x, y int) bool {
	switch v.activeTab {
	case TabPlan:
		return v.graphCanvasRect.Contains(x, y)
	case TabTree:
		return v.treePaneRect.Contains(x, y)
	}
	return false
}
