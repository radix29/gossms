package tui

import (
	"strings"
	"unicode/utf8"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Tokenizer
// ---------------------------------------------------------------------------

type sqlTokKind int

const (
	sqlTokIdent sqlTokKind = iota
	sqlTokKeyword
	sqlTokDot
	sqlTokComma
	sqlTokParenOpen
	sqlTokParenClose
)

type sqlToken struct {
	kind  sqlTokKind
	text  string // sqlTokIdent: unwrapped name; sqlTokKeyword: uppercased
	start int    // rune offset into the flattened buffer
}

type sqlLexState int

const (
	sqlLexNormal sqlLexState = iota
	sqlLexLineComment
	sqlLexBlockComment
	sqlLexSingleQuote
	sqlLexBracket
	sqlLexDoubleQuote
)

// flattenLines joins a multi-line buffer into one rune slice with '\n'
// separators, so the tokenizer can scan linearly without juggling
// (row, col) pairs — a comment or string literal spanning several lines
// then falls out of the same state machine for free.
// Sized up front: this runs on every keystroke while the completion popup
// is open, and growing a nil slice to the size of a large script re-copies
// the whole buffer a dozen times over.
func flattenLines(lines [][]rune) []rune {
	n := 0
	for _, l := range lines {
		n += len(l) + 1
	}
	if n > 0 {
		n-- // separators go between lines, not after the last one
	}
	buf := make([]rune, 0, n)
	for i, l := range lines {
		if i > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, l...)
	}
	return buf
}

// offsetForCursor converts an Editor (row, col) into an offset into
// flattenLines' output.
func offsetForCursor(lines [][]rune, row, col int) int {
	off := 0
	for i := 0; i < row && i < len(lines); i++ {
		off += len(lines[i]) + 1
	}
	if row < len(lines) {
		off += core.Clamp(col, 0, len(lines[row]))
	}
	return off
}

// tokenizeSQLPrefix scans buf[:upTo] into a token stream, and reports the
// lexer's state at upTo (sqlLexBracket means upTo sits inside an
// unterminated bracket identifier, which sqlCompletionCandidates still
// completes; any other non-normal state suppresses completion entirely
// rather than guessing) plus the offset right after the last top-level ';'
// seen — one of the two inputs statementStartOffset combines with GO-line
// detection to scope FROM/clause analysis to the current statement only —
// and the quoteStart offset (see tokenizeSQLRange).
func tokenizeSQLPrefix(buf []rune, upTo int) ([]sqlToken, sqlLexState, int, int) {
	return tokenizeSQLRange(buf, 0, upTo, false)
}

