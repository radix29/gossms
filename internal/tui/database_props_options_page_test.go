package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"
)

// optionsPageResponses scripts the two reads the Options page makes: the
// by-name lookup and the options row. Every boolean option comes back OFF, so
// a test that switches one ON knows which direction it moved.
func optionsPageResponses() []fakeResponse {
	return []fakeResponse{
		{match: "compatibility_level, collation_name", cols: 8, rows: [][]driver.Value{{
			"appdb", int64(7), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now(),
		}}},
		{match: "page_verify_option_desc", cols: 25, rows: [][]driver.Value{
			append([]driver.Value{"sa", "CHECKSUM", "MULTI_USER", "NONE", false, "OFF"}, falses(19)...),
		}},
	}
}

// TestEveryDatabaseOptionRowWritesTheOptionItIsLabelled is the test this page
// most needed, and the one docs/testing.md's round-trip rule is about.
//
// The page is twenty-one label/DatabaseOption/items triples built by
// dbOptBoolRow and dbOptSelectRow, and load and apply read the same table —
// so a triple that pairs the wrong label with the wrong option is invisible
// to anything that only checks the page round-trips. The row labelled "Auto
// shrink" would set AUTO_CLOSE, the page would report success, and the only
// evidence would be a database that behaves unlike its own Properties dialog.
//
// Naming each pair here is what makes that catchable, and it is also why this
// asserts on the statement rather than on the row: the row cannot tell you
// which option it is bound to.
func TestEveryDatabaseOptionRowWritesTheOptionItIsLabelled(t *testing.T) {
	// Label -> the ALTER DATABASE ... SET clause it must produce. Every row
	// on the page that dbOptSelectRow tracks is here; adding a row to the
	// page without adding it here is caught by the count check below.
	want := map[string]string{
		"Auto close":                   "AUTO_CLOSE ON",
		"Auto create statistics":       "AUTO_CREATE_STATISTICS ON",
		"Auto shrink":                  "AUTO_SHRINK ON",
		"Auto update statistics":       "AUTO_UPDATE_STATISTICS ON",
		"Auto update statistics async": "AUTO_UPDATE_STATISTICS_ASYNC ON",
		"Close cursor on commit":       "CURSOR_CLOSE_ON_COMMIT ON",
		"ANSI NULL default":            "ANSI_NULL_DEFAULT ON",
		"ANSI NULLS enabled":           "ANSI_NULLS ON",
		"ANSI padding enabled":         "ANSI_PADDING ON",
		"ANSI warnings enabled":        "ANSI_WARNINGS ON",
		"Arithmetic abort enabled":     "ARITHABORT ON",
		"Concat null yields null":      "CONCAT_NULL_YIELDS_NULL ON",
		"Numeric round-abort":          "NUMERIC_ROUNDABORT ON",
		"Quoted identifier":            "QUOTED_IDENTIFIER ON",
		"Recursive triggers":           "RECURSIVE_TRIGGERS ON",
		"Read committed snapshot":      "READ_COMMITTED_SNAPSHOT ON",
		"Allow snapshot isolation":     "ALLOW_SNAPSHOT_ISOLATION ON",
		"Trustworthy":                  "TRUSTWORTHY ON",
		"Containment type":             "CONTAINMENT PARTIAL",
		"Default cursor":               "CURSOR_DEFAULT LOCAL",
		"Page verify":                  "PAGE_VERIFY NONE",
	}
	choice := map[string]string{
		"Containment type": "PARTIAL",
		"Default cursor":   "LOCAL",
		"Page verify":      "NONE",
	}

	for label, clause := range want {
		t.Run(label, func(t *testing.T) {
			sc, inst := newFakeConn(t, optionsPageResponses()...)
			form, apply := loadPage(t, pageDatabaseOptions(sc, "appdb"), inst)

			value := choice[label]
			if value == "" {
				value = "ON"
			}
			editSelect(t, form, label, value)

			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			stmts := inst.Statements()
			if len(stmts) != 1 {
				t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
			}
			if got, want := stmts[0], "ALTER DATABASE [appdb] SET "+clause; got != want {
				t.Errorf("got:  %s\nwant: %s", got, want)
			}
		})
	}

	// A row added to the page but not to the table above would otherwise be
	// silently untested — which is the same blind spot in a different place.
	sc, inst := newFakeConn(t, optionsPageResponses()...)
	form, _ := loadPage(t, pageDatabaseOptions(sc, "appdb"), inst)
	var selects int
	for _, r := range form.Rows() {
		if _, ok := r.(interface{ Items() []string }); ok {
			selects++
		}
	}
	// The three the table does not cover are Restrict access and Compatibility
	// level, which apply writes through their own methods, and nothing else.
	if got := len(want) + 2; got != selects {
		t.Errorf("the page has %d dropdowns; this test names %d plus the 2 handled separately", selects, len(want))
	}
}

