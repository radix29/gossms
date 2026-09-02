package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Credential Properties > General.
//
// The credential scripted here is the second of three, so a page that ignored
// its selection and read whichever row sorts first cannot pass. The by-name
// read is scoped with arg: and placed before the list read, because gosmo's
// CredentialByNameContext query also contains "FROM   sys.credentials" and
// responses match by substring in order.

const credentialUnderTest = "app_cred"

func credentialResponses() []fakeResponse {
	now := time.Now()
	return []fakeResponse{
		{
			match: "FROM   sys.credentials",
			arg:   credentialUnderTest,
			cols:  7,
			rows: [][]driver.Value{
				{int64(2), credentialUnderTest, `DOMAIN\svc_app`, now, now, nil, nil},
			},
		},
		{
			match: "FROM   sys.credentials",
			cols:  7,
			rows: [][]driver.Value{
				{int64(1), "aaa_first_cred", `DOMAIN\svc_first`, now, now, nil, nil},
				{int64(2), credentialUnderTest, `DOMAIN\svc_app`, now, now, nil, nil},
				{int64(3), "zzz_last_cred", `DOMAIN\svc_last`, now, now, nil, nil},
			},
		},
	}
}

func loadCredentialGeneralPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t, credentialResponses()...)
	name := credentialUnderTest
	form, apply := loadPage(t, pageCredentialGeneral(sc, &name), inst)
	return inst, apply, form
}

// The page must load the credential it was opened on, not the first row in
// sys.credentials.
func TestCredentialGeneralLoadsTheSelectedCredential(t *testing.T) {
	_, _, form := loadCredentialGeneralPage(t)

	if got := textRow(t, form, "Identity").Value(); got != `DOMAIN\svc_app` {
		t.Errorf("Identity is %q, want the selected credential's %q", got, `DOMAIN\svc_app`)
	}
}

// A page opened and closed writes nothing — and on this page that includes not
// sending an ALTER, which would clear the secret.
func TestCredentialGeneralWritesNothingWhenUntouched(t *testing.T) {
	inst, apply, _ := loadCredentialGeneralPage(t)

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// The rule this page exists to enforce: ALTER CREDENTIAL resets both halves,
// and omitting SECRET sets the stored secret to NULL. Changing the identity
// with the password blank must therefore be refused, not applied — an applied
// bare ALTER destroys a secret that cannot be read back and cannot be restored.
func TestCredentialGeneralRefusesAnIdentityChangeWithNoPassword(t *testing.T) {
	inst, apply, form := loadCredentialGeneralPage(t)

	editText(t, form, "Identity", `DOMAIN\svc_other`)

	err := apply(t.Context())
	if err == nil {
		t.Fatal("the identity change was applied with a blank password, which clears the stored secret")
	}
	if !strings.Contains(err.Error(), "clears the stored secret") {
		t.Errorf("the error does not say why it was refused: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a statement was sent anyway:\n%s", strings.Join(stmts, "\n"))
	}
}

// With a password typed, both halves go in one ALTER.
func TestCredentialGeneralWritesBothHalvesWhenThePasswordIsTyped(t *testing.T) {
	inst, apply, form := loadCredentialGeneralPage(t)

	editText(t, form, "Identity", `DOMAIN\svc_other`)
	editText(t, form, "Password", "hunter2")
	editText(t, form, "Confirm password", "hunter2")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want one statement, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	want := `ALTER CREDENTIAL [app_cred] WITH IDENTITY = N'DOMAIN\svc_other', SECRET = N'hunter2'`
	if stmts[0] != want {
		t.Errorf("got:\n%s\nwant:\n%s", stmts[0], want)
	}
}

// A password typed on its own — the ordinary "change the password" gesture —
// still has to carry the identity, since ALTER CREDENTIAL requires it and
// would otherwise be a syntax error.
func TestCredentialGeneralPasswordOnlyChangeKeepsTheIdentity(t *testing.T) {
	inst, apply, form := loadCredentialGeneralPage(t)

	editText(t, form, "Password", "hunter2")
	editText(t, form, "Confirm password", "hunter2")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want one statement, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], `IDENTITY = N'DOMAIN\svc_app'`) {
		t.Errorf("the password-only change dropped the identity:\n%s", stmts[0])
	}
}

func TestCredentialGeneralRefusesAMismatchedConfirmation(t *testing.T) {
	inst, apply, form := loadCredentialGeneralPage(t)

	editText(t, form, "Password", "hunter2")
	editText(t, form, "Confirm password", "hunter3")

	if err := apply(t.Context()); err == nil {
		t.Fatal("a mismatched confirmation was accepted")
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("a statement was sent anyway:\n%s", strings.Join(stmts, "\n"))
	}
}

// A provider credential shows its binding and says it is fixed; an ordinary
// one has no provider section at all.
func TestCredentialGeneralShowsTheProviderOnlyForAProviderCredential(t *testing.T) {
	now := time.Now()
	sc, inst := newFakeConn(t, fakeResponse{
		match: "FROM   sys.credentials",
		cols:  7,
		rows: [][]driver.Value{
			{int64(4), "ekm_cred", "ekm_user", now, now, "CRYPTOGRAPHIC PROVIDER", "MyEKM"},
		},
	})
	name := "ekm_cred"
	form, _ := loadPage(t, pageCredentialGeneral(sc, &name), inst)

	var provider *propsheet.StaticRow
	for _, r := range form.Rows() {
		if sr, ok := r.(*propsheet.StaticRow); ok && sr.Label() == sheetLabel("Provider") {
			provider = sr
		}
	}
	if provider == nil {
		t.Fatal("a provider credential shows no Provider row")
	}
	if provider.Value() != "MyEKM" {
		t.Errorf("Provider is %q, want %q", provider.Value(), "MyEKM")
	}

	_, _, plain := loadCredentialGeneralPage(t)
	for _, r := range plain.Rows() {
		if sr, ok := r.(*propsheet.StaticRow); ok && sr.Label() == sheetLabel("Provider") {
			t.Error("an ordinary credential shows a Provider row it can never have")
		}
	}
}
