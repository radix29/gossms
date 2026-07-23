package dialogs

import (
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// List navigation
// ---------------------------------------------------------------------------

func (d *FileDialog) handleListKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyUp:
		if d.sel > 0 {
			d.sel--
			d.ensureVisible()
			d.syncNameFromSelection()
		}
	case tcell.KeyDown:
		if d.sel < len(d.entries)-1 {
			d.sel++
			d.ensureVisible()
			d.syncNameFromSelection()
		}
	case tcell.KeyPgUp:
		d.sel = core.Max(0, d.sel-fileListRows)
		d.ensureVisible()
		d.syncNameFromSelection()
	case tcell.KeyPgDn:
		d.sel = core.Min(len(d.entries)-1, d.sel+fileListRows)
		d.ensureVisible()
		d.syncNameFromSelection()
	case tcell.KeyHome:
		d.sel, d.scroll = 0, 0
		d.syncNameFromSelection()
	case tcell.KeyEnd:
		d.sel = len(d.entries) - 1
		d.ensureVisible()
		d.syncNameFromSelection()
	default:
		if r := core.EvRune(ev); r != 0 && ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) == 0 {
			d.typeaheadJump(r)
		} else {
			return false
		}
	}
	return true
}

// typeaheadJump extends the pending typeahead buffer with r and jumps
// selection to the first entry whose name starts with it (case-
// insensitive); if nothing matches the extended buffer, it restarts from
// just r — classic file-manager "type to jump" navigation.
func (d *FileDialog) typeaheadJump(r rune) {
	for _, candidate := range []string{d.typeahead + string(r), string(r)} {
		lower := strings.ToLower(candidate)
		for i, e := range d.entries {
			if strings.HasPrefix(strings.ToLower(e.name), lower) {
				d.typeahead = candidate
				d.sel = i
				d.ensureVisible()
				d.syncNameFromSelection()
				return
			}
		}
	}
}

func (d *FileDialog) ensureVisible() {
	if d.sel < d.scroll {
		d.scroll = d.sel
	}
	if d.sel >= d.scroll+fileListRows {
		d.scroll = d.sel - fileListRows + 1
	}
	if d.scroll < 0 {
		d.scroll = 0
	}
}

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

func (d *FileDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	switch ev.Key() {
	case tcell.KeyEscape:
		d.cancel()
		return true
	case tcell.KeyEnter:
		d.confirmFocused()
		return true
	case tcell.KeyTab:
		switch d.focus {
		case ffPath:
			if d.completeField(d.pathField, true) {
				return true
			}
		case ffName:
			if d.completeField(d.nameField, false) {
				return true
			}
		}
		d.setFocus((d.focus + 1) % 4)
		return true
	case tcell.KeyBacktab:
		d.setFocus((d.focus - 1 + 4) % 4)
		return true
	}

	switch d.focus {
	case ffPath:
		return d.pathField.HandleKey(ev)
	case ffName:
		return d.nameField.HandleKey(ev)
	case ffList:
		return d.handleListKey(ev)
	case ffButtons:
		if ev.Key() == tcell.KeyLeft || ev.Key() == tcell.KeyRight {
			d.btnFocus = (d.btnFocus + 1) % 2
		}
		return true
	}
	return true
}

func (d *FileDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	// A release must reach whichever field currently has focus even when it
	// lands outside the dialog (consumed below) — otherwise its next press
	// is swallowed as a continuation of the stale drag. Each returns false
	// on ButtonNone, so this has no effect beyond resetting the latch.
	if ev.Buttons() == tcell.ButtonNone {
		d.listMouseDragging = false
		switch d.focus {
		case ffPath:
			d.pathField.HandleMouse(ev)
		case ffName:
			d.nameField.HandleMouse(ev)
		}
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if ev.Buttons() == tcell.ButtonNone {
		return true
	}
	if i := d.ButtonClicked(ev, d.buttonLabels()); i >= 0 {
		d.btnFocus = i
		d.activateButton()
		return true
	}

	mx, my := ev.Position()
	switch ev.Buttons() {
	case tcell.Button1:
		if d.pathField.HitTest(mx, my) {
			d.setFocus(ffPath)
			d.pathField.HandleMouse(ev)
			return true
		}
		if d.nameField.HitTest(mx, my) {
			d.setFocus(ffName)
			d.nameField.HandleMouse(ev)
			return true
		}
		if lr := d.listRect(); lr.Contains(mx, my) {
			if d.listMouseDragging {
				// Still the same physical press — do not re-activate on
				// every resent motion event.
				return true
			}
			d.listMouseDragging = true
			if idx := d.scroll + (my - lr.Y); idx >= 0 && idx < len(d.entries) {
				same := idx == d.sel
				d.sel = idx
				d.setFocus(ffList)
				d.syncNameFromSelection()
				if same {
					d.activateSelected()
				}
			}
			return true
		}
	case tcell.WheelUp:
		if lr := d.listRect(); lr.Contains(mx, my) && d.scroll > 0 {
			d.scroll--
		}
	case tcell.WheelDown:
		if lr := d.listRect(); lr.Contains(mx, my) && d.scroll < len(d.entries)-lr.H {
			d.scroll++
		}
	}
	return true
}
