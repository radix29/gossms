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

// Script closes the dialog and runs as a Background Task; on a failed read
// the task fails, the status line says so and no window opens.
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
	if len(a.tasks) != 1 || a.tasks[0].Label != "Generate Scripts: appdb" {
		t.Fatalf("tasks = %v, want one Generate Scripts: appdb", a.tasks)
	}
	task := a.tasks[0]
	drainUntil(t, a, func() bool { return task.Done }, "the task to finish")
	if task.Err == nil || !strings.Contains(task.Err.Error(), "schema read refused") || task.Cancelled {
		t.Errorf("task = done %v, err %v, cancelled %v; want failed on the schema read", task.Done, task.Err, task.Cancelled)
	}
	if !strings.HasPrefix(a.statusText, "Generate Scripts: appdb failed:") {
		t.Errorf("status = %q, want the task's failure", a.statusText)
	}
	if a.panels.Count() != 0 {
		t.Errorf("a failed script opened %d window(s)", a.panels.Count())
	}
}

// The run's reads have no deadline (the 30 s one failed large databases),
// Cancel reaches the read in flight, and a cancelled run is reported as
// cancelled and opens no window.
func TestGenerateScriptsCancelStopsTheRun(t *testing.T) {
	gate := make(chan struct{})
	const schemas = "FROM   sys.schemas"
	sc, inst := newGConn(t, dbByNameResp("appdb", 7),
		fakeResponse{match: schemas, err: errors.New("read ended"), block: gate})
	a := newTestApp()
	d := NewGenerateScriptsDialog(a)
	d.show(sc, "appdb")
	d.script()
	task := a.tasks[0]
	drainUntil(t, a, func() bool { return len(readContexts(inst, schemas)) == 1 }, "the schema read to reach the server")
	ctx := readContexts(inst, schemas)[0]
	if _, ok := ctx.Deadline(); ok {
		t.Error("the scripting read has a deadline")
	}
	task.Cancel()
	if ctx.Err() == nil {
		t.Error("Cancel did not reach the read in flight")
	}
	close(gate)
	drainUntil(t, a, func() bool { return task.Done }, "the task to finish")
	if !task.Cancelled {
		t.Errorf("task err %v, cancelled %v; want cancelled", task.Err, task.Cancelled)
	}
	if a.statusText != "Generate Scripts: appdb cancelled" {
		t.Errorf("status = %q", a.statusText)
	}
	if a.panels.Count() != 0 {
		t.Errorf("a cancelled script opened %d window(s)", a.panels.Count())
	}
}
