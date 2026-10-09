package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
)

// Change Tracking's table grid is two toggle columns over one row per table,
// read back index-parallel against the table list it was built from. Turning
// change tracking off on a table discards its change data — an application
// that was reading it has no way back except a full resync — and the row that
// does it is addressed by position, not by name.
//
// The database half has its own pairing to get wrong: retention is a number
// and a unit from two separate controls, and the unit is an index into a
// package-level slice.

func changeTrackingResponses() []fakeResponse {
	return []fakeResponse{
		dbByNameResp("appdb", 5),
		{match: "sys.change_tracking_databases", cols: 4, rows: [][]driver.Value{
			{int64(1), true, int64(2), "DAYS"},
		}},
		// Three tables in two schemas, with different starting states so no
		// single row's state can stand in for another's.
		{match: "sys.change_tracking_tables", db: "appdb", cols: 4, rows: [][]driver.Value{
			{"dbo", "Customers", int64(1), true},
			{"dbo", "Orders", int64(1), false},
			{"sales", "Regions", int64(0), false},
		}},
	}
}

// TestChangeTrackingTogglesActOnTheTableTheRowIsNamedFor. Three tables, and the
// one acted on is the third — a page that used the first row whatever was
// clicked would pass on a one-table fixture.
func TestChangeTrackingTogglesActOnTheTableTheRowIsNamedFor(t *testing.T) {
	sc, inst := newFakeConn(t, changeTrackingResponses()...)
	form, apply := loadPage(t, pageDatabaseChangeTracking(sc, "appdb"), inst)

	toggleByName(t, toggleGrid(t, form), "sales.Regions", 0)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	for _, want := range []string{"[sales].[Regions]", "ENABLE CHANGE_TRACKING"} {
		if !strings.Contains(stmts[0], want) {
			t.Errorf("wrote:\n%s\nwant it to contain: %s", stmts[0], want)
		}
	}
}

// TestChangeTrackingDisablesTheTableTheRowIsNamedFor is the destructive
// direction: DISABLE CHANGE_TRACKING throws away the table's change data.
func TestChangeTrackingDisablesTheTableTheRowIsNamedFor(t *testing.T) {
	sc, inst := newFakeConn(t, changeTrackingResponses()...)
	form, apply := loadPage(t, pageDatabaseChangeTracking(sc, "appdb"), inst)

	toggleByName(t, toggleGrid(t, form), "dbo.Orders", 0)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	for _, want := range []string{"[dbo].[Orders]", "DISABLE CHANGE_TRACKING"} {
		if !strings.Contains(stmts[0], want) {
			t.Errorf("wrote:\n%s\nwant it to contain: %s", stmts[0], want)
		}
	}
}

// TestChangeTrackingColumnsUpdatedIsADifferentColumn. The grid's two toggle
// columns feed two different arguments of the same call, so reading them in
// the wrong order enables tracking on a table the user only asked to track
// columns for — and both produce a statement that looks reasonable.
func TestChangeTrackingColumnsUpdatedIsADifferentColumn(t *testing.T) {
	sc, inst := newFakeConn(t, changeTrackingResponses()...)
	form, apply := loadPage(t, pageDatabaseChangeTracking(sc, "appdb"), inst)

	// Orders is already tracked with columns off; ticking only the second
	// column must leave tracking on and turn column tracking on.
	toggleByName(t, toggleGrid(t, form), "dbo.Orders", 1)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.StatementsIn("appdb")
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	if !strings.Contains(stmts[0], "ENABLE CHANGE_TRACKING") || !strings.Contains(stmts[0], "TRACK_COLUMNS_UPDATED = ON") {
		t.Errorf("wrote:\n%s\nwant ENABLE CHANGE_TRACKING with TRACK_COLUMNS_UPDATED = ON", stmts[0])
	}
}

// TestChangeTrackingLoadsEachTablesOwnState is the read half of the same
// index-parallel pairing.
func TestChangeTrackingLoadsEachTablesOwnState(t *testing.T) {
	sc, inst := newFakeConn(t, changeTrackingResponses()...)
	form, _ := loadPage(t, pageDatabaseChangeTracking(sc, "appdb"), inst)

	tg := toggleGrid(t, form)
	want := map[string][2]bool{
		"dbo.Customers": {true, true},
		"dbo.Orders":    {true, false},
		"sales.Regions": {false, false},
	}
	if len(tg.Text()) != len(want) {
		t.Fatalf("the grid has %d rows, want %d", len(tg.Text()), len(want))
	}
	for i, row := range tg.Text() {
		w, ok := want[row[0]]
		if !ok {
			t.Errorf("unexpected grid row %q", row[0])
			continue
		}
		got := tg.Values()[i]
		if got[0] != w[0] || got[1] != w[1] {
			t.Errorf("%s shows %v, want %v", row[0], got, w)
		}
	}
}

