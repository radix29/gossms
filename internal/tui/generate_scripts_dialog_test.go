package tui

import (
	"errors"
	"strings"
	"testing"
)

// The database menu offers Generate Scripts..., ungated: it reads the catalog
// and opens a script, and runs nothing.
func TestDatabaseMenuOffersGenerateScripts(t *testing.T) {
	sc, _ := newGConn(t, dbByNameResp("appdb", 7))
	a := newTestApp()
	node := &explorerNode{label: "appdb", data: nodeData{Type: NodeDatabase, DBName: "appdb", DBState: "ONLINE", conn: sc}}
	item := findMenuItem(a.contextMenuItemsForNode(node), "Generate Scripts...")
	if item == nil {
		t.Fatal("the database menu has no Generate Scripts... item")
	}
	if item.Enabled != nil {
		t.Error("Generate Scripts... is gated; it only reads the catalog")
	}
}

// Script closes the dialog and, on a failed read, reports on the status line
// and opens no window.
func TestGenerateScriptsReportsAFailedRead(t *testing.T) {
	sc, _ := newGConn(t, dbByNameResp("appdb", 7),
		fakeResponse{match: "FROM   sys.schemas", err: errors.New("schema read refused")})
	a := newTestApp()
	d := NewGenerateScriptsDialog(a)
	d.show(sc, "appdb")
	if d.perms == nil || d.output == nil {
		t.Fatal("the page did not build on show")
	}
	if d.perms.Checked() || d.output.Selected() != genScriptsToQuery {
		t.Error("defaults are not no-permissions, new query window")
	}
	d.script()
	if d.Visible() {
		t.Error("Script left the dialog open")
	}
	drainUntil(t, a, func() bool { return strings.HasPrefix(a.statusText, "Script error:") }, "the error status")
	if a.panels.Count() != 0 {
		t.Errorf("a failed script opened %d window(s)", a.panels.Count())
	}
}
