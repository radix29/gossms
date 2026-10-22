package tui

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/vt"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/query"
)

// sortTestQueryPanel shows two result sets: the first typed, with a NULL in each
// column, the second a one-column set. click presses and releases on a column's
// header cell through the panel's own mouse routing, drawing first as the app
// does between two clicks: the grid sizes the columns that locate the header
// cells in Draw.
func sortTestQueryPanel(t *testing.T) (qp *QueryPanel, click func(col int)) {
	t.Helper()
	a := newTestApp()
	a.cfg.MaxCellLength = config.DefaultMaxCellLength
	qp = NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 100, 30)
	scr, err := tcell.NewTerminfoScreenFromTty(vt.NewMockTerm(vt.MockOptSize{X: 100, Y: 30}))
	if err != nil {
		t.Fatal(err)
	}
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(scr.Fini)
	set := query.ResultSet{
		Columns:     []string{"name", "n", "amount"},
		ColumnTypes: []string{"nvarchar(50)", "bigint", "decimal(18,2)"},
		Rows: [][]string{
			{"beta", "9", "10.50"},
			{"NULL", "100", "2.00"},
			{"Alpha", "NULL", "-3.25"},
			{"gamma", "9007199254740993", "NULL"},
			{"delta", "9007199254740992", "0.10"},
		},
	}
	set.MarkNull(1, 0)
	set.MarkNull(2, 1)
	set.MarkNull(3, 2)
	other := query.ResultSet{Columns: []string{"x"}, ColumnTypes: []string{"int"},
		Rows: [][]string{{"2"}, {"1"}}}
	qp.setResult(&query.Result{Sets: []query.ResultSet{set, other}}, false)
	return qp, func(col int) {
		qp.Draw(scr)
		b := qp.results.Bounds()
		// Past the row-number gutter: the widest row number plus a pad each side.
		x := b.X + 1 + len(strconv.Itoa(len(qp.result.Sets[qp.activeTab].Rows))) + 2
		for i := range col {
			x += qp.results.ColumnWidth(i)
		}
		qp.HandleMouse(tcell.NewEventMouse(x, b.Y, tcell.Button1, tcell.ModNone))
		qp.HandleMouse(tcell.NewEventMouse(x, b.Y, tcell.ButtonNone, tcell.ModNone))
	}
}

func resultsColumn(qp *QueryPanel, col int) []string {
	var out []string
	for i := range len(qp.result.Sets[qp.activeTab].Rows) {
		out = append(out, qp.results.Row(i)[col])
	}
	return out
}

// A header click sorts by the column's declared type: integers exactly (two
// bigints a float64 cannot tell apart), decimals by value, text
// case-insensitively, NULL last both ways as in the Details pane. A second click
// reverses it, and the NULL marks move with their rows.
func TestResultsHeaderClickSortsByDeclaredType(t *testing.T) {
	qp, click := sortTestQueryPanel(t)

	click(1)
	if got, want := resultsColumn(qp, 1), []string{"9", "100", "9007199254740992", "9007199254740993", "NULL"}; !slices.Equal(got, want) {
		t.Fatalf("bigint ascending = %v, want %v", got, want)
	}
	if !qp.results.IsNull(4, 1) || qp.results.IsNull(0, 1) {
		t.Error("the NULL mark did not move with its row")
	}
	if col, desc := qp.results.SortIndicator(); col != 1 || desc {
		t.Errorf("indicator = (%d, %v), want (1, false)", col, desc)
	}
	click(1)
	if got, want := resultsColumn(qp, 1), []string{"9007199254740993", "9007199254740992", "100", "9", "NULL"}; !slices.Equal(got, want) {
		t.Fatalf("bigint descending = %v, want %v", got, want)
	}

	click(2)
	if got, want := resultsColumn(qp, 2), []string{"-3.25", "0.10", "2.00", "10.50", "NULL"}; !slices.Equal(got, want) {
		t.Errorf("decimal ascending = %v, want %v", got, want)
	}
	click(0)
	if got, want := resultsColumn(qp, 0), []string{"Alpha", "beta", "delta", "gamma", "NULL"}; !slices.Equal(got, want) {
		t.Errorf("nvarchar ascending = %v, want %v", got, want)
	}
	if got := qp.result.Sets[0].Rows[0][0]; got != "beta" {
		t.Errorf("the result set itself was reordered: row 0 is %q", got)
	}
}

