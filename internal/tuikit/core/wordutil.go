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
