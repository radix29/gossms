package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/config"
)

// selectAuth picks m in the dialog's auth dropdown the way a user does from
// the keyboard — F4 opens it, arrows move, Enter closes it — so the dialog's
// own reaction to the change runs as it would.
func selectAuth(t *testing.T, d *ConnectDialog, m config.AuthMethod) {
	t.Helper()
	key := func(k tcell.Key) { d.HandleKey(tcell.NewEventKey(k, "", tcell.ModNone)) }
	d.setFocus(indexOfFocusable(d.focusable, d.ddAuth))
	key(tcell.KeyF4)
	methods := config.AllAuthMethods()
	for range methods {
		key(tcell.KeyUp)
	}
	for _, x := range methods {
		if x == m {
			break
		}
		key(tcell.KeyDown)
	}
	key(tcell.KeyEnter)
	if d.authMethod() != m || d.ddAuth.IsOpen() || !d.Visible() {
		t.Fatalf("could not select %s from the keyboard (got %s, open %v)",
			config.AuthMethodName(m), config.AuthMethodName(d.authMethod()), d.ddAuth.IsOpen())
	}
}

// SEC-1: a new connection encrypts the whole session. Trust Server
// Certificate stays ticked by default so a self-signed dev instance still
// connects.
func TestConnectDialogDefaultsToMandatoryEncryption(t *testing.T) {
	d := NewConnectDialog(newTestApp())
	opts := d.currentOptions()
	if opts.Encrypt != config.EncryptMandatory {
		t.Errorf("new dialog Encrypt = %q, want %q", opts.Encrypt, config.EncryptMandatory)
	}
	if !opts.TrustServerCertificate {
		t.Error("new dialog has Trust Server Certificate unticked, want ticked")
	}
}

// A saved connection reopens with its own mode, not the new default — Optional
// included, which is what every entry saved before the dropdown maps to.
func TestConnectDialogPreFillKeepsTheSavedEncryptMode(t *testing.T) {
	d := NewConnectDialog(newTestApp())
	for _, m := range config.AllEncryptModes() {
		d.PreFill(&config.Connection{Server: "s", Encrypt: m, HostNameInCertificate: "cn"})
		if got := d.currentOptions(); got.Encrypt != m || got.HostNameInCertificate != "cn" {
			t.Errorf("PreFill(%q) → Encrypt %q, host %q", m, got.Encrypt, got.HostNameInCertificate)
		}
	}
	d.PreFill(&config.Connection{Server: "s", Encrypt: "bogus"})
	if got := d.currentOptions().Encrypt; got != config.EncryptMandatory {
		t.Errorf("PreFill of an unknown mode → %q, want Mandatory", got)
	}
}

// BUG-3: the fields a method does not read are greyed out and are not in the
// options it connects or saves with.
func TestConnectDialogSendsOnlyTheFieldsTheMethodReads(t *testing.T) {
	d := NewConnectDialog(newTestApp())
	d.Show()
	d.fServer.SetValue("s")
	d.fUser.SetValue("user")
	d.fPassword.SetValue("pw")
	d.fTenantID.SetValue("tenant")
	d.fClientID.SetValue("client")

	cases := map[config.AuthMethod]authFields{
		config.AuthSQLServer:             {user: true, password: true},
		config.AuthWindows:               {user: true, password: true},
		config.AuthEntraDefault:          {tenant: true},
		config.AuthEntraPassword:         {user: true, password: true, tenant: true},
		config.AuthEntraMSI:              {client: true},
		config.AuthEntraServicePrincipal: {password: true, tenant: true, client: true},
		config.AuthEntraInteractive:      {tenant: true, client: true},
		config.AuthEntraDeviceCode:       {tenant: true, client: true},
		config.AuthEntraAzCLI:            {tenant: true},
	}
	if len(cases) != len(config.AllAuthMethods()) {
		t.Fatalf("%d cases for %d auth methods", len(cases), len(config.AllAuthMethods()))
	}
	for m, want := range cases {
		selectAuth(t, d, m)
		enabled := authFields{d.fUser.Enabled(), d.fPassword.Enabled(), d.fTenantID.Enabled(), d.fClientID.Enabled()}
		if enabled != want {
			t.Errorf("%s: enabled user/password/tenant/client = %+v, want %+v", config.AuthMethodName(m), enabled, want)
		}
		o := d.currentOptions()
		sent := authFields{o.User != "", o.Password != "", o.TenantID != "", o.ClientID != ""}
		if sent != want {
			t.Errorf("%s: options carry user/password/tenant/client = %+v, want %+v", config.AuthMethodName(m), sent, want)
		}
	}

	// Switching back finds the text still there: disabling is not clearing.
	selectAuth(t, d, config.AuthSQLServer)
	if o := d.currentOptions(); o.User != "user" || o.Password != "pw" {
		t.Errorf("after a round trip through other methods, user/password = %q/%q, want user/pw", o.User, o.Password)
	}
}

// Tab steps over a greyed-out field rather than parking focus where no key
// does anything.
func TestConnectDialogTabSkipsDisabledFields(t *testing.T) {
	d := NewConnectDialog(newTestApp())
	d.Show()
	selectAuth(t, d, config.AuthEntraMSI) // user, password, tenant greyed
	d.setFocus(indexOfFocusable(d.focusable, d.fDatabase))

	d.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone))
	if d.focusable[d.focusIdx] != d.fClientID {
		t.Fatalf("Tab from Database focused %T #%d, want the Client ID field", d.focusable[d.focusIdx], d.focusIdx)
	}
	d.HandleKey(tcell.NewEventKey(tcell.KeyBacktab, "", tcell.ModNone))
	if d.focusable[d.focusIdx] != d.fDatabase {
		t.Fatalf("Backtab from Client ID focused #%d, want Database", d.focusIdx)
	}
}

// A service principal saved with its application id in User (the only place
// it reached gosmo before) reopens with it in the Client ID field — the one
// that is live for the method — so it is still sent.
func TestConnectDialogPreFillMovesALegacyServicePrincipalId(t *testing.T) {
	d := NewConnectDialog(newTestApp())
	d.PreFill(&config.Connection{Server: "s", AuthMethod: config.AuthEntraServicePrincipal, User: "app-id", Password: "pw"})
	if got := d.currentOptions().ClientID; got != "app-id" {
		t.Errorf("ClientID = %q, want app-id carried over from User", got)
	}
}

// BUG-2: a bad Extra Properties entry is reported in the preview, before
// Connect, instead of the preview showing a string that will not dial.
func TestConnectDialogPreviewReportsABadExtraProperty(t *testing.T) {
	d := NewConnectDialog(newTestApp())
	d.fServer.SetValue("s")
	d.fExtraProps.SetText("database=other")
	d.refreshConnStrPreview()
	if got := d.fConnStrPreview.Text(); len(got) < 6 || got[:6] != "Cannot" {
		t.Errorf("preview = %q, want the refusal", got)
	}
	d.fExtraProps.SetText("ApplicationIntent=ReadOnly")
	d.refreshConnStrPreview()
	if got := d.fConnStrPreview.Text(); len(got) < 12 || got[:12] != "sqlserver://" {
		t.Errorf("preview = %q, want a connection string", got)
	}
}
