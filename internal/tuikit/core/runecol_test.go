package core

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

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

// graphemeProbes are the clusters K1 named, each wider or narrower than the sum
// of its runes' widths, with the width tcell gives the cluster.
var graphemeProbes = []struct {
	s     string
	width int
}{
	{"❤️", 2},    // heart + VS16: runes sum to 1
	{"🇺🇸", 2},    // two regional indicators: runes sum to 4
	{"👨‍👩‍👧", 2}, // ZWJ family: runes sum to 6
	{"👍🏽", 2},    // emoji + skin-tone modifier: runes sum to 4
	{"#️⃣", 2},   // keycap: runes sum to 1
	{"é", 1},    // base + combining acute
	{"각", 2},   // conjoining Hangul jamo: runes sum to 4
}

// TestGraphemeWidthsMatchTheTerminal is K1: every helper measures by grapheme
// cluster, as DisplayWidth and tcell do, so a cluster after "x" starts at column
// 1 and the "y" after it at 1+width.
func TestGraphemeWidthsMatchTheTerminal(t *testing.T) {
	for _, p := range graphemeProbes {
		if got := DisplayWidth(p.s); got != p.width {
			t.Fatalf("probe %q: DisplayWidth = %d, want %d — probe table is stale", p.s, got, p.width)
		}
		line := []rune("x" + p.s + "y")
		n := len(line)
		if got := RunesWidth(line); got != p.width+2 {
			t.Errorf("RunesWidth(%q) = %d, want %d", string(line), got, p.width+2)
		}
		if end, w := GraphemeAt(line, 1); end != n-1 || w != p.width {
			t.Errorf("GraphemeAt(%q, 1) = (%d, %d), want (%d, %d)", string(line), end, w, n-1, p.width)
		}
		if got := ColumnOfRune(line, n-1); got != 1+p.width {
			t.Errorf("ColumnOfRune(%q, y) = %d, want %d", string(line), got, 1+p.width)
		}
		// Every column of the cluster resolves to its start, and the one after
		// to the "y".
		for c := 1; c < 1+p.width; c++ {
			if got := RuneIndexAtColumn(line, c); got != 1 {
				t.Errorf("RuneIndexAtColumn(%q, %d) = %d, want 1", string(line), c, got)
			}
		}
		if got := RuneIndexAtColumn(line, 1+p.width); got != n-1 {
			t.Errorf("RuneIndexAtColumn(%q, %d) = %d, want %d", string(line), 1+p.width, got, n-1)
		}
		// The cursor steps over the cluster whole, both ways, and an index inside
		// it maps to its start column.
		if got := NextGrapheme(line, 1); got != n-1 {
			t.Errorf("NextGrapheme(%q, 1) = %d, want %d", string(line), got, n-1)
		}
		if got := PrevGrapheme(line, n-1); got != 1 {
			t.Errorf("PrevGrapheme(%q, %d) = %d, want 1", string(line), n-1, got)
		}
		for mid := 2; mid < n-1; mid++ {
			if got := ColumnOfRune(line, mid); got != 1 {
				t.Errorf("ColumnOfRune(%q, %d) inside the cluster = %d, want 1", string(line), mid, got)
			}
		}
	}
}

// TestPrevGraphemeAfterARunOfClusters walks a line of nothing but multi-rune
// clusters backwards, where no ASCII pair gives PrevGrapheme a shortcut.
func TestPrevGraphemeAfterARunOfClusters(t *testing.T) {
	line := []rune("🇺🇸🇫🇷❤️👍🏽")
	var forward []int
	for i := 0; i < len(line); i = NextGrapheme(line, i) {
		forward = append(forward, i)
	}
	if want := []int{0, 2, 4, 6}; !slices.Equal(forward, want) {
		t.Fatalf("cluster starts = %v, want %v", forward, want)
	}
	i := len(line)
	for k := len(forward) - 1; k >= 0; k-- {
		i = PrevGrapheme(line, i)
		if i != forward[k] {
			t.Fatalf("PrevGrapheme walk reached %d, want %d", i, forward[k])
		}
	}
	// Past the end and at the start, one virtual step, as before.
	if got := PrevGrapheme(line, len(line)+2); got != len(line)+1 {
		t.Errorf("PrevGrapheme past the end = %d, want %d", got, len(line)+1)
	}
	if got := NextGrapheme(line, len(line)); got != len(line)+1 {
		t.Errorf("NextGrapheme at the end = %d, want %d", got, len(line)+1)
	}
}

// TestGraphemeAtLongCluster: a cluster longer than GraphemeAt's first window
// (a base with 40 combining marks) is still found whole.
func TestGraphemeAtLongCluster(t *testing.T) {
	line := []rune("a" + strings.Repeat("́", 40) + "b")
	if end, w := GraphemeAt(line, 0); end != 41 || w != 1 {
		t.Fatalf("GraphemeAt = (%d, %d), want (41, 1)", end, w)
	}
}

// FuzzRunesWidthAgreesWithDisplayWidth fuzzes the fast paths against the
// string measure over mixed text.
func FuzzRunesWidthAgreesWithDisplayWidth(f *testing.F) {
	for _, p := range graphemeProbes {
		f.Add("ab" + p.s + "世" + p.s)
	}
	f.Add("SELECT N'é' AS [x]")
	f.Fuzz(func(t *testing.T, s string) {
		if !utf8.ValidString(s) || strings.ContainsAny(s, "\r\n\t") {
			return
		}
		line := []rune(s)
		if got, want := RunesWidth(line), DisplayWidth(s); got != want {
			t.Fatalf("RunesWidth(%q) = %d, DisplayWidth = %d", s, got, want)
		}
		for i := 0; i < len(line); i = NextGrapheme(line, i) {
			if back := PrevGrapheme(line, NextGrapheme(line, i)); back != i {
				t.Fatalf("%q: PrevGrapheme(NextGrapheme(%d)) = %d", s, i, back)
			}
		}
		// The fast paths against a plain cluster-by-cluster walk.
		for idx := 0; idx <= len(line)+1; idx++ {
			if got, want := ColumnOfRune(line, idx), refColumnOfRune(line, idx); got != want {
				t.Fatalf("%q: ColumnOfRune(%d) = %d, cluster walk gives %d", s, idx, got, want)
			}
		}
		for col := 0; col <= RunesWidth(line)+1; col++ {
			gi, gs := ClusterAtColumn(line, col)
			wi, ws := refClusterAtColumn(line, col)
			if gi != wi || gs != ws {
				t.Fatalf("%q: ClusterAtColumn(%d) = (%d, %d), cluster walk gives (%d, %d)", s, col, gi, gs, wi, ws)
			}
		}
	})
}

func refColumnOfRune(line []rune, idx int) int {
	col, i := 0, 0
	for i < idx && i < len(line) {
		end, w := GraphemeAt(line, i)
		if end > idx {
			return col
		}
		col += w
		i = end
	}
	return col + max(idx-len(line), 0)
}

func refClusterAtColumn(line []rune, col int) (int, int) {
	if col <= 0 {
		return 0, 0
	}
	c := 0
	for i := 0; i < len(line); {
		end, w := GraphemeAt(line, i)
		if c+w > col {
			return i, c
		}
		c += w
		i = end
	}
	return len(line) + (col - c), col
}
