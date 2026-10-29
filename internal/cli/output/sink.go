package output

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/radix29/gossms/internal/fileutil"
)

// Sink receives a command's result sets row by row and decides, per the
// output options, where each row goes:
//
//   - stdout, buffered (text, json, markdown, csv, tsv): the first MaxRows rows
//     of each set are kept for Render, the rest counted and dropped, so the
//     total is exact while memory stays bounded (the executor runs uncapped:
//     a later statement still runs);
//   - stdout, jsonl: each row is written as its line at once, nothing is kept;
//   - --out-file: every row goes to the file, unclipped, and stdout gets the
//     summary.
//
// Its methods are internal/query's TypedRowSink; output imports no query
// package, so it satisfies the interface by shape. Main makes the Sink before
// the handler runs (an unusable --out-file fails before connecting), passes
// it on the Invocation, adds the sets a handler built itself (Add), and calls
// Finish once the outcome is known.
type Sink struct {
	o     Options
	sets  []Set
	jsonl *jsonlWriter // stdout jsonl
	file  *outFile
	cells []*string // Row's scratch, valid during one call
	err   error     // the first stdout write error
}

// outFile is --out-file: a temp file beside the target, renamed over it at
// Finish (fileutil.Stream), so a failed or cancelled run leaves the
// destination as it was.
type outFile struct {
	path   string
	format Format
	st     *fileutil.Stream
	rec    *recordWriter // csv, tsv
	jsonl  *jsonlWriter  // jsonl
	sets   []Set         // json, markdown, text: rendered whole at Finish
	err    error
}

// outFileFormats maps an --out-file extension to its format.
var outFileFormats = map[string]Format{
	".csv": FormatCSV, ".tsv": FormatTSV, ".jsonl": FormatJSONL,
	".json": FormatJSON, ".md": FormatMarkdown, ".txt": FormatText,
}

// NewSink is the Sink for o, writing streamed rows to stdout. A bad --out-file
// — an unknown extension, an existing file without --force, a destination
// that cannot be written — is a USAGE_ERROR *Error, before anything runs.
func NewSink(stdout io.Writer, o Options) (*Sink, error) {
	s := &Sink{o: o}
	if o.OutFile != "" {
		f, err := openOutFile(o)
		if err != nil {
			return nil, err
		}
		s.file = f
	} else if o.Format == FormatJSONL {
		s.jsonl = newJSONLWriter(stdout, o.MaxColWidth, o.MaxOutputBytes)
	}
	return s, nil
}

func openOutFile(o Options) (*outFile, error) {
	path := o.OutFile
	format, ok := outFileFormats[strings.ToLower(filepath.Ext(path))]
	if !ok {
		return nil, NewError(CodeUsageError, fmt.Sprintf("--out-file %s: the extension names no format", path),
			"Use .csv, .tsv, .jsonl, .json, .md or .txt.")
	}
	if fi, err := os.Stat(path); err == nil {
		if fi.IsDir() {
			return nil, NewError(CodeUsageError, fmt.Sprintf("--out-file %s: is a directory", path), "")
		}
		if !o.Force {
			return nil, NewError(CodeUsageError, fmt.Sprintf("--out-file %s: the file exists", path),
				"Add --force to replace it, or choose another path.")
		}
	}
	st, err := fileutil.CreateStream(path, 0o644)
	if err != nil {
		return nil, NewError(CodeUsageError, fmt.Sprintf("--out-file %s: %v", path, err), "")
	}
	f := &outFile{path: path, format: format, st: st}
	switch format {
	case FormatCSV, FormatTSV:
		f.rec = newRecordWriter(st, format, o.NoHeaders)
	case FormatJSONL:
		f.jsonl = newJSONLWriter(st, 0, 0)
	}
	return f, nil
}

// BeginSet starts a set whose column types are unknown: every column reads
// nullable, the safe side.
func (s *Sink) BeginSet(columns []string) error {
	nullable := make([]bool, len(columns))
	for i := range nullable {
		nullable[i] = true
	}
	return s.BeginTypedSet(columns, nil, nullable)
}

// BeginTypedSet starts a set. types and nullable parallel columns; either may
// be nil.
func (s *Sink) BeginTypedSet(columns, types []string, nullable []bool) error {
	cols := make([]Column, len(columns))
	for i, name := range columns {
		cols[i] = Column{Name: name, Nullable: true}
		if i < len(types) {
			cols[i].Type = types[i]
		}
		if i < len(nullable) {
			cols[i].Nullable = nullable[i]
		}
	}
	return s.begin("", cols)
}

// begin starts a set named name (empty for a script's).
func (s *Sink) begin(name string, cols []Column) error {
	s.sets = append(s.sets, Set{Name: name, Columns: cols, InFile: s.file != nil})
	if f := s.file; f != nil {
		switch {
		case f.rec != nil:
			f.fail(f.rec.beginSet(cols))
		case f.jsonl != nil:
			f.jsonl.beginSet(name, cols)
			f.fail(f.jsonl.writeErr)
		default:
			f.sets = append(f.sets, Set{Name: name, Columns: cols})
		}
		return f.err
	}
	if s.jsonl != nil && !s.jsonl.beginSet(name, cols) {
		// Over --max-output-bytes already: the set's rows are all dropped.
		s.cur().Truncated = true
		s.cur().TruncatedBy = TruncatedByMaxOutputBytes
	}
	return s.stdoutErr()
}

