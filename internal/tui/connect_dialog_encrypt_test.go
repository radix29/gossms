package tui

import (
	"slices"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/microsoft/go-mssqldb/msdsn"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
)

func TestConnectCertificateValidationReachesTheDriver(t *testing.T) {
	check := func(t *testing.T, d *ConnectDialog, wantTrust bool) {
		t.Helper()
		d.fServer.SetValue("sql.example.com")
		opts := d.currentOptions()
		if opts.Encrypt != config.EncryptMandatory || opts.TrustServerCertificate != wantTrust {
			t.Fatalf("Encrypt = %q, TrustServerCertificate = %v; want Mandatory and %v",
				opts.Encrypt, opts.TrustServerCertificate, wantTrust)
		}
		dsn, err := db.BuildConnectionString(opts)
		if err != nil {
			t.Fatal(err)
		}
		driver, err := msdsn.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		if driver.TLSConfig == nil {
			t.Fatal("Mandatory encryption did not configure TLS")
		}
		if driver.TLSConfig.InsecureSkipVerify != wantTrust {
			t.Fatalf("driver InsecureSkipVerify = %v, want %v", driver.TLSConfig.InsecureSkipVerify, wantTrust)
		}
	}
	t.Run("new form", func(t *testing.T) {
		check(t, NewConnectDialog(newTestApp()), false)
	})
	t.Run("explicit opt-in and reset", func(t *testing.T) {
		d := NewConnectDialog(newTestApp())
		d.Show()
		d.setFocus(indexOfFocusable(d.focusable, d.cbTrust))
		d.HandleKey(tcell.NewEventKey(tcell.KeyRune, " ", tcell.ModNone))
		check(t, d, true)
		d.btnFocus = connectBtnReset
		d.doButton()
		check(t, d, false)
	})
	for _, trust := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved validation", true: "saved opt-in"}[trust], func(t *testing.T) {
			d := NewConnectDialog(newTestApp())
			saved := config.Connection{
				Server: "sql.example.com", Encrypt: config.EncryptMandatory,
				TrustServerCertificate: trust,
			}
			d.PreFill(&saved)
			check(t, d, trust)
			if saved.TrustServerCertificate != trust {
				t.Fatal("PreFill changed the saved connection")
			}
		})
	}
}

// setEncryptMode's "" fallback used to be a tail call into itself with
// EncryptMandatory, which terminated only because AllEncryptModes happens to
// contain that mode. The two live in different packages and nothing pinned the
// dependency, so dropping or renaming a mode turned the dialog's first
// setEncryptMode into a stack overflow.
//
// Both halves are pinned: that "" still lands on Mandatory, and that it gets
// there without recursing. A test that only asserted the result would go on
// passing while the recursive form was reintroduced.
func TestSetEncryptModeFallsBackWithoutRecursing(t *testing.T) {
	d := NewConnectDialog(newTestApp())
	d.setEncryptMode(config.EncryptStrict)
	d.setEncryptMode("")
	if got := d.currentOptions().Encrypt; got != config.EncryptMandatory {
		t.Errorf(`"" selected %q, want %q`, got, config.EncryptMandatory)
	}
}

// A hand-edited encrypt_mode gossms never writes is shown as it is and
// refused, not shown and dialled as Mandatory (docs/decisions.md § Always On,
// peer credentials). Picking a listed mode is the repair, and drops nothing
// else from the form.
func TestConnectRefusesAnUnknownSavedEncryptMode(t *testing.T) {
	for _, m := range []config.EncryptMode{"yes", "true", "Optional", "mandatory "} {
		t.Run(string(m), func(t *testing.T) {
			d := NewConnectDialog(newTestApp())
			saved := config.Connection{Server: "sql.example.com", User: "u", Password: "p", Encrypt: m}
			d.PreFill(&saved)
			if got := d.ddEncrypt.Value(); got != string(m) {
				t.Errorf("Encrypt shows %q, want the saved %q", got, m)
			}
			if got := d.currentOptions().Encrypt; got != m {
				t.Errorf("currentOptions().Encrypt = %q, want the saved %q", got, m)
			}
			if got := d.fConnStrPreview.Text(); !strings.Contains(got, "unknown encryption mode") {
				t.Errorf("preview = %q, want the refusal", got)
			}
			if _, err := db.BuildConnectionString(d.currentOptions()); err == nil {
				t.Error("BuildConnectionString accepted the unknown mode")
			}
			for i, want := range config.AllEncryptModes() {
				d.ddEncrypt.SetSelected(i)
				d.refreshConnStrPreview()
				if got := d.currentOptions().Encrypt; got != want {
					t.Errorf("row %d = %q, want %q", i, got, want)
				}
				if got := d.fConnStrPreview.Text(); strings.Contains(got, "Cannot build") {
					t.Errorf("%s: preview = %q, want a connection string", want, got)
				}
			}
			d.setEncryptMode(config.EncryptStrict)
			if n := len(d.ddEncrypt.Items()); n != len(config.AllEncryptModes()) {
				t.Errorf("a known mode left %d rows, want %d", n, len(config.AllEncryptModes()))
			}
		})
	}
}

// The fallback no longer depends on Mandatory being in the list, but the
// dialog's *meaning* still does: a list without it would silently connect
// under a weaker mode than the one the fallback names. Cheaper to pin than to
// rediscover.
func TestAllEncryptModesContainsMandatory(t *testing.T) {
	modes := config.AllEncryptModes()
	if len(modes) == 0 {
		t.Fatal("AllEncryptModes is empty, so the Connect dialog has no mode to select at all")
	}
	if !slices.Contains(modes, config.EncryptMandatory) {
		t.Errorf("AllEncryptModes %v no longer contains %q, which setEncryptMode falls back to",
			modes, config.EncryptMandatory)
	}
}
