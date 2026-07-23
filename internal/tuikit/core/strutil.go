package core

import (
	"strings"

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

// WrapText greedily word-wraps text to at most w display columns per line.
func WrapText(text string, w int) []string {
	if w <= 0 {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{""}
	}
	lines := make([]string, 0, 4)
	cur := words[0]
	for _, word := range words[1:] {
		if DisplayWidth(cur+" "+word) > w {
			lines = append(lines, cur)
			cur = word
		} else {
			cur = cur + " " + word
		}
	}
	return append(lines, cur)
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

// PadLeft pads s to exactly n display columns with leading spaces, for a
// right-aligned fixed-width column (e.g. a byte size next to a name). If s
// is already n columns or wider, it returns s truncated to n columns
// (without an ellipsis) so the result always occupies exactly n columns.
func PadLeft(s string, n int) string {
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
	return strings.Repeat(" ", n-w) + s
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

// FormatThousands renders n in base 10 with "," every three digits, e.g.
// 1234567 -> "1,234,567".
func FormatThousands(n int64) string {
	neg := n < 0
	if neg {
		n = -n
	}
	digits := Itoa(int(n))
	var sb strings.Builder
	if neg {
		sb.WriteByte('-')
	}
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
