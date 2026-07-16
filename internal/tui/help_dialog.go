package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// HelpDialog is the F1 help modal. It embeds dialogs.ModalDialog and
// renders a static, scrollable list of keyboard/mouse shortcuts.
type HelpDialog struct {
	dialogs.ModalDialog
	scroll int
}

// NewHelpDialog creates the help dialog.
func NewHelpDialog(app *App) *HelpDialog {
	d := &HelpDialog{}
	d.InitModal(app.screen, "goSSMS Help", 62, 28)
	return d
}

var helpLines = []string{
	"goSSMS - Go SQL Server Management Studio",
	"",
	"KEYBOARD SHORTCUTS",
	"------------------",
	"",
	"Global",
	"  F1          Show this help",
	"  F10         Activate menu bar",
	"  (in menu)   Left/Right switch menus, Up/Down select an item,",
	"              Enter activates, Escape/F10 closes",
	"  Ctrl+Q      Quit",
	"  Click status bar  Show message history",
	"  Ctrl+Shift+O Connect to server",
	"  Ctrl+O      Open a .sql file as a new query",
	"  Ctrl+N      New query panel",
	"  Ctrl+W      Close current query",
	"  Ctrl+S      Save query",
	"  Ctrl+C/X/V  Copy / cut / paste (editor and dialog fields)",
	"  Ctrl+Space  Open context menu (keyboard right-click equivalent)",
	"",
	"Object Explorer",
	"  Arrow keys  Navigate tree",
	"  Enter/+     Expand node",
	"  -/Backspace Collapse node",
	"  F5          Refresh node",
	"  Right click Context menu (also Shift+F10, Menu key, Ctrl+Space)",
	"  Ctrl+Left   Shrink explorer",
	"  Ctrl+Right  Grow explorer",
	"",
	"Query Editor",
	"  F5          Execute (runs only the selection, if any)",
	"  Shift+Arrow Select text with the keyboard",
	"  Click+drag  Select text with the mouse",
	"  Ctrl+Z       Undo",
	"  Ctrl+Y       Redo",
	"  Ctrl+Up     Grow editor / shrink results",
	"  Ctrl+Down   Shrink editor / grow results",
	"  Ctrl+PgUp/PgDn  Previous / next result tab (grids and Messages)",
	"",
	"Focus / Panel Tabs",
	"  Ctrl+Tab         Cycle focus: Explorer -> Query Editor -> Results",
	"  Ctrl+Shift+Tab   Cycle focus in reverse: Explorer -> Results -> Editor",
	"  Ctrl+Shift+Right Next panel/tab (Query N, Object Explorer Details, ...)",
	"  Ctrl+Shift+Left  Previous panel/tab",
	"  Ctrl+0..9        Jump to panel N, counted from the left (0 = Object",
	"                   Explorer Details); only while a panel has focus",
	"  Some terminals reserve Ctrl+Tab/Ctrl+Shift+Tab for their own tab",
	"  switching and never forward it — Ctrl+Shift+Left/Right and Ctrl+0..9",
	"  are the reliable fallback on those.",
	"  The currently focused pane's title/header bar is highlighted.",
	"",
	"MOUSE",
	"-----",
	"  Click       Select / focus",
	"  Dbl-click   Open / expand",
	"  Right-click Context menu",
	"  Drag splitter to resize panels",
	"  Scroll wheel in tree / results",
}

// Draw renders the help dialog.
func (d *HelpDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	contentStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	headStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.BorderActive).Bold(true)

	inner := d.InnerRect()
	dataH := inner.H - 2 // leave room for the Close button

	for row := 0; row < dataH; row++ {
		idx := d.scroll + row
		if idx >= len(helpLines) {
			break
		}
		line := helpLines[idx]
		st := contentStyle
		if len(line) > 0 && line[0] != ' ' {
			st = headStyle
		}
		core.FillRect(s, core.Rect{X: inner.X, Y: inner.Y + 1 + row, W: inner.W, H: 1}, ' ', contentStyle)
		core.DrawTextClipped(s, inner.X+1, inner.Y+1+row, inner.W-2, st, line)
	}

	if len(helpLines) > dataH {
		sbStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		core.DrawScrollbar(s, d.Rect().Right()-1, inner.Y+1, dataH, len(helpLines), dataH, d.scroll, sbStyle, sbThumb)
	}

	d.DrawButtons(s, []string{"Close"}, 0)
}

// HandleKey processes keyboard events.
func (d *HelpDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	dataH := d.InnerRect().H - 2
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyEnter:
		d.Hide()
	case tcell.KeyUp:
		if d.scroll > 0 {
			d.scroll--
		}
	case tcell.KeyDown:
		if d.scroll+dataH < len(helpLines) {
			d.scroll++
		}
	case tcell.KeyPgUp:
		d.scroll = core.Max(0, d.scroll-dataH)
	case tcell.KeyPgDn:
		d.scroll = core.Max(0, core.Min(len(helpLines)-dataH, d.scroll+dataH))
	}
	return true
}

// HandleMouse handles mouse events.
func (d *HelpDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if d.ButtonClicked(ev, []string{"Close"}) == 0 {
		d.Hide()
		return true
	}
	switch ev.Buttons() {
	case tcell.WheelUp:
		if d.scroll > 0 {
			d.scroll--
		}
	case tcell.WheelDown:
		dataH := d.InnerRect().H - 2
		if d.scroll+dataH < len(helpLines) {
			d.scroll++
		}
	}
	return true
}
