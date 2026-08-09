package controls

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// discardScreen swallows every cell. The recording screens the tests use are
// backed by a map, whose insert cost per cell swamps what these benchmarks
// are trying to measure.
type discardScreen struct {
	tcell.Screen
	w, h int
}

func (s *discardScreen) Size() (int, int)                               { return s.w, s.h }
func (s *discardScreen) SetContent(int, int, rune, []rune, tcell.Style) {}
func (s *discardScreen) ShowCursor(int, int)                            {}

// benchScript builds an n-line T-SQL script containing block comments, so the
// highlighter's cross-line state actually has something to track.
func benchScript(n int) string {
	var sb strings.Builder
	for i := range n {
		switch i % 10 {
		case 3:
			sb.WriteString("/* a block comment opened here\n")
		case 5:
			sb.WriteString("   still inside it */ SELECT 'x' FROM dbo.SomeTable\n")
		default:
			sb.WriteString("SELECT col1, col2, 42 FROM dbo.SomeTable WHERE col1 = 'value'\n")
		}
	}
	return sb.String()
}

// BenchmarkEditorDrawHighlighted10k is the measurement behind the
// prefix-state cache: a Draw pass over a 40-row viewport scrolled to the
// bottom of a 10,000-line script. The cost that mattered was
// startsInBlockComment replaying the whole document for the pass's first row,
// which no per-call memo could avoid — a pass starts at scrollRow and the
// previous one ended at scrollRow+H-1, so the sequence breaks at every pass
// boundary. Keyed on the document version instead, a pass that follows no
// edit does no replay at all.
func BenchmarkEditorDrawHighlighted10k(b *testing.B) {
	e := NewEditor(SQLHighlighter(&theme.Default))
	e.SetText(benchScript(10000))
	e.SetBounds(0, 0, 100, 40)
	e.scrollRow = e.doc.Len() - 40
	s := &discardScreen{w: 100, h: 40}

	b.ResetTimer()
	for b.Loop() {
		e.Draw(s)
	}
}

// BenchmarkEditorDrawHighlightedTyping10k is the same viewport with an edit
// between passes — the case that still pays one replay, and the one the cache
// must not skip. It bounds what typing in a large script costs.
func BenchmarkEditorDrawHighlightedTyping10k(b *testing.B) {
	e := NewEditor(SQLHighlighter(&theme.Default))
	e.SetText(benchScript(10000))
	e.SetBounds(0, 0, 100, 40)
	e.cursorRow, e.cursorCol = e.doc.Len()-1, 0
	e.ensureCursorVisible()
	s := &discardScreen{w: 100, h: 40}

	b.ResetTimer()
	for b.Loop() {
		e.insertRune('x')
		e.Draw(s)
	}
}

// BenchmarkEditorTypeInto20k is the measurement behind undo steps being
// deltas: one keystroke through HandleKey, which pushes an undo step, into a
// 20,000-line script. A step that copied the whole buffer cost 3.2 ms and
// 5 MB per character here — the editor's single worst per-keystroke cost, and
// paid whether or not the user ever pressed Ctrl+Z.
//
// Typing is measured without a Draw so the step is what is being timed rather
// than the highlighter; BenchmarkEditorDrawHighlightedTyping10k bounds the
// other half.
func BenchmarkEditorTypeInto20k(b *testing.B) {
	e := NewEditor(nil)
	e.SetText(benchScript(20000))
	e.SetBounds(0, 0, 100, 40)
	k := runeKey('x', tcell.ModNone)

	// The caret moves on each iteration so no single line grows by one rune
	// per iteration: a benchmark that types into the same line ends up
	// measuring a 40 KB line rather than a 60-character one, and reports the
	// per-keystroke cost as several times what it is.
	row := 0
	b.ResetTimer()
	for b.Loop() {
		e.cursorRow, e.cursorCol = row, 0
		e.HandleKey(k)
		row++
		if row >= e.doc.Len() {
			row = 0
		}
	}
}

// BenchmarkEditorDrawWrappedLargeCell mirrors DataGrid's full-cell viewer: one
// very long logical line, read-only, behind a small window. buildVisualLines
// re-segmented the whole value on every event before it was memoised.
func BenchmarkEditorDrawWrappedLargeCell(b *testing.B) {
	e := NewEditor(nil)
	e.SetWrapMode(true)
	e.SetReadOnly(true)
	e.SetGutterVisible(false)
	e.SetText(strings.Repeat("<Node attribute=\"value\" other=\"12345\"/> ", 5000))
	e.SetBounds(0, 0, 80, 15)
	s := &discardScreen{w: 80, h: 15}

	b.ResetTimer()
	for b.Loop() {
		e.Draw(s)
	}
}
