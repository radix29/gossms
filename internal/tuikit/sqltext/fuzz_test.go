package sqltext

import (
	"strings"
	"testing"
)

// fuzzMaxRunes bounds a fuzz input: the line-start check below is quadratic,
// and every construct the lexer knows fits in far less.
const fuzzMaxRunes = 2000

// FuzzLexer drives every entry point here with arbitrary text: every keystroke
// in the editor reaches Next, StatementAt and LineEnd, and every executed
// script SplitBatches. The seeds — lexCorpus, its lines, and the delimiter
// shapes the example tests pin — run in plain go test; fuzzing is on demand:
//
//	go test ./internal/tuikit/sqltext -run XXX -fuzz FuzzLexer -fuzztime 60s
func FuzzLexer(f *testing.F) {
	f.Add(lexCorpus)
	for l := range strings.SplitSeq(lexCorpus, "\n") {
		f.Add(l)
	}
	for _, s := range []string{
		"", "GO", "GO 3", "go -- 2", "SELECT 1;\nGO\nSELECT 2",
		"'it''s'", "[a]]b]", `"c""d"`, "/* /* */ GO */", "-- x\nGO",
		"$5.00 @@ROWCOUNT ##t #@x", "名前 ä1  \r\n",
		"WITH c AS (SELECT 1) SELECT * FROM c\nINSERT t SELECT 1 DELETE t",
		"[x\nGO\ny]", "'x\nGO\ny'", "/*\nGO\n*/", "GO 99999999999999999999",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, text string) {
		buf := []rune(text)
		if len(buf) > fuzzMaxRunes {
			return
		}
		checkNextBounds(t, buf, 0, len(buf), State{})
		// Resuming across a limit: stop anywhere, then carry the state on.
		for k := 0; k <= len(buf); k += 1 + len(buf)/16 {
			st := checkNextBounds(t, buf, 0, k, State{})
			checkNextBounds(t, buf, k, len(buf), st)
		}
		checkLineByLineMatchesFlat(t, text)
		checkLineEndMatchesFlat(t, text)

		lines := make([][]rune, 0, strings.Count(text, "\n")+1)
		for l := range strings.SplitSeq(text, "\n") {
			lines = append(lines, []rune(l))
		}
		for row, line := range lines {
			for col := 0; col <= len(line); col++ {
				checkStatementAt(t, text, lines, row, col)
			}
		}

		for _, b := range SplitBatches(text) {
			if strings.TrimSpace(b.Text) == "" || !strings.Contains(text, b.Text) || b.Count < 0 {
				t.Fatalf("SplitBatches(%q): bad batch %+v", text, b)
			}
		}
	})
}

// checkNextBounds walks buf[from:limit] with Next from st and fails on a token
// out of bounds or a stuck lexer. It returns the state at limit.
//
// One empty token is progress: a line comment resumed at its '\n' is a
// zero-width comment that returns the lexer to ModeNormal. Two in a row are
// not.
func checkNextBounds(t *testing.T, buf []rune, from, limit int, st State) State {
	t.Helper()
	empty := false
	for i := from; ; {
		var tok Token
		tok, st = Next(buf, i, limit, st)
		if tok.Kind == KindEnd {
			return st
		}
		if tok.Start < i || tok.End < tok.Start || tok.End > limit || (empty && tok.End == tok.Start) {
			t.Fatalf("Next(%q, %d, %d) = %+v: out of bounds or no progress", string(buf), i, limit, tok)
		}
		empty = tok.End == tok.Start
		if st.Depth < 0 || (st.Mode != ModeBlockComment && st.Depth != 0) {
			t.Fatalf("Next(%q, %d, %d): bad state %+v", string(buf), i, limit, st)
		}
		i = tok.End
	}
}

// checkStatementAt fails when StatementAt's span is not an ordered pair of
// positions inside lines.
func checkStatementAt(t *testing.T, text string, lines [][]rune, row, col int) {
	t.Helper()
	sr, sc, er, ec, ok := StatementAt(lines, row, col)
	if !ok {
		return
	}
	in := func(r, c int) bool { return r >= 0 && r < len(lines) && c >= 0 && c <= len(lines[r]) }
	if !in(sr, sc) || !in(er, ec) || sr > er || (sr == er && sc > ec) {
		t.Fatalf("StatementAt(%q, %d, %d) = %d,%d-%d,%d: out of bounds or reversed", text, row, col, sr, sc, er, ec)
	}
}
