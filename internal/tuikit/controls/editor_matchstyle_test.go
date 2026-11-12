package controls

import (
	"strings"
	"testing"

	"github.com/radix29/gossms/internal/tuikit/theme"
	"github.com/radix29/gossms/internal/tuikit/tuikittest"
)

// marks reads row y back as one character per column: 'm' for the search
// match style, 's' for the selection style, '.' for anything else.
func marks(s *tuikittest.Screen, y, w int) string {
	match, sel := theme.StyleSearchMatch(), theme.StyleSelected()
	var sb strings.Builder
	for x := range w {
		switch s.StyleAt(x, y) {
		case match:
			sb.WriteByte('m')
		case sel:
			sb.WriteByte('s')
		default:
			sb.WriteByte('.')
		}
	}
	return sb.String()
}

// styleEditor returns a gutterless editor sized w×h at the origin, with no
// highlighter so the only styles in play are default, match and selection.
func styleEditor(text string, w, h int) *Editor {
	e := NewEditor(nil)
	e.SetGutterVisible(false)
	e.SetText(text)
	e.SetBounds(0, 0, w, h)
	return e
}

// Every match on a line must be painted, not just the first — and the columns
// between them must not be. A lookup that stops advancing, or that advances
// past a match it has not finished, shows up here as a shifted or missing run.
func TestDrawPaintsEveryMatchOnALine(t *testing.T) {
	e := styleEditor("ab..ab...ab", 11, 3)
	setSearch(t, e, SearchOptions{Query: "ab"})

	s := tuikittest.NewScreen(11, 3)
	e.Draw(s)

	if got, want := marks(s, 0, 11), "mm..mm...mm"; got != want {
		t.Errorf("styles = %q, want %q", got, want)
	}
}

// The current match is the editor's selection, and the selection style wins.
// The others stay in the match style, which is what tells them apart.
func TestDrawPaintsCurrentMatchAsSelection(t *testing.T) {
	e := styleEditor("ab..ab", 6, 3)
	setSearch(t, e, SearchOptions{Query: "ab"})
	if !e.FindNext(1) {
		t.Fatal("FindNext found nothing — test premise is wrong")
	}

	s := tuikittest.NewScreen(6, 3)
	e.Draw(s)

	if got, want := marks(s, 0, 6), "ss..mm"; got != want {
		t.Errorf("styles = %q, want %q", got, want)
	}
}

// Horizontal scrolling starts the row's first styled column part-way into the
// line: the match lookup has to arrive already advanced past everything left
// of the window, not restart at the line's first match.
func TestDrawPaintsMatchesUnderHorizontalScroll(t *testing.T) {
	e := styleEditor("ab..ab..ab..ab", 6, 3)
	setSearch(t, e, SearchOptions{Query: "ab"})
	e.scrollCol = 8 // window shows "ab..ab"

	s := tuikittest.NewScreen(6, 3)
	e.Draw(s)

	if got, want := marks(s, 0, 6), "mm..mm"; got != want {
		t.Errorf("styles = %q, want %q", got, want)
	}
}

// Wrap mode draws each segment of a logical line as its own row, so the
// lookup restarts per segment while the rune indices keep climbing. A cursor
// that is not re-primed for the segment's starting index paints the wrong
// columns on every row after the first.
func TestDrawPaintsMatchesInWrapMode(t *testing.T) {
	// Width 5 wraps at 4: wrapWidth keeps the last column for the caret.
	e := styleEditor("ab..ab..ab..ab..", 5, 6)
	e.SetWrapMode(true)
	setSearch(t, e, SearchOptions{Query: "ab"})

	s := tuikittest.NewScreen(5, 6)
	e.Draw(s)

	for row := range 4 {
		if got, want := marks(s, row, 4), "mm.."; got != want {
			t.Errorf("wrapped row %d styles = %q, want %q", row, got, want)
		}
	}
}

// A wide rune inside a match must carry the match style across both of its
// cells: the second is drawn by tcell as a continuation of the first, so the
// style the first cell was given is what shows.
func TestDrawPaintsMatchesAroundWideRunes(t *testing.T) {
	e := styleEditor("x漢字x", 6, 3)
	setSearch(t, e, SearchOptions{Query: "漢字"})

	s := tuikittest.NewScreen(6, 3)
	e.Draw(s)

	// Columns: x=0, 漢=1-2, 字=3-4, x=5. The screen styles a continuation
	// column as its glyph, as tcell does, so all four read as matched.
	if got, want := marks(s, 0, 6), ".mmmm."; got != want {
		t.Errorf("styles = %q, want %q", got, want)
	}
}

// BenchmarkEditorDrawManyMatchesWideLine is the measurement behind the
// advancing match cursor: one common short string searched in a line far
// wider than the window. styleForRune scanned the line's whole match list for
// every rune it styled, making a pass cost visible columns × matches on that
// line even though only a handful of those matches are on screen.
func BenchmarkEditorDrawManyMatchesWideLine(b *testing.B) {
	e := NewEditor(nil)
	e.SetGutterVisible(false)
	e.SetText(strings.Repeat("SELECT col FROM dbo.T WHERE col = 'x'; ", 400))
	e.SetBounds(0, 0, 100, 40)
	if err := e.SetSearch(SearchOptions{Query: "col"}); err != nil {
		b.Fatalf("SetSearch: %v", err)
	}
	s := &discardScreen{w: 100, h: 40}

	b.ResetTimer()
	for b.Loop() {
		e.Draw(s)
	}
}

// The same line under wrap mode, where every drawn row is a segment of it and
// so every row pays the lookup — DataGrid's full-cell viewer with a search
// active.
func BenchmarkEditorDrawManyMatchesWrapped(b *testing.B) {
	e := NewEditor(nil)
	e.SetGutterVisible(false)
	e.SetWrapMode(true)
	e.SetReadOnly(true)
	e.SetText(strings.Repeat("SELECT col FROM dbo.T WHERE col = 'x'; ", 400))
	e.SetBounds(0, 0, 80, 15)
	if err := e.SetSearch(SearchOptions{Query: "col"}); err != nil {
		b.Fatalf("SetSearch: %v", err)
	}
	s := &discardScreen{w: 80, h: 15}

	b.ResetTimer()
	for b.Loop() {
		e.Draw(s)
	}
}
