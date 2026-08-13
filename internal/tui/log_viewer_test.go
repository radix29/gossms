package tui

import (
	"context"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/tuikit/widgets"
)

// newTestLogViewer builds a LogViewer with no App and no connection — enough
// for the filtering, labelling and keyboard behaviour, none of which reads
// either.
func newTestLogViewer() *LogViewer {
	lv := &LogViewer{
		logType:  gosmo.ErrorLogSQLServer,
		files:    make(map[gosmo.ErrorLogType][]*gosmo.ErrorLogFile),
		grid:     controls.NewDataGrid(),
		filter:   widgets.NewInputField(logFilterLabel, logFilterWidth, false),
		splitter: layout.NewHorizontalSplitter(""),
	}
	lv.buildTools()
	return lv
}

func testLogEntries() []*gosmo.ErrorLogEntry {
	at := func(h, m int) time.Time { return time.Date(2026, 8, 12, h, m, 0, 0, time.UTC) }
	return []*gosmo.ErrorLogEntry{
		{Date: at(10, 0), Process: "Server", Text: "Starting up database 'master'."},
		{Date: at(10, 1), Process: "spid19s", Text: "Recovery is complete."},
		{Date: at(10, 2), Process: "Server", Text: "Login failed for user 'sa'."},
	}
}

// TestLogViewerFilterMatchesSourceAndMessage pins the filter down to a
// case-insensitive substring over both the source and the message columns.
func TestLogViewerFilterMatchesSourceAndMessage(t *testing.T) {
	lv := newTestLogViewer()
	lv.entries = testLogEntries()

	lv.applyFilter()
	if len(lv.shown) != 3 {
		t.Fatalf("empty filter shows %d entries, want all 3", len(lv.shown))
	}

	lv.filter.SetValue("RECOVERY")
	lv.applyFilter()
	if len(lv.shown) != 1 || lv.shown[0].Text != "Recovery is complete." {
		t.Errorf("filter %q matched %d entries (%v), want the one Recovery row", "RECOVERY", len(lv.shown), lv.shown)
	}

	// "spid19s" appears only in a Source cell, never in a message.
	lv.filter.SetValue("spid19s")
	lv.applyFilter()
	if len(lv.shown) != 1 {
		t.Errorf("filter %q matched %d entries, want 1 (matched on Source)", "spid19s", len(lv.shown))
	}

	lv.filter.SetValue("no such text")
	lv.applyFilter()
	if len(lv.shown) != 0 {
		t.Errorf("filter %q matched %d entries, want 0", "no such text", len(lv.shown))
	}
}

// TestSortLogEntriesDesc pins the grid's order: newest first, and stable
// within one timestamp so a multi-entry second (a startup sequence, a stack
// dump) still reads in the order the log wrote it.
func TestSortLogEntriesDesc(t *testing.T) {
	at := func(m int) time.Time { return time.Date(2026, 8, 12, 10, m, 0, 0, time.UTC) }
	entries := []*gosmo.ErrorLogEntry{
		{Date: at(0), Text: "oldest"},
		{Date: at(5), Text: "tie A"},
		{Date: at(5), Text: "tie B"},
		{Date: at(9), Text: "newest"},
	}
	got := sortLogEntriesDesc(entries)
	want := []string{"newest", "tie A", "tie B", "oldest"}
	for i, w := range want {
		if got[i].Text != w {
			t.Errorf("entry %d = %q, want %q (order: %v)", i, got[i].Text, w, want)
		}
	}
}

// TestLogViewerSelectedEntryTracksFilter guards the indexing: the grid is
// built from shown, so selectedEntry must index that and not entries — with a
// filter applied the two no longer line up.
func TestLogViewerSelectedEntryTracksFilter(t *testing.T) {
	lv := newTestLogViewer()
	lv.entries = testLogEntries()
	lv.filter.SetValue("server")
	lv.applyFilter()
	if len(lv.shown) != 2 {
		t.Fatalf("filter matched %d entries, want 2", len(lv.shown))
	}
	lv.grid.SetSelectedRow(1)
	got := lv.selectedEntry()
	if got == nil || got.Text != "Login failed for user 'sa'." {
		t.Errorf("selectedEntry() = %v, want the second filtered row", got)
	}
}

