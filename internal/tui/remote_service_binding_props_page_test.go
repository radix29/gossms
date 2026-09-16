package tui

import "testing"

// A binding's user is what authenticates the remote service through the
// certificate it owns; is_anonymous_on is the separate answer to "no
// credentials at all". A page conflating the two describes a security posture
// the server does not have.

func TestRemoteServiceBindingPageShowsItsUserAndAnonymousFlag(t *testing.T) {
	sc, inst := newFakeConn(t, brokerDBResp(),
		brokerRowByArg(remoteServiceBindingResp(), "ClaimBinding", 0))
	pages := remoteServiceBindingPropPages(sc, brokerDB, "ClaimBinding")
	form, apply := loadPage(t, pages[0], inst)

	if apply != nil {
		t.Error("Remote Service Binding Properties is read-only; its page returned an apply")
	}
	if got := staticValue(t, form, "User"); got != "claim_user" {
		t.Errorf("User reads %q, want the binding's remote principal", got)
	}
	if got := staticValue(t, form, "Anonymous"); got != boolStr(false) {
		t.Errorf("Anonymous reads %q, want %q", got, boolStr(false))
	}
	if got := staticValue(t, form, "Remote service"); got != "//claims/Remote" {
		t.Errorf("Remote service reads %q", got)
	}
}
