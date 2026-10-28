package output

import (
	"bytes"
	"encoding/json"
	"io"
)

// jsonEnvelope is the JSON format. Struct field order is output order, and
// every key but "data" is always present (null or [] when empty) so a parser
// can rely on the shape; "data" appears only for a record answer, in place of
// rows in "resultSets".
type jsonEnvelope struct {
	SchemaVersion string        `json:"schemaVersion"`
	Status        Status        `json:"status"`
	Command       string        `json:"command"`
	Server        *string       `json:"server"`
	Database      *string       `json:"database"`
	DurationMs    int64         `json:"durationMs"`
	Data          jsonFields    `json:"data,omitempty"`
	ResultSets    []jsonSet     `json:"resultSets"`
	Messages      []jsonMessage `json:"messages"`
	Warnings      []string      `json:"warnings"`
	Errors        []jsonError   `json:"errors"`
}

type jsonSet struct {
	Columns       []jsonColumn `json:"columns"`
	Rows          [][]*string  `json:"rows"`
	RowCount      int          `json:"rowCount"`
	TotalRowCount int          `json:"totalRowCount"`
	Truncated     bool         `json:"truncated"`
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
		v, err := json.Marshal(fl.Value)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// renderJSON writes r as one indented JSON object and a trailing newline.
// HTML escaping is off: SQL text is full of <, > and &, and < helps no
// reader.
func renderJSON(w io.Writer, r *Result) error {
	env := jsonEnvelope{
		SchemaVersion: SchemaVersion,
		Status:        r.Status,
		Command:       r.Command,
		Server:        nullable(r.Server),
		Database:      nullable(r.Database),
		DurationMs:    r.Duration.Milliseconds(),
		Data:          jsonFields(r.Fields),
		ResultSets:    []jsonSet{},
		Messages:      []jsonMessage{},
		Warnings:      r.Warnings,
		Errors:        []jsonError{},
	}
	if env.Warnings == nil {
		env.Warnings = []string{}
	}
	for _, s := range r.Sets {
		js := jsonSet{
			Columns:       []jsonColumn{},
			Rows:          s.Rows,
			RowCount:      len(s.Rows),
			TotalRowCount: s.TotalRowCount,
			Truncated:     s.Truncated,
			RowsAffected:  s.RowsAffected,
		}
		if js.Rows == nil {
			js.Rows = [][]*string{}
		}
		for _, c := range s.Columns {
			js.Columns = append(js.Columns, jsonColumn{Name: c.Name, Type: nullable(c.Type), Nullable: c.Nullable})
		}
		env.ResultSets = append(env.ResultSets, js)
	}
	for _, m := range r.Messages {
		env.Messages = append(env.Messages, jsonMessage(m))
	}
	for _, e := range r.Errors {
		env.Errors = append(env.Errors, jsonError{Code: e.Code, Message: e.Message, Hint: nullable(e.Hint)})
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(env)
}

// nullable is nil for "", so JSON says null rather than an empty string that
// could be a value.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
