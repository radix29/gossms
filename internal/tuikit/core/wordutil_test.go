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

// Combining marks, ZWJ, variation selectors and emoji modifiers continue the
// cluster they are attached to, so no result lands inside a cluster (B3).
const (
	eAcute  = "e\u0301"                                    // e + COMBINING ACUTE: 2 runes, 1 cluster
	family  = "\U0001F468\u200D\U0001F469\u200D\U0001F467" // 5 runes, 1 cluster
	flagRO  = "\U0001F1F7\U0001F1F4"                       // 2 regional indicators, 1 cluster
	thumbs  = "\U0001F44D\U0001F3FD"                       // thumbs up + skin tone, 1 cluster
	zwjWord = "a\u200Db"                                   // a+ZWJ is one cluster, then b
)

func TestWordBoundaryRightGraphemes(t *testing.T) {
	cases := []struct {
		line string
		col  int
		want int
	}{
		{eAcute + " x", 0, 2},
		{"caf" + eAcute + " bar", 0, 5},
		{zwjWord + " c", 0, 3},
		{"x\uFE0Fy z", 0, 3},
		{"a." + family + ".b", 1, 8},
		{"ab" + flagRO + "cd", 0, 2},
		{"ab" + flagRO + "cd", 2, 4},
		{"ab" + flagRO + "cd", 4, 6},
		{"ok" + thumbs + "!", 2, 5},
		{"a\U0001F3FD b", 0, 2}, // an emoji modifier extends a letter too
	}
	for _, c := range cases {
		if got := WordBoundaryRight([]rune(c.line), c.col); got != c.want {
			t.Errorf("WordBoundaryRight(%q, %d) = %d, want %d", c.line, c.col, got, c.want)
		}
	}
}

func TestWordBoundaryLeftGraphemes(t *testing.T) {
	cases := []struct {
		line string
		col  int
		want int
	}{
		{"foo " + eAcute, 6, 4},
		{"caf" + eAcute + " bar", 5, 0},
		{zwjWord, 3, 0},
		{"a." + family + ".b", 8, 1},
		{"ab" + flagRO + "cd", 4, 2},
		{"ab" + flagRO, 4, 2},
		{"a \u0301", 3, 0}, // a space carrying a mark is whitespace
		{"\u0432\u0430\u0441\u044F \u043F\u0435\u0442\u044F", 9, 5},
	}
	for _, c := range cases {
		if got := WordBoundaryLeft([]rune(c.line), c.col); got != c.want {
			t.Errorf("WordBoundaryLeft(%q, %d) = %d, want %d", c.line, c.col, got, c.want)
		}
	}
}

func TestWordBoundsAt(t *testing.T) {
	cases := []struct {
		line       string
		col        int
		start, end int
		ok         bool
	}{
		{"foo.bar baz", 1, 0, 3, true},
		{"foo.bar baz", 3, 3, 4, true},
		{"foo.bar baz", 7, 4, 7, true}, // on whitespace: the run to its left
		{"foo.bar baz", 11, 8, 11, true},
		{"foo  bar", 4, 0, 0, false},
		{"  foo", 0, 0, 0, false},
		{"", 0, 0, 0, false},
		{"caf" + eAcute + " bar", 1, 0, 5, true},
		{"caf" + eAcute + " bar", 5, 0, 5, true},
		{"x " + eAcute + "y", 3, 2, 5, true},
		{"a." + family + ".b", 3, 1, 8, true},
		{"ab" + flagRO + "cd", 2, 2, 4, true},
		{"ab" + flagRO + "cd", 4, 4, 6, true},
	}
	for _, c := range cases {
		s, e, ok := WordBoundsAt([]rune(c.line), c.col)
		if s != c.start || e != c.end || ok != c.ok {
			t.Errorf("WordBoundsAt(%q, %d) = %d, %d, %v, want %d, %d, %v",
				c.line, c.col, s, e, ok, c.start, c.end, c.ok)
		}
	}
}
