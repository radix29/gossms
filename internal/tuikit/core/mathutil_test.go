package core

import "testing"

func TestMinMax(t *testing.T) {
	if got := Min(3, 5); got != 3 {
		t.Errorf("Min(3, 5) = %d, want 3", got)
	}
	if got := Min(5, 3); got != 3 {
		t.Errorf("Min(5, 3) = %d, want 3", got)
	}
	if got := Max(3, 5); got != 5 {
		t.Errorf("Max(3, 5) = %d, want 5", got)
	}
	if got := Max(5, 3); got != 5 {
		t.Errorf("Max(5, 3) = %d, want 5", got)
	}
}

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

func TestClampF(t *testing.T) {
	cases := []struct {
		v, lo, hi, want float64
	}{
		{0.5, 0, 1, 0.5},
		{-0.5, 0, 1, 0},
		{1.5, 0, 1, 1},
	}
	for _, c := range cases {
		if got := ClampF(c.v, c.lo, c.hi); got != c.want {
			t.Errorf("ClampF(%v, %v, %v) = %v, want %v", c.v, c.lo, c.hi, got, c.want)
		}
	}
}
