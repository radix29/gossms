package format

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/radix29/gossms/internal/sqltext"
)

// format lays src out in style s (sql_layout.go): a layout pass over
// sqltext's tokens with a small block and paren stack, not a parser. Only
// whitespace between tokens changes, plus keyword case and, when
// s.aliasEquals, the order of a select-list alias (sql_alias.go); comments,
// literals, quoted identifiers and GO lines come through byte for byte (a
// "--" comment loses trailing blanks, which are no part of it to the server).
//
// An unterminated literal, quoted identifier or block comment, an unbalanced
// parenthesis or invalid UTF-8 is a *SyntaxError: the layout would have to
// guess where the code resumes. Leading blank lines and trailing whitespace
// are dropped and one final newline kept iff src ended with one.
func (s sqlStyle) format(src string, o Options) (string, *sqlLexed, error) {
	if !utf8.ValidString(src) {
		// The lexer works on runes, which would turn a stray byte into U+FFFD
		// unnoticed by a guard comparing runes.
		bad := 0
		for bad < len(src) {
			r, n := utf8.DecodeRuneInString(src[bad:])
			if r == utf8.RuneError && n == 1 {
				break
			}
			bad += n
		}
		line, col := position(src, bad)
		return "", nil, &SyntaxError{Lang: LangSQL, Line: line, Col: col, Msg: "invalid UTF-8"}
	}
	lx, err := lexSQL(src)
	if err != nil {
		return "", nil, err
	}
	if err := checkParens(src, lx.toks); err != nil {
		return "", nil, err
	}
	l := newSQLLayout(lx, o, s)
	out := l.run()
	if strings.HasSuffix(src, "\n") && out != "" {
		out += "\n"
	}
	return out, lx, nil
}

// sqlTok is one token of a script as the layout sees it. Whitespace is not a
// token: nl and sp record what stood before one.
type sqlTok struct {
	kind  sqltext.Kind // KindWord, KindNumber, KindString, KindQuotedIdent, KindComment or KindPunct
	goSep bool         // a whole GO separator line, text its trimmed spelling
	text  string       // the source spelling; a compound operator (<>, >=, ::) spans its lexer tokens
	up    string       // text upper-cased, for words
	nl    int          // newlines in the whitespace before it
	sp    bool         // any whitespace before it
	pos   int          // byte offset in the source, for errors
	p0    int          // its lexer tokens: sqlLexed.parts[p0:p1]
	p1    int
}

// sqlLexed is a lexed script: the layout tokens, and the lexer tokens they
// span, indexing buf.
type sqlLexed struct {
	buf   []rune
	toks  []sqlTok
	parts []sqltext.Token
}

func (t *sqlTok) isPunct(s string) bool { return t.kind == sqltext.KindPunct && t.text == s }
func (t *sqlTok) isWord(s string) bool  { return t.kind == sqltext.KindWord && t.up == s }
func (t *sqlTok) comment() bool         { return t.kind == sqltext.KindComment }
func (t *sqlTok) lineComment() bool     { return t.comment() && strings.HasPrefix(t.text, "--") }

// sqlCompound are the operators the lexer returns one rune at a time but the
// layout must never put a space inside. Only adjacent runes join: "< >" is
// two tokens to the server too.
var sqlCompound = wordSet(`<> != !< !> >= <= += -= *= /= %= &= |= ^= ::`)

