package safety

import (
	"strings"

	"github.com/radix29/gosmo/tsql"
	"github.com/radix29/gossms/internal/sqltext"
)

// token is one lexed token of a batch, as classification reads it.
type token struct {
	kind sqltext.Kind
	// word is a word's text upper-cased; a quoted identifier's text unquoted;
	// a punctuation rune itself. Empty for a literal.
	word string
	// raw is the token's text as written.
	raw   string
	line  int // 1-based script line
	depth int // parenthesis depth before the token
}

func (t token) is(w string) bool { return t.kind == sqltext.KindWord && t.word == w }

func (t token) punct(r string) bool { return t.kind == sqltext.KindPunct && t.word == r }

// lexBatch lexes text into tokens (comments and newlines dropped) and returns
// the indices that begin a statement: the first token, every token after a
// ';', and every token sqltext.StatementCutter cuts at. ';' itself is not kept.
func lexBatch(text string, startLine int) ([]token, []int) {
	buf := []rune(text)
	var toks []token
	starts := map[int]bool{0: true}
	cutter := sqltext.NewStatementCutter(func(pos int) { starts[pos] = true })
	line, depth := startLine+1, 0
	var st sqltext.State
	for i := 0; ; {
		t, next := sqltext.Next(buf, i, len(buf), st)
		if t.Kind == sqltext.KindEnd {
			break
		}
		st, i = next, t.End
		raw := string(buf[t.Start:t.End])
		tokLine := line
		line += strings.Count(raw, "\n")
		switch t.Kind {
		case sqltext.KindNewline, sqltext.KindComment:
			continue
		case sqltext.KindPunct:
			if raw == ";" {
				cutter.Feed(buf, t, len(toks))
				starts[len(toks)] = true
				continue
			}
		}
		tk := token{kind: t.Kind, raw: raw, line: tokLine, depth: depth}
		switch t.Kind {
		case sqltext.KindWord, sqltext.KindNumber:
			tk.word = strings.ToUpper(raw)
		case sqltext.KindQuotedIdent:
			tk.word = unquote(raw)
		case sqltext.KindPunct:
			tk.word = raw
			switch raw {
			case "(":
				depth++
			case ")":
				depth = max(depth-1, 0)
			}
		}
		cutter.Feed(buf, t, len(toks))
		toks = append(toks, tk)
	}
	var cuts []int
	for i := range len(toks) {
		if starts[i] {
			cuts = append(cuts, i)
		}
	}
	return toks, cuts
}

// unquote is a [bracketed] or "quoted" identifier token's name: gosmo's
// scanner reads raw as one span and Unescape undoubles its closer. An
// unterminated one (the script ends inside it) runs to the end, which
// tsql.UnquoteName would instead return unchanged, delimiter and all.
func unquote(raw string) string {
	for sp := range tsql.Spans(raw) {
		return sp.Unescape(raw)
	}
	return raw
}

// dottedName is a possibly dotted name (word (. word)*) as readName reads it.
type dottedName struct {
	// raw is the name as written, delimiters and dots kept; "" for none.
	raw string
	// parts is each part unquoted with its case kept; an omitted part
	// (db..t) is "".
	parts []string
	// words is each part as its token's word: a word upper-cased, a quoted
	// identifier unquoted. nameParts folds these, not parts, so that a
	// letter whose upper case is ASCII (ſ, ı) folds as it always has.
	words []string
}

// readName reads the possibly dotted name starting at toks[i] and the index
// after it: the one reader of the shape, so every check agrees on where a name
// ends. ok is false when toks[i] starts no name, or the name ends on a dot
// (one more part than written). A @variable or #temp part is read like any
// other; callers that refuse one say so.
func readName(toks []token, i int) (n dottedName, next int, ok bool) {
	var raw strings.Builder
	expect := true
	for ; i < len(toks); i++ {
		t := toks[i]
		switch {
		case expect && t.kind == sqltext.KindWord:
			n.parts, n.words, expect = append(n.parts, t.raw), append(n.words, t.word), false
		case expect && t.kind == sqltext.KindQuotedIdent:
			n.parts, n.words, expect = append(n.parts, t.word), append(n.words, t.word), false
		case t.punct("."):
			if expect {
				n.parts, n.words = append(n.parts, ""), append(n.words, "")
			}
			expect = true
		default:
			n.raw = raw.String()
			return n, i, len(n.parts) > 0 && !expect
		}
		raw.WriteString(t.raw)
	}
	n.raw = raw.String()
	return n, i, len(n.parts) > 0 && !expect
}

// objectName reads the possibly dotted name starting at toks[i], as written;
// "" when there is none.
func objectName(toks []token, i int) string {
	n, _, _ := readName(toks, i)
	return n.raw
}

// nameParts reads the possibly dotted name starting at toks[i], unquoted and
// lower-cased, and the index after it; none when it starts with a @variable,
// which is no procedure or object name. An omitted part (master..sp_who) is "".
func nameParts(toks []token, i int) ([]string, int) {
	if i < len(toks) && toks[i].kind == sqltext.KindWord && strings.HasPrefix(toks[i].raw, "@") {
		return nil, i
	}
	n, next, _ := readName(toks, i)
	for k, w := range n.words {
		n.words[k] = strings.ToLower(w)
	}
	return n.words, next
}
