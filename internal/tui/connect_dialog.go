package tui

import (
	"strconv"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// focusable is satisfied by any tuikit widget that supports keyboard focus.
type focusable interface {
	Focus(bool)
}

// maxServerMatches caps how many rows of the server-field autocomplete
// list are drawn/hit-tested at once. Matches beyond this many simply
// aren't shown — the list doesn't scroll — so with up to
// config.MaxSavedConnections (30) saved entries, typing a longer prefix
// is how the rest are reached.
const maxServerMatches = 10

// ConnectDialog is the "Connect to Server" modal dialog. It embeds
// dialogs.ModalDialog (focus trap, overlay, button row) and composes
// tuikit/widgets controls for its form fields.
type ConnectDialog struct {
	dialogs.ModalDialog
	app *App

	fServer   *widgets.InputField
	fPort     *widgets.InputField
	fDatabase *widgets.InputField
	fUser     *widgets.InputField
	fPassword *widgets.InputField
	fTenantID *widgets.InputField
	fClientID *widgets.InputField
	ddAuth    *widgets.DropDown
	cbTrust   *widgets.CheckBox
	cbEncrypt *widgets.CheckBox

	// fExtraProps is a free-form, word-wrapped, editable text box: extra
	// "key=value" connection-string properties the user wants appended
	// verbatim, joined onto the built string with a leading "&" (see
	// refreshConnStrPreview / db.BuildConnectionString). Focusable and in
	// the Tab cycle.
	fExtraProps *controls.Editor

	// fConnStrPreview is a preview of the connection string that would be
	// built from the current fields, password shown masked (see
	// refreshConnStrPreview). Focusable and in the Tab cycle so its text
	// can be selected/copied; it's still rebuilt from the other fields on
	// every blur (see setFocus), so any manual edit made here doesn't
	// survive leaving the field.
	fConnStrPreview *controls.Editor

	// extraPropsLabelY and connStrLabelY are the row each editor's own
	// label is drawn on, computed by layoutFields and read back by Draw.
	extraPropsLabelY int
	connStrLabelY    int

	focusIdx  int
	focusable []focusable
	btnFocus  int // 0=Connect 1=Cancel

	// Server-field autocomplete: saved connections (config.Config,
	// most-recently-used and capped — see config.MaxSavedConnections)
	// whose Server matches what's currently typed in fServer, shown as a
	// list beneath it once at least 4 characters have been typed — or
	// immediately on a mouse click in the field, whatever its content
	// (see openMatchesForClick). A
	// connection is saved automatically, auto-named
	// "server,port,database,user", the moment it actually succeeds (see
	// App.connectServer).
	matches   []config.Connection
	matchOpen bool
	matchSel  int
}

// NewConnectDialog creates the connection dialog.
func NewConnectDialog(app *App) *ConnectDialog {
	d := &ConnectDialog{app: app}
	d.InitModal(app.screen, "Connect to Server", 62, 31)

	methods := config.AllAuthMethods()
	authItems := make([]string, len(methods))
	for i, m := range methods {
		authItems[i] = config.AuthMethodName(m)
	}

	d.fServer = widgets.NewInputField("Server:  ", 38, false)
	d.fPort = widgets.NewInputField("Port:    ", 6, false)
	d.fPort.SetValue("1433")
	d.fDatabase = widgets.NewInputField("Database:", 38, false)
	d.fUser = widgets.NewInputField("User:    ", 38, false)
	d.fPassword = widgets.NewInputField("Password:", 38, true)
	d.fTenantID = widgets.NewInputField("TenantID:", 38, false)
	d.fClientID = widgets.NewInputField("ClientID:", 38, false)
	d.ddAuth = widgets.NewDropDown("Auth:    ", authItems, 38)
	d.cbTrust = widgets.NewCheckBox("Trust Server Certificate")
	d.cbTrust.SetChecked(true)
	d.cbEncrypt = widgets.NewCheckBox("Encrypt Connection")

	d.fExtraProps = controls.NewEditor(nil)
	d.fExtraProps.SetGutterVisible(false)
	d.fExtraProps.SetWrapMode(true)

	d.fConnStrPreview = controls.NewEditor(nil)
	d.fConnStrPreview.SetGutterVisible(false)
	d.fConnStrPreview.SetWrapMode(true)

	d.rebuildFocusable()
	return d
}

func (d *ConnectDialog) rebuildFocusable() {
	d.focusable = []focusable{
		d.fServer, d.fPort, d.ddAuth, d.fDatabase,
		d.fUser, d.fPassword, d.fTenantID, d.fClientID,
		d.cbTrust, d.cbEncrypt, d.fExtraProps, d.fConnStrPreview,
	}
}

// PreFill pre-fills the dialog from an existing connection. Used by
// applyMatch when a server-field autocomplete suggestion is chosen.
func (d *ConnectDialog) PreFill(c *config.Connection) {
	d.fServer.SetValue(c.Server)
	d.fPort.SetValue(strconv.Itoa(c.Port))
	d.fDatabase.SetValue(c.Database)
	d.fUser.SetValue(c.User)
	d.fPassword.SetValue(c.Password)
	d.fTenantID.SetValue(c.TenantID)
	d.fClientID.SetValue(c.ClientID)
	d.cbTrust.SetChecked(c.TrustServerCertificate)
	d.cbEncrypt.SetChecked(c.Encrypt)
	d.fExtraProps.SetText(c.ExtraProperties)
	for i, m := range config.AllAuthMethods() {
		if m == c.AuthMethod {
			d.ddAuth.SetSelected(i)
			break
		}
	}
	d.setFocus(0)
}

// Show opens the dialog, focuses the first field, and refreshes the
// server-match list against whatever's already in it — fields persist
// across Show/Hide, so a dialog reopened with a server already typed in
// should immediately reflect that.
func (d *ConnectDialog) Show() {
	d.ModalDialog.Show()
	d.setFocus(0)
	d.updateMatches()
}

func (d *ConnectDialog) setFocus(i int) {
	for _, f := range d.focusable {
		f.Focus(false)
	}
	if i >= 0 && i < len(d.focusable) {
		d.focusIdx = i
		d.focusable[i].Focus(true)
	}
	// The autocomplete list only makes sense while the server field has
	// focus; moving away from it closes the list.
	if i != 0 {
		d.matchOpen = false
	}
	// Every focus change is a blur of whatever was focused before — the
	// natural point to refresh the preview so it updates once a field is
	// left, not on every keystroke inside it.
	d.refreshConnStrPreview()
}

// connStrPasswordMask replaces a non-empty password in the connection-
// string preview — a fixed placeholder rather than a run of '*' the same
// length as the real password, so the preview can't leak its length.
const connStrPasswordMask = "XXXXX"

// refreshConnStrPreview rebuilds the connection-string preview from the
// current field values. The real password is never written into the
// preview — see connStrPasswordMask.
func (d *ConnectDialog) refreshConnStrPreview() {
	opts := d.currentOptions()
	if opts.Password != "" {
		opts.Password = connStrPasswordMask
	}
	d.fConnStrPreview.SetText(db.BuildConnectionString(opts))
}

// updateMatches re-runs the server-field autocomplete lookup against
// whatever's currently typed in fServer. Nothing happens until at least
// 4 characters have been typed; the list closes itself the moment there
// are no matches (or the field shrinks back below 4 characters).
func (d *ConnectDialog) updateMatches() {
	typed := d.fServer.Value()
	if len(typed) < 4 {
		d.matchOpen = false
		d.matches = nil
		return
	}
	d.matches = d.app.cfg.MatchByServer(typed)
	if len(d.matches) == 0 {
		d.matchOpen = false
		return
	}
	if d.matchSel < 0 || d.matchSel >= len(d.matches) {
		d.matchSel = 0
	}
	d.matchOpen = true
}

// openMatchesForClick opens the saved-connections list on a mouse click in
// the server field, regardless of how much has been typed — an empty field
// lists every saved connection. Typing afterwards re-filters through
// updateMatches, which restores its usual 4-character threshold.
func (d *ConnectDialog) openMatchesForClick() {
	d.matches = d.app.cfg.MatchByServer(d.fServer.Value())
	if len(d.matches) == 0 {
		d.matchOpen = false
		return
	}
	if d.matchSel < 0 || d.matchSel >= len(d.matches) {
		d.matchSel = 0
	}
	d.matchOpen = true
}

// applyMatch fills the dialog from a saved connection chosen off the
// server-field autocomplete list, via arrow+Enter or a click.
func (d *ConnectDialog) applyMatch(c config.Connection) {
	d.PreFill(&c)
	d.matchOpen = false
}

// Draw renders the dialog.
func (d *ConnectDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	d.layoutFields()

	inner := d.InnerRect()
	p := theme.Active()
	labelStyle := tcell.StyleDefault.Background(p.DialogBg).Foreground(p.Text)
	core.DrawText(s, inner.X+1, inner.Y+1, labelStyle, "Server Type: SQL Server Database Engine")

	d.fServer.Draw(s)
	d.fPort.Draw(s)
	d.ddAuth.Draw(s)
	d.fDatabase.Draw(s)
	d.fUser.Draw(s)
	d.fPassword.Draw(s)

	authMethod := config.AllAuthMethods()[d.ddAuth.Selected()]
	if authMethod >= config.AuthEntraDefault {
		d.fTenantID.Draw(s)
		d.fClientID.Draw(s)
	}

	d.cbTrust.Draw(s)
	d.cbEncrypt.Draw(s)

	core.DrawText(s, inner.X+1, d.extraPropsLabelY, labelStyle, "Extra Properties:")
	d.fExtraProps.Draw(s)

	core.DrawText(s, inner.X+1, d.connStrLabelY, labelStyle, "Connection String:")
	d.fConnStrPreview.Draw(s)

	d.DrawSeparator(s)
	d.DrawButtons(s, []string{"Connect", "Cancel"}, d.btnFocus)

	// Drawn last so neither the open auth-method list nor the server-match
	// list gets painted over by the fields/buttons positioned below them.
	d.ddAuth.DrawOverlay(s)
	d.drawMatches(s)
}

// drawMatches renders the server-field autocomplete list as an overlay
// directly beneath fServer, styled to match ddAuth's own open list
// (DropDown.DrawOverlay) for visual consistency. Like any combo-box
// suggestion list, it's necessarily drawn on top of whatever's positioned
// below fServer (fPort, ddAuth, etc.) while it's open.
func (d *ConnectDialog) drawMatches(s tcell.Screen) {
	if !d.matchOpen || len(d.matches) == 0 {
		return
	}
	p := theme.Active()
	listStyle := tcell.StyleDefault.Background(p.MenuBar).Foreground(p.Text)
	selStyle := theme.StyleSelected()

	x := d.fServer.InputX() + 1 // +1: past the '[' border, onto the text
	y := d.fServer.RectY() + 1
	w := d.fServer.Width()
	n := core.Min(len(d.matches), maxServerMatches)
	for i := 0; i < n; i++ {
		st := listStyle
		if i == d.matchSel {
			st = selStyle
		}
		core.FillRect(s, core.Rect{X: x, Y: y + i, W: w, H: 1}, ' ', st)
		core.DrawTextClipped(s, x, y+i, w, st, d.matches[i].Name)
	}
}

func (d *ConnectDialog) layoutFields() {
	inner := d.InnerRect()
	lx := inner.X + 1
	row := inner.Y + 3
	d.fServer.SetBounds(lx, row)
	row++
	d.fPort.SetBounds(lx, row)
	row++
	d.ddAuth.SetBounds(lx, row)
	row++
	d.fDatabase.SetBounds(lx, row)
	row++
	d.fUser.SetBounds(lx, row)
	row++
	d.fPassword.SetBounds(lx, row)
	row++

	authMethod := config.AllAuthMethods()[d.ddAuth.Selected()]
	if authMethod >= config.AuthEntraDefault {
		d.fTenantID.SetBounds(lx, row)
		row++
		d.fClientID.SetBounds(lx, row)
		row++
	} else {
		row += 2
	}
	row++ // blank row above Trust Server Certificate
	d.cbTrust.SetBounds(lx, row)
	row++
	d.cbEncrypt.SetBounds(lx, row)
	row++
	row++ // blank row below Encrypt Connection

	// Same on-screen width as the Password field's whole visible box
	// (label + brackets + content), computed from real widget geometry.
	previewW := d.fPassword.InputX() + d.fPassword.Width() + 2 - lx

	d.extraPropsLabelY = row
	row++
	d.fExtraProps.SetBounds(lx, row, previewW, 4)
	row += 4

	d.connStrLabelY = row
	row++
	d.fConnStrPreview.SetBounds(lx, row, previewW, 4)
}

// currentOptions assembles a config.Connection from the dialog fields.
// Name is left as the zero value; config.Config.AddOrUpdate fills in the
// auto-generated name once a connection actually succeeds.
func (d *ConnectDialog) currentOptions() config.Connection {
	port, _ := strconv.Atoi(d.fPort.Value())
	if port == 0 {
		port = 1433
	}
	authMethod := config.AllAuthMethods()[d.ddAuth.Selected()]
	return config.Connection{
		Server:                 d.fServer.Value(),
		Port:                   port,
		Database:               d.fDatabase.Value(),
		AuthMethod:             authMethod,
		User:                   d.fUser.Value(),
		Password:               d.fPassword.Value(),
		TenantID:               d.fTenantID.Value(),
		ClientID:               d.fClientID.Value(),
		TrustServerCertificate: d.cbTrust.Checked(),
		Encrypt:                d.cbEncrypt.Checked(),
		ExtraProperties:        d.fExtraProps.Text(),
	}
}

// HandleKey routes keyboard events.
func (d *ConnectDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}

	// While the server-field autocomplete list is open, arrows navigate
	// it and Enter/Escape act on it — taking priority over the field's
	// own key handling and the dialog's usual Tab-cycling/Enter-confirms.
	if d.matchOpen && d.focusIdx == 0 {
		switch ev.Key() {
		case tcell.KeyDown:
			if d.matchSel < len(d.matches)-1 {
				d.matchSel++
			}
			return true
		case tcell.KeyUp:
			if d.matchSel > 0 {
				d.matchSel--
			}
			return true
		case tcell.KeyEnter:
			d.applyMatch(d.matches[d.matchSel])
			return true
		case tcell.KeyEscape:
			d.matchOpen = false
			return true
		}
	}

	switch ev.Key() {
	case tcell.KeyTab:
		d.setFocus((d.focusIdx + 1) % len(d.focusable))
		return true
	case tcell.KeyBacktab:
		d.setFocus((d.focusIdx - 1 + len(d.focusable)) % len(d.focusable))
		return true
	case tcell.KeyEscape:
		d.Hide()
		return true
	case tcell.KeyEnter:
		if d.ddAuth.IsOpen() {
			d.ddAuth.HandleKey(ev)
			return true
		}
		d.doButton()
		return true
	case tcell.KeyF1:
		d.btnFocus = (d.btnFocus + 1) % 2
		return true
	}

	if d.focusIdx < len(d.focusable) {
		switch w := d.focusable[d.focusIdx].(type) {
		case *widgets.InputField:
			consumed := w.HandleKey(ev)
			if w == d.fServer {
				d.updateMatches()
			}
			return consumed
		case *widgets.DropDown:
			consumed := w.HandleKey(ev)
			d.refreshConnStrPreview()
			return consumed
		case *widgets.CheckBox:
			consumed := w.HandleKey(ev)
			d.refreshConnStrPreview()
			return consumed
		case *controls.Editor:
			return w.HandleKey(ev)
		}
	}
	return true
}

