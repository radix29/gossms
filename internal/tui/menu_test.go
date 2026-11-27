package tui

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gosmo"
	"github.com/radix29/tuikit/controls"
	"github.com/radix29/tuikit/theme"
	"github.com/radix29/tuikit/tuikittest"
)

func TestToolsMenuAndToolbarPreserveLegacyGates(t *testing.T) {
	for _, tt := range []struct {
		name    string
		edition gosmo.EngineEdition
		denied  bool
		enabled [4]bool
	}{
		{"Disconnected", 0, false, [4]bool{false, true, false, false}},
		{"SQL Server", gosmo.EngineEnterprise, false, [4]bool{true, true, true, true}},
		{"Azure SQL", gosmo.EngineAzureSQLDatabase, false, [4]bool{true, true, false, false}},
		{"Managed Instance", gosmo.EngineAzureManagedInst, false, [4]bool{true, true, true, true}},
		{"Fabric SQL database", gosmo.EngineFabricSQLDatabase, false, [4]bool{true, true, false, false}},
		{"Fabric Warehouse", gosmo.EngineAzureSynapseSrvls, false, [4]bool{false, false, false, false}},
		{"SQL Server denied", gosmo.EngineEnterprise, true, [4]bool{false, true, true, false}},
		{"Azure SQL denied", gosmo.EngineAzureSQLDatabase, true, [4]bool{false, true, false, false}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp()
			if tt.edition != 0 {
				var responses []fakeResponse
				if tt.denied {
					responses = capabilityResponses(true, nil, []string{"VIEW SERVER STATE", "ALTER ANY EVENT SESSION"}, nil, nil)
				}
				sc, _ := newGConnOnAzure(t, int(tt.edition), responses...)
				sc.Opts.Server = tt.name
				addTestConnOf(a, sc)
				if tt.denied {
					sc.ProbeCapabilities(sc.Server.Context())
				}
			}
			menus := a.buildMenus()
			for i, label := range []string{"Activity Monitor", "XEvent Profiler", "Replication Monitor"} {
				item := findMenuBarItem(t, menus, "Tools", label)
				if enabled := item.Enabled == nil || item.Enabled(); enabled != tt.enabled[i] {
					t.Errorf("%s enabled = %v, want %v", label, enabled, tt.enabled[i])
				}
				if label == "XEvent Profiler" {
					for _, sub := range item.Sub {
						if sub.Enabled() != tt.enabled[3] {
							t.Errorf("%s enabled = %v, want %v", sub.Label, sub.Enabled(), tt.enabled[3])
						}
					}
				}
			}
			for _, label := range []string{"Server Properties", "Database Properties", "Query List", "Background Tasks", "Options"} {
				findMenuBarItem(t, menus, "Tools", label)
			}
			monitorPresent := false
			for _, button := range a.buildToolbar() {
				if button.Tooltip == "Activity Monitor" {
					monitorPresent = true
					if button.Enabled() != tt.enabled[0] {
						t.Errorf("toolbar Activity Monitor enabled = %v, want %v", button.Enabled(), tt.enabled[0])
					}
				}
			}
			if !monitorPresent {
				t.Error("toolbar Activity Monitor is missing")
			}
		})
	}
}

func TestToolsMenuAndToolbarRefreshForSelectedConnection(t *testing.T) {
	a := newTestApp()
	a.buildUI()
	a.detailBrowser = nil
	sql, _ := newGConn(t)
	sql.Opts.Server = "SQL Server"
	addTestConnOf(a, sql)
	sqlNode := a.explorer.Selected()
	addTestFabricConn(t, a, "Fabric Warehouse")
	fabricNode := a.explorer.Selected()
	for _, node := range []*explorerNode{sqlNode, fabricNode, sqlNode} {
		a.explorer.tree.Select(node)
		a.menuBar.Close()
		s := tuikittest.NewScreen(160, 32)
		a.setScreen(s)
		a.layoutAll()
		a.menuBar.Open()
		for range 4 {
			a.menuBar.HandleKey(tcell.NewEventKey(tcell.KeyRight, "", tcell.ModNone))
		}
		a.menuBar.Draw(s)
		a.toolbar.Draw(s)
		a.menuBar.DrawOverlay(s)
		for _, label := range []string{"Activity Monitor", "XEvent Profiler", "Replication Monitor"} {
			x, y, present := s.Find(label)
			if !present {
				t.Fatalf("%s: %s disappeared:\n%s", node.label, label, s.Snapshot())
			}
			if _, style, _ := s.Get(x, y); (style == theme.StyleDisabled()) != node.data.conn.IsFabricDW() {
				t.Fatalf("%s: wrong disabled style for %s:\n%s", node.label, label, s.Snapshot())
			}
		}
		if _, _, present := s.Find("Background Tasks"); !present {
			t.Fatalf("Background Tasks disappeared:\n%s", s.Snapshot())
		}
		x, y, present := s.Find("📈")
		if !present {
			t.Fatalf("%s: Activity Monitor toolbar button disappeared:\n%s", node.label, s.Snapshot())
		}
		if _, style, _ := s.Get(x, y); (style == theme.StyleDisabled()) != node.data.conn.IsFabricDW() {
			t.Fatalf("%s: wrong Activity Monitor toolbar style:\n%s", node.label, s.Snapshot())
		}
	}
}

