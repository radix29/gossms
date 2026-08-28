package tui

import (
	"context"
	"database/sql/driver"
	"testing"

	"github.com/gdamore/tcell/v3"

	dbconn "github.com/radix29/gossms/internal/db"
)

// newConnectedNode builds a standalone explorerNode of an unhandled type
// (falls to fetchNodeDetails' default case, which never touches sc.Server)
// wired to a fake, "open" connection — safe to exercise ShowNodeDetails'
// cache/dispatch logic without a real gosmo.Server or network access.
func newConnectedNode(label string) (*explorerNode, *dbconn.ServerConn) {
	sc := &dbconn.ServerConn{}
	return &explorerNode{label: label, data: nodeData{Type: NodeColumn, Name: label, conn: sc}}, sc
}

func TestShowNodeDetailsUsesCache(t *testing.T) {
	a := newTestApp()
	node, _ := newConnectedNode("cached-node")

	db := NewDetailBrowser("test")
	db.cache[node] = &detailResult{
		cols: []string{"Property", "Value"},
		rows: [][]string{{"Name", "cached-node"}, {"Type", "Column"}},
	}

	db.ShowNodeDetails(a, node)

	if got := db.grid.Row(0); got[1] != "cached-node" {
		t.Fatalf("grid row 0 = %v, want cached data to be shown synchronously", got)
	}
	// A cache hit must not disturb the "Loading..." status a real fetch
	// would set — it never runs fetchNodeDetails at all.
	if db.grid.Status() == "Loading..." {
		t.Error("status = Loading..., want the cached result applied instead of a fresh fetch")
	}
}

// RefreshCurrent must refetch the node the panel is displaying, not
// whatever the explorer has selected — pin it by pointing the panel at one
// node while a second, unrelated node stays cached and untouched.
func TestRefreshCurrentRefetchesThePanelsOwnNode(t *testing.T) {
	a := newTestApp()
	shown, _ := newConnectedNode("shown-node")
	other, _ := newConnectedNode("other-node")

	db := NewDetailBrowser("test")
	db.cache[shown] = &detailResult{cols: []string{"Property", "Value"}, rows: [][]string{{"Name", "stale"}}}
	db.cache[other] = &detailResult{cols: []string{"Property", "Value"}, rows: [][]string{{"Name", "other"}}}
	db.ShowNodeDetails(a, shown)

	db.RefreshCurrent(a)

	if _, ok := db.cache[shown]; ok {
		t.Error("cache still has an entry for the displayed node after RefreshCurrent")
	}
	if _, ok := db.cache[other]; !ok {
		t.Error("RefreshCurrent dropped the cache entry for a node it isn't showing")
	}
	if db.grid.Status() != "Loading..." {
		t.Errorf("status = %q, want Loading... after RefreshCurrent", db.grid.Status())
	}
}

// RefreshCurrent with nothing displayed must be a no-op, not a fetch for a
// nil node — the panel is in that state right after PurgeConn empties it.
func TestRefreshCurrentWithNoNodeIsANoOp(t *testing.T) {
	a := newTestApp()
	db := NewDetailBrowser("test")

	db.RefreshCurrent(a)

	if db.grid.Status() == "Loading..." {
		t.Error("RefreshCurrent started a fetch with no node displayed")
	}
}

func TestInvalidateRefetchesCurrentlyDisplayedNode(t *testing.T) {
	a := newTestApp()
	node, _ := newConnectedNode("current-node")

	db := NewDetailBrowser("test")
	db.cache[node] = &detailResult{cols: []string{"Property", "Value"}, rows: [][]string{{"Name", "stale"}}}
	db.ShowNodeDetails(a, node) // cache hit, sets db.currentNode = node

	db.Invalidate(a, node)

	if _, ok := db.cache[node]; ok {
		t.Error("cache still has an entry for node after Invalidate")
	}
	if db.grid.Status() != "Loading..." {
		t.Errorf("status = %q, want Loading... (Invalidate should refetch the currently-displayed node)", db.grid.Status())
	}
}

