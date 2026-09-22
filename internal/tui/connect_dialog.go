package tui

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/core"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// Dialog geometry. The right pane is a column of label+value rows, and every
// label is padded to connectLabelWidth at construction — InputField and
// DropDown fix their label at New time, so the caller pads. The widest label,
// "Host Name In Certificate", is exactly connectLabelWidth columns; a longer
// one would push its own value column out of line with the rest.
//
// connectTwoPaneWidth is also the threshold: a terminal at least that wide
// gets the History pane, a narrower one gets the right pane alone at
// connectOnePaneWidth (decision 3 — it drops the pane, it does not clip).
const (
	connectLabelWidth = 24
	connectValueWidth = 34
	// A field row is label + gap + "[" + value + "]".
	connectRightWidth  = connectLabelWidth + 1 + connectValueWidth + 2
	connectHistoryPane = 30
	// +2 for the dialog border, +1 for the left margin, +1 for the right.
	connectOnePaneWidth = connectRightWidth + 4
	// border + history pane + rule + gap + right pane + border.
	connectTwoPaneWidth = connectHistoryPane + connectRightWidth + 5
	connectDialogHeight = 24
)

// connectTab is which of the right pane's two tabs is showing.
type connectTab int

const (
	connectTabProperties connectTab = iota
	connectTabString
)

var connectTabLabels = [...]string{"Connection Properties", "Connection String"}

// Button row. The indices are what btnFocus holds, left to right as drawn, and
// everything that draws, gates or hit-tests the row goes through the two label
// lists so all three agree — a list that differs from the drawn one puts every
// click one button off.
//
// Delete sits alone at the left end, apart from the buttons that act on the
// form: it destroys a saved connection and its password, and a misclick beside
// Connect is not something the user can undo.
const (
	connectBtnDelete = iota
	connectBtnReset
	connectBtnConnect
	connectBtnCancel
	connectButtonCount
)

var (
	connectLeftButtons  = []string{"Delete"}
	connectRightButtons = []string{"Reset", "Connect", "Cancel"}
)

// buttonRowLeftX is where the left-hand group starts.
func (d *ConnectDialog) buttonRowLeftX() int { return d.InnerRect().X + 1 }

// ConnectDialog is the "Connect to Server" modal dialog, embedding
// dialogs.ModalDialog and composing tuikit/widgets controls for its fields.
//
// The layout follows SSMS 21: saved connections in a History list on the left,
// and on the right a tabbed pane — the connection's properties, or the
// connection string they build — over a Custom Properties section both tabs
// share.
type ConnectDialog struct {
	dialogs.ModalDialog
	app *App

	// fServer carries the port folded in, SSMS-style: "host", "host,port",
	// "host\instance,port". There is no separate Port field — but
	// config.Connection.Port stays a stored field, because the sealed-password
	// AAD (config.connectionAAD), the saved-connection dedup key
	// (Connection.GeneratedName) and db.ResolveServer's SQL Browser rule are
	// all keyed off it. The fold is presentation only: currentOptions splits
	// the text back apart with gosmo.ParseServerAddress and PreFill re-joins it
	// with db.ResolveServer, so a saved entry's GeneratedName is unchanged by
	// the round trip and its stored password still decrypts.
	fServer    *widgets.InputField
	fDatabase  *widgets.InputField
	fUser      *widgets.InputField
	fPassword  *widgets.InputField
	cbRemember *widgets.CheckBox
	fTenantID  *widgets.InputField
	fClientID  *widgets.InputField
	ddAuth     *widgets.DropDown
	cbTrust    *widgets.CheckBox
	ddEncrypt  *widgets.DropDown
	fHostCert  *widgets.InputField

	// fExtraProps is a free-form, word-wrapped text box of extra "key=value"
	// driver parameters, separated by ';', '&' or line breaks — see
	// db.ParseExtraProperties. A key one of the dialog's own fields controls
	// is refused, and the preview says so. It sits in the Custom Properties
	// section, which both tabs show.
	fExtraProps *controls.Editor

	// fConnStrPreview previews the connection string the current fields would
	// build, password masked. Focusable so its text can be selected and copied,
	// but rebuilt on every blur (see setFocus), so a manual edit here doesn't
	// survive leaving the field.
	fConnStrPreview *controls.Editor

	// history is the saved-connection picker in the left pane, and
	// historyConns the connections behind its rows, most recent first
	// (config.Config.MatchByServer("")). It replaces the server field's old
	// autocomplete overlay: the list is the picker, there is no filter box.
	history      *controls.ListBox
	historyConns []config.Connection

	// Layout, computed by layoutFields and read back by Draw. rightX is the
	// left edge of the tabbed pane; paneRuleX is the vertical rule's column,
	// or -1 in one-pane mode.
	twoPane          bool
	rightX           int
	paneRuleX        int
	historyHeaderY   int
	tabRect          core.Rect
	extraPropsLabelY int
	connStrLabelY    int

	tab       connectTab
	focusIdx  int
	focusable []focusable
	btnFocus  int // an index into the button row; see connectBtnDelete

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

	// drag is the text-selection gesture a click in one of the dialog's text
	// fields starts — see dialogs.FieldGesture for the ordering its three calls
	// depend on.
	drag dialogs.FieldGesture
}

