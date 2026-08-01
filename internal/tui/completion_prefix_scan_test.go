package tui

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// scanCompletionPrefix replaced a scan that tokenized the whole prefix and
// then threw away everything before the current statement. The replacement
// lexes the prefix without materialising tokens, then tokenizes only the
// statement — which means it has to agree with the one-pass form about where
// that statement begins. These tests pin the two together token-for-token,
// over the corpus below and over randomly assembled scripts.

// referenceLineStartsNormal reports, per line, whether it begins outside any
// comment, string literal, or quoted identifier. A plain character-at-a-time
// walk written independently of lexSQL, so the differential tests below have
// something of their own to disagree with.
func referenceLineStartsNormal(lines [][]rune) []bool {
	const (
		normal = iota
		blockComment
		singleQuote
		bracket
		doubleQuote
	)
	out := make([]bool, len(lines))
	state := normal
	for r, line := range lines {
		out[r] = state == normal
		lineComment := false
		for c := 0; c < len(line); {
			// A closing delimiter doubled (…'' , ]] , "") is an escape, not
			// the end of the run — same rule lexSQL applies.
			switch {
			case lineComment:
				c = len(line)
			case state == blockComment:
				if c+1 < len(line) && line[c] == '*' && line[c+1] == '/' {
					state = normal
					c += 2
				} else {
					c++
				}
			case state == singleQuote:
				if line[c] != '\'' {
					c++
				} else if c+1 < len(line) && line[c+1] == '\'' {
					c += 2
				} else {
					state = normal
					c++
				}
			case state == bracket:
				if line[c] != ']' {
					c++
				} else if c+1 < len(line) && line[c+1] == ']' {
					c += 2
				} else {
					state = normal
					c++
				}
			case state == doubleQuote:
				if line[c] != '"' {
					c++
				} else if c+1 < len(line) && line[c+1] == '"' {
					c += 2
				} else {
					state = normal
					c++
				}
			case c+1 < len(line) && line[c] == '-' && line[c+1] == '-':
				lineComment = true
			case c+1 < len(line) && line[c] == '/' && line[c+1] == '*':
				state = blockComment
				c += 2
			case line[c] == '\'':
				state = singleQuote
				c++
			case line[c] == '[':
				state = bracket
				c++
			case line[c] == '"':
				state = doubleQuote
				c++
			default:
				c++
			}
		}
	}
	return out
}

// referenceStatementStartOffset walks forwards over every line keeping the
// last GO seen — the shape the production scan had before it folded GO
// detection into the lexer, plus the context test that fix added: a "GO" line
// only separates batches if the line actually begins outside a comment,
// string literal, or quoted identifier.
func referenceStatementStartOffset(lines [][]rune, cursorRow int, semiStart int) int {
	normal := referenceLineStartsNormal(lines)
	start := semiStart
	for i := 0; i < cursorRow && i < len(lines); i++ {
		if normal[i] && strings.EqualFold(strings.TrimSpace(string(lines[i])), "GO") {
			if goStart := offsetForCursor(lines, i+1, 0); goStart > start {
				start = goStart
			}
		}
	}
	return start
}

// referenceScanCompletionPrefix is the original one-pass approach: tokenize
// [0, upTo) in full, then discard every token before the statement start.
func referenceScanCompletionPrefix(lines [][]rune, buf []rune, cursorRow, upTo int) completionPrefixScan {
	tokens, state, semiStart, quoteStart := tokenizeSQLRange(buf, 0, upTo, false)
	batchStart := referenceStatementStartOffset(lines, cursorRow, semiStart)
	return completionPrefixScan{
		tokens:     tokensFrom(tokens, batchStart),
		state:      state,
		batchStart: batchStart,
		quoteStart: quoteStart,
	}
}

// compareScans reports the first difference between the two scans, or "".
func compareScans(want, got completionPrefixScan) string {
	if want.state != got.state {
		return fmt.Sprintf("state = %d, want %d", got.state, want.state)
	}
	if want.batchStart != got.batchStart {
		return fmt.Sprintf("batchStart = %d, want %d", got.batchStart, want.batchStart)
	}
	// quoteStart is only meaningful in the two quoted states.
	if (want.state == sqlLexBracket || want.state == sqlLexDoubleQuote) && want.quoteStart != got.quoteStart {
		return fmt.Sprintf("quoteStart = %d, want %d", got.quoteStart, want.quoteStart)
	}
	if len(want.tokens) != len(got.tokens) {
		return fmt.Sprintf("len(tokens) = %d, want %d\n got: %s\nwant: %s",
			len(got.tokens), len(want.tokens), dumpTokens(got.tokens), dumpTokens(want.tokens))
	}
	for i := range want.tokens {
		if want.tokens[i] != got.tokens[i] {
			return fmt.Sprintf("token[%d] = %+v, want %+v\n got: %s\nwant: %s",
				i, got.tokens[i], want.tokens[i], dumpTokens(got.tokens), dumpTokens(want.tokens))
		}
	}
	return ""
}

