package tui

import (
	"context"
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
	node := &explorerNode{label: "Server Objects", data: nodeData{Type: NodeManagement, conn: sc}}

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
