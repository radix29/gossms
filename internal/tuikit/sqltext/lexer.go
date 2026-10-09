package sqltext

import (
	"unicode"
	"unicode/utf8"
)

// Mode is the lexical context the lexer is in between two tokens.
type Mode uint8

const (
	ModeNormal       Mode = iota
	ModeLineComment       // after "--", up to the end of the line
	ModeBlockComment      // inside "/* ... */", State.Depth levels deep
	ModeString            // inside a '...' literal
	ModeBracket           // inside a [bracketed] identifier
	ModeQuoted            // inside a "quoted" identifier
)

// State is what the lexer carries from one token to the next: its Mode and, in
// a block comment, its nesting depth. T-SQL nests block comments, so
// "/* /* */ GO */" is one comment and its GO no separator. The zero value is
// the state a script starts in.
type State struct {
	Mode  Mode
	Depth int
}

// NextLine is the state the line after one ending in s starts in: a line
// comment ends with its line, everything else carries over. A caller lexing
// line by line passes the line's length as the limit, which leaves an
// unterminated line comment in ModeLineComment; this step closes it.
func (s State) NextLine() State {
	if s.Mode == ModeLineComment {
		return State{}
	}
	return s
}

// Kind is what a Token is.
type Kind uint8

const (
	// KindEnd means Next reached its limit without finding a token.
	KindEnd Kind = iota
	// KindNewline is a '\n' reached in ModeNormal: the next rune begins a line in
	// normal state, the only place a "GO" separator can stand. A newline inside a
	// literal or block comment is part of that token.
	KindNewline
	// KindWord is a keyword or a bare name: a run of word runes not starting with a
	// digit, or one or two of the same sigil ('#' temp table, '@' variable or
	// @@global) followed by word runes. The sigil alone is a word too: it is what
	// the cursor sits on at the first keystroke of "#t". "#@x" is no name, so it
	// lexes as "#" and "@x".
	KindWord
	// KindNumber starts with a digit and runs on through word runes and '.':
	// 42, 1.5, 0x1F, 1e5.
	KindNumber
	// KindString is a '...' literal; a doubled quote stays inside it.
	KindString
	// KindQuotedIdent is a [bracketed] or "quoted" identifier; a doubled
	// closing delimiter stays inside it. buf[Start] tells which.
	KindQuotedIdent
	// KindComment is a "--" comment up to (not including) its '\n', or a
	// "/* ... */" block comment, nested ones included.
	KindComment
	// KindPunct is any other rune that is not white space, one per token.
	KindPunct
)

// Token is one lexeme: buf[Start:End].
//
// A token Next began in ModeNormal starts at its opening delimiter. One it
// resumed (a literal, identifier or comment the previous call left open) starts
// where that call stopped. Either way, a token reaching the limit unclosed
// leaves the returned State in its mode, and Next continues it from there.
type Token struct {
	Kind       Kind
	Start, End int
}

// Next lexes the token at or after buf[i], reading nothing at or past limit,
// and returns it with the state after it. In ModeNormal it first skips white
// space other than '\n'. It returns KindEnd, at limit, when only white space is
// left, and leaves st as it is when i is already at limit.
//
// This is the one T-SQL lexer: SplitBatches, StatementAt, the syntax highlighter
// and IntelliSense's scanner (internal/tui/sqlparse) all use it, so a ';', GO
// line or keyword inside a comment, literal or quoted identifier means the same
// to all four. (Separate state machines drifted: the highlighter coloured
// keywords in [brackets], and a "/*" inside one commented out the document.)
// A caller may lex a flat buffer, newlines included, or one line at a time with
// the State carried across and State.NextLine applied at each line end. The two
// agree: no two-rune delimiter ("--", "/*", "*/", a doubled quote or bracket)
// can span a '\n'.
func Next(buf []rune, i, limit int, st State) (Token, State) {
	if i >= limit {
		return Token{KindEnd, limit, limit}, st
	}
	switch st.Mode {
	case ModeLineComment:
		return lineComment(buf, i, i, limit)
	case ModeBlockComment:
		return blockComment(buf, i, i, limit, st.Depth)
	case ModeString:
		return quoted(buf, i, i, limit, '\'', KindString, ModeString)
	case ModeBracket:
		return quoted(buf, i, i, limit, ']', KindQuotedIdent, ModeBracket)
	case ModeQuoted:
		return quoted(buf, i, i, limit, '"', KindQuotedIdent, ModeQuoted)
	}
	for i < limit {
		c := buf[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r':
			i++
		case c == '\n':
			return Token{KindNewline, i, i + 1}, State{}
		case c == '-' && i+1 < limit && buf[i+1] == '-':
			return lineComment(buf, i, i+2, limit)
		case c == '/' && i+1 < limit && buf[i+1] == '*':
			return blockComment(buf, i, i+2, limit, 1)
		case c == '\'':
			return quoted(buf, i, i+1, limit, '\'', KindString, ModeString)
		case c == '[':
			return quoted(buf, i, i+1, limit, ']', KindQuotedIdent, ModeBracket)
		case c == '"':
			return quoted(buf, i, i+1, limit, '"', KindQuotedIdent, ModeQuoted)
		case c == '#' || c == '@':
			j := i + 1
			if j < limit && buf[j] == c {
				j++
			}
			return Token{KindWord, i, wordEnd(buf, j, limit)}, State{}
		case isDigit(c):
			j := i + 1
			for j < limit && (IsWordRune(buf[j]) || buf[j] == '.') {
				j++
			}
			return Token{KindNumber, i, j}, State{}
		case IsWordRune(c):
			return Token{KindWord, i, wordEnd(buf, i+1, limit)}, State{}
		case c >= utf8.RuneSelf && unicode.IsSpace(c):
			i++
		default:
			return Token{KindPunct, i, i + 1}, State{}
		}
	}
	return Token{KindEnd, limit, limit}, State{}
}

