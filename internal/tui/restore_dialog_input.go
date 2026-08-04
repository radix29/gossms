package tui

import (
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// HandleKey routes keyboard events.
func (d *RestoreDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}

	switch d.mode {
	case restoreModeProgress:
		switch ev.Key() {
		case tcell.KeyEscape:
			d.Hide()
		case tcell.KeyEnter:
			d.btnFocus = core.Min(d.btnFocus, len(d.progressButtons())-1)
			d.doProgressButton()
		case tcell.KeyTab, tcell.KeyF1:
			d.btnFocus = (d.btnFocus + 1) % len(d.progressButtons())
		case tcell.KeyBacktab:
			n := len(d.progressButtons())
			d.btnFocus = (d.btnFocus - 1 + n) % n
		}
		return true
	case restoreModeInspect:
		switch ev.Key() {
		case tcell.KeyEscape:
			d.mode = restoreModeForm
			d.btnFocus = 0
			d.SetTitle("Restore Database")
		case tcell.KeyLeft:
			d.selectHeader(d.headerIdx - 1)
		case tcell.KeyRight:
			d.selectHeader(d.headerIdx + 1)
		case tcell.KeyEnter:
			d.doInspectButton()
		case tcell.KeyTab, tcell.KeyF1:
			d.btnFocus = (d.btnFocus + 1) % len(restoreInspectButtons)
		case tcell.KeyBacktab:
			n := len(restoreInspectButtons)
			d.btnFocus = (d.btnFocus - 1 + n) % n
		}
		return true
	}

	openDD := d.openDropDown()
	switch ev.Key() {
	case tcell.KeyTab:
		d.setFocus((d.focusIdx + 1) % len(d.focusable))
		return true
	case tcell.KeyBacktab:
		d.setFocus((d.focusIdx - 1 + len(d.focusable)) % len(d.focusable))
		return true
	case tcell.KeyEscape:
		if openDD != nil {
			openDD.HandleKey(ev)
			return true
		}
		d.Hide()
		return true
	case tcell.KeyEnter:
		if openDD != nil {
			openDD.HandleKey(ev)
			d.syncSourceState()
			return true
		}
		if b, ok := d.focusable[d.focusIdx].(*widgets.Button); ok {
			return b.HandleKey(ev)
		}
		d.doFormButton()
		return true
	case tcell.KeyF1:
		d.btnFocus = (d.btnFocus + 1) % len(restoreFormButtons)
		return true
	}

	if h, ok := d.focusable[d.focusIdx].(interface {
		HandleKey(*tcell.EventKey) bool
	}); ok {
		consumed := h.HandleKey(ev)
		d.syncSourceState()
		return consumed
	}
	return true
}

// openDropDown returns whichever history dropdown is currently open, if any.
func (d *RestoreDialog) openDropDown() *widgets.DropDown {
	if d.ddHistDB.IsOpen() {
		return d.ddHistDB
	}
	if d.ddHistSet.IsOpen() {
		return d.ddHistSet
	}
	return nil
}

// HandleMouse routes mouse events.
func (d *RestoreDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	// A release must reach every mouseDragging-latched widget even when it
	// lands outside the dialog (consumed below) or in a mode with an early
	// return above — otherwise its next press is swallowed as a
	// continuation of the stale drag. Each returns false on ButtonNone, so
	// this has no effect beyond resetting the latch.
	if ev.Buttons() == tcell.ButtonNone {
		d.rbSource.HandleMouse(ev)
		d.ddHistDB.HandleMouse(ev)
		d.ddHistSet.HandleMouse(ev)
		d.rbRecovery.HandleMouse(ev)
		d.btnBrowse.HandleMouse(ev)
		d.cbReplace.HandleMouse(ev)
		d.cbVerify.HandleMouse(ev)
		d.cbClose.HandleMouse(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}

	switch d.mode {
	case restoreModeProgress:
		if i := d.ButtonClicked(ev, d.progressButtons()); i >= 0 {
			d.btnFocus = i
			d.doProgressButton()
		}
		return true
	case restoreModeInspect:
		if i := d.ButtonClicked(ev, restoreInspectButtons); i >= 0 {
			d.btnFocus = i
			d.doInspectButton()
		}
		return true
	}

	if ev.Buttons() == tcell.ButtonNone {
		if f, ok := d.focusable[d.focusIdx].(*widgets.InputField); ok {
			f.HandleMouse(ev)
		}
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}

	histMode := d.rbSource.Selected() == 1

	// An open dropdown's list is an overlay drawn last, so it gets first
	// refusal of every click — ahead of ButtonClicked below, which would
	// otherwise steal a click landing on an open list row that happens to
	// visually overlap the button row.
	if histMode {
		if dd := d.openDropDown(); dd != nil && dd.HandleMouse(ev) {
			d.focusTo(dd)
			d.syncSourceState()
			return true
		}
	}

	if i := d.ButtonClicked(ev, restoreFormButtons); i >= 0 {
		d.btnFocus = i
		d.doFormButton()
		return true
	}

	if d.rbSource.HandleMouse(ev) {
		d.focusTo(d.rbSource)
		d.syncSourceState()
		return true
	}
	if histMode {
		for _, dd := range []*widgets.DropDown{d.ddHistDB, d.ddHistSet} {
			if dd.HandleMouse(ev) {
				d.focusTo(dd)
				d.syncSourceState()
				return true
			}
		}
	} else {
		if d.btnBrowse.HandleMouse(ev) {
			d.focusTo(d.btnBrowse)
			return true
		}
	}
	if d.rbRecovery.HandleMouse(ev) {
		d.focusTo(d.rbRecovery)
		return true
	}

	for _, cb := range []*widgets.CheckBox{d.cbReplace, d.cbVerify, d.cbClose} {
		if cb.HandleMouse(ev) {
			d.focusTo(cb)
			return true
		}
	}
	mx, my := ev.Position()
	fields := []*widgets.InputField{d.fTarget}
	if !histMode {
		fields = append(fields, d.fFile)
	}
	for _, f := range fields {
		if f.HitTest(mx, my) {
			d.focusTo(f)
			f.HandleMouse(ev)
			return true
		}
	}
	return true
}
