package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Route Properties is the second writable page here, and its one trap is that
// ALTER ROUTE can change a setting but never clear one: the server refuses an
// empty value and NULL does not parse. A page treating an emptied row as
// "leave it alone" loses the user's edit silently; one sending the empty value
// is refused by the server with a message about syntax.

func loadRoutePage(t *testing.T, name string, row int) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t, brokerDBResp(), brokerRowByArg(routeResp(), name, row))
	pages := routePropPages(sc, brokerDB, name)
	if len(pages) != 1 {
		t.Fatalf("want one page, got %d", len(pages))
	}
	form, apply := loadPage(t, pages[0], inst)
	return inst, apply, form
}

func TestRoutePageSendsOnlyTheSettingThatChanged(t *testing.T) {
	inst, apply, form := loadRoutePage(t, "ClaimRoute", 1)

	editText(t, form, "Address", "TCP://newhost:4022")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmt := oneBrokerStatement(t, inst)
	if !strings.Contains(stmt, "ADDRESS = N'TCP://newhost:4022'") {
		t.Errorf("wrote:\n%s\nwant the new address", stmt)
	}
	for _, unwanted := range []string{"SERVICE_NAME", "MIRROR_ADDRESS", "LIFETIME", "BROKER_INSTANCE"} {
		if strings.Contains(stmt, unwanted) {
			t.Errorf("wrote:\n%s\nwant no %s clause — nothing on that row changed", stmt, unwanted)
		}
	}
}

// TestRoutePageRefusesToClearASetting is the whole reason this page reads its
// rows rather than restating them: there is no statement for it.
func TestRoutePageRefusesToClearASetting(t *testing.T) {
	inst, apply, form := loadRoutePage(t, "ClaimRoute", 1)

	editText(t, form, "Mirror address", "")

	err := apply(context.Background())
	if err == nil {
		t.Fatal("apply succeeded in clearing a route setting, which ALTER ROUTE cannot do")
	}
	if !strings.Contains(err.Error(), "cannot be cleared") {
		t.Errorf("error %q does not say the setting cannot be cleared", err)
	}
	if stmts := inst.StatementsIn(brokerDB); len(stmts) != 0 {
		t.Errorf("wrote %d statements for a refused edit:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
}

// TestRoutePageRefusesAZeroLifetime. LIFETIME must be 1 or more, and 0 is what
// a route with no lifetime shows — so a page sending the row back unchecked
// would put a clause on every route that never expires.
func TestRoutePageRefusesAZeroLifetime(t *testing.T) {
	inst, apply, form := loadRoutePage(t, "ClaimRoute", 1)

	editText(t, form, "Lifetime (seconds)", "0")

	if err := apply(context.Background()); err == nil {
		t.Fatal("apply succeeded with LIFETIME = 0")
	}
	if stmts := inst.StatementsIn(brokerDB); len(stmts) != 0 {
		t.Errorf("wrote %d statements for a refused edit:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
}

// TestRoutePageLeavesAnUntouchedNeverExpiringRouteAlone. AutoCreatedLocal has
// no remote service, no broker instance, no mirror and no lifetime, so every
// one of those rows loads empty or zero — the state a page that restated its
// rows would try to write back, and be refused for.
func TestRoutePageLeavesAnUntouchedNeverExpiringRouteAlone(t *testing.T) {
	inst, apply, form := loadRoutePage(t, "AutoCreatedLocal", 0)

	if got := staticValue(t, form, "Expires"); got != "(never)" {
		t.Errorf("Expires reads %q for a route with no lifetime, want (never)", got)
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.StatementsIn(brokerDB); len(stmts) != 0 {
		t.Errorf("wrote %d statements for an untouched page:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
}
