package tui

import (
	"database/sql/driver"
	"strings"
	"testing"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

const agListenerFixtureDNS = "aag1-listener"

// agListenerResponses is the group plus one listener with one address. The
// address read goes first: its JOIN names sys.availability_group_listeners
// too, and the first matching answer wins.
func agListenerResponses() []fakeResponse {
	return []fakeResponse{
		agGroupResponse(),
		{match: "sys.availability_group_listener_ip_addresses", cols: 5, rows: [][]driver.Value{
			{"lst-1", "10.0.0.50", "255.255.255.0", false, "ONLINE"},
		}},
		{match: "FROM sys.availability_group_listeners l", cols: 7, rows: [][]driver.Value{
			{"ag-0001", "lst-1", agListenerFixtureDNS, int64(1433), true, "", false},
		}},
	}
}

func loadAGListenerPage(t *testing.T) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t, agListenerResponses()...)
	form, apply := loadPage(t, pageAGListenerGeneral(sc, agFixtureName, agListenerFixtureDNS), inst)
	return inst, apply, form
}

// Script Changes runs the apply closure against a WithScript context and
// then leaves the page as it was. The closure used to clear the pending
// addresses on its way out, so after scripting the page stopped being dirty
// while its grid still listed the address "To be added" — and the next Apply
// sent nothing. See docs/ui-rules.md, "An apply closure never writes page
// state".
func TestAGListenerScriptChangesKeepsThePendingAddress(t *testing.T) {
	inst, apply, form := loadAGListenerPage(t)

	editText(t, form, "IP address", "10.0.1.50")
	editText(t, form, "Subnet mask", "255.255.255.0")
	clickButton(t, form, "Add Address")

	scriptCtx, script := gosmo.WithScript(t.Context())
	if err := apply(scriptCtx); err != nil {
		t.Fatalf("script apply: %v", err)
	}
	if got := script.String(); !strings.Contains(got, "10.0.1.50") {
		t.Fatalf("the script does not add the pending address:\n%s", got)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Fatalf("Script Changes reached the server:\n%s", strings.Join(stmts, "\n"))
	}

	if !form.Dirty() {
		t.Error("the page is no longer dirty after Script Changes; the pending address would never be applied")
	}
	grid := plainGrid(t, form)
	if row := grid.Row(1); row == nil || row[0] != "10.0.1.50" || row[2] != "To be added" {
		t.Errorf("grid row 1 is %q, want the pending 10.0.1.50 still marked To be added", row)
	}

	// And the real Apply that follows still sends it.
	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "ADD IP (N'10.0.1.50', N'255.255.255.0')")
}
