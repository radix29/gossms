// Package core provides the foundational types and drawing primitives used
// by every other tuikit sub-package.  It has no dependency on any other
// tuikit package and can be imported in isolation.
package core

import (
	"strings"

	"github.com/clipperhouse/displaywidth"
	"github.com/gdamore/tcell/v3"
	"github.com/radix29/gossms/internal/tuikit/theme"
)

// ---------------------------------------------------------------------------
// Geometry
// ---------------------------------------------------------------------------

// Rect describes a rectangular region on the terminal screen.
type Rect struct {
	X, Y, W, H int
}

// NewRect constructs a Rect.
func NewRect(x, y, w, h int) Rect { return Rect{x, y, w, h} }

// Right returns the exclusive right edge (X + W).
func (r Rect) Right() int { return r.X + r.W }

// Bottom returns the exclusive bottom edge (Y + H).
func (r Rect) Bottom() int { return r.Y + r.H }

// Inner returns the rectangle inset by d on every side.
func (r Rect) Inner(d int) Rect {
	return Rect{r.X + d, r.Y + d, r.W - 2*d, r.H - 2*d}
}

// Contains reports whether (x,y) is inside the rectangle.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// IsZero reports whether the rect is empty.
func (r Rect) IsZero() bool { return r.W == 0 && r.H == 0 }

// ---------------------------------------------------------------------------
// Screen conveniences
// ---------------------------------------------------------------------------

// Init creates, initialises, and returns a new tcell.Screen with mouse enabled.
func Init() (tcell.Screen, error) {
	s, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	if err := s.Init(); err != nil {
		return nil, err
	}
	s.EnableMouse()
	s.SetStyle(theme.StyleDefault())
	return s, nil
}

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

// ---------------------------------------------------------------------------
// String helpers
// ---------------------------------------------------------------------------

// DisplayWidth returns the number of terminal columns s occupies, summing
// grapheme cluster widths (not byte length, not rune count). Other tuikit
// packages should use this instead of importing displaywidth directly, to
// keep that dependency confined to core.
func DisplayWidth(s string) int {
	return displaywidth.String(s)
}

// Truncate clips s to at most n display columns, appending "…" if clipped.
// Operates on display width (via displaywidth), not rune count, so wide
// CJK characters and multi-rune grapheme clusters are handled correctly.
func Truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if displaywidth.String(s) <= n {
		return s
	}
	var sb strings.Builder
	width := 0
	budget := n - 1 // reserve one column for the ellipsis
	g := displaywidth.StringGraphemes(s)
	for g.Next() {
		gw := g.Width()
		if width+gw > budget {
			break
		}
		sb.WriteString(g.Value())
		width += gw
	}
	sb.WriteString("…")
	return sb.String()
}

// PadRight pads s to exactly n display columns with trailing spaces.
// If s is already n columns or wider, it returns s truncated to n columns
// (without an ellipsis) so the result always occupies exactly n columns.
func PadRight(s string, n int) string {
	w := displaywidth.String(s)
	if w == n {
		return s
	}
	if w > n {
		// Hard-clip to n columns without an ellipsis, for fixed-width cells.
		var sb strings.Builder
		width := 0
		g := displaywidth.StringGraphemes(s)
		for g.Next() {
			gw := g.Width()
			if width+gw > n {
				break
			}
			sb.WriteString(g.Value())
			width += gw
		}
		for width < n { // pad any remainder if the last wide grapheme didn't fit
			sb.WriteByte(' ')
			width++
		}
		return sb.String()
	}
	return s + strings.Repeat(" ", n-w)
}

// Itoa converts n to a decimal string without importing strconv.
func Itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// JoinPath joins path segments with " > ".
func JoinPath(parts []string) string {
	return strings.Join(parts, " > ")
}

// EvRune extracts the first rune from a tcell v3 EventKey.
// In tcell v3, Rune() was replaced with Str() which returns a string.
func EvRune(ev interface{ Str() string }) rune {
	for _, r := range ev.Str() {
		return r
	}
	return 0
}

// ---------------------------------------------------------------------------
// Integer math helpers
// ---------------------------------------------------------------------------

// Min returns the smaller of a and b.
func Min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Max returns the larger of a and b.
func Max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Clamp restricts v to [lo, hi].
func Clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ClampF restricts a float64 to [lo, hi].
func ClampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
