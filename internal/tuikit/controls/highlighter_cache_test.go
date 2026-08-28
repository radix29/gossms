package controls

import (
	"reflect"
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// Version-keyed caches: prefix states and wrap flattening
// ---------------------------------------------------------------------------
//
// Both highlighters and Editor's wrap flattening now do their O(document) work
// once per *edit* rather than once per Draw. That is only correct while
// Document.Version() cannot go stale, so these tests attack the cache from the
// side a stale version would show up on: change the text, ask again, and
// require the answer to reflect the new text rather than the old.

// TestSQLHighlighterCacheInvalidatesOnEdit is the load-bearing one. Closing an
// open block comment must uncolour every line after it on the very next call —
// a cache that kept the pre-edit prefix states would keep drawing the rest of
// the document as comment text until something else happened to rebuild it.
func TestSQLHighlighterCacheInvalidatesOnEdit(t *testing.T) {
	e := NewEditor(nil)
	e.SetBounds(0, 0, 60, 10)
	e.SetText("SELECT 1 /* open\nSELECT 2\nSELECT 3")
	hl := SQLHighlighter(&theme.Default)
	doc := e.Document()

	// Warm the cache: lines 1 and 2 are inside the unterminated comment, so
	// each is a single run covering the whole line.
	for idx := range 3 {
		hl(doc, idx)
	}
	if runs := hl(doc, 2); len(runs) != 1 || runs[0].Len != len(doc.Line(2)) {
		t.Fatalf("line 2 before the edit = %v, want one run covering the whole line", runs)
	}

	// Close the comment on line 0.
	e.cursorRow, e.cursorCol = 0, len(doc.Line(0))
	e.insertRune('*')
	e.insertRune('/')

	got := hl(doc, 2)
	want := SQLHighlighter(&theme.Default)(docOf(doc.all()), 2) // fresh: full replay
	if !reflect.DeepEqual(got, want) {
		t.Errorf("line 2 after closing the comment = %v, want %v — the cache served pre-edit state", got, want)
	}
	if len(got) == 1 && got[0].Len == len(doc.Line(2)) {
		t.Error("line 2 is still coloured as one whole-line comment after the comment was closed")
	}
}

// TestSQLHighlighterCacheInvalidatesOnLineCountChange covers the shape change
// a plain rune edit doesn't: inserting and deleting lines moves every later
// line's state to a different index, so a cache sized to the old document
// would answer for the wrong line.
func TestSQLHighlighterCacheInvalidatesOnLineCountChange(t *testing.T) {
	e := NewEditor(nil)
	e.SetBounds(0, 0, 60, 10)
	e.SetText("SELECT 1\n/* open\nstill inside\n*/ SELECT 2")
	hl := SQLHighlighter(&theme.Default)
	doc := e.Document()

	for idx := range doc.Len() {
		hl(doc, idx)
	}

	// Push the whole comment down a line.
	e.cursorRow, e.cursorCol = 0, 0
	e.insertNewline()

	for idx := range doc.Len() {
		got := hl(doc, idx)
		want := SQLHighlighter(&theme.Default)(docOf(doc.all()), idx)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("line %d (%q) after inserting a line = %v, want %v",
				idx, string(doc.Line(idx)), got, want)
		}
	}
}

// TestHighlighterCacheDistinguishesTwoDocuments: two Documents number their
// versions independently from zero, so a highlighter handed a second one at
// the same version must not answer with the first one's states. The cache is
// keyed on the pointer as well as the number for exactly this.
func TestHighlighterCacheDistinguishesTwoDocuments(t *testing.T) {
	inComment := docOf([][]rune{
		[]rune("/* open"),
		[]rune("still inside"),
	})
	plain := docOf([][]rune{
		[]rune("SELECT 1"),
		[]rune("SELECT 2"),
	})
	if inComment.Version() != plain.Version() {
		t.Fatalf("test needs two documents at the same version, got %d and %d",
			inComment.Version(), plain.Version())
	}

	hl := SQLHighlighter(&theme.Default)
	hl(inComment, 0)
	hl(inComment, 1)

	got := hl(plain, 1)
	want := SQLHighlighter(&theme.Default)(docOf(plain.all()), 1)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("second document's line 1 = %v, want %v — the first document's states leaked", got, want)
	}
}

