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

// A word starting with '@'/'#' must not be left for the identifier-body
// loop to consume (see the comment in SQLHighlighter's Word branch) — that
// loop's condition is already false there, so the tokenizer never advances
// and spins forever. Covers local variables, system variables (@@ROWCOUNT),
// and temp tables (#Temp, ##Global).
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

// TestSQLHighlighterBlockCommentSpansMultipleLines covers multi-line block
// comments: one opened on one line and closed two lines later colors every
// rune in between — including entire lines that are nothing but comment
// body — and stops once the closing "*/" is reached.
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
// class of bug as the @/# test above: an unterminated /* (blockCommentEnd
// returning -1 forever) must not spin — every line after it is treated as
// fully inside the comment.
func TestSQLHighlighterUnterminatedBlockCommentDoesNotHang(t *testing.T) {
	lines := [][]rune{
		[]rune("SELECT 1 /* never closed"),
		[]rune("SELECT 2"),
	}
	highlightLineWords(t, lines, 0)
	highlightLineWords(t, lines, 1)
}

// memoCorpus is the document the memoization differential tests below run
// over: every shape whose highlighting depends on carried-over block-comment
// state, plus the ones that deliberately don't (a /* inside a string or after
// a --, which this highlighter documents itself as not tracking — the point
// is that both code paths agree, not that either is clever about it).
var memoCorpus = [][]rune{
	[]rune("SELECT 1"),
	[]rune("SELECT /* closed on this line */ 2"),
	[]rune("SELECT /* opens here"),
	[]rune("still inside the comment"),
	[]rune(""),
	[]rune("*/ SELECT 3"),
	[]rune("SELECT 4 -- line comment with /* inside it"),
	[]rune("SELECT '/* in a string literal */' AS s"),
	[]rune("*/ closes then /* reopens"),
	[]rune("SELECT 5"),
	[]rune("/**/ SELECT 6 /**/"),
	[]rune("/* a */ /* b */ /* c"),
	[]rune("*/"),
	[]rune("SELECT @@ROWCOUNT, #tmp.x FROM #tmp"),
	[]rune("SELECT 7 /* trailing, never closed"),
	[]rune("dangling"),
}

// reference highlights one line the way the pre-memoization highlighter did:
// a closure that has never been called takes startsInBlockComment's full
// replay for every idx (the memo's fast path needs idx == lastIdx+1, and
// lastIdx starts at -1), so this is the original implementation rather than a
// second copy of it maintained alongside.
func reference(lines [][]rune, idx int) []ColorRun {
	return SQLHighlighter(&theme.Default)(lines, idx)
}

func sameRuns(a, b []ColorRun) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestSQLHighlighterMemoMatchesFullReplayInDrawOrder is the equivalence test
// for the block-comment memo: one long-lived closure walked in Draw's own
// strictly-increasing order — where every row but the first takes the O(1)
// path — must colour every line exactly as the full replay does.
func TestSQLHighlighterMemoMatchesFullReplayInDrawOrder(t *testing.T) {
	hl := SQLHighlighter(&theme.Default)
	for idx := range memoCorpus {
		got := hl(memoCorpus, idx)
		want := reference(memoCorpus, idx)
		if !sameRuns(got, want) {
			t.Errorf("line %d (%q): memoized = %v, full replay = %v",
				idx, string(memoCorpus[idx]), got, want)
		}
	}
}

// TestSQLHighlighterMemoMatchesFullReplayOnJumps covers the access orders a
// real Draw pass produces that aren't a simple walk: restarting at the top
// (a redraw after an edit), resuming mid-document (the view scrolled), and
// re-highlighting the same line twice in a row. Each must fall back to the
// replay rather than reusing a state that doesn't belong to idx-1.
func TestSQLHighlighterMemoMatchesFullReplayOnJumps(t *testing.T) {
	orders := map[string][]int{
		"restart at top":                        {0, 1, 2, 3, 0, 1, 2, 3},
		"scrolled forward":                      {0, 1, 2, 8, 9, 10, 11},
		"scrolled backward":                     {10, 11, 12, 2, 3, 4},
		"same line repeated":                    {3, 3, 3, 4, 4},
		"reverse":                               {15, 14, 13, 12, 11, 10},
		"single jump into unterminated comment": {14, 15},
	}
	for name, order := range orders {
		t.Run(name, func(t *testing.T) {
			hl := SQLHighlighter(&theme.Default)
			for _, idx := range order {
				got := hl(memoCorpus, idx)
				want := reference(memoCorpus, idx)
				if !sameRuns(got, want) {
					t.Errorf("line %d (%q): memoized = %v, full replay = %v",
						idx, string(memoCorpus[idx]), got, want)
				}
			}
		})
	}
}

// TestSQLHighlighterMemoSurvivesDocumentReplacement pins the SetText case the
// memo's correctness argument rests on: Editor.SetText resets scrollRow to 0,
// so the next pass starts at line 0 — where no line can already be inside a
// block comment — and the state left over from the previous document is never
// consulted. Highlighting a document that ends mid-comment and then starting a
// different one at line 0 must not colour that new line 0 as a comment.
func TestSQLHighlighterMemoSurvivesDocumentReplacement(t *testing.T) {
	hl := SQLHighlighter(&theme.Default)
	for idx := range memoCorpus {
		hl(memoCorpus, idx)
	}
	replacement := [][]rune{
		[]rune("SELECT 1"),
		[]rune("SELECT 2"),
	}
	for idx := range replacement {
		got := hl(replacement, idx)
		want := reference(replacement, idx)
		if !sameRuns(got, want) {
			t.Errorf("replacement line %d: memoized = %v, full replay = %v", idx, got, want)
		}
	}
}
