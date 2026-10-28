// Cell rendering: each column type's text as SSMS's grid shows it.

package query

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	mssql "github.com/microsoft/go-mssqldb"
)

// appendGUID appends a uniqueidentifier as SSMS renders it: NULL or uppercase
// dashed.
func appendGUID(dst []byte, g mssql.NullUniqueIdentifier) []byte {
	if !g.Valid {
		return append(dst, "NULL"...)
	}
	return append(dst, g.UUID.String()...)
}

// defaultTimeLayout is for a time.Time from a column with no known layout;
// matches plain "datetime". (A sql_variant's goes through appendVariant.)
const defaultTimeLayout = "2006-01-02 15:04:05.000"

// timeLayout returns SSMS's grid layout for a date/time column type, or "" for
// other types. Each type shows exactly the parts it stores — a single datetime
// layout would invent "00:00:00.000" for dates and truncate datetime2.
//
// datetime2, time and datetimeoffset have a scale (0-7) for fractional digits,
// from DecimalSize; unknown scale uses 7. Other types have fixed precision.
func timeLayout(databaseTypeName string, scale int, scaleKnown bool) string {
	if !scaleKnown {
		scale = 7
	}
	switch databaseTypeName {
	case "DATE":
		return "2006-01-02"
	case "TIME":
		return "15:04:05" + fracLayout(scale)
	case "SMALLDATETIME":
		return "2006-01-02 15:04:05"
	case "DATETIME":
		return defaultTimeLayout
	case "DATETIME2":
		return "2006-01-02 15:04:05" + fracLayout(scale)
	case "DATETIMEOFFSET":
		return "2006-01-02 15:04:05" + fracLayout(scale) + " -07:00"
	}
	return ""
}

// fracLayout returns the fractional-second layout fragment for scale; "" at 0
// (no decimal point, as SSMS shows time(0)).
func fracLayout(scale int) string {
	if scale <= 0 {
		return ""
	}
	return "." + strings.Repeat("0", min(scale, 7))
}

// appendValue renders a cell as SSMS does: NULL for nil, 1/0 for bit, 0x… for
// binary, date/time in its column's layout. isDecimalLike marks a []byte
// holding decoded decimal/money digits (render as text, not hex). layout is
// empty for non-date/time columns. Append form, so cells render through one
// reused buffer.
func appendValue(dst []byte, v any, isDecimalLike bool, layout string) []byte {
	switch x := v.(type) {
	case nil:
		return append(dst, "NULL"...)
	case bool:
		if x {
			return append(dst, '1')
		}
		return append(dst, '0')
	case []byte:
		if isDecimalLike {
			return append(dst, x...)
		}
		return appendHexUpper(dst, x)
	case time.Time:
		if layout == "" {
			layout = defaultTimeLayout
		}
		return x.AppendFormat(dst, layout)
	case float64:
		return appendFloat(dst, x, 64)
	case float32:
		return appendFloat(dst, float64(x), 32)
	case string:
		return append(dst, x...)
	case int64:
		// go-mssqldb returns every integer type as int64, the commonest cell;
		// strconv is ~4x faster than %v.
		return strconv.AppendInt(dst, x, 10)
	default:
		return fmt.Appendf(dst, "%v", x)
	}
}

// appendMoney renders money/smallmoney digits as SSMS does, SqlMoney's
// "#0.00##": at least two decimals, at most four, trailing zeros past the
// second dropped (12.5000 -> 12.50, 12.1230 -> 12.123). The driver always
// sends four; the padding covers anything shorter.
func appendMoney(dst []byte, digits []byte) []byte {
	intPart, frac, _ := bytes.Cut(digits, []byte{'.'})
	frac = bytes.TrimRight(frac, "0")
	dst = append(dst, intPart...)
	dst = append(dst, '.')
	dst = append(dst, frac...)
	for range 2 - len(frac) {
		dst = append(dst, '0')
	}
	return dst
}

// appendVariant renders a sql_variant cell. go-mssqldb decodes the variant's
// inner type and then drops it: decimal, numeric and money arrive as ASCII
// digits in a []byte, a uniqueidentifier as its 16 wire bytes, and every
// temporal type as a bare time.Time. So the value has to say what it was:
//
//   - a []byte that is exactly a decimal literal is decimal or money digits,
//     shown as text. A varbinary variant whose bytes spell one shows as text too
//     — rare, accepted (docs/decisions.md);
//   - a time.Time in a nameless fixed zone is a datetimeoffset, the only type
//     the driver decodes into one (the others get the connection's zone), and
//     keeps its offset;
//   - fractional seconds show three digits, as datetime does, and more only when
//     the value carries them.
//
// A date or time variant still shows as a datetime, and a GUID as hex: the value
// alone can't tell them from a datetime at midnight or a binary(16).
func appendVariant(dst []byte, v any) []byte {
	switch x := v.(type) {
	case []byte:
		if isDecimalLiteral(x) {
			return append(dst, x...)
		}
	case time.Time:
		dst = x.AppendFormat(dst, "2006-01-02 15:04:05.0000000")
		for n := 7; n > 3 && dst[len(dst)-1] == '0'; n-- {
			dst = dst[:len(dst)-1]
		}
		if x.Location().String() == "" {
			dst = x.AppendFormat(dst, " -07:00")
		}
		return dst
	}
	return appendValue(dst, v, false, "")
}

// isDecimalLiteral reports whether b is -?digits(.digits)?, the form the
// driver gives decimal and money digits in.
func isDecimalLiteral(b []byte) bool {
	if len(b) > 0 && b[0] == '-' {
		b = b[1:]
	}
	intPart, frac, hasDot := bytes.Cut(b, []byte{'.'})
	return allDigits(intPart) && (!hasDot || allDigits(frac))
}

func allDigits(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// appendHexUpper appends b as SSMS's "0x…" uppercase hex in one pass.
func appendHexUpper(dst []byte, b []byte) []byte {
	dst = append(dst, '0', 'x')
	for _, c := range b {
		dst = append(dst, hexUpperDigits[c>>4], hexUpperDigits[c&0x0f])
	}
	return dst
}

const hexUpperDigits = "0123456789ABCDEF"

// appendFloat renders float/real as SSMS's grid does: plain decimal in the
// readable range, scientific outside it with SSMS's upper-case "E+300" (Go's
// %g would show 1000000 as "1e+06"); the cut-offs match SSMS 21/22. Shortest
// round-trip precision, so pasted-back text reparses to the same float64
// (SSMS rounds to 15 significant digits; deliberately not copied).
func appendFloat(dst []byte, f float64, bits int) []byte {
	abs := math.Abs(f)
	if f != 0 && !math.IsInf(f, 0) && !math.IsNaN(f) && (abs < 1e-4 || abs >= 1e15) {
		return strconv.AppendFloat(dst, f, 'E', -1, bits)
	}
	return strconv.AppendFloat(dst, f, 'f', -1, bits)
}
