package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// The symmetric key Properties' Encryption page: the ENCRYPTION BY list, its
// Add / Remove, the decryptor every change is applied with, and the
// last-encryption guard.

// symEncKeys is the database the page reads: aaa_key (certificate aaa_cert +
// password), parent_key (aaa_cert, so it opens with nothing typed), pw_key
// (a password only, so it does not), pwcert_key (pw_cert, whose private key
// has a password) and child_key (parent_key).
var symEncKeys = map[string][][]driver.Value{
	"aaa_key": {
		symKeyRow("aaa_key", 256, "ENCRYPTION BY CERTIFICATE OAEP256", "aaa_cert"),
		symKeyRow("aaa_key", 256, "ENCRYPTION BY PASSWORD V2", nil),
	},
	"parent_key": {symKeyRow("parent_key", 258, "ENCRYPTION BY CERTIFICATE OAEP256", "aaa_cert")},
	"pw_key":     {symKeyRow("pw_key", 259, "ENCRYPTION BY PASSWORD V2", nil)},
	"pwcert_key": {symKeyRow("pwcert_key", 260, "ENCRYPTION BY CERTIFICATE OAEP256", "pw_cert")},
	"child_key":  {symKeyRow("child_key", 261, "ENCRYPTION BY SYMMETRIC KEY", "parent_key")},
}

// loadSymEncPage loads the Encryption page of key in appdb.
func loadSymEncPage(t *testing.T, key string) (*propsheet.Form, propApply, *fakeInstance) {
	t.Helper()
	var all [][]driver.Value
	for _, k := range []string{"aaa_key", "parent_key", "pw_key", "pwcert_key", "child_key"} {
		all = append(all, symEncKeys[k]...)
	}
	sc, inst := newFakeConn(t, dbScopedCredDatabaseRow(),
		fakeResponse{match: "FROM   sys.symmetric_keys", arg: key, cols: 13, rows: symEncKeys[key]},
		fakeResponse{match: "FROM   sys.symmetric_keys", cols: 13, rows: all},
		fakeResponse{match: "FROM   sys.certificates", cols: 15, rows: [][]driver.Value{
			certRow("aaa_cert", "ENCRYPTED_BY_MASTER_KEY", certValidExpiry),
			certRow("old_cert", "NO_PRIVATE_KEY", certValidExpiry),
			certRow("pw_cert", "ENCRYPTED_BY_PASSWORD", certValidExpiry),
		}},
		asymKeyListResponse(),
	)
	pages := symmetricKeyPropPages(sc, "appdb", key)
	form, apply := loadPage(t, pages[1], inst)
	if apply == nil {
		t.Fatal("the Encryption page returned no apply")
	}
	return form, apply, inst
}

// symEncGrid is the page's grid, as displayed.
func symEncGrid(t *testing.T, f *propsheet.Form) *propsheet.GridRow {
	t.Helper()
	for _, r := range f.Rows() {
		if g, ok := r.(*propsheet.GridRow); ok {
			return g
		}
	}
	t.Fatal("no grid on the Encryption page")
	return nil
}

func symEncRows(t *testing.T, f *propsheet.Form) []string {
	t.Helper()
	g := symEncGrid(t, f).Grid
	var out []string
	for i := 0; g.Row(i) != nil; i++ {
		out = append(out, strings.TrimSpace(strings.Join(g.Row(i), " ")))
	}
	return out
}

func symEncButton(t *testing.T, f *propsheet.Form, label string) bool {
	t.Helper()
	for _, r := range f.Rows() {
		if br, ok := r.(*propsheet.ButtonsRow); ok {
			for _, b := range br.Buttons() {
				if b.Label() == label {
					return b.Enabled()
				}
			}
		}
	}
	t.Fatalf("no button %q", label)
	return false
}

// typeSecret types into one of the page's password fields. They are not
// dirty-tracked — a password typed and never used is not a change — so
// editText's dirty check does not apply.
func typeSecret(t *testing.T, f *propsheet.Form, label, value string) {
	t.Helper()
	textRow(t, f, label).Edit(value)
}

func pickItem(t *testing.T, r *propsheet.SelectRow, item string) {
	t.Helper()
	i := slices.Index(r.Items(), item)
	if i < 0 {
		t.Fatalf("%s offers %q, not %q", r.Label(), r.Items(), item)
	}
	r.Edit(i)
}

