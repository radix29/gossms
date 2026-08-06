package tui

import (
	"unicode"
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

// flattenLinesInto joins a multi-line buffer into one rune slice with '\n'
// separators, so the tokenizer can scan linearly without juggling
// (row, col) pairs — a comment or string literal spanning several lines
// then falls out of the same state machine for free.
//
// It writes into dst's existing capacity when that is big enough, and sizes a
// fresh allocation up front otherwise. Both matter: this runs on every
// keystroke while the completion popup is open, so the caller keeps dst across
// keystrokes to stop a large script allocating (and handing the GC) a fresh
// copy of itself on every one, and growing a nil slice to a large script's
// size re-copies the whole buffer a dozen times over.
//
// The result borrows dst, so it stays valid only until the next call with the
// same dst — every consumer here copies what it keeps (a sqlToken holds a
// string, not a slice of the buffer).
func flattenLinesInto(dst []rune, lines [][]rune) []rune {
	n := 0
	for _, l := range lines {
		n += len(l) + 1
	}
	if n > 0 {
		n-- // separators go between lines, not after the last one
	}
	buf := dst[:0]
	if cap(buf) < n {
		buf = make([]rune, 0, n)
	}
	for i, l := range lines {
		if i > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, l...)
	}
	return buf
}

// offsetForCursor converts an Editor (row, col) into an offset into
// flattenLinesInto's output.
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

// tokenizeSQLRange scans buf[from:upTo] into a token stream, always
// starting in sqlLexNormal state — valid both for a scan from the buffer
// start and for one resumed exactly at the cursor (statementEndOffset, and
// sqlCompletionCandidates' own forward scan), since callers only ever resume
// there after confirming the lexer state at that offset is already
// sqlLexNormal (see sqlCompletionCandidates' "inside a string/quoted-
// identifier/comment" bail-out).
//
// The second return is the lexer's state on reaching upTo: sqlLexBracket
// means upTo sits inside an unterminated bracket identifier, which
// sqlCompletionCandidates still completes, while any other non-normal state
// suppresses completion entirely rather than guessing.
//
// stopAtSemicolon changes what the third return value means and, for a
// resumed/forward scan, where scanning stops:
//   - false (a whole-prefix scan, and the forward token scan that extends
//     FROM-scope analysis past the cursor): scanning continues through every
//     top-level ';' up to upTo, and the third return is the offset right
//     after the LAST one seen — one of the two boundaries
//     scanCompletionPrefix combines with GO-line detection to scope
//     FROM/clause analysis to the current statement only.
//   - true (statementEndOffset's use): scanning stops at the FIRST
//     top-level ';', and the third return is that ';'s own offset (or upTo
//     if none was found) — the statement's end boundary.
//
// The fourth return is the offset of the opening '[' or '"' when the final
// state is sqlLexBracket/sqlLexDoubleQuote — how sqlCompletionCandidates
// finds where an unterminated bracket identifier's replace span starts.
// Meaningless (stale or zero) in every other final state.
func tokenizeSQLRange(buf []rune, from, upTo int, stopAtSemicolon bool) ([]sqlToken, sqlLexState, int, int) {
	return tokenizeSQLRangeFrom(buf, from, upTo, stopAtSemicolon, sqlLexNormal)
}

// tokenizeSQLRangeFrom is tokenizeSQLRange with an explicit starting lexer
// state, for resuming a scan at an offset whose state a previous pass already
// established. Every caller passes sqlLexNormal today — scanCompletionPrefix
// only ever resumes at a normal-state batch boundary — but the parameter is
// what makes that a stated precondition rather than a coincidence.
func tokenizeSQLRangeFrom(buf []rune, from, upTo int, stopAtSemicolon bool, initial sqlLexState) ([]sqlToken, sqlLexState, int, int) {
	// Roughly one token per 8 runes of SQL, so the append loop below stops
	// re-copying a large script's token stream on every keystroke. An
	// estimate only — append still grows it if the guess is low.
	tokens := make([]sqlToken, 0, (upTo-from)/8+16)
	r := lexSQL(buf, from, upTo, stopAtSemicolon, initial, &tokens, goScan{})
	return tokens, r.state, r.boundary, r.quoteStart
}

// goScan bounds which lines lexSQL considers as candidate "GO" batch
// separators: only one whose first rune sits in [lo, hi). The zero value
// disables GO detection, which is what every scan that only wants tokens
// passes.
//
// The bound is what keeps the cursor's own row out of the prefix scan and
// keeps rows at or above it out of the forward scan — both used to be
// expressed as the loop bounds of a separate pass over lines.
type goScan struct{ lo, hi int }

func (g goScan) enabled() bool         { return g.hi > g.lo }
func (g goScan) covers(start int) bool { return start >= g.lo && start < g.hi }

