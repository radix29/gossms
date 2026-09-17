package tui

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// ---------------------------------------------------------------------------
// Input
// ---------------------------------------------------------------------------

// HandleKey routes keyboard events.
func (d *ConnectDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}

	// While an attempt is in flight every control but Cancel is inert, so the
	// fields can't be edited out from under the connection being made. Escape
	// and Enter both reach Cancel, which is where focus already is; everything
	// else is swallowed rather than passed on, so Tab can't walk into a
	// disabled field.
	if d.connecting {
		switch ev.Key() {
		case tcell.KeyEscape, tcell.KeyEnter:
			d.btnFocus = connectBtnCancel
			d.doButton()
		}
		return true
	}

	// The tabs answer the same keys the query panel's result tabs do.
	switch {
	case ev.Key() == tcell.KeyPgUp && ev.Modifiers()&tcell.ModCtrl != 0:
		d.setTab(connectTabProperties)
		return true
	case ev.Key() == tcell.KeyPgDn && ev.Modifiers()&tcell.ModCtrl != 0:
		d.setTab(connectTabString)
		return true
	}

	// The History list owns the arrows and Enter while it has focus, ahead of
	// the dialog's own Enter-confirms: Enter there connects to the highlighted
	// saved connection.
	if d.focusedWidget() == d.history && d.history.HandleKey(ev) {
		return true
	}

	switch ev.Key() {
	case tcell.KeyTab:
		d.stepFocus(+1)
		return true
	case tcell.KeyBacktab:
		d.stepFocus(-1)
		return true
	case tcell.KeyEscape:
		d.Hide()
		return true
	case tcell.KeyEnter:
		if d.ddAuth.IsOpen() {
			d.ddAuth.HandleKey(ev)
			d.applyAuthFields()
			d.refreshConnStrPreview()
			return true
		}
		if d.ddEncrypt.IsOpen() {
			d.ddEncrypt.HandleKey(ev)
			d.refreshConnStrPreview()
			return true
		}
		d.doButton()
		return true
	case tcell.KeyF1:
		d.btnFocus = (d.btnFocus + 1) % connectButtonCount
		return true
	}

	switch w := d.focusedWidget().(type) {
	case *widgets.InputField:
		return w.HandleKey(ev)
	case *widgets.DropDown:
		consumed := w.HandleKey(ev)
		if w == d.ddAuth {
			d.applyAuthFields()
		}
		d.refreshConnStrPreview()
		return consumed
	case *widgets.CheckBox:
		consumed := w.HandleKey(ev)
		d.refreshConnStrPreview()
		return consumed
	case *controls.Editor:
		return w.HandleKey(ev)
	}
	return true
}

// doButton runs the focused button. Every button but Cancel refuses to act
// while it is drawn gated, so a click or an Enter on a greyed button does
// nothing rather than quietly working anyway.
func (d *ConnectDialog) doButton() {
	if gated := d.buttonsDisabled(); d.btnFocus >= 0 && d.btnFocus < len(gated) && gated[d.btnFocus] {
		if d.btnFocus == connectBtnConnect && !d.connecting {
			d.app.setStatus("Enter a server name to connect")
		}
		return
	}
	switch d.btnFocus {
	case connectBtnConnect:
		if _, _, ok := d.serverParts(); !ok {
			d.app.alertDialog.ShowAlert("Connect",
				fmt.Sprintf("The port in %q must be a number from 1 to 65535",
					strings.TrimSpace(d.fServer.Value())))
			return
		}
		d.startConnect(d.currentOptions())
	case connectBtnReset:
		d.resetForm()
	case connectBtnDelete:
		d.deleteSelectedHistory()
	case connectBtnCancel:
		d.Hide()
	}
}

// buttonClicked is ModalDialog.ButtonClicked over both button groups,
// answering an index into the whole row. A call that hits nothing latches
// nothing, so trying the left group first costs the right one nothing.
func (d *ConnectDialog) buttonClicked(ev *tcell.EventMouse) int {
	if i := d.ButtonClickedAt(ev, d.buttonRowLeftX(), connectLeftButtons); i >= 0 {
		return i
	}
	if i := d.ButtonClickedAt(ev, d.ButtonRowStartX(connectRightButtons), connectRightButtons); i >= 0 {
		return i + len(connectLeftButtons)
	}
	return -1
}

