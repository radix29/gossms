package core

import "testing"

func TestIsWordRune(t *testing.T) {
	cases := map[rune]bool{
		'a': true, 'Z': true, '_': true, '5': true,
		' ': false, '.': false, '-': false, '\t': false,
	}
	for r, want := range cases {
		if got := IsWordRune(r); got != want {
			t.Errorf("IsWordRune(%q) = %v, want %v", r, got, want)
		}
	}
}

func TestWordBoundaryLeft(t *testing.T) {
	cases := []struct {
		line string
		col  int
		want int
	}{
		{"foo.bar baz", 11, 8},
		{"foo.bar baz", 8, 4},
		{"foo.bar baz", 4, 3},
		{"foo.bar baz", 3, 0},
		{"foo.bar baz", 0, 0},
		{"   foo", 3, 0},
		{"", 0, 0},
	}
	for _, c := range cases {
		if got := WordBoundaryLeft([]rune(c.line), c.col); got != c.want {
			t.Errorf("WordBoundaryLeft(%q, %d) = %d, want %d", c.line, c.col, got, c.want)
		}
	}
}

func TestWordBoundaryRight(t *testing.T) {
	cases := []struct {
		line string
		col  int
		want int
	}{
		{"foo.bar baz", 0, 3},
		{"foo.bar baz", 3, 4},
		{"foo.bar baz", 4, 7},
		{"foo.bar baz", 7, 11},
		{"foo.bar baz", 11, 11},
		{"foo   ", 3, 6},
		{"", 0, 0},
	}
	for _, c := range cases {
		if got := WordBoundaryRight([]rune(c.line), c.col); got != c.want {
			t.Errorf("WordBoundaryRight(%q, %d) = %d, want %d", c.line, c.col, got, c.want)
		}
	}
}
