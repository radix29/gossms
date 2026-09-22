package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// backupCertificateTestDialog builds the Back Up Certificate page the way
// show() would, minus the prefetch, for a certificate whose private key is
// protected as protection says.
func backupCertificateTestDialog(t *testing.T, protection string) *BackupCertificateDialog {
	t.Helper()
	sc, _ := newFakeConn(t)
	d := &BackupCertificateDialog{dbName: "certdb", certName: "c2"}
	d.sc = sc
	d.pages = []string{"General"}
	d.forms = make([]*propsheet.Form, 1)
	d.applyFns = make([]propApply, 1)
	d.buildPages(&certBackupPrefetch{
		cert:       &gosmo.Certificate{Name: "c2", PvtKeyEncryptionType: protection},
		defaultDir: `C:\Backup\`,
	})
	return d
}

func scriptCertificateBackup(t *testing.T, d *BackupCertificateDialog) []string {
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

// The certificate file is suggested in the server's backup directory, and an
// untouched private-key file backs up the public certificate alone.
func TestBackupCertificatePublicOnly(t *testing.T) {
	d := backupCertificateTestDialog(t, "ENCRYPTED_BY_MASTER_KEY")
	stmts := scriptCertificateBackup(t, d)
	want := `USE [certdb];` + "\nGO\n" + `BACKUP CERTIFICATE [c2] TO FILE = N'C:\Backup\c2.cer'`
	if len(stmts) != 1 || stmts[0] != want {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(stmts, "\n"), want)
	}
}

// With a private-key file, both passwords are sent — and Script Changes shows
// placeholders in their place, never what was typed.
func TestBackupCertificateWithAPasswordProtectedKey(t *testing.T) {
	d := backupCertificateTestDialog(t, "ENCRYPTED_BY_PASSWORD")
	f := d.forms[0]
	editText(t, f, "Certificate file", `D:\keys\c2.cer`)
	editText(t, f, "Private key file", `D:\keys\c2.pvk`)
	editText(t, f, "Encryption password", "Enc!pw1")
	editText(t, f, "Confirm password", "Enc!pw1")
	editText(t, f, "Decryption password", "Dec!pw1")

	stmts := scriptCertificateBackup(t, d)
	want := `USE [certdb];` + "\nGO\n" + `BACKUP CERTIFICATE [c2] TO FILE = N'D:\keys\c2.cer' WITH PRIVATE KEY (FILE = N'D:\keys\c2.pvk', ` +
		`ENCRYPTION BY PASSWORD = N'` + scriptedPasswordPlaceholder + `', DECRYPTION BY PASSWORD = N'` + scriptedPasswordPlaceholder + `')`
	if len(stmts) != 1 || stmts[0] != want {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(stmts, "\n"), want)
	}
}

// A certificate with no private key offers no private-key rows at all; one
// protected by the master key offers no Decryption password.
func TestBackupCertificateRowsFollowTheKey(t *testing.T) {
	for _, tc := range []struct {
		protection      string
		keyFile, decryp bool
	}{
		{"NO_PRIVATE_KEY", false, false},
		{"ENCRYPTED_BY_MASTER_KEY", true, false},
		{"ENCRYPTED_BY_PASSWORD", true, true},
	} {
		f := backupCertificateTestDialog(t, tc.protection).forms[0]
		if got := hasRowLabelled(f, "Private key file"); got != tc.keyFile {
			t.Errorf("%s: Private key file shown = %v, want %v", tc.protection, got, tc.keyFile)
		}
		if got := hasRowLabelled(f, "Decryption password"); got != tc.decryp {
			t.Errorf("%s: Decryption password shown = %v, want %v", tc.protection, got, tc.decryp)
		}
	}
}

// hasRowLabelled reports whether the form has an editable row with label.
func hasRowLabelled(f *propsheet.Form, label string) bool {
	for _, r := range f.Rows() {
		if l, ok := r.(interface{ Label() string }); ok && l.Label() == label {
			return true
		}
	}
	return false
}

func TestValidateCertificateBackup(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		file, keyFile, enc, confirm string
		needsDecryption             bool
		wantErr                     string
	}{
		{"public only", `C:\c.cer`, "", "", "", false, ""},
		{"public only ignores an untyped decryption password", `C:\c.cer`, "", "", "", true, ""},
		{"no file", "", "", "", "", false, "certificate file is required"},
		{"same file twice", `C:\c.cer`, `C:\c.cer`, "p", "p", false, "file of its own"},
		{"no encryption password", `C:\c.cer`, `C:\c.pvk`, "", "", false, "password to encrypt"},
		{"mismatch", `C:\c.cer`, `C:\c.pvk`, "p", "q", false, "do not match"},
		{"password key, no decryption password", `C:\c.cer`, `C:\c.pvk`, "p", "p", true, "Decryption password"},
		{"with private key", `C:\c.cer`, `C:\c.pvk`, "p", "p", false, ""},
	} {
		err := validateCertificateBackup(tc.file, tc.keyFile, tc.enc, tc.confirm, tc.needsDecryption)
		switch {
		case tc.wantErr == "" && err != nil:
			t.Errorf("%s: unexpected error %v", tc.name, err)
		case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
			t.Errorf("%s: error = %v, want one containing %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestBackupFilePath(t *testing.T) {
	for _, tc := range []struct{ dir, name, want string }{
		{`C:\Backup`, "c1", `C:\Backup\c1.cer`},
		{`C:\Backup\`, "c1", `C:\Backup\c1.cer`},
		{"/var/opt/mssql/data/", "c1", "/var/opt/mssql/data/c1.cer"},
		{"", "c1", ""},
		{`C:\Backup`, "", ""},
	} {
		if got := backupFilePath(tc.dir, tc.name, ".cer"); got != tc.want {
			t.Errorf("backupFilePath(%q, %q) = %q, want %q", tc.dir, tc.name, got, tc.want)
		}
	}
}

// keyMenuNode is a probed connection to appdb whose effective ALTER on the
// certificate or asymmetric key c1 reads alter, as a node holding its private
// key or not.
func keyMenuNode(t *testing.T, typ NodeType, alter, hasKey bool) *explorerNode {
	t.Helper()
	kind := gosmo.DatabaseSecurableCertificate
	if typ == NodeAsymmetricKey {
		kind = gosmo.DatabaseSecurableAsymmetricKey
	}
	responses := withSecurablePermAnswers(capabilityResponses(true, nil, nil, nil, []string{"ALTER", "CONTROL"}),
		"ALTER", map[string]bool{gosmo.DatabaseSecurableKey(kind, "", "c1"): alter})
	sc, _ := newFakeConn(t, responses...)
	sc.ProbeCapabilities()
	sc.DatabaseCapabilities(context.Background(), "appdb")
	return &explorerNode{data: nodeData{Type: typ, DBName: "appdb", Name: "c1", conn: sc, HasPrivateKey: hasKey}}
}

func keyMenuItem(t *testing.T, items []controls.MenuItem, label string) controls.MenuItem {
	t.Helper()
	i := slices.IndexFunc(items, func(it controls.MenuItem) bool { return it.Label == label })
	if i < 0 {
		t.Fatalf("menu %q has no %q", labelsOf(items), label)
	}
	return items[i]
}

// Remove Private Key is withheld, with its own note, from a certificate or
// asymmetric key with no private key — ALTER on it or not — and otherwise
// follows the effective ALTER on the object. Back Up Certificate is ungated.
func TestRemovePrivateKeyItem(t *testing.T) {
	a := newTestApp()
	for _, typ := range []NodeType{NodeCertificate, NodeAsymmetricKey} {
		for _, tc := range []struct {
			alter, hasKey, want bool
			note                string
		}{
			{true, true, true, ""},
			{false, true, false, "needs ALTER"},
			{true, false, false, "no private key"},
			{false, false, false, "no private key"},
		} {
			node := keyMenuNode(t, typ, tc.alter, tc.hasKey)
			items := a.contextMenuItemsForNode(node)
			it := keyMenuItem(t, items, "Remove Private Key...")
			enabled := it.Enabled == nil || it.Enabled()
			if enabled != tc.want {
				t.Errorf("type %v alter=%v key=%v: enabled = %v, want %v", typ, tc.alter, tc.hasKey, enabled, tc.want)
			}
			if !enabled && !strings.HasPrefix(it.Note, tc.note) {
				t.Errorf("type %v alter=%v key=%v: note = %q, want %q", typ, tc.alter, tc.hasKey, it.Note, tc.note)
			}
			if typ == NodeCertificate {
				if b := keyMenuItem(t, items, "Back Up Certificate..."); b.Enabled != nil && !b.Enabled() {
					t.Errorf("alter=%v key=%v: Back Up Certificate is withheld", tc.alter, tc.hasKey)
				}
			}
		}
	}
}
