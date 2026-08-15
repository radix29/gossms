package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// handleKey processes keyboard events. Returns true to signal quit.
func (a *App) handleKey(ev *tcell.EventKey) (quit bool) {
	// Record every key while the diagnostics dialog is open, before anything
	// else consumes it — including the clipboard shortcuts and whatever closes
	// the dialog itself, so those appear in the log too.
	if a.keyDiagDialog.Visible() {
		a.keyDiagDialog.RecordKey(ev)
	}

	// Clipboard shortcuts are handled centrally, before any dialog can consume
	// the key and regardless of what has focus. SetClipboard/GetClipboard are
	// Screen methods, available only here in the application layer.
	switch ev.Key() {
	case tcell.KeyCtrlC:
		a.copySelection()
		return false
	case tcell.KeyCtrlX:
		a.cutSelection()
		return false
	case tcell.KeyCtrlV:
		a.pasteFromClipboard()
		return false
	}

	if top := a.topDialog(); top != nil {
		top.HandleKey(ev)
		return false
	}
	if a.contextMenu.Visible() {
		a.contextMenu.HandleKey(ev)
		return false
	}
	if a.menuBar.IsOpen() {
		a.menuBar.HandleKey(ev)
		return false
	}

	switch ev.Key() {
	case tcell.KeyF1:
		a.helpDialog.Show()
		return false
	case tcell.KeyF10:
		// Plain F10 activates the menu bar; reaching this case means it
		// wasn't already open, since the IsOpen() check above intercepts
		// F10-to-close. Shift+F10 is "open context menu" and falls through to
		// the focused explorer/panel below.
		if ev.Modifiers()&tcell.ModShift == 0 {
			a.menuBar.Open()
			return false
		}
	case tcell.KeyCtrlQ:
		// Only a quit that went through returns true — with unsaved panels
		// requestQuit prompts and quits from its callback, so the event loop
		// must keep running.
		return a.requestQuit()
	case tcell.KeyCtrlN:
		a.newQueryPanel()
		return false
	case tcell.KeyCtrlW:
		a.closeActivePanel()
		return false
	case tcell.KeyCtrlO:
		// tcell has no KeyCtrlShiftO: Ctrl+<letter> with Shift is only
		// distinguishable from plain Ctrl+<letter> on terminals with a modern
		// keyboard protocol (Kitty, some xterm modifyOtherKeys configs).
		// Elsewhere Ctrl+Shift+O falls through to Open rather than Connect.
		if ev.Modifiers()&tcell.ModShift != 0 {
			a.connectDialog.Show()
		} else {
			a.openQueryFile()
		}
		return false
	case tcell.KeyCtrlS:
		a.saveQuery(false)
		return false
	case tcell.KeyCtrlF:
		a.findDialog.ShowFind()
		return false
	case tcell.KeyF3:
		// Ctrl+F3 searches for the word under the caret; Shift+F3 steps
		// backwards. Both modifiers decorate a key that works plainly, so a
		// terminal that can't encode them still gets Find Next.
		switch {
		case ev.Modifiers()&tcell.ModCtrl != 0:
			a.findWordAtCursor()
		case ev.Modifiers()&tcell.ModShift != 0:
			a.findNextInEditor(-1)
		default:
			a.findNextInEditor(1)
		}
		return false
	case tcell.KeyF5:
		// Explorer focused: refresh the selected node. Otherwise the active
		// panel gets first refusal before the "F5 executes" default, as plain
		// Tab is offered below — a panel that refreshes rather than executes
		// (the Always On dashboard) has no other way to see the key, and
		// QueryPanel's own F5 matches executeActiveQuery anyway.
		switch {
		case a.focus == "explorer":
			a.refreshSelected()
		case !a.panels.HandleKey(ev):
			a.executeActiveQuery()
		}
		return false
	case tcell.KeyTab:
		// tcell has no KeyCtrlTab/KeyCtrlShiftTab: both arrive as KeyTab with
		// ModCtrl (and ModShift), on terminals with a modern keyboard
		// protocol. Elsewhere — including emulators that reserve Ctrl+Tab for
		// their own tab switching — they are indistinguishable from plain Tab
		// and fall through to the focus toggle below. Ctrl+Shift+Right/Left
		// and Ctrl+0..9 are the reliable alternatives there.
		//
		// Ctrl+Tab cycles focus forward between Object Explorer, the active
		// query panel's editor and its results pane; Ctrl+Shift+Tab reverses.
		switch {
		case ev.Modifiers()&tcell.ModCtrl != 0 && ev.Modifiers()&tcell.ModShift != 0:
			a.cycleFocusReverse()
			return false
		case ev.Modifiers()&tcell.ModCtrl != 0:
			a.cycleFocus()
			return false
		case a.focus == "explorer":
			a.focusPanels()
			return false
		}
		// Plain Tab while a panel is focused: the panel consumes it first, so
		// the query editor's Tab/indent beats the "Tab switches pane"
		// convention. Only a panel that refuses the key falls back to
		// focusing Explorer.
		if a.panels.HandleKey(ev) {
			return false
		}
		a.focusExplorer()
		return false
	case tcell.KeyBacktab:
		// Some terminals report Shift+Tab (and Ctrl+Shift+Tab) as this key
		// rather than KeyTab+ModShift. Backtab implies Shift, so Ctrl
		// alongside it reverses the focus cycle; plain Backtab falls through
		// to the focused explorer/panel, e.g. the editor's Dedent.
		if ev.Modifiers()&tcell.ModCtrl != 0 {
			a.cycleFocusReverse()
			return false
		}
	case tcell.KeyLeft:
		// Ctrl+Shift+Left switches to the previous panel/tab. Plain
		// Ctrl+Left falls through below — explorer resize / editor
		// word-jump.
		if ev.Modifiers()&tcell.ModCtrl != 0 && ev.Modifiers()&tcell.ModShift != 0 {
			a.prevPanel()
			return false
		}
	case tcell.KeyRight:
		// Ctrl+Shift+Right switches to the next panel/tab.
		if ev.Modifiers()&tcell.ModCtrl != 0 && ev.Modifiers()&tcell.ModShift != 0 {
			a.nextPanel()
			return false
		}
	case tcell.KeyRune:
		// Ctrl+0..9 jumps to panel N from the left (Object Explorer Details
		// is always 0), only while a panel already holds focus — Object
		// Explorer's tree navigation doesn't use it.
		if a.focus == "panels" && ev.Modifiers()&tcell.ModCtrl != 0 {
			if r := core.EvRune(ev); r >= '0' && r <= '9' {
				a.jumpToPanel(int(r - '0'))
				return false
			}
		}
	}

	// Explorer splitter keyboard resize (Ctrl+Left/Right), gated to explorer
	// focus so the same keys reach the editor's word-jump when a panel has it.
	if a.focus == "explorer" && a.explorerSplit.HandleKey(ev) {
		a.layoutAll()
		return false
	}

	if a.focus == "explorer" {
		a.explorer.HandleKey(ev)
	} else {
		a.panels.HandleKey(ev)
	}
	return false
}

