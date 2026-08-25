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
	// Ctrl+Z reverts the page to what it loaded with — the only way a user
	// reaches Form.Revert and the 21 RevertFn closures behind it. Handled here
	// rather than in zoneForm so it works from the page list and button row
	// too, and ahead of the focused row for the same reason F5 is: no row wants
	// it. Nothing inside a form row does either — widgets.InputField takes
	// Ctrl+A and Ctrl+U and no propsheet row hosts a controls.Editor, which is
	// the one widget with its own Ctrl+Z.
	if ev.Key() == tcell.KeyCtrlZ {
		if p.RevertPage(p.current) {
			p.SetMessage("Reverted to the loaded values.", false)
		} else {
			p.SetMessage("Nothing to revert — no unsaved changes on this page.", false)
		}
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
			if p.btnFocus < len(p.buttonLabels())-1 {
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
		p.dragZone = zoneNone
	}
	// Everything from the press that armed dragZone through to its release
	// belongs to the zone that claimed it, wherever the pointer has drifted
	// to since — including outside the dialog, which is why this outranks
	// ConsumeOutsideClick. See the field's doc comment.
	//
	// A wheel tick arriving mid-gesture is swallowed rather than routed: it
	// isn't part of the gesture, and letting it fall through hands it to the
	// positional routing below, which both scrolls whatever the pointer has
	// drifted over and calls setZone — so wheeling while dragging the form's
	// scrollbar moved the focus zone out from under the drag. Same rule as
	// App.handleMouse's gestureOwner (internal/tui/app_events.go); App can't
	// cover this one, since it dispatches the top dialog before its own
	// gesture check and never arms a gesture for a dialog click.
	if p.dragZone != zoneNone {
		if ev.Buttons() == tcell.Button1 {
			p.routeDrag(ev)
		}
		return true
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
			p.armDrag(ev, zoneForm)
			p.setZone(zoneForm)
			return true
		}
	}
	if i := p.ButtonClicked(ev, p.buttonLabels()); i >= 0 {
		p.armDrag(ev, zoneButtons)
		p.setZone(zoneButtons)
		p.btnFocus = i
		p.activateButton(i)
		return true
	}
	if p.pageList.HandleMouse(ev) {
		p.armDrag(ev, zonePages)
		p.setZone(zonePages)
		return true
	}
	if f := p.PageForm(p.current); f != nil && f.HandleMouse(ev) {
		p.armDrag(ev, zoneForm)
		p.setZone(zoneForm)
		return true
	}
	return true
}

// armDrag records that zone consumed a Button1 press, so every further
// event until the release goes back to it — see the dragZone field.
func (p *PropertySheet) armDrag(ev *tcell.EventMouse, zone focusZone) {
	if ev.Buttons() == tcell.Button1 {
		p.dragZone = zone
	}
}

// routeDrag delivers a held-Button1 event to the zone that armed the
// gesture. zoneButtons swallows it: ModalDialog.ButtonClicked already fired
// the action on the press and its mouseDragging latch suppresses the
// repeats, so there's nothing further to deliver — the point is only that
// no other zone sees them either.
func (p *PropertySheet) routeDrag(ev *tcell.EventMouse) {
	switch p.dragZone {
	case zoneForm:
		if f := p.PageForm(p.current); f != nil {
			f.HandleMouse(ev)
		}
	case zonePages:
		p.pageList.HandleMouse(ev)
	}
}