func (s *Sink) cur() *Set { return &s.sets[len(s.sets)-1] }

// Row takes one row. cells and isNull are the executor's, reused for the next
// row: a row kept is copied.
func (s *Sink) Row(cells []string, isNull []bool) error {
	set := s.cur()
	set.TotalRowCount++
	s.cells = s.cells[:0]
	for i := range cells {
		if i < len(isNull) && isNull[i] {
			s.cells = append(s.cells, nil)
		} else {
			s.cells = append(s.cells, &cells[i])
		}
	}
	return s.row(set, s.cells)
}

// row routes one row; its cell pointers are valid during the call only.
func (s *Sink) row(set *Set, row []*string) error {
	if f := s.file; f != nil {
		switch {
		case f.rec != nil:
			f.fail(f.rec.row(row, len(set.Columns)))
		case f.jsonl != nil:
			f.jsonl.row(row)
			f.fail(f.jsonl.writeErr)
		default:
			fs := &f.sets[len(f.sets)-1]
			fs.Rows = append(fs.Rows, copyRow(row))
			fs.TotalRowCount++
		}
		return f.err
	}
	switch {
	case set.Truncated:
		// Counted only: a limit already cut this set.
	case s.o.MaxRows > 0 && len(set.Rows)+set.streamed >= s.o.MaxRows:
		markTruncated(set, TruncatedByMaxRows)
	case s.jsonl != nil:
		if s.jsonl.row(row) {
			set.streamed++
		} else {
			markTruncated(set, TruncatedByMaxOutputBytes)
		}
	default:
		set.Rows = append(set.Rows, copyRow(row))
	}
	return s.stdoutErr()
}

// EndSet ends the current set; the Sink counted its rows itself.
func (s *Sink) EndSet(int) error { return nil }

func copyRow(row []*string) []*string {
	out := make([]*string, len(row))
	for i, c := range row {
		if c != nil {
			v := *c
			out[i] = &v
		}
	}
	return out
}

func (f *outFile) fail(err error) {
	if err != nil && f.err == nil {
		f.err = err
	}
}

func (s *Sink) stdoutErr() error {
	if s.jsonl != nil && s.jsonl.writeErr != nil && s.err == nil {
		s.err = s.jsonl.writeErr
	}
	return s.err
}

// Add takes sets a handler built itself, as if they had been streamed: the
// same limits and destinations apply. A set's RowsAffected is kept.
func (s *Sink) Add(sets []Set) {
	for _, in := range sets {
		if s.begin(in.Name, in.Columns) != nil {
			return
		}
		for _, row := range in.Rows {
			set := s.cur()
			set.TotalRowCount++
			if s.row(set, row) != nil {
				return
			}
		}
		s.cur().RowsAffected = in.RowsAffected
	}
}

// Finish ends the run: r's sets become the Sink's (the rows kept for stdout,
// or none when they were streamed or went to the file). failed — an error or a
// cancel — discards --out-file and leaves the destination unchanged, which a
// warning says; otherwise the file is completed and renamed into place, and
// r.OutFile set. The error is a stdout or file write failure, as a
// GENERAL_ERROR *Error.
func (s *Sink) Finish(r *Result, failed bool) error {
	r.Sets = s.sets
	if s.jsonl != nil {
		r.streamed = true
	}
	var errs []error
	if s.err != nil {
		errs = append(errs, NewError(CodeGeneralError, "writing output: "+s.err.Error(), ""))
	}
	if f := s.file; f != nil {
		switch {
		case failed:
			f.st.Abort()
			r.Warnings = append(r.Warnings, fmt.Sprintf("--out-file %s was not written: the command did not succeed, "+
				"so the file is unchanged.", f.path))
		default:
			if err := f.finish(r, s.o); err != nil {
				f.st.Abort()
				errs = append(errs, NewError(CodeGeneralError, fmt.Sprintf("writing --out-file %s: %v", f.path, err),
					"The destination is unchanged."))
				break
			}
			r.OutFile = f.path
		}
	}
	return errors.Join(errs...)
}

// finish completes the file in its format and commits it. A json, markdown or
// text file holds the whole envelope, its rows and cells unlimited, as it
// stands when the command succeeded; --null-text, --no-headers and
// --no-messages apply as on stdout.
func (f *outFile) finish(r *Result, o Options) error {
	if f.err != nil {
		return f.err
	}
	all := Options{Format: f.format, NullText: o.NullText, NoHeaders: o.NoHeaders, NoMessages: o.NoMessages}
	switch {
	case f.rec != nil:
		if err := f.rec.flush(); err != nil {
			return err
		}
	case f.jsonl != nil:
		c := *r
		c.Status, c.Sets = StatusOK, make([]Set, len(r.Sets))
		shown := make([]int, len(r.Sets))
		for i, s := range r.Sets {
			s.InFile = false
			c.Sets[i], shown[i] = s, s.TotalRowCount
		}
		if err := f.jsonl.summary(&c, shown); err != nil {
			return err
		}
	default:
		c := *r
		c.Status, c.Sets = StatusOK, f.sets
		for i := range c.Sets {
			c.Sets[i].RowsAffected = r.Sets[i].RowsAffected
		}
		if o.NoMessages {
			c.Messages = nil
		}
		if err := render(f.st, &c, all); err != nil {
			return err
		}
	}
	return f.st.Commit()
}