// tokenizeSQLRange scans buf[from:upTo] into a token stream, always
// starting in sqlLexNormal state — valid both for tokenizeSQLPrefix's
// from-buffer-start scan and for a scan resumed exactly at the cursor
// (statementEndOffset, and sqlCompletionCandidates' own forward scan),
// since callers only ever resume there after confirming the lexer state at
// that offset is already sqlLexNormal (see sqlCompletionCandidates' "inside
// a string/quoted-identifier/comment" bail-out).
//
// stopAtSemicolon changes what the third return value means and, for a
// resumed/forward scan, where scanning stops:
//   - false (tokenizeSQLPrefix's use, and the forward token scan that
//     extends FROM-scope analysis past the cursor): scanning continues
//     through every top-level ';' up to upTo, and the third return is the
//     offset right after the LAST one seen — statementStartOffset's other
//     input.
//   - true (statementEndOffset's use): scanning stops at the FIRST
//     top-level ';', and the third return is that ';'s own offset (or upTo
//     if none was found) — the statement's end boundary.
//
// The fourth return is the offset of the opening '[' or '"' when the final
// state is sqlLexBracket/sqlLexDoubleQuote — how sqlCompletionCandidates
// finds where an unterminated bracket identifier's replace span starts.
// Meaningless (stale or zero) in every other final state.
func tokenizeSQLRange(buf []rune, from, upTo int, stopAtSemicolon bool) ([]sqlToken, sqlLexState, int, int) {
	// Roughly one token per 8 runes of SQL, so the append loop below stops
	// re-copying a large script's token stream on every keystroke. An
	// estimate only — append still grows it if the guess is low.
	tokens := make([]sqlToken, 0, (upTo-from)/8+16)
	state := sqlLexNormal
	quoteStart := 0
	semiStart := from
	i := from
	for i < upTo {
		c := buf[i]
		switch state {
		case sqlLexLineComment:
			if c == '\n' {
				state = sqlLexNormal
			}
			i++
			continue
		case sqlLexBlockComment:
			if c == '*' && i+1 < upTo && buf[i+1] == '/' {
				state = sqlLexNormal
				i += 2
			} else {
				i++
			}
			continue
		case sqlLexSingleQuote:
			if c == '\'' {
				if i+1 < upTo && buf[i+1] == '\'' {
					i += 2
					continue
				}
				state = sqlLexNormal
				i++
				continue
			}
			i++
			continue
		case sqlLexDoubleQuote:
			if c == '"' {
				if i+1 < upTo && buf[i+1] == '"' {
					i += 2
					continue
				}
				tokens = append(tokens, sqlToken{kind: sqlTokIdent, text: string(buf[quoteStart+1 : i]), start: quoteStart})
				state = sqlLexNormal
				i++
				continue
			}
			i++
			continue
		case sqlLexBracket:
			if c == ']' {
				if i+1 < upTo && buf[i+1] == ']' {
					i += 2
					continue
				}
				tokens = append(tokens, sqlToken{kind: sqlTokIdent, text: string(buf[quoteStart+1 : i]), start: quoteStart})
				state = sqlLexNormal
				i++
				continue
			}
			i++
			continue
		}

		// state == sqlLexNormal
		switch {
		case c == '-' && i+1 < upTo && buf[i+1] == '-':
			state = sqlLexLineComment
			i += 2
		case c == '/' && i+1 < upTo && buf[i+1] == '*':
			state = sqlLexBlockComment
			i += 2
		case c == '\'':
			state = sqlLexSingleQuote
			i++
		case c == '"':
			state = sqlLexDoubleQuote
			quoteStart = i
			i++
		case c == '[':
			state = sqlLexBracket
			quoteStart = i
			i++
		case c == '.':
			tokens = append(tokens, sqlToken{kind: sqlTokDot, start: i})
			i++
		case c == ',':
			tokens = append(tokens, sqlToken{kind: sqlTokComma, start: i})
			i++
		case c == '(':
			tokens = append(tokens, sqlToken{kind: sqlTokParenOpen, start: i})
			i++
		case c == ')':
			tokens = append(tokens, sqlToken{kind: sqlTokParenClose, start: i})
			i++
		case c == ';':
			if stopAtSemicolon {
				return tokens, state, i, quoteStart
			}
			semiStart = i + 1
			i++
		case core.IsWordRune(c):
			start := i
			for i < upTo && core.IsWordRune(buf[i]) {
				i++
			}
			// The keyword test runs before the word is materialised, and
			// allocates nothing: a keyword token borrows the table's own
			// canonical spelling, so only identifiers pay for a string. This
			// loop runs over every word in the buffer on every keystroke
			// while the completion popup is open, and the previous
			// string()+ToUpper()+ToUpper() cost two or three allocations per
			// word.
			if kw, ok := sqlKeywordCanonical(buf, start, i); ok {
				tokens = append(tokens, sqlToken{kind: sqlTokKeyword, text: kw, start: start})
			} else {
				tokens = append(tokens, sqlToken{kind: sqlTokIdent, text: string(buf[start:i]), start: start})
			}
		default:
			i++ // whitespace, operators, semicolons, @/# sigils, numeric literals, ...
		}
	}
	if stopAtSemicolon {
		return tokens, state, upTo, quoteStart
	}
	return tokens, state, semiStart, quoteStart
}

// statementEndOffset finds where the statement containing the cursor ends —
// the next top-level ';' at or after upTo, the start of the next bare "GO"
// batch-separator line after cursorRow, or len(buf), whichever comes first.
// Combined with statementStartOffset, this lets FROM-scope/clause analysis
// see the whole current statement regardless of where in it the cursor
// sits — a table named in "SELECT | FROM Customers c" resolves the same as
// one already fully typed above the cursor.
func statementEndOffset(lines [][]rune, buf []rune, cursorRow, upTo int) int {
	end := len(buf)
	if _, _, stop, _ := tokenizeSQLRange(buf, upTo, len(buf), true); stop < end {
		end = stop
	}
	for row := cursorRow + 1; row < len(lines); row++ {
		if strings.EqualFold(strings.TrimSpace(string(lines[row])), "GO") {
			if goStart := offsetForCursor(lines, row, 0); goStart < end {
				end = goStart
			}
			break
		}
	}
	return end
}

