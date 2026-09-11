package tui

import (
	"unicode"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// normalizeCtrlRune folds a Ctrl-modified letter back to KeyCtrlA..KeyCtrlZ,
// keeping other modifiers.
//
// tcell folds only when Ctrl is the sole modifier, so Ctrl+Shift+<letter>
// arrives as KeyRune with ModCtrl|ModShift and matches no binding. Kitty sends
// the base rune ('o'), xterm's modifyOtherKeys the shifted one ('O'), hence
// ToLower. Without this, Ctrl+Shift chords are dead on exactly the terminals
// that can encode them.
func normalizeCtrlRune(ev *tcell.EventKey) *tcell.EventKey {
	// Ctrl+Shift only: Ctrl+Alt is AltGr on many layouts and must stay text.
	if ev.Key() != tcell.KeyRune || ev.Modifiers()&^tcell.ModShift != tcell.ModCtrl {
		return ev
	}
	str := []rune(ev.Str())
	if len(str) != 1 {
		return ev
	}
	r := unicode.ToLower(str[0])
	if r < 'a' || r > 'z' {
		return ev
	}
	return tcell.NewEventKey(tcell.KeyCtrlA+tcell.Key(r-'a'), "", ev.Modifiers())
}

// handleKey processes keyboard events; returns true to quit.
func (a *App) handleKey(ev *tcell.EventKey) (quit bool) {
	// Log every key while the diagnostics dialog is open, before anything
	// consumes it, including clipboard keys and whatever closes the dialog.
	if a.keyDiagDialog.Visible() {
		a.keyDiagDialog.RecordKey(ev)
	}

	ev = normalizeCtrlRune(ev)

	// Clipboard shortcuts are handled centrally before dialogs or focus:
	// SetClipboard/GetClipboard are Screen methods, available only here.
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
		// Plain F10 opens the menu bar (the IsOpen check above handles
		// closing). Shift+F10 is the context menu and falls through.
		if ev.Modifiers()&tcell.ModShift == 0 {
			a.menuBar.Open()
			return false
		}
	case tcell.KeyCtrlQ:
		// Returns true only if quitting went through; with unsaved panels
		// requestQuit prompts and quits from its callback.
		return a.requestQuit()
	case tcell.KeyCtrlN:
		a.newQueryPanel()
		return false
	case tcell.KeyCtrlW:
		a.closeActivePanel()
		return false
	case tcell.KeyF9:
		a.connectDialog.Show()
		return false
	case tcell.KeyCtrlO:
		// Ctrl+Shift+O arrives as KeyCtrlO+ModShift only via normalizeCtrlRune
		// on modern-protocol terminals; elsewhere it's plain Ctrl+O (open
		// file). F9 works everywhere.
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
		// Ctrl+F3 finds the word under the caret, Shift+F3 steps back; both
		// decorate plain F3.
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
		// Explorer focused: refresh the selection. Otherwise the active panel
		// sees F5 first (like Tab below), so panels that refresh rather than
		// execute (the Always On dashboard) get it; QueryPanel's F5 executes
		// anyway.
		switch {
		case a.focus == focusOnExplorer:
			a.refreshSelected()
		case !a.panels.HandleKey(ev):
			a.executeActiveQuery()
		}
		return false
	case tcell.KeyTab:
		// tcell has no KeyCtrlTab: Ctrl(+Shift)+Tab arrive as KeyTab with
		// ModCtrl (and ModShift) on modern-protocol terminals. Elsewhere,
		// including emulators that reserve Ctrl+Tab, they look like plain Tab;
		// Ctrl+Shift+Right/Left and Ctrl+0..9 are the reliable alternatives.
		//
		// Ctrl+Tab cycles focus forward through Object Explorer, the query
		// editor and its results; Ctrl+Shift+Tab reverses.
		switch {
		case ev.Modifiers()&tcell.ModCtrl != 0 && ev.Modifiers()&tcell.ModShift != 0:
			a.cycleFocusReverse()
			return false
		case ev.Modifiers()&tcell.ModCtrl != 0:
			a.cycleFocus()
			return false
		case a.focus == focusOnExplorer:
			a.focusPanels()
			return false
		}
		// Plain Tab goes to the focused panel first, so the editor's indent
		// wins; only a refusing panel falls back to focusing Explorer.
		if a.panels.HandleKey(ev) {
			return false
		}
		a.focusExplorer()
		return false
	case tcell.KeyBacktab:
		// Some terminals send Shift+Tab as Backtab instead of KeyTab+ModShift.
		// With Ctrl it reverses the focus cycle; plain Backtab falls through
		// (e.g. the editor's Dedent).
		if ev.Modifiers()&tcell.ModCtrl != 0 {
			a.cycleFocusReverse()
			return false
		}
	case tcell.KeyLeft:
		// Ctrl+Shift+Left: previous panel. Plain Ctrl+Left falls through
		// (explorer resize, word jump).
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
		// Ctrl+0..9 jumps to panel N from the left (Object Explorer Details is
		// 0), only while a panel has focus.
		if a.focus == focusOnPanels && ev.Modifiers()&tcell.ModCtrl != 0 {
			if r := core.EvRune(ev); r >= '0' && r <= '9' {
				a.jumpToPanel(int(r - '0'))
				return false
			}
		}
	}

	// Explorer splitter resize (Ctrl+Left/Right), only with explorer focus so
	// the editor keeps word jump.
	if a.focus == focusOnExplorer && a.explorerSplit.HandleKey(ev) {
		a.layoutAll()
		return false
	}

	if a.focus == focusOnExplorer {
		a.explorer.HandleKey(ev)
	} else {
		a.panels.HandleKey(ev)
	}
	return false
}

