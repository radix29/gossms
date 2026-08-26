package tui

import (
	"context"
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// qsDatabaseResponse answers the by-name read every Query Store report opens
// with. Scoped with arg: DatabaseByName's query also contains
// "FROM sys.databases", so without it the list answer serves the by-name read.
func qsDatabaseResponse() fakeResponse {
	return fakeResponse{match: "FROM sys.databases", arg: "appdb", cols: 8, rows: [][]driver.Value{
		{"appdb", int64(5), "ONLINE", "FULL", int64(160), "SQL_Latin1_General_CP1_CI_AS", false, time.Now()},
	}}
}

// qsOptionsResponse answers the sys.database_query_store_options read that
// gates every report, in the given actual/desired state.
func qsOptionsResponse(actual, desired string) fakeResponse {
	return fakeResponse{match: "FROM   sys.database_query_store_options", cols: 16, rows: [][]driver.Value{{
		desired, actual, int64(0), int64(37), int64(100),
		int64(900), int64(60), int64(200),
		"AUTO", "AUTO",
		int64(30), "ON",
		int64(1), int64(2), int64(3), int64(4),
	}}}
}

// qsStatRow is one row of the twelve-column shape every per-query report
// returns — see gosmo's scanQueryStats.
func qsStatRow(queryID int64, object, text string, value float64, execs, forced int64, plans int64) []driver.Value {
	return []driver.Value{
		queryID, text, object, execs, plans, forced,
		time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC),
		value, float64(0), int64(0), float64(0), float64(0),
	}
}

// TestQueryStoreFolderListsTheSevenReports pins the folder's children: the
// seven SSMS views, each a leaf carrying the database it belongs to. A leaf
// with no DBName reaches queryStoreReportDetail with an empty database name
// and reports on whatever the connection happens to be pointed at.
func TestQueryStoreFolderListsTheSevenReports(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	node := &explorerNode{label: "Query Store", data: nodeData{Type: NodeQueryStore, DBName: "appdb", conn: sc}}
	children, err := childLoaders[NodeQueryStore](l, node)
	if err != nil {
		t.Fatalf("loadQueryStoreChildren: %v", err)
	}
	want := []string{
		"Regressed Queries",
		"Overall Resource Consumption",
		"Top Resource Consuming Queries",
		"Queries With Forced Plans",
		"Queries With High Variation",
		"Query Wait Statistics",
		"Tracked Queries",
	}
	if len(children) != len(want) {
		t.Fatalf("got %d reports, want %d", len(children), len(want))
	}
	for i, c := range children {
		if c.label != want[i] {
			t.Errorf("report[%d].label = %q, want %q", i, c.label, want[i])
		}
		// The Detail Browser dispatches on Name, not on the label — they are
		// the same string here, and a leaf whose Name was left empty would
		// render as the right row and report nothing.
		if c.data.Name != want[i] {
			t.Errorf("report[%d].data.Name = %q, want %q", i, c.data.Name, want[i])
		}
		if c.data.Type != NodeQueryStoreReport {
			t.Errorf("report[%d] has type %v, want NodeQueryStoreReport", i, c.data.Type)
		}
		if c.data.DBName != "appdb" {
			t.Errorf("report[%d].data.DBName = %q, want appdb", i, c.data.DBName)
		}
		if c.data.conn != sc {
			t.Errorf("report[%d] did not carry the connection", i)
		}
	}
}

// TestEveryQueryStoreReportTitleDispatches confirms every leaf the folder
// creates resolves to a loader. The titles are derived from the same table
// the loaders live in, so this can only fail if one is added to the tree by
// hand — but a leaf that renders and reports "unknown Query Store report" is
// exactly the dead menu entry the context-gating rule exists to prevent.
func TestEveryQueryStoreReportTitleDispatches(t *testing.T) {
	for _, title := range queryStoreReportTitles {
		r, ok := queryStoreReportByTitle(title)
		if !ok {
			t.Errorf("report %q has no loader", title)
			continue
		}
		if r.load == nil {
			t.Errorf("report %q has a nil loader", title)
		}
		if r.Description == "" {
			t.Errorf("report %q has no description for the folder grid", title)
		}
	}
	if len(queryStoreReportTitles) != 7 {
		t.Errorf("got %d reports, want SSMS's seven", len(queryStoreReportTitles))
	}
}

