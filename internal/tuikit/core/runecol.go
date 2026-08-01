package core

import "github.com/clipperhouse/displaywidth"

// ---------------------------------------------------------------------------
// Rune-index <-> terminal-column conversion
// ---------------------------------------------------------------------------

// Text that is *indexed* by rune still has to be *drawn* in terminal
// columns, and the two do not correspond one-for-one: a CJK ideograph or an
// emoji occupies two columns, a combining mark none. Editor and InputField
// both index by rune (cursor, selection, wrap segments), so every place they
// turn an index into a screen position — or a click position back into an
// index — goes through the four helpers below. They are the entire
// conversion between the two coordinate systems; a caller that reaches for
// len(line) instead reintroduces the drift these exist to remove.

// RuneWidth returns how many terminal columns r occupies on its own: 2 for a
// wide (CJK/emoji) rune, 0 for a combining mark or other zero-width rune, 1
// otherwise.
//
// Printable ASCII short-circuits the table lookup. That range is the
// overwhelming majority of every character this project measures — T-SQL is
// ASCII apart from string literals — and these run per rune over whole
// documents, so the branch is worth it. TestRuneWidthASCIIFastPathAgrees
// checks it against displaywidth for the entire range rather than trusting
// the assumption.
func RuneWidth(r rune) int {
	if r >= 0x20 && r < 0x7F {
		return 1
	}
	return displaywidth.Rune(r)
}

// RunesWidth returns the total column width of line.
func RunesWidth(line []rune) int {
	w := 0
	for _, r := range line {
		if r >= 0x20 && r < 0x7F {
			w++
			continue
		}
		w += displaywidth.Rune(r)
	}
	return w
}

// ColumnOfRune returns the column at which the rune at index idx begins —
// the width of line[:idx].
//
// An idx past the end of line counts one column per missing rune, matching
// the virtual one-column-per-position model a text widget uses for a cursor
// or selection sitting past end-of-line. RuneIndexAtColumn is its inverse
// over that range too, so the two round-trip past the end as well as within.
func ColumnOfRune(line []rune, idx int) int {
	if idx <= 0 {
		return 0
	}
	col, i := 0, 0
	for ; i < idx && i < len(line); i++ {
		col += RuneWidth(line[i])
	}
	return col + (idx - i)
}

// RuneIndexAtColumn returns the index of the rune covering column col.
//
// It snaps to the start of a wide rune whichever of its columns was hit, so
// clicking either half of a CJK character puts the cursor before it and
// never inside it — an index between a wide rune's two columns has no valid
// text position behind it. A col past the line's last column returns one
// index per extra column, inverting ColumnOfRune's past-the-end rule.
func RuneIndexAtColumn(line []rune, col int) int {
	if col <= 0 {
		return 0
	}
	c := 0
	for i, r := range line {
		w := RuneWidth(r)
		if c+w > col {
			return i
		}
		c += w
	}
	return len(line) + (col - c)
}
