package agent

import (
	"strings"

	"github.com/clipperhouse/displaywidth"
)

// Plain is md with its markup taken out, for `agent guide -o text`: a
// heading is its text underlined (# with =, ## with -), a fenced block's
// lines are indented four spaces without the fences, a table is aligned
// columns without its rule row, and inline code and emphasis lose their
// marks. It knows only the markdown the guide is written in.
func Plain(md string) string {
	var out []string
	lines := strings.Split(strings.TrimSuffix(md, "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		switch {
		case strings.HasPrefix(line, "```"):
			for i++; i < len(lines) && !strings.HasPrefix(lines[i], "```"); i++ {
				out = append(out, strings.TrimRight("    "+lines[i], " "))
			}
		case strings.HasPrefix(line, "|"):
			var rows [][]string
			for ; i < len(lines) && strings.HasPrefix(lines[i], "|"); i++ {
				if cells := tableCells(lines[i]); !isRule(cells) {
					rows = append(rows, cells)
				}
			}
			i--
			out = append(out, alignRows(rows)...)
		case strings.HasPrefix(line, "# "), strings.HasPrefix(line, "## "):
			level, text, _ := strings.Cut(line, " ")
			text = plainInline(text)
			under := "="
			if level == "##" {
				under = "-"
			}
			out = append(out, text, strings.Repeat(under, displaywidth.String(text)))
		case strings.HasPrefix(line, "### "):
			out = append(out, plainInline(strings.TrimPrefix(line, "### ")))
		default:
			out = append(out, plainInline(line))
		}
	}
	return strings.Join(out, "\n") + "\n"
}

// plainInline drops inline code's backticks and emphasis marks.
func plainInline(s string) string {
	s = strings.ReplaceAll(s, "`", "")
	s = strings.ReplaceAll(s, "**", "")
	return s
}

// tableCells splits a "| a | b |" row at its unescaped pipes; an escaped
// pipe is a literal one.
func tableCells(line string) []string {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "|")
	line = strings.TrimSuffix(line, "|")
	var cells []string
	var cell strings.Builder
	for i := 0; i < len(line); i++ {
		switch {
		case line[i] == '\\' && i+1 < len(line) && line[i+1] == '|':
			cell.WriteByte('|')
			i++
		case line[i] == '|':
			cells = append(cells, plainInline(strings.TrimSpace(cell.String())))
			cell.Reset()
		default:
			cell.WriteByte(line[i])
		}
	}
	return append(cells, plainInline(strings.TrimSpace(cell.String())))
}

// isRule reports whether cells is a table's |---|---| row.
func isRule(cells []string) bool {
	for _, c := range cells {
		if strings.Trim(c, "-: ") != "" {
			return false
		}
	}
	return true
}

// alignRows pads every column but the last to its widest cell, two spaces
// apart.
func alignRows(rows [][]string) []string {
	var widths []int
	for _, r := range rows {
		for j, c := range r {
			if j >= len(widths) {
				widths = append(widths, 0)
			}
			widths[j] = max(widths[j], displaywidth.String(c))
		}
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		var b strings.Builder
		for j, c := range r {
			b.WriteString(c)
			if j < len(r)-1 {
				b.WriteString(strings.Repeat(" ", widths[j]-displaywidth.String(c)+2))
			}
		}
		out[i] = strings.TrimRight(b.String(), " ")
	}
	return out
}
