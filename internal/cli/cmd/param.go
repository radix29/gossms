package cmd

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/sqltext"
)

// optParam is query run's --param name=value:type.
const optParam = "param"

// param is one --param, checked: a declaration sp_executesql accepts and a
// value expression that cannot leave its literal.
type param struct {
	name  string // with its leading @
	typ   string // lower case, e.g. "decimal(9,2)"
	value string // T-SQL literal: N'…', or 0x… for a binary type

	// text, base and length are a char/varchar value as given, its type
	// name and its (n) (-1 for max), kept for checkCodePage: the server
	// converts the N'…' literal in the session database's code page, known
	// only once connected. Unset for every other type.
	text   string
	base   string
	length int
}

// paramName is a regular T-SQL variable name without its @. Anything else —
// a quoted name, @@x — is refused rather than escaped: the name goes into the
// declaration list, which sp_executesql parses as T-SQL.
var paramName = regexp.MustCompile(`^[\p{L}_#][\p{L}\p{N}_@#$]*$`)

// paramTypes is every type --param declares, with the arguments it takes.
// The list is closed for the same reason as paramName: the type is T-SQL in
// the declaration, so it is matched, never passed through.
var paramTypes = map[string]paramArgs{
	"bigint": argsNone, "int": argsNone, "smallint": argsNone, "tinyint": argsNone, "bit": argsNone,
	"money": argsNone, "smallmoney": argsNone, "real": argsNone,
	"date": argsNone, "datetime": argsNone, "smalldatetime": argsNone,
	"uniqueidentifier": argsNone, "xml": argsNone, "sql_variant": argsNone, "sysname": argsNone,
	"decimal": argsPrecision, "numeric": argsPrecision,
	"float": argsScale, "time": argsScale, "datetime2": argsScale, "datetimeoffset": argsScale,
	"char": argsLength, "varchar": argsLength, "nchar": argsLength, "nvarchar": argsLength,
	"binary": argsLength, "varbinary": argsLength,
}

// paramArgs is what may follow a type name in parentheses.
type paramArgs int

const (
	argsNone      paramArgs = iota
	argsScale               // (n)
	argsPrecision           // (p) or (p,s)
	argsLength              // (n) or (max)
)

var paramType = regexp.MustCompile(`^([a-z_0-9]+)(?:\(([0-9a-z, ]*)\))?$`)

