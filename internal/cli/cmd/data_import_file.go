package cmd

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/radix29/gossms/internal/export"
)

// The files data import reads: what data export (or --out-file) writes as
// .csv, .tsv and .jsonl, one result set per file.

// importFormat is an --in-file's format.
type importFormat string

const (
	importCSV   importFormat = "csv"
	importTSV   importFormat = "tsv"
	importJSONL importFormat = "jsonl"
)

// importFormats is --in-format's enum and the extensions that name each.
var importFormats = []string{string(importCSV), string(importTSV), string(importJSONL)}

// importFile is an input file's columns, then its rows.
type importFile interface {
	// Columns is the file's column names, from its header row or its
	// _resultSet line.
	Columns() []string
	// Next is the next row: one cell per column, isNull marking a NULL, and
	// the line the row began on. io.EOF at the end; any other error is an
	// *importError.
	Next() (cells []string, isNull []bool, line int, err error)
}

// importError is a file that cannot be read or a cell that cannot be
// converted: where, and why. Its answer is USAGE_ERROR naming them.
type importError struct {
	line   int
	column string // "" when the fault is the row's, not a cell's
	err    error
}

func (e *importError) Error() string {
	if e.column == "" {
		return fmt.Sprintf("line %d: %v", e.line, e.err)
	}
	return fmt.Sprintf("line %d, column %s: %v", e.line, e.column, e.err)
}

func (e *importError) Unwrap() error { return e.err }

func fileErrorf(line int, format string, args ...any) *importError {
	return &importError{line: line, err: fmt.Errorf(format, args...)}
}

// recordError is an export.RecordReader failure as an *importError.
func recordError(err error) error {
	if pe, ok := errors.AsType[*export.ParseError](err); ok {
		return &importError{line: pe.Line, err: pe.Err}
	}
	return err
}

// utf8BOM is the byte order mark a file saved by Excel or Notepad starts
// with. RecordReader keeps it (a cell may begin with U+FEFF), so the header
// drops it here: it would make the first column's name unknown.
const utf8BOM = "\uFEFF"

// csvImport reads a .csv or .tsv: a header row naming the columns, then the
// rows, with RecordReader's NULL convention (an empty field NULL, "" the
// empty string).
type csvImport struct {
	rr   *export.RecordReader
	cols []string
}

func newCSVImport(r io.Reader, sep rune) (*csvImport, error) {
	rr := export.NewRecordReader(r, sep)
	cells, _, err := rr.Read()
	switch {
	case err == io.EOF:
		return nil, fileErrorf(1, "the file is empty: it needs a header row naming the columns")
	case err != nil:
		return nil, recordError(err)
	case len(cells) == 0:
		return nil, fileErrorf(1, "a blank line where the header row naming the columns belongs")
	}
	cells[0] = strings.TrimPrefix(cells[0], utf8BOM)
	for i, c := range cells {
		if c == "" {
			return nil, fileErrorf(1, "header field %d is empty: every column needs a name", i+1)
		}
	}
	return &csvImport{rr: rr, cols: cells}, nil
}

func (c *csvImport) Columns() []string { return c.cols }

func (c *csvImport) Next() ([]string, []bool, int, error) {
	cells, isNull, err := c.rr.Read()
	if err != nil {
		return nil, nil, 0, recordError(err)
	}
	line := c.rr.Line()
	if len(cells) == 0 {
		// A blank line separates result sets: only blank lines may follow.
		for {
			more, _, err := c.rr.Read()
			if err != nil {
				return nil, nil, 0, recordError(err)
			}
			if len(more) > 0 {
				return nil, nil, 0, fileErrorf(c.rr.Line(),
					"a second result set after the blank line on line %d: import one set per file", line)
			}
		}
	}
	if len(cells) != len(c.cols) {
		return nil, nil, 0, fileErrorf(line, "%d fields, and the header row has %d", len(cells), len(c.cols))
	}
	return cells, isNull, line, nil
}

