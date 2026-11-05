// Cell parsing: a cell's text back into the Go value a bulk load sends for its
// destination column — the inverse of cells.go, over gosmo's value check.

package query

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/radix29/gosmo"
)

// ErrUnsupportedType is NewCellParser's refusal of a column type a bulk load
// cannot fill from text: sql_variant, whose cells lost their base type, CLR
// types (geography, geometry, hierarchyid), rowversion, which the server
// writes, types this parser does not know, and a char column whose code page
// gosmo has no encoder for.
var ErrUnsupportedType = errors.New("no text form to load")

// NewCellParser returns the function that turns one cell's text into the
// value a bulk load sends for col: the text query's cells render (the grid's,
// so an exported file loads back), and the commonest other spellings of the
// same value. A NULL is the caller's: the parser sees only non-NULL text.
//
// It is the text layer over gosmo's own check (gosmo.BulkColumn.Converter),
// which BulkInsert applies again: ranges, lengths, decimal scale, padding and
// char encoding are gosmo's, so its messages are the parser's. This layer
// reads the grid's date/time and binary forms (gosmo takes those as
// time.Time and []byte) and refuses, for a date/time, more fractional-second
// digits than the column keeps (datetime's 3, time's scale): text that would
// be rounded is an error, not a silent change. Numbers, bits,
// uniqueidentifiers and strings go to gosmo as the text itself. Checking here
// as well lets the caller name the file line, and lets a dry run check
// without loading. col.Name is best left empty: the caller names the column.
//
// The returned function keeps state (a code page's encoder) and is for one
// goroutine.
func NewCellParser(col gosmo.BulkColumn) (func(text string) (any, error), error) {
	var text func(string) (any, error)
	switch col.Type {
	case "bit", "tinyint", "smallint", "int", "bigint", "decimal", "numeric", "money", "smallmoney",
		"float", "real", "uniqueidentifier", "nchar", "nvarchar", "ntext", "xml", "char", "varchar", "text":
		text = func(s string) (any, error) { return s, nil }
	case "date", "time", "datetime", "smalldatetime", "datetime2", "datetimeoffset":
		text = timeParser(col.Type, col.Scale)
	case "binary", "varbinary", "image":
		text = parseBinary
	default:
		return nil, fmt.Errorf("%s column: %w", col.Type, ErrUnsupportedType)
	}
	conv, err := col.Converter()
	if errors.Is(err, gosmo.ErrUnsupported) {
		return nil, fmt.Errorf("%s column: %w (%v)", col.Type, ErrUnsupportedType, err)
	}
	if err != nil {
		return nil, err
	}
	return func(s string) (any, error) {
		v, err := text(s)
		if err != nil {
			return nil, err
		}
		return conv(v)
	}, nil
}

// timeParser parses the grid's layout for typ (timeLayout), also accepting a
// T between date and time, fewer fractional digits than the scale, and a
// datetimeoffset with no offset (+00:00, as SQL Server reads one). The
// result is a time.Time in UTC, or in the text's offset for datetimeoffset.
func timeParser(typ string, scale int) func(string) (any, error) {
	hasDate, hasClock := typ != "time", typ != "date"
	maxFrac := scale
	switch typ {
	case "datetime", "smalldatetime":
		maxFrac = 3
	}
	return func(s string) (any, error) {
		bad := func() (any, error) {
			return nil, fmt.Errorf("%q is not a %s (%s)", s, typ, timeLayout(strings.ToUpper(typ), scale, true))
		}
		rest := s
		year, month, day := 1900, 1, 1
		if hasDate {
			var ok bool
			if year, month, day, rest, ok = cutDate(rest); !ok {
				return bad()
			}
			if hasClock {
				if rest == "" || rest[0] != ' ' && rest[0] != 'T' {
					return bad()
				}
				rest = rest[1:]
			}
		}
		var c clock
		if hasClock {
			var ok bool
			if c, rest, ok = cutClock(rest); !ok {
				return bad()
			}
			if c.fracDigits > maxFrac {
				return nil, fmt.Errorf("%s has more than %d fractional-second digits", s, maxFrac)
			}
		}
		loc := time.UTC
		if typ == "datetimeoffset" {
			rest = strings.TrimPrefix(rest, " ")
			if rest != "" {
				offset, ok := parseOffset(rest)
				if !ok {
					return bad()
				}
				loc, rest = time.FixedZone("", offset), ""
			}
		}
		if rest != "" {
			return bad()
		}
		t := time.Date(year, time.Month(month), day, c.hour, c.min, c.sec, c.nsec, loc)
		if t.Day() != day || t.Month() != time.Month(month) {
			return bad() // a day the month does not have
		}
		return t, nil
	}
}

