package tui

import (
	"database/sql/driver"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Table Properties > Change Tracking — the single-table counterpart of
// Database Properties' table grid (database_props_change_tracking_page_test.go).
//
// Turning tracking off discards the table's change data, and an application
// reading it has no way back except a full resync. Both controls are Select
// rows over the same on/off list, so the page decides from two indexes what
// one ALTER TABLE says.

func tableChangeTrackingResponses(rows ...[]driver.Value) []fakeResponse {
	return []fakeResponse{
		dbByNameResp(principalDatabase, 5),
		{match: "sys.change_tracking_databases", cols: 4, rows: [][]driver.Value{
			{int64(1), true, int64(2), "DAYS"},
		}},
		// The by-table read, ahead of any list read for the same reason every
		// by-name response goes first.
		{match: "SCHEMA_NAME(t.schema_id) = @p1", db: principalDatabase, cols: 4, rows: rows},
	}
}

func loadTableChangeTrackingPage(t *testing.T, rows ...[]driver.Value) (*fakeInstance, propApply, *propsheet.Form) {
	t.Helper()
	sc, inst := newFakeConn(t, tableChangeTrackingResponses(rows...)...)
	form, apply := loadPage(t, pageTableChangeTracking(sc, principalDatabase, principalSchema, "Orders"), inst)
	return inst, apply, form
}

// trackedOff and trackedOn are the two states the by-table read can report.
func trackedOff() []driver.Value { return []driver.Value{principalSchema, "Orders", false, false} }
func trackedOn() []driver.Value  { return []driver.Value{principalSchema, "Orders", true, true} }

// TestTableChangeTrackingEnablingCarriesTheColumnSetting. TRACK_COLUMNS_UPDATED
// is a second dropdown folded into the same statement, so it is the one that
// can be lost without the statement looking wrong.
func TestTableChangeTrackingEnablingCarriesTheColumnSetting(t *testing.T) {
	inst, apply, form := loadTableChangeTrackingPage(t, trackedOff())

	editSelect(t, form, "Table change tracking", "ON")
	editSelect(t, form, "Track columns updated", "ON")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, principalDatabase,
		"ALTER TABLE [sales].[Orders] ENABLE CHANGE_TRACKING WITH (TRACK_COLUMNS_UPDATED = ON)")
}

// TestTableChangeTrackingEnablingWithoutColumnsSaysOff — the other half of the
// same dropdown, since a statement that always said ON would pass the test
// above.
func TestTableChangeTrackingEnablingWithoutColumnsSaysOff(t *testing.T) {
	inst, apply, form := loadTableChangeTrackingPage(t, trackedOff())

	editSelect(t, form, "Table change tracking", "ON")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, principalDatabase,
		"ALTER TABLE [sales].[Orders] ENABLE CHANGE_TRACKING WITH (TRACK_COLUMNS_UPDATED = OFF)")
}

// TestTableChangeTrackingDisablingDiscardsTheChangeData is the destructive
// direction.
func TestTableChangeTrackingDisablingDiscardsTheChangeData(t *testing.T) {
	inst, apply, form := loadTableChangeTrackingPage(t, trackedOn())

	editSelect(t, form, "Table change tracking", "OFF")

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatementIn(t, inst, principalDatabase, "ALTER TABLE [sales].[Orders] DISABLE CHANGE_TRACKING")
}

// TestTableChangeTrackingUnlistedTableLoadsAsOff. A table gosmo does not list
// — ms-shipped, or dropped since the tree was populated — must show as "off"
// rather than failing the page, which is what the listing scan this replaced
// did.
func TestTableChangeTrackingUnlistedTableLoadsAsOff(t *testing.T) {
	_, apply, form := loadTableChangeTrackingPage(t)

	if got := selectRow(t, form, "Table change tracking").Value(); got != "OFF" {
		t.Errorf("a table with no change-tracking row shows %q, want OFF", got)
	}
	if apply == nil {
		t.Error("the page came up with no apply closure")
	}
}

// TestTableChangeTrackingUntouchedPageWritesNothing. Both rows are seeded from
// the server, and ALTER TABLE ... ENABLE CHANGE_TRACKING on a table that
// already has it is not free — it resets the tracking baseline.
func TestTableChangeTrackingUntouchedPageWritesNothing(t *testing.T) {
	inst, apply, _ := loadTableChangeTrackingPage(t, trackedOn())

	if err := apply(t.Context()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.StatementsIn(principalDatabase); len(stmts) != 0 {
		t.Errorf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