// jsonlImport reads gossms's jsonl: a _resultSet line naming the columns
// and their keys, then one object per row; a _summary line ends it.
type jsonlImport struct {
	r    *bufio.Reader
	line int
	cols []string // names
	keys []string // the row objects' keys, by column
	done bool
}

func newJSONLImport(r io.Reader) (*jsonlImport, error) {
	j := &jsonlImport{r: bufio.NewReader(r)}
	b, err := j.readLine()
	if err == io.EOF {
		return nil, fileErrorf(1, "the file is empty: it needs a _resultSet line naming the columns")
	}
	if err != nil {
		return nil, err
	}
	var head struct {
		Set *struct {
			Columns []struct {
				Name string `json:"name"`
				Key  string `json:"key"`
			} `json:"columns"`
		} `json:"_resultSet"`
	}
	if json.Unmarshal(b, &head) != nil || head.Set == nil || len(head.Set.Columns) == 0 {
		return nil, fileErrorf(j.line, "not a _resultSet line: data import reads the jsonl gossms writes "+
			"(a _resultSet line naming the columns, then one object per row)")
	}
	for i, c := range head.Set.Columns {
		if c.Name == "" || c.Key == "" {
			return nil, fileErrorf(j.line, "_resultSet column %d has no name or no key", i+1)
		}
		j.cols, j.keys = append(j.cols, c.Name), append(j.keys, c.Key)
	}
	return j, nil
}

func (j *jsonlImport) Columns() []string { return j.cols }

// readLine is the next line that is not blank, without its line break.
func (j *jsonlImport) readLine() ([]byte, error) {
	for {
		b, err := j.r.ReadBytes('\n')
		if err != nil && err != io.EOF {
			return nil, err
		}
		if err == io.EOF && len(b) == 0 {
			return nil, io.EOF
		}
		j.line++
		if b = bytes.TrimRight(b, "\r\n"); len(bytes.TrimSpace(b)) > 0 {
			return b, nil
		}
		if err == io.EOF {
			return nil, io.EOF
		}
	}
}

func (j *jsonlImport) Next() ([]string, []bool, int, error) {
	if j.done {
		return nil, nil, 0, io.EOF
	}
	b, err := j.readLine()
	if err != nil {
		return nil, nil, 0, err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, nil, 0, fileErrorf(j.line, "not a JSON object: %v", err)
	}
	if _, ok := obj["_summary"]; ok {
		// The envelope's summary closes the file; nothing after it is rows.
		j.done = true
		return nil, nil, 0, io.EOF
	}
	if _, ok := obj["_resultSet"]; ok {
		return nil, nil, 0, fileErrorf(j.line, "a second result set: import one set per file")
	}
	if len(obj) != len(j.keys) {
		return nil, nil, 0, fileErrorf(j.line, "%d values, and the _resultSet line names %d columns", len(obj), len(j.keys))
	}
	cells, isNull := make([]string, len(j.keys)), make([]bool, len(j.keys))
	for i, k := range j.keys {
		raw, ok := obj[k]
		if !ok {
			return nil, nil, 0, fileErrorf(j.line, "no value for key %q", k)
		}
		var err error
		if cells[i], isNull[i], err = jsonCellText(raw); err != nil {
			return nil, nil, 0, &importError{line: j.line, column: j.cols[i], err: err}
		}
	}
	return cells, isNull, j.line, nil
}

// jsonCellText is a jsonl cell as the grid's text: null a NULL, a string as
// it is, a number as written (decimal digits are not rounded through a
// float), true and false a bit's 1 and 0.
func jsonCellText(raw json.RawMessage) (text string, null bool, err error) {
	switch raw[0] {
	case 'n':
		return "", true, nil
	case 't':
		return "1", false, nil
	case 'f':
		return "0", false, nil
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", false, err
		}
		return s, false, nil
	case '{', '[':
		return "", false, errors.New("an object or array, not a cell value")
	}
	return string(raw), false, nil
}
