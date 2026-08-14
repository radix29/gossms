package core

import "testing"

func TestClamp(t *testing.T) {
	cases := []struct {
		v, lo, hi, want int
	}{
		{5, 0, 10, 5},
		{-5, 0, 10, 0},
		{15, 0, 10, 10},
		{0, 0, 10, 0},
		{10, 0, 10, 10},
	}
	for _, c := range cases {
		if got := Clamp(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("Clamp(%d, %d, %d) = %d, want %d", c.v, c.lo, c.hi, got, c.want)
		}
	}
}

// Clamp(i, 0, len(x)-1) over an empty x asks for the empty range [0, -1].
// hi wins, so the result is -1 — "no selection" — and not 0, which would be
// a row that isn't there. Callers across the package are written against
// this; pinned here so it can't be "fixed" into returning lo.
func TestClampEmptyRangeYieldsHi(t *testing.T) {
	if got := Clamp(3, 0, -1); got != -1 {
		t.Errorf("Clamp(3, 0, -1) = %d, want -1 (no selection)", got)
	}
	if got := Clamp(0, 0, -1); got != -1 {
		t.Errorf("Clamp(0, 0, -1) = %d, want -1 (no selection)", got)
	}
	// lo is applied first, so a v below it still comes back as lo even on an
	// empty range. No caller passes a negative index, but the asymmetry is
	// the part a reader would guess wrong.
	if got := Clamp(-5, 0, -1); got != 0 {
		t.Errorf("Clamp(-5, 0, -1) = %d, want 0 (lo is applied before hi)", got)
	}
}

func TestClampFloat(t *testing.T) {
	cases := []struct {
		v, lo, hi, want float64
	}{
		{0.5, 0, 1, 0.5},
		{-0.5, 0, 1, 0},
		{1.5, 0, 1, 1},
	}
	for _, c := range cases {
		if got := Clamp(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("Clamp(%v, %v, %v) = %v, want %v", c.v, c.lo, c.hi, got, c.want)
		}
	}
}