// TestQueryStoreReportSaysWhenQueryStoreIsOff is the case a grid cannot show
// on its own. Every report against a database with Query Store off returns no
// rows and no error, which reads identically to a database nothing has run
// in — and sends the user looking for a bug instead of a setting.
func TestQueryStoreReportSaysWhenQueryStoreIsOff(t *testing.T) {
	sc, inst := newFakeConn(t, qsDatabaseResponse(), qsOptionsResponse("OFF", "OFF"))
	// newFakeConn's own connect-time reads are already counted, so measure
	// the delta rather than the total.
	before := inst.QueryCount()

	cols, rows, err := queryStoreReportDetail(context.Background(), sc, "appdb", "Top Resource Consuming Queries")
	if err != nil {
		t.Fatalf("queryStoreReportDetail: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("a database with Query Store off produced an empty grid")
	}
	if cols[0] != "Property" {
		t.Errorf("columns = %v, want the Property/Value shape", cols)
	}
	if !strings.Contains(rows[0][1], "OFF") {
		t.Errorf("first row = %v, want it to name the OFF state", rows[0])
	}
	joined := strings.Join(rows[1], " ")
	if !strings.Contains(joined, "Database Properties") {
		t.Errorf("the explanation does not say where to turn Query Store on: %v", rows[1])
	}
	// And it must not have gone on to run the report anyway: the reads are
	// the by-name lookup and the options read, nothing more.
	if n := inst.QueryCount() - before; n > 2 {
		t.Errorf("%d queries ran against a database with Query Store off, want at most the 2 that established it", n)
	}
}

// TestQueryStoreReportNamesTheStateMismatch covers the state that is not
// simply on or off: a Query Store that filled its quota reads READ_ONLY while
// still desiring READ_WRITE. It still has data, so the report must run — and
// the folder must say why collection stopped.
func TestQueryStoreReportNamesTheStateMismatch(t *testing.T) {
	sc, _ := newFakeConn(t, qsDatabaseResponse(), qsOptionsResponse("READ_ONLY", "READ_WRITE"))

	_, rows, err := queryStoreFolderDetail(context.Background(), sc, "appdb")
	if err != nil {
		t.Fatalf("queryStoreFolderDetail: %v", err)
	}
	if len(rows) == 0 || rows[0][0] != "State" {
		t.Fatalf("first row = %v, want the State row", rows)
	}
	if got := rows[0][1]; !strings.Contains(got, "READ_ONLY") || !strings.Contains(got, "READ_WRITE") {
		t.Errorf("State = %q, want it to name both the actual and the requested state", got)
	}
}

// TestQueryStoreFolderGridListsEveryReport pins the folder's own grid: the
// state rows, then one row per report. A folder that listed six would leave a
// leaf in the tree the pane never explains.
func TestQueryStoreFolderGridListsEveryReport(t *testing.T) {
	sc, _ := newFakeConn(t, qsDatabaseResponse(), qsOptionsResponse("READ_WRITE", "READ_WRITE"))

	_, rows, err := queryStoreFolderDetail(context.Background(), sc, "appdb")
	if err != nil {
		t.Fatalf("queryStoreFolderDetail: %v", err)
	}
	listed := map[string]bool{}
	for _, r := range rows {
		listed[r[0]] = true
	}
	for _, title := range queryStoreReportTitles {
		if !listed[title] {
			t.Errorf("the folder grid does not list %q", title)
		}
	}
}

// TestTopResourceQueriesReportRendersItsRows drives one report end to end and
// checks each scripted value landed in its own column. Eight columns of
// mostly numbers is the shape where two swap unnoticed: the grid still
// renders, and a query's execution count reads as its plan count.
func TestTopResourceQueriesReportRendersItsRows(t *testing.T) {
	sc, inst := newFakeConn(t,
		qsDatabaseResponse(),
		qsOptionsResponse("READ_WRITE", "READ_WRITE"),
		fakeResponse{match: "sys.query_store_runtime_stats", cols: 12, rows: [][]driver.Value{
			// 2500 µs, 1234 executions, 3 plans, plan 77 forced.
			qsStatRow(11, "dbo.p", "SELECT\n   1", 2500, 1234, 77, 3),
			// A second query, with no forced plan.
			qsStatRow(12, "", "SELECT 2", 100, 5, 0, 1),
		}})

	cols, rows, err := queryStoreReportDetail(context.Background(), sc, "appdb", "Top Resource Consuming Queries")
	if err != nil {
		t.Fatalf("queryStoreReportDetail: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (unanswered: %v)", len(rows), inst.unmatched)
	}
	want := map[string]string{
		"Query ID":       "11",
		"Object":         "dbo.p",
		"Total Duration": "2.50 ms",
		"Executions":     "1,234",
		"Plans":          "3",
		"Forced Plan":    "77",
		"Query":          "SELECT 1",
	}
	for name, wantVal := range want {
		idx := -1
		for i, c := range cols {
			if c == name {
				idx = i
			}
		}
		if idx < 0 {
			t.Errorf("no %q column in %v", name, cols)
			continue
		}
		if rows[0][idx] != wantVal {
			t.Errorf("%s = %q, want %q", name, rows[0][idx], wantVal)
		}
	}

	// The second row is the one with nothing forced.
	forcedIdx := 0
	for i, c := range cols {
		if c == "Forced Plan" {
			forcedIdx = i
		}
	}
	if rows[1][forcedIdx] != "-" {
		t.Errorf("Forced Plan = %q for a query with no forced plan, want a dash", rows[1][forcedIdx])
	}
}

// TestQueryStoreReportRejectsAnUnknownTitle keeps the dispatch honest: a
// title with no loader must say so rather than returning an empty grid that
// looks like a report with no rows.
func TestQueryStoreReportRejectsAnUnknownTitle(t *testing.T) {
	sc, _ := newFakeConn(t, qsDatabaseResponse(), qsOptionsResponse("READ_WRITE", "READ_WRITE"))
	if _, _, err := queryStoreReportDetail(context.Background(), sc, "appdb", "Not A Report"); err == nil {
		t.Error("an unknown report title returned no error")
	}
}

// TestQueryStoreOneLineFlattensAndTruncates. Query Store keeps a statement
// exactly as submitted — newlines and indentation included — and a raw
// newline in a grid cell breaks the row it is in.
func TestQueryStoreOneLineFlattensAndTruncates(t *testing.T) {
	got := queryStoreOneLine("SELECT\n\tid,\r\n\tname\nFROM   dbo.t")
	if strings.ContainsAny(got, "\n\r\t") {
		t.Errorf("queryStoreOneLine kept whitespace that breaks a grid row: %q", got)
	}
	if got != "SELECT id, name FROM dbo.t" {
		t.Errorf("queryStoreOneLine = %q, want the collapsed statement", got)
	}
	long := strings.Repeat("x", queryStoreQueryTextWidth*2)
	if w := len([]rune(queryStoreOneLine(long))); w > queryStoreQueryTextWidth {
		t.Errorf("a long statement rendered %d columns wide, want at most %d", w, queryStoreQueryTextWidth)
	}
}

// TestPlanIDOrDashHidesZero. A forced plan id of 0 means "no plan is forced";
// printing it as 0 reads as a real plan and makes every query look pinned.
func TestPlanIDOrDashHidesZero(t *testing.T) {
	if got := planIDOrDash(0); got != "-" {
		t.Errorf("planIDOrDash(0) = %q, want a dash", got)
	}
	if got := planIDOrDash(77); got != "77" {
		t.Errorf("planIDOrDash(77) = %q, want 77", got)
	}
}

// TestQueryStoreIsOnAcceptsReadOnly. READ_ONLY is what a Query Store that
// filled its quota degrades to: it stopped collecting, but everything it
// already holds is still worth reporting. Treating it as off would blank
// every report at exactly the moment they matter.
func TestQueryStoreIsOnAcceptsReadOnly(t *testing.T) {
	for _, tt := range []struct {
		state string
		want  bool
	}{
		{"READ_WRITE", true},
		{"READ_ONLY", true},
		{"OFF", false},
		{"ERROR", false},
	} {
		if got := queryStoreIsOn(&gosmo.QueryStoreInfo{ActualState: tt.state}); got != tt.want {
			t.Errorf("queryStoreIsOn(%q) = %v, want %v", tt.state, got, tt.want)
		}
	}
}

// TestRegressedQueriesComparesTheTwoHalvesOfItsWindow.
//
// gosmo's default baseline is the equally long window immediately *before*
// From. Taking it would make the pane need twice queryStoreDetailWindow of
// Query Store history before it could show a row — verified live against a
// database with two minutes of history, where the report was permanently
// empty and read as broken rather than as young. The baseline must therefore
// stay inside the window the other six reports already read.
func TestRegressedQueriesComparesTheTwoHalvesOfItsWindow(t *testing.T) {
	to := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	from := to.Add(-queryStoreDetailWindow)
	mid := from.Add(queryStoreDetailWindow / 2)

	opts := queryStoreRegressionOptions(gosmo.QueryStoreReportOptions{From: from, To: to})

	if !opts.BaselineFrom.Before(opts.BaselineTo) {
		t.Fatalf("baseline [%v, %v) is empty", opts.BaselineFrom, opts.BaselineTo)
	}
	if opts.BaselineFrom.Before(from) {
		t.Errorf("baseline starts at %v, before the window's own start %v — "+
			"the pane would need twice its window of history to show anything",
			opts.BaselineFrom, from)
	}
	if !opts.BaselineFrom.Equal(from) || !opts.BaselineTo.Equal(mid) {
		t.Errorf("baseline = [%v, %v), want the first half [%v, %v)",
			opts.BaselineFrom, opts.BaselineTo, from, mid)
	}
	if !opts.From.Equal(mid) || !opts.To.Equal(to) {
		t.Errorf("reported window = [%v, %v), want the second half [%v, %v)",
			opts.From, opts.To, mid, to)
	}
	// The two halves must not overlap, or a query's own executions would be
	// counted on both sides and every regression would shrink toward zero.
	if opts.BaselineTo.After(opts.From) {
		t.Errorf("the halves overlap: baseline ends %v, reported window starts %v",
			opts.BaselineTo, opts.From)
	}
}
