package output

import (
	"encoding/json"
	"unicode/utf8"

	"github.com/radix29/gossms/internal/export"
)

// cellKind is how JSON writes a column's cells (export.Kind).
type cellKind = export.Kind

const (
	kindString = export.KindString
	kindNumber = export.KindNumber
	kindBool   = export.KindBool
	kindJSON   = export.KindJSON
)

// TypeJSON is the Type of a column whose cells are JSON documents (an
// event's fields and actions): JSON and jsonl write a valid one as a value
// rather than a string, and whole — --max-col-width would cut it into
// invalid JSON. Text, markdown, csv and tsv show the document's text.
const TypeJSON = "json"

// kindOf is export.KindOf, which reads TypeJSON as kindJSON.
func kindOf(t string) cellKind { return export.KindOf(t) }

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

func isJSONNumber(s string) bool { return export.IsJSONNumber(s) }

// clip cuts s to width characters (runes) and marks what it cut:
// "abc…[+1,234 chars]". width 0 keeps everything. A count of characters, not
// display columns: the marker states how much is missing, which a column
// count would not.
//
// A cell is cut only when the cut saves more characters than the marker
// adds: cutting one character of a 12-character cell at width 11 printed
// 22, defeating the option's purpose of bounding line width. So a cut cell
// is always shorter than the whole one, and a cell is never longer than
// width plus its marker.
func clip(s string, width int) string {
	if width <= 0 || len(s) <= width {
		return s
	}
	n := utf8.RuneCountInString(s)
	if n <= width {
		return s
	}
	marker := clipMarker(n - width)
	if n-width <= utf8.RuneCountInString(marker) {
		return s
	}
	cut := 0
	for range width {
		_, size := utf8.DecodeRuneInString(s[cut:])
		cut += size
	}
	return s[:cut] + marker
}

// clipMarker is clip's mark for m characters cut: "…[+1 char]",
// "…[+1,234 chars]".
func clipMarker(m int) string {
	unit := " chars]"
	if m == 1 {
		unit = " char]"
	}
	return "…[+" + thousands(m) + unit
}
