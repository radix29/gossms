package tui

import (
	"context"
	"slices"
	"strings"
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

// TestLogViewerNarrowingDropsFilterFocus pins the resize case: the filter field
// is parked off-screen when the toolbar outgrows the panel, and HandleKey routes
// on filterFocused alone — so a field left focused there swallowed every
// keystroke into something that wasn't drawn.
func TestLogViewerNarrowingDropsFilterFocus(t *testing.T) {
	lv := newTestLogViewer()
	lv.active = true
	lv.SetBounds(0, 0, 200, 30)
	lv.setFilterFocused(true)
	if !lv.filterVisible() || !lv.filterFocused {
		t.Fatal("setup failed: the filter field must be visible and focused at width 200")
	}

	lv.SetBounds(0, 0, 40, 30)
	if lv.filterVisible() {
		t.Fatal("filter field fit at width 40; the rest of this test is meaningless")
	}
	if lv.filterFocused {
		t.Error("narrowing left focus on the hidden filter field; keystrokes go nowhere visible")
	}
	// Focus must land back on the grid, not nowhere: typing has to reach
	// something after the resize.
	x := tcell.NewEventKey(tcell.KeyRune, "x", tcell.ModNone)
	before := lv.filter.Value()
	lv.HandleKey(x)
	if lv.filter.Value() != before {
		t.Errorf("a keystroke after narrowing reached the hidden filter field (value %q)", lv.filter.Value())
	}
}

// TestLogViewerBusyToolbarIsInert is the "dimmed but clickable" regression:
// drawToolbar greys every cell while a read is in flight, and both the click
// path and F5 must refuse it. They didn't — clicking a dimmed Refresh started a
// second read and left the first running to logReadTimeout.
func TestLogViewerBusyToolbarIsInert(t *testing.T) {
	lv := newTestLogViewer()
	lv.SetBounds(0, 0, 200, 30)

	fired := 0
	lv.tools[logToolRefresh].action = func() { fired++ }
	r := lv.tools[logToolRefresh].rect
	if r.IsZero() {
		t.Fatal("Refresh cell didn't fit at width 200; the rest of this test is meaningless")
	}
	click := tcell.NewEventMouse(r.X+1, r.Y, tcell.Button1, tcell.ModNone)
	f5 := tcell.NewEventKey(tcell.KeyF5, "", tcell.ModNone)

	lv.busy = true
	if lv.toolsEnabled() {
		t.Fatal("toolsEnabled() is true while busy — the toolbar draws dimmed on the same answer")
	}
	lv.HandleMouse(click)
	lv.dragZone = lZoneNone // the press armed the gesture; clear it for the idle click below
	if !lv.HandleKey(f5) {
		t.Error("F5 while busy returned false — the key belongs to the panel even when it does nothing")
	}
	if fired != 0 {
		t.Errorf("Refresh ran %d times while busy, want 0 (dimmed cells must be inert)", fired)
	}

	lv.busy = false
	lv.HandleMouse(click)
	lv.dragZone = lZoneNone
	lv.HandleKey(f5)
	if fired != 2 {
		t.Errorf("Refresh ran %d times when idle, want 2 (one click, one F5)", fired)
	}
}

// TestLogViewerLoadCancelsSupersededRead pins the other half: seq discards a
// superseded result, but the query behind it has to be cancelled too or it runs
// on the shared host connection until logReadTimeout.
func TestLogViewerLoadCancelsSupersededRead(t *testing.T) {
	lv := newTestLogViewer()
	cancelled := false
	lv.cancel = func() { cancelled = true }

	lv.cancelRead()
	if !cancelled {
		t.Error("cancelRead() didn't call the in-flight read's cancel func")
	}
	if lv.cancel != nil {
		t.Error("cancelRead() left the stale cancel func in place; the next call would cancel a finished read")
	}
}

// TestLogViewerExportTextRendersShownEntries pins what the export writes: the
// header plus one flattened line per *shown* entry, not per entry read. The
// text is rendered on the UI goroutine for a reason — applyFilter reuses
// shown's backing array, so the write goroutine gets a finished string rather
// than a slice that changes under it.
func TestLogViewerExportTextRendersShownEntries(t *testing.T) {
	lv := newTestLogViewer()
	lv.entries = testLogEntries()
	lv.filter.SetValue("server")
	lv.applyFilter()

	lines := strings.Split(strings.TrimRight(lv.exportText(), "\n"), "\n")
	if len(lines) != 1+len(lv.shown) {
		t.Fatalf("exportText produced %d lines, want %d (header + %d shown)", len(lines), 1+len(lv.shown), len(lv.shown))
	}
	if got := strings.Split(lines[0], "\t"); !slices.Equal(got, logExportColumns) {
		t.Errorf("header = %v, want %v", got, logExportColumns)
	}
	if n := len(strings.Split(lines[1], "\t")); n != len(logExportColumns) {
		t.Errorf("first row has %d fields, want %d", n, len(logExportColumns))
	}
	// The unfiltered entry must be absent — the export follows the filter.
	if strings.Contains(lv.exportText(), "Recovery is complete.") {
		t.Error("exportText included a filtered-out entry")
	}
}

// TestLogViewerDetailLinesAreCached pins the per-frame wrap: drawDetails and
// every scroll step ask for the whole wrapped entry, so the result is memoized
// per (entry, width) — and must still re-wrap when either changes, or a resize
// would leave the pane showing the old wrap.
func TestLogViewerDetailLinesAreCached(t *testing.T) {
	lv := newTestLogViewer()
	lv.entries = testLogEntries()
	lv.applyFilter()
	e := lv.entries[0]

	first := lv.detailLines(e, 40)
	if len(first) == 0 {
		t.Fatal("detailLines returned nothing")
	}
	if again := lv.detailLines(e, 40); &again[0] != &first[0] {
		t.Error("a repeat call at the same width re-wrapped instead of returning the cached lines")
	}
	if narrow := lv.detailLines(e, 20); &narrow[0] == &first[0] {
		t.Error("a width change returned the cache; the pane would draw the old wrap")
	}
	if other := lv.detailLines(lv.entries[1], 40); other[2] == first[2] {
		t.Errorf("a different entry returned the first entry's Source line (%q)", other[2])
	}
	// The header lines name the log file, not the entry, so a fresh
	// enumeration has to drop the cache even though the entry is unchanged.
	lv.detailLines(e, 40)
	lv.invalidateDetailCache()
	if after := lv.detailLines(e, 40); &after[0] == &first[0] {
		t.Error("invalidateDetailCache didn't force a re-wrap")
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

// -- the toolbar's overflow ---------------------------------------------------

// lvOverflowFiles is an enumeration whose labels are the ones a real instance
// produces: the File selector carries the file's date, which is what makes it
// the row's longest cell — a test run against the bare "Current" label
// measures a toolbar no user ever sees.
func lvOverflowFiles() []*gosmo.ErrorLogFile {
	return []*gosmo.ErrorLogFile{
		{Number: 0, LastWritten: time.Date(2026, 8, 28, 9, 14, 0, 0, time.UTC)},
		{Number: 1, LastWritten: time.Date(2026, 8, 27, 9, 0, 0, 0, time.UTC)},
	}
}

// TestNoLogViewerToolbarButtonIsUnreachableAtAnyWidth. A cell that does not fit
// the row got a zero rect, which is neither painted nor hit-tested — and only
// Refresh has a key binding (F5), so Search, Recycle and Export simply could
// not be invoked. The row wants 121 columns with both selectors labelled and
// the pane gets 70% of the terminal, so that was every ordinary terminal.
// Every cell must now be drawn or be in the More menu.
func TestNoLogViewerToolbarButtonIsUnreachableAtAnyWidth(t *testing.T) {
	sawOverflow := false
	for w := 20; w <= 200; w++ {
		lv := newTestLogViewer()
		lv.files[gosmo.ErrorLogSQLServer] = lvOverflowFiles()
		lv.SetBounds(0, 0, w, 40)
		lv.refreshToolLabels()
		lv.layoutTools()

		inMenu := map[int]bool{}
		for _, i := range lv.hidden {
			inMenu[i] = true
		}
		for i, tb := range lv.tools {
			if tb.rect.IsZero() != inMenu[i] {
				t.Fatalf("width %d: cell %d (%q) is neither drawn nor in the More menu", w, i, tb.label)
			}
		}
		if len(lv.hidden) == 0 {
			continue
		}
		sawOverflow = true
		if lv.more.rect.IsZero() && w > 12 {
			t.Fatalf("width %d: the row hides %d cells with no More cell to reach them", w, len(lv.hidden))
		}
		if !lv.more.rect.IsZero() && lv.more.rect.Right() > lv.rect.Right() {
			t.Fatalf("width %d: the More cell runs past the pane", w)
		}
		// The hidden set has to be the row's tail, or the menu holds the
		// middle of the toolbar and the row itself has a gap in it.
		want := len(lv.tools) - len(lv.hidden)
		for n, i := range lv.hidden {
			if i != want+n {
				t.Fatalf("width %d: hidden cells %v are not the row's tail starting at %d", w, lv.hidden, want)
			}
		}
	}
	if !sawOverflow {
		t.Fatal("nothing overflowed at any width, so this proves nothing")
	}
}

// TestTheLogViewerOverflowMenuRunsTheHiddenAction end to end: the button is not
// drawn, the More cell is, and choosing the entry runs the action the button
// would have.
func TestTheLogViewerOverflowMenuRunsTheHiddenAction(t *testing.T) {
	a, lv, _ := newRecycleTestViewer(t)
	a.logSearchDialog = NewLogSearchDialog(a) // newTestApp builds no dialogs
	lv.entries = testLogEntries()
	lv.applyFilter()

	lv.SetBounds(0, 0, 60, 40)
	lv.refreshToolLabels()
	lv.layoutTools()
	if !lv.tools[logToolSearch].rect.IsZero() {
		t.Fatal("Search still fits at 60 columns, so this proves nothing")
	}
	if lv.more.rect.IsZero() {
		t.Fatal("no More cell to reach it through")
	}

	// Press the More cell where the mouse would.
	press := tcell.NewEventMouse(lv.more.rect.X+1, lv.more.rect.Y, tcell.Button1, 0)
	if !lv.HandleMouse(press) {
		t.Fatal("the More cell did not take the press")
	}
	chooseMenuItem(t, a, "Search...")

	if !a.logSearchDialog.Visible() {
		t.Error("choosing Search from the More menu did not open the search dialog")
	}
}

// TestTheLogViewerOverflowMenuKeepsTheGate. A hidden action must not become
// reachable in a state its button would have refused, and the withheld entry
// still has to say why — MenuItem.Note, which shows precisely while disabled.
func TestTheLogViewerOverflowMenuKeepsTheGate(t *testing.T) {
	a := newTestApp()
	sc := probedConn(t, "", nil, []string{"CONTROL SERVER"}, nil, nil)
	a.connections = append(a.connections, sc)

	lv := newTestLogViewer()
	lv.app, lv.conn = a, sc
	lv.SetBounds(0, 0, 60, 40)
	lv.refreshToolLabels()
	lv.layoutTools()
	if !lv.tools[logToolRecycle].rect.IsZero() {
		t.Fatal("Recycle still fits at 60 columns, so this proves nothing")
	}

	lv.showOverflowMenu()
	var found bool
	for _, it := range a.contextMenu.Items() {
		if !strings.Contains(it.Label, "Recycle") {
			continue
		}
		found = true
		if it.Enabled == nil || it.Enabled() {
			t.Error("Recycle is offered in the More menu to a login refused CONTROL SERVER")
		}
		if !strings.Contains(it.Note, "CONTROL SERVER") {
			t.Errorf("the withheld entry's note is %q, want the missing right named", it.Note)
		}
	}
	if !found {
		t.Fatalf("Recycle is not in the More menu; it holds %v", a.contextMenu.Items())
	}
}
