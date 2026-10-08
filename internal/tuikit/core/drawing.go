package core

import (
	"github.com/clipperhouse/displaywidth"
	"github.com/gdamore/tcell/v3"
)

// ---------------------------------------------------------------------------
// Drawing primitives
// ---------------------------------------------------------------------------

// DrawText draws text starting at (x,y), no clipping. Wide characters (CJK) and
// multi-rune grapheme clusters (flags, combining marks) advance the column by
// their display width, not 1 per rune.
func DrawText(s tcell.Screen, x, y int, style tcell.Style, text string) {
	col := x
	g := displaywidth.StringGraphemes(text)
	for g.Next() {
		w := g.Width()
		if w <= 0 {
			// Zero-width grapheme (a lone combining mark): emit it as a combining rune on
			// the previous cell when possible, otherwise skip; there is no cell to advance
			// into.
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

// DrawTextLine draws what DrawTextClipped(s, x, y, maxW, style,
// TruncateLine(text, maxW)) draws — line breaks and tabs as one space, "…"
// in the last column when text does not fit — without building the
// truncated string. A grid draws every visible cell every frame, and a clipped
// cell's TruncateLine was an allocation each time.
//
// One pass: graphemes are drawn while they fit maxW-1 columns. The first that
// does not is where "…" goes if text overflows, and what follows it fits in
// at most the two columns that grapheme's width allows, so up to two are held
// back until the overflow question is answered.
func DrawTextLine(s tcell.Screen, x, y, maxW int, style tcell.Style, text string) {
	if maxW <= 0 {
		return
	}
	budget := maxW - 1 // reserve one column for the ellipsis
	width := 0
	var held [2]heldGrapheme
	nHeld := 0
	cutting := false
	g := displaywidth.StringGraphemes(text)
	for g.Next() {
		v, gw := g.Value(), g.Width()
		if v == "\r\n" || v == "\n" || v == "\r" || v == "\t" {
			v, gw = " ", 1
		}
		if !cutting && width+gw > budget {
			cutting = true
		}
		width += gw
		if width > maxW {
			putGrapheme(s, x+width-gw-heldWidth(held[:nHeld]), y, "…", style)
			return
		}
		switch {
		case gw <= 0:
			// Measured but never drawn, as DrawTextClipped skips it.
		case cutting:
			held[nHeld].v, held[nHeld].w = v, gw
			nHeld++
		default:
			putGrapheme(s, x+width-gw, y, v, style)
		}
	}
	col := x + width - heldWidth(held[:nHeld])
	for _, h := range held[:nHeld] {
		putGrapheme(s, col, y, h.v, style)
		col += h.w
	}
}

// heldGrapheme is one grapheme DrawTextLine has measured but not yet drawn.
type heldGrapheme struct {
	v string
	w int
}

func heldWidth(held []heldGrapheme) int {
	w := 0
	for _, h := range held {
		w += h.w
	}
	return w
}

// DrawTextOffset draws text horizontally scrolled by startCol display columns:
// graphemes whose virtual column falls before startCol are skipped, then up to
// maxW columns are drawn from (x,y). A grapheme straddling startCol is dropped
// rather than partially rendered, as DrawTextClipped treats the maxW boundary.
// Used for content, like a TreeView row, that scrolls sideways.
func DrawTextOffset(s tcell.Screen, x, y, startCol, maxW int, style tcell.Style, text string) {
	if maxW <= 0 {
		return
	}
	vcol := 0
	g := displaywidth.StringGraphemes(text)
	for g.Next() {
		w := g.Width()
		if w <= 0 {
			continue
		}
		if vcol < startCol {
			vcol += w
			continue
		}
		col := x + (vcol - startCol)
		if col+w > x+maxW {
			break
		}
		putGrapheme(s, col, y, g.Value(), style)
		vcol += w
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

// putGrapheme writes a (possibly multi-rune) grapheme cluster starting at
// (x,y). Wide graphemes (width 2) occupy two cells; the second is left for the
// terminal to render as part of the wide glyph, so only the first receives
// content.
//
// Put, not SetContent: SetContent re-packs its rune and combining runes into a
// string for Put, an allocation per visible cell per frame, and splitting the
// grapheme into runes for it was another.
func putGrapheme(s tcell.Screen, x, y int, grapheme string, style tcell.Style) {
	if grapheme == "" {
		return
	}
	s.Put(x, y, grapheme, style)
}

// PutRune writes the single-rune grapheme r at (x,y): SetContent(x, y, r, nil,
// style) without its allocation (see putGrapheme). tuikit cell drawing goes
// through here or putGrapheme, never SetContent.
func PutRune(s tcell.Screen, x, y int, r rune, style tcell.Style) {
	s.Put(x, y, RuneString(r), style)
}

// asciiRunes holds every ASCII character at its own index, so a one-byte slice
// of it is the character as a string with no allocation.
const asciiRunes = "\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f" +
	"\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1f" +
	" !\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~\x7f"

// boxRunesFirst..boxRunesLast cover U+2500-U+259F (box drawing and block
// elements): every border, separator, scrollbar and thumb tuikit draws.
const boxRunesFirst, boxRunesLast = '\u2500', '\u259f'

var boxRunes = func() (t [boxRunesLast - boxRunesFirst + 1]string) {
	for i := range t {
		t[i] = string(rune(boxRunesFirst + i))
	}
	return t
}()

// RuneString is string(r) without the allocation for ASCII and the box and
// block glyphs, the runes nearly every drawn cell holds.
func RuneString(r rune) string {
	switch {
	case r >= 0 && r < 0x80:
		return asciiRunes[r : r+1]
	case r >= boxRunesFirst && r <= boxRunesLast:
		return boxRunes[r-boxRunesFirst]
	}
	return string(r)
}

// FillRect fills a rectangle with the given rune and style.
//
// One Put per cell rather than tcell's FillArea, which converts the rune to a
// string for every cell where Put takes the one string built here.
func FillRect(s tcell.Screen, r Rect, ch rune, style tcell.Style) {
	str := RuneString(ch)
	for row := r.Y; row < r.Y+r.H; row++ {
		for col := r.X; col < r.X+r.W; col++ {
			s.Put(col, row, str, style)
		}
	}
}

// DimArea fades every cell within r in place by blending its foreground and
// background toward overlay at strength num/den (0 = unchanged, den = fully
// overlay). It reads the drawn content with Screen.Get and rewrites each cell
// keeping its rune, so the UI stays visible but dimmed rather than wiped by a
// solid fill. Cells with the terminal default (unset) colour are untouched.
func DimArea(s tcell.Screen, r Rect, overlay tcell.Color, num, den int) {
	if num <= 0 || den <= 0 {
		return
	}
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; {
			str, style, width := s.Get(x, y)
			s.Put(x, y, str, dimStyle(style, overlay, num, den))
			if width < 1 {
				// Advance by the grapheme's display width so the trailing cell of a wide char
				// isn't re-processed on its own.
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

// BlendColor mixes a toward b, weighting b by num/den (0 returns a, 1 returns
// b). An invalid (unset) a is returned as-is; an invalid b is treated as black.
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
		PutRune(s, col, y, '─', style)
	}
}

// DrawVLine draws a vertical line using '│'.
func DrawVLine(s tcell.Screen, x, y, h int, style tcell.Style) {
	for row := y; row < y+h; row++ {
		PutRune(s, x, row, '│', style)
	}
}

// BoxRunes is the rune set a box border is drawn with.
type BoxRunes struct {
	TopLeft, TopRight, BottomLeft, BottomRight, Horizontal, Vertical rune
}

var (
	// SingleBox is the single-line border DrawBox draws.
	SingleBox = BoxRunes{'┌', '┐', '└', '┘', '─', '│'}
	// DoubleBox is a double-line border.
	DoubleBox = BoxRunes{'╔', '╗', '╚', '╝', '═', '║'}
)

// DrawBox draws a single-line box border around r.
func DrawBox(s tcell.Screen, r Rect, style tcell.Style) {
	DrawBoxWith(s, r, style, SingleBox)
}

// DrawBoxWith draws a box border around r with the given runes.
func DrawBoxWith(s tcell.Screen, r Rect, style tcell.Style, b BoxRunes) {
	x, y, w, h := r.X, r.Y, r.W, r.H
	PutRune(s, x, y, b.TopLeft, style)
	PutRune(s, x+w-1, y, b.TopRight, style)
	PutRune(s, x, y+h-1, b.BottomLeft, style)
	PutRune(s, x+w-1, y+h-1, b.BottomRight, style)
	for col := x + 1; col < x+w-1; col++ {
		PutRune(s, col, y, b.Horizontal, style)
		PutRune(s, col, y+h-1, b.Horizontal, style)
	}
	for row := y + 1; row < y+h-1; row++ {
		PutRune(s, x, row, b.Vertical, style)
		PutRune(s, x+w-1, row, b.Vertical, style)
	}
}

// DrawBoxTitle draws a box with a centred title on the top border.
func DrawBoxTitle(s tcell.Screen, r Rect, title string, borderStyle, titleStyle tcell.Style) {
	DrawBox(s, r, borderStyle)
	if title == "" {
		return
	}
	titleStr := " " + title + " "
	tx := r.X + (r.W-DisplayWidth(titleStr))/2
	if tx < r.X+1 {
		tx = r.X + 1
	}
	DrawTextClipped(s, tx, r.Y, r.X+r.W-2-tx+1, titleStyle, titleStr)
}

// DrawScrollbar draws a vertical scrollbar at x spanning [y, y+h). total is the
// total item count; visible how many fit on screen; offset the first visible
// item index.
func DrawScrollbar(s tcell.Screen, x, y, h, total, visible, offset int, style, thumbStyle tcell.Style) {
	for i := 0; i < h; i++ {
		PutRune(s, x, y+i, '│', style)
	}
	if total <= visible || total == 0 {
		return
	}
	thumbH, at := scrollThumb(h, total, visible, offset)
	thumbY := y + at
	for i := 0; i < thumbH && thumbY+i < y+h; i++ {
		PutRune(s, x, thumbY+i, '█', thumbStyle)
	}
}

// scrollThumb returns the thumb's length and its start within a track of length
// n, for total > visible. The start spans the free track, n-length, in step
// with offset's span, total-visible, so the last offset puts the thumb's end on
// the track's last cell. Placing it at offset*n/total left rounding to decide,
// and the thumb stopped short of the bottom (a 10-row track over 30 rows, 10
// visible, ended at row 8).
func scrollThumb(n, total, visible, offset int) (length, start int) {
	length = Clamp(n*visible/total, 1, n)
	return length, Clamp(offset, 0, total-visible) * (n - length) / (total - visible)
}

// ScrollOffsetForDrag returns the scroll offset a click or drag at
// track-relative row y (0 at the track's first row, matching the y..y+h span
// DrawScrollbar was given) should jump the view to, given the same
// h/total/visible: the track's first row is offset 0 and its last the final
// offset, as scrollThumb draws them. HandleScrollbarDrag is the higher-level
// helper most callers want; this is exposed for callers needing the offset math
// without the button/latch handling.
func ScrollOffsetForDrag(y, h, total, visible int) int {
	if h <= 0 || total <= visible {
		return 0
	}
	if h == 1 {
		return 0
	}
	// Linear from the first row to the last. y*total/h topped out at
	// (h-1)*total/h (900 of 995 on a 10-row track over 1000 rows), making the end
	// of a long list unreachable by drag.
	return Clamp(y, 0, h-1) * (total - visible) / (h - 1)
}

// DrawScrollbarH draws a horizontal scrollbar at y spanning [x, x+w), the
// counterpart of DrawScrollbar for content (like PlanView's operator graph
// canvas) that scrolls sideways.
func DrawScrollbarH(s tcell.Screen, x, y, w, total, visible, offset int, style, thumbStyle tcell.Style) {
	for i := 0; i < w; i++ {
		PutRune(s, x+i, y, '─', style)
	}
	if total <= visible || total == 0 {
		return
	}
	thumbW, at := scrollThumb(w, total, visible, offset)
	thumbX := x + at
	for i := 0; i < thumbW && thumbX+i < x+w; i++ {
		PutRune(s, thumbX+i, y, '█', thumbStyle)
	}
}

// HandleScrollbarDrag is the mouse-side counterpart of DrawScrollbar: given the
// same x/y/h/total a caller passed to DrawScrollbar, it handles a Button1 press
// or drag on that bar. Unlike DrawScrollbar it takes a single h as both track
// length and visible count (every current caller passes the same value for
// both); a caller with a genuinely different visible count needs its own drag
// math.
//
// *dragging latches for the whole gesture: set true on the qualifying initial
// press and left untouched after, so every call while the button stays down
// keeps controlling *scroll even once the mouse drifts off the bar's column.
// The caller clears *dragging on release (typically with its other latches, e.g.
// DataGrid/TreeView/ListBox/Editor's mouseDragging, or
// ModalDialog.ConsumeOutsideClick for every embedding dialog). Returns false
// (writing nothing) for anything but a qualifying Button1 event, so callers can
// chain it before their own hit-testing.
func HandleScrollbarDrag(ev *tcell.EventMouse, x, y, h, total int, dragging *bool, scroll *int) bool {
	if ev.Buttons() != tcell.Button1 || total <= h || h <= 0 {
		return false
	}
	mx, my := ev.Position()
	if !*dragging && (mx != x || my < y || my >= y+h) {
		return false
	}
	*dragging = true
	*scroll = ScrollOffsetForDrag(my-y, h, total, h)
	return true
}

// HandleScrollbarDragH is HandleScrollbarDrag's horizontal counterpart, for a
// bar drawn by DrawScrollbarH at row y spanning [x, x+w). The same
// h/visible-conflation caveat applies to w.
func HandleScrollbarDragH(ev *tcell.EventMouse, x, y, w, total int, dragging *bool, scroll *int) bool {
	if ev.Buttons() != tcell.Button1 || total <= w || w <= 0 {
		return false
	}
	mx, my := ev.Position()
	if !*dragging && (my != y || mx < x || mx >= x+w) {
		return false
	}
	*dragging = true
	*scroll = ScrollOffsetForDrag(mx-x, w, total, w)
	return true
}
