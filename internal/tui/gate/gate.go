package gate

import (
	"github.com/radix29/gosmo"
)

// gate.go is the [Right] type and the catalogue of rights the application
// asks about. The rule is in allows.go, the wording in text.go, the menu
// wrappers in menu.go and the named sets in sets.go.

// Right is one permission an action needs. Role names the fixed server or
// database role that also confers it, and exists only for the sentence the user
// is shown — the gate always asks about the permission, because
// HAS_PERMS_BY_NAME already answers 1 for a member of the role (and for a
// sysadmin), while IS_SRVROLEMEMBER does not fold sysadmin in.
type Right struct {
	Name string
	Role string
	DB   bool // database-scope rather than server-scope

	// Schema narrows the question to the schema the object lives in, asked of
	// gosmo's per-schema probe rather than the database-wide one. A principal
	// granted ALTER on one schema holds no database-wide permission at all, so
	// this is the only right that can speak for it.
	//
	// It is also how ObjectDenial asks the schema DENY question — there, and
	// only there, a schema-scoped right can *withhold*.
	Schema bool

	// Membership makes Name a fixed database *role* to be a member of, in the
	// database InDB, rather than a permission to hold. It exists for SQL Agent,
	// whose actions are permitted by membership of an msdb role and by nothing
	// HAS_PERMS_BY_NAME can be asked about.
	//
	// It cannot fail open on its own: InRole answers false for a role never
	// asked about exactly as it does for one the login is not in, so AllowsOn
	// checks gosmo.DatabaseCapabilities.Probed before believing a false.
	Membership bool
	InDB       string

	// ServerRole is Membership's server-scope twin: Name is a fixed server role
	// to be a member of. It exists for the writes SQL Server permits by role
	// membership alone — a backup device is added and dropped by
	// sp_addumpdevice/sp_dropdevice, which diskadmin carries and no server
	// permission answers for.
	//
	// Like Membership it cannot fail open on its own, so RightsAllow checks
	// gosmo.Capabilities.Probed before believing a false. It also asks about
	// sysadmin: membership of sysadmin implies membership of no other fixed
	// role, so a sysadmin reads 0 for diskadmin while being permitted
	// everything it carries.
	ServerRole bool

	// Object narrows the question to the object itself, asked of gosmo's
	// per-object probe. It is the only right that can speak for a principal
	// granted ALTER on one table and nothing else — such a principal reads 0
	// at schema and database scope alike.
	//
	// It can only ever *add* permission. gosmo's ObjectPermissions map holds a
	// row for an object explicitly granted, denied or owned and for no other,
	// so its silence means "no explicit grant", never "not probed" — see
	// gosmo.DatabaseCapabilities.HasOnObject.
	Object bool

	// Securable narrows the question to one assembly, user-defined type or XML
	// schema collection — classes 5, 6 and 10 — asked of gosmo's per-securable
	// probe. Object cannot speak for these, being class 1 only, and no wider
	// right can either: a principal granted CONTROL on one assembly, or owning
	// it, reads 0 at every database and schema scope there is.
	//
	// Unlike Object it is read Permits-wise, and so can withhold: gosmo's map
	// is a HAS_PERMS_BY_NAME answer with a row for every securable the login
	// can see, so a 0 is the server's answer rather than a silence. That is
	// what lets it be Move to Schema's only right.
	Securable gosmo.DatabaseSecurableKind

	// DeniedOnPrincipal names the DATABASE_PRINCIPAL-scope (class 4) permission
	// whose DENY withholds this right, or "" for a right no class-4 DENY can
	// reach. It is deliberately not Name: the right is the database-wide ALTER
	// ANY USER, while the DENY that beats it sits on the user itself as plain
	// ALTER.
	//
	// It also keeps the principal arm of ObjectDenial from firing on the wrong
	// kind of node — ObjectDenial is asked about a table by the same name as a
	// denied user just as readily, and only a right declaring this field makes
	// it ask the class-4 question at all.
	DeniedOnPrincipal string

	// DeniedOnServer is DeniedOnPrincipal's server-scope twin: the server-scope
	// permission whose explicit DENY withholds this right, or "" for a right no
	// server-class DENY can reach. ServerSecurable says which kind of securable
	// that DENY sits on.
	//
	// The kind is declared rather than inferred because ObjectDenial is handed
	// a bare name, and it keeps the arm from firing on the wrong family — a
	// login and an endpoint of the same name are two securables.
	DeniedOnServer  string
	ServerSecurable gosmo.ServerSecurableKind

	// DeniedOnAG names the AVAILABILITY GROUP-scope (class 108) permission
	// whose absence on the group withholds this right, or "" for a right no
	// class-108 DENY can reach. Class 108's major_id is an internal id no
	// supported view maps back to a name, so gosmo asks HAS_PERMS_BY_NAME per
	// group instead of reading sys.server_permissions.
	//
	// So the answer is only read as a *denial* while the right beside it is
	// held: HAS_PERMS_BY_NAME reads 0 both for a group carrying DENY ALTER and
	// for a login holding nothing at this scope, and the second is already
	// withheld by the server-wide right.
	DeniedOnAG string

	// Alt are narrower permissions that also satisfy this one and are not named
	// in the message. SQL Server 2022 split VIEW SERVER STATE into two halves
	// and either half suffices, but naming all three in one sentence says less
	// than naming the one the user is likely to be granted.
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
	// The login's class-101 DENY is all-or-nothing, so one arm on one right
	// covers Login Properties, Rename and Delete alike: verified live on majors
	// 13 and 17, DENY ALTER ON LOGIN::x withholds ALTER LOGIN — rename and
	// password both — and DROP LOGIN, every refusal Msg 15151.
	AlterAnyLogin = Right{Name: "ALTER ANY LOGIN", Role: "securityadmin",
		DeniedOnServer: "ALTER", ServerSecurable: gosmo.ServerSecurableLogin}
	// No DeniedOnServer twin: verified live on majors 13 and 17, ALTER SERVER
	// ROLE ... WITH NAME and DROP SERVER ROLE go through with DENY ALTER ON
	// SERVER ROLE::r in place, while the same DENY refuses the membership
	// edits. Declaring it would withhold a rename and a drop the server allows.
	// See docs/decisions.md § Permission gating.
	AlterAnyServerRole = Right{Name: "ALTER ANY SERVER ROLE", Role: "securityadmin"}
	// AlterAnyServerRoleMembers is the same right for the page that edits a
	// server role's *membership*, where that class-101 DENY does withhold:
	// ALTER SERVER ROLE r ADD MEMBER is refused under DENY ALTER ON SERVER
	// ROLE::r (Msg 15151), majors 13 and 17. The split is per action, not per
	// object — AlterAnyDBRole/AlterAnyDBRoleMembers one scope up.
	//
	// Unlike the database scope, the *member* is not asked about: adding a
	// login carrying a class-101 DENY to an undenied server role goes through
	// (majors 13 and 17), so Login Properties > Server Roles declares the
	// plain right.
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
	// The availability group's class-108 DENY is all-or-nothing: probed live on
	// the two-node cluster, DENY ALTER ON AVAILABILITY GROUP::g withholds every
	// ALTER AVAILABILITY GROUP there is — options SET, ADD/REMOVE DATABASE,
	// MODIFY REPLICA, FAILOVER — each Msg 15151, with the server-wide right
	// reading 1 throughout.
	//
	// It leaves the ALTER DATABASE ... SET HADR family alone, checked against
	// the database instead, so Suspend/Resume, Join and Unjoin stay on plain
	// gate — see alwayson_menu.go.
	AlterAnyAG = Right{Name: "ALTER ANY AVAILABILITY GROUP", Role: "sysadmin",
		DeniedOnAG: "ALTER"}
	// Held for a feature that does not exist yet: nothing creates or alters a
	// linked server. Declared here so permission_gate_names_test.go checks the
	// literal against gosmo's Probed* list — a misspelled name would read back
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
	// No DeniedOnPrincipal twin: verified live on majors 13, 14 and 17, DROP
	// ROLE and ALTER ROLE ... WITH NAME go through with DENY ALTER ON ROLE::x
	// in place, while the same DENY on a *user* refuses both matching
	// statements. Declaring it would make the gate refuse what the server would
	// have run. See docs/decisions.md § Permission gating.
	AlterAnyDBRole = Right{Name: "ALTER ANY ROLE", Role: "db_securityadmin", DB: true}
	// A database audit specification is gated at *database* scope, not by
	// AlterAnyAudit: SQL Server checks ALTER ANY DATABASE AUDIT for
	// CREATE/ALTER/DROP DATABASE AUDIT SPECIFICATION, and the server-scope
	// ALTER ANY SERVER AUDIT beside it answers for the audit the
	// specification writes to, not for the specification.
	AlterAnyDBAudit = Right{Name: "ALTER ANY DATABASE AUDIT", Role: "db_owner", DB: true}
	// The right SQL Server checks for ENABLE/DISABLE/DROP TRIGGER ... ON
	// DATABASE. It stands alone: HAS_PERMS_BY_NAME folds in what implies it, so
	// db_ddladmin and a database-wide ALTER both answer 1 without being asked
	// separately, and a principal with neither answers 0 — all verified live.
	AlterAnyDatabaseDDLTrigger = Right{Name: "ALTER ANY DATABASE DDL TRIGGER", Role: "db_ddladmin", DB: true}
	// AlterAnyDBRoleMembers is the same right for the pages that edit a role's
	// *membership*, where that class-4 DENY does withhold: ALTER ROLE r ADD
	// MEMBER u is refused under DENY ALTER ON ROLE::r (Msg 15151), verified
	// live on majors 13 and 17.
	//
	// The split is per action, not per object: naming this one on the General
	// page would withhold a rename the server allows, and naming the plain one
	// on Members offers an edit it refuses.
	AlterAnyDBRoleMembers = Right{Name: "ALTER ANY ROLE", Role: "db_securityadmin", DB: true,
		DeniedOnPrincipal: "ALTER"}
	// Held for a feature that does not exist yet — there is no New Table — for
	// the reason given at AlterAnyLinkedSrv.
	//lint:ignore U1000 declared ahead of a New Table feature, for the reason
	// given at AlterAnyLinkedSrv.
	CreateTable = Right{Name: "CREATE TABLE", Role: "db_ddladmin", DB: true}

	AlterAnySchema = Right{Name: "ALTER ANY SCHEMA", Role: "db_ddladmin", DB: true}
	ViewDBState    = Right{Name: "VIEW DATABASE STATE", Role: "db_owner", DB: true}

	// The three below name db_owner because no narrower role carries them:
	// verified live, db_ddladmin answers 0 to all three, and a database-wide
	// ALTER answers 1 for the two key permissions but 0 for ALTER ANY SECURITY
	// POLICY.
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

	// The five Service Broker rights. Each is enough on its own to ALTER and to
	// DROP its family — probed live with a WITHOUT LOGIN user per right on
	// majors 13, 14 and 17, identical throughout — and a right from one family
	// confers nothing on another (ALTER ANY MESSAGE TYPE cannot alter a route,
	// Msg 15151).
	//
	// The role is db_owner because no narrower one carries them on every
	// supported major, and the role is the name the *user* is sent to ask for.
	// See docs/db-rules.md on per-version role contents.
	//
	// There is deliberately no broker-priority twin: SQL Server enforces a
	// CREATE/ALTER BROKER PRIORITY permission it does not publish —
	// HAS_PERMS_BY_NAME answers NULL for every spelling, and
	// sys.fn_builtin_permissions has no DATABASE-class row matching %PRIORITY%
	// — so a right for it would read CapabilityUnknown forever. A broker
	// priority takes ALTER on the database alone; see BrokerPriorityWriteRights.
	AlterAnyMessageType = Right{Name: "ALTER ANY MESSAGE TYPE", Role: "db_owner", DB: true}
	AlterAnyContract    = Right{Name: "ALTER ANY CONTRACT", Role: "db_owner", DB: true}
	AlterAnyService     = Right{Name: "ALTER ANY SERVICE", Role: "db_owner", DB: true}
	AlterAnyRoute       = Right{Name: "ALTER ANY ROUTE", Role: "db_owner", DB: true}
	AlterAnyRSB         = Right{Name: "ALTER ANY REMOTE SERVICE BINDING", Role: "db_owner", DB: true}

	// A backup device is added and dropped by sp_addumpdevice/sp_dropdevice,
	// which diskadmin carries and which no server *permission* answers for — so
	// this is a role membership. CONTROL SERVER would be a knowingly wrong gate:
	// a pure diskadmin, the principal the feature is for, would be shown a
	// read-only banner on a page they can in fact write.
	DiskAdmin = Right{Name: "diskadmin", ServerRole: true}

	// The two SQL Agent rights are memberships, not permissions: New Job and
	// its siblings are permitted by membership of an msdb role, which grants
	// EXECUTE on individual procedures rather than the database-scope EXECUTE a
	// permission probe can ask about. See AgentWriteRights.
	SQLAgentUser = Right{Name: "SQLAgentUserRole", Membership: true, InDB: "msdb"}
	MsdbOwner    = Right{Name: "db_owner", Membership: true, InDB: "msdb"}

	// AlterOnObject is the grant made directly on one object, which no wider
	// scope reflects: a principal granted ALTER on one table reads 0 for every
	// database- and schema-scope permission there is.
	AlterOnObject = Right{Name: "ALTER", DB: true, Object: true}

	// ControlOnObject is CONTROL on one object, and is not AlterOnObject with a
	// wider Name: gosmo's object block matches CONTROL alongside whatever
	// permission it asks about, so the ALTER map answers 1 for a principal
	// holding either and cannot tell them apart. The one statement that needs
	// the distinction is ALTER SCHEMA ... TRANSFER — see ClassOneTransferRights.
	ControlOnObject = Right{Name: "CONTROL", DB: true, Object: true}

	// CONTROL on one assembly, type or XML schema collection: what its owner
	// holds implicitly, and what CONTROL on — or ownership of — its schema, or
	// CONTROL on the database, confers. Probed live on majors 13, 14 and 17: it
	// permits the drop and the rename beside the wider rights that also do, and
	// it is the *only* thing that permits ALTER SCHEMA ... TRANSFER — see
	// securableTransferRights.
	ControlOnAssembly            = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableAssembly}
	ControlOnType                = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableType}
	ControlOnXMLSchemaCollection = Right{Name: "CONTROL", DB: true, Securable: gosmo.DatabaseSecurableXMLSchemaCollection}

	// AlterOnSchema is what SQL Server actually checks for a rename, a
	// move or a drop of a schema object. No role carries it: it is granted on
	// the schema itself, and a principal holding it may hold nothing else.
	AlterOnSchema = Right{Name: "ALTER", DB: true, Schema: true}
)