func dumpTokens(ts []sqlToken) string {
	var b strings.Builder
	for _, t := range ts {
		fmt.Fprintf(&b, "{%d %q @%d} ", t.kind, t.text, t.start)
	}
	return b.String()
}

// splitRunes turns a script into the [][]rune shape the editor hands the
// completion provider.
func splitRunes(script string) [][]rune {
	parts := strings.Split(script, "\n")
	lines := make([][]rune, len(parts))
	for i, p := range parts {
		lines[i] = []rune(p)
	}
	return lines
}

// checkEveryCursorPosition compares the two scans at every (row, col) in the
// script — an exhaustive sweep rather than a sample, since the interesting
// divergences are all about exactly where the cursor lands.
func checkEveryCursorPosition(t *testing.T, script string) {
	t.Helper()
	lines := splitRunes(script)
	buf := flattenLines(lines)
	for row := range lines {
		for col := 0; col <= len(lines[row]); col++ {
			upTo := offsetForCursor(lines, row, col)
			want := referenceScanCompletionPrefix(lines, buf, row, upTo)
			got := scanCompletionPrefix(lines, buf, row, upTo)
			if diff := compareScans(want, got); diff != "" {
				t.Fatalf("cursor (row=%d,col=%d) in script:\n%s\n\n%s", row, col, script, diff)
			}
		}
	}
}

// diffCorpus is aimed squarely at the assumptions the two-pass scan makes:
// that the statement boundary is findable without tokens, and that tokenizing
// from it reproduces the same tokens the full-prefix pass produced there.
// Half the entries are "GO" in a context that does not separate batches.
var diffCorpus = map[string]string{
	"plain": "SELECT a, b FROM dbo.T WHERE x = 1",

	"go separated": "SELECT 1 FROM A\nGO\nSELECT 2 FROM B\nGO\nSELECT 3 FROM C",

	"semicolons": "SELECT 1 FROM A; SELECT 2 FROM B; SELECT 3 FROM C",

	// A GO line inside a block comment is not a real batch separator, so
	// nothing here starts a new batch.
	"go inside block comment": "SELECT 1\n/*\nGO\n*/\nFROM T",

	// The same, with tokens after the comment closes on the GO-following line.
	"go inside block comment then code": "SELECT x\n/* hide\nGO\n*/ FROM dbo.Orders o WHERE o.id = 1",

	// A bracket identifier spanning the GO line — the GO is inside it, so the
	// whole thing stays one batch and the ']' still closes the identifier it
	// opened on line 1.
	"bracket spans go line": "SELECT [weird\nGO\nname] FROM T",

	"double quote spans go line": "SELECT \"weird\nGO\nname\" FROM T",

	// A string literal that contains what looks like a separator.
	"semicolon in string":  "SELECT 'a;b' AS c FROM T WHERE y = 'GO'",
	"go word in string":    "SELECT 'GO' FROM T",
	"string spans go line": "SELECT 'weird\nGO\nname' FROM T",

	"line comment with semicolon": "SELECT 1 -- ;not a boundary\nFROM T",
	"line comment with go":        "SELECT 1 -- GO\nFROM T",

	"semicolon inside brackets": "SELECT [a;b] FROM [c;d]",

	// Unterminated openers: the states completion either continues in
	// (bracket) or bails out of.
	"unterminated bracket":       "SELECT * FROM [Cus",
	"unterminated block comment": "SELECT 1\n/* never closed\nGO\nSELECT 2",
	"unterminated string":        "SELECT 'never closed\nGO\nSELECT 2",

	"escaped bracket":     "SELECT [a]]b] FROM T",
	"escaped quote":       "SELECT 'it''s' FROM T",
	"escaped doublequote": "SELECT \"a\"\"b\" FROM T",

	"go casing and spacing": "SELECT 1\n  go  \nSELECT 2\n\tGo\t\nSELECT 3",

	// The commented-out GO in full: the alias on line 1 must stay in scope
	// for the column completion on the last line.
	"commented go with trailing alias": "SELECT * FROM dbo.Patients p\n/*\nGO\n*/\nWHERE p.",

	// "GO" that is not alone on its line is not a separator.
	"go not alone": "SELECT 1\nGO 5\nSELECT 2\nXGO\nSELECT 3\nGOO\nSELECT 4",

	"semicolon then go":     "SELECT 1;\nGO\nSELECT 2",
	"go then semicolon":     "SELECT 1\nGO\nSELECT 2; SELECT 3",
	"empty":                 "",
	"only go":               "GO",
	"leading go":            "GO\nSELECT 1 FROM T",
	"trailing semicolon":    "SELECT 1 FROM T;",
	"consecutive semicolon": "SELECT 1;;;SELECT 2",
	"qualified names":       "SELECT o.* FROM [my db].dbo.[Ord.ers] AS o JOIN a.b.c d ON d.x = o.y",
	"parens and commas":     "SELECT f(a, b), (c), COUNT(*) FROM T GROUP BY a, b",
	"unicode":               "SELECT [Ку], 'пароль' FROM [密码] WHERE x = N'密'",
}

