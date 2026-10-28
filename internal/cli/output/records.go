package output

import (
	"io"

	"github.com/radix29/gossms/internal/export"
)

// recordWriter is csv or tsv over export.RecordWriter, the writer the TUI's
// Results To File uses: NULL an empty field, the empty string "", sets
// separated by a blank line (docs/decisions.md § SQL NULL in results). Rows
// only — no envelope, no cell clipping: a delimited file is an export, and
// exports are lossless.
type recordWriter struct {
	rw        *export.RecordWriter
	noHeaders bool
	cells     []string
	isNull    []bool
}

func newRecordWriter(w io.Writer, f Format, noHeaders bool) *recordWriter {
	sep := rune(export.CSV)
	if f == FormatTSV {
		sep = export.TSV
	}
	return &recordWriter{rw: export.NewRecordWriter(w, sep), noHeaders: noHeaders}
}

func (w *recordWriter) beginSet(cols []Column) error {
	if w.noHeaders {
		return w.rw.BeginHeaderlessSet()
	}
	names := make([]string, len(cols))
	for i, c := range cols {
		names[i] = c.Name
	}
	return w.rw.BeginSet(names)
}

// row writes cells (nil for NULL), padded to ncols.
func (w *recordWriter) row(row []*string, ncols int) error {
	w.cells, w.isNull = w.cells[:0], w.isNull[:0]
	for i := range ncols {
		var c *string
		if i < len(row) {
			c = row[i]
		}
		if c == nil {
			w.cells, w.isNull = append(w.cells, ""), append(w.isNull, true)
			continue
		}
		w.cells, w.isNull = append(w.cells, *c), append(w.isNull, false)
	}
	return w.rw.WriteRecord(w.cells, w.isNull)
}

func (w *recordWriter) flush() error { return w.rw.Flush() }

// renderRecords writes r's sets as csv or tsv.
func renderRecords(w io.Writer, r *Result, o Options) error {
	rw := newRecordWriter(w, o.Format, o.NoHeaders)
	for _, s := range r.Sets {
		if s.InFile {
			continue
		}
		if err := rw.beginSet(s.Columns); err != nil {
			return err
		}
		for _, row := range s.Rows {
			if err := rw.row(row, len(s.Columns)); err != nil {
				return err
			}
		}
	}
	return rw.flush()
}
