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

// The Signatures page of Certificate and Asymmetric Key Properties, and a
// module's "Signed by" Details rows.

// sigRow is a sys.crypt_properties row in the column order gosmo scans it.
func sigRow(schema, module, typ, desc, signer string) []driver.Value {
	return []driver.Value{int64(1000), schema, module, typ, desc, []byte{0x01, 0x02}, signer}
}

// signableModulesResponse is the database's signable modules: three, so the
// one a test signs is not the first.
func signableModulesResponse() fakeResponse {
	return fakeResponse{match: "o.type IN ('P', 'FN', 'TF', 'TR')", cols: 3, rows: [][]driver.Value{
		{"dbo", "p1", "SQL_STORED_PROCEDURE"},
		{"dbo", "p2", "SQL_STORED_PROCEDURE"},
		{"sales", "f1", "SQL_SCALAR_FUNCTION"},
	}}
}

// loadCertSigPage loads the Signatures page of certificate aaa_cert, whose
// private key is protected by protection, and which signs dbo.p1 and dbo.p2.
func loadCertSigPage(t *testing.T, protection string) (*propsheet.Form, propApply, *fakeInstance) {
	t.Helper()
	sc, inst := newFakeConn(t, dbScopedCredDatabaseRow(),
		fakeResponse{match: "FROM   sys.certificates", arg: "aaa_cert", cols: 15,
			rows: [][]driver.Value{certRow("aaa_cert", protection, certValidExpiry)}},
		fakeResponse{match: "FROM   sys.crypt_properties", arg: "aaa_cert", cols: 7, rows: [][]driver.Value{
			sigRow("dbo", "p1", "SQL_STORED_PROCEDURE", "SIGNATURE BY CERTIFICATE", "aaa_cert"),
			sigRow("dbo", "p2", "SQL_STORED_PROCEDURE", "SIGNATURE BY CERTIFICATE", "aaa_cert"),
		}},
		signableModulesResponse(),
	)
	pages := certificatePropPages(sc, "appdb", "aaa_cert")
	form, apply := loadPage(t, pages[1], inst)
	if apply == nil {
		t.Fatal("the Signatures page returned no apply")
	}
	return form, apply, inst
}

func sigGridRows(t *testing.T, f *propsheet.Form) []string {
	t.Helper()
	return symEncRows(t, f)
}

func TestCertificateSignaturesPageLists(t *testing.T) {
	form, _, _ := loadCertSigPage(t, "ENCRYPTED_BY_MASTER_KEY")
	want := []string{"dbo.p1 Stored procedure Signature", "dbo.p2 Stored procedure Signature"}
	if got := sigGridRows(t, form); !slices.Equal(got, want) {
		t.Errorf("grid = %q, want %q", got, want)
	}
	if got, want := selectRow(t, form, "Sign module").Items(), []string{"dbo.p1", "dbo.p2", "sales.f1"}; !slices.Equal(got, want) {
		t.Errorf("Sign module offers %q, want %q", got, want)
	}
	if !symEncButton(t, form, "Add") {
		t.Error("Add is disabled for a certificate with a private key")
	}
	// The master key opens the private key, so no password is asked for.
	for _, r := range form.Rows() {
		if tr, ok := r.(*propsheet.TextRow); ok && tr.Label() == sheetLabel("Private key password") {
			t.Error("a master-key-protected certificate asks for a password")
		}
	}
	if form.Dirty() {
		t.Error("a freshly loaded page is dirty")
	}
}

