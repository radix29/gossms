package sqlparse

import (
	"unicode"
	"unicode/utf8"

	"github.com/radix29/gossms/internal/tuikit/core"
)

// ---------------------------------------------------------------------------
// Tokenizer
// ---------------------------------------------------------------------------

type TokenKind int

const (
	TokenIdent TokenKind = iota
	TokenKeyword
	TokenDot
	TokenComma
	TokenParenOpen
	TokenParenClose
)

type Token struct {
	Kind  TokenKind
	Text  string // TokenIdent: unwrapped name; TokenKeyword: uppercased
	Start int    // rune offset into the flattened buffer
}

type LexState int

const (
	LexNormal LexState = iota
	LexLineComment
	LexBlockComment
	LexSingleQuote
	LexBracket
	LexDoubleQuote
)

// FlattenLinesInto joins a multi-line buffer into one rune slice with '\n'
// separators, so the tokenizer can scan linearly without juggling (row, col)
// pairs — a comment or string literal spanning several lines then falls out of
// the same state machine.
//
// It reuses dst's capacity when big enough and sizes a fresh allocation up
// front otherwise. Both matter: this runs on every keystroke while the
// completion popup is open, so callers keep dst across keystrokes to stop a
// large script allocating a fresh copy of itself each time, and growing a nil
// slice to that size re-copies the whole buffer a dozen times over.
//
// The result borrows dst, so it is valid only until the next call with the
// same dst — every consumer copies what it keeps (a Token holds a string).
func FlattenLinesInto(dst []rune, lines [][]rune) []rune {
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

// OffsetForCursor converts an Editor (row, col) into an offset into
// FlattenLinesInto's output.
func OffsetForCursor(lines [][]rune, row, col int) int {
	off := 0
	for i := 0; i < row && i < len(lines); i++ {
		off += len(lines[i]) + 1
	}
	if row < len(lines) {
		off += core.Clamp(col, 0, len(lines[row]))
	}
	return off
}

// TokenizeRange scans buf[from:upTo] into a token stream, always starting in
// LexNormal state — valid both from the buffer start and resumed at the
// cursor, since callers only resume there after confirming the state at that
// offset is already LexNormal.
//
// The second return is the lexer's state on reaching upTo: LexBracket means
// upTo sits inside an unterminated bracket identifier, which the completion
// provider still completes; any other non-normal state suppresses completion
// rather than guessing.
//
// stopAtSemicolon changes what the third return means and where scanning
// stops:
//   - false (a whole-prefix scan, and the forward scan extending FROM-scope
//     analysis past the cursor): scanning continues through every top-level
//     ';' up to upTo, and the third return is the offset right after the LAST
//     one — one of the two boundaries ScanPrefix combines with GO-line
//     detection to scope analysis to the current statement.
//   - true (StatementEndOffset): scanning stops at the FIRST top-level ';',
//     and the third return is that ';'s offset, or upTo if none.
//
// The fourth return is the offset of the opening '[' or '"' when the final
// state is LexBracket/LexDoubleQuote — how the completion provider finds where
// an unterminated bracket identifier's replace span starts. Meaningless in
// every other final state.
func TokenizeRange(buf []rune, from, upTo int, stopAtSemicolon bool) ([]Token, LexState, int, int) {
	return TokenizeRangeFrom(buf, from, upTo, stopAtSemicolon, LexNormal)
}

// TokenizeRangeFrom is TokenizeRange with an explicit starting lexer state,
// for resuming a scan at an offset whose state a previous pass established.
// Every caller passes LexNormal — ScanPrefix only resumes at a normal-state
// batch boundary — but the parameter makes that a stated precondition.
func TokenizeRangeFrom(buf []rune, from, upTo int, stopAtSemicolon bool, initial LexState) ([]Token, LexState, int, int) {
	// Roughly one token per 8 runes of SQL, so the append loop stops re-copying
	// a large script's token stream on every keystroke. An estimate only.
	tokens := make([]Token, 0, (upTo-from)/8+16)
	r := lexSQL(buf, from, upTo, stopAtSemicolon, initial, &tokens, goScan{})
	return tokens, r.state, r.boundary, r.quoteStart
}

// goScan bounds which lines lexSQL considers candidate "GO" batch separators:
// only one whose first rune sits in [lo, hi). The zero value disables GO
// detection, which is what a tokens-only scan passes.
//
// The bound keeps the cursor's own row out of the prefix scan and rows at or
// above it out of the forward scan.
type goScan struct{ lo, hi int }

func (g goScan) enabled() bool         { return g.hi > g.lo }
func (g goScan) covers(start int) bool { return start >= g.lo && start < g.hi }

// lexResult is what one pass of lexSQL learned about the text it walked.
type lexResult struct {
	// state is the lexer state on reaching upTo (or the stopping ';').
	state LexState
	// boundary is the offset right after the last top-level ';' seen, or —
	// when stopAtSemicolon — the first such ';' offset. See TokenizeRange.
	boundary int
	// quoteStart is the offset of the opening '[' or '"' when state is
	// LexBracket/LexDoubleQuote; meaningless otherwise.
	quoteStart int
	// firstGo is the offset of the first bare "GO" separator line inside the
	// scan's goScan bounds, or -1 if none. lastGo is the offset of the line
	// *after* the last such line, or 0 if none — the batch boundary each
	// establishes, from the two directions the callers need.
	firstGo int
	lastGo  int
}

// lexSQL is the single T-SQL state machine behind every scan in this file.
//
// tokens, when non-nil, receives a token per identifier/keyword/punctuation.
// nil lexes without materialising anything, which is what the prefix pass
// wants: identifier tokens are a scan's only allocating part, and a pass that
// exists purely to locate the current statement discards all of them.
//
// gs, when enabled, also makes this where bare "GO" batch separators are
// recognised. That has to happen inside the state machine, not in a separate
// textual pass over the lines: a "GO" alone on a line inside a block comment,
// a string literal or a bracketed identifier is not a separator, and treating
// it as one scopes completion to the wrong statement — `SELECT * FROM
// dbo.Patients p` above a commented-out GO left the alias `p` out of scope,
// so `p.` offered nothing.
func lexSQL(buf []rune, from, upTo int, stopAtSemicolon bool, initial LexState, tokens *[]Token, gs goScan) lexResult {
	state := initial
	quoteStart := 0
	semiStart := from
	firstGo, lastGo := -1, 0
	// Called at every offset that both begins a line and is reached in
	// LexNormal state — the only positions a separator can occupy.
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
	// shape produces that: a quoted identifier opened before from and closed
	// inside it, anchored at the unseen opening bracket/quote — so quoteStart
	// is still 0. Dropping it matches a whole-buffer scan followed by
	// TokensFrom(tokens, from).
	keep := func(start int) bool { return tokens != nil && start >= from }
	emit := func(t Token) {
		if keep(t.Start) {
			*tokens = append(*tokens, t)
		}
	}
	// emitIdent takes slice bounds rather than a finished Token so the string
	// conversion happens only for a token actually kept: as an argument,
	// string(buf[lo:hi]) is evaluated before emit can decline it, allocating a
	// string per identifier even on a tokens == nil pass.
	emitIdent := func(start, lo, hi int) {
		if keep(start) {
			*tokens = append(*tokens, Token{Kind: TokenIdent, Text: string(buf[lo:hi]), Start: start})
		}
	}
	i := from
	for i < upTo {
		c := buf[i]
		switch state {
		case LexLineComment:
			if c == '\n' {
				// The next line starts in normal state, so it can be a
				// separator — a "-- note" line above a GO is ordinary SQL.
				state = LexNormal
				noteGoLine(i + 1)
			}
			i++
			continue
		case LexBlockComment:
			if c == '*' && i+1 < upTo && buf[i+1] == '/' {
				state = LexNormal
				i += 2
			} else {
				i++
			}
			continue
		case LexSingleQuote:
			if c == '\'' {
				if i+1 < upTo && buf[i+1] == '\'' {
					i += 2
					continue
				}
				state = LexNormal
				i++
				continue
			}
			i++
			continue
		case LexDoubleQuote:
			if c == '"' {
				if i+1 < upTo && buf[i+1] == '"' {
					i += 2
					continue
				}
				emitIdent(quoteStart, quoteStart+1, i)
				state = LexNormal
				i++
				continue
			}
			i++
			continue
		case LexBracket:
			if c == ']' {
				if i+1 < upTo && buf[i+1] == ']' {
					i += 2
					continue
				}
				emitIdent(quoteStart, quoteStart+1, i)
				state = LexNormal
				i++
				continue
			}
			i++
			continue
		}

		// state == LexNormal
		switch {
		case c == '-' && i+1 < upTo && buf[i+1] == '-':
			state = LexLineComment
			i += 2
		case c == '/' && i+1 < upTo && buf[i+1] == '*':
			state = LexBlockComment
			i += 2
		case c == '\'':
			state = LexSingleQuote
			i++
		case c == '"':
			state = LexDoubleQuote
			quoteStart = i
			i++
		case c == '[':
			state = LexBracket
			quoteStart = i
			i++
		case c == '.':
			emit(Token{Kind: TokenDot, Start: i})
			i++
		case c == ',':
			emit(Token{Kind: TokenComma, Start: i})
			i++
		case c == '(':
			emit(Token{Kind: TokenParenOpen, Start: i})
			i++
		case c == ')':
			emit(Token{Kind: TokenParenClose, Start: i})
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
			// Classification is pure and unused when nothing is collected, so
			// a non-collecting pass skips it — the hottest branch in the
			// scan, hit once per word of the whole prefix.
			if !keep(start) {
				break
			}
			// The keyword test runs before the word is materialised and
			// allocates nothing: a keyword token borrows the table's own
			// canonical spelling, so only identifiers pay for a string.
			if kw, ok := sqlKeywordCanonical(buf, start, i); ok {
				emit(Token{Kind: TokenKeyword, Text: kw, Start: start})
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

// StatementEndOffset finds where the statement containing the cursor ends —
// the next top-level ';' at or after upTo, the start of the next bare "GO"
// line after cursorRow, or len(buf), whichever comes first. Combined with
// ScanPrefix's batch start, this lets FROM-scope/clause analysis see the whole
// statement regardless of where in it the cursor sits: a table named in
// "SELECT | FROM Customers c" resolves the same as one typed above the cursor.
//
// Both boundaries come out of one lexer pass, so a ';' or "GO" inside a
// comment or a literal ends nothing.
func StatementEndOffset(lines [][]rune, buf []rune, cursorRow, upTo int) int {
	// The forward scan resumes at the cursor, which callers only do after
	// confirming the lexer is in LexNormal there. Only rows strictly below
	// the cursor's own can end its statement.
	r := lexSQL(buf, upTo, len(buf), true, LexNormal, nil,
		goScan{lo: OffsetForCursor(lines, cursorRow+1, 0), hi: len(buf)})
	end := r.boundary
	if r.firstGo >= 0 && r.firstGo < end {
		end = r.firstGo
	}
	return end
}

// goSeparatorLineAt reports whether the line beginning at buf[start] — running
// to the next '\n' or to limit — is a bare "GO" batch separator (exactly "GO"
// case-insensitively, apart from surrounding whitespace), and returns the
// offset of the line after it.
//
// The scan bails on the first rune that can't be part of one, so an ordinary
// line costs a rune or two rather than a walk to its end: this runs once per
// line of the whole prefix on every keystroke while the popup is open.
//
// Deliberately simpler than Editor.SelectStatementAtCursor's separator rule,
// which also accepts a repeat count and a trailing line comment. Completion
// context needs neither.
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

// PrefixScan is everything the query editor's completion provider needs to
// know about the text before the cursor.
type PrefixScan struct {
	// Tokens covers the cursor's own statement only, ascending by Start.
	Tokens     []Token
	State      LexState
	BatchStart int
	QuoteStart int
}

// ScanPrefix locates the statement the cursor sits in and tokenizes that
// statement alone.
//
// Two passes, because the entire prefix must be lexed to know where the
// current statement starts — an unterminated block comment thousands of lines
// up changes the answer — while only the current statement's tokens are read.
// The first pass therefore lexes with tokens off, the expensive half:
// materialising an identifier allocates a string, and on a large script the
// discarded tokens outnumber the kept ones by orders of magnitude.
func ScanPrefix(lines [][]rune, buf []rune, cursorRow, upTo int) PrefixScan {
	// Only "GO" lines strictly above the cursor's row separate the statement
	// the cursor is in — hence the bound at that row's start.
	r := lexSQL(buf, 0, upTo, false, LexNormal, nil,
		goScan{lo: 0, hi: OffsetForCursor(lines, cursorRow, 0)})

	// The statement starts at whichever boundary is later: past the last
	// top-level ';', or the line after the last real "GO". The second pass
	// resumes in LexNormal unconditionally because both are normal-state
	// positions by construction — the lexer recognises either only there, and
	// a separator line carries nothing that could still be open past it.
	batchStart := max(r.boundary, r.lastGo)
	tokens, _, _, _ := TokenizeRangeFrom(buf, batchStart, upTo, false, LexNormal)
	return PrefixScan{
		Tokens:     tokens,
		State:      r.state,
		BatchStart: batchStart,
		QuoteStart: r.quoteStart,
	}
}

// TokensFrom returns the suffix of tokens (already in ascending start
// order) beginning at the first one whose start is >= from.
func TokensFrom(tokens []Token, from int) []Token {
	for i, t := range tokens {
		if t.Start >= from {
			return tokens[i:]
		}
	}
	return nil
}

// maxSQLKeywordLen bounds sqlKeywordCanonical's stack buffer, so it must be a
// constant. sqlKeywordList's longest entries are 10 characters; the headroom
// means adding a keyword rarely needs this touched, and
// TestKeywordsFitCanonicalScratch fails loudly if one exceeds it.
const maxSQLKeywordLen = 24

// sqlKeywordCanon maps each keyword from sqlKeywordList to itself. Handing
// back the map's own key lets a keyword token carry a canonical string without
// allocating one; IsKeyword reads the same map, so there is only ever one.
var sqlKeywordCanon = func() map[string]string {
	m := make(map[string]string, len(sqlKeywordList))
	for _, k := range sqlKeywordList {
		m[k] = k
	}
	return m
}()

// IsKeyword reports whether an already-uppercased word is a keyword.
func IsKeyword(upper string) bool {
	_, ok := sqlKeywordCanon[upper]
	return ok
}

// sqlKeywordCanonical reports whether buf[start:end] is a SQL keyword and, if
// so, returns its canonical uppercase spelling without allocating. The word is
// ASCII-uppercased into a stack array for the lookup (Go compiles a map index
// on string(byteSlice) without copying), and the returned string is the
// table's own key.
//
// Anything longer than the longest keyword, or holding a non-ASCII rune, is
// rejected outright: neither can match, and skipping them keeps the array
// small and the fold trivially correct.
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

// sqlKeywordList holds enough T-SQL clause/reserved words for clause
// detection, FROM-scope parsing, and deciding when a candidate name needs
// bracket-quoting — not the engine's full reserved-word list. The single place
// a keyword is declared; sqlKeywordCanon derives from it.
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
