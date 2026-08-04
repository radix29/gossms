package tui

import "github.com/gdamore/tcell/v3"

// HandleKey routes keys to result tab switching (Ctrl+PgUp/PgDn), F5
// execute, or whichever of the editor/results grid last got a mouse click
// (see resultsFocused). The splitter's Ctrl+Up/Down resize only gets first
// refusal while the results grid holds focus — while the editor holds it,
// Ctrl+arrows must reach the editor's own word-jump/line-select bindings
// instead of being swallowed for resize on every keystroke.
func (p *QueryPanel) HandleKey(ev *tcell.EventKey) bool {
	// The results grid's context menu or "Show Value" popup, if open, must
	// get every key unconditionally — both are centred on the whole screen
	// (see controls.DataGrid.DrawOverlay), independent of resultsFocused, so
	// without this check their keys (Shift+arrows, Ctrl+A, Escape) would
	// fall through to the editor instead whenever the editor holds focus.
	if !p.onMessagesTab() && !p.textTabActive() && !p.planTabActive() && p.results.OverlayActive() {
		return p.results.HandleKey(ev)
	}
	// Same rule for the execution plan's Operator Summary grid, which owns
	// the identical pair of popups one level down (see PlanView.OverlayActive).
	if p.planTabActive() && p.planView.OverlayActive() {
		return p.planView.HandleKey(ev)
	}
	// Same reasoning, for the SQL editor's own completion popup: it floats
	// over the editor's rect independently of resultsFocused, so it must
	// get every key before F5/Ctrl+PgUp/splitter routing below gets a
	// chance to misroute one meant for it.
	if p.editor.CompletionActive() {
		return p.editor.HandleKey(ev)
	}
	if ev.Key() == tcell.KeyF5 {
		p.Execute()
		return true
	}
	// Ctrl+R reloads this panel's autocomplete inventory — only while the
	// editor holds focus, matching Ctrl+Up/Down's identical resultsFocused
	// gating just below, so it doesn't collide with a future results-grid
	// binding on the same key.
	if ev.Key() == tcell.KeyCtrlR && !p.resultsFocused {
		p.refreshCompletionCache()
		return true
	}
	// Ctrl+Enter selects the T-SQL statement at the cursor (see
	// controls.Editor.SelectStatementAtCursor) — the first step toward
	// "execute current statement"; this only selects, it doesn't run it.
	//
	// Two encodings reach us, and both have to be accepted. A terminal with
	// a modern keyboard protocol reports it as Enter with Ctrl held. Every
	// other terminal — xfce4-terminal and the rest of the VTE family
	// included — sends a bare LF (0x0A) instead, which tcell decodes as
	// KeyCtrlJ, since plain Enter is CR (0x0D). Only the first was handled,
	// so on an ordinary terminal the key did nothing at all: KeyCtrlJ isn't
	// KeyEnter, so the editor below didn't want it either. Menu > Query >
	// Execute at Cursor kept working throughout, which is the tell that the
	// action was fine and only the binding was missing.
	if isCtrlEnter(ev) {
		p.editor.SelectStatementAtCursor()
		return true
	}
	// Ctrl+PgUp/PgDn cycle the result tabs. Like Ctrl+Tab (see app_events),
	// the Ctrl modifier on these keys is only reported by terminals with a
	// modern keyboard protocol; elsewhere they stay plain PgUp/PgDn and
	// fall through to the editor.
	if (p.result != nil || p.planView != nil) && ev.Modifiers()&tcell.ModCtrl != 0 {
		switch ev.Key() {
		case tcell.KeyPgUp:
			p.setActiveTab(p.activeTab - 1)
			return true
		case tcell.KeyPgDn:
			p.setActiveTab(p.activeTab + 1)
			return true
		}
	}
	if !p.resultsFocused {
		return p.editor.HandleKey(ev)
	}
	if p.splitter.HandleKey(ev) {
		p.layoutChildren()
		return true
	}
	switch {
	case p.onMessagesTab():
		if p.messages.HandleKey(ev) {
			return true
		}
	case p.planTabActive():
		if p.planView.HandleKey(ev) {
			return true
		}
	case p.textTabActive():
		if p.resultsText.HandleKey(ev) {
			return true
		}
	default:
		if p.results.HandleKey(ev) {
			return true
		}
	}
	if ev.Key() == tcell.KeyEscape {
		p.setResultsFocused(false)
		return true
	}
	return false
}

// setResultsFocused switches keyboard focus between the editor and results
// grid, updating both sub-regions' visual focus state to match (see
// syncFocusVisuals).
func (p *QueryPanel) setResultsFocused(v bool) {
	p.resultsFocused = v
	p.syncFocusVisuals()
}