// NewConnectDialog creates the connection dialog.
func NewConnectDialog(app *App) *ConnectDialog {
	d := &ConnectDialog{app: app}
	d.InitModal(app.screen, "Connect to Server", connectOnePaneWidth, connectDialogHeight)

	methods := config.AllAuthMethods()
	authItems := make([]string, len(methods))
	for i, m := range methods {
		authItems[i] = config.AuthMethodName(m)
	}

	label := func(s string) string { return core.PadRight(s, connectLabelWidth) }
	d.fServer = widgets.NewInputField(label("Server Name"), connectValueWidth, false)
	d.ddAuth = widgets.NewDropDown(label("Authentication"), authItems, connectValueWidth)
	d.fUser = widgets.NewInputField(label("User Name"), connectValueWidth, false)
	d.fPassword = widgets.NewInputField(label("Password"), connectValueWidth, true)
	d.cbRemember = widgets.NewCheckBox("Remember Password")
	d.fTenantID = widgets.NewInputField(label("Tenant ID"), connectValueWidth, false)
	d.fClientID = widgets.NewInputField(label("Client ID"), connectValueWidth, false)
	d.fDatabase = widgets.NewInputField(label("Database Name"), connectValueWidth, false)

	modes := config.AllEncryptModes()
	encryptItems := make([]string, len(modes))
	for i, m := range modes {
		encryptItems[i] = config.EncryptModeName(m)
	}
	d.ddEncrypt = widgets.NewDropDown(label("Encrypt"), encryptItems, connectValueWidth)
	d.setEncryptMode(config.EncryptMandatory)
	d.cbTrust = widgets.NewCheckBox("Trust Server Certificate")
	d.cbTrust.SetChecked(true)
	d.fHostCert = widgets.NewInputField(label("Host Name In Certificate"), connectValueWidth, false)

	d.fExtraProps = controls.NewEditor(nil)
	d.fExtraProps.SetGutterVisible(false)
	d.fExtraProps.SetWrapMode(true)

	d.fConnStrPreview = controls.NewEditor(nil)
	d.fConnStrPreview.SetGutterVisible(false)
	d.fConnStrPreview.SetWrapMode(true)

	d.history = controls.NewListBox()
	d.history.OnSelect = func(i int) { d.applyHistory(i) }
	d.history.OnActivate = func(i int) {
		d.applyHistory(i)
		d.btnFocus = connectBtnConnect
		d.doButton()
	}

	d.applySize()
	d.rebuildFocusable()
	d.applyAuthFields()
	return d
}

// applySize picks the two-pane or one-pane width off the current terminal size
// and recentres. Called from the constructor, from Show and from Relayout, so a
// terminal resized across an open dialog switches modes rather than keeping the
// width it opened at.
func (d *ConnectDialog) applySize() {
	w := connectOnePaneWidth
	if d.app != nil && d.app.screen != nil {
		if sw, _ := d.app.screen.Size(); sw >= connectTwoPaneWidth {
			w = connectTwoPaneWidth
		}
	}
	d.SetSize(w, connectDialogHeight)
}

