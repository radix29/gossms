package tui

import (
	"context"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// newCredentialTestDialog builds the New Credential dialog's General page the
// way show() would, minus the connection: show() is what allocates forms and
// applyFns, so the test does that itself.
func newCredentialTestDialog(t *testing.T) *NewCredentialDialog {
	t.Helper()
	sc, _ := newFakeConn(t)
	d := &NewCredentialDialog{}
	d.sc = sc
	d.pages = []string{"General"}
	d.forms = make([]*propsheet.Form, 1)
	d.applyFns = make([]propApply, 1)
	d.buildPages(&ncredentialPrefetch{existingNames: map[string]bool{"existing_cred": true}})
	return d
}

// SQL Server stores IDENTITY verbatim, so a pasted account name with trailing
// whitespace produces a credential that will not authenticate, with nothing on
// the page saying why. The preflight already validates the trimmed value; the
// apply has to send the same thing.
func TestNewCredentialTrimsTheIdentity(t *testing.T) {
	d := newCredentialTestDialog(t)

	editText(t, d.forms[0], "Credential name", "  rv_test_cred  ")
	editText(t, d.forms[0], "Identity", "  DOMAIN\\svc_acct  ")
	editText(t, d.forms[0], "Password", "pa'ss")
	editText(t, d.forms[0], "Confirm password", "pa'ss")

	if err := d.preflight(); err != nil {
		t.Fatalf("preflight: %v", err)
	}
	ctx, script := gosmo.WithScript(context.Background())
	if err := d.applyFns[0](ctx); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(script.Statements) != 1 {
		t.Fatalf("want one statement, got %d:\n%s", len(script.Statements), strings.Join(script.Statements, "\n"))
	}
	want := `CREATE CREDENTIAL [rv_test_cred] WITH IDENTITY = N'DOMAIN\svc_acct', SECRET = N'pa''ss'`
	if script.Statements[0] != want {
		t.Errorf("got:\n%s\nwant:\n%s", script.Statements[0], want)
	}
}

// An identity of nothing but whitespace is refused before the round trip.
func TestNewCredentialPreflightRefusesABlankIdentity(t *testing.T) {
	d := newCredentialTestDialog(t)

	editText(t, d.forms[0], "Credential name", "rv_test_cred")
	editText(t, d.forms[0], "Identity", "   ")

	if err := d.preflight(); err == nil {
		t.Fatal("a whitespace-only identity was accepted")
	}
}