func TestSymmetricKeyEncryptionPageOffers(t *testing.T) {
	form, _, _ := loadSymEncPage(t, "aaa_key")
	if got, want := symEncRows(t, form), []string{"Certificate aaa_cert", "Password"}; !slices.Equal(got, want) {
		t.Errorf("grid = %q, want %q", got, want)
	}
	// Every certificate and asymmetric key — encrypting needs only the public
	// half — but of the symmetric keys only those that open with nothing
	// typed, and never the key itself.
	want := []string{"Password", "Certificate aaa_cert", "Certificate old_cert", "Certificate pw_cert",
		"Asymmetric key aaa_key", "Asymmetric key pub_key", "Symmetric key parent_key", "Symmetric key child_key"}
	if got := selectRow(t, form, "Add encryption by").Items(); !slices.Equal(got, want) {
		t.Errorf("Add offers %q, want %q", got, want)
	}
	// The master-key-protected certificate needs no password, so it is the
	// default decryptor even though the password could open the key too.
	dec := selectRow(t, form, "Decrypt by")
	if got := dec.Items(); !slices.Equal(got, []string{"Certificate aaa_cert", "Password"}) {
		t.Errorf("Decrypt by offers %q", got)
	}
	if dec.Value() != "Certificate aaa_cert" {
		t.Errorf("default decryptor = %q", dec.Value())
	}
	if form.Dirty() {
		t.Error("a freshly loaded page is dirty")
	}
}

func TestSymmetricKeyEncryptionAddsThenRemovesOnOneOpenEach(t *testing.T) {
	form, apply, inst := loadSymEncPage(t, "aaa_key")

	pickItem(t, selectRow(t, form, "Add encryption by"), "Asymmetric key aaa_key")
	clickButton(t, form, "Add")
	// Remove the password: it has to be typed, the server finds it by it.
	symEncGrid(t, form).Grid.SetSelectedRow(1)
	clickButton(t, form, "Remove")
	if got := symEncRows(t, form); !slices.Equal(got, []string{"Certificate aaa_cert", "Password", "Asymmetric key aaa_key"}) {
		t.Fatalf("Remove without the password changed the grid: %q", got)
	}
	typeSecret(t, form, "Password", "old secret")
	clickButton(t, form, "Remove")
	if got := symEncRows(t, form); !slices.Equal(got, []string{"Certificate aaa_cert", "Asymmetric key aaa_key"}) {
		t.Fatalf("grid = %q", got)
	}
	if textRow(t, form, "Password").Value() != "" {
		t.Error("the removed password was left in the field")
	}
	if !form.Dirty() {
		t.Fatal("the page is not dirty after an Add and a Remove")
	}

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 2 {
		t.Fatalf("apply ran %d statements, want 2: %q", len(stmts), stmts)
	}
	for i, want := range []string{
		"ALTER SYMMETRIC KEY [aaa_key] ADD ENCRYPTION BY ASYMMETRIC KEY [aaa_key]",
		"ALTER SYMMETRIC KEY [aaa_key] DROP ENCRYPTION BY PASSWORD = N'old secret'",
	} {
		if !strings.Contains(stmts[i], want) {
			t.Errorf("statement %d does not contain %q:\n%s", i, want, stmts[i])
		}
		if !strings.Contains(stmts[i], "OPEN SYMMETRIC KEY [aaa_key] DECRYPTION BY CERTIFICATE [aaa_cert]") {
			t.Errorf("statement %d does not open the key by the certificate:\n%s", i, stmts[i])
		}
	}
}

