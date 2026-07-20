package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/layout"
)

// newTestApp builds an App with just enough wired up for connection
// lifecycle tests — no screen, no event loop.
func newTestApp() *App {
	a := &App{cfg: &config.Config{}}
	a.explorer = NewObjectExplorer(a)
	a.panels = layout.NewPanelManager()
	return a
}

// addTestConn registers a fake connection (nil gosmo.Server — Close is
// nil-safe) exactly the way connectServer does: append + AddRoot.
func addTestConn(a *App, server string) *db.ServerConn {
	sc := &db.ServerConn{Opts: config.Connection{Server: server}}
	a.connections = append(a.connections, sc)
	a.explorer.AddRoot(server, sc)
	return sc
}

// AddRoot must select the node it just added — not merely add it and leave
// the tree's previous selection (or its zero-value default) in place.
// Object Explorer Details is driven entirely by TreeView's OnSelect
// callback (see onNodeSelected), so a server that's added without
// selecting it never populates its own detail view until the user manually
// clicks away and back — the exact bug this pins down.
func TestAddRootSelectsNewNode(t *testing.T) {
	a := newTestApp()
	sc1 := addTestConn(a, "server-one")
	if got := a.explorer.Selected(); got == nil || got.data.conn != sc1 {
		t.Fatalf("Selected() after first AddRoot = %+v, want the new root (sc1)", got)
	}

	sc2 := addTestConn(a, "server-two")
	if got := a.explorer.Selected(); got == nil || got.data.conn != sc2 {
		t.Fatalf("Selected() after second AddRoot = %+v, want the newest root (sc2), not the first", got)
	}
}

// Disconnecting one connection must not re-bind query panels that point at
// another: with index-based references, removing connection 0 shifted every
// later index so a panel bound to connection 1 silently targeted the wrong
// server. Pointer identity makes that impossible; this test pins it down.
func TestDisconnectKeepsOtherPanelsBound(t *testing.T) {
	a := newTestApp()
	sc1 := addTestConn(a, "server-one")
	sc2 := addTestConn(a, "server-two")

	qp1 := NewQueryPanel(a, "Query 1")
	qp1.conn = sc1
	qp2 := NewQueryPanel(a, "Query 2")
	qp2.conn = sc2
	a.panels.AddPanel(qp1)
	a.panels.AddPanel(qp2)

	a.disconnect(sc1)

	if len(a.connections) != 1 || a.connections[0] != sc2 {
		t.Fatalf("connections after disconnect = %v, want [sc2]", a.connections)
	}
	if got := len(a.explorer.roots); got != 1 {
		t.Fatalf("explorer roots after disconnect = %d, want 1", got)
	}
	if a.explorer.roots[0].data.conn != sc2 {
		t.Errorf("remaining root bound to %+v, want sc2", a.explorer.roots[0].data.conn)
	}
	if !a.isConnected(qp2.conn) {
		t.Errorf("panel bound to sc2 reports disconnected")
	}
	if qp2.conn != sc2 {
		t.Errorf("panel conn = %+v, want sc2", qp2.conn)
	}
	if a.isConnected(qp1.conn) {
		t.Errorf("panel bound to closed sc1 still reports connected")
	}
}

// disconnectActive resolves the connection from the selected tree node.
// AddRoot selects the newly connected server's own root (see its doc
// comment) — matching SSMS, where connecting a server always focuses it in
// Object Explorer — so after connecting sc1 then sc2, sc2's root is what's
// selected and disconnectActive acts on it.
func TestDisconnectActiveUsesSelectedRoot(t *testing.T) {
	a := newTestApp()
	sc1 := addTestConn(a, "server-one")
	sc2 := addTestConn(a, "server-two")

	a.disconnectActive()

	if len(a.connections) != 1 || a.connections[0] != sc1 {
		t.Fatalf("connections after disconnectActive = %v, want [sc1]", a.connections)
	}
	if a.isConnected(sc2) {
		t.Errorf("sc2 still reported connected after disconnectActive")
	}
}

// resolveConn returns the owning connection for any node, walking up to the
// nearest ancestor that carries one (error placeholders carry none).
func TestResolveConn(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	root := a.explorer.roots[0]

	child := &explorerNode{label: "Databases", data: nodeData{Type: NodeDatabases}, parent: root}
	if got := resolveConn(child); got != sc {
		t.Errorf("resolveConn(child without conn) = %+v, want sc", got)
	}
	if got := resolveConn(root); got != sc {
		t.Errorf("resolveConn(root) = %+v, want sc", got)
	}
	if got := resolveConn(nil); got != nil {
		t.Errorf("resolveConn(nil) = %+v, want nil", got)
	}
	if a.isConnected(nil) {
		t.Errorf("isConnected(nil) = true, want false")
	}
}