func TestInvalidateOfNonCurrentNodeOnlyDropsCache(t *testing.T) {
	a := newTestApp()
	nodeA, _ := newConnectedNode("node-a")
	nodeB, _ := newConnectedNode("node-b")

	db := NewDetailBrowser("test")
	db.cache[nodeA] = &detailResult{cols: []string{"Property", "Value"}, rows: [][]string{{"Name", "a"}}}
	db.cache[nodeB] = &detailResult{cols: []string{"Property", "Value"}, rows: [][]string{{"Name", "b"}}}
	db.ShowNodeDetails(a, nodeB) // nodeB is now current, shown from cache

	db.Invalidate(a, nodeA) // not the displayed node

	if _, ok := db.cache[nodeA]; ok {
		t.Error("cache still has an entry for nodeA after Invalidate")
	}
	// nodeB's cache and on-screen data must be untouched — Invalidate only
	// forces a refetch for the node currently on screen.
	if _, ok := db.cache[nodeB]; !ok {
		t.Error("Invalidate(nodeA) incorrectly dropped nodeB's cache entry")
	}
	if got := db.grid.Row(0); got[1] != "b" {
		t.Errorf("grid row 0 = %v, want nodeB's cached data still shown", got)
	}
}

func TestInvalidateNilReceiverIsSafe(t *testing.T) {
	var db *DetailBrowser
	a := newTestApp()
	node, _ := newConnectedNode("n")
	db.Invalidate(a, node) // must not panic
}

// TestFetchNodeDetailsFallsBackToChildList checks the "if not explicitly
// defined, just list the child objects" fallback: a folder node type with
// no purpose-built case in fetchNodeDetails (Server Objects, here) shows its
// children's labels instead of the leaf-style Property/Value grid that made
// no sense for a folder.
func TestFetchNodeDetailsFallsBackToChildList(t *testing.T) {
	sc := &dbconn.ServerConn{}
	node := &explorerNode{label: "Server Objects", data: nodeData{Type: NodeServerObjects, conn: sc}}

	cols, rows, err := fetchNodeDetails(context.Background(), sc, node)
	if err != nil {
		t.Fatalf("fetchNodeDetails: %v", err)
	}
	if len(cols) != 1 || cols[0] != "Name" {
		t.Fatalf("cols = %v, want [Name]", cols)
	}
	var gotLinkedServers bool
	for _, r := range rows {
		if len(r) == 1 && r[0] == "Linked Servers" {
			gotLinkedServers = true
		}
	}
	if !gotLinkedServers {
		t.Errorf("rows = %v, want a \"Linked Servers\" row (Server Objects' only child now)", rows)
	}
}

// TestFetchNodeDetailsLeafKeepsPropertyValue checks a genuine leaf type
// (no children) still gets the original Property/Value grid, not the
// child-list fallback — mirrors newConnectedNode's own "never touches
// sc.Server" comment above, since a leaf must not call into childLoaders
// at all.
func TestFetchNodeDetailsLeafKeepsPropertyValue(t *testing.T) {
	sc := &dbconn.ServerConn{}
	node := &explorerNode{label: "my_col", data: nodeData{Type: NodeColumn, conn: sc}}

	cols, _, err := fetchNodeDetails(context.Background(), sc, node)
	if err != nil {
		t.Fatalf("fetchNodeDetails: %v", err)
	}
	if len(cols) != 2 || cols[0] != "Property" || cols[1] != "Value" {
		t.Fatalf("cols = %v, want [Property Value]", cols)
	}
}

func TestShowNodeDetailsNotConnected(t *testing.T) {
	a := newTestApp()
	sc := &dbconn.ServerConn{}
	sc.Close() // marks it closed, so isConnected reports false
	node := &explorerNode{label: "n", data: nodeData{Type: NodeColumn, conn: sc}}

	db := NewDetailBrowser("test")
	db.ShowNodeDetails(a, node)

	if got := db.grid.Row(0); got[1] != "Not connected" {
		t.Errorf("grid row 0 = %v, want a Not connected status row", got)
	}
}

// TestRefreshButtonFiresOnceOnPress checks the title bar's refresh button
// runs OnRefresh on a fresh Button1 press and not again on the held-button
// motion events tcell resends before the release — the mouseDragging latch.
func TestRefreshButtonFiresOnceOnPress(t *testing.T) {
	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 80, 20)
	fired := 0
	db.OnRefresh = func() { fired++ }

	x, y := db.refreshRect.X, db.refreshRect.Y
	db.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, 0))
	db.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, 0))
	if fired != 1 {
		t.Fatalf("OnRefresh fired %d times on press+hold, want 1", fired)
	}

	db.HandleMouse(tcell.NewEventMouse(x, y, tcell.ButtonNone, 0))
	db.HandleMouse(tcell.NewEventMouse(x, y, tcell.Button1, 0))
	if fired != 2 {
		t.Fatalf("OnRefresh fired %d times after release + fresh press, want 2", fired)
	}
}