// lexSQL tokenizes src with sqltext.Next, GO separator lines (by the rule
// SplitBatches runs them by) folded into one token each, compound operators
// and exponent numbers (1e+5, which the lexer splits at the sign) joined.
func lexSQL(src string) (*sqlLexed, error) {
	buf := []rune(src)
	// About one token per three runes in typical T-SQL; sized up front, a
	// 20 000-line script spent a third of its time growing these.
	lx := &sqlLexed{buf: buf, toks: make([]sqlTok, 0, len(buf)/3+16), parts: make([]sqltext.Token, 0, len(buf)/3+16)}
	// byteAt maps a rune offset back to a byte offset, for positions. Calls
	// come in increasing order, so it walks forward from the last.
	ri, bi := 0, 0
	byteAt := func(r int) int {
		for ; ri < r; ri++ {
			bi += utf8.RuneLen(buf[ri])
		}
		return bi
	}
	nl, sp := 0, false
	lineStart := true
	var st sqltext.State
	for i := 0; i < len(buf); {
		if lineStart {
			lineStart = false
			if next, _, ok := sqltext.GoSeparatorAt(buf, i, len(buf)); ok {
				end := next
				if end > i && buf[end-1] == '\n' {
					end--
				}
				at := byteAt(i)
				lx.toks = append(lx.toks, sqlTok{goSep: true, text: strings.TrimSpace(src[at:byteAt(end)]), nl: nl, sp: true, pos: at})
				nl, sp = 0, false
				if next > end {
					nl, sp, lineStart = 1, true, true
				}
				i = next
				continue
			}
		}
		t, nst := sqltext.Next(buf, i, len(buf), st)
		if t.Kind == sqltext.KindEnd {
			break
		}
		if t.Start > i {
			sp = true
		}
		i = t.End
		if t.Kind == sqltext.KindNewline {
			nl++
			sp, lineStart, st = true, true, nst
			continue
		}
		if nst.Mode != sqltext.ModeNormal && nst.Mode != sqltext.ModeLineComment {
			line, col := position(src, byteAt(t.Start))
			return nil, &SyntaxError{Lang: LangSQL, Line: line, Col: col, Msg: unterminated(buf[t.Start])}
		}
		st = sqltext.State{}
		at := byteAt(t.Start)
		text := src[at:byteAt(t.End)]
		if t.Kind == sqltext.KindComment && strings.HasPrefix(text, "--") {
			text = strings.TrimRight(text, " \t\r")
		}
		lx.parts = append(lx.parts, t)
		p := len(lx.parts)
		tok := sqlTok{kind: t.Kind, text: text, nl: nl, sp: sp, pos: at, p0: p - 1, p1: p}
		if t.Kind == sqltext.KindWord {
			tok.up = strings.ToUpper(text)
		}
		nl, sp = 0, false
		if n := len(lx.toks); n > 0 && !tok.sp && joins(&lx.toks[n-1], &tok) {
			prev := &lx.toks[n-1]
			prev.text += tok.text
			prev.p1 = p
			continue
		}
		lx.toks = append(lx.toks, tok)
	}
	return lx, nil
}

// joins reports whether b, lexed right after a with nothing between, is part
// of the same layout token: a compound operator, or the sign and exponent of
// a decimal number the lexer cut at "e+" (1.5e+10, not 0x1e + 5).
func joins(a, b *sqlTok) bool {
	switch {
	case a.kind == sqltext.KindPunct && b.kind == sqltext.KindPunct && len(a.text) == 1:
		return sqlCompound[a.text+b.text]
	case a.kind == sqltext.KindNumber && b.kind == sqltext.KindPunct && (b.text == "+" || b.text == "-"):
		last := a.text[len(a.text)-1]
		hex := len(a.text) > 1 && (a.text[1] == 'x' || a.text[1] == 'X')
		return (last == 'e' || last == 'E') && !hex
	case a.kind == sqltext.KindNumber && b.kind == sqltext.KindNumber:
		last := a.text[len(a.text)-1]
		return last == '+' || last == '-'
	}
	return false
}

func unterminated(open rune) string {
	switch open {
	case '\'':
		return "unterminated string literal"
	case '[', '"':
		return "unterminated quoted identifier"
	}
	return "unterminated block comment"
}

// checkParens rejects an unbalanced script: the layout indents by paren
// depth, and a stray ")" or unclosed "(" has no right place to go.
func checkParens(src string, toks []sqlTok) error {
	var open []int
	unclosed := func() error {
		line, col := position(src, open[len(open)-1])
		return &SyntaxError{Lang: LangSQL, Line: line, Col: col, Msg: `"(" is never closed`}
	}
	for _, t := range toks {
		switch {
		case t.goSep && len(open) > 0:
			return unclosed()
		case t.isPunct("("):
			open = append(open, t.pos)
		case t.isPunct(")"):
			if len(open) == 0 {
				line, col := position(src, t.pos)
				return &SyntaxError{Lang: LangSQL, Line: line, Col: col, Msg: `")" has no matching "("`}
			}
			open = open[:len(open)-1]
		}
	}
	if len(open) > 0 {
		return unclosed()
	}
	return nil
}

// sqlSameTokens is the SQL guard's comparison: in and out lexed, every
// token but whitespace compared — a GO line by its trimmed spelling, a
// keyword case-insensitively so a re-cased keyword passes while a re-cased
// identifier does not, a "--" comment without its trailing blanks — except
// that an input select-list item "expr AS name" may come out "name = expr"
// (sql_alias.go). It reports how many input tokens agreed before the first
// difference. a is in already lexed by lexSQL, or nil to lex it here.
func sqlSameTokens(in string, a *sqlLexed, out string) (int, bool, error) {
	if a == nil {
		var err error
		if a, err = lexSQL(in); err != nil {
			return 0, false, err
		}
	}
	b, err := lexSQL(out)
	if err != nil {
		return 0, false, err
	}
	ca, cb := sigCursor{lx: a, p: -1}, sigCursor{lx: b, p: -1}
	n := 0
	for ; ; n++ {
		x, okA := ca.next()
		y, okB := cb.next()
		if !okA || !okB {
			if okA == okB {
				return n, true, nil
			}
			break
		}
		if !sameSig(a, x, b, y) {
			break
		}
	}
	// Only now find the alias swaps: a second statement-cutter pass over the
	// input that took the guard from a third to over half of Format's time.
	// Output that moved no alias never needs it.
	g := sigCompare{a: a, b: b, sa: sigStream(a), sb: sigStream(b)}
	if g.swaps = sqlAliasSwaps(newSigView(a, g.sa)); len(g.swaps) == 0 {
		return n, false, nil
	}
	x, y, ok := g.run(0, len(g.sa), 0, -1)
	return x, ok && y == len(g.sb), nil
}

