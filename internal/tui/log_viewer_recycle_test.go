package tui

import (
	"database/sql/driver"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// recycleTestResponses script the two reads Load makes, so a recycle that
// ends in Refresh has something to come back to. Without them Load fails and
// the assertions below would be reading a panel that never loaded.
func recycleTestResponses() []fakeResponse {
	return []fakeResponse{
		{match: "sp_enumerrorlogs", cols: 3, rows: [][]driver.Value{
			{int64(0), "08/21/2026  09:00", int64(4096)},
			{int64(1), "08/20/2026  09:00", int64(8192)},
		}},
		{match: "xp_readerrorlog", cols: 3, rows: [][]driver.Value{
			{time.Date(2026, 8, 21, 9, 0, 0, 0, time.UTC), "Server", "Recovery is complete."},
		}},
	}
}

// newRecycleTestViewer wires a LogViewer to a scripted fake instance through a
// real App, so the confirm dialog, the background goroutine and the posted
// callback all run for real.
func newRecycleTestViewer(t *testing.T) (*App, *LogViewer, *fakeInstance) {
	t.Helper()
	a := newTestApp()
	sc, inst := newFakeConn(t, recycleTestResponses()...)
	sc.Opts.Server = "FAKE\\SQL"
	a.connections = append(a.connections, sc)

	lv := newTestLogViewer()
	lv.app = a
	lv.conn = sc
	return a, lv, inst
}

// TestLogViewerToolbarConstantsMatchTheirCells pins the logTool* indexes to the
// cells buildTools actually lays out. The two lists are positional and kept in
// step by hand, so adding a const without its entry (or in the wrong place)
// silently re-points popMenu's anchor and F5 at another button — which is what
// adding Recycle in the middle of the list risked.
func TestLogViewerToolbarConstantsMatchTheirCells(t *testing.T) {
	lv := newTestLogViewer()
	// The two selectors carry a live label rather than a fixed one, so they are
	// matched on their prefix; the buttons are matched exactly.
	wantPrefix := map[int]string{
		logToolLogType: "Log: ",
		logToolFile:    "File: ",
	}
	wantExact := map[int]string{
		logToolRefresh: "Refresh",
		logToolSearch:  "Search...",
		logToolRecycle: "Recycle...",
		logToolExport:  "Export...",
	}
	if len(lv.tools) != len(wantPrefix)+len(wantExact) {
		t.Fatalf("buildTools laid out %d cells, the logTool* constants name %d",
			len(lv.tools), len(wantPrefix)+len(wantExact))
	}
	for i, want := range wantPrefix {
		if !strings.HasPrefix(lv.tools[i].label, want) {
			t.Errorf("tools[%d].label = %q, want a %q selector", i, lv.tools[i].label, want)
		}
	}
	for i, want := range wantExact {
		if lv.tools[i].label != want {
			t.Errorf("tools[%d].label = %q, want %q", i, lv.tools[i].label, want)
		}
	}
}

// TestLogViewerRecycleCyclesTheFamilyOnScreen is the whole path: the toolbar
// cell, the confirmation, the write, and the Refresh behind it.
//
// It deliberately runs on the Agent family rather than the SQL Server one the
// panel opens with — the two log types come from one shared statement table,
// and a test that only ever exercised the default would pass with the panel
// ignoring lv.logType entirely.
func TestLogViewerRecycleCyclesTheFamilyOnScreen(t *testing.T) {
	a, lv, inst := newRecycleTestViewer(t)
	lv.logType = gosmo.ErrorLogAgent

	if !lv.runTool(logToolRecycle) {
		t.Fatal("the Recycle cell refused to run on an idle toolbar")
	}
	if !a.confirmDialog.Visible() {
		t.Fatal("Recycle did not ask for confirmation")
	}
	if !lv.busy {
		t.Error("the toolbar was left live while the question was up; F5 could start a read under the cycle")
	}
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone)) // Yes

	drainUntil(t, a, func() bool { return !lv.busy }, "the recycle to finish")

	var cycled []string
	for _, stmt := range inst.Statements() {
		if strings.Contains(stmt, "sp_cycle") {
			cycled = append(cycled, stmt)
		}
	}
	want := "EXEC msdb.dbo.sp_cycle_agent_errorlog"
	if len(cycled) != 1 || cycled[0] != want {
		t.Fatalf("statements matching sp_cycle = %q, want exactly [%q]", cycled, want)
	}
}

// TestLogViewerRecycleDropsTheStaleFileList pins the follow-up. A cycle
// renumbers every archive, so the enumeration cached before it is wrong
// afterwards: leave it in place and the file selector offers archive numbers
// that no longer name the files they used to.
func TestLogViewerRecycleDropsTheStaleFileList(t *testing.T) {
	a, lv, _ := newRecycleTestViewer(t)
	// A cached list that the scripted enumeration cannot reproduce: if it
	// survives the recycle, the panel reused the cache instead of dropping it.
	lv.files[gosmo.ErrorLogSQLServer] = []*gosmo.ErrorLogFile{
		{Number: 0, Date: "stale"},
		{Number: 1, Date: "stale"},
		{Number: 2, Date: "stale"},
	}

	lv.runTool(logToolRecycle)
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))
	drainUntil(t, a, func() bool {
		return !lv.busy && len(lv.files[gosmo.ErrorLogSQLServer]) > 0
	}, "the recycle and the reload behind it to finish")

	files := lv.files[gosmo.ErrorLogSQLServer]
	if len(files) != 2 {
		t.Fatalf("file list has %d entries after the recycle, want the 2 the server reported", len(files))
	}
	for _, f := range files {
		if f.Date == "stale" {
			t.Fatal("the pre-cycle enumeration survived; the selector still offers the old archive numbering")
		}
	}
}