// findMenuBarItem locates a buildMenus() item by its owning menu's label
// and its own label, failing the test if either isn't found — the shared
// lookup for every Enabled-wiring test below. Reuses findMenuItem
// (explorer_loaders_test.go) for the inner lookup once the right Menu is
// found, since that one already searches a flat []controls.MenuItem by
// label for the context-menu tests.
func findMenuBarItem(t *testing.T, menus []controls.Menu, menuLabel, itemLabel string) controls.MenuItem {
	t.Helper()
	for _, m := range menus {
		if m.Label != menuLabel {
			continue
		}
		if it := findMenuItem(m.Items, itemLabel); it != nil {
			return *it
		}
	}
	t.Fatalf("menu item %s > %s not found", menuLabel, itemLabel)
	return controls.MenuItem{}
}

// TestServerPropertiesEnabledReflectsConnections pins down that Tools >
// Server Properties (and, by the same len(a.connections) predicate,
// Activity Monitor) goes from disabled to enabled the instant a connection
// is added — with no rebuild of buildMenus() needed in between, since
// Enabled is a live closure over a.connections, not a snapshot.
func TestServerPropertiesEnabledReflectsConnections(t *testing.T) {
	a := newTestApp()
	menus := a.buildMenus()
	item := findMenuBarItem(t, menus, "Tools", "Server Properties")

	if item.Enabled() {
		t.Fatalf("Server Properties Enabled() = true with zero connections, want false")
	}

	addTestConn(a, "server1")
	if !item.Enabled() {
		t.Fatalf("Server Properties Enabled() = false after addTestConn, want true")
	}
}

// TestDisconnectEnabledReflectsSelection pins down File > Disconnect's
// Enabled predicate (a.selectedServerConn() != nil), which also backs
// disconnectActive's own guard after the app_connections.go refactor.
func TestDisconnectEnabledReflectsSelection(t *testing.T) {
	a := newTestApp()
	menus := a.buildMenus()
	item := findMenuBarItem(t, menus, "File", "Disconnect")

	if item.Enabled() {
		t.Fatalf("Disconnect Enabled() = true with nothing selected, want false")
	}

	// AddRoot selects the node it just added (see TestAddRootSelectsNewNode).
	addTestConn(a, "server1")
	if !item.Enabled() {
		t.Fatalf("Disconnect Enabled() = false with a connected server selected, want true")
	}
}

// TestDatabasePropertiesEnabledReflectsDBNameSelection pins down Tools >
// Database Properties' Enabled predicate: it needs a selected node whose
// DBName is set, not merely any selection (a bare server-node selection,
// as covered by TestDisconnectEnabledReflectsSelection, must NOT enable it).
func TestDatabasePropertiesEnabledReflectsDBNameSelection(t *testing.T) {
	a := newTestApp()
	menus := a.buildMenus()
	item := findMenuBarItem(t, menus, "Tools", "Database Properties")

	if item.Enabled() {
		t.Fatalf("Database Properties Enabled() = true with nothing selected, want false")
	}

	addTestConn(a, "server1")
	node := a.explorer.Selected()
	if node == nil {
		t.Fatalf("setup: no node selected after addTestConn")
	}
	if item.Enabled() {
		t.Fatalf("Database Properties Enabled() = true for a server node with no DBName, want false")
	}

	node.data.DBName = "master"
	if !item.Enabled() {
		t.Fatalf("Database Properties Enabled() = false once the selected node has a DBName, want true")
	}
}