// The key cannot lose its last encryption (Msg 15558): Remove is disabled
// while one is left, counting a pending Add, since apply adds first.
func TestSymmetricKeyEncryptionKeepsTheLastOne(t *testing.T) {
	form, _, _ := loadSymEncPage(t, "pw_key")
	if symEncButton(t, form, "Remove") {
		t.Error("Remove is enabled on a key with one encryption")
	}
	pickItem(t, selectRow(t, form, "Add encryption by"), "Certificate aaa_cert")
	clickButton(t, form, "Add")
	if !symEncButton(t, form, "Remove") {
		t.Fatal("Remove stays disabled once a second encryption is pending")
	}
	typeSecret(t, form, "Password", "pw")
	symEncGrid(t, form).Grid.SetSelectedRow(0)
	clickButton(t, form, "Remove")
	if got := symEncRows(t, form); !slices.Equal(got, []string{"Certificate aaa_cert"}) {
		t.Fatalf("grid = %q", got)
	}
	if symEncButton(t, form, "Remove") {
		t.Error("Remove is enabled with one encryption left")
	}
	// Revert brings back the loaded list and the guard with it.
	symEncGrid(t, form).Revert()
	if got := symEncRows(t, form); !slices.Equal(got, []string{"Password"}) || form.Dirty() {
		t.Errorf("after Revert grid = %q, dirty = %v", got, form.Dirty())
	}
}

// A password decryptor, or a certificate whose private key a password
// protects, needs that password; apply refuses before writing anything.
func TestSymmetricKeyEncryptionDecryptorPassword(t *testing.T) {
	for _, tc := range []struct{ key, decryptor, open string }{
		{"pw_key", "Password", "DECRYPTION BY PASSWORD = N'pw'"},
		{"pwcert_key", "Certificate pw_cert", "DECRYPTION BY CERTIFICATE [pw_cert] WITH PASSWORD = N'pw'"},
	} {
		form, apply, inst := loadSymEncPage(t, tc.key)
		if got := selectRow(t, form, "Decrypt by").Value(); got != tc.decryptor {
			t.Errorf("%s: decryptor = %q, want %q", tc.key, got, tc.decryptor)
		}
		pickItem(t, selectRow(t, form, "Add encryption by"), "Certificate aaa_cert")
		clickButton(t, form, "Add")
		err := apply(context.Background())
		if err == nil || !strings.Contains(err.Error(), "Decryption password") {
			t.Errorf("%s: apply with no decryption password = %v", tc.key, err)
		}
		if stmts := inst.StatementsIn("appdb"); len(stmts) != 0 {
			t.Errorf("%s: apply wrote %q before refusing", tc.key, stmts)
		}
		typeSecret(t, form, "Decryption password", "pw")
		if err := apply(context.Background()); err != nil {
			t.Fatalf("%s: apply: %v", tc.key, err)
		}
		if stmts := inst.StatementsIn("appdb"); len(stmts) != 1 || !strings.Contains(stmts[0], tc.open) {
			t.Errorf("%s: statements = %q, want one opening %s", tc.key, stmts, tc.open)
		}
	}
}

// Adding a symmetric key opens it too, by the chain the page resolved; and a
// key encrypted by one opens through it.
func TestSymmetricKeyEncryptionOpensTheParentChain(t *testing.T) {
	form, apply, inst := loadSymEncPage(t, "aaa_key")
	pickItem(t, selectRow(t, form, "Add encryption by"), "Symmetric key parent_key")
	clickButton(t, form, "Add")
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 1 || !strings.Contains(stmts[0], "OPEN SYMMETRIC KEY [parent_key] DECRYPTION BY CERTIFICATE [aaa_cert]") ||
		!strings.Contains(stmts[0], "ADD ENCRYPTION BY SYMMETRIC KEY [parent_key]") {
		t.Errorf("statements = %q", stmts)
	}

	form, _, _ = loadSymEncPage(t, "child_key")
	if got := selectRow(t, form, "Decrypt by").Items(); !slices.Equal(got, []string{"Symmetric key parent_key"}) {
		t.Errorf("child_key's decryptors = %q", got)
	}
}

// Removing the decryptor's own encryption goes last: once it is gone the key
// no longer opens that way, and each change opens it afresh.
func TestSymmetricKeyEncryptionRemovesTheDecryptorLast(t *testing.T) {
	form, apply, inst := loadSymEncPage(t, "aaa_key")
	pickItem(t, selectRow(t, form, "Add encryption by"), "Certificate old_cert")
	clickButton(t, form, "Add")
	grid := symEncGrid(t, form).Grid
	grid.SetSelectedRow(0) // aaa_cert, the decryptor
	clickButton(t, form, "Remove")
	typeSecret(t, form, "Password", "old secret")
	grid.SetSelectedRow(0) // now the password
	clickButton(t, form, "Remove")
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 3 {
		t.Fatalf("statements = %q", stmts)
	}
	for i, want := range []string{"ADD ENCRYPTION BY CERTIFICATE [old_cert]", "DROP ENCRYPTION BY PASSWORD", "DROP ENCRYPTION BY CERTIFICATE [aaa_cert]"} {
		if !strings.Contains(stmts[i], want) {
			t.Errorf("statement %d = %q, want %s", i, stmts[i], want)
		}
	}
}

