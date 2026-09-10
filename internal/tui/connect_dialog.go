package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// maxServerMatches caps how many rows of the server-field autocomplete list are
// drawn and hit-tested. The list doesn't scroll, so a longer prefix is how the
// rest are reached.
const maxServerMatches = 10

// ConnectDialog is the "Connect to Server" modal dialog, embedding
// dialogs.ModalDialog and composing tuikit/widgets controls for its fields.
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
	ddEncrypt *widgets.DropDown
	fHostCert *widgets.InputField

	// fExtraProps is a free-form, word-wrapped text box of extra "key=value"
	// driver parameters, separated by ';', '&' or line breaks — see
	// db.ParseExtraProperties. A key one of the dialog's own fields controls
	// is refused, and the preview says so.
	fExtraProps *controls.Editor

	// fConnStrPreview previews the connection string the current fields would
	// build, password masked. Focusable so its text can be selected and copied,
	// but rebuilt on every blur (see setFocus), so a manual edit here doesn't
	// survive leaving the field.
	fConnStrPreview *controls.Editor

	// extraPropsLabelY and connStrLabelY are the row each editor's own
	// label is drawn on, computed by layoutFields and read back by Draw.
	extraPropsLabelY int
	connStrLabelY    int

	focusIdx  int
	focusable []focusable
	btnFocus  int // 0=Connect 1=Cancel

	// connecting is set from the moment Connect is pressed until the attempt
	// resolves: the dialog stays open, every control but Cancel is inert, and
	// a spinner runs on the button row. connectStarted is what the spinner
	// reads its frame off, and connectAttempt closing both stops the ticker
	// goroutine and marks the attempt abandoned — a callback whose channel is
	// no longer d.connectAttempt belongs to a superseded or cancelled attempt
	// and must not touch the dialog. connectCancel aborts the dial itself, so
	// Cancel stops the attempt rather than leaving it to run to its timeout.
	connecting     bool
	connectStarted time.Time
	connectAttempt chan struct{}
	connectCancel  context.CancelFunc
	// connectLabel is what the spinner says the attempt is doing:
	// "Signing in..." while a person signs in (App.signInPhase), else
	// "Connecting...".
	connectLabel string

	// Server-field autocomplete: saved connections whose Server matches what is
	// typed in fServer, listed beneath it once four characters are in — or
	// immediately on a click in the field, whatever its content (see
	// openMatchesForClick). A connection is saved automatically the moment it
	// succeeds (see App.connectServer).
	matches   []config.Connection
	matchOpen bool
	matchSel  int

	// drag is the text-selection gesture a click in one of the dialog's text
	// fields starts — see dialogs.FieldGesture for the ordering its three calls
	// depend on.
	drag dialogs.FieldGesture
}

// NewConnectDialog creates the connection dialog.
func NewConnectDialog(app *App) *ConnectDialog {
	d := &ConnectDialog{app: app}
	d.InitModal(app.screen, "Connect to Server", 62, 32)

	methods := config.AllAuthMethods()
	authItems := make([]string, len(methods))
	for i, m := range methods {
		authItems[i] = config.AuthMethodName(m)
	}

	d.fServer = widgets.NewInputField("Server:  ", 38, false)
	d.fPort = widgets.NewInputField("Port:    ", 6, false)
	d.fDatabase = widgets.NewInputField("Database:", 38, false)
	d.fUser = widgets.NewInputField("User:    ", 38, false)
	d.fPassword = widgets.NewInputField("Password:", 38, true)
	d.fTenantID = widgets.NewInputField("TenantID:", 38, false)
	d.fClientID = widgets.NewInputField("ClientID:", 38, false)
	d.ddAuth = widgets.NewDropDown("Auth:    ", authItems, 38)
	d.cbTrust = widgets.NewCheckBox("Trust Server Certificate")
	d.cbTrust.SetChecked(true)

	modes := config.AllEncryptModes()
	encryptItems := make([]string, len(modes))
	for i, m := range modes {
		encryptItems[i] = config.EncryptModeName(m)
	}
	d.ddEncrypt = widgets.NewDropDown("Encrypt: ", encryptItems, 38)
	d.setEncryptMode(config.EncryptMandatory)
	d.fHostCert = widgets.NewInputField("CertHost:", 38, false)

	d.fExtraProps = controls.NewEditor(nil)
	d.fExtraProps.SetGutterVisible(false)
	d.fExtraProps.SetWrapMode(true)

	d.fConnStrPreview = controls.NewEditor(nil)
	d.fConnStrPreview.SetGutterVisible(false)
	d.fConnStrPreview.SetWrapMode(true)

	d.rebuildFocusable()
	d.applyAuthFields()
	return d
}

