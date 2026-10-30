package export

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// RecordReader reads what RecordWriter writes, keeping the NULL convention
// encoding/csv's Reader cannot see: an empty unquoted field is a NULL, a quoted
// "" is an empty string, and a bare NULL is the string 'NULL'.
//
// It is the writer's exact inverse, with the one loss the format has: a row of
// one NULL column was written as "", so it reads back as an empty string. A
// blank line, which the writer writes only between result sets, reads as a
// record of no fields.
//
// Parsing is strict where the writer is regular — a quote inside an unquoted
// field, text after a closing quote, or a quote left open at the end of the
// input is an error naming the line — and lenient about one thing a file
// edited on Windows brings: a CR before a line's LF is dropped (a CR inside a
// quoted field is kept, and the writer quotes every field holding one). A
// UTF-8 byte order mark is not skipped: a field the writer wrote beginning with
// U+FEFF reads back with it.
type RecordReader struct {
	r   *bufio.Reader
	sep byte

	// line is the number of lines consumed so far; start is the line the
	// last record began on.
	line, start int

	// buf holds the line readLine last returned, and lastBreak the line
	// break it ended with: "\n", "\r\n", or "" for the input's last line.
	buf       []byte
	lastBreak string
}

// NewRecordReader returns a reader of records separated by sep (CSV or TSV)
// from r.
func NewRecordReader(r io.Reader, sep rune) *RecordReader {
	return &RecordReader{r: bufio.NewReader(r), sep: byte(sep)}
}

// Line returns the line the record last read began on, counting from 1: a
// quoted line break makes a record span more than one.
func (rr *RecordReader) Line() int { return rr.start }

// ParseError is a malformed record: Line is the line the error is on, which
// for an unterminated quote is the record's first.
type ParseError struct {
	Line int
	Err  error
}

func (e *ParseError) Error() string { return fmt.Sprintf("line %d: %v", e.Line, e.Err) }
func (e *ParseError) Unwrap() error { return e.Err }

// The ways a record can be malformed.
var (
	ErrBareQuote      = errors.New(`a quote (") in an unquoted field`)
	ErrTextAfterQuote = errors.New("text after a quoted field's closing quote")
	ErrOpenQuote      = errors.New("a quoted field with no closing quote")
)

// Read returns the next record: its cells, and isNull marking the NULL fields
// (their cells are ""). A blank line is a record of no fields. At the end of
// the input it returns io.EOF; a last line without a line break is still a
// record.
func (rr *RecordReader) Read() (cells []string, isNull []bool, err error) {
	line, err := rr.readLine()
	if err != nil {
		return nil, nil, err
	}
	rr.start = rr.line
	if len(line) == 0 {
		return []string{}, []bool{}, nil
	}
	for {
		var cell []byte
		null := false
		if len(line) > 0 && line[0] == '"' {
			cell, line, err = rr.quoted(line[1:])
			if err != nil {
				return nil, nil, err
			}
		} else {
			end := bytes.IndexByte(line, rr.sep)
			if end < 0 {
				end = len(line)
			}
			cell = line[:end]
			if bytes.IndexByte(cell, '"') >= 0 {
				return nil, nil, &ParseError{rr.line, ErrBareQuote}
			}
			null = len(cell) == 0
			line = line[end:]
		}
		cells = append(cells, string(cell))
		isNull = append(isNull, null)
		if len(line) == 0 {
			return cells, isNull, nil
		}
		// line[0] is the separator: quoted and the unquoted cut both stop
		// only there or at the end. A separator ending the line leaves an
		// empty line, the empty unquoted field (a NULL) after it.
		line = line[1:]
	}
}

// quoted reads a quoted field whose opening quote is already consumed from
// line, pulling further lines while the field runs past a line break. It
// returns the field's text and the rest of the record after the closing
// quote, which is empty or starts with the separator.
func (rr *RecordReader) quoted(line []byte) (cell, rest []byte, err error) {
	for {
		i := bytes.IndexByte(line, '"')
		if i < 0 {
			// The field holds the line break: keep it as written (readLine
			// dropped it, and a CR before it only outside quotes).
			cell = append(cell, line...)
			cell = append(cell, rr.lastBreak...)
			if line, err = rr.readLine(); err != nil {
				if err == io.EOF {
					return nil, nil, &ParseError{rr.start, ErrOpenQuote}
				}
				return nil, nil, err
			}
			continue
		}
		cell = append(cell, line[:i]...)
		line = line[i+1:]
		if len(line) > 0 && line[0] == '"' {
			cell = append(cell, '"')
			line = line[1:]
			continue
		}
		if len(line) > 0 && line[0] != rr.sep {
			return nil, nil, &ParseError{rr.line, ErrTextAfterQuote}
		}
		return cell, line, nil
	}
}

// readLine returns the next line without its line break, which it records in
// lastBreak; io.EOF once the input is used up. The line is valid until the
// next call.
func (rr *RecordReader) readLine() ([]byte, error) {
	rr.buf = rr.buf[:0]
	for {
		chunk, err := rr.r.ReadSlice('\n')
		rr.buf = append(rr.buf, chunk...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil && err != io.EOF {
			return nil, err
		}
		if err == io.EOF && len(rr.buf) == 0 {
			return nil, io.EOF
		}
		break
	}
	rr.line++
	rr.lastBreak = ""
	if n := len(rr.buf); n > 0 && rr.buf[n-1] == '\n' {
		rr.buf, rr.lastBreak = rr.buf[:n-1], "\n"
		if n := len(rr.buf); n > 0 && rr.buf[n-1] == '\r' {
			rr.buf, rr.lastBreak = rr.buf[:n-1], "\r\n"
		}
	}
	return rr.buf, nil
}
