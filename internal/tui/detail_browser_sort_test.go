package tui

import (
	"slices"
	"testing"

	"github.com/gdamore/tcell/v3"
	dbconn "github.com/radix29/gossms/internal/db"
)

// sortTestPane shows a Tables-folder-shaped list on a fresh pane: rows in the
// loader's order, each with its row object.
func sortTestPane(t *testing.T) (*DetailBrowser, [][]string) {
	t.Helper()
	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	db.currentNode = &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, conn: &dbconn.ServerConn{}}}
	rows := [][]string{
		{"dbo.beta", "1,200", "9 MB"},
		{"dbo.Alpha", "35", "120 MB"},
		{"dbo.gamma", "…", "N/A"},
		{"dbo.delta", "1,000,000", "2 MB"},
	}
	objs := make([]nodeData, len(rows))
	for i, r := range rows {
		objs[i] = nodeData{Type: NodeTable, Schema: "dbo", Name: r[0][4:]}
	}
	db.applyResult(&detailResult{cols: []string{"Name", "Row Count", "Data (MB)"}, rows: rows, objs: objs})
	return db, rows
}

func gridColumn(db *DetailBrowser, col int) []string {
	var out []string
	for i := range len(db.base.rows) {
		out = append(out, db.grid.Row(i)[col])
	}
	return out
}

// clickHeader presses and releases on column col's header cell.
func clickHeader(db *DetailBrowser, col int) {
	x := db.grid.Bounds().X + 1
	for i := range col {
		x += db.grid.ColumnWidth(i)
	}
	y := db.grid.Bounds().Y
	db.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, tcell.ModNone))
	db.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, tcell.ModNone))
}

// A header click sorts by the column's value (numbers numerically, despite the
// separators and units), a second click reverses it, and a cell with no value
// yet stays last both ways. The row objects move with their rows, since Delete
// reads them by grid row.
func TestDetailBrowserHeaderClickSortsRowsAndObjects(t *testing.T) {
	db, _ := sortTestPane(t)

	clickHeader(db, 1)
	if got, want := gridColumn(db, 0), []string{"dbo.Alpha", "dbo.beta", "dbo.delta", "dbo.gamma"}; !slices.Equal(got, want) {
		t.Fatalf("Row Count ascending = %v, want %v", got, want)
	}
	if col, desc := db.grid.SortIndicator(); col != 1 || desc {
		t.Errorf("indicator = (%d, %v), want (1, false)", col, desc)
	}
	clickHeader(db, 1)
	if got, want := gridColumn(db, 0), []string{"dbo.delta", "dbo.beta", "dbo.Alpha", "dbo.gamma"}; !slices.Equal(got, want) {
		t.Fatalf("Row Count descending = %v, want %v", got, want)
	}
	for i, o := range db.rowObjs {
		if "dbo."+o.Name != db.grid.Row(i)[0] {
			t.Errorf("row %d shows %q but its object is %q", i, db.grid.Row(i)[0], o.Name)
		}
	}

	clickHeader(db, 2)
	if got, want := gridColumn(db, 0), []string{"dbo.delta", "dbo.beta", "dbo.Alpha", "dbo.gamma"}; !slices.Equal(got, want) {
		t.Errorf("Data (MB) ascending = %v, want %v", got, want)
	}
	clickHeader(db, 0)
	if got, want := gridColumn(db, 0), []string{"dbo.Alpha", "dbo.beta", "dbo.delta", "dbo.gamma"}; !slices.Equal(got, want) {
		t.Errorf("Name ascending = %v, want %v (case-insensitive)", got, want)
	}
}

// The cursor stays on the object it was on, not on its old index.
func TestDetailBrowserSortKeepsTheCursorOnItsRow(t *testing.T) {
	db, _ := sortTestPane(t)
	db.grid.SetSelectedCell(3, 0) // dbo.delta
	clickHeader(db, 0)
	r, _ := db.grid.SelectedCell()
	if got := db.grid.Row(r)[0]; got != "dbo.delta" {
		t.Errorf("cursor on %q after sorting, want dbo.delta", got)
	}
}

// Cells a progressive loader fills in place after the sort reach the sorted
// view, and its last stage re-sorts them out of the placeholders' order.
func TestDetailBrowserSortReappliesWhenTheBackfillLands(t *testing.T) {
	db, rows := sortTestPane(t)
	clickHeader(db, 1)
	rows[2][1] = "5" // dbo.gamma's count lands
	if r := slices.IndexFunc(db.base.rows, func(r []string) bool { return r[0] == "dbo.gamma" }); db.base.rows[r][1] != "5" {
		t.Fatal("an in-place cell write did not reach the pane's rows")
	}
	db.resort()
	if got, want := gridColumn(db, 0), []string{"dbo.gamma", "dbo.Alpha", "dbo.beta", "dbo.delta"}; !slices.Equal(got, want) {
		t.Errorf("after the backfill = %v, want %v", got, want)
	}
}

