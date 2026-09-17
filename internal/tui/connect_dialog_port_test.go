package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// The port is folded into the Server Name field, SSMS-style. It is optional,
// and a named instance must not carry one: a port in the address suppresses
// the SQL Browser lookup that resolves the instance's real, dynamic port, so
// "win10cli\sql2017" with 1433 attached silently reaches the *default*
// instance. currentOptions must report 0 for a field with no port in it.
func TestConnectDialogServerFieldStartsWithNoPort(t *testing.T) {
	a, _ := newConnectDialogApp(t)
	d := a.connectDialog

	if got := d.fServer.Value(); got != "" {
		t.Errorf("Server Name field = %q on a fresh dialog, want empty", got)
	}
	d.fServer.SetValue(`win10cli\sql2017`)
	opts := d.currentOptions()
	if opts.Server != `win10cli\sql2017` || opts.Port != 0 {
		t.Errorf("currentOptions() = %q port %d, want the instance and port 0", opts.Server, opts.Port)
	}
	got, err := db.BuildConnectionString(opts)
	if err != nil {
		t.Fatalf("BuildConnectionString: %v", err)
	}
	if !strings.HasPrefix(got, "sqlserver://win10cli/sql2017?") {
		t.Errorf("preview DSN = %q, want no port so the browser resolves the instance", got)
	}
}

// A saved connection carrying the default 1433 must reopen without it shown,
// or every entry saved before the fold keeps pinning the port; a real one
// round-trips through the field.
func TestConnectDialogPreFillFoldsThePortIntoTheServerField(t *testing.T) {
	a, _ := newConnectDialogApp(t)
	d := a.connectDialog

	d.PreFill(&config.Connection{Server: `win10cli\sql2017`, Port: 1433})
	if got := d.fServer.Value(); got != `win10cli\sql2017` {
		t.Errorf("Server Name = %q for a saved 1433, want no port shown", got)
	}
	d.PreFill(&config.Connection{Server: "win10cli", Port: 55253})
	if got := d.fServer.Value(); got != "win10cli:55253" {
		t.Errorf("Server Name = %q for a saved 55253, want it folded in", got)
	}
	if got := d.currentOptions(); got.Server != "win10cli" || got.Port != 55253 {
		t.Errorf("currentOptions() = %q port %d, want win10cli/55253", got.Server, got.Port)
	}
	// A named instance joins with a comma — gosmo reads a colon there as part
	// of the instance name.
	d.PreFill(&config.Connection{Server: `win10cli\sql2017`, Port: 55253})
	if got := d.fServer.Value(); got != `win10cli\sql2017,55253` {
		t.Errorf("Server Name = %q, want the comma form", got)
	}
}

// The fold is presentation only. config.Connection.Port is bound into the
// sealed-password AAD (config.connectionAAD) and into the dedup key
// (GeneratedName) that the completion inventories share — so a saved entry
// pushed through the field and read back must produce the same name, or every
// stored password for it stops decrypting and it dedups as a new connection.
func TestConnectDialogFoldingThePortKeepsTheConnectionIdentity(t *testing.T) {
	a, _ := newConnectDialogApp(t)
	d := a.connectDialog

	for _, saved := range []config.Connection{
		{Server: "win10cli", Port: 55253, User: "sa"},
		{Server: `win10cli\sql2017`, Port: 55253, User: "sa"},
		{Server: `win10cli\sql2017`, Port: 0, User: "sa"},
		{Server: "win10cli", Port: 1433, Database: "master", User: "sa"},
		{Server: "t-qmi-01.public.686f.database.windows.net", Port: 3342, User: "sa"},
		{Server: "[fe80::1]", Port: 1444, User: "sa"},
	} {
		d.PreFill(&saved)
		got := d.currentOptions()
		got.AuthMethod, got.User = saved.AuthMethod, saved.User
		if got.GeneratedName() != saved.GeneratedName() {
			t.Errorf("round trip of %q port %d → %q port %d: name %q, want %q",
				saved.Server, saved.Port, got.Server, got.Port,
				got.GeneratedName(), saved.GeneratedName())
		}
	}
}

// gossms opens the Connect dialog at startup with every field blank, and Enter
// there used to dial anyway — the driver's own "ConnectionOptions.Server is
// required" surfaced as "Could not connect to : ...". Connect is gated on a
// non-empty Server instead, and refuses rather than closing the dialog.
func TestConnectDialogConnectIsGatedOnAServerName(t *testing.T) {
	a, _ := newConnectDialogApp(t)
	d := a.connectDialog

	if d.canConnect() {
		t.Fatalf("canConnect() is true with a blank Server field")
	}
	d.btnFocus = connectBtnConnect
	d.doButton()
	if !d.Visible() {
		t.Errorf("Connect with a blank Server closed the dialog")
	}
	if got := a.statusText; got != "Enter a server name to connect" {
		t.Errorf("status = %q, want the gating message", got)
	}

	d.fServer.SetValue("   ")
	if d.canConnect() {
		t.Errorf("canConnect() is true for a whitespace-only Server")
	}
	d.fServer.SetValue("ubusql1")
	if !d.canConnect() {
		t.Errorf("canConnect() is false with a server name typed")
	}
}

// An out-of-range port in the field is refused, not silently dropped:
// connecting to 1433 because "99999" didn't fit looks like the typo worked. A
// non-numeric one is not a port at all — gosmo.ParseServerAddress leaves it in
// the host and the driver's own error is what surfaces.
func TestConnectDialogRejectsAnOutOfRangePort(t *testing.T) {
	a, _ := newConnectDialogApp(t)
	d := a.connectDialog

	d.fServer.SetValue("ubusql1,99999")
	if _, _, ok := d.serverParts(); ok {
		t.Errorf("serverParts() accepted port 99999")
	}
	d.fServer.SetValue("ubusql1,1500")
	if s, p, ok := d.serverParts(); !ok || s != "ubusql1" || p != 1500 {
		t.Errorf("serverParts() = %q, %d, %v; want ubusql1, 1500, true", s, p, ok)
	}
	d.fServer.SetValue("ubusql1,abc")
	if s, p, ok := d.serverParts(); !ok || s != "ubusql1,abc" || p != 0 {
		t.Errorf("serverParts() = %q, %d, %v; want the text left in the host", s, p, ok)
	}
}
