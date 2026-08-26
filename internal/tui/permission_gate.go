package tui

import (
	"strings"

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
	rightCreateAnyDatabase  = requiredRight{name: "CREATE ANY DATABASE", role: "dbcreator"}
	rightAlterAnyDatabase   = requiredRight{name: "ALTER ANY DATABASE", role: "dbcreator"}
	rightAlterAnyEndpoint   = requiredRight{name: "ALTER ANY ENDPOINT", role: "sysadmin"}
	rightAlterAnyAG         = requiredRight{name: "ALTER ANY AVAILABILITY GROUP", role: "sysadmin"}
	rightAlterAnyLinkedSrv  = requiredRight{name: "ALTER ANY LINKED SERVER", role: "sysadmin"}

	rightBackupDatabase = requiredRight{name: "BACKUP DATABASE", role: "db_backupoperator", db: true}
	rightAlterDatabase  = requiredRight{name: "ALTER", role: "db_owner", db: true}
	rightControlDB      = requiredRight{name: "CONTROL", role: "db_owner", db: true}
	rightAlterAnyUser   = requiredRight{name: "ALTER ANY USER", role: "db_accessadmin", db: true}
	rightAlterAnyDBRole = requiredRight{name: "ALTER ANY ROLE", role: "db_securityadmin", db: true}
	rightAlterAnySchema = requiredRight{name: "ALTER ANY SCHEMA", role: "db_ddladmin", db: true}
	rightCreateTable    = requiredRight{name: "CREATE TABLE", role: "db_ddladmin", db: true}
	rightViewDBState    = requiredRight{name: "VIEW DATABASE STATE", role: "db_owner", db: true}
)

// String renders the right the way the user is told about it: the permission,
// and the role that also carries it in parentheses.
func (r requiredRight) String() string {
	if r.role == "" {
		return r.name
	}
	return r.name + " (" + r.role + ")"
}

// requiresText is the sentence shown when an action is withheld. Any one of
// the rights is enough, which is why they are joined with "or".
func requiresText(rights ...requiredRight) string {
	names := make([]string, len(rights))
	for i, r := range rights {
		names[i] = r.String()
	}
	return "Requires " + strings.Join(names, " or ") + "."
}

// allowsAction reports whether an action needing any one of rights may still
// be offered on sc, for the database dbName (ignored by server-scope rights).
//
// The test is Allows, never Has: an action is withheld only when the server
// answered "no" to *every* right that would permit it. An unprobed connection,
// a probe that failed, a database whose answer is not cached yet, and a
// permission this instance does not define all leave the action offered
// exactly as it was before any of this existed. Gating on Has instead would
// empty the menus of a sysadmin whose probe timed out.
//
// Database-scope rights are read from the cache only — see
// db.ServerConn.CachedDatabaseCapabilities. This runs on the UI goroutine
// while a menu is being drawn.
func allowsAction(sc *db.ServerConn, dbName string, rights ...requiredRight) bool {
	if sc == nil || len(rights) == 0 {
		return true
	}
	for _, r := range rights {
		switch {
		case !r.db:
			// The name and its alternates are asked separately rather than
			// joined into one slice: this runs per menu item and per toolbar
			// cell on every draw, and the join allocated each time.
			caps := sc.Capabilities()
			if caps.Allows(r.name) {
				return true
			}
			for _, n := range r.alt {
				if caps.Allows(n) {
					return true
				}
			}
		case dbName == "":
			// No database to ask about — a folder-level action that will
			// prompt for one. Nothing measured, so nothing withheld.
			return true
		// Permits, not Allows: an inaccessible database answers
		// CapabilityUnknown to every permission and unknown fails open, which
		// would leave Back Up and Delete offered on exactly the databases the
		// login cannot open. See gosmo.DatabaseCapabilities.Permits.
		case sc.CachedDatabaseCapabilities(dbName).Permits(r.name):
			return true
		}
	}
	return false
}

// gate returns item with its Enabled predicate extended to consult the
// capability set, keeping any predicate it already had. The two are ANDed:
// "no active query panel" and "no rights for this" are both reasons to
// withhold, and neither should cancel the other out.
func gate(item controls.MenuItem, sc *db.ServerConn, dbName string, rights ...requiredRight) controls.MenuItem {
	prev := item.Enabled
	allowed := func() bool { return allowsAction(sc, dbName, rights...) }
	if len(rights) > 0 {
		// Shown only while the item is disabled, and only the first right —
		// the whole "Requires X (role) or Y or Z." sentence would double the
		// width of every context menu it appears in.
		item.Note = "needs " + rights[0].name
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

// databaseWriteRights are what permits the ALTER DATABASE-shaped writes every
// Database Properties page but Permissions makes — any one of them.
func databaseWriteRights() []requiredRight {
	return []requiredRight{rightAlterDatabase, rightControlDB, rightAlterAnyDatabase}
}
