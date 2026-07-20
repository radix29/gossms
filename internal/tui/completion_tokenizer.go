package tui

import (
	"strings"

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
func flattenLines(lines [][]rune) []rune {
	var buf []rune
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
	var tokens []sqlToken
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
			word := string(buf[start:i])
			if sqlKeywords[strings.ToUpper(word)] {
				tokens = append(tokens, sqlToken{kind: sqlTokKeyword, text: strings.ToUpper(word), start: start})
			} else {
				tokens = append(tokens, sqlToken{kind: sqlTokIdent, text: word, start: start})
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

// sqlKeywords recognises enough T-SQL clause/reserved words for clause
// detection, FROM-scope parsing, and deciding when a candidate name needs
// bracket-quoting — not the engine's full reserved-word list.
var sqlKeywords = map[string]bool{
	"SELECT": true, "FROM": true, "WHERE": true, "JOIN": true, "INNER": true,
	"LEFT": true, "RIGHT": true, "FULL": true, "OUTER": true, "CROSS": true,
	"ON": true, "GROUP": true, "ORDER": true, "BY": true, "HAVING": true,
	"INSERT": true, "INTO": true, "VALUES": true, "UPDATE": true, "SET": true,
	"DELETE": true, "TRUNCATE": true, "TABLE": true, "AS": true, "AND": true,
	"OR": true, "NOT": true, "NULL": true, "IS": true, "IN": true,
	"EXISTS": true, "BETWEEN": true, "LIKE": true, "DISTINCT": true, "TOP": true,
	"UNION": true, "EXCEPT": true, "INTERSECT": true, "ALL": true, "CASE": true, "WHEN": true, "THEN": true,
	"ELSE": true, "END": true, "CAST": true, "CONVERT": true, "DECLARE": true,
	"EXEC": true, "EXECUTE": true, "PROCEDURE": true, "FUNCTION": true,
	"VIEW": true, "INDEX": true, "PRIMARY": true, "KEY": true, "FOREIGN": true,
	"REFERENCES": true, "DEFAULT": true, "CHECK": true, "CONSTRAINT": true,
	"ALTER": true, "DROP": true, "CREATE": true, "WITH": true, "MERGE": true,
}
