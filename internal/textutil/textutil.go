package textutil

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Breaks is every character OneLine replaces: the line breaks Unicode and
// T-SQL text can carry (CR, LF, VT, FF, NEL, LINE SEPARATOR, PARAGRAPH
// SEPARATOR) and tab. strings.ContainsAny(s, Breaks) is how a caller asks
// whether s would draw on more than one line or with a tab's unknown width.
const Breaks = "\r\n\t\v\f\u0085  "

// OneLine replaces each character of Breaks in s with one space and Visible
// spells out every other control character, so "\r\n" becomes two spaces and a
// value's own spacing survives. s is returned as is when it holds neither.
func OneLine(s string) string {
	if !strings.ContainsAny(s, Breaks) {
		return Visible(s)
	}
	return Visible(strings.Map(func(r rune) rune {
		if strings.ContainsRune(Breaks, r) {
			return ' '
		}
		return r
	}, s))
}

// isHidden reports whether r is a control that must not reach a terminal as
// itself: a C0 control other than the layout characters of Breaks (and the
// \n that ends a line), DEL, a C1 control other than NEL, or a bidirectional
// override or isolate (U+202A-U+202E, U+2066-U+2069), which reorders the
// text around it.
func isHidden(r rune) bool {
	switch {
	case r == '\n', r == '\r', r == '\t', r == '\v', r == '\f', r == '\u0085':
		return false
	case r < 0x20, r >= 0x7f && r <= 0x9f:
		return true
	case r >= 0x202a && r <= 0x202e, r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}

// Visible replaces each character isHidden names with its JSON spelling
// (\u001b, \u202e), the form --format json already shows, so a value cannot
// carry an escape sequence or reorder text on the reader's terminal. Line
// breaks and tabs are left for the caller to fold. s is returned as is when
// it holds none.
func Visible(s string) string {
	if !strings.ContainsFunc(s, isHidden) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if isHidden(r) {
			fmt.Fprintf(&b, "\\u%04x", r)
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// JSONEscape spells out, as \uXXXX, every character in encoded JSON that
// Visible would hide and encoding/json leaves raw: DEL, the C1 controls
// (U+009B is CSI to an 8-bit-aware terminal, and U+0085 too, which isHidden
// spares) and the bidirectional overrides. encoding/json already escapes the
// C0 controls, and every non-ASCII byte of JSON text sits inside a string, so
// the value decodes unchanged. b is returned as is when it holds none.
func JSONEscape(b []byte) []byte {
	need := func(r rune) bool { return r >= 0x7f && (isHidden(r) || r == 0x85) }
	found := false
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		if need(r) {
			found = true
			break
		}
		i += n
	}
	if !found {
		return b
	}
	var out bytes.Buffer
	for i := 0; i < len(b); {
		r, n := utf8.DecodeRune(b[i:])
		if need(r) {
			fmt.Fprintf(&out, "\\u%04x", r)
		} else {
			out.Write(b[i : i+n])
		}
		i += n
	}
	return out.Bytes()
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
