package core

import (
	"github.com/clipperhouse/displaywidth"
	"github.com/gdamore/tcell/v3"
)

// ---------------------------------------------------------------------------
// Drawing primitives
// ---------------------------------------------------------------------------

// DrawText draws text starting at (x,y), no clipping. Wide characters
// (e.g. CJK) and multi-rune grapheme clusters (e.g. flags, combining marks)
// advance the column by their true display width, not by 1-per-rune.
func DrawText(s tcell.Screen, x, y int, style tcell.Style, text string) {
	col := x
	g := displaywidth.StringGraphemes(text)
	for g.Next() {
		w := g.Width()
		if w <= 0 {
			// Zero-width grapheme (e.g. a lone combining mark): still emit
			// it as a combining rune on the previous cell when possible,
			// otherwise just skip — there is no cell to advance into.
			continue
		}
		putGrapheme(s, col, y, g.Value(), style)
		col += w
	}
}

// DrawTextClipped draws text clipped to maxW display columns (not runes).
func DrawTextClipped(s tcell.Screen, x, y, maxW int, style tcell.Style, text string) {
	col := x
	g := displaywidth.StringGraphemes(text)
	for g.Next() {
		w := g.Width()
		if w <= 0 {
			continue
		}
		if col+w > x+maxW {
			break
		}
		putGrapheme(s, col, y, g.Value(), style)
		col += w
	}
}

// DrawTextRight draws text right-aligned within w display columns, ending at x+w.
func DrawTextRight(s tcell.Screen, x, y, w int, style tcell.Style, text string) {
	text = Truncate(text, w)
	tw := displaywidth.String(text)
	startX := x + w - tw
	col := startX
	g := displaywidth.StringGraphemes(text)
	for g.Next() {
		gw := g.Width()
		if gw <= 0 {
			continue
		}
		putGrapheme(s, col, y, g.Value(), style)
		col += gw
	}
}

// putGrapheme writes a (possibly multi-rune) grapheme cluster to the screen
// starting at (x,y). The first rune is the primary cell content; any
// additional runes are passed as combining characters. Wide graphemes
// (width 2) occupy two cells per tcell's SetContent contract — the second
// cell is conventionally left for the terminal to render as part of the
// wide glyph, so only the first cell receives content.
func putGrapheme(s tcell.Screen, x, y int, grapheme string, style tcell.Style) {
	runes := []rune(grapheme)
	if len(runes) == 0 {
		return
	}
	var comb []rune
	if len(runes) > 1 {
		comb = runes[1:]
	}
	s.SetContent(x, y, runes[0], comb, style)
}

// FillRect fills a rectangle with the given rune and style.
func FillRect(s tcell.Screen, r Rect, ch rune, style tcell.Style) {
	for row := r.Y; row < r.Y+r.H; row++ {
		for col := r.X; col < r.X+r.W; col++ {
			s.SetContent(col, row, ch, nil, style)
		}
	}
}

// ClearRect fills a rectangle with spaces.
func ClearRect(s tcell.Screen, r Rect, style tcell.Style) {
	FillRect(s, r, ' ', style)
}

// DimArea fades every cell within r in place by blending its foreground and
// background colours toward overlay at strength num/den (0 = unchanged,
// den = fully overlay). It reads the already-drawn content with Screen.Get
// and rewrites each cell keeping its rune, so the underlying UI stays visible
// but dimmed rather than wiped by a solid fill. Cells whose colour is the
// terminal default (unset) are left untouched.
func DimArea(s tcell.Screen, r Rect, overlay tcell.Color, num, den int) {
	if num <= 0 || den <= 0 {
		return
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; {
			str, style, width := s.Get(x, y)
			s.Put(x, y, str, dimStyle(style, overlay, num, den))
			if width < 1 {
				// Advance by the grapheme's display width so the trailing
				// cell of a wide char isn't re-processed on its own.
				width = 1
			}
			x += width
		}
	}
}

// dimStyle blends a style's foreground and background toward overlay.
func dimStyle(st tcell.Style, overlay tcell.Color, num, den int) tcell.Style {
	if fg := st.GetForeground(); fg.Valid() {
		st = st.Foreground(BlendColor(fg, overlay, num, den))
	}
	if bg := st.GetBackground(); bg.Valid() {
		st = st.Background(BlendColor(bg, overlay, num, den))
	}
	return st
}

// BlendColor mixes a toward b, weighting b by num/den; num/den == 0 returns
// a unchanged, num/den == 1 returns b. An invalid (unset) a is returned
// as-is; an invalid b is treated as black.
func BlendColor(a, b tcell.Color, num, den int) tcell.Color {
	if !a.Valid() || den <= 0 {
		return a
	}
	ar, ag, ab := a.RGB()
	var br, bg, bb int32
	if b.Valid() {
		br, bg, bb = b.RGB()
	}
	inv, n, d := int32(den-num), int32(num), int32(den)
	mix := func(x, y int32) int32 { return (x*inv + y*n) / d }
	return tcell.NewRGBColor(mix(ar, br), mix(ag, bg), mix(ab, bb))
}

// DrawHLine draws a horizontal line using '─'.
func DrawHLine(s tcell.Screen, x, y, w int, style tcell.Style) {
	for col := x; col < x+w; col++ {
		s.SetContent(col, y, '─', nil, style)
	}
}

// DrawVLine draws a vertical line using '│'.
func DrawVLine(s tcell.Screen, x, y, h int, style tcell.Style) {
	for row := y; row < y+h; row++ {
		s.SetContent(x, row, '│', nil, style)
	}
}

// DrawBox draws a single-line box border around r.
func DrawBox(s tcell.Screen, r Rect, style tcell.Style) {
	x, y, w, h := r.X, r.Y, r.W, r.H
	s.SetContent(x, y, '┌', nil, style)
	s.SetContent(x+w-1, y, '┐', nil, style)
	s.SetContent(x, y+h-1, '└', nil, style)
	s.SetContent(x+w-1, y+h-1, '┘', nil, style)
	for col := x + 1; col < x+w-1; col++ {
		s.SetContent(col, y, '─', nil, style)
		s.SetContent(col, y+h-1, '─', nil, style)
	}
	for row := y + 1; row < y+h-1; row++ {
		s.SetContent(x, row, '│', nil, style)
		s.SetContent(x+w-1, row, '│', nil, style)
	}
}

// DrawBoxTitle draws a box with a centred title on the top border.
func DrawBoxTitle(s tcell.Screen, r Rect, title string, borderStyle, titleStyle tcell.Style) {
	DrawBox(s, r, borderStyle)
	if title == "" {
		return
	}
	titleStr := " " + title + " "
	tx := r.X + (r.W-len(titleStr))/2
	if tx < r.X+1 {
		tx = r.X + 1
	}
	DrawTextClipped(s, tx, r.Y, r.X+r.W-2-tx+1, titleStyle, titleStr)
}

// DrawScrollbar draws a vertical scrollbar at x spanning [y, y+h).
// total is the total number of items; visible is how many fit on screen;
// offset is the first visible item index.
func DrawScrollbar(s tcell.Screen, x, y, h, total, visible, offset int, style, thumbStyle tcell.Style) {
	for i := 0; i < h; i++ {
		s.SetContent(x, y+i, '│', nil, style)
	}
	if total <= visible || total == 0 {
		return
	}
	thumbH := Max(1, h*visible/total)
	thumbY := y + offset*h/total
	for i := 0; i < thumbH && thumbY+i < y+h; i++ {
		s.SetContent(x, thumbY+i, '█', nil, thumbStyle)
	}
}
