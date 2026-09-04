package tui

import (
	"slices"
	"strings"

	"github.com/radix29/gosmo"
	"github.com/radix29/gossms/internal/db"
	"github.com/radix29/gossms/internal/tuikit/controls"
)

// requiredRight is one permission an action needs. role names the fixed
// server or database role that also confers it, and exists only for the
// sentence the user is shown — the gate itself always asks about the
// permission, because HAS_PERMS_BY_NAME already answers 1 for a member of the
// role (and for a sysadmin), while IS_SRVROLEMEMBER does not fold sysadmin in.
type requiredRight struct {
	name string
	role string
	db   bool // database-scope rather than server-scope

	// schema narrows the question to the schema the object lives in, asked of
	// gosmo's per-schema probe rather than the database-wide one. A principal
	// granted ALTER on one schema holds no database-wide permission at all, so
	// this is the only right that can speak for it.
	//
	// It is also the right objectDenial asks the schema DENY question through —
	// there, and only there, a schema-scoped right can *withhold*.
	schema bool

	// membership makes name a fixed database *role* to be a member of, in the
	// database inDB, rather than a permission to hold. It exists for SQL
	// Agent, whose actions are permitted by membership of an msdb role and by
	// nothing HAS_PERMS_BY_NAME can be asked about.
	//
	// A membership right is the only kind that cannot fail open on its own:
	// InRole answers false for a role never asked about exactly as it does for
	// one the login is not in, so allowsActionOn checks
	// gosmo.DatabaseCapabilities.Probed before believing a false.
	membership bool
	inDB       string

	// serverRole makes name a fixed *server* role to be a member of rather
	// than a permission to hold — membership's server-scope twin. It exists
	// for the writes SQL Server permits by role membership alone: a backup
	// device is added and dropped by sp_addumpdevice/sp_dropdevice, which
	// diskadmin carries and no server permission answers for.
	//
	// Like membership it cannot fail open on its own — InServerRole answers
	// false for a role never asked about exactly as it does for a login that
	// is not in it — so rightsAllow checks gosmo.Capabilities.Probed before
	// believing a false. And it asks about sysadmin too: membership of
	// sysadmin implies membership of no other fixed role, so a sysadmin reads
	// 0 for diskadmin while being permitted everything it carries.
	serverRole bool

	// object narrows the question to the object itself, asked of gosmo's
	// per-object probe. It is the only right that can speak for a principal
	// granted ALTER on one table and nothing else — such a principal reads 0
	// at schema and database scope alike.
	//
	// It can only ever *add* permission. gosmo's ObjectPermissions map holds a
	// row for an object explicitly granted, denied or owned and for no other,
	// so its silence means "no explicit grant", never "not probed" — see
	// gosmo.DatabaseCapabilities.HasOnObject.
	object bool

	// alt are narrower permissions that also satisfy this one and are not
	// named in the message. SQL Server 2022 split VIEW SERVER STATE into two
	// halves, and a login holding either half can do the thing — but naming
	// all three in one sentence says less than naming the one the user is
	// likely to be granted.
	alt []string
}