// Removing the second signature and adding a module that is not first in the
// list: the drop is applied before the add, each on the chosen module.
func TestCertificateSignaturesPageRemovesAndAdds(t *testing.T) {
	form, apply, inst := loadCertSigPage(t, "ENCRYPTED_BY_MASTER_KEY")

	symEncGrid(t, form).Grid.SetSelectedRow(1)
	clickButton(t, form, "Remove")
	pickItem(t, selectRow(t, form, "Sign module"), "sales.f1")
	clickButton(t, form, "Add")
	want := []string{"dbo.p1 Stored procedure Signature", "sales.f1 Scalar function Signature"}
	if got := sigGridRows(t, form); !slices.Equal(got, want) {
		t.Fatalf("grid = %q, want %q", got, want)
	}
	if !form.Dirty() {
		t.Fatal("the page is not dirty after Remove and Add")
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	wantStmts := []string{
		"DROP SIGNATURE FROM [dbo].[p2] BY CERTIFICATE [aaa_cert]",
		"ADD SIGNATURE TO [sales].[f1] BY CERTIFICATE [aaa_cert]",
	}
	if got := inst.StatementsIn("appdb"); !slices.Equal(got, wantStmts) {
		t.Errorf("statements = %q, want %q", got, wantStmts)
	}
}

// Add after Remove on the same module undoes the Remove rather than dropping
// and re-signing; Add on a module already signed is refused.
func TestCertificateSignaturesPageAddUndoesRemove(t *testing.T) {
	form, apply, inst := loadCertSigPage(t, "ENCRYPTED_BY_MASTER_KEY")

	symEncGrid(t, form).Grid.SetSelectedRow(1)
	clickButton(t, form, "Remove")
	pickItem(t, selectRow(t, form, "Sign module"), "dbo.p2")
	clickButton(t, form, "Add")
	pickItem(t, selectRow(t, form, "Sign module"), "dbo.p1")
	clickButton(t, form, "Add")
	want := []string{"dbo.p1 Stored procedure Signature", "dbo.p2 Stored procedure Signature"}
	if got := sigGridRows(t, form); !slices.Equal(got, want) {
		t.Fatalf("grid = %q, want %q", got, want)
	}
	if form.Dirty() {
		t.Error("the page is dirty after its Remove was undone")
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertNoStatementsIn(t, inst, "appdb")
}

// A password-protected private key needs its password to sign, and none to
// remove a signature.
func TestCertificateSignaturesPageNeedsThePasswordToSign(t *testing.T) {
	form, apply, inst := loadCertSigPage(t, "ENCRYPTED_BY_PASSWORD")

	pickItem(t, selectRow(t, form, "Sign module"), "sales.f1")
	clickButton(t, form, "Add")
	if err := apply(context.Background()); err == nil || !strings.Contains(err.Error(), "Private key password") {
		t.Fatalf("apply with no password = %v, want the password asked for", err)
	}
	assertNoStatementsIn(t, inst, "appdb")

	typeSecret(t, form, "Private key password", "pk secret")
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := []string{"ADD SIGNATURE TO [sales].[f1] BY CERTIFICATE [aaa_cert] WITH PASSWORD = N'pk secret'"}
	if got := inst.StatementsIn("appdb"); !slices.Equal(got, want) {
		t.Errorf("statements = %q, want %q", got, want)
	}
}

func TestCertificateSignaturesPageWithoutPrivateKey(t *testing.T) {
	form, apply, inst := loadCertSigPage(t, "NO_PRIVATE_KEY")
	if symEncButton(t, form, "Add") {
		t.Error("Add is enabled for a certificate with no private key")
	}
	clickButton(t, form, "Remove") // row 0, dbo.p1
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	want := []string{"DROP SIGNATURE FROM [dbo].[p1] BY CERTIFICATE [aaa_cert]"}
	if got := inst.StatementsIn("appdb"); !slices.Equal(got, want) {
		t.Errorf("statements = %q, want %q", got, want)
	}
}

// An asymmetric key's counter signature is listed as one, and removing it is
// DROP COUNTER SIGNATURE.
func TestAsymmetricKeySignaturesPageRemovesACounterSignature(t *testing.T) {
	sc, inst := newFakeConn(t, dbScopedCredDatabaseRow(),
		fakeResponse{match: "FROM   sys.asymmetric_keys", arg: "aaa_key", cols: 10,
			rows: [][]driver.Value{asymKeyRow("aaa_key", "ENCRYPTED_BY_MASTER_KEY")}},
		fakeResponse{match: "FROM   sys.crypt_properties", arg: "aaa_key", cols: 7, rows: [][]driver.Value{
			sigRow("dbo", "p1", "SQL_STORED_PROCEDURE", "SIGNATURE BY ASYMMETRIC KEY", "aaa_key"),
			sigRow("sales", "f1", "SQL_SCALAR_FUNCTION", "COUNTER SIGNATURE BY ASYMMETRIC KEY", "aaa_key"),
		}},
		signableModulesResponse(),
	)
	form, apply := loadPage(t, asymmetricKeyPropPages(sc, "appdb", "aaa_key")[1], inst)
	want := []string{"dbo.p1 Stored procedure Signature", "sales.f1 Scalar function Counter signature"}
	if got := sigGridRows(t, form); !slices.Equal(got, want) {
		t.Fatalf("grid = %q, want %q", got, want)
	}
	symEncGrid(t, form).Grid.SetSelectedRow(1)
	clickButton(t, form, "Remove")
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	wantStmts := []string{"DROP COUNTER SIGNATURE FROM [sales].[f1] BY ASYMMETRIC KEY [aaa_key]"}
	if got := inst.StatementsIn("appdb"); !slices.Equal(got, wantStmts) {
		t.Errorf("statements = %q, want %q", got, wantStmts)
	}
}

func TestModuleDetailListsSigners(t *testing.T) {
	sc, _ := newFakeConn(t, dbScopedCredDatabaseRow(),
		fakeResponse{match: "FROM   sys.crypt_properties", arg: "p2", cols: 7, rows: [][]driver.Value{
			sigRow("dbo", "p2", "SQL_STORED_PROCEDURE", "SIGNATURE BY CERTIFICATE", "aaa_cert"),
			sigRow("dbo", "p2", "SQL_STORED_PROCEDURE", "COUNTER SIGNATURE BY ASYMMETRIC KEY", "aaa_key"),
			sigRow("dbo", "p2", "SQL_STORED_PROCEDURE", "SIGNATURE BY CERTIFICATE", ""),
		}},
		fakeResponse{match: "FROM   sys.crypt_properties", cols: 7},
	)
	node := &explorerNode{label: "dbo.p2", data: nodeData{Type: NodeStoredProcedure, DBName: "appdb", Schema: "dbo", Name: "p2", conn: sc}}
	_, rows, err := moduleDetail(context.Background(), sc, node)
	if err != nil {
		t.Fatalf("moduleDetail: %v", err)
	}
	var got []string
	for _, r := range rows {
		if r[0] == "Signed by" {
			got = append(got, r[1])
		}
	}
	want := []string{"Certificate aaa_cert", "Asymmetric key aaa_key (counter signature)", "Signature by certificate (not visible)"}
	if !slices.Equal(got, want) {
		t.Errorf("Signed by = %q, want %q", got, want)
	}

	node = &explorerNode{label: "dbo.p1", data: nodeData{Type: NodeStoredProcedure, DBName: "appdb", Schema: "dbo", Name: "p1", conn: sc}}
	if _, rows, err = moduleDetail(context.Background(), sc, node); err != nil {
		t.Fatalf("moduleDetail: %v", err)
	}
	if !slices.ContainsFunc(rows, func(r []string) bool { return r[0] == "Signed by" && r[1] == "(not signed)" }) {
		t.Errorf("an unsigned module's rows = %q", rows)
	}
}

func TestModuleSignatureText(t *testing.T) {
	for _, tc := range []struct {
		sig  gosmo.ModuleSignature
		want string
	}{
		{gosmo.ModuleSignature{Kind: gosmo.SignerCertificate, Signer: "c1"}, "Certificate c1"},
		{gosmo.ModuleSignature{Kind: gosmo.SignerAsymmetricKey, Signer: "a1", Counter: true}, "Asymmetric key a1 (counter signature)"},
		{gosmo.ModuleSignature{CryptTypeDesc: "COUNTER SIGNATURE BY CERTIFICATE", Counter: true}, "Counter signature by certificate (not visible)"},
	} {
		if got := moduleSignatureText(&tc.sig); got != tc.want {
			t.Errorf("moduleSignatureText(%+v) = %q, want %q", tc.sig, got, tc.want)
		}
	}
}
