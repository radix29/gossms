package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// HandleKey routes keyboard events.
func (d *BackupDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}

	if d.mode == backupModeProgress {
		switch ev.Key() {
		case tcell.KeyEscape:
			d.Hide()
		case tcell.KeyEnter:
			d.btnFocus = min(d.btnFocus, len(d.progressButtons())-1)
			d.doProgressButton()
		case tcell.KeyTab, tcell.KeyF1:
			d.btnFocus = (d.btnFocus + 1) % len(d.progressButtons())
		case tcell.KeyBacktab:
			n := len(d.progressButtons())
			d.btnFocus = (d.btnFocus - 1 + n) % n
		}
		return true
	}

	switch ev.Key() {
	case tcell.KeyTab:
		d.setFocus(nextFocus(d.focusIdx, len(d.focusable)))
		return true
	case tcell.KeyBacktab:
		d.setFocus(prevFocus(d.focusIdx, len(d.focusable)))
		return true
	case tcell.KeyEscape:
		if d.ddDatabase.IsOpen() {
			d.ddDatabase.HandleKey(ev)
			return true
		}
		d.Hide()
		return true
	case tcell.KeyEnter:
		if d.ddDatabase.IsOpen() {
			d.ddDatabase.HandleKey(ev)
			d.syncAutoDest()
			return true
		}
		if b, ok := d.focusable[d.focusIdx].(*widgets.Button); ok {
			return b.HandleKey(ev)
		}
		d.doFormButton()
		return true
	case tcell.KeyF1:
		d.btnFocus = (d.btnFocus + 1) % len(backupFormButtons)
		return true
	}

	if h, ok := d.focusable[d.focusIdx].(interface {
		HandleKey(*tcell.EventKey) bool
	}); ok {
		consumed := h.HandleKey(ev)
		d.syncAutoDest()
		return consumed
	}
	return true
}

// HandleMouse routes mouse events.
func (d *BackupDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	// A release must reach every mouseDragging-latched widget even when it
	// lands outside the dialog (consumed below) or in progress mode (early
	// return above) — otherwise its next press is swallowed as a
	// continuation of the stale drag. Each returns false on ButtonNone, so
	// this has no effect beyond resetting the latch.
	if ev.Buttons() == tcell.ButtonNone {
		d.ddDatabase.HandleMouse(ev)
		d.rbType.HandleMouse(ev)
		d.btnBrowse.HandleMouse(ev)
		d.cbCompress.HandleMouse(ev)
		d.cbVerify.HandleMouse(ev)
		d.cbChecksum.HandleMouse(ev)
		d.cbCopyOnly.HandleMouse(ev)
		// Terminate a text-selection drag in the field that claimed the
		// press, wherever the release landed. Done before
		// ConsumeOutsideClick, which returns early on a release outside the
		// dialog — exactly the case that would otherwise strand the latch.
		if d.dragField != nil {
			d.dragField.HandleMouse(ev)
			d.dragField = nil
		}
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}

	if d.mode == backupModeProgress {
		if i := d.ButtonClicked(ev, d.progressButtons()); i >= 0 {
			d.btnFocus = i
			d.doProgressButton()
		}
		return true
	}

	// Always forward a release to a focused input so a text-selection drag
	// terminates cleanly (see ConnectDialog.HandleMouse).
	if ev.Buttons() == tcell.ButtonNone {
		if f, ok := d.focusable[d.focusIdx].(*widgets.InputField); ok {
			f.HandleMouse(ev)
		}
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}

	// The gesture belongs to whichever field claimed its press, so motion is
	// replayed there without hit-testing — ahead of every widget below,
	// since none of them can own a gesture this one already started.
	if d.dragField != nil {
		d.dragField.HandleMouse(ev)
		return true
	}

	// The database dropdown's open list is an overlay drawn last, so it
	// gets first refusal of every click — ahead of ButtonClicked below,
	// which would otherwise steal a click landing on an open list row that
	// happens to visually overlap the button row.
	if d.ddDatabase.HandleMouse(ev) {
		d.focusTo(d.ddDatabase)
		d.syncAutoDest()
		return true
	}

	if i := d.ButtonClicked(ev, backupFormButtons); i >= 0 {
		d.btnFocus = i
		d.doFormButton()
		return true
	}

	if d.rbType.HandleMouse(ev) {
		d.focusTo(d.rbType)
		d.syncAutoDest()
		return true
	}
	if d.btnBrowse.HandleMouse(ev) {
		d.focusTo(d.btnBrowse)
		return true
	}

	for _, cb := range []*widgets.CheckBox{d.cbCompress, d.cbVerify, d.cbChecksum, d.cbCopyOnly} {
		if cb.HandleMouse(ev) {
			d.focusTo(cb)
			return true
		}
	}
	mx, my := ev.Position()
	if d.fDest.HitTest(mx, my) {
		d.focusTo(d.fDest)
		d.fDest.HandleMouse(ev)
		d.dragField = d.fDest
		return true
	}
	return true
}
