package gate

import (
	"slices"
	"strings"

	"github.com/radix29/gosmo"
)

// text.go words a right for the user: the name alone, with its role, and the
// "Requires …" sentence a disabled menu item carries. Denial wording is in
// allows.go.

// nameOnly is the permission as the user is told to ask for it, without the
// role (RequiresText gathers roles into one trailing clause).
//
// A schema-scoped right is named for the securable with no role:
// "ALTER (db_ddladmin)" would send the user after a role granting far more
// than what is missing.
func (r Right) nameOnly() string {
	switch {
	case r.Securable != "":
		return r.Name + " on the " + databaseSecurableWord(r.Securable)
	case r.Schema:
		return r.Name + " on the object's schema"
	case r.Membership:
		return "membership of " + r.Name + " in " + r.InDB
	case r.ServerRole:
		return "membership of the " + r.Name + " server role"
	case r.Object:
		return r.Name + " on the object itself"
	}
	return r.Name
}

// databaseSecurableWord renders a class 5/6/10 kind as the sentence says it —
// serverSecurableWord's database-scope twin, keeping "XML" in capitals.
func databaseSecurableWord(k gosmo.DatabaseSecurableKind) string {
	if k == gosmo.DatabaseSecurableXMLSchemaCollection {
		return "XML schema collection"
	}
	return strings.ToLower(string(k))
}

// String renders one right on its own: the permission with the role that also
// carries it, as permission_error.go names it. RequiresText does not use it.
func (r Right) String() string {
	if r.Role == "" {
		return r.nameOnly()
	}
	return r.Name + " (" + r.Role + ")"
}

// orList joins names the way the sentence reads them: "A", "A or B",
// "A, B or C".
func orList(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// RequiresText is the sentence shown when an action is withheld. Any one of
// the rights suffices, hence "or".
//
// Roles are gathered into one trailing clause because the alternatives for an
// action mostly share a Role: spelled out per right it read "ALTER (db_owner)
// or CONTROL (db_owner) or ALTER ANY DATABASE (dbcreator)", naming db_owner
// twice and looking like six things to ask for. A right with no role stays
// outside the clause (see Right.String).
func RequiresText(rights ...Right) string {
	var named, plain, roles []string
	for _, r := range rights {
		if r.Role == "" {
			plain = append(plain, r.nameOnly())
			continue
		}
		named = append(named, r.Name)
		if !slices.Contains(roles, r.Role) {
			roles = append(roles, r.Role)
		}
	}
	out := orList(named)
	if out != "" {
		out += " (" + strings.Join(roles, ", ") + ")"
	}
	if rest := orList(plain); rest != "" {
		if out != "" {
			out += " or "
		}
		out += rest
	}
	return "Requires " + out + "."
}
