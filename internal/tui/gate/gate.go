package gate

import (
	"github.com/radix29/gosmo"
)

// gate.go is the [Right] type and the catalogue of rights the application
// asks about. The rule is in allows.go, the wording in text.go, the menu
// wrappers in menu.go and the named sets in sets.go.

// Right is one permission an action needs. role names the fixed
// server or database role that also confers it, and exists only for the
// sentence the user is shown — the gate itself always asks about the
// permission, because HAS_PERMS_BY_NAME already answers 1 for a member of the
// role (and for a sysadmin), while IS_SRVROLEMEMBER does not fold sysadmin in.
type Right struct {
	Name string
	Role string
	DB   bool // database-scope rather than server-scope

	// schema narrows the question to the schema the object lives in, asked of
	// gosmo's per-schema probe rather than the database-wide one. A principal
	// granted ALTER on one schema holds no database-wide permission at all, so
	// this is the only right that can speak for it.
	//
	// It is also the right ObjectDenial asks the schema DENY question through —
	// there, and only there, a schema-scoped right can *withhold*.
	Schema bool

	// Membership makes Name a fixed database *role* to be a member of, in the
	// database InDB, rather than a permission to hold. It exists for SQL
	// Agent, whose actions are permitted by membership of an msdb role and by
	// nothing HAS_PERMS_BY_NAME can be asked about.
	//
	// A Membership right is the only kind that cannot fail open on its own:
	// InRole answers false for a role never asked about exactly as it does for
	// one the login is not in, so AllowsOn checks
	// gosmo.DatabaseCapabilities.Probed before believing a false.
	Membership bool
	InDB       string

	// ServerRole makes Name a fixed *server* role to be a member of rather
	// than a permission to hold — Membership's server-scope twin. It exists
	// for the writes SQL Server permits by role membership alone: a backup
	// device is added and dropped by sp_addumpdevice/sp_dropdevice, which
	// diskadmin carries and no server permission answers for.
	//
	// Like Membership it cannot fail open on its own — InServerRole answers
	// false for a role never asked about exactly as it does for a login that
	// is not in it — so RightsAllow checks gosmo.Capabilities.Probed before
	// believing a false. And it asks about sysadmin too: membership of
	// sysadmin implies membership of no other fixed role, so a sysadmin reads
	// 0 for diskadmin while being permitted everything it carries.
	ServerRole bool

	// object narrows the question to the object itself, asked of gosmo's
	// per-object probe. It is the only right that can speak for a principal
	// granted ALTER on one table and nothing else — such a principal reads 0
	// at schema and database scope alike.
	//
	// It can only ever *add* permission. gosmo's ObjectPermissions map holds a
	// row for an object explicitly granted, denied or owned and for no other,
	// so its silence means "no explicit grant", never "not probed" — see
	// gosmo.DatabaseCapabilities.HasOnObject.
	Object bool

	// securable narrows the question to one assembly, user-defined type or XML
	// schema collection — classes 5, 6 and 10 — asked of gosmo's per-securable
	// probe. object cannot speak for these, being class 1 only, and no wider
	// right can either: a principal granted CONTROL on one assembly, or owning
	// it, reads 0 at every database and schema scope there is.
	//
	// Unlike object it is read Permits-wise, and so can withhold: gosmo's map
	// is a HAS_PERMS_BY_NAME answer with a row for every securable the login
	// can see, so a 0 is the server's answer rather than a silence. That is
	// what lets it be Move to Schema's only right — see
	// gosmo.ProbedSecurablePermissions for what CONTROL decides, probed live.
	Securable gosmo.DatabaseSecurableKind

	// DeniedOnPrincipal names the DATABASE_PRINCIPAL-scope (class 4)
	// permission whose DENY withholds this right, or "" for a right no class-4
	// DENY can reach. It is deliberately not r.Name: the right is the
	// database-wide ALTER ANY USER, while the DENY that beats it sits on the
	// user itself as plain ALTER.
	//
	// It is also what keeps the principal arm of ObjectDenial from firing on
	// the wrong kind of node. ObjectDenial is asked about a table by the same
	// name as a denied user just as readily, and only a right declaring this
	// field makes it ask the class-4 question at all — the way r.Object and
	// r.Schema discriminate the arms above.
	DeniedOnPrincipal string

	// DeniedOnServer names the *server*-scope permission whose explicit DENY
	// withholds this right, or "" for a right no server-class DENY can reach,
	// and ServerSecurable says which kind of securable that DENY sits on.
	// DeniedOnPrincipal's server-scope twin, and read the same way: the right
	// is the server-wide ALTER ANY LOGIN while the DENY that beats it sits on
	// the login itself as plain ALTER.
	//
	// The kind is part of the declaration rather than inferred from the node,
	// because ObjectDenial is handed a bare name and only gosmo's catalog read
	// knows what that name is. It is also what keeps the arm from firing on
	// the wrong family — a login and an endpoint of the same name are two
	// securables, and the map keeps them apart.
	DeniedOnServer  string
	ServerSecurable gosmo.ServerSecurableKind

	// DeniedOnAG names the AVAILABILITY GROUP-scope (class 108) permission
	// whose absence on the group withholds this right, or "" for a right no
	// class-108 DENY can reach. DeniedOnServer's sibling, asked of a different
	// gosmo map for a reason that is SQL Server's: class 108's major_id is an
	// internal id no supported view maps back to a name, so gosmo asks
	// HAS_PERMS_BY_NAME per group instead of reading sys.server_permissions —
	// see gosmo.ProbedAvailabilityGroupPermissions.
	//
	// The consequence here is that the answer is only read as a *denial* while
	// the right beside it is held. HAS_PERMS_BY_NAME reads 0 both for a group
	// carrying DENY ALTER and for a login holding nothing at this scope at
	// all, and the second is already withheld by the server-wide right — where
	// naming the group would replace "needs ALTER ANY AVAILABILITY GROUP" with
	// a denial the user cannot act on.
	DeniedOnAG string

	// Alt are narrower permissions that also satisfy this one and are not
	// named in the message. SQL Server 2022 split VIEW SERVER STATE into two
	// halves, and a login holding either half can do the thing — but naming
	// all three in one sentence says less than naming the one the user is
	// likely to be granted.
	Alt []string
}

