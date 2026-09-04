package tui

import (
	"context"
	"database/sql/driver"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/propsheet"
)

// Query Store's page is thirteen editors that all leave through one
// QueryStoreOptions struct and one ALTER DATABASE ... SET QUERY_STORE
// statement. Nothing on screen ties an editor to the clause it fills, so two
// fields crossed in that struct — the flush interval into the statistics
// interval, the compile-CPU threshold into the execution-CPU one — write a
// configuration nobody chose and report success. Every value below is
// distinct for exactly that reason: a crossed pair puts the wrong number in
// the right-looking clause.
//
// The two action checkboxes are on the same page and are not configuration:
// Clear discards every query and plan Query Store has collected. Ticking one
// by accident is the worst thing this page can do, so both are pinned to run
// only when ticked.

// queryStoreResponses answers the page's two reads. The scripted starting
// values are deliberately unlike the values the tests edit to, so every edit
// below is a real change.
func queryStoreResponses() []fakeResponse {
	return []fakeResponse{
		{match: "FROM sys.databases", arg: "appdb", cols: 8, rows: [][]driver.Value{
			{"appdb", int64(5), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
		}},
		{match: "FROM   sys.database_query_store_options", cols: 16, rows: [][]driver.Value{{
			"READ_WRITE", "READ_WRITE", int64(0), int64(37), int64(100),
			int64(900), int64(60), int64(200),
			"AUTO", "AUTO",
			int64(30), "ON",
			int64(1), int64(2), int64(3), int64(4),
		}}},
	}
}

// TestQueryStoreWritesEveryFieldIntoItsOwnClause sets all thirteen editors to
// distinct values in one pass and reads the single statement back clause by
// clause. One pass rather than one per field because the page writes every
// option on every apply: a per-field test would see the other twelve carrying
// their loaded values and could not tell a crossed pair from a correct one.
func TestQueryStoreWritesEveryFieldIntoItsOwnClause(t *testing.T) {
	sc, inst := newFakeConn(t, queryStoreResponses()...)
	form, apply := loadPage(t, pageDatabaseQueryStore(sc, "appdb"), inst)

	// CUSTOM, so the capture-policy clause is emitted at all.
	editSelect(t, form, "Requested state", "READ_ONLY")
	editSelect(t, form, "Query capture mode", "CUSTOM")
	editText(t, form, "Max size", "512")
	editSelect(t, form, "Size based cleanup mode", "OFF")
	editText(t, form, "Stale query threshold", "45")
	editText(t, form, "Data flush interval", "660")
	editText(t, form, "Statistics interval", "15")
	editText(t, form, "Max plans per query", "77")
	editSelect(t, form, "Wait stats capture", "OFF")
	editText(t, form, "Custom: execution count", "88")
	editText(t, form, "Custom: total compile CPU", "1234")
	editText(t, form, "Custom: total execution CPU", "5678")
	editText(t, form, "Custom: stale threshold", "99")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}

	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want one ALTER DATABASE, got %d:\n%s", len(stmts), strings.Join(stmts, "\n"))
	}
	got := stmts[0]
	for label, clause := range map[string]string{
		"Requested state":             "OPERATION_MODE = READ_ONLY",
		"Query capture mode":          "QUERY_CAPTURE_MODE = CUSTOM",
		"Max size":                    "MAX_STORAGE_SIZE_MB = 512",
		"Size based cleanup mode":     "SIZE_BASED_CLEANUP_MODE = OFF",
		"Stale query threshold":       "CLEANUP_POLICY = (STALE_QUERY_THRESHOLD_DAYS = 45)",
		"Data flush interval":         "DATA_FLUSH_INTERVAL_SECONDS = 660",
		"Statistics interval":         "INTERVAL_LENGTH_MINUTES = 15",
		"Max plans per query":         "MAX_PLANS_PER_QUERY = 77",
		"Wait stats capture":          "WAIT_STATS_CAPTURE_MODE = OFF",
		"Custom: execution count":     "EXECUTION_COUNT = 88",
		"Custom: total compile CPU":   "TOTAL_COMPILE_CPU_TIME_MS = 1234",
		"Custom: total execution CPU": "TOTAL_EXECUTION_CPU_TIME_MS = 5678",
		"Custom: stale threshold":     "STALE_CAPTURE_POLICY_THRESHOLD = 99 HOURS",
	} {
		if !strings.Contains(got, clause) {
			t.Errorf("%q did not reach its clause: wanted %q in\n%s", label, clause, got)
		}
	}
	if !strings.Contains(got, "ALTER DATABASE [appdb]") {
		t.Errorf("statement does not name the database:\n%s", got)
	}
}

