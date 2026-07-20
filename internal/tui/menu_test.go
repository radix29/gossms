package tui

import (
	"testing"

	"github.com/radix29/gossms/internal/tuikit/controls"
)

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

	a.panels.SetActive(a.panels.AddPanel(NewDetailBrowser("Object Explorer Details")))
	if item.Enabled() {
		t.Fatalf("Close Enabled() = true for a non-closable active panel (DetailBrowser), want false")
	}

	qp := NewQueryPanel(a, "Query 1")
	a.panels.SetActive(a.panels.AddPanel(qp))
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
	a.panels.SetActive(a.panels.AddPanel(qp))
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
	a.panels.SetActive(a.panels.AddPanel(qp))
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

	a.panels.SetActive(a.panels.AddPanel(NewQueryPanel(a, "Query 1")))
	if item.Enabled() {
		t.Fatalf("Next Panel Enabled() = true with 1 panel, want false")
	}

	a.panels.SetActive(a.panels.AddPanel(NewQueryPanel(a, "Query 2")))
	if !item.Enabled() {
		t.Fatalf("Next Panel Enabled() = false with 2 panels, want true")
	}
}