// A Property/Value view is a record whose field order is the loader's; showing
// a node never sorted shows its loader's order, while a Refresh of the sorted
// one keeps it.
func TestDetailBrowserSortScope(t *testing.T) {
	db, _ := sortTestPane(t)
	clickHeader(db, 1)

	db.applyResult(&detailResult{cols: []string{"Name", "Row Count", "Data (MB)"},
		rows: [][]string{{"dbo.b", "2", ""}, {"dbo.a", "1", ""}}})
	if got := gridColumn(db, 0); !slices.Equal(got, []string{"dbo.a", "dbo.b"}) {
		t.Errorf("a refreshed result of the same node = %v, want it still sorted", got)
	}

	db.applyResult(&detailResult{cols: []string{"Property", "Value"},
		rows: [][]string{{"Name", "x"}, {"Collation", "y"}}})
	clickHeader(db, 0)
	if got := gridColumn(db, 0); !slices.Equal(got, []string{"Name", "Collation"}) {
		t.Errorf("Property/Value rows = %v after a header click, want the loader's order", got)
	}
	if col, _ := db.grid.SortIndicator(); col != -1 {
		t.Errorf("a Property/Value view shows a sort indicator on column %d", col)
	}

	dead := &dbconn.ServerConn{}
	dead.Close() // not connected, so showing the node starts no fetch
	db.ShowNodeDetails(newTestApp(), &explorerNode{label: "Views", data: nodeData{Type: NodeViews, conn: dead}})
	if db.sort != (detailSort{}) {
		t.Errorf("sort = %+v after showing another node, want none", db.sort)
	}
}

// Each node keeps its own sort: reselecting a sorted node served from the cache
// shows it sorted again, a node never sorted keeps its loader's order, the node
// a folder reload puts in its place inherits it, and the entries go with their
// connection.
func TestDetailBrowserReselectKeepsTheNodesSort(t *testing.T) {
	a := newTestApp()
	sc := &dbconn.ServerConn{}
	cols := []string{"Name", "Row Count"}
	tables := &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, conn: sc}}
	views := &explorerNode{label: "Views", data: nodeData{Type: NodeViews, conn: sc}}

	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 100, 30)
	db.cache[tables] = &detailResult{cols: cols, rows: [][]string{{"dbo.a", "1"}, {"dbo.b", "2"}}}
	db.cache[views] = &detailResult{cols: cols, rows: [][]string{{"dbo.v2", "2"}, {"dbo.v1", "1"}}}

	db.ShowNodeDetails(a, tables)
	clickHeader(db, 1)
	clickHeader(db, 1) // Row Count descending
	if got := gridColumn(db, 0); !slices.Equal(got, []string{"dbo.b", "dbo.a"}) {
		t.Fatalf("Tables sorted = %v, want [dbo.b dbo.a]", got)
	}

	db.ShowNodeDetails(a, views)
	if got := gridColumn(db, 0); !slices.Equal(got, []string{"dbo.v2", "dbo.v1"}) {
		t.Errorf("Views = %v, want its loader's order — Tables' sort leaked onto it", got)
	}
	if col, _ := db.grid.SortIndicator(); col != -1 {
		t.Errorf("Views shows a sort indicator on column %d", col)
	}

	db.ShowNodeDetails(a, tables)
	if got := gridColumn(db, 0); !slices.Equal(got, []string{"dbo.b", "dbo.a"}) {
		t.Errorf("Tables reselected = %v, want it still sorted [dbo.b dbo.a]", got)
	}
	if col, desc := db.grid.SortIndicator(); col != 1 || !desc {
		t.Errorf("Tables reselected: indicator (%d, %v), want (1, true)", col, desc)
	}

	// A folder reload retires the node and puts a new one in its place: the
	// replacement stands for the same folder, so it is shown sorted.
	db.Forget([]*explorerNode{tables})
	reloaded := &explorerNode{label: "Tables", data: tables.data}
	db.cache[reloaded] = &detailResult{cols: cols, rows: [][]string{{"dbo.a", "1"}, {"dbo.c", "3"}, {"dbo.b", "2"}}}
	db.ShowNodeDetails(a, reloaded)
	if got := gridColumn(db, 0); !slices.Equal(got, []string{"dbo.c", "dbo.b", "dbo.a"}) {
		t.Errorf("Tables after a folder reload = %v, want it still sorted [dbo.c dbo.b dbo.a]", got)
	}
	// The same folder on another connection is another folder.
	other := &explorerNode{label: "Tables", data: nodeData{Type: NodeTables, conn: &dbconn.ServerConn{}}}
	db.cache[other] = &detailResult{cols: cols, rows: [][]string{{"dbo.a", "1"}, {"dbo.b", "2"}}}
	db.ShowNodeDetails(a, other)
	if col, _ := db.grid.SortIndicator(); col != -1 {
		t.Errorf("Tables on another connection shows a sort indicator on column %d", col)
	}

	db.PurgeConn(sc)
	if len(db.sorts) != 0 {
		t.Errorf("PurgeConn left %d sorts of the closed connection's nodes", len(db.sorts))
	}
}

func TestParseDetailNumber(t *testing.T) {
	for in, want := range map[string]bool{
		"1,234": true, "12 MB": true, "-3": true, "45%": true, "0.5": true,
		"dbo.t": false, "Inf": false, "NaN": false, "16.0.4135.4": false,
		"2026-01-02 10:00:00": false, "0x1p3": false, "ONLINE": false,
	} {
		if _, ok := parseDetailNumber(in); ok != want {
			t.Errorf("parseDetailNumber(%q) ok = %v, want %v", in, ok, want)
		}
	}
}
