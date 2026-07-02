package tui

import (
	"strconv"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// focusable is satisfied by any tuikit widget that supports keyboard focus.
type focusable interface {
	Focus(bool)
}

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
	fConnName *widgets.InputField
	ddAuth    *widgets.DropDown
	cbTrust   *widgets.CheckBox
	cbEncrypt *widgets.CheckBox

	focusIdx  int
	focusable []focusable
	btnFocus  int // 0=Connect 1=Cancel 2=ConnStr
}

// NewConnectDialog creates the connection dialog.
func NewConnectDialog(app *App) *ConnectDialog {
	d := &ConnectDialog{app: app}
	d.InitModal(app.screen, "Connect to Server", 62, 28)

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
	d.fConnName = widgets.NewInputField("Save As: ", 38, false)
	d.ddAuth = widgets.NewDropDown("Auth:    ", authItems, 38)
	d.cbTrust = widgets.NewCheckBox("Trust Server Certificate")
	d.cbTrust.SetChecked(true)
	d.cbEncrypt = widgets.NewCheckBox("Encrypt Connection")

	d.rebuildFocusable()
	return d
}

func (d *ConnectDialog) rebuildFocusable() {
	d.focusable = []focusable{
		d.fServer, d.fPort, d.ddAuth, d.fDatabase,
		d.fUser, d.fPassword, d.fTenantID, d.fClientID,
		d.fConnName, d.cbTrust, d.cbEncrypt,
	}
}

// PreFill pre-fills the dialog from an existing connection.
func (d *ConnectDialog) PreFill(c *config.Connection) {
	d.fServer.SetValue(c.Server)
	d.fPort.SetValue(strconv.Itoa(c.Port))
	d.fDatabase.SetValue(c.Database)
	d.fUser.SetValue(c.User)
	d.fPassword.SetValue(c.Password)
	d.fTenantID.SetValue(c.TenantID)
	d.fClientID.SetValue(c.ClientID)
	d.fConnName.SetValue(c.Name)
	d.cbTrust.SetChecked(c.TrustServerCertificate)
	d.cbEncrypt.SetChecked(c.Encrypt)
	for i, m := range config.AllAuthMethods() {
		if m == c.AuthMethod {
			d.ddAuth.SetSelected(i)
			break
		}
	}
	d.setFocus(0)
}

// Show opens the dialog and focuses the first field.
func (d *ConnectDialog) Show() {
	d.ModalDialog.Show()
	d.setFocus(0)
}

func (d *ConnectDialog) setFocus(i int) {
	for _, f := range d.focusable {
		f.Focus(false)
	}
	if i >= 0 && i < len(d.focusable) {
		d.focusIdx = i
		d.focusable[i].Focus(true)
	}
}

// Draw renders the dialog.
func (d *ConnectDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	d.layoutFields()

	inner := d.InnerRect()
	labelStyle := tcell.StyleDefault
	core.DrawText(s, inner.X+1, inner.Y+1, labelStyle, "Server Type: Database Engine")

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

	d.fConnName.Draw(s)
	d.cbTrust.Draw(s)
	d.cbEncrypt.Draw(s)

	d.DrawSeparator(s)
	d.DrawButtons(s, []string{"Connect", "Cancel", "Conn Str"}, d.btnFocus)

	// Drawn last so the open auth-method list isn't painted over by the
	// fields positioned below it (fDatabase, fUser, fPassword, etc.).
	d.ddAuth.DrawOverlay(s)
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
	d.fConnName.SetBounds(lx, row)
	row++
	d.cbTrust.SetBounds(lx, row)
	row++
	d.cbEncrypt.SetBounds(lx, row)
}

// currentOptions assembles a config.Connection from the dialog fields.
func (d *ConnectDialog) currentOptions() config.Connection {
	port, _ := strconv.Atoi(d.fPort.Value())
	if port == 0 {
		port = 1433
	}
	authMethod := config.AllAuthMethods()[d.ddAuth.Selected()]
	return config.Connection{
		Name:                   d.fConnName.Value(),
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
	}
}

// HandleKey routes keyboard events.
func (d *ConnectDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
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
		d.btnFocus = (d.btnFocus + 1) % 3
		return true
	}

	if d.focusIdx < len(d.focusable) {
		switch w := d.focusable[d.focusIdx].(type) {
		case *widgets.InputField:
			return w.HandleKey(ev)
		case *widgets.DropDown:
			return w.HandleKey(ev)
		case *widgets.CheckBox:
			return w.HandleKey(ev)
		}
	}
	return true
}

