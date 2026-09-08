package tui

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"
	"time"

	gosmo "github.com/radix29/gosmo"
)

// log_viewer_merge_test.go covers the Log File Viewer's merged multi-file view:
// the read fan-out, the order the files are merged in, what a file that could
// not be read does to the grid, and the checklist that picks the set.

// mergeTestFiles is the enumeration both merged-read tests select from — the
// current log and one archive.
func mergeTestFiles() fakeResponse {
	return fakeResponse{match: "sp_enumerrorlogs", cols: 3, rows: [][]driver.Value{
		{int64(0), "08/21/2026  09:00", int64(4096)},
		{int64(1), "08/20/2026  09:00", int64(8192)},
	}}
}

func mergeTestEntry(h, m int, text string) []driver.Value {
	return []driver.Value{time.Date(2026, 8, 21, h, m, 0, 0, time.UTC), "Server", text}
}

// bothFiles is the two-file selection the tests read.
func bothFiles() []logFileRef {
	return []logFileRef{
		{Type: gosmo.ErrorLogSQLServer, Num: 0},
		{Type: gosmo.ErrorLogSQLServer, Num: 1},
	}
}

// TestLogViewerMergesFilesNewestFirstAndDeterministically is the one place this
// feature can silently lie. Two files read concurrently land in whatever order
// the server answers, and entries sharing a timestamp — the ordinary case, the
// log writes several rows a second — would then swap places under the cursor on
// every refresh. The merge is by selection order, so the same read twice gives
// the same grid.
func TestLogViewerMergesFilesNewestFirstAndDeterministically(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t,
		mergeTestFiles(),
		fakeResponse{match: "xp_readerrorlog 0, 1", cols: 3, rows: [][]driver.Value{
			mergeTestEntry(10, 0, "current, tied"),
			mergeTestEntry(10, 2, "current, newest"),
		}},
		fakeResponse{match: "xp_readerrorlog 1, 1", cols: 3, rows: [][]driver.Value{
			mergeTestEntry(10, 0, "archive, tied"),
			mergeTestEntry(10, 1, "archive, middle"),
		}},
	)
	a.connections = append(a.connections, sc)
	lv := NewLogViewer(a, sc, gosmo.ErrorLogSQLServer, 0)

	want := []string{"current, newest", "archive, middle", "current, tied", "archive, tied"}
	for pass := range 2 {
		lv.ShowLogs(gosmo.ErrorLogSQLServer, bothFiles())
		waitAndDrain(t, a)
		if len(lv.entries) != 4 {
			t.Fatalf("pass %d: merged read returned %d entries, want all 4", pass, len(lv.entries))
		}
		for i, w := range want {
			if got := lv.entries[i].entry.Text; got != w {
				t.Fatalf("pass %d: entry %d = %q, want %q (order: %v)", pass, i, got, w, want)
			}
		}
		// The tie has to break by *file*, and the file each row came from is
		// what the File column and the details pane read.
		if lv.entries[2].ref.Num != 0 || lv.entries[3].ref.Num != 1 {
			t.Errorf("pass %d: the tied rows came from files %d and %d, want the selection's order 0 then 1",
				pass, lv.entries[2].ref.Num, lv.entries[3].ref.Num)
		}
	}
}

// TestLogViewerMergedReadKeepsWhatItCouldRead. One unreadable archive out of
// two must not empty a grid holding the other one's rows — and the status line
// has to say a file is missing, since the entry count alone cannot.
func TestLogViewerMergedReadKeepsWhatItCouldRead(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t,
		mergeTestFiles(),
		fakeResponse{match: "xp_readerrorlog 1, 1", err: errors.New("the log file could not be opened")},
		fakeResponse{match: "xp_readerrorlog 0, 1", cols: 3, rows: [][]driver.Value{
			mergeTestEntry(10, 0, "current, readable"),
		}},
	)
	a.connections = append(a.connections, sc)
	lv := NewLogViewer(a, sc, gosmo.ErrorLogSQLServer, 0)

	lv.ShowLogs(gosmo.ErrorLogSQLServer, bothFiles())
	waitAndDrain(t, a)

	if len(lv.entries) != 1 || lv.entries[0].entry.Text != "current, readable" {
		t.Fatalf("a failed archive took the readable file's rows with it: %v", lv.entries)
	}
	if len(lv.readErrs) != 1 || lv.readErrs[0].ref.Num != 1 {
		t.Fatalf("readErrs = %v, want the one archive that failed", lv.readErrs)
	}
	if got := lv.grid.Status(); !strings.Contains(got, "1 of 2 files read") {
		t.Errorf("status = %q, want it to say how much of the selection is shown", got)
	}
	if got := lv.grid.Status(); !strings.Contains(got, "Archive #1") {
		t.Errorf("status = %q, want it to name the file that failed", got)
	}
}

