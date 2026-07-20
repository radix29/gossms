package core

import "testing"

func TestRectRightBottom(t *testing.T) {
	r := NewRect(2, 3, 10, 5)
	if got := r.Right(); got != 12 {
		t.Errorf("Right() = %d, want 12", got)
	}
	if got := r.Bottom(); got != 8 {
		t.Errorf("Bottom() = %d, want 8", got)
	}
}

func TestRectInner(t *testing.T) {
	r := NewRect(0, 0, 10, 10)
	got := r.Inner(1)
	want := Rect{1, 1, 8, 8}
	if got != want {
		t.Errorf("Inner(1) = %+v, want %+v", got, want)
	}
}

func TestRectContains(t *testing.T) {
	r := NewRect(2, 2, 3, 3) // covers x in [2,5), y in [2,5)
	cases := []struct {
		x, y int
		want bool
	}{
		{2, 2, true},
		{4, 4, true},
		{5, 4, false}, // exclusive right edge
		{4, 5, false}, // exclusive bottom edge
		{1, 2, false},
		{2, 1, false},
	}
	for _, c := range cases {
		if got := r.Contains(c.x, c.y); got != c.want {
			t.Errorf("Contains(%d, %d) = %v, want %v", c.x, c.y, got, c.want)
		}
	}
}

func TestRectIsZero(t *testing.T) {
	if !(Rect{}).IsZero() {
		t.Error("zero-value Rect must report IsZero() == true")
	}
	// X/Y alone don't make a rect "zero" per the current implementation —
	// only W and H are considered.
	if !(NewRect(1, 1, 0, 0)).IsZero() {
		t.Error("Rect{X:1,Y:1,W:0,H:0} should still report IsZero() == true")
	}
	if (NewRect(0, 0, 5, 5)).IsZero() {
		t.Error("a rect with nonzero W/H must not report IsZero() == true")
	}
}