func (d *ConnectDialog) doButton() {
	switch d.btnFocus {
	case 0: // Connect
		opts := d.currentOptions()
		if opts.Name != "" {
			d.app.cfg.AddOrUpdate(opts)
			_ = d.app.cfg.Save()
		}
		d.Hide()
		d.app.connectServer(opts)
	case 1: // Cancel
		d.Hide()
	case 2: // Conn Str
		opts := d.currentOptions()
		d.app.connStrDialog.SetConnStr(db.BuildConnectionString(opts))
		d.app.connStrDialog.Show()
	}
}

// HandleMouse routes mouse events; the embedded ModalDialog blocks all
// clicks outside its bounds via ConsumeOutsideClick.
func (d *ConnectDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if ev.Buttons() != tcell.Button1 {
		return false
	}

	if i := d.ButtonClicked(ev, []string{"Connect", "Cancel", "Conn Str"}); i >= 0 {
		d.btnFocus = i
		d.doButton()
		return true
	}

	if d.ddAuth.HandleMouse(ev) {
		return true
	}

	mx, my := ev.Position()
	if my == d.cbTrust.RectY() && mx >= d.cbTrust.RectX() && mx < d.cbTrust.RectX()+3 {
		d.cbTrust.SetChecked(!d.cbTrust.Checked())
		return true
	}
	if my == d.cbEncrypt.RectY() && mx >= d.cbEncrypt.RectX() && mx < d.cbEncrypt.RectX()+3 {
		d.cbEncrypt.SetChecked(!d.cbEncrypt.Checked())
		return true
	}

	fields := []*widgets.InputField{
		d.fServer, d.fPort, d.fDatabase, d.fUser, d.fPassword,
		d.fTenantID, d.fClientID, d.fConnName,
	}
	for _, f := range fields {
		if f.HitTest(mx, my) {
			for fi, foc := range d.focusable {
				if foc == f {
					d.setFocus(fi)
					break
				}
			}
			return true
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// ConnStrDialog — view/edit the generated connection string
// ---------------------------------------------------------------------------

// ConnStrDialog shows and lets the user edit the connection string.
type ConnStrDialog struct {
	dialogs.ModalDialog
	field    *widgets.InputField
	btnFocus int
}

// NewConnStrDialog creates the connection string dialog.
func NewConnStrDialog(app *App) *ConnStrDialog {
	d := &ConnStrDialog{}
	d.InitModal(app.screen, "Connection String", 70, 10)
	d.field = widgets.NewInputField("", 64, false)
	return d
}

// SetConnStr sets the connection string to display.
func (d *ConnStrDialog) SetConnStr(s string) { d.field.SetValue(s) }

// GetConnStr returns the current (possibly edited) connection string.
func (d *ConnStrDialog) GetConnStr() string { return d.field.Value() }

// Draw renders the connection string dialog.
func (d *ConnStrDialog) Draw(s tcell.Screen) {
	if !d.Visible() {
		return
	}
	d.DrawBase(s)
	inner := d.InnerRect()
	core.DrawText(s, inner.X+1, inner.Y+1, tcell.StyleDefault, "Connection string (editable):")
	d.field.SetBounds(inner.X+1, inner.Y+3)
	d.field.Draw(s)

	d.DrawSeparator(s)
	d.DrawButtons(s, []string{"OK", "Cancel"}, d.btnFocus)
}

// HandleKey processes keys for the conn string dialog.
func (d *ConnStrDialog) HandleKey(ev *tcell.EventKey) bool {
	if !d.Visible() {
		return false
	}
	d.field.Focus(true)
	switch ev.Key() {
	case tcell.KeyEscape, tcell.KeyEnter:
		d.Hide()
		return true
	case tcell.KeyTab:
		d.btnFocus = (d.btnFocus + 1) % 2
		return true
	}
	d.field.HandleKey(ev)
	return true
}

// HandleMouse processes mouse events.
func (d *ConnStrDialog) HandleMouse(ev *tcell.EventMouse) bool {
	if !d.Visible() {
		return false
	}
	if d.ConsumeOutsideClick(ev) {
		return true
	}
	if i := d.ButtonClicked(ev, []string{"OK", "Cancel"}); i >= 0 {
		d.btnFocus = i
		d.Hide()
		return true
	}
	return true
}
