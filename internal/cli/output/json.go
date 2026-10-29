package output

import (
	"bytes"
	"encoding/json"
	"io"
)

// jsonEnvelope is the JSON format. Struct field order is output order, and
// every key but "data" is always present (null or [] when empty) so a parser
// can rely on the shape; "data" appears only for a record answer.
type jsonEnvelope struct {
	SchemaVersion string        `json:"schemaVersion"`
	Status        Status        `json:"status"`
	Command       string        `json:"command"`
	Server        *string       `json:"server"`
	Database      *string       `json:"database"`
	DurationMs    int64         `json:"durationMs"`
	RowsAffected  *int64        `json:"rowsAffected"`
	OutFile       *string       `json:"outFile"`
	Data          jsonFields    `json:"data,omitempty"`
	ResultSets    []any         `json:"resultSets"` // jsonSet, or jsonSetSummary in jsonl's summary
	Messages      []jsonMessage `json:"messages"`
	Warnings      []string      `json:"warnings"`
	Errors        []any         `json:"errors"` // jsonError or jsonSQLError
}

type jsonSet struct {
	Name          *string      `json:"name"`
	Columns       []jsonColumn `json:"columns"`
	Rows          [][]any      `json:"rows"`
	RowCount      int          `json:"rowCount"`
	TotalRowCount int          `json:"totalRowCount"`
	Truncated     bool         `json:"truncated"`
	TruncatedBy   *string      `json:"truncatedBy"`
	RowsAffected  *int64       `json:"rowsAffected"`
}

// jsonSetSummary is a set in jsonl's _summary line: everything but the rows,
// which are the lines before it.
type jsonSetSummary struct {
	Name          *string      `json:"name"`
	Columns       []jsonColumn `json:"columns"`
	RowCount      int          `json:"rowCount"`
	TotalRowCount int          `json:"totalRowCount"`
	Truncated     bool         `json:"truncated"`
	TruncatedBy   *string      `json:"truncatedBy"`
	RowsAffected  *int64       `json:"rowsAffected"`
}

type jsonColumn struct {
	Name     string  `json:"name"`
	Type     *string `json:"type"`
	Nullable bool    `json:"nullable"`
}

type jsonMessage struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type jsonError struct {
	Code    Code    `json:"code"`
	Message string  `json:"message"`
	Hint    *string `json:"hint"`
}

// jsonSQLError is an error with a SQL Server error behind it: jsonError's
// keys plus the server's fields, in the order docs/cli.md § Errors shows.
type jsonSQLError struct {
	Code           Code    `json:"code"`
	SQLErrorNumber int32   `json:"sqlErrorNumber"`
	Severity       uint8   `json:"severity"`
	State          uint8   `json:"state"`
	Line           int     `json:"line"`
	Procedure      *string `json:"procedure"`
	Message        string  `json:"message"`
	Hint           *string `json:"hint"`
}

// jsonFields is Result.Fields as one JSON object, keys in Fields order
// (encoding/json would sort a map's).
type jsonFields []Field

func (f jsonFields) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, fl := range f {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := json.Marshal(fl.Key)
		if err != nil {
			return nil, err
		}
		v := []byte("null")
		if !fl.Null {
			if v, err = json.Marshal(fl.Value); err != nil {
				return nil, err
			}
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// renderJSON writes r as one indented JSON object and a trailing newline.
func renderJSON(w io.Writer, r *Result, o Options) error {
	env := newEnvelope(r)
	for _, s := range r.Sets {
		js := jsonSet{
			Name:          nullable(s.Name),
			Columns:       jsonColumns(s.Columns),
			Rows:          [][]any{},
			RowCount:      len(s.Rows),
			TotalRowCount: s.TotalRowCount,
			Truncated:     s.Truncated,
			TruncatedBy:   nullable(s.TruncatedBy),
			RowsAffected:  s.RowsAffected,
		}
		k := kinds(s.Columns)
		for _, row := range s.Rows {
			js.Rows = append(js.Rows, jsonRow(row, k, o.MaxColWidth))
		}
		env.ResultSets = append(env.ResultSets, js)
	}
	return encodeJSON(w, env, "  ")
}

// newEnvelope is r's envelope without its result sets.
func newEnvelope(r *Result) jsonEnvelope {
	env := jsonEnvelope{
		SchemaVersion: SchemaVersion,
		Status:        r.Status,
		Command:       r.Command,
		Server:        nullable(r.Server),
		Database:      nullable(r.Database),
		DurationMs:    r.Duration.Milliseconds(),
		RowsAffected:  r.RowsAffected,
		OutFile:       nullable(r.OutFile),
		Data:          jsonFields(r.Fields),
		ResultSets:    []any{},
		Messages:      []jsonMessage{},
		Warnings:      r.Warnings,
		Errors:        []any{},
	}
	if env.Warnings == nil {
		env.Warnings = []string{}
	}
	for _, m := range r.Messages {
		env.Messages = append(env.Messages, jsonMessage(m))
	}
	for _, e := range r.Errors {
		if q := e.SQL; q != nil {
			env.Errors = append(env.Errors, jsonSQLError{Code: e.Code, SQLErrorNumber: q.Number, Severity: q.Severity,
				State: q.State, Line: q.Line, Procedure: nullable(q.Procedure), Message: e.Message, Hint: nullable(e.Hint)})
			continue
		}
		env.Errors = append(env.Errors, jsonError{Code: e.Code, Message: e.Message, Hint: nullable(e.Hint)})
	}
	return env
}

func jsonColumns(cols []Column) []jsonColumn {
	out := []jsonColumn{}
	for _, c := range cols {
		out = append(out, jsonColumn{Name: c.Name, Type: nullable(c.Type), Nullable: c.Nullable})
	}
	return out
}

// jsonRow is one row's cells as JSON values; a short row is padded with
// nulls, so every row has one value per column.
func jsonRow(row []*string, k []cellKind, width int) []any {
	out := make([]any, len(k))
	for i := range k {
		if i < len(row) {
			out[i] = jsonCell(row[i], k[i], width)
		}
	}
	return out
}

// encodeJSON writes v and a newline, indented by indent ("" for one line).
// HTML escaping is off: SQL text is full of <, > and &, and \u003c helps no
// reader.
func encodeJSON(w io.Writer, v any, indent string) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	return enc.Encode(v)
}

// nullable is nil for "", so JSON says null rather than an empty string that
// could be a value.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
