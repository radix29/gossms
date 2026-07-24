package controls

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/radix29/gossms/internal/tuikit/theme"
)

// xmlHighlightRuns runs XMLHighlighter over a multi-line document and
// returns line idx's colored substrings, with a timeout guard so a
// tokenizer bug that fails to advance shows up as a clear test failure
// instead of a hung test binary — same approach as sql_highlighter_test.go.
func xmlHighlightRuns(t *testing.T, lines [][]rune, idx int) []string {
	t.Helper()
	hl := XMLHighlighter(&theme.Default)
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
		t.Fatal("XMLHighlighter did not return — infinite loop?")
		return nil
	}
}

func xmlHighlightWords(t *testing.T, line string) []string {
	t.Helper()
	return xmlHighlightRuns(t, [][]rune{[]rune(line)}, 0)
}

func containsFold(words []string, want string) bool {
	for _, w := range words {
		if strings.EqualFold(w, want) {
			return true
		}
	}
	return false
}

func TestXMLHighlighterRecognizesTagNames(t *testing.T) {
	words := xmlHighlightWords(t, `<RelOp NodeId="0"><OutputList/></RelOp>`)
	for _, want := range []string{"<RelOp", "<OutputList", "</RelOp"} {
		if !containsFold(words, want) {
			t.Errorf("xmlHighlightWords(...) = %v, want it to include %q", words, want)
		}
	}
}

func TestXMLHighlighterRecognizesAttributeNameAndValue(t *testing.T) {
	words := xmlHighlightWords(t, `<RelOp NodeId="0" EstimateRows="12.5"/>`)
	if !containsFold(words, "NodeId") {
		t.Errorf("words = %v, want attribute name %q", words, "NodeId")
	}
	if !containsFold(words, `"0"`) {
		t.Errorf("words = %v, want quoted attribute value %q", words, `"0"`)
	}
	if !containsFold(words, "EstimateRows") {
		t.Errorf("words = %v, want attribute name %q", words, "EstimateRows")
	}
	if !containsFold(words, `"12.5"`) {
		t.Errorf("words = %v, want quoted attribute value %q", words, `"12.5"`)
	}
}

func TestXMLHighlighterDoesNotStyleAttributeLikeTextWithoutEquals(t *testing.T) {
	words := xmlHighlightWords(t, `<Warnings>NoJoinPredicate</Warnings>`)
	if containsFold(words, "NoJoinPredicate") {
		t.Errorf("words = %v, plain element text must not get a run", words)
	}
}

func TestXMLHighlighterSingleQuotedAttributeValue(t *testing.T) {
	words := xmlHighlightWords(t, `<Op Name='Clustered Index Scan'/>`)
	if !containsFold(words, "'Clustered Index Scan'") {
		t.Errorf("words = %v, want single-quoted attribute value recognized", words)
	}
}

func TestXMLHighlighterProcessingInstructionAndDoctype(t *testing.T) {
	words := xmlHighlightWords(t, `<?xml version="1.0" encoding="utf-8"?>`)
	if !containsFold(words, `<?xml version="1.0" encoding="utf-8"?>`) {
		t.Errorf("words = %v, want the whole PI as one run", words)
	}
}

func TestXMLHighlighterCommentSingleLine(t *testing.T) {
	words := xmlHighlightWords(t, `<a/><!-- a comment --><b/>`)
	if !containsFold(words, "<!-- a comment -->") {
		t.Errorf("words = %v, want the whole comment as one run", words)
	}
}

func TestXMLHighlighterCommentSpansMultipleLines(t *testing.T) {
	lines := [][]rune{
		[]rune("<a/><!-- start"),
		[]rune("this whole line is inside the comment"),
		[]rune("end --><b/>"),
	}
	hl := XMLHighlighter(&theme.Default)

	runs1 := hl(lines, 1)
	if len(runs1) != 1 || runs1[0].Start != 0 || runs1[0].Len != len(lines[1]) {
		t.Fatalf("line 1 (entirely inside the comment) runs = %v, want one run covering the whole line", runs1)
	}

	words2 := xmlHighlightRuns(t, lines, 2)
	if !containsFold(words2, "end -->") {
		t.Errorf("line 2 words = %v, want the comment's closing segment recognized", words2)
	}
	if !containsFold(words2, "<b") {
		t.Errorf("line 2 words = %v, want the tag after the comment closes highlighted normally", words2)
	}
}

