package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"
)

// Database Scoped Configurations is eleven controls over one statement shape.
// Every row carries the option name it writes as a *string* alongside the
// widget, in a slice built in the same order the rows are declared, and apply
// walks that slice — so a mismatch between the label a user reads and the name
// stored next to it is invisible until the wrong option changes. MAXDOP and
// PARAMETER_SNIFFING are both query-plan controls whose effect shows up as
// "queries got slower", days later, on a database nobody knowingly touched.
//
// The bool rows have a second table to get wrong: they are Selects over
// OFF/ON, and the option takes the keyword, not the 0/1 the catalog view
// reports. An off-by-one there sets every one of them backwards.

// scopedConfigOptions is the label-to-option-name pairing this page promises,
// written out rather than derived, so a name changed under a label fails here.
var scopedConfigOptions = []struct {
	label  string
	option string
	isBool bool
}{
	{"Max DOP", "MAXDOP", false},
	{"Legacy cardinality estimation", "LEGACY_CARDINALITY_ESTIMATION", true},
	{"Parameter sniffing", "PARAMETER_SNIFFING", true},
	{"Query optimizer hotfixes", "QUERY_OPTIMIZER_HOTFIXES", true},
	{"Interleaved execution for TVFs", "INTERLEAVED_EXECUTION_TVF", true},
	{"Batch mode memory grant feedback", "BATCH_MODE_MEMORY_GRANT_FEEDBACK", true},
	{"Batch mode adaptive joins", "BATCH_MODE_ADAPTIVE_JOINS", true},
	{"TSQL scalar UDF inlining", "TSQL_SCALAR_UDF_INLINING", true},
	{"Accelerated plan forcing", "ACCELERATED_PLAN_FORCING", true},
	{"Optimized plan forcing", "OPTIMIZED_PLAN_FORCING", true},
	{"Global temporary table auto drop", "GLOBAL_TEMPORARY_TABLE_AUTO_DROP", true},
}

// scopedConfigResponses answers the page's two reads: the database by name and
// every scoped configuration in it. Every boolean option is scripted OFF, so
// switching one ON is a real edit in every case.
func scopedConfigResponses() []fakeResponse {
	rows := [][]driver.Value{
		{int64(1), "MAXDOP", "0", "", true},
		// An option the page has no editor for. It belongs in the read-only
		// grid and nowhere else, and it is here so a test that walked the
		// scripted list instead of the page's own rows would notice.
		{int64(99), "XTP_PROCEDURE_EXECUTION_STATISTICS", "0", "", true},
	}
	for _, o := range scopedConfigOptions {
		if o.isBool {
			rows = append(rows, []driver.Value{int64(len(rows) + 1), o.option, "0", "", true})
		}
	}
	return []fakeResponse{
		{match: "FROM sys.databases", arg: "appdb", cols: 8, rows: [][]driver.Value{
			{"appdb", int64(5), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
		}},
		{match: "FROM   sys.database_scoped_configurations", cols: 5, rows: rows},
	}
}

// TestEveryScopedConfigRowWritesTheOptionItIsLabelled is the label-to-name
// table, one page load per row so a statement can only have come from the row
// under test.
func TestEveryScopedConfigRowWritesTheOptionItIsLabelled(t *testing.T) {
	for _, o := range scopedConfigOptions {
		t.Run(o.label, func(t *testing.T) {
			sc, inst := newFakeConn(t, scopedConfigResponses()...)
			form, apply := loadPage(t, pageDatabaseScopedConfig(sc, "appdb"), inst)

			if o.isBool {
				editSelect(t, form, o.label, "ON")
			} else {
				editText(t, form, o.label, "4")
			}
			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}

			stmts := inst.Statements()
			if len(stmts) != 1 {
				t.Fatalf("want exactly one statement, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
			}
			want := "SET " + o.option + " = "
			if o.isBool {
				want += "ON"
			} else {
				want += "4"
			}
			if !strings.Contains(stmts[0], want) {
				t.Errorf("editing %q wrote:\n%s\nwant it to contain: %s", o.label, stmts[0], want)
			}
		})
	}
}

// TestScopedConfigBoolRowsWriteTheKeywordNotTheCatalogValue pins the second
// table. sys.database_scoped_configurations reports a boolean as "0"/"1" and
// ALTER DATABASE SCOPED CONFIGURATION only accepts ON/OFF, so the page carries
// its own OFF/ON pair; selecting OFF has to reach the server as OFF and not as
// index 0.
func TestScopedConfigBoolRowsWriteTheKeywordNotTheCatalogValue(t *testing.T) {
	for _, tc := range []struct{ pick, want string }{
		{"ON", "SET PARAMETER_SNIFFING = ON"},
		{"OFF", "SET PARAMETER_SNIFFING = OFF"},
	} {
		t.Run(tc.pick, func(t *testing.T) {
			sc, inst := newFakeConn(t, scopedConfigResponses()...)
			form, apply := loadPage(t, pageDatabaseScopedConfig(sc, "appdb"), inst)

			// Everything is scripted OFF, so selecting OFF is only an edit
			// once the row has been moved off it.
			if tc.pick == "OFF" {
				selectRow(t, form, "Parameter sniffing").SetSelected(1)
			}
			editSelect(t, form, "Parameter sniffing", tc.pick)
			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}
			stmts := inst.Statements()
			if len(stmts) != 1 || !strings.Contains(stmts[0], tc.want) {
				t.Fatalf("selecting %q wrote %q, want one statement containing %q", tc.pick, stmts, tc.want)
			}
		})
	}
}

