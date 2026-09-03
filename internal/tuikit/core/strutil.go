package core

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/clipperhouse/displaywidth"
)

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
	// One pass answers both questions the clip needs: cut remembers where the
	// string would have to end to leave a column for the ellipsis, while width
	// keeps running so a string that turns out to fit is returned whole.
	budget := n - 1 // reserve one column for the ellipsis
	var pos, cut, width int
	haveCut := false
	g := displaywidth.StringGraphemes(s)
	for g.Next() {
		gw := g.Width()
		if !haveCut && width+gw > budget {
			cut, haveCut = pos, true
		}
		width += gw
		if width > n {
			return s[:cut] + "…"
		}
		pos += len(g.Value())
	}
	return s
}

// WrapText greedily word-wraps text to at most w display columns per line.
//
// A word too wide for a line of its own is hard-broken across as many as it
// needs, rather than emitted whole on an over-wide line. Every caller draws
// the result through DrawTextClipped, so an unbroken token was silently cut
// at the pane's right edge with no ellipsis and no way to reach the rest —
// a log entry's stack frame, an unspaced path, a certificate thumbprint.
//
// Runs of whitespace, including leading indentation, are not preserved:
// splitting on strings.Fields is what makes a paragraph reflow. A caller
// that needs the original spacing has to keep it itself.
func WrapText(text string, w int) []string {
	lines, _ := wrapLines(text, w)
	return lines
}

// WrapTextLimit is WrapText capped at maxLines: text needing more lines than
// that has its overflow folded back into the last line and clipped there with
// an ellipsis. Dropping the surplus lines instead would leave a message that
// merely stops early reading like a complete one — a truncated SQL Server
// error is the case this exists for. A w or maxLines of zero or less leaves
// nowhere to draw and returns nil.
//
// The fold re-joins with a space only where the wrap broke at one. WrapText
// hard-breaks a word too long for the line, and gluing those halves back with
// a space turns one unreachable path into two plausible-looking ones.
func WrapTextLimit(text string, w, maxLines int) []string {
	if w <= 0 || maxLines <= 0 {
		return nil
	}
	lines, hardBreak := wrapLines(text, w)
	if len(lines) <= maxLines {
		return lines
	}
	var rest strings.Builder
	rest.WriteString(lines[maxLines-1])
	for i := maxLines; i < len(lines); i++ {
		if !hardBreak[i-1] {
			rest.WriteByte(' ')
		}
		rest.WriteString(lines[i])
	}
	return append(lines[:maxLines-1], Truncate(rest.String(), w))
}

// wrapLines is WrapText plus, per line, whether the break after it fell
// inside a word rather than at a space — what WrapTextLimit needs to rejoin
// the overflow without inventing spaces. The last entry describes no break
// and is always false.
func wrapLines(text string, w int) (lines []string, hardBreak []bool) {
	if w <= 0 {
		return []string{text}, []bool{false}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}, []bool{false}
	}
	lines, hardBreak = make([]string, 0, 4), make([]bool, 0, 4)
	cur := ""
	for _, word := range words {
		if cur != "" {
			if DisplayWidth(cur)+1+DisplayWidth(word) <= w {
				cur += " " + word
				continue
			}
			lines, hardBreak = append(lines, cur), append(hardBreak, false)
		}
		// word now starts a fresh line. One that doesn't fit on an empty
		// line is split until what's left does; splitGrapheme always takes
		// at least one grapheme, which is what makes this terminate for a
		// grapheme wider than w itself.
		for DisplayWidth(word) > w {
			var head string
			head, word = splitGrapheme(word, w)
			lines, hardBreak = append(lines, head), append(hardBreak, true)
		}
		cur = word
	}
	// cur is empty when the last word divided exactly into full lines, and
	// appending it then would hand the caller a blank line to draw. The
	// len check keeps the "always at least one line" promise for the one
	// case where that is the only line there is.
	if cur != "" || len(lines) == 0 {
		lines, hardBreak = append(lines, cur), append(hardBreak, false)
	}
	return lines, hardBreak
}