func (d *ConnectDialog) rebuildFocusable() {
	d.focusable = []focusable{
		d.fServer, d.fPort, d.ddAuth, d.fDatabase,
		d.fUser, d.fPassword, d.fTenantID, d.fClientID,
		d.cbTrust, d.ddEncrypt, d.fHostCert, d.fExtraProps, d.fConnStrPreview,
	}
}

// authMethod is the method selected in ddAuth.
func (d *ConnectDialog) authMethod() config.AuthMethod {
	return config.AllAuthMethods()[d.ddAuth.Selected()]
}

// applyAuthFields enables the credential fields the selected method reads
// (config.FieldsFor) and disables the rest. A disabled field keeps its place
// in the focus ring (docs/ui-rules.md); Tab steps over it (stepFocus), and if
// the field that has focus is the one just switched off, focus moves back to
// the method dropdown that switched it.
func (d *ConnectDialog) applyAuthFields() {
	f := config.FieldsFor(d.authMethod())
	d.fUser.SetEnabled(f.User)
	d.fPassword.SetEnabled(f.Password)
	d.fTenantID.SetEnabled(f.Tenant)
	d.fClientID.SetEnabled(f.Client)
	if in, ok := d.focusable[d.focusIdx].(*widgets.InputField); ok && !in.Enabled() {
		d.setFocus(indexOfFocusable(d.focusable, d.ddAuth))
	}
}

// stepFocus moves focus dir (+1 or -1) around the ring, past any disabled
// field.
func (d *ConnectDialog) stepFocus(dir int) {
	n := len(d.focusable)
	i := d.focusIdx
	for range n {
		i = (i + dir + n) % n
		if in, ok := d.focusable[i].(*widgets.InputField); ok && !in.Enabled() {
			continue
		}
		break
	}
	d.setFocus(i)
}

// setEncryptMode selects m in ddEncrypt. A value not in the list — only a
// hand-edited config.json has one — selects Mandatory rather than leaving
// the dropdown on whatever it last showed.
func (d *ConnectDialog) setEncryptMode(m config.EncryptMode) {
	for i, mode := range config.AllEncryptModes() {
		if mode == m {
			d.ddEncrypt.SetSelected(i)
			return
		}
	}
	d.setEncryptMode(config.EncryptMandatory)
}

// PreFill pre-fills the dialog from an existing connection — applyMatch's path
// when an autocomplete suggestion is chosen.
func (d *ConnectDialog) PreFill(c *config.Connection) {
	d.fServer.SetValue(c.Server)
	d.fPort.SetValue(portFieldValue(c.Port))
	d.fDatabase.SetValue(c.Database)
	d.fUser.SetValue(c.User)
	d.fPassword.SetValue(c.Password)
	d.fTenantID.SetValue(c.TenantID)
	d.fClientID.SetValue(c.ClientID)
	d.cbTrust.SetChecked(c.TrustServerCertificate)
	d.setEncryptMode(c.Encrypt)
	d.fHostCert.SetValue(c.HostNameInCertificate)
	d.fExtraProps.SetText(c.ExtraProperties)
	for i, m := range config.AllAuthMethods() {
		if m == c.AuthMethod {
			d.ddAuth.SetSelected(i)
			break
		}
	}
	// A service principal saved before the dialog greyed User for it carries
	// its application id there; db.toGosmoOptions falls back to it, and the
	// field it now belongs in shows it.
	if c.AuthMethod == config.AuthEntraServicePrincipal && c.ClientID == "" {
		d.fClientID.SetValue(c.User)
	}
	d.setFocus(0)
	d.applyAuthFields()
}

// Show opens the dialog, focuses the first field, and refreshes the server-match
// list against whatever is already in it: fields persist across Show/Hide, so a
// dialog reopened with a server typed in reflects that at once.
func (d *ConnectDialog) Show() {
	d.ModalDialog.Show()
	// A latch must not survive into the next showing: a dialog dismissed mid-drag
	// would reopen still routing every click to that field.
	d.drag.Clear()
	// Back onto Connect: an attempt moves the button focus to Cancel for its
	// duration, and one that succeeded or was cancelled closed the dialog with
	// it still there — so Enter on the next showing closed the dialog instead
	// of connecting.
	d.btnFocus = 0
	d.setFocus(0)
	d.updateMatches()
}

