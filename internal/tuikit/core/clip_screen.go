package core

import "github.com/gdamore/tcell/v3"

// ---------------------------------------------------------------------------
// ClipScreen
// ---------------------------------------------------------------------------

// ClipScreen wraps a tcell.Screen and drops cell writes outside a clip
// rectangle, confining widgets that draw at fixed offsets without each Draw
// learning to clip. It covers SetContent, the Put family (which drawing.go
// bottoms out in), and Fill/FillArea/Clear.
type ClipScreen struct {
	tcell.Screen
	clip Rect
}

// NewClipScreen wraps s with the clip set to the whole screen.
func NewClipScreen(s tcell.Screen) *ClipScreen {
	c := &ClipScreen{Screen: s}
	c.ResetClip()
	return c
}

// SetClip narrows drawing to r.
func (c *ClipScreen) SetClip(r Rect) { c.clip = r }

// ResetClip widens the clip back to the whole screen.
func (c *ClipScreen) ResetClip() {
	w, h := c.Screen.Size()
	c.clip = Rect{W: w, H: h}
}

// Clip returns the current clip rectangle.
func (c *ClipScreen) Clip() Rect { return c.clip }

// SetContent writes the cell only if it lies inside the clip.
func (c *ClipScreen) SetContent(x, y int, primary rune, combining []rune, style tcell.Style) {
	if !c.clip.Contains(x, y) {
		return
	}
	c.Screen.SetContent(x, y, primary, combining, style)
}

// Put writes the first grapheme of str only if (x,y) is inside the clip. It
// reports the same remainder and width either way, so a caller advancing across
// a row (DimArea) steps over clipped cells rather than stalling.
func (c *ClipScreen) Put(x, y int, str string, style tcell.Style) (string, int) {
	if c.clip.Contains(x, y) {
		return c.Screen.Put(x, y, str, style)
	}
	head, rest := splitGrapheme(str, 1)
	return rest, DisplayWidth(head)
}

// PutStrStyled writes str one grapheme at a time through Put, so the part of
// it inside the clip lands and the rest is dropped.
func (c *ClipScreen) PutStrStyled(x, y int, str string, style tcell.Style) {
	sw, _ := c.Screen.Size()
	for str != "" && x < sw {
		var w int
		str, w = c.Put(x, y, str, style)
		if w < 1 {
			w = 1
		}
		x += w
	}
}

// PutStr writes str in the default style, clipped.
func (c *ClipScreen) PutStr(x, y int, str string) {
	c.PutStrStyled(x, y, str, tcell.StyleDefault)
}

// FillArea fills the part of the area inside the clip; tcell's own clips only
// to the screen.
func (c *ClipScreen) FillArea(x, y, w, h int, ch rune, style tcell.Style) {
	x0, y0 := max(x, c.clip.X), max(y, c.clip.Y)
	x1, y1 := min(x+w, c.clip.Right()), min(y+h, c.clip.Bottom())
	if x1 <= x0 || y1 <= y0 {
		return
	}
	c.Screen.FillArea(x0, y0, x1-x0, y1-y0, ch, style)
}

// Fill fills the clip rectangle rather than the whole screen.
func (c *ClipScreen) Fill(ch rune, style tcell.Style) {
	FillRect(c, c.clip, ch, style)
}

// Clear erases the clip rectangle rather than the whole screen.
func (c *ClipScreen) Clear() { c.Fill(' ', tcell.StyleDefault) }

// SetClip narrows drawing on s to r when s is a *ClipScreen, else does nothing,
// so Draw methods needn't care whether the caller supplied a clipping screen.
func SetClip(s tcell.Screen, r Rect) {
	if c, ok := s.(*ClipScreen); ok {
		c.SetClip(r)
	}
}