// TestChangeTrackingRetentionUnitIsWrittenAsItsKeyword. The unit control is an
// index into retentionUnits and the statement needs the keyword, so an
// off-by-one sets a two-*minute* retention where the user chose two days —
// which silently discards change data an application had days to read.
func TestChangeTrackingRetentionUnitIsWrittenAsItsKeyword(t *testing.T) {
	for _, unit := range retentionUnits {
		t.Run(unit, func(t *testing.T) {
			sc, inst := newFakeConn(t, changeTrackingResponses()...)
			form, apply := loadPage(t, pageDatabaseChangeTracking(sc, "appdb"), inst)

			// The page opens on DAYS, so that arm needs moving off it first.
			if unit == "DAYS" {
				selectRow(t, form, "Retention period units").SetSelected(1)
			}
			editSelect(t, form, "Retention period units", unit)

			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			// ALTER DATABASE is not itself database-scoped — it names the
			// database, so it runs on whatever connection the pool hands out.
			stmts := inst.Statements()
			if len(stmts) != 1 {
				t.Fatalf("want exactly one statement, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
			}
			// The period comes from a different control and has to travel
			// with the unit unchanged.
			if !strings.Contains(stmts[0], "CHANGE_RETENTION = 2 "+unit) {
				t.Errorf("choosing %s wrote:\n%s\nwant CHANGE_RETENTION = 2 %s", unit, stmts[0], unit)
			}
		})
	}
}

// TestChangeTrackingWritesNothingWhenUntouched. Every control here is set from
// the server on load, and the database-level write is gated on four Dirty()
// checks at once — so a single row that came back dirty would re-issue ALTER
// DATABASE ... SET CHANGE_TRACKING on a database opened only to look at.
func TestChangeTrackingWritesNothingWhenUntouched(t *testing.T) {
	sc, inst := newFakeConn(t, changeTrackingResponses()...)
	_, apply := loadPage(t, pageDatabaseChangeTracking(sc, "appdb"), inst)
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Fatalf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// TestChangeTrackingOffUnticksTheTablesFirst. Switching a tracked database off
// and unticking its tables in one Apply is the only way this page turns a
// database off, and the server refuses the database half while any table is
// still tracked (Msg 22115) — so the page wrote the database first, failed,
// and never reached the tables. Off is tables first, then the database.
func TestChangeTrackingOffUnticksTheTablesFirst(t *testing.T) {
	sc, inst := newFakeConn(t, changeTrackingResponses()...)
	form, apply := loadPage(t, pageDatabaseChangeTracking(sc, "appdb"), inst)

	tg := toggleGrid(t, form)
	toggleByName(t, tg, "dbo.Customers", 0)
	toggleByName(t, tg, "dbo.Orders", 0)
	editSelect(t, form, "Change tracking", "OFF")

	if err := form.Validate(); err != nil {
		t.Fatalf("validate refused a consistent edit: %v", err)
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 3 {
		t.Fatalf("want two table statements and the database's, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	for _, s := range stmts[:2] {
		if !strings.Contains(s, "DISABLE CHANGE_TRACKING") {
			t.Errorf("a table write came after the database's, or is not a disable:\n%s", strings.Join(stmts, "\n"))
		}
	}
	if !strings.Contains(stmts[2], "CHANGE_TRACKING = OFF") {
		t.Errorf("the database was not switched off last:\n%s", strings.Join(stmts, "\n"))
	}
}

// TestChangeTrackingOnStillWritesTheDatabaseFirst is the other direction, which
// the reorder must leave alone: a table can be tracked only once its database
// is.
func TestChangeTrackingOnStillWritesTheDatabaseFirst(t *testing.T) {
	r := changeTrackingResponses()
	r[1].rows = [][]driver.Value{{int64(0), false, int64(0), ""}} // not tracked: the LEFT JOIN's ISNULLs
	r[2].rows = [][]driver.Value{{"dbo", "Customers", int64(0), false}}
	sc, inst := newFakeConn(t, r...)
	form, apply := loadPage(t, pageDatabaseChangeTracking(sc, "appdb"), inst)

	editSelect(t, form, "Change tracking", "ON")
	editText(t, form, "Retention period", "2")
	toggleByName(t, toggleGrid(t, form), "dbo.Customers", 0)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 2 || !strings.Contains(stmts[0], "ALTER DATABASE") || !strings.Contains(stmts[1], "ENABLE CHANGE_TRACKING") {
		t.Errorf("want the database enabled, then the table:\n%s", strings.Join(stmts, "\n"))
	}
}

// TestChangeTrackingOffWithATableLeftTrackedIsRefused. The server would refuse
// the database half anyway, but only after the table writes before it had
// landed; the form says which table first and nothing is written.
func TestChangeTrackingOffWithATableLeftTrackedIsRefused(t *testing.T) {
	sc, inst := newFakeConn(t, changeTrackingResponses()...)
	form, _ := loadPage(t, pageDatabaseChangeTracking(sc, "appdb"), inst)

	toggleByName(t, toggleGrid(t, form), "dbo.Customers", 0)
	editSelect(t, form, "Change tracking", "OFF")

	err := form.Validate()
	if err == nil {
		t.Fatal("switching the database off with dbo.Orders still tracked was accepted")
	}
	if !strings.Contains(err.Error(), "dbo.Orders") || strings.Contains(err.Error(), "dbo.Customers") {
		t.Errorf("the refusal should name exactly the table still ticked: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Errorf("wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