// parseParam reads one --param value: the name up to the first '=', the type
// after the last ':' (a value may hold both characters; a type holds
// neither).
func parseParam(raw string) (param, error) {
	bad := func(msg string) (param, error) {
		return param{}, usageError(fmt.Sprintf("--param %q: %s", raw, msg),
			"Give --param name=value:type, e.g. --param id=42:int or --param name=O'Brien:nvarchar(50).")
	}
	name, rest, ok := strings.Cut(raw, "=")
	if !ok {
		return bad("no '='")
	}
	colon := strings.LastIndexByte(rest, ':')
	if colon < 0 {
		return bad("no ':type' after the value")
	}
	value, typ := rest[:colon], strings.ToLower(strings.TrimSpace(rest[colon+1:]))
	name = strings.TrimPrefix(strings.TrimSpace(name), "@")
	if !paramName.MatchString(name) {
		return bad(fmt.Sprintf("%q is not a variable name (letters, digits, _, # and $; not @@)", name))
	}
	m := paramType.FindStringSubmatch(typ)
	if m == nil {
		return bad(fmt.Sprintf("%q is not a type", typ))
	}
	kind, known := paramTypes[m[1]]
	if !known {
		return bad(fmt.Sprintf("%q is not a type --param declares", m[1]))
	}
	// A length-less string or binary type is length 1 in a declaration, so
	// sp_executesql would receive the value's first character.
	if kind == argsLength && !strings.Contains(typ, "(") {
		return bad(fmt.Sprintf("%s needs a length: %s(n) or %s(max)", m[1], m[1], m[1]))
	}
	if !typeArgsValid(kind, m[2], strings.Contains(typ, "(")) {
		return bad(fmt.Sprintf("%q: wrong arguments for %s", typ, m[1]))
	}
	typ = m[1]
	if strings.Contains(m[0], "(") {
		typ += "(" + strings.ReplaceAll(m[2], " ", "") + ")"
	}
	p := param{name: "@" + name, typ: typ}
	if m[1] == "binary" || m[1] == "varbinary" {
		// nvarchar does not convert to binary implicitly, so the value is a
		// binary literal itself, checked to be nothing else.
		if !isHexLiteral(value) {
			return bad("a binary value is written 0x followed by hex digits")
		}
		if n, ok := paramLength(m[2]); ok && (len(value)-2+1)/2 > n {
			return bad(fmt.Sprintf("%d bytes do not fit %s", (len(value)-2+1)/2, typ))
		}
		p.value = value
	} else {
		// An empty string converts to 0, 1900-01-01 and the like, so a
		// shell variable that expanded to nothing would run with a value
		// nobody wrote. Only a string type can mean "empty".
		if strings.TrimSpace(value) == "" && !paramStringTypes[m[1]] {
			return param{}, usageError(fmt.Sprintf("--param %q: an empty value is not a%s %s", raw, article(m[1]), m[1]),
				"Give the value, or use a string type for an empty string.")
		}
		// sp_executesql converts to the declared type without Msg 8152, so a
		// longer string would be cut and match other rows. char/varchar count
		// runes, exact for ASCII and a floor otherwise — code-page bytes
		// depend on the collation, which checkCodePage measures once
		// connected; nchar/nvarchar count UTF-16 code units.
		limit, ok := paramLength(m[2])
		if m[1] == "sysname" {
			limit, ok = 128, true
		}
		if ok && paramLengthTypes[m[1]] {
			n := utf8.RuneCountInString(value)
			if m[1] == "nchar" || m[1] == "nvarchar" || m[1] == "sysname" {
				n = len(utf16.Encode([]rune(value)))
			}
			if n > limit {
				return param{}, usageError(fmt.Sprintf("--param %s: %d characters do not fit %s", p.name, n, typ),
					"Shorten the value or declare a longer type.")
			}
		}
		if m[1] == "char" || m[1] == "varchar" {
			p.text, p.base, p.length = value, m[1], -1
			if ok {
				p.length = limit
			}
		}
		if kind == argsPrecision {
			if err := decimalFits(p.name, value, m[1], m[2]); err != nil {
				return param{}, err
			}
		}
		// The server's string-to-money conversion drops commas anywhere and
		// takes a currency sign, so 12,50 would run as 1250.00 with no
		// error. gosmo's money check (the one data import uses) refuses
		// anything but [+-]digits[.digits], over four decimals, and out of
		// range.
		if m[1] == "money" || m[1] == "smallmoney" {
			if err := moneyFits(p.name, value, m[1]); err != nil {
				return param{}, err
			}
		}
		// A date or bit is checked and rewritten in a form the server reads
		// one way: its own conversion would succeed with a different value.
		switch {
		case paramDateTypes[m[1]]:
			v, err := dateLiteral(p.name, value, m[1])
			if err != nil {
				return param{}, err
			}
			value = v
		case m[1] == "bit":
			v, err := bitLiteral(p.name, value)
			if err != nil {
				return param{}, err
			}
			value = v
		}
		// The server converts the string to the declared type, as it would
		// a client's string parameter; a bad value fails there (Msg 8114).
		p.value = gosmo.QuoteLiteral(value)
	}
	return p, nil
}

// needsCodePage reports whether p is a char/varchar value checkCodePage must
// check on the server. ASCII needs none: it is one byte, and the same byte,
// in every code page char data can have, so parseParam's character count is
// exact.
func (p param) needsCodePage() bool {
	if p.base == "" {
		return false
	}
	for i := 0; i < len(p.text); i++ {
		if p.text[i] >= utf8.RuneSelf {
			return true
		}
	}
	return false
}

