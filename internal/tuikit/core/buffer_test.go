package core

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// dotted is a w×h Buffer pre-filled with '.', so a cell nothing wrote to
// reads back as '.' (a drawn space would be indistinguishable otherwise).
func dotted(w, h int) *Buffer {
	b := NewBuffer(w, h)
	b.Fill('.', tcell.StyleDefault)
	return b
}

// written counts the cells of b that are no longer '.'.
func written(b *Buffer) int {
	n := 0
	w, h := b.Size()
	for y := range h {
		for x := range w {
			if b.RuneAt(x, y) != '.' {
				n++
			}
		}
	}
	return n
}

func TestBufferSetContentAndRow(t *testing.T) {
	c := NewBuffer(6, 2)
	DrawText(c, 1, 0, tcell.StyleDefault, "abc")

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

func TestBufferOutOfRangeIsIgnored(t *testing.T) {
	c := NewBuffer(3, 1)
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

// TestBufferFillAreaFillsOnlyTheArea pins T65: Buffer had no FillArea, so
// the call fell through to the nil embedded Screen and panicked.
func TestBufferFillAreaFillsOnlyTheArea(t *testing.T) {
	c := NewBuffer(5, 3)
	c.FillArea(-1, 1, 3, 5, '#', tcell.StyleDefault)

	for y, want := range []string{"     ", "##   ", "##   "} {
		if got := c.Row(y); got != want {
			t.Errorf("Row(%d) = %q, want %q", y, got, want)
		}
	}
}

func TestBufferWideGraphemeReservesTrailingCell(t *testing.T) {
	c := NewBuffer(4, 1)
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

func TestBufferWideGraphemeInLastColumnBecomesSpace(t *testing.T) {
	c := NewBuffer(2, 1)
	c.SetContent(1, 0, '漢', nil, tcell.StyleDefault)

	if str, _, w := c.Get(1, 0); str != " " || w != 1 {
		t.Errorf("last-column wide grapheme = %q width %d, want \" \" width 1", str, w)
	}
}

func TestBufferBlitCopiesWindow(t *testing.T) {
	c := NewBuffer(6, 3)
	DrawText(c, 0, 1, tcell.StyleDefault, "abcdef")

	s := dotted(20, 8)
	c.Blit(s, Rect{X: 2, Y: 1, W: 3, H: 1}, Rect{X: 10, Y: 5, W: 3, H: 1})

	for i, want := range []rune{'c', 'd', 'e'} {
		if got := s.RuneAt(10+i, 5); got != want {
			t.Errorf("blitted cell %d = %q, want %q", i, got, want)
		}
	}
	if got := s.RuneAt(13, 5); got != '.' {
		t.Errorf("blit wrote past the destination width: %q", got)
	}
}

func TestBufferBlitClipsToSmallerDestination(t *testing.T) {
	c := NewBuffer(4, 4)
	DrawText(c, 0, 0, tcell.StyleDefault, "abcd")

	s := dotted(20, 8)
	c.Blit(s, c.Rect(), Rect{X: 0, Y: 0, W: 2, H: 1})

	if got := s.RuneAt(1, 0); got != 'b' {
		t.Errorf("cell (1,0) = %q, want 'b'", got)
	}
	if got := s.RuneAt(2, 0); got != '.' {
		t.Errorf("blit exceeded the destination rect at (2,0): %q", got)
	}
}

func TestBufferBlitReplacesStraddlingWideGlyphWithSpace(t *testing.T) {
	c := NewBuffer(4, 1)
	c.SetContent(1, 0, '漢', nil, tcell.StyleDefault)

	s := dotted(20, 8)
	// The destination is two columns wide, so the wide glyph at src column
	// 1 has only one column to live in.
	c.Blit(s, Rect{X: 0, Y: 0, W: 4, H: 1}, Rect{X: 0, Y: 0, W: 2, H: 1})

	if got := s.RuneAt(1, 0); got != ' ' {
		t.Errorf("straddling wide glyph = %q, want a space", got)
	}
}

func TestNewBufferClampsNegativeDimensions(t *testing.T) {
	c := NewBuffer(-3, -1)
	if w, h := c.Size(); w != 0 || h != 0 {
		t.Fatalf("Size() = %d×%d, want 0×0", w, h)
	}
	c.SetContent(0, 0, 'x', nil, tcell.StyleDefault)
	if got := c.Row(0); got != "" {
		t.Errorf("Row(0) = %q, want empty", got)
	}
}

func TestBufferPutStrStyledClipsAtTheRightEdge(t *testing.T) {
	b := NewBuffer(5, 1)
	b.PutStrStyled(1, 0, "a日bc", tcell.StyleDefault.Bold(true))
	if got, want := b.Row(0), " a日b"; got != want {
		t.Errorf("Row(0) = %q, want %q", got, want)
	}
	if !b.StyleAt(1, 0).HasBold() {
		t.Error("PutStrStyled dropped the style")
	}
	b.PutStr(0, 0, "z")
	if got := b.RuneAt(0, 0); got != 'z' {
		t.Errorf("PutStr: RuneAt(0,0) = %q, want 'z'", got)
	}
}

// TestBufferRecordsTheCursor: a Buffer used to panic on ShowCursor (the call
// fell through to the nil embedded Screen), so an Editor could not draw into
// one.
func TestBufferRecordsTheCursor(t *testing.T) {
	b := NewBuffer(4, 2)
	if _, _, ok := b.Cursor(); ok {
		t.Fatal("a new Buffer shows a cursor")
	}
	b.ShowCursor(3, 1)
	if x, y, ok := b.Cursor(); !ok || x != 3 || y != 1 {
		t.Errorf("Cursor() = %d,%d,%v, want 3,1,true", x, y, ok)
	}
	b.ShowCursor(4, 1)
	if _, _, ok := b.Cursor(); ok {
		t.Error("an off-buffer ShowCursor left the cursor visible")
	}
	b.ShowCursor(0, 0)
	b.HideCursor()
	if _, _, ok := b.Cursor(); ok {
		t.Error("HideCursor left the cursor visible")
	}
}

func TestBufferTextReadsAColumnRange(t *testing.T) {
	b := NewBuffer(8, 1)
	DrawText(b, 0, 0, tcell.StyleDefault, "a日bcd")
	if got, want := b.Text(1, 0, 4), "日bc"; got != want {
		t.Errorf("Text(1,0,4) = %q, want %q", got, want)
	}
	if got, want := b.Text(-2, 0, 3), "a"; got != want {
		t.Errorf("Text(-2,0,3) = %q, want %q", got, want)
	}
	if got := b.RuneAt(2, 0); got != 0 {
		t.Errorf("RuneAt on a wide glyph's trailing cell = %q, want 0", got)
	}
}
