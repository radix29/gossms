package sqlparse

import (
	"unicode/utf8"

	"github.com/radix29/gossms/internal/tuikit/sqltext"
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
	// TokenStar is '*'. A token only because the select-list parser must tell
	// "SELECT *" and "SELECT a.*" from a named column; other operators are dropped.
	TokenStar
)

type Token struct {
	Kind  TokenKind
	Text  string // TokenIdent: unwrapped, unescaped name; TokenKeyword: uppercased
	Start int    // rune offset into the flattened buffer

	// Quoted marks a [bracketed] or "quoted" identifier. Its Text can spell a
	// reserved word ("[FOR]") that is still a name, never the clause word.
	Quoted bool
}

// LexState is the lexer's mode at the end of a scan: sqltext.Mode under the
// names this package's callers use.
type LexState = sqltext.Mode

const (
	LexNormal       = sqltext.ModeNormal
	LexLineComment  = sqltext.ModeLineComment
	LexBlockComment = sqltext.ModeBlockComment
	LexSingleQuote  = sqltext.ModeString
	LexBracket      = sqltext.ModeBracket
	LexDoubleQuote  = sqltext.ModeQuoted
)

// FlattenLinesInto joins a multi-line buffer into one rune slice with '\n'
// separators, so the tokenizer scans linearly and a multi-line comment or
// string literal falls out of the same state machine.
//
// It reuses dst's capacity when big enough, else sizes a fresh allocation up
// front. This runs on every keystroke while the completion popup is open:
// callers keep dst across keystrokes so a large script doesn't copy itself each
// time, and growing a nil slice re-copies the buffer a dozen times.
//
// The result borrows dst, valid only until the next call with the same dst;
// every consumer copies what it keeps (a Token holds a string).
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
		off += min(max(col, 0), len(lines[row]))
	}
	return off
}

// TokenizeRange scans buf[from:upTo] into a token stream, starting in LexNormal
// state: valid from the buffer start, and at any offset a previous scan reached
// in that state (a ';' or "GO" boundary, a statement start).
//
// The second return is the lexer's state on reaching upTo: LexBracket means
// upTo sits inside an unterminated bracket identifier, which completion still
// completes; any other non-normal state suppresses completion.
//
// stopAtSemicolon changes the third return and where scanning stops:
//   - false (a whole-prefix scan, and the forward scan extending FROM-scope
//     analysis past the cursor): scanning continues through every top-level
//     ';' up to upTo, and the third return is the offset right after the LAST
//     one, which ScanPrefix combines with GO-line detection to scope analysis
//     to the current statement.
//   - true (NarrowStatementForward): scanning stops at the FIRST top-level ';',
//     and the third return is its offset, or upTo if none.
//
// The fourth return is the offset of the opening '[' or '"' when the final
// state is LexBracket/LexDoubleQuote (where the replace span starts);
// meaningless otherwise.
func TokenizeRange(buf []rune, from, upTo int, stopAtSemicolon bool) ([]Token, LexState, int, int) {
	// Estimate: roughly one token per 8 runes of SQL, so the append loop doesn't
	// re-copy a large script's token stream on every keystroke.
	tokens := make([]Token, 0, (upTo-from)/8+16)
	r := lexSQL(buf, from, upTo, stopAtSemicolon, &tokens, goScan{}, nil, nil)
	return tokens, r.state, r.boundary, r.quoteStart
}

// goScan bounds which lines lexSQL considers candidate "GO" separators: only
// one whose first rune sits in [lo, hi). The zero value disables GO detection
// (a tokens-only scan). The bound keeps the cursor's own row out of the prefix
// scan, and rows at or above it out of the forward scan.
type goScan struct{ lo, hi int }

func (g goScan) enabled() bool         { return g.hi > g.lo }
func (g goScan) covers(start int) bool { return start >= g.lo && start < g.hi }

// lexResult is what one pass of lexSQL learned about the text it walked.
type lexResult struct {
	// state is the lexer state on reaching upTo (or the stopping ';').
	state LexState
	// boundary is the offset right after the last top-level ';' seen, or (with
	// stopAtSemicolon) the first such ';' offset. See TokenizeRange.
	boundary int
	// quoteStart is the offset of the opening '[' or '"' when state is
	// LexBracket/LexDoubleQuote; meaningless otherwise.
	quoteStart int
	// firstGo is the offset of the first bare "GO" separator line inside the scan's
	// goScan bounds, or -1. lastGo is the offset of the line *after* the last such
	// line, or 0: the same batch boundary from the two directions callers need.
	firstGo int
	lastGo  int
}