// splitGrapheme cuts s at the last grapheme boundary that keeps the head
// within n display columns, returning the head and the remainder.
//
// At least one grapheme always moves into the head, even one wider than n on
// its own — the head would otherwise come back empty with the remainder
// unchanged, and WrapText's loop over it would never end.
func splitGrapheme(s string, n int) (head, rest string) {
	end, width := 0, 0
	g := displaywidth.StringGraphemes(s)
	for g.Next() {
		gw := g.Width()
		if end > 0 && width+gw > n {
			break
		}
		end += len(g.Value())
		width += gw
	}
	return s[:end], s[end:]
}

// CenterOffset returns the left padding needed to center content of width
// contentW within a field of width fieldW, clamped to 0 if contentW >= fieldW.
func CenterOffset(fieldW, contentW int) int {
	if contentW >= fieldW {
		return 0
	}
	return (fieldW - contentW) / 2
}

// PadRight pads s to exactly n display columns with trailing spaces.
// If s is already n columns or wider, it returns s truncated to n columns
// (without an ellipsis) so the result always occupies exactly n columns.
func PadRight(s string, n int) string { return padSpaces(s, n, false) }

// PadLeft pads s to exactly n display columns with leading spaces, for a
// right-aligned fixed-width column (e.g. a byte size next to a name). If s
// is already n columns or wider, it returns s truncated to n columns
// (without an ellipsis) so the result always occupies exactly n columns.
func PadLeft(s string, n int) string { return padSpaces(s, n, true) }

// padSpaces is PadRight and PadLeft's shared body; left picks the side the
// padding goes on. Both promise *exactly* n display columns, and a caller
// drawing a fixed-width grid depends on that literally.
func padSpaces(s string, n int, left bool) string {
	if n <= 0 {
		return ""
	}
	// Padding is concatenation, and a grapheme cluster can absorb the bytes
	// that follow it — so a string ending mid-cluster comes back wider than
	// the sum of its parts: PadRight("0\xcc", 2) measured 1 column, then 3
	// once a space was appended. Only invalid UTF-8 can end mid-cluster, and
	// replacing it is what makes the "exactly n" promise true for any input
	// rather than merely for the ones we happen to feed it today.
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	w := DisplayWidth(s)
	switch {
	case w == n:
		return s
	case w < n:
		if left {
			return strings.Repeat(" ", n-w) + s
		}
		return s + strings.Repeat(" ", n-w)
	}
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
	// Pad the remainder when a wide grapheme straddling the limit didn't fit.
	return sb.String() + strings.Repeat(" ", n-width)
}

// FormatThousands renders n in base 10 with "," every three digits, e.g.
// 1234567 -> "1,234,567".
//
// The digits come from strconv rather than a hand-rolled loop: negating n to
// take its digits is wrong at math.MinInt64, where the negation is a no-op
// and the loop produces no digits at all — FormatThousands(math.MinInt64)
// answered "--".
func FormatThousands(n int64) string {
	digits := strconv.FormatInt(n, 10)
	sign := ""
	if strings.HasPrefix(digits, "-") {
		sign, digits = "-", digits[1:]
	}
	var sb strings.Builder
	sb.Grow(len(sign) + len(digits) + (len(digits)-1)/3)
	sb.WriteString(sign)
	for i := 0; i < len(digits); i++ {
		if i > 0 && (len(digits)-i)%3 == 0 {
			sb.WriteByte(',')
		}
		sb.WriteByte(digits[i])
	}
	return sb.String()
}

// EvRune extracts the first rune from a tcell v3 EventKey.
// In tcell v3, Rune() was replaced with Str() which returns a string.
func EvRune(ev interface{ Str() string }) rune {
	for _, r := range ev.Str() {
		return r
	}
	return 0
}