// checkCodePage refuses a char/varchar parameter whose value the server's
// conversion would change: sp_executesql converts the N'…' literal in the
// session database's default collation, turning a character its code page
// lacks into '?' and cutting a value over (n) bytes, with no error. srv is
// on the session's database; nothing has been sent when it refuses. Reads
// nothing when no value needs it (needsCodePage).
func checkCodePage(ctx context.Context, srv *gosmo.Server, ps []param) error {
	if !slices.ContainsFunc(ps, param.needsCodePage) {
		return nil
	}
	name, err := srv.CurrentDatabase(ctx)
	if err != nil {
		return readError(err)
	}
	dbase, err := srv.DatabaseByName(ctx, name)
	if err != nil {
		return readError(err)
	}
	cp, err := srv.CollationCodePage(ctx, dbase.Collation)
	if err != nil {
		return readError(err)
	}
	for _, p := range ps {
		if p.needsCodePage() {
			if err := p.fitsCodePage(cp, dbase.Collation); err != nil {
				return err
			}
		}
	}
	return nil
}

// fitsCodePage refuses p's value when code page cp, collation's, lacks one
// of its characters or holds it in more than (n) bytes. The encoding is
// gosmo's BulkColumn check, the one data import uses.
func (p param) fitsCodePage(cp int, collation string) error {
	hint := fmt.Sprintf("Declare n%s(n), or use characters the database's code page (CP%d) has.", p.base, cp)
	// Length -1: the bytes are counted below, in the message's terms rather
	// than a bulk column's.
	conv, err := gosmo.BulkColumn{Type: "varchar", Length: -1, CodePage: cp, DatabaseCodePage: cp}.Converter()
	if err != nil {
		// A Unicode-only collation (code page 0) or one gosmo has no encoder
		// for: the value cannot be checked, so it is not sent.
		return usageError(fmt.Sprintf("--param %s: %s cannot be checked in the database's collation %s (code page %d)",
			p.name, p.typ, collation, cp), hint)
	}
	v, err := conv(p.text)
	if err != nil {
		reason := err.Error()
		if e, ok := errors.AsType[*gosmo.BulkValueError](err); ok {
			// gosmo words it for a column; here the code page is the
			// session database's.
			reason = strings.Replace(e.Reason, "the column's", "the database's", 1)
		}
		return usageError(fmt.Sprintf("--param %s: %s", p.name, reason), hint)
	}
	n := 0
	switch v := v.(type) {
	case []byte:
		n = len(v)
	case string: // a UTF-8 code page: the text is the bytes
		n = len(v)
	}
	if p.length >= 0 && n > p.length {
		return usageError(fmt.Sprintf("--param %s: %d bytes in code page %d do not fit %s", p.name, n, cp, p.typ),
			"Shorten the value, declare a longer type, or declare n"+p.base+"(n).")
	}
	return nil
}

// paramDateTypes take an ISO 8601 value only.
var paramDateTypes = map[string]bool{
	"date": true, "datetime": true, "smalldatetime": true, "datetime2": true, "datetimeoffset": true,
}

// paramDate is the ISO 8601 form --param accepts for a date type: the date,
// then optionally a time (T or a space before it; seconds and a fraction
// optional), then optionally a zone.
var paramDate = regexp.MustCompile(`^([0-9]{4})-([0-9]{2})-([0-9]{2})` +
	`(?:[T ]([0-9]{2}):([0-9]{2})(?::([0-9]{2})(?:\.([0-9]{1,7}))?)?)?` +
	`(Z|[+-][0-9]{2}:[0-9]{2})?$`)

