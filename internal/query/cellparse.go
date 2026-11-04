// Cell parsing: a cell's text back into the Go value a bulk load sends for its
// destination column — the inverse of cells.go.

package query

import (
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	mssql "github.com/microsoft/go-mssqldb"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// TargetColumn is a destination column as NewCellParser needs it.
type TargetColumn struct {
	// Type is the column's system type as sys.types names it ("int",
	// "nvarchar", "datetime2"); an alias type's base type.
	Type string

	// Length is the declared n of char, varchar, nchar, nvarchar, binary and
	// varbinary — bytes, except nchar/nvarchar's UTF-16 code units — and -1
	// for (max). Other types ignore it: text, ntext, image and xml have no
	// declared length (sys.columns' 16 is a pointer's size).
	Length int

	// Precision and Scale are decimal/numeric's; Scale is also the
	// fractional-second digits of time, datetime2 and datetimeoffset.
	Precision, Scale int

	// CodePage is the code page of a char, varchar or text column's
	// collation (COLLATIONPROPERTY(…, 'CodePage')): 65001 for a _UTF8
	// collation, 1252 for Latin1_General.
	CodePage int

	// DatabaseCodePage is the code page of the database's default collation,
	// which is what a bulk load's char text is read in: INSERT BULK declares
	// the columns with no COLLATE, so the server takes the bytes in the
	// database default and converts them to the column's collation, a
	// character the column's code page lacks becoming '?'. Unused for a
	// UTF-8 column (charParser).
	DatabaseCodePage int
}

// ErrUnsupportedType is NewCellParser's refusal of a column type a bulk load
// cannot fill from text: sql_variant, whose cells lost their base type, CLR
// types (geography, geometry, hierarchyid), rowversion, which the server
// writes, and types this parser does not know.
var ErrUnsupportedType = errors.New("no text form to load")

// NewCellParser returns the function that turns one cell's text into the
// value a bulk load sends for col: the text query's cells render (the grid's,
// so an exported file loads back), and the commonest other spellings of the
// same value. A NULL is the caller's: the parser sees only non-NULL text.
//
// go-mssqldb's bulk copy checks almost nothing — an int too big for a tinyint
// wraps (300 loads as 44), a varchar gets the string's UTF-8 bytes whatever
// the collation, a too-long string fails the whole load with no row named —
// so every check that names the bad value is made here:
//
//   - integers, money and dates in their type's range; decimal within its
//     precision and scale; floats finite;
//   - no more fractional digits than the column keeps (decimal's scale,
//     money's 4, datetime's 3, time's scale): text that would be rounded is
//     an error, not a silent change;
//   - strings and binaries within the declared length, char/nchar/binary
//     padded to it as an INSERT pads; char text encoded to the database's code
//     page (TargetColumn.DatabaseCodePage), a character that or the column's
//     code page lacks an error;
//   - text that is not valid UTF-8 an error, so a file in another encoding is
//     refused rather than loaded as mojibake.
//
// The returned function keeps state (a code page's encoder) and is for one
// goroutine.
func NewCellParser(col TargetColumn) (func(text string) (any, error), error) {
	switch col.Type {
	case "bit":
		return parseBit, nil
	case "tinyint":
		return intParser(0, math.MaxUint8), nil
	case "smallint":
		return intParser(math.MinInt16, math.MaxInt16), nil
	case "int":
		return intParser(math.MinInt32, math.MaxInt32), nil
	case "bigint":
		return intParser(math.MinInt64, math.MaxInt64), nil
	case "decimal", "numeric":
		return decimalParser(col.Precision, col.Scale), nil
	case "money":
		return moneyParser(math.MinInt64, math.MaxInt64), nil
	case "smallmoney":
		return moneyParser(math.MinInt32, math.MaxInt32), nil
	case "float":
		return floatParser(64), nil
	case "real":
		return floatParser(32), nil
	case "date", "time", "datetime", "smalldatetime", "datetime2", "datetimeoffset":
		return timeParser(col.Type, col.Scale), nil
	case "uniqueidentifier":
		return parseGUID, nil
	case "binary", "varbinary":
		return binaryParser(col.Length, col.Type == "binary"), nil
	case "image":
		return binaryParser(-1, false), nil
	case "nchar", "nvarchar":
		return ncharParser(col.Length, col.Type == "nchar"), nil
	case "ntext", "xml":
		return ncharParser(-1, false), nil
	case "char", "varchar":
		return charParser(col.Length, col.Type == "char", col.CodePage, col.DatabaseCodePage)
	case "text":
		return charParser(-1, false, col.CodePage, col.DatabaseCodePage)
	}
	return nil, fmt.Errorf("%s column: %w", col.Type, ErrUnsupportedType)
}

func parseBit(s string) (any, error) {
	switch strings.ToLower(s) {
	case "1", "true":
		return true, nil
	case "0", "false":
		return false, nil
	}
	return nil, fmt.Errorf("%q is not a bit (1, 0, true or false)", s)
}

// intParser returns int64, the type go-mssqldb takes for every integer
// column.
func intParser(lo, hi int64) func(string) (any, error) {
	return func(s string) (any, error) {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return nil, fmt.Errorf("%q is not an integer", s)
		}
		if err != nil || n < lo || n > hi {
			return nil, fmt.Errorf("%s is out of range (%d to %d)", s, lo, hi)
		}
		return n, nil
	}
}

