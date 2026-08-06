package charts

import (
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/core"
)

// recordScreen captures SetContent calls so a Blit can be asserted against
// what actually reached the screen, including position.
type recordScreen struct {
	tcell.Screen
	cells map[[2]int]rune
}

func newRecordScreen() *recordScreen {
	return &recordScreen{cells: map[[2]int]rune{}}
}

func (r *recordScreen) SetContent(x, y int, primary rune, _ []rune, _ tcell.Style) {
	r.cells[[2]int{x, y}] = primary
}

func (r *recordScreen) at(x, y int) rune { return r.cells[[2]int{x, y}] }

func TestCanvasSetContentAndRow(t *testing.T) {
	c := NewCanvas(6, 2)
	core.DrawText(c, 1, 0, tcell.StyleDefault, "abc")

	if got, want := c.Row(0), " abc  "; got != want {
		t.Errorf("Row(0) = %q, want %q", got, want)
	}
	if got, want := c.Row(1), "      "; got != want {
		t.Errorf("Row(1) = %q, want %q", got, want)
	}
	if str, _, w := c.Get(1, 0); str != "a" || w != 1 {
		t.Errorf("Get(1,0) = %q width %d, want \"a\" width 1", str, w)
	}
}

func TestCanvasOutOfRangeIsIgnored(t *testing.T) {
	c := NewCanvas(3, 1)
	c.SetContent(-1, 0, 'x', nil, tcell.StyleDefault)
	c.SetContent(0, 5, 'x', nil, tcell.StyleDefault)
	c.SetContent(9, 0, 'x', nil, tcell.StyleDefault)

	if got, want := c.Row(0), "   "; got != want {
		t.Errorf("Row(0) = %q, want %q — out-of-range writes must not land", got, want)
	}
	if str, _, w := c.Get(9, 0); str != "" || w != 0 {
		t.Errorf("Get(9,0) = %q width %d, want empty width 0", str, w)
	}
}

func TestCanvasWideGraphemeReservesTrailingCell(t *testing.T) {
	c := NewCanvas(4, 1)
	c.SetContent(0, 0, '漢', nil, tcell.StyleDefault)

	if _, _, w := c.Get(0, 0); w != 2 {
		t.Errorf("wide grapheme width = %d, want 2", w)
	}
	if str, _, w := c.Get(1, 0); str != "" || w != 0 {
		t.Errorf("trailing cell = %q width %d, want empty width 0", str, w)
	}
	if got, want := c.Row(0), "漢  "; got != want {
		t.Errorf("Row(0) = %q, want %q — trailing cell must not re-emit the glyph", got, want)
	}
}

func TestCanvasWideGraphemeInLastColumnBecomesSpace(t *testing.T) {
	c := NewCanvas(2, 1)
	c.SetContent(1, 0, '漢', nil, tcell.StyleDefault)

	if str, _, w := c.Get(1, 0); str != " " || w != 1 {
		t.Errorf("last-column wide grapheme = %q width %d, want \" \" width 1", str, w)
	}
}

func TestCanvasBlitCopiesWindow(t *testing.T) {
	c := NewCanvas(6, 3)
	core.DrawText(c, 0, 1, tcell.StyleDefault, "abcdef")

	s := newRecordScreen()
	c.Blit(s, core.Rect{X: 2, Y: 1, W: 3, H: 1}, core.Rect{X: 10, Y: 5, W: 3, H: 1})

	for i, want := range []rune{'c', 'd', 'e'} {
		if got := s.at(10+i, 5); got != want {
			t.Errorf("blitted cell %d = %q, want %q", i, got, want)
		}
	}
	if got := s.at(13, 5); got != 0 {
		t.Errorf("blit wrote past the destination width: %q", got)
	}
}

func TestCanvasBlitClipsToSmallerDestination(t *testing.T) {
	c := NewCanvas(4, 4)
	core.DrawText(c, 0, 0, tcell.StyleDefault, "abcd")

	s := newRecordScreen()
	c.Blit(s, c.Rect(), core.Rect{X: 0, Y: 0, W: 2, H: 1})

	if got := s.at(1, 0); got != 'b' {
		t.Errorf("cell (1,0) = %q, want 'b'", got)
	}
	if got := s.at(2, 0); got != 0 {
		t.Errorf("blit exceeded the destination rect at (2,0): %q", got)
	}
}

func TestCanvasBlitReplacesStraddlingWideGlyphWithSpace(t *testing.T) {
	c := NewCanvas(4, 1)
	c.SetContent(1, 0, '漢', nil, tcell.StyleDefault)

	s := newRecordScreen()
	// The destination is two columns wide, so the wide glyph at src column
	// 1 has only one column to live in.
	c.Blit(s, core.Rect{X: 0, Y: 0, W: 4, H: 1}, core.Rect{X: 0, Y: 0, W: 2, H: 1})

	if got := s.at(1, 0); got != ' ' {
		t.Errorf("straddling wide glyph = %q, want a space", got)
	}
}

func TestNewCanvasClampsNegativeDimensions(t *testing.T) {
	c := NewCanvas(-3, -1)
	if w, h := c.Size(); w != 0 || h != 0 {
		t.Fatalf("Size() = %d×%d, want 0×0", w, h)
	}
	c.SetContent(0, 0, 'x', nil, tcell.StyleDefault)
	if got := c.Row(0); got != "" {
		t.Errorf("Row(0) = %q, want empty", got)
	}
}
