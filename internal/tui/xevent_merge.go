package tui

import (
	"context"
	"fmt"
	"slices"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
	"github.com/radix29/gossms/internal/tuikit/layout"
	"github.com/radix29/gossms/internal/xevent"
)

// xevent_merge.go is Merge Extended Event Files: every .xel file on the
// server matching a wildcard pattern, read with fn_xe_file_target_read_file
// and shown in one Extended Events viewer in time order — SSMS's File > Open >
// Merge Extended Event Files, except that the files are the server's, not the
// client's (reading a local .xel is out of scope: the format has no public
// decoder). Offered on the Extended Events folder and the File menu.

// xeMergeDefaultPattern is the pattern the prompt starts on: every event file
// in the error-log directory, where system_health, AlwaysOn_health and a
// session given a bare file name write theirs.
const xeMergeDefaultPattern = "*.xel"

// xeMergeHelp is the prompt's explanation.
const xeMergeHelp = "Reads every Extended Events file on the server matching a pattern and shows " +
	"their events together in time order. Use * as a wildcard, e.g. " +
	`D:\XE\deadlocks*.xel; a name without a folder is looked for in the error-log directory.`

// xeMergeDefaultPatternAzure is the prompt's start on an Azure engine
// edition: the one local file set a Managed Instance reads (see
// xeReadsOnlyByPattern) — *.xel there is Msg 40538.
const xeMergeDefaultPatternAzure = "system_health*.xel"

// xeMergeHelpAzure is xeMergeHelp on an Azure engine edition, where the files
// a session writes are blobs: the server reads them with the credential named
// after their container (a database-scoped one on Azure SQL Database). Of a
// Managed Instance's own local files it reads system_health's alone, and only
// by that session's pattern.
const xeMergeHelpAzure = "Reads every Extended Events file matching a pattern and shows their events " +
	"together in time order. Use * as a wildcard. Files a session writes here are in Azure Blob Storage: " +
	"give the URL, e.g. https://account.blob.core.windows.net/container/session*.xel, which the server " +
	"reads with the credential named after the container. Of the server's own files only system_health's " +
	"can be read, as system_health*.xel."

// extendedEventsMenuItems is the Extended Events folder's menu.
func extendedEventsMenuItems(a *App, sc *db.ServerConn, node *explorerNode, newQuery, refresh controls.MenuItem) []controls.MenuItem {
	return []controls.MenuItem{
		newQuery,
		{Divider: true},
		{Label: "Merge Extended Event Files...", Action: func() { a.promptMergeXEventFiles(sc) }},
		{Divider: true},
		refresh,
	}
}

// promptMergeXEventFilesActive is File > Merge Extended Event Files, on the
// connection Object Explorer has selected.
func (a *App) promptMergeXEventFilesActive() {
	if sc := a.connOrFirst(); sc != nil {
		a.promptMergeXEventFiles(sc)
	}
}

// promptMergeXEventFiles asks for the pattern and opens the merged viewer.
func (a *App) promptMergeXEventFiles(sc *db.ServerConn) {
	if !a.requireConn(sc) {
		return
	}
	help, pattern := xeMergeHelp, xeMergeDefaultPattern
	if serverIsAzure(sc) {
		help, pattern = xeMergeHelpAzure, xeMergeDefaultPatternAzure
	}
	a.promptDialog.ShowPrompt("Merge Extended Event Files", help, "Files:", pattern,
		func(pattern string) { a.openMergedXEventFiles(sc, pattern) })
}

// openMergedXEventFiles opens a viewer on the files matching pattern, or
// raises the one already open on them.
func (a *App) openMergedXEventFiles(sc *db.ServerConn, pattern string) {
	if !a.requireConn(sc) {
		return
	}
	idx := a.panels.FindIndex(func(p layout.Panel) bool {
		v, ok := p.(*XEventViewer)
		return ok && v.host == sc && v.files == pattern
	})
	if idx < 0 {
		v := NewXEventViewer(a, sc, "", gosmo.XETargetEventFile, false)
		v.files = pattern
		v.hiddenCols = loadXEHiddenColumns(a, v.columnsKey())
		idx = a.panels.AddPanel(v)
		a.connectXEventViewer(v)
	} else {
		v := a.panels.PanelAt(idx).(*XEventViewer)
		if !v.feed.running {
			v.refreshTarget()
		}
	}
	a.panels.SetActive(idx)
	a.focusPanels()
}

// columnsKey is the name the Choose Columns state is saved under: the
// session's, or for merged files one name for all of them, since the files a
// pattern matches change.
func (v *XEventViewer) columnsKey() string {
	if v.files != "" {
		return "(merged event files)"
	}
	return v.session
}

// readMerged is the reader for merged files: each file the pattern lists
// read whole, a chunk per call, then every event sorted by timestamp and
// posted at once — the store appends, and files of different sessions
// interleave in time. Where the files can't be listed (a login without
// xp_dirtree's answer, a URL) or can't be read by their paths (a Managed
// Instance, xeReadsOnlyByPattern) the pattern itself is read as one set.
//
// A merge can match far more than the viewer holds; whenever the events read
// pass twice the capacity they are sorted and the oldest dropped, so a big
// merge costs twice the capacity in memory, not the whole set.
func (r *xeReader) readMerged(ctx context.Context) (xeBatch, bool, error) {
	b := xeBatch{source: "merged " + gosmo.XETargetEventFile}
	if !r.planned {
		r.planned = true
		var files []string
		if !xeReadsOnlyByPattern(r.sc, r.pattern) {
			files, _ = r.sc.Server.EventFiles(ctx, r.pattern)
		}
		if len(files) == 0 {
			files = []string{r.pattern}
		}
		r.backlog = files
		r.mergeFiles = len(files)
	}
	if len(r.backlog) == 0 {
		return r.flushMerged(b), false, nil
	}
	file := r.backlog[0]
	evs, next, err := r.sc.Server.ReadEventFile(ctx, file, r.cursor, xeReadChunk)
	if err != nil {
		return b, false, fmt.Errorf("%s: %w", file, err)
	}
	r.fileEvents = append(r.fileEvents, convertXEvents(evs)...)
	if len(r.fileEvents) > 2*r.capacity {
		before := len(r.fileEvents)
		r.fileEvents = newestByTime(r.fileEvents, r.capacity)
		b.discarded = before - len(r.fileEvents)
	}
	if len(evs) >= xeReadChunk {
		r.cursor = next
	} else {
		r.cursor = gosmo.EventFileCursor{}
		r.backlog = r.backlog[1:]
	}
	if len(r.backlog) == 0 {
		return r.flushMerged(b), false, nil
	}
	b.progress = fmt.Sprintf("merging: file %d of %d, %d events so far",
		r.mergeFiles-len(r.backlog)+1, r.mergeFiles, len(r.fileEvents))
	return b, true, nil
}

// flushMerged hands the merged events over in time order and ends the read.
func (r *xeReader) flushMerged(b xeBatch) xeBatch {
	b.events = newestByTime(r.fileEvents, len(r.fileEvents))
	b.progress = ""
	if r.mergeFiles > 1 {
		b.source = fmt.Sprintf("%d files merged", r.mergeFiles)
	}
	r.fileEvents, r.backlog = nil, nil
	return b
}

// newestByTime sorts evs by timestamp, stably so a file's own order breaks
// ties, and keeps the newest n.
func newestByTime(evs []xevent.Event, n int) []xevent.Event {
	slices.SortStableFunc(evs, func(a, b xevent.Event) int { return a.Timestamp.Compare(b.Timestamp) })
	if len(evs) > n {
		evs = evs[len(evs)-n:]
	}
	return evs
}
