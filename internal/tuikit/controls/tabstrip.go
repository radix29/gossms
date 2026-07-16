package controls

import "github.com/radix29/gossms/internal/tuikit/core"

// TabSegment is the on-screen column extent of one part of a tab-bar entry
// — e.g. a tab's label, or its trailing close-button glyph.
type TabSegment struct {
	X, W int
}

// TabStripSegments lays out a horizontal row of tab-bar entries starting at
// column x, one screen row tall. Each element of widths is the part widths
// for one entry — a plain tab has a single part (its label text); a
// closable tab has two (label, then close glyph) — drawn glued together
// with no gap between parts of the same entry. Entries themselves are
// separated by a 1-column gap. Layout stops before any entry whose total
// width would put it at or past maxX (exclusive), so the returned slice may
// be shorter than widths when tabs don't all fit.
//
// Draw and hit-test code for a tab bar must both build their column math
// from this same call — a caller that computes segments once for drawing
// and again by hand for hit-testing is exactly the drift hazard this
// replaces.
func TabStripSegments(x int, widths [][]int, maxX int) [][]TabSegment {
	col := x
	out := make([][]TabSegment, 0, len(widths))
	for _, parts := range widths {
		total := 0
		for _, w := range parts {
			total += w
		}
		if col+total > maxX {
			break
		}
		segs := make([]TabSegment, len(parts))
		c := col
		for i, w := range parts {
			segs[i] = TabSegment{X: c, W: w}
			c += w
		}
		out = append(out, segs)
		col += total + 1
	}
	return out
}

// TabLabelWidth is a convenience for the common single-part case: the
// on-screen width of a tab whose text is padded with one leading and one
// trailing space, e.g. " Results 1 ".
func TabLabelWidth(label string) int {
	return core.DisplayWidth(" " + label + " ")
}
