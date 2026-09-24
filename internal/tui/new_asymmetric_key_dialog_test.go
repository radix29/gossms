package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// newAsymmetricKeyTestDialog is newCertificateTestDialog for New Asymmetric
// Key.
func newAsymmetricKeyTestDialog(t *testing.T, hasMasterKey bool) *NewAsymmetricKeyDialog {
	t.Helper()
	dmk := int64(0)
	if hasMasterKey {
		dmk = 1
	}
	sc, _ := newFakeConn(t, fakeResponse{match: "##MS_DatabaseMasterKey##", cols: 1, rows: [][]driver.Value{{dmk}}})
	d := &NewAsymmetricKeyDialog{dbName: "keydb"}
	d.sc = sc
	d.pages = []string{"General"}
	d.forms = make([]*propsheet.Form, 1)
	d.applyFns = make([]propApply, 1)
	d.buildPages(&nasymPrefetch{existingNames: newNameSet("", "taken"), hasMasterKey: hasMasterKey})
	return d
}

func scriptNewAsymmetricKey(t *testing.T, d *NewAsymmetricKeyDialog) []string {
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

// No master key: Script Changes creates one first, the password a placeholder.
func TestNewAsymmetricKeyScriptsTheMasterKeyFirst(t *testing.T) {
	d := newAsymmetricKeyTestDialog(t, false)
	f := d.forms[0]
	editText(t, f, "Key name", "k1")
	editRadio(t, f, "Algorithm", "RSA_4096")
	editText(t, f, "Master key password", "S3cret!pw")
	editText(t, f, "Confirm master key password", "S3cret!pw")

	stmts := scriptNewAsymmetricKey(t, d)
	want := []string{
		"USE [keydb];\nGO\nCREATE MASTER KEY ENCRYPTION BY PASSWORD = N'<insert password here>'",
		"USE [keydb];\nGO\nCREATE ASYMMETRIC KEY [k1] WITH ALGORITHM = RSA_4096",
	}
	if strings.Join(stmts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(stmts, "\n"), strings.Join(want, "\n"))
	}
}

// RSA_2048 is the default, and an existing master key gets no second one.
func TestNewAsymmetricKeyDefaultsAndExistingMasterKey(t *testing.T) {
	d := newAsymmetricKeyTestDialog(t, true)
	editText(t, d.forms[0], "Key name", "k1")

	stmts := scriptNewAsymmetricKey(t, d)
	want := "USE [keydb];\nGO\nCREATE ASYMMETRIC KEY [k1] WITH ALGORITHM = RSA_2048"
	if len(stmts) != 1 || stmts[0] != want {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(stmts, "\n"), want)
	}
}

func TestNewAsymmetricKeyByPassword(t *testing.T) {
	d := newAsymmetricKeyTestDialog(t, false)
	f := d.forms[0]
	editText(t, f, "Key name", "k1")
	editRadio(t, f, "Private key encrypted by", "Password")
	editText(t, f, "Password", "k3y!pw")
	editText(t, f, "Confirm password", "k3y!pw")

	stmts := scriptNewAsymmetricKey(t, d)
	want := "USE [keydb];\nGO\nCREATE ASYMMETRIC KEY [k1] WITH ALGORITHM = RSA_2048 ENCRYPTION BY PASSWORD = N'<insert password here>'"
	if len(stmts) != 1 || stmts[0] != want {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(stmts, "\n"), want)
	}
}

// Only the algorithms a compatibility level 130+ database accepts.
func TestNewAsymmetricKeyOffersNoWeakAlgorithm(t *testing.T) {
	for _, a := range asymKeyAlgorithms {
		if a == gosmo.AsymmetricKeyRSA512 || a == gosmo.AsymmetricKeyRSA1024 {
			t.Errorf("offers %s, a syntax error at compatibility level 130+", a)
		}
	}
}

func TestValidateNewAsymmetricKey(t *testing.T) {
	taken := newNameSet("", "taken")
	ok := keyProtectionInput{hasMasterKey: true, dbName: "d"}
	if err := validateNewAsymmetricKey("k", taken, ok); err != nil {
		t.Errorf("valid input refused: %v", err)
	}
	if err := validateNewAsymmetricKey("", taken, ok); err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Errorf("no name: %v", err)
	}
	if err := validateNewAsymmetricKey("TAKEN", taken, ok); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("taken: %v", err)
	}
	// The protection's own checks run too — the table in
	// TestValidateNewCertificate covers them.
	if err := validateNewAsymmetricKey("k", taken, keyProtectionInput{dbName: "d"}); err == nil || !strings.Contains(err.Error(), "no database master key") {
		t.Errorf("no master key, no password: %v", err)
	}
}