// TestLogViewerMergedReadFailsOnlyWhenEverythingFails. The grid goes to an
// error when there is nothing to show — the single-file view's behaviour, which
// the merge must not turn into a silently empty grid.
func TestLogViewerMergedReadFailsOnlyWhenEverythingFails(t *testing.T) {
	a := newTestApp()
	sc, _ := newFakeConn(t,
		mergeTestFiles(),
		fakeResponse{match: "xp_readerrorlog", err: errors.New("boom")},
	)
	a.connections = append(a.connections, sc)
	lv := NewLogViewer(a, sc, gosmo.ErrorLogSQLServer, 0)

	lv.ShowLogs(gosmo.ErrorLogSQLServer, bothFiles())
	waitAndDrain(t, a)

	if len(lv.entries) != 0 {
		t.Fatalf("entries = %v, want none", lv.entries)
	}
	if got := lv.grid.Status(); got != "Error" {
		t.Errorf("status = %q, want the grid in its error state", got)
	}
	if got := lv.grid.Row(0); len(got) != 1 || !strings.Contains(got[0], "boom") {
		t.Errorf("grid row = %v, want the read's error", got)
	}
}

// TestLogViewerFileColumnOnlyWhenMerging. The Source column is the log's own
// ProcessInfo and says nothing about which file a row came from, so a merged
// grid needs a File column — and the single-file view must be left exactly as
// it was, header and all.
func TestLogViewerFileColumnOnlyWhenMerging(t *testing.T) {
	lv := newTestLogViewer()
	row := logRow{
		entry: &gosmo.ErrorLogEntry{Date: time.Now(), Process: "Server", Text: "hello"},
		ref:   logFileRef{Type: gosmo.ErrorLogSQLServer, Num: 1},
	}

	if got := lv.gridColumns(); len(got) != 3 || got[1] != "Source" {
		t.Errorf("one file selected: columns = %v, want the unchanged three", got)
	}
	if got := lv.cells(row); len(got) != 3 {
		t.Errorf("one file selected: cells = %v, want three", got)
	}

	lv.sel = bothFiles()
	got := lv.gridColumns()
	if len(got) != 4 || got[1] != "File" {
		t.Fatalf("two files selected: columns = %v, want a File column", got)
	}
	if len(lv.exportColumns()) != 4 {
		t.Errorf("export header = %v, want the File column too", lv.exportColumns())
	}
	cells := lv.cells(row)
	if len(cells) != 4 || cells[1] != "Archive #1" {
		t.Errorf("cells = %v, want the row's own file in the File column", cells)
	}
}

// TestLogViewerSelectionLabelsCountFiles. Every label that named one file has a
// plural form; the toolbar cell and the status line would otherwise claim the
// grid holds whichever file happened to be first.
func TestLogViewerSelectionLabelsCountFiles(t *testing.T) {
	lv := newTestLogViewer()
	lv.entries = testLogEntries()
	lv.applyFilter()
	if got := lv.summary(); !strings.Contains(got, "Current") {
		t.Errorf("one file: summary = %q, want it to name the file", got)
	}

	lv.sel = bothFiles()
	lv.applyFilter()
	lv.refreshToolLabels()
	if got := lv.selectionLabel(); got != "2 files" {
		t.Errorf("selectionLabel = %q, want %q", got, "2 files")
	}
	if got := lv.tools[logToolFile].label; !strings.Contains(got, "2 files") {
		t.Errorf("file selector label = %q, want it to say how many files are merged", got)
	}
	if got := lv.summary(); !strings.Contains(got, "2 files") {
		t.Errorf("summary = %q, want it to say how many files are merged", got)
	}
}

