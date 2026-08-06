package controls

import (
	"strings"
	"testing"
)

// setSearch compiles opts onto e, failing the test on a compile error.
func setSearch(t *testing.T, e *Editor, opts SearchOptions) {
	t.Helper()
	if err := e.SetSearch(opts); err != nil {
		t.Fatalf("SetSearch(%+v): %v", opts, err)
	}
}

// findAt runs FindNext and returns the selection it produced, as
// "row:startCol-endCol".
func findAt(t *testing.T, e *Editor, dir int) string {
	t.Helper()
	if !e.FindNext(dir) {
		return "none"
	}
	sr, sc, er, ec := e.selectionBounds()
	if sr != er {
		t.Fatalf("match spans rows %d..%d; matches must be within one line", sr, er)
	}
	return string(rune('0'+sr)) + ":" + itoa(sc) + "-" + itoa(ec)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestSearchFindNextWrapsAround(t *testing.T) {
	e := newTestEditor("select a from t\nselect b from t")
	setSearch(t, e, SearchOptions{Query: "select"})

	if got, want := findAt(t, e, 1), "0:0-6"; got != want {
		t.Fatalf("first find = %s, want %s", got, want)
	}
	if got, want := findAt(t, e, 1), "1:0-6"; got != want {
		t.Fatalf("second find = %s, want %s", got, want)
	}
	// Third find has nothing after it and must wrap to the top, not stall.
	if got, want := findAt(t, e, 1), "0:0-6"; got != want {
		t.Fatalf("wrapped find = %s, want %s", got, want)
	}

	if i, n := e.MatchPosition(); i != 1 || n != 2 {
		t.Fatalf("MatchPosition = %d of %d, want 1 of 2", i, n)
	}
}

func TestSearchFindPreviousWrapsBackwards(t *testing.T) {
	e := newTestEditor("aa\nbb aa\ncc")
	setSearch(t, e, SearchOptions{Query: "aa"})

	// Cursor starts at (0,0), before every match, so the first backwards
	// find wraps to the last one.
	if got, want := findAt(t, e, -1), "1:3-5"; got != want {
		t.Fatalf("first back = %s, want %s", got, want)
	}
	if got, want := findAt(t, e, -1), "0:0-2"; got != want {
		t.Fatalf("second back = %s, want %s", got, want)
	}
}

func TestSearchNoMatch(t *testing.T) {
	e := newTestEditor("select 1")
	setSearch(t, e, SearchOptions{Query: "nowhere"})
	if e.FindNext(1) {
		t.Fatal("FindNext reported a match for an absent term")
	}
	if n := e.MatchCount(); n != 0 {
		t.Fatalf("MatchCount = %d, want 0", n)
	}
}

func TestSearchMatchCaseAndWholeWord(t *testing.T) {
	e := newTestEditor("Order orders REORDER order")

	setSearch(t, e, SearchOptions{Query: "order"})
	if got, want := e.MatchCount(), 4; got != want {
		t.Fatalf("case-insensitive substring matches = %d, want %d", got, want)
	}

	setSearch(t, e, SearchOptions{Query: "order", MatchCase: true})
	if got, want := e.MatchCount(), 2; got != want {
		t.Fatalf("case-sensitive matches = %d, want %d", got, want)
	}

	setSearch(t, e, SearchOptions{Query: "order", WholeWord: true})
	if got, want := e.MatchCount(), 2; got != want {
		t.Fatalf("whole-word matches = %d, want %d", got, want)
	}

	setSearch(t, e, SearchOptions{Query: "order", WholeWord: true, MatchCase: true})
	if got, want := e.MatchCount(), 1; got != want {
		t.Fatalf("whole-word case-sensitive matches = %d, want %d", got, want)
	}
}

func TestSearchRegexpGroupReplacement(t *testing.T) {
	e := newTestEditor("dbo.Orders, dbo.Items")
	setSearch(t, e, SearchOptions{
		Query: `dbo\.(\w+)`, Replace: "sales.$1", Regexp: true,
	})
	if got, want := e.ReplaceAll(), 2; got != want {
		t.Fatalf("ReplaceAll = %d, want %d", got, want)
	}
	if got, want := e.Text(), "sales.Orders, sales.Items"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestSearchInvalidRegexpIsReported(t *testing.T) {
	e := newTestEditor("abc")
	if err := e.SetSearch(SearchOptions{Query: "(unclosed", Regexp: true}); err == nil {
		t.Fatal("SetSearch accepted an invalid regular expression")
	}
	if e.HasSearch() {
		t.Fatal("a failed SetSearch left a search active")
	}
	if n := e.MatchCount(); n != 0 {
		t.Fatalf("MatchCount = %d after a failed SetSearch, want 0", n)
	}
}

func TestSearchZeroWidthMatchesAreSkipped(t *testing.T) {
	e := newTestEditor("aaa\nbbb")
	setSearch(t, e, SearchOptions{Query: `x*`, Regexp: true})
	if n := e.MatchCount(); n != 0 {
		t.Fatalf("MatchCount = %d for a zero-width pattern, want 0", n)
	}
	// The real failure this guards: a zero-width match would be selectable,
	// and Find Next would then never advance past it.
	if e.FindNext(1) {
		t.Fatal("FindNext selected a zero-width match")
	}
}

func TestSearchWideRuneColumns(t *testing.T) {
	// 日本 are two columns each but one rune each: the match must be
	// reported in rune indices, or the selection lands mid-character.
	e := newTestEditor("-- 日本語 comment\nselect 日本 from t")
	setSearch(t, e, SearchOptions{Query: "日本"})
	if got, want := e.MatchCount(), 2; got != want {
		t.Fatalf("MatchCount = %d, want %d", got, want)
	}
	e.FindNext(1)
	sr, sc, _, ec := e.selectionBounds()
	if sr != 0 || sc != 3 || ec != 5 {
		t.Fatalf("first match at (%d,%d-%d), want (0,3-5)", sr, sc, ec)
	}
	if got := e.SelectedText(); got != "日本" {
		t.Fatalf("SelectedText = %q, want %q", got, "日本")
	}
}

func TestReplaceCurrentNeedsTheMatchSelected(t *testing.T) {
	e := newTestEditor("aa bb aa")
	setSearch(t, e, SearchOptions{Query: "aa", Replace: "zz"})

	// Nothing found yet: Replace must find rather than overwrite.
	if e.ReplaceCurrent() {
		t.Fatal("ReplaceCurrent replaced before anything was found")
	}
	if got := e.Text(); got != "aa bb aa" {
		t.Fatalf("text changed before a match was current: %q", got)
	}

	e.FindNext(1)
	if !e.ReplaceCurrent() {
		t.Fatal("ReplaceCurrent did not replace the current match")
	}
	if got, want := e.Text(), "zz bb aa"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
	// It also advances, so a second press replaces the next one.
	if !e.ReplaceCurrent() {
		t.Fatal("ReplaceCurrent did not replace the match it advanced to")
	}
	if got, want := e.Text(), "zz bb zz"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestReplaceAllIsOneUndoStep(t *testing.T) {
	original := "a a a\na a a\na a a"
	e := newTestEditor(original)
	setSearch(t, e, SearchOptions{Query: "a", Replace: "b"})

	if got, want := e.ReplaceAll(), 9; got != want {
		t.Fatalf("ReplaceAll = %d, want %d", got, want)
	}
	if got, want := e.Text(), "b b b\nb b b\nb b b"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}

	e.Undo()
	if got := e.Text(); got != original {
		t.Fatalf("one Undo left %q, want the original %q", got, original)
	}
}

func TestReplaceAllLongerReplacementKeepsOffsets(t *testing.T) {
	// Replacing left-to-right with a longer string shifts the offsets of
	// the matches still to come on the same line; right-to-left doesn't.
	e := newTestEditor("x x x")
	setSearch(t, e, SearchOptions{Query: "x", Replace: "yyyy"})
	if got, want := e.ReplaceAll(), 3; got != want {
		t.Fatalf("ReplaceAll = %d, want %d", got, want)
	}
	if got, want := e.Text(), "yyyy yyyy yyyy"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestReplaceAllInSelection(t *testing.T) {
	e := newTestEditor("a one\na two\na three")
	// Select from (0,2) to (1,5): "one\na two".
	e.selecting = true
	e.selAnchorRow, e.selAnchorCol = 0, 2
	e.cursorRow, e.cursorCol = 1, 5

	setSearch(t, e, SearchOptions{Query: "a", Replace: "Z", InSelection: true})
	if got, want := e.ReplaceAll(), 1; got != want {
		t.Fatalf("ReplaceAll in selection = %d, want %d", got, want)
	}
	if got, want := e.Text(), "a one\nZ two\na three"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestReplaceAllInSelectionWithNoSelectionDoesNothing(t *testing.T) {
	// The dangerous failure mode: "Selection only" with nothing selected
	// falling back to replacing the whole document.
	e := newTestEditor("a a a")
	setSearch(t, e, SearchOptions{Query: "a", Replace: "Z", InSelection: true})
	if got := e.ReplaceAll(); got != 0 {
		t.Fatalf("ReplaceAll = %d with no selection, want 0", got)
	}
	if got, want := e.Text(), "a a a"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestSearchReadOnlyEditorRefusesReplacement(t *testing.T) {
	e := newTestEditor("a a")
	e.SetReadOnly(true)
	setSearch(t, e, SearchOptions{Query: "a", Replace: "b"})
	e.FindNext(1)
	if e.ReplaceCurrent() || e.ReplaceAll() != 0 {
		t.Fatal("a read-only editor was modified by find/replace")
	}
	if got, want := e.Text(), "a a"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

func TestSearchRescansAfterEdit(t *testing.T) {
	e := newTestEditor("foo\nbar")
	setSearch(t, e, SearchOptions{Query: "foo"})
	if got := e.MatchCount(); got != 1 {
		t.Fatalf("MatchCount = %d, want 1", got)
	}
	e.SetText("foo foo\nfoo")
	if got := e.MatchCount(); got != 3 {
		t.Fatalf("MatchCount after edit = %d, want 3 — the match cache went stale", got)
	}
}

func TestSearchMatchSpansForLine(t *testing.T) {
	e := newTestEditor("x\nab ab\nx\nab")
	setSearch(t, e, SearchOptions{Query: "ab"})
	spans := e.matchSpansForLine(1)
	if len(spans) != 2 || spans[0].startCol != 0 || spans[1].startCol != 3 {
		t.Fatalf("row 1 spans = %+v, want two at cols 0 and 3", spans)
	}
	if got := e.matchSpansForLine(0); got != nil {
		t.Fatalf("row 0 spans = %+v, want none", got)
	}
	if got := e.matchSpansForLine(3); len(got) != 1 {
		t.Fatalf("row 3 spans = %+v, want one", got)
	}
}

func TestClearSearchDropsMatches(t *testing.T) {
	e := newTestEditor("a a a")
	setSearch(t, e, SearchOptions{Query: "a"})
	e.ClearSearch()
	if e.HasSearch() || e.MatchCount() != 0 || e.matchSpansForLine(0) != nil {
		t.Fatal("ClearSearch left search state behind")
	}
}

func TestSearchFindsInLongLineScrollsHorizontally(t *testing.T) {
	e := newTestEditor(strings.Repeat("x", 200) + "needle")
	e.SetBounds(0, 0, 40, 5)
	setSearch(t, e, SearchOptions{Query: "needle"})
	if !e.FindNext(1) {
		t.Fatal("FindNext found nothing")
	}
	if e.scrollCol == 0 {
		t.Fatal("a match past the right edge did not scroll into view")
	}
	if col := e.cursorDisplayCol(); col < e.scrollCol || col >= e.scrollCol+40 {
		t.Fatalf("cursor column %d outside the visible window [%d,%d)", col, e.scrollCol, e.scrollCol+40)
	}
}

// Match bounds are rune indices, not byte offsets. Every position Editor
// works in is a rune index, so a byte offset reaching one of them lands
// mid-character on the first non-ASCII line: the match would be reported
// short and a replacement would cut a rune in half.
func TestSearchReportsRuneIndicesOnNonASCIILines(t *testing.T) {
	// "héllo wörlo" — a two-byte rune before each match.
	e := newTestEditor("héllo wörlo")
	setSearch(t, e, SearchOptions{Query: "o"})
	spans := e.matchSpansForLine(0)
	if len(spans) != 2 {
		t.Fatalf("spans = %+v, want two", spans)
	}
	// Rune indices: h0 é1 l2 l3 o4 ' '5 w6 ö7 r8 l9 o10.
	if spans[0].startCol != 4 || spans[0].endCol != 5 {
		t.Errorf("first span = %+v, want cols 4-5", spans[0])
	}
	if spans[1].startCol != 10 || spans[1].endCol != 11 {
		t.Errorf("second span = %+v, want cols 10-11 — byte offsets leaked through", spans[1])
	}
}

// ASCII and non-ASCII lines take different paths through the offset
// conversion. A document mixing them must not carry one line's mapping into
// the next.
func TestSearchMixesASCIIAndNonASCIILines(t *testing.T) {
	e := newTestEditor("aXa\néXé\naXa")
	setSearch(t, e, SearchOptions{Query: "X"})
	for row := range 3 {
		spans := e.matchSpansForLine(row)
		if len(spans) != 1 || spans[0].startCol != 1 || spans[0].endCol != 2 {
			t.Errorf("row %d spans = %+v, want one at cols 1-2", row, spans)
		}
	}
}

// A replacement is applied at the reported bounds, so bounds that are off by
// the multi-byte prefix corrupt the line rather than merely mis-highlighting
// it.
func TestSearchReplacesCorrectlyOnNonASCIILines(t *testing.T) {
	e := newTestEditor("ééfooéé")
	setSearch(t, e, SearchOptions{Query: "foo", Replace: "bar"})
	if n := e.ReplaceAll(); n != 1 {
		t.Fatalf("ReplaceAll replaced %d, want 1", n)
	}
	if got, want := e.Text(), "éébaréé"; got != want {
		t.Fatalf("text = %q, want %q", got, want)
	}
}

// BenchmarkSearchScanASCII is the measurement behind skipping the byte-to-rune
// map for pure-ASCII lines: a full rescan of a large T-SQL script, which is
// what an edit forces on the next Draw. The mapping is the identity for every
// line here, and building it allocated len(line)+1 ints per line per scan.
func BenchmarkSearchScanASCII(b *testing.B) {
	e := NewEditor(nil)
	e.SetText(benchScript(5000))
	if err := e.SetSearch(SearchOptions{Query: "col1"}); err != nil {
		b.Fatalf("SetSearch: %v", err)
	}

	b.ResetTimer()
	for b.Loop() {
		e.search.scanned = false
		e.scanMatches()
	}
}

// The same scan over lines that do need the mapping, bounding what the
// non-ASCII path still costs.
func BenchmarkSearchScanNonASCII(b *testing.B) {
	e := NewEditor(nil)
	e.SetText(strings.ReplaceAll(benchScript(5000), "SomeTable", "TabelleÜber"))
	if err := e.SetSearch(SearchOptions{Query: "col1"}); err != nil {
		b.Fatalf("SetSearch: %v", err)
	}

	b.ResetTimer()
	for b.Loop() {
		e.search.scanned = false
		e.scanMatches()
	}
}
