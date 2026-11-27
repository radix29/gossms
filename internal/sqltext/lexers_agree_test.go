package sqltext

import (
	"slices"
	"strings"
	"testing"

	"github.com/radix29/gosmo/tsql"
)

// lexedSpan is one comment, literal or quoted identifier as a byte range of the
// text, in a form both lexers can be reduced to. kind is '-' (line comment),
// '/' (block comment), a single quote (literal), '[' or '"' (quoted
// identifier).
type lexedSpan struct {
	kind       byte
	start, end int
	closed     bool
}

// sqltextSpans lexes s as one flat buffer with Next and keeps the comments,
// literals and quoted identifiers, as byte offsets into s. A line comment is
// closed (tsql.Spans says so even at the end of the text); anything else is
// open when Next left its mode set, which on a flat buffer only the last
// token can.
func sqltextSpans(s string) []lexedSpan {
	buf := []rune(s)
	// Byte offset of each rune, plus len(s): ranging over a string and
	// []rune(s) agree rune for rune, an invalid byte being one U+FFFD in both.
	offs := make([]int, 0, len(buf)+1)
	for i := range s {
		offs = append(offs, i)
	}
	offs = append(offs, len(s))

	var out []lexedSpan
	st := State{}
	for i := 0; ; {
		var t Token
		t, st = Next(buf, i, len(buf), st)
		if t.Kind == KindEnd {
			return out
		}
		i = t.End
		var kind byte
		switch t.Kind {
		case KindComment:
			kind = '/'
			if buf[t.Start] == '-' {
				kind = '-'
			}
		case KindString:
			kind = '\''
		case KindQuotedIdent:
			kind = byte(buf[t.Start])
		default:
			continue
		}
		closed := st.Mode == ModeNormal || st.Mode == ModeLineComment
		out = append(out, lexedSpan{kind, offs[t.Start], offs[t.End], closed})
	}
}

// tsqlSpans is gosmo's tsql.Spans reduced the same way. An N'…' literal's span
// starts at its N where Next leaves the N a word of its own, so its start moves
// past it: the one representational difference between the two.
func tsqlSpans(s string) []lexedSpan {
	var out []lexedSpan
	for sp := range tsql.Spans(s) {
		var kind byte
		switch sp.Kind {
		case tsql.SpanCode:
			continue
		case tsql.SpanLineComment:
			kind = '-'
		case tsql.SpanBlockComment:
			kind = '/'
		case tsql.SpanLiteral:
			kind = '\''
		case tsql.SpanNLiteral:
			kind, sp.Start = '\'', sp.Start+1
		case tsql.SpanBracketIdent:
			kind = '['
		case tsql.SpanQuotedIdent:
			kind = '"'
		}
		out = append(out, lexedSpan{kind, sp.Start, sp.End, sp.Closed})
	}
	return out
}

// FuzzLexersAgree guards the second T-SQL lexer. Next is gossms's one lexer
// (docs/decisions.md § T-SQL lexing), but gosmo's tsql.Spans is another, and
// internal/cli/safety uses both: Next to classify, tsql to unquote. They are
// not merged — the editor needs Next's line-resumable State, which Spans has
// no use for — so this pins that they find the same comments, literals and
// quoted identifiers at the same byte ranges, closed or not, on any text. A
// rule changed in one and not the other (a delimiter, nesting, where a
// comment ends) fails here. Seeds run in plain go test; fuzzing is on demand:
//
//	go test ./internal/sqltext -run XXX -fuzz FuzzLexersAgree -fuzztime 60s -fuzzminimizetime 0
func FuzzLexersAgree(f *testing.F) {
	f.Add(lexCorpus)
	for l := range strings.SplitSeq(lexCorpus, "\n") {
		f.Add(l)
	}
	for _, s := range []string{
		"", "SELECT 1",
		// Nesting and overlapping block-comment delimiters.
		"/* a /* b */ c */ x", "/*/ x */", "/*/*/", "/**/*/", "*/ /*", "/* -- */ x",
		// Unterminated, each form.
		"'abc", "[abc", `"abc`, "/* a /* b */", "/*", "N'", "--",
		// Doubled closers, and closers that are not doubled.
		"'it''s'", "[a]]b]", `"c""d"`, "[a]]'b]'", "''''", "[]]]", `""""`,
		// Each form inside the others.
		"[--x] y", "'--x' y", `"/*" y`, "'/* x' */", "-- 'x\n'y'", "/* [ */ ]",
		// Line-comment ends: LF, CR, CRLF, end of text.
		"SELECT 1 -- c\rDROP TABLE t", "-- c\r\n'x'", "-- c",
		// The N prefix: a word of its own, the tail of a word, either case.
		"N'x'", "n'x'", "IN'x'", "@N'x'", "ÄN'x'", "SELECT N'a', N'b'",
		// Non-ASCII and invalid UTF-8 around delimiters.
		"名前 '値' [列] /* 註 */ -- 注", "'\xff' [\xfe] \x80", "\u2028'x'\u00a0[y]",
		// Numbers ending at a delimiter.
		"SELECT 1DELETE 1e5'x' 0x1F[y] 1.e--c\n$1e5/*z*/ 1N'x'",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > fuzzMaxRunes {
			return
		}
		if got, want := sqltextSpans(text), tsqlSpans(text); !slices.Equal(got, want) {
			t.Fatalf("lexers disagree on %q:\n sqltext.Next %+v\n tsql.Spans   %+v", text, got, want)
		}
	})
}
