package textutil

import "strings"

// Breaks is every character OneLine replaces: the line breaks Unicode and
// T-SQL text can carry (CR, LF, VT, FF, NEL, LINE SEPARATOR, PARAGRAPH
// SEPARATOR) and tab. strings.ContainsAny(s, Breaks) is how a caller asks
// whether s would draw on more than one line or with a tab's unknown width.
const Breaks = "\r\n\t\v\f\u0085  "

// OneLine replaces each character of Breaks in s with one space and leaves
// everything else as it was, so "\r\n" becomes two spaces and a value's own
// spacing survives. s is returned as is when it holds none.
func OneLine(s string) string {
	if !strings.ContainsAny(s, Breaks) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(Breaks, r) {
			return ' '
		}
		return r
	}, s)
}

// Collapse folds every run of whitespace in s (unicode.IsSpace, which covers
// Breaks and also NBSP) to one space and drops leading and trailing
// whitespace.
func Collapse(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// AllDigits reports whether s is a non-empty run of ASCII digits 0-9.
func AllDigits[T ~string | ~[]byte](s T) bool {
	if len(s) == 0 {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Nullable is nil for "" and a pointer to s otherwise, so JSON encodes an
// absent string as null rather than an empty string that could be a value.
func Nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
