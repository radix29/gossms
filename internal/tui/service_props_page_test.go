package tui

import (
	"strings"
	"testing"
)

// A service's queue comes from a LEFT join in gosmo's read — a service whose
// queue this login cannot see lists with an empty one — and its contracts are
// a second query grouped by service id, so a page reading them by position
// would show another service's list.

func TestServicePageShowsItsQueueAndItsContracts(t *testing.T) {
	sc, inst := newFakeConn(t, brokerDBResp(),
		brokerRowByArg(brokerServiceResp(), "//claims/Service", 1), serviceContractResp())
	pages := brokerServicePropPages(sc, brokerDB, "//claims/Service")
	form, apply := loadPage(t, pages[0], inst)

	if apply != nil {
		t.Error("Service Properties is read-only; its page returned an apply")
	}
	if got := staticValue(t, form, "Queue"); got != "dbo.ClaimQueue" {
		t.Errorf("Queue reads %q, want the queue the service receives on", got)
	}
	values := staticValuesJoined(form)
	for _, want := range []string{"//claims/Contract", "DEFAULT"} {
		if !strings.Contains(values, want) {
			t.Errorf("page does not list contract %s", want)
		}
	}
}