// statementStartOffset combines the last top-level ';' tokenizeSQLPrefix
// saw (semiStart) with the nearest bare "GO" batch-separator line strictly
// above cursorRow, and returns whichever is later — the same two
// boundaries controls.Editor.SelectStatementAtCursor recognises (see
// tuikit/controls/sql_statement.go's isGoSeparatorLine), reimplemented
// here in the simpler form completion context needs: no repeat-count or
// trailing-comment parsing, just "this line is exactly GO".
func statementStartOffset(lines [][]rune, cursorRow int, semiStart int) int {
	start := semiStart
	for i := 0; i < cursorRow && i < len(lines); i++ {
		if strings.EqualFold(strings.TrimSpace(string(lines[i])), "GO") {
			if goStart := offsetForCursor(lines, i+1, 0); goStart > start {
				start = goStart
			}
		}
	}
	return start
}

// tokensFrom returns the suffix of tokens (already in ascending start
// order) beginning at the first one whose start is >= from.
func tokensFrom(tokens []sqlToken, from int) []sqlToken {
	for i, t := range tokens {
		if t.start >= from {
			return tokens[i:]
		}
	}
	return nil
}

// maxSQLKeywordLen bounds sqlKeywordCanonical's stack buffer, so it has to
// be a constant. The longest entries in sqlKeywordList are 10 characters
// ("REFERENCES", "CONSTRAINT"); the headroom means adding a keyword doesn't
// usually need touching this, and TestKeywordsFitCanonicalScratch fails
// loudly if one ever exceeds it.
const maxSQLKeywordLen = 24

// sqlKeywordCanon is the lookup built from sqlKeywordList, mapping each
// keyword to itself. Handing back the map's own key is what lets a keyword
// token carry a canonical string without allocating one; membership tests
// (bracketIfNeeded) read the same map, so there is only ever one.
var sqlKeywordCanon = func() map[string]string {
	m := make(map[string]string, len(sqlKeywordList))
	for _, k := range sqlKeywordList {
		m[k] = k
	}
	return m
}()

// isSQLKeyword reports whether an already-uppercased word is a keyword.
func isSQLKeyword(upper string) bool {
	_, ok := sqlKeywordCanon[upper]
	return ok
}

// sqlKeywordCanonical reports whether buf[start:end] is a SQL keyword and,
// if so, returns its canonical uppercase spelling — without allocating. The
// word is ASCII-uppercased into a stack array for the lookup (Go compiles a
// map index on string(byteSlice) without copying), and the returned string
// is the table's own key rather than a fresh one.
//
// Anything longer than the longest keyword, or holding a non-ASCII rune, is
// rejected outright: neither can match, and skipping them keeps the
// stack array small and the fold trivially correct (ASCII case folding has
// none of Unicode's special cases).
func sqlKeywordCanonical(buf []rune, start, end int) (string, bool) {
	n := end - start
	if n <= 0 || n > maxSQLKeywordLen {
		return "", false
	}
	var scratch [maxSQLKeywordLen]byte
	for i := 0; i < n; i++ {
		c := buf[start+i]
		if c >= utf8.RuneSelf {
			return "", false
		}
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		scratch[i] = byte(c)
	}
	kw, ok := sqlKeywordCanon[string(scratch[:n])]
	return kw, ok
}

// sqlKeywordList recognises enough T-SQL clause/reserved words for clause
// detection, FROM-scope parsing, and deciding when a candidate name needs
// bracket-quoting — not the engine's full reserved-word list. It is the
// single place a keyword is declared; sqlKeywordCanon is derived from it.
var sqlKeywordList = []string{
	"SELECT", "FROM", "WHERE", "JOIN", "INNER", "LEFT", "RIGHT", "FULL",
	"OUTER", "CROSS", "ON", "GROUP", "ORDER", "BY", "HAVING", "INSERT",
	"INTO", "VALUES", "UPDATE", "SET", "DELETE", "TRUNCATE", "TABLE",
	"AS", "AND", "OR", "NOT", "NULL", "IS", "IN", "EXISTS", "BETWEEN",
	"LIKE", "DISTINCT", "TOP", "UNION", "EXCEPT", "INTERSECT", "ALL",
	"CASE", "WHEN", "THEN", "ELSE", "END", "CAST", "CONVERT", "DECLARE",
	"EXEC", "EXECUTE", "PROCEDURE", "FUNCTION", "VIEW", "INDEX", "PRIMARY",
	"KEY", "FOREIGN", "REFERENCES", "DEFAULT", "CHECK", "CONSTRAINT",
	"ALTER", "DROP", "CREATE", "WITH", "MERGE",
}
