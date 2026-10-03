package sqltext

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// lexAll lexes s as one flat buffer and renders each token as kind:text.
func lexAll(s string, st State) ([]string, State) {
	buf := []rune(s)
	var out []string
	for i := 0; ; {
		var t Token
		t, st = Next(buf, i, len(buf), st)
		if t.Kind == KindEnd {
			return out, st
		}
		out = append(out, fmt.Sprintf("%s:%s", kindNames[t.Kind], string(buf[t.Start:t.End])))
		i = t.End
	}
}

var kindNames = map[Kind]string{
	KindNewline: "nl", KindWord: "w", KindNumber: "n", KindString: "s",
	KindQuotedIdent: "q", KindComment: "c", KindPunct: "p",
}

func TestNextTokens(t *testing.T) {
	tests := []struct {
		in   string
		want []string
		end  State
	}{
		{"SELECT a.b, *", []string{"w:SELECT", "w:a", "p:.", "w:b", "p:,", "p:*"}, State{}},
		{"#t ##g @v @@ROWCOUNT", []string{"w:#t", "w:##g", "w:@v", "w:@@ROWCOUNT"}, State{}},
		// The sigil alone is a word; '#'/'@' after the sigil continue it.
		{"# @ #@x", []string{"w:#", "w:@", "w:#@x"}, State{}},
		// '$', '#' and '@' continue a word but never start one: a leading '$'
		// is punctuation (money, $action), a leading '#'/'@' the sigil.
		{"Price$ t#1 a@b @x@y ##t#", []string{"w:Price$", "w:t#1", "w:a@b", "w:@x@y", "w:##t#"}, State{}},
		{"$5.00 $action t.$x", []string{"p:$", "n:5.00", "p:$", "w:action", "w:t", "p:.", "p:$", "w:x"}, State{}},
		{"@a+@b,c$=1", []string{"w:@a", "p:+", "w:@b", "p:,", "w:c$", "p:=", "n:1"}, State{}},
		{"@Delete", []string{"w:@Delete"}, State{}},
		{"42 1.5 0x1F 1e5 x1", []string{"n:42", "n:1.5", "n:0x1F", "n:1e5", "w:x1"}, State{}},
		{"_a ä1 名前", []string{"w:_a", "w:ä1", "w:名前"}, State{}},
		{"'it''s' N'x'", []string{"s:'it''s'", "w:N", "s:'x'"}, State{}},
		{"[a]]b] \"c\"\"d\"", []string{"q:[a]]b]", `q:"c""d"`}, State{}},
		{"[a/*b] x", []string{"q:[a/*b]", "w:x"}, State{}},
		{"a -- b /* c\nd", []string{"w:a", "c:-- b /* c", "nl:\n", "w:d"}, State{}},
		{"/* a /* b */ c */ d", []string{"c:/* a /* b */ c */", "w:d"}, State{}},
		{"a\u00a0b\r\n", []string{"w:a", "w:b", "nl:\n"}, State{}},
		{"x <> 1;", []string{"w:x", "p:<", "p:>", "n:1", "p:;"}, State{}},
		// Left open at the limit: the token runs to it, and the state says
		// what it is still inside.
		{"a 'b", []string{"w:a", "s:'b"}, State{Mode: ModeString}},
		{"[b", []string{"q:[b"}, State{Mode: ModeBracket}},
		{`"b`, []string{`q:"b`}, State{Mode: ModeQuoted}},
		{"-- b", []string{"c:-- b"}, State{Mode: ModeLineComment}},
		{"/* /* */", []string{"c:/* /* */"}, State{Mode: ModeBlockComment, Depth: 1}},
		// A closing delimiter at the very end closes: what follows it is a
		// newline or nothing, never the doubled delimiter.
		{"'b'", []string{"s:'b'"}, State{}},
	}
	for _, tt := range tests {
		got, end := lexAll(tt.in, State{})
		if !slices.Equal(got, tt.want) || end != tt.end {
			t.Errorf("lex %q:\n got %q %+v\nwant %q %+v", tt.in, got, end, tt.want, tt.end)
		}
	}
}

// A token left open is continued from the state alone.
func TestNextResumes(t *testing.T) {
	tests := []struct {
		in   string
		from State
		want []string
		end  State
	}{
		{"b' c", State{Mode: ModeString}, []string{"s:b'", "w:c"}, State{}},
		{"b]] c] d", State{Mode: ModeBracket}, []string{"q:b]] c]", "w:d"}, State{}},
		{`b" d`, State{Mode: ModeQuoted}, []string{`q:b"`, "w:d"}, State{}},
		{"*/ */ x", State{Mode: ModeBlockComment, Depth: 2}, []string{"c:*/ */", "w:x"}, State{}},
		{"*/ x", State{Mode: ModeBlockComment, Depth: 2}, []string{"c:*/ x"}, State{Mode: ModeBlockComment, Depth: 1}},
		{"x\ny", State{Mode: ModeLineComment}, []string{"c:x", "nl:\n", "w:y"}, State{}},
		{"", State{Mode: ModeString}, nil, State{Mode: ModeString}},
	}
	for _, tt := range tests {
		got, end := lexAll(tt.in, tt.from)
		if !slices.Equal(got, tt.want) || end != tt.end {
			t.Errorf("lex %q from %+v:\n got %q %+v\nwant %q %+v", tt.in, tt.from, got, end, tt.want, tt.end)
		}
	}
}

