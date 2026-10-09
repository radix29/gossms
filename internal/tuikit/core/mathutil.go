package core

import "cmp"

// ---------------------------------------------------------------------------
// Math helpers
// ---------------------------------------------------------------------------

// Clamp restricts v to [lo, hi], generic over any ordered type.
//
// An empty range (hi < lo, which Clamp(i, 0, len(x)-1) becomes for an empty x)
// is not rejected: lo is applied first, so v < lo returns lo and anything else
// hi. With v a real index that yields -1, this package's "no selection", which
// callers rely on; a caller needing another answer for an empty collection must
// check for emptiness itself.
func Clamp[T cmp.Ordered](v, lo, hi T) T {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
