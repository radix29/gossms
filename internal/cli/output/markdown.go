package output

import (
	"io"
	"strings"

	"github.com/clipperhouse/displaywidth"
)

// renderMarkdown writes the text envelope with each result set as a GitHub
// table: the marker line, a blank line (a table cannot interrupt a
// paragraph), then the table. A cell's | is escaped and its line breaks are
// <br>, so a value cannot end its row or the table. --no-headers does not
// apply: a GitHub table has no form without a header row.
func renderMarkdown(w io.Writer, r *Result, o Options) error {
	return writeSections(w, textSections(r, o, func(n int, s Set) []string {
		out := []string{setMarker(n, s)}
		if len(s.Columns) > 0 && !s.InFile {
			mo := o
			mo.NoHeaders = false
			head, rows := textCells(s, mo, markdownCell)
			out = append(out, "")
			out = append(out, markdownTable(head, rows, len(s.Columns))...)
		}
		if note := truncationNote(s, o); note != "" {
			out = append(out, "", note)
		}
		return out
	}))
}

// markdownTable is head, the |---| rule and rows, padded to each column's
// display width so the source reads as a table too.
func markdownTable(head []string, rows [][]string, ncols int) []string {
	widths := columnWidths(head, rows, ncols)
	line := func(cells []string) string {
		var b strings.Builder
		b.WriteByte('|')
		for i, wd := range widths {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			b.WriteString(" " + c + strings.Repeat(" ", wd-displaywidth.String(c)) + " |")
		}
		return b.String()
	}
	var rule strings.Builder
	rule.WriteByte('|')
	for _, wd := range widths {
		rule.WriteString(strings.Repeat("-", wd+2) + "|")
	}
	out := []string{line(head), rule.String()}
	for _, r := range rows {
		out = append(out, line(r))
	}
	return out
}

// markdownCell is a cell made safe inside a GitHub table row.
func markdownCell(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "|", `\|`)
	s = strings.ReplaceAll(s, "\r\n", "<br>")
	return strings.NewReplacer("\n", "<br>", "\r", "<br>", "\t", " ",
		"\v", " ", "\f", " ", "\u0085", "<br>", " ", "<br>", " ", "<br>").Replace(s)
}
