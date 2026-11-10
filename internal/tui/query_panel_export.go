package tui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/radix29/gossms/internal/export"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/query"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// csvSink implements query.RowSink by writing each row to a CSV file as it
// arrives, so an export never holds more than one row in memory. The format —
// sets separated by a blank line, each preceded by its header row, and the NULL
// convention — is export.RecordWriter's, shared with the command line's csv.
type csvSink struct {
	st *fileutil.Stream
	w  *export.RecordWriter
}

// newCSVSink returns a sink streaming to a temp file beside path. path itself is
// replaced only by Close, so a failed or stopped run (Abort) leaves what the user
// agreed to overwrite as it was.
func newCSVSink(path string) (*csvSink, error) {
	st, err := fileutil.CreateStream(path, 0o644)
	if err != nil {
		return nil, err
	}
	return &csvSink{st: st, w: export.NewRecordWriter(st, export.CSV)}, nil
}

// BeginSet writes the separator (for every set after the first) and header.
func (s *csvSink) BeginSet(columns []string) error { return s.w.BeginSet(columns) }

func (s *csvSink) Row(cells []string, isNull []bool) error { return s.w.WriteRecord(cells, isNull) }

// EndSet flushes this set's rows so a long export reaches the disk as it goes
// rather than only at Close.
func (s *csvSink) EndSet(int) error { return s.w.Flush() }

// Close flushes the rows and replaces the destination with them. A flush error is
// preferred over a commit error since it names the actual failure; either way the
// destination is left as it was.
func (s *csvSink) Close() error {
	if err := s.w.Flush(); err != nil {
		s.st.Abort()
		return err
	}
	return s.st.Commit()
}

// Abort discards the rows written so far; the destination is not touched.
func (s *csvSink) Abort() { s.st.Abort() }

// finish ends a run: a stopped run or one that reported any error is aborted, so
// the destination the user agreed to overwrite is left as it was (kept), and
// anything else is committed by Close. The handle is released either way.
func (s *csvSink) finish(cancelled bool, res *query.Result) (kept bool, err error) {
	if cancelled || res.HasErrors() {
		s.Abort()
		return true, nil
	}
	return false, s.Close()
}

// promptResultsFile asks where a Results To File run should write, then hands the
// chosen path to run. Cancelling the dialog calls neither.
//
// The prompt comes *before* execution: rows are streamed as scanned (csvSink), so
// the destination has to be known by the time the query starts. This also matches SSMS, which asks for the filename when you execute in
// Results To File mode.
func (p *QueryPanel) promptResultsFile(run func(path string)) {
	p.app.fileDialog.ShowSave("Results To File", "results.csv", run)
}

// reportExport appends the outcome of a streamed export to res so it shows up
// in the Messages tab, and mirrors it to the status bar.
func (p *QueryPanel) reportExport(res *query.Result, path string, rows int, kept bool, err error) {
	msg := query.Message{Text: fmt.Sprintf("%d row(s) written to %s", rows, path)}
	if kept {
		msg = query.Message{Text: fmt.Sprintf("Results not written; %s was not changed", path)}
	}
	if err != nil {
		msg = query.Message{Text: fmt.Sprintf("write results to %s: %v", path, err), IsError: true}
	}
	res.Messages = append(res.Messages, msg)
	if p.result == res {
		p.renderActiveTab()
	}
	p.app.setStatus(msg.Text)
}

// resultsExportFormats are Export Results' formats, in menu order.
var resultsExportFormats = []struct {
	label, ext string
	format     export.Format
}{
	{"To CSV File...", "csv", export.FormatCSV},
	{"To Tab-Separated File...", "tsv", export.FormatTSV},
	{"To JSON File...", "json", export.FormatJSON},
	{"To XML File...", "xml", export.FormatXML},
}