// OFF is its own statement shape — SET QUERY_STORE = OFF, with no option
// list at all — so it is the one operation-mode value the clause assertions
// above cannot reach.
func TestQueryStoreOffTurnsItOffOutright(t *testing.T) {
	sc, inst := newFakeConn(t, queryStoreResponses()...)
	form, apply := loadPage(t, pageDatabaseQueryStore(sc, "appdb"), inst)

	editSelect(t, form, "Requested state", "OFF")
	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	assertOneStatement(t, inst, "SET QUERY_STORE = OFF")
}

// A page opened to look at must write nothing. Every apply here rewrites the
// whole configuration, so a row dirtied on load would resend thirteen options
// — including an operation mode — for a database nobody edited.
func TestQueryStoreWritesNothingWhenUntouched(t *testing.T) {
	sc, inst := newFakeConn(t, queryStoreResponses()...)
	_, apply := loadPage(t, pageDatabaseQueryStore(sc, "appdb"), inst)

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if stmts := inst.Statements(); len(stmts) != 0 {
		t.Fatalf("an untouched page wrote:\n%s", strings.Join(stmts, "\n"))
	}
}

// Flush and Clear run only when their checkbox is ticked, and each runs its
// own action rather than the other's: Flush persists what is in memory,
// Clear throws away everything collected so far.
func TestQueryStoreActionsRunOnlyWhenTicked(t *testing.T) {
	for _, tc := range []struct {
		label string
		want  string
		other string
	}{
		{"Flush data to disk on Apply", "sp_query_store_flush_db", "SET QUERY_STORE CLEAR"},
		{"Clear Query Store on Apply", "SET QUERY_STORE CLEAR", "sp_query_store_flush_db"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			sc, inst := newFakeConn(t, queryStoreResponses()...)
			form, apply := loadPage(t, pageDatabaseQueryStore(sc, "appdb"), inst)

			editCheck(t, form, tc.label, true)
			if err := apply(context.Background()); err != nil {
				t.Fatalf("apply: %v", err)
			}

			// Exactly one: ticking an action must not rewrite the
			// configuration alongside it.
			assertOneStatement(t, inst, tc.want)
			if strings.Contains(inst.Statements()[0], tc.other) {
				t.Errorf("ticking %q ran the other action:\n%s", tc.label, inst.Statements()[0])
			}
		})
	}
}

// queryStoreResponsesAt answers the page's two reads the way the gated SELECT
// answers them on a given major: wait stats blank before 2017, the capture
// policy zeroed before 2019 (gosmo substitutes a typed NULL for each column
// the instance lacks, and the page reads the zero value back).
func queryStoreResponsesAt(major int) []fakeResponse {
	r := queryStoreResponses()
	if major < 15 {
		r[1].rows[0][12], r[1].rows[0][13] = int64(0), int64(0)
		r[1].rows[0][14], r[1].rows[0][15] = int64(0), int64(0)
	}
	if major < 14 {
		r[1].rows[0][11] = ""
	}
	return r
}

// rowLabels is every labelled row on a form, for asserting a row is absent —
// which no by-label lookup can do.
func rowLabels(f *propsheet.Form) []string {
	var out []string
	for _, r := range f.Rows() {
		if l, ok := r.(interface{ Label() string }); ok {
			out = append(out, l.Label())
		}
	}
	return out
}

