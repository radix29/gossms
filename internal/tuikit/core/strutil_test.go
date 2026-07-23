package core

import "testing"

func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		s    string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"你好", 4},                   // CJK: 2 columns per rune
		{"é", 1},                   // "e" + combining acute accent: one grapheme cluster
		{"\U0001F1FA\U0001F1F8", 2}, // flag emoji (regional indicator pair)
	}
	for _, c := range cases {
		if got := DisplayWidth(c.s); got != c.want {
			t.Errorf("DisplayWidth(%q) = %d, want %d", c.s, got, c.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"hello", 10, "hello"},      // fits, unchanged
		{"hello world", 5, "hell…"}, // clipped with ellipsis
		{"你好吗", 3, "你…"},            // wide runes: only one fits in the budget
		{"abc", 0, ""},              // n<=0 always empty
		{"", 5, ""},                 // empty input
	}
	for _, c := range cases {
		if got := Truncate(c.s, c.n); got != c.want {
			t.Errorf("Truncate(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}

func TestPadRight(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"ab", 5, "ab   "},  // padded with trailing spaces
		{"ab", 2, "ab"},     // exact width, unchanged
		{"abcde", 3, "abc"}, // hard-clipped, no ellipsis
		{"你好", 3, "你 "},     // wide rune that doesn't fit is dropped, remainder padded
	}
	for _, c := range cases {
		if got := PadRight(c.s, c.n); got != c.want {
			t.Errorf("PadRight(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
		if got := DisplayWidth(PadRight(c.s, c.n)); got != c.n {
			t.Errorf("DisplayWidth(PadRight(%q, %d)) = %d, want exactly %d", c.s, c.n, got, c.n)
		}
	}
}

func TestPadLeft(t *testing.T) {
	cases := []struct {
		s    string
		n    int
		want string
	}{
		{"ab", 5, "   ab"},  // padded with leading spaces
		{"ab", 2, "ab"},     // exact width, unchanged
		{"abcde", 3, "abc"}, // hard-clipped, no ellipsis
		{"你好", 3, "你 "},     // wide rune that doesn't fit is dropped, remainder padded
	}
	for _, c := range cases {
		if got := PadLeft(c.s, c.n); got != c.want {
			t.Errorf("PadLeft(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
		if got := DisplayWidth(PadLeft(c.s, c.n)); got != c.n {
			t.Errorf("DisplayWidth(PadLeft(%q, %d)) = %d, want exactly %d", c.s, c.n, got, c.n)
		}
	}
}

func TestItoa(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{0, "0"},
		{123, "123"},
		{-5, "-5"},
		{1000000, "1000000"},
		{-1, "-1"},
	}
	for _, c := range cases {
		if got := Itoa(c.n); got != c.want {
			t.Errorf("Itoa(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestFormatThousands(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{5, "5"},
		{999, "999"},
		{1000, "1,000"},
		{1234567, "1,234,567"},
		{100423, "100,423"},
		{-1234567, "-1,234,567"},
		{-5, "-5"},
	}
	for _, c := range cases {
		if got := FormatThousands(c.n); got != c.want {
			t.Errorf("FormatThousands(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestJoinPath(t *testing.T) {
	cases := []struct {
		parts []string
		want  string
	}{
		{[]string{"a", "b", "c"}, "a > b > c"},
		{[]string{"only"}, "only"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := JoinPath(c.parts); got != c.want {
			t.Errorf("JoinPath(%v) = %q, want %q", c.parts, got, c.want)
		}
	}
}

// evRuneStub implements the minimal interface{ Str() string } EvRune expects,
// standing in for a real tcell.EventKey.
type evRuneStub struct{ s string }

func (e evRuneStub) Str() string { return e.s }

func TestEvRune(t *testing.T) {
	cases := []struct {
		s    string
		want rune
	}{
		{"a", 'a'},
		{"", 0},
		{"你", '你'},
	}
	for _, c := range cases {
		if got := EvRune(evRuneStub{c.s}); got != c.want {
			t.Errorf("EvRune(%q) = %q, want %q", c.s, got, c.want)
		}
	}
}