func (a *App) handleMouse(ev *tcell.EventMouse) {
	// As handleKey: log first, since what the terminal delivered is what this
	// dialog shows.
	if a.keyDiagDialog.Visible() {
		a.keyDiagDialog.RecordMouse(ev)
	}
	mx, my := ev.Position()
	_, h := a.screen.Size()

	// freshPress is true only for the Button1 event starting a gesture, not
	// tcell's resends while held.
	freshPress := ev.Buttons() == tcell.Button1 && !a.mouseButtonDown
	switch ev.Buttons() {
	case tcell.Button1:
		a.mouseButtonDown = true
	case tcell.ButtonNone:
		a.mouseButtonDown = false
		a.gestureOwner = ownerNone
	}

	// An overlay that opened or closed mid-press must not get that button:
	// ModalDialog.ButtonClicked and ContextMenu.HandleMouse treat the first
	// Button1 they see as a fresh press, so a resend could activate a button on
	// a dialog a menu action just opened. Releases still pass so latches reset.
	if freshPress {
		a.gestureOverlay = a.overlaySnapshot()
	} else if ev.Buttons() == tcell.Button1 && a.overlaySnapshot() != a.gestureOverlay {
		return
	}

	// A release never takes the early returns below; see routeRelease.
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
		// An open dropdown gets first refusal on every mouse event; MenuBar
		// decides what closes it (only an outside click).
		a.menuBar.HandleMouse(ev)
		return
	}

	// Object Explorer → editor drag-and-drop: once dragNode is armed it takes
	// every event ahead of the menu row, status row and splitter, or a drag
	// crossing them pops a menu or Status History. Every Button1 event is
	// swallowed so the drag stays on its node. The drop happens on release, in
	// routeRelease.
	if a.dragNode != nil {
		if ev.Buttons() == tcell.Button1 {
			// Swallow motion while a drop is pending.
			a.dragX, a.dragY = mx, my
		} else {
			a.dragNode = nil
		}
		return
	}

	// From the arming press to its release, events belong to the region that
	// claimed it, wherever the pointer goes.
	//
	// A mid-gesture wheel tick is swallowed rather than routed to whatever the
	// pointer is over (wheeling while dragging the splitter would scroll the
	// panels).
	if a.gestureOwner != ownerNone {
		if ev.Buttons() == tcell.Button1 {
			a.routeGesture(ev)
		}
		return
	}

	if my == 0 {
		// The toolbar sits at the right end of MenuBar's row. MenuBar still
		// sees every event first, so its hover clears when moving onto the
		// toolbar.
		a.armGesture(ev, ownerMenuRow)
		a.menuBar.HandleMouse(ev)
		a.toolbar.HandleMouse(ev)
		return
	}

	if my == h-1 {
		// freshPress, so a drag drifting across the status row doesn't pop the
		// dialog.
		a.armGesture(ev, ownerStatusRow)
		if freshPress {
			a.statusHistoryDialog.Show()
		}
		return
	}

	// Explorer/panel splitter drag.
	if a.explorerSplit.HandleMouse(ev) {
		a.armGesture(ev, ownerSplitter)
		a.layoutAll()
		return
	}

	left := a.explorerSplit.FirstRect()
	if mx < left.Right() {
		a.armGesture(ev, ownerExplorer)
		if a.focus != focusOnExplorer {
			a.focusExplorer()
		}
		a.explorer.HandleMouse(ev)
		// Armed from what the press hit (NodeAt), only on a fresh press.
		// Selected() may be a node never touched — a press on the scrollbar,
		// border or blank space leaves the selection — and arming from it drags
		// the wrong object and kills the scrollbar drag.
		if freshPress {
			if n := a.explorer.NodeAt(mx, my); n != nil && isDraggableNode(n.data.Type) {
				a.dragNode = n
				a.dragX, a.dragY = mx, my
			}
		}
		return
	}
	a.armGesture(ev, ownerPanels)
	if a.focus != focusOnPanels {
		a.focusPanels()
	}
	a.panels.HandleMouse(ev)
}