// HandleMouse routes mouse events; the embedded ModalDialog blocks clicks
// outside its bounds via ConsumeOutsideClick.
func (d *ConnectDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	// A release must reach every mouseDragging-latched widget even when it lands
	// outside the dialog or on a widget that isn't focused, or its next press is
	// swallowed as a continuation of the stale drag. Each returns false on
	// ButtonNone, so this does nothing beyond resetting the latch.
	if ev.Buttons() == tcell.ButtonNone {
		d.cbTrust.HandleMouse(ev)
		d.cbRemember.HandleMouse(ev)
		d.ddAuth.HandleMouse(ev)
		d.ddEncrypt.HandleMouse(ev)
		d.history.HandleMouse(ev)
		// End a text-selection drag in the field that claimed the press,
		// wherever the release landed. Before ConsumeOutsideClick, which returns
		// early on a release outside the dialog and would strand the latch.
		d.drag.Release(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}

	// Always forward a release to whichever field has focus, so a text-selection
	// drag started in it ends cleanly even if the release lands elsewhere in the
	// dialog. After the d.drag release above because the gesture tracks only
	// InputFields, while Editor keeps its own latch.
	if ev.Buttons() == tcell.ButtonNone {
		switch f := d.focusedWidget().(type) {
		case *widgets.InputField:
			f.HandleMouse(ev)
		case *controls.Editor:
			f.HandleMouse(ev)
		}
		return true
	}

	// The wheel scrolls whatever it is over, without moving focus there.
	if ev.Buttons() == tcell.WheelUp || ev.Buttons() == tcell.WheelDown {
		if d.twoPane && d.history.HandleMouse(ev) {
			return true
		}
		for _, ed := range d.visibleEditors() {
			if ed.HandleMouse(ev) {
				return true
			}
		}
		return true
	}

	if ev.Buttons() != tcell.Button1 {
		return false
	}

	// While an attempt is in flight only Cancel answers a click — same gating
	// as HandleKey, and ahead of every field hit-test below.
	if d.connecting {
		if i := d.buttonClicked(ev); i == connectBtnCancel {
			d.btnFocus = connectBtnCancel
			d.doButton()
		}
		return true
	}

	// The gesture belongs to whichever field claimed its press, so motion is
	// replayed there without hit-testing — ahead of every widget below, none of
	// which can own a gesture this one started.
	if d.drag.Replay(ev) {
		return true
	}

	// A dropdown's open list is an overlay drawn last, so it gets first
	// refusal of every click — ahead of ButtonClicked, which would otherwise
	// steal a click on a list row overlapping the button row. The open one
	// first: its list may cover the other dropdown's own row.
	if d.tab == connectTabProperties {
		dropdowns := []*widgets.DropDown{d.ddAuth, d.ddEncrypt}
		if d.ddEncrypt.IsOpen() {
			dropdowns = []*widgets.DropDown{d.ddEncrypt, d.ddAuth}
		}
		for _, dd := range dropdowns {
			if dd.HandleMouse(ev) {
				if dd == d.ddAuth {
					d.applyAuthFields()
				}
				d.refreshConnStrPreview()
				return true
			}
		}
	}

	if i := d.buttonClicked(ev); i >= 0 {
		d.btnFocus = i
		d.doButton()
		return true
	}

	if d.tabClicked(ev) {
		return true
	}

	mx, my := ev.Position()
	if d.twoPane && d.history.HandleMouse(ev) {
		d.setFocus(indexOfFocusable(d.focusable, d.history))
		return true
	}

	if d.tab == connectTabProperties {
		for _, cb := range []*widgets.CheckBox{d.cbTrust, d.cbRemember} {
			if cb.HandleMouse(ev) {
				d.setFocus(indexOfFocusable(d.focusable, cb))
				d.refreshConnStrPreview()
				return true
			}
		}
	}

	// Editor.HandleMouse checks its own bounds (Editor has no separate
	// HitTest), so it doubles as the hit test.
	for _, ed := range d.visibleEditors() {
		if ed.HandleMouse(ev) {
			d.setFocus(indexOfFocusable(d.focusable, ed))
			return true
		}
	}

	if d.tab == connectTabProperties {
		fields := []*widgets.InputField{
			d.fServer, d.fUser, d.fPassword, d.fTenantID, d.fClientID,
			d.fDatabase, d.fHostCert,
		}
		for _, f := range fields {
			if f.HitTest(mx, my) {
				if !f.Enabled() {
					// Greyed out for this auth method: a click neither focuses
					// it nor starts a selection in it.
					return true
				}
				d.setFocus(indexOfFocusable(d.focusable, f))
				// Position the cursor or start a drag-selection at the click
				// point, not just switch focus to the field.
				d.drag.Claim(f, ev)
				return true
			}
		}
	}
	return true
}

// visibleEditors is the editors the active tab shows, in focus order. The
// Custom Properties editor is on both tabs.
func (d *ConnectDialog) visibleEditors() []*controls.Editor {
	if d.tab == connectTabString {
		return []*controls.Editor{d.fConnStrPreview, d.fExtraProps}
	}
	return []*controls.Editor{d.fExtraProps}
}

// tabClicked switches tabs on a click in the tab bar, and reports whether it
// consumed the event.
func (d *ConnectDialog) tabClicked(ev *tcell.EventMouse) bool {
	mx, my := ev.Position()
	if my != d.tabRect.Y {
		return false
	}
	for i, seg := range d.tabSegments() {
		if mx >= seg[0].X && mx < seg[0].X+seg[0].W {
			d.setTab(connectTab(i))
			return true
		}
	}
	return false
}
