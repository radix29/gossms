package core

import "unicode"

// ---------------------------------------------------------------------------
// Word-boundary helpers, shared by Editor and InputField for Ctrl+Left/
// Right word-jump navigation and Ctrl+Backspace/Delete word deletion.
//
// They step by grapheme cluster, not by rune, and classify a cluster by its
// first rune. A combining mark, ZWJ, variation selector or emoji modifier
// therefore continues the word (or punctuation run) it is attached to, and
// every result is a cluster boundary — the runecol rule: the cursor never
// lands inside a cluster. Classifying rune by rune put a mark in the
// punctuation class, so Ctrl+Right on "e"+U+0301 stopped between the two and
// Ctrl+Backspace left an orphaned mark (TestWordBoundaryRightGraphemes).
// ---------------------------------------------------------------------------

// IsWordRune reports whether r is a "word" character: a Unicode letter,
// digit, or underscore.
func IsWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

type wordClass uint8

const (
	classSpace wordClass = iota
	classWord
	classPunct
)

// clusterClass classifies the cluster starting at rune index s by its first
// rune.
func clusterClass(line []rune, s int) wordClass {
	switch r := line[s]; {
	case unicode.IsSpace(r):
		return classSpace
	case IsWordRune(r):
		return classWord
	default:
		return classPunct
	}
}

// spaceAnchor returns the nearest k < hi that is a cluster boundary right after
// a whitespace cluster, or 0. A forward cluster walk can start there in the
// "after whitespace" state without segmenting from the line's start, which on
// a long non-ASCII line would cost a segmentation per rune per keystroke.
func spaceAnchor(line []rune, hi int) int {
	for k := hi - 1; k > 0; k-- {
		if p := line[k-1]; p < joinFloor && unicode.IsSpace(p) {
			if end, _ := GraphemeAt(line, k-1); end == k {
				return k
			}
		}
	}
	return 0
}

// lastRunStart walks clusters from a (0 or a spaceAnchor) up to stop and
// returns the start of the last non-whitespace same-class run that begins
// before stop.
func lastRunStart(line []rune, a, stop int) (int, bool) {
	start, found, prev := 0, false, classSpace
	for s := a; s < stop; s = NextGrapheme(line, s) {
		c := clusterClass(line, s)
		if c != classSpace && c != prev {
			start, found = s, true
		}
		prev = c
	}
	return start, found
}

// WordBoundaryLeft returns the column reached by moving left from col: skip
// whitespace to the left, then one run of same-class clusters (word vs. other
// non-space). Never less than 0; does not cross lines.
func WordBoundaryLeft(line []rune, col int) int {
	j := Clamp(col, 0, len(line))
	// Trailing plain whitespace is skipped rune by rune: a space below joinFloor
	// never extends the cluster before it.
	for j > 0 && line[j-1] < joinFloor && unicode.IsSpace(line[j-1]) {
		j--
	}
	if j == 0 {
		return 0
	}
	if s, ok := lastRunStart(line, spaceAnchor(line, j), j); ok {
		return s
	}
	// Only whitespace clusters (a space carrying a mark) between the anchor and
	// j: walk the whole prefix.
	s, _ := lastRunStart(line, 0, j)
	return s
}

// WordBoundsAt returns the [start, end) rune range of the word under col (what
// a double-click selects): a run of word clusters, or of same-class non-space
// punctuation. A click past the line end or on whitespace takes the run
// immediately to its left; whitespace with no word to its left reports
// ok == false.
func WordBoundsAt(line []rune, col int) (start, end int, ok bool) {
	n := len(line)
	i := Clamp(col, 0, n)
	prev := classSpace
	s := spaceAnchor(line, i)
	for s < n {
		e := NextGrapheme(line, s)
		c := clusterClass(line, s)
		if c != classSpace && c != prev {
			start = s
		}
		if i < e { // the cluster under col
			if c == classSpace {
				break
			}
			for e < n && clusterClass(line, e) == c {
				e = NextGrapheme(line, e)
			}
			return start, e, true
		}
		prev = c
		s = e
	}
	// col is on whitespace (starting at s) or past the end: the run ending at s.
	if prev == classSpace {
		return 0, 0, false
	}
	return start, s, true
}

// WordBoundaryRight mirrors WordBoundaryLeft, moving right from col. Never
// returns more than len(line).
func WordBoundaryRight(line []rune, col int) int {
	n := len(line)
	i := Clamp(col, 0, n)
	for i < n && clusterClass(line, i) == classSpace {
		i = NextGrapheme(line, i)
	}
	if i == n {
		return n
	}
	c := clusterClass(line, i)
	for i < n && clusterClass(line, i) == c {
		i = NextGrapheme(line, i)
	}
	return i
}
