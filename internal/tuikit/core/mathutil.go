package core

import "cmp"

// ---------------------------------------------------------------------------
// Math helpers
// ---------------------------------------------------------------------------

// Clamp restricts v to [lo, hi]. Generic over any ordered type: the layout
// splitter clamps a float64 ratio, everything else clamps int columns, rows
// and indices.
//
// An empty range — hi < lo, which is what Clamp(i, 0, len(x)-1) becomes for
// an empty x — is not rejected: lo is applied first, so v < lo still returns
// lo and anything else returns hi. For that call, with v a real index, that
// means -1, which is this package's "no selection" — an index clamped against
// an empty collection lands there rather than on row 0, which isn't there.
// Callers rely on it. A caller that needs a different answer for an empty
// collection has to check for emptiness itself.
func Clamp[T cmp.Ordered](v, lo, hi T) T {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
