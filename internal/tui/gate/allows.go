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
// answered "no" to *every* right that would permit it. An unprobed connection,
// a probe that failed, a database whose answer is not cached yet, and a
// permission this instance does not define all leave the action offered.
// Gating on Has instead would empty the menus of a sysadmin whose probe timed
// out.
//
// Database-scope rights are read from the cache only — see
// db.ServerConn.CachedDatabaseCapabilities. This runs on the UI goroutine
// while a menu is being drawn.
func Allows(sc *db.ServerConn, dbName string, rights ...Right) bool {
	return AllowsOn(sc, dbName, "", "", rights...)
}

// AllowsOn is Allows for an action aimed at one Object: schema is
// the schema that object lives in, which is what a schema-scoped right is
// asked about. Empty means "not an object in a schema", and a schema-scoped
// right then grants nothing — the database-wide alternatives beside it still
// answer, so nothing is withheld that was offered before.
func AllowsOn(sc *db.ServerConn, dbName, schema, object string, rights ...Right) bool {
	if sc == nil || len(rights) == 0 {
		return true
	}
	return RightsAllow(sc.Capabilities(), sc.CachedDatabaseCapabilities, dbName, schema, object, rights...)
}

// RightsAllow is the whole of the Allows rule, in one place: whether an action
// needing any one of rights may still be offered, given a server capability set
// and a way to reach a database's.
//
// dbCaps is what separates the two callers. The menus pass
// CachedDatabaseCapabilities, because they run on the UI goroutine while a menu
// is being drawn and must not issue a query; a Properties page passes the
// probing form, because its load already runs on a background goroutine. The
// *rule* must not differ between them, which is why there is only one copy of
// it — pageReadOnlyReason had its own, and that copy understood neither the
// membership rights SQL Agent gates on nor the schema- and object-scoped ones,
// so a login holding ALTER on just the one table would have been shown a
// read-only banner for a page it could in fact write.
func RightsAllow(server *gosmo.Capabilities, dbCaps func(string) *gosmo.DatabaseCapabilities, dbName, schema, object string, rights ...Right) bool {
	// A DENY on the object itself is the one answer in the whole gate that
	// withholds rather than adds, and it has to be asked before the rights
	// below rather than among them: SQL Server resolves it over every wider
	// grant, so any one of them would otherwise answer yes for a write it
	// then refuses. See ObjectDenial.
	if _, _, denied := ObjectDenial(server, dbCaps, dbName, schema, object, rights...); denied {
		return false
	}
	for _, r := range rights {
		switch {
		case r.Membership:
			// Unknown must allow explicitly here rather than falling through
			// to the next right: InRole cannot tell "not a member" from
			// "never asked", so an unprobed msdb would withhold every SQL
			// Agent action from the login that holds the role. Probed is the
			// only thing that separates the two.
			caps := dbCaps(r.InDB)
			if !caps.Probed() || caps.InRole(r.Name) {
				return true
			}
		case r.ServerRole:
			// Unknown must allow explicitly, for Membership's reason:
			// InServerRole cannot tell "not a member" from "never asked". And
			// sysadmin is asked separately — it implies membership of no other
			// fixed role, so a sysadmin reads 0 for diskadmin while being
			// permitted everything diskadmin carries.
			if !server.Probed() || server.InServerRole(r.Name) || server.IsSysadmin() {
				return true
			}
		case r.Securable != "":
			// No schema guard: an assembly has none, and asks with "".
			if dbName == "" || object == "" {
				continue
			}
			// PermitsOnSecurable, not HasOnSecurable, and it is the one arm
			// here that may answer yes for a securable with no row: the map
			// is not sparse, so a missing row is one created since the probe,
			// and unknown fails open. A 0 falls through to the wider rights.
			if dbCaps(dbName).PermitsOnSecurable(r.Securable, schema, object, r.Name) {
				return true
			}
		case r.Object:
			if dbName == "" || schema == "" || object == "" {
				continue
			}
			// Has, not Permits: the map is sparse, so "not denied" is true of
			// every object in the database and would permit everything. An
			// object with no row leaves the wider rights beside this one to
			// answer.
			if dbCaps(dbName).HasOnObject(schema, object, r.Name) {
				return true
			}
		case r.Schema:
			if dbName == "" || schema == "" {
				continue
			}
			// PermitsOnSchema, not AllowsOnSchema: an inaccessible database
			// answers unknown for every schema, and unknown fails open — see
			// gosmo.DatabaseCapabilities.Permits.
			if dbCaps(dbName).PermitsOnSchema(schema, r.Name) {
				return true
			}
		case !r.DB:
			// The name and its alternates are asked separately rather than
			// joined into one slice: this runs per menu item and per toolbar
			// cell on every draw, and the join allocated each time.
			if server.Allows(r.Name) {
				return true
			}
			for _, n := range r.Alt {
				if server.Allows(n) {
					return true
				}
			}
		case dbName == "":
			// No database to ask about — a folder-level action that will
			// prompt for one. Nothing measured, so nothing withheld.
			return true
		default:
			// Permits, not Allows: an inaccessible database answers
			// CapabilityUnknown to every permission and unknown fails open,
			// which would leave Back Up and Delete offered on exactly the
			// databases the login cannot open. See
			// gosmo.DatabaseCapabilities.Permits.
			caps := dbCaps(dbName)
			if caps.Permits(r.Name) {
				return true
			}
			// alt is consulted at this scope too. No database-scope right
			// declares one today, so only the test reaches this loop — but a
			// right whose alternates counted at server scope and were ignored
			// here would withhold the action from a login that holds one of
			// them, and nothing at run time tells that apart from a real
			// denial. The next 2022-style permission split is as likely to
			// land at database scope as at server scope.
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

	// ServerSecurable is the login, server role or endpoint the action is
	// aimed at, and ServerKind which of the three it is. The kind is carried
	// because the sentence has to name it: "denied on login x" and "denied on
	// endpoint x" are different securables, and only the right that asked
	// knows which was meant.
	ServerSecurable string
	ServerKind      gosmo.ServerSecurableKind

	// AvailabilityGroup is the group the action is aimed at. Kept apart from
	// ServerSecurable because it is a different gosmo map with a different
	// reading — see Right.DeniedOnAG.
	AvailabilityGroup string
}

// ObjectDenial reports the right whose DENY withholds an action, the securable
// that DENY sits on where it is not the object itself, and whether there is
// one. It is the only part of the gate that
// withholds on an object-scope answer, and it is sound for a reason
// HasOnObject's sparseness argument does not cover: it asks for a state the
// probe recorded, so silence stays silence.
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
// A database that was never probed records nothing, which reads as no denial —
// unknown fails open here as everywhere else.
//
// A DENY on one *column* of the object withholds just as hard, and is asked
// about second: SQL Server resolves it over every wider grant the same way, so
// a statement touching the whole table fails for a login holding the
// permission on the table itself. Nothing gossms writes is scoped to named
// columns, so a column denial is a denial of the action outright.
//
// A DENY on the object's *schema* is asked about third, and for the same
// reason: SQL Server resolves it over a database-wide grant, so a principal
// with ALTER on the database and DENY ALTER on dbo was offered every rename
// and drop in it and met Msg 297 on each. It is asked of gosmo's
// DeniedOnSchema rather than of PermitsOnSchema because HAS_PERMS_BY_NAME
// answers 0 for a schema permission simply never granted — which is the
// ordinary case, and withholding on it would empty the menus of every login
// that works through a database-wide grant.
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
	// The server-scope arm comes before the dbName guard because it is the one
	// securable family that lives outside a database entirely: a login, a
	// server role and an endpoint all carry an empty DBName, and every arm
	// below would answer nothing about them. It is asked only of a right that
	// declares DeniedOnServer, so a node of some other family sharing a denied
	// login's name is not withheld — the way DeniedOnPrincipal discriminates
	// the class-4 arm.
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
	// The availability-group arm sits beside the server one and above the
	// dbName guard for the same reason: a group, a replica and a listener all
	// carry an empty DBName.
	//
	// server.Has, not Allows: the group answer is a HAS_PERMS_BY_NAME 0, which
	// means "denied on this group" only while the server-wide right is held.
	// Without that guard every login lacking ALTER ANY AVAILABILITY GROUP
	// would be told the group is denied instead of which right to ask for.
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
	// The class-4 arm comes before the schema guard below because it is the
	// one securable here that has no Schema: a user node carries an empty
	// Schema and its own name as the object.
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
	// Everything below is scoped by the object's schema, and answers nothing
	// without one. The guard stays here rather than moving up to the top so
	// that a schema-less node reaches the arm above and nothing else — the
	// wider arms were never asked for one and extending them is a separate
	// question with its own live answer to establish.
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

// serverSecurableWord renders a server securable kind as the sentence says it.
// gosmo spells the kinds the way SQL Server's DENY statement does — "LOGIN",
// "SERVER ROLE", "ENDPOINT" — and shouting them mid-sentence reads as a
// keyword rather than as the thing the user clicked.
func serverSecurableWord(k gosmo.ServerSecurableKind) string {
	return strings.ToLower(string(k))
}

// DeniedText is the sentence for an action withheld by a DENY on the object
// rather than by a missing right — RequiresText's counterpart. It names no
// role and asks for nothing, because there is nothing to ask for: the login
// may hold every right in the list already, and the DENY overrides all of
// them. Only the object's own permission can be changed.
func DeniedText(r Right, at Site) string {
	switch {
	case at.Column != "":
		return r.Name + " is denied on column " + at.Column + " of this object."
	case at.Schema != "":
		return r.Name + " is denied on schema " + at.Schema + "."
	case at.Database != "":
		return r.Name + " is denied on database " + at.Database + "."
	case at.Principal != "":
		// r.DeniedOnPrincipal, not r.Name: the right is the database-wide
		// ALTER ANY USER and the DENY that beats it is plain ALTER on the
		// principal, so naming the right here would describe a row that does
		// not exist.
		//
		// "principal", not "user": class 4 covers database roles too, and the
		// membership pages ask this question about a role. The gate is given a
		// name and cannot tell the two apart — only the catalog can — so it
		// says the word that is true of both.
		return r.DeniedOnPrincipal + " is denied on principal " + at.Principal + "."
	case at.ServerSecurable != "":
		// r.DeniedOnServer, not r.Name, for at.Principal's reason: the right
		// is the server-wide ALTER ANY LOGIN and the DENY that beats it is
		// plain ALTER on the securable.
		//
		// The kind *is* named here, where the class-4 sentence says the
		// vaguer "principal": the right declared which securable it asked
		// about, so the sentence can say it — and it has to, since "denied on
		// x" would not tell a login from the endpoint beside it.
		return r.DeniedOnServer + " is denied on " + serverSecurableWord(at.ServerKind) + " " + at.ServerSecurable + "."
	case at.AvailabilityGroup != "":
		return r.DeniedOnAG + " is denied on availability group " + at.AvailabilityGroup + "."
	}
	return r.Name + " is denied on this object."
}