// TestLogViewerRecycleCancelledWritesNothing pins the No answer: no statement
// and, just as importantly, a toolbar that works again.
func TestLogViewerRecycleCancelledWritesNothing(t *testing.T) {
	a, lv, inst := newRecycleTestViewer(t)

	lv.runTool(logToolRecycle)
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEscape, "", tcell.ModNone)) // No

	for _, stmt := range inst.Statements() {
		if strings.Contains(stmt, "sp_cycle") {
			t.Fatalf("declining the question still ran %q", stmt)
		}
	}
	if lv.busy {
		t.Fatal("the busy latch survived a declined recycle; the whole toolbar stays dimmed and inert")
	}
	if !lv.toolsEnabled() {
		t.Error("toolsEnabled() is false after declining")
	}
}

// TestLogViewerRecycleIsInertWhileBusy is the same rule every other toolbar
// cell follows: a cell drawn dimmed must not act on a click.
func TestLogViewerRecycleIsInertWhileBusy(t *testing.T) {
	a, lv, inst := newRecycleTestViewer(t)
	lv.busy = true

	if lv.runTool(logToolRecycle) {
		t.Error("the Recycle cell ran while the toolbar was dimmed")
	}
	if a.confirmDialog.Visible() {
		t.Error("Recycle asked for confirmation while busy")
	}
	if got := inst.Statements(); len(got) != 0 {
		t.Errorf("a refused Recycle still executed %q", got)
	}
}

// TestLogViewerRecyclePanicClearsTheLatch is Load's safegoRepair rule applied
// to the cycle: busy is released in the posted callback, which a panic unwinds
// straight past, and toolsEnabled gates the entire toolbar on it — so without
// the repair step every button sits inert for the panel's lifetime.
//
// A nil gosmo.Server is what panics: CycleLogContext reaches straight through
// to the connection pool it does not have.
func TestLogViewerRecyclePanicClearsTheLatch(t *testing.T) {
	a := newTestApp()
	lv := newTestLogViewer()
	lv.app = a
	lv.conn = &db.ServerConn{}

	lv.runTool(logToolRecycle)
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))

	drainUntil(t, a, func() bool { return !lv.busy }, "the latch to clear after the panic")

	// Clearing the latch is only worth anything if the toolbar works again.
	if !lv.toolsEnabled() {
		t.Fatal("toolsEnabled() is still false after the panic")
	}
	fired := 0
	lv.tools[logToolRefresh].action = func() { fired++ }
	if !lv.runTool(logToolRefresh) || fired != 1 {
		t.Error("the next toolbar click was refused — the latch never really cleared")
	}
}

// TestExplorerLogFoldersOfferRecycle pins the menu item onto both log folders.
// The SQL Server and Agent folders share one case arm, so an item added to
// only one of them would mean the branch was split without noticing.
func TestExplorerLogFoldersOfferRecycle(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "FAKE\\SQL")
	for _, nt := range []NodeType{NodeSQLServerLogs, NodeAgentErrorLogs} {
		node := &explorerNode{data: nodeData{Type: nt, conn: sc}}
		var labels []string
		for _, it := range a.nodeMenuItems(node) {
			labels = append(labels, it.Label)
		}
		if !slicesContains(labels, "Recycle") {
			t.Errorf("%v folder menu = %q, want a Recycle item", nt, labels)
		}
	}
}

// TestExplorerRecycleRefreshesTheOtherFamilyToo is why the open viewer is put
// through Refresh and not Load. Recycling the Agent log from the tree while the
// viewer sits on the SQL Server family reloads that family — and Load stops
// there, leaving the Agent numbering the cycle just invalidated in the cache,
// to be handed to the user the moment they flip the selector.
func TestExplorerRecycleRefreshesTheOtherFamilyToo(t *testing.T) {
	a, lv, _ := newRecycleTestViewer(t)
	lv.logType = gosmo.ErrorLogSQLServer
	a.panels.AddPanel(lv)
	lv.files[gosmo.ErrorLogAgent] = []*gosmo.ErrorLogFile{{Number: 0, Date: "stale"}}

	node := &explorerNode{data: nodeData{Type: NodeAgentErrorLogs, conn: lv.conn}}
	a.recycleLogFrom(lv.conn, gosmo.ErrorLogAgent, node)
	a.confirmDialog.HandleKey(tcell.NewEventKey(tcell.KeyEnter, "", tcell.ModNone))

	drainUntil(t, a, func() bool {
		return !lv.busy && len(lv.files[gosmo.ErrorLogSQLServer]) > 0
	}, "the recycle and the viewer's reload to finish")

	for _, f := range lv.files[gosmo.ErrorLogAgent] {
		if f.Date == "stale" {
			t.Fatal("the cycled family's pre-cycle enumeration survived in the open viewer")
		}
	}
}
