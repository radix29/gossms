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

// Dialog geometry. The right pane is label+value rows; every label is padded
// to connectLabelWidth at construction (InputField and DropDown fix their
// label at New time). The widest, "Host Name In Certificate", fills it exactly.
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

// Button row. Indices are what btnFocus holds, left to right as drawn;
// drawing, gating and hit-testing all use the two label lists so a click
// never lands one button off.
//
// Delete sits alone at the left end: it destroys a saved connection and its
// password, and a misclick beside Connect cannot be undone.
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
// The layout follows SSMS 21: a History list on the left, and on the right
// a tabbed pane (properties or the built connection string) over a Custom
// Properties section both tabs share.
type ConnectDialog struct {
	dialogs.ModalDialog
	app *App

	// fServer carries the port folded in, SSMS-style: "host", "host,port",
	// "host\instance,port". config.Connection.Port stays a stored field because
	// the sealed-password AAD (config.connectionAAD), the dedup key
	// (Connection.GeneratedName) and config.DialPort's SQL Browser rule key off
	// it. The fold is presentation only: currentOptions splits it with
	// gosmo.ParseServerAddress and PreFill re-joins it with config.ResolveServer,
	// so GeneratedName is unchanged by the round trip and the stored password
	// still decrypts.
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

	// fExtraProps holds extra "key=value" driver parameters separated by ';',
	// '&' or line breaks (db.ParseExtraProperties). A key a dialog field
	// controls is refused and the preview says so.
	fExtraProps *controls.Editor

	// fConnStrPreview previews the connection string (password masked).
	// Focusable so text can be copied, but rebuilt on every blur (setFocus), so
	// manual edits do not survive.
	fConnStrPreview *controls.Editor

	// history is the saved-connection picker; historyConns the connections
	// behind its rows, most recent first (config.Config.MatchByServer("")).
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
	// onButtons is set while the button row holds focus (the stop past either
	// end of the Tab ring). Every field is blurred; focusIdx keeps the last.
	onButtons bool

	// connecting is set from Connect until the attempt resolves: the dialog
	// stays open, every control but Cancel is inert, a spinner runs.
	// connectStarted drives the spinner frame; closing connectAttempt stops the
	// ticker and marks the attempt abandoned (a callback whose channel is no
	// longer d.connectAttempt is stale and must not touch the dialog).
	// connectCancel aborts the dial itself.
	connecting     bool
	connectStarted time.Time
	connectAttempt chan struct{}
	connectCancel  context.CancelFunc
	// connectLabel is what the spinner says the attempt is doing:
	// "Signing in..." while a person signs in (App.signInPhase), else
	// "Connecting...".
	connectLabel string

	// target is the query window this showing connects (set by
	// ShowForQueryPanel, cleared by Hide): Connect dials that window's own
	// connection instead of adding a server; targetThen runs once it is up.
	target     *QueryPanel
	targetThen func()

	// drag is the text-selection gesture a field click starts; see
	// dialogs.FieldGesture for call ordering.
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

// applySize picks two-pane or one-pane width from the terminal size and
// recentres. Called from the constructor, Show and Relayout so a resize
// across an open dialog switches modes.
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

// rebuildFocusable rebuilds the focus ring for the current pane mode and
// tab; a hidden tab's controls must not be reachable by Tab (the Custom
// Properties editor is in both rings). Focus stays on the same widget if it
// survives, else the first entry.
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
	i := max(indexOfFocusable(list, prev), 0)
	d.focusIdx = i
	if d.onButtons {
		// A resize switching the pane mode is no reason to leave the buttons.
		setFocusIn(list, -1, i)
		return
	}
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
// in the ring (docs/ui-rules.md); Tab steps over it, and if it held focus,
// focus moves back to the method dropdown.
//
// Remember Password is greyed with Password: no password, nothing to
// remember.
func (d *ConnectDialog) applyAuthFields() {
	f := config.FieldsFor(d.authMethod())
	d.fUser.SetEnabled(f.User)
	d.fPassword.SetEnabled(f.Password)
	d.cbRemember.SetEnabled(f.Password)
	d.fTenantID.SetEnabled(f.Tenant)
	d.fClientID.SetEnabled(f.Client)
	// The dropdown is only in the ring on the properties tab; on the other one
	// nothing focusable can be disabled, so there is nothing to move off.
	if i := indexOfFocusable(d.focusable, d.ddAuth); i >= 0 && !d.onButtons && !focusableEnabled(d.focusedWidget()) {
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

// stepFocus moves focus dir (+1 or -1) along the ring, past any disabled
// field; past either end is the button row.
func (d *ConnectDialog) stepFocus(dir int) {
	for i := d.focusIdx + dir; i >= 0 && i < len(d.focusable); i += dir {
		if focusableEnabled(d.focusable[i]) {
			d.setFocus(i)
			return
		}
	}
	d.onButtons = true
	setFocusIn(d.focusable, -1, d.focusIdx)
	// Leaving a field is a blur, the point the preview refreshes at — see
	// setFocus.
	d.refreshConnStrPreview()
}

// leaveButtons is Tab (+1) or Backtab (-1) off the button row, onto the first
// or last enabled field. The highlight goes back to Connect, which is what
// Enter in a field fires.
func (d *ConnectDialog) leaveButtons(dir int) {
	d.onButtons = false
	d.btnFocus = connectBtnConnect
	d.focusIdx = -1 // stepFocus starts one past this, at the first field
	if dir < 0 {
		d.focusIdx = len(d.focusable)
	}
	d.stepFocus(dir)
}

// setEncryptMode selects m in ddEncrypt. An unknown value (only a hand-edited
// config.json) selects Mandatory.
//
// The fallback resolves an index rather than recursing with Mandatory: recursion
// relies on AllEncryptModes containing Mandatory, and dropping a mode would turn
// into a stack overflow. The final 0 keeps that local.
func (d *ConnectDialog) setEncryptMode(m config.EncryptMode) {
	modes := config.AllEncryptModes()
	i := slices.Index(modes, m)
	if i < 0 {
		i = max(slices.Index(modes, config.EncryptMandatory), 0)
	}
	d.ddEncrypt.SetSelected(i)
}

// PreFill pre-fills the dialog from an existing connection (the History
// pane's path).
//
// Server and Port come back as the single folded address, joined by
// config.ResolveServer as gosmo's Port does, so the display is what would be
// dialled. Remember Password is ticked for an entry carrying a password (or
// an unreadable one) so older entries keep it until deliberately unticked.
func (d *ConnectDialog) PreFill(c *config.Connection) {
	d.fServer.SetValue(config.ResolveServer(c.Server, c.Port))
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

// syncHistorySelection moves the History highlight onto the row the form is
// showing, matched on the saved-connection key. Without it a reopened dialog
// highlights a stale row (the list survives Hide and reloadHistory
// renumbers). A form matching nothing saved leaves the highlight.
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

// resetForm clears the right pane to its built state: empty fields, SQL
// Server Authentication, Trust Server Certificate on, Encrypt Mandatory,
// Remember Password off. History is left alone.
//
// The emptied Server field is what Show reads to decide on a History
// pre-fill, so a reset-and-dismissed dialog reopens on the latest
// connection.
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

// deleteSelectedHistory removes the highlighted saved connection after a
// confirmation naming it; the entry carries the sealed password, so it is
// not recoverable by retyping.
//
// The confirmation is a nested dialog, leaving this one inert (see
// dialog_stack.go), so the selection cannot move under the callback.
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
			d.app.saveConfig(func(err error) {
				if err != nil {
					d.app.alertDialog.ShowAlert("Delete Connection",
						"Removed, but the configuration could not be saved: "+err.Error())
				}
			})
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
// recent first (MatchByServer with an empty prefix).
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

// Show opens the dialog: re-decides the pane mode against the terminal size
// and refills History (a connection made since is a new row).
func (d *ConnectDialog) Show() {
	d.applySize()
	d.ModalDialog.Show()
	// A latch must not survive into the next showing (it would route every
	// click to that field).
	d.drag.Clear()
	// Back onto Connect: an attempt moves focus to Cancel, and one that
	// succeeded or was cancelled closed the dialog with it still there, so Enter
	// would close instead of connecting.
	d.btnFocus = connectBtnConnect
	d.twoPane = d.Rect().W >= connectTwoPaneWidth
	d.reloadHistory()
	// Open on the most recent connection (as SSMS does), but only into an empty
	// form: fields persist across Show/Hide and a half-typed server name must
	// not be replaced.
	if len(d.historyConns) > 0 && strings.TrimSpace(d.fServer.Value()) == "" {
		d.history.SetSelected(0)
		d.applyHistory(0)
	} else {
		d.syncHistorySelection()
	}
	d.rebuildFocusable()
	d.setFocus(0)
}

// ShowForQueryPanel opens the dialog to connect qp (an Execute in a window
// with no connection), then runs then (nil for nothing) on success. A window
// that had a connection pre-fills it, in its last database.
func (d *ConnectDialog) ShowForQueryPanel(qp *QueryPanel, then func()) {
	if qp.conn != nil {
		opts := qp.conn.Opts
		if qp.database != "" {
			opts.Database = qp.database
		}
		d.PreFill(&opts)
		// PreFill ticks Remember Password for any password, as a History
		// entry wants; this one's was in memory only unless asked for.
		d.cbRemember.SetChecked(opts.RememberPassword)
	}
	d.Show()
	// Onto the form, not History: Enter there re-fills from the highlighted row,
	// which carries no database and, unremembered, no password.
	d.setFocus(indexOfFocusable(d.focusable, d.fServer))
	d.target, d.targetThen = qp, then
}

func (d *ConnectDialog) setFocus(i int) {
	d.onButtons = false
	d.focusIdx = setFocusIn(d.focusable, i, d.focusIdx)
	// Every focus change blurs the old field: the preview refreshes once a
	// field is left, not per keystroke.
	d.refreshConnStrPreview()
}

// refreshConnStrPreview rebuilds the preview from the current fields. The
// real password is never written: db.BuildConnectionString masks secrets.
//
// A setting Connect would refuse (an Extra Properties entry that is not
// key=value, or names a setting a field controls) shows as that error.
func (d *ConnectDialog) refreshConnStrPreview() {
	// Nothing to preview yet (avoids gosmo's "Server is required" at startup).
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

// matchLabel is a saved connection's History name: GeneratedName with
// server and database clipped (ellipsis) to matchServerWidth and
// matchDatabaseWidth so a long FQDN does not push the user off the row.
// Display only: the stored Name is the dedup key and stays whole.
func matchLabel(c config.Connection) string {
	c.Server = core.Truncate(c.Server, matchServerWidth)
	c.Database = core.Truncate(c.Database, matchDatabaseWidth)
	return c.GeneratedName()
}

// serverParts splits the Server Name field into the server (host plus named
// instance) and the folded-in port, reporting whether the port is usable.
//
// An empty port is 0: config.DialPort passes it on as unspecified (a named
// instance takes its port from SQL Browser). A non-numeric trailing port is
// left in the host by gosmo.ParseServerAddress and the driver's error
// surfaces. A numeric port outside 1-65535 is rejected rather than
// falling back to 1433, which would look like the typo worked.
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

// currentOptions assembles a config.Connection from the fields. Name is left
// zero; config.Config.AddOrUpdate fills it once a connection succeeds.
//
// A field the selected method does not read (config.FieldsFor) is left empty
// whatever it holds, so a password typed before switching to Managed
// Identity is not saved. The widget keeps its text.
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

// canConnect reports whether Connect has enough to dial: the driver rejects
// an empty Server, so Connect is gated rather than showing "Server is
// required".
func (d *ConnectDialog) canConnect() bool {
	server, _, _ := d.serverParts()
	return server != ""
}

// connectSpinner is the busy indicator (Braille: one cell wide, so the
// label after it never moves).
var connectSpinner = widgets.SpinnerBraille

// startConnect enters the connecting state and dials. The dialog closes on
// success; on failure it returns to normal with fields as typed.
func (d *ConnectDialog) startConnect(opts config.Connection) {
	d.stopConnecting()
	attempt := make(chan struct{})
	// Background is deliberate: this dial produces the first ServerConn, so
	// ARCHITECTURE.md § Threading model's "derive from sc.Server.Context()"
	// cannot apply. Cancel/Escape abort through this cancel func; quitting
	// mid-dial leaves it running but Run returns straight into exit.
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
	done := func(err error) bool {
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
		// Back onto Connect: startConnect moved focus to Cancel, and leaving it
		// would let the Enter dismissing the error alert close the dialog.
		d.btnFocus = connectBtnConnect
		return true
	}
	if qp := d.target; qp != nil {
		then := d.targetThen
		d.app.dialQueryPanel(ctx, qp, opts, phase, done, func() {
			d.app.rememberConnection(opts)
			if then != nil {
				then()
			}
		})
		return
	}
	d.app.connectServer(ctx, opts, phase, done)
}

// stopConnecting leaves the connecting state, stopping the spinner and
// aborting any attempt in flight. Idempotent; after a finished attempt the
// cancel is a no-op.
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

// Hide leaves the connecting state so a dismissed dialog does not reopen
// with a spinner running.
func (d *ConnectDialog) Hide() {
	d.stopConnecting()
	d.target, d.targetThen = nil, nil
	d.ModalDialog.Hide()
}

// FocusedClipboardTarget implements core.ClipboardHost: whichever text field or
// editor has focus. A dropdown, checkbox, list or button answers nil.
func (d *ConnectDialog) FocusedClipboardTarget() core.ClipboardTarget {
	if d.onButtons {
		return nil
	}
	return focusedClipboardTarget(d.focusable, d.focusIdx)
}