func (a *App) handleMouse(ev *tcell.EventMouse) {
	mx, my := ev.Position()
	_, h := a.screen.Size()

	// freshPress is true only for the Button1 event that begins a gesture —
	// false for every resend while the button stays down (tcell's all-motion
	// tracking resends on each motion), however far the cursor has drifted.
	freshPress := ev.Buttons() == tcell.Button1 && !a.mouseButtonDown
	switch ev.Buttons() {
	case tcell.Button1:
		a.mouseButtonDown = true
	case tcell.ButtonNone:
		a.mouseButtonDown = false
		a.gestureOwner = ownerNone
	}

	// An overlay that opened or closed while the button was down must not be
	// handed that button. ModalDialog.ButtonClicked and ContextMenu.HandleMouse
	// treat the first Button1 they see as a fresh press, and a dialog sees none
	// until shown — so a resend landing on a button of the dialog a context-menu
	// action just opened activates it. Releases still get through, so latches
	// reset.
	if freshPress {
		a.gestureOverlay = a.overlaySnapshot()
	} else if ev.Buttons() == tcell.Button1 && a.overlaySnapshot() != a.gestureOverlay {
		return
	}

	// A release never takes one of the early returns below — see routeRelease
	// for why it must reach every latch-owning widget even under an overlay.
	if ev.Buttons() == tcell.ButtonNone {
		a.routeRelease(ev)
		return
	}

	if top := a.topDialog(); top != nil {
		top.HandleMouse(ev)
		return
	}
	if a.contextMenu.Visible() {
		a.contextMenu.HandleMouse(ev)
		return
	}
	if a.menuBar.IsOpen() {
		// A dropdown gets absolute first refusal on every mouse event, on any
		// row — nothing else may react or take focus until it closes. MenuBar
		// decides what closes it (only an outside click does).
		a.menuBar.HandleMouse(ev)
		return
	}

	// Object Explorer → query editor drag-and-drop: once a.dragNode is armed
	// below it gets first refusal on every event, ahead of the menu/toolbar
	// row, the status row and the splitter — otherwise a drag crossing row 0
	// pops a menu and one crossing the status row pops Status History. While
	// armed every Button1 event is swallowed, so the drag always refers to the
	// node it started on. The drop happens on the release, in routeRelease.
	if a.dragNode != nil {
		if ev.Buttons() == tcell.Button1 {
			// swallow motion; nothing else may react while a drop is pending
			a.dragX, a.dragY = mx, my
		} else {
			a.dragNode = nil
		}
		return
	}

	// Everything from the press that armed gestureOwner to its release belongs
	// to the region that claimed it, wherever the pointer has drifted.
	//
	// A wheel tick mid-gesture is swallowed rather than routed: it isn't part
	// of the gesture, and letting it through hands it to whatever the pointer
	// has drifted over — wheeling while dragging the splitter scrolled the
	// panels underneath. Swallowing keeps the "nothing else may react until the
	// release" invariant without inventing a wheel meaning per owner.
	if a.gestureOwner != ownerNone {
		if ev.Buttons() == tcell.Button1 {
			a.routeGesture(ev)
		}
		return
	}

	if my == 0 {
		// Toolbar occupies the right-aligned end of MenuBar's row. MenuBar
		// still sees every event first so its hover state clears when the
		// mouse moves off a label into the toolbar; it is a no-op for columns
		// outside its labels.
		a.armGesture(ev, ownerMenuRow)
		a.menuBar.HandleMouse(ev)
		a.toolbar.HandleMouse(ev)
		return
	}

	if my == h-1 {
		// freshPress, not just Button1, so a drag that started elsewhere and
		// drifts across the status row doesn't pop this dialog.
		a.armGesture(ev, ownerStatusRow)
		if freshPress {
			a.statusHistoryDialog.Show()
		}
		return
	}

	// Explorer/panel splitter drag
	if a.explorerSplit.HandleMouse(ev) {
		a.armGesture(ev, ownerSplitter)
		a.layoutAll()
		return
	}

	left := a.explorerSplit.FirstRect()
	if mx < left.Right() {
		a.armGesture(ev, ownerExplorer)
		if a.focus != "explorer" {
			a.focusExplorer()
		}
		a.explorer.HandleMouse(ev)
		// Armed from what the press landed on (NodeAt), not from whatever
		// ends up selected, and only on a fresh press. Selected() is true of
		// a node the user never touched: a press on the tree's scrollbar, its
		// border, or blank space below the last node leaves the selection
		// alone, and arming from it both dragged the wrong object and killed
		// the scrollbar drag, since the dragNode branch above swallows every
		// later event and the thumb could no longer follow the mouse.
		if freshPress {
			if n := a.explorer.NodeAt(mx, my); n != nil && isDraggableNode(n.data.Type) {
				a.dragNode = n
				a.dragX, a.dragY = mx, my
			}
		}
		return
	}
	a.armGesture(ev, ownerPanels)
	if a.focus != "panels" {
		a.focusPanels()
	}
	a.panels.HandleMouse(ev)
}

