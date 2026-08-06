package charts

import "testing"

func TestBlockRampsClampToRange(t *testing.T) {
	if got := VBlock(-3); got != ' ' {
		t.Errorf("VBlock(-3) = %q, want a space", got)
	}
	if got := VBlock(99); got != '█' {
		t.Errorf("VBlock(99) = %q, want '█'", got)
	}
	if got := HBlock(4); got != '▌' {
		t.Errorf("HBlock(4) = %q, want '▌'", got)
	}
	if got := VBlock(4); got != '▄' {
		t.Errorf("VBlock(4) = %q, want '▄'", got)
	}
	if got := HBlock(8); got != '█' {
		t.Errorf("HBlock(8) = %q, want '█'", got)
	}
}

func TestEighthsRounding(t *testing.T) {
	cases := []struct {
		cells      float64
		whole, rem int
	}{
		{cells: 0, whole: 0, rem: 0},
		{cells: -1, whole: 0, rem: 0},
		{cells: 2, whole: 2, rem: 0},
		{cells: 2.5, whole: 2, rem: 4},
		{cells: 0.125, whole: 0, rem: 1},
		{cells: 1.9, whole: 1, rem: 7},
		{cells: 1.99, whole: 2, rem: 0}, // rounds to nearest eighth, not down
	}
	for _, c := range cases {
		whole, rem := eighths(c.cells)
		if whole != c.whole || rem != c.rem {
			t.Errorf("eighths(%v) = %d cells + %d eighths, want %d + %d", c.cells, whole, rem, c.whole, c.rem)
		}
	}
}

// A value far too small for even one eighth still has to show something —
// snapping it to zero would report "no activity" for a metric that is
// genuinely non-zero, which is the difference between an idle server and a
// lightly loaded one.
func TestEighthsPromotesTinyNonZeroValues(t *testing.T) {
	whole, rem := eighths(0.0001)
	if whole != 0 || rem != 1 {
		t.Errorf("eighths(0.0001) = %d + %d, want 0 + 1", whole, rem)
	}
}
