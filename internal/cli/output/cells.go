package output

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// cellKind is how JSON writes a column's cells: the grid's text mapped by the
// column's declared type, never formatted a second way (docs/cli.md § Value
// formatting).
type cellKind int

const (
	kindString cellKind = iota
	kindNumber          // int, bigint, smallint, tinyint, real, float
	kindBool            // bit
	kindJSON            // TypeJSON
)

// TypeJSON is the Type of a column whose cells are JSON documents (an
// event's fields and actions): JSON and jsonl write a valid one as a value
// rather than a string, and whole — --max-col-width would cut it into
// invalid JSON. Text, markdown, csv and tsv show the document's text.
const TypeJSON = "json"

// kindOf is the kind of a column of SQL type t ("int", "decimal(18,2)").
// decimal, numeric and money stay strings: most JSON parsers would read them
// as a float and lose digits.
func kindOf(t string) cellKind {
	base, _, _ := strings.Cut(strings.ToLower(t), "(")
	switch base {
	case "int", "bigint", "smallint", "tinyint", "real", "float":
		return kindNumber
	case "bit":
		return kindBool
	case TypeJSON:
		return kindJSON
	}
	return kindString
}

func kinds(cols []Column) []cellKind {
	out := make([]cellKind, len(cols))
	for i, c := range cols {
		out[i] = kindOf(c.Type)
	}
	return out
}

// jsonCell is one cell as a JSON value: null, a number, a bool, a document
// (TypeJSON), or the text clipped to width. A number or bit whose text is not one (a hand-built set)
// stays a string, so the output is always valid JSON.
func jsonCell(c *string, k cellKind, width int) any {
	switch {
	case c == nil:
		return nil
	case k == kindNumber && isJSONNumber(*c):
		return json.RawMessage(*c)
	case k == kindBool && *c == "1":
		return true
	case k == kindBool && *c == "0":
		return false
	case k == kindJSON && json.Valid([]byte(*c)):
		return json.RawMessage(*c)
	}
	return clip(*c, width)
}

// isJSONNumber reports whether s is a number in JSON's grammar:
// -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?. The grid's float text
// ("0.5", "1E+300", "-1.5E-05") always is; the check keeps a stray value
// from making the document invalid.
func isJSONNumber(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	digits := func() int {
		n := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			n++
		}
		return n
	}
	start := i
	if n := digits(); n == 0 || (n > 1 && s[start] == '0') {
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		if digits() == 0 {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if digits() == 0 {
			return false
		}
	}
	return i == len(s)
}

// clip cuts s to width characters (runes) and marks what it cut:
// "abc…[+1,234 chars]". width 0 keeps everything. A count of characters, not
// display columns: the marker states how much is missing, which a column
// count would not.
func clip(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	n := utf8.RuneCountInString(s)
	if n <= width {
		return s
	}
	cut := 0
	for range width {
		_, size := utf8.DecodeRuneInString(s[cut:])
		cut += size
	}
	return s[:cut] + "…[+" + thousands(n-width) + " chars]"
}