func TestScanCompletionPrefixMatchesReference(t *testing.T) {
	for name, script := range diffCorpus {
		t.Run(name, func(t *testing.T) { checkEveryCursorPosition(t, script) })
	}
}

// fragments are recombined into synthetic scripts below. The set is chosen so
// that concatenation routinely produces unbalanced comments, strings, and
// brackets straddling GO lines — the shapes that break a resumed scan.
var fragments = []string{
	"SELECT a, b FROM dbo.T",
	"SELECT * FROM [Cus",
	"GO",
	" go ",
	"GO 3",
	";",
	"; SELECT 2 FROM B",
	"/*",
	"*/",
	"/* inline */",
	"-- trailing comment",
	"'unterminated",
	"'closed'",
	"''",
	"[bracketed]",
	"[unterminated",
	"]",
	"\"dq\"",
	"\"unterminated",
	"WHERE x = 1",
	"JOIN o ON o.id = t.id",
	"a.b.c",
	"f(x, y)",
	"",
	"   ",
	"N'lit;eral'",
	"SELECT 'GO'",
}

// Randomly assembled scripts, with the cursor swept across every position of
// each. Seeded, so a failure reproduces.
func TestScanCompletionPrefixMatchesReferenceOnGeneratedScripts(t *testing.T) {
	rng := rand.New(rand.NewSource(20260730))
	for i := 0; i < 400; i++ {
		var b strings.Builder
		nLines := 1 + rng.Intn(6)
		for l := 0; l < nLines; l++ {
			if l > 0 {
				b.WriteByte('\n')
			}
			for f := 0; f < 1+rng.Intn(3); f++ {
				if f > 0 {
					b.WriteByte(' ')
				}
				b.WriteString(fragments[rng.Intn(len(fragments))])
			}
		}
		script := b.String()
		t.Run(fmt.Sprintf("script%03d", i), func(t *testing.T) {
			checkEveryCursorPosition(t, script)
		})
	}
}

// Typing a script one rune at a time, checking after every keystroke — the
// access pattern the completion popup actually produces.
func TestScanCompletionPrefixMatchesReferenceWhileTyping(t *testing.T) {
	for name, script := range diffCorpus {
		t.Run(name, func(t *testing.T) {
			runes := []rune(script)
			for n := 0; n <= len(runes); n++ {
				lines := splitRunes(string(runes[:n]))
				buf := flattenLines(lines)
				row := len(lines) - 1
				upTo := offsetForCursor(lines, row, len(lines[row]))
				want := referenceScanCompletionPrefix(lines, buf, row, upTo)
				got := scanCompletionPrefix(lines, buf, row, upTo)
				if diff := compareScans(want, got); diff != "" {
					t.Fatalf("after typing %d rune(s) of:\n%s\n\n%s", n, script, diff)
				}
			}
		})
	}
}

