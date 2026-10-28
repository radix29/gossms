// Package output is the command line's result envelope, its error model and
// one renderer per --output format (docs/cli.md § Output).
//
// A handler fills a Result; Main renders it with Render. Text and JSON carry
// the same envelope: the header (status, command, server, database,
// duration), a record (Fields) or tables (Sets), then messages, warnings and
// errors.
package output

import (
	"fmt"
	"io"
	"time"
)

// SchemaVersion is the envelope's version: JSON's "schemaVersion" and
// `gossms version`'s cliSchema. It changes only when the structure breaks
// compatibility.
const SchemaVersion = "1.0"

// Format is an --output value.
type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// Formats is every implemented format, in help order: the --output enum. A
// format is listed only once its renderer exists (docs/cli.md § Global
// options).
var Formats = []string{string(FormatText), string(FormatJSON)}

// Status is a Result's outcome.
type Status string

const (
	StatusOK        Status = "ok"
	StatusError     Status = "error"
	StatusBlocked   Status = "blocked"
	StatusCancelled Status = "cancelled"
)

// Result is what a command handler returns for Main to render.
type Result struct {
	Status   Status
	Command  string // the command path, "query run"
	Server   string // empty when the command connects to none
	Database string
	Duration time.Duration
	// Fields is a record answer (`version`, later `server info`), in output
	// order; nil for a tabular one.
	Fields   []Field
	Sets     []Set
	Messages []Message
	Warnings []string
	Errors   []*Error
}

// Field is one key/value of a record answer. Key is the JSON key, camelCase;
// text prints it as UPPER_SNAKE ("cliSchema" -> "CLI_SCHEMA").
type Field struct {
	Key   string
	Value string
}

// Set is one result set. A cell is the grid's text (internal/query renders
// every value as SSMS's grid does), nil for NULL.
//
// `profile list` builds its set by hand; `query run` will fill sets from the
// executor. Typed JSON cells (numbers, bools) arrive with the column types in
// phase 2.
type Set struct {
	Columns       []Column
	Rows          [][]*string
	TotalRowCount int  // rows the server returned; len(Rows) when not truncated
	Truncated     bool // Rows holds only the first TotalRowCount rows' head
	RowsAffected  *int64
}

// Column is one result-set column.
type Column struct {
	Name     string
	Type     string // the SQL type name; empty when unknown
	Nullable bool
}

// Message is a PRINT or informational message.
type Message struct {
	Type string // "info" (more kinds arrive with the query executor)
	Text string
}

// Render writes r to w in format. quiet drops the header (text only: a JSON
// envelope is always whole, since a parser relies on its shape).
func Render(w io.Writer, format Format, r *Result, quiet bool) error {
	switch format {
	case FormatText:
		return renderText(w, r, quiet)
	case FormatJSON:
		return renderJSON(w, r)
	}
	return fmt.Errorf("output format %q is not implemented", format)
}