// The rights the application gates on. Every name is in gosmo's Probed* list
// *for its scope*; one that is not would read back as CapabilityUnknown
// forever and gate nothing, and nothing at run time tells that apart from a
// login that holds the right. That is why they are declared here rather than
// spelled at each call site, and why permission_gate_names_test.go reads these
// literals back and checks each against gosmo.
var (
	rightControlServer   = requiredRight{name: "CONTROL SERVER", role: "sysadmin"}
	rightViewServerState = requiredRight{name: "VIEW SERVER STATE", role: "sysadmin",
		alt: []string{"VIEW SERVER PERFORMANCE STATE", "VIEW SERVER SECURITY STATE"}}
	rightAlterSettings      = requiredRight{name: "ALTER SETTINGS", role: "serveradmin"}
	rightAlterAnyLogin      = requiredRight{name: "ALTER ANY LOGIN", role: "securityadmin"}
	rightAlterAnyServerRole = requiredRight{name: "ALTER ANY SERVER ROLE", role: "securityadmin"}
	rightAlterAnyCredential = requiredRight{name: "ALTER ANY CREDENTIAL", role: "securityadmin"}
	rightCreateAnyDatabase  = requiredRight{name: "CREATE ANY DATABASE", role: "dbcreator"}
	rightAlterAnyDatabase   = requiredRight{name: "ALTER ANY DATABASE", role: "dbcreator"}
	rightAlterAnyEndpoint   = requiredRight{name: "ALTER ANY ENDPOINT", role: "sysadmin"}
	rightAlterAnyAudit      = requiredRight{name: "ALTER ANY SERVER AUDIT", role: "sysadmin"}
	rightAlterAnyAG         = requiredRight{name: "ALTER ANY AVAILABILITY GROUP", role: "sysadmin"}
	// Held for a feature that does not exist yet: nothing in the application
	// creates or alters a linked server, so nothing gates on this. It is
	// declared here rather than at the future call site because
	// permission_gate_names_test.go checks every literal in this block against
	// gosmo's Probed* list — a name added later and misspelled would read back
	// as CapabilityUnknown forever and gate nothing.
	rightAlterAnyLinkedSrv = requiredRight{name: "ALTER ANY LINKED SERVER", role: "sysadmin"}

	rightBackupDatabase = requiredRight{name: "BACKUP DATABASE", role: "db_backupoperator", db: true}
	rightAlterDatabase  = requiredRight{name: "ALTER", role: "db_owner", db: true}
	rightControlDB      = requiredRight{name: "CONTROL", role: "db_owner", db: true}
	rightAlterAnyUser   = requiredRight{name: "ALTER ANY USER", role: "db_accessadmin", db: true}
	rightAlterAnyDBRole = requiredRight{name: "ALTER ANY ROLE", role: "db_securityadmin", db: true}
	// Held for a feature that does not exist yet — there is no New Table — for
	// the reason given at rightAlterAnyLinkedSrv.
	rightCreateTable = requiredRight{name: "CREATE TABLE", role: "db_ddladmin", db: true}

	rightAlterAnySchema = requiredRight{name: "ALTER ANY SCHEMA", role: "db_ddladmin", db: true}
	rightViewDBState    = requiredRight{name: "VIEW DATABASE STATE", role: "db_owner", db: true}

	// The three below name db_owner rather than a narrower role because there
	// isn't one: verified live 2026-08-27, db_ddladmin answers 0 to all three,
	// and a database-wide ALTER answers 1 for the two key permissions but 0
	// for ALTER ANY SECURITY POLICY — so no wider right can stand in for it.
	rightAlterAnyCMK       = requiredRight{name: "ALTER ANY COLUMN MASTER KEY", role: "db_owner", db: true}
	rightAlterAnyCEK       = requiredRight{name: "ALTER ANY COLUMN ENCRYPTION KEY", role: "db_owner", db: true}
	rightAlterAnySecPolicy = requiredRight{name: "ALTER ANY SECURITY POLICY", role: "db_owner", db: true}

	// The two SQL Agent rights are memberships, not permissions: what permits
	// New Job and its three siblings is membership of an msdb role, which
	// grants EXECUTE on individual procedures rather than the database-scope
	// EXECUTE a permission probe can ask about. See agentWriteRights.
	// A backup device is added and dropped by sp_addumpdevice/sp_dropdevice,
	// which diskadmin carries and which no server *permission* answers for —
	// so this is a role membership, not a permission. CONTROL SERVER would be
	// a knowingly wrong gate here: a pure diskadmin, the one principal the
	// feature is for, would be shown a read-only banner on a page they can in
	// fact write.
	rightDiskAdmin = requiredRight{name: "diskadmin", serverRole: true}

	rightSQLAgentUser = requiredRight{name: "SQLAgentUserRole", membership: true, inDB: "msdb"}
	rightMsdbOwner    = requiredRight{name: "db_owner", membership: true, inDB: "msdb"}

	// rightAlterOnObject is the grant made directly on one object, which no
	// wider scope reflects: a principal granted ALTER on one table reads 0 for
	// every database- and schema-scope permission there is.
	rightAlterOnObject = requiredRight{name: "ALTER", db: true, object: true}

	// rightAlterOnSchema is what SQL Server actually checks for a rename, a
	// move or a drop of a schema object. No role carries it: it is granted on
	// the schema itself, and a principal holding it may hold nothing else.
	rightAlterOnSchema = requiredRight{name: "ALTER", db: true, schema: true}
)

// nameOnly is the permission as the user is told to ask for it, without the
// role — requiresText gathers the roles into one trailing clause instead.
//
// A schema-scoped right is named for the securable and carries no role at all:
// "ALTER (db_ddladmin)" would send the user after a role that grants something
// much wider than what is missing.
func (r requiredRight) nameOnly() string {
	switch {
	case r.schema:
		return r.name + " on the object's schema"
	case r.membership:
		return "membership of " + r.name + " in " + r.inDB
	case r.serverRole:
		return "membership of the " + r.name + " server role"
	case r.object:
		return r.name + " on the object itself"
	}
	return r.name
}