// isGoSeparatorLine replaced strings.EqualFold(strings.TrimSpace(...), "GO").
func TestIsGoSeparatorLineMatchesStringVersion(t *testing.T) {
	cases := []string{
		"GO", "go", "Go", "gO", " GO ", "\tGO\t", "  go  ", " GO ",
		"GO 5", "XGO", "GOO", "G O", "", " ", "\t", "SELECT 1", "G", "O",
		"GO;", ";GO", "GO--x", "ＧＯ",
	}
	for _, c := range cases {
		want := strings.EqualFold(strings.TrimSpace(c), "GO")
		if got := isGoSeparatorLine([]rune(c)); got != want {
			t.Errorf("isGoSeparatorLine(%q) = %v, want %v", c, got, want)
		}
	}
}

// The bug this whole GO-detection rework exists for: a "GO" commented out
// with a block comment is not a batch separator, so the alias declared above
// it is still in scope and `p.` completes against it. Asserted as the scope
// boundary rather than as candidates, since the boundary is what was wrong.
func TestCommentedOutGoDoesNotStartANewBatch(t *testing.T) {
	cases := map[string]string{
		"block comment": "SELECT * FROM dbo.Patients p\n/*\nGO\n*/\nWHERE p.",
		"string literal": "SELECT * FROM dbo.Patients p WHERE note = 'x\nGO\ny'\n" +
			"AND p.",
		"bracket identifier": "SELECT * FROM dbo.[Pat\nGO\nients] p\nWHERE p.",
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			lines := splitRunes(script)
			buf := flattenLines(lines)
			row := len(lines) - 1
			got := scanCompletionPrefix(lines, buf, row, offsetForCursor(lines, row, len(lines[row])))
			if got.batchStart != 0 {
				t.Errorf("batchStart = %d, want 0 — the commented-out GO was treated as a batch separator, "+
					"scoping completion past the table alias", got.batchStart)
			}
		})
	}
}

// The inverse: a real GO still ends the batch, so an alias above it is gone.
func TestRealGoStillStartsANewBatch(t *testing.T) {
	script := "SELECT * FROM dbo.Patients p\nGO\nWHERE p."
	lines := splitRunes(script)
	buf := flattenLines(lines)
	row := len(lines) - 1
	want := offsetForCursor(lines, 2, 0)
	if got := scanCompletionPrefix(lines, buf, row, offsetForCursor(lines, row, len(lines[row]))).batchStart; got != want {
		t.Errorf("batchStart = %d, want %d (the line after the GO)", got, want)
	}
}

// flattenLinesInto reuses the caller's buffer across keystrokes, so a shorter
// script following a longer one must not leave the previous run's tail
// visible.
func TestFlattenLinesIntoReuseMatchesFreshAllocation(t *testing.T) {
	scripts := []string{
		"SELECT a, b, c FROM dbo.SomeVeryLongTableName WHERE x = 1 AND y = 2",
		"SELECT 1",
		"",
		"a\nb\nc",
		"SELECT * FROM T\nGO\nSELECT * FROM U",
		"x",
	}
	var reused []rune
	for _, script := range scripts {
		lines := splitRunes(script)
		want := flattenLines(lines)
		reused = flattenLinesInto(reused, lines)
		if string(reused) != string(want) {
			t.Fatalf("flattenLinesInto(reused) = %q, want %q (stale tail from a previous call?)", string(reused), string(want))
		}
		if len(reused) != len(want) {
			t.Fatalf("len = %d, want %d", len(reused), len(want))
		}
	}
}

// The whole scan must be unaffected by whether its buffer was freshly
// allocated or recycled.
func TestScanCompletionPrefixUnaffectedByBufferReuse(t *testing.T) {
	var reused []rune
	// Prime the buffer with the longest script so later shorter ones recycle it.
	longest := ""
	for _, s := range diffCorpus {
		if len(s) > len(longest) {
			longest = s
		}
	}
	reused = flattenLinesInto(reused, splitRunes(longest))

	for name, script := range diffCorpus {
		t.Run(name, func(t *testing.T) {
			lines := splitRunes(script)
			fresh := flattenLines(lines)
			reused = flattenLinesInto(reused, lines)
			for row := range lines {
				for col := 0; col <= len(lines[row]); col++ {
					upTo := offsetForCursor(lines, row, col)
					want := scanCompletionPrefix(lines, fresh, row, upTo)
					got := scanCompletionPrefix(lines, reused, row, upTo)
					if diff := compareScans(want, got); diff != "" {
						t.Fatalf("cursor (row=%d,col=%d) in %q: recycled buffer differs: %s", row, col, script, diff)
					}
				}
			}
		})
	}
}
