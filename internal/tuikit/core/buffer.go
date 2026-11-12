package core

import (
	"unicode/utf8"

	"github.com/clipperhouse/displaywidth"
	"github.com/gdamore/tcell/v3"
)

// Buffer is an off-screen cell grid that satisfies tcell.Screen well enough for
// drawing. It has two users: a dashboard is rendered into a fixed-size Buffer
// and the visible window blitted onto the real screen, so it scrolls on both
// axes without each chart knowing the offset; and tests draw into one and read
// the cells back (tuikittest builds on it).
//
// The drawing half of tcell.Screen is implemented (Size, SetContent, Get, Put,
// PutStr, PutStrStyled, Fill, FillArea, Clear, ShowCursor, HideCursor), with
// the cursor recorded for Cursor to report. The embedded interface is nil, so
// any other method (Init, Show, EventQ, ...) panics deliberately: it means a
// Buffer was confused with a real screen.
type Buffer struct {
	tcell.Screen // nil; see the type comment

	w, h  int
	cells []bufferCell

	curX, curY int // last ShowCursor; -1,-1 when hidden
}

// bufferCell is one grid cell. width is its display width: 1 for an ordinary
// cell, 2 for the first cell of a wide grapheme, 0 for the trailing cell it
// covers (no content, skipped when blitting).
//
// Content is a primary rune plus the rest of its grapheme, not one string (an
// allocation per cell, 62% of the dashboard draw path's allocations). rest is
// non-empty only for a cell carrying combining marks.
type bufferCell struct {
	primary rune // 0 in a trailing cell, which has no content of its own
	rest    string
	style   tcell.Style
	width   int
}

// text is the cell's content as one string, allocated on demand (Get and Row
// want a string, the draw path does not).
func (c bufferCell) text() string {
	switch {
	case c.primary == 0:
		return ""
	case c.rest == "":
		return string(c.primary)
	}
	return string(c.primary) + c.rest
}

// NewBuffer returns a w×h buffer of spaces in tcell.StyleDefault. Non-positive
// dimensions clamp to zero, giving a buffer that discards every draw.
func NewBuffer(w, h int) *Buffer {
	w, h = max(w, 0), max(h, 0)
	c := &Buffer{w: w, h: h, cells: make([]bufferCell, w*h), curX: -1, curY: -1}
	c.Fill(' ', tcell.StyleDefault)
	return c
}

// Size returns the buffer dimensions (tcell.Screen).
func (c *Buffer) Size() (int, int) { return c.w, c.h }

// Rect is the buffer's full extent.
func (c *Buffer) Rect() Rect { return Rect{X: 0, Y: 0, W: c.w, H: c.h} }

// Fill sets every cell to ch in style (tcell.Screen).
func (c *Buffer) Fill(ch rune, style tcell.Style) {
	cell := bufferCell{primary: ch, style: style, width: max(displaywidth.Rune(ch), 1)}
	for i := range c.cells {
		c.cells[i] = cell
	}
}

// FillArea sets every cell of the w×h area at (x,y) to ch in style
// (tcell.Screen). The part of the area off the buffer is ignored, as tcell
// ignores the part off the screen.
func (c *Buffer) FillArea(x, y, w, h int, ch rune, style tcell.Style) {
	for row := max(y, 0); row < min(y+h, c.h); row++ {
		for col := max(x, 0); col < min(x+w, c.w); col++ {
			c.setRune(col, row, ch, style)
		}
	}
}

// Clear fills the buffer with spaces in tcell.StyleDefault (tcell.Screen).
func (c *Buffer) Clear() { c.Fill(' ', tcell.StyleDefault) }

// SetContent writes primary plus any combining runes at (x,y)
// (tcell.Screen). Out-of-range coordinates are ignored.
func (c *Buffer) SetContent(x, y int, primary rune, combining []rune, style tcell.Style) {
	if len(combining) == 0 {
		// Same per-cell fast path as Put's single rune: no string, no segmentation.
		c.setRune(x, y, primary, style)
		return
	}
	c.set(x, y, string(append([]rune{primary}, combining...)), style)
}

// Put writes the first grapheme of str at (x,y) and returns the rest of the
// string along with the width written (tcell.Screen).
func (c *Buffer) Put(x, y int, str string, style tcell.Style) (string, int) {
	if r, n := utf8.DecodeRuneInString(str); n == len(str) && n > 0 {
		// One rune (PutRune's call, one per cell per chart): must not segment.
		c.setRune(x, y, r, style)
		return "", max(displaywidth.Rune(r), 1)
	}
	g := displaywidth.StringGraphemes(str)
	if !g.Next() {
		return "", 0
	}
	first := g.Value()
	w := max(g.Width(), 1)
	c.set(x, y, first, style)
	return str[len(first):], w
}

// PutStr writes str from (x,y) in tcell.StyleDefault (tcell.Screen).
func (c *Buffer) PutStr(x, y int, str string) { c.PutStrStyled(x, y, str, tcell.StyleDefault) }

// PutStrStyled writes str from (x,y), one grapheme per Put, clipped at the
// buffer's right edge (tcell.Screen).
func (c *Buffer) PutStrStyled(x, y int, str string, style tcell.Style) {
	for str != "" && x < c.w {
		var w int
		str, w = c.Put(x, y, str, style)
		x += w
	}
}

// ShowCursor records the cursor position (tcell.Screen); Cursor reports it.
// A position off the buffer hides it, as on a real screen.
func (c *Buffer) ShowCursor(x, y int) {
	if !c.inBounds(x, y) {
		x, y = -1, -1
	}
	c.curX, c.curY = x, y
}

