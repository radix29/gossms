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

// DisplayWidthAtMost is min(DisplayWidth(s), n), stopping as soon as the
// width reaches n — for a caller that only needs to know whether s fits, and
// may be handed a multi-megabyte cell (a varchar(max) or XML value) it would
// otherwise measure in full. n <= 0 returns 0.
func DisplayWidthAtMost(s string, n int) int {
	if n <= 0 {
		return 0
	}
	// No grapheme is wider than its byte length (printable ASCII is one byte
	// per column; anything wider than one column takes at least two bytes), so
	// a string this short is under the limit whatever it holds, and the
	// full measure's printable-ASCII fast path is the cheaper walk.
	if len(s) <= n {
		return DisplayWidth(s)
	}
	width := 0
	g := displaywidth.StringGraphemes(s)
	for g.Next() {
		if width += g.Width(); width >= n {
			return n
		}
	}
	return width
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

// TruncateLine is Truncate for a one-row rendering of text that may hold line
// breaks or tabs — a grid cell. CR, LF, CRLF and TAB each render as one space:
// they measure 0 columns, so drawn as they are the words either side ran
// together ("line1line2"). A string without them is not copied.
func TruncateLine(s string, n int) string {
	if n <= 0 {
		return ""
	}
	// buf stays nil until the first break is mapped; until then the output
	// is s[:pos].
	var buf []byte
	budget := n - 1 // reserve one column for the ellipsis
	var pos, cut, width int
	haveCut := false
	g := displaywidth.StringGraphemes(s)
	for g.Next() {
		v, gw := g.Value(), g.Width()
		brk := v == "\r\n" || v == "\n" || v == "\r" || v == "\t"
		if brk {
			gw = 1
		}
		if !haveCut && width+gw > budget {
			cut, haveCut = pos, true
		}
		width += gw
		if width > n {
			if buf == nil {
				return s[:cut] + "…"
			}
			return string(buf[:cut]) + "…"
		}
		if brk && buf == nil {
			buf = append(make([]byte, 0, pos+n), s[:pos]...)
		}
		switch {
		case buf == nil:
			pos += len(v)
		case brk:
			buf = append(buf, ' ')
			pos = len(buf)
		default:
			buf = append(buf, v...)
			pos = len(buf)
		}
	}
	if buf == nil {
		return s
	}
	return string(buf)
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
	return foldOverflow(lines, hardBreak, w, maxLines)
}

// WrapParagraphs is WrapText that keeps the text's line breaks: each line of
// it (split on \n, \r\n or a lone \r) is a paragraph wrapped on its own, and
// an empty one stays a blank line — "question?\n\nconsequence" draws as two
// paragraphs with a gap, where WrapText would fold it into one. Line breaks
// at either end are dropped, as WrapText drops surrounding whitespace, so a
// message ending in "\n" gains no trailing blank line.
func WrapParagraphs(text string, w int) []string {
	lines, _ := wrapParagraphLines(text, w)
	return lines
}

// WrapParagraphsLimit is WrapParagraphs capped at maxLines the way
// WrapTextLimit caps WrapText: the overflow, paragraph breaks included, is
// folded into the last line and clipped there with an ellipsis. A paragraph
// break folds to a space. A w or maxLines of zero or less returns nil.
func WrapParagraphsLimit(text string, w, maxLines int) []string {
	if w <= 0 || maxLines <= 0 {
		return nil
	}
	lines, hardBreak := wrapParagraphLines(text, w)
	return foldOverflow(lines, hardBreak, w, maxLines)
}

// ParagraphsWidth is the display width of text's widest line as
// WrapParagraphs draws it (whitespace runs collapsed) — the w that puts each
// line on one row of its own.
func ParagraphsWidth(text string) int {
	width := 0
	for _, para := range splitParagraphs(text) {
		width = max(width, DisplayWidth(strings.Join(strings.Fields(para), " ")))
	}
	return width
}

// splitParagraphs is text's lines, with \r\n and a lone \r read as \n and
// the surrounding whitespace (line breaks included) trimmed first.
func splitParagraphs(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(strings.TrimSpace(text), "\n")
}

// wrapParagraphLines is wrapLines run per paragraph and concatenated. The
// break after a paragraph's last line is a soft one, so a fold joins across
// it with a space.
func wrapParagraphLines(text string, w int) (lines []string, hardBreak []bool) {
	for _, para := range splitParagraphs(text) {
		l, hb := wrapLines(para, w)
		lines, hardBreak = append(lines, l...), append(hardBreak, hb...)
	}
	return lines, hardBreak
}

// foldOverflow caps lines at maxLines, folding the surplus into the last line
// kept and clipping it with an ellipsis; hardBreak says, per line, whether the
// break after it fell inside a word (rejoined without a space). Blank lines
// are dropped from the fold.
func foldOverflow(lines []string, hardBreak []bool, w, maxLines int) []string {
	if len(lines) <= maxLines {
		return lines
	}
	var rest strings.Builder
	rest.WriteString(lines[maxLines-1])
	for i := maxLines; i < len(lines); i++ {
		// A blank line (a paragraph gap) contributes nothing, not a space of
		// its own: folding "a\n\nb" must read "a b", not "a  b" or " b".
		if lines[i] == "" {
			continue
		}
		if !hardBreak[i-1] && rest.Len() > 0 {
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
	// Widths are carried, never re-measured: re-measuring the remainder on
	// every hard break made one unbroken token of n bytes O(n²/w) — a 64 KB
	// base64 blob took 33 ms to wrap, a 48 KB CJK run 210 ms, on the UI
	// goroutine. A width splits exactly at a grapheme boundary, which is the
	// only place splitGraphemeWidth cuts.
	cur, curW := "", 0
	for _, word := range words {
		wordW := DisplayWidth(word)
		if cur != "" {
			if curW+1+wordW <= w {
				cur += " " + word
				curW += 1 + wordW
				continue
			}
			lines, hardBreak = append(lines, cur), append(hardBreak, false)
		}
		// word now starts a fresh line. One that doesn't fit on an empty
		// line is split until what's left does; splitGraphemeWidth always
		// takes at least one grapheme, which is what makes this terminate for
		// a grapheme wider than w itself.
		for wordW > w {
			var head string
			var headW int
			head, word, headW = splitGraphemeWidth(word, w)
			wordW -= headW
			lines, hardBreak = append(lines, head), append(hardBreak, true)
		}
		cur, curW = word, wordW
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
	head, rest, _ = splitGraphemeWidth(s, n)
	return head, rest
}

// splitGraphemeWidth is splitGrapheme also returning the head's display width,
// so a caller splitting repeatedly can keep the remainder's width without
// measuring it again.
func splitGraphemeWidth(s string, n int) (head, rest string, headWidth int) {
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
	return s[:end], s[end:], width
}

// TrimLastGrapheme returns s without its last grapheme cluster — what one
// Backspace removes. Cutting a byte (or a rune) instead leaves half a
// multi-byte character or a dangling combining mark behind.
func TrimLastGrapheme(s string) string {
	last := 0
	g := displaywidth.StringGraphemes(s)
	for end := 0; g.Next(); end += len(g.Value()) {
		last = end
	}
	return s[:last]
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
// It is for matching a key; text insertion uses EvText.
func EvRune(ev interface{ Str() string }) rune {
	for _, r := range ev.Str() {
		return r
	}
	return 0
}

// EvText is every rune a KeyRune event carries. Str is one key or a composed
// sequence — an IME commit, a ZWJ emoji, a base letter and its combining
// marks — and inserting only EvRune dropped all but the first.
func EvText(ev interface{ Str() string }) []rune {
	return []rune(ev.Str())
}