// A7: the page offered controls the server can only reject. CUSTOM capture
// mode and the four Custom: rows write QUERY_CAPTURE_POLICY, which is 2019 and
// later — and the CUSTOM keyword itself is too — while Wait stats capture
// writes WAIT_STATS_CAPTURE_MODE, which is 2017 and later.
func TestQueryStorePageHidesControlsTheServerRejects(t *testing.T) {
	customRows := []string{
		"Custom: execution count", "Custom: total compile CPU",
		"Custom: total execution CPU", "Custom: stale threshold",
	}

	t.Run("2016 offers neither CUSTOM nor wait stats", func(t *testing.T) {
		sc, inst := newFakeConnAtVersion(t, "13.0.4001.0", queryStoreResponsesAt(13)...)
		form, _ := loadPage(t, pageDatabaseQueryStore(sc, "appdb"), inst)

		if items := selectRow(t, form, "Query capture mode").Items(); slices.Contains(items, "CUSTOM") {
			t.Errorf("2016 offers CUSTOM: %v", items)
		}
		labels := rowLabels(form)
		for _, l := range append(customRows, "Wait stats capture") {
			if slices.Contains(labels, sheetLabel(l)) {
				t.Errorf("2016 draws %q, which it has no setting for", l)
			}
		}
	})

	t.Run("2017 offers wait stats but not CUSTOM", func(t *testing.T) {
		sc, inst := newFakeConnAtVersion(t, "14.0.2120.1", queryStoreResponsesAt(14)...)
		form, _ := loadPage(t, pageDatabaseQueryStore(sc, "appdb"), inst)

		if items := selectRow(t, form, "Query capture mode").Items(); slices.Contains(items, "CUSTOM") {
			t.Errorf("2017 offers CUSTOM, which it rejects: %v", items)
		}
		labels := rowLabels(form)
		if !slices.Contains(labels, sheetLabel("Wait stats capture")) {
			t.Error("2017 has WAIT_STATS_CAPTURE_MODE; the row must be there")
		}
		for _, l := range customRows {
			if slices.Contains(labels, sheetLabel(l)) {
				t.Errorf("2017 draws %q, which it has no setting for", l)
			}
		}
	})

	t.Run("2019 offers everything", func(t *testing.T) {
		sc, inst := newFakeConnAtVersion(t, "15.0.4123.1", queryStoreResponsesAt(15)...)
		form, _ := loadPage(t, pageDatabaseQueryStore(sc, "appdb"), inst)

		if items := selectRow(t, form, "Query capture mode").Items(); !slices.Contains(items, "CUSTOM") {
			t.Errorf("2019 does not offer CUSTOM: %v", items)
		}
		labels := rowLabels(form)
		for _, l := range append(customRows, "Wait stats capture") {
			if !slices.Contains(labels, sheetLabel(l)) {
				t.Errorf("2019 is missing %q", l)
			}
		}
	})
}

// The other half of A7, and the one a row-presence test cannot reach: with the
// row gone, Apply must not write the value anyway. indexOf resolved the 2016
// read's empty wait-stats value to onOff[0] — "ON", which the server never
// reported — and gosmo's allowlist then rejected the blank before that, so
// every Apply on 2016 failed whatever the user changed.
func TestQueryStoreApplyOn2016WritesNoWaitStatsClause(t *testing.T) {
	sc, inst := newFakeConnAtVersion(t, "13.0.4001.0", queryStoreResponsesAt(13)...)
	form, apply := loadPage(t, pageDatabaseQueryStore(sc, "appdb"), inst)

	editText(t, form, "Max size", "512")

	if err := apply(context.Background()); err != nil {
		t.Fatalf("apply on 2016: %v", err)
	}
	// The write is an ALTER DATABASE naming the database in the statement, so
	// it runs on the connection's own database, not inside appdb.
	stmts := inst.Statements()
	if len(stmts) != 1 {
		t.Fatalf("want one statement, got %d: %q", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "ALTER DATABASE [appdb]") {
		t.Errorf("statement does not name the database:\n%s", stmts[0])
	}
	if strings.Contains(stmts[0], "WAIT_STATS_CAPTURE_MODE") {
		t.Errorf("2016 apply writes WAIT_STATS_CAPTURE_MODE:\n%s", stmts[0])
	}
	if strings.Contains(stmts[0], "QUERY_CAPTURE_POLICY") {
		t.Errorf("2016 apply writes QUERY_CAPTURE_POLICY:\n%s", stmts[0])
	}
	if !strings.Contains(stmts[0], "MAX_STORAGE_SIZE_MB = 512") {
		t.Errorf("the edit that was made did not reach the server:\n%s", stmts[0])
	}
}