// TestLogViewerTabLeavesPanel is the keyboard-trap regression: Tab walks the
// grid to the filter and then declines the key, which is the only way
// App.handleKey moves focus back to Object Explorer. A panel that answers
// true to every Tab can only be left with the mouse.
func TestLogViewerTabLeavesPanel(t *testing.T) {
	lv := newTestLogViewer()
	lv.SetBounds(0, 0, 200, 30) // wide enough for the filter field to fit
	if !lv.filterVisible() {
		t.Fatal("filter field didn't fit at width 200; the rest of this test is meaningless")
	}
	tab := tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone)

	if !lv.HandleKey(tab) {
		t.Fatal("first Tab returned false, want true (grid → filter)")
	}
	if !lv.filterFocused {
		t.Fatal("first Tab didn't move focus to the filter field")
	}
	if lv.HandleKey(tab) {
		t.Error("second Tab returned true — the panel traps focus instead of handing Tab back to App")
	}
	if lv.filterFocused {
		t.Error("leaving the panel left focus on the filter; it should reset to the grid")
	}
}

// TestLogViewerTabLeavesPanelWhenFilterHidden covers the narrow-panel case:
// with no room for the filter field there is nothing to move focus to, so the
// very first Tab must be declined.
func TestLogViewerTabLeavesPanelWhenFilterHidden(t *testing.T) {
	lv := newTestLogViewer()
	lv.SetBounds(0, 0, 40, 30)
	if lv.filterVisible() {
		t.Fatal("filter field fit at width 40; the rest of this test is meaningless")
	}
	if lv.HandleKey(tcell.NewEventKey(tcell.KeyTab, "", tcell.ModNone)) {
		t.Error("Tab returned true with no filter field to focus — the panel traps focus")
	}
}

// TestFlattenLogText pins the grid cell down to one line: an entry's text can
// span several, and a grid row is one row tall.
func TestFlattenLogText(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"Microsoft SQL Server 2025\n\tJun 18 2026\r\nCopyright", "Microsoft SQL Server 2025 Jun 18 2026 Copyright"},
		{"trailing\n", "trailing"},
	}
	for _, c := range cases {
		if got := flattenLogText(c.in); got != c.want {
			t.Errorf("flattenLogText(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestSplitLogLines pins the details pane's opposite requirement: the lines
// the log wrote are preserved, in every newline convention.
func TestSplitLogLines(t *testing.T) {
	got := splitLogLines("one\r\ntwo\rthree\nfour")
	want := []string{"one", "two", "three", "four"}
	if len(got) != len(want) {
		t.Fatalf("splitLogLines returned %d lines (%q), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestErrorLogFileLabel covers the tree/selector label, including the
// fallback when the server's date format wasn't one gosmo could parse.
func TestErrorLogFileLabel(t *testing.T) {
	written := time.Date(2026, 8, 12, 21, 49, 0, 0, time.UTC)
	cases := []struct {
		f    *gosmo.ErrorLogFile
		want string
	}{
		{&gosmo.ErrorLogFile{Number: 0, LastWritten: written}, "Current — 2026-08-12 21:49"},
		{&gosmo.ErrorLogFile{Number: 3, LastWritten: written}, "Archive #3 — 2026-08-12 21:49"},
		{&gosmo.ErrorLogFile{Number: 1, Date: "12.08.2026 21:49"}, "Archive #1 — 12.08.2026 21:49"},
		{&gosmo.ErrorLogFile{Number: 2}, "Archive #2"},
	}
	for _, c := range cases {
		if got := errorLogFileLabel(c.f); got != c.want {
			t.Errorf("errorLogFileLabel(%+v) = %q, want %q", c.f, got, c.want)
		}
	}
}

// TestLogFoldersAreInTheTree pins both entry points in Object Explorer: the
// Management folder under the server root carrying SQL Server Logs, and Error
// Logs under SQL Server Agent.
func TestLogFoldersAreInTheTree(t *testing.T) {
	a := newTestApp()
	sc := addTestConn(a, "server-one")
	l := loaderCtx{ctx: context.Background(), sc: sc}

	childLabels := func(nt NodeType) map[string]NodeType {
		children, err := childLoaders[nt](l, &explorerNode{data: nodeData{Type: nt, conn: sc}})
		if err != nil {
			t.Fatalf("loader for %v: %v", nt, err)
		}
		out := make(map[string]NodeType, len(children))
		for _, c := range children {
			out[c.label] = c.data.Type
		}
		return out
	}

	if got, ok := childLabels(NodeServer)["Management"]; !ok || got != NodeManagement {
		t.Errorf(`server root's "Management" child has type %v (present: %v), want NodeManagement`, got, ok)
	}
	if got, ok := childLabels(NodeManagement)["SQL Server Logs"]; !ok || got != NodeSQLServerLogs {
		t.Errorf(`Management's "SQL Server Logs" child has type %v (present: %v), want NodeSQLServerLogs`, got, ok)
	}
	if got, ok := childLabels(NodeAgentJobs)["Error Logs"]; !ok || got != NodeAgentErrorLogs {
		t.Errorf(`SQL Server Agent's "Error Logs" child has type %v (present: %v), want NodeAgentErrorLogs`, got, ok)
	}
}
