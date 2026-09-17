package tui

import (
	"fmt"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
)

// log_viewer_rows.go is what the read's rows become on screen: the grid's
// columns, the client-side filter over them, the summary line, and the text
// helpers the two share. The read itself is in log_viewer_load.go.

// logGridColumns are the entry grid's columns. The marker on Date says which
// way rows are ordered; Source is whichever of ProcessInfo and ErrorLevel the
// log family populates.
var logGridColumns = []string{"Date ▼", "Source", "Message"}

// logGridColumnsMulti is logGridColumns with the File column a merged view
// needs. Source is the log's own ProcessInfo/severity and says nothing about
// which *file* a row came from, so a merged grid without this column cannot be
// read at all. It appears only while more than one file is selected — the
// single-file view is untouched.
var logGridColumnsMulti = []string{"Date ▼", "File", "Source", "Message"}

// logExportColumns are the same columns without the sort marker: an exported
// header row names the column rather than describing the grid.
var logExportColumns = []string{"Date", "Source", "Message"}

// logExportColumnsMulti is logExportColumns with the File column, on the same
// condition the grid's is.
var logExportColumnsMulti = []string{"Date", "File", "Source", "Message"}

// gridColumns and exportColumns are the headers for the current selection.
func (lv *LogViewer) gridColumns() []string {
	if lv.multiFile() {
		return logGridColumnsMulti
	}
	return logGridColumns
}

func (lv *LogViewer) exportColumns() []string {
	if lv.multiFile() {
		return logExportColumnsMulti
	}
	return logExportColumns
}

// cells renders one row for the grid and the export, which share a shape.
func (lv *LogViewer) cells(r logRow) []string {
	if lv.multiFile() {
		return []string{formatSQLDate(r.entry.Date), lv.rowFileLabel(r.ref), r.entry.Source(), flattenLogText(r.entry.Text)}
	}
	return []string{formatSQLDate(r.entry.Date), r.entry.Source(), flattenLogText(r.entry.Text)}
}

// applyFilter rebuilds shown from entries and hands it to the grid, matching a
// case-insensitive substring over the source and message. An empty filter
// shows everything.
func (lv *LogViewer) applyFilter() {
	lv.invalidateDetailCache()
	needle := strings.ToLower(strings.TrimSpace(lv.filter.Value()))
	lv.shown = lv.shown[:0]
	for _, r := range lv.entries {
		if needle == "" || logEntryMatches(r.entry, needle) {
			lv.shown = append(lv.shown, r)
		}
	}
	rows := make([][]string, 0, len(lv.shown))
	for _, r := range lv.shown {
		rows = append(rows, lv.cells(r))
	}
	lv.grid.SetData(lv.gridColumns(), rows)
	lv.detailScroll = 0
	lv.setStatus(lv.summary())
}

// invalidateDetailCache forces the next detailLines call to re-wrap. The cache
// is keyed on the entry pointer, but two of the three lines above the message
// name the log file — a fresh enumeration can rename "Archive #3" without the
// selected entry changing.
func (lv *LogViewer) invalidateDetailCache() {
	lv.detailCacheEntry, lv.detailCache = nil, nil
}

// summary is the status line under the grid: how much of the file is shown,
// which file it is, and what the server was asked for when a search is in
// force. Naming the search matters — "no entries" on a searched read means the
// search found nothing, not that the log is empty.
func (lv *LogViewer) summary() string {
	switch {
	case len(lv.entries) == 0:
		return fmt.Sprintf("%s%s — no entries%s", lv.scopeLabel(), lv.searchSuffix(), lv.readErrSuffix())
	case len(lv.shown) == len(lv.entries):
		return fmt.Sprintf("%s%s — %d entries%s", lv.scopeLabel(), lv.searchSuffix(), len(lv.entries), lv.readErrSuffix())
	default:
		return fmt.Sprintf("%s%s — %d of %d entries match the filter%s",
			lv.scopeLabel(), lv.searchSuffix(), len(lv.shown), len(lv.entries), lv.readErrSuffix())
	}
}

