package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

// The Port field is optional. It used to open pre-filled with "1433", which a
// named instance must not carry: a port in the address suppresses the SQL
// Browser lookup that resolves the instance's real, dynamic port, so
// "win10cli\sql2017" with 1433 attached silently reaches the *default*
// instance. currentOptions must report 0 for an untouched field.
func TestConnectDialogPortFieldStartsEmpty(t *testing.T) {
	a, _ := newConnectDialogApp(t)
	d := a.connectDialog

	if got := d.fPort.Value(); got != "" {
		t.Errorf("Port field = %q on a fresh dialog, want empty", got)
	}
	d.fServer.SetValue(`win10cli\sql2017`)
	opts := d.currentOptions()
	if opts.Port != 0 {
		t.Errorf("currentOptions().Port = %d for an empty field, want 0", opts.Port)
	}
	if got := db.BuildConnectionString(opts); got != `sqlserver://win10cli/sql2017?TrustServerCertificate=true&encrypt=false` {
		t.Errorf("preview DSN = %q, want no port so the browser resolves the instance", got)
	}
}

// A saved connection carrying the old pre-filled 1433 must reopen with a blank
// field too, or every entry saved before this change keeps pinning the port.
func TestConnectDialogPreFillHidesDefaultPort(t *testing.T) {
	a, _ := newConnectDialogApp(t)
	d := a.connectDialog

	d.PreFill(&config.Connection{Server: `win10cli\sql2017`, Port: 1433})
	if got := d.fPort.Value(); got != "" {
		t.Errorf("Port field = %q for a saved 1433, want empty", got)
	}
	d.PreFill(&config.Connection{Server: "win10cli", Port: 55253})
	if got := d.fPort.Value(); got != "55253" {
		t.Errorf("Port field = %q for a saved 55253, want it shown", got)
	}
	if got := d.currentOptions().Port; got != 55253 {
		t.Errorf("currentOptions().Port = %d, want 55253", got)
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
	d.btnFocus = 0
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