// lexResult is what one pass of lexSQL learned about the text it walked.
type lexResult struct {
	// state is the lexer state on reaching upTo (or the stopping ';').
	state sqlLexState
	// boundary is the offset right after the last top-level ';' seen, or —
	// when stopAtSemicolon — the first such ';' offset. See tokenizeSQLRange.
	boundary int
	// quoteStart is the offset of the opening '[' or '"' when state is
	// sqlLexBracket/sqlLexDoubleQuote; meaningless otherwise.
	quoteStart int
	// firstGo is the offset of the first bare "GO" separator line inside the
	// scan's goScan bounds, or -1 when there was none. lastGo is the offset of
	// the line *after* the last such line, or 0 when there was none — i.e. the
	// batch boundary each one establishes, from the two directions the two
	// callers need.
	firstGo int
	lastGo  int
}

// lexSQL is the single T-SQL state machine behind every scan in this file.
//
// tokens, when non-nil, receives a token per identifier/keyword/punctuation
// found. Passing nil lexes without materialising anything, which is what the
// prefix pass wants: identifier tokens are the only allocating part of a scan,
// and a pass that exists purely to locate the current statement throws all of
// them away.
//
// gs, when enabled, also makes this the place bare "GO" batch separators are
// recognised. That has to happen inside the state machine rather than in a
// separate textual pass over the lines: a "GO" alone on its own line inside a
// block comment, a string literal or a bracketed identifier is not a batch
// separator, and treating it as one scoped completion to the wrong statement —
// `SELECT * FROM dbo.Patients p` followed by a commented-out GO left the alias
// `p` out of scope, so `p.` offered nothing.
func lexSQL(buf []rune, from, upTo int, stopAtSemicolon bool, initial sqlLexState, tokens *[]sqlToken, gs goScan) lexResult {
	state := initial
	quoteStart := 0
	semiStart := from
	firstGo, lastGo := -1, 0
	// Called at every offset that both begins a line and is reached in
	// sqlLexNormal state — the only positions a separator can occupy.
	noteGoLine := func(start int) {
		if !gs.enabled() || !gs.covers(start) {
			return
		}
		next, ok := goSeparatorLineAt(buf, start, upTo)
		if !ok {
			return
		}
		if firstGo < 0 {
			firstGo = start
		}
		lastGo = next
	}
	if from == 0 || (from > 0 && buf[from-1] == '\n') {
		noteGoLine(from)
	}
	// A token starting before from is never this range's to report. Only one
	// shape can produce that: a quoted identifier opened before from and
	// closed inside it, whose token is anchored at the (unseen) opening
	// bracket/quote — so quoteStart is still 0 here. Dropping it matches what
	// a scan of the whole buffer followed by tokensFrom(tokens, from) yields.
	keep := func(start int) bool { return tokens != nil && start >= from }
	emit := func(t sqlToken) {
		if keep(t.start) {
			*tokens = append(*tokens, t)
		}
	}
	// emitIdent exists so the string conversion happens only for a token that
	// is actually kept. Passing the slice bounds instead of a finished
	// sqlToken is the whole point: as an argument, string(buf[lo:hi]) is
	// evaluated before emit can decline it, which allocated a string per
	// identifier even on a tokens == nil pass — exactly the cost this scan is
	// arranged to avoid.
	emitIdent := func(start, lo, hi int) {
		if keep(start) {
			*tokens = append(*tokens, sqlToken{kind: sqlTokIdent, text: string(buf[lo:hi]), start: start})
		}
	}
	i := from
	for i < upTo {
		c := buf[i]
		switch state {
		case sqlLexLineComment:
			if c == '\n' {
				// The next line starts in normal state, so it can be a
				// separator — a "-- note" line above a GO is ordinary SQL.
				state = sqlLexNormal
				noteGoLine(i + 1)
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
				emitIdent(quoteStart, quoteStart+1, i)
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
				emitIdent(quoteStart, quoteStart+1, i)
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
			emit(sqlToken{kind: sqlTokDot, start: i})
			i++
		case c == ',':
			emit(sqlToken{kind: sqlTokComma, start: i})
			i++
		case c == '(':
			emit(sqlToken{kind: sqlTokParenOpen, start: i})
			i++
		case c == ')':
			emit(sqlToken{kind: sqlTokParenClose, start: i})
			i++
		case c == ';':
			if stopAtSemicolon {
				return lexResult{state, i, quoteStart, firstGo, lastGo}
			}
			semiStart = i + 1
			i++
		case core.IsWordRune(c):
			start := i
			for i < upTo && core.IsWordRune(buf[i]) {
				i++
			}
			// Classifying the word is pure and its result unused when nothing
			// is being collected, so a non-collecting pass skips it — this is
			// the single hottest branch in the scan, once per word in the whole
			// prefix.
			if !keep(start) {
				break
			}
			// The keyword test runs before the word is materialised, and
			// allocates nothing: a keyword token borrows the table's own
			// canonical spelling, so only identifiers pay for a string. This
			// loop runs over every word in the buffer on every keystroke
			// while the completion popup is open, and the previous
			// string()+ToUpper()+ToUpper() cost two or three allocations per
			// word.
			if kw, ok := sqlKeywordCanonical(buf, start, i); ok {
				emit(sqlToken{kind: sqlTokKeyword, text: kw, start: start})
			} else {
				emitIdent(start, start, i)
			}
		default:
			// whitespace, operators, semicolons, @/# sigils, numeric literals, ...
			if c == '\n' {
				noteGoLine(i + 1)
			}
			i++
		}
	}
	if stopAtSemicolon {
		return lexResult{state, upTo, quoteStart, firstGo, lastGo}
	}
	return lexResult{state, semiStart, quoteStart, firstGo, lastGo}
}

// statementEndOffset finds where the statement containing the cursor ends —
// the next top-level ';' at or after upTo, the start of the next bare "GO"
// batch-separator line after cursorRow, or len(buf), whichever comes first.
// Combined with the batch start scanCompletionPrefix finds, this lets
// FROM-scope/clause analysis see the whole current statement regardless of
// where in it the cursor sits — a table named in "SELECT | FROM Customers c"
// resolves the same as one already fully typed above the cursor.
//
// Both boundaries come out of the one lexer pass, so a ';' or a "GO" that is
// really inside a comment or a literal ends nothing.
func statementEndOffset(lines [][]rune, buf []rune, cursorRow, upTo int) int {
	// The forward scan resumes at the cursor, which callers only do after
	// confirming the lexer is in sqlLexNormal there. Only rows strictly below
	// the cursor's own can end its statement.
	r := lexSQL(buf, upTo, len(buf), true, sqlLexNormal, nil,
		goScan{lo: offsetForCursor(lines, cursorRow+1, 0), hi: len(buf)})
	end := r.boundary
	if r.firstGo >= 0 && r.firstGo < end {
		end = r.firstGo
	}
	return end
}

// goSeparatorLineAt reports whether the line beginning at buf[start] — running
// to the next '\n' or to limit — is a bare "GO" batch separator (exactly "GO",
// case-insensitively, apart from surrounding whitespace), and returns the
// offset of the line after it.
//
// The scan bails on the first rune that cannot be part of one, so an ordinary
// line costs a rune or two rather than a walk to its end: this is called once
// per line of the whole prefix, on every keystroke while the completion popup
// is open.
//
// Deliberately simpler than controls.Editor.SelectStatementAtCursor's own
// separator rule (tuikit/controls/sql_statement.go), which also accepts a
// repeat count and a trailing line comment. Completion context has no use for
// either.
func goSeparatorLineAt(buf []rune, start, limit int) (int, bool) {
	i := start
	for i < limit && buf[i] != '\n' && unicode.IsSpace(buf[i]) {
		i++
	}
	if i+1 >= limit ||
		(buf[i] != 'G' && buf[i] != 'g') ||
		(buf[i+1] != 'O' && buf[i+1] != 'o') {
		return 0, false
	}
	i += 2
	for i < limit && buf[i] != '\n' && unicode.IsSpace(buf[i]) {
		i++
	}
	if i < limit && buf[i] != '\n' {
		return 0, false
	}
	return min(i+1, limit), true
}

// isGoSeparatorLine applies goSeparatorLineAt's rule to a standalone line.
func isGoSeparatorLine(line []rune) bool {
	_, ok := goSeparatorLineAt(line, 0, len(line))
	return ok
}

// completionPrefixScan is everything sqlCompletionCandidates needs to know
// about the text before the cursor.
type completionPrefixScan struct {
	// tokens covers the cursor's own statement only, ascending by start.
	tokens     []sqlToken
	state      sqlLexState
	batchStart int
	quoteStart int
}

// scanCompletionPrefix locates the statement the cursor sits in and tokenizes
// that statement alone.
//
// Two passes, because the entire prefix must be lexed to know where the
// current statement starts — an unterminated block comment thousands of lines
// up changes the answer — while only the current statement's tokens are ever
// read. The first pass therefore lexes with tokens switched off, which is the
// expensive half: materialising an identifier token allocates a string, and on
// a large script the tokens that get discarded outnumber the kept ones by
// orders of magnitude. The second pass tokenizes from batchStart alone.
func scanCompletionPrefix(lines [][]rune, buf []rune, cursorRow, upTo int) completionPrefixScan {
	// Only "GO" lines strictly above the cursor's own row are separators for
	// the statement the cursor is in — hence the bound at that row's start.
	r := lexSQL(buf, 0, upTo, false, sqlLexNormal, nil,
		goScan{lo: 0, hi: offsetForCursor(lines, cursorRow, 0)})

	// The statement starts at whichever boundary is later: just past the last
	// top-level ';', or the line after the last real "GO". The second pass can
	// resume in sqlLexNormal unconditionally because both are normal-state
	// positions by construction — the lexer only recognises either one there,
	// and a separator line carries nothing that could still be open past it.
	batchStart := max(r.boundary, r.lastGo)
	tokens, _, _, _ := tokenizeSQLRangeFrom(buf, batchStart, upTo, false, sqlLexNormal)
	return completionPrefixScan{
		tokens:     tokens,
		state:      r.state,
		batchStart: batchStart,
		quoteStart: r.quoteStart,
	}
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
