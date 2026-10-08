package gate

import (
	"strings"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
)

// allows.go is the rule itself: whether any one of a set of rights permits an
// action, and whether an explicit DENY withholds it. The rule fails open — see
// doc.go — and [ObjectDenial] is the one answer that can withhold rather than
// add.

// Allows reports whether an action needing any one of rights may still
// be offered on sc, for the database dbName (ignored by server-scope rights).
//
// The test is Allows, never Has: an action is withheld only when the server
// answered "no" to *every* right that would permit it. An unprobed
// connection, a failed probe, an uncached database and a permission this
// instance does not define all leave it offered. Gating on Has would empty
// a sysadmin's menus when the probe timed out.
//
// Database-scope rights are read from the cache only (see
// db.ServerConn.CachedDatabaseCapabilities): this runs on the UI goroutine
// during menu draw.
func Allows(sc *db.ServerConn, dbName string, rights ...Right) bool {
	return AllowsOn(sc, dbName, "", "", rights...)
}

// AllowsOn is Allows for an action aimed at one Object; schema is the schema
// it lives in, which a schema-scoped right is asked about. Empty means "not
// in a schema": such a right grants nothing, but the database-wide
// alternatives still answer, so nothing offered before is withheld.
func AllowsOn(sc *db.ServerConn, dbName, schema, object string, rights ...Right) bool {
	if sc == nil || len(rights) == 0 {
		return true
	}
	return RightsAllow(sc.Capabilities(), sc.CachedDatabaseCapabilities, dbName, schema, object, rights...)
}

// RightsAllow is the whole of the Allows rule, in one place: whether an
// action needing any one of rights may still be offered, given a server
// capability set and a way to reach a database's.
//
// dbCaps separates the callers. Menus pass CachedDatabaseCapabilities (UI
// goroutine, no query); a Properties page passes the probing form (its load
// is already on a background goroutine). The *rule* must not differ, hence
// one copy: pageReadOnlyReason's own copy understood neither the membership
// rights SQL Agent gates on nor schema/object-scoped ones, so a login with
// ALTER on one table saw a read-only banner for a page it could write.
func RightsAllow(server *gosmo.Capabilities, dbCaps func(string) *gosmo.DatabaseCapabilities, dbName, schema, object string, rights ...Right) bool {
	// A DENY on the object itself is the one answer in the gate that withholds
	// rather than adds, and is asked before the rights below: SQL Server
	// resolves it over every wider grant, so any of them would answer yes for a
	// write it then refuses. See ObjectDenial.
	if _, _, denied := ObjectDenial(server, dbCaps, dbName, schema, object, rights...); denied {
		return false
	}
	for _, r := range rights {
		switch {
		case r.Membership:
			// Unknown must allow explicitly: InRole cannot tell "not a member" from
			// "never asked", so an unprobed msdb would withhold every SQL Agent action
			// from a role holder. Probed separates the two.
			caps := dbCaps(r.InDB)
			if !caps.Probed() || caps.InRole(r.Name) {
				return true
			}
		case r.ServerRole:
			// Unknown must allow explicitly, as Membership: InServerRole cannot tell
			// "not a member" from "never asked". sysadmin is asked separately: it
			// implies no other fixed role, so it reads 0 for diskadmin while being
			// permitted everything diskadmin carries.
			if !server.Probed() || server.InServerRole(r.Name) || server.IsSysadmin() {
				return true
			}
		case r.Securable != "":
			// No schema guard: an assembly has none, and asks with "".
			if dbName == "" || object == "" {
				continue
			}
			// PermitsOnSecurable, not HasOnSecurable; the one arm that may answer yes
			// for a securable with no row: the map is not sparse, so a missing row is
			// one created since the probe, and unknown fails open. A 0 falls through.
			if dbCaps(dbName).PermitsOnSecurable(r.Securable, schema, object, r.Name) {
				return true
			}
		case r.Object:
			if dbName == "" || schema == "" || object == "" {
				continue
			}
			// Has, not Permits: the map is sparse, so "not denied" is true of every
			// object and would permit everything. No row leaves the wider rights to
			// answer.
			if dbCaps(dbName).HasOnObject(schema, object, r.Name) {
				return true
			}
		case r.Schema:
			if dbName == "" || schema == "" {
				continue
			}
			// PermitsOnSchema, not AllowsOnSchema: an inaccessible database answers
			// unknown for every schema, which fails open (gosmo.DatabaseCapabilities.Permits).
			if dbCaps(dbName).PermitsOnSchema(schema, r.Name) {
				return true
			}
		case !r.DB:
			// The name and its alternates are asked separately, not joined into one
			// slice: this runs per menu item and toolbar cell on every draw.
			if server.Allows(r.Name) {
				return true
			}
			// Has, not Allows: an alternate is a name SQL Server 2022 split out of
			// r.Name; 2016-2019 answer NULL (CapabilityUnknown), which Allows would read
			// as "not denied" and offer the action to logins the wide name refused. The
			// wide name above is what fails open for a probe that never ran.
			for _, n := range r.Alt {
				if server.Has(n) {
					return true
				}
			}
		case dbName == "":
			// No database to ask about (a folder-level action that prompts for one):
			// nothing measured, nothing withheld.
			return true
		default:
			// Permits, not Allows: an inaccessible database answers CapabilityUnknown to
			// everything and unknown fails open, which would offer Back Up and Delete on
			// databases the login cannot open (gosmo.DatabaseCapabilities.Permits).
			caps := dbCaps(dbName)
			if caps.Permits(r.Name) {
				return true
			}
			// alt is consulted at this scope too. No database-scope right declares one
			// today, only the test reaches it, but ignoring alternates here would
			// withhold the action from a holder with nothing at run time to tell it
			// from a real denial; the next 2022-style split may land at database scope.
			for _, n := range r.Alt {
				if caps.Permits(n) {
					return true
				}
			}
		}
	}
	return false
}

