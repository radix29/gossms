package output

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"github.com/clipperhouse/displaywidth"
)

// renderText writes the text format: KEY: value header lines, a blank line
// before each further section, fixed === markers an agent can split on.
//
//	STATUS: OK               header (dropped by --quiet)
//	COMMAND: version
//	DURATION_MS: 0
//
//	VERSION: v0.2.0          Fields, as KEY: value
//
//	=== DEFINITION (3 lines) ===   a Verbatim field, its lines as they are
//	CREATE VIEW …
//
//	=== RESULT 1 (5 rows, truncated: no) ===   "RESULT 1: columns (…)" when named
//	name | create_date       Sets, aligned by display width
//	-----+------------
//	NOTE: …                  when the set was truncated
//
//	=== MESSAGES ===  /  === WARNINGS ===
//
//	ERROR_CODE: USAGE_ERROR  one block per error
//	MESSAGE: …
//	HINT: …
//
// Every value is one line: a line break or tab inside it prints as a space,
// so no value can forge a header or marker line. A Verbatim field is the one
// exception, and its marker says how many lines follow so a reader takes
// exactly those. tables false prints each
// set as its marker and note only: csv's and tsv's summary on stderr.
func renderText(w io.Writer, r *Result, o Options, tables bool) error {
	return writeSections(w, textSections(r, o, func(n int, s Set) []string {
		if !tables {
			return setHead(n, s, o)
		}
		return textSet(n, s, o)
	}, func(f Field) []string {
		lines := verbatimLines(f.Value)
		return append([]string{verbatimMarker(f, len(lines))}, lines...)
	}))
}

// verbatimLines is a Verbatim value's lines, \r\n and \r read as line ends
// (output is \n on every OS); no trailing empty line for a final break.
func verbatimLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// verbatimMarker is a Verbatim field's "=== KEY (n lines) ===" line.
func verbatimMarker(f Field, n int) string {
	lines := strconv.Itoa(n) + " lines"
	if n == 1 {
		lines = "1 line"
	}
	return "=== " + upperSnake(f.Key) + " (" + lines + ") ==="
}

// textSections is the envelope as sections of lines, each set drawn by set and
// each non-null Verbatim field by verbatim. Markdown shares it, with its own
// tables and code blocks.
func textSections(r *Result, o Options, set func(n int, s Set) []string, verbatim func(f Field) []string) [][]string {
	var sections [][]string
	if !o.Quiet {
		h := []string{
			keyValue("STATUS", strings.ToUpper(string(r.Status))),
			keyValue("COMMAND", r.Command),
		}
		if r.Server != "" {
			h = append(h, keyValue("SERVER", r.Server))
		}
		if r.Database != "" {
			h = append(h, keyValue("DATABASE", r.Database))
		}
		h = append(h, keyValue("DURATION_MS", strconv.FormatInt(r.Duration.Milliseconds(), 10)))
		if r.RowsAffected != nil {
			h = append(h, keyValue("ROWS_AFFECTED", strconv.FormatInt(*r.RowsAffected, 10)))
		}
		if r.OutFile != "" {
			h = append(h, keyValue("OUT_FILE", r.OutFile))
		}
		if len(r.Sets) > 0 {
			h = append(h, keyValue("RESULT_SETS", strconv.Itoa(len(r.Sets))))
		}
		sections = append(sections, h)
	}
	var f []string
	var blocks [][]string
	for _, fl := range r.Fields {
		switch {
		case !fl.Verbatim:
			f = append(f, keyValue(upperSnake(fl.Key), fl.Value))
		case !fl.Null:
			blocks = append(blocks, verbatim(fl))
		}
	}
	if len(f) > 0 {
		sections = append(sections, f)
	}
	sections = append(sections, blocks...)
	for i, s := range r.Sets {
		sections = append(sections, set(i+1, s))
	}
	if len(r.Messages) > 0 {
		m := []string{"=== MESSAGES ==="}
		for _, msg := range r.Messages {
			m = append(m, oneLine(msg.Text))
		}
		sections = append(sections, m)
	}
	if len(r.Warnings) > 0 {
		sections = append(sections, append([]string{"=== WARNINGS ==="}, mapLines(r.Warnings)...))
	}
	for _, e := range r.Errors {
		b := []string{keyValue("ERROR_CODE", string(e.Code))}
		if q := e.SQL; q != nil {
			b = append(b,
				keyValue("SQL_ERROR_NUMBER", strconv.FormatInt(int64(q.Number), 10)),
				keyValue("SEVERITY", strconv.Itoa(int(q.Severity))),
				keyValue("STATE", strconv.Itoa(int(q.State))),
				keyValue("LINE", strconv.Itoa(q.Line)))
			if q.Procedure != "" {
				b = append(b, keyValue("PROCEDURE", q.Procedure))
			}
		}
		b = append(b, keyValue("MESSAGE", e.Message))
		if e.Hint != "" {
			b = append(b, keyValue("HINT", e.Hint))
		}
		sections = append(sections, b)
	}
	return sections
}

func writeSections(w io.Writer, sections [][]string) error {
	bw := bufio.NewWriter(w)
	for i, s := range sections {
		if i > 0 {
			bw.WriteByte('\n')
		}
		for _, line := range s {
			bw.WriteString(line)
			bw.WriteByte('\n')
		}
	}
	return bw.Flush()
}

