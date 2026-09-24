package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// newCertificateTestDialog builds the New Certificate page the way show()
// would, minus the prefetch: hasMasterKey is what the prefetch read, and dmk
// what the apply's own master-key check will be answered with.
func newCertificateTestDialog(t *testing.T, hasMasterKey bool) *NewCertificateDialog {
	t.Helper()
	dmk := int64(0)
	if hasMasterKey {
		dmk = 1
	}
	sc, _ := newFakeConn(t, fakeResponse{match: "##MS_DatabaseMasterKey##", cols: 1, rows: [][]driver.Value{{dmk}}})
	d := &NewCertificateDialog{dbName: "certdb"}
	d.sc = sc
	d.pages = []string{"General"}
	d.forms = make([]*propsheet.Form, 1)
	d.applyFns = make([]propApply, 1)
	d.buildPages(&ncertPrefetch{existingNames: newNameSet("", "taken"), hasMasterKey: hasMasterKey})
	return d
}

func scriptNewCertificate(t *testing.T, d *NewCertificateDialog) []string {
	t.Helper()
	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	ctx, script := gosmo.WithScript(context.Background())
	if err := d.applyFns[0](ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return script.Statements()
}

// No master key: Script Changes creates one before the certificate, and
// neither typed password reaches the query window.
func TestNewCertificateScriptsTheMasterKeyFirstWithoutThePassword(t *testing.T) {
	d := newCertificateTestDialog(t, false)
	f := d.forms[0]
	editText(t, f, "Certificate name", "c1")
	editText(t, f, "Subject", "test cert")
	editText(t, f, "Expiry date (yyyy-mm-dd)", "2099-12-31")
	editText(t, f, "Master key password", "S3cret!pw")
	editText(t, f, "Confirm master key password", "S3cret!pw")

	stmts := scriptNewCertificate(t, d)
	want := []string{
		"USE [certdb];\nGO\nCREATE MASTER KEY ENCRYPTION BY PASSWORD = N'<insert password here>'",
		"USE [certdb];\nGO\nCREATE CERTIFICATE [c1] WITH SUBJECT = N'test cert', EXPIRY_DATE = N'20991231'",
	}
	if strings.Join(stmts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(stmts, "\n"), strings.Join(want, "\n"))
	}
}

// A database that already has a master key gets no second one.
func TestNewCertificateLeavesAnExistingMasterKeyAlone(t *testing.T) {
	d := newCertificateTestDialog(t, true)
	editText(t, d.forms[0], "Certificate name", "c1")
	editText(t, d.forms[0], "Subject", "s")

	stmts := scriptNewCertificate(t, d)
	if len(stmts) != 1 || !strings.HasPrefix(stmts[0], "USE [certdb];\nGO\nCREATE CERTIFICATE [c1]") {
		t.Fatalf("want the CREATE CERTIFICATE alone, got:\n%s", strings.Join(stmts, "\n"))
	}
}

// A password-protected private key needs no master key, so none is created
// even in a database that has none — and the password is a placeholder too.
func TestNewCertificateByPasswordNeedsNoMasterKey(t *testing.T) {
	d := newCertificateTestDialog(t, false)
	f := d.forms[0]
	editText(t, f, "Certificate name", "c1")
	editText(t, f, "Subject", "s")
	editRadio(t, f, "Private key encrypted by", "Password")
	editText(t, f, "Password", "k3y!pw")
	editText(t, f, "Confirm password", "k3y!pw")

	stmts := scriptNewCertificate(t, d)
	want := "USE [certdb];\nGO\nCREATE CERTIFICATE [c1] ENCRYPTION BY PASSWORD = N'<insert password here>' WITH SUBJECT = N's'"
	if len(stmts) != 1 || stmts[0] != want {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(stmts, "\n"), want)
	}
}

func TestValidateNewCertificate(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	ok := newCertificateInput{name: "c", subject: "s",
		keyProtectionInput: keyProtectionInput{hasMasterKey: true, dbName: "d"},
		existingNames:      newNameSet("", "taken")}
	cases := []struct {
		name string
		edit func(*newCertificateInput)
		want string // substring of the error; "" means accepted
	}{
		{"valid", func(*newCertificateInput) {}, ""},
		{"no name", func(in *newCertificateInput) { in.name = "" }, "name is required"},
		{"taken, any case", func(in *newCertificateInput) { in.name = "TAKEN" }, "already exists"},
		{"no subject", func(in *newCertificateInput) { in.subject = "  " }, "subject is required"},
		{"bad start", func(in *newCertificateInput) { in.start = "22/09/2026" }, "start date"},
		{"bad expiry", func(in *newCertificateInput) { in.expiry = "tomorrow" }, "expiry date"},
		{"expiry before start", func(in *newCertificateInput) { in.start, in.expiry = "2027-01-02", "2027-01-01" }, "after the start"},
		{"expiry already past", func(in *newCertificateInput) { in.expiry = "2026-01-01" }, "after the start"},
		{"past start, later expiry", func(in *newCertificateInput) { in.start, in.expiry = "2020-01-01", "2021-01-01" }, ""},
		{"no master key, no password", func(in *newCertificateInput) { in.hasMasterKey = false }, "no database master key"},
		{"no master key, mismatch", func(in *newCertificateInput) { in.hasMasterKey, in.mkPassword, in.mkConfirm = false, "a", "b" }, "do not match"},
		{"no master key, password", func(in *newCertificateInput) { in.hasMasterKey, in.mkPassword, in.mkConfirm = false, "a", "a" }, ""},
		{"by password, blank", func(in *newCertificateInput) { in.byPassword = true }, "password is required"},
		{"by password, mismatch", func(in *newCertificateInput) { in.byPassword, in.keyPassword, in.keyConfirm = true, "a", "b" }, "do not match"},
		{"by password, no master key", func(in *newCertificateInput) {
			in.byPassword, in.keyPassword, in.keyConfirm, in.hasMasterKey = true, "a", "a", false
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := ok
			c.edit(&in)
			err := validateNewCertificate(in, now)
			switch {
			case c.want == "" && err != nil:
				t.Errorf("refused: %v", err)
			case c.want != "" && err == nil:
				t.Errorf("accepted; want an error containing %q", c.want)
			case c.want != "" && !strings.Contains(err.Error(), c.want):
				t.Errorf("got %q, want it to contain %q", err, c.want)
			}
		})
	}
}