// lexSQL is the single walk behind every scan in this file, over sqltext.Next:
// the lexer the executor splits batches by and the editor selects statements
// and colours text by.
//
// tokens, when non-nil, receives a token per identifier/keyword/punctuation.
// nil lexes without materialising anything, which the prefix pass wants:
// identifier tokens are a scan's only allocating part.
//
// gs, when enabled, is where bare "GO" separators are recognised. That must
// happen inside the lexer's walk, not a separate textual pass: a "GO" alone on
// a line inside a block comment, string literal or bracketed identifier is not
// a separator, and treating it as one scopes completion to the wrong statement.
//
// onBoundary, when non-nil, is called once per batch boundary crossed, in
// ascending offset order, with the offset a scan may resume at in LexNormal
// state and whether a "GO" line established it rather than a top-level ';'.
// lexResult keeps only the last of each, all a cold scan needs; the sink is how
// PrefixCache collects the rest, to restart a later scan from the last boundary
// below an edit.
//
// onLine, when non-nil, is called at every line start reached in LexNormal
// (from itself included, when it begins a line) with goNext the start of the
// line after it when it is a "GO" separator inside gs, or -1. These are the
// positions BatchCache can resume a scan at, or resynchronise with a previous
// pass, without saving lexer state. Returning true stops the walk right there,
// before the line is lexed: the result then describes [from, start) and the
// state is LexNormal.
//
// Every scan starts in LexNormal: from is the buffer start, a ';' or "GO"
// boundary, a statement start or a line start reached in that state.
func lexSQL(buf []rune, from, upTo int, stopAtSemicolon bool, tokens *[]Token, gs goScan, onBoundary func(off int, isGo bool), onLine func(start, goNext int) bool) lexResult {
	var st sqltext.State
	quoteStart := 0
	semiStart := from
	firstGo, lastGo := -1, 0
	// Called at every offset that both begins a line and is reached in LexNormal
	// state: the only positions a separator can occupy.
	noteGoLine := func(start int) {
		goNext := -1
		if gs.enabled() && gs.covers(start) {
			if next, _, ok := sqltext.GoSeparatorAt(buf, start, upTo); ok {
				goNext = next
				if firstGo < 0 {
					firstGo = start
				}
				lastGo = next
				if onBoundary != nil {
					onBoundary(next, true)
				}
			}
		}
		// Every call site moves on to start straight after this, so pulling upTo back
		// to it ends the walk at the next token.
		if onLine != nil && onLine(start, goNext) {
			upTo = start
		}
	}
	if from == 0 || (from > 0 && buf[from-1] == '\n') {
		noteGoLine(from)
	}
	emit := func(k TokenKind, start int) {
		if tokens != nil {
			*tokens = append(*tokens, Token{Kind: k, Start: start})
		}
	}
	// emitIdent takes slice bounds rather than a finished Token so the string
	// conversion happens only for a token actually kept: as an argument,
	// string(buf[lo:hi]) would be evaluated before emit could decline it,
	// allocating per identifier even on a tokens == nil pass.
	emitIdent := func(start, lo, hi int) {
		if tokens != nil {
			*tokens = append(*tokens, Token{Kind: TokenIdent, Text: string(buf[lo:hi]), Start: start})
		}
	}
	for i := from; i < upTo; {
		var t sqltext.Token
		t, st = sqltext.Next(buf, i, upTo, st)
		if t.Kind == sqltext.KindEnd {
			break
		}
		i = t.End
		switch t.Kind {
		case sqltext.KindNewline:
			noteGoLine(i)
		case sqltext.KindQuotedIdent:
			// Unclosed at upTo, it is the identifier the cursor sits in, whose replace span
			// starts at its opening delimiter; it is no token.
			if st.Mode != sqltext.ModeNormal {
				quoteStart = t.Start
				break
			}
			if tokens != nil {
				*tokens = append(*tokens, Token{Kind: TokenIdent, Text: unquoteIdent(buf[t.Start+1:t.End-1], buf[t.End-1]), Start: t.Start, Quoted: true})
			}
		case sqltext.KindWord:
			if buf[t.Start] == '#' || buf[t.Start] == '@' {
				// A temp table (#t, ##t), variable or table variable (@t), or built-in global
				// (@@ROWCOUNT): the sigil is part of the name. Dropping it makes "FROM #Orders"
				// read as the catalog table Orders (offering its columns) and "@t" and a real
				// table t indistinguishable. Never a keyword: a sigil-prefixed word is a name.
				emitIdent(t.Start, t.Start, t.End)
				break
			}
			// Classification is pure and unused when nothing is collected, so a
			// non-collecting pass skips it (the hottest branch, hit once per word of the
			// prefix).
			if tokens == nil {
				break
			}
			// The keyword test runs before the word is materialised and allocates nothing:
			// a keyword token borrows the table's canonical spelling, so only identifiers
			// pay for a string.
			if kw, ok := sqlKeywordCanonical(buf, t.Start, t.End); ok {
				*tokens = append(*tokens, Token{Kind: TokenKeyword, Text: kw, Start: t.Start})
			} else {
				emitIdent(t.Start, t.Start, t.End)
			}
		case sqltext.KindNumber:
			emitIdent(t.Start, t.Start, t.End)
		case sqltext.KindPunct:
			switch buf[t.Start] {
			case '.':
				emit(TokenDot, t.Start)
			case ',':
				emit(TokenComma, t.Start)
			case '(':
				emit(TokenParenOpen, t.Start)
			case ')':
				emit(TokenParenClose, t.Start)
			case '*':
				emit(TokenStar, t.Start)
			case ';':
				if stopAtSemicolon {
					return lexResult{LexNormal, t.Start, quoteStart, firstGo, lastGo}
				}
				semiStart = t.End
				if onBoundary != nil {
					onBoundary(semiStart, false)
				}
			}
		}
	}
	if stopAtSemicolon {
		return lexResult{st.Mode, upTo, quoteStart, firstGo, lastGo}
	}
	return lexResult{st.Mode, semiStart, quoteStart, firstGo, lastGo}
}

