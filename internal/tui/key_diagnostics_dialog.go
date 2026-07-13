package tui

import (
	"fmt"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// maxKeyDiagLines caps the diagnostics log so a long session doesn't grow
// it without bound; oldest entries are dropped first.
const maxKeyDiagLines = 200

// KeyDiagnosticsDialog is a small modal that shows exactly what tcell
// decoded for every key event while it's open — the raw Key/Modifiers/rune
// values plus ev.Name()'s human-readable decode (e.g. "Shift+Left"). It
// exists to turn "a shortcut doesn't seem to work" from a guessing game
// into a 10-second check of whether the terminal is actually delivering
// the modifier bits goSSMS expects; see internal/tuikit/controls/editor.go
// and widgets/input_field.go for the Shift+Arrow selection handling this
// is most often used to debug.
//
// Newest entries are prepended, so the most recent key press is always the
// first line — no scrolling needed for the common case of pressing one key
// and immediately checking what was recorded.
type KeyDiagnosticsDialog struct {
	dialogs.ModalDialog
	lines  []string
	scroll int
}

// NewKeyDiagnosticsDialog creates the key diagnostics dialog.
func NewKeyDiagnosticsDialog(app *App) *KeyDiagnosticsDialog {
	d := &KeyDiagnosticsDialog{}
	d.InitModal(app.screen, "Key Diagnostics", 70, 24)
	return d
}

// RecordKey appends a formatted description of ev to the log. Call only
// while the dialog is visible (see app_events.go's handleKey) — recording
// keys the user can't currently see would just be wasted work.
func (d *KeyDiagnosticsDialog) RecordKey(ev *tcell.EventKey) {
	line := fmt.Sprintf("%-20s  Key=%-4d Mod=%-3d Str=%-8q Repeat=%d",
		ev.Name(), ev.Key(), ev.Modifiers(), ev.Str(), ev.Repeat())
	d.lines = append([]string{line}, d.lines...)
	if len(d.lines) > maxKeyDiagLines {
		d.lines = d.lines[:maxKeyDiagLines]
	}
}

// Show resets the log and scroll position, then displays the dialog —
// each open starts a fresh diagnostic session rather than accumulating
// across unrelated opens.
func (d *KeyDiagnosticsDialog) Show() {
	d.lines = nil
	d.scroll = 0
	d.ModalDialog.Show()
}

// Draw renders the dialog.
func (d *KeyDiagnosticsDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	p := theme.Active()
	contentStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	dimStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.TextDim)

	inner := d.InnerRect()
	dataH := inner.H - 2 // leave room for the Close button

	if len(d.lines) == 0 {
		core.DrawText(s, inner.X+1, inner.Y+1, dimStyle, "Press any key to see how it was decoded...")
	}
	for row := 0; row < dataH; row++ {
		idx := d.scroll + row
		if idx >= len(d.lines) {
			break
		}
		core.FillRect(s, core.Rect{X: inner.X, Y: inner.Y + 1 + row, W: inner.W, H: 1}, ' ', contentStyle)
		core.DrawTextClipped(s, inner.X+1, inner.Y+1+row, inner.W-2, contentStyle, d.lines[idx])
	}

	if len(d.lines) > dataH {
		sbStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Border)
		sbThumb := tcell.StyleDefault.Background(p.BorderActive).Foreground(p.BorderActive)
		core.DrawScrollbar(s, d.Rect().Right()-1, inner.Y+1, dataH, len(d.lines), dataH, d.scroll, sbStyle, sbThumb)
	}

	d.DrawButtons(s, []string{"Close"}, 0)
}

// HandleKey processes keyboard events. RecordKey (called from
// app_events.go before this) has already logged ev by the time this runs,
// so Escape/Enter closing the dialog shows up in the log too.
func (d *KeyDiagnosticsDialog) HandleKey(ev *tcell.EventKey) bool {
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
		if d.scroll+dataH < len(d.lines) {
			d.scroll++
		}
	case tcell.KeyPgUp:
		d.scroll = core.Max(0, d.scroll-dataH)
	case tcell.KeyPgDn:
		d.scroll = core.Max(0, core.Min(len(d.lines)-dataH, d.scroll+dataH))
	}
	return true
}

// HandleMouse handles mouse events.
func (d *KeyDiagnosticsDialog) HandleMouse(ev *tcell.EventMouse) bool {
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
	dataH := d.InnerRect().H - 2
	switch ev.Buttons() {
	case tcell.WheelUp:
		if d.scroll > 0 {
			d.scroll--
		}
	case tcell.WheelDown:
		if d.scroll+dataH < len(d.lines) {
			d.scroll++
		}
	}
	return true
}