// Relayout re-fits the dialog to a resized terminal, re-deciding the pane mode
// first: ModalDialog.Relayout only recentres at the size last requested.
func (d *ConnectDialog) Relayout() { d.applySize() }

// rebuildFocusable rebuilds the focus ring for the current pane mode and tab.
// A control the hidden tab owns must not be reachable by Tab, so the ring holds
// only what is on screen; the Custom Properties editor is on both tabs and so
// is in both rings. Focus stays on the same widget when it survives the
// rebuild, and falls back to the first entry when it doesn't.
func (d *ConnectDialog) rebuildFocusable() {
	prev := d.focusedWidget()
	var list []focusable
	if d.twoPane {
		list = append(list, d.history)
	}
	if d.tab == connectTabString {
		list = append(list, d.fConnStrPreview, d.fExtraProps)
	} else {
		list = append(list,
			d.fServer, d.ddAuth, d.fUser, d.fPassword, d.cbRemember,
			d.fTenantID, d.fClientID, d.fDatabase, d.ddEncrypt, d.cbTrust,
			d.fHostCert, d.fExtraProps)
	}
	d.focusable = list
	i := indexOfFocusable(list, prev)
	if i < 0 {
		i = 0
	}
	d.focusIdx = i
	d.setFocus(i)
}

// focusedWidget is the ring entry that has focus, or nil before the first
// rebuild.
func (d *ConnectDialog) focusedWidget() focusable {
	if d.focusIdx < 0 || d.focusIdx >= len(d.focusable) {
		return nil
	}
	return d.focusable[d.focusIdx]
}

