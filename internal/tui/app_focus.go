package tui

import (
	"github.com/radix29/tuikit/layout"
)

// appFocus names which half of the window has the keyboard: Object Explorer or
// the panel area.
type appFocus int

const (
	focusOnExplorer appFocus = iota
	focusOnPanels
)

func (f appFocus) String() string {
	if f == focusOnPanels {
		return "panels"
	}
	return "explorer"
}

func (a *App) focusExplorer() {
	a.showExplorer()
	a.focus = focusOnExplorer
	a.explorer.Focus(true)
	a.syncActivePanelFocus()
}

// showExplorer un-hides Object Explorer without moving focus: a connect
// reveals the tree it adds to (addServerRoot), as SSMS does, but must not pull
// the keyboard off the editor.
func (a *App) showExplorer() {
	if a.explorerHidden() {
		a.explorerSplit.SetCollapsed(layout.CollapseNone)
		a.layoutAll()
	}
}

// hideExplorer collapses the explorer's side of the split (the [x] on its
// border). The splitter keeps its ratio for showExplorer, and while collapsed
// takes no key or mouse event and gives the panels the whole width.
func (a *App) hideExplorer() {
	a.explorerSplit.SetCollapsed(layout.CollapseFirst)
	a.drag.Cancel()
	a.focusPanels()
	a.layoutAll()
}

// explorerHidden reports whether the [x] hid Object Explorer. The nil check
// is for the test fixtures that build an App with no splitter.
func (a *App) explorerHidden() bool {
	return a.explorerSplit != nil && a.explorerSplit.Collapsed() == layout.CollapseFirst
}

func (a *App) focusPanels() {
	a.focus = focusOnPanels
	a.explorer.Focus(false)
	a.syncActivePanelFocus()
}

// syncActivePanelFocus keeps the visible panel's Activatable state (title bar
// highlight, cursor visibility) in sync with a.focus. PanelManager knows
// nothing about a.focus and calls SetActive only when its own active index
// changes, so anything that changes the active panel while a.focus stays
// focusOnExplorer (nextPanel/prevPanel) must call this, or the new panel shows as
// focused while Object Explorer holds real keyboard focus.
func (a *App) syncActivePanelFocus() {
	if p, ok := a.panels.ActivePanel().(layout.Activatable); ok {
		p.SetActive(a.focus == focusOnPanels)
	}
}

// cycleFocus advances keyboard focus one step (Ctrl+Tab): Object Explorer ->
// the active query panel's editor -> its results pane -> Object Explorer. A
// non-query panel, or a query panel with no results yet, offers no middle stop
// and degrades to a two-way explorer/panels toggle.
func (a *App) cycleFocus() {
	qp := a.activeQueryPanel()
	switch {
	case a.focus == focusOnExplorer:
		a.focusPanels()
		if qp != nil {
			qp.setResultsFocused(false)
		}
	case qp != nil && !qp.resultsFocused && qp.result != nil:
		qp.setResultsFocused(true)
	default:
		a.cycleToExplorer(qp, false)
	}
}

// cycleFocusReverse is cycleFocus backwards (Ctrl+Shift+Tab): Object Explorer
// -> results pane -> editor -> Object Explorer. Degrades the same way.
func (a *App) cycleFocusReverse() {
	qp := a.activeQueryPanel()
	switch {
	case a.focus == focusOnExplorer:
		a.focusPanels()
		if qp != nil {
			qp.setResultsFocused(qp.result != nil)
		}
	case qp != nil && qp.resultsFocused:
		qp.setResultsFocused(false)
	default:
		a.cycleToExplorer(qp, true)
	}
}

// cycleToExplorer is the focus cycles' step onto Object Explorer. A hidden
// explorer is skipped: the cycle wraps to the panels' first stop, the editor
// going forward, the results pane (when there are any) in reverse.
func (a *App) cycleToExplorer(qp *QueryPanel, reverse bool) {
	if !a.explorerHidden() {
		a.focusExplorer()
		return
	}
	a.focusPanels()
	if qp != nil {
		qp.setResultsFocused(reverse && qp.result != nil)
	}
}

// nextPanel and prevPanel run the tab-bar's Next/Prev panel action
// (Ctrl+Shift+Right/Left and the View menu), wrapping PanelManager.Next/Prev
// and re-syncing focus visuals, since they can fire while a.focus ==
// focusOnExplorer.
func (a *App) nextPanel() {
	a.panels.Next()
	a.syncActivePanelFocus()
}

func (a *App) prevPanel() {
	a.panels.Prev()
	a.syncActivePanelFocus()
}

// jumpToPanel switches to panel i, counted from the left (Ctrl+0..9; 0 is
// always Object Explorer Details). Out-of-range i is a silent no-op.
func (a *App) jumpToPanel(i int) {
	a.panels.SetActiveIndex(i)
	a.syncActivePanelFocus()
}
