package output

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
)

// The keys of jsonl's two non-row lines. A column whose key would equal one is
// renamed, so a row can never pass for either.
const (
	jsonlSetKey     = "_resultSet"
	jsonlSummaryKey = "_summary"
)

// jsonlWriter writes the jsonl format: for each result set a
// {"_resultSet": {...}} line naming its columns, then one JSON object per row
// keyed by column, and last a {"_summary": {...}} line holding the envelope
// without the rows. Each line is written whole as it is made, so a reader can
// act on rows while the query runs.
//
// MaxOutputBytes (0: none) is checked before each line: the first line that
// would pass it is not written, and neither is any after it but the summary,
// which is exempt — a reader always learns how the command ended.
type jsonlWriter struct {
	w        io.Writer
	width    int   // MaxColWidth
	limit    int64 // MaxOutputBytes
	written  int64
	full     bool // the limit was reached
	sets     int
	keys     [][]byte // the current set's keys, JSON-encoded
	kinds    []cellKind
	line     bytes.Buffer
	scratch  bytes.Buffer
	encoder  *json.Encoder
	writeErr error
}

func newJSONLWriter(w io.Writer, width int, limit int64) *jsonlWriter {
	j := &jsonlWriter{w: w, width: width, limit: limit}
	j.encoder = json.NewEncoder(&j.scratch)
	j.encoder.SetEscapeHTML(false)
	return j
}

// jsonlColumn is a column in a _resultSet line: jsonColumn plus the key its
// cells have in the row objects.
type jsonlColumn struct {
	Name     string  `json:"name"`
	Key      string  `json:"key"`
	Type     *string `json:"type"`
	Nullable bool    `json:"nullable"`
}

// rowKeys is each column's key in a row object: its name, "column<n>" for a
// nameless one, and name_<n> (n its 1-based position, repeated until unique)
// for a duplicate or a name that would read as a _resultSet or _summary line.
func rowKeys(cols []Column) []string {
	used := map[string]bool{jsonlSetKey: true, jsonlSummaryKey: true}
	keys := make([]string, len(cols))
	for i, c := range cols {
		k := c.Name
		if k == "" {
			k = "column" + strconv.Itoa(i+1)
		}
		for used[k] {
			k += "_" + strconv.Itoa(i+1)
		}
		used[k] = true
		keys[i] = k
	}
	return keys
}

// beginSet writes a set's _resultSet line. It reports whether the line was
// written (false once the limit is reached).
func (j *jsonlWriter) beginSet(cols []Column) bool {
	j.sets++
	keys := rowKeys(cols)
	j.kinds = kinds(cols)
	j.keys = j.keys[:0]
	jc := make([]jsonlColumn, len(cols))
	for i, c := range cols {
		jc[i] = jsonlColumn{Name: c.Name, Key: keys[i], Type: nullable(c.Type), Nullable: c.Nullable}
		j.keys = append(j.keys, bytes.Clone(j.encode(keys[i])))
	}
	head := struct {
		Set struct {
			Index   int           `json:"index"`
			Columns []jsonlColumn `json:"columns"`
		} `json:"_resultSet"`
	}{}
	head.Set.Index, head.Set.Columns = j.sets, jc
	j.line.Reset()
	j.line.Write(j.encode(head))
	return j.emit()
}

// row writes one row object. It reports whether the row was written (false
// once the limit is reached).
func (j *jsonlWriter) row(row []*string) bool {
	if j.full {
		return false
	}
	j.line.Reset()
	j.line.WriteByte('{')
	for i, k := range j.keys {
		if i > 0 {
			j.line.WriteByte(',')
		}
		j.line.Write(k)
		j.line.WriteByte(':')
		var c *string
		if i < len(row) {
			c = row[i]
		}
		j.line.Write(j.encode(jsonCell(c, j.kinds[i], j.width)))
	}
	j.line.WriteByte('}')
	return j.emit()
}

// emit writes j.line and its newline, unless that would pass the limit.
func (j *jsonlWriter) emit() bool {
	if j.full {
		return false
	}
	j.line.WriteByte('\n')
	if j.limit > 0 && j.written+int64(j.line.Len()) > j.limit {
		j.full = true
		return false
	}
	n, err := j.w.Write(j.line.Bytes())
	j.written += int64(n)
	if err != nil && j.writeErr == nil {
		j.writeErr = err
	}
	return true
}

// summary writes the _summary line: r's envelope, each set without its rows
// and with rowCount the rows written as lines (shown).
func (j *jsonlWriter) summary(r *Result, shown []int) error {
	env := newEnvelope(r)
	for i, s := range r.Sets {
		env.ResultSets = append(env.ResultSets, jsonSetSummary{
			Columns:       jsonColumns(s.Columns),
			RowCount:      shown[i],
			TotalRowCount: s.TotalRowCount,
			Truncated:     s.Truncated,
			TruncatedBy:   nullable(s.TruncatedBy),
			RowsAffected:  s.RowsAffected,
		})
	}
	line := struct {
		Summary jsonEnvelope `json:"_summary"`
	}{env}
	if err := encodeJSON(j.w, line, ""); err != nil {
		return err
	}
	return j.writeErr
}

// encode is v as compact JSON, without HTML escaping; the bytes are valid
// until the next call.
func (j *jsonlWriter) encode(v any) []byte {
	j.scratch.Reset()
	if err := j.encoder.Encode(v); err != nil {
		// Only a value encoding/json cannot represent fails, and every value
		// here is a string, number, bool, null or a struct of them.
		panic(err)
	}
	return bytes.TrimSuffix(j.scratch.Bytes(), []byte{'\n'})
}

// renderJSONL writes r as jsonl: the sets' lines (unless the Sink already
// streamed them), then the summary. A set the limit cut is marked truncated by
// max-output-bytes, and its rowCount is the rows written.
func renderJSONL(w io.Writer, r *Result, o Options) error {
	shown := make([]int, len(r.Sets))
	if r.streamed {
		for i, s := range r.Sets {
			shown[i] = s.streamed
		}
		return newJSONLWriter(w, o.MaxColWidth, 0).summary(r, shown)
	}
	j := newJSONLWriter(w, o.MaxColWidth, o.MaxOutputBytes)
	for i := range r.Sets {
		s := &r.Sets[i]
		if s.InFile {
			continue
		}
		if !j.beginSet(s.Columns) && len(s.Rows) > 0 {
			markTruncated(s, TruncatedByMaxOutputBytes)
			continue
		}
		for _, row := range s.Rows {
			if !j.row(row) {
				markTruncated(s, TruncatedByMaxOutputBytes)
				break
			}
			shown[i]++
		}
	}
	return j.summary(r, shown)
}

// markTruncated marks s cut by limit: the last limit that removed rows, which
// is the one to raise.
func markTruncated(s *Set, limit string) {
	s.Truncated, s.TruncatedBy = true, limit
}
