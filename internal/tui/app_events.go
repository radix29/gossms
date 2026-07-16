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
		// this case means the menu bar wasn't already open (see the IsOpen()
		// check above, which intercepts F10-to-close before we get here).
		// Shift+F10 means "open context menu" instead — not handled here,
		// it falls through to the focused explorer/panel below.
		if ev.Modifiers()&tcell.ModShift == 0 {
			a.menuBar.Open()
			return false
		}
	case tcell.KeyCtrlQ:
		a.quit()
		return true
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
	case tcell.KeyF5:
		if a.focus == "explorer" {
			a.refreshSelected()
		} else {
			a.executeActiveQuery()
		}
		return false
	case tcell.KeyTab:
		// tcell has no distinct KeyCtrlTab/KeyCtrlShiftTab constants —
		// Ctrl+Tab (and Ctrl+Shift+Tab) are reported as KeyTab with
		// ModCtrl (and ModShift) set, on terminals that support a modern
		// keyboard protocol; on legacy terminals — and some terminal
		// emulators reserve Ctrl+Tab for their own tab switching and never
		// forward it at all — they may be indistinguishable from plain
		// Tab, in which case this simply falls through to the explorer/
		// panel focus toggle below. Ctrl+Shift+Right/Left (see the KeyLeft/
		// KeyRight cases below) and Ctrl+0..9 are more reliable
		// alternatives on such terminals.
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
		// first — the query editor's own Tab/indent handling needs
		// priority over the classic "Tab switches pane" convention, same
		// as any real text editor never surrendering focus on Tab. Only
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
		// Shift was held, so Ctrl held alongside it means reverse the
		// focus cycle (see the KeyTab case above); plain Backtab falls
		// through below to the focused explorer/panel (e.g. the query
		// editor's Dedent binding), same as any other key.
		if ev.Modifiers()&tcell.ModCtrl != 0 {
			a.cycleFocusReverse()
			return false
		}
	case tcell.KeyLeft:
		// Ctrl+Shift+Left switches to the previous panel/tab — the job
		// Ctrl+Shift+Tab used to do (see the KeyTab case above). Plain
		// Ctrl+Left falls through below (explorer resize / editor word-
		// jump), same as before this case was added.
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

	if top := a.topDialog(); top != nil {
		top.HandleMouse(ev)
		return
	}
	if a.contextMenu.Visible() {
		a.contextMenu.HandleMouse(ev)
		return
	}
	if my == 0 {
		// Toolbar occupies the right-aligned end of the same row MenuBar
		// draws into; MenuBar is still given every event first so its own
		// hover state clears when the mouse moves off a label into the
		// toolbar's region (see MenuBar.HandleMouse, which is a no-op for
		// columns outside its labels). If the toolbar then claims the
		// event, any dropdown MenuBar left open is closed — clicking the
		// toolbar has the same "click elsewhere closes the menu" effect a
		// click anywhere else in the app already has (line 207 below).
		a.menuBar.HandleMouse(ev)
		if a.toolbar.HandleMouse(ev) && a.menuBar.IsOpen() {
			a.menuBar.Close()
		}
		return
	}
	if a.menuBar.IsOpen() {
		if a.menuBar.HandleMouse(ev) {
			return
		}
		a.menuBar.Close()
	}

	if my == h-1 {
		if ev.Buttons() == tcell.Button1 {
			a.statusHistoryDialog.Show()
		}
		return
	}

	// Object Explorer → query editor drag-and-drop: once a.dragNode is
	// armed (below), it must get absolute first refusal on every event,
	// ahead of even the splitter — otherwise a drag path that happens to
	// cross the splitter's column arms *its* resize drag too, and a drag
	// path that lingers inside Object Explorer keeps reaching
	// TreeView.HandleMouse, which reselects (or, worse, toggles
	// expand/collapse on) whatever row the pointer passes over. While
	// armed, every Button1 event is swallowed outright — the drag always
	// refers to the node it started on, never whatever's currently under
	// the cursor — and only a release is acted on.
	if a.dragNode != nil {
		switch ev.Buttons() {
		case tcell.ButtonNone:
			a.dropExplorerNode(mx, my)
			a.dragNode = nil
		case tcell.Button1:
			// swallow motion; nothing else may react while a drop is pending
		default:
			a.dragNode = nil
		}
		return
	}

	// Explorer/panel splitter drag
	if a.explorerSplit.HandleMouse(ev) {
		a.layoutAll()
		return
	}

	left := a.explorerSplit.FirstRect()
	if mx < left.Right() {
		if a.focus != "explorer" {
			a.focusExplorer()
		}
		a.explorer.HandleMouse(ev)
		if ev.Buttons() == tcell.Button1 {
			if n := a.explorer.Selected(); n != nil && isDraggableNode(n.data.Type) {
				a.dragNode = n
			}
		}
		return
	}
	if a.focus != "panels" {
		a.focusPanels()
	}
	a.panels.HandleMouse(ev)
}
