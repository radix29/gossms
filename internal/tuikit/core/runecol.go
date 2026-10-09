package core

import (
	"unicode/utf8"

	"github.com/clipperhouse/displaywidth"
)

// ---------------------------------------------------------------------------
// Rune-index <-> terminal-column conversion
// ---------------------------------------------------------------------------

// Text indexed by rune is drawn in terminal columns, and the two differ: a CJK
// ideograph or emoji takes two columns, a combining mark none. Editor and
// InputField index by rune, so every index <-> screen position conversion goes
// through the helpers below; reaching for len(line) reintroduces the drift.
//
// The unit of width is the grapheme cluster, because that is what tcell's Put,
// DisplayWidth and the terminal measure. Summing rune widths gets "❤️" as 1
// column instead of 2, a flag and "👍🏽" as 4 instead of 2, a ZWJ family as 6:
// the glyph overwrote the next cell and every later column was off (review plan
// K1). A rune index inside a cluster maps to its cluster's start, and the cursor
// moves by NextGrapheme/PrevGrapheme so it never lands there.

// joinFloor is the first code point that can join a neighbour into a multi-rune
// cluster (U+0300). Below it every rune is its own cluster (Extend, ZWJ,
// SpacingMark, Prepend, Hangul jamo, regional indicators and variation selectors
// are all above), apart from CR LF, which text widgets never hold. T-SQL is
// almost entirely below it, so the common case never segments.
const joinFloor = 0x300

// GraphemeAt returns the end (exclusive rune index) and display width of the
// grapheme cluster starting at rune index i. i at or past the end returns
// (i, 0). i is expected to be a cluster boundary; from inside one, the rest is
// returned as a cluster. LoneASCII is split out so a walk can take the
// printable-ASCII case inline: the editor runs it per character drawn and
// wrapped.
func GraphemeAt(line []rune, i int) (end, width int) {
	if i >= 0 && LoneASCII(line, i) {
		return i + 1, 1
	}
	return graphemeAtSlow(line, i)
}

// LoneASCII reports whether line[i] is printable ASCII, a one-column cluster on
// its own, so a walk can take the common case inline and call GraphemeAt for the
// rest.
func LoneASCII(line []rune, i int) bool {
	r := line[i]
	return r >= 0x20 && r < 0x7F && (i+1 == len(line) || line[i+1] < joinFloor)
}

// graphemeAtSlow is GraphemeAt past its inlined fast path.
func graphemeAtSlow(line []rune, i int) (end, width int) {
	n := len(line)
	if i < 0 {
		i = 0
	}
	if i >= n {
		return i, 0
	}
	if line[i] < joinFloor && (i+1 == n || line[i+1] < joinFloor) {
		return i + 1, RuneWidth(line[i])
	}
	// Segment a short window from i, widening it only when the first cluster fills
	// it (a long run of combining marks): a window of the rest of the line would
	// make a walk quadratic. The bytes live on the stack.
	var buf [128]byte
	for win := 8; ; win *= 2 {
		j := min(i+win, n)
		b := buf[:0]
		for _, r := range line[i:j] {
			b = utf8.AppendRune(b, r)
		}
		g := displaywidth.BytesGraphemes(b)
		if !g.Next() {
			return i + 1, RuneWidth(line[i])
		}
		k := utf8.RuneCount(g.Value())
		if i+k < j || j == n {
			return i + k, g.Width()
		}
	}
}

// NextGrapheme returns the rune index just past the grapheme cluster at i —
// where Right and Delete move to. Past the end it returns i+1, so a caller's
// virtual past-end positions still step one at a time.
func NextGrapheme(line []rune, i int) int {
	if i >= len(line) {
		return i + 1
	}
	end, _ := GraphemeAt(line, i)
	return end
}

