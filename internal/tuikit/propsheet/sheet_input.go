package propsheet

import (
	"github.com/gdamore/tcell/v3"
)

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

func (p *PropertySheet) HandleKey(ev *tcell.EventKey) bool {
	if !p.Visible() {
		return false
	}
	if ev.Key() == tcell.KeyF5 {
		p.Refresh(p.current)
		return true
	}
	// Escape cancels the whole sheet everywhere except zoneForm, where the
	// focused row gets first refusal — an open dropdown overlay consumes
	// Escape to close itself (see DropDown.HandleKey) rather than the
	// whole dialog vanishing out from under it. If the form doesn't want
	// the key, Escape falls through to cancel below, same as elsewhere.
	if ev.Key() == tcell.KeyEscape && p.zone != zoneForm {
		p.cancel()
		return true
	}

	switch p.zone {
	case zonePages:
		if p.pageList.HandleKey(ev) {
			return true
		}
		switch ev.Key() {
		case tcell.KeyTab, tcell.KeyRight, tcell.KeyEnter:
			p.setZone(zoneForm)
		case tcell.KeyBacktab:
			p.setZone(zoneButtons)
		}
	case zoneForm:
		if f := p.PageForm(p.current); f != nil && f.HandleKey(ev) {
			return true
		}
		switch ev.Key() {
		case tcell.KeyEscape:
			p.cancel()
			return true
		case tcell.KeyTab:
			p.setZone(zoneButtons)
		case tcell.KeyBacktab, tcell.KeyLeft:
			p.setZone(zonePages)
		}
	case zoneButtons:
		switch ev.Key() {
		case tcell.KeyLeft:
			if p.btnFocus > 0 {
				p.btnFocus--
			}
		case tcell.KeyRight:
			if p.btnFocus < len(sheetButtonLabels)-1 {
				p.btnFocus++
			}
		case tcell.KeyEnter:
			p.activateButton(p.btnFocus)
		case tcell.KeyTab:
			p.setZone(zonePages)
		case tcell.KeyBacktab:
			p.setZone(zoneForm)
		}
	}
	return true
}

func (p *PropertySheet) HandleMouse(ev *tcell.EventMouse) bool {
	if !p.Visible() {
		return false
	}
	// A release that lands outside the dialog is consumed by
	// ConsumeOutsideClick below before the current page's Form (and any
	// mouseDragging-latched Button/CheckBox row it hosts) or the page list
	// (its own mouseDragging latch) ever sees it, leaving the latch set and
	// swallowing the next press. Reset both here first; HandleMouse returns
	// false on ButtonNone so this has no other effect.
	if ev.Buttons() == tcell.ButtonNone {
		if f := p.PageForm(p.current); f != nil {
			f.HandleMouse(ev)
		}
		p.pageList.HandleMouse(ev)
	}
	if p.ConsumeOutsideClick(ev) {
		return true
	}
	// A focused row's open overlay (SelectRow's dropdown list, GridRow's
	// "Show Value" popup) is drawn last (see Form.DrawOverlays) and can
	// visually extend below the row's own band far enough to overlap the
	// button row or page list — give it first refusal here, same as
	// DataGrid.OverlayActive()/QueryPanel do one level down.
	if f := p.PageForm(p.current); f != nil && f.OverlayActive() {
		if f.HandleMouse(ev) {
			p.setZone(zoneForm)
			return true
		}
	}
	if i := p.ButtonClicked(ev, sheetButtonLabels); i >= 0 {
		p.setZone(zoneButtons)
		p.btnFocus = i
		p.activateButton(i)
		return true
	}
	if p.pageList.HandleMouse(ev) {
		p.setZone(zonePages)
		return true
	}
	if f := p.PageForm(p.current); f != nil && f.HandleMouse(ev) {
		p.setZone(zoneForm)
		return true
	}
	return true
}
