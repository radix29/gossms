package tui

import (
	"encoding/csv"
	"fmt"
	"os"

	"github.com/radix29/gossms/internal/query"
)

// csvSink implements query.RowSink by writing each row to a CSV file as it
// arrives, so an export never holds more than one row in memory. Result sets
// are separated by a blank line, each preceded by its header row — the same
// layout the previous buffer-everything writer produced.
//
// The write path is deliberately dumb: no counting beyond what EndSet is
// handed, no buffering past csv.Writer's own, nothing retained between rows.
type csvSink struct {
	f *os.File
	w *csv.Writer

	// sets counts result sets begun so far, so the blank-line separator goes
	// between sets and not before the first.
	sets int
}

// newCSVSink creates (or truncates) path and returns a sink writing to it.
func newCSVSink(path string) (*csvSink, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &csvSink{f: f, w: csv.NewWriter(f)}, nil
}

// BeginSet writes the separator (for every set after the first) and header.
func (s *csvSink) BeginSet(columns []string) error {
	if s.sets > 0 {
		s.w.Flush()
		if err := s.w.Error(); err != nil {
			return err
		}
		if _, err := s.f.WriteString("\n"); err != nil {
			return err
		}
	}
	s.sets++
	return s.w.Write(columns)
}

func (s *csvSink) Row(cells []string) error { return s.w.Write(cells) }

// EndSet flushes this set's rows so a long export reaches the disk as it
// goes rather than only at Close.
func (s *csvSink) EndSet(int) error {
	s.w.Flush()
	return s.w.Error()
}

// Close flushes and closes the file. A flush error is preferred over a close
// error since it names the actual failure, but a close error is still
// reported rather than dropped — a disk-full condition is often only visible
// there.
func (s *csvSink) Close() error {
	s.w.Flush()
	err := s.w.Error()
	if cerr := s.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// promptResultsFile asks where a Results To File run should write, then hands
// the chosen path to run. Cancelling the dialog calls neither.
//
// The prompt comes *before* execution, not after: the rows are streamed
// straight to the file as they are scanned (see csvSink), so the destination
// has to exist by the time the query starts. This also matches SSMS, which
// asks for the filename when you execute in Results To File mode.
func (p *QueryPanel) promptResultsFile(run func(path string)) {
	p.app.fileDialog.ShowSave("Results To File", "results.csv", run)
}

// reportExport appends the outcome of a streamed export to res so it shows up
// in the Messages tab, and mirrors it to the status bar.
func (p *QueryPanel) reportExport(res *query.Result, path string, rows int, err error) {
	msg := query.Message{Text: fmt.Sprintf("%d row(s) written to %s", rows, path)}
	if err != nil {
		msg = query.Message{Text: fmt.Sprintf("write results to %s: %v", path, err), IsError: true}
	}
	res.Messages = append(res.Messages, msg)
	if p.result == res {
		p.renderActiveTab()
	}
	p.app.setStatus(msg.Text)
}
