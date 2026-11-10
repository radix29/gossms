package export

import "io"

// Format is a file format WriteTable writes.
type Format int

const (
	FormatCSV Format = iota
	FormatTSV
	FormatJSON
	FormatXML
)

// Table is one result set's rows: query.ResultSet is one, and so is a sorted
// view of it.
type Table interface {
	Len() int
	Row(i int) []string
	IsNull(row, col int) bool
}

// WriteTable writes t, a result set with the given columns and declared types
// (types matter to JSON only, and may be nil), to w in format f: CSV and TSV
// as RecordWriter writes a set (header row, then the rows), JSON as
// JSONWriter, XML as XMLWriter.
func WriteTable(w io.Writer, f Format, columns, types []string, t Table) error {
	var write func(cells []string, isNull []bool) error
	var finish func() error
	switch f {
	case FormatJSON:
		jw := NewJSONWriter(w, columns, types)
		write, finish = jw.WriteRecord, jw.Close
	case FormatXML:
		xw := NewXMLWriter(w, columns)
		write, finish = xw.WriteRecord, xw.Close
	default:
		sep := rune(CSV)
		if f == FormatTSV {
			sep = TSV
		}
		rw := NewRecordWriter(w, sep)
		if err := rw.BeginSet(columns); err != nil {
			return err
		}
		write, finish = rw.WriteRecord, rw.Flush
	}
	isNull := make([]bool, len(columns))
	for i := range t.Len() {
		for c := range isNull {
			isNull[c] = t.IsNull(i, c)
		}
		if err := write(t.Row(i), isNull); err != nil {
			return err
		}
	}
	return finish()
}