// sigStream is lx's guard stream.
func sigStream(lx *sqlLexed) []sigTok {
	c := sigCursor{lx: lx, p: -1}
	s := make([]sigTok, 0, len(lx.parts)+len(lx.toks)/8)
	for {
		x, ok := c.next()
		if !ok {
			return s
		}
		s = append(s, x)
	}
}

// sigCompare walks two guard streams side by side.
type sigCompare struct {
	a, b   *sqlLexed
	sa, sb []sigTok
	swaps  map[int]int // sqlAliasSwaps of a; nil until a plain comparison fails
}

// run compares sa[x:xEnd] with sb from y, returning where each stopped and
// whether all of sa[x:xEnd] matched. At an item sqlAliasSwaps found, sb may
// hold "name = expr" for sa's "expr AS name" — the same number of tokens, so
// the expression (which may hold swapped items of its own) is compared by a
// nested run. item is the item a nested run is comparing, not to be swapped
// again.
func (g *sigCompare) run(x, xEnd, y, item int) (int, int, bool) {
	for x < xEnd {
		if as, ok := g.swaps[x]; ok && x != item && y+1 < len(g.sb) && g.isEquals(y+1) &&
			sameSig(g.a, g.sa[as+1], g.b, g.sb[y]) {
			nx, ny, ok := g.run(x, as, y+2, x)
			if !ok {
				return nx, ny, false
			}
			x, y = as+2, ny
			continue
		}
		if y >= len(g.sb) || !sameSig(g.a, g.sa[x], g.b, g.sb[y]) {
			return x, y, false
		}
		x, y = x+1, y+1
	}
	return x, y, true
}

func (g *sigCompare) isEquals(y int) bool {
	s := g.sb[y]
	if s.part < 0 {
		return false
	}
	p := g.b.parts[s.part]
	return p.Kind == sqltext.KindPunct && p.End-p.Start == 1 && g.b.buf[p.Start] == '='
}

// sigTok is one token of the guard's stream: a lexer token of lx, or a GO
// line (part < 0) whose spelling is lx.toks[tok].text.
type sigTok struct{ tok, part int }

// sigCursor walks a lexed script's guard stream.
type sigCursor struct {
	lx   *sqlLexed
	t, p int
}

func (c *sigCursor) next() (sigTok, bool) {
	for c.t < len(c.lx.toks) {
		tk := &c.lx.toks[c.t]
		if tk.goSep {
			c.t++
			return sigTok{c.t - 1, -1}, true
		}
		c.p = max(c.p, tk.p0)
		if c.p < tk.p1 {
			c.p++
			return sigTok{c.t, c.p - 1}, true
		}
		c.t++
	}
	return sigTok{}, false
}

func sameSig(a *sqlLexed, x sigTok, b *sqlLexed, y sigTok) bool {
	if (x.part < 0) != (y.part < 0) {
		return false
	}
	if x.part < 0 {
		return a.toks[x.tok].text == b.toks[y.tok].text
	}
	pa, pb := a.parts[x.part], b.parts[y.part]
	if pa.Kind != pb.Kind {
		return false
	}
	ra, rb := a.buf[pa.Start:pa.End], b.buf[pb.Start:pb.End]
	switch {
	case pa.Kind == sqltext.KindComment && len(ra) > 1 && ra[0] == '-' && ra[1] == '-':
		ra, rb = trimBlankRunes(ra), trimBlankRunes(rb)
	case pa.Kind == sqltext.KindWord && len(ra) == len(rb) && !slices.Equal(ra, rb):
		// Only a keyword may differ, and then only in case.
		return strings.EqualFold(string(ra), string(rb)) && sqlKeywords[strings.ToUpper(string(ra))]
	}
	return slices.Equal(ra, rb)
}

func trimBlankRunes(r []rune) []rune {
	for len(r) > 0 && (r[len(r)-1] == ' ' || r[len(r)-1] == '\t' || r[len(r)-1] == '\r') {
		r = r[:len(r)-1]
	}
	return r
}
