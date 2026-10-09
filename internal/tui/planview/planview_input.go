package planview

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
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

// routeToContent forwards ev to the active tab (XML editor, Tree, or Plan);
// shared by HandleMouse's release, latched and default branches.
func (v *PlanView) routeToContent(ev *tcell.EventMouse) bool {
	switch {
	case v.activeTab == TabXML:
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
	// An open summary popup outranks the tab row, statement bar and content
	// area: it's centred on the whole screen, so its coordinates land inside
	// all of them.
	//
	// Releases included. Routing one by position would never reach the grid
	// (the popup sits nowhere near the summary strip), and DataGrid hands a
	// release to the popup's editor, whose HandleMouse clears mouseDragging
	// wherever it landed. Withholding it strands that latch and the next press
	// reads as more of the same drag. PlanView's own latch comes down here too.
	if v.summaryOverlayActive() {
		if ev.Buttons() == tcell.ButtonNone {
			v.mouseDragging = false
		}
		return v.handleSummaryMouse(ev)
	}
	// Always forward releases to the XML editor, so a text-selection drag ends
	// cleanly even if the cursor left this control (as QueryPanel.HandleMouse).
	if ev.Buttons() == tcell.ButtonNone {
		v.mouseDragging = false
		return v.routeToContent(ev)
	}
	if !v.rect.Contains(mx, my) {
		return false
	}
	if v.tabRect.H == 1 && my == v.tabRect.Y && ev.Buttons() == tcell.Button1 {
		// A drag started in the content area (e.g. XML selection) resends
		// Button1 on every motion; if it drifts into the tab row,
		// mouseDragging is already true, so forward to the content handler
		// rather than misfire a tab switch/Expand/statement step.
		if v.mouseDragging {
			return v.routeToContent(ev)
		}
		v.mouseDragging = true
		if v.expandBtnRect.W > 0 && v.expandBtnRect.Contains(mx, my) {
			if v.OnExpand != nil {
				v.OnExpand()
			}
			return true
		}
		if i := v.tabAt(mx); i >= 0 {
			v.setActiveTab(Tab(i))
		}
		return true
	}
	if v.bannerRect.H == 1 && my == v.bannerRect.Y {
		if ev.Buttons() != tcell.Button1 {
			return true
		}
		if v.mouseDragging {
			return v.routeToContent(ev)
		}
		v.mouseDragging = true
		v.openMissingIndexDetails()
		return true
	}
	if v.stmtRect.H == 1 && my == v.stmtRect.Y && ev.Buttons() == tcell.Button1 {
		if v.mouseDragging {
			return v.routeToContent(ev)
		}
		v.mouseDragging = true
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
	if ev.Buttons() == tcell.Button1 {
		v.mouseDragging = true
	}
	return v.routeToContent(ev)
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
