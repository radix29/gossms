package core

import "unicode"

// ---------------------------------------------------------------------------
// Word-boundary helpers, shared by Editor and InputField for Ctrl+Left/
// Right word-jump navigation and Ctrl+Backspace/Delete word deletion.
// ---------------------------------------------------------------------------

// IsWordRune reports whether r is a "word" character: a Unicode letter,
// digit, or underscore.
func IsWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// WordBoundaryLeft returns the column reached by moving left from col: skip
// any whitespace immediately to the left, then skip one contiguous run of
// same-class runes (word runes vs. other non-space runes). Never returns
// less than 0. Does not cross line boundaries — callers handle that at
// column 0 themselves, same as plain Left already does.
func WordBoundaryLeft(line []rune, col int) int {
	i := Clamp(col, 0, len(line))
	for i > 0 && unicode.IsSpace(line[i-1]) {
		i--
	}
	if i == 0 {
		return 0
	}
	word := IsWordRune(line[i-1])
	for i > 0 && !unicode.IsSpace(line[i-1]) && IsWordRune(line[i-1]) == word {
		i--
	}
	return i
}

// WordBoundsAt returns the [start, end) rune range of the word under col —
// the span a double-click selects. The class of the rune at col decides what
// counts as "the word": a run of word runes, or a run of same-class
// non-space punctuation. A click past the end of the line, or one landing
// between words on whitespace, takes the run immediately to its left, so
// double-clicking just past a word still selects that word; whitespace with
// no word to its left reports ok == false and selects nothing.
func WordBoundsAt(line []rune, col int) (start, end int, ok bool) {
	i := Clamp(col, 0, len(line))
	if i == len(line) || unicode.IsSpace(line[i]) {
		if i == 0 || unicode.IsSpace(line[i-1]) {
			return 0, 0, false
		}
		i--
	}
	word := IsWordRune(line[i])
	start, end = i, i+1
	for start > 0 && !unicode.IsSpace(line[start-1]) && IsWordRune(line[start-1]) == word {
		start--
	}
	for end < len(line) && !unicode.IsSpace(line[end]) && IsWordRune(line[end]) == word {
		end++
	}
	return start, end, true
}

// WordBoundaryRight mirrors WordBoundaryLeft, moving right from col. Never
// returns more than len(line).
func WordBoundaryRight(line []rune, col int) int {
	i := Clamp(col, 0, len(line))
	for i < len(line) && unicode.IsSpace(line[i]) {
		i++
	}
	if i == len(line) {
		return i
	}
	word := IsWordRune(line[i])
	for i < len(line) && !unicode.IsSpace(line[i]) && IsWordRune(line[i]) == word {
		i++
	}
	return i
}