func (d *ConnectDialog) setFocus(i int) {
	d.focusIdx = setFocusIn(d.focusable, i, d.focusIdx)
	// The autocomplete list only makes sense while the server field has focus.
	if i != 0 {
		d.matchOpen = false
	}
	// Every focus change blurs whatever was focused — the point to refresh the
	// preview, so it updates once a field is left rather than per keystroke.
	d.refreshConnStrPreview()
}

// refreshConnStrPreview rebuilds the connection-string preview from the
// current field values. The real password is never written into it:
// db.BuildConnectionString masks every secret.
//
// A setting Connect would refuse — an Extra Properties entry that is not
// key=value, or one naming a setting a field here controls — shows as that
// error instead, so it is found before Connect is pressed.
func (d *ConnectDialog) refreshConnStrPreview() {
	// Nothing to preview yet — and gosmo's "Server is required" would be the
	// first thing a user sees on the dialog gossms opens at startup.
	if !d.canConnect() {
		d.fConnStrPreview.SetText("")
		return
	}
	s, err := db.BuildConnectionString(d.currentOptions())
	if err != nil {
		s = "Cannot build a connection string: " + err.Error()
	}
	d.fConnStrPreview.SetText(s)
}

// updateMatches re-runs the server-field autocomplete lookup against what is
// typed in fServer. Nothing happens below four characters, and the list closes
// itself once there are no matches.
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

// ClipboardEdited re-filters the saved-connections list after Ctrl+X or Ctrl+V
// changed the server field — the follow-up HandleKey does after a keystroke
// there. Only for that field: an edit elsewhere, a paste into Password, must not
// re-run the lookup and pop the list open over it.
func (d *ConnectDialog) ClipboardEdited(target core.ClipboardTarget) {
	if target == core.ClipboardTarget(d.fServer) {
		d.updateMatches()
	}
}

// openMatchesForClick opens the saved-connections list on a click in the server
// field however much is typed; an empty field lists every saved connection.
// Typing afterwards re-filters through updateMatches and its 4-character
// threshold.
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
// autocomplete list, by arrow+Enter or a click.
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
	d.fTenantID.Draw(s)
	d.fClientID.Draw(s)

	d.cbTrust.Draw(s)
	d.ddEncrypt.Draw(s)
	d.fHostCert.Draw(s)

	core.DrawText(s, inner.X+1, d.extraPropsLabelY, labelStyle, "Extra Properties:")
	d.fExtraProps.Draw(s)

	core.DrawText(s, inner.X+1, d.connStrLabelY, labelStyle, "Connection String:")
	d.fConnStrPreview.Draw(s)

	d.DrawSeparator(s)
	d.DrawButtonsGated(s, []string{"Connect", "Cancel"}, d.btnFocus,
		[]bool{!d.canConnect() || d.connecting})
	// After the buttons, never before: on a clamped rect DrawButtonsGated
	// clears the whole button row, which would wipe the spinner.
	d.drawConnecting(s, labelStyle)

	// Drawn last, so neither dropdown's list nor the server-match list is
	// painted over by the fields and buttons below them.
	d.ddAuth.DrawOverlay(s)
	d.ddEncrypt.DrawOverlay(s)
	d.drawMatches(s)
}

// drawMatches renders the server-field autocomplete list as an overlay directly
// beneath fServer, styled like ddAuth's open list. While open it necessarily
// covers whatever sits below fServer.
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
	n := min(len(d.matches), maxServerMatches)
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
	d.fTenantID.SetBounds(lx, row)
	row++
	d.fClientID.SetBounds(lx, row)
	row++
	row++ // blank row above Trust Server Certificate
	d.cbTrust.SetBounds(lx, row)
	row++
	d.ddEncrypt.SetBounds(lx, row)
	row++
	d.fHostCert.SetBounds(lx, row)
	row++
	row++ // blank row below the TLS settings

	// Same on-screen width as the Password field's whole visible box (label +
	// brackets + content), from real widget geometry.
	previewW := d.fPassword.InputX() + d.fPassword.Width() + 2 - lx

	d.extraPropsLabelY = row
	row++
	d.fExtraProps.SetBounds(lx, row, previewW, 4)
	row += 4

	d.connStrLabelY = row
	row++
	d.fConnStrPreview.SetBounds(lx, row, previewW, 4)
}

// defaultPort is SQL Server's own default TCP port, the one the driver dials
// when the address carries none.
const defaultPort = 1433

// portFieldValue renders a stored port for the Port field, leaving the default
// and the unset one blank: the field is optional, and a named instance resolves
// its port through SQL Browser, so a pre-filled "1433" reads as a requirement
// and, once carried into the address, overrides the instance lookup.
func portFieldValue(port int) string {
	if port == 0 || port == defaultPort {
		return ""
	}
	return strconv.Itoa(port)
}

