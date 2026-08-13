package core

import "cmp"

// ---------------------------------------------------------------------------
// Math helpers
// ---------------------------------------------------------------------------

// Clamp restricts v to [lo, hi]. Generic over any ordered type: the layout
// splitter clamps a float64 ratio, everything else clamps int columns, rows
// and indices.
func Clamp[T cmp.Ordered](v, lo, hi T) T {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