func (d *ConnectDialog) doButton() {
	switch d.btnFocus {
	case 0: // Connect
		opts := d.currentOptions()
		d.Hide()
		d.app.connectServer(opts)
	case 1: // Cancel
		d.Hide()
	}
}

// HandleMouse routes mouse events; the embedded ModalDialog blocks all
// clicks outside its bounds via ConsumeOutsideClick.
func (d *ConnectDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	// A release must reach every mouseDragging-latched widget even when it
	// lands outside the dialog (consumed below) or the widget isn't the
	// currently focused one — otherwise its next press is swallowed as a
	// continuation of the stale drag. Each returns false on ButtonNone, so
	// this has no effect beyond resetting the latch.
	if ev.Buttons() == tcell.ButtonNone {
		d.cbTrust.HandleMouse(ev)
		d.cbEncrypt.HandleMouse(ev)
		d.ddAuth.HandleMouse(ev)
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}

	// Always forward a release to whichever field currently has focus, so
	// a text-selection drag started in it terminates cleanly even if the
	// release happens elsewhere in the dialog.
	if ev.Buttons() == tcell.ButtonNone {
		if d.focusIdx < len(d.focusable) {
			switch f := d.focusable[d.focusIdx].(type) {
			case *widgets.InputField:
				f.HandleMouse(ev)
			case *controls.Editor:
				f.HandleMouse(ev)
			}
		}
		return true
	}

	if ev.Buttons() != tcell.Button1 {
		return false
	}

	// The match list is an overlay drawn last (over fPort/ddAuth/etc.), so
	// it's hit-tested first.
	if d.matchOpen && len(d.matches) > 0 {
		if i, ok := d.matchHit(ev); ok {
			d.applyMatch(d.matches[i])
			return true
		}
		d.matchOpen = false
	}

	if i := d.ButtonClicked(ev, []string{"Connect", "Cancel"}); i >= 0 {
		d.btnFocus = i
		d.doButton()
		return true
	}

	if d.ddAuth.HandleMouse(ev) {
		d.refreshConnStrPreview()
		return true
	}

	if d.cbTrust.HandleMouse(ev) {
		d.refreshConnStrPreview()
		return true
	}
	if d.cbEncrypt.HandleMouse(ev) {
		d.refreshConnStrPreview()
		return true
	}

	// fExtraProps/fConnStrPreview.HandleMouse itself checks whether the
	// click falls within its bounds (Editor has no separate HitTest), so
	// it doubles as the hit test here.
	for _, ed := range []*controls.Editor{d.fExtraProps, d.fConnStrPreview} {
		if ed.HandleMouse(ev) {
			for fi, foc := range d.focusable {
				if foc == ed {
					d.setFocus(fi)
					break
				}
			}
			return true
		}
	}

	mx, my := ev.Position()
	fields := []*widgets.InputField{
		d.fServer, d.fPort, d.fDatabase, d.fUser, d.fPassword,
		d.fTenantID, d.fClientID,
	}
	for _, f := range fields {
		if f.HitTest(mx, my) {
			for fi, foc := range d.focusable {
				if foc == f {
					d.setFocus(fi)
					break
				}
			}
			// Position the cursor / start a drag-selection at the click
			// point, not just switch focus to the field.
			f.HandleMouse(ev)
			if f == d.fServer {
				d.openMatchesForClick()
			}
			return true
		}
	}
	return true
}

// matchHit reports which server-match-list row (if any) contains the
// click, as an index into d.matches.
func (d *ConnectDialog) matchHit(ev *tcell.EventMouse) (int, bool) {
	mx, my := ev.Position()
	x := d.fServer.InputX() + 1
	y := d.fServer.RectY() + 1
	w := d.fServer.Width()
	n := core.Min(len(d.matches), maxServerMatches)
	if my < y || my >= y+n || mx < x || mx >= x+w {
		return 0, false
	}
	return my - y, true
}