// HideCursor hides the cursor (tcell.Screen).
func (c *Buffer) HideCursor() { c.curX, c.curY = -1, -1 }

// Cursor returns the last ShowCursor position, and whether it is visible.
func (c *Buffer) Cursor() (x, y int, visible bool) {
	return c.curX, c.curY, c.curX >= 0
}

// RuneAt returns the primary rune at (x,y): 0 for the trailing cell of a wide
// grapheme and off the buffer.
func (c *Buffer) RuneAt(x, y int) rune { return c.cellAt(x, y).primary }

// StyleAt returns the style at (x,y), tcell.StyleDefault off the buffer.
func (c *Buffer) StyleAt(x, y int) tcell.Style {
	if !c.inBounds(x, y) {
		return tcell.StyleDefault
	}
	return c.cells[y*c.w+x].style
}

// Get returns the content, style, and display width at (x,y)
// (tcell.Screen). Out-of-range coordinates return the zero values, matching
// tcell's own contract.
func (c *Buffer) Get(x, y int) (string, tcell.Style, int) {
	if !c.inBounds(x, y) {
		return "", tcell.StyleDefault, 0
	}
	cell := c.cells[y*c.w+x]
	return cell.text(), cell.style, cell.width
}

// set writes one grapheme, measuring it with a full segmentation.
func (c *Buffer) set(x, y int, str string, style tcell.Style) {
	if !c.inBounds(x, y) {
		return
	}
	primary, n := utf8.DecodeRuneInString(str)
	if n == 0 {
		return
	}
	c.write(x, y, bufferCell{
		primary: primary,
		rest:    str[n:],
		style:   style,
		width:   max(displaywidth.String(str), 1),
	})
}

// setRune writes a single-rune grapheme, measuring it with the cheap
// per-rune width rather than a segmentation.
func (c *Buffer) setRune(x, y int, r rune, style tcell.Style) {
	if !c.inBounds(x, y) {
		return
	}
	if r == 0 {
		// A zero primary marks a trailing cell, so it can't also mean content; tcell
		// draws a blank for one and so does this.
		r = ' '
	}
	c.write(x, y, bufferCell{primary: r, style: style, width: max(displaywidth.Rune(r), 1)})
}

// write stores one composed cell, reserving the trailing cell of a wide
// grapheme so a later Get or Blit doesn't emit the same glyph twice.
func (c *Buffer) write(x, y int, cell bufferCell) {
	if x+cell.width > c.w {
		// A wide grapheme in the last column has no room for its second half; tcell
		// substitutes a space and so does this.
		cell = bufferCell{primary: ' ', style: cell.style, width: 1}
	}
	i := y*c.w + x
	c.cells[i] = cell
	for k := 1; k < cell.width; k++ {
		c.cells[i+k] = bufferCell{style: cell.style}
	}
}

// cellAt is Get without composing a string; out-of-range reads come back as
// the zero cell, which has width 0 and so draws nothing.
func (c *Buffer) cellAt(x, y int) bufferCell {
	if !c.inBounds(x, y) {
		return bufferCell{}
	}
	return c.cells[y*c.w+x]
}

func (c *Buffer) inBounds(x, y int) bool {
	return x >= 0 && y >= 0 && x < c.w && y < c.h
}

// Blit copies the src region onto s at dst, clipping a src past the buffer edge
// and a dst smaller than src. A wide grapheme whose second half falls outside
// dst becomes a space, so no stray half-glyph bleeds beside the viewport.
func (c *Buffer) Blit(s tcell.Screen, src Rect, dst Rect) {
	rows := min(src.H, dst.H)
	cols := min(src.W, dst.W)
	for dy := range rows {
		for dx := 0; dx < cols; {
			// Read the cell rather than Get it: Get composes a string per cell.
			cell := c.cellAt(src.X+dx, src.Y+dy)
			if cell.width == 0 {
				// Trailing half of a wide grapheme, or off-buffer: nothing of its own to draw.
				dx++
				continue
			}
			if dx+cell.width > cols {
				PutRune(s, dst.X+dx, dst.Y+dy, ' ', cell.style)
				break
			}
			if cell.rest == "" {
				PutRune(s, dst.X+dx, dst.Y+dy, cell.primary, cell.style)
			} else {
				s.Put(dst.X+dx, dst.Y+dy, cell.text(), cell.style)
			}
			dx += cell.width
		}
	}
}

// Row returns row y as a plain string, one entry per cell of content (trailing
// cells of wide graphemes contribute nothing). For tests and golden files; it
// carries no style.
func (c *Buffer) Row(y int) string { return c.Text(0, y, c.w) }

// Text returns the w columns of row y from x as Row does; the part off the
// buffer contributes nothing.
func (c *Buffer) Text(x, y, w int) string {
	if y < 0 || y >= c.h {
		return ""
	}
	out := make([]rune, 0, max(w, 0))
	for col := max(x, 0); col < min(x+w, c.w); col++ {
		cell := c.cells[y*c.w+col]
		if cell.width == 0 {
			continue
		}
		if cell.primary == 0 {
			out = append(out, ' ')
			continue
		}
		out = append(out, cell.primary)
		out = append(out, []rune(cell.rest)...)
	}
	return string(out)
}

// Rows returns every row as Row does, top to bottom.
func (c *Buffer) Rows() []string {
	out := make([]string, c.h)
	for y := range out {
		out[y] = c.Row(y)
	}
	return out
}
