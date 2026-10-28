package cmd

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/sqltext"
)

// optParam is query run's --param name=value:type.
const optParam = "param"

// param is one --param, checked: a declaration sp_executesql accepts and a
// value expression that cannot leave its literal.
type param struct {
	name  string // with its leading @
	typ   string // lower case, e.g. "decimal(9,2)"
	value string // T-SQL literal: N'…', or 0x… for a binary type
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
		p.value = value
	} else {
		// The server converts the string to the declared type, as it would
		// a client's string parameter; a bad value fails there (Msg 8114).
		p.value = gosmo.QuoteLiteral(value)
	}
	return p, nil
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
		return (len(parts) == 1 || len(parts) == 2) && !slices.ContainsFunc(parts, func(s string) bool { return !number(s) })
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