// String renders one right on its own — the permission with the role that also
// carries it, as permission_error.go names it in a single-right sentence.
// requiresText does not use it: with several alternatives the roles are
// collapsed into one clause instead.
func (r requiredRight) String() string {
	if r.role == "" {
		return r.nameOnly()
	}
	return r.name + " (" + r.role + ")"
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

// requiresText is the sentence shown when an action is withheld. Any one of
// the rights is enough, which is why they are joined with "or".
//
// The roles are gathered into one trailing clause instead of following each
// permission, because the alternatives for one action overwhelmingly share a
// role: three rights spelled out gave "ALTER (db_owner) or CONTROL (db_owner)
// or ALTER ANY DATABASE (dbcreator)", which names db_owner twice and reads as
// six things to ask for rather than three. A right with no role stays outside
// the clause — see requiredRight.String.
func requiresText(rights ...requiredRight) string {
	var named, plain, roles []string
	for _, r := range rights {
		if r.role == "" {
			plain = append(plain, r.nameOnly())
			continue
		}
		named = append(named, r.name)
		if !slices.Contains(roles, r.role) {
			roles = append(roles, r.role)
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

// allowsAction reports whether an action needing any one of rights may still
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
func allowsAction(sc *db.ServerConn, dbName string, rights ...requiredRight) bool {
	return allowsActionOn(sc, dbName, "", "", rights...)
}

// allowsActionOn is allowsAction for an action aimed at one object: schema is
// the schema that object lives in, which is what a schema-scoped right is
// asked about. Empty means "not an object in a schema", and a schema-scoped
// right then grants nothing — the database-wide alternatives beside it still
// answer, so nothing is withheld that was offered before.
func allowsActionOn(sc *db.ServerConn, dbName, schema, object string, rights ...requiredRight) bool {
	if sc == nil || len(rights) == 0 {
		return true
	}
	return rightsAllow(sc.Capabilities(), sc.CachedDatabaseCapabilities, dbName, schema, object, rights...)
}

// rightsAllow is the whole of the Allows rule, in one place: whether an action
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
func rightsAllow(server *gosmo.Capabilities, dbCaps func(string) *gosmo.DatabaseCapabilities, dbName, schema, object string, rights ...requiredRight) bool {
	// A DENY on the object itself is the one answer in the whole gate that
	// withholds rather than adds, and it has to be asked before the rights
	// below rather than among them: SQL Server resolves it over every wider
	// grant, so any one of them would otherwise answer yes for a write it
	// then refuses. See objectDenial.
	if _, _, denied := objectDenial(server, dbCaps, dbName, schema, object, rights...); denied {
		return false
	}
	for _, r := range rights {
		switch {
		case r.membership:
			// Unknown must allow explicitly here rather than falling through
			// to the next right: InRole cannot tell "not a member" from
			// "never asked", so an unprobed msdb would withhold every SQL
			// Agent action from the login that holds the role. Probed is the
			// only thing that separates the two.
			caps := dbCaps(r.inDB)
			if !caps.Probed() || caps.InRole(r.name) {
				return true
			}
		case r.serverRole:
			// Unknown must allow explicitly, for membership's reason:
			// InServerRole cannot tell "not a member" from "never asked". And
			// sysadmin is asked separately — it implies membership of no other
			// fixed role, so a sysadmin reads 0 for diskadmin while being
			// permitted everything diskadmin carries.
			if !server.Probed() || server.InServerRole(r.name) || server.IsSysadmin() {
				return true
			}
		case r.object:
			if dbName == "" || schema == "" || object == "" {
				continue
			}
			// Has, not Permits: the map is sparse, so "not denied" is true of
			// every object in the database and would permit everything. An
			// object with no row leaves the wider rights beside this one to
			// answer.
			if dbCaps(dbName).HasOnObject(schema, object, r.name) {
				return true
			}
		case r.schema:
			if dbName == "" || schema == "" {
				continue
			}
			// PermitsOnSchema, not AllowsOnSchema: an inaccessible database
			// answers unknown for every schema, and unknown fails open — see
			// gosmo.DatabaseCapabilities.Permits.
			if dbCaps(dbName).PermitsOnSchema(schema, r.name) {
				return true
			}
		case !r.db:
			// The name and its alternates are asked separately rather than
			// joined into one slice: this runs per menu item and per toolbar
			// cell on every draw, and the join allocated each time.
			if server.Allows(r.name) {
				return true
			}
			for _, n := range r.alt {
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
			if caps.Permits(r.name) {
				return true
			}
			// alt is consulted at this scope too. No database-scope right
			// declares one today, so only the test reaches this loop — but a
			// right whose alternates counted at server scope and were ignored
			// here would withhold the action from a login that holds one of
			// them, and nothing at run time tells that apart from a real
			// denial. The next 2022-style permission split is as likely to
			// land at database scope as at server scope.
			for _, n := range r.alt {
				if caps.Permits(n) {
					return true
				}
			}
		}
	}
	return false
}

// denialSite names the securable a DENY was found on, for the sentence the
// user is shown. At most one field is set; both empty means the object itself.
type denialSite struct {
	column string // a column of the object
	schema string // the object's schema
}

// objectDenial reports the right whose DENY withholds an action, the securable
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
// A DENY on the object's *schema* is asked about last, and for the same
// reason: SQL Server resolves it over a database-wide grant, so a principal
// with ALTER on the database and DENY ALTER on dbo was offered every rename
// and drop in it and met Msg 297 on each. It is asked of gosmo's
// DeniedOnSchema rather than of PermitsOnSchema because HAS_PERMS_BY_NAME
// answers 0 for a schema permission simply never granted — which is the
// ordinary case, and withholding on it would empty the menus of every login
// that works through a database-wide grant.
func objectDenial(server *gosmo.Capabilities, dbCaps func(string) *gosmo.DatabaseCapabilities, dbName, schema, object string, rights ...requiredRight) (requiredRight, denialSite, bool) {
	if dbName == "" || schema == "" || server.InServerRole("sysadmin") {
		return requiredRight{}, denialSite{}, false
	}
	var caps *gosmo.DatabaseCapabilities
	asked := false
	ask := func() *gosmo.DatabaseCapabilities {
		if !asked {
			caps, asked = dbCaps(dbName), true
		}
		return caps
	}
	if object != "" {
		for _, r := range rights {
			if !r.object {
				continue
			}
			if ask().DeniedOnObject(schema, object, r.name) {
				return r, denialSite{}, true
			}
			if col, denied := ask().DeniedOnAnyColumn(schema, object, r.name); denied {
				return r, denialSite{column: col}, true
			}
		}
	}
	for _, r := range rights {
		if !r.schema {
			continue
		}
		if ask().DeniedOnSchema(schema, r.name) {
			return r, denialSite{schema: schema}, true
		}
	}
	return requiredRight{}, denialSite{}, false
}

// deniedOnObject is objectDenial for a live connection — the shape the menu
// gates ask it in, with the cached capabilities the UI goroutine may use.
func deniedOnObject(sc *db.ServerConn, dbName, schema, object string, rights ...requiredRight) (requiredRight, denialSite, bool) {
	if sc == nil {
		return requiredRight{}, denialSite{}, false
	}
	return objectDenial(sc.Capabilities(), sc.CachedDatabaseCapabilities, dbName, schema, object, rights...)
}

// deniedText is the sentence for an action withheld by a DENY on the object
// rather than by a missing right — requiresText's counterpart. It names no
// role and asks for nothing, because there is nothing to ask for: the login
// may hold every right in the list already, and the DENY overrides all of
// them. Only the object's own permission can be changed.
func deniedText(r requiredRight, at denialSite) string {
	switch {
	case at.column != "":
		return r.name + " is denied on column " + at.column + " of this object."
	case at.schema != "":
		return r.name + " is denied on schema " + at.schema + "."
	}
	return r.name + " is denied on this object."
}

// gate returns item with its Enabled predicate extended to consult the
// capability set, keeping any predicate it already had. The two are ANDed:
// "no active query panel" and "no rights for this" are both reasons to
// withhold, and neither should cancel the other out.
func gate(item controls.MenuItem, sc *db.ServerConn, dbName string, rights ...requiredRight) controls.MenuItem {
	return gateOn(item, sc, dbName, "", "", rights...)
}

// gateOn is gate for an action aimed at one object in a schema — see
// allowsActionOn.
func gateOn(item controls.MenuItem, sc *db.ServerConn, dbName, schema, object string, rights ...requiredRight) controls.MenuItem {
	prev := item.Enabled
	allowed := func() bool { return allowsActionOn(sc, dbName, schema, object, rights...) }
	if len(rights) > 0 {
		// Shown only while the item is disabled, and only the first right —
		// the whole "Requires X (role) or Y or Z." sentence would double the
		// width of every context menu it appears in.
		item.Note = "needs " + rights[0].name
		// Unless a DENY on the object is what withheld it, and then naming a
		// right sends the user after one they may already hold — the denial
		// beats it. Read once here rather than in the predicate: the menu is
		// rebuilt each time it opens, and Note is a string, not a callback.
		if r, at, denied := deniedOnObject(sc, dbName, schema, object, rights...); denied {
			switch {
			case at.column != "":
				item.Note = r.name + " denied on column " + at.column
			case at.schema != "":
				item.Note = r.name + " denied on schema " + at.schema
			default:
				item.Note = r.name + " denied on this object"
			}
		}
		// And only when the rights are why it is disabled. An item its own
		// predicate has already withheld — a failover offered on secondaries
		// only — is grey for a reason this note does not describe, and naming
		// a permission there sends the user after one they may already hold.
		item.NoteWhen = func() bool { return (prev == nil || prev()) && !allowed() }
	}
	item.Enabled = func() bool {
		if prev != nil && !prev() {
			return false
		}
		return allowed()
	}
	return item
}

// withRequires attaches the rights a page's writes need. Declared where the
// page set is assembled rather than inside each page, so one list shows what
// every page of a dialog needs and a new page cannot quietly arrive ungated.
func withRequires(p propPage, in string, rights ...requiredRight) propPage {
	p.requires = rights
	p.requiresIn = in
	return p
}

// withRequiresOn is withRequires for a page whose object- or schema-scoped
// rights need to know which securable to ask about — every page set built on
// objectWriteRights(). object is the *table*: SQL Server checks ALTER on the
// table for a change to its index, its statistics or one of its keys, and the
// probe records the table, not the index.
func withRequiresOn(p propPage, in, schema, object string, rights ...requiredRight) propPage {
	p = withRequires(p, in, rights...)
	p.requiresSchema = schema
	p.requiresObject = object
	return p
}

// databaseWriteRights are what permits the ALTER DATABASE-shaped writes every
// Database Properties page but Permissions makes — any one of them.
func databaseWriteRights() []requiredRight {
	return []requiredRight{rightAlterDatabase, rightControlDB, rightAlterAnyDatabase}
}

// objectWriteRights are what permits a write aimed at one object in a schema —
// a rename, a move, a drop, a new index, new statistics. Any one of them.
//
// The set reaches the object itself. SQL Server checks ALTER on the object,
// and a grant made directly on one table is reflected at no wider scope — such
// a principal reads 0 for every database- and schema-scope permission, so the
// four wider rights all deny and the action was withheld from someone who
// could perform it. rightAlterOnObject is the one that speaks for them.
//
// OBJECT scope costs no query per object, whatever HAS_PERMS_BY_NAME would
// cost: gosmo's object block reads the whole database in one pass, as a fourth
// part of the probe that was already running.
func objectWriteRights() []requiredRight {
	return []requiredRight{
		rightAlterDatabase, rightControlDB, rightAlterAnySchema,
		rightAlterOnSchema, rightAlterOnObject,
	}
}

// agentWriteRights are what permits SQL Agent's New Job / New Schedule /
// New Alert / New Operator — any one of them.
//
// Three facts settled live on 2026-08-27 shape this set, and each of them
// breaks the gate if it is assumed the other way:
//
//   - A sysadmin reads IS_ROLEMEMBER = 0 for all three SQLAgent* roles. It
//     maps to dbo, and dbo is not a member of any of them. Gating on the
//     Agent roles alone withholds every Agent action from the one login that
//     certainly may perform them, which is why CONTROL SERVER is here.
//   - The roles nest, and IS_ROLEMEMBER resolves the nesting: a member of
//     SQLAgentOperatorRole reads 1 for SQLAgentUserRole. So the narrowest role
//     is the whole test for "or above", and SQLAgentReaderRole and
//     SQLAgentOperatorRole need no separate check.
//   - msdb db_owner is a real non-sysadmin case and is not covered by either
//     of the above.
//
// The order is the message, not the test: gate shows only rights[0] in a
// withheld item's note, so the narrowest sufficient right goes first. Led with
// CONTROL SERVER the note read "needs CONTROL SERVER", which sends a user who
// wants to create a job away to ask for sysadmin.
func agentWriteRights() []requiredRight {
	return []requiredRight{rightSQLAgentUser, rightMsdbOwner, rightControlServer}
}