// splitDecimal splits -?digits(.digits)? (a + sign allowed, either digit run
// empty but not both) into its sign and digit runs.
func splitDecimal(s string) (neg bool, intPart, frac string, ok bool) {
	t := s
	if t != "" && (t[0] == '-' || t[0] == '+') {
		neg, t = t[0] == '-', t[1:]
	}
	intPart, frac, _ = strings.Cut(t, ".")
	ok = intPart+frac != "" && digitsOnly(intPart) && digitsOnly(frac)
	return neg, intPart, frac, ok
}

func digitsOnly(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// decimalParser returns the digits as a string, which go-mssqldb scales to
// the column exactly (a float would round).
func decimalParser(precision, scale int) func(string) (any, error) {
	return func(s string) (any, error) {
		neg, intPart, frac, ok := splitDecimal(s)
		if !ok {
			return nil, fmt.Errorf("%q is not a decimal number", s)
		}
		intPart = strings.TrimLeft(intPart, "0")
		if len(frac) > scale {
			return nil, fmt.Errorf("%s has more than %d decimal places", s, scale)
		}
		if len(intPart) > precision-scale {
			return nil, fmt.Errorf("%s does not fit decimal(%d,%d)", s, precision, scale)
		}
		return decimalText(neg, intPart, frac), nil
	}
}

func decimalText(neg bool, intPart, frac string) string {
	if intPart == "" {
		intPart = "0"
	}
	if neg {
		intPart = "-" + intPart
	}
	if frac == "" {
		return intPart
	}
	return intPart + "." + frac
}

// moneyParser checks the value as money stores it, an integer count of
// ten-thousandths in [lo, hi], and returns the digits as a string.
func moneyParser(lo, hi int64) func(string) (any, error) {
	return func(s string) (any, error) {
		neg, intPart, frac, ok := splitDecimal(s)
		if !ok {
			return nil, fmt.Errorf("%q is not a money amount", s)
		}
		if len(frac) > 4 {
			return nil, fmt.Errorf("%s has more than 4 decimal places", s)
		}
		digits := strings.TrimLeft(intPart+frac+strings.Repeat("0", 4-len(frac)), "0")
		var units uint64
		inRange := len(digits) <= 19
		for i := 0; inRange && i < len(digits); i++ {
			d := uint64(digits[i] - '0')
			inRange = units <= (math.MaxUint64-d)/10
			units = units*10 + d
		}
		limit := uint64(hi)
		if neg {
			limit = uint64(-(lo + 1)) + 1
		}
		if !inRange || units > limit {
			return nil, fmt.Errorf("%s is out of range (%s to %s)", s, moneyText(lo), moneyText(hi))
		}
		return decimalText(neg, strings.TrimLeft(intPart, "0"), frac), nil
	}
}

// moneyText renders ten-thousandths as money's decimal text.
func moneyText(units int64) string {
	s := strconv.FormatInt(units, 10)
	return s[:len(s)-4] + "." + s[len(s)-4:]
}

// floatParser returns float64 for float and float32 for real, each parsed at
// its own size so the grid's shortest round-trip text gives back the stored
// bits.
func floatParser(bits int) func(string) (any, error) {
	return func(s string) (any, error) {
		f, err := strconv.ParseFloat(s, bits)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return nil, fmt.Errorf("%q is not a number", s)
		}
		if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("%s is out of range for %s", s, map[int]string{32: "real", 64: "float"}[bits])
		}
		if bits == 32 {
			return float32(f), nil
		}
		return f, nil
	}
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
		return dateInRange(s, typ, t)
	}
}

