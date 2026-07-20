package tui

import (
	"testing"

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
