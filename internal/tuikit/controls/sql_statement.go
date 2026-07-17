package controls

import (
	"strings"
	"unicode"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// T-SQL statement-boundary detection for Editor (Ctrl+Enter: select the
// statement at the cursor)
// ---------------------------------------------------------------------------

// SelectStatementAtCursor selects the T-SQL statement containing the
// cursor. Statement boundaries are ';', a "GO" batch separator alone on its
// own line — mirroring, for "GO", the same rule go-mssqldb's batch.Split
// applies when internal/query splits a script into batches to execute (see
// internal/query/executor.go) — and, additionally, a top-level (paren-depth
// zero) DML-leading keyword (SELECT/INSERT/UPDATE/DELETE/MERGE/WITH), so
// scripts stacking several ad hoc statements with no ';' between them —
// completely normal in SSMS, where a trailing ';' is optional — still split
// correctly. A UNION/EXCEPT/INTERSECT-chained SELECT, a CTE's own main
// SELECT after WITH ... AS (...), and INSERT ... SELECT are recognised as
// continuations of the same statement, not new ones (see sqlStatementAt
// below — the same heuristic internal/tui's IntelliSense scopes column
// completion with, dmlStatementStarts in
// internal/tui/completion_provider.go, reimplemented here in row/column form
// since tuikit must never import tui). All boundary kinds are ignored inside
// string literals ('...'), bracketed/quoted identifiers ([...], "..."), and
// comments (--... and /* ... */), so one of those characters appearing
// inside never splits a statement in two.
//
// This is a lexical approximation, not a full T-SQL parser: only
// INSERT ... VALUES followed by a later, genuinely separate SELECT with no
// ';' between them is (rarely) missed — a known best-effort limitation,
// matching dmlStatementStarts' own documented one.
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

// dmlStatementLeaders are the T-SQL keywords that can only ever begin a new
// statement — mirrors internal/tui/completion_provider.go's map of the same
// name exactly (kept in sync by hand; tuikit cannot import tui to share it).
var dmlStatementLeaders = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"MERGE": true, "WITH": true,
}

// dmlBoundaryKeywords is dmlStatementLeaders plus the small set of
// additional keywords the boundary heuristic below needs to track —
// VALUES (clears a pending INSERT ... SELECT/CTE main-query suppression),
// and UNION/EXCEPT/INTERSECT/ALL (recognise a chained SELECT as a
// continuation, not a new statement). Every other keyword is irrelevant to
// this narrow heuristic, so — unlike completion_provider.go's much larger
// sqlKeywords table, which also drives clause detection and FROM-scope
// parsing — this set only needs to be exactly these.
var dmlBoundaryKeywords = map[string]bool{
	"SELECT": true, "INSERT": true, "UPDATE": true, "DELETE": true,
	"MERGE": true, "WITH": true, "VALUES": true,
	"UNION": true, "EXCEPT": true, "INTERSECT": true, "ALL": true,
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

	// DML-leader statement-boundary tracking (see the doc comment above) —
	// reset at every ';'/GO batch boundary, since a boundary of either kind
	// always falls at paren depth 0 in valid SQL and dmlStatementStarts (the
	// tui-side analogue) is likewise given a fresh token stream per batch.
	parenDepth := 0
	prevKeyword, prevPrevKeyword := "", ""
	pendingMainSelect := false

	for r, line := range lines {
		if state == stNormal && isGoSeparatorLine(line) {
			segments = append(segments, span{curRow, curCol, r, 0})
			curRow, curCol = r+1, 0
			parenDepth = 0
			prevKeyword, prevPrevKeyword = "", ""
			pendingMainSelect = false
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
					parenDepth = 0
					prevKeyword, prevPrevKeyword = "", ""
					pendingMainSelect = false
				case line[c] == '(':
					parenDepth++
					c++
				case line[c] == ')':
					if parenDepth > 0 {
						parenDepth--
					}
					c++
				case core.IsWordRune(line[c]):
					start := c
					for c < len(line) && core.IsWordRune(line[c]) {
						c++
					}
					word := strings.ToUpper(string(line[start:c]))
					if parenDepth == 0 && dmlBoundaryKeywords[word] {
						switch {
						case word == "VALUES":
							pendingMainSelect = false
						case dmlStatementLeaders[word]:
							continuesUnion := prevKeyword == "UNION" || prevKeyword == "EXCEPT" || prevKeyword == "INTERSECT" ||
								(prevKeyword == "ALL" && prevPrevKeyword == "UNION")
							switch {
							case word == "SELECT" && pendingMainSelect:
								pendingMainSelect = false
							case word == "SELECT" && continuesUnion:
								// UNION-chain continuation of the same statement, not a new one.
							default:
								if r > curRow || (r == curRow && start > curCol) {
									segments = append(segments, span{curRow, curCol, r, start})
									curRow, curCol = r, start
								}
								pendingMainSelect = word == "WITH" || word == "INSERT"
							}
						}
						prevPrevKeyword = prevKeyword
						prevKeyword = word
					}
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

	// Adjacent segments share their boundary point (segment i's end equals
	// segment i+1's start), so a cursor sitting exactly there matches both;
	// take the last match, not the first, so it resolves to the statement
	// the cursor is positioned at the *start* of — the common case, since
	// users land there via Home/a mouse click on the new statement's first
	// line — rather than the one it trails.
	found := -1
	for i, sg := range segments {
		if cmp(row, col, sg.sr, sg.sc) >= 0 && cmp(row, col, sg.er, sg.ec) <= 0 {
			found = i
		}
	}
	if found < 0 {
		return 0, 0, 0, 0, false
	}
	sg := segments[found]
	return trimStatementRange(lines, sg.sr, sg.sc, sg.er, sg.ec)
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