// dateInRange applies datetime's and smalldatetime's narrower ranges, and
// rounds smalldatetime to the minute as SQL Server does (29.998 seconds
// down, 29.999 up): go-mssqldb would drop the seconds.
func dateInRange(s, typ string, t time.Time) (any, error) {
	switch typ {
	case "datetime":
		// .9985 and up rounds to the next 1/300 second, past the last day.
		if t.Year() < 1753 || t.Year() == 9999 && t.YearDay() == 365 && t.Hour() == 23 && t.Minute() == 59 && t.Second() == 59 && t.Nanosecond() >= 998500000 {
			return nil, fmt.Errorf("%s is out of range for datetime (1753-01-01 to 9999-12-31 23:59:59.997)", s)
		}
	case "smalldatetime":
		if t.Second()*1000+t.Nanosecond()/1e6 >= 29999 {
			t = t.Add(time.Minute)
		}
		t = t.Truncate(time.Minute)
		if t.Before(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)) || t.After(time.Date(2079, 6, 6, 23, 59, 0, 0, time.UTC)) {
			return nil, fmt.Errorf("%s is out of range for smalldatetime (1900-01-01 to 2079-06-06 23:59)", s)
		}
	}
	return t, nil
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

// parseGUID returns a uniqueidentifier's 16 bytes in the order go-mssqldb's
// bulk copy sends as they are, the wire order (the first three groups
// little-endian): the grid's dashed text, upper or lower case, optionally in
// braces.
func parseGUID(s string) (any, error) {
	t := s
	if len(t) == 38 && t[0] == '{' && t[37] == '}' {
		t = t[1:37]
	}
	ok := len(t) == 36
	for i := 0; ok && i < 36; i++ {
		switch i {
		case 8, 13, 18, 23:
			ok = t[i] == '-'
		default:
			ok = isHexDigit(t[i])
		}
	}
	var g mssql.UniqueIdentifier
	if !ok || g.Scan(t) != nil {
		return nil, fmt.Errorf("%q is not a uniqueidentifier", s)
	}
	return g.Value()
}

