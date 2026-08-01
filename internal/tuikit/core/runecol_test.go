package core

import (
	"testing"

	"github.com/clipperhouse/displaywidth"
)

func TestRuneWidth(t *testing.T) {
	cases := map[rune]int{
		'a': 1,
		' ': 1,
		'世': 2,
		'界': 2,
		'́': 0, // combining acute accent
	}
	for r, want := range cases {
		if got := RuneWidth(r); got != want {
			t.Errorf("RuneWidth(%q) = %d, want %d", r, got, want)
		}
	}
}

func TestRunesWidth(t *testing.T) {
	cases := map[string]int{
		"":     0,
		"abc":  3,
		"世界":   4,
		"世界OK": 6,
		"é":   1, // base + combining mark share one column
	}
	for s, want := range cases {
		if got := RunesWidth([]rune(s)); got != want {
			t.Errorf("RunesWidth(%q) = %d, want %d", s, got, want)
		}
	}
}

func TestColumnOfRune(t *testing.T) {
	line := []rune("世界OK")
	cases := map[int]int{
		0: 0, // before 世
		1: 2, // before 界 — 世 took columns 0-1
		2: 4, // before 'O'
		3: 5,
		4: 6,
		// Past the end, each missing rune counts one column, which is what
		// lets a caret or selection sit beyond end-of-line.
		5: 7,
		6: 8,
	}
	for idx, want := range cases {
		if got := ColumnOfRune(line, idx); got != want {
			t.Errorf("ColumnOfRune(%q, %d) = %d, want %d", string(line), idx, got, want)
		}
	}
	if got := ColumnOfRune(line, -3); got != 0 {
		t.Errorf("ColumnOfRune with a negative index = %d, want 0", got)
	}
}

// TestRuneIndexAtColumnSnapsToWideRuneStarts pins the click-targeting rule:
// a column landing on either cell of a double-width rune resolves to that
// rune, never to a position between its two cells — there is no text
// position there, and returning one puts the caret inside a glyph.
func TestRuneIndexAtColumnSnapsToWideRuneStarts(t *testing.T) {
	line := []rune("世界OK")
	cases := map[int]int{
		0: 0, // left half of 世
		1: 0, // right half of 世
		2: 1, // left half of 界
		3: 1, // right half of 界
		4: 2, // 'O'
		5: 3, // 'K'
		6: 4, // just past the end
		7: 5, // one virtual position per further column
	}
	for col, want := range cases {
		if got := RuneIndexAtColumn(line, col); got != want {
			t.Errorf("RuneIndexAtColumn(%q, col=%d) = %d, want %d", string(line), col, got, want)
		}
	}
	if got := RuneIndexAtColumn(line, -2); got != 0 {
		t.Errorf("RuneIndexAtColumn with a negative column = %d, want 0", got)
	}
}

// TestColumnOfRuneAndRuneIndexAtColumnRoundTrip: every rune boundary must
// survive index -> column -> index, inside the line and past its end. These
// two are inverses over boundaries, and Editor/InputField rely on that every
// time a caret is placed and then redrawn.
func TestColumnOfRuneAndRuneIndexAtColumnRoundTrip(t *testing.T) {
	for _, s := range []string{"", "abc", "世界", "a世b界c", "世a界b", "éx"} {
		line := []rune(s)
		for idx := 0; idx <= len(line)+3; idx++ {
			col := ColumnOfRune(line, idx)
			back := RuneIndexAtColumn(line, col)
			// A zero-width rune shares its base's column, so the round trip
			// lands on the base — the only boundary that column identifies.
			if idx < len(line) && RuneWidth(line[idx]) == 0 {
				continue
			}
			if back != idx {
				t.Errorf("%q: idx %d -> col %d -> idx %d", s, idx, col, back)
			}
		}
	}
}

// TestRuneWidthASCIIFastPathAgrees checks the branch RuneWidth takes for
// printable ASCII against the table it skips, over the whole range — the
// speedup is only sound if the two never disagree.
func TestRuneWidthASCIIFastPathAgrees(t *testing.T) {
	for r := rune(0); r < 0x100; r++ {
		if got, want := RuneWidth(r), displaywidth.Rune(r); got != want {
			t.Errorf("RuneWidth(%U) = %d, displaywidth.Rune = %d", r, got, want)
		}
	}
}