// appGestureOwner names the region owning the in-progress gesture; see
// App.gestureOwner.
type appGestureOwner int

const (
	ownerNone    appGestureOwner = iota // no gesture in progress
	ownerMenuRow                        // menu bar / toolbar (row 0)
	ownerStatusRow
	ownerSplitter
	ownerExplorer
	ownerPanels
)

// routeRelease handles a Button1 release. Unlike other events it can't stop at
// the first taker: it resets per-widget mouseDragging latches, and the latch
// holder often isn't where the release would route.
//
// Dialogs open on the press, so they're on the stack when the release arrives.
// Returning early there left MenuBar's and Toolbar's latches armed, making the
// next click read as a resend (the first toolbar click after a dialog-opening
// one did nothing; the first menu header click didn't open). Same for editor,
// grid or tree latches when an alert pops mid-drag. It's ModalDialog's "a latch
// must not survive into the next showing" rule one layer up.
//
// So the top overlay gets the release first, then every latch owner regardless.
// Off-bounds, each only resets: MenuBar/Toolbar clear their flag off-row;
// Splitter clears dragging; TreeView clears its latch before its bounds check
// and acts only on Button1/Button2; PanelManager forwards any release to the
// active panel.
func (a *App) routeRelease(ev *tcell.EventMouse) {
	if top := a.topDialog(); top != nil {
		top.HandleMouse(ev)
	} else if a.contextMenu.Visible() {
		a.contextMenu.HandleMouse(ev)
	}

	// Complete a pending Object Explorer drop only if nothing modal opened
	// mid-drag. dragNode is disarmed either way; armed, it swallows all later
	// mouse events.
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

// armGesture records that region consumed a Button1 press, so events until the
// release return to it.
func (a *App) armGesture(ev *tcell.EventMouse, region appGestureOwner) {
	if ev.Buttons() == tcell.Button1 {
		a.gestureOwner = region
	}
}

// routeGesture delivers a held-Button1 event to the arming region. ownerMenuRow
// and ownerStatusRow swallow it: MenuBar and Toolbar acted on the press and
// latch against repeats, and the status row acts only on fresh presses.
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

// overlayStack is the modal layers open at one instant: top dialog (or nil),
// context menu and dropdown. Compared by value to detect changes mid-gesture.
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
