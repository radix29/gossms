package tui

import (
	"context"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// formAndApply pairs a built page's form with its apply, so the New Login
// tests below can drive one without threading two values through every call.
type formAndApply struct {
	form  *propsheet.Form
	apply propApply
}

// newLoginGeneral builds the New Login General page over the fake instance,
// with a prefetch naming two of everything so nothing a test picks is the
// first item in its list — a page that ignored the selection would otherwise
// pass.
func newLoginGeneral(t *testing.T) (*fakeInstance, *formAndApply) {
	t.Helper()
	sc, inst := newFakeConn(t)
	pf := &nloginPrefetch{
		existingNames: map[string]bool{},
		dbNames:       []string{"master", "sales", "warehouse"},
		langNames:     []string{"us_english", "British"},
		certNames:     []string{"AuditCert", "SigningCert"},
		asymKeyNames:  []string{"AuditKey", "SigningKey"},
	}
	form, apply, _ := buildNewLoginGeneralPage(sc, pf)
	return inst, &formAndApply{form: form, apply: apply}
}

// runApply runs the page's apply and fails on an error, returning the
// statements that reached the server.
func (fa *formAndApply) runApply(t *testing.T, inst *fakeInstance) []string {
	t.Helper()
	if err := fa.apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	return inst.Statements()
}

// applyError runs the page's apply expecting a refusal, and asserts nothing
// was written — a refusal that arrives after the CREATE LOGIN is not a
// refusal.
func (fa *formAndApply) applyError(t *testing.T, inst *fakeInstance) string {
	t.Helper()
	err := fa.apply(context.Background())
	if err == nil {
		t.Fatal("apply succeeded, want a refusal")
	}
	if got := inst.Statements(); len(got) != 0 {
		t.Fatalf("the refusal still wrote %v", got)
	}
	return err.Error()
}

// The SQL path is the one that already worked; it is here so the four new
// sources are compared against it rather than against nothing.
func TestNewLoginCreatesASQLLogin(t *testing.T) {
	inst, p := newLoginGeneral(t)
	editText(t, p.form, "Login name", "app_login")
	editText(t, p.form, "Password", "hunter2")
	stmts := p.runApply(t, inst)
	if len(stmts) != 1 || !strings.Contains(stmts[0], "CREATE LOGIN [app_login] WITH PASSWORD") {
		t.Fatalf("statements = %v, want one CREATE LOGIN with a password", stmts)
	}
}

// Windows takes no password, so a page that sent the typed one anyway would
// be refused by gosmo — which is the point: the source drives what is sent.
func TestNewLoginCreatesAWindowsLogin(t *testing.T) {
	inst, p := newLoginGeneral(t)
	editText(t, p.form, "Login name", `CONTOSO\dba`)
	editText(t, p.form, "Password", "typed then abandoned")
	editRadio(t, p.form, "Authentication", "Windows Authentication")
	stmts := p.runApply(t, inst)
	if len(stmts) != 1 || !strings.Contains(stmts[0], `CREATE LOGIN [CONTOSO\dba] FROM WINDOWS`) {
		t.Fatalf("statements = %v, want one CREATE LOGIN FROM WINDOWS", stmts)
	}
	if strings.Contains(stmts[0], "PASSWORD") {
		t.Errorf("the abandoned password reached the statement: %s", stmts[0])
	}
}

// The object id is the half of the Entra source gosmo gained for this page.
func TestNewLoginCreatesAnEntraLoginWithAnObjectID(t *testing.T) {
	inst, p := newLoginGeneral(t)
	editText(t, p.form, "Login name", "sales team")
	editRadio(t, p.form, "Authentication", "Microsoft Entra Authentication")
	editText(t, p.form, "Entra object ID", "3f2504e0-4f89-11d3-9a0c-0305e82c3301")
	stmts := p.runApply(t, inst)
	if len(stmts) != 1 {
		t.Fatalf("statements = %v, want one CREATE LOGIN", stmts)
	}
	if !strings.Contains(stmts[0], "FROM EXTERNAL PROVIDER") ||
		!strings.Contains(stmts[0], "OBJECT_ID = N'3f2504e0-4f89-11d3-9a0c-0305e82c3301'") {
		t.Errorf("statement = %q, want FROM EXTERNAL PROVIDER WITH OBJECT_ID", stmts[0])
	}
}

// No object id is the ordinary case — the server resolves the name itself.
func TestNewLoginEntraWithoutAnObjectIDOmitsTheClause(t *testing.T) {
	inst, p := newLoginGeneral(t)
	editText(t, p.form, "Login name", "dba@contoso.com")
	editRadio(t, p.form, "Authentication", "Microsoft Entra Authentication")
	stmts := p.runApply(t, inst)
	if len(stmts) != 1 || strings.Contains(stmts[0], "OBJECT_ID") {
		t.Fatalf("statements = %v, want a bare FROM EXTERNAL PROVIDER", stmts)
	}
}

// An object id typed under Entra and then abandoned by switching source must
// not ride along — gosmo refuses it, so a page that kept it creates nothing.
func TestNewLoginObjectIDDoesNotSurviveASourceChange(t *testing.T) {
	inst, p := newLoginGeneral(t)
	editText(t, p.form, "Login name", `CONTOSO\dba`)
	editRadio(t, p.form, "Authentication", "Microsoft Entra Authentication")
	editText(t, p.form, "Entra object ID", "3f2504e0-4f89-11d3-9a0c-0305e82c3301")
	editRadio(t, p.form, "Authentication", "Windows Authentication")
	stmts := p.runApply(t, inst)
	if len(stmts) != 1 || strings.Contains(stmts[0], "OBJECT_ID") {
		t.Fatalf("statements = %v, want a plain FROM WINDOWS", stmts)
	}
}

// The mapped sources pick from master's certificates and asymmetric keys.
// The second name in each list, so a page that ignored the pick still fails.
func TestNewLoginCreatesAMappedLogin(t *testing.T) {
	for _, tc := range []struct {
		option, pick, want string
	}{
		{"Mapped to a certificate", "SigningCert", "FROM CERTIFICATE [SigningCert]"},
		{"Mapped to an asymmetric key", "SigningKey", "FROM ASYMMETRIC KEY [SigningKey]"},
	} {
		t.Run(tc.option, func(t *testing.T) {
			inst, p := newLoginGeneral(t)
			editText(t, p.form, "Login name", "signer_login")
			editRadio(t, p.form, "Authentication", tc.option)
			editSelect(t, p.form, "Mapped to", tc.pick)
			stmts := p.runApply(t, inst)
			if len(stmts) != 1 || !strings.Contains(stmts[0], tc.want) {
				t.Fatalf("statements = %v, want %s", stmts, tc.want)
			}
		})
	}
}

// The certificate list and the key list are different lists, and the picker
// holds whichever the current source names. Switching between the two mapped
// sources with a pick already made must not map a certificate login to a key.
func TestNewLoginMappedPickerFollowsTheSource(t *testing.T) {
	inst, p := newLoginGeneral(t)
	editText(t, p.form, "Login name", "signer_login")
	editRadio(t, p.form, "Authentication", "Mapped to a certificate")
	editSelect(t, p.form, "Mapped to", "SigningCert")
	editRadio(t, p.form, "Authentication", "Mapped to an asymmetric key")
	editSelect(t, p.form, "Mapped to", "SigningKey")
	stmts := p.runApply(t, inst)
	if len(stmts) != 1 || !strings.Contains(stmts[0], "FROM ASYMMETRIC KEY [SigningKey]") {
		t.Fatalf("statements = %v, want the key the picker was left on", stmts)
	}
	if strings.Contains(stmts[0], "Cert") {
		t.Errorf("the abandoned certificate reached the statement: %s", stmts[0])
	}
}

// Nothing picked is a refusal rather than a CREATE LOGIN the server rejects.
func TestNewLoginMappedWithNothingPickedIsRefused(t *testing.T) {
	// A prefetch that read no certificates leaves the picker on its sentinel,
	// which is also what a login without permission on master sees.
	sc, inst := newFakeConn(t)
	form, apply, _ := buildNewLoginGeneralPage(sc, &nloginPrefetch{
		existingNames: map[string]bool{}, dbNames: []string{"master"},
	})
	p := &formAndApply{form: form, apply: apply}
	editText(t, p.form, "Login name", "signer_login")
	editRadio(t, p.form, "Authentication", "Mapped to a certificate")
	if msg := p.applyError(t, inst); !strings.Contains(msg, "certificate") {
		t.Errorf("refusal = %q, want it to name the certificate that is missing", msg)
	}
}

// SQL Server refuses DEFAULT_DATABASE and DEFAULT_LANGUAGE for a mapped login
// in CREATE and in ALTER alike (verified live), so a page that sent either
// would leave the login created and the apply failed.
func TestNewLoginMappedRefusesDefaults(t *testing.T) {
	for _, row := range []struct{ label, value string }{
		{"Default database", "sales"},
		{"Default language", "British"},
	} {
		t.Run(row.label, func(t *testing.T) {
			inst, p := newLoginGeneral(t)
			editText(t, p.form, "Login name", "signer_login")
			editRadio(t, p.form, "Authentication", "Mapped to a certificate")
			editSelect(t, p.form, "Mapped to", "SigningCert")
			editSelect(t, p.form, row.label, row.value)
			if msg := p.applyError(t, inst); !strings.Contains(msg, "default database or language") {
				t.Errorf("refusal = %q, want it to name the defaults", msg)
			}
		})
	}
}

// A default database and language still reach a login that can have them, by
// the two routes gosmo uses: DEFAULT_DATABASE in the CREATE for a Windows
// login, and a following ALTER LOGIN for the language.
func TestNewLoginDefaultsReachAWindowsLogin(t *testing.T) {
	inst, p := newLoginGeneral(t)
	editText(t, p.form, "Login name", `CONTOSO\dba`)
	editRadio(t, p.form, "Authentication", "Windows Authentication")
	editSelect(t, p.form, "Default database", "warehouse")
	editSelect(t, p.form, "Default language", "British")
	stmts := p.runApply(t, inst)
	if len(stmts) != 2 {
		t.Fatalf("statements = %v, want the CREATE and the language ALTER", stmts)
	}
	if !strings.Contains(stmts[0], "DEFAULT_DATABASE = [warehouse]") {
		t.Errorf("statements[0] = %q, want the default database in the CREATE", stmts[0])
	}
	if !strings.Contains(stmts[1], "DEFAULT_LANGUAGE") || !strings.Contains(stmts[1], "British") {
		t.Errorf("statements[1] = %q, want the language ALTER", stmts[1])
	}
}

// The password policy is a SQL-login-only follow-up; ticking it under another
// source must not produce an ALTER LOGIN the server refuses.
func TestNewLoginPasswordPolicyIsSQLOnly(t *testing.T) {
	inst, p := newLoginGeneral(t)
	editText(t, p.form, "Login name", `CONTOSO\dba`)
	editCheck(t, p.form, "Enforce password expiration", true)
	editRadio(t, p.form, "Authentication", "Windows Authentication")
	stmts := p.runApply(t, inst)
	if len(stmts) != 1 {
		t.Fatalf("statements = %v, want the CREATE alone", stmts)
	}
}

// The page's own gating is not the only check: gosmo refuses every mismatched
// combination too. This pins the pairing the page relies on.
func TestNewLoginSourcesMatchGosmosOwn(t *testing.T) {
	for _, src := range nloginSources {
		if src.source == gosmo.LoginSourceAuto {
			t.Errorf("%q maps to LoginSourceAuto, which resolves from the password rather than from what the user picked", src.label)
		}
	}
	if len(nloginSources) != 5 {
		t.Errorf("nloginSources has %d entries; gosmo has five real sources", len(nloginSources))
	}
}