// exportResultsMenu is the Export Results cascade, in the File menu and both of
// the results grid's context menus. target is the panel to export from when the
// item is chosen: the File menu's is whichever query panel is active then, the
// grid's is its own. The cascade is greyed when there is no result set to export.
func exportResultsMenu(target func() *QueryPanel) controls.MenuItem {
	sub := make([]controls.MenuItem, len(resultsExportFormats))
	for i, f := range resultsExportFormats {
		sub[i] = controls.MenuItem{Label: f.label, Action: func() {
			if p := target(); p != nil {
				p.exportResults(f.format, f.ext)
			}
		}}
	}
	return controls.MenuItem{Label: "Export Results", Sub: sub,
		Enabled: func() bool { p := target(); return p != nil && p.canExportResults() },
		Note:    "no result set"}
}

// canExportResults reports whether a result set's tab is the active one: Export
// writes that set, not the Messages or Execution Plan tab.
func (p *QueryPanel) canExportResults() bool {
	_, ok := p.activeResultSet()
	return ok
}

// gridExportMenuItems is the results grid's OnGridMenuItems: Export Results
// below Copy All on the row-number header cell's menu.
func (p *QueryPanel) gridExportMenuItems() []controls.MenuItem {
	if !p.canExportResults() {
		return nil
	}
	return []controls.MenuItem{exportResultsMenu(func() *QueryPanel { return p })}
}

// resultMenuItems is the results grid's OnMenuItems: the column's sort items,
// then Export Results, so the keyboard (Ctrl+Space) reaches Export too.
func (p *QueryPanel) resultMenuItems() []controls.MenuItem {
	items := p.resultSortMenuItems()
	if exp := p.gridExportMenuItems(); len(exp) > 0 {
		if len(items) > 0 {
			items = append(items, controls.MenuItem{Divider: true})
		}
		items = append(items, exp...)
	}
	return items
}

// exportResults asks where to write the active result set as format f, then
// writes it off the UI goroutine. The rows go in the order the grid shows them
// (a column sort included), every row and column, whole: unlike Copy, an
// export is lossless, and a NULL stays distinct from the string 'NULL' in every
// format (docs/decisions.md § SQL NULL in results). The set is a snapshot taken
// now: a result is never changed once installed, and a new run replaces it
// rather than writing into it.
func (p *QueryPanel) exportResults(f export.Format, ext string) {
	set, ok := p.activeResultSet()
	if !ok {
		p.app.setStatus("No result set to export")
		return
	}
	var rows export.Table = set
	if p.resultsMode != ResultsModeText {
		if s, ok := p.activeSort(); ok {
			rows = sortedResultSet{set: set, perm: s.perm}
		}
	}
	p.app.fileDialog.ShowSave("Export Results", p.exportFileName(ext), func(path string) {
		n := rows.Len()
		p.app.safego("exporting results", func() {
			err := writeResultSet(path, f, set, rows)
			p.app.postAndWake(func() {
				if err != nil {
					p.app.setStatus(fmt.Sprintf("Export failed: %v", err))
					return
				}
				p.app.setStatus(fmt.Sprintf("Exported %d row(s) to %s", n, path))
			})
		})
		p.app.setStatus(fmt.Sprintf("Exporting %d row(s) to %s...", n, path))
	})
}

// exportFileName suggests the export's name: the script's own name, or
// "results", with the set's number when the result has more than one.
func (p *QueryPanel) exportFileName(ext string) string {
	name := "results"
	if p.filePath != "" {
		name = strings.TrimSuffix(filepath.Base(p.filePath), filepath.Ext(p.filePath))
	}
	if p.result != nil && len(p.result.Sets) > 1 {
		name += fmt.Sprintf("-%d", p.activeTab+1)
	}
	return name + "." + ext
}

// writeResultSet writes rows (set's, in the order shown) to path as format f,
// through a temp file: a failed write leaves path as it was.
func writeResultSet(path string, f export.Format, set query.ResultSet, rows export.Table) error {
	st, err := fileutil.CreateStream(path, 0o644)
	if err != nil {
		return err
	}
	if err := export.WriteTable(st, f, set.Columns, set.ColumnTypes, rows); err != nil {
		st.Abort()
		return err
	}
	return st.Commit()
}