// setMarker is a set's "=== RESULT n (…) ===" line.
func setMarker(n int, s Set) string {
	if s.InFile {
		return fmt.Sprintf("=== RESULT %d%s (%s, sent to --out-file) ===", n, setName(s), rowCount(s.TotalRowCount))
	}
	rows := rowCount(len(s.Rows))
	if s.Truncated {
		rows = fmt.Sprintf("%s of %s rows", thousands(len(s.Rows)), thousands(s.TotalRowCount))
	}
	truncated := "no"
	if s.Truncated {
		truncated = "yes"
	}
	return fmt.Sprintf("=== RESULT %d%s (%s, truncated: %s) ===", n, setName(s), rows, truncated)
}

// setName is ": name" for a named set, else empty.
func setName(s Set) string {
	if s.Name == "" {
		return ""
	}
	return ": " + oneLine(s.Name)
}

func rowCount(n int) string {
	if n == 1 {
		return "1 row"
	}
	return thousands(n) + " rows"
}

// truncationNote says which limit cut a set and how to get every row; empty
// when nothing did.
func truncationNote(s Set, o Options) string {
	switch {
	case !s.Truncated:
		return ""
	case s.TruncatedBy == TruncatedByMaxOutputBytes:
		return fmt.Sprintf("NOTE: Output truncated by --max-output-bytes %d. Raise it, or use --out-file to get all rows.", o.MaxOutputBytes)
	}
	return fmt.Sprintf("NOTE: Output truncated by --max-rows %d. Use --max-rows 0 or --out-file to get all rows.", o.MaxRows)
}

// setHead is a set's marker and its truncation note, without the table.
func setHead(n int, s Set, o Options) []string {
	out := []string{setMarker(n, s)}
	if note := truncationNote(s, o); note != "" {
		out = append(out, note)
	}
	return out
}

// textCells is a set's header (nil under NoHeaders) and rows as display
// strings: NULL as NullText, every cell clipped and made one line.
func textCells(s Set, o Options, line func(string) string) (head []string, rows [][]string) {
	if !o.NoHeaders {
		head = make([]string, len(s.Columns))
		for i, c := range s.Columns {
			head[i] = line(c.Name)
		}
	}
	for _, row := range s.Rows {
		cells := make([]string, len(s.Columns))
		for i := range cells {
			switch {
			case i >= len(row):
			case row[i] == nil:
				cells[i] = line(o.NullText)
			default:
				cells[i] = line(clip(*row[i], o.MaxColWidth))
			}
		}
		rows = append(rows, cells)
	}
	return head, rows
}

// textSet is one result set: its marker line, then the column names and rows
// as a table whose columns align by display width (displaywidth.String, the
// function core.DisplayWidth wraps — tuikit/core imports tcell, which
// internal/cli must not reach), then its truncation note. The last column is
// not padded, so no line ends in spaces.
func textSet(n int, s Set, o Options) []string {
	out := []string{setMarker(n, s)}
	if len(s.Columns) > 0 && !s.InFile {
		head, rows := textCells(s, o, oneLine)
		out = append(out, alignedTable(head, rows, len(s.Columns))...)
	}
	if note := truncationNote(s, o); note != "" {
		out = append(out, note)
	}
	return out
}

// alignedTable is head (when non-nil), a -+- rule and rows, " | "-separated
// and padded to each column's display width.
func alignedTable(head []string, rows [][]string, ncols int) []string {
	widths := columnWidths(head, rows, ncols)
	join := func(line []string) string {
		var b strings.Builder
		for i, c := range line {
			if i > 0 {
				b.WriteString(" |")
				if c != "" || i < len(line)-1 {
					b.WriteByte(' ')
				}
			}
			b.WriteString(c)
			if i < len(line)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-displaywidth.String(c)))
			}
		}
		return b.String()
	}
	var out []string
	if head != nil {
		out = append(out, join(head))
		rule := make([]string, len(widths))
		for i, wd := range widths {
			rule[i] = strings.Repeat("-", wd)
		}
		out = append(out, strings.Join(rule, "-+-"))
	}
	for _, line := range rows {
		out = append(out, join(line))
	}
	return out
}

// columnWidths is each column's widest display width over head and rows.
func columnWidths(head []string, rows [][]string, ncols int) []int {
	widths := make([]int, ncols)
	for _, line := range append([][]string{head}, rows...) {
		for i, c := range line {
			widths[i] = max(widths[i], displaywidth.String(c))
		}
	}
	return widths
}

// keyValue is one "KEY: value" line; "KEY:" alone for an empty value, so no
// line ends in a space.
func keyValue(key, value string) string {
	if value == "" {
		return key + ":"
	}
	return key + ": " + oneLine(value)
}

// oneLine replaces every line break and tab in s with a space.
func oneLine(s string) string {
	const breaks = "\r\n\t\v\f\u0085\u2028\u2029"
	if !strings.ContainsAny(s, breaks) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(breaks, r) {
			return ' '
		}
		return r
	}, s)
}

func mapLines(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = oneLine(s)
	}
	return out
}

// upperSnake is a camelCase JSON key as a text key: "cliSchema" ->
// "CLI_SCHEMA", "durationMs" -> "DURATION_MS".
func upperSnake(key string) string {
	var b strings.Builder
	for i, r := range key {
		if i > 0 && unicode.IsUpper(r) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// thousands formats n with comma separators: 18432 -> "18,432". Only counts
// in a marker line use it; cell values never get separators.
func thousands(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
