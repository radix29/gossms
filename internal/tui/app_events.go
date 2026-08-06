package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// handleKey processes keyboard events. Returns true to signal quit.
func (a *App) handleKey(ev *tcell.EventKey) (quit bool) {
	// Record every key while the diagnostics dialog is open, before
	// anything else consumes it — including the clipboard shortcuts and
	// whatever closes the dialog itself (Escape/Enter), so those show up
	// in the log too. See key_diagnostics_dialog.go.
	if a.keyDiagDialog.Visible() {
		a.keyDiagDialog.RecordKey(ev)
	}

	// Clipboard shortcuts are handled centrally, before any dialog gets a
	// chance to consume the key, and regardless of what currently has
	// focus (a dialog's InputField, or the active query panel's Editor).
	// SetClipboard/GetClipboard are Screen methods, only available here in
	// the application layer.
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
		// Classic TUI convention: plain F10 activates the menu bar. Reaching
		// this case means the menu bar wasn't already open — the IsOpen()
		// check above intercepts F10-to-close. Shift+F10 means "open context
		// menu" and falls through to the focused explorer/panel below.
		if ev.Modifiers()&tcell.ModShift == 0 {
			a.menuBar.Open()
			return false
		}
	case tcell.KeyCtrlQ:
		// Only a quit that actually went through returns true — with unsaved
		// panels requestQuit puts up a prompt instead and quits (or doesn't)
		// from its callback, so the event loop must keep running.
		return a.requestQuit()
	case tcell.KeyCtrlN:
		a.newQueryPanel()
		return false
	case tcell.KeyCtrlW:
		a.closeActivePanel()
		return false
	case tcell.KeyCtrlO:
		// tcell has no separate KeyCtrlShiftO constant — Ctrl+<letter>
		// combined with Shift is only distinguishable from plain
		// Ctrl+<letter> on terminals with a modern keyboard protocol
		// (Kitty protocol, some xterm "modifyOtherKeys" configs); legacy
		// terminals report both identically. On those, Ctrl+Shift+O will
		// fall through to Open below rather than Connect.
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
		// backwards. Both modifiers are optional decoration on a key that
		// works plainly, so a terminal that can't encode them (see the
		// Ctrl+Enter note in README.md) still gets Find Next.
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
		if a.focus == "explorer" {
			a.refreshSelected()
		} else {
			a.executeActiveQuery()
		}
		return false
	case tcell.KeyTab:
		// tcell has no distinct KeyCtrlTab/KeyCtrlShiftTab constants —
		// Ctrl+Tab and Ctrl+Shift+Tab arrive as KeyTab with ModCtrl (and
		// ModShift) set, on terminals supporting a modern keyboard
		// protocol. On legacy terminals — and emulators that reserve
		// Ctrl+Tab for their own tab switching and never forward it — they
		// may be indistinguishable from plain Tab, in which case this falls
		// through to the explorer/panel focus toggle below.
		// Ctrl+Shift+Right/Left (see the KeyLeft/KeyRight cases below) and
		// Ctrl+0..9 are the reliable alternatives there.
		//
		// Ctrl+Tab cycles focus forward between Object Explorer, the
		// active query panel's editor, and its results pane (see
		// cycleFocus); Ctrl+Shift+Tab runs the same cycle in reverse (see
		// cycleFocusReverse).
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
		// Plain Tab while a panel is focused: let the panel consume it
		// first — the query editor's own Tab/indent handling takes
		// priority over the classic "Tab switches pane" convention. Only
		// fall back to switching focus to Explorer if the panel doesn't
		// want the key (e.g. a read-only panel with no Tab handling).
		if a.panels.HandleKey(ev) {
			return false
		}
		a.focusExplorer()
		return false
	case tcell.KeyBacktab:
		// Some terminals report Shift+Tab (and Ctrl+Shift+Tab) as this
		// distinct key instead of KeyTab+ModShift. Backtab always implies
		// Shift was held, so Ctrl alongside it reverses the focus cycle
		// (see the KeyTab case above); plain Backtab falls through below to
		// the focused explorer/panel, e.g. the query editor's Dedent.
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
		// Ctrl+0..9 jumps directly to panel N, counted from the left
		// (Object Explorer Details is always panel 0) — only while a panel
		// already holds keyboard focus, matching the query editor/results
		// context this shortcut is for; Object Explorer's own tree
		// navigation doesn't use it.
		if a.focus == "panels" && ev.Modifiers()&tcell.ModCtrl != 0 {
			if r := core.EvRune(ev); r >= '0' && r <= '9' {
				a.jumpToPanel(int(r - '0'))
				return false
			}
		}
	}

	// Explorer splitter keyboard resize (Ctrl+Left/Right). Gated to
	// explorer focus so Ctrl+Left/Right can reach the query editor's
	// word-jump navigation when a panel is focused instead.
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

	// freshPress is true only for the Button1 event that actually begins a
	// gesture — false for every resent Button1 while the button stays down
	// (tcell's all-motion tracking resends it on each motion event),
	// however far the cursor has since drifted from wherever the gesture
	// started. See mouseButtonDown's doc comment.
	freshPress := ev.Buttons() == tcell.Button1 && !a.mouseButtonDown
	switch ev.Buttons() {
	case tcell.Button1:
		a.mouseButtonDown = true
	case tcell.ButtonNone:
		a.mouseButtonDown = false
		a.gestureOwner = ownerNone
	}

	// An overlay that opened (or closed) while the button was already down
	// must not be handed that button. ModalDialog.ButtonClicked and
	// ContextMenu.HandleMouse both treat the first Button1 they see as a
	// fresh press, and a dialog sees none until it's shown — so a resend
	// landing on a button of the dialog a context-menu action just opened
	// activates it. Releases still get through, so every latch resets.
	if freshPress {
		a.gestureOverlay = a.overlaySnapshot()
	} else if ev.Buttons() == tcell.Button1 && a.overlaySnapshot() != a.gestureOverlay {
		return
	}

	// A release never takes one of the early returns below — see
	// routeRelease for why it has to reach every latch-owning widget even
	// when an overlay is up.
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
		// A dropdown gets absolute first refusal on every mouse event, on
		// any row — nothing else (toolbar, explorer, panels, splitter) may
		// react or take focus until it closes. MenuBar itself decides
		// whether the event closes it (only an outside click does).
		a.menuBar.HandleMouse(ev)
		return
	}

	// Object Explorer → query editor drag-and-drop: once a.dragNode is
	// armed (below) it gets absolute first refusal on every event, ahead of
	// the menu/toolbar row, the status row, and the splitter — otherwise a
	// drag crossing row 0 pops a menu and one crossing the status row pops
	// the Status History dialog. While armed, every Button1 event is
	// swallowed — the drag always refers to the node it started on. The
	// drop itself happens on the release, in routeRelease.
	if a.dragNode != nil {
		if ev.Buttons() == tcell.Button1 {
			// swallow motion; nothing else may react while a drop is pending
			a.dragX, a.dragY = mx, my
		} else {
			a.dragNode = nil
		}
		return
	}

	// Everything from the press that armed gestureOwner through to its
	// release belongs to the region that claimed it, wherever the pointer
	// has drifted to since — see the field's doc comment.
	//
	// A wheel tick arriving mid-gesture is swallowed rather than routed: it
	// isn't part of the gesture, and letting it through would hand it to
	// whatever region the pointer has drifted over — wheeling while dragging
	// the splitter scrolled the panels underneath it. Swallowing keeps the
	// "nothing else may react until the release" invariant whole without
	// inventing a wheel meaning for each owner.
	if a.gestureOwner != ownerNone {
		if ev.Buttons() == tcell.Button1 {
			a.routeGesture(ev)
		}
		return
	}

	if my == 0 {
		// Toolbar occupies the right-aligned end of the same row MenuBar
		// draws into; MenuBar is still given every event first so its own
		// hover state clears when the mouse moves off a label into the
		// toolbar's region (see MenuBar.HandleMouse, which is a no-op for
		// columns outside its labels).
		a.armGesture(ev, ownerMenuRow)
		a.menuBar.HandleMouse(ev)
		a.toolbar.HandleMouse(ev)
		return
	}

	if my == h-1 {
		// freshPress (not just ev.Buttons()==Button1) so a drag that
		// started elsewhere and merely drifts across the status row
		// mid-gesture doesn't pop this dialog; only a press beginning here
		// does.
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
		// Armed from what the press actually landed on (NodeAt), not from
		// whatever ends up selected afterward, and only on a fresh press.
		// Selected() is true of a node the user never touched: a press on
		// the tree's scrollbar, on its border, or on blank space below the
		// last node leaves the selection alone, and arming from it both
		// dragged the wrong object and — because the dragNode branch above
		// swallows every later event — killed the scrollbar drag outright,
		// since the thumb could no longer follow the mouse.
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

// routeRelease handles a Button1 release. Every other event stops at the
// first branch that wants it; a release can't, because a release is what
// resets the per-widget mouseDragging latches, and the widget holding one
// is often not the one the release would be routed to.
//
// A dialog opens on the *press* — a toolbar button's action, a menu item's
// — so it is already on the dialog stack by the time the matching release
// arrives, and returning early there left MenuBar's and Toolbar's latches
// armed for good. Both then read the next fresh click as a resend of that
// old gesture and refuse it: the first toolbar click after any
// dialog-opening one did nothing, and the first click on a menu header
// didn't open the menu. Same for an editor/grid/tree drag latch when a
// background failure pops an alert mid-drag. Same class as ModalDialog's
// own "a latch must not survive into the next showing" rule (see
// ModalDialog.Show), one layer up: the dialog clears its own latches, and
// nothing was clearing the ones underneath it.
//
// So the top overlay gets the release first — for its own latch — and then
// it goes to every latch owner regardless. That broadcast is a no-op
// beyond the reset for a release outside a widget's own bounds:
// MenuBar/Toolbar clear their drag flag and bail when off their row;
// Splitter clears dragging and returns; the explorer's TreeView clears its
// latch before its bounds check, and acts on no button but Button1/Button2;
// PanelManager forwards any release to the active panel regardless of
// position (see PanelManager.HandleMouse).
func (a *App) routeRelease(ev *tcell.EventMouse) {
	if top := a.topDialog(); top != nil {
		top.HandleMouse(ev)
	} else if a.contextMenu.Visible() {
		a.contextMenu.HandleMouse(ev)
	}

	// Finish a pending Object Explorer drag-and-drop — but only if nothing
	// modal went up mid-drag, since pasting a node's SQL into an editor the
	// user can't currently see or reach isn't what the gesture asked for.
	// Either way dragNode is disarmed; leaving it armed used to swallow
	// every subsequent mouse event.
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
// already acted on the press and suppress the repeats with their own
// mouseDragging latches, and the status row only ever acts on a fresh
// press — the point is only that no other region sees them either.
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

// overlayStack is the set of modal layers open at one instant — the top
// dialog (nil if none), plus whether the context menu and a menu-bar
// dropdown are showing. Compared by value to notice one appearing or
// vanishing mid-gesture; see handleMouse.
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
