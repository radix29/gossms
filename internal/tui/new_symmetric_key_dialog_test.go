package tui

import (
	"context"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// newSymmetricKeyTestDialog is newAsymmetricKeyTestDialog for New Symmetric
// Key. The pickers' second entries are the ones the tests pick, so a dialog
// that ignored the selection and took the first object would fail.
func newSymmetricKeyTestDialog(t *testing.T) (*NewSymmetricKeyDialog, *fakeInstance) {
	t.Helper()
	sc, rec := newFakeConn(t)
	d := &NewSymmetricKeyDialog{dbName: "keydb"}
	d.sc = sc
	d.pages = []string{"General"}
	d.forms = make([]*propsheet.Form, 1)
	d.applyFns = make([]propApply, 1)
	d.buildPages(&nsymPrefetch{
		existingNames:  map[string]bool{"taken": true},
		certificates:   []string{"cert_a", "cert_b"},
		asymmetricKeys: []string{"asym_a", "asym_b"},
	})
	return d, rec
}

func scriptNewSymmetricKey(t *testing.T, d *NewSymmetricKeyDialog) []string {
	t.Helper()
	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	ctx, script := gosmo.WithScript(context.Background())
	if err := d.applyFns[0](ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return script.Statements
}

// Every encryptor at once, in gosmo's order, each secret a placeholder — and
// no master key statement, which a symmetric key does not need.
func TestNewSymmetricKeyScriptsEveryEncryptionWithPlaceholders(t *testing.T) {
	d, _ := newSymmetricKeyTestDialog(t)
	f := d.forms[0]
	editText(t, f, "Key name", "k1")
	editRadio(t, f, "Algorithm", "AES_128")
	editSelect(t, f, "Certificate", "cert_b")
	editSelect(t, f, "Asymmetric key", "asym_b")
	editText(t, f, "Password", "S3cret!pw")
	editText(t, f, "Confirm password", "S3cret!pw")
	editText(t, f, "Key source", "the source")
	editText(t, f, "Confirm key source", "the source")
	editText(t, f, "Identity value", "the identity")
	editText(t, f, "Confirm identity value", "the identity")

	stmts := scriptNewSymmetricKey(t, d)
	want := "USE [keydb];\nCREATE SYMMETRIC KEY [k1] WITH ALGORITHM = AES_128, " +
		"KEY_SOURCE = N'<insert key source here>', IDENTITY_VALUE = N'<insert identity value here>' " +
		"ENCRYPTION BY CERTIFICATE [cert_b], PASSWORD = N'<insert password here>', ASYMMETRIC KEY [asym_b]"
	if len(stmts) != 1 || stmts[0] != want {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(stmts, "\n"), want)
	}
	for _, secret := range []string{"S3cret!pw", "the source", "the identity"} {
		if strings.Contains(stmts[0], secret) {
			t.Errorf("script carries %q in clear", secret)
		}
	}
}

// AES_256 is the default, and one encryptor is enough.
func TestNewSymmetricKeyDefaults(t *testing.T) {
	d, _ := newSymmetricKeyTestDialog(t)
	editText(t, d.forms[0], "Key name", "k1")
	editSelect(t, d.forms[0], "Asymmetric key", "asym_b")

	stmts := scriptNewSymmetricKey(t, d)
	want := "USE [keydb];\nCREATE SYMMETRIC KEY [k1] WITH ALGORITHM = AES_256 ENCRYPTION BY ASYMMETRIC KEY [asym_b]"
	if len(stmts) != 1 || stmts[0] != want {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(stmts, "\n"), want)
	}
}

// A real Apply sends the typed secrets, not the placeholders.
func TestNewSymmetricKeyAppliesTheTypedSecrets(t *testing.T) {
	d, rec := newSymmetricKeyTestDialog(t)
	f := d.forms[0]
	editText(t, f, "Key name", "k1")
	editText(t, f, "Password", "S3cret!pw")
	editText(t, f, "Confirm password", "S3cret!pw")
	editText(t, f, "Key source", "src")
	editText(t, f, "Confirm key source", "src")
	editText(t, f, "Identity value", "idv")
	editText(t, f, "Confirm identity value", "idv")
	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	if err := d.applyFns[0](context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := "CREATE SYMMETRIC KEY [k1] WITH ALGORITHM = AES_256, KEY_SOURCE = N'src', IDENTITY_VALUE = N'idv' ENCRYPTION BY PASSWORD = N'S3cret!pw'"
	if got := rec.StatementsIn("keydb"); len(got) != 1 || got[0] != want {
		t.Fatalf("applied %q, want %q", got, want)
	}
}

// Only the algorithms a compatibility level 130+ database accepts.
func TestNewSymmetricKeyOffersOnlyAES(t *testing.T) {
	for _, a := range symKeyAlgorithms {
		if !strings.HasPrefix(string(a), "AES_") {
			t.Errorf("offers %s, a syntax error at compatibility level 130+", a)
		}
	}
}

func TestValidateNewSymmetricKey(t *testing.T) {
	ok := newSymmetricKeyInput{name: "k", certificate: "c", existingNames: map[string]bool{"taken": true}, dbName: "d"}
	if err := validateNewSymmetricKey(ok); err != nil {
		t.Fatalf("valid input refused: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(*newSymmetricKeyInput)
		want string
	}{
		{"no name", func(in *newSymmetricKeyInput) { in.name = "" }, "name is required"},
		{"taken, any case", func(in *newSymmetricKeyInput) { in.name = "TAKEN" }, "already exists"},
		{"temporary", func(in *newSymmetricKeyInput) { in.name = "#k" }, "temporary key"},
		{"global temporary", func(in *newSymmetricKeyInput) { in.name = "##k" }, "temporary key"},
		{"no encryptor", func(in *newSymmetricKeyInput) { in.certificate = "" }, "choose a certificate"},
		{"password mismatch", func(in *newSymmetricKeyInput) { in.password, in.passwordConfirm = "a", "b" }, "passwords do not match"},
		{"key source mismatch", func(in *newSymmetricKeyInput) {
			in.keySource, in.sourceConfirm, in.identityValue, in.identityConfirm = "a", "b", "i", "i"
		}, "key sources do not match"},
		{"identity mismatch", func(in *newSymmetricKeyInput) {
			in.keySource, in.sourceConfirm, in.identityValue, in.identityConfirm = "s", "s", "a", "b"
		}, "identity values do not match"},
		{"key source alone", func(in *newSymmetricKeyInput) { in.keySource, in.sourceConfirm = "s", "s" }, "or neither"},
		{"identity value alone", func(in *newSymmetricKeyInput) { in.identityValue, in.identityConfirm = "i", "i" }, "or neither"},
	} {
		in := ok
		tc.edit(&in)
		if err := validateNewSymmetricKey(in); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
	// A password alone is enough.
	pw := ok
	pw.certificate, pw.password, pw.passwordConfirm = "", "p", "p"
	if err := validateNewSymmetricKey(pw); err != nil {
		t.Errorf("password alone refused: %v", err)
	}
}