// Site names the securable a DENY was found on, for the sentence the
// user is shown. At most one field is set; all empty means the object itself.
type Site struct {
	Column    string // a column of the object
	Schema    string // the object's schema
	Database  string // the database the object lives in
	Principal string // the database user the action is aimed at

	// ServerSecurable is the login, server role or endpoint the action is aimed
	// at, and ServerKind which of the three; the sentence must name it ("denied
	// on login x" differs from "on endpoint x").
	ServerSecurable string
	ServerKind      gosmo.ServerSecurableKind

	// AvailabilityGroup is the group the action is aimed at, apart from
	// ServerSecurable because it is a different gosmo map (Right.DeniedOnAG).
	AvailabilityGroup string
}

// ObjectDenial reports the right whose DENY withholds an action, the
// securable that DENY sits on where not the object itself, and whether there
// is one. The only part of the gate that withholds on an object-scope
// answer; sound because it asks for a state the probe recorded, so silence
// stays silence.
//
// Three facts it rests on, each verified live on 2026-09-01 and each a wrong
// gate if assumed the other way:
//
//   - An object-scope DENY beats every wider grant. A principal holding
//     database-wide ALTER, or db_owner, reads HAS_PERMS_BY_NAME 0 on a table
//     denied ALTER, and its rename fails Msg 297 — which is what the login saw
//     instead of a greyed-out item before this check existed.
//   - A member of sysadmin bypasses the check, and must be asked about first.
//     The probe's principal set includes public, so a DENY made to public is
//     recorded for everyone including a sysadmin, whose write SQL Server then
//     allows anyway.
//   - The object's owner needs no exception. SQL Server refuses a DENY aimed
//     at the owner of the securable, and ALTER AUTHORIZATION deletes an
//     existing DENY row as it transfers ownership, so an owner never carries
//     one. An owner denied through public *is* refused by the server.
//
// A database never probed records nothing, which reads as no denial
// (unknown fails open).
//
// A DENY on one *column* withholds just as hard and is asked second: SQL
// Server resolves it over every wider grant, so a whole-table statement
// fails for a table-level holder. Nothing gossms writes is column-scoped,
// so a column denial denies the action outright.
//
// A DENY on the object's *schema* is asked third, for the same reason: with
// ALTER on the database and DENY ALTER on dbo, every rename and drop met
// Msg 297. It asks gosmo's DeniedOnSchema, not PermitsOnSchema:
// HAS_PERMS_BY_NAME answers 0 for a schema permission simply never granted
// (the ordinary case), and withholding on that would empty the menus of
// every login using a database-wide grant.
//
// A DENY at *database* scope is asked about last, and is the one arm that
// exists for a grant *narrower* than itself rather than wider. The r.Object
// and r.Schema arms of RightsAllow answer yes on HasOnObject and
// PermitsOnSchema, neither of which can see a class-0 row, so a principal
// granted ALTER on one table and denied ALTER on the database was offered
// every write on that table. Verified live 2026-09-04: with GRANT ALTER ON
// OBJECT::dbo.t1 and DENY ALTER at database scope, the server answers
// HAS_PERMS_BY_NAME('dbo.t1','OBJECT','ALTER') = 0 and refuses the ALTER —
// the wider DENY beats the narrower GRANT, which is the opposite of the
// column-level exception and the reason this arm cannot be folded into the
// loop. It is asked only of rights declared db-scoped, the way the arms above
// ask only of the scope they can answer for.
//
// It goes through gosmo's DeniedOnDatabase, never Permission/Permits: those
// answer HAS_PERMS_BY_NAME, whose 0 means "does not hold" and is the *ordinary*
// reading for a principal working through a narrower grant, so withholding on
// it takes the write away from exactly the principal it was granted to. Only
// the catalog can say a DENY row exists — the same distinction
// ExplicitSchemaPermissions exists for, one scope wider.
func ObjectDenial(server *gosmo.Capabilities, dbCaps func(string) *gosmo.DatabaseCapabilities, dbName, schema, object string, rights ...Right) (Right, Site, bool) {
	if server.InServerRole("sysadmin") {
		return Right{}, Site{}, false
	}
	// The server-scope arm precedes the dbName guard: a login, server role or
	// endpoint carries an empty DBName, which every arm below would answer
	// nothing about. Asked only of a right declaring DeniedOnServer, so another
	// family sharing a denied login's name is not withheld (as DeniedOnPrincipal
	// for the class-4 arm).
	if object != "" {
		for _, r := range rights {
			if r.DeniedOnServer == "" {
				continue
			}
			if server.DeniedOnServerSecurable(r.ServerSecurable, object, r.DeniedOnServer) {
				return r, Site{ServerSecurable: object, ServerKind: r.ServerSecurable}, true
			}
		}
	}
	// The availability-group arm sits above the dbName guard likewise: a group,
	// replica and listener carry an empty DBName.
	//
	// server.Has, not Allows: the group answer is a HAS_PERMS_BY_NAME 0, which
	// means "denied on this group" only while the server-wide right is held.
	// Otherwise every login lacking ALTER ANY AVAILABILITY GROUP would be told
	// the group is denied instead of which right to ask for.
	if object != "" {
		for _, r := range rights {
			if r.DeniedOnAG == "" || !server.Has(r.Name) {
				continue
			}
			if !server.PermitsOnAvailabilityGroup(object, r.DeniedOnAG) {
				return r, Site{AvailabilityGroup: object}, true
			}
		}
	}
	if dbName == "" {
		return Right{}, Site{}, false
	}
	var caps *gosmo.DatabaseCapabilities
	asked := false
	ask := func() *gosmo.DatabaseCapabilities {
		if !asked {
			caps, asked = dbCaps(dbName), true
		}
		return caps
	}
	// The class-4 arm precedes the schema guard: a user node has no Schema, only
	// its own name as the object.
	if object != "" {
		for _, r := range rights {
			if r.DeniedOnPrincipal == "" {
				continue
			}
			if ask().DeniedOnPrincipal(object, r.DeniedOnPrincipal) {
				return r, Site{Principal: object}, true
			}
		}
	}
	// Everything below is schema-scoped and answers nothing without one. The
	// guard stays here so a schema-less node reaches only the arm above; the
	// wider arms were never asked for one and extending them needs its own live
	// answer.
	if schema == "" {
		return Right{}, Site{}, false
	}
	if object != "" {
		for _, r := range rights {
			if !r.Object {
				continue
			}
			if ask().DeniedOnObject(schema, object, r.Name) {
				return r, Site{}, true
			}
			if col, denied := ask().DeniedOnAnyColumn(schema, object, r.Name); denied {
				return r, Site{Column: col}, true
			}
		}
	}
	for _, r := range rights {
		if !r.Schema {
			continue
		}
		if ask().DeniedOnSchema(schema, r.Name) {
			return r, Site{Schema: schema}, true
		}
	}
	for _, r := range rights {
		if !r.DB {
			continue
		}
		if ask().DeniedOnDatabase(r.Name) {
			return r, Site{Database: dbName}, true
		}
	}
	return Right{}, Site{}, false
}