// TestLogViewerDetailsPaneNamesTheRowsOwnFile. With several files merged, the
// details pane is where a row says which file it came from at full width —
// naming the *selection* there would name the wrong file on every row but one.
func TestLogViewerDetailsPaneNamesTheRowsOwnFile(t *testing.T) {
	lv := newTestLogViewer()
	lv.sel = bothFiles()
	row := logRow{
		entry: &gosmo.ErrorLogEntry{Date: time.Now(), Process: "Server", Text: "hello"},
		ref:   logFileRef{Type: gosmo.ErrorLogSQLServer, Num: 1},
	}
	lines := lv.detailLines(row, 60)
	if len(lines) < 2 || !strings.Contains(lines[1], "Archive #1") {
		t.Errorf("details pane's Log line = %q, want the row's own file", lines[1])
	}
}

// TestLogViewerChecklistReadsNothingUntilApplied. The toggles edit a working
// copy: dismissing the menu with Escape has to leave the grid describing the
// files it actually holds, not the ones that were ticked.
func TestLogViewerChecklistReadsNothingUntilApplied(t *testing.T) {
	a, lv, inst := newRecycleTestViewer(t)
	lv.files[gosmo.ErrorLogSQLServer] = []*gosmo.ErrorLogFile{
		{Number: 0, Date: "08/21/2026  09:00"},
		{Number: 1, Date: "08/20/2026  09:00"},
	}

	lv.showLogFileMenu()
	chooseMenuItem(t, a, "Select Files...")
	before := inst.QueryCount()
	chooseMenuItem(t, a, "Archive #1")

	if len(lv.sel) != 1 {
		t.Fatalf("ticking a file changed the selection to %v before it was applied", lv.sel)
	}
	if inst.QueryCount() != before {
		t.Fatalf("ticking a file read from the server (%d queries, was %d)", inst.QueryCount(), before)
	}
	if len(lv.pending) != 2 {
		t.Fatalf("pending = %v, want the seeded file plus the ticked one", lv.pending)
	}

	chooseMenuItem(t, a, "Read 2 files")
	waitAndDrain(t, a)
	if len(lv.sel) != 2 {
		t.Fatalf("applying the checklist left the selection at %v", lv.sel)
	}
}

// TestLogViewerChecklistUntickIsReversible. The same row toggles both ways, and
// an emptied selection falls back to the current log rather than a grid with
// nothing to describe.
func TestLogViewerChecklistUntickIsReversible(t *testing.T) {
	a, lv, _ := newRecycleTestViewer(t)
	lv.files[gosmo.ErrorLogSQLServer] = []*gosmo.ErrorLogFile{{Number: 0, Date: "08/21/2026  09:00"}}

	lv.showLogFileMenu()
	chooseMenuItem(t, a, "Select Files...")
	chooseMenuItem(t, a, "Current")
	if len(lv.pending) != 0 {
		t.Fatalf("unticking the only file left pending = %v", lv.pending)
	}
	chooseMenuItem(t, a, "Current")
	if len(lv.pending) != 1 {
		t.Fatalf("re-ticking left pending = %v", lv.pending)
	}

	lv.ShowLogs(gosmo.ErrorLogSQLServer, nil)
	waitAndDrain(t, a)
	if len(lv.sel) != 1 || lv.sel[0].Num != 0 {
		t.Errorf("an empty selection = %v, want the current log", lv.sel)
	}
}

// TestLogViewerReanchorsAMergedSelectionAfterACycle. A cycle renumbers every
// archive one higher and deletes the oldest, so a set chosen by number no
// longer names the files it was chosen from — re-reading those numbers would
// hand back a different set, one file of which may be gone. A single-file view
// keeps its number, which is what it has always done.
func TestLogViewerReanchorsAMergedSelectionAfterACycle(t *testing.T) {
	lv := newTestLogViewer()
	lv.sel = bothFiles()
	lv.reanchorAfterCycle(gosmo.ErrorLogSQLServer)
	if len(lv.sel) != 1 || lv.sel[0].Num != 0 {
		t.Errorf("after a cycle of the family on screen, sel = %v, want the current log alone", lv.sel)
	}

	lv.sel = bothFiles()
	lv.reanchorAfterCycle(gosmo.ErrorLogAgent)
	if len(lv.sel) != 2 {
		t.Errorf("cycling the *other* family changed this one's selection to %v", lv.sel)
	}

	lv.sel = []logFileRef{{Type: gosmo.ErrorLogSQLServer, Num: 1}}
	lv.reanchorAfterCycle(gosmo.ErrorLogSQLServer)
	if len(lv.sel) != 1 || lv.sel[0].Num != 1 {
		t.Errorf("a single-file view was re-anchored to %v; it keeps its archive number", lv.sel)
	}
}
