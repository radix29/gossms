package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/dialogs"
	"github.com/radix29/gossms/internal/tuikit/layout"
)

// newTestApp builds an App for connection lifecycle tests, with no screen or
// event loop.
func newTestApp() *App {
	a := &App{cfg: &config.Config{}}
	a.explorer = NewObjectExplorer(a)
	a.panels = layout.NewPanelManager()
	a.confirmDialog = dialogs.NewConfirmDialog(nil)
	a.confirmTypedDialog = dialogs.NewTypedConfirmDialog(nil)
	// handleKey and handleMouse query it, as App.buildUI guarantees it exists.
	a.keyDiagDialog = NewKeyDiagnosticsDialog(a)
	a.promptDialog = dialogs.NewPromptDialog(nil)
	// Every confirmed write uses it (runWithProgress).
	a.progressDialog = dialogs.NewProgressDialog(nil)
	// As App.buildUI does; a nil one crashes instead of failing the assertion.
	a.contextMenu = new(controls.ContextMenu{})
	return a
}

// addTestConn registers a fake connection (nil gosmo.Server; Close is nil-safe)
// as connectServer does: append + AddRoot.
func addTestConn(a *App, server string) *db.ServerConn {
	sc := &db.ServerConn{Opts: config.Connection{Server: server}}
	a.connections = append(a.connections, sc)
	a.explorer.AddRoot(server, sc)
	return sc
}

// AddRoot must select the new node: Object Explorer Details is driven by
// TreeView's OnSelect (see onNodeSelected).
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

// Panels hold a pointer, not an index, so disconnecting one connection can't
// rebind another's panels.
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

// AddRoot selects the new root, so after sc1 then sc2, disconnectActive acts on
// sc2.
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

// resolveConn walks up to the nearest ancestor with a connection (error
// placeholders carry none).
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