func isHexDigit(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// binaryParser reads the grid's 0x-prefixed hex (either case) into bytes,
// zero-padded to n for binary(n).
func binaryParser(n int, fixed bool) func(string) (any, error) {
	return func(s string) (any, error) {
		if len(s) < 2 || s[0] != '0' || s[1] != 'x' && s[1] != 'X' {
			return nil, fmt.Errorf("%q is not binary (0x followed by hex digits)", truncateForError(s))
		}
		b, err := hex.DecodeString(s[2:])
		if err != nil {
			return nil, fmt.Errorf("%q is not binary (0x followed by hex digits)", truncateForError(s))
		}
		if n > 0 && len(b) > n {
			return nil, fmt.Errorf("%d bytes is longer than the column's %d", len(b), n)
		}
		if fixed && len(b) < n {
			b = append(b, make([]byte, n-len(b))...)
		}
		return b, nil
	}
}

// ncharParser checks length in UTF-16 code units, nvarchar(n)'s unit, and
// pads nchar(n) with spaces.
func ncharParser(n int, fixed bool) func(string) (any, error) {
	return func(s string) (any, error) {
		if !utf8.ValidString(s) {
			return nil, errNotUTF8
		}
		if n <= 0 {
			return s, nil
		}
		units := 0
		for _, r := range s {
			units += utf16.RuneLen(r)
		}
		if units > n {
			return nil, fmt.Errorf("%d characters is longer than the column's %d", units, n)
		}
		if fixed && units < n {
			s += strings.Repeat(" ", n-units)
		}
		return s, nil
	}
}

var errNotUTF8 = errors.New("the text is not valid UTF-8")

// charParser encodes to the database's code page, which INSERT BULK reads
// char text in — go-mssqldb would send the UTF-8 bytes as they are — and
// refuses a character the column's code page lacks, which the server's
// conversion would store as '?'. Length is in bytes of the column's code
// page, char(n)'s and varchar(n)'s unit, and char(n) is padded with spaces.
//
// A column with a UTF-8 collation is the exception: the server describes it
// to a client that has not negotiated UTF-8 (go-mssqldb has not) as nchar or
// nvarchar, so the driver sends it UTF-16 from a string — bytes would go as
// they are and fail with Msg 4895 — and the server converts that to the
// column's UTF-8 whatever the database's code page.
func charParser(n int, fixed bool, codePage, dbCodePage int) (func(string) (any, error), error) {
	if codePage == 65001 {
		return func(s string) (any, error) {
			if !utf8.ValidString(s) {
				return nil, errNotUTF8
			}
			if n > 0 && len(s) > n {
				return nil, fmt.Errorf("%d bytes is longer than the column's %d", len(s), n)
			}
			if fixed && len(s) < n {
				s += strings.Repeat(" ", n-len(s))
			}
			return s, nil
		}, nil
	}
	load, err := encoderFor(dbCodePage)
	if err != nil {
		return nil, err
	}
	// col encodes only to check and measure, when the column's code page is
	// not the one the bytes travel in.
	col := load
	if codePage != dbCodePage {
		if col, err = encoderFor(codePage); err != nil {
			return nil, err
		}
	}
	encode := func(enc *encoding.Encoder, cp int, s, whose string) ([]byte, error) {
		if enc == nil {
			return []byte(s), nil
		}
		b, err := enc.Bytes([]byte(s))
		if err != nil {
			return nil, unencodable(s, enc, cp, whose)
		}
		return b, nil
	}
	return func(s string) (any, error) {
		if !utf8.ValidString(s) {
			return nil, errNotUTF8
		}
		stored, err := encode(col, codePage, s, "column's")
		if err != nil {
			return nil, err
		}
		if n > 0 && len(stored) > n {
			return nil, fmt.Errorf("%d bytes is longer than the column's %d", len(stored), n)
		}
		if fixed && len(stored) < n {
			// A space is one byte in every code page here.
			pad := strings.Repeat(" ", n-len(stored))
			s += pad
			stored = append(stored, pad...)
		}
		if codePage == dbCodePage {
			return stored, nil
		}
		return encode(load, dbCodePage, s, "database's")
	}, nil
}

// encoderFor returns code page cp's encoder, nil for UTF-8.
func encoderFor(cp int) (*encoding.Encoder, error) {
	if cp == 65001 {
		return nil, nil
	}
	e, ok := codePages[cp]
	if !ok {
		return nil, fmt.Errorf("code page %d: %w", cp, ErrUnsupportedType)
	}
	return e.NewEncoder(), nil
}

// unencodable names the first character of s that code page cp lacks.
func unencodable(s string, enc *encoding.Encoder, cp int, whose string) error {
	for _, r := range s {
		if _, err := enc.String(string(r)); err != nil {
			return fmt.Errorf("%q (U+%04X) is not in the %s code page %d", r, r, whose, cp)
		}
	}
	return fmt.Errorf("the text cannot be encoded in the %s code page %d", whose, cp)
}

// codePages are the code pages SQL Server collations use, except UTF-8.
var codePages = map[int]encoding.Encoding{
	437:  charmap.CodePage437,
	850:  charmap.CodePage850,
	874:  charmap.Windows874,
	932:  japanese.ShiftJIS,
	936:  simplifiedchinese.GBK,
	949:  korean.EUCKR,
	950:  traditionalchinese.Big5,
	1250: charmap.Windows1250,
	1251: charmap.Windows1251,
	1252: charmap.Windows1252,
	1253: charmap.Windows1253,
	1254: charmap.Windows1254,
	1255: charmap.Windows1255,
	1256: charmap.Windows1256,
	1257: charmap.Windows1257,
	1258: charmap.Windows1258,
}

// truncateForError shortens s for an error message: a binary cell can be
// megabytes.
func truncateForError(s string) string {
	if len(s) <= 40 {
		return s
	}
	return s[:40] + "…"
}