// port reads the Port field and reports whether it parsed. An empty field means
// "unspecified" — 0, which resolveServer leaves out of the address entirely
// rather than pinning to 1433, since a named instance takes its port from SQL
// Browser. An invalid port is rejected rather than silently falling back:
// connecting to 1433 because "14 33" didn't parse looks like the typo worked.
func (d *ConnectDialog) port() (int, bool) {
	v := strings.TrimSpace(d.fPort.Value())
	if v == "" {
		return 0, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return 0, false
	}
	return n, true
}

// currentOptions assembles a config.Connection from the dialog fields. Name is
// left zero; config.Config.AddOrUpdate fills in the generated name once a
// connection succeeds.
//
// A field the selected method does not read (config.FieldsFor) is left empty
// whatever it holds: a password typed before switching to Managed Identity
// would otherwise be saved, sealed, with a connection that never sends it.
// The widget keeps its text, so switching back restores it.
func (d *ConnectDialog) currentOptions() config.Connection {
	port, ok := d.port()
	if !ok {
		port = 0
	}
	authMethod := d.authMethod()
	f := config.FieldsFor(authMethod)
	only := func(on bool, v string) string {
		if on {
			return v
		}
		return ""
	}
	return config.Connection{
		Server:                 d.fServer.Value(),
		Port:                   port,
		Database:               d.fDatabase.Value(),
		AuthMethod:             authMethod,
		User:                   only(f.User, d.fUser.Value()),
		Password:               only(f.Password, d.fPassword.Value()),
		TenantID:               only(f.Tenant, d.fTenantID.Value()),
		ClientID:               only(f.Client, d.fClientID.Value()),
		TrustServerCertificate: d.cbTrust.Checked(),
		Encrypt:                config.AllEncryptModes()[d.ddEncrypt.Selected()],
		HostNameInCertificate:  d.fHostCert.Value(),
		ExtraProperties:        d.fExtraProps.Text(),
	}
}

// connectSpinner is the busy indicator shown while a connection attempt is in
// flight. Braille: one cell wide, so the "Connecting..." after it never moves.
var connectSpinner = widgets.SpinnerBraille

// drawConnecting paints the spinner and its label at the left end of the button
// row, opposite the buttons themselves.
func (d *ConnectDialog) drawConnecting(s tcell.Screen, style tcell.Style) {
	if !d.connecting {
		return
	}
	x := d.InnerRect().X + 1
	y := d.ButtonRowY()
	connectSpinner.DrawSince(s, x, y, style, d.connectStarted)
	core.DrawText(s, x+connectSpinner.Width()+1, y, style, d.connectLabel)
}

// startConnect puts the dialog into its connecting state and dials. The dialog
// stays up: it closes on success, and on failure returns to normal with the
// fields as typed, so a wrong password is corrected in place rather than
// retyped into a reopened dialog.
func (d *ConnectDialog) startConnect(opts config.Connection) {
	d.stopConnecting()
	attempt := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	d.connecting = true
	d.connectStarted = time.Now()
	d.connectAttempt = attempt
	d.connectCancel = cancel
	d.connectLabel = "Connecting..."
	// Cancel is the only live control from here, so focus is moved onto it.
	d.btnFocus = 1
	d.matchOpen = false

	d.app.safego("animating the connect dialog spinner", func() {
		ticker := time.NewTicker(connectSpinner.Period)
		defer ticker.Stop()
		for {
			select {
			case <-attempt:
				return
			case <-ticker.C:
				// A bare wake, the way QueryPanel's elapsed timer does it:
				// there is no result to post, only a frame to redraw.
				d.app.wakeEventLoop()
			}
		}
	})

	phase := func(label string) {
		if d.connectAttempt == attempt {
			d.connectLabel = label
		}
	}
	d.app.connectServer(ctx, opts, phase, func(err error) bool {
		if d.connectAttempt != attempt {
			// Cancelled, or superseded by a later attempt — this one no
			// longer owns the dialog, and connectServer winds it back.
			return false
		}
		d.stopConnecting()
		if err == nil {
			d.Hide()
			return true
		}
		// Back onto Connect: startConnect moved the button focus to Cancel
		// for the duration, and leaving it there would make the Enter that
		// dismisses the error alert's successor keystroke close the dialog
		// the failed attempt deliberately kept open.
		d.btnFocus = 0
		return true
	})
}