// TestXMLHighlighterCacheInvalidatesOnEdit mirrors the SQL case for the other
// built-in highlighter, whose cross-line state is a comment or CDATA section.
func TestXMLHighlighterCacheInvalidatesOnEdit(t *testing.T) {
	e := NewEditor(nil)
	e.SetBounds(0, 0, 60, 10)
	e.SetText("<a><!-- open\nstill inside\n<b/>")
	hl := XMLHighlighter(&theme.Default)
	doc := e.Document()

	for idx := range 3 {
		hl(doc, idx)
	}

	e.cursorRow, e.cursorCol = 0, len(doc.Line(0))
	for _, r := range "-->" {
		e.insertRune(r)
	}

	got := hl(doc, 2)
	want := XMLHighlighter(&theme.Default)(docOf(doc.all()), 2)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("line 2 after closing the comment = %v, want %v — the cache served pre-edit state", got, want)
	}
}

// TestBuildVisualLinesCacheHitsAndInvalidates pins both halves of the wrap
// memo: repeated calls at the same width and version do no work, and any of
// an edit or a width change rebuilds. A stale hit renders the previous
// document's wrap segments — old text under a new cursor.
func TestBuildVisualLinesCacheHitsAndInvalidates(t *testing.T) {
	e := NewEditor(nil)
	e.SetWrapMode(true)
	e.SetGutterVisible(false)
	// The content width must be the same 10 the test asks for below, or
	// ensureCursorVisible's own buildVisualLines call rebuilds at a different
	// width and masks a stale hit rather than exposing it.
	e.SetBounds(0, 0, 10, 6)
	e.SetText("aaaa bbbb cccc dddd")

	first := e.buildVisualLines(10)
	if !e.vlCacheValid {
		t.Fatal("buildVisualLines did not mark its cache valid")
	}
	firstLen := len(first)

	// Same width, untouched document: must be the identical flattening.
	if got := e.buildVisualLines(10); !reflect.DeepEqual(got, first) {
		t.Errorf("second call at the same width = %v, want the cached %v", got, first)
	}

	// A different width must not be answered from the cache.
	narrow := e.buildVisualLines(5)
	if len(narrow) <= firstLen {
		t.Errorf("wrapping at width 5 gave %d visual rows, want more than the %d at width 10 — "+
			"the cache answered for the wrong width", len(narrow), firstLen)
	}

	// An edit must not be answered from the cache either.
	e.buildVisualLines(10)
	e.cursorRow, e.cursorCol = 0, 0
	e.Paste("eeee ffff gggg ")
	afterEdit := e.buildVisualLines(10)
	want := referenceVisualLines(e.doc.all(), 10)
	if !reflect.DeepEqual(afterEdit, want) {
		t.Errorf("after an edit = %v, want %v — the cache served the pre-edit document", afterEdit, want)
	}
}

// TestBuildVisualLinesCacheSurvivesAReadOnlyViewer is the case the memo was
// added for: DataGrid's full-cell viewer is read-only, so the flattening of a
// large value must be computed once and reused for every redraw. Asserted via
// the version, which is what the cache keys on and what a stray mutation
// would move.
func TestBuildVisualLinesCacheSurvivesAReadOnlyViewer(t *testing.T) {
	e := NewEditor(nil)
	e.SetWrapMode(true)
	e.SetReadOnly(true)
	e.SetGutterVisible(false)
	e.SetBounds(0, 0, 20, 8)
	e.SetText("one long logical line that wraps across a good many visual rows in a narrow box")

	e.buildVisualLines(18)
	version := e.doc.Version()

	s := newGlyphScreen(20, 8)
	for range 5 {
		e.Draw(s)
	}
	if e.doc.Version() != version {
		t.Errorf("Version() moved from %d to %d across read-only Draws — "+
			"the flattening would be rebuilt on every one", version, e.doc.Version())
	}
	if !e.vlCacheValid || e.vlCacheVersion != version {
		t.Error("the wrap cache was invalidated by a Draw that changed nothing")
	}
}

// TestPrefixStatesIncrementalReplayMatchesFullReplay is the differential test
// for the resume-and-converge path in prefixStates.replay. That path trusts
// two claims: a single-line edit cannot change the state any earlier line
// begins in, and once a recomputed state matches the stored one the rest of
// the document is already correct. Both are true only if the step function is
// a pure left fold, so this hammers it with edits that open, close and nest
// block comments and checks every line against a replay that assumes nothing.
func TestPrefixStatesIncrementalReplayMatchesFullReplay(t *testing.T) {
	e := NewEditor(nil)
	e.SetBounds(0, 0, 80, 20)
	e.SetText(strings.Join([]string{
		"SELECT 1",
		"/* opened here",
		"still inside",
		"*/ SELECT 2",
		"SELECT 3 -- with /* in a line comment",
		"SELECT '/*' AS in_a_string",
		"SELECT 4",
		"/* another",
		"SELECT 5",
	}, "\n"))
	doc := e.Document()

	var cache prefixStates[bool]
	// Warm it, then edit one line at a time and re-check the whole document.
	// Each edit is a single setLine, which is the case the resume path takes.
	edits := []struct {
		row  int
		text string
	}{
		{4, "SELECT 3 /* now a real comment"},     // opens one: must propagate down
		{4, "SELECT 3 -- back to a line comment"}, // closes it again
		{1, "SELECT 1b"},                          // removes the first opener
		{7, "*/ SELECT 6"},                        // a stray close
		{2, "/* reopened"},                        // opens mid-document
		{8, "SELECT 5 */"},                        // closes at the bottom
		{0, "SELECT 0 /*"},                        // line 0: dirtyFrom 0, full replay
	}
	for i := range doc.Len() {
		cache.at(doc, i, false, blockCommentToggleEnd)
	}
	for n, ed := range edits {
		e.doc.setLine(ed.row, []rune(ed.text))
		for i := range doc.Len() {
			got := cache.at(doc, i, false, blockCommentToggleEnd)
			want := startsInBlockComment(doc.all(), i)
			if got != want {
				t.Fatalf("after edit %d (row %d -> %q): line %d starts-in-comment = %v, want %v",
					n, ed.row, ed.text, i, got, want)
			}
		}
	}
}

