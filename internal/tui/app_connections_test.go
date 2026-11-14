package tui

import (
	"strings"
	"testing"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/config"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/dialogs"
	"github.com/radix29/tuikit/layout"
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
	// An Execute with no connection opens it (QueryPanel.runRefused).
	a.connectDialog = NewConnectDialog(a)
	// The Connect dialog's Profile... button opens it.
	a.profileDialog = NewProfileDialog(nil)
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

func TestExplorerRootShowsSavedProfile(t *testing.T) {
	saved := config.Connection{Server: "long-server.example.com", Database: "app", User: "sa"}
	for _, tc := range []struct {
		name    string
		opts    config.Connection
		profile bool
	}{
		{"matching connection", saved, true},
		{"different server", config.Connection{Server: "other", Database: "app", User: "sa"}, false},
		{"different database", config.Connection{Server: saved.Server, Database: "other", User: "sa"}, false},
		{"different login", config.Connection{Server: saved.Server, Database: "app", User: "other"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp()
			a.cfg.AddOrUpdate(saved)
			if err := a.cfg.SetProfile(saved.GeneratedName(), "finance-prod", config.AllowRead); err != nil {
				t.Fatal(err)
			}
			sc := &db.ServerConn{Opts: tc.opts}
			fullLabel := sc.Label()
			root := a.explorer.AddRoot(fullLabel, sc)
			want := fullLabel
			if tc.profile {
				want = "finance-prod (" + fullLabel + ")"
			}
			if got := a.explorer.view.SelectedNode().Label; got != want {
				t.Errorf("tree root label = %q, want %q", got, want)
			}
			if root.label != fullLabel || FormatNodePath(root) != fullLabel {
				t.Errorf("underlying label or status changed: %q, %q", root.label, FormatNodePath(root))
			}
			if !tc.profile {
				return
			}
			details := NewDetailBrowser("Details")
			details.cache[root] = &detailResult{cols: []string{"Property", "Value"}}
			details.ShowNodeDetails(a, root)
			wantTitle := "Object Explorer Details \u2014 " + fullLabel
			if got := details.Title(); got != wantTitle {
				t.Errorf("Details title = %q, want unchanged %q", got, wantTitle)
			}
			details.Retitle(root)
			if got := details.Title(); got != wantTitle {
				t.Errorf("retitled Details = %q, want unchanged %q", got, wantTitle)
			}
			child := &explorerNode{label: "Databases", parent: root}
			if got := FormatNodePath(child); got != fullLabel+" > Databases" {
				t.Errorf("child status = %q, want full connection breadcrumb", got)
			}
			if sc.Opts != tc.opts {
				t.Error("displaying a profile changed the active connection options")
			}
			profile, err := a.cfg.ProfileByName("finance-prod")
			if err != nil || profile.MaxAllow != config.AllowRead {
				t.Errorf("displaying a profile changed its saved ceiling: %v, %v", profile.MaxAllow, err)
			}
		})
	}
}

func TestExplorerRootWithoutProfileKeepsOriginalLabel(t *testing.T) {
	for _, tc := range []struct {
		name  string
		conn  *db.ServerConn
		saved bool
	}{
		{name: "unsaved connection", conn: &db.ServerConn{Opts: config.Connection{Server: "srv", User: "sa"}}},
		{name: "saved connection without profile", conn: &db.ServerConn{Opts: config.Connection{Server: "srv", User: "sa"}}, saved: true},
		{name: "no connection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApp()
			if tc.saved {
				a.cfg.AddOrUpdate(tc.conn.Opts)
			}
			root := a.explorer.AddRoot("original root label", tc.conn)
			if root.label != "original root label" || FormatNodePath(root) != "original root label" {
				t.Errorf("an unprofiled root changed: label %q, status %q", root.label, FormatNodePath(root))
			}
			if got := a.explorer.view.SelectedNode().Label; got != "original root label" {
				t.Errorf("unprofiled tree label = %q, want original root label", got)
			}
		})
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

// A pre-2016 SP1 instance gets the warning and a supported or Azure one does
// not: without it, the first folder that names a newer column fails with
// "Invalid column name", which reads as a goSSMS bug.
func TestWarnIfUnsupported(t *testing.T) {
	for _, c := range []struct {
		info *gosmo.ServerInfo
		want bool
	}{
		{&gosmo.ServerInfo{ProductVersion: "12.0.6024.0", VersionMajor: 12, VersionBuild: 6024}, true},
		{&gosmo.ServerInfo{ProductVersion: "13.0.1601.5", VersionMajor: 13, VersionBuild: 1601}, true},
		{&gosmo.ServerInfo{ProductVersion: "13.0.6300.2", VersionMajor: 13, VersionBuild: 6300}, false},
		{&gosmo.ServerInfo{ProductVersion: "12.0.2000.8", VersionMajor: 12, EngineEdition: int(gosmo.EngineAzureManagedInst)}, false},
	} {
		a := newTestApp()
		a.alertDialog = dialogs.NewAlertDialog(nil)
		a.warnIfUnsupported(c.info)
		if got := a.alertDialog.Visible(); got != c.want {
			t.Errorf("%s: warned = %v, want %v", c.info.ProductVersion, got, c.want)
		}
		if c.want && !strings.Contains(a.alertDialog.Message(), c.info.ProductVersion) {
			t.Errorf("%s: warning %q does not name the version", c.info.ProductVersion, a.alertDialog.Message())
		}
	}
}
