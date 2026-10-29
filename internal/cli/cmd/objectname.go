package cmd

import (
	"fmt"
	"strings"

	gosmo "github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/tuikit/sqltext"
)

// ObjectName is an object argument split into its parts, each unquoted:
// Sales.[Order.Detail] is schema "Sales", name "Order.Detail". Schema is ""
// for a one-part name, which resolveObject looks up across schemas.
type ObjectName struct {
	Schema string
	Name   string
}

// String is the name as T-SQL would write it, each part bracketed.
func (n ObjectName) String() string {
	if n.Schema == "" {
		return gosmo.QuoteName(n.Name)
	}
	return gosmo.QualifiedName(n.Schema, n.Name)
}

// parseObjectName splits s, an object argument, into its parts with the T-SQL
// lexer, so a [bracketed] or "quoted" part keeps its dots and doubled closing
// delimiters: one part (name), two (schema.name), or three
// (database.schema.name) when the database is the connection's own, database
// — another one is refused naming -d, since the commands look only in the
// connection's database (D1). A three-part name's database is compared
// exactly: a different spelling may be another database. An empty part
// (db..name) or anything but parts and dots — a comment, a literal, a
// four-part name — is a usage error.
func parseObjectName(s, database string) (ObjectName, error) {
	bad := func(why string) error {
		return usageError(fmt.Sprintf("object name %q: %s", s, why),
			"Write schema.name or name; bracket a part holding a dot or space: Sales.[Order Detail].")
	}
	buf := []rune(s)
	var parts []string
	wantPart := true
	for i, st := 0, (sqltext.State{}); ; {
		var t sqltext.Token
		t, st = sqltext.Next(buf, i, len(buf), st)
		i = t.End
		text := string(buf[t.Start:t.End])
		switch t.Kind {
		case sqltext.KindEnd:
			if st.Mode != sqltext.ModeNormal {
				return ObjectName{}, bad("an unclosed quoted part")
			}
			switch {
			case len(parts) == 0:
				return ObjectName{}, bad("it is empty")
			case wantPart:
				return ObjectName{}, bad("it ends in a dot")
			}
			return objectName(s, parts, database)
		case sqltext.KindWord, sqltext.KindQuotedIdent:
			if !wantPart {
				return ObjectName{}, bad("parts must be separated by dots")
			}
			if t.Kind == sqltext.KindQuotedIdent {
				text = gosmo.UnquoteName(text)
			}
			if text == "" {
				return ObjectName{}, bad("a part is empty")
			}
			parts, wantPart = append(parts, text), false
		case sqltext.KindPunct:
			if text != "." {
				return ObjectName{}, bad(fmt.Sprintf("unexpected %q", text))
			}
			if wantPart {
				return ObjectName{}, bad("a part is empty")
			}
			wantPart = true
		default:
			// A number (1x, 1.5) runs on through dots; a comment, string or
			// line break is no part of a name.
			return ObjectName{}, bad(fmt.Sprintf("unexpected %q", strings.TrimSpace(text)))
		}
	}
}

func objectName(s string, parts []string, database string) (ObjectName, error) {
	switch len(parts) {
	case 1:
		return ObjectName{Name: parts[0]}, nil
	case 2:
		return ObjectName{Schema: parts[0], Name: parts[1]}, nil
	case 3:
		if parts[0] != database {
			return ObjectName{}, usageError(
				fmt.Sprintf("object name %q names database %s; this command looks in %s", s,
					gosmo.QuoteName(parts[0]), gosmo.QuoteName(database)),
				fmt.Sprintf("Pass -d %s and name the object as schema.name.", gosmo.QuoteName(parts[0])))
		}
		return ObjectName{Schema: parts[1], Name: parts[2]}, nil
	}
	return ObjectName{}, usageError(fmt.Sprintf("object name %q has %d parts", s, len(parts)),
		"Write schema.name or name; a linked server's objects are out of reach here.")
}
