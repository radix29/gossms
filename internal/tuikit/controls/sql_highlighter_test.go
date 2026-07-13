package controls

import (
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/theme"
)

// highlightWords runs the highlighter over a single-line document and
// returns the substrings it colored as keywords (style == kwStyle would
// require exporting the style, so instead this just returns every colored
// run's text — enough to confirm a given word got recognized at all).
func highlightWords(t *testing.T, line string) []string {
	t.Helper()
	return highlightLineWords(t, [][]rune{[]rune(line)}, 0)
}

// highlightLineWords is highlightWords for a multi-line document, so tests
// can exercise cross-line state (block comments spanning several lines).
func highlightLineWords(t *testing.T, lines [][]rune, idx int) []string {
	t.Helper()
	hl := SQLHighlighter(&theme.Default)
	done := make(chan []ColorRun, 1)
	go func() { done <- hl(lines, idx) }()
	select {
	case runs := <-done:
		line := lines[idx]
		out := make([]string, len(runs))
		for i, run := range runs {
			out[i] = string(line[run.Start : run.Start+run.Len])
		}
		return out
	case <-time.After(2 * time.Second):
		t.Fatal("SQLHighlighter did not return — infinite loop?")
		return nil
	}
}

// A word starting with '@'/'#' must not be a leading character the
// identifier-body loop itself has to consume (see the comment in
// SQLHighlighter's Word branch) — otherwise the tokenizer never advances
// past it and loops forever. This covers local variables, system
// variables (@@ROWCOUNT), and temp tables (#Temp, ##Global).
func TestSQLHighlighterDoesNotHangOnAtOrHashPrefixedWords(t *testing.T) {
	for _, line := range []string{
		"DECLARE @id INT",
		"SELECT @@ROWCOUNT",
		"SELECT * FROM #TempTable",
		"SELECT * FROM ##GlobalTemp",
		"@",
		"#",
		"@@",
	} {
		highlightWords(t, line) // must return within the test's timeout
	}
}

func TestSQLHighlighterRecognizesNewKeywordCategories(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"MERGE INTO Foo", "MERGE"},                                 // reserved keyword
		{"DECLARE @x SQL_VARIANT", "SQL_VARIANT"},                   // data type
		{"SELECT @@ROWCOUNT", "@@ROWCOUNT"},                         // system variable
		{"BEGIN TRY", "TRY"},                                        // control flow
		{"SELECT DATEADD(day, 1, GETDATE())", "DATEADD"},            // built-in function
		{"SELECT JSON_VALUE(x, '$.a')", "JSON_VALUE"},               // JSON function
		{"SELECT geometry::STGeomFromText(x, 0)", "STGEOMFROMTEXT"}, // spatial (uppercased match)
	}
	for _, tt := range tests {
		words := highlightWords(t, tt.line)
		found := false
		for _, w := range words {
			if strings.EqualFold(w, tt.want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("highlightWords(%q) = %v, want it to include %q", tt.line, words, tt.want)
		}
	}
}

// TestSQLHighlighterBlockCommentSingleLine confirms a /* ... */ comment
// that opens and closes on the same line is colored as one run, and that
// code after the closing "*/" is highlighted normally again.
func TestSQLHighlighterBlockCommentSingleLine(t *testing.T) {
	lines := [][]rune{[]rune("SELECT 1 /* a comment */ , 2")}
	words := highlightLineWords(t, lines, 0)
	found := false
	for _, w := range words {
		if w == "/* a comment */" {
			found = true
		}
	}
	if !found {
		t.Errorf("highlightLineWords = %v, want a run for %q", words, "/* a comment */")
	}
}

// TestSQLHighlighterBlockCommentSpansMultipleLines is the core regression
// test for multi-line block comment support: a comment opened on one line
// and closed two lines later must color every rune in between — including
// entire lines that are nothing but comment body — and stop coloring once
// the closing "*/" is reached.
func TestSQLHighlighterBlockCommentSpansMultipleLines(t *testing.T) {
	lines := [][]rune{
		[]rune("SELECT 1 /* start"),
		[]rune("this whole line is inside the comment"),
		[]rune("end */ SELECT 2"),
	}
	hl := SQLHighlighter(&theme.Default)

	runs0 := hl(lines, 0)
	if len(runs0) == 0 || runs0[len(runs0)-1].Start+runs0[len(runs0)-1].Len != len(lines[0]) {
		t.Fatalf("line 0 runs = %v, want the comment to extend to end of line", runs0)
	}

	runs1 := hl(lines, 1)
	if len(runs1) != 1 || runs1[0].Start != 0 || runs1[0].Len != len(lines[1]) {
		t.Fatalf("line 1 (entirely inside the comment) runs = %v, want one run covering the whole line", runs1)
	}

	runs2 := hl(lines, 2)
	if len(runs2) == 0 || runs2[0].Start != 0 {
		t.Fatalf("line 2 runs = %v, want the comment's closing segment to start at column 0", runs2)
	}
	closeEnd := runs2[0].Start + runs2[0].Len
	if string(lines[2][:closeEnd]) != "end */" {
		t.Errorf("line 2 comment run = %q, want %q", string(lines[2][:closeEnd]), "end */")
	}
	// "SELECT 2" after the comment closes must be highlighted as code again
	// (SELECT recognized as a keyword), not swallowed into the comment run.
	words := highlightLineWords(t, lines, 2)
	foundSelect := false
	for _, w := range words {
		if strings.EqualFold(w, "SELECT") {
			foundSelect = true
		}
	}
	if !foundSelect {
		t.Errorf("highlightLineWords(line 2) = %v, want SELECT recognized after the comment closes", words)
	}
}

// TestSQLHighlighterUnterminatedBlockCommentDoesNotHang guards the same
// class of bug as the @/# regression test above: an unterminated /*
// (blockCommentEnd returning -1 forever) must not spin — every line after
// it should just be treated as fully inside the comment.
func TestSQLHighlighterUnterminatedBlockCommentDoesNotHang(t *testing.T) {
	lines := [][]rune{
		[]rune("SELECT 1 /* never closed"),
		[]rune("SELECT 2"),
	}
	highlightLineWords(t, lines, 0)
	highlightLineWords(t, lines, 1)
}
