package tui

import (
	"fmt"

	"github.com/radix29/gossms/internal/export"
	"github.com/radix29/gossms/internal/fileutil"
	"github.com/radix29/gossms/internal/query"
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