// TestScopedConfigLoadsEachOptionsOwnValue is the read half of the same
// pairing. Two options are scripted with values that differ from every other,
// so a row showing its neighbour's value is visible here rather than only
// after an Apply writes it back.
func TestScopedConfigLoadsEachOptionsOwnValue(t *testing.T) {
	resp := scopedConfigResponses()
	for i := range resp[1].rows {
		switch resp[1].rows[i][1] {
		case "MAXDOP":
			resp[1].rows[i][2] = "8"
		case "PARAMETER_SNIFFING":
			resp[1].rows[i][2] = "1"
		}
	}
	sc, inst := newFakeConn(t, resp...)
	form, _ := loadPage(t, pageDatabaseScopedConfig(sc, "appdb"), inst)

	if got := textRow(t, form, "Max DOP").Value(); got != "8" {
		t.Errorf("Max DOP shows %q, want %q", got, "8")
	}
	if got := selectRow(t, form, "Parameter sniffing").Value(); got != "ON" {
		t.Errorf("Parameter sniffing shows %q, want ON", got)
	}
	// Its neighbour in the declaration order, scripted 0, must not have
	// followed it.
	if got := selectRow(t, form, "Legacy cardinality estimation").Value(); got != "OFF" {
		t.Errorf("Legacy cardinality estimation shows %q, want OFF", got)
	}
}

// TestScopedConfigWritesNothingWhenUntouched. Every row on this page is set
// from the server during load, and a row that came back dirty would rewrite an
// optimizer setting on a database whose Properties were only opened to look.
func TestScopedConfigWritesNothingWhenUntouched(t *testing.T) {
	sc, inst := newFakeConn(t, scopedConfigResponses()...)
	_, apply := loadPage(t, pageDatabaseScopedConfig(sc, "appdb"), inst)
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Fatalf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// TestScopedConfigOffersNoControlForAnOptionTheServerDoesNotHave. Half these
// options postdate SQL Server 2019, so on an older instance
// sys.database_scoped_configurations has no row for them at all. Before this
// was fixed the boolean ones still rendered as a live OFF/ON dropdown that was
// left out of the page's tracked list: switching one on and pressing OK
// reported success and sent nothing.
//
// Asserted through the widget rather than the statement log on purpose — the
// old behaviour wrote nothing either, so "no statement" passes on the bug.
func TestScopedConfigOffersNoControlForAnOptionTheServerDoesNotHave(t *testing.T) {
	resp := scopedConfigResponses()
	var kept [][]driver.Value
	for _, r := range resp[1].rows {
		if r[1] == "MAXDOP" || r[1] == "PARAMETER_SNIFFING" {
			continue
		}
		kept = append(kept, r)
	}
	resp[1].rows = kept

	sc, inst := newFakeConn(t, resp...)
	form, apply := loadPage(t, pageDatabaseScopedConfig(sc, "appdb"), inst)

	for _, label := range []string{"Max DOP", "Parameter sniffing"} {
		row := textRow(t, form, label)
		if row.Value() != "N/A" {
			t.Errorf("%q shows %q for a missing option, want N/A", label, row.Value())
		}
		// A disabled row refuses Edit, which is what stops the user from
		// making a change the page cannot send.
		row.Edit("4")
		if row.Dirty() {
			t.Errorf("%q accepted an edit for an option this server does not have", label)
		}
	}
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Fatalf("wrote:\n%s", strings.Join(stmts, "\n"))
	}
}