// The cursor stays on the row it was on, not on its old index.
func TestResultsSortKeepsTheCursorOnItsRow(t *testing.T) {
	qp, click := sortTestQueryPanel(t)
	qp.results.SetSelectedCell(4, 2) // delta
	click(0)
	r, c := qp.results.SelectedCell()
	if got := qp.results.Row(r)[0]; got != "delta" || c != 2 {
		t.Errorf("cursor on %q col %d after sorting, want delta col 2", got, c)
	}
}

// Each tab keeps its own sort across a tab switch, and a new run drops them all.
func TestResultsSortIsPerTabAndPerRun(t *testing.T) {
	qp, click := sortTestQueryPanel(t)
	click(0)

	qp.setActiveTab(1)
	if got := resultsColumn(qp, 0); !slices.Equal(got, []string{"2", "1"}) {
		t.Errorf("second set = %v, want the server's order", got)
	}
	if col, _ := qp.results.SortIndicator(); col != -1 {
		t.Errorf("second set shows a sort indicator on column %d", col)
	}
	qp.setActiveTab(0)
	if got := resultsColumn(qp, 0); got[0] != "Alpha" {
		t.Errorf("first set back = %v, want it still sorted", got)
	}
	if col, _ := qp.results.SortIndicator(); col != 0 {
		t.Errorf("first set back: indicator on column %d, want 0", col)
	}

	qp.setResult(&query.Result{Sets: []query.ResultSet{{Columns: []string{"name"},
		Rows: [][]string{{"b"}, {"a"}}}}}, false)
	if got := resultsColumn(qp, 0); !slices.Equal(got, []string{"b", "a"}) {
		t.Errorf("a new run = %v, want the server's order", got)
	}
}

// The cell menu's Sort items sort the cursor's column, the current order's
// withheld.
func TestResultsSortMenuItems(t *testing.T) {
	qp, _ := sortTestQueryPanel(t)
	qp.results.SetSelectedCell(0, 2)
	items := qp.resultSortMenuItems()
	if len(items) != 2 {
		t.Fatalf("menu items = %d, want 2", len(items))
	}
	items[1].Action() // Sort Descending
	if got := resultsColumn(qp, 2); got[0] != "10.50" || got[4] != "NULL" {
		t.Errorf("amount descending = %v", got)
	}
	if items[1].Enabled() || !items[0].Enabled() {
		t.Error("Sort Descending still enabled on the column sorted descending")
	}
}

// A set too large to sort on the UI goroutine says so on the status line and is
// shown sorted once the run lands. A second click while it runs reverses the
// order being produced, superseding the first run.
func TestResultsSortsALargeSetOffTheUIGoroutine(t *testing.T) {
	a := newTestApp()
	qp := NewQueryPanel(a, "Query 1")
	qp.SetBounds(0, 0, 80, 24)
	set := query.ResultSet{Columns: []string{"n"}, ColumnTypes: []string{"int"}}
	for r := range sortSyncRows + 1 {
		set.Rows = append(set.Rows, []string{strconv.Itoa((r * 7919) % (sortSyncRows + 1))})
	}
	qp.setResult(&query.Result{Sets: []query.ResultSet{set}}, false)

	qp.sortResultsByColumn(0)
	if !qp.sorting() || !strings.Contains(qp.resultsStatusText(), "Sorting...") {
		t.Fatalf("status while sorting = %q, want it to say so", qp.resultsStatusText())
	}
	qp.sortResultsByColumn(0) // reverses the order in flight
	drainUntil(t, a, func() bool { return !qp.sorting() }, "the sort")
	a.drainPending()

	if got := qp.results.Row(0)[0]; got != strconv.Itoa(sortSyncRows) {
		t.Errorf("first row = %s, want the largest value (descending)", got)
	}
	if col, desc := qp.results.SortIndicator(); col != 0 || !desc {
		t.Errorf("indicator = (%d, %v), want (0, true)", col, desc)
	}
	if strings.Contains(qp.resultsStatusText(), "Sorting") {
		t.Errorf("status after the sort landed = %q, still sorting", qp.resultsStatusText())
	}

	// A new run drops a sort still in flight for the old result.
	qp.sortResultsByColumn(0)
	qp.clearResults()
	small := &query.Result{Sets: []query.ResultSet{{Columns: []string{"n"}, Rows: [][]string{{"2"}, {"1"}}}}}
	qp.setResult(small, false)
	a.drainPending()
	if got := qp.results.Row(0)[0]; got != "2" {
		t.Errorf("the new run shows %q first, want the server's order", got)
	}
}