// appGestureOwner names the region that owns the in-progress mouse gesture
// — see App.gestureOwner.
type appGestureOwner int

const (
	ownerNone    appGestureOwner = iota // no gesture in progress
	ownerMenuRow                        // menu bar / toolbar (row 0)
	ownerStatusRow
	ownerSplitter
	ownerExplorer
	ownerPanels
)

// routeRelease handles a Button1 release. Every other event stops at the first
// branch that wants it; a release can't, because a release resets the
// per-widget mouseDragging latches and the widget holding one is often not the
// one the release would route to.
//
// A dialog opens on the *press* — a toolbar button's action, a menu item's — so
// it is already on the dialog stack when the matching release arrives, and
// returning early there leaves MenuBar's and Toolbar's latches armed for good.
// Both then read the next fresh click as a resend and refuse it: the first
// toolbar click after any dialog-opening one does nothing, and the first click
// on a menu header doesn't open the menu. Same for an editor/grid/tree drag
// latch when a background failure pops an alert mid-drag. It is ModalDialog's
// "a latch must not survive into the next showing" rule one layer up: the
// dialog clears its own latches, nothing was clearing the ones underneath.
//
// So the top overlay gets the release first, for its own latch, and then every
// latch owner gets it regardless. The broadcast is a no-op beyond the reset for
// a release outside a widget's bounds: MenuBar/Toolbar clear their drag flag
// and bail when off their row; Splitter clears dragging and returns; TreeView
// clears its latch before its bounds check and acts on no button but
// Button1/Button2; PanelManager forwards any release to the active panel.
func (a *App) routeRelease(ev *tcell.EventMouse) {
	if top := a.topDialog(); top != nil {
		top.HandleMouse(ev)
	} else if a.contextMenu.Visible() {
		a.contextMenu.HandleMouse(ev)
	}

	// Finish a pending Object Explorer drop, but only if nothing modal went up
	// mid-drag: pasting a node's SQL into an editor the user can't see isn't
	// what the gesture asked for. Either way dragNode is disarmed — left armed
	// it swallows every subsequent mouse event.
	if a.dragNode != nil {
		if a.topDialog() == nil {
			mx, my := ev.Position()
			a.dropExplorerNode(mx, my)
		}
		a.dragNode = nil
	}

	a.menuBar.HandleMouse(ev)
	a.toolbar.HandleMouse(ev)
	a.explorerSplit.HandleMouse(ev)
	a.explorer.HandleMouse(ev)
	a.panels.HandleMouse(ev)
}