func TestSymmetricKeyEncryptionScriptsPasswordsAsPlaceholders(t *testing.T) {
	form, apply, _ := loadSymEncPage(t, "pw_key")
	pickItem(t, selectRow(t, form, "Add encryption by"), "Password")
	typeSecret(t, form, "Password", "new secret")
	typeSecret(t, form, "Confirm password", "new secret")
	clickButton(t, form, "Add")
	typeSecret(t, form, "Decryption password", "old secret")

	ctx, script := gosmo.WithScript(t.Context())
	if err := apply(ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := strings.Join(script.Statements, "\n")
	if strings.Contains(got, "secret") {
		t.Errorf("the script carries a password:\n%s", got)
	}
	if !strings.Contains(got, "DECRYPTION BY PASSWORD = N'"+scriptedPasswordPlaceholder+"'") ||
		!strings.Contains(got, "ADD ENCRYPTION BY PASSWORD = N'"+scriptedPasswordPlaceholder+"'") {
		t.Errorf("script:\n%s", got)
	}
}

func TestSymmetricKeyEncryptionAddRefusals(t *testing.T) {
	form, _, _ := loadSymEncPage(t, "aaa_key")
	add := selectRow(t, form, "Add encryption by")

	pickItem(t, add, "Certificate aaa_cert")
	clickButton(t, form, "Add")
	pickItem(t, add, "Password")
	clickButton(t, form, "Add")
	typeSecret(t, form, "Password", "a")
	typeSecret(t, form, "Confirm password", "b")
	clickButton(t, form, "Add")
	if got := symEncRows(t, form); !slices.Equal(got, []string{"Certificate aaa_cert", "Password"}) || form.Dirty() {
		t.Errorf("a duplicate, a blank password or a mismatch changed the grid: %q", got)
	}

	// Add after Remove undoes the Remove instead of dropping and re-adding.
	symEncGrid(t, form).Grid.SetSelectedRow(0)
	clickButton(t, form, "Remove")
	pickItem(t, add, "Certificate aaa_cert")
	clickButton(t, form, "Add")
	if form.Dirty() {
		t.Error("Remove then Add of the same certificate left the page dirty")
	}
}

// The page is gated on the effective ALTER on the key, alone — B9. Each row
// is what the server did on 13 and 17 (2026-09-22): an ALTER-on-key grantee
// may ADD / DROP ENCRYPTION with no CONTROL, and CONTROL with ALTER denied is
// refused (Msg 15151). The Delete set gated on CONTROL and got both wrong.
func TestSymmetricKeyEncryptionPageIsGated(t *testing.T) {
	for _, tc := range []struct {
		name           string
		alter, control bool
		want           bool
	}{
		{"ALTER on the key alone", true, false, true},
		{"ALTER ANY SYMMETRIC KEY, or the owner", true, true, true},
		{"CONTROL on the key, ALTER denied", false, true, false},
		{"VIEW DEFINITION only", false, false, false},
	} {
		key := gosmo.DatabaseSecurableKey(gosmo.DatabaseSecurableSymmetricKey, "", "k1")
		responses := withSecurableAnswers(capabilityResponses(true, nil, nil, nil,
			[]string{"ALTER", "CONTROL", "ALTER ANY SYMMETRIC KEY"}),
			map[string]bool{key: tc.control})
		responses = withSecurablePermAnswers(responses, "ALTER", map[string]bool{key: tc.alter})
		sc, _ := newFakeConn(t, responses...)
		sc.ProbeCapabilities()

		p := symmetricKeyPropPages(sc, "appdb", "k1")[1]
		if p.requiresIn != "appdb" || p.requiresObject != "k1" {
			t.Fatalf("requires asked in %q on %q", p.requiresIn, p.requiresObject)
		}
		if got := pageReadOnlyReason(context.Background(), sc, p) == ""; got != tc.want {
			t.Errorf("%s: writable = %v, want %v", tc.name, got, tc.want)
		}
	}
}
