package planview

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// HandleKey switches tabs (1/2/3), pages the statement selector ([/]), or
// forwards to the XML editor when it's the active tab. Returns false for
// anything else so the host can route focus-navigation keys elsewhere.
func (v *PlanView) HandleKey(ev *tcell.EventKey) bool {
	// An open summary popup outranks everything below, search included:
	// its keys would otherwise be read as tab digits ('1'/'2'/'3'), sort
	// keys, or search input, and there'd be no way to dismiss it.
	if v.summaryOverlayActive() {
		return v.handleSummaryOverlayKey(ev)
	}
	// Search must get first refusal of every key while active (or while
	// idle but eligible, for '/', 'n', 'N', 'w', 'p') — otherwise a typed
	// digit like '1' would switch tabs instead of extending the query.
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

// routeToContent forwards ev to whichever tab is currently active (XML
// editor, Tree, or Plan/graph) — shared by HandleMouse's release branch,
// its already-latched tab/stmt branches, and its own default case.
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
	// An open summary popup outranks the tab row, the statement bar, and
	// the content area alike — it's centred on the whole screen, so its
	// coordinates land inside all of them.
	//
	// Releases included. Routing one by position instead would never reach
	// the grid — the popup sits nowhere near the summary strip the position
	// branches gate on — and DataGrid hands a release to the popup's editor,
	// whose HandleMouse clears mouseDragging regardless of where the release
	// landed, precisely so a drag terminates cleanly. Withholding it strands
	// that latch, and the next press is read as more of the same drag.
	// PlanView's own latch comes down here too: it's the same gesture.
	if v.summaryOverlayActive() {
		if ev.Buttons() == tcell.ButtonNone {
			v.mouseDragging = false
		}
		return v.handleSummaryMouse(ev)
	}
	// Always forward release events to the XML editor, regardless of
	// position, so an in-progress text-selection drag terminates cleanly
	// even if the cursor has moved outside this control before release —
	// same reasoning as QueryPanel.HandleMouse.
	if ev.Buttons() == tcell.ButtonNone {
		v.mouseDragging = false
		return v.routeToContent(ev)
	}
	if !v.rect.Contains(mx, my) {
		return false
	}
	if v.tabRect.H == 1 && my == v.tabRect.Y && ev.Buttons() == tcell.Button1 {
		// A drag that started in the content area (e.g. an XML text
		// selection) resends Button1 on every motion event while held —
		// if the cursor drifts up into the tab row mid-drag, mouseDragging
		// is already true from that press, so forward to the content
		// handler instead of misfiring a tab switch/Expand/statement step.
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
	if ev.Buttons() == tcell.Button1 {
		v.mouseDragging = true
	}
	return v.routeToContent(ev)
}