// TestRestrictAccessDoesNotGoThroughSetDatabaseOption pins the one dropdown
// deliberately built with propsheet.Select rather than dbOptSelectRow, so it
// stays out of the tracked table.
//
// It matters because the two paths emit different statements. SetUserAccess
// appends WITH ROLLBACK IMMEDIATE; the generic option path does not, and
// "ALTER DATABASE ... SET SINGLE_USER" without it blocks until every other
// connection leaves — a Properties dialog that appears to hang, on a database
// nobody can now connect to. Moving this row into the tracked table would look
// like tidying and would produce exactly that.
func TestRestrictAccessDoesNotGoThroughSetDatabaseOption(t *testing.T) {
	sc, inst := newFakeConn(t, optionsPageResponses()...)
	form, apply := loadPage(t, pageDatabaseOptions(sc, "appdb"), inst)

	editSelect(t, form, "Restrict access", "SINGLE_USER")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "WITH ROLLBACK IMMEDIATE") {
		t.Errorf("Restrict access did not go through SetUserAccess:\n%s", stmts[0])
	}
}

// A6: the Compatibility level dropdown must offer only levels the connected
// server accepts. Against 2017 the fixed 100..170 list offered 150, 160 and
// 170, and picking one got "Valid values of the database compatibility level
// are 100, 110, 120, 130 or 140. (15048)" — a dropdown entry that can only
// fail.
func TestCompatibilityLevelDropdownIsCappedAtTheServerVersion(t *testing.T) {
	resp := func(level int64) []fakeResponse {
		r := optionsPageResponses()
		r[0].rows[0][4] = level
		return r
	}

	t.Run("2017 offers nothing above 140", func(t *testing.T) {
		sc, inst := newFakeConnAtVersion(t, "14.0.2120.1", resp(140)...)
		form, _ := loadPage(t, pageDatabaseOptions(sc, "appdb"), inst)

		items := selectRow(t, form, "Compatibility level").Items()
		for _, bad := range []string{"150", "160", "170"} {
			if slices.Contains(items, bad) {
				t.Errorf("2017 offers %q, which the server rejects: %v", bad, items)
			}
		}
		if got := items[len(items)-1]; got != "140" {
			t.Errorf("highest level offered is %q, want 140: %v", got, items)
		}
	})

	// The cap must not hide what the database actually is: one restored from a
	// newer instance still displays its real level, selected, even though the
	// server cannot be asked to set it.
	t.Run("a restored database above the cap still shows its level", func(t *testing.T) {
		sc, inst := newFakeConnAtVersion(t, "14.0.2120.1", resp(160)...)
		form, _ := loadPage(t, pageDatabaseOptions(sc, "appdb"), inst)

		row := selectRow(t, form, "Compatibility level")
		items := row.Items()
		if !slices.Contains(items, "160") {
			t.Fatalf("the database's own level 160 is missing: %v", items)
		}
		if got := items[row.Selected()]; got != "160" {
			t.Errorf("selected %q, want 160: %v", got, items)
		}
		if slices.Contains(items, "150") || slices.Contains(items, "170") {
			t.Errorf("levels the server rejects are still offered: %v", items)
		}
	})

	t.Run("2025 offers 170", func(t *testing.T) {
		sc, inst := newFakeConnAtVersion(t, "17.0.1125.2", resp(170)...)
		form, _ := loadPage(t, pageDatabaseOptions(sc, "appdb"), inst)

		items := selectRow(t, form, "Compatibility level").Items()
		if got := items[len(items)-1]; got != "170" {
			t.Errorf("highest level offered is %q, want 170: %v", got, items)
		}
	})
}

// TestCompatibilityLevelWritesItsOwnStatement covers the other row outside the
// tracked table. Compatibility level is a number, not one of the simple SET
// values the generic path accepts, so it has to have its own write.
func TestCompatibilityLevelWritesItsOwnStatement(t *testing.T) {
	sc, inst := newFakeConn(t, optionsPageResponses()...)
	form, apply := loadPage(t, pageDatabaseOptions(sc, "appdb"), inst)

	editSelect(t, form, "Compatibility level", "150")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want exactly one statement, got %d: %q", len(stmts), stmts)
	}
	if got, want := stmts[0], "ALTER DATABASE [appdb] SET COMPATIBILITY_LEVEL = 150"; got != want {
		t.Errorf("got:  %s\nwant: %s", got, want)
	}
}

// TestDatabaseOptionsPageWritesOnlyWhatChanged: twenty-three rows, one edit,
// one statement. A page that wrote every row on OK would look identical in the
// UI and would reset options the user never opened the page to touch.
func TestDatabaseOptionsPageWritesOnlyWhatChanged(t *testing.T) {
	sc, inst := newFakeConn(t, optionsPageResponses()...)
	form, apply := loadPage(t, pageDatabaseOptions(sc, "appdb"), inst)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply on an untouched page: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Fatalf("an untouched page wrote %d statements: %q", len(stmts), stmts)
	}

	editSelect(t, form, "Auto shrink", "ON")
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 1 {
		t.Errorf("one edit wrote %d statements: %q", len(stmts), stmts)
	}
}
