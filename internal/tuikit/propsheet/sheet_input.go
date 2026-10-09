package propsheet

import (
	"github.com/gdamore/tcell/v3"
)

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

// focusedRowHandles gives the focused row the chance to consume a key the sheet
// would otherwise take. Asked only for keys with a sheet-wide meaning, so it
// never reaches Form's focus cycling.
func (p *PropertySheet) focusedRowHandles(ev *tcell.EventKey) bool {
	f := p.PageForm(p.current)
	if f == nil {
		return false
	}
	kh, ok := f.Focused().(KeyHandler)
	return ok && kh.HandleKey(ev)
}

// While an Apply, OK or Script Changes is in flight the form takes no input
// (keys, mouse, paste, Refresh, Revert): the page's apply runs on the pipeline
// goroutine and reads the form's rows, so a concurrent edit raced it and could
// reach the server unvalidated. Apply reloads the page afterwards, so nothing
// typed then would survive anyway. The page list, the button row (only Cancel
// acts) and Escape still work. See formLocked.
func (p *PropertySheet) HandleKey(ev *tcell.EventKey) bool {
	if !p.Visible() {
		return false
	}
	if p.formLocked() && (ev.Key() == tcell.KeyF5 || ev.Key() == tcell.KeyCtrlZ) {
		return true
	}
	if ev.Key() == tcell.KeyF5 {
		p.Refresh(p.current)
		return true
	}
	// Ctrl+Z reverts the page to what it loaded with: the only way a user reaches
	// Form.Revert and the RevertFn closures behind it. Handled here, not in
	// zoneForm, so it works from the page list and button row too, and ahead of the
	// focused row as F5 is (widgets.InputField takes Ctrl+A and Ctrl+U but not
	// Ctrl+Z).
	//
	// The focused row still gets first refusal: EditorRow's controls.Editor has a
	// Ctrl+Z of its own, and without this an undo in a job step's T-SQL box reverted
	// the whole page. A read-only editor refuses the key (readOnlySafeKey), so a
	// non-T-SQL step still reverts.
	if ev.Key() == tcell.KeyCtrlZ {
		if p.zone == zoneForm && p.focusedRowHandles(ev) {
			return true
		}
		if p.RevertPage(p.current) {
			p.SetMessage("Reverted to the loaded values.", false)
		} else {
			p.SetMessage("Nothing to revert — no unsaved changes on this page.", false)
		}
		return true
	}
	// Escape cancels the whole sheet everywhere except zoneForm, where the focused
	// row gets first refusal: an open dropdown consumes Escape to close itself (see
	// DropDown.HandleKey) rather than the dialog vanishing from under it. If the
	// form doesn't want it, Escape falls through to cancel below.
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
		if p.formLocked() {
			switch ev.Key() {
			case tcell.KeyEscape:
				p.cancel()
			case tcell.KeyTab:
				p.setZone(zoneButtons)
			case tcell.KeyBacktab, tcell.KeyLeft:
				p.setZone(zonePages)
			}
			return true
		}
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
	// A release outside the dialog is consumed by ConsumeOutsideClick below before
	// the Form (and any mouseDragging-latched Button/CheckBox row) or page list
	// sees it, leaving the latch set and swallowing the next press. Reset both here
	// first; HandleMouse returns false on ButtonNone, so nothing else happens.
	if ev.Buttons() == tcell.ButtonNone {
		if f := p.PageForm(p.current); f != nil {
			f.HandleMouse(ev)
		}
		p.pageList.HandleMouse(ev)
		p.dragZone = zoneNone
	}
	// Everything from the press that armed dragZone through its release belongs to
	// the zone that claimed it, wherever the pointer drifted, including outside the
	// dialog, which is why this outranks ConsumeOutsideClick (see the field's doc).
	//
	// A wheel tick mid-gesture is swallowed, not routed: it isn't part of the
	// gesture, and routing would scroll whatever the pointer drifted over and call
	// setZone, moving the focus zone out from under a scrollbar drag. Same rule as
	// App.handleMouse's gestureOwner (internal/tui/app_events.go); App can't cover
	// this one since it dispatches the top dialog before its gesture check and never
	// arms a gesture for a dialog click.
	if p.dragZone != zoneNone {
		if ev.Buttons() == tcell.Button1 {
			p.routeDrag(ev)
		}
		return true
	}
	if p.ConsumeOutsideClick(ev) {
		return true
	}
	if p.formLocked() {
		// The button row and page list only; see HandleKey.
		if i := p.ButtonClicked(ev, p.buttonLabels()); i >= 0 {
			p.armDrag(ev, zoneButtons)
			p.setZone(zoneButtons)
			p.btnFocus = i
			p.activateButton(i)
		} else if p.pageList.HandleMouse(ev) {
			p.armDrag(ev, zonePages)
			p.setZone(zonePages)
		}
		return true
	}
	// A focused row's open overlay (SelectRow's dropdown, GridRow's "Show Value"
	// popup) draws last (see Form.DrawOverlays) and can extend below its band far
	// enough to overlap the button row or page list, so it gets first refusal here,
	// as DataGrid.OverlayActive()/QueryPanel do one level down.
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

// formLocked reports whether the form refuses input: while applying. See
// HandleKey.
func (p *PropertySheet) formLocked() bool { return p.applying }

// armDrag records that zone consumed a Button1 press, so every event until the
// release goes back to it (see dragZone).
func (p *PropertySheet) armDrag(ev *tcell.EventMouse, zone focusZone) {
	if ev.Buttons() == tcell.Button1 {
		p.dragZone = zone
	}
}

// routeDrag delivers a held-Button1 event to the zone that armed the gesture.
// zoneButtons swallows it: ModalDialog.ButtonClicked already fired on the press
// and its mouseDragging latch suppresses repeats; the point is that no other
// zone sees them.
func (p *PropertySheet) routeDrag(ev *tcell.EventMouse) {
	switch p.dragZone {
	case zoneForm:
		if f := p.PageForm(p.current); f != nil && !p.formLocked() {
			f.HandleMouse(ev)
		}
	case zonePages:
		p.pageList.HandleMouse(ev)
	}
}
