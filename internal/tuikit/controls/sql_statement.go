package controls

import "unicode"

// ---------------------------------------------------------------------------
// T-SQL statement-boundary detection for Editor (Ctrl+Enter: select the
// statement at the cursor)
// ---------------------------------------------------------------------------

// SelectStatementAtCursor selects the T-SQL statement containing the
// cursor. Statement boundaries are ';' and a "GO" batch separator alone on
// its own line — mirroring, for "GO", the same rule go-mssqldb's
// batch.Split applies when internal/query splits a script into batches to
// execute (see internal/query/executor.go) — with both ignored inside
// string literals ('...'), bracketed/quoted identifiers ([...], "..."),
// and comments (--... and /* ... */), so a semicolon or "GO" inside those
// never splits a statement in two.
//
// This is a lexical approximation, not a full T-SQL parser: statements the
// engine can tell apart without an explicit separator (e.g. two bare
// SELECTs on consecutive lines with neither ';' nor GO between them)
// aren't split apart, and stay selected as one statement. Good enough for
// the common case of ';'- or GO-separated scripts; a real parser would be
// needed to close that gap.
//
// No-ops (returns false, selection untouched) if the statement at the
// cursor is empty or all-whitespace — e.g. the cursor sits on a blank line
// between two GO separators.
func (e *Editor) SelectStatementAtCursor() bool {
	sr, sc, er, ec, ok := sqlStatementAt(e.lines, e.cursorRow, e.cursorCol)
	if !ok {
		return false
	}
	e.selecting = true
	e.selBlock = false
	e.selAnchorRow, e.selAnchorCol = sr, sc
	e.cursorRow, e.cursorCol = er, ec
	e.clampCursor()
	e.desiredCol = e.cursorCol
	e.ensureCursorVisible()
	return true
}

// sqlStatementAt scans lines for statement boundaries and returns the
// trimmed [startRow,startCol]-[endRow,endCol] span of the statement
// containing (row, col). ok is false when that statement is empty.
func sqlStatementAt(lines [][]rune, row, col int) (startRow, startCol, endRow, endCol int, ok bool) {
	type span struct{ sr, sc, er, ec int }
	var segments []span

	const (
		stNormal = iota
		stBlockComment
		stSingleQuote
		stBracket
		stDoubleQuote
	)
	state := stNormal
	curRow, curCol := 0, 0

	for r, line := range lines {
		if state == stNormal && isGoSeparatorLine(line) {
			segments = append(segments, span{curRow, curCol, r, 0})
			curRow, curCol = r+1, 0
			continue
		}
		c := 0
		for c < len(line) {
			switch state {
			case stBlockComment:
				if c+1 < len(line) && line[c] == '*' && line[c+1] == '/' {
					state = stNormal
					c += 2
				} else {
					c++
				}
			case stSingleQuote:
				if line[c] == '\'' {
					if c+1 < len(line) && line[c+1] == '\'' {
						c += 2
					} else {
						state = stNormal
						c++
					}
				} else {
					c++
				}
			case stBracket:
				if line[c] == ']' {
					if c+1 < len(line) && line[c+1] == ']' {
						c += 2
					} else {
						state = stNormal
						c++
					}
				} else {
					c++
				}
			case stDoubleQuote:
				if line[c] == '"' {
					if c+1 < len(line) && line[c+1] == '"' {
						c += 2
					} else {
						state = stNormal
						c++
					}
				} else {
					c++
				}
			default: // stNormal
				switch {
				case c+1 < len(line) && line[c] == '-' && line[c+1] == '-':
					c = len(line) // line comment: rest of the line is skipped
				case c+1 < len(line) && line[c] == '/' && line[c+1] == '*':
					state = stBlockComment
					c += 2
				case line[c] == '\'':
					state = stSingleQuote
					c++
				case line[c] == '[':
					state = stBracket
					c++
				case line[c] == '"':
					state = stDoubleQuote
					c++
				case line[c] == ';':
					c++
					segments = append(segments, span{curRow, curCol, r, c})
					curRow, curCol = r, c
				default:
					c++
				}
			}
		}
	}
	lastRow := len(lines) - 1
	segments = append(segments, span{curRow, curCol, lastRow, len(lines[lastRow])})

	cmp := func(r1, c1, r2, c2 int) int {
		switch {
		case r1 != r2:
			if r1 < r2 {
				return -1
			}
			return 1
		case c1 != c2:
			if c1 < c2 {
				return -1
			}
			return 1
		default:
			return 0
		}
	}

	for _, sg := range segments {
		if cmp(row, col, sg.sr, sg.sc) >= 0 && cmp(row, col, sg.er, sg.ec) <= 0 {
			return trimStatementRange(lines, sg.sr, sg.sc, sg.er, sg.ec)
		}
	}
	return 0, 0, 0, 0, false
}

// isGoSeparatorLine reports whether line consists of nothing but a "GO"
// batch separator: optional leading whitespace, "GO" (case-insensitive,
// not itself the prefix of a longer identifier like "goto" or "gone"),
// then only whitespace, an optional repeat count, and/or a trailing line
// comment until the end of the line.
func isGoSeparatorLine(line []rune) bool {
	i := 0
	for i < len(line) && unicode.IsSpace(line[i]) {
		i++
	}
	if i+2 > len(line) || unicode.ToUpper(line[i]) != 'G' || unicode.ToUpper(line[i+1]) != 'O' {
		return false
	}
	i += 2
	if i < len(line) && (unicode.IsLetter(line[i]) || unicode.IsDigit(line[i]) || line[i] == '_') {
		return false
	}
	for i < len(line) {
		switch {
		case unicode.IsSpace(line[i]):
			i++
		case unicode.IsDigit(line[i]):
			i++
		case i+1 < len(line) && line[i] == '-' && line[i+1] == '-':
			return true
		default:
			return false
		}
	}
	return true
}

// trimStatementRange trims leading and trailing whitespace (including
// blank lines) from [sr,sc]-[er,ec], so the selection wraps tightly around
// the statement's actual text instead of the separator whitespace next to
// it. ok is false if nothing but whitespace remains.
func trimStatementRange(lines [][]rune, sr, sc, er, ec int) (int, int, int, int, bool) {
	for {
		if sr > er || (sr == er && sc >= ec) {
			return 0, 0, 0, 0, false
		}
		if sc >= len(lines[sr]) {
			sr++
			sc = 0
			continue
		}
		if unicode.IsSpace(lines[sr][sc]) {
			sc++
			continue
		}
		break
	}
	for {
		if sr > er || (sr == er && sc >= ec) {
			return 0, 0, 0, 0, false
		}
		if ec == 0 {
			er--
			ec = len(lines[er])
			continue
		}
		if unicode.IsSpace(lines[er][ec-1]) {
			ec--
			continue
		}
		break
	}
	return sr, sc, er, ec, true
}