// dateLiteral checks a date-type value and rewrites it in a form every
// DATEFORMAT and LANGUAGE reads the same: YYYYMMDD for a date alone,
// YYYY-MM-DDThh:mm:ss[.f…][zone] with a time. The server reads YYYY-MM-DD as
// year-day-month for datetime and smalldatetime under a dmy language
// (British, German, French…), and any other form (01/02/2026) by DATEFORMAT
// for every type — a different date, with no error. What does not fit the
// type (a year before 1753 for datetime, more decimals than it keeps) is
// left to the server to refuse or round, as for time.
func dateLiteral(name, value, base string) (string, error) {
	v := strings.TrimSpace(value)
	form := "YYYY-MM-DD[Thh:mm[:ss[.fffffff]]]"
	switch base {
	case "date":
		form = "YYYY-MM-DD"
	case "datetimeoffset":
		form += "[Z|±hh:mm]"
	}
	bad := func(msg string) (string, error) {
		return "", usageError(fmt.Sprintf("--param %s: %q %s", name, v, msg),
			"Write the date as "+form+": another form is read by the login's language, and can name another day.")
	}
	m := paramDate.FindStringSubmatch(v)
	if m == nil {
		return bad("is not an ISO 8601 date")
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	d, _ := strconv.Atoi(m[3])
	if y < 1 || mo < 1 || mo > 12 || d < 1 || d > daysIn(y, mo) {
		return bad("is not a date")
	}
	hasTime, zone := m[4] != "", m[8]
	switch {
	case hasTime && base == "date":
		return bad("has a time; date keeps none")
	case zone != "" && base != "datetimeoffset":
		return bad(fmt.Sprintf("has a time zone; %s keeps none", base))
	case zone != "" && !hasTime:
		return bad("has a time zone but no time")
	}
	if !hasTime {
		return m[1] + m[2] + m[3], nil
	}
	h, _ := strconv.Atoi(m[4])
	mi, _ := strconv.Atoi(m[5])
	s := 0
	if m[6] != "" {
		s, _ = strconv.Atoi(m[6])
	}
	if h > 23 || mi > 59 || s > 59 {
		return bad("is not a time of day")
	}
	if len(zone) == 6 {
		zh, _ := strconv.Atoi(zone[1:3])
		zm, _ := strconv.Atoi(zone[4:6])
		if zh > 14 || zm > 59 || (zh == 14 && zm > 0) {
			return bad("has a zone outside -14:00 to +14:00")
		}
	}
	out := fmt.Sprintf("%s-%s-%sT%s:%s:%02d", m[1], m[2], m[3], m[4], m[5], s)
	if m[7] != "" {
		out += "." + m[7]
	}
	return out + zone, nil
}

// daysIn is the number of days in month mo of year y (proleptic Gregorian,
// as SQL Server's date types count them).
func daysIn(y, mo int) int {
	switch mo {
	case 2:
		if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	}
	return 31
}

// bitLiteral accepts 0, 1, true and false (any case): the server turns any
// other number into 1 silently.
func bitLiteral(name, value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false":
		return "0", nil
	case "1", "true":
		return "1", nil
	}
	return "", usageError(fmt.Sprintf("--param %s: %q is not a bit", name, strings.TrimSpace(value)),
		"Give 0, 1, true or false: the server would turn any other number into 1.")
}

// paramDecimal is a value the server converts to decimal: a sign, digits
// and a point. Anything else (1,5 or 1e3) fails there with Msg 8114, so it
// is not checked here.
var paramDecimal = regexp.MustCompile(`^[+-]?([0-9]*)(?:\.([0-9]*))?$`)

// decimalFits refuses a decimal/numeric value the declared precision and
// scale would change: the server rounds extra decimals silently. A bare type
// is (18,0), (p) is (p,0). Leading zeros and trailing fractional zeros do
// not count, since dropping them changes nothing.
func decimalFits(name, value, base, args string) error {
	prec, scale := 18, 0
	if args != "" {
		parts := strings.Split(strings.ReplaceAll(args, " ", ""), ",")
		prec, _ = strconv.Atoi(parts[0])
		if len(parts) == 2 {
			scale, _ = strconv.Atoi(parts[1])
		}
	}
	m := paramDecimal.FindStringSubmatch(strings.TrimSpace(value))
	if m == nil || m[1]+m[2] == "" {
		return nil
	}
	whole, frac := len(strings.TrimLeft(m[1], "0")), len(strings.TrimRight(m[2], "0"))
	typ := fmt.Sprintf("%s(%d,%d)", base, prec, scale)
	places := "places"
	if frac == 1 {
		places = "place"
	}
	switch {
	case frac > scale:
		return usageError(fmt.Sprintf("--param %s: %s has %d decimal %s; %s keeps %d", name, strings.TrimSpace(value), frac, places, typ, scale),
			"Declare a scale that holds the value: the server would round it.")
	case whole > prec-scale:
		return usageError(fmt.Sprintf("--param %s: %s has %d digits before the point; %s keeps %d", name, strings.TrimSpace(value), whole, typ, prec-scale),
			"Declare a larger precision.")
	}
	return nil
}