// The rights the application gates on. Every name is in gosmo's Probed* list
// *for its scope*; one that is not would read back as CapabilityUnknown
// forever and gate nothing, and nothing at run time tells that apart from a
// login that holds the right. That is why they are declared here rather than
// spelled at each call site, and why permission_gate_names_test.go reads these
// literals back and checks each against gosmo.
var (
	ControlServer   = Right{Name: "CONTROL SERVER", Role: "sysadmin"}
	ViewServerState = Right{Name: "VIEW SERVER STATE", Role: "sysadmin",
		Alt: []string{"VIEW SERVER PERFORMANCE STATE", "VIEW SERVER SECURITY STATE"}}
	AlterSettings = Right{Name: "ALTER SETTINGS", Role: "serveradmin"}
	// The login's class-101 DENY is all-or-nothing, which is why one arm on
	// the one right covers Login Properties, Rename and Delete alike: verified
	// live on majors 13 and 17, DENY ALTER ON LOGIN::x withholds ALTER LOGIN —
	// the rename and the password both — *and* DROP LOGIN, every refusal
	// Msg 15151. The server role below repeats none of that; see
	// AlterAnyServerRoleMembers.
	AlterAnyLogin = Right{Name: "ALTER ANY LOGIN", Role: "securityadmin",
		DeniedOnServer: "ALTER", ServerSecurable: gosmo.ServerSecurableLogin}
	// No DeniedOnServer twin, and the asymmetry is SQL Server's rather than an
	// omission — the database role's split at server scope: verified live on
	// majors 13 and 17, ALTER SERVER ROLE ... WITH NAME and DROP SERVER ROLE
	// check ALTER ANY SERVER ROLE at server scope and go through with
	// DENY ALTER ON SERVER ROLE::r in place, while the same DENY refuses the
	// membership edits. Declaring it here would withhold a rename and a drop
	// the server allows. See docs/decisions.md § Permission gating.
	AlterAnyServerRole = Right{Name: "ALTER ANY SERVER ROLE", Role: "securityadmin"}
	// AlterAnyServerRoleMembers is the same right for the page that edits
	// a server role's *membership*, where the class-101 DENY the comment above
	// says withholds nothing does withhold: ALTER SERVER ROLE r ADD MEMBER is
	// refused under DENY ALTER ON SERVER ROLE::r (Msg 15151), verified live on
	// majors 13 and 17, while the rename and the drop beside it go through.
	//
	// The split is per action, not per object, and the two rights exist to
	// carry it — AlterAnyDBRole/AlterAnyDBRoleMembers one scope up.
	//
	// Unlike the database scope, the *member* is not asked about. Adding a
	// login carrying a class-101 DENY to an undenied server role goes through
	// (majors 13 and 17, 2026-09-05) where adding a class-4-denied user to an
	// undenied database role is refused — so there is no login-side twin of
	// this right, and Login Properties > Server Roles declares the plain one.
	AlterAnyServerRoleMembers = Right{Name: "ALTER ANY SERVER ROLE", Role: "securityadmin",
		DeniedOnServer: "ALTER", ServerSecurable: gosmo.ServerSecurableServerRole}
	// ALTER ANY CREDENTIAL is server-scope only, and has no database-scoped
	// twin: a database-scoped credential is permitted by CONTROL on the
	// database alone — see DBScopedCredentialRights().
	AlterAnyCredential = Right{Name: "ALTER ANY CREDENTIAL", Role: "securityadmin"}
	CreateAnyDatabase  = Right{Name: "CREATE ANY DATABASE", Role: "dbcreator"}
	AlterAnyDatabase   = Right{Name: "ALTER ANY DATABASE", Role: "dbcreator"}
	// The endpoint's class-105 DENY withholds ALTER ENDPOINT, and its refusal
	// is Msg 6004 rather than the 15151 every class-101 one carries — verified
	// live on majors 13 and 17. One arm on the one right: the folder's New
	// Endpoint names no securable and so never reaches it.
	AlterAnyEndpoint = Right{Name: "ALTER ANY ENDPOINT", Role: "sysadmin",
		DeniedOnServer: "ALTER", ServerSecurable: gosmo.ServerSecurableEndpoint}
	AlterAnyAudit = Right{Name: "ALTER ANY SERVER AUDIT", Role: "sysadmin"}
	// The availability group's class-108 DENY is all-or-nothing, the login's
	// shape rather than the server role's split: probed live on the two-node
	// cluster 2026-09-05, DENY ALTER ON AVAILABILITY GROUP::g withholds every
	// ALTER AVAILABILITY GROUP there is — the options SET, ADD/REMOVE
	// DATABASE, MODIFY REPLICA and FAILOVER — each Msg 15151, with the
	// server-wide right reading 1 throughout.
	//
	// It leaves the ALTER DATABASE ... SET HADR family alone, which is checked
	// against the database instead: Resume and Join both went through with the
	// DENY in place. Suspend/Resume, Join and Unjoin therefore stay on plain
	// gate — see alwayson_menu.go.
	AlterAnyAG = Right{Name: "ALTER ANY AVAILABILITY GROUP", Role: "sysadmin",
		DeniedOnAG: "ALTER"}
	// Held for a feature that does not exist yet: nothing in the application
	// creates or alters a linked server, so nothing gates on this. It is
	// declared here rather than at the future call site because
	// permission_gate_names_test.go checks every literal in this block against
	// gosmo's Probed* list — a name added later and misspelled would read back
	// as CapabilityUnknown forever and gate nothing.
	//lint:ignore U1000 declared ahead of a New/Alter Linked Server feature so
	// permission_gate_names_test.go checks the literal against gosmo's Probed*
	// list now rather than after a misspelling has shipped.
	AlterAnyLinkedSrv = Right{Name: "ALTER ANY LINKED SERVER", Role: "sysadmin"}

	BackupDatabase = Right{Name: "BACKUP DATABASE", Role: "db_backupoperator", DB: true}
	AlterDatabase  = Right{Name: "ALTER", Role: "db_owner", DB: true}
	ControlDB      = Right{Name: "CONTROL", Role: "db_owner", DB: true}
	AlterAnyUser   = Right{Name: "ALTER ANY USER", Role: "db_accessadmin", DB: true,
		DeniedOnPrincipal: "ALTER"}
	// No DeniedOnPrincipal twin, and the asymmetry is SQL Server's rather than
	// an omission: verified live on majors 13, 14 and 17, DROP ROLE and
	// ALTER ROLE ... WITH NAME check ALTER ANY ROLE at database scope and go
	// through with DENY ALTER ON ROLE::x in place — while the same DENY on a
	// *user* refuses both of the matching statements. Declaring it here would
	// withhold two items the server allows — a gate refusing what the server
	// would have run, which is the one failure the class-4 work exists to
	// avoid; see docs/decisions.md § Permission gating. gosmo records the
	// role rows anyway and says so on DatabaseCapabilities.DeniedOnPrincipal.
	AlterAnyDBRole = Right{Name: "ALTER ANY ROLE", Role: "db_securityadmin", DB: true}
	// A database audit specification is gated at *database* scope, not by
	// AlterAnyAudit: SQL Server checks ALTER ANY DATABASE AUDIT for
	// CREATE/ALTER/DROP DATABASE AUDIT SPECIFICATION, and the server-scope
	// ALTER ANY SERVER AUDIT beside it answers for the audit the
	// specification writes to, not for the specification.
	AlterAnyDBAudit = Right{Name: "ALTER ANY DATABASE AUDIT", Role: "db_owner", DB: true}
	// The right SQL Server checks for ENABLE/DISABLE/DROP TRIGGER ... ON
	// DATABASE. It stands alone: HAS_PERMS_BY_NAME folds in what implies it,
	// so a member of db_ddladmin and a principal granted a database-wide
	// ALTER both answer 1 without either being asked separately, and a
	// principal with neither answers 0 — all three verified live 2026-09-08.
	AlterAnyDatabaseDDLTrigger = Right{Name: "ALTER ANY DATABASE DDL TRIGGER", Role: "db_ddladmin", DB: true}
	// AlterAnyDBRoleMembers is the same right for the pages that edit a
	// role's *membership*, where the class-4 DENY the comment above says
	// withholds nothing does withhold: ALTER ROLE r ADD MEMBER u is refused
	// under DENY ALTER ON ROLE::r (Msg 15151), verified live 2026-09-04 on
	// majors 13 and 17, while the rename and the drop beside it go through.
	//
	// So the split is per action, not per object, and the two rights exist to
	// carry it: naming this one on the General page would withhold a rename
	// the server allows, and naming the plain one on Members offers an edit it
	// refuses. See docs/decisions.md § Permission gating.
	AlterAnyDBRoleMembers = Right{Name: "ALTER ANY ROLE", Role: "db_securityadmin", DB: true,
		DeniedOnPrincipal: "ALTER"}
	// Held for a feature that does not exist yet — there is no New Table — for
	// the reason given at AlterAnyLinkedSrv.
	//lint:ignore U1000 declared ahead of a New Table feature, for the reason
	// given at AlterAnyLinkedSrv.
	CreateTable = Right{Name: "CREATE TABLE", Role: "db_ddladmin", DB: true}

	AlterAnySchema = Right{Name: "ALTER ANY SCHEMA", Role: "db_ddladmin", DB: true}
	ViewDBState    = Right{Name: "VIEW DATABASE STATE", Role: "db_owner", DB: true}

	// The three below name db_owner rather than a narrower role because there
	// isn't one: verified live 2026-08-27, db_ddladmin answers 0 to all three,
	// and a database-wide ALTER answers 1 for the two key permissions but 0
	// for ALTER ANY SECURITY POLICY — so no wider right can stand in for it.
	AlterAnyCMK       = Right{Name: "ALTER ANY COLUMN MASTER KEY", Role: "db_owner", DB: true}
	AlterAnyCEK       = Right{Name: "ALTER ANY COLUMN ENCRYPTION KEY", Role: "db_owner", DB: true}
	AlterAnySecPolicy = Right{Name: "ALTER ANY SECURITY POLICY", Role: "db_owner", DB: true}

	// What DROP PARTITION FUNCTION/SCHEME, DROP ASSEMBLY and DROP EXTERNAL
	// DATA SOURCE/FILE FORMAT/LIBRARY check — see dbScopedOpRights for the
	// live evidence. The role is the one that confers the right on *every*
	// supported version, which is why the external data source and file
	// format name db_owner: db_ddladmin carries both on major 17 but reads 0,
	// and is refused the drop, on 13 and 14.
	AlterAnyDataspace     = Right{Name: "ALTER ANY DATASPACE", Role: "db_ddladmin", DB: true}
	AlterAnyAssembly      = Right{Name: "ALTER ANY ASSEMBLY", Role: "db_ddladmin", DB: true}
	AlterAnyExtDataSource = Right{Name: "ALTER ANY EXTERNAL DATA SOURCE", Role: "db_owner", DB: true}
	AlterAnyExtFileFormat = Right{Name: "ALTER ANY EXTERNAL FILE FORMAT", Role: "db_owner", DB: true}
	// 2017 and later. On 2016 it reads CapabilityUnknown and fails open, which
	// gates nothing that exists: that version has no external libraries.
	AlterAnyExtLibrary = Right{Name: "ALTER ANY EXTERNAL LIBRARY", Role: "db_ddladmin", DB: true}

	// The five Service Broker rights. Each is enough on its own to ALTER and
	// to DROP its family — probed live 2026-09-16 with a WITHOUT LOGIN user
	// per right on majors 13, 14 and 17, which answered identically, line for
	// line — and a right from one family confers nothing on another
	// (ALTER ANY MESSAGE TYPE cannot alter a route, Msg 15151).
	//
	// The role is db_owner rather than a narrower one because no narrower one
	// was probed to carry them on every supported major, and a right's role is
	// the name the *user* is sent to ask for: naming db_ddladmin on a major
	// where it does not confer the permission sends them after a grant that
	// would not help. See docs/db-rules.md on per-version role contents.
	//
	// There is deliberately no broker-priority twin: SQL Server enforces a
	// CREATE/ALTER BROKER PRIORITY permission it does not publish —
	// HAS_PERMS_BY_NAME answers NULL for every spelling of one, and
	// sys.fn_builtin_permissions has no DATABASE-class row matching
	// %PRIORITY% — so a right declared for it would read CapabilityUnknown
	// forever and gate nothing. A broker priority takes ALTER on the database
	// alone; see BrokerPriorityWriteRights.
	AlterAnyMessageType = Right{Name: "ALTER ANY MESSAGE TYPE", Role: "db_owner", DB: true}
	AlterAnyContract    = Right{Name: "ALTER ANY CONTRACT", Role: "db_owner", DB: true}
	AlterAnyService     = Right{Name: "ALTER ANY SERVICE", Role: "db_owner", DB: true}
	AlterAnyRoute       = Right{Name: "ALTER ANY ROUTE", Role: "db_owner", DB: true}
	AlterAnyRSB         = Right{Name: "ALTER ANY REMOTE SERVICE BINDING", Role: "db_owner", DB: true}

	// The two SQL Agent rights are memberships, not permissions: what permits
	// New Job and its three siblings is membership of an msdb role, which
	// grants EXECUTE on individual procedures rather than the database-scope
	// EXECUTE a permission probe can ask about. See AgentWriteRights.
	// A backup device is added and dropped by sp_addumpdevice/sp_dropdevice,
	// which diskadmin carries and which no server *permission* answers for —
	// so this is a role membership, not a permission. CONTROL SERVER would be
	// a knowingly wrong gate here: a pure diskadmin, the one principal the
	// feature is for, would be shown a read-only banner on a page they can in
	// fact write.
	DiskAdmin = Right{Name: "diskadmin", ServerRole: true}

	SQLAgentUser = Right{Name: "SQLAgentUserRole", Membership: true, InDB: "msdb"}
	MsdbOwner    = Right{Name: "db_owner", Membership: true, InDB: "msdb"}

	// AlterOnObject is the grant made directly on one object, which no
	// wider scope reflects: a principal granted ALTER on one table reads 0 for
	// every database- and schema-scope permission there is.
	AlterOnObject = Right{Name: "ALTER", DB: true, Object: true}

	// ControlOnObject is CONTROL on one object, and it is not
	// AlterOnObject with a wider Name: gosmo's object block matches
	// CONTROL alongside whatever permission it asks about, so the ALTER map
	// answers 1 for a principal holding either and cannot tell them apart.
	// The CONTROL map is the one that can, and the one statement that needs
	// the distinction is ALTER SCHEMA ... TRANSFER — see ClassOneTransferRights.
	ControlOnObject = Right{Name: "CONTROL", DB: true, Object: true}

	// CONTROL on one assembly, type or XML schema collection: what its owner
	// holds implicitly, and what CONTROL on — or ownership of — its schema, or
	// CONTROL on the database, confers. Probed live 2026-09-11 on majors 13, 14
	// and 17 with a WITHOUT LOGIN user per case: it permits the drop and the
	// rename beside the wider rights that also do, and it is the *only* thing
	// that permits ALTER SCHEMA ... TRANSFER — see securableTransferRights.
	ControlOnAssembly            = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableAssembly}
	ControlOnType                = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableType}
	ControlOnXMLSchemaCollection = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableXMLSchemaCollection}

	// AlterOnSchema is what SQL Server actually checks for a rename, a
	// move or a drop of a schema object. No role carries it: it is granted on
	// the schema itself, and a principal holding it may hold nothing else.
	AlterOnSchema = Right{Name: "ALTER", DB: true, Schema: true}
)