// TestCloseEnabledRespectsClosable pins down File > Close: it must stay
// disabled while the active panel implements layout.Closable and returns
// false (Object Explorer Details' real behavior — see NewDetailBrowser),
// and become enabled for an ordinary panel like a QueryPanel.
func TestCloseEnabledRespectsClosable(t *testing.T) {
	a := newTestApp()
	menus := a.buildMenus()
	item := findMenuBarItem(t, menus, "File", "Close")

	if item.Enabled() {
		t.Fatalf("Close Enabled() = true with no active panel, want false")
	}

	a.panels.SetActiveIndex(a.panels.AddPanel(NewDetailBrowser("Object Explorer Details")))
	if item.Enabled() {
		t.Fatalf("Close Enabled() = true for a non-closable active panel (DetailBrowser), want false")
	}

	qp := NewQueryPanel(a, "Query 1")
	a.panels.SetActiveIndex(a.panels.AddPanel(qp))
	if !item.Enabled() {
		t.Fatalf("Close Enabled() = false for an ordinary closable active panel (QueryPanel), want true")
	}
}

// TestSaveEnabledReflectsActiveQueryPanel is representative of every other
// Query-menu item gated on a.activeQueryPanel() != nil alone (Execute,
// Execute at Cursor, Refresh IntelliSense Cache, Estimated Execution Plan,
// Results To Text/Grid/File) — they all share the identical predicate, so
// this one case stands in for the rest rather than repeating it verbatim.
func TestSaveEnabledReflectsActiveQueryPanel(t *testing.T) {
	a := newTestApp()
	menus := a.buildMenus()
	item := findMenuBarItem(t, menus, "File", "Save")

	if item.Enabled() {
		t.Fatalf("Save Enabled() = true with no active query panel, want false")
	}

	qp := NewQueryPanel(a, "Query 1")
	a.panels.SetActiveIndex(a.panels.AddPanel(qp))
	if !item.Enabled() {
		t.Fatalf("Save Enabled() = false with an active query panel, want true")
	}
}

// TestCancelExecutingQueryEnabledTracksExecuting pins down Query > Cancel
// Executing Query: enabled only once its active query panel is actually
// executing, not merely present — and flips back the instant executing
// clears, with no rebuild needed (a live closure, per the design).
func TestCancelExecutingQueryEnabledTracksExecuting(t *testing.T) {
	a := newTestApp()
	menus := a.buildMenus()
	item := findMenuBarItem(t, menus, "Query", "Cancel Executing Query")

	qp := NewQueryPanel(a, "Query 1")
	a.panels.SetActiveIndex(a.panels.AddPanel(qp))
	if item.Enabled() {
		t.Fatalf("Cancel Executing Query Enabled() = true while idle, want false")
	}

	qp.executing = true
	if !item.Enabled() {
		t.Fatalf("Cancel Executing Query Enabled() = false while executing, want true")
	}

	qp.executing = false
	if item.Enabled() {
		t.Fatalf("Cancel Executing Query Enabled() = true after executing cleared, want false")
	}
}

// TestNextPanelEnabledReflectsPanelCount pins down View > Next Panel /
// Prev Panel: meaningless (and disabled) with 0 or 1 panels open, enabled
// once a second one exists.
func TestNextPanelEnabledReflectsPanelCount(t *testing.T) {
	a := newTestApp()
	menus := a.buildMenus()
	item := findMenuBarItem(t, menus, "View", "Next Panel")

	if item.Enabled() {
		t.Fatalf("Next Panel Enabled() = true with 0 panels, want false")
	}

	a.panels.SetActiveIndex(a.panels.AddPanel(NewQueryPanel(a, "Query 1")))
	if item.Enabled() {
		t.Fatalf("Next Panel Enabled() = true with 1 panel, want false")
	}

	a.panels.SetActiveIndex(a.panels.AddPanel(NewQueryPanel(a, "Query 2")))
	if !item.Enabled() {
		t.Fatalf("Next Panel Enabled() = false with 2 panels, want true")
	}
}
