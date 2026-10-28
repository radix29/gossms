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

// nullText is how text shows a NULL cell (--null-text arrives in phase 2).
const nullText = "NULL"

// renderText writes the text format: KEY: value header lines, a blank line
// before each further section, fixed === markers an agent can split on.
//
//	STATUS: OK               header (dropped by --quiet)
//	COMMAND: version
//	DURATION_MS: 0
//
//	VERSION: v0.2.0          Fields, as KEY: value
//
//	=== RESULT 1 (5 rows, truncated: no) ===
//	name | create_date       Sets, aligned by display width
//	-----+------------
//
//	=== MESSAGES ===  /  === WARNINGS ===
//
//	ERROR_CODE: USAGE_ERROR  one block per error
//	MESSAGE: …
//	HINT: …
//
// Every value is one line: a line break or tab inside it prints as a space,
// so no value can forge a header or marker line.
func renderText(w io.Writer, r *Result, quiet bool) error {
	bw := bufio.NewWriter(w)
	var sections [][]string
	if !quiet {
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
		if len(r.Sets) > 0 {
			h = append(h, keyValue("RESULT_SETS", strconv.Itoa(len(r.Sets))))
		}
		sections = append(sections, h)
	}
	if len(r.Fields) > 0 {
		var f []string
		for _, fl := range r.Fields {
			f = append(f, keyValue(upperSnake(fl.Key), fl.Value))
		}
		sections = append(sections, f)
	}
	for i, s := range r.Sets {
		sections = append(sections, textSet(i+1, s))
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
		b := []string{keyValue("ERROR_CODE", string(e.Code)), keyValue("MESSAGE", e.Message)}
		if e.Hint != "" {
			b = append(b, keyValue("HINT", e.Hint))
		}
		sections = append(sections, b)
	}
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

// textSet is one result set: its marker line, then the column names and rows
// as a table whose columns align by display width (displaywidth.String, the
// function core.DisplayWidth wraps — tuikit/core imports tcell, which
// internal/cli must not reach). The last column is not padded, so no line
// ends in spaces.
func textSet(n int, s Set) []string {
	rows := fmt.Sprintf("%s rows", thousands(len(s.Rows)))
	if len(s.Rows) == 1 {
		rows = "1 row"
	}
	if s.Truncated {
		rows = fmt.Sprintf("%s of %s rows", thousands(len(s.Rows)), thousands(s.TotalRowCount))
	}
	truncated := "no"
	if s.Truncated {
		truncated = "yes"
	}
	out := []string{fmt.Sprintf("=== RESULT %d (%s, truncated: %s) ===", n, rows, truncated)}
	if len(s.Columns) == 0 {
		return out
	}

	cells := make([][]string, 0, len(s.Rows)+1)
	head := make([]string, len(s.Columns))
	for i, c := range s.Columns {
		head[i] = oneLine(c.Name)
	}
	cells = append(cells, head)
	for _, row := range s.Rows {
		line := make([]string, len(s.Columns))
		for i := range line {
			switch {
			case i >= len(row):
			case row[i] == nil:
				line[i] = nullText
			default:
				line[i] = oneLine(*row[i])
			}
		}
		cells = append(cells, line)
	}
	widths := make([]int, len(s.Columns))
	for _, line := range cells {
		for i, c := range line {
			widths[i] = max(widths[i], displaywidth.String(c))
		}
	}

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
	out = append(out, join(head))
	rule := make([]string, len(widths))
	for i, wd := range widths {
		rule[i] = strings.Repeat("-", wd)
	}
	out = append(out, strings.Join(rule, "-+-"))
	for _, line := range cells[1:] {
		out = append(out, join(line))
	}
	return out
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
