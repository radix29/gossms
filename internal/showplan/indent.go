package showplan

import "strings"

// Indent pretty-prints a single-line XML document with two-space
// indentation, for display. Documents that already span multiple lines
// (e.g. SSMS-saved .sqlplan files) are returned unchanged. Purely
// textual — no namespace rewriting, attributes untouched.
func Indent(x string) string {
	// A trailing newline at end-of-file is near-universal and must not by
	// itself count as "already multi-line" — trim it before checking, or
	// every single-line document with a plain EOF newline would falsely
	// match and be returned completely unindented.
	trimmed := strings.TrimRight(x, "\r\n \t")
	if strings.Contains(trimmed, ">\n") || strings.Contains(trimmed, ">\r") {
		return x
	}
	var sb strings.Builder
	sb.Grow(len(x) + len(x)/8)
	depth := 0
	first := true
	writeLine := func(s string, d int) {
		if !first {
			sb.WriteByte('\n')
			for range d {
				sb.WriteString("  ")
			}
		}
		first = false
		sb.WriteString(s)
	}
	i := 0
	for i < len(x) {
		lt := strings.IndexByte(x[i:], '<')
		if lt < 0 {
			if txt := strings.TrimSpace(x[i:]); txt != "" {
				writeLine(txt, depth)
			}
			break
		}
		if txt := strings.TrimSpace(x[i : i+lt]); txt != "" {
			writeLine(txt, depth)
		}
		end := tagEnd(x, i+lt)
		if end < 0 {
			writeLine(x[i+lt:], depth)
			break
		}
		tag := x[i+lt : end+1]
		closing := strings.HasPrefix(tag, "</")
		selfContained := strings.HasSuffix(tag, "/>") ||
			strings.HasPrefix(tag, "<?") || strings.HasPrefix(tag, "<!")
		if closing && depth > 0 {
			depth--
		}
		writeLine(tag, depth)
		if !closing && !selfContained {
			depth++
		}
		i = end + 1
	}
	return sb.String()
}

// tagEnd returns the index of the '>' closing the tag that starts at
// x[start] (which must be '<'), skipping any '>' inside quoted attribute
// values. Returns -1 if the tag never closes.
func tagEnd(x string, start int) int {
	quote := byte(0)
	for i := start; i < len(x); i++ {
		c := x[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i
		}
	}
	return -1
}