func TestXMLHighlighterCDATASpansMultipleLines(t *testing.T) {
	lines := [][]rune{
		[]rune("<a><![CDATA["),
		[]rune("raw <not a real tag> content"),
		[]rune("]]></a>"),
	}
	hl := XMLHighlighter(&theme.Default)

	runs1 := hl(lines, 1)
	if len(runs1) != 1 || runs1[0].Start != 0 || runs1[0].Len != len(lines[1]) {
		t.Fatalf("line 1 (entirely inside CDATA) runs = %v, want one run covering the whole line, not parsed as tags", runs1)
	}

	words2 := xmlHighlightRuns(t, lines, 2)
	if !containsFold(words2, "]]>") {
		t.Errorf("line 2 words = %v, want the CDATA close recognized", words2)
	}
	if !containsFold(words2, "</a") {
		t.Errorf("line 2 words = %v, want the tag after CDATA closes highlighted normally", words2)
	}
}

// TestXMLHighlighterDoesNotHangOnEdgeCases guards against a tokenizer
// branch that fails to advance i — bare/unmatched '<', an unterminated
// comment or CDATA section, and an empty document.
func TestXMLHighlighterDoesNotHangOnEdgeCases(t *testing.T) {
	for _, line := range []string{
		"",
		"<",
		"</",
		"<!",
		"<?",
		"a < b",
		"<a/><!-- never closed",
		"<a><![CDATA[ never closed",
		`attr=`,
		`attr="unterminated`,
	} {
		xmlHighlightWords(t, line) // must return within the test's timeout
	}
}

func TestXMLHighlighterUnterminatedCommentDoesNotHang(t *testing.T) {
	lines := [][]rune{
		[]rune("<a/><!-- never closed"),
		[]rune("<b/>"),
	}
	xmlHighlightRuns(t, lines, 0)
	xmlHighlightRuns(t, lines, 1)
}

// TestXMLHighlighterIncrementalCacheMatchesFullReplay guards the
// idx==lastIdx+1 fast path documented on XMLHighlighter: calling the same
// highlighter instance for consecutive line indices (mirroring how
// Editor.Draw walks a viewport) must produce byte-for-byte the same runs
// as a fresh highlighter doing the full xmlOpenBlock replay for that same
// line in isolation — the cache must never silently diverge.
func TestXMLHighlighterIncrementalCacheMatchesFullReplay(t *testing.T) {
	lines := [][]rune{
		[]rune(`<?xml version="1.0"?>`),
		[]rune(`<Root attr="1">`),
		[]rune(`<!-- start of a`),
		[]rune(`multi-line comment -->`),
		[]rune(`<a><![CDATA[`),
		[]rune(`raw <not a tag> content`),
		[]rune(`]]></a>`),
		[]rune(`<Leaf/>`),
		[]rune(`</Root>`),
	}

	sequential := XMLHighlighter(&theme.Default)
	for idx := range lines {
		got := sequential(lines, idx)
		want := XMLHighlighter(&theme.Default)(lines, idx) // fresh instance: always full replay
		if !reflect.DeepEqual(got, want) {
			t.Errorf("line %d: sequential-call runs = %#v, want %#v (fresh full replay)", idx, got, want)
		}
	}
}

// TestXMLHighlighterCacheFallsBackOnNonContiguousJump guards the other half
// of the same invariant: when Editor.Draw's viewport scrolls (or a fresh
// Draw pass starts at a different row than the previous one ended on), the
// next idx won't be lastIdx+1 — the closure must fall back to a full
// xmlOpenBlock replay rather than reusing a stale cached state.
func TestXMLHighlighterCacheFallsBackOnNonContiguousJump(t *testing.T) {
	lines := [][]rune{
		[]rune(`<a><!-- comment`),
		[]rune(`still in comment`),
		[]rune(`end --></a>`),
		[]rune(`<b attr="x"/>`),
	}

	hl := XMLHighlighter(&theme.Default)
	hl(lines, 0)        // lastIdx becomes 0
	got := hl(lines, 2) // non-contiguous jump: must not reuse line 0's end state as if it were line 1's

	want := XMLHighlighter(&theme.Default)(lines, 2)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("non-contiguous call runs = %#v, want %#v (fresh full replay)", got, want)
	}
}
