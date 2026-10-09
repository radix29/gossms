package tui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/radix29/gossms/internal/query"
)

// csvSink implements query.RowSink by writing each row to a CSV file as it
// arrives, so an export never holds more than one row in memory. Result sets are
// separated by a blank line, each preceded by its header row.
//
// A SQL NULL is written as an empty field and an empty string as "", so the file
// keeps the two apart, and the string 'NULL' as NULL: the convention of
// PostgreSQL's COPY CSV and most importers. encoding/csv never quotes an empty
// field, hence writeRecord. One limit is the format's: a row of one NULL column is
// an empty line, the same as the separator between sets.
//
// The write path is deliberately dumb: no counting beyond what EndSet is handed,
// no buffering past bufio's own, nothing retained between rows.
type csvSink struct {
	f *os.File
	w *bufio.Writer

	// sets counts result sets begun so far, so the blank-line separator goes between
	// sets and not before the first.
	sets int
}

// newCSVSink creates (or truncates) path and returns a sink writing to it.
func newCSVSink(path string) (*csvSink, error) {
	f, err := os.Create(path)
	if err != nil {
		return nil, err
	}
	return &csvSink{f: f, w: bufio.NewWriter(f)}, nil
}

// writeRecord writes one CSV line: cells comma-separated, a NULL (isNull, which
// may be nil or shorter) as nothing, an empty string as "", and any other field
// quoted exactly where encoding/csv would quote it. bufio's error is sticky, so
// the last write's error is every write's.
func (s *csvSink) writeRecord(cells []string, isNull []bool) error {
	for i, c := range cells {
		if i > 0 {
			s.w.WriteByte(',')
		}
		switch {
		case i < len(isNull) && isNull[i]:
		case c == "":
			s.w.WriteString(`""`)
		case csvNeedsQuotes(c):
			s.w.WriteByte('"')
			s.w.WriteString(strings.ReplaceAll(c, `"`, `""`))
			s.w.WriteByte('"')
		default:
			s.w.WriteString(c)
		}
	}
	return s.w.WriteByte('\n')
}

// csvNeedsQuotes is encoding/csv's rule for a non-empty field with ',' as the
// separator: a quote, the separator or a line break anywhere, a leading space,
// or the field \. (which a PostgreSQL reader takes as end of data).
func csvNeedsQuotes(field string) bool {
	if field == `\.` || strings.ContainsAny(field, "\",\r\n") {
		return true
	}
	r, _ := utf8.DecodeRuneInString(field)
	return unicode.IsSpace(r)
}

// BeginSet writes the separator (for every set after the first) and header.
func (s *csvSink) BeginSet(columns []string) error {
	if s.sets > 0 {
		if err := s.w.WriteByte('\n'); err != nil {
			return err
		}
	}
	s.sets++
	return s.writeRecord(columns, nil)
}

func (s *csvSink) Row(cells []string, isNull []bool) error { return s.writeRecord(cells, isNull) }

// EndSet flushes this set's rows so a long export reaches the disk as it goes
// rather than only at Close.
func (s *csvSink) EndSet(int) error { return s.w.Flush() }

// Close flushes and closes the file. A flush error is preferred over a close
// error since it names the actual failure, but a close error is still reported
// rather than dropped: a disk-full condition is often only visible there.
func (s *csvSink) Close() error {
	err := s.w.Flush()
	if cerr := s.f.Close(); err == nil {
		err = cerr
	}
	return err
}

// promptResultsFile asks where a Results To File run should write, then hands the
// chosen path to run. Cancelling the dialog calls neither.
//
// The prompt comes *before* execution: rows are streamed straight to the file as
// scanned (csvSink), so the destination has to exist by the time the query
// starts. This also matches SSMS, which asks for the filename when you execute in
// Results To File mode.
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