// moneyFits refuses a money/smallmoney value the server would read as
// another amount or round, with gosmo's BulkColumn check for the type.
func moneyFits(name, value, base string) error {
	conv, err := gosmo.BulkColumn{Type: base}.Converter()
	if err == nil {
		_, err = conv(strings.TrimSpace(value))
	}
	if err != nil {
		return usageError(fmt.Sprintf("--param %s: %v", name, err),
			"Write the amount with a point and no grouping or currency sign: 1250.00 or 12.50.")
	}
	return nil
}

// paramStringTypes may hold an empty value.
var paramStringTypes = map[string]bool{
	"char": true, "varchar": true, "nchar": true, "nvarchar": true,
	"sysname": true, "xml": true, "sql_variant": true,
}

// paramLengthTypes have a length the value is checked against.
var paramLengthTypes = map[string]bool{"char": true, "varchar": true, "nchar": true, "nvarchar": true, "sysname": true}

// paramLength is the n of a type's "(n)"; false for no argument or (max).
func paramLength(args string) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(args))
	return n, err == nil
}

func article(typ string) string {
	// "a uniqueidentifier": u reads as "you" in every type name that has it.
	if strings.ContainsRune("aeio", rune(typ[0])) {
		return "n"
	}
	return ""
}

func typeArgsValid(kind paramArgs, args string, given bool) bool {
	if !given {
		return true
	}
	parts := strings.Split(strings.ReplaceAll(args, " ", ""), ",")
	number := func(s string) bool {
		return s != "" && len(s) <= 4 && strings.Trim(s, "0123456789") == ""
	}
	switch kind {
	case argsScale:
		return len(parts) == 1 && number(parts[0])
	case argsPrecision:
		// The precision is 1 to 38 and the scale at most the precision.
		if len(parts) > 2 || slices.ContainsFunc(parts, func(s string) bool { return !number(s) }) {
			return false
		}
		prec, _ := strconv.Atoi(parts[0])
		scale := 0
		if len(parts) == 2 {
			scale, _ = strconv.Atoi(parts[1])
		}
		return prec >= 1 && prec <= 38 && scale <= prec
	case argsLength:
		return len(parts) == 1 && (number(parts[0]) || parts[0] == "max")
	}
	return false
}

func isHexLiteral(s string) bool {
	if len(s) < 2 || (s[:2] != "0x" && s[:2] != "0X") {
		return false
	}
	return strings.Trim(s[2:], "0123456789abcdefABCDEF") == ""
}

// readParams is query run's --param values, checked; nil when none was given.
func readParams(values []string) ([]param, error) {
	var ps []param
	for _, raw := range values {
		p, err := parseParam(raw)
		if err != nil {
			return nil, err
		}
		if slices.ContainsFunc(ps, func(q param) bool { return strings.EqualFold(q.name, p.name) }) {
			return nil, usageError("--param "+p.name+" given more than once", "Give each parameter once.")
		}
		ps = append(ps, p)
	}
	return ps, nil
}

// parameterize wraps script in sp_executesql with ps as its parameters (D13):
//
//	EXEC sp_executesql N'<script>', N'@id int, …', @id = N'42', …;
//
// The script must be one batch, sent once — sp_executesql runs one batch, and
// GO is not T-SQL. The script was classified as written, not as the
// sp_executesql the classifier would fail closed to write. Its first line is
// moved to its line in the script, so an error inside it, which the server
// numbers from the dynamic text's first line, names the script's line.
func parameterize(script string, ps []param) (string, error) {
	batches := sqltext.SplitBatches(script)
	if len(batches) != 1 || batches[0].Count != 1 {
		return "", usageError("--param needs a script of one batch, run once",
			"Remove the GO separators: sp_executesql runs a single batch.")
	}
	b := batches[0]
	decls := make([]string, len(ps))
	assigns := make([]string, len(ps))
	for i, p := range ps {
		decls[i] = p.name + " " + p.typ
		assigns[i] = p.name + " = " + p.value
	}
	inner := strings.Repeat("\n", b.StartLine) + b.Text
	return "EXEC sp_executesql " + gosmo.QuoteLiteral(inner) + ", " + gosmo.QuoteLiteral(strings.Join(decls, ", ")) +
		", " + strings.Join(assigns, ", ") + ";", nil
}