// readErrSuffix says how much of the selection the grid is actually showing,
// or "" when every file was read. A merged read that dropped one archive shows
// the rest, so without this the panel would silently be short a file — and the
// entry count alone cannot say so.
func (lv *LogViewer) readErrSuffix() string {
	if len(lv.readErrs) == 0 {
		return ""
	}
	first := lv.readErrs[0]
	return fmt.Sprintf(" — %d of %d files read (%s: %v)",
		len(lv.sel)-len(lv.readErrs), len(lv.sel), lv.rowFileLabel(first.ref), displayError(first.err))
}

// searchSuffix describes the server-side search for the status line, or "" if
// there is none.
func (lv *LogViewer) searchSuffix() string {
	parts := make([]string, 0, 3)
	if lv.search.Text1 != "" {
		parts = append(parts, fmt.Sprintf("%q", lv.search.Text1))
	}
	if lv.search.Text2 != "" {
		parts = append(parts, fmt.Sprintf("%q", lv.search.Text2))
	}
	if !lv.search.From.IsZero() || !lv.search.To.IsZero() {
		parts = append(parts, fmt.Sprintf("%s..%s",
			orDefault(formatLogSearchTime(lv.search.From), "…"),
			orDefault(formatLogSearchTime(lv.search.To), "…")))
	}
	if len(parts) == 0 {
		return ""
	}
	return " searching " + strings.Join(parts, " + ")
}

// showSearch opens the Search dialog and re-reads with whatever it returns,
// unconditionally, including for an unchanged search: a press that appeared to
// do nothing would read as the dialog having failed.
func (lv *LogViewer) showSearch() {
	if !lv.app.requireConn(lv.conn) {
		return
	}
	lv.app.logSearchDialog.ShowLogSearch(lv.search, func(search gosmo.LogSearch) {
		lv.search = search
		lv.detailScroll = 0
		lv.Load()
	})
}

// setStatus writes the panel's one-line state into the grid's own status bar,
// so it sits with the rows it describes.
func (lv *LogViewer) setStatus(s string) { lv.grid.SetStatus(s) }

// logEntryMatches reports whether needle (already lowercased) appears in the
// entry's source or message.
func logEntryMatches(e *gosmo.ErrorLogEntry, needle string) bool {
	return strings.Contains(strings.ToLower(e.Text), needle) ||
		strings.Contains(strings.ToLower(e.Source()), needle)
}

// flattenLogText makes one grid line out of a log entry's text. An entry can
// carry embedded newlines and tabs — the startup banner spans four lines — and
// a grid cell is one row tall, so they become spaces. The details pane shows
// the text as written.
func flattenLogText(s string) string {
	if !strings.ContainsAny(s, "\r\n\t") {
		return s
	}
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(s)), " ")
}

// sortLogRowsDesc orders merged rows newest first, as SSMS's Log File Viewer
// opens. The sort is stable and the input is in selection order, file by file,
// so a timestamp shared across two files breaks by file and then by position
// within the file — the same order every time. An unstable sort, or a merge in
// completion order, would reorder same-second rows under the cursor on every
// refresh, and reversing a shared second would scramble a startup sequence or
// a stack dump.
func sortLogRowsDesc(rows []logRow) []logRow {
	slices.SortStableFunc(rows, func(a, b logRow) int {
		return b.entry.Date.Compare(a.entry.Date)
	})
	return rows
}

// splitLogLines breaks an entry's text into the lines the log wrote. One
// xp_readerrorlog row can span several — the startup banner puts the build date
// and the OS on their own indented lines — and the details pane wraps each
// separately rather than reflowing them into a paragraph. Line breaks survive;
// indentation does not, since core.WrapText splits on strings.Fields.
func splitLogLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.Split(s, "\n")
}

// selectedLogRow is the row the grid's cursor is on, and whether there is one.
// Indexed against shown, which is what the grid was built from.
func (lv *LogViewer) selectedLogRow() (logRow, bool) {
	row := lv.grid.SelectedRow()
	if row < 0 || row >= len(lv.shown) {
		return logRow{}, false
	}
	return lv.shown[row], true
}