// stopConnecting leaves the connecting state, stopping the spinner goroutine
// and aborting whatever attempt was in flight. Idempotent. After a successful
// or failed attempt the cancel is a no-op: ConnectContext has returned.
func (d *ConnectDialog) stopConnecting() {
	if d.connectAttempt != nil {
		close(d.connectAttempt)
		d.connectAttempt = nil
	}
	if d.connectCancel != nil {
		d.connectCancel()
		d.connectCancel = nil
	}
	d.connecting = false
}

// Hide leaves the connecting state as it closes, so a dialog dismissed
// mid-attempt doesn't reopen with a spinner running and its fields inert.
func (d *ConnectDialog) Hide() {
	d.stopConnecting()
	d.ModalDialog.Hide()
}

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
			d.btnFocus = 1
			d.doButton()
		}
		return true
	}

	// While the autocomplete list is open, arrows navigate it and Enter/Escape
	// act on it, ahead of the field's own key handling and the dialog's
	// Tab-cycling and Enter-confirms.
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
	}
	return true
}

// canConnect reports whether Connect has enough to dial: the driver rejects an
// empty Server outright, so Connect is gated on it rather than reporting
// "Could not connect to : ConnectionOptions.Server is required" — which is
// what pressing Enter on the dialog gossms opens at startup used to produce.
func (d *ConnectDialog) canConnect() bool {
	return strings.TrimSpace(d.fServer.Value()) != ""
}

func (d *ConnectDialog) doButton() {
	switch d.btnFocus {
	case 0: // Connect
		if !d.canConnect() {
			d.app.setStatus("Enter a server name to connect")
			return
		}
		if _, ok := d.port(); !ok {
			d.app.alertDialog.ShowAlert("Connect",
				fmt.Sprintf("Port must be a number from 1 to 65535, not %q", strings.TrimSpace(d.fPort.Value())))
			return
		}
		if d.connecting {
			return
		}
		d.startConnect(d.currentOptions())
	case 1: // Cancel
		d.Hide()
	}
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
		d.ddAuth.HandleMouse(ev)
		d.ddEncrypt.HandleMouse(ev)
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

	// While an attempt is in flight only Cancel answers a click — same gating
	// as HandleKey, and ahead of every field hit-test below.
	if d.connecting {
		if i := d.ButtonClicked(ev, []string{"Connect", "Cancel"}); i == 1 {
			d.btnFocus = 1
			d.doButton()
		}
		return true
	}

	// The gesture belongs to whichever field claimed its press, so motion is
	// replayed there without hit-testing — ahead of the match-list overlay and
	// every widget below, none of which can own a gesture this one started.
	if d.drag.Replay(ev) {
		return true
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

	// A dropdown's open list is an overlay drawn last, so it gets first
	// refusal of every click — ahead of ButtonClicked, which would otherwise
	// steal a click on a list row overlapping the button row. The open one
	// first: its list may cover the other dropdown's own row.
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

	if i := d.ButtonClicked(ev, []string{"Connect", "Cancel"}); i >= 0 {
		d.btnFocus = i
		d.doButton()
		return true
	}

	if d.cbTrust.HandleMouse(ev) {
		d.refreshConnStrPreview()
		return true
	}

	// fExtraProps/fConnStrPreview.HandleMouse checks its own bounds (Editor has
	// no separate HitTest), so it doubles as the hit test.
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
		d.fTenantID, d.fClientID, d.fHostCert,
	}
	for _, f := range fields {
		if f.HitTest(mx, my) {
			if !f.Enabled() {
				// Greyed out for this auth method: a click neither focuses
				// it nor starts a selection in it.
				return true
			}
			for fi, foc := range d.focusable {
				if foc == f {
					d.setFocus(fi)
					break
				}
			}
			// Position the cursor or start a drag-selection at the click point,
			// not just switch focus to the field.
			d.drag.Claim(f, ev)
			if f == d.fServer {
				d.openMatchesForClick()
			}
			return true
		}
	}
	return true
}

// matchHit reports which server-match-list row contains the click, as an index
// into d.matches.
func (d *ConnectDialog) matchHit(ev *tcell.EventMouse) (int, bool) {
	mx, my := ev.Position()
	x := d.fServer.InputX() + 1
	y := d.fServer.RectY() + 1
	w := d.fServer.Width()
	n := min(len(d.matches), maxServerMatches)
	if my < y || my >= y+n || mx < x || mx >= x+w {
		return 0, false
	}
	return my - y, true
}

// FocusedClipboardTarget implements core.ClipboardHost: whichever text field or
// editor has focus. A dropdown, checkbox or button answers nil.
func (d *ConnectDialog) FocusedClipboardTarget() core.ClipboardTarget {
	return focusedClipboardTarget(d.focusable, d.focusIdx)
}