// armGesture records that region consumed a Button1 press, so every
// further event until the release goes back to it — see gestureOwner.
func (a *App) armGesture(ev *tcell.EventMouse, region appGestureOwner) {
	if ev.Buttons() == tcell.Button1 {
		a.gestureOwner = region
	}
}

// routeGesture delivers a held-Button1 event to the region that armed the
// gesture. ownerMenuRow and ownerStatusRow swallow it: MenuBar and Toolbar
// acted on the press and suppress repeats with their own latches, and the
// status row acts only on a fresh press. The point is that no other region
// sees them.
func (a *App) routeGesture(ev *tcell.EventMouse) {
	switch a.gestureOwner {
	case ownerSplitter:
		if a.explorerSplit.HandleMouse(ev) {
			a.layoutAll()
		}
	case ownerExplorer:
		a.explorer.HandleMouse(ev)
	case ownerPanels:
		a.panels.HandleMouse(ev)
	}
}

// overlayStack is the set of modal layers open at one instant: the top dialog
// (nil if none), plus whether the context menu and a menu-bar dropdown show.
// Compared by value to notice one appearing or vanishing mid-gesture.
type overlayStack struct {
	dialog      Dialog
	contextMenu bool
	menuBar     bool
}

func (a *App) overlaySnapshot() overlayStack {
	return overlayStack{
		dialog:      a.topDialog(),
		contextMenu: a.contextMenu.Visible(),
		menuBar:     a.menuBar.IsOpen(),
	}
}
