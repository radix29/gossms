package export

import (
	"bufio"
	"io"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Separators the RecordWriter supports.
const (
	CSV = ','
	TSV = '\t'
)

// RecordWriter writes result sets as delimited records: each set's header row
// then its rows, sets separated by a blank line.
//
// A SQL NULL is written as an empty field and an empty string as "", so the file
// keeps the two apart, and the string 'NULL' as NULL: the convention of
// PostgreSQL's COPY CSV and most importers. encoding/csv never quotes an empty
// field, hence WriteRecord. One limit is the format's: a row of one NULL column is
// written as "" too, since an empty line is skipped by readers and is the separator
// between sets, so there a NULL and an empty string are the same.
//
// TSV follows the same rules with a tab for the comma (docs/decisions.md): a
// field holding a tab, a quote or a line break is quoted, which is what
// spreadsheets read, rather than PostgreSQL TEXT's \t escapes.
//
// The write path is deliberately dumb: no counting, no buffering past bufio's
// own, nothing retained between rows. Writes are buffered until Flush.
type RecordWriter struct {
	w   *bufio.Writer
	sep rune

	// sets counts result sets begun so far, so the blank-line separator goes between
	// sets and not before the first.
	sets int
}

// NewRecordWriter returns a writer of records separated by sep (CSV or TSV)
// onto w.
func NewRecordWriter(w io.Writer, sep rune) *RecordWriter {
	return &RecordWriter{w: bufio.NewWriter(w), sep: sep}
}

// WriteRecord writes one line: cells separated by the separator, a NULL (isNull,
// which may be nil or shorter) as nothing, an empty string as "", and any other
// field quoted exactly where encoding/csv would quote it. bufio's error is
// sticky, so the last write's error is every write's.
func (rw *RecordWriter) WriteRecord(cells []string, isNull []bool) error {
	// A lone NULL would be a bare "\n", which readers (encoding/csv's included) skip
	// as a blank line and which is also the separator between sets. Write it as ""
	// the way encoding/csv quotes a lone empty field: for this one shape NULL and an
	// empty string are the same on disk.
	if len(cells) == 1 && len(isNull) > 0 && isNull[0] {
		rw.w.WriteString(`""`)
		return rw.w.WriteByte('\n')
	}
	for i, c := range cells {
		if i > 0 {
			rw.w.WriteRune(rw.sep)
		}
		switch {
		case i < len(isNull) && isNull[i]:
		case c == "":
			rw.w.WriteString(`""`)
		case rw.needsQuotes(c):
			rw.w.WriteByte('"')
			rw.w.WriteString(strings.ReplaceAll(c, `"`, `""`))
			rw.w.WriteByte('"')
		default:
			rw.w.WriteString(c)
		}
	}
	return rw.w.WriteByte('\n')
}

// needsQuotes is encoding/csv's rule for a non-empty field: a quote, the
// separator or a line break anywhere, a leading space, or the field \. (which a
// PostgreSQL reader takes as end of data).
func (rw *RecordWriter) needsQuotes(field string) bool {
	if field == `\.` || strings.ContainsAny(field, "\"\r\n") || strings.ContainsRune(field, rw.sep) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(field)
	return unicode.IsSpace(r)
}

// BeginSet writes the separator (for every set after the first) and header.
func (rw *RecordWriter) BeginSet(columns []string) error {
	if err := rw.BeginHeaderlessSet(); err != nil {
		return err
	}
	return rw.WriteRecord(columns, nil)
}

// BeginHeaderlessSet is BeginSet without the header row: the separator only,
// for every set after the first.
func (rw *RecordWriter) BeginHeaderlessSet() error {
	rw.sets++
	if rw.sets > 1 {
		return rw.w.WriteByte('\n')
	}
	return nil
}

// Flush writes any buffered records to the underlying writer.
func (rw *RecordWriter) Flush() error { return rw.w.Flush() }