// setTab switches the right pane and rebuilds the focus ring for it.
func (d *ConnectDialog) setTab(t connectTab) {
	if d.tab == t {
		return
	}
	d.tab = t
	// The preview is what the other tab shows; rebuild it before it is looked
	// at rather than waiting for the next blur.
	d.refreshConnStrPreview()
	d.rebuildFocusable()
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
//
// Remember Password is greyed with the Password field it belongs to: a method
// that sends no password has nothing to remember.
func (d *ConnectDialog) applyAuthFields() {
	f := config.FieldsFor(d.authMethod())
	d.fUser.SetEnabled(f.User)
	d.fPassword.SetEnabled(f.Password)
	d.cbRemember.SetEnabled(f.Password)
	d.fTenantID.SetEnabled(f.Tenant)
	d.fClientID.SetEnabled(f.Client)
	// The dropdown is only in the ring on the properties tab; on the other one
	// nothing focusable can be disabled, so there is nothing to move off.
	if i := indexOfFocusable(d.focusable, d.ddAuth); i >= 0 && !focusableEnabled(d.focusedWidget()) {
		d.setFocus(i)
	}
}

// focusableEnabled reports whether w accepts input; a widget with no enabled
// state always does.
func focusableEnabled(w focusable) bool {
	switch c := w.(type) {
	case *widgets.InputField:
		return c.Enabled()
	case *widgets.CheckBox:
		return c.Enabled()
	}
	return true
}

// stepFocus moves focus dir (+1 or -1) around the ring, past any disabled
// field.
func (d *ConnectDialog) stepFocus(dir int) {
	n := len(d.focusable)
	i := d.focusIdx
	for range n {
		i = (i + dir + n) % n
		if !focusableEnabled(d.focusable[i]) {
			continue
		}
		break
	}
	d.setFocus(i)
}

// setEncryptMode selects m in ddEncrypt. A value not in the list — only a
// hand-edited config.json has one — selects Mandatory rather than leaving
// the dropdown on whatever it last showed.
//
// The fallback resolves an index rather than calling back in with Mandatory:
// the recursive form terminated only because AllEncryptModes happens to
// contain Mandatory, an unpinned dependency across two packages that turned
// dropping a mode into a stack overflow here. The final 0 keeps that property
// local — the list is never empty, and its first entry is a real mode.
func (d *ConnectDialog) setEncryptMode(m config.EncryptMode) {
	modes := config.AllEncryptModes()
	i := slices.Index(modes, m)
	if i < 0 {
		i = max(slices.Index(modes, config.EncryptMandatory), 0)
	}
	d.ddEncrypt.SetSelected(i)
}

// PreFill pre-fills the dialog from an existing connection — the History
// pane's path when a saved connection is selected.
//
// Server and Port come back as the single folded address the field shows;
// db.ResolveServer is the same join dialling uses, so what is displayed is
// what would be dialled. Remember Password comes back ticked for an entry that
// carries a password (or an unreadable one), so entries saved before the box
// existed keep their password until it is deliberately unticked.
func (d *ConnectDialog) PreFill(c *config.Connection) {
	d.fServer.SetValue(db.ResolveServer(c.Server, c.Port))
	d.fDatabase.SetValue(c.Database)
	d.fUser.SetValue(c.User)
	d.fPassword.SetValue(c.Password)
	d.cbRemember.SetChecked(c.RememberPassword || c.Password != "" || c.PasswordUnreadable())
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
	d.applyAuthFields()
	// Every field reads from its start: these values were picked, not typed,
	// so the head of a long server name or database name is what identifies
	// the connection, and SetValue alone leaves the view on the tail.
	for _, f := range []*widgets.InputField{
		d.fServer, d.fDatabase, d.fUser, d.fPassword, d.fTenantID, d.fClientID,
		d.fHostCert,
	} {
		f.ShowFromStart()
	}
	d.refreshConnStrPreview()
}

// applyHistory pre-fills the right pane from History row i.
func (d *ConnectDialog) applyHistory(i int) {
	if i < 0 || i >= len(d.historyConns) {
		return
	}
	d.PreFill(&d.historyConns[i])
}

// syncHistorySelection moves the History highlight onto the row the right pane
// is showing, matched on the saved-connection key. Without it a reopened dialog
// highlights whatever row the list was left on while the form shows a different
// connection: the list survives Hide, and reloadHistory renumbers every row as
// soon as a connection is made. A form that matches nothing saved — a
// half-typed server name — leaves the highlight where it is.
func (d *ConnectDialog) syncHistorySelection() {
	name := d.currentOptions().GeneratedName()
	for i, c := range d.historyConns {
		if c.GeneratedName() == name {
			d.history.SetSelected(i)
			return
		}
	}
}

// buttonsDisabled is the gating DrawButtonsGated paints and doButton enforces.
// Cancel is never gated — it is the only way out of an attempt in flight.
func (d *ConnectDialog) buttonsDisabled() []bool {
	gated := make([]bool, connectButtonCount)
	gated[connectBtnDelete] = d.connecting || d.history.Selected() < 0
	gated[connectBtnReset] = d.connecting
	gated[connectBtnConnect] = !d.canConnect() || d.connecting
	gated[connectBtnCancel] = false
	return gated
}

// resetForm clears the right pane back to the state the dialog is built in:
// empty fields, SQL Server Authentication, Trust Server Certificate on, Encrypt
// Mandatory, Remember Password off. History is left alone — the saved
// connections are not what Reset clears.
//
// The emptied Server field is also what Show reads to decide whether to
// pre-fill from History, so a dialog reset and dismissed reopens on the most
// recent connection rather than staying blank.
func (d *ConnectDialog) resetForm() {
	d.fServer.SetValue("")
	d.fDatabase.SetValue("")
	d.fUser.SetValue("")
	d.fPassword.SetValue("")
	d.fTenantID.SetValue("")
	d.fClientID.SetValue("")
	d.fHostCert.SetValue("")
	d.fExtraProps.SetText("")
	d.cbRemember.SetChecked(false)
	d.cbTrust.SetChecked(true)
	d.ddAuth.SetSelected(0)
	d.setEncryptMode(config.EncryptMandatory)
	d.setTab(connectTabProperties)
	d.applyAuthFields()
	d.btnFocus = connectBtnConnect
	if i := indexOfFocusable(d.focusable, d.fServer); i >= 0 {
		d.setFocus(i)
	}
	d.refreshConnStrPreview()
}

// deleteSelectedHistory removes the highlighted saved connection, after a
// confirmation naming it — the entry carries the sealed password, so this is
// not recoverable by retyping the server name.
//
// The confirmation runs as a nested dialog over this one, which leaves the
// Connect dialog inert until it is answered (see dialog_stack.go), so the
// selection the callback reads cannot have moved under it.
func (d *ConnectDialog) deleteSelectedHistory() {
	i := d.history.Selected()
	if i < 0 || i >= len(d.historyConns) || d.app == nil || d.app.cfg == nil {
		return
	}
	name := d.historyConns[i].GeneratedName()
	d.app.confirmDialog.ShowConfirm("Delete Connection",
		"Remove "+name+" from the saved connections? Any password saved with it is deleted too.",
		func(confirmed bool) {
			if !confirmed || !d.app.cfg.RemoveConnection(name) {
				return
			}
			if err := d.app.cfg.Save(); err != nil {
				d.app.alertDialog.ShowAlert("Delete Connection",
					"Removed, but the configuration could not be saved: "+err.Error())
			}
			d.reloadHistory()
			if len(d.historyConns) == 0 {
				d.resetForm()
				return
			}
			// Land on the row that took the deleted one's place, so the
			// highlight and the right pane still agree.
			next := min(i, len(d.historyConns)-1)
			d.history.SetSelected(next)
			d.applyHistory(next)
		})
}

// reloadHistory refills the History pane from the saved connections, most
// recent first — config.Config.MatchByServer with an empty prefix, which is
// what that ordering guarantee is still for now that the autocomplete overlay
// it was written for is gone.
func (d *ConnectDialog) reloadHistory() {
	d.historyConns = nil
	if d.app != nil && d.app.cfg != nil {
		d.historyConns = d.app.cfg.MatchByServer("")
	}
	items := make([]string, len(d.historyConns))
	for i, c := range d.historyConns {
		items[i] = matchLabel(c)
	}
	d.history.SetItems(items)
}

// Show opens the dialog: the pane mode is re-decided against the current
// terminal size and the History pane refilled, since a connection made since
// the last showing is a new row in it.
func (d *ConnectDialog) Show() {
	d.applySize()
	d.ModalDialog.Show()
	// A latch must not survive into the next showing: a dialog dismissed mid-drag
	// would reopen still routing every click to that field.
	d.drag.Clear()
	// Back onto Connect: an attempt moves the button focus to Cancel for its
	// duration, and one that succeeded or was cancelled closed the dialog with
	// it still there — so Enter on the next showing closed the dialog instead
	// of connecting.
	d.btnFocus = connectBtnConnect
	d.twoPane = d.Rect().W >= connectTwoPaneWidth
	d.reloadHistory()
	// Open on the most recent connection, as SSMS does — but only into an
	// empty form. The fields persist across Show/Hide, so a dialog reopened
	// over a half-typed server name must not have it replaced by whatever
	// History happens to be sitting on.
	if len(d.historyConns) > 0 && strings.TrimSpace(d.fServer.Value()) == "" {
		d.history.SetSelected(0)
		d.applyHistory(0)
	} else {
		d.syncHistorySelection()
	}
	d.rebuildFocusable()
	d.setFocus(0)
}

func (d *ConnectDialog) setFocus(i int) {
	d.focusIdx = setFocusIn(d.focusable, i, d.focusIdx)
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

// Column budgets for the server and database parts of a History row label,
// sized for the connectHistoryPane-column pane.
const (
	matchServerWidth   = 14
	matchDatabaseWidth = 8
)

// matchLabel is a saved connection's name as the History pane shows it:
// GeneratedName with the server and database clipped (with an ellipsis) to
// matchServerWidth and matchDatabaseWidth, so a long FQDN or database name
// doesn't push the user — the part that tells two entries for one server
// apart — off the end of the row. Display only: the stored Name is the dedup
// key and stays whole.
func matchLabel(c config.Connection) string {
	c.Server = core.Truncate(c.Server, matchServerWidth)
	c.Database = core.Truncate(c.Database, matchDatabaseWidth)
	return c.GeneratedName()
}

// serverParts splits the Server Name field into the server (host, plus its
// named instance) and the port folded into it, and reports whether the port
// is usable.
//
// An empty port means "unspecified" — 0, which db.ResolveServer leaves out of
// the address entirely rather than pinning to 1433, since a named instance
// takes its port from SQL Browser. A non-numeric trailing port is not a port
// at all: gosmo.ParseServerAddress leaves it in the host, and the driver's own
// error is what surfaces. A numeric one outside 1-65535 is rejected rather
// than silently falling back, since connecting to 1433 because "99999" didn't
// fit looks like the typo worked.
func (d *ConnectDialog) serverParts() (server string, port int, ok bool) {
	host, instance, port := gosmo.ParseServerAddress(strings.TrimSpace(d.fServer.Value()))
	server = host
	if instance != "" {
		server = host + `\` + instance
	}
	if port != 0 && (port < 1 || port > 65535) {
		return server, 0, false
	}
	return server, port, true
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
	server, port, ok := d.serverParts()
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
		Server:                 server,
		Port:                   port,
		Database:               d.fDatabase.Value(),
		AuthMethod:             authMethod,
		User:                   only(f.User, d.fUser.Value()),
		Password:               only(f.Password, d.fPassword.Value()),
		RememberPassword:       f.Password && d.cbRemember.Checked(),
		TenantID:               only(f.Tenant, d.fTenantID.Value()),
		ClientID:               only(f.Client, d.fClientID.Value()),
		TrustServerCertificate: d.cbTrust.Checked(),
		Encrypt:                config.AllEncryptModes()[d.ddEncrypt.Selected()],
		HostNameInCertificate:  d.fHostCert.Value(),
		ExtraProperties:        d.fExtraProps.Text(),
	}
}

// canConnect reports whether Connect has enough to dial: the driver rejects an
// empty Server outright, so Connect is gated on it rather than reporting
// "Could not connect to : ConnectionOptions.Server is required" — which is
// what pressing Enter on the dialog gossms opens at startup used to produce.
func (d *ConnectDialog) canConnect() bool {
	server, _, _ := d.serverParts()
	return server != ""
}

// connectSpinner is the busy indicator shown while a connection attempt is in
// flight. Braille: one cell wide, so the "Connecting..." after it never moves.
var connectSpinner = widgets.SpinnerBraille

// startConnect puts the dialog into its connecting state and dials. The dialog
// stays up: it closes on success, and on failure returns to normal with the
// fields as typed, so a wrong password is corrected in place rather than
// retyped into a reopened dialog.
func (d *ConnectDialog) startConnect(opts config.Connection) {
	d.stopConnecting()
	attempt := make(chan struct{})
	// Background is deliberate here, and is the one place ARCHITECTURE.md
	// § Threading model's "derive from ServerConn.Context()" cannot apply:
	// this dial is what produces the first ServerConn, so there is no parent
	// to derive from. Cancel (and Escape, which presses it) aborts the attempt
	// through this cancel func; quitting mid-dial leaves it running, but Run
	// returns straight into main's exit, so the attempt never outlives the
	// process.
	ctx, cancel := context.WithCancel(context.Background())
	d.connecting = true
	d.connectStarted = time.Now()
	d.connectAttempt = attempt
	d.connectCancel = cancel
	d.connectLabel = "Connecting..."
	// Cancel is the only live control from here, so focus is moved onto it.
	d.btnFocus = connectBtnCancel

	d.app.animateUntil("animating the connect dialog spinner", connectSpinner.Period, attempt)

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
		d.btnFocus = connectBtnConnect
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

// FocusedClipboardTarget implements core.ClipboardHost: whichever text field or
// editor has focus. A dropdown, checkbox, list or button answers nil.
func (d *ConnectDialog) FocusedClipboardTarget() core.ClipboardTarget {
	return focusedClipboardTarget(d.focusable, d.focusIdx)
}