// HandleMouse routes mouse events to the splitter (drag), result tabs,
// editor, results grid, or results-text view.
func (p *QueryPanel) HandleMouse(ev *tcell.EventMouse) bool {
	// Same reasoning as the OverlayActive check at the top of HandleKey:
	// the popup/menu can visually overlap the editor's rect, so it must get
	// every mouse event — including the drag-release that follows a
	// click-drag selection inside it — before any position-based routing
	// below gets a chance to hand the click to the editor instead.
	if !p.onMessagesTab() && !p.textTabActive() && !p.planTabActive() && p.results.OverlayActive() {
		p.setResultsFocused(true)
		return p.results.HandleMouse(ev)
	}
	// Same rule for the execution plan's Operator Summary grid. PlanView's
	// own HandleMouse lets releases through to its inner branches, so the
	// forwarding below still clears any latch this doesn't reach.
	if p.planTabActive() && p.planView.OverlayActive() {
		p.setResultsFocused(true)
		return p.planView.HandleMouse(ev)
	}
	// Same reasoning, for the SQL editor's own completion popup.
	if p.editor.CompletionActive() {
		return p.editor.HandleMouse(ev)
	}
	mx, my := ev.Position()
	// Always forward release events — regardless of position — to the
	// splitter, the query editor, the messages view, the results-text view,
	// the execution plan view, and the results grid, so an in-progress
	// splitter drag, text-selection drag, or cell-block selection drag
	// terminates cleanly even if the cursor has moved outside this panel's
	// column (or out of whichever of those widgets started the drag) before
	// the button was released. Without forwarding to results too, its own
	// drag-tracking flag never resets, so every click after the very first
	// one in the grid's lifetime gets mistaken for a continued drag from
	// that first click's anchor instead of a fresh single-cell selection.
	if ev.Buttons() == tcell.ButtonNone {
		handled := false
		if p.splitter.HandleMouse(ev) {
			p.layoutChildren()
			handled = true
		}
		if p.editor.HandleMouse(ev) {
			handled = true
		}
		if p.messages.HandleMouse(ev) {
			handled = true
		}
		if p.resultsText.HandleMouse(ev) {
			handled = true
		}
		if p.results.HandleMouse(ev) {
			handled = true
		}
		if p.planView != nil && p.planView.HandleMouse(ev) {
			handled = true
		}
		p.dragZone = qZoneNone
		return handled
	}
	// Everything from the press that armed dragZone through to its release
	// belongs to the sub-region that claimed it, wherever the pointer has
	// drifted to since — including out of the panel's own columns, which is
	// why this outranks the bounds check. See the field's doc comment.
	//
	// A wheel tick arriving mid-gesture is swallowed rather than routed, same
	// rule as App.handleMouse's gestureOwner and PropertySheet's dragZone: it
	// isn't part of the gesture, and the positional routing below would hand
	// it to whichever sub-region the pointer has drifted over. App's own
	// gestureOwner (armed as ownerPanels for any press in this column) happens
	// to swallow it one level up today, so this is belt-and-braces — but the
	// invariant belongs to whoever owns the gesture, not to a caller that
	// might stop arming one.
	if p.dragZone != qZoneNone {
		if ev.Buttons() == tcell.Button1 {
			return p.routeDrag(ev)
		}
		return true
	}
	if mx < p.rect.X || mx >= p.rect.X+p.rect.W {
		return false
	}
	if p.splitter.HandleMouse(ev) {
		p.layoutChildren()
		p.armDrag(ev, qZoneSplitter)
		return true
	}
	if p.tabRect.H == 1 && my == p.tabRect.Y && ev.Buttons() == tcell.Button1 {
		p.armDrag(ev, qZoneTabs)
		if i := p.tabAt(mx); i >= 0 {
			p.setActiveTab(i)
		}
		return true
	}
	// A left- or right-click decides which sub-region owns keyboard focus
	// from now on (see resultsFocused) — matches ordinary GUI
	// click-to-focus, and is the only way focus moves into the results
	// grid (Escape is the way back out, see HandleKey).
	if ev.Buttons() == tcell.Button1 || ev.Buttons() == tcell.Button2 {
		if p.editor.HandleMouse(ev) {
			p.armDrag(ev, qZoneEditor)
			p.setResultsFocused(false)
			return true
		}
		if p.resultsHandleMouse(ev) {
			p.armDrag(ev, qZoneResults)
			p.setResultsFocused(true)
			return true
		}
		p.armDrag(ev, qZoneUnclaimed)
		return false
	}
	if p.editor.HandleMouse(ev) {
		return true
	}
	return p.resultsHandleMouse(ev)
}

// isCtrlEnter reports whether ev is Ctrl+Enter, in either encoding a
// terminal can deliver it as. A modern keyboard protocol (kitty, xterm's
// modifyOtherKeys) reports Enter with ModCtrl set; everything else sends a
// bare LF, 0x0A, which tcell decodes as KeyCtrlJ — plain Enter being CR,
// 0x0D. Nothing else in the app binds Ctrl+J, so accepting it costs
// nothing. See its use in HandleKey.
func isCtrlEnter(ev *tcell.EventKey) bool {
	if ev.Key() == tcell.KeyCtrlJ {
		return true
	}
	return ev.Key() == tcell.KeyEnter && ev.Modifiers()&tcell.ModCtrl != 0
}

// resultsHandleMouse dispatches to whichever of the four widgets sharing
// the results rect is currently shown — see layoutChildren.
func (p *QueryPanel) resultsHandleMouse(ev *tcell.EventMouse) bool {
	switch {
	case p.onMessagesTab():
		return p.messages.HandleMouse(ev)
	case p.planTabActive():
		return p.planView.HandleMouse(ev)
	case p.textTabActive():
		return p.resultsText.HandleMouse(ev)
	default:
		return p.results.HandleMouse(ev)
	}
}

// armDrag records that zone consumed a Button1 press, so every further
// event until the release goes back to it — see the dragZone field.
func (p *QueryPanel) armDrag(ev *tcell.EventMouse, zone queryDragZone) {
	if ev.Buttons() == tcell.Button1 {
		p.dragZone = zone
	}
}

// routeDrag delivers a held-Button1 event to the sub-region that armed the
// gesture. qZoneTabs and qZoneUnclaimed swallow it: the tab switch already
// happened on the press, and there is nothing further to deliver — the
// point is only that no other sub-region sees the repeats.
func (p *QueryPanel) routeDrag(ev *tcell.EventMouse) bool {
	switch p.dragZone {
	case qZoneSplitter:
		if p.splitter.HandleMouse(ev) {
			p.layoutChildren()
		}
	case qZoneEditor:
		p.editor.HandleMouse(ev)
	case qZoneResults:
		p.resultsHandleMouse(ev)
	}
	return true
}