// unquoteIdent is a quoted identifier's body with each doubled closing
// delimiter ("]]" in [a]]b], `""` in "a""b") collapsed to one, which is the
// name the catalog holds.
func unquoteIdent(body []rune, closer rune) string {
	i := 0
	for i < len(body) && body[i] != closer {
		i++
	}
	if i == len(body) {
		return string(body)
	}
	out := make([]rune, 0, len(body)-1)
	for i = 0; i < len(body); i++ {
		out = append(out, body[i])
		if body[i] == closer && i+1 < len(body) && body[i+1] == closer {
			i++
		}
	}
	return string(out)
}

// PrefixScan is everything the query editor's completion provider needs to
// know about the text before the cursor.
type PrefixScan struct {
	// Tokens covers the cursor's own statement only, ascending by Start.
	Tokens     []Token
	State      LexState
	BatchStart int
	QuoteStart int

	// GoStart is where the cursor's GO-delimited batch begins: the line after the
	// last real "GO" above it, or 0. BatchStart is the later of this and the last
	// top-level ';'. Only tests read it, since temp tables carry across GO;
	// BatchCache's tests check its batch start against it.
	GoStart int
}

// TokensFrom returns the suffix of tokens (already in ascending start order)
// beginning at the first one whose start is >= from.
func TokensFrom(tokens []Token, from int) []Token {
	for i, t := range tokens {
		if t.Start >= from {
			return tokens[i:]
		}
	}
	return nil
}

// maxSQLKeywordLen bounds sqlKeywordCanonical's stack buffer, so it must be a
// constant. sqlKeywordList's longest entries are 10 characters;
// TestKeywordsFitCanonicalScratch fails if one exceeds this.
const maxSQLKeywordLen = 24

// sqlKeywordCanon maps each keyword from sqlKeywordList to itself: handing back
// the map's own key lets a keyword token carry a canonical string without
// allocating.
var sqlKeywordCanon = func() map[string]string {
	m := make(map[string]string, len(sqlKeywordList))
	for _, k := range sqlKeywordList {
		m[k] = k
	}
	return m
}()

// sqlKeywordCanonical reports whether buf[start:end] is a SQL keyword and, if
// so, returns its canonical uppercase spelling without allocating. The word is
// ASCII-uppercased into a stack array for the lookup (Go compiles a map index
// on string(byteSlice) without copying), and the result is the table's own key.
//
// A word longer than the longest keyword or holding a non-ASCII rune is
// rejected outright: neither can match, and it keeps the array small and the
// fold trivially correct.
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

// sqlKeywordList holds the T-SQL words the tokenizer marks TokenKeyword: enough
// for clause detection and FROM-scope parsing, not the engine's reserved-word
// list. Whether a name needs bracket-quoting is gosmo.IsReservedKeyword's
// question; the two differ on purpose (CAST and APPLY are clause words here but
// legal bare identifiers; USER and DESC the reverse). The one place a keyword
// is declared; sqlKeywordCanon derives from it.
var sqlKeywordList = []string{
	"SELECT", "FROM", "WHERE", "JOIN", "INNER", "LEFT", "RIGHT", "FULL",
	"OUTER", "CROSS", "ON", "GROUP", "ORDER", "BY", "HAVING", "INSERT",
	"INTO", "VALUES", "UPDATE", "SET", "DELETE", "TRUNCATE", "TABLE",
	"AS", "AND", "OR", "NOT", "NULL", "IS", "IN", "EXISTS", "BETWEEN",
	"LIKE", "DISTINCT", "TOP", "UNION", "EXCEPT", "INTERSECT", "ALL",
	"CASE", "WHEN", "THEN", "ELSE", "END", "CAST", "CONVERT", "DECLARE",
	"EXEC", "EXECUTE", "PROCEDURE", "FUNCTION", "VIEW", "INDEX", "PRIMARY",
	"KEY", "FOREIGN", "REFERENCES", "DEFAULT", "CHECK", "CONSTRAINT",
	"ALTER", "DROP", "CREATE", "WITH", "MERGE", "APPLY",
}
