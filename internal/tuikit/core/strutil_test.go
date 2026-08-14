package core

import (
	"math"
	"slices"
	"strings"
	"testing"
)

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
		// The single pass decides "fits" and "where to cut" together, so the
		// boundary either side of n is where it would go wrong first.
		{"abcde", 5, "abcde"},  // exactly n, returned whole
		{"abcdef", 5, "abcd…"}, // one column over
		{"ab", 1, "…"},         // no room for anything but the ellipsis
		{"a", 1, "a"},          // ...unless it already fits
		{"你好", 2, "…"},         // a wide grapheme straddling the budget is dropped whole
		{"你a", 3, "你a"},        // mixed widths summing to exactly n
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
		// The extremes. Taking a number's digits by negating it and dividing
		// down is wrong at math.MinInt64, where the negation is a no-op and
		// the loop produces nothing: this used to answer "--".
		{math.MaxInt64, "9,223,372,036,854,775,807"},
		{math.MinInt64, "-9,223,372,036,854,775,808"},
		{math.MinInt64 + 1, "-9,223,372,036,854,775,807"},
	}
	for _, c := range cases {
		if got := FormatThousands(c.n); got != c.want {
			t.Errorf("FormatThousands(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}

func TestWrapText(t *testing.T) {
	cases := []struct {
		name string
		text string
		w    int
		want []string
	}{
		{"fits on one line", "one two", 20, []string{"one two"}},
		{"greedy fill", "aaa bbb ccc", 7, []string{"aaa bbb", "ccc"}},
		{"exact fit is not a break", "aaa bbb", 7, []string{"aaa bbb"}},
		{"empty text", "   ", 10, []string{""}},
		{"non-positive width passes text through", "anything", 0, []string{"anything"}},
		// A word wider than the line is broken rather than left to overflow.
		// Unbroken it came back as one 12-column line for a 5-column pane,
		// and every caller draws through DrawTextClipped — so the tail was
		// cut off the screen with no ellipsis and no way to reach it.
		{"long word is hard-broken", "abcdefghijkl", 5, []string{"abcde", "fghij", "kl"}},
		{"long word after a short one", "hi abcdefghijkl", 5, []string{"hi", "abcde", "fghij", "kl"}},
		{"long word before a short one", "abcdefghijkl hi", 5, []string{"abcde", "fghij", "kl hi"}},
		// The break lands on grapheme boundaries, not bytes: a wide rune
		// that would straddle the edge moves to the next line whole.
		{"wide runes break on a boundary", "你好吗朋友", 4, []string{"你好", "吗朋", "友"}},
		// One grapheme wider than the whole line still has to make progress,
		// or the split loop never terminates.
		{"grapheme wider than the line", "你好", 1, []string{"你", "好"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := WrapText(c.text, c.w)
			if !slices.Equal(got, c.want) {
				t.Fatalf("WrapText(%q, %d) = %q, want %q", c.text, c.w, got, c.want)
			}
		})
	}
}

// Whatever the input, no line may come back wider than the width asked for —
// that is the whole promise, and the one a caller drawing with
// DrawTextClipped depends on. The single exception is a lone grapheme wider
// than the whole line, which has nowhere narrower to go and must still be
// emitted or the split loop cannot make progress. The non-positive-width
// passthrough is the other documented exception, covered above.
func TestWrapTextNeverExceedsWidth(t *testing.T) {
	texts := []string{
		"a short sentence",
		"C:\\Program_Files\\Microsoft_SQL_Server\\MSSQL16.MSSQLSERVER\\MSSQL\\DATA\\db_log.ldf",
		"mixed 你好吗朋友们 with_a_very_long_unbroken_token_indeed and more words",
		strings.Repeat("x", 200),
		"你好吗朋友们你好吗朋友们",
	}
	for _, text := range texts {
		for w := 1; w <= 40; w++ {
			for _, line := range WrapText(text, w) {
				got := DisplayWidth(line)
				if got <= w {
					continue
				}
				if _, rest := splitGrapheme(line, 0); rest == "" {
					continue // one grapheme, wider than w on its own
				}
				t.Fatalf("WrapText(%q, %d) produced a %d-column line %q", text, w, got, line)
			}
		}
	}
}

// No line comes back empty either — a blank row in a wrapped message reads as
// a paragraph break that isn't there. A word dividing exactly into full lines
// used to leave one on the end.
func TestWrapTextEmitsNoBlankLines(t *testing.T) {
	for _, text := range []string{"abcdefghij", "hi abcdefghij", "你好", "你好吗朋友们"} {
		for w := 1; w <= 12; w++ {
			lines := WrapText(text, w)
			for i, line := range lines {
				if line == "" {
					t.Fatalf("WrapText(%q, %d) = %q — line %d is blank", text, w, lines, i)
				}
			}
		}
	}
}

// Breaking a word must not lose or reorder any of it.
func TestWrapTextPreservesEveryWord(t *testing.T) {
	text := "prefix C:\\a_very_long_unbroken_path_component_here\\file.ldf suffix"
	for w := 1; w <= 30; w++ {
		joined := strings.Join(WrapText(text, w), "")
		want := strings.ReplaceAll(text, " ", "")
		if strings.ReplaceAll(joined, " ", "") != want {
			t.Fatalf("WrapText(text, %d) lost or reordered characters: %q", w, joined)
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

// TestWrapTextLimitDoesNotInventSpaces pins the fold's one subtlety. WrapText
// hard-breaks a token too long for the line, so rejoining the overflow with a
// space — which is what the two call sites did before WrapTextLimit existed —
// splits an unreachable UNC path into two plausible-looking ones.
func TestWrapTextLimitDoesNotInventSpaces(t *testing.T) {
	const path = "bakserverxyz.corp.internal.example.invalid/share/db.bak"
	lines := WrapTextLimit(path+" is unreachable", 20, 2)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	// The clipped line is a prefix of the path continuing from line 1, with
	// nothing inserted at the break.
	joined := strings.TrimSuffix(lines[0]+lines[1], "…")
	if !strings.HasPrefix(path, joined) {
		t.Errorf("folded lines %q + %q = %q, which is not a prefix of %q — a space was injected mid-token",
			lines[0], lines[1], joined, path)
	}
}

// A soft break is still rejoined with the space it was broken at.
func TestWrapTextLimitKeepsSpacesAtSoftBreaks(t *testing.T) {
	lines := WrapTextLimit("alpha beta gamma delta epsilon", 11, 2)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if strings.Contains(lines[1], "gammadelta") {
		t.Errorf("last line = %q, want the words kept apart", lines[1])
	}
}

// TestPadIsExactlyNColumnsForAnyInput pins the contract both functions state:
// exactly n display columns, whatever they are handed. Malformed UTF-8 broke
// it — a trailing partial cluster absorbed the padding space and PadRight
// came back 3 columns wide for n=2.
func TestPadIsExactlyNColumnsForAnyInput(t *testing.T) {
	inputs := []string{"", "a", "ab", "abc", "0\xcc", "\xcc", "\xff\xfe", "é", "日", "日本語", "é", "a\xcc\xcc"}
	for _, s := range inputs {
		for n := 1; n <= 5; n++ {
			if w := DisplayWidth(PadRight(s, n)); w != n {
				t.Errorf("DisplayWidth(PadRight(%q, %d)) = %d, want %d", s, n, w, n)
			}
			if w := DisplayWidth(PadLeft(s, n)); w != n {
				t.Errorf("DisplayWidth(PadLeft(%q, %d)) = %d, want %d", s, n, w, n)
			}
		}
	}
}