// TestRefreshButtonMissDelegatesToGrid checks a press below the title bar
// still reaches the data grid.
func TestRefreshButtonMissDelegatesToGrid(t *testing.T) {
	db := NewDetailBrowser("test")
	db.SetBounds(0, 0, 80, 20)
	db.OnRefresh = func() { t.Error("OnRefresh fired for a press outside the button") }
	db.grid.SetData([]string{"Property", "Value"}, [][]string{{"Name", "a"}, {"Type", "b"}})

	db.HandleMouse(tcell.NewEventMouse(2, db.refreshRect.Y+4, tcell.Button1, 0))
	if db.grid.SelectedRow() != 1 {
		t.Errorf("grid selected row = %d, want 1 (press should reach the grid)", db.grid.SelectedRow())
	}
}

// TestDetailBrowserShowValueReadsTheStatementByQueryID. The Detail Browser's
// grid holds only [][]string, shared with every other node type, so the
// flattened cell is all it has — and opening that cell in a query panel ships
// a batch whose FROM clause is inside a line comment. The row's query id is
// the handle back to the real statement.
func TestDetailBrowserShowValueReadsTheStatementByQueryID(t *testing.T) {
	const stored = "SELECT 1 -- pick one\nFROM dbo.t"

	a := newTestApp()
	sc, inst := newFakeConn(t, fakeResponse{
		match: "WHERE  q.query_id = @p1", cols: 2,
		rows: [][]driver.Value{{stored, "dbo.q"}},
	})
	db := a.newDetailBrowser()

	node := &explorerNode{
		label: "Top Resource Consuming Queries",
		data: nodeData{Type: NodeQueryStoreReport, Name: "Top Resource Consuming Queries",
			DBName: "appdb", conn: sc},
	}
	flat := queryStoreOneLine(stored)
	db.cache[node] = &detailResult{
		cols: []string{qsQueryIDColumn, "Object", qsQueryColumn},
		rows: [][]string{{"7", "dbo.p", "SELECT 9"}, {"12", "dbo.q", flat}},
	}
	db.ShowNodeDetails(a, node)

	// The *second* row: a hook that ignored the cursor and read row 0 would
	// pass with the selection left at the top.
	queryCol := db.grid.ColumnIndex(qsQueryColumn)
	db.grid.SetSelectedCell(1, queryCol)

	before := a.panels.Count()
	if !db.showQueryStoreValue(a, queryCol, qsQueryColumn, flat) {
		t.Fatal("the hook declined the Query column of a Query Store report")
	}
	drainUntil(t, a, func() bool { return a.panels.Count() > before }, "the statement panel to open")

	qp, ok := a.panels.PanelAt(a.panels.Count() - 1).(*QueryPanel)
	if !ok {
		t.Fatalf("the new panel is %T, want a query panel", a.panels.PanelAt(a.panels.Count()-1))
	}
	if got := qp.editor.Text(); got != stored {
		t.Errorf("the panel holds %q, want the stored statement %q", got, stored)
	}
	// It asked about query 12, not query 7 — a hook that read row 0 gets the
	// same answer out of this fake, so the bound id is the only witness.
	args, ok := inst.ReadArgs("WHERE  q.query_id = @p1")
	if !ok || len(args) != 1 {
		t.Fatalf("the text read bound %v, want one parameter", args)
	}
	if got, _ := args[0].Value.(int64); got != 12 {
		t.Errorf("the text read asked about query %v, want 12 — the selected row", args[0].Value)
	}
}

// TestDetailBrowserShowValueLeavesOtherGridsAlone: every other node type's
// grid has no query id to read by, and its cells are the only text there is.
func TestDetailBrowserShowValueLeavesOtherGridsAlone(t *testing.T) {
	a := newTestApp()
	db := a.newDetailBrowser()
	node, _ := newConnectedNode("a-column")
	db.cache[node] = &detailResult{
		cols: []string{qsQueryIDColumn, qsQueryColumn},
		rows: [][]string{{"12", "SELECT 1"}},
	}
	db.ShowNodeDetails(a, node)
	db.grid.SetSelectedCell(0, 1)

	before := a.panels.Count()
	// Claimed by showSQLCellValue's own path — which opens the cell, because
	// on a grid that is not a Query Store report the cell is the whole value.
	db.showQueryStoreValue(a, 1, qsQueryColumn, "SELECT 1")
	if a.panels.Count() != before+1 {
		t.Fatalf("panel count %d, want %d", a.panels.Count(), before+1)
	}
	if qp, ok := a.panels.PanelAt(a.panels.Count() - 1).(*QueryPanel); ok {
		if got := qp.editor.Text(); got != "SELECT 1" {
			t.Errorf("the panel holds %q, want the cell", got)
		}
	}
}