// PrevGrapheme returns the start of the cluster that ends at rune index i
// (where Left and Backspace move). i past the end steps back one. Clusters are
// found only walking forward, so it backs up to a known boundary first: the
// nearest point between two runes both below joinFloor, or the line's start.
func PrevGrapheme(line []rune, i int) int {
	if i <= 0 {
		return 0
	}
	if i > len(line) {
		return i - 1
	}
	k := i - 1
	for k > 0 && (line[k] >= joinFloor || line[k-1] >= joinFloor) {
		k--
	}
	for {
		end, _ := GraphemeAt(line, k)
		if end >= i {
			return k
		}
		k = end
	}
}

// RuneWidth returns how many terminal columns r occupies on its own: 2 for a
// wide (CJK/emoji) rune, 0 for a combining mark or other zero-width rune, 1
// otherwise. Printable ASCII short-circuits the table lookup: it is nearly all
// that is measured (T-SQL) and this runs per rune over whole documents.
// TestRuneWidthASCIIFastPathAgrees checks it against displaywidth.
func RuneWidth(r rune) int {
	if r >= 0x20 && r < 0x7F {
		return 1
	}
	return displaywidth.Rune(r)
}

// RunesWidth returns the total column width of line. It and the two
// conversions below sum rune widths up to the first rune that can join a
// cluster, then back up one rune (its possible base, which the run below
// joinFloor guarantees starts a cluster) and walk by cluster. Document width
// runs this over every line after an edit.
func RunesWidth(line []rune) int {
	w := 0
	for i, r := range line {
		if r >= 0x20 && r < 0x7F {
			w++
			continue
		}
		if r >= joinFloor {
			j := max(i-1, 0) // its possible base
			for k := j; k < i; k++ {
				w -= RuneWidth(line[k])
			}
			for j < len(line) {
				end, cw := GraphemeAt(line, j)
				w += cw
				j = end
			}
			return w
		}
		w += RuneWidth(r)
	}
	return w
}

// ColumnOfRune returns the column at which the rune at idx begins. An idx
// inside a cluster returns the cluster's start column. An idx past the end
// counts one column per missing rune, the virtual model a cursor or selection
// past end-of-line uses; RuneIndexAtColumn inverts it, round-tripping past the
// end too.
func ColumnOfRune(line []rune, idx int) int {
	if idx <= 0 {
		return 0
	}
	// The rune at idx is looked at too: if it joins, idx is inside a cluster whose
	// base is the rune before it.
	col, lim := 0, min(idx+1, len(line))
	for i := range lim {
		r := line[i]
		if r >= joinFloor {
			j := max(i-1, 0) // its possible base
			for k := j; k < min(i, idx); k++ {
				col -= RuneWidth(line[k])
			}
			for j < idx && j < len(line) {
				end, w := GraphemeAt(line, j)
				if end > idx {
					return col
				}
				col += w
				j = end
			}
			return col + max(idx-len(line), 0)
		}
		if i < idx {
			col += RuneWidth(r)
		}
	}
	return col + max(idx-len(line), 0)
}

// RuneIndexAtColumn returns the index of the cluster covering column col. It
// snaps to the start of a wide cluster whichever column was hit, so a click on
// either half of a CJK character puts the cursor before it, never inside. A col
// past the end returns one index per extra column, inverting ColumnOfRune.
func RuneIndexAtColumn(line []rune, col int) int {
	i, _ := ClusterAtColumn(line, col)
	return i
}

// ClusterAtColumn is RuneIndexAtColumn that also returns the column the
// cluster starts at — for a caller drawing from col, whose first cluster may
// straddle it. Past the end, start is col itself.
func ClusterAtColumn(line []rune, col int) (idx, start int) {
	if col <= 0 {
		return 0, 0
	}
	c := 0
	for i := 0; i < len(line); i++ {
		r := line[i]
		if r >= joinFloor || (i+1 < len(line) && line[i+1] >= joinFloor) {
			// From here by cluster: line[i] is a boundary, as every rune before it
			// stood alone.
			for i < len(line) {
				end, w := GraphemeAt(line, i)
				if c+w > col {
					return i, c
				}
				c += w
				i = end
			}
			break
		}
		w := RuneWidth(r)
		if c+w > col {
			return i, c
		}
		c += w
	}
	return len(line) + (col - c), col
}
