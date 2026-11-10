package export

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
)

// Kind is how JSON writes a column's cells: the grid's text mapped by the
// column's declared type, never formatted a second way (docs/cli.md § Value
// formatting).
type Kind int

const (
	KindString Kind = iota
	KindNumber      // int, bigint, smallint, tinyint, real, float
	KindBool        // bit
	KindJSON        // json: a document, written as a value
)

// KindOf is the kind of a column of SQL type t ("int", "decimal(18,2)").
// decimal, numeric and money stay strings: most JSON parsers would read them
// as a float and lose digits. "json" is SQL Server's json type, and the type
// the command line gives a column of JSON documents (an event's fields).
func KindOf(t string) Kind {
	base, _, _ := strings.Cut(strings.ToLower(t), "(")
	switch base {
	case "int", "bigint", "smallint", "tinyint", "real", "float":
		return KindNumber
	case "bit":
		return KindBool
	case "json":
		return KindJSON
	}
	return KindString
}

// IsJSONNumber reports whether s is a number in JSON's grammar:
// -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?. The grid's float text
// ("0.5", "1E+300", "-1.5E-05") always is; the check keeps a stray value
// from making the document invalid.
func IsJSONNumber(s string) bool {
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

// ColumnKeys is each column's key in a row object (or element name in a row):
// its name, "column<n>" for a nameless one, and name_<n> (n its 1-based
// position, repeated until unique) for a duplicate or a name in reserved. A
// result set's names are neither unique nor non-empty (SELECT 1, 1), and a
// reader keying by name would otherwise drop a column.
func ColumnKeys(names []string, reserved ...string) []string {
	used := make(map[string]bool, len(names)+len(reserved))
	for _, r := range reserved {
		used[r] = true
	}
	keys := make([]string, len(names))
	for i, k := range names {
		if k == "" {
			k = "column" + strconv.Itoa(i+1)
		}
		for used[k] {
			k += "_" + strconv.Itoa(i+1)
		}
		used[k] = true
		keys[i] = k
	}
	return keys
}

// JSONWriter writes one result set as a JSON array of row objects, one object
// per line, keyed by ColumnKeys:
//
//	[
//	{"id":1,"name":"Ann","active":true,"note":null},
//	{"id":2,"name":"Bob","active":false,"note":""}
//	]
//
// A NULL is null, so it never reads as the string 'NULL' or "". Cells are
// valued by their column's Kind: int/float as numbers, bit as true/false, a
// json column's valid document as itself, and everything else — decimal and
// money included — as a string. A cell that is not what its type says (a
// number column holding "1,5") stays a string, so the document is always
// valid JSON.
//
// Rows are written as they come, nothing retained between them; Close ends
// the array and flushes.
type JSONWriter struct {
	w     *bufio.Writer
	keys  [][]byte // each column's key, JSON-encoded, with its colon
	kinds []Kind
	rows  int
	buf   bytes.Buffer
	enc   *json.Encoder
}

// NewJSONWriter returns a writer of rows with the given columns and their
// declared types (types may be nil or shorter: a missing type is a string).
func NewJSONWriter(w io.Writer, columns, types []string) *JSONWriter {
	j := &JSONWriter{w: bufio.NewWriter(w)}
	j.enc = json.NewEncoder(&j.buf)
	j.enc.SetEscapeHTML(false)
	for i, k := range ColumnKeys(columns) {
		j.keys = append(j.keys, append(j.encode(k), ':'))
		t := ""
		if i < len(types) {
			t = types[i]
		}
		j.kinds = append(j.kinds, KindOf(t))
	}
	return j
}

// encode is s as a JSON string, without the newline Encoder adds. The bytes are
// a copy: buf is reused.
func (j *JSONWriter) encode(s string) []byte {
	j.buf.Reset()
	_ = j.enc.Encode(s) // a string always encodes
	return bytes.Clone(bytes.TrimSuffix(j.buf.Bytes(), []byte{'\n'}))
}

// WriteRecord writes one row object. isNull may be nil or shorter; cells past
// the columns are dropped, missing ones are null.
func (j *JSONWriter) WriteRecord(cells []string, isNull []bool) error {
	if j.rows == 0 {
		j.w.WriteString("[\n{")
	} else {
		j.w.WriteString(",\n{")
	}
	j.rows++
	for i, key := range j.keys {
		if i > 0 {
			j.w.WriteByte(',')
		}
		j.w.Write(key)
		if i >= len(cells) || (i < len(isNull) && isNull[i]) {
			j.w.WriteString("null")
			continue
		}
		c := cells[i]
		switch k := j.kinds[i]; {
		case k == KindNumber && IsJSONNumber(c):
			j.w.WriteString(c)
		case k == KindBool && c == "1":
			j.w.WriteString("true")
		case k == KindBool && c == "0":
			j.w.WriteString("false")
		case k == KindJSON && json.Valid([]byte(c)):
			// Compacted: a document's own line breaks would break the
			// one-row-per-line layout.
			j.buf.Reset()
			_ = json.Compact(&j.buf, []byte(c)) // valid, so it compacts
			j.w.Write(j.buf.Bytes())
		default:
			j.w.Write(j.encode(c))
		}
	}
	_, err := j.w.WriteString("}")
	return err
}

// Close ends the array — [] for a set with no rows — and flushes.
func (j *JSONWriter) Close() error {
	if j.rows == 0 {
		j.w.WriteString("[]\n")
	} else {
		j.w.WriteString("\n]\n")
	}
	return j.w.Flush()
}