// LineEnd returns the state the line after line starts in, given the state line
// starts in: Next over the whole line, then State.NextLine. The per-line step
// of every line-by-line scan, and the one the highlighter's cache replays.
func LineEnd(line []rune, st State) State {
	for i := 0; ; {
		var t Token
		t, st = Next(line, i, len(line), st)
		if t.Kind == KindEnd {
			return st.NextLine()
		}
		i = t.End
	}
}

// IsWordRune reports whether r can start a T-SQL word: a Unicode letter or
// digit, or '_'. A word starting with a digit is a number. Inside a word
// IsWordContinue applies instead.
func IsWordRune(r rune) bool {
	if r < utf8.RuneSelf {
		return r == '_' || (r|0x20 >= 'a' && r|0x20 <= 'z') || (r >= '0' && r <= '9')
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

// IsWordContinue reports whether r can continue a T-SQL word once started:
// IsWordRune, or '$', '#' or '@' (a regular identifier's later characters, so
// Price$, t#1 and a@b are each one name; gosmo's QuoteNameIfNeeded leaves them
// bare, and completion inserts them so). Only continuation: a leading '$'
// stays punctuation, so $5.00 is money and $action a pseudo-column, and a
// leading '#'/'@' is the sigil Next lexes itself.
func IsWordContinue(r rune) bool {
	return IsWordRune(r) || r == '$' || r == '#' || r == '@'
}

// WordStart returns where the word ending at end in line starts, scanning back
// over IsWordContinue runes then forward past any that cannot start one, so the
// start is never a '$' and a '#'/'@' sigil sits just before it rather than in
// it. It is end when no word ends there.
func WordStart(line []rune, end int) int {
	start := end
	for start > 0 && IsWordContinue(line[start-1]) {
		start--
	}
	for start < end && !IsWordRune(line[start]) {
		start++
	}
	return start
}

func isDigit(r rune) bool {
	if r < utf8.RuneSelf {
		return r >= '0' && r <= '9'
	}
	return unicode.IsDigit(r)
}

func wordEnd(buf []rune, j, limit int) int {
	for j < limit && IsWordContinue(buf[j]) {
		j++
	}
	return j
}

// lineComment ends the "--" comment that began at start, scanning from j.
func lineComment(buf []rune, start, j, limit int) (Token, State) {
	for j < limit && buf[j] != '\n' {
		j++
	}
	if j < limit {
		return Token{KindComment, start, j}, State{}
	}
	return Token{KindComment, start, j}, State{Mode: ModeLineComment}
}

// blockComment ends the block comment that began at start and is depth levels
// deep at j.
func blockComment(buf []rune, start, j, limit, depth int) (Token, State) {
	for j < limit {
		switch {
		case buf[j] == '/' && j+1 < limit && buf[j+1] == '*':
			depth++
			j += 2
		case buf[j] == '*' && j+1 < limit && buf[j+1] == '/':
			j += 2
			if depth--; depth == 0 {
				return Token{KindComment, start, j}, State{}
			}
		default:
			j++
		}
	}
	return Token{KindComment, start, limit}, State{Mode: ModeBlockComment, Depth: depth}
}

// quoted ends the literal or identifier that began at start, scanning from j
// for its closing delimiter, which a doubled delimiter is not.
func quoted(buf []rune, start, j, limit int, closer rune, kind Kind, mode Mode) (Token, State) {
	for j < limit {
		if buf[j] != closer {
			j++
			continue
		}
		if j+1 < limit && buf[j+1] == closer {
			j += 2
			continue
		}
		return Token{kind, start, j + 1}, State{}
	}
	return Token{kind, start, limit}, State{Mode: mode}
}