// cutDate reads yyyy-mm-dd from the front of s.
func cutDate(s string) (year, month, day int, rest string, ok bool) {
	if len(s) < 10 || s[4] != '-' || s[7] != '-' {
		return 0, 0, 0, s, false
	}
	year, ok1 := fixedDigits(s[0:4])
	month, ok2 := fixedDigits(s[5:7])
	day, ok3 := fixedDigits(s[8:10])
	ok = ok1 && ok2 && ok3 && year >= 1 && month >= 1 && month <= 12 && day >= 1 && day <= 31
	return year, month, day, s[10:], ok
}

type clock struct {
	hour, min, sec, nsec, fracDigits int
}

// cutClock reads hh:mm:ss with an optional fraction of up to 9 digits from the
// front of s.
func cutClock(s string) (c clock, rest string, ok bool) {
	if len(s) < 8 || s[2] != ':' || s[5] != ':' {
		return c, s, false
	}
	var ok1, ok2, ok3 bool
	c.hour, ok1 = fixedDigits(s[0:2])
	c.min, ok2 = fixedDigits(s[3:5])
	c.sec, ok3 = fixedDigits(s[6:8])
	if !ok1 || !ok2 || !ok3 || c.hour > 23 || c.min > 59 || c.sec > 59 {
		return c, s, false
	}
	rest = s[8:]
	if rest == "" || rest[0] != '.' {
		return c, rest, true
	}
	n := 1
	for n < len(rest) && rest[n] >= '0' && rest[n] <= '9' {
		n++
	}
	c.fracDigits = n - 1
	if c.fracDigits == 0 || c.fracDigits > 9 {
		return c, s, false
	}
	c.nsec, _ = fixedDigits(rest[1:n] + strings.Repeat("0", 9-c.fracDigits))
	return c, rest[n:], true
}

// parseOffset reads all of s as Z or ±hh:mm within ±14:00, in seconds.
func parseOffset(s string) (int, bool) {
	if s == "Z" {
		return 0, true
	}
	if len(s) != 6 || s[0] != '+' && s[0] != '-' || s[3] != ':' {
		return 0, false
	}
	h, ok1 := fixedDigits(s[1:3])
	m, ok2 := fixedDigits(s[4:6])
	secs := (h*60 + m) * 60
	if !ok1 || !ok2 || m > 59 || secs > 14*3600 {
		return 0, false
	}
	if s[0] == '-' {
		secs = -secs
	}
	return secs, true
}

// fixedDigits parses s, all decimal digits.
func fixedDigits(s string) (int, bool) {
	n := 0
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
		n = n*10 + int(s[i]-'0')
	}
	return n, s != ""
}

// parseBinary reads the grid's 0x-prefixed hex (either case) into bytes.
func parseBinary(s string) (any, error) {
	if len(s) < 2 || s[0] != '0' || s[1] != 'x' && s[1] != 'X' {
		return nil, fmt.Errorf("%q is not binary (0x followed by hex digits)", truncateForError(s))
	}
	b, err := hex.DecodeString(s[2:])
	if err != nil {
		return nil, fmt.Errorf("%q is not binary (0x followed by hex digits)", truncateForError(s))
	}
	return b, nil
}

// truncateForError shortens s for an error message: a binary cell can be
// megabytes.
func truncateForError(s string) string {
	if len(s) <= 40 {
		return s
	}
	return s[:40] + "…"
}