// DeniedOn is ObjectDenial for a live connection — the shape the menu
// gates ask it in, with the cached capabilities the UI goroutine may use.
func DeniedOn(sc *db.ServerConn, dbName, schema, object string, rights ...Right) (Right, Site, bool) {
	if sc == nil {
		return Right{}, Site{}, false
	}
	return ObjectDenial(sc.Capabilities(), sc.CachedDatabaseCapabilities, dbName, schema, object, rights...)
}

// serverSecurableWord renders a server securable kind for the sentence.
// gosmo spells kinds as DENY does ("SERVER ROLE"); shouting them mid-sentence
// reads as a keyword.
func serverSecurableWord(k gosmo.ServerSecurableKind) string {
	return strings.ToLower(string(k))
}

// DeniedText is the sentence for an action withheld by a DENY on the object
// rather than a missing right (RequiresText's counterpart). It names no role
// and asks for nothing: the login may hold every right already and the DENY
// overrides all. Only the object's own permission can change.
func DeniedText(r Right, at Site) string {
	right, where := deniedPhrase(r, at)
	if at.Column != "" {
		where += " of this object"
	}
	return right + " is denied on " + where + "."
}

// deniedPhrase is the two halves every DENY wording shares — the right the
// DENY is on and the securable it sits on — for DeniedText's sentence and a
// withheld menu item's note.
func deniedPhrase(r Right, at Site) (right, where string) {
	switch {
	case at.Column != "":
		return r.Name, "column " + at.Column
	case at.Schema != "":
		return r.Name, "schema " + at.Schema
	case at.Database != "":
		return r.Name, "database " + at.Database
	case at.Principal != "":
		// r.DeniedOnPrincipal, not r.Name: the right is the database-wide ALTER ANY
		// USER but the DENY that beats it is plain ALTER on the principal.
		//
		// "principal", not "user": class 4 covers database roles too and the gate,
		// given only a name, cannot tell them apart.
		return r.DeniedOnPrincipal, "principal " + at.Principal
	case at.ServerSecurable != "":
		// r.DeniedOnServer, not r.Name, as at.Principal: the DENY that beats the
		// server-wide ALTER ANY LOGIN is plain ALTER on the securable.
		//
		// The kind *is* named here, since the right declared its securable and
		// "denied on x" would not tell a login from an endpoint.
		return r.DeniedOnServer, serverSecurableWord(at.ServerKind) + " " + at.ServerSecurable
	case at.AvailabilityGroup != "":
		return r.DeniedOnAG, "availability group " + at.AvailabilityGroup
	}
	return r.Name, "this object"
}
