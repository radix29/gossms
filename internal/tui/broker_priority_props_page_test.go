package tui

import "testing"

// A conversation priority's three criteria are each optional, and an empty one
// means ANY — how CREATE BROKER PRIORITY spells a criterion it was not given.
// Rendered blank it reads as a row the page failed to load, and rendered as a
// name it would claim the priority is narrower than it is.

func TestBrokerPriorityPageRendersAnAbsentCriterionAsAny(t *testing.T) {
	sc, inst := newFakeConn(t, brokerDBResp(),
		brokerRowByArg(brokerPriorityResp(), "ClaimPriority", 0))
	pages := brokerPriorityPropPages(sc, brokerDB, "ClaimPriority")
	form, apply := loadPage(t, pages[0], inst)

	if apply != nil {
		t.Error("Broker Priority Properties is read-only; its page returned an apply")
	}
	if got := staticValue(t, form, "Remote service"); got != "ANY" {
		t.Errorf("Remote service reads %q for a priority that names none, want ANY", got)
	}
	if got := staticValue(t, form, "Contract"); got != "//claims/Contract" {
		t.Errorf("Contract reads %q", got)
	}
	if got := staticValue(t, form, "Priority level"); got != "8" {
		t.Errorf("Priority level reads %q, want 8", got)
	}
}
