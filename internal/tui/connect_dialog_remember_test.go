package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/config"
)

// Every successful connection's password used to be sealed into config.json
// unconditionally. Remember Password gates that, and defaults to off — so a
// password typed into a fresh dialog is used for the dial and never written
// down (decision 1).
func TestRememberPasswordDefaultsToOff(t *testing.T) {
	d := NewConnectDialog(newTestApp())
	if d.cbRemember.Checked() {
		t.Error("Remember Password is ticked on a fresh dialog")
	}
	d.fServer.SetValue("ubusql1")
	d.fUser.SetValue("sa")
	d.fPassword.SetValue("s3cr3t!")
	opts := d.currentOptions()
	if opts.Password != "s3cr3t!" {
		t.Errorf("currentOptions().Password = %q — the dial needs it", opts.Password)
	}
	if opts.RememberPassword {
		t.Error("currentOptions().RememberPassword is true with the box unticked")
	}

	// AddOrUpdate is what App.connectServer saves through.
	cfg := &config.Config{}
	cfg.AddOrUpdate(opts)
	if got := cfg.Connections[0].Password; got != "" {
		t.Errorf("saved password = %q, want nothing stored", got)
	}
	if got := cfg.Connections[0].Server; got != "ubusql1" {
		t.Errorf("saved server = %q — the entry itself must still be listed", got)
	}
}

// A saved connection that carries a password pre-fills with the box ticked, so
// entries sealed under the old always-save behaviour keep working until one is
// deliberately unticked.
func TestRememberPasswordPreFillsTickedForASavedPassword(t *testing.T) {
	d := NewConnectDialog(newTestApp())

	d.PreFill(&config.Connection{Server: "ubusql1", User: "sa", Password: "s3cr3t!"})
	if !d.cbRemember.Checked() {
		t.Error("an entry with a saved password pre-filled with the box unticked")
	}
	if got := d.currentOptions(); !got.RememberPassword || got.Password != "s3cr3t!" {
		t.Errorf("currentOptions() = remember %v password %q, want true/s3cr3t!", got.RememberPassword, got.Password)
	}

	d.PreFill(&config.Connection{Server: "ubusql1", User: "sa"})
	if d.cbRemember.Checked() {
		t.Error("an entry with no saved password pre-filled with the box ticked")
	}
}

// A method that sends no password has nothing to remember: the box greys out
// with the field it belongs to, and currentOptions reports it off whatever the
// widget still holds — the same rule the credential fields follow.
func TestRememberPasswordGreysWithThePasswordField(t *testing.T) {
	d := NewConnectDialog(newTestApp())
	d.Show()
	d.fServer.SetValue("s")
	d.fPassword.SetValue("s3cr3t!")
	d.cbRemember.SetChecked(true)

	selectAuth(t, d, config.AuthEntraMSI) // password not read
	if d.cbRemember.Enabled() {
		t.Error("Remember Password is live for Managed Identity, which sends no password")
	}
	if got := d.currentOptions(); got.RememberPassword || got.Password != "" {
		t.Errorf("currentOptions() = remember %v password %q, want false/empty", got.RememberPassword, got.Password)
	}

	selectAuth(t, d, config.AuthSQLServer)
	if !d.cbRemember.Enabled() {
		t.Error("Remember Password stayed greyed for SQL Server Authentication")
	}
	if got := d.currentOptions(); !got.RememberPassword || got.Password != "s3cr3t!" {
		t.Errorf("switching back lost the tick or the password: %v / %q", got.RememberPassword, got.Password)
	}
}
