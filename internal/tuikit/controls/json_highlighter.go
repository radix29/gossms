package controls

import (
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// JSON syntax highlighter (can be used as a Highlighter for Editor)
// ---------------------------------------------------------------------------

// JSONHighlighter is the built-in JSON syntax highlighter for Editor, for a host
// showing JSON text (e.g. a result-set cell opened in its own panel).
//
// Unlike SQLHighlighter and XMLHighlighter this needs no prefixStates cache, and
// that is a property of JSON, not an omission: no JSON token can span a line. A
// string may not contain a literal newline (RFC 8259 §7), there are no comments,
// and every other token is a literal or a single punctuation character. So each
// line's highlighting depends only on that line.
//
// That also makes it the cheapest of the three on the shape a SQL Server JSON
// column actually arrives in: one enormous single line. Cost is linear in the
// line, once.
func JSONHighlighter(p *theme.Palette) Highlighter {
	return func(doc *Document, idx int) []ColorRun {
		// Built per call, not once: p is the live palette, so a theme switch
		// recolours an open editor.
		keyStyle := theme.On(p.EditorBg, p.EditorKeyword).Bold(true)
		strStyle := theme.On(p.EditorBg, p.EditorString)
		numStyle := theme.On(p.EditorBg, p.EditorNumber)
		litStyle := theme.On(p.EditorBg, p.EditorComment)
		line := doc.Line(idx)
		runs := make([]ColorRun, 0, 8)
		for i := 0; i < len(line); {
			switch c := line[i]; {
			case c == '"':
				end := jsonStringEnd(line, i)
				// A key is a string whose next non-space character is ':' —
				// the only thing distinguishing it from a value, since JSON
				// object keys are ordinary strings.
				style := strStyle
				if jsonNextNonSpace(line, end) == ':' {
					style = keyStyle
				}
				runs = append(runs, ColorRun{i, end - i, style})
				i = end
			case c == '-' || (c >= '0' && c <= '9'):
				end := jsonNumberEnd(line, i)
				runs = append(runs, ColorRun{i, end - i, numStyle})
				i = end
			case c == 't' || c == 'f' || c == 'n':
				// true/false/null. Anything else alphabetic is not valid JSON
				// at all, so it is left unstyled rather than guessed at.
				if end, ok := jsonLiteralEnd(line, i); ok {
					runs = append(runs, ColorRun{i, end - i, litStyle})
					i = end
				} else {
					i++
				}
			default:
				// Structural punctuation ({}[],:), whitespace, and anything
				// malformed — all unstyled, matching how the XML highlighter
				// leaves its delimiters plain.
				i++
			}
		}
		return runs
	}
}

// jsonStringEnd returns the offset just past the string literal starting at
// the opening quote line[start], or len(line) if it is never closed — an
// unterminated string can only mean a truncated or malformed document, and
// colouring the rest of the line as string is the useful reading of it.
//
// A backslash escapes the following character, so \" does not close the
// string and \\ does not escape the quote after it.
func jsonStringEnd(line []rune, start int) int {
	for i := start + 1; i < len(line); i++ {
		switch line[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(line)
}

// jsonNumberEnd returns the offset just past the number starting at start.
// Deliberately permissive about shape — it consumes the run of characters a
// JSON number is built from rather than validating exponent/fraction order,
// since a highlighter's job is to colour the token, not to reject it.
func jsonNumberEnd(line []rune, start int) int {
	i := start + 1
	for i < len(line) {
		c := line[i]
		if (c >= '0' && c <= '9') || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-' {
			i++
			continue
		}
		break
	}
	return i
}

// jsonLiteralEnd matches true/false/null at start, returning the offset just
// past it. The word must end there: "nullable" is not the literal null, and
// styling its first four characters would be worse than leaving it plain.
func jsonLiteralEnd(line []rune, start int) (int, bool) {
	for _, lit := range [...]string{"true", "false", "null"} {
		end := start + len(lit)
		if end > len(line) {
			continue
		}
		if string(line[start:end]) != lit {
			continue
		}
		if end < len(line) && jsonWordRune(line[end]) {
			return 0, false
		}
		return end, true
	}
	return 0, false
}

// jsonWordRune reports whether c could continue a bare word, so a literal is
// only recognised when nothing word-like follows it.
func jsonWordRune(c rune) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// jsonNextNonSpace returns the first non-space character at or after i, or 0
// at end of line.
func jsonNextNonSpace(line []rune, i int) rune {
	for ; i < len(line); i++ {
		if line[i] != ' ' && line[i] != '\t' {
			return line[i]
		}
	}
	return 0
}