// TestPrefixStatesResumeHandlesLineCountChanges: the resume path is only
// valid when the line count is unchanged. Inserting or deleting lines shifts
// every later state to a different index, so those must fall back to a full
// replay however few versions behind the cache is.
func TestPrefixStatesResumeHandlesLineCountChanges(t *testing.T) {
	e := NewEditor(nil)
	e.SetBounds(0, 0, 80, 20)
	e.SetText("SELECT 1\n/* open\ninside\n*/ SELECT 2\nSELECT 3")
	doc := e.Document()

	var cache prefixStates[bool]
	for i := range doc.Len() {
		cache.at(doc, i, false, blockCommentToggleEnd)
	}

	// One structural mutation, so the version advances by exactly 1 — the
	// same distance a resumable setLine leaves.
	e.cursorRow, e.cursorCol = 0, 0
	e.insertNewline()

	for i := range doc.Len() {
		got := cache.at(doc, i, false, blockCommentToggleEnd)
		want := startsInBlockComment(doc.all(), i)
		if got != want {
			t.Errorf("after inserting a line: line %d (%q) starts-in-comment = %v, want %v",
				i, string(doc.Line(i)), got, want)
		}
	}
}

// TestPrefixStatesIncrementalReplayAfterReplaceRange covers the resume path
// for the *other* mutation that now leaves a non-zero dirtyFrom. replaceRange
// (undo, redo) used to reset it to 0 and force a full replay; it now names its
// span's first line, which makes the resume branch reachable for a splice that
// happens to keep the line count — and only the length test in
// prefixStates.at keeps it off the ones that don't.
//
// Every case is checked against startsInBlockComment over the whole document,
// so a resume that trusts a stale earlier state shows up as a wrong colour
// rather than as a passing round trip.
func TestPrefixStatesIncrementalReplayAfterReplaceRange(t *testing.T) {
	e := NewEditor(nil)
	e.SetBounds(0, 0, 80, 20)
	e.SetText(strings.Join([]string{
		"SELECT 1",
		"/* opened here",
		"still inside",
		"*/ SELECT 2",
		"SELECT 3",
		"SELECT 4",
	}, "\n"))
	doc := e.Document()

	var cache prefixStates[bool]
	splices := []struct {
		name   string
		row, n int
		with   []string
	}{
		// Same line count: the resume branch, from a line that is not 0.
		{"a one-line span opens a comment", 4, 1, []string{"SELECT 3 /* now open"}},
		{"a one-line span closes it", 5, 1, []string{"SELECT 4 */"}},
		{"a two-line span, same length", 1, 2, []string{"SELECT 1b", "SELECT 1c"}},
		// Line count changes: must fall back to a full replay however few
		// versions behind the cache is.
		{"the span grows", 2, 1, []string{"/* reopened", "inside", "*/ done"}},
		{"the span shrinks", 1, 3, []string{"SELECT 2 /* left open"}},
		{"a span at line 0", 0, 1, []string{"SELECT 0 */"}},
	}
	for i := range doc.Len() {
		cache.at(doc, i, false, blockCommentToggleEnd)
	}
	for n, sp := range splices {
		with := make([][]rune, len(sp.with))
		for i, s := range sp.with {
			with[i] = []rune(s)
		}
		e.doc.replaceRange(sp.row, sp.n, with)
		for i := range doc.Len() {
			got := cache.at(doc, i, false, blockCommentToggleEnd)
			want := startsInBlockComment(doc.all(), i)
			if got != want {
				t.Fatalf("after splice %d (%s): line %d starts-in-comment = %v, want %v",
					n, sp.name, i, got, want)
			}
		}
	}
}
