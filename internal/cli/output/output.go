// Package output is the command line's result envelope, its error model and
// one renderer per --output format (docs/cli.md § Output).
//
// A handler fills a Result, streaming rows into the invocation's Sink when it
// has them; Main finishes the Sink and renders with Render. Every format
// carries the same envelope: the header (status, command, server, database,
// duration, rows affected), a record (Fields) and/or tables (Sets), then
// messages, warnings and errors.
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
	FormatText     Format = "text"
	FormatJSON     Format = "json"
	FormatJSONL    Format = "jsonl"
	FormatCSV      Format = "csv"
	FormatTSV      Format = "tsv"
	FormatMarkdown Format = "markdown"
)

// Formats is every implemented format, in help order: the --output enum. A
// format is listed only once its renderer exists (docs/cli.md § Global
// options).
var Formats = []string{string(FormatText), string(FormatJSON), string(FormatJSONL),
	string(FormatCSV), string(FormatTSV), string(FormatMarkdown)}

// RowsOnly reports whether f puts nothing but rows on stdout (csv, tsv): their
// summary goes to stderr instead.
func (f Format) RowsOnly() bool { return f == FormatCSV || f == FormatTSV }

// The output options' defaults (docs/cli.md § Output options).
const (
	DefaultMaxRows        = 200
	DefaultMaxColWidth    = 256
	DefaultMaxOutputBytes = 1 << 20
	DefaultNullText       = "NULL"
)

// Options is how a Result is rendered: the output options of the command line.
type Options struct {
	Format Format
	Quiet  bool // text and markdown: no header block

	// MaxRows is the rows kept per result set on stdout; 0 keeps every row.
	MaxRows int
	// MaxColWidth is the characters (runes) a cell keeps in text, markdown,
	// json and jsonl; 0 keeps all. Never applied to csv, tsv or a file.
	MaxColWidth int
	// MaxOutputBytes caps stdout; 0 is no cap. Rows are dropped to fit, never
	// part of a value.
	MaxOutputBytes int64
	// NullText is how text and markdown show a NULL.
	NullText string
	// NoHeaders leaves out column headers (text, csv, tsv).
	NoHeaders bool
	// NoMessages leaves out PRINT and informational messages.
	NoMessages bool

	// OutFile, when set, receives every row in the format its extension names;
	// stdout gets the summary. Force replaces an existing file.
	OutFile string
	Force   bool
}

// DefaultOptions is format with every other option at its default: what a
// result is rendered with before the command line has parsed.
func DefaultOptions(format Format) Options {
	return Options{Format: format, MaxRows: DefaultMaxRows, MaxColWidth: DefaultMaxColWidth,
		MaxOutputBytes: DefaultMaxOutputBytes, NullText: DefaultNullText}
}

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
	// RowsAffected is the sum of the DML row counts the server reported; nil
	// when it reported none.
	RowsAffected *int64
	// OutFile is the --out-file written, set by Sink.Finish once committed.
	OutFile string
	// Fields is a record answer (`version`, `query classify`'s summary), in
	// output order; nil when there is none.
	Fields   []Field
	Sets     []Set
	Messages []Message
	Warnings []string
	Errors   []*Error

	// streamed: the rows went to stdout as they arrived (jsonl), so Render
	// writes only the summary.
	streamed bool
}

// Field is one key/value of a record answer. Key is the JSON key, camelCase;
// text prints it as UPPER_SNAKE ("cliSchema" -> "CLI_SCHEMA").
type Field struct {
	Key   string
	Value string
	// Null is a value that is not there (JSON null; text prints KEY: as for
	// an empty one).
	Null bool
	// Verbatim is a multi-line value kept whole — a module's definition. Text
	// and markdown print it after the record as its own section, under an
	// "=== KEY (n lines) ===" marker, line breaks intact; JSON as a string.
	Verbatim bool
}

// Set is one result set. A cell is the grid's text (internal/query renders
// every value as SSMS's grid does), nil for NULL; JSON types it by the
// column's Type (typed.go).
type Set struct {
	// Name says what the set holds when a command returns several fixed ones
	// ("columns", "indexes"); empty for a script's sets.
	Name          string
	Columns       []Column
	Rows          [][]*string
	TotalRowCount int  // rows the server returned; len(Rows) when not truncated
	Truncated     bool // Rows holds only the first rows of TotalRowCount
	// TruncatedBy names the limit that cut Rows: TruncatedByMaxRows or
	// TruncatedByMaxOutputBytes; empty when not truncated.
	TruncatedBy  string
	RowsAffected *int64
	// InFile: the rows went to --out-file, and Rows is empty on purpose.
	InFile bool

	// streamed is the rows written to stdout as jsonl lines, when the Sink
	// streamed them (Result.streamed).
	streamed int
}

// The limits a Set can be truncated by, spelled as JSON's truncatedBy.
const (
	TruncatedByMaxRows        = "max-rows"
	TruncatedByMaxOutputBytes = "max-output-bytes"
)

// Column is one result-set column.
type Column struct {
	Name     string
	Type     string // the SQL type name as SSMS writes it ("nvarchar(50)"); empty when unknown
	Nullable bool
}

// Message is a PRINT or informational message.
type Message struct {
	Type string // "info"
	Text string
}

// Render writes r to w in o's format. Text's and markdown's quiet drops the
// header (a JSON envelope is always whole, since a parser relies on its
// shape); csv and tsv write rows only (RenderSummary has the rest).
func Render(w io.Writer, r *Result, o Options) error {
	if o.NoMessages && len(r.Messages) > 0 {
		c := *r
		c.Messages, r = nil, &c
	}
	switch o.Format {
	case FormatText, FormatJSON, FormatMarkdown, FormatCSV, FormatTSV:
		return renderCapped(w, r, o)
	case FormatJSONL:
		return renderJSONL(w, r, o)
	}
	return fmt.Errorf("output format %q is not implemented", o.Format)
}

// RenderSummary writes csv's and tsv's summary — the text envelope, its
// result sets as their marker lines and truncation notes only — for stderr.
func RenderSummary(w io.Writer, r *Result, o Options) error {
	o.Format = FormatText
	return renderText(w, r, o, false)
}

// render writes r in o's format, uncapped.
func render(w io.Writer, r *Result, o Options) error {
	switch o.Format {
	case FormatText:
		return renderText(w, r, o, true)
	case FormatMarkdown:
		return renderMarkdown(w, r, o)
	case FormatJSON:
		return renderJSON(w, r, o)
	case FormatCSV, FormatTSV:
		return renderRecords(w, r, o)
	}
	return fmt.Errorf("output format %q is not implemented", o.Format)
}
