package output

import (
	"io"
	"strings"

	"github.com/clipperhouse/displaywidth"

	"github.com/radix29/gossms/internal/textutil"
)

// renderMarkdown writes the text envelope with each Verbatim field as a fenced
// code block under its marker and each result set as a GitHub table: the
// marker line, a blank line (a table cannot interrupt a paragraph), then the
// table. A cell's | is escaped, & < > are entities and its line breaks are
// <br>, so a value cannot end its row or the table or read as HTML.
// --no-headers does not apply: a GitHub table has no form without a header
// row.
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
	}, func(f Field) []string {
		lines := verbatimLines(textutil.Visible(f.Value))
		fence := strings.Repeat("`", max(3, longestRun(f.Value, '`')+1))
		out := []string{verbatimMarker(f, len(lines)), "", fence}
		return append(append(out, lines...), fence)
	}))
}

// longestRun is the longest run of r in s: a code fence must be longer than
// any backtick run inside the block, or the block ends early.
func longestRun(s string, r rune) int {
	longest, run := 0, 0
	for _, c := range s {
		if c != r {
			run = 0
			continue
		}
		run++
		longest = max(longest, run)
	}
	return longest
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

// markdownCell is a cell made safe inside a GitHub table row. GitHub renders
// inline HTML in a cell, so &, < and > become entities: unescaped, an xml
// column's <root a="1"/> renders as nothing, &lt; reads as <, and a value
// holding the text <br> would look like a line break. The inline-Markdown
// specials ` * _ [ ] ! ~ are backslash-escaped (a literal character in
// CommonMark), so a database value cannot render as a remote image, a link,
// emphasis or code when the table is posted to a PR or a chat; the
// block-level ones (# - + > and list digits) cannot start a cell inside a row,
// so they stay bare. One pass, so the <br> written for a line break stays
// markup and an & or \ is never escaped twice.
func markdownCell(s string) string {
	return markdownCellReplacer.Replace(strings.ReplaceAll(textutil.Visible(s), "\r\n", "\n"))
}

var markdownCellReplacer = strings.NewReplacer(`\`, `\\`, "|", `\|`,
	"`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "!", `\!`, "~", `\~`,
	"&", "&amp;", "<", "&lt;", ">", "&gt;",
	"\n", "<br>", "\r", "<br>", "\t", " ", "\v", " ", "\f", " ",
	"\u0085", "<br>", "\u2028", "<br>", "\u2029", "<br>")