// lexCorpus mixes every construct whose state crosses a line.
var lexCorpus = strings.Join([]string{
	"SELECT 'one",
	"two' AS [a",
	"b]]c], \"d",
	"e\" FROM t -- x /* y",
	"/* outer /* inner",
	"*/ still */ SELECT 1.5 -- 'q",
	"GO",
	"[x/*y] '/*' /* -- */ z",
	"@@ROWCOUNT ##t #@x",
	"'",
	"GO",
	"'",
	"",
	"/*",
	"",
	"*/",
}, "\n")

// A caller lexing line by line, carrying the State and applying NextLine,
// sees the same tokens and the same states as one lexing the flat buffer.
// SplitBatches, StatementAt and the highlighter lex by line, and sqlparse
// lexes flat, so this is what makes the four agree.
func TestLineByLineLexingMatchesFlat(t *testing.T) {
	type span struct {
		kind       Kind
		start, end int
	}
	buf := []rune(lexCorpus)
	var flat []span
	var flatEnd State
	for i := 0; ; {
		var tok Token
		tok, flatEnd = Next(buf, i, len(buf), flatEnd)
		if tok.Kind == KindEnd {
			break
		}
		flat = append(flat, span{tok.Kind, tok.Start, tok.End})
		i = tok.End
	}

	// The line scan, in flat offsets. A token resumed from the line above is
	// the rest of that line's last token, newline included; a newline the
	// flat scan reports is one reached in ModeNormal.
	var byLine []span
	var st State
	off := 0
	for n, text := range strings.Split(lexCorpus, "\n") {
		line := []rune(text)
		if n > 0 && st.Mode == ModeNormal {
			byLine = append(byLine, span{KindNewline, off - 1, off})
		}
		for i := 0; ; {
			resumed := st.Mode != ModeNormal
			var tok Token
			tok, st = Next(line, i, len(line), st)
			if tok.Kind == KindEnd {
				break
			}
			if resumed {
				byLine[len(byLine)-1].end = off + tok.End
			} else {
				byLine = append(byLine, span{tok.Kind, off + tok.Start, off + tok.End})
			}
			i = tok.End
		}
		st = st.NextLine()
		off += len(line) + 1
	}
	if !slices.Equal(byLine, flat) {
		t.Fatalf("line-by-line lexing differs from flat:\n line %v\n flat %v", byLine, flat)
	}
	if st != flatEnd.NextLine() {
		t.Errorf("end state: line-by-line %+v, flat %+v", st, flatEnd)
	}
}

// LineEnd is the step the highlighter cache replays; it must agree with the
// state a flat lex reaches at each line start.
func TestLineEndMatchesFlatStateAtEveryLineStart(t *testing.T) {
	lines := strings.Split(lexCorpus, "\n")
	var st State
	off := 0
	buf := []rune(lexCorpus)
	for n, line := range lines {
		// The flat state at this line's start.
		var flat State
		for i := 0; i < off; {
			var tok Token
			tok, flat = Next(buf, i, off, flat)
			if tok.Kind == KindEnd {
				break
			}
			i = tok.End
		}
		if flat.NextLine() != st {
			t.Errorf("line %d (%q): LineEnd chain says %+v, flat lex says %+v", n, line, st, flat)
		}
		st = LineEnd([]rune(line), st)
		off += len([]rune(line)) + 1
	}
}

func TestIsWordContinue(t *testing.T) {
	for _, r := range "aZ_09é名$#@" {
		if !IsWordContinue(r) {
			t.Errorf("IsWordContinue(%q) = false", r)
		}
	}
	for _, r := range " .,;()[]'\"-+*/" {
		if IsWordContinue(r) {
			t.Errorf("IsWordContinue(%q) = true", r)
		}
	}
}

func TestWordStart(t *testing.T) {
	for _, tt := range []struct {
		line string
		end  int
		want int
	}{
		{"SELECT Price$", 13, 7},
		{"SELECT Pri", 10, 7},
		{"t.a@b", 5, 2},
		{"@var", 4, 1},    // the sigil stays before the word
		{"@x@y", 4, 1},    // one variable; '@' mid-word is in it
		{"#tmp#1", 6, 1},  // likewise a temp table
		{"$action", 7, 1}, // a leading '$' is not the word's
		{"x = $", 5, 5},   // nothing to start
		{"a.", 2, 2},
		{"", 0, 0},
	} {
		if got := WordStart([]rune(tt.line), tt.end); got != tt.want {
			t.Errorf("WordStart(%q, %d) = %d, want %d", tt.line, tt.end, got, tt.want)
		}
	}
}

func TestIsWordRune(t *testing.T) {
	for _, r := range "aZ_09äé名٥" {
		if !IsWordRune(r) {
			t.Errorf("IsWordRune(%q) = false", r)
		}
	}
	for _, r := range " \t\n.,;()[]'\"@#$-+*/`{|}~^\u00a0" {
		if IsWordRune(r) {
			t.Errorf("IsWordRune(%q) = true", r)
		}
	}
}
